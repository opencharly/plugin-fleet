package deploy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// selectors.go — the per-deploy SELECTOR flags of `charly deploy add`.
//
// The W3.1 precedence chain is flag > deploy entity > env > settings file >
// built-in default. Before this file the deploy-entity level had no CLI source:
// `deploy add` could not write Engine / Backend / Namespace / Rootful onto the
// persisted entry, so a resolver reading the second level could never see a
// value the CLI had set.
//
// Three rules bind this file:
//   - the vocabulary is the schema's (container.EngineNames, spec.VmBackends,
//     spec.RootfulModes), never a list here;
//   - the engine flag is validated THROUGH deploykit.ResolvePodEngine, so the
//     candy `engine:` CONSTRAINT stays the resolver's decision — a conflict is
//     an error, never an override;
//   - a write goes through the SAME locked read-modify-write cycle every other
//     plugin-side overlay write uses (mutateDeployConfig), and only PATCHES an
//     entry the substrate has already created — it never fabricates a partial
//     entry (a pod entry without `image:` would fail the next load).

// autoSelector is the resolution selector shared by the engine and VM-backend
// vocabularies; spec's own resolvers spell it the same way. It is not a provider
// word: it asks the resolver to detect at that level.
const autoSelector = "auto"

// selectorFlags are the four per-deploy selection flags.
type selectorFlags struct {
	Engine    string
	Backend   string
	Namespace string
	Rootful   string
}

func (f selectorFlags) any() bool {
	return f.Engine != "" || f.Backend != "" || f.Namespace != "" || f.Rootful != ""
}

// selectorTargets maps a substrate kind to the selectors its canonical table
// declares (schema/deploy.cue): pod carries engine/namespace/rootful, vm carries
// backend, every other substrate carries none. It is stated ONCE; the loader
// re-checks the same fact from #DeployTraits at load, so this is a fast refusal,
// not the authority.
func selectorTargets(target string) map[string]bool {
	switch target {
	case "pod":
		return map[string]bool{"engine": true, "namespace": true, "rootful": true}
	case "vm":
		return map[string]bool{"backend": true}
	default:
		return nil
	}
}

// selectorTargetFor resolves the substrate a deploy's selectors are judged
// against. A `vm:` ADDRESS prefix is a target hint for a REF-BASED deploy, where
// ClassifyNodeTarget has no node and falls back to "pod" — the resolved VM
// entity is the stronger signal, and is how the substrate will route.
func selectorTargetFor(target, vmEntity string) string {
	if vmEntity != "" {
		return "vm"
	}
	return target
}

// applySelectorFlags validates f and writes the values onto a COPY of node
// (flag > authored deploy value). A nil node with at least one flag set
// synthesizes a minimal {target} node, so a ref-based deploy still carries its
// selectors to the substrate. With no flags the node is returned untouched.
func applySelectorFlags(node *spec.DeployNode, target string, f selectorFlags, candyEngine spec.EngineName) (*spec.DeployNode, error) {
	if !f.any() {
		return node, nil
	}
	allowed := selectorTargets(target)
	if f.Engine != "" && !allowed["engine"] {
		return nil, fmt.Errorf("--engine applies to a pod deploy; target %q does not select an engine", target)
	}
	if f.Backend != "" && !allowed["backend"] {
		return nil, fmt.Errorf("--backend applies to a vm deploy; target %q does not select a VM backend", target)
	}
	if f.Namespace != "" && !allowed["namespace"] {
		return nil, fmt.Errorf("--namespace applies to a pod deploy; target %q does not select a containerd namespace", target)
	}
	if f.Rootful != "" && !allowed["rootful"] {
		return nil, fmt.Errorf("--rootful applies to a pod deploy; target %q does not select a rootful mode", target)
	}

	out := spec.DeployNode{Target: target}
	if node != nil {
		out = *node
	}
	if f.Engine != "" {
		engine, err := resolveEngineFlag(f.Engine, out.Engine, candyEngine)
		if err != nil {
			return nil, err
		}
		out.Engine = engine
	}
	if f.Backend != "" {
		backend, err := resolveBackendFlag(f.Backend, out.Backend)
		if err != nil {
			return nil, err
		}
		out.Backend = backend
	}
	if f.Rootful != "" {
		if !slices.Contains(spec.RootfulModes, f.Rootful) {
			return nil, fmt.Errorf("--rootful %q is not one of %s", f.Rootful, strings.Join(spec.RootfulModes, ", "))
		}
		out.Rootful = spec.RootfulMode(f.Rootful)
	}
	if f.Namespace != "" {
		out.Namespace = f.Namespace
	}
	return &out, nil
}

