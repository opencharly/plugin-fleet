package deploy

// del_resolve.go — the `charly deploy del` target resolution, relocated from the deleted
// charly/deploy_add_cmd.go's deployDelCmd + the deleted charly/host_build_deploy_del_resolve.go
// "deploy-del-resolve" HostBuild seam (K-wave 2 cone R2 bank C). The plugin already threads the
// merged deploy tree plugin-side (resolveTreeViaLoader); resolveDelNode consumes it here instead
// of round-tripping through a host seam. The host's deploy-node-del-dispatch seam (the terminal
// ResolveTarget + Del step) stays registry-coupled.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/kit"
	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// resolveDelNode resolves the DeployNode + canonical kind for a
// `charly deploy del` invocation. Precedence:
//   - literal "host" name → synthetic local node (legacy)
//   - "vm:<name>" prefix  → synthetic vm node (legacy ref-based del)
//   - charly.yml entry    → the merged node (canonical target)
//   - no entry, pod artifact present → synthetic pod node (ref-based pod del)
//   - no entry, nothing present      → "no such deployment" error
//
// The returned node always carries a non-empty Target so the host's ResolveTarget can
// dispatch. For a ref-based pod deploy with no charly.yml entry (e.g. the entry
// was removed while the deploy is still up) the node is synthesized — but ONLY
// when a real pod artifact exists (a quadlet unit, or a live container for a
// direct-mode deploy). A mistyped/unknown name has no artifact and is rejected
// loudly, instead of being silently synthesized into a pod del that tears down
// nothing and then fails with a misleading "unknown target pod".
func resolveDelNode(name string, tree map[string]spec.DeployNode) (*spec.DeployNode, string, error) {
	if name == "host" {
		return &spec.DeployNode{Target: "local"}, "local", nil
	}
	// Try the REAL tree resolution FIRST — "vm:"-prefix-aware via resolveDeployNodeByPath's own
	// spec.SplitVmAddress use (RCA #9). tree is threaded PLUGIN-SIDE (resolveTreeViaLoader); a
	// nil/empty tree falls through to the "vm:"-prefix / pod-artifact fallbacks below.
	if tree != nil {
		if node, ok := resolveDeployNodeByPath(tree, name); ok && node.Target != "" {
			n := *node
			return &n, n.Target, nil
		}
	}
	if _, isVm := spec.SplitVmAddress(name); isVm {
		// Fallback ONLY for a genuine tree-absence: a "vm:"-prefixed address with no matching
		// tree entry (the deploy was removed from charly.yml, or never had one). The synthetic
		// Target-only placeholder is all we can offer; the host dispatch's own name normalization
		// still targets the right domain identity regardless.
		return &spec.DeployNode{Target: "vm"}, "vm", nil
	}
	if podDeploymentArtifactExists(name) {
		return &spec.DeployNode{Target: "pod"}, "pod", nil
	}
	return nil, "", fmt.Errorf("no such deployment %q — run `charly deploy show` to see "+
		"deployments (a VM deploy is torn down as `charly deploy del vm:%s`)", name, name)
}

// resolveDeployNodeByPath walks the merged deploy tree by dotted path (root.child.child). Ported
// from the deleted charly/plugin_loader.go helper — a pure spec-native walk the plugin can run on
// the tree it already resolved.
func resolveDeployNodeByPath(tree map[string]spec.DeployNode, name string) (*spec.DeployNode, bool) {
	node, _, _, ok := walkDeployNodePath(tree, name)
	return node, ok
}

// walkDeployNodePath is the ONE dotted-path walk: the leaf node PLUS its ROOT-FIRST ancestor
// path/node lists (EXCLUDING the leaf) — the same ordering spec.ReconstructParentExec consumes
// and the same lists candy/plugin-fleet's own `deploy add` walk threads to the resolve-target-add
// seam. Two consumers, one resolution (R3): resolveDeployNodeByPath takes only the leaf, and
// delAncestorChain (the del seam) takes the whole chain. A "vm:"-prefixed address is stripped
// first (spec.SplitVmAddress), exactly as the former host helper did.
//
// ok=false when the root name is absent or ANY member segment fails to resolve, so a
// mistyped/unknown path never yields a partial chain.
func walkDeployNodePath(tree map[string]spec.DeployNode, name string) (leaf *spec.DeployNode, ancestorPaths []string, ancestorNodes []spec.Deploy, ok bool) {
	name, _ = spec.SplitVmAddress(name)
	parts := strings.Split(name, ".")
	root, found := tree[parts[0]]
	if !found {
		return nil, nil, nil, false
	}
	cur := &root
	for i, seg := range parts[1:] {
		member := cur.MemberByName(seg)
		if member == nil || member.Node == nil {
			return nil, nil, nil, false
		}
		ancestorPaths = append(ancestorPaths, strings.Join(parts[:i+1], "."))
		ancestorNodes = append(ancestorNodes, *cur)
		cur = member.Node
	}
	return cur, ancestorPaths, ancestorNodes, true
}

