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
// (local + every EXTERNAL substrate, CLASSIFIED so that an unclassified target never inherits the
// box resolver) don't compile a primary image plan — everything comes from
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

	// The router keys on the CLASSIFIED PROPERTY, never on the snapshot alone: a target absent
	// from c.externalSubstrates is not assumed to be image-bearing, because that snapshot degrades
	// to empty when its HostBuild leg fails (opencharly/plugin-fleet#10).
	switch classifyDeployTarget(target, c.externalSubstrates) {
	case imageCompiling:
		ref, refErr := resolveRefForTarget(classifyRP, target, refStr, dir)
		if refErr != nil {
			return nil, "", nil, refErr
		}
		plans, base, candySet, err = c.compileRefSelection(ref, hostCtxJSON, tag, vmEntity, dir)
		if err != nil {
			return nil, "", nil, err
		}
	case targetOnly:
		// A target that compiles NO primary image plan: its workload is entirely add_candy:, its
		// positional ref is not a box, and the deploy path identity is the base. `local` is always
		// here, and so is every EXTERNAL substrate (the now-externalized vm/android/kubernetes)
		// whose word classifyDeployTarget found in c.externalSubstrates — the snapshot is an INPUT
		// to that classification, never the decision, which is the point of this router
		// (opencharly/plugin-fleet#10).
		base = path
	default:
		// notImageCompiling: a target that compiles no primary image and whose ref is not a box
		// (vm when its substrate is absent from the closure, and any future target nobody has
		// classified). Consulting rp.Boxes for it could only ever produce a wrong answer, so fail
		// here, naming the real cause.
		return nil, "", nil, substrateUnavailableError(target)
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
// Every other target compiles no primary image — its workload is entirely add_candy: — and used to
// reach the ref resolver only because its substrate was absent from c.externalSubstrates; since
// classifyDeployTarget routes those targets away from the resolver entirely, this annotator now
// covers the resolution failures that CAN still happen (a pod/kubernetes ref that is not a box or
// candy), where the cause is the same missing substrate. For a non-image-bearing target the
// box-or-candy message is not just incomplete, it points the wrong way: it invites the operator to
// make their kind:vm entity into a box, which is not a thing.
var imageBearingTargets = map[string]bool{"pod": true, "kubernetes": true}

// deployTargetClass names the three classes the compiler's router distinguishes, keyed on the
// PROPERTY that decides it — "does this target's positional ref name something that compiles a
// primary image?" — rather than on membership in a loader-threaded snapshot.
//
// Why the property: c.externalSubstrates DEGRADES TO EMPTY when its HostBuild leg fails, so a
// target whose plugin was simply absent from the closure fell through to the box resolver and
// failed with a message about boxes, for a ref that can never be a box
// (opencharly/plugin-fleet#10). The class for a target nobody has classified is therefore
// notImageCompiling — the SAFE one — so a NEW substrate cannot silently inherit box-resolver
// behaviour by being missing from a list.
type deployTargetClass int

const (
	// targetOnly: no primary image plan is compiled and the positional ref is not a box. The
	// workload is entirely add_candy: and the deploy path identity is the base — this is `local`
	// and every external deploy substrate, including vm/android/kubernetes when their substrate
	// word is present in the closure.
	targetOnly deployTargetClass = iota
	// imageCompiling: the positional ref IS a box (or a standalone candy) and a primary image plan
	// is compiled from it — pod and kubernetes.
	imageCompiling
	// notImageCompiling: the target compiles NO primary image and its ref is not a box. Reaching the
	// box resolver would only ever produce a wrong answer for it (there is no input for which a
	// `kind: vm` entity resolves as a box), so it must never get there.
	notImageCompiling
)

// classifyDeployTarget is THE one classifier for that property: the router and the resolution
// error's annotator both read imageBearingTargets through it, so the two can never drift.
func classifyDeployTarget(target string, externalSubstrates map[string]bool) deployTargetClass {
	if imageBearingTargets[target] {
		return imageCompiling
	}
	if target == "local" || externalSubstrates[target] {
		return targetOnly
	}
	return notImageCompiling
}

// substrateUnavailableError is the ONE wording for "this project has no such deploy substrate".
// It exists because a target that is neither image-bearing nor a known substrate cannot compile
// anything: rp.Boxes holds no ref it could name, so the only honest answer is to say so instead of
// consulting the box resolver (opencharly/plugin-fleet#10).
func substrateUnavailableError(target string) error {
	return fmt.Errorf("the %q deploy substrate is not available in this project — its deploy plugin "+
		"(plugin-deploy-%s) is not in the candy closure, and a target that is neither image-bearing "+
		"nor a known substrate compiles no primary image plan, so its ref is not resolved as a box",
		target, target)
}

// resolveRefForTarget resolves a deploy's positional ref and, on failure, reports it with
// the target named and the likely cause attached. It is the ONE place the two are joined,
// extracted so that joining is testable — the compile path around it needs a fully resolved
// project and a live loader, which a unit test cannot stand up.
func resolveRefForTarget(rp *spec.ResolvedProject, target, refStr, dir string) (*DeployRef, error) {
	ref, err := resolveDeployRef(rp, refStr, dir)
	if err != nil {
		return nil, fmt.Errorf("resolving ref %q for target %q: %w", refStr, target, annotateMissingSubstrate(target, err))
	}
	return ref, nil
}

// annotateMissingSubstrate adds the likely CAUSE to a ref-resolution failure on a target
// that should never have resolved a box in the first place.
//
// Measured: a `vm:` deploy in a project whose closure lacks plugin-deploy-vm fails with
//
//	ResolveDeployRef: "omarchy-vm" not found as a box or candy in the resolved-project envelope
//
// after `charly vm build` and `charly vm create` on that SAME entity both succeeded — so
// charly plainly knows it is a VM, and the message sends the reader to fix the one thing
// that is not wrong. The resolution error is preserved verbatim; only the cause is added.
func annotateMissingSubstrate(target string, err error) error {
	if target == "" || target == "local" || imageBearingTargets[target] {
		return err
	}
	return fmt.Errorf("the %q deploy substrate is not available in this project — its deploy plugin "+
		"(plugin-deploy-%s) is not in the candy closure, so the ref was resolved as a box instead. "+
		"Compose it, or add_candy: something that pulls it in. Underlying: %w", target, target, err)
}
