// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// graph.go — the link graph of one collection (ADR-067 D6; FR-040..FR-046,
// FR-051, FR-054).
//
// The graph is what a search index cannot give you: "what refers to this?".
// It is built by walking the collection once, streaming each note, extracting
// its links and headings, and resolving every link by the stated rules. No
// judgement is applied anywhere — the Librarian (the layer that would PROPOSE
// links) is explicitly out of scope (NB-15), and no part of graph correctness
// may depend on it.
//
// Reproducibility is a property of construction, not of an assertion: every
// input is sorted before it is used and every output is sorted before it is
// returned, so two builds over the same bytes produce the same answers on any
// machine (US-11, FR-046). Note that this is asserted behaviourally — the
// scorch index underneath is not byte-reproducible and never will be.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// Bounds on neighbourhood queries (ADR-067 D7, FR-054). An unbounded
// neighbourhood over a 100,000-note collection is a plausible self-DoS, so
// both limits are enforced here rather than trusted to the caller — and a
// request above either is CLAMPED and the clamping reported, never silently
// applied and never rejected outright.
const (
	// MaxNeighborhoodHops is the furthest a neighbourhood query may reach.
	MaxNeighborhoodHops = 3
	// MaxNeighborhoodNodes is the most nodes a neighbourhood may contain.
	MaxNeighborhoodNodes = 500
)

// LinkGraph is the resolved link graph of one collection.
//
// It is immutable once built. All accessors return copies, so a caller cannot
// reach in and mutate the graph another caller is reading.
type LinkGraph struct {
	root     CollectionRoot
	files    []string
	notes    []string
	index    *NoteIndex
	headings map[string][]Heading

	outbound   map[string][]ResolvedLink
	inbound    map[string][]ResolvedLink
	unresolved []ResolvedLink
	ambiguous  []ResolvedLink
	skipped    []SkippedEntry
}

// BuildLinkGraph walks the collection and resolves every link in it.
//
// Only markdown notes are opened. Attachments are registered by filename and
// path so "![[diagram-v3.png]]" resolves and "diagram-v3" is findable, but
// their contents are never read for any reason (FR-039a) — a collection of
// 100,000 attachments must cost zero content bytes.
//
// Nothing that fails is dropped silently (NB-9): a note that cannot be opened,
// an entry that is a symlink, a path that will not resolve inside the root —
// each is recorded in Skipped with a reason.
func BuildLinkGraph(fsys LinkFS, root CollectionRoot) (*LinkGraph, error) {
	if !root.Valid() {
		return nil, fmt.Errorf("%w: root not initialised", ErrCollectionRootInvalid)
	}
	walk, err := WalkContained(fsys, root)
	if err != nil {
		return nil, err
	}

	g := &LinkGraph{
		root:     root,
		files:    walk.Files,
		index:    NewNoteIndex(walk.Files),
		headings: make(map[string][]Heading),
		outbound: make(map[string][]ResolvedLink),
		inbound:  make(map[string][]ResolvedLink),
		skipped:  walk.Skipped,
	}

	type pending struct {
		note  string
		links []Link
	}
	var queue []pending

	for _, rel := range walk.Files {
		if !IsMarkdownPath(rel) {
			continue
		}
		// FR-043 applied a second time, to the path we are about to OPEN.
		// The walk already proved containment; this is what keeps the
		// guarantee true if a caller ever builds a graph from a file list
		// that did not come from the walk.
		resolved, resolveErr := root.ResolveContained(fsys, rel)
		if resolveErr != nil {
			g.skipped = append(g.skipped, SkippedEntry{
				RelPath: rel,
				Reason:  SkipOutsideRoot,
				Detail:  resolveErr.Error(),
			})
			continue
		}
		f, openErr := fsys.Open(resolved)
		if openErr != nil {
			g.skipped = append(g.skipped, SkippedEntry{
				RelPath: rel,
				Reason:  SkipUnreadable,
				Detail:  openErr.Error(),
			})
			continue
		}
		// Stat the file we actually opened, not a separately-lstat'ed path:
		// what matters is the size disagreement against THIS read, and an fd
		// stat cannot be raced by something else replacing the entry between
		// two syscalls the way two path-based calls could be.
		fi, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			g.skipped = append(g.skipped, SkippedEntry{
				RelPath: rel,
				Reason:  SkipUnreadable,
				Detail:  statErr.Error(),
			})
			continue
		}
		scan, scanErr := ScanNote(f)
		_ = f.Close()
		// FR-111, asked of the graph's own read: a cloud-dematerialised file
		// (OneDrive/iCloud/rclone) stats with its real size and reads as a
		// clean, errorless EOF. ScanNote sees that as a note with no links —
		// scanErr == nil, zero bytes read — and without this check it joins
		// g.notes as an ordinary, link-free note instead of g.skipped. That is
		// not a cosmetic miss: BuildLinkGraph is the sole authority Rename
		// trusts for "what points at this note". A note silently scanned as
		// empty here means every real citation TO or FROM it goes undetected,
		// and a rename that should rewrite those citations reports
		// links_rewritten=0 and succeeds while leaving them dangling.
		// ClassifyContentFailure is the one classifier for this (lifecycle.go);
		// duplicating its size-disagreement test here would let the two
		// definitions of "evicted" drift apart.
		if cErr := ClassifyContentFailure(resolved, fi.Size(), int(scan.Stats.BytesRead), scanErr); cErr != nil {
			g.skipped = append(g.skipped, SkippedEntry{
				RelPath: rel,
				Reason:  SkipUnreadable,
				Detail:  cErr.Error(),
			})
			continue
		}
		g.notes = append(g.notes, rel)
		g.headings[rel] = scan.Headings
		queue = append(queue, pending{note: rel, links: scan.Links})
	}

	// Resolution happens only after every note has been scanned, because a
	// "#Heading" anchor can only be checked once the target's headings are
	// known — and a link may point forwards in walk order.
	for _, p := range queue {
		for _, l := range p.links {
			res := g.index.Resolve(p.note, l)
			if res.State == ResolveResolved && res.Heading != "" {
				if h, ok := findHeading(g.headings[res.To], res.Heading); ok {
					res.HeadingFound = true
					res.HeadingLine = h.Line
				}
			}
			g.outbound[p.note] = append(g.outbound[p.note], res)
			switch res.State {
			case ResolveResolved:
				g.inbound[res.To] = append(g.inbound[res.To], res)
			case ResolveUnresolved:
				g.unresolved = append(g.unresolved, res)
			}
			if res.Ambiguous {
				g.ambiguous = append(g.ambiguous, res)
			}
		}
	}

	sort.Strings(g.notes)
	for k := range g.outbound {
		sortResolved(g.outbound[k])
	}
	for k := range g.inbound {
		sortResolved(g.inbound[k])
	}
	sortResolved(g.unresolved)
	sortResolved(g.ambiguous)
	sort.Slice(g.skipped, func(i, j int) bool {
		if g.skipped[i].RelPath != g.skipped[j].RelPath {
			return g.skipped[i].RelPath < g.skipped[j].RelPath
		}
		return g.skipped[i].Reason < g.skipped[j].Reason
	})
	return g, nil
}

