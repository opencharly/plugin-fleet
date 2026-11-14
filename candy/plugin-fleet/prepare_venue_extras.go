package deploy

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// prepareVenueExtras builds the extra params a substrate's PrepareVenue op receives.
//
// `image` is the base image the overlay/venue prep resolves. A dispatch-merged node
// carries it as `image:`; a REF-BASED deploy carries NO node at all (unified_targets.go:
// "node … nil for a ref-based deploy"), so before this helper the key was the only thing
// left and the substrate fell back to it — handing a DEPLOY KEY to the box resolver:
//
//	plugin-deploy-pod prepare-venue: overlay build: resolving "openwebui-lab" via charly.yml: not found
//
// for every `deploy add <name> <box>` whose name differs from its box (plugin-deploy-pod#51).
// The plans the operator's ref was resolved into DO carry the box (`InstallPlan.Box` is the
// deployable box name), so the extras fall back to it — the ref the operator actually named,
// never the key.
func prepareVenueExtras(opts spec.LifecycleOpts, node *spec.Deploy, plans []*deploykit.InstallPlan) map[string]any {
	extra := map[string]any{"opts": opts}
	if node != nil && node.Image != "" {
		extra["image"] = node.Image
		return extra
	}
	if box := planBaseBox(plans); box != "" {
		extra["image"] = box
	}
	return extra
}

// planBaseBox returns the box the dispatch's plans were resolved from — the first
// non-empty `InstallPlan.Box` — or "" when no plan carries one. Nil plans and plans for a
// single candy (which set `Candy` instead) are skipped rather than shadowing a later plan.
func planBaseBox(plans []*deploykit.InstallPlan) string {
	for _, p := range plans {
		if p != nil && p.Box != "" {
			return p.Box
		}
	}
	return ""
}
