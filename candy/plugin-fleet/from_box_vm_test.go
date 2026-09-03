package fleet

import (
	"os"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// from_box_vm_test.go — the VM path of `charly fleet from-box vm:<ref>`.

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
	if err := writeVmBoxEntity("my-vm", "/tmp/disk.qcow2", "arch", "bios"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("charly.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kind: imported") || !strings.Contains(string(data), "disk_path: /tmp/disk.qcow2") {
		t.Fatalf("entity not written; got: %s", data)
	}
	// Idempotence guard: a second write of the same name must error.
	if err := writeVmBoxEntity("my-vm", "/tmp/disk.qcow2", "arch", "bios"); err == nil {
		t.Fatal("a duplicate entity name must error")
	}
}
