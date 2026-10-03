package deploy

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// del_resolve_ancestors_test.go — the R10 gate for the del seam's ancestor chain
// (opencharly/charly#765). `charly deploy del <root>.<member>` must ship the member's ROOT-FIRST
// ancestor path/node lists so the HOST can re-derive the SAME parentExec chain the `deploy add`
// walk threaded to the resolve-target-add seam and hand it to Del as the VENUE. Without it the
// teardown (a FRESH process, forked by candy/plugin-check's cleanup step) resolved its venue from
// specexec.RootExecutorForDeployNode(node) — the OPERATOR'S HOST for an in-substrate member with no
// `host:` field — and replayed the member's reversible ops (`pacman -R` for a `package:` list)
// against the workstation while its `add` had landed correctly in the guest.

// TestDelAncestorChain_NestedMemberIsRootFirstExcludingTarget pins the exact shape the host's
// spec.ReconstructParentExec consumes: for `stack.web.db` the chain is [stack, stack.web] with the
// node AT each path — root-first, and the target itself EXCLUDED (including it would derive the
// target's own executor, whose `Del` then runs against a venue nested one hop too deep).
func TestDelAncestorChain_NestedMemberIsRootFirstExcludingTarget(t *testing.T) {
	tree := makeTree()
	paths, nodes := delAncestorChain(tree, "stack.web.db")

	if len(paths) != 2 || len(nodes) != 2 {
		t.Fatalf("delAncestorChain(stack.web.db) = %v / %d nodes, want 2 ancestors [stack stack.web]", paths, len(nodes))
	}
	if paths[0] != "stack" || paths[1] != "stack.web" {
		t.Fatalf("ancestor paths = %v, want ROOT-FIRST [stack stack.web]", paths)
	}
	// Each node must be the node AT its own path — ReconstructParentExec pairs them by index, so a
	// shifted list would derive the wrong hop and silently reconstruct a different venue.
	if nodes[0].Target != "container" {
		t.Fatalf("ancestor_nodes[0].Target = %q, want %q (the node AT `stack`)", nodes[0].Target, "container")
	}
	if nodes[1].Target != "container" || len(nodes[1].Member) != 1 {
		t.Fatalf("ancestor_nodes[1] = %+v, want the node AT `stack.web` (target=container, its one `db` member)", nodes[1])
	}
	if nodes[1].Target == "host" {
		t.Fatalf("ancestor_nodes[1] looks like the TARGET `stack.web.db` (target=host) — the chain must EXCLUDE the target")
	}
}

// TestDelAncestorChain_OneLevelAndPrefixForms covers the one-hop case and the legacy "vm:" CLI
// addressing prefix, which is an ADDRESSING hint, never an identity: `vm:stack.web` must resolve
// exactly as `stack.web` does.
func TestDelAncestorChain_OneLevelAndPrefixForms(t *testing.T) {
	tree := makeTree()

	paths, nodes := delAncestorChain(tree, "stack.web")
	if len(paths) != 1 || paths[0] != "stack" || len(nodes) != 1 {
		t.Fatalf("delAncestorChain(stack.web) = %v / %d nodes, want one ancestor [stack]", paths, len(nodes))
	}

	vmPaths, vmNodes := delAncestorChain(tree, "vm:stack.web")
	if len(vmPaths) != 1 || vmPaths[0] != "stack" || len(vmNodes) != 1 {
		t.Fatalf("delAncestorChain(vm:stack.web) = %v / %d nodes, want the SAME [stack] as the bare form",
			vmPaths, len(vmNodes))
	}
}

// TestDelAncestorChain_NoAncestorsKeepThePreviousBehaviour pins every case that must yield an
// EMPTY chain: the host then leaves specexec.RootExecutorForDeployNode in place, exactly as before
// this change. A synthetic or top-level target that gained a phantom ancestor would be a new bug.
func TestDelAncestorChain_NoAncestorsKeepThePreviousBehaviour(t *testing.T) {
	tree := makeTree()
	cases := []struct {
		name string
		tree map[string]spec.DeployNode
		addr string
	}{
		{"top-level bare name", tree, "arch"},
		{"literal host (synthetic local node)", tree, "host"},
		{"vm: address with no tree entry", tree, "vm:nonexistent"},
		{"unknown root", tree, "nope.web"},
		{"missing intermediate segment", tree, "stack.missing.db"},
		{"nil tree (tree-absent project)", nil, "stack.web.db"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			paths, nodes := delAncestorChain(c.tree, c.addr)
			if paths != nil || nodes != nil {
				t.Fatalf("delAncestorChain(%q) = %v / %v, want nil/nil — a target with no resolvable "+
					"ancestor chain must keep the host's previous RootExecutorForDeployNode behaviour",
					c.addr, paths, nodes)
			}
		})
	}
}

