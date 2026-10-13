package deploy

// dispatch.go — the K4-C SHAPE-2 per-node COMPILE orchestration, ported PLUGIN-SIDE from the former
// host-side charly/ compile seam (the since-deleted compileNodePlans + compileRefSelection /
// compileBoxSelection / compileStandaloneCandySelection / compileAddCandyOnBox).
// The plugin's own tree-walk (walk.go dispatchOne) now COMPILES the InstallPlans IN-PROC via the
// shared compilePlansForRequest (compile.go) — NO OpCompile round-trip — killing the former
// plugin→host→plugin double-bounce (plugin dispatchOne → HostBuild("deploy-node-dispatch") → host
// dispatchNode → compileNodePlans → compileViaPlugin(OpCompile) → back into the plugin). The host
// half now does ONLY the genuine floor-M residue a plugin cannot (reconstruct the ancestor executor
// chain, loadConfigForDeploy, ResolveTarget + Add), reached via the ONE thin
// HostBuild("resolve-target-add") seam.

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// compileNodePlans compiles the InstallPlans for one tree position plugin-side, dispatching on the
// classified target — the port of the former host deployAddCmd.compileNodePlans. Target-only deploys
// (local + every EXTERNAL substrate) don't compile a primary image plan — everything comes from
// add_candy. For pod/kubernetes targets the add_candy compiles against the BASE IMAGE's context rather than
// the operator host's. Returns the plans, the base identity, and the candy set (both for the
// deployID stamp). Ref classification resolves off the resolved-project envelope (rp.Boxes/
// rp.Candies) via resolveDeployRef (deploy_ref.go).
func (c *DeployAddCmd) compileNodePlans(target, refStr, tag, path string, addCandies []string, vmEntity, builderImageOverride string) ([]*spec.InstallPlan, string, []string, error) {
	dir := c.dir
	var plans []*spec.InstallPlan
	var base string
	var candySet []string

	// The host-computed HostContext (the MachineVenue probe + glibc + builder-image override —
	// vmshared.DetectHostDistro/DetectHostGlibc are sdk-portable, so the plugin computes these
	// itself). ActiveInit resolves plugin-side inside compilePlansForRequest off rp.Init (retiring
	// the former host preresolveActiveInitInto). Reused across every shape's compile.
	hostCtx := detectHostContext()
	if builderImageOverride != "" {
		hostCtx.BuilderImage = builderImageOverride
	}
	hostCtxJSON, err := json.Marshal(hostCtx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("compile: marshal host context: %w", err)
	}

	// The BASE envelope for ref classification only (no ExtraCandyRefs; IncludeDisabled=true to
	// mirror the former host ResolveDeployRef's namespace-aware ResolveBoxRef, which never filtered
	// enabled). Each shape's own compile re-fetches with its own ExtraCandyRefs (compile.go).
	classifyRP, err := fetchResolvedProject(dir, nil, true)
	if err != nil {
		return nil, "", nil, err
	}

	ref, targetOnly, routeErr := routeTargetRef(classifyRP, target, refStr, dir, c.externalSubstrates, c.externalSubstratesErr)
	if routeErr != nil {
		return nil, "", nil, routeErr
	}
	if targetOnly {
		// Target-only deploys (local + every EXTERNAL deploy substrate, incl. the now-externalized
		// vm/android/kubernetes) compile no primary image plan — the workload is entirely add_candy:.
		// base is the deploy path identity. WHICH targets those are is decided by the TARGET CLASS in
		// routeTargetRef, never by membership in the loader-threaded snapshot: that set degrades to nil
		// on three separate failures, and a nil lookup reads as "not target-only"
		// (opencharly/plugin-fleet#10).
		base = path
	} else {
		plans, base, candySet, err = c.compileRefSelection(ref, hostCtxJSON, tag, vmEntity, dir)
		if err != nil {
			return nil, "", nil, err
		}
	}

	// pod/kubernetes add_candy overlays compile against the PRIMARY base image; primaryBoxName is set
	// exactly when the primary ref is a LOCAL box (a candy/remote primary ref leaves it "" and the
	// overlay falls back to the standalone-candy compile — matching the OLD baseImg==nil path).
	primaryBoxName := ""
	if (target == "pod" || target == "kubernetes") && refStr != "" {
		if pref, perr := resolveDeployRef(classifyRP, refStr, dir); perr == nil && pref.Kind == RefKindBox && pref.Source != RefSourceRemote {
			primaryBoxName = pref.Name
		}
	}
	for _, al := range addCandies {
		alRef, alErr := resolveDeployRefAsCandy(classifyRP, al, dir)
		if alErr != nil {
			return nil, "", nil, fmt.Errorf("resolving --add-candy %q: %w", al, alErr)
		}
		var alPlans []*spec.InstallPlan
		if primaryBoxName != "" {
			alPlans, err = c.compileAddCandyOnBox(alRef, primaryBoxName, hostCtxJSON, tag, dir)
		} else {
			alPlans, _, _, err = c.compileRefSelection(alRef, hostCtxJSON, tag, vmEntity, dir)
		}
		if err != nil {
			return nil, "", nil, fmt.Errorf("compiling --add-candy %q: %w", al, err)
		}
		// Mark each plan's own candy (plus transitive deps) as overlay candies so the Pod target
		// picks them ALL up — not just the user-facing ref name.
		overlayNames := make([]string, 0, len(alPlans)+1)
		for _, p := range alPlans {
			if p.Candy != "" {
				overlayNames = append(overlayNames, p.Candy)
			}
		}
		// ALSO carry the ORIGINAL authored add_candy ref `al` (possibly REMOTE/qualified) alongside
		// the bare resolved candy name(s): a bare candy name is a silent no-op for a REMOTE ref in
		// the downstream resolved-project re-fetches (ScanAllCandyWithConfigOpts gates on
		// IsRemoteCandyRef), so without the qualified ref itself those re-fetches never fetch a remote
		// add_candy at all (RCA'd K1-alpha regression: check-addcandy-pod's overlay-deploy path).
		overlayNames = append(overlayNames, al)
		for _, p := range alPlans {
			p.AddCandies = append(p.AddCandies, overlayNames...)
		}
		plans = append(plans, alPlans...)
	}
	return plans, base, candySet, nil
}

