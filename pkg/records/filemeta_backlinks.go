// Omnipus — ADR-068 D24.2 / spec FR-132: `file.backlinks`, derived rather than
// stored, workspace-scoped, and bounded by a mid-scan abort.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// WHY THIS IS THREE THINGS AND NOT ONE
//
// FR-132 cites knowledgefind/near.go::buildRelationGraph as the precedent. That
// function has a CAP, a STREAMING ABORT and a NAMED REFUSAL — three separate
// mechanisms — and Draft 10 cited it while shipping none of them. This file
// takes all three, because any two of them are worthless:
//
//   - a cap with no abort is a number in a comment: the work still happens and
//     the caller finds out afterwards, having already paid;
//   - an abort with no named refusal is a query that stops and returns an
//     honest-looking short answer, which is the silent wrong result this whole
//     design exists to remove;
//   - a refusal with no cap never fires.
//
// The abort is MID-SCAN and not a pre-count, for near.go's own reason: ruling
// R-A keeps every aggregate off the SQL path, so there is no COUNT over the
// link table to precede the scan with. The bound is therefore enforced where
// the work is, and it is enforced by refusing to do more of it.
//
// SCOPE (FR-132). Obsidian's backlinks are vault-wide. The closest reading that
// does not cross FR-062's workspace boundary is "vault-wide WITHIN THE CALLER'S
// WORKSPACE SCOPE", and that is what this computes. The narrowing itself lives
// in the caller's Selector — this package cannot open a store — so the scope is
// carried here as a RECORDED FACT that the refusal names, not as an enforcement
// this file could perform. That division is stated rather than blurred: a
// refusal that names the wrong scope would send the operator to narrow
// something that was never wide.
// ---------------------------------------------------------------------------

// MaxBacklinkEdges is FR-132's bound: the link edges one backlink derivation
// may visit within the caller's scope.
//
// It is a var rather than a const so a bound test can shrink it and observe the
// far side of the boundary without writing 200,001 real rows for every
// assertion. Production code never assigns it. (near.go's MaxHopTraversalEdges
// is a var for the identical reason; the two bounds are deliberately DIFFERENT
// numbers over different tables, and neither may be quietly aligned to the
// other — FR-132 states 200,000 and FR-065a states 50,000.)
var MaxBacklinkEdges = 200_000

// errBacklinkBoundExceeded aborts the edge stream. It never reaches a caller:
// BuildBacklinkIndex translates it into *BacklinkBoundError, which carries the
// count and the remedy.
var errBacklinkBoundExceeded = errors.New("records: backlink derivation exceeded its edge bound")

// BacklinkBoundError is FR-132's named refusal.
type BacklinkBoundError struct {
	// Count is the edge at which the scan stopped — the bound plus one, not a
	// total, because there is no total: the scan was abandoned before one could
	// be known. Saying "more than N" rather than a precise figure is the honest
	// rendering of that.
	Count int
	Limit int
	// Scope is the path prefix the derivation was told it was running under. It
	// is in the message because "narrow the scope" is unactionable advice
	// without it.
	Scope  string
	Remedy string
}

func (e *BacklinkBoundError) Error() string {
	scope := e.Scope
	if scope == "" {
		scope = "the whole workspace"
	}
	return fmt.Sprintf(
		"file.backlinks scans every link edge in scope (%s) and this scope holds more than %d of them; "+
			"backlinks are derived, never stored, so there is no cheaper way to answer; %s",
		scope, e.Limit, e.Remedy)
}

// IsBacklinkBoundExceeded reports whether err is FR-132's refusal.
func IsBacklinkBoundExceeded(err error) bool {
	var be *BacklinkBoundError
	return errors.As(err, &be)
}

// FileLinkRow is one row of FR-131's note_links child table, as the store
// streams it.
//
// It is a plain value with no store attached, which is what keeps this whole
// layer on the correct side of ruling R-A: the derivation consumes rows that
// have already been retrieved and can no more reach a database than a string
// can.
type FileLinkRow struct {
	// NotePath is the note that HOLDS the link — the source of the edge, and
	// therefore the backlink a target note gets.
	NotePath string
	// Target is the link target exactly as the note wrote it: a bare note name
	// ("Acme"), a vault path ("Clients/Acme.md"), either with or without an
	// extension.
	Target  string
	Heading string
	Display string
	Raw     string
	// Embed distinguishes `![[x]]` from `[[x]]`. Both count as backlinks —
	// Obsidian counts an embed as a link to the embedded note, and a reader
	// asking "what references this?" means both.
	Embed bool
}

// Wikilink renders the row as the link value the comparator and the renderer
// see.
func (r FileLinkRow) Wikilink() Wikilink {
	raw := r.Raw
	if raw == "" {
		raw = "[[" + r.Target + "]]"
	}
	return Wikilink{Target: r.Target, Heading: r.Heading, Display: r.Display, Raw: raw}
}

// BacklinkScope records the narrowing the caller opened the edge stream under.
type BacklinkScope struct {
	// PathPrefix is the caller's workspace scope (FR-060) — the same
	// caller-independent prefix the Selector carries, never query text.
	PathPrefix string
}

// LinkEdgeStream yields every link edge in scope, once each, calling visit per
// edge. An error returned by visit MUST abort the stream and propagate — that
// is how the mid-scan abort works, and a stream that swallows visit's error
// silently removes the bound.
type LinkEdgeStream func(visit func(FileLinkRow) error) error

// BacklinkIndex is the inverse edge map for one query, built once and reused
// for every candidate that query evaluates.
//
// Building it per candidate would re-scan the whole workspace once per note —
// the quadratic cost the bound is not there to authorise.
type BacklinkIndex struct {
	scope BacklinkScope
	edges int
	// bySource is keyed by a FOLDED target key and holds the set of source note
	// paths that reach it. A set, because two links from one note to the same
	// target are one backlink.
	bySource map[string]map[string]bool
}

