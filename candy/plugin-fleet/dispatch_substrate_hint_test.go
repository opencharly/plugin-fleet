package fleet

import (
	"errors"
	"strings"
	"testing"
)

// A `vm:` deploy in a project whose closure lacks plugin-deploy-vm used to fail with
//
//	ResolveDeployRef: "omarchy-vm" not found as a box or candy in the resolved-project envelope
//
// after `charly vm build` and `charly vm create` on that SAME entity both succeeded. charly
// plainly knows it is a VM, and that message sends the reader to fix the one thing that is
// not wrong — there is no way to make a kind:vm entity into a box.
func TestAnnotateMissingSubstrate_NamesTheCauseForATargetWithNoImage(t *testing.T) {
	underlying := errors.New(`ResolveDeployRef: "omarchy-vm" not found as a box or candy in the resolved-project envelope`)
	got := annotateMissingSubstrate("vm", underlying)

	for _, want := range []string{"vm", "plugin-deploy-vm", "not available in this project"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("the annotated error must mention %q; got: %v", want, got)
		}
	}
	// The original must survive verbatim — the annotation adds a cause, it does not replace
	// the evidence.
	if !strings.Contains(got.Error(), "not found as a box or candy") {
		t.Errorf("the underlying resolution error was lost: %v", got)
	}
	if !errors.Is(got, underlying) {
		t.Error("the annotation must wrap, so errors.Is still finds the original")
	}
}

// pod and kubernetes DO compile a primary image from their positional ref, so for them
// "not found as a box or candy" is the whole truth and must not be muddied with a substrate
// theory that does not apply.
func TestAnnotateMissingSubstrate_LeavesImageBearingTargetsAlone(t *testing.T) {
	underlying := errors.New(`ResolveDeployRef: "typo-box" not found as a box or candy`)
	for _, target := range []string{"pod", "kubernetes", "local", ""} {
		got := annotateMissingSubstrate(target, underlying)
		if got != underlying {
			t.Errorf("target %q: the error must pass through untouched; got: %v", target, got)
		}
	}
}

// A target charly does not know is still better served by the hint than by silence: the
// annotation is keyed on "this target compiles no primary image", not on a hardcoded list
// of substrate names, so a future substrate gets it for free.
func TestAnnotateMissingSubstrate_AppliesToAnyNonImageTarget(t *testing.T) {
	underlying := errors.New("ResolveDeployRef: nope")
	got := annotateMissingSubstrate("android", underlying)
	if !strings.Contains(got.Error(), "plugin-deploy-android") {
		t.Errorf("a future substrate should get the hint without a code change; got: %v", got)
	}
}
