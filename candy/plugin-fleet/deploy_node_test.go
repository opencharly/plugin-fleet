package deploy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"

	"github.com/opencharly/sdk/deploykit"
)

// deploy_node_test.go — relocated (in part) from charly/deploy_node_test.go (#55 decoupling,
// Batch A): 9 of 11 tests assert deploykit tree/merge functions directly, zero charly dep.
// The spec.DeployNode method-asserting unit tests (dot-in-name rejection, HasMembers) live
// beside the spec/member_tree.go implementation (zero kit dep).

func makeTree() map[string]spec.DeployNode {
	return map[string]spec.DeployNode{
		"stack": {
			Target: "container",
			Member: []spec.Member{
				{Name: "web", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{
					Target: "container",
					Member: []spec.Member{
						{Name: "db", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Target: "host"}},
					},
				}},
				{Name: "worker", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Target: "host"}},
			},
		},
		"arch": {
			Target: "vm",
			From:   "arch",
		},
	}
}

func TestWalkPreOrder_RootThenChildren(t *testing.T) {
	tree := makeTree()
	root := tree["stack"]
	var paths []string
	err := deploykit.DeployWalkPreOrder(&root, "stack", func(path string, node *spec.DeployNode) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{"stack", "stack.web", "stack.web.db", "stack.worker"}
	if !equalSlices(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestWalkPostOrder_ChildrenThenRoot(t *testing.T) {
	tree := makeTree()
	root := tree["stack"]
	var paths []string
	err := deploykit.DeployWalkPostOrder(&root, "stack", func(path string, node *spec.DeployNode) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{"stack.web.db", "stack.web", "stack.worker", "stack"}
	if !equalSlices(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestResolveNodePath_FindsNested(t *testing.T) {
	tree := makeTree()
	node, ancestors, err := deploykit.ResolveNodePath(tree, "stack.web.db")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if node.Target != "host" {
		t.Errorf("resolved target = %q, want host", node.Target)
	}
	if len(ancestors) != 2 {
		t.Errorf("ancestors len = %d, want 2", len(ancestors))
	}
}

func TestResolveNodePath_MissingSegment(t *testing.T) {
	tree := makeTree()
	_, _, err := deploykit.ResolveNodePath(tree, "stack.missing.db")
	if err == nil {
		t.Fatal("expected error for missing segment")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected error to name the missing segment, got %v", err)
	}
}

func TestResolveNodePath_EmptyPath(t *testing.T) {
	tree := makeTree()
	_, _, err := deploykit.ResolveNodePath(tree, "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestResolveNodePath_MalformedDots(t *testing.T) {
	tree := makeTree()
	for _, bad := range []string{"stack.", ".stack", "stack..web"} {
		if _, _, err := deploykit.ResolveNodePath(tree, bad); err == nil {
			t.Errorf("expected error for malformed path %q", bad)
		}
	}
}

// TestMemberTree_AuthoredOrder pins the ONE ordered member tree's iteration contract: the
// position-derived helpers (DeployLevelMembers / InSubstrateMembers) return their entries in
// AUTHORED order — the deterministic successor of the former member-map key sorts.
func TestMemberTree_AuthoredOrder(t *testing.T) {
	node := &spec.DeployNode{
		Target: "pod",
		Member: []spec.Member{
			{Name: "z", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Target: "pod"}},
			{Name: "peer-m", Position: spec.PositionDeployLevel, Node: &spec.DeployNode{Target: "pod"}},
			{Name: "a", Position: spec.PositionInSubstrate, Node: &spec.DeployNode{Target: "pod"}},
			{Name: "peer-a", Position: spec.PositionDeployLevel, Node: &spec.DeployNode{Target: "pod"}},
		},
	}
	var inSub, level []string
	for _, m := range node.InSubstrateMembers() {
		inSub = append(inSub, m.Name)
	}
	for _, m := range node.DeployLevelMembers() {
		level = append(level, m.Name)
	}
	if !equalSlices(inSub, []string{"z", "a"}) {
		t.Errorf("InSubstrateMembers = %v, want [z a] (authored order)", inSub)
	}
	if !equalSlices(level, []string{"peer-m", "peer-a"}) {
		t.Errorf("DeployLevelMembers = %v, want [peer-m peer-a] (authored order)", level)
	}
	if !node.HasMembers() || node.MemberByName("a") == nil || node.MemberByName("nope") != nil {
		t.Errorf("MemberByName/HasMembers misbehave on the ordered tree")
	}
}

// TestMergeDeployConfigsLocalCutoverFields locks in the field-level merge for
// the kind:local target fields: Local, User,
// SSHArgs. Without these, target:local deployments authored in the project
// deploy.yml lost their template ref + ssh overrides whenever the merged deploy
// tree was built via MergeDeployConfigs(projectDC, localDC), leaving the local deploy
// with an empty candy list and a silent no-op install.
//
// Fixture name `charly-cachyos` matches the deployment key (renamed from `qc`
// in the 2026-05 cross-kind name reuse cutover; the entry itself relocated to
// the opencharly/distro-cachyos submodule in the 2026-05 CachyOS migration).
func TestMergeDeployConfigsLocalCutoverFields(t *testing.T) {
	project := &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{
		"charly-cachyos": {
			Target:  "local",
			From:    "charly-cachyos",
			Host:    "local",
			User:    "alice",
			SSHArgs: []string{"-o", "ServerAliveInterval=30"},
		},
	}}
	merged := deploykit.MergeDeployConfigs(project, nil)
	got, ok := merged.Deploy["charly-cachyos"]
	if !ok {
		t.Fatal("charly-cachyos dropped by MergeDeployConfigs")
	}
	if got.From != "charly-cachyos" {
		t.Errorf("Local field lost: got %q want %q", got.From, "charly-cachyos")
	}
	if got.User != "alice" {
		t.Errorf("User field lost: got %q", got.User)
	}
	if !equalSlices(got.SSHArgs, []string{"-o", "ServerAliveInterval=30"}) {
		t.Errorf("SSHArgs field lost: got %v", got.SSHArgs)
	}
	// Per-machine overlay wins on collision (mirrors Host's behavior).
	overlay := &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{
		"charly-cachyos": {From: "ci-runner", User: "bob", SSHArgs: []string{"-o", "ProxyJump=bastion"}},
	}}
	merged = deploykit.MergeDeployConfigs(project, overlay)
	got = merged.Deploy["charly-cachyos"]
	if got.From != "ci-runner" {
		t.Errorf("overlay Local should win: got %q", got.From)
	}
	if got.User != "bob" {
		t.Errorf("overlay User should win: got %q", got.User)
	}
	if !equalSlices(got.SSHArgs, []string{"-o", "ProxyJump=bastion"}) {
		t.Errorf("overlay SSHArgs should win: got %v", got.SSHArgs)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMergeDeployConfigsPreservesAllFields locks in the 2026-05 regression
// fix: pre-fix MergeDeployConfigs hand-rolled per-field copies and silently
// dropped 19+ DeployNode fields (ResolvedPort, Description, Secret,
// Sidecar, Shell, Deploy, ForwardGpgAgent, ForwardSSHAgent, Kind,
// Replica, Restart, Schedule, Resources, Expose, Storage, Probes, Cpus,
// Ram, DiskSize). Any future addition of a struct field would silently
// regress in the same way. The post-fix reflect-based merger walks every
// yaml-tagged field, so adding a new field is automatically merge-correct.
//
// This test pre-populates ALL persistable fields with non-zero values
// and asserts every one survives the merge.
func TestMergeDeployConfigsPreservesAllFields(t *testing.T) {
	tr := true
	rp := []string{"32718:2718"}
	desc := "testing"
	sec := []vmshared.DeploySecretConfig{{Name: "test"}}
	sd := map[string]json.RawMessage{"side": json.RawMessage(`{"image":"img"}`)}
	kubernetesDeploy := &vmshared.KubernetesDeployConfig{Namespace: "test-ns"}
	res := &vmshared.DeployResources{}
	exp := &vmshared.DeployExpose{Host: "example.com", TLS: true}
	storage := []vmshared.DeployStorage{{Name: "s"}}
	probes := &vmshared.DeployProbes{}

	src := spec.DeployNode{
		ResolvedPort:    rp,
		Description:     desc,
		Secret:          sec,
		ForwardGpgAgent: &tr,
		ForwardSSHAgent: &tr,
		Sidecar:         sd,
		Deploy:          kubernetesDeploy,
		Kind:            "service",
		Replica:         3,
		Restart:         "always",
		Schedule:        "* * * * *",
		Resources:       res,
		Expose:          exp,
		Storage:         storage,
		Probes:          probes,
		Cpus:            4,
		Ram:             "16G",
		DiskSize:        "40G",
	}
	cfg := &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{"x": src}}
	merged := deploykit.MergeDeployConfigs(cfg, nil)
	got := merged.Deploy["x"]

	checks := []struct {
		name string
		fail bool
	}{
		{"ResolvedPort", !equalSlices(got.ResolvedPort, rp)},
		{"Description", got.Description == ""},
		{"Secret", len(got.Secret) != 1},
		{"ForwardGpgAgent", got.ForwardGpgAgent == nil || !*got.ForwardGpgAgent},
		{"ForwardSSHAgent", got.ForwardSSHAgent == nil || !*got.ForwardSSHAgent},
		{"Sidecar", len(got.Sidecar) != 1},
		{"Deploy", got.Deploy == nil},
		{"Kind", got.Kind != "service"},
		{"Replica", got.Replica != 3},
		{"Restart", got.Restart != "always"},
		{"Schedule", got.Schedule != "* * * * *"},
		{"Resources", got.Resources == nil},
		{"Expose", got.Expose == nil},
		{"Storage", len(got.Storage) != 1},
		{"Probes", got.Probes == nil},
		{"Cpus", got.Cpus != 4},
		{"Ram", got.Ram != "16G"},
		{"DiskSize", got.DiskSize != "40G"},
	}
	dropped := []string{}
	for _, c := range checks {
		if c.fail {
			dropped = append(dropped, c.name)
		}
	}
	if len(dropped) > 0 {
		t.Errorf("MergeDeployConfigs dropped %d fields: %v", len(dropped), dropped)
	}
}

// TestMergeDeployConfigsPreservesPreemptible — relocated from charly/deploy_preserve_test.go
// (#55 decoupling, Batch A: this specific test, unlike its 3 siblings in that file which
// genuinely need charly's own LoadUnified for a loader-integration round-trip and stay per
// ruling 1, asserts deploykit.MergeDeployConfigs directly against plain literal fixtures —
// zero charly dep). Covers the project↔per-host overlay merge — the documented former
// drop-site for Disposable/Lifecycle. The committed project profile (no preemptible) merged
// with the per-host overlay (preemptible) must keep the per-host flag, regardless of merge
// order.
func TestMergeDeployConfigsPreservesPreemptible(t *testing.T) {
	project := &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{
		"cachyos-gpu": {Target: "vm", From: "cachyos-gpu"}, // committed: NO preemptible
	}}
	perHost := &deploykit.DeployConfig{Deploy: map[string]spec.DeployNode{
		"cachyos-gpu": {Preemptible: &spec.PreemptibleConfig{Holds: []string{"nvidia-gpu"}}}, // local opt-in
	}}
	for _, tc := range []struct {
		name    string
		configs []*deploykit.DeployConfig
	}{
		{"project then per-host", []*deploykit.DeployConfig{project, perHost}},
		{"per-host then project", []*deploykit.DeployConfig{perHost, project}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := deploykit.MergeDeployConfigs(tc.configs...)
			node := merged.Deploy["cachyos-gpu"]
			if node.Preemptible == nil || len(node.Preemptible.Holds) != 1 {
				t.Errorf("merge DROPPED per-host preemptible: got %+v", node.Preemptible)
			}
			if node.From != "cachyos-gpu" {
				t.Errorf("merge lost committed vm field: got %q", node.From)
			}
		})
	}
}