// delAncestorChain returns the ROOT-FIRST ancestor path/node lists for a `charly deploy del`
// target's tree position, EXCLUDING the target itself — the del twin of the ancestor lists the
// `deploy add` walk threads to the resolve-target-add seam. The del dispatch carries them so the
// host can re-derive the SAME parentExec chain (the VENUE) ResolveTarget would have built for that
// position and hand it to Del as venue_json; without them a teardown reached from a FRESH process
// falls back to specexec.RootExecutorForDeployNode(node) — the OPERATOR'S HOST for an in-substrate
// member carrying no `host:` field — and replays the member's reversible ops (a `package:` list's
// `pacman -R`) on the workstation while its `add` landed correctly in the guest
// (opencharly/charly#765, the third variant of the #627/#680 mechanism).
//
// Empty for a TOP-LEVEL target (a bare or namespace-qualified name with no member descent) and for
// any unreachable/synthetic one ("host", "vm:<name>" with no tree entry, a ref-based pod artifact):
// those have no ancestors and keep the host's previous RootExecutorForDeployNode behaviour.
func delAncestorChain(tree map[string]spec.DeployNode, name string) ([]string, []spec.Deploy) {
	if tree == nil {
		return nil, nil
	}
	_, paths, nodes, ok := walkDeployNodePath(tree, name)
	if !ok || len(paths) == 0 {
		return nil, nil
	}
	return paths, nodes
}

// delDispatchRequest is the deploy-node-del-dispatch request for a teardown: the clean deploy
// identity, the resolved node, the teardown gates, and the target's ROOT-FIRST ancestor chain.
//
// The chain is the whole point of the change it belongs to (opencharly/charly#765): the ADD half
// threads the same two lists to the resolve-target-add seam, and the host re-derives the parentExec
// (the VENUE) from them. DEL previously sent none, so a teardown reached from a FRESH process had no
// venue and fell back to `specexec.RootExecutorForDeployNode(node)` — the operator's HOST for an
// in-substrate member carrying no `host:` field — replaying the member's `package:` reverse ops on
// the workstation while its `add` had landed correctly in the guest. Empty for a top-level or
// synthetic target, which keeps that fallback's previous behaviour.
//
// Extracted from DeployDelCmd.Run and kept pure (no state read, no I/O) so the WIRE SHAPE the del
// path actually ships is asserted directly by a test, not merely its two inputs.
func delDispatchRequest(tree map[string]spec.DeployNode, node *spec.DeployNode, c *DeployDelCmd) spec.DeployNodeDelDispatchRequest {
	// "vm:" is a CLI ADDRESSING hint, never an identity: strip it here so both the deploy identity
	// the host's ResolveTarget sees and the dotted path the chain is resolved against are the clean
	// form, exactly as the former helper did.
	name, _ := spec.SplitVmAddress(c.Name)
	ancestorPaths, ancestorNodes := delAncestorChain(tree, name)
	return spec.DeployNodeDelDispatchRequest{
		Name:            name,
		Node:            node,
		AssumeYes:       c.AssumeYes,
		KeepRepoChanges: c.KeepRepoChanges,
		KeepServices:    c.KeepServices,
		KeepImage:       c.KeepImage,
		DryRun:          c.DryRun,
		AncestorPaths:   ancestorPaths,
		AncestorNodes:   ancestorNodes,
	}
}

// podDeploymentArtifactExists reports whether a pod deploy named `name` has a persisted artifact on
// this host: a quadlet unit (`.container`/`.pod`, written by `charly config`/`charly start`) OR a
// live container (a direct-mode `engine.run=direct` deploy has no quadlet). It is the discriminator
// that lets a ref-based `charly deploy del <name>` with no charly.yml entry still tear a real pod
// down, while a mistyped name (no artifact) is rejected.
func podDeploymentArtifactExists(name string) bool {
	cn := specexec.NestedContainerName(name)
	if dir, err := spec.QuadletDir(); err == nil {
		for _, suffix := range []string{".container", ".pod"} {
			if _, err := os.Stat(filepath.Join(dir, "charly-"+cn+suffix)); err == nil {
				return true
			}
		}
	}
	return kit.ContainerExists("", "charly-"+cn)
}