// BuildBacklinkIndex streams every link edge in the caller's scope exactly once
// and inverts it.
//
// It refuses with *BacklinkBoundError above MaxBacklinkEdges, aborting the
// stream at the edge that crosses the bound rather than after it.
//
// MEMORY, STATED. At the bound this holds up to 200,000 edges expanded across
// at most three keys each, i.e. up to ~600,000 map entries plus the source path
// strings — tens of megabytes, and it is the reason the bound is 200,000 rather
// than "as many as there are". A caller that cannot afford it narrows the scope,
// which is exactly what the refusal says.
func BuildBacklinkIndex(scope BacklinkScope, stream LinkEdgeStream) (*BacklinkIndex, error) {
	if stream == nil {
		return nil, &QueryError{
			Property: FileBacklinksProp,
			Reason:   "no link-edge stream was supplied, so backlinks cannot be derived",
			Remedy:   "pass the store's note_links stream, narrowed to the caller's workspace scope",
		}
	}

	ix := &BacklinkIndex{scope: scope, bySource: map[string]map[string]bool{}}
	err := stream(func(row FileLinkRow) error {
		ix.edges++
		if ix.edges > MaxBacklinkEdges {
			return errBacklinkBoundExceeded
		}
		source := cleanVaultPath(row.NotePath)
		target := cleanVaultPath(row.Target)
		if source == "" || target == "" {
			// A row with no source or no target connects nothing. It is a
			// check_integrity finding, not this query's business, and skipping
			// it hides nothing a reader would otherwise see here.
			return nil
		}
		for _, key := range backlinkKeys(target) {
			set := ix.bySource[key]
			if set == nil {
				set = map[string]bool{}
				ix.bySource[key] = set
			}
			set[source] = true
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errBacklinkBoundExceeded) {
			return nil, &BacklinkBoundError{
				Count: ix.edges,
				Limit: MaxBacklinkEdges,
				Scope: scope.PathPrefix,
				Remedy: "narrow the workspace scope — mount a smaller folder — or drop file.backlinks from the view " +
					"and read the note's references a few knowledge_read calls at a time",
			}
		}
		return nil, err
	}
	return ix, nil
}

// EdgeCount is how many edges the derivation actually visited. It is what a
// response's cost line reports, and what a bound test asserts against.
func (ix *BacklinkIndex) EdgeCount() int { return ix.edges }

// Scope is the narrowing this index was built under.
func (ix *BacklinkIndex) Scope() BacklinkScope { return ix.scope }

// For returns the notes that link to notePath, as one Wikilink per SOURCE note,
// sorted by path.
//
// SORTED, because Go's map iteration is randomised and an unsorted answer would
// reorder a rendered column between two identical calls — the kind of
// instability that reads as data changing.
//
// SELF-LINKS ARE DROPPED. A note linking to itself is not its own backlink;
// including it would put every self-referencing note into its own "what
// references this?" list, which answers a question nobody asked.
//
// ONE IMPRECISION, NAMED. A bare `[[Acme]]` names a note by name, and two notes
// in different folders can share a name. Obsidian resolves that by proximity;
// this does not, and both notes therefore receive the backlink. Over-inclusion
// is the direction to err in here — the alternative is picking one silently,
// and a backlink that is missing is indistinguishable from a note nobody links
// to.
func (ix *BacklinkIndex) For(notePath string) []Wikilink {
	if ix == nil {
		return nil
	}
	self := cleanVaultPath(notePath)
	if self == "" {
		return nil
	}
	seen := map[string]bool{}
	var sources []string
	for _, key := range backlinkKeys(self) {
		for source := range ix.bySource[key] {
			if source == self || seen[source] {
				continue
			}
			seen[source] = true
			sources = append(sources, source)
		}
	}
	sort.Strings(sources)

	out := make([]Wikilink, 0, len(sources))
	for _, s := range sources {
		out = append(out, Wikilink{Target: s, Raw: "[[" + s + "]]"})
	}
	return out
}

// Apply fills in the backlink fields of one FileMeta, including the
// BacklinksDerived flag — so a caller cannot set the values and forget the flag,
// which would make a correct answer look like an uncomputed one.
func (ix *BacklinkIndex) Apply(m *FileMeta) {
	if m == nil {
		return
	}
	m.Backlinks = ix.For(m.Path)
	m.BacklinksDerived = true
}

// backlinkKeys is the set of folded keys one path answers to, and the ONE place
// the name-resolution rule for backlinks is written down.
//
// Three keys, in descending specificity:
//
//	"Clients/Acme.md" -> full path, path without extension, base name
//
// A link written in any of those three forms therefore finds the note, which is
// what a vault actually contains: `[[Acme]]`, `[[Clients/Acme]]` and
// `[[Clients/Acme.md]]` are three spellings of one reference.
//
// Folded with FoldKey — the package's one folding function (FR-011a) — rather
// than strings.ToLower, which gets German ß wrong, or strings.EqualFold, which
// disagrees with ToLower on Greek. A backlink key is text and text folds one
// way here.
func backlinkKeys(p string) []string {
	p = cleanVaultPath(p)
	if p == "" {
		return nil
	}
	keys := []string{FoldKey(p)}

	noExt := p
	if ext := path.Ext(p); ext != "" {
		noExt = strings.TrimSuffix(p, ext)
	}
	if noExt != p && noExt != "" {
		keys = append(keys, FoldKey(noExt))
	}

	base := path.Base(noExt)
	if base != "" && base != "." && base != "/" && base != noExt {
		keys = append(keys, FoldKey(base))
	}
	return keys
}
