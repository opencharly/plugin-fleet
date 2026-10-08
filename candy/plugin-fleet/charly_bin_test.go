package deploy

import (
	"strings"
	"testing"
)

// TestSelectCharlyBinaryRefusesTheAmbientGuess is the regression guard for
// opencharly/plugin-fleet#29, and it is a unit test of the DECISION, because the decision was the
// defect: the old resolution fell back to `exec.LookPath("charly")` — ANY binary on PATH — so the
// verdict of a live loader test depended on the host's package vintage instead of on the tree under
// test, and the skew came out looking like a defect in writeVmBoxEntity.
//
// Mutation control: restoring that fallback (`if envBin == "" && ambientPath != "" { return
// ambientPath, "" }`) turns case 2 red — it would hand back the ambient binary as usable, which is
// exactly the laundering this pins shut.
func TestSelectCharlyBinaryRefusesTheAmbientGuess(t *testing.T) {
	t.Run("CHARLY_BIN names the binary: used verbatim, no ambient fallback", func(t *testing.T) {
		bin, skip := selectCharlyBinary("/tree/bin/charly", "/usr/bin/charly")
		if bin != "/tree/bin/charly" || skip != "" {
			t.Fatalf("selectCharlyBinary = (%q, %q), want the named binary and no skip", bin, skip)
		}
	})

	t.Run("CHARLY_BIN unset with an ambient charly: REFUSED, and the refusal names it", func(t *testing.T) {
		bin, skip := selectCharlyBinary("", "/usr/bin/charly")
		if bin != "" {
			t.Fatalf("selectCharlyBinary handed back %q as usable; an unvetted PATH binary is exactly "+
				"what plugin-fleet#29 was", bin)
		}
		for _, want := range []string{"/usr/bin/charly", "CHARLY_BIN", "plugin-fleet#29"} {
			if !strings.Contains(skip, want) {
				t.Errorf("the skip reason %q does not name %q — a refusal must say what it refused and why", skip, want)
			}
		}
	})

	t.Run("CHARLY_BIN unset and no charly anywhere: unproven, not failed", func(t *testing.T) {
		bin, skip := selectCharlyBinary("", "")
		if bin != "" {
			t.Fatalf("selectCharlyBinary invented a binary %q", bin)
		}
		if !strings.Contains(skip, "CHARLY_BIN") || !strings.Contains(skip, "unproven") {
			t.Errorf("the skip reason %q must say the boundary is unproven and name CHARLY_BIN", skip)
		}
	})
}

// TestVmBoxControlConfigIsAVmNodeShape pins the POSITIVE CONTROL's own shape. The control exists to
// answer "can this binary load this tree's `vm:` entity at all?", which an EMPTY config could not
// (that was the old probe's defect: every vintage loads nothing). If a future edit reduces the
// control to something unrelated to the asserted shape, it silently stops discriminating skew from
// a real regression — so the shape is asserted here rather than left to the reader's eye.
func TestVmBoxControlConfigIsAVmNodeShape(t *testing.T) {
	for _, want := range []string{"vm:", "source:", "kind: imported", "disk_path:", "disk_format: qcow2"} {
		if !strings.Contains(vmBoxControlConfig, want) {
			t.Errorf("the live loader control no longer carries %q, so it can no longer separate a stale "+
				"binary from a writer regression (opencharly/plugin-fleet#29):\n%s", want, vmBoxControlConfig)
		}
	}
}
