package deploy

// deploy_node_security.go — the `charly deploy add` SECURITY CARRY (opencharly/plugin-fleet#42).
//
// A project's deploy node may author a `security:` block (`cap_add`, `privileged`, `cgroupns`,
// `security_opt`, `ipc_mode`, `shm_size`, `group_add`, `devices`, …). Before this file, `charly
// deploy add` persisted NOTHING of it: the only writers of a per-host entry's `security:` were
// `charly config` (which derives it from the IMAGE LABELS plus auto-detected devices) and the
// check-bed seed (`deploykit.PersistBedDeployOverrides`). An ordinary operator deploy therefore
// ran with a narrower boundary than its tree declared — measured on a `pod:` node whose
// `security.cap_add: [NET_ADMIN]` never reached the quadlet, so `ip link add … type dummy` failed
// with `RTNETLINK answers: Operation not permitted` (opencharly/pod-crabbox-k3s#6).
//
// The fix is the SAME shape the bed path already uses: seed the authored block into the per-host
// entry so the existing `MergeDeployOntoMetadata` → quadlet path honors it, with `charly config`
// unioning the auto-detected devices into it afterwards (`deploykit.AppendUnique`, so a device the
// tree already listed is never emitted twice).
//
// Two deliberate boundaries:
//   - MERGE, never replace. An entry may already carry state another writer produced (detected
//     devices from a previous `charly config`, a tunnel, volumes). Authored list fields are
//     UNIONED into the existing block; an authored scalar/bool overrides the existing value;
//     a field the tree does not declare keeps whatever is already there.
//   - NO fabrication outside the pod image case. A pod entry with an `image:` is the one fresh
//     shape `charly config` can adopt; any other target must already have an entry (fabricating a
//     `vm:`/`local:` entry without its identifying cross-ref would fail the next load).

import (
	"context"
	"reflect"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/deploy"
	"github.com/opencharly/spec/spec"
)

// mergeAuthoredSecurity folds an authored deploy node's security block into the per-host entry's
// existing block. List fields (cap_add / devices / security_opt / group_add / mount) are UNIONED
// with deploykit.AppendUnique, so an authored device that detection already recorded appears once;
// an authored scalar or `privileged: true` overrides the entry's value; a field the tree leaves
// unset falls through to the entry's. It mirrors the merge direction of
// deploykit.MergeDeployOntoMetadata (overlay fields win, unset fields fall through) while keeping
// the union semantics deploykit.MergeCandySecurity already established for the list fields.
func mergeAuthoredSecurity(existing, authored *spec.Security) *spec.Security {
	if authored == nil {
		return existing
	}
	out := spec.Security{}
	if existing != nil {
		out = *existing
	}
	if authored.Privileged {
		out.Privileged = true
	}
	out.CapAdd = deploykit.AppendUnique(out.CapAdd, authored.CapAdd...)
	out.Devices = deploykit.AppendUnique(out.Devices, authored.Devices...)
	out.SecurityOpt = deploykit.AppendUnique(out.SecurityOpt, authored.SecurityOpt...)
	out.GroupAdd = deploykit.AppendUnique(out.GroupAdd, authored.GroupAdd...)
	out.Mounts = deploykit.AppendUnique(out.Mounts, authored.Mounts...)
	if authored.CgroupNS != "" {
		out.CgroupNS = authored.CgroupNS
	}
	if authored.IpcMode != "" {
		out.IpcMode = authored.IpcMode
	}
	if authored.ShmSize != "" {
		out.ShmSize = authored.ShmSize
	}
	if authored.MemoryMax != "" {
		out.MemoryMax = authored.MemoryMax
	}
	if authored.MemoryHigh != "" {
		out.MemoryHigh = authored.MemoryHigh
	}
	if authored.MemorySwapMax != "" {
		out.MemorySwapMax = authored.MemorySwapMax
	}
	if authored.Cpus != "" {
		out.Cpus = authored.Cpus
	}
	return &out
}

// applyAuthoredSecurity writes the node's authored security block onto dc's entry for key, one
// locked read-modify-write cycle's mutation (deploykit.MutateDeployConfig). It reports whether the
// config changed, so a node with no `security:` (or one whose fields are already present) costs a
// read and never a rewrite — the device-detection path stays exactly as it was.
//
// An ABSENT entry is seeded only for an image-bearing pod deploy: that is the shape
// `charly config` resolves (`{pod: {image: …}}`), so `deploy add`-then-`config` on a fresh project
// carries the authored block instead of dropping it. Every other absent entry is left alone — the
// substrate creates it on its own path.
func applyAuthoredSecurity(dc *deploykit.DeployConfig, key string, node *spec.DeployNode) bool {
	if dc == nil || dc.Deploy == nil || key == "" || node == nil || node.Security == nil {
		return false
	}
	entry, ok := dc.Deploy[key]
	if !ok {
		if node.Target != "pod" || node.Image == "" {
			return false
		}
		entry = spec.DeployNode{Target: "pod", Image: node.Image}
	}
	merged := mergeAuthoredSecurity(entry.Security, node.Security)
	if entry.Security != nil && reflect.DeepEqual(*entry.Security, *merged) {
		return false
	}
	entry.Security = merged
	dc.Deploy[key] = entry
	return true
}

// persistDeployNodeSecurity seeds a deploy node's authored security block into the per-host
// charly.yml after a successful `deploy add` dispatch — the SAME position (and the same locked
// cycle) as persistDeploySelectors. It is a no-op when the tree declares no security, when the
// name is a dotted in-substrate child path (those deploy into their parent's venue, not the
// host overlay), or when the node is host-rooted / an external in-place substrate (no
// `charly config` step consumes the entry; the mirrored guards are
// deploykit.PersistBedDeployOverrides').
func persistDeployNodeSecurity(ctx context.Context, name string, node *spec.DeployNode) error {
	if node == nil || node.Security == nil {
		return nil
	}
	key, _ := spec.SplitVmAddress(name)
	if key == "" || strings.Contains(key, ".") {
		return nil
	}
	if deploy.HostRooted(node) || deploy.ExternalInPlaceVenue(node) {
		return nil
	}
	_, err := deploykit.MutateDeployConfig(loadDeployConfig, saveDeployConfig, func(dc *deploykit.DeployConfig) (bool, error) {
		return applyAuthoredSecurity(dc, key, node), nil
	}, ctx)
	return err
}
