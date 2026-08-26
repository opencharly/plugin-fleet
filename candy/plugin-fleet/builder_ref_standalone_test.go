package fleet

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestExternalBuilderPluginRef_StandaloneRef pins the Phase-4 contract: spec.ExternalBuilderPluginRef
// returns the STANDALONE plugin-candy ref (@github.com/opencharly/plugin-builder-<word>/candy/<name>),
// NOT the pre-cutover in-repo path (@github.com/opencharly/charly/candy/plugin-builder-<word>). The
// in-repo candy dirs are deleted in Phase 4, so the old ref makes the builder-connect fetch hang
// (the charly parity test's 10m timeout — RCA'd in spec#51). This test FAILS with the old spec pin
// (v0.2026232.520) and passes with the pinned fix (v0.2026232.521-0.20260826041346-cb64f9476eba).
func TestExternalBuilderPluginRef_StandaloneRef(t *testing.T) {
	for _, word := range []string{"pixi", "npm", "cargo", "aur"} {
		ref, ok := spec.ExternalBuilderPluginRef(word)
		if !ok {
			t.Fatalf("ExternalBuilderPluginRef(%q): not a first-party externalized builder", word)
		}
		if !strings.HasPrefix(ref, "@github.com/opencharly/plugin-builder-"+word+"/candy/") {
			t.Fatalf("ExternalBuilderPluginRef(%q) = %q: want the standalone plugin-candy ref "+
				"@github.com/opencharly/plugin-builder-%s/candy/... (the pre-cutover in-repo path "+
				"@github.com/opencharly/charly/candy/... is deleted in Phase 4)", word, ref, word)
		}
		if strings.Contains(ref, "/charly/candy/") {
			t.Fatalf("ExternalBuilderPluginRef(%q) = %q: stale in-repo ref (charly/candy/...) — "+
				"the moved dirs are deleted in Phase 4", word, ref)
		}
		t.Logf("ExternalBuilderPluginRef(%q) = %s", word, ref)
	}
}
