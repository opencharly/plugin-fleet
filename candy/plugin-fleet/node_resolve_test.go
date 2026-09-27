package deploy

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestResolveVmEntity is the regression guard for the bed-deploy reach bug
// (moved here from charly/synthetic_vm_image_test.go, W4 pure-helpers
// relocation): a kind:check bed (and any deploy.yml target:vm entry) names
// its VM via the node's `vm:` cross-ref, NOT a "vm:"-prefixed deploy name.
// Before the fix the candy compiler only recognized the "vm:" prefix, so a
// bed fell through to the plain host-adhoc synthetic box (host distro → pac)
// and the deploy ran `pacman` on a debian/fedora guest. resolveVmEntity must
// surface node.From so the guest-tuned synthetic vm box (candy_select.go's
// syntheticVmBoxFromEnvelope, K4 unit B) is reached.
func TestResolveVmEntity(t *testing.T) {
	cases := []struct {
		name       string
		deployName string
		node       *spec.DeployNode
		want       string
	}{
		{"bed via node.vm (the bug)", "check-fedora-vm", &spec.DeployNode{From: "fedora-vm"}, "fedora-vm"},
		{"deploy.yml target:vm via node.vm", "my-guest", &spec.DeployNode{Target: "vm", From: "arch"}, "arch"},
		{"cli vm: prefix, no node", "vm:arch", nil, "arch"},
		{"node.vm wins over prefix", "vm:ignored", &spec.DeployNode{From: "real-vm"}, "real-vm"},
		{"non-vm deploy -> empty", "my-pod", &spec.DeployNode{}, ""},
		{"nil node, non-prefixed -> empty", "some-pod", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVmEntity(tc.deployName, tc.node); got != tc.want {
				t.Errorf("resolveVmEntity(%q, %+v) = %q, want %q", tc.deployName, tc.node, got, tc.want)
			}
		})
	}
}

// TestResolveNodeOverlays_PropagatesExplicitTagToNodeVersion was DELETED with
// the schema-versioning removal cutover: `spec.Deploy`/`DeployNode` no longer
// carry a `version:` field, so there is no node.Version to propagate or clobber.
// The resolved tag now comes solely from the CLI --tag (or a CalVer git tag);
// resolveNodeOverlays is covered by TestResolveVmEntity above and the
// add-candy/install-opts paths.
