package deploy

import (
	"strings"

	"github.com/opencharly/spec/spec"
)

// qualifyEntityImageRef resolves a deploy node's authored `image:` in the namespace of the entity
// that declares it.
//
// An entity imported under a namespace is addressed as `ns.name` (the project projection) or
// `ns/name` (the deploy key), but the `image:` it AUTHORS is a short name for a box inside its OWN
// namespace. The resolved-project envelope keys a namespaced box by FULLKEY (`ns.name`) — a bare
// leaf that lives only in a namespace is absent under its bare key — so both resolution sites fail:
//
//	charly deploy add ...: resolving ref "kde-vnc-cdp" for target "pod": ResolveDeployRef: "kde-vnc-cdp"
//	  not found as a box or candy in the resolved-project envelope
//	plugin-deploy-pod prepare-venue: overlay build: resolving "kde-vnc-cdp" via charly.yml: not found
//
// (opencharly/opencharly#1316). The rule is the one DESIGN states for every reference, not a local
// convenience:
//
//   - D-LOAD-6 — "the scope a reference resolves in is the document it is written in", so an imported
//     entity's short ref resolves in ITS namespace;
//   - D-REF-1 — the identity's first segment is a NAMESPACE if one with that name exists, otherwise a
//     node of this project, which is what makes the scope decidable from the identity;
//   - D-REF-2 — "a short name never resolves outside its project: no search path, no catalog, no
//     fallback", so the namespace is the ONLY scope tried, never a second chance after the root;
//   - D-REF-6 — an imported project resolves its own names through its own imports, which the
//     importer does not see, so a root box that shares the leaf must not answer for it.
//
// The ref this does not touch: a full OCI ref, a remote `@github` ref, a local path, an
// already-qualified name, and anything on an entity whose first segment is not a namespace (a root
// node, or a nested child path — the dot there is tree position, and D-REF-1's existence test is what
// tells the two apart).
func qualifyEntityImageRef(rp *spec.ResolvedProject, entityPath, ref string) string {
	if rp == nil || ref == "" || !isBareBoxLeaf(ref) {
		return ref
	}
	// D-REF-1: the identity's first segment is a NAMESPACE if one with that name exists, otherwise a
	// node of this project. The entity's own scope is therefore decidable from its identity alone,
	// and D-LOAD-6 fixes where the ref resolves: in the document it is written in — here, that
	// namespace — and D-REF-2/ D-REF-6 say nowhere else. So there is NO bare-name fallback: a root
	// box that happens to share the leaf must not answer for an imported entity, and a qualified box
	// that is missing is reported by the resolver that owns it, naming the qualified ref.
	head := namespaceOfEntityPath(entityPath)
	if head == "" || !isNamespace(rp, head) {
		return ref
	}
	return head + "." + ref
}

// isNamespace reports whether head names a namespace of the resolved project. D-REF-1's test is
// existence, and the envelope carries a namespace's contents under keys prefixed `head.` (boxes,
// candies and deploys alike are keyed by fullkey), so the prefix IS the existence proof available
// here — a namespace that binds nothing contributes no key and can qualify nothing.
func isNamespace(rp *spec.ResolvedProject, head string) bool {
	prefix := head + "."
	for k := range rp.Boxes {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for k := range rp.Candies {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for k := range rp.Deploy {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// isBareBoxLeaf reports whether ref is a plain local box leaf — the only shape a namespace
// qualification can apply to. A full OCI ref, a local path, a remote @github ref and an already
// qualified name all carry their own meaning and are left alone.
func isBareBoxLeaf(ref string) bool {
	switch {
	case ref == "",
		strings.HasPrefix(ref, "@"),
		strings.HasPrefix(ref, "."),
		strings.HasPrefix(ref, "/"),
		strings.HasSuffix(ref, ".yml"),
		strings.HasSuffix(ref, ".yaml"):
		return false
	}
	return !strings.ContainsAny(ref, "/:@.")
}

// namespaceOfEntityPath returns the path head a qualification may be tried under — the FIRST
// separator of either spelling (`ns.name` in the project projection, `ns/name` as a deploy key) — or
// "" when the path carries none. The head is a CANDIDATE only: the caller proves it by looking the
// qualified box up, because a dotted path may be a nested child rather than a namespace.
func namespaceOfEntityPath(path string) string {
	if i := strings.IndexByte(path, '/'); i > 0 {
		return path[:i]
	}
	if i := strings.IndexByte(path, '.'); i > 0 {
		return path[:i]
	}
	return ""
}