// TestDelDispatchRequest_ShipsTheChainAndTheCleanIdentity asserts the request the del path
// ACTUALLY ships — the wire shape, not just the chain helper's return value. The chain is the
// venue the host re-derives from (charly#765); a refactor that computed it and then dropped it
// from the literal (or shipped the "vm:"-prefixed name into ResolveTarget) must fail here.
func TestDelDispatchRequest_ShipsTheChainAndTheCleanIdentity(t *testing.T) {
	tree := makeTree()

	t.Run("nested member: the chain rides the request, and the vm: form is stripped", func(t *testing.T) {
		node := tree["stack"].Member[0].Node // the `web` member
		req := delDispatchRequest(tree, node, &DeployDelCmd{
			Name: "vm:stack.web", AssumeYes: true, KeepServices: true, KeepRepoChanges: true, KeepImage: true,
		})

		if req.Name != "stack.web" {
			t.Fatalf("Name = %q, want the STRIPPED identity %q — the host's ResolveTarget must never see "+
				"the vm: addressing prefix", req.Name, "stack.web")
		}
		if req.Node != node {
			t.Fatalf("Node = %v, want the resolved node it was given (the seam's `no venue for the wrong node` hazard)", req.Node)
		}
		if len(req.AncestorPaths) != 1 || req.AncestorPaths[0] != "stack" {
			t.Fatalf("AncestorPaths = %v, want [stack] — WITHOUT this the host reconstructs no parentExec, "+
				"the plugin falls back to RootExecutorForDeployNode(node) = the OPERATOR'S HOST, and the "+
				"teardown replays the member's reverse ops on the workstation (charly#765)", req.AncestorPaths)
		}
		if len(req.AncestorNodes) != 1 || req.AncestorNodes[0].Target != "container" {
			t.Fatalf("AncestorNodes = %+v, want the node AT `stack` — the two lists are paired BY INDEX by "+
				"spec.ReconstructParentExec, so a shifted or short list derives a different venue",
				req.AncestorNodes)
		}
		if !req.AssumeYes || !req.KeepServices || !req.KeepRepoChanges || !req.KeepImage || req.DryRun {
			t.Fatalf("teardown gates = {assumeYes:%v keepServices:%v keepRepoChanges:%v keepImage:%v dryRun:%v}, "+
				"want the four SET and dry-run clear — a dropped gate makes a nested member's teardown "+
				"prompt (and hang) or destroy state it was told to keep",
				req.AssumeYes, req.KeepServices, req.KeepRepoChanges, req.KeepImage, req.DryRun)
		}
	})

	t.Run("top-level target: no chain, request unchanged from before this change", func(t *testing.T) {
		req := delDispatchRequest(tree, &spec.DeployNode{Target: "vm", From: "arch"}, &DeployDelCmd{Name: "arch"})
		if req.AncestorPaths != nil || req.AncestorNodes != nil {
			t.Fatalf("AncestorPaths/AncestorNodes = %v/%v, want nil for a top-level target — the host must "+
				"keep its previous RootExecutorForDeployNode behaviour", req.AncestorPaths, req.AncestorNodes)
		}
		if req.Name != "arch" {
			t.Fatalf("Name = %q, want arch", req.Name)
		}
	})
}

// TestResolveDeployNodeByPath_StillResolvesTheLeaf proves the shared walk refactor (R3: one
// resolution, two consumers) did not change leaf resolution — resolveDelNode depends on it.
func TestResolveDeployNodeByPath_StillResolvesTheLeaf(t *testing.T) {
	tree := makeTree()
	node, ok := resolveDeployNodeByPath(tree, "stack.web.db")
	if !ok || node == nil {
		t.Fatalf("resolveDeployNodeByPath(stack.web.db) = %v, %v; want the leaf node", node, ok)
	}
	if node.Target != "host" {
		t.Fatalf("resolved leaf target = %q, want host", node.Target)
	}
	if _, ok := resolveDeployNodeByPath(tree, "stack.missing"); ok {
		t.Fatalf("resolveDeployNodeByPath(stack.missing) reported ok for a missing segment")
	}
	// The namespace-qualified full-key form: `arch` is a root key, and a dotted ADDRESS whose first
	// segment is a root resolves by descent — never by a verbatim key match that would skip the walk.
	if _, ok := resolveDeployNodeByPath(tree, "arch"); !ok {
		t.Fatalf("resolveDeployNodeByPath(arch) failed for a real top-level key")
	}
}
