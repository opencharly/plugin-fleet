package deploy

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// selectors_test.go — the per-deploy selector flags. Each arm pins one property
// the flags exist for: the value lands on the deploy ENTITY (flag overrides the
// authored value), it survives a YAML read-back, the schema vocabulary is the
// only list, and the candy constraint stays the resolver's decision.

func TestApplySelectorFlags_RoundTripOntoTheEntity(t *testing.T) {
	node := &spec.DeployNode{Target: "pod", Image: "fedora-coder", Engine: spec.EngineName("podman")}
	flags := selectorFlags{Engine: "containerd", Namespace: "k8s.io", Rootful: "native"}

	got, err := applySelectorFlags(node, "pod", flags, "")
	if err != nil {
		t.Fatalf("applySelectorFlags: %v", err)
	}
	if got.Engine != spec.EngineName("containerd") {
		t.Errorf("Engine = %q, want containerd (the flag overrides the authored value)", got.Engine)
	}
	if got.Namespace != "k8s.io" {
		t.Errorf("Namespace = %q, want k8s.io", got.Namespace)
	}
	if got.Rootful != spec.RootfulMode("native") {
		t.Errorf("Rootful = %q, want native", got.Rootful)
	}
	if got.Image != "fedora-coder" {
		t.Errorf("Image = %q; applying selectors must not disturb the rest of the entity", got.Image)
	}
	// The authored node is not mutated in place.
	if node.Engine != spec.EngineName("podman") {
		t.Errorf("the input node was mutated: Engine = %q", node.Engine)
	}

	// A VM backend takes the vm target.
	vm, err := applySelectorFlags(&spec.DeployNode{Target: "vm", From: "arch"}, "vm", selectorFlags{Backend: "krun"}, "")
	if err != nil {
		t.Fatalf("applySelectorFlags(vm): %v", err)
	}
	if vm.Backend != spec.VmBackend("krun") {
		t.Errorf("Backend = %q, want krun", vm.Backend)
	}
}

func TestApplySelectorFlags_SynthesizesNodeForRefDeploy(t *testing.T) {
	got, err := applySelectorFlags(nil, "pod", selectorFlags{Engine: "containerd"}, "")
	if err != nil {
		t.Fatalf("applySelectorFlags(nil): %v", err)
	}
	if got == nil || got.Target != "pod" || got.Engine != spec.EngineName("containerd") {
		t.Fatalf("synthesized node = %+v, want {target:pod engine:containerd}", got)
	}

	// With no flags a nil node stays nil (the host keeps its own synthesis path).
	if got, err := applySelectorFlags(nil, "pod", selectorFlags{}, ""); err != nil || got != nil {
		t.Fatalf("applySelectorFlags(nil, no flags) = (%+v, %v), want (nil, nil)", got, err)
	}
}

func TestApplySelectorFlags_RefusesTheWrongSubstrate(t *testing.T) {
	for _, arm := range []struct {
		name   string
		target string
		flags  selectorFlags
		want   string
	}{
		{"engine on local", "local", selectorFlags{Engine: "podman"}, "applies to a pod deploy"},
		{"backend on pod", "pod", selectorFlags{Backend: "krun"}, "applies to a vm deploy"},
		{"namespace on vm", "vm", selectorFlags{Namespace: "k8s.io"}, "applies to a pod deploy"},
		{"rootful on kubernetes", "kubernetes", selectorFlags{Rootful: "native"}, "applies to a pod deploy"},
	} {
		_, err := applySelectorFlags(&spec.DeployNode{Target: arm.target}, arm.target, arm.flags, "")
		if err == nil || !strings.Contains(err.Error(), arm.want) {
			t.Errorf("%s: error = %v, want one containing %q", arm.name, err, arm.want)
		}
	}
}

func TestApplySelectorFlags_NamesTheSchemaVocabulary(t *testing.T) {
	_, err := applySelectorFlags(&spec.DeployNode{Target: "pod"}, "pod", selectorFlags{Engine: "nerdctl"}, "")
	if err == nil {
		t.Fatal("engine=nerdctl must be refused")
	}
	for _, want := range container.EngineNames() {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the engine rejection must name %q:\n%s", want, err)
		}
	}

	_, err = applySelectorFlags(&spec.DeployNode{Target: "vm"}, "vm", selectorFlags{Backend: "bhyve"}, "")
	if err == nil {
		t.Fatal("backend=bhyve must be refused")
	}
	for _, want := range spec.VmBackends {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the backend rejection must name %q:\n%s", want, err)
		}
	}

	_, err = applySelectorFlags(&spec.DeployNode{Target: "pod"}, "pod", selectorFlags{Rootful: "rootless"}, "")
	if err == nil {
		t.Fatal("rootful=rootless must be refused")
	}
	for _, want := range spec.RootfulModes {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the rootful rejection must name %q:\n%s", want, err)
		}
	}
}

