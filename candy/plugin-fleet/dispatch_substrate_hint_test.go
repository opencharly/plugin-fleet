package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
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

// The WIRING, not just the helper: resolveRefForTarget is the one place the resolver and the
// annotation are joined, and this drives it exactly as compileNodePlans does — an empty
// envelope (no boxes, no candies) and a plain local name, which is precisely the shape a
// `vm:` deploy hits when its substrate is absent.
//
// An earlier revision of this change tested only annotateMissingSubstrate's logic; removing
// the call site left those tests green, which the reviewer correctly rejected. This test
// fails if the annotation is dropped from the call path.
func TestResolveRefForTarget_AnnotatesOnTheCallPath(t *testing.T) {
	empty := &spec.ResolvedProject{} // no Boxes, no Candies — the substrate-absent shape

	_, err := resolveRefForTarget(empty, "vm", "omarchy-vm", t.TempDir())
	if err == nil {
		t.Fatal("a name that is neither a box nor a candy must fail")
	}
	for _, want := range []string{
		`resolving ref "omarchy-vm"`,  // the ref
		`for target "vm"`,             // the target, which the old message omitted
		"plugin-deploy-vm",            // the cause
		"not found as a box or candy", // the preserved original
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the call-path error must contain %q; got: %v", want, err)
		}
	}
}

// The same call path must NOT invent a substrate theory for pod, where the positional ref
// really is a box and "not found as a box or candy" is the whole truth.
func TestResolveRefForTarget_LeavesPodUnannotatedOnTheCallPath(t *testing.T) {
	empty := &spec.ResolvedProject{}

	_, err := resolveRefForTarget(empty, "pod", "typo-box", t.TempDir())
	if err == nil {
		t.Fatal("a name that is neither a box nor a candy must fail")
	}
	if strings.Contains(err.Error(), "substrate is not available") {
		t.Errorf("pod got a substrate theory it cannot have; got: %v", err)
	}
	if !strings.Contains(err.Error(), "not found as a box or candy") {
		t.Errorf("the underlying error was lost for pod; got: %v", err)
	}
}
