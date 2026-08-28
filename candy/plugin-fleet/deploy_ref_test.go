package fleet

// deploy_ref_test.go — the K4-C shape-2 ref-classification parity golden (the plugin half of the
// former charly/deploy_ref_test.go). It proves the box-vs-candy classification resolveDeployRef /
// resolveDeployRefAsCandy now do OFF THE RESOLVED-PROJECT ENVELOPE (rp.Boxes / rp.Candies) is
// byte-faithful to the former host LoadUnified + os.Stat resolver across every ref form:
// local-name (box-first / candy-first preference + namespace-qualified fullKey + cross-kind reuse),
// remote (@github box/candy/bare subpath), and local-path (classifyYAMLFile). NON-VACUOUS: the rp
// is a HAND-BUILT envelope independent of the resolver — a resolver that ignored rp would fail the
// box-only / candy-only / not-found cases.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
)

// testEnvelope hand-builds a resolved-project envelope with one box-only name, one candy-only name,
// one name in BOTH kinds (cross-kind reuse), and one NAMESPACE-QUALIFIED box (fullKey-keyed).
func testEnvelope() *spec.ResolvedProject {
	return &spec.ResolvedProject{
		Boxes: map[string]spec.ResolvedBoxView{
			"fedora-coder":   {},
			"charly.jupyter": {}, // namespace-qualified fullKey (root has no bare "jupyter")
			"dup":            {}, // cross-kind reuse — also a candy below
		},
		Candies: map[string]spec.CandyView{
			"ripgrep": {},
			"dup":     {}, // cross-kind reuse
		},
	}
}

func TestResolveDeployRef_LocalNameBox(t *testing.T) {
	got, err := resolveDeployRef(testEnvelope(), "fedora-coder", "")
	if err != nil {
		t.Fatalf("resolveDeployRef: %v", err)
	}
	if got.Kind != RefKindBox || got.Source != RefSourceLocalName || got.Name != "fedora-coder" {
		t.Fatalf("box name: got %+v, want box/local-name/fedora-coder", got)
	}
}

func TestResolveDeployRef_LocalNameCandy(t *testing.T) {
	got, err := resolveDeployRef(testEnvelope(), "ripgrep", "")
	if err != nil {
		t.Fatalf("resolveDeployRef: %v", err)
	}
	if got.Kind != RefKindCandy || got.Source != RefSourceLocalName {
		t.Fatalf("candy name: got %+v, want candy/local-name", got)
	}
}

func TestResolveDeployRef_NamespaceQualifiedBox(t *testing.T) {
	// rp.Boxes is fullKey-keyed, so a qualified ref is a direct map hit (byte-exact to the host
	// ResolveBoxRef descent). A bare leaf that lives ONLY in a namespace is absent under its bare
	// key — the resolver reports not-found, exactly as ResolveBoxRef returns false for it.
	got, err := resolveDeployRef(testEnvelope(), "charly.jupyter", "")
	if err != nil {
		t.Fatalf("qualified box: %v", err)
	}
	if got.Kind != RefKindBox || got.Name != "charly.jupyter" {
		t.Fatalf("qualified box: got %+v, want box/charly.jupyter", got)
	}
	if _, err := resolveDeployRef(testEnvelope(), "jupyter", ""); err == nil {
		t.Fatal("bare leaf of a namespaced-only box must be not-found (matching ResolveBoxRef)")
	}
}

func TestResolveDeployRef_CrossKindPreference(t *testing.T) {
	// "dup" is BOTH a box and a candy — preferKind decides. The primary <ref> resolver is box-first;
	// the --add-candy resolver is candy-first.
	if got, err := resolveDeployRef(testEnvelope(), "dup", ""); err != nil || got.Kind != RefKindBox {
		t.Fatalf("primary dup: got %+v err=%v, want box-first", got, err)
	}
	if got, err := resolveDeployRefAsCandy(testEnvelope(), "dup", ""); err != nil || got.Kind != RefKindCandy {
		t.Fatalf("add-candy dup: got %+v err=%v, want candy-first", got, err)
	}
}

func TestResolveDeployRef_NotFound(t *testing.T) {
	if _, err := resolveDeployRef(testEnvelope(), "nope", ""); err == nil {
		t.Fatal("unknown name must error")
	}
}

func TestResolveDeployRef_RemoteSubpath(t *testing.T) {
	cases := []struct {
		ref  string
		kind RefKind
	}{
		{"@github.com/opencharly/charly/candy/ripgrep:v2026.1.0", RefKindCandy},
		{"@github.com/opencharly/charly/box/fedora-coder:v2026.1.0", RefKindBox},
		{"github.com/opencharly/charly/candy/ripgrep@v2026.1.0", RefKindCandy}, // no leading @
		{"@github.com/opencharly/charly", RefKindBox},                          // bare repo → box
	}
	for _, c := range cases {
		got, err := resolveDeployRef(testEnvelope(), c.ref, "")
		if err != nil {
			t.Fatalf("remote %q: %v", c.ref, err)
		}
		if got.Kind != c.kind || got.Source != RefSourceRemote {
			t.Fatalf("remote %q: got %s/%s, want %s/remote", c.ref, got.Kind, got.Source, c.kind)
		}
	}
}