// TestEngineFlag_CandyConstraintConflict pins the precedence rule's constraint
// half: a composed candy that requires a different engine is an ERROR, never an
// override, even when the flag names a valid engine.
func TestEngineFlag_CandyConstraintConflict(t *testing.T) {
	if _, err := resolveEngineFlag("containerd", "", "podman"); err == nil ||
		!strings.Contains(err.Error(), "engine constraint conflict") {
		t.Fatalf("conflicting candy constraint = %v, want an engine constraint conflict", err)
	}
	if got, err := resolveEngineFlag("containerd", "", "containerd"); err != nil || got != spec.EngineName("containerd") {
		t.Fatalf("matching candy constraint = (%q, %v), want (containerd, nil)", got, err)
	}
	// `auto` is a selector, not a provider word: it is passed through for
	// detection at the substrate, not resolved (and not rejected) here.
	if got, err := resolveEngineFlag("auto", "", ""); err != nil || got != spec.EngineName("auto") {
		t.Fatalf("engine=auto = (%q, %v), want (auto, nil)", got, err)
	}
}

func TestSelectorTargetFor_VmAddressWins(t *testing.T) {
	if got := selectorTargetFor("pod", "arch"); got != "vm" {
		t.Errorf("selectorTargetFor(pod, arch) = %q, want vm (a `vm:` ref-based deploy)", got)
	}
	if got := selectorTargetFor("pod", ""); got != "pod" {
		t.Errorf("selectorTargetFor(pod, \"\") = %q, want pod", got)
	}
	if got := selectorTargetFor("local", ""); got != "local" {
		t.Errorf("selectorTargetFor(local, \"\") = %q, want local", got)
	}
	// The derived target is what the flag path judges: --backend is valid on it.
	if _, err := applySelectorFlags(nil, selectorTargetFor("pod", "arch"), selectorFlags{Backend: "krun"}, ""); err != nil {
		t.Errorf("--backend on a vm: ref-based deploy must be accepted: %v", err)
	}
}

func TestApplyDeploySelectors_PersistsAndReadsBack(t *testing.T) {
	dc := &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{
		"my-pod": {Target: "pod", Image: "fedora-coder"},
	}}
	flags := selectorFlags{Engine: "containerd", Namespace: "k8s.io", Rootful: "native"}

	if !applyDeploySelectors(dc, "my-pod", flags) {
		t.Fatal("applyDeploySelectors reported no change")
	}
	entry := dc.Deploy["my-pod"]
	if entry.Engine != spec.EngineName("containerd") || entry.Namespace != "k8s.io" ||
		entry.Rootful != spec.RootfulMode("native") || entry.Image != "fedora-coder" {
		t.Fatalf("entry after apply = %+v", entry)
	}

	// Read it back through YAML, the boundary the next load uses.
	data, err := yaml.Marshal(dc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back deploykit.DeployConfig
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := back.Deploy["my-pod"]
	if !ok {
		t.Fatal("the entry did not survive the YAML round-trip")
	}
	if got.Engine != spec.EngineName("containerd") || got.Namespace != "k8s.io" ||
		got.Rootful != spec.RootfulMode("native") || got.Image != "fedora-coder" {
		t.Fatalf("entry read back = %+v", got)
	}

	// Same values again: no write.
	if applyDeploySelectors(dc, "my-pod", flags) {
		t.Error("re-applying identical selectors must report no change")
	}
}

func TestApplyDeploySelectors_NeverFabricatesOrClimbs(t *testing.T) {
	dc := &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{}}
	// No entry yet: the substrate has not created one, so a patch must not
	// fabricate a partial (image-less) pod entry.
	if applyDeploySelectors(dc, "my-pod", selectorFlags{Engine: "containerd"}) {
		t.Error("an absent entry must be left alone")
	}
	if len(dc.Deploy) != 0 {
		t.Errorf("a partial entry was fabricated: %+v", dc.Deploy)
	}

	// A nested (dotted) child lives inside its parent's entry, never top-level.
	dc.Deploy["root"] = spec.DeployNode{Target: "pod", Image: "base"}
	if applyDeploySelectors(dc, "root.child", selectorFlags{Engine: "containerd"}) {
		t.Error("a dotted child name must not be patched as a top-level entry")
	}
	if _, ok := dc.Deploy["root.child"]; ok {
		t.Error("a top-level entry was created for a dotted child")
	}
}
