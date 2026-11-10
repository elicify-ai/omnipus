package gateway

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// collectionFileNames spells the schema and view files the records loaders
// report as the API contracts require: relative to the owning collection's
// root, with forward slashes (RecordType.source_path, RecordProblem.paths,
// KnowledgeBaseUnloadableView.paths).
//
// The loaders join every file under the root they were handed, which is the
// collection root as configured or, for a checked load, its resolved form, so
// both spellings are accepted. A file that is under neither is named by its
// base name alone; its rejection is still reported.
type collectionFileNames struct {
	roots []string // cleaned, longest first
}

func newCollectionFileNames(collectionRoot string) collectionFileNames {
	roots := []string{filepath.Clean(collectionRoot)}
	if resolved, err := filepath.EvalSymlinks(collectionRoot); err == nil && filepath.Clean(resolved) != roots[0] {
		roots = append(roots, filepath.Clean(resolved))
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) > len(roots[j]) })
	return collectionFileNames{roots: roots}
}

// name is p relative to the collection root, forward-slashed.
func (n collectionFileNames) name(p string) string {
	if p == "" {
		return ""
	}
	for _, root := range n.roots {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			continue
		}
		slashed := filepath.ToSlash(rel)
		if slashed == "." || slashed == ".." || strings.HasPrefix(slashed, "../") {
			continue
		}
		return slashed
	}
	return filepath.Base(p)
}

func (n collectionFileNames) names(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, n.name(p))
	}
	return out
}

// text rewrites a loader reason: each reported file is replaced by its
// collection-relative name, and any other spelling of a file under the root
// (a read error naming it, for example) loses the root prefix.
func (n collectionFileNames) text(reason string, paths []string) string {
	ordered := append([]string(nil), paths...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, p := range ordered {
		if p == "" {
			continue
		}
		reason = strings.ReplaceAll(reason, p, n.name(p))
	}
	for _, root := range n.roots {
		for _, prefix := range []string{root + string(filepath.Separator), filepath.ToSlash(root) + "/"} {
			reason = strings.ReplaceAll(reason, prefix, "")
		}
	}
	return reason
}

func (n collectionFileNames) viewRejection(rej records.ViewRejection) records.ViewRejection {
	rej.Reason = n.text(rej.Reason, rej.Paths)
	rej.Paths = n.names(rej.Paths)
	return rej
}

func (n collectionFileNames) schemaRejection(rej records.SchemaRejection) records.SchemaRejection {
	rej.Reason = n.text(rej.Reason, rej.Paths)
	rej.Paths = n.names(rej.Paths)
	return rej
}
