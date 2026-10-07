package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// schemaHeader is the header a fixture charly.yml carries to LOAD under the
// real charly loader. The schema-versioning removal cutover deleted the
// `version:` stamp entirely (and the SDK's kit.LatestSchemaVersion helper), so
// a config now carries NO schema header — this returns the empty prefix the
// fixtures prepend.
func schemaHeader() string {
	return ""
}

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
	if err := os.WriteFile("charly.yml", []byte("{}\n"), 0o644); err != nil {
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

// selectCharlyBinary is the PURE half of charlyForTree: it decides which charly binary a live
// loader test may drive, and it never consults the host implicitly.
//
// The silent `exec.LookPath("charly")` fallback it replaces resolved ANY binary on PATH, whatever
// its vintage. On a host with the distro package installed that is a different BUILD of charly than
// the tree under test, and the resulting skew was reported as a CODE defect in `writeVmBoxEntity` —
// a misdiagnosis that sent the reader after the writer while the real cause sat in the host's
// package (opencharly/plugin-fleet#29). The test's subject is THIS tree's writer and THIS tree's
// loader, so the binary must be named explicitly:
//
//   - CHARLY_BIN set   → that binary, verbatim. A stale choice is the operator's to correct, and
//     charlyForTree then FAILS LOUDLY naming the skew, never the writer.
//   - CHARLY_BIN unset → no binary, with a skip reason that NAMES the ambient binary it refused
//     when one exists (live-or-skip: an uncrossed boundary says so out loud).
func selectCharlyBinary(envBin, ambientPath string) (bin, skipReason string) {
	if envBin != "" {
		return envBin, ""
	}
	if ambientPath != "" {
		return "", fmt.Sprintf("CHARLY_BIN is not set — refusing the charly on PATH (%s): a binary not "+
			"built from THIS tree can be another version, and using one is how a host skew was "+
			"reported as a code defect in writeVmBoxEntity (opencharly/plugin-fleet#29). Set CHARLY_BIN "+
			"to the binary this tree builds (scripts/bootstrap-charly.sh).", ambientPath)
	}
	return "", "CHARLY_BIN is not set and no charly is on PATH — loader acceptance unproven here " +
		"(set CHARLY_BIN to the binary this tree builds)"
}

// vmBoxControlConfig is a HAND-AUTHORED config in the shape this tree's writer must produce: one
// node carrying a `vm:` kind key whose `source:` is the imported-disk source `vmBoxMetadataToEntity`
// builds. It is the POSITIVE CONTROL for the live loader assertions.
//
// Why a control at all, and why not an empty file (the former probe): an EMPTY config is loadable by
// ANY vintage of charly, so it proved nothing and let a stale binary's skew fall through to the
// writer's own assertion, where it read as "the emitted charly.yml did not LOAD" — a code defect that
// was really a package version (opencharly/plugin-fleet#29). A control of the ASSERTED SHAPE answers
// the only question that separates the two: can this binary load this ear's `vm:` entity at all?
//
// It cannot mask a real regression, and that is the property the old docstring claimed for its
// empty config and did not have: the control is hand-authored and never passes through
// writeVmBoxEntity, so a writer that emits a malformed node still fails the real assertion below.
const vmBoxControlConfig = `control-vm:
    vm:
        source:
            kind: imported
            libvirt_name: charly-arch
            disk_path: /tmp/control.qcow2
            disk_format: qcow2
        ssh:
            user: arch
        firmware: bios
`

// charlyVersionOf reports a charly binary's version line for a failure message (best effort — a
// binary that cannot even answer `version` is itself the skew being reported).
func charlyVersionOf(charly string) string {
	out, err := exec.Command(charly, "version").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("`%s version` failed: %v", charly, err)
	}
	return strings.TrimSpace(string(out))
}

// charlyForTree resolves the charly binary a live loader test may drive, and REFUSES to guess.
// CHARLY_BIN unset is a visible SKIP; a named binary that cannot load a canonical config of this
// tree's own `vm:` shape is a VERSION SKEW and FAILS LOUDLY, naming the binary and its version.
func charlyForTree(t *testing.T) string {
	t.Helper()
	ambient, _ := exec.LookPath("charly")
	charly, skip := selectCharlyBinary(os.Getenv("CHARLY_BIN"), ambient)
	if charly == "" {
		t.Skip(skip)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(vmBoxControlConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(charly, "box", "validate", "-C", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("CHARLY_BIN=%s cannot load a canonical `vm:` config in THIS tree's shape, so it cannot "+
			"witness anything about writeVmBoxEntity: this is a VERSION SKEW in the named binary, NOT a "+
			"code defect. Rebuild (scripts/bootstrap-charly.sh) and point CHARLY_BIN at that binary.\n"+
			"charly version: %s\n%s", charly, charlyVersionOf(charly), out)
	}
	return charly
}

// TestWriteVmBoxEntity_LoadsWithRealCharly is the end-to-end proof the fix's central claim
// needs: the emitted charly.yml must actually LOAD. The structural test above only checks the
// YAML shape; this runs the REAL charly loader (`charly box validate`) against the file the
// writer produced, so "the loader accepts it" is proven, not asserted. A pre-fix writer (a
// top-level `vm:` map) makes this fail with "no kind discriminator".
//
// It drives ONLY the binary CHARLY_BIN names (see charlyForTree), because a bare `go test` must not
// depend on whatever charly the HOST happens to have on PATH: that made this test green on a host
// with no charly and red on a host with an old packaged one, reporting the version skew as a defect
// in writeVmBoxEntity (opencharly/plugin-fleet#29). CHARLY_BIN unset is a visible SKIP; a named
// binary that cannot load a canonical config of this tree's own `vm:` shape FAILS LOUDLY, naming
// the skew rather than the writer. CI's candy job builds a binary and sets CHARLY_BIN.
func TestWriteVmBoxEntity_LoadsWithRealCharly(t *testing.T) {
	charly := charlyForTree(t)

	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()
	if err := os.WriteFile("charly.yml", []byte(schemaHeader()), 0o644); err != nil {
		t.Fatal(err)
	}
	entity := vmBoxMetadataToEntity(&spec.VmBoxMetadata{SSHUser: "arch", Firmware: "bios"})
	entity["source"].(map[string]any)["disk_path"] = "/tmp/disk.qcow2"
	if err := writeVmBoxEntity("my-vm", entity); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(charly, "box", "validate", "-C", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("the emitted charly.yml did not LOAD with the real charly loader (%v).\n"+
			"CHARLY_BIN=%s\ncharly version: %s\n"+
			"If that version is not the one THIS tree builds, the cause is a HOST VERSION SKEW, not a "+
			"defect in writeVmBoxEntity (opencharly/plugin-fleet#29):\n%s",
			err, charly, charlyVersionOf(charly), out)
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
	ref := os.Getenv("FROM_BOX_VM_IMAGE")
	if ref == "" {
		t.Skip("set FROM_BOX_VM_IMAGE=<a local VM-box image ref> to run the live deploy from-box")
	}
	charly := charlyForTree(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(schemaHeader()), 0o644); err != nil {
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
