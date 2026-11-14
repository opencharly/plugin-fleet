package deploy

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// TestCountDeployEntries pins the second half of sdk#517: the import success line reported the
// number of FILES, so `charly deploy import one-file.yml` printed "Imported 1 file(s)" over an
// import that landed nothing at all. The reported number must be the entries the files carry.
func TestCountDeployEntries(t *testing.T) {
	inputs := []*deploykit.DeployConfig{
		{Deploy: map[string]spec.DeployNode{"a": {Image: "x"}, "b": {Image: "y"}}},
		nil, // a nil input contributes nothing and must not panic the count
		{Deploy: map[string]spec.DeployNode{"c": {Image: "z"}}},
		{}, // an empty config is zero entries, not one
	}
	if got := countDeployEntries(inputs); got != 3 {
		t.Fatalf("countDeployEntries = %d, want 3 (a, b, c)", got)
	}
	if got := countDeployEntries(nil); got != 0 {
		t.Fatalf("countDeployEntries(nil) = %d, want 0", got)
	}
}

// TestPluralWord pins the wording the line uses, so a one-entry import does not read
// "1 entries".
func TestPluralWord(t *testing.T) {
	cases := map[int]string{0: "entries", 1: "entry", 2: "entries"}
	for n, want := range cases {
		if got := pluralWord(n, "entry", "entries"); got != want {
			t.Fatalf("pluralWord(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestCountBoxInInputs pins the review's finding 2: `--box` must report an entry only when the INPUT
// files carry that box. A box that only the existing config holds made the import line print
// "Imported 1 entry" over nothing imported for it.
func TestCountBoxInInputs(t *testing.T) {
	inputs := []*deploykit.DeployConfig{
		nil,
		{Deploy: map[string]spec.DeployNode{"present": {Image: "x"}}},
	}
	if got := countBoxInInputs(inputs, "present"); got != 1 {
		t.Fatalf("countBoxInInputs(present) = %d, want 1", got)
	}
	if got := countBoxInInputs(inputs, "absent"); got != 0 {
		t.Fatalf("countBoxInInputs(absent) = %d, want 0 (the import lands nothing for a box the files do not carry)", got)
	}
	if got := countBoxInInputs(nil, "anything"); got != 0 {
		t.Fatalf("countBoxInInputs(nil) = %d, want 0", got)
	}
}

// TestImportSuccessLine pins the EMITTED sentence, not just the numbers that feed it: a revert to
// "Imported %d file(s)" — the bug this change removes — or a wrong field order would otherwise pass
// every other test and the repo gate (review round 2, block 2).
func TestImportSuccessLine(t *testing.T) {
	cases := []struct {
		entries, files int
		want           string
	}{
		{2, 1, "Imported 2 entries from 1 file(s)"},
		{1, 1, "Imported 1 entry from 1 file(s)"},
		{0, 3, "Imported 0 entries from 3 file(s)"},
		{3, 2, "Imported 3 entries from 2 file(s)"},
	}
	for _, c := range cases {
		if got := importSuccessLine(c.entries, c.files); got != c.want {
			t.Fatalf("importSuccessLine(%d,%d) = %q, want %q", c.entries, c.files, got, c.want)
		}
	}
	// The old wording must be IMPOSSIBLE to emit, not merely unlikely.
	if got := importSuccessLine(1, 1); strings.Contains(got, "file(s) into") || strings.Contains(got, "Imported 1 file") {
		t.Fatalf("the success line must never report FILES as the import's result; got %q", got)
	}
}
