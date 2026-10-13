package deploy

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// load_executor.go — the K1-LOADER RELOCATION WITNESS (Unit D): command:deploy drives the whole
// project loader ITSELF, plugin-side, proving a genuine out-of-module plugin can run it without
// importing charly core. The executor-backed loaderkit.LoaderExecutor + the LoadUnified call it
// used to carry inline (a private copy shared with three other candies) now live in the shared
// loaderkit.LoadUnifiedViaExecutor helper (#55 Cone A Unit 2, R3) — it dispatches each
// registry-coupled loader step over sdk.Executor.HostBuild to charly's "loader-*" host legs, runs
// the PURE relocated LOAD-half seams plugin-side, and self-serves the android/preemptible capability
// validators over InvokeProvider(kind, OpResolve). resolveTreeViaLoader below just calls that helper.

// resolveTreeViaLoader is the witness entry: it drives loaderkit.LoadUnifiedViaExecutor PLUGIN-SIDE
// to resolve the `charly deploy add` deploy tree, replacing the former host-resident merged-tree read. The
// host "deploy-plugins-connect" preamble connects the deployment's out-of-tree plugin candies
// (registry-coupled) and hands back the project dir; the project load + local-overlay merge
// (deploykit.LoadDeployConfig / MergeDeployConfigs) are pure sdk. It returns the merged tree and
// whether the root's stamped descent is the "ssh" venue (a vm root → node-only dispatch) — read
// directly from node.Descent, which the loader stamps (byte-identical to the host's former
// registry-backed nodeTraits check for a stamped node).
func resolveTreeViaLoader(path string, addCandy []string) (map[string]spec.DeployNode, bool, string, error) {
	if cmdExec == nil {
		return nil, false, "", fmt.Errorf("deploy deploy-plugins-connect: no host reverse channel (command not compiled-in?)")
	}
	var pre spec.DeployPluginsConnectReply
	if err := hostDeploySeamJSON("deploy-plugins-connect", spec.DeployPluginsConnectRequest{
		Path:     path,
		AddCandy: addCandy,
	}, &pre); err != nil {
		return nil, false, "", err
	}

	var projectDC *deploykit.DeployConfig
	if uf, ok, err := loaderkit.LoadUnifiedViaExecutor(cmdCtx, cmdExec, pre.Dir); err != nil {
		return nil, false, "", err
	} else if ok && uf != nil {
		projectDC = deploykit.ProjectDeployConfig(uf)
	}

	localDC, _ := deploykit.LoadDeployConfig(cmdCtx)
	merged := deploykit.MergeDeployConfigs(projectDC, localDC)
	if merged == nil || merged.Deploy == nil {
		return nil, false, pre.Dir, nil
	}

	rootVenueSSH := false
	if node, _, e := deploykit.ResolveNodePath(merged.Deploy, path); e == nil && node != nil && node.Descent != nil {
		rootVenueSSH = node.Descent.Venue == "ssh"
	}
	return merged.Deploy, rootVenueSSH, pre.Dir, nil
}

// fetchExternalSubstrates returns the loader-threaded ExternalDeploySubstrates DATA snapshot (the
// EXACT set of substrate words for which the host's isExternalDeploySubstrate returns true — K1-
// LOADER RELOCATION) AND the reason it could not be read when it could not.
//
// The reason is returned instead of being folded into an empty set (opencharly/plugin-fleet#10):
// indexing a nil map yields false, so a degraded snapshot silently routed a non-image-bearing
// target (vm/android/…) into the box/candy resolver, where a same-named candy could answer for a
// VM. compileNodePlans now branches on the TARGET CLASS and reports this error for the targets
// that need a substrate; an image-bearing target (pod/kubernetes) never consults the set at all.
func fetchExternalSubstrates() (map[string]bool, error) {
	if cmdExec == nil {
		return nil, errors.New("no exec seam is available, so the loader-threaded substrate set cannot be read")
	}
	out, err := cmdExec.HostBuild(cmdCtx, "loader-threaded", nil)
	if err != nil {
		return nil, fmt.Errorf("loader-threaded HostBuild: %w", err)
	}
	var t spec.Threaded
	if err := json.Unmarshal(out, &t); err != nil {
		return nil, fmt.Errorf("decode the loader-threaded snapshot: %w", err)
	}
	return t.ExternalDeploySubstrates, nil
}

// fetchLoaderPrimaries returns the loader-threaded Primaries DATA snapshot (plugin-verb WORD →
// scalar-sugar primary field — the SAME registry-derived map candy/plugin-build's resolve fills
// spec.ResolvedProject.Primaries from, and the host's deleted deploy-config-save leg fed
// deploykit.MarshalDeployNode via loaderThreaded().Primaries). The node-form deploy-state WRITE
// (saveDeployConfig / persistDeployState) reads it here so command:deploy resugars each plan step
// PLUGIN-SIDE — no host round-trip through the deleted deploy-config-save seam (#55 K4). A
// HostBuild failure degrades to an empty map (a plan with no plugin-verb sugar marshals identically).
func fetchLoaderPrimaries() map[string]string {
	if cmdExec == nil {
		return nil
	}
	out, err := cmdExec.HostBuild(cmdCtx, "loader-threaded", nil)
	if err != nil {
		return nil
	}
	var t spec.Threaded
	if err := json.Unmarshal(out, &t); err != nil {
		return nil
	}
	return t.Primaries
}
