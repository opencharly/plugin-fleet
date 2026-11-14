package deploy

import (
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
