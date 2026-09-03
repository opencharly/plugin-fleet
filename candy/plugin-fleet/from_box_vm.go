package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// from_box_vm.go — the VM path of `charly fleet from-box vm:<ref> [name]`.
//
// A SOURCE-LESS VM provisioning: reads the VM box image's ai.opencharly.vm.box
// metadata (deploykit.VmCapabilitiesFromLabels — the VM analog of the pod
// from-box), extracts the disk layer from the box image (EmitVmBox's convention:
// the disk is COPY'd as /disk.qcow2 in the scratch image), writes a kind:vm
// entity (source.kind: imported) into the project's charly.yml, and prints the
// deploy command. The actual deploy is the existing `charly fleet add vm:<name>`
// path — the box image is the source, from-box vm: turns it into a runnable VM.

// parseVmBoxRef strips the `vm:` prefix from a from-box ref and derives the
// deploy name (the image-ref basename without tag, mirroring DeriveDeploymentName).
func parseVmBoxRef(ref, name string) (imageRef, deployName string, err error) {
	imageRef = strings.TrimPrefix(strings.TrimSpace(ref), "vm:")
	if imageRef == "" {
		return "", "", fmt.Errorf("charly fleet from-box vm: a box image <ref> is required (e.g. vm:localhost/charly-base:2026.246.0640)")
	}
	deployName = name
	if deployName == "" {
		deployName = spec.DeriveDeploymentName(imageRef)
	}
	return imageRef, deployName, nil
}

// vmBoxMetadataToEntity maps the VM box metadata onto a kind:vm entity body
// (source.kind: imported — adopt the extracted disk). Pure and testable.
func vmBoxMetadataToEntity(meta *spec.VmBoxMetadata) map[string]any {
	sshUser := meta.SSHUser
	if sshUser == "" {
		sshUser = meta.BaseUser
	}
	entity := map[string]any{
		"source": map[string]any{
			"kind":         "imported",
			"libvirt_name": "charly-" + sshUser, // placeholder; the deploy domain keys the real name
			"disk_path":    "",                  // filled by the caller with the extracted disk
			"disk_format":  "qcow2",
		},
	}
	if sshUser != "" {
		entity["ssh"] = map[string]any{"user": sshUser}
	}
	if meta.Firmware != "" {
		entity["firmware"] = meta.Firmware
	}
	return entity
}

// extractVmBoxDisk extracts the /disk.qcow2 layer from a VM box image into dst.
// podman save <ref> | tar -xOf - disk.qcow2 > dst — streamed, no intermediate
// tarball (the box image's disk layer can be multi-GB).
func extractVmBoxDisk(engine, ref, dst string) error {
	save := exec.Command(engine, "save", ref)
	tar := exec.Command("tar", "-xOf", "-", "disk.qcow2")
	pipe, err := save.StdoutPipe()
	if err != nil {
		return err
	}
	tar.Stdin = pipe
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	tar.Stdout = out
	if err := tar.Start(); err != nil {
		return err
	}
	if err := save.Start(); err != nil {
		return err
	}
	if err := save.Wait(); err != nil {
		return fmt.Errorf("podman save %q: %w", ref, err)
	}
	if err := tar.Wait(); err != nil {
		return fmt.Errorf("tar extract disk from %q: %w", ref, err)
	}
	return nil
}

// writeVmBoxEntity appends a kind:vm entity to the project's charly.yml using
// the yaml.v3 Node API (comment + key-order preserving, mirroring plugin-vm's
// writeVmCloneDeclaration).
func writeVmBoxEntity(name, diskPath, sshUser, firmware string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(cwd, "charly.yml")
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("charly.yml not found in %s; run `charly box new project .` first", cwd)
		}
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	topMap := root.Content[0]
	vmMap := findOrCreateMapEntry(topMap, "vm")
	if alreadyHas(vmMap, name) {
		return fmt.Errorf("charly.yml: vm entry %q already exists; pick a different name or remove the existing entry first", name)
	}
	entry := buildVmBoxNode(diskPath, sshUser, firmware)
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
	vmMap.Content = append(vmMap.Content, keyNode, entry)
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&root); err != nil {
		return err
	}
	_ = enc.Close()
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func findOrCreateMapEntry(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key && parent.Content[i+1].Kind == yaml.MappingNode {
			return parent.Content[i+1]
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode
}

func alreadyHas(parent *yaml.Node, key string) bool {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return true
		}
	}
	return false
}

func buildVmBoxNode(diskPath, sshUser, firmware string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	sourceVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addStrPair(sourceVal, "kind", "imported")
	addStrPair(sourceVal, "libvirt_name", "charly-"+sshUser)
	addStrPair(sourceVal, "disk_path", diskPath)
	addStrPair(sourceVal, "disk_format", "qcow2")
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"}, sourceVal)
	if sshUser != "" {
		sshVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		addStrPair(sshVal, "user", sshUser)
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "ssh"}, sshVal)
	}
	if firmware != "" {
		n.Content = append(n.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "firmware"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: firmware})
	}
	return n
}

func addStrPair(parent *yaml.Node, key, val string) {
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val})
}

// runFromBoxVm is the `charly fleet from-box vm:<ref>` path.
func runFromBoxVm(c *FleetFromBoxCmd) error {
	imageRef, name, err := parseVmBoxRef(c.Ref, c.Name)
	if err != nil {
		return err
	}
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	engine := kit.EngineBinary(rt.RunEngine)

	meta, err := deploykit.VmCapabilitiesFromLabels(engine, imageRef)
	if err != nil {
		return fmt.Errorf("from-box vm %q: reading VM box metadata: %w (is this a VM box image? run `charly vm build` to emit one)", imageRef, err)
	}

	stateRoot, err := vmshared.VmStateRoot()
	if err != nil {
		return err
	}
	diskDir := filepath.Join(stateRoot, "charly-"+name)
	if err := os.MkdirAll(diskDir, 0o755); err != nil {
		return err
	}
	diskPath := filepath.Join(diskDir, "disk.qcow2")
	if err := extractVmBoxDisk(engine, imageRef, diskPath); err != nil {
		return fmt.Errorf("from-box vm %q: extracting disk from box image: %w", imageRef, err)
	}

	sshUser := meta.SSHUser
	if sshUser == "" {
		sshUser = meta.BaseUser
	}
	if err := writeVmBoxEntity(name, diskPath, sshUser, meta.Firmware); err != nil {
		return err
	}

	fmt.Printf("provisioned VM box %q as kind:vm entity %q (disk: %s)\n", imageRef, name, diskPath)
	fmt.Printf("  deploy with: charly fleet add vm:%s\n", name)
	return nil
}

var _ = json.Marshal // keep encoding/json for future metadata serialization
var _ = io.Copy      // keep io for future streamed reads
var _ = sdk.Executor{}
