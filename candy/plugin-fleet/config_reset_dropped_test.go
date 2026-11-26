package deploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/kit"
	"gopkg.in/yaml.v3"
)

// TestRemoveDroppedEntry_RemovesOnlyTheUnloadableKey is the regression witness for
// opencharly/plugin-fleet#56: `charly deploy reset <box>` must be able to remove an entry the
// LOADER dropped (a pod body with no `image:`) — the loaded DeployConfig cannot see it, and while
// it sits in the file SaveDeployConfig refuses every later write. The removal must take exactly
// that key, leaving the loadable entries and the config's other sections intact.
func TestRemoveDroppedEntry_RemovesOnlyTheUnloadableKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(kit.DeployConfigEnv, path)

	const fixture = `good:
    pod:
        image: good-image
broken:
    pod:
        resolved_image: broken-overlay:cabc723f3910
cache:
    entries: 0
`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	removed, err := removeDroppedEntry("broken")
	if err != nil {
		t.Fatalf("removeDroppedEntry: %v", err)
	}
	if !removed {
		t.Fatal("removeDroppedEntry reported nothing removed for a key that is in the file")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("the file no longer parses after the removal: %v", err)
	}
	if _, ok := doc["broken"]; ok {
		t.Fatalf("the unloadable entry is still present:\n%s", got)
	}
	if _, ok := doc["good"]; !ok {
		t.Fatalf("the loadable entry was removed with it:\n%s", got)
	}
	if _, ok := doc["cache"]; !ok {
		t.Fatalf("a non-deploy section was removed with it:\n%s", got)
	}
}

// TestRemoveDroppedEntry_NeverTouchesAReservedSection: a deploy entry cannot share a name with a
// reserved top-level section, so a reset aimed at one of those names must decline rather than
// delete the section (the QUAL finding on this PR's first verdict).
func TestRemoveDroppedEntry_NeverTouchesAReservedSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(kit.DeployConfigEnv, path)

	const fixture = "cache:\n    entries: 7\ngood:\n    pod:\n        image: good-image\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	for _, reserved := range []string{"cache", "provides", "ledger", "system", "defaults"} {
		removed, err := removeDroppedEntry(reserved)
		if err != nil {
			t.Fatalf("removeDroppedEntry(%q): %v", reserved, err)
		}
		if removed {
			t.Fatalf("removeDroppedEntry(%q) claimed to remove a reserved section", reserved)
		}
	}
	got, _ := os.ReadFile(path)
	if string(got) != fixture {
		t.Fatalf("a reserved-section name rewrote the file:\n%s", got)
	}
}

// TestRemoveDroppedEntry_NoSuchKeyLeavesTheFileAlone: an ordinary miss (the reset command's
// "No overrides" case) must not rewrite the file.
func TestRemoveDroppedEntry_NoSuchKeyLeavesTheFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(kit.DeployConfigEnv, path)

	const fixture = "good:\n    pod:\n        image: good-image\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	removed, err := removeDroppedEntry("absent")
	if err != nil {
		t.Fatalf("removeDroppedEntry: %v", err)
	}
	if removed {
		t.Fatal("removeDroppedEntry reported a removal for a key that is not in the file")
	}
	got, _ := os.ReadFile(path)
	if string(got) != fixture {
		t.Fatalf("the file changed on an ordinary miss:\n%s", got)
	}
}

// TestRemoveDroppedEntry_UnparseableFileIsLeftAlone: repairing a broken file is `charly migrate`'s
// job, not a reset's — the helper must decline rather than rewrite it.
func TestRemoveDroppedEntry_UnparseableFileIsLeftAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "charly.yml")
	t.Setenv(kit.DeployConfigEnv, path)

	const fixture = "good: [unclosed\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	removed, err := removeDroppedEntry("good")
	if err != nil {
		t.Fatalf("removeDroppedEntry: %v", err)
	}
	if removed {
		t.Fatal("removeDroppedEntry claimed a removal from an unparseable file")
	}
	got, _ := os.ReadFile(path)
	if string(got) != fixture {
		t.Fatalf("the unparseable file was rewritten:\n%s", got)
	}
}
