package deploy

// deploy_node_security_test.go — the persist-path witness for opencharly/plugin-fleet#42:
// `charly deploy add` must CARRY a deploy node's authored `security:` block into the per-host
// entry, merging, never replacing, so `charly config`'s existing MergeDeployOntoMetadata →
// quadlet path emits `AddCapability=`/`SecurityOpt=`/`CgroupNS=`.
//
// These tests drive the REAL mutation (applyAuthoredSecurity) through the REAL locked cycle
// (deploykit.MutateDeployConfig) and the REAL sdk save/read — the same ones the walk
// (walk.go dispatchOne → persistDeployNodeSecurity) uses. Only the reader/marshal are the
// test-scoped node-form ones (bedTestLoadDeployConfig / bedTestMarshalNode), because the
// production reader is the loader-backed reverse-channel read a standalone plugin test cannot
// construct — the identical precedent bed_persist_writer_test.go establishes.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

func seedPerHostOverlay(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	return path
}

func runAuthoredSecurityCycle(t *testing.T, key string, node *spec.DeployNode) {
	t.Helper()
	save := func(dc *deploykit.DeployConfig) error {
		return deploykit.SaveDeployConfig(dc, bedTestMarshalNode, bedTestLoadDeployConfig, context.Background())
	}
	if _, err := deploykit.MutateDeployConfig(bedTestLoadDeployConfig, save, func(dc *deploykit.DeployConfig) (bool, error) {
		return applyAuthoredSecurity(dc, key, node), nil
	}, context.Background()); err != nil {
		t.Fatalf("persist cycle: %v", err)
	}
}

// TestDeployAddSecurities_CarriesAuthoredBlockIntoPersistedNode is the #42 reproduction at the
// persist boundary: the entry already carries the auto-detected devices a previous `charly config`
// recorded, and the tree declares cap_add + cgroupns + security_opt. Every authored field must come
// back intact, the detected devices must be present, and /dev/kvm — declared by the tree AND
// detected — must appear ONCE. Without the fix the authored block is absent entirely.
func TestDeployAddSecurity_CarriesAuthoredBlockIntoPersistedNode(t *testing.T) {
	seedPerHostOverlay(t, `check-pod-k3s:
    pod:
        image: pod-k3s
        security:
            devices:
                - /dev/kvm
                - /dev/fuse
`)

	node := &spec.DeployNode{
		Target: "pod",
		Image:  "pod-k3s",
		Security: &spec.Security{
			CapAdd:      []string{"NET_ADMIN"},
			CgroupNS:    "host",
			SecurityOpt: []string{"label=disable"},
			Devices:     []string{"/dev/kvm"},
		},
	}
	runAuthoredSecurityCycle(t, "check-pod-k3s", node)

	dc, err := bedTestLoadDeployConfig()
	if err != nil {
		t.Fatalf("reload after persist: %v", err)
	}
	entry, ok := dc.Deploy["check-pod-k3s"]
	if !ok {
		t.Fatal("check-pod-k3s entry vanished")
	}
	if entry.Security == nil {
		t.Fatal("authored security block was NOT persisted (the #42 drop)")
	}
	sec := *entry.Security
	if !reflect.DeepEqual(sec.CapAdd, []string{"NET_ADMIN"}) {
		t.Errorf("cap_add = %v, want [NET_ADMIN] — an authored capability was dropped", sec.CapAdd)
	}
	if sec.CgroupNS != "host" {
		t.Errorf("cgroupns = %q, want \"host\"", sec.CgroupNS)
	}
	if !reflect.DeepEqual(sec.SecurityOpt, []string{"label=disable"}) {
		t.Errorf("security_opt = %v, want [label=disable]", sec.SecurityOpt)
	}
	// The detected devices survived AND the tree-declared /dev/kvm was not duplicated.
	want := []string{"/dev/kvm", "/dev/fuse"}
	if !reflect.DeepEqual(sec.Devices, want) {
		t.Errorf("devices = %v, want %v (detected devices unioned, duplicates removed)", sec.Devices, want)
	}
	if n := bytesCount(sec.Devices, "/dev/kvm"); n != 1 {
		t.Errorf("/dev/kvm appears %d times, want exactly once", n)
	}
}

// TestDeployAddSecurity_NoAuthoredSecurityLeavesDetectionPathUntouched pins requirement 4: a
// source node with NO `security:` at all must leave the persisted entry exactly as the
// detection-only path wrote it — the mutation reports "no change", so no rewrite happens at all.
func TestDeployAddSecurity_NoAuthoredSecurityLeavesDetectionPathUntouched(t *testing.T) {
	seeded := `check-pod-k3s:
    pod:
        image: pod-k3s
        security:
            devices:
                - /dev/kvm
`
	path := seedPerHostOverlay(t, seeded)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}

	node := &spec.DeployNode{Target: "pod", Image: "pod-k3s"} // no Security
	runAuthoredSecurityCycle(t, "check-pod-k3s", node)

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after cycle: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a node with no authored security rewrote the overlay\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	dc, err := bedTestLoadDeployConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	sec := dc.Deploy["check-pod-k3s"].Security
	if sec == nil || !reflect.DeepEqual(sec.Devices, []string{"/dev/kvm"}) {
		t.Errorf("detected devices not preserved: got %+v", sec)
	}
}

// TestDeployAddSecurity_SeedsFreshPodEntry pins the fresh-project sequence `deploy add` then
// `charly config`: with no per-host entry yet, an image-bearing POD node's authored security must
// still be seeded (as the {pod: {image: …}} shape config resolves), while a non-pod node with no
// entry is never fabricated (its identifying cross-ref would be missing).
func TestDeployAddSecurity_SeedsFreshPodEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	pod := &spec.DeployNode{
		Target:   "pod",
		Image:    "pod-k3s",
		Security: &spec.Security{CapAdd: []string{"NET_ADMIN"}},
	}
	runAuthoredSecurityCycle(t, "fresh-pod", pod)

	vm := &spec.DeployNode{
		Target:   "vm",
		Security: &spec.Security{CapAdd: []string{"NET_ADMIN"}},
	}
	runAuthoredSecurityCycle(t, "fresh-vm", vm)

	dc, err := bedTestLoadDeployConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entry, ok := dc.Deploy["fresh-pod"]
	if !ok {
		t.Fatal("fresh pod entry not seeded — `deploy add` + `charly config` would drop the authored block")
	}
	if entry.Image != "pod-k3s" || entry.Target != "pod" {
		t.Errorf("seeded entry = {target:%q image:%q}, want {target:pod image:pod-k3s}", entry.Target, entry.Image)
	}
	if entry.Security == nil || !reflect.DeepEqual(entry.Security.CapAdd, []string{"NET_ADMIN"}) {
		t.Errorf("seeded entry lost the authored cap_add: %+v", entry.Security)
	}
	if _, ok := dc.Deploy["fresh-vm"]; ok {
		t.Error("a non-pod node with no existing entry must not be fabricated into the overlay")
	}
}

func bytesCount(list []string, want string) int {
	n := 0
	for _, v := range list {
		if v == want {
			n++
		}
	}
	return n
}