// resolveEngineFlag routes the flag through the shared resolver so BOTH the
// vocabulary check and the candy constraint are the resolver's. `auto` asks for
// detection at this level; it is passed through without probing here (the
// substrate resolves it at run time), so it needs no vocabulary membership.
func resolveEngineFlag(flag string, deploy spec.EngineName, candy spec.EngineName) (spec.EngineName, error) {
	if flag == autoSelector {
		return spec.EngineName(flag), nil
	}
	engine, err := deploykit.ResolvePodEngine(flag, deploykit.Selection{Engine: deploy, CandyEngine: candy}, nil)
	if err != nil {
		return "", fmt.Errorf("--engine %q: %w", flag, err)
	}
	return engine, nil
}

// resolveBackendFlag routes the flag through the shared VM-backend resolver.
// `auto` is a selector outside spec.VmBackends and is passed through.
func resolveBackendFlag(flag string, deploy spec.VmBackend) (spec.VmBackend, error) {
	if flag == autoSelector {
		return spec.VmBackend(flag), nil
	}
	backend, err := deploykit.ResolveVmBackend(flag, deploykit.Selection{Backend: deploy}, nil)
	if err != nil {
		return "", fmt.Errorf("--backend %q: %w", flag, err)
	}
	return backend, nil
}

// applySelectorsToEntry writes f onto one deploy entry, reporting whether
// anything changed. It never clears a field for an absent flag (the CLI only
// overrides what was passed).
func applySelectorsToEntry(entry *spec.DeployNode, f selectorFlags) bool {
	changed := false
	if f.Engine != "" && entry.Engine != spec.EngineName(f.Engine) {
		entry.Engine = spec.EngineName(f.Engine)
		changed = true
	}
	if f.Backend != "" && entry.Backend != spec.VmBackend(f.Backend) {
		entry.Backend = spec.VmBackend(f.Backend)
		changed = true
	}
	if f.Namespace != "" && entry.Namespace != f.Namespace {
		entry.Namespace = f.Namespace
		changed = true
	}
	if f.Rootful != "" && entry.Rootful != spec.RootfulMode(f.Rootful) {
		entry.Rootful = spec.RootfulMode(f.Rootful)
		changed = true
	}
	return changed
}

// applyDeploySelectors patches the TOP-LEVEL entry named key. A nested (dotted)
// name lives inside its parent's entry and is never a top-level one; an entry
// the substrate has not created yet is left alone (nothing is fabricated).
// Returns whether the config changed.
func applyDeploySelectors(dc *deploykit.DeployConfig, key string, f selectorFlags) bool {
	if dc == nil || dc.Deploy == nil || key == "" || strings.Contains(key, ".") || !f.any() {
		return false
	}
	entry, ok := dc.Deploy[key]
	if !ok {
		return false
	}
	if !applySelectorsToEntry(&entry, f) {
		return false
	}
	dc.Deploy[key] = entry
	return true
}

// persistDeploySelectors runs applyDeploySelectors through the shared locked
// read-modify-write cycle, so a concurrent overlay writer is merged onto rather
// than clobbered. It is called AFTER a successful dispatch, once the substrate
// has created the entry this patches.
func persistDeploySelectors(name string, f selectorFlags) error {
	if !f.any() {
		return nil
	}
	key, _ := spec.SplitVmAddress(name)
	if key == "" {
		return nil
	}
	return mutateDeployConfig(func(dc *deploykit.DeployConfig) (bool, error) {
		return applyDeploySelectors(dc, key, f), nil
	})
}
