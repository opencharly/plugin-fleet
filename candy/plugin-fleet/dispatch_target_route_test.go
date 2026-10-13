package deploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// THE ROW'S MECHANISM, pinned as behaviour: a `vm:` deploy whose substrate is absent must be
// REFUSED before any box or candy is consulted — even when the envelope holds a candy of the same
// name, which is exactly how the old branch produced a wrong answer instead of a wrong message.
//
// The branch it replaces read `c.externalSubstrates[target]`, a snapshot that degrades to nil on
// three separate failures; indexing a nil map yields false, so the vm target fell through into
// resolveDeployRef. This test drives the JOINED path (routeTargetRef), so removing the join — or
// reversing the order — turns it red.
func TestRouteTargetRef_RefusesAVmBeforeAnyCandyIsConsulted(t *testing.T) {
	// An envelope that WOULD answer for "omarchy-vm" as a candy: the shape the row warns about.
	env := &spec.ResolvedProject{Candies: map[string]spec.CandyView{"omarchy-vm": {}}}
	degraded := errors.New("loader-threaded HostBuild: boom")

	ref, targetOnly, err := routeTargetRef(env, "vm", "omarchy-vm", t.TempDir(), nil, degraded)
	if err == nil {
		t.Fatal("a vm whose substrate set could not be read must be refused")
	}
	if ref != nil || targetOnly {
		t.Errorf("a refused target must return no ref and not report targetOnly; got ref=%v targetOnly=%v", ref, targetOnly)
	}
	for _, want := range []string{`"vm"`, "compiles no primary image plan", "same-named candy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must mention %q; got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "not found as a box or candy") {
		t.Errorf("the refusal leaked the box/candy message the row exists to stop: %v", err)
	}
	if !errors.Is(err, degraded) {
		t.Errorf("the degraded read's cause must wrap, so errors.Is finds it; got: %v", err)
	}

	// CONTROL OF THE CONTROL: the resolver WOULD answer for this very ref, so the refusal above is a
	// genuine change of outcome and not a no-op. This is precisely what the old branch did with a
	// degraded snapshot — a nil lookup read false, the vm deploy went to resolveDeployRef, and the
	// candy answered for it.
	if direct, derr := resolveDeployRef(env, "omarchy-vm", t.TempDir()); derr != nil || direct == nil {
		t.Fatalf("the control envelope must resolve %q as a candy, else the refusal proves nothing: %v", "omarchy-vm", derr)
	}

	// THE CONTROL, same envelope and same ref: pod IS image-bearing, so the resolver IS consulted
	// and the candy answers. This proves the envelope resolution works and that the CLASS — not the
	// ref, not the snapshot — decides.
	ref, targetOnly, err = routeTargetRef(env, "pod", "omarchy-vm", t.TempDir(), nil, degraded)
	if err != nil {
		t.Fatalf("an image-bearing target must resolve its ref through the box/candy resolver; got: %v", err)
	}
	if targetOnly || ref == nil {
		t.Errorf("pod with a resolvable ref must return that ref, not the target-only path; got ref=%v targetOnly=%v", ref, targetOnly)
	}
}

// A HEALTHY snapshot that simply does not list the target's substrate — the other half of the row's
// reproduction — is refused with the substrate message, and still never reaches the resolver.
func TestRouteTargetRef_RefusesANonImageTargetMissingFromAHealthySet(t *testing.T) {
	env := &spec.ResolvedProject{Candies: map[string]spec.CandyView{"omarchy-vm": {}}}
	healthy := map[string]bool{"local": true, "pod": true} // a real set, without vm

	ref, targetOnly, err := routeTargetRef(env, "vm", "omarchy-vm", t.TempDir(), healthy, nil)
	if err == nil || ref != nil || targetOnly {
		t.Fatalf("a vm missing from a healthy substrate set must be refused; got ref=%v targetOnly=%v err=%v", ref, targetOnly, err)
	}
	for _, want := range []string{`"vm"`, "plugin-deploy-vm", "not available in this project", "was never a box or candy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must mention %q; got: %v", want, err)
		}
	}
}

// The class decides, and presence decides only WITHIN the class: pod/kubernetes compile a primary
// image whatever the snapshot says; local needs no substrate word; a non-image target whose
// substrate IS present is target-only, unchanged.
func TestTargetOnlyRoute_ClassAndPresence(t *testing.T) {
	broken := errors.New("degraded")
	for _, target := range []string{"pod", "kubernetes"} {
		targetOnly, err := targetOnlyRoute(target, nil, broken)
		if err != nil || targetOnly {
			t.Errorf("%q compiles a primary image and must resolve its ref (snapshot-independent); got targetOnly=%v err=%v", target, targetOnly, err)
		}
	}
	if targetOnly, err := targetOnlyRoute("local", nil, broken); err != nil || !targetOnly {
		t.Errorf("local is target-only and needs no substrate word; got targetOnly=%v err=%v", targetOnly, err)
	}
	// A FUTURE substrate gets this for free: the class test is "not image-bearing", not a list.
	for _, target := range []string{"vm", "android", "kubevirt", "some-future-substrate"} {
		if targetOnly, err := targetOnlyRoute(target, map[string]bool{target: true}, nil); err != nil || !targetOnly {
			t.Errorf("%q with a present substrate is target-only; got targetOnly=%v err=%v", target, targetOnly, err)
		}
	}
}

// The resolver path keeps its own contract for the targets that legitimately reach it: the failure
// names the ref and the target, and carries NO substrate theory — for pod the positional ref really
// is a box.
func TestResolveRefForTarget_NamesRefAndTargetWithoutASubstrateTheory(t *testing.T) {
	empty := &spec.ResolvedProject{}

	_, err := resolveRefForTarget(empty, "pod", "typo-box", t.TempDir())
	if err == nil {
		t.Fatal("a name that is neither a box nor a candy must fail")
	}
	for _, want := range []string{`resolving ref "typo-box"`, `for target "pod"`, "not found as a box or candy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the resolver error must contain %q; got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "substrate is not available") {
		t.Errorf("pod got a substrate theory it cannot have; got: %v", err)
	}
}
