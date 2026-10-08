package deploy

import (
	"strings"
	"testing"
)

// TestClassifyDeployTarget_RoutesByThePropertyNotTheSnapshot is the acceptance test for
// opencharly/plugin-fleet#10: which targets reach the box resolver is decided by the PROPERTY
// ("does this target's positional ref name something that compiles a primary image?"), never by
// membership in a loader-threaded snapshot.
//
// The case that matters is the last two: a target whose substrate is absent from the closure — and
// a target nobody has classified at all — must NOT reach the resolver. Before this change they fell
// through to it, because c.externalSubstrates degrades to EMPTY when its HostBuild leg fails, and
// the failure then talked about boxes for a ref that can never be a box.
func TestClassifyDeployTarget_RoutesByThePropertyNotTheSnapshot(t *testing.T) {
	// A closure that knows the external substrates, and the degraded one that knows none.
	fullSnapshot := map[string]bool{"vm": true, "android": true, "local": true}
	emptySnapshot := map[string]bool{}

	tests := []struct {
		class  string
		target string
		ext    map[string]bool
		want   deployTargetClass
	}{
		{"imageCompiling — pod's ref IS a box, so the resolver is the right call", "pod", fullSnapshot, imageCompiling},
		{"imageCompiling — kubernetes likewise", "kubernetes", fullSnapshot, imageCompiling},
		{"imageCompiling — a bare pod deploy still compiles with NO substrate snapshot at all (the invariance the old comment promised)", "pod", emptySnapshot, imageCompiling},
		{"imageCompiling — pod with a nil snapshot", "pod", nil, imageCompiling},
		{"targetOnly — local never resolves a ref", "local", fullSnapshot, targetOnly},
		{"targetOnly — local with a nil snapshot", "local", nil, targetOnly},
		{"targetOnly — vm whose substrate IS in the closure", "vm", fullSnapshot, targetOnly},
		{"targetOnly — android whose substrate IS in the closure", "android", fullSnapshot, targetOnly},
		{"notImageCompiling — THE #10 CASE: vm whose substrate is absent from the closure must not reach the resolver", "vm", emptySnapshot, notImageCompiling},
		{"notImageCompiling — a target nobody has classified cannot silently inherit box-resolver behaviour", "newsubstrate", fullSnapshot, notImageCompiling},
		{"notImageCompiling — nor can it when the snapshot is empty", "newsubstrate", emptySnapshot, notImageCompiling},
	}
	for _, tc := range tests {
		t.Run(tc.class, func(t *testing.T) {
			got := classifyDeployTarget(tc.target, tc.ext)
			if got != tc.want {
				t.Errorf("classifyDeployTarget(%q, %v) = %d, want %d (%s)", tc.target, tc.ext, got, tc.want, tc.class)
			}
			// The router's decision, stated as the test's own assertion so the two cannot drift:
			// ONLY the imageCompiling class consults the box resolver.
			if reaches := got == imageCompiling; reaches != (tc.want == imageCompiling) {
				t.Errorf("target %q would%s reach the box resolver", tc.target, map[bool]string{true: "", false: " NOT"}[reaches])
			}
		})
	}
}

// TestSubstrateUnavailableError_NamesTheSubstrateAndNeverTheResolver pins the wording of the early
// failure a non-image-compiling target now gets: it must name the substrate and its plugin, and it
// must not tell the operator that the ref was resolved as a box — which is precisely the message the
// issue was filed about.
func TestSubstrateUnavailableError_NamesTheSubstrateAndNeverTheResolver(t *testing.T) {
	err := substrateUnavailableError("vm")
	if err == nil {
		t.Fatal("substrateUnavailableError returned nil")
	}
	msg := err.Error()
	for _, want := range []string{`"vm" deploy substrate is not available`, "deploy:vm", "not resolved as a box"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message must contain %q; got: %s", want, msg)
		}
	}
	if !strings.Contains(msg, "is in the candy closure") {
		t.Errorf("the message must name the real cause (no plugin serving the substrate in the closure); got: %s", msg)
	}
	// The diagnostic must not assert a plugin that need not exist: a target nobody has classified
	// may have none, so the convention is named AS a convention (block-2 nit of the #33 review).
	if !strings.Contains(msg, "the convention is plugin-deploy-vm") {
		t.Errorf("the message must mark the plugin name as a convention, not a fact; got: %s", msg)
	}
}