// sortResolved imposes the one total order used everywhere in the graph:
// source note, then position within it. Position is by byte offset rather than
// line so that several links on one line keep the order they were written in.
func sortResolved(links []ResolvedLink) {
	sort.Slice(links, func(i, j int) bool {
		if links[i].From != links[j].From {
			return links[i].From < links[j].From
		}
		if links[i].Offset != links[j].Offset {
			return links[i].Offset < links[j].Offset
		}
		return links[i].To < links[j].To
	})
}

// findHeading matches a "#Heading" anchor against a note's headings.
//
// Matching folds case and collapses runs of whitespace, because an anchor is
// written by a human copying a heading they can see and a double space is not
// a different section. It is otherwise exact: no fuzzy matching, no closest
// guess (NB-5).
func findHeading(headings []Heading, anchor string) (Heading, bool) {
	want := normalizeHeading(anchor)
	if want == "" {
		return Heading{}, false
	}
	for _, h := range headings {
		if normalizeHeading(h.Text) == want {
			return h, true
		}
	}
	return Heading{}, false
}

// normalizeHeading folds with records.FoldKey, not strings.ToLower — this
// package's rule for text comparison (pkg/records/value.go's FoldKey doc,
// AC-8.9): a heading is a note's own text, written by whoever authored it,
// and may be non-ASCII. strings.ToLower gets Unicode wrong in both
// directions (the Turkish İ/i pair collapses onto a false match; "straße" and
// "Straße" fail to match at all) that records.FoldKey exists to fix.
func normalizeHeading(s string) string {
	return records.FoldKey(strings.Join(strings.Fields(s), " "))
}

// Root returns the collection root the graph was built over.
func (g *LinkGraph) Root() CollectionRoot { return g.root }

// Files returns every contained file found, sorted. Attachments included.
func (g *LinkGraph) Files() []string { return append([]string(nil), g.files...) }

// Notes returns every markdown note that was successfully scanned, sorted.
func (g *LinkGraph) Notes() []string { return append([]string(nil), g.notes...) }

