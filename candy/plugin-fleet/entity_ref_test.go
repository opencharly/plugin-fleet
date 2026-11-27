package deploy

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// rpOf builds a resolved-project envelope carrying exactly the box fullkeys given.
func rpOf(keys ...string) *spec.ResolvedProject {
	rp := &spec.ResolvedProject{Boxes: map[string]spec.ResolvedBoxView{}}
	for _, k := range keys {
		rp.Boxes[k] = spec.ResolvedBoxView{}
	}
	return rp
}

// TestQualifyEntityImageRef pins opencharly/opencharly#1316: a deploy imported under a namespace
// authors a SHORT `image:`, but the envelope keys that box by fullkey (`ns.name`), so both
// resolution sites failed on the bare name:
//
//	ResolveDeployRef: "kde-vnc-cdp" not found as a box or candy in the resolved-project envelope
//	plugin-deploy-pod prepare-venue: overlay build: resolving "kde-vnc-cdp" via charly.yml: not found
//
// Each row is a rule, not a spelling: the qualification is proved by EXISTENCE so a dotted path that
// is a NESTED CHILD (not a namespace) cannot be misread, and a ref that resolves as written is never
// rewritten.
func TestQualifyEntityImageRef(t *testing.T) {
	tests := []struct {
		name     string
		rp       *spec.ResolvedProject
		path     string
		ref      string
		expected string
	}{
		{
			name:     "the measured defect: an imported entity's short image resolves in its own namespace",
			rp:       rpOf("kde-vnc-cdp.kde-vnc-cdp"),
			path:     "kde-vnc-cdp.check-kde-vnc-cdp-pod",
			ref:      "kde-vnc-cdp",
			expected: "kde-vnc-cdp.kde-vnc-cdp",
		},
		{
			name:     "the deploy-key spelling (slash) qualifies the same way",
			rp:       rpOf("kde-vnc-cdp.kde-vnc-cdp"),
			path:     "kde-vnc-cdp/check-kde-vnc-cdp-pod",
			ref:      "kde-vnc-cdp",
			expected: "kde-vnc-cdp.kde-vnc-cdp",
		},
		{
			name:     "a ROOT deploy's box is untouched (no namespace in the path)",
			rp:       rpOf("app"),
			path:     "app",
			ref:      "app",
			expected: "app",
		},
		{
			name:     "D-REF-2: a ROOT box with the same leaf does not answer for an imported entity",
			rp:       rpOf("app", "ns.app"),
			path:     "ns.dev",
			ref:      "app",
			expected: "ns.app",
		},
		{
			name:     "a NESTED child path is not a namespace: no head.ref box, no rewrite",
			rp:       rpOf("elsewhere.native-box"),
			path:     "parent.child",
			ref:      "native-box",
			expected: "native-box",
		},
		{
			name:     "D-REF-2: a missing box in the entity's OWN scope is still qualified (no fallback)",
			rp:       rpOf("ns.widget"),
			path:     "ns.dev",
			ref:      "absent",
			expected: "ns.absent",
		},
		{
			name:     "a full OCI ref passes through",
			rp:       rpOf("ns.widget"),
			path:     "ns.dev",
			ref:      "quay.io/fedora/fedora:43",
			expected: "quay.io/fedora/fedora:43",
		},
		{
			name:     "a remote ref passes through",
			rp:       rpOf("ns.widget"),
			path:     "ns.dev",
			ref:      "@github.com/opencharly/pod-kde-vnc:v2026.331.1657",
			expected: "@github.com/opencharly/pod-kde-vnc:v2026.331.1657",
		},
		{
			name:     "a local path passes through",
			rp:       rpOf("ns.widget"),
			path:     "ns.dev",
			ref:      "./custom.yml",
			expected: "./custom.yml",
		},
		{
			name:     "an already qualified ref passes through (no double qualification)",
			rp:       rpOf("ns.widget"),
			path:     "ns.dev",
			ref:      "ns.widget",
			expected: "ns.widget",
		},
		{
			name:     "no envelope: nothing is guessed",
			rp:       nil,
			path:     "ns.dev",
			ref:      "widget",
			expected: "widget",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := qualifyEntityImageRef(tc.rp, tc.path, tc.ref); got != tc.expected {
				t.Fatalf("qualifyEntityImageRef(%q, %q) = %q, want %q", tc.path, tc.ref, got, tc.expected)
			}
		})
	}
}

// TestPrepareVenueExtrasQualifiesTheNodesImage is the lifecycle-path half of #1316: the dispatch that
// reaches prepare-venue (a check bed) carries the node and NO plans, so the qualification has to be
// applied here — the compile path's is not on this route.
func TestPrepareVenueExtrasQualifiesTheNodesImage(t *testing.T) {
	rp := rpOf("kde-vnc-cdp.kde-vnc-cdp")
	extra := prepareVenueExtras(rp, "kde-vnc-cdp.check-kde-vnc-cdp-pod", spec.LifecycleOpts{}, &spec.Deploy{Image: "kde-vnc-cdp"}, nil)
	if got := extra["image"]; got != "kde-vnc-cdp.kde-vnc-cdp" {
		t.Fatalf("an imported entity's image must reach the substrate qualified; got %v", got)
	}

	// Without an envelope the ref is exactly as authored — today's behaviour, and the substrate
	// reports its own resolution error rather than a rewritten name.
	extra = prepareVenueExtras(nil, "kde-vnc-cdp.check-kde-vnc-cdp-pod", spec.LifecycleOpts{}, &spec.Deploy{Image: "kde-vnc-cdp"}, nil)
	if got := extra["image"]; got != "kde-vnc-cdp" {
		t.Fatalf("with no envelope the authored ref must pass through unchanged; got %v", got)
	}
}

// TestPrepareVenueExtrasQualifiesThePlansBox is the branch a CHECK BED actually takes, measured: the
// dispatch carries NO node image, so the image reaches the substrate from the compiled plan's Box —
// and for an imported entity that Box is the bare LEAF ("kde-vnc-cdp"), which the host then cannot
// resolve. The same namespace rule has to apply to it.
func TestPrepareVenueExtrasQualifiesThePlansBox(t *testing.T) {
	rp := rpOf("kde-vnc-cdp.kde-vnc-cdp")
	plans := []*deploykit.InstallPlan{{Box: "kde-vnc-cdp"}}

	extra := prepareVenueExtras(rp, "kde-vnc-cdp/check-kde-vnc-cdp-pod", spec.LifecycleOpts{}, nil, plans)
	if got := extra["image"]; got != "kde-vnc-cdp.kde-vnc-cdp" {
		t.Fatalf("a plan-derived image must reach the substrate qualified for an imported entity; got %v", got)
	}

	// A root-scope plan box is untouched.
	extra = prepareVenueExtras(rpOf("app"), "app", spec.LifecycleOpts{}, nil, []*deploykit.InstallPlan{{Box: "app"}})
	if got := extra["image"]; got != "app" {
		t.Fatalf("a root-scope plan box must pass through unchanged; got %v", got)
	}
}
