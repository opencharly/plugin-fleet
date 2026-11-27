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
// (opencharly/opencharly#1316). The rule is the one the loader already applies to every other ref
// inside a namespace: resolve it THERE. It is applied here, at the deploy boundary, because that is
// the one place that knows both the entity's address and its authored ref — and because it changes no
// stored shape: the schema stays the short name the submodule authors, so the same charly.yml is
// still a standalone project.
//
// The qualification is proved by EXISTENCE, never by guessing at the path: the qualified box must be
// present in the envelope. A dotted deploy path is ambiguous — it is a NAMESPACE separator in
// `ns.name` and a NESTED-CHILD separator in `root.child` (tree position, /charly-core:deploy) — so a
// path head is only treated as a namespace when `head.ref` is a real box there. A short ref that
// resolves as written, a full ref, a path and a remote ref are all returned untouched.
func qualifyEntityImageRef(rp *spec.ResolvedProject, entityPath, ref string) string {
	if rp == nil || ref == "" || !isBareBoxLeaf(ref) {
		return ref
	}
	// Authored as written and resolvable as written: nothing to do. This is also what keeps a
	// namespaced entity that deliberately names a ROOT box working.
	if _, ok := rp.Boxes[ref]; ok {
		return ref
	}
	head := namespaceOfEntityPath(entityPath)
	if head == "" {
		return ref
	}
	qualified := head + "." + ref
	if _, ok := rp.Boxes[qualified]; ok {
		return qualified
	}
	return ref
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