// compileRefSelection dispatches a primary ref (box vs candy) to the shared in-proc compiler,
// mirroring the former host-side compileRefSelection → compileBoxSelection /
// compileStandaloneCandySelection. Remote image refs are unsupported (unchanged). base is ref.Name
// for both shapes (matching the OLD semantics — the compile returns the box view name, but candy-ref
// units keep ref.Name). candySet is read back off each compiled plan's own Candy name.
func (c *DeployAddCmd) compileRefSelection(ref *DeployRef, hostCtxJSON []byte, tag, vmEntity, dir string) ([]*spec.InstallPlan, string, []string, error) {
	if ref.Source == RefSourceRemote && ref.Kind == RefKindBox {
		return nil, "", nil, fmt.Errorf("remote image refs are not supported by deploy add (ref=%s)", ref.Raw)
	}
	var req spec.DeployCompileRequest
	if ref.Kind == RefKindBox {
		// BOX-REF shape (primary pod/kubernetes image): the plugin resolves the box view + candy order off
		// the envelope (box_select.go).
		req = spec.DeployCompileRequest{Dir: dir, BoxRef: ref.Name, HostContextJSON: hostCtxJSON, Tag: tag}
	} else {
		// CANDY shape (target:local/vm/external, no base image): vm_entity is threaded TOLERANTLY —
		// the plugin tries it against rp.Templates.VM and falls back to a plain host box on a miss
		// (candy_select.go), matching the OLD tolerant lookup exactly.
		// The ref's SCOPE travels WITH it: this candy composes INTO the box the deploy creates,
		// whose name is ref.Name (the compile returns the box view name, but candy-ref units keep
		// ref.Name). A constant scope here is the #739 collision.
		req = spec.DeployCompileRequest{Dir: dir, CandyRef: ref.Raw, VmEntity: vmEntity, HostContextJSON: hostCtxJSON, Tag: tag, ExtraCandyRefs: []spec.ExtraCandyRef{{Ref: ref.Raw, Scope: spec.BoxScope(ref.Name)}}}
	}
	plans, err := compilePlansForRequest(cmdCtx, cmdExec, req)
	if err != nil {
		return nil, "", nil, err
	}
	return plans, ref.Name, candyOrderFromPlans(plans), nil
}

