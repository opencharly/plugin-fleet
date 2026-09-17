package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// from_box_vm_test.go — the VM path of `charly deploy from-box vm:<ref>`.

func TestParseVmBoxRef(t *testing.T) {
	cases := []struct {
		ref, name string
		wantImage string
		wantName  string
		wantErr   bool
	}{
		{"vm:localhost/charly-base:2026.246.0640", "", "localhost/charly-base:2026.246.0640", "charly-base", false},
		{"vm:localhost/charly-base:2026.246.0640", "my-vm", "localhost/charly-base:2026.246.0640", "my-vm", false},
		{"vm:", "", "", "", true},
		{"", "", "", "", true},
	}
	for _, c := range cases {
		img, name, err := parseVmBoxRef(c.ref, c.name)
		if c.wantErr {
			if err == nil {
				t.Fatalf("parseVmBoxRef(%q) must error", c.ref)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseVmBoxRef(%q): %v", c.ref, err)
		}
		if img != c.wantImage || name != c.wantName {
			t.Fatalf("parseVmBoxRef(%q, %q) = (%q, %q), want (%q, %q)", c.ref, c.name, img, name, c.wantImage, c.wantName)
		}
	}
}

func TestVmBoxMetadataToEntity(t *testing.T) {
	meta := &spec.VmBoxMetadata{
		Distro:   "arch",
		BaseUser: "arch",
		SSHUser:  "arch",
		Firmware: "bios",
	}
	entity := vmBoxMetadataToEntity(meta)
	src, ok := entity["source"].(map[string]any)
	if !ok {
		t.Fatalf("entity must carry a source map; got %+v", entity)
	}
	if src["kind"] != "imported" || src["disk_format"] != "qcow2" {
		t.Fatalf("source must be imported/qcow2; got %+v", src)
	}
	ssh, ok := entity["ssh"].(map[string]any)
	if !ok || ssh["user"] != "arch" {
		t.Fatalf("entity must carry the ssh user; got %+v", entity)
	}
	if entity["firmware"] != "bios" {
		t.Fatalf("entity must carry the firmware; got %+v", entity)
	}

	// No ssh user in metadata → no ssh block, no firmware → no firmware key.
	meta2 := &spec.VmBoxMetadata{Distro: "debian"}
	entity2 := vmBoxMetadataToEntity(meta2)
	if _, ok := entity2["ssh"]; ok {
		t.Fatalf("no ssh user in metadata must mean no ssh block; got %+v", entity2)
	}
	if _, ok := entity2["firmware"]; ok {
		t.Fatalf("no firmware in metadata must mean no firmware key; got %+v", entity2)
	}
}

func TestWriteVmBoxEntity(t *testing.T) {
	dir := t.TempDir()
	// writeVmBoxEntity uses os.Getwd — chdir into the temp dir for the test.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()
	if err := os.WriteFile("charly.yml", []byte("version: 2026.246.0000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entity := vmBoxMetadataToEntity(&spec.VmBoxMetadata{SSHUser: "arch", Firmware: "bios"})
	entity["source"].(map[string]any)["disk_path"] = "/tmp/disk.qcow2"
	if err := writeVmBoxEntity("my-vm", entity); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("charly.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kind: imported") || !strings.Contains(string(data), "disk_path: /tmp/disk.qcow2") {
		t.Fatalf("entity not written; got: %s", data)
	}
	// NAME-FIRST SHAPE (the schema-compaction contract): the entity lands as
	// `<name>: { vm: { … } }` — never a legacy top-level `vm:` map, which the loader
	// HARD-REJECTS with "no kind discriminator".
	var doc map[string]yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("written charly.yml does not parse: %v", err)
	}
	if _, bad := doc["vm"]; bad {
		t.Fatalf("writer emitted a legacy top-level `vm:` map — the loader rejects it\n%s", data)
	}
	node, ok := doc["my-vm"]
	if !ok {
		t.Fatalf("no name-first `my-vm` node in the written config\n%s", data)
	}
	var body map[string]yaml.Node
	if err := node.Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["vm"]; !ok {
		t.Fatalf("`my-vm` node carries no `vm:` kind key\n%s", data)
	}
	// Idempotence guard: a second write of the same name must error.
	if err := writeVmBoxEntity("my-vm", entity); err == nil {
		t.Fatal("a duplicate entity name must error")
	}
}

// TestWriteVmBoxEntity_LoadsWithRealCharly is the end-to-end proof the fix's central claim
// needs: the emitted charly.yml must actually LOAD. The structural test above only checks the
// YAML shape; this runs the REAL charly loader (`charly box validate`) against the file the
// writer produced, so "the loader accepts it" is proven, not asserted. A pre-fix writer (a
// top-level `vm:` map) makes this fail with "no kind discriminator".
//
// The test is skipped when no charly binary is available (CI's candy job builds one; a bare
// `go test` on a machine without charly skips rather than fails). Set CHARLY_BIN to point at
// one explicitly.
func TestWriteVmBoxEntity_LoadsWithRealCharly(t *testing.T) {
	charly := os.Getenv("CHARLY_BIN")
	if charly == "" {
		if p, err := exec.LookPath("charly"); err == nil {
			charly = p
		}
	}
	if charly == "" {
		t.Skip("no charly binary (set CHARLY_BIN or put charly on PATH) — loader acceptance unproven here")
	}

	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()
	if err := os.WriteFile("charly.yml", []byte("version: 2026.249.2125\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entity := vmBoxMetadataToEntity(&spec.VmBoxMetadata{SSHUser: "arch", Firmware: "bios"})
	entity["source"].(map[string]any)["disk_path"] = "/tmp/disk.qcow2"
	if err := writeVmBoxEntity("my-vm", entity); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(charly, "box", "validate", "-C", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("the emitted charly.yml did not LOAD with the real charly loader (%v):\n%s", err, out)
	}
	if !strings.Contains(string(out), "box validate: OK") {
		t.Fatalf("charly box validate did not report OK:\n%s", out)
	}
}

// TestWriteVmBoxEntity_RealDeployFromBoxLive runs the REAL `charly deploy from-box vm:<ref>`
// end-to-end against a VM-box image already in local storage, then loads the emitted charly.yml:
// the complete gateway path the fix touches. Requires a VM-box image (an `ai.opencharly.vm.box`
// label) in local storage + a charly binary; skips otherwise.
func TestWriteVmBoxEntity_RealDeployFromBoxLive(t *testing.T) {
	charly := os.Getenv("CHARLY_BIN")
	if charly == "" {
		if p, err := exec.LookPath("charly"); err == nil {
			charly = p
		}
	}
	ref := os.Getenv("FROM_BOX_VM_IMAGE")
	if charly == "" || ref == "" {
		t.Skip("set CHARLY_BIN and FROM_BOX_VM_IMAGE=<a local VM-box image ref> to run the live deploy from-box")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte("version: 2026.249.2125\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(charly, "--dir", dir, "deploy", "from-box", "vm:"+ref).CombinedOutput()
	if err != nil {
		t.Fatalf("charly deploy from-box vm:%s failed (%v):\n%s", ref, err, out)
	}
	load, err := exec.Command(charly, "box", "validate", "-C", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("the charly.yml emitted by deploy from-box did not LOAD (%v):\n%s\n--- deploy output ---\n%s", err, load, out)
	}
	if !strings.Contains(string(load), "box validate: OK") {
		t.Fatalf("emitted charly.yml did not validate OK:\n%s", load)
	}
}
