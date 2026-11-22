package deploy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// builder_preresolve.go — the DEPLOY-TIME builder-IR pre-pass (FLOOR-SLIM-proper Unit-8, spike-
// proven): the host's OWN builder_preresolve.go used to run this before Invoking command:deploy's
// OpCompile, threading the result on hostCtx.BuilderContext (part of the marshalled HostContext).
// Since command:deploy already re-hydrates the resolved-project envelope (rp.CandyModels +
// rp.ExternalizedBuilders, both wire-native — R3, no duplicate data source) and already holds a
// live *sdk.Executor for its own OpCompile Invoke, it can run the SAME pre-pass itself via
// exec.InvokeProvider(ClassBuilder, word, OpCollectContext/OpReverse, …) — F10's generic
// plugin↔plugin dispatch, spike-proven live (a real out-of-process ClassBuilder plugin round-trips
// correctly through this exact call shape).
//
// K-wave 2 cone R1: charly/builder_preresolve.go — the host's remaining CONNECT step
// (ensureBuildersConnected) — is DELETED. Its "genuinely host-only" claim did not hold: its two-pass
// ScanAllCandyWithConfigOpts + loadProjectPlugins body was a second copy of the host's own generic
// connectPluginByWordRef, which exec.InvokeProvider ALREADY falls back to. So the lazy-connect
// fallback is now the ONE connect path for every builder dispatch, build-time and deploy-time alike.
// That also closes a latent gap on THIS path: the pre-pass below passed an EMPTY ExtraRef, so it only
// ever reached Pass-1 (the deploy project's own candy closure) — a box/<distro> submodule deploy that
// triggers a builder purely by DETECTION but vendors the plugin candy nowhere had no Pass-2 to fall
// back to. Both Invokes now carry spec.ExternalBuilderPluginRef(word), the same canonical ref the
// build-time resolve uses (R3: one connect rule, not one per dispatch site).

// builderInvokeOpts carries the builder's canonical plugin-candy ref so the host's InvokeProvider
// falls back to connectPluginByWordRef's Pass-2 fetch when the deploy project's own candy closure
// vendors the plugin nowhere. Shared by both Invokes below (R3) and identical to what the build-time
// resolve passes (sdk/deploykit renderSeamCaller.resolveBuilderStage).
func builderInvokeOpts(word string) sdk.InvokeProviderOpts {
	if ref, ok := spec.ExternalBuilderPluginRef(word); ok {
		return sdk.InvokeProviderOpts{ExtraRef: ref}
	}
	return sdk.InvokeProviderOpts{}
}

// preresolveBuilderContexts detects which builders the image needs (deploykit.DetectExternalizedBuilders
// — the SAME NeedsBuilder rule the render and the apply compile use, opencharly/sdk#608; a needed
// builder whose plugin cannot be reached fails at its Invoke, never silently) and, for each
// (candy, builder) pair, Invokes the builder
// plugin's OpCollectContext then OpReverse via exec.InvokeProvider, returning the map
// hostCtx.BuilderContext carries into deploykit.BuildDeployPlan. Mirrors the deleted
// charly/builder_preresolve.go's preresolveBuilderContexts byte-for-byte in outcome, only the
// dispatch mechanism differs (InvokeProvider instead of a core-private providerRegistry handle).
func preresolveBuilderContexts(ctx context.Context, exec *sdk.Executor, order []string, candyModels map[string]spec.CandyReader, img *buildkit.ResolvedBox) (map[string]deploykit.BuilderPreresolved, error) {
	needed := deploykit.DetectExternalizedBuilders(order, candyModels, img)
	if len(needed) == 0 {
		return nil, nil
	}
	var out map[string]deploykit.BuilderPreresolved
	for _, candyName := range order {
		layer := candyModels[candyName]
		if layer == nil {
			continue
		}
		for _, bName := range needed {
			bDef := img.BuilderConfig.Builder[bName] // needed ⇒ the vocabulary defines it
			if !deploykit.NeedsBuilder(layer, img, bName, bDef) {
				continue
			}
			collected, err := invokeBuilderCollect(ctx, exec, bName, layer, bDef, img)
			if err != nil {
				return nil, fmt.Errorf("candy %q: builder %q collect-context: %w", candyName, bName, err)
			}
			reverse, err := invokeBuilderReverse(ctx, exec, bName, layer.GetName(), collected.Context)
			if err != nil {
				return nil, fmt.Errorf("candy %q: builder %q reverse: %w", candyName, bName, err)
			}
			if out == nil {
				out = map[string]deploykit.BuilderPreresolved{}
			}
			// The package output is the builder plugin's own declaration (its collect reply), which
			// the compile carries onto the BuilderStep (opencharly/sdk#608).
			out[deploykit.BuilderCtxKey(layer.GetName(), bName)] = deploykit.BuilderPreresolved{Context: collected.Context, Reverse: reverse, Package: collected.Package}
		}
	}
	return out, nil
}

// invokeBuilderCollect InvokeProviders the builder plugin's OpCollectContext, returning its reply:
// the builder-specific stage-context keys and the package output it declares — the plugin-side
// twin of the deleted
// charly/builder_preresolve.go's invokeBuilderCollect. The input comes from the ONE constructor
// (deploykit.BuilderCollectInputFor) the golden-compile tool uses too.
func invokeBuilderCollect(ctx context.Context, exec *sdk.Executor, word string, layer spec.CandyReader, bDef *spec.Builder, img *buildkit.ResolvedBox) (spec.BuilderCollectReply, error) {
	var reply spec.BuilderCollectReply
	in, err := deploykit.BuilderCollectInputFor(layer, img, word, bDef)
	if err != nil {
		return reply, err
	}
	params, err := json.Marshal(in)
	if err != nil {
		return reply, fmt.Errorf("marshal collect-context input: %w", err)
	}
	resJSON, err := exec.InvokeProvider(ctx, "builder", word, sdk.OpCollectContext, params, nil, builderInvokeOpts(word))
	if err != nil {
		return reply, err
	}
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return reply, fmt.Errorf("decode collect-context reply: %w", err)
		}
	}
	return reply, nil
}

// invokeBuilderReverse InvokeProviders the builder plugin's OpReverse with the resolved stage
// context, returning the teardown ops stashed onto BuilderStep.PreResolvedReverse.
func invokeBuilderReverse(ctx context.Context, exec *sdk.Executor, word, candy string, stageContext map[string]any) ([]spec.ReverseOp, error) {
	params, err := json.Marshal(spec.BuilderReverseInput{Candy: candy, Builder: word, Context: stageContext})
	if err != nil {
		return nil, fmt.Errorf("marshal reverse input: %w", err)
	}
	resJSON, err := exec.InvokeProvider(ctx, "builder", word, sdk.OpReverse, params, nil, builderInvokeOpts(word))
	if err != nil {
		return nil, err
	}
	var reply spec.BuilderReverseReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return nil, fmt.Errorf("decode reverse reply: %w", err)
		}
	}
	return reply.ReverseOps, nil
}