// compileAddCandyOnBox is the ADD-CANDY-ON-BOX shape: the add_candy overlay compiled against the
// primary pod/kubernetes base image, ALL resolved off the envelope (box_select.go
// resolveAddCandyOnBoxSelection). ExtraCandyRefs carries alRef.Raw so the plugin's OWN
// resolved-project re-fetch discovers a REMOTE overlay ref.
func (c *DeployAddCmd) compileAddCandyOnBox(alRef *DeployRef, baseBoxRef string, hostCtxJSON []byte, tag, dir string) ([]*spec.InstallPlan, error) {
	return compilePlansForRequest(cmdCtx, cmdExec, spec.DeployCompileRequest{
		Dir:             dir,
		CandyRef:        alRef.Raw,
		BaseBoxRef:      baseBoxRef,
		HostContextJSON: hostCtxJSON,
		Tag:             tag,
		ExtraCandyRefs:  []spec.ExtraCandyRef{{Ref: alRef.Raw, Scope: spec.BoxScope(baseBoxRef)}},
	})
}

// candyOrderFromPlans reconstructs the compiled candy set (for deployID + overlay provenance) from
// each plan's own Candy name — plans preserve the plugin's topo-sorted order, so this needs no
// separate CandySet in the reply.
func candyOrderFromPlans(plans []*spec.InstallPlan) []string {
	order := make([]string, 0, len(plans))
	for _, p := range plans {
		if p.Candy != "" {
			order = append(order, p.Candy)
		}
	}
	return order
}

// detectHostContext builds the compile-time HostContext for a deploy — the plugin-side twin of the
// former host charly/deploy_add_cmd.go detectHostContext (vmshared.DetectHostDistro/DetectHostGlibc
// are sdk-portable). MachineVenue is set on a real host (hd != nil); the ActiveInit resolution runs
// plugin-side in compilePlansForRequest off rp.Init.
func detectHostContext() deploykit.HostContext {
	hd, _ := vmshared.DetectHostDistro()
	glibc, _ := vmshared.DetectHostGlibc()
	if hd == nil {
		return deploykit.HostContext{}
	}
	return deploykit.HostContext{
		MachineVenue: true,
		Distro:       hd.PrimaryTag(),
		GlibcVersion: glibc,
	}
}

// printPlans renders the compiled plans for a --dry-run — plugin-side (command:deploy is compiled-in,
// so os.Stdout is charly's real stdout). The port of the former host deployAddCmd.printPlans.
func printPlans(plans []*spec.InstallPlan, formatJSON bool) error {
	if formatJSON {
		return json.NewEncoder(os.Stdout).Encode(plans)
	}
	for _, p := range plans {
		fmt.Println(deploykit.DescribePlan(p))
	}
	return nil
}

// imageBearingTargets are the deploy targets whose primary positional ref IS a box: they
// compile an image plan, so "not found as a box or candy" is the whole truth for them.
//
// EVERY OTHER target compiles no primary image plan — its workload is entirely add_candy: — and
// must never reach the box/candy resolver at all. This map is the CLASS; it is what
// targetOnlyRoute branches on, and it cannot degrade the way a data snapshot can.
var imageBearingTargets = map[string]bool{"pod": true, "kubernetes": true}