// Links returns the outbound links of a note, in document order — resolved and
// unresolved alike, because a reader needs to see the broken one (US-7 AS-8).
func (g *LinkGraph) Links(relPath string) []ResolvedLink {
	return append([]ResolvedLink(nil), g.outbound[normalizeRel(relPath)]...)
}

// Backlinks returns every link pointing AT a note, regardless of which of the
// four spellings was used to write it (AC-7.2).
func (g *LinkGraph) Backlinks(relPath string) []ResolvedLink {
	return append([]ResolvedLink(nil), g.inbound[normalizeRel(relPath)]...)
}

// Unresolved returns every link that found no target, with the reason
// (FR-042). A link that escapes the collection appears here, never nowhere.
func (g *LinkGraph) Unresolved() []ResolvedLink {
	return append([]ResolvedLink(nil), g.unresolved...)
}

// Ambiguous returns every link that resolved through the tie-break because its
// name matched more than one file (FR-041). These links DID resolve — they
// appear here in addition, not instead.
func (g *LinkGraph) Ambiguous() []ResolvedLink {
	return append([]ResolvedLink(nil), g.ambiguous...)
}

// Skipped returns every reported exclusion: symlinks, unreadable files, paths
// that would not resolve inside the root (FR-044, NB-9).
func (g *LinkGraph) Skipped() []SkippedEntry {
	return append([]SkippedEntry(nil), g.skipped...)
}

// Outline returns a note's headings in document order (FR-062).
func (g *LinkGraph) Outline(relPath string) []Heading {
	return append([]Heading(nil), g.headings[normalizeRel(relPath)]...)
}

// Orphans returns every note nothing links to, sorted.
//
// "Orphan" here means UNREACHABLE — no inbound resolved link — which is the
// sense the term carries in a note collection: a note you can only find by
// searching for it. It deliberately does not also require the note to have no
// outbound links; a note that links out but that nothing links back to is
// exactly the case worth surfacing.
func (g *LinkGraph) Orphans() []string {
	var out []string
	for _, n := range g.notes {
		if len(g.inbound[n]) == 0 {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Neighborhood is the bounded result of a neighbourhood query.
type Neighborhood struct {
	// Nodes are the notes reached, sorted, including the origin.
	Nodes []string
	// Hops is the hop limit actually applied.
	Hops int
	// MaxNodes is the node limit actually applied.
	MaxNodes int
	// HopsClamped reports that the requested hop count exceeded the cap and
	// was reduced (FR-037's principle, applied to FR-054's bound).
	HopsClamped bool
	// NodesClamped reports that traversal stopped because the node cap was
	// reached, so the result is a subset of the true neighbourhood.
	NodesClamped bool
}

// Neighborhood returns the notes within hops link-steps of relPath, following
// links in both directions, bounded by both caps.
//
// Both bounds are clamped and REPORTED rather than silently applied: a caller
// that asked for 5 hops and got 3 must be able to tell, or it will read a
// truncated neighbourhood as a complete one — the same class of dishonesty
// US-6 forbids for partial search results.
func (g *LinkGraph) Neighborhood(relPath string, hops, maxNodes int) Neighborhood {
	out := Neighborhood{Hops: hops, MaxNodes: maxNodes}
	if hops < 0 {
		out.Hops = 0
	}
	if out.Hops > MaxNeighborhoodHops {
		out.Hops = MaxNeighborhoodHops
		out.HopsClamped = true
	}
	if maxNodes <= 0 || maxNodes > MaxNeighborhoodNodes {
		if maxNodes > MaxNeighborhoodNodes {
			out.NodesClamped = true
		}
		out.MaxNodes = MaxNeighborhoodNodes
	}

	origin := normalizeRel(relPath)
	seen := map[string]struct{}{origin: {}}
	frontier := []string{origin}
	nodes := []string{origin}

	for hop := 0; hop < out.Hops && len(frontier) > 0; hop++ {
		var next []string
		for _, cur := range frontier {
			neighbours := make([]string, 0, len(g.outbound[cur])+len(g.inbound[cur]))
			for _, l := range g.outbound[cur] {
				if l.State == ResolveResolved {
					neighbours = append(neighbours, l.To)
				}
			}
			for _, l := range g.inbound[cur] {
				neighbours = append(neighbours, l.From)
			}
			sort.Strings(neighbours)
			for _, n := range neighbours {
				if _, dup := seen[n]; dup {
					continue
				}
				if len(nodes) >= out.MaxNodes {
					out.NodesClamped = true
					sort.Strings(nodes)
					out.Nodes = nodes
					return out
				}
				seen[n] = struct{}{}
				nodes = append(nodes, n)
				next = append(next, n)
			}
		}
		frontier = next
	}
	sort.Strings(nodes)
	out.Nodes = nodes
	return out
}
