package deploy

// Relocated from charly/check_bed_run_test.go (#55 decoupling cone, Batch C,
// per the binding file-ownership ruling): TestBedCheckLiveRefs asserts
// deploy.BedCheckLiveRefs directly — a genuine deploykit-behavior
// assertion (per Ambiguous-item-1's ruling), not charly-loader integration
// coverage, so it moves here rather than staying in charly/check_bed_run_test.go
// alongside the bed-persist cluster's genuinely-integration tests.

import (
	"testing"

	"github.com/opencharly/spec/deploy"
	"github.com/opencharly/spec/spec"
)

// TestBedCheckLiveRefs proves `charly check run <bed>` check-lives the substrate AND
// every in-substrate member (dotted, authored order) — so a nested pod's baked candy/box
// check runs against its real venue. Before the nested-check fix this produced
// only [name], so a nested selkies-kde pod was deployed but never evaluated.
func TestBedCheckLiveRefs(t *testing.T) {
	// Flat bed: just the substrate (identical to the prior behavior).
	if got := deploy.BedCheckLiveRefs("check-pod", nil); len(got) != 1 || got[0] != "check-pod" {
		t.Fatalf("flat bed: got %v, want [check-pod]", got)
	}
	// Nested bed: substrate first, then each member child as a dotted path in authored
	// order (the ONE ordered Member tree — the deterministic successor of the key sort).
	nested := &spec.DeployNode{
		Target: "vm",
		Member: []spec.Member{
			{Name: "cuda-pod", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Target: "pod"}},
			{Name: "selkies-kde", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Target: "pod"}},
		},
	}
	got := deploy.BedCheckLiveRefs("check-cachyos-gpu-vm", nested)
	want := []string{
		"check-cachyos-gpu-vm",
		"check-cachyos-gpu-vm.cuda-pod", // authored before selkies-kde
		"check-cachyos-gpu-vm.selkies-kde",
	}
	if len(got) != len(want) {
		t.Fatalf("nested bed: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nested bed ref[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}
