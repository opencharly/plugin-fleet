package deploy

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// builder_preresolve_test.go — relocated from charly/builder_preresolve_test.go (#55 decoupling,
// Batch A): single test asserts deploykit.DetectExternalizedBuilders directly across 5 scenarios,
// zero charly coupling.

// TestDetectExternalizedBuilders_ScopedAndDistroGated is the regression gate for the C4 over-build:
// a deploy must surface ONLY the builders its resolved closure actually triggers, and a manager
// declaration (aur) is scoped by its distro tag exactly like OS packages — a fedora deploy never
// surfaces an arch-tagged aur declaration (deploykit.NeedsBuilder over the image's tag chain,
// opencharly/sdk#608). The pre-fix code (blanket detection, no scope) would FAIL these.
func TestDetectExternalizedBuilders_ScopedAndDistroGated(t *testing.T) {
	// (1) A pixi-only deploy on a FEDORA image surfaces ONLY pixi — not npm/cargo/aur. This is the
	// exact C4 scenario (check-jupyter-pod): jupyter has pixi.toml, nothing else.
	pixiOnly := map[string]spec.CandyReader{"jupyter": pixiCandy(t, "jupyter")}
	got := deploykit.DetectExternalizedBuilders([]string{"jupyter"}, pixiOnly, builderTestImg("fedora"))
	if !reflect.DeepEqual(got, []string{"pixi"}) {
		t.Fatalf("pixi-only fedora deploy surfaced %v, want exactly [pixi] (the C4 over-build is back if this lists npm/cargo/aur)", got)
	}

	// (2) A candy declaring aur packages under `arch`, deployed on a FEDORA image, surfaces NO aur —
	// the tag scope. (It also carries no pixi.toml etc., so the result is empty.)
	multiAur := map[string]spec.CandyReader{"chrome": aurCandy("chrome", "google-chrome")}
	got = deploykit.DetectExternalizedBuilders([]string{"chrome"}, multiAur, builderTestImg("fedora"))
	if len(got) != 0 {
		t.Fatalf("fedora deploy of an aur:-section candy surfaced %v, want [] (an arch-tagged aur declaration must not reach a fedora image)", got)
	}

	// (3) The SAME candy on an ARCH image DOES surface aur — with no box opt-in.
	got = deploykit.DetectExternalizedBuilders([]string{"chrome"}, multiAur, builderTestImg("arch"))
	if !reflect.DeepEqual(got, []string{"aur"}) {
		t.Fatalf("arch deploy of an aur:-section candy surfaced %v, want [aur]", got)
	}

	// (4) A candy that triggers NO builder surfaces nothing.
	none := map[string]spec.CandyReader{"plain": testCandy("plain", spec.CandyModel{}, spec.CandyView{})}
	if got := deploykit.DetectExternalizedBuilders([]string{"plain"}, none, builderTestImg("fedora")); len(got) != 0 {
		t.Fatalf("no-builder candy surfaced %v, want []", got)
	}

	// (5) No BuilderConfig (e.g. a synthetic compile context) → nil, never a panic.
	if got := deploykit.DetectExternalizedBuilders([]string{"jupyter"}, pixiOnly, &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "x"}}); got != nil {
		t.Fatalf("nil BuilderConfig surfaced %v, want nil", got)
	}
}