// TestResolveDeployRef_RemotePrimaryLayerCandy — a bare standalone candy repo used as the
// PRIMARY <ref> (@github.com/opencharly/layer-<name>:v...) must classify as candy even though
// the primary resolver is box-first (preferKind=Box). The cutover's standalone repos dropped the
// candy/<name> subpath, so a remote primary layer ref (e.g. a check-* R10 bed fixture advanced
// to its own repo) would otherwise default to box and be rejected as a "remote image ref" in
// compileRefSelection. This is the fleet-add remote-primary-candy gap (todo #34).
func TestResolveDeployRef_RemotePrimaryLayerCandy(t *testing.T) {
	cases := []struct {
		ref  string
		kind RefKind
	}{
		{"@github.com/opencharly/layer-uv:v2026.237.458", RefKindCandy},
		{"@github.com/opencharly/layer-charly-check:v2026.240.0001", RefKindCandy},
		{"@github.com/opencharly/layer-check-local-layer:v2026.240.0001", RefKindCandy},
		{"@github.com/opencharly/layer-check-stack-layer:v2026.240.0001", RefKindCandy},
		// a bare NON-layer repo stays box-first for the primary path (unchanged)
		{"@github.com/opencharly/charly", RefKindBox},
	}
	for _, c := range cases {
		got, err := resolveDeployRef(testEnvelope(), c.ref, "")
		if err != nil {
			t.Fatalf("primary remote %q: %v", c.ref, err)
		}
		if got.Kind != c.kind || got.Source != RefSourceRemote {
			t.Fatalf("primary remote %q: got %s/%s, want %s/remote", c.ref, got.Kind, got.Source, c.kind)
		}
	}
}

// TestResolveDeployRefAsCandy_BareRemote — the post-cutover standalone candy repos are
// referenced BARE (@github.com/opencharly/layer-uv:v... — no candy/<name> subpath), so
// --add-candy must classify a bare remote ref as candy (preferKind), not box. The pre-cutover
// in-repo refs always carried the candy/ subpath; the hardcoded box default for bare refs
// misclassified every standalone --add-candy ref and fleet add rejected it.
func TestResolveDeployRefAsCandy_BareRemote(t *testing.T) {
	cases := []struct {
		ref  string
		kind RefKind
	}{
		{"@github.com/opencharly/layer-uv:v2026.237.458", RefKindCandy},
		{"@github.com/opencharly/layer-pre-commit:v2026.239.0016", RefKindCandy},
		{"@github.com/opencharly/plugin-spice/candy/plugin-spice:v2026.237.1428", RefKindCandy}, // subpath form still candy
		{"@github.com/opencharly/layer-tailscale-up:v2026.238.2102", RefKindCandy},
	}
	for _, c := range cases {
		got, err := resolveDeployRefAsCandy(testEnvelope(), c.ref, "")
		if err != nil {
			t.Fatalf("add-candy remote %q: %v", c.ref, err)
		}
		if got.Kind != c.kind || got.Source != RefSourceRemote {
			t.Fatalf("add-candy remote %q: got %s/%s, want %s/remote", c.ref, got.Kind, got.Source, c.kind)
		}
	}
	// The primary <ref> path classifies a bare layer-* repo as candy (the fleet-add
	// remote-primary-candy gap fix): a standalone candy repo used as the primary ref must
	// reach the candy compile branch, not be rejected as a "remote image ref".
	if got, err := resolveDeployRef(testEnvelope(), "@github.com/opencharly/layer-uv:v2026.237.458", ""); err != nil || got.Kind != RefKindCandy {
		t.Fatalf("primary bare layer remote: got %+v err=%v, want candy", got, err)
	}
	// A bare NON-layer repo stays box-first for the primary path (unchanged).
	if got, err := resolveDeployRef(testEnvelope(), "@github.com/opencharly/charly", ""); err != nil || got.Kind != RefKindBox {
		t.Fatalf("primary bare non-layer remote: got %+v err=%v, want box", got, err)
	}
}

func TestResolveDeployRef_LocalPath(t *testing.T) {
	dir := t.TempDir()
	boxPath := filepath.Join(dir, "mybox.yml")
	if err := os.WriteFile(boxPath, []byte("base: quay.io/fedora/fedora:43\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candyPath := filepath.Join(dir, "mycandy.yml")
	if err := os.WriteFile(candyPath, []byte("rpm:\n  packages: [ripgrep]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveDeployRef(testEnvelope(), boxPath, ""); err != nil || got.Kind != RefKindBox || got.Source != RefSourceLocalPath {
		t.Fatalf("local box path: got %+v err=%v", got, err)
	}
	if got, err := resolveDeployRef(testEnvelope(), candyPath, ""); err != nil || got.Kind != RefKindCandy || got.Source != RefSourceLocalPath {
		t.Fatalf("local candy path: got %+v err=%v", got, err)
	}
}
