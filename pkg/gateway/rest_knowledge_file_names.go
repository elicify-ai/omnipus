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

// text rewrites a loader reason so it names files exactly as paths does.
//
// It is one left-to-right pass over the original reason, and text it has
// produced is never scanned again: a relative name such as
// ".omnipus-vault/records/a.yaml" must stay intact even when the collection
// root is itself named /records. At each position it replaces, in order:
//   - a reported file (longest first), with that file's name;
//   - a file under the root that is not in paths (a read error naming it, for
//     example), starting where a word starts, with that file's name.
//
// Everything else is copied unchanged.
func (n collectionFileNames) text(reason string, paths []string) string {
	reported := make([]reportedSpelling, 0, 2*len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		reported = append(reported, reportedSpelling{p, p})
		if slashed := filepath.ToSlash(p); slashed != p {
			reported = append(reported, reportedSpelling{slashed, p})
		}
	}
	sort.SliceStable(reported, func(i, j int) bool { return len(reported[i].text) > len(reported[j].text) })

	var b strings.Builder
	for i := 0; i < len(reason); {
		rest := reason[i:]
		if s, ok := firstPrefix(rest, reported); ok {
			b.WriteString(n.name(s.path))
			i += len(s.text)
			continue
		}
		if wordStartsAt(reason, i) {
			if end, ok := n.rootedFileEnd(rest); ok {
				b.WriteString(n.name(filepath.FromSlash(rest[:end])))
				i += end
				continue
			}
		}
		b.WriteByte(reason[i])
		i++
	}
	return b.String()
}

// reportedSpelling is one way a reported file may be written in a reason.
type reportedSpelling struct{ text, path string }

func firstPrefix(s string, candidates []reportedSpelling) (reportedSpelling, bool) {
	for _, c := range candidates {
		if strings.HasPrefix(s, c.text) {
			return c, true
		}
	}
	return reportedSpelling{}, false
}

// isNameDelimiter reports whether b ends a file name inside reason text.
// Only ASCII bytes are delimiters, so a multi-byte character in a file name
// never splits it.
func isNameDelimiter(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || strings.IndexByte("\"'(),;:[]", b) >= 0
}

func wordStartsAt(s string, i int) bool {
	return i == 0 || isNameDelimiter(s[i-1]) || s[i-1] == '='
}

// rootedFileEnd reports the length of the file name at the start of s when it
// begins with one of the root folders, native or slash-separated.
func (n collectionFileNames) rootedFileEnd(s string) (int, bool) {
	for _, root := range n.roots {
		for _, prefix := range []string{root + string(filepath.Separator), filepath.ToSlash(root) + "/"} {
			if !strings.HasPrefix(s, prefix) {
				continue
			}
			end := len(prefix)
			for end < len(s) && !isNameDelimiter(s[end]) {
				end++
			}
			return end, true
		}
	}
	return 0, false
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
