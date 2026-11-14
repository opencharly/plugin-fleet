package deploy

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// TestPrepareVenueExtrasCarriesTheImage pins plugin-deploy-pod#51: a REF-based deploy
// dispatch carries no node at all (unified_targets.go: "node … nil for a ref-based
// deploy"), so `extra["image"]` was never set and the substrate fell back to the deploy
// KEY — which the host then resolved as a box:
//
//	overlay build: resolving "openwebui-lab" via charly.yml: not found
//
// The plans the operator's ref was resolved into DO carry the box, so the extras must
// fall back to it.
func TestPrepareVenueExtrasCarriesTheImage(t *testing.T) {
	plans := []*deploykit.InstallPlan{{Box: "comfyui-lab"}}

	t.Run("node image wins", func(t *testing.T) {
		extra := prepareVenueExtras(spec.LifecycleOpts{}, &spec.Deploy{Image: "declared-box"}, plans)
		if got := extra["image"]; got != "declared-box" {
			t.Fatalf("a dispatch-merged node's image must win; got %v", got)
		}
	})

	t.Run("ref-based deploy takes the plan's box", func(t *testing.T) {
		extra := prepareVenueExtras(spec.LifecycleOpts{}, nil, plans)
		if got := extra["image"]; got != "comfyui-lab" {
			t.Fatalf("a ref-based deploy must carry the plan's box as the image (the deploy key is not a box); got %v", got)
		}
	})

	t.Run("empty node image also falls back", func(t *testing.T) {
		extra := prepareVenueExtras(spec.LifecycleOpts{}, &spec.Deploy{}, plans)
		if got := extra["image"]; got != "comfyui-lab" {
			t.Fatalf("an empty node image must fall back to the plan's box; got %v", got)
		}
	})

	t.Run("no box anywhere leaves image unset", func(t *testing.T) {
		extra := prepareVenueExtras(spec.LifecycleOpts{}, nil, []*deploykit.InstallPlan{{}})
		if _, ok := extra["image"]; ok {
			t.Fatalf("with no node image and no plan box, image must stay unset (the substrate's own fallback applies); got %v", extra["image"])
		}
	})
}

// TestPlanBaseBoxSkipsEmptyPlans pins the scan: a nil plan or a plan with no Box must not
// shadow a later plan that carries one.
func TestPlanBaseBoxSkipsEmptyPlans(t *testing.T) {
	plans := []*deploykit.InstallPlan{nil, {}, {Box: "later-box"}}
	if got := planBaseBox(plans); got != "later-box" {
		t.Fatalf("the scan must skip nil/empty plans; got %q", got)
	}
	if got := planBaseBox(nil); got != "" {
		t.Fatalf("no plans must yield empty; got %q", got)
	}
}