// routeTargetRef is the ONE place the class decision and the box/candy resolution are joined: the
// class is consulted FIRST, and only an image-bearing target can ever reach the resolver. Joining
// them here — rather than calling the two helpers from separate places in compileNodePlans — is what
// makes the ORDER testable, and the order IS the row: the refusal must happen before any box or
// candy is consulted, so that a same-named candy can never answer for a `vm:` deploy.
func routeTargetRef(rp *spec.ResolvedProject, target, refStr, dir string, external map[string]bool, externalErr error) (ref *DeployRef, targetOnly bool, err error) {
	targetOnly, err = targetOnlyRoute(target, external, externalErr)
	if err != nil || targetOnly {
		return nil, targetOnly, err
	}
	ref, err = resolveRefForTarget(rp, target, refStr, dir)
	return ref, false, err
}

// targetOnlyRoute decides whether this deploy compiles a primary image plan, and for the targets
// that need a deploy SUBSTRATE it also decides whether that substrate is available — returning the
// error to fail with when it is not.
//
// WHY THE CLASS AND NOT THE SNAPSHOT (opencharly/plugin-fleet#10). The branch this replaces read
// `c.externalSubstrates[target]`, a snapshot that degrades to nil on THREE separate failures (no
// exec seam, HostBuild error, decode error) — and indexing a nil map yields false. A `vm:` deploy
// in a project whose closure lacks plugin-deploy-vm was therefore routed into the box/candy
// resolver, where a same-named candy could answer for a VM, and where the only other outcome was a
// message inviting the operator to turn a kind:vm entity into a box. Measured on main: after
// `charly vm build` and `charly vm create` on the SAME entity both succeeded, `charly deploy add`
// reported `ResolveDeployRef: "omarchy-vm" not found as a box or candy in the resolved-project
// envelope`. The class is structural and cannot degrade.
//
// Returns (targetOnly, err): (true, nil) — no primary image plan, proceed with base = path;
// (false, nil) — the positional ref IS a box, resolve it; (false, err) — refuse, before any box or
// candy is consulted.
func targetOnlyRoute(target string, external map[string]bool, externalErr error) (bool, error) {
	if target == "local" {
		return true, nil // local needs no substrate word; it is the host's own shell
	}
	if imageBearingTargets[target] {
		return false, nil
	}
	if externalErr != nil {
		return false, fmt.Errorf("cannot tell whether %q is a deploy substrate of this project (%w), "+
			"and a %q deploy compiles no primary image plan — refusing to resolve its ref as a box or "+
			"candy, where a same-named candy could answer for it", target, externalErr, target)
	}
	if !external[target] {
		return false, missingSubstrateErr(target)
	}
	return true, nil
}

// missingSubstrateErr is the ONE place the substrate-absent message lives.
func missingSubstrateErr(target string) error {
	return fmt.Errorf("the %q deploy substrate is not available in this project — its deploy plugin "+
		"(plugin-deploy-%s) is not in the candy closure. Compose it, or add_candy: something that "+
		"pulls it in. A %q deploy compiles no primary image plan, so its ref was never a box or candy",
		target, target, target)
}

// resolveRefForTarget resolves a deploy's positional ref and, on failure, reports it with the
// target named. It is reached ONLY for image-bearing targets (see targetOnlyRoute), which is why
// the failure carries no substrate theory: for pod/kubernetes, "not found as a box or candy" is the
// whole truth. Extracted so the joining is testable — the compile path around it needs a fully
// resolved project and a live loader, which a unit test cannot stand up.
func resolveRefForTarget(rp *spec.ResolvedProject, target, refStr, dir string) (*DeployRef, error) {
	ref, err := resolveDeployRef(rp, refStr, dir)
	if err != nil {
		return nil, fmt.Errorf("resolving ref %q for target %q: %w", refStr, target, err)
	}
	return ref, nil
}
