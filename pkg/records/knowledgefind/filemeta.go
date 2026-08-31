// Omnipus — ADR-068 D24.2 / spec FR-130..FR-132: assembling one candidate's
// file metadata from the parent row and the two child streams.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// THE ASSEMBLY STRATEGY IS FR-131'S, AND IT IS NAMED BECAUSE THE OBVIOUS ONE
// IS WRONG.
//
// `note_props`, `note_tags` and `note_links` are three children of one parent.
// Joining any two of them into the candidate statement returns their CARTESIAN
// PRODUCT — a note with 30 properties, 10 tags and 40 links yields 12,000 rows
// where the truth is 30 — and every aggregate over that is wrong by the same
// factor. D16.6 fixed exactly this fan-out once already, and FR-131 forbids its
// return by naming the alternative: each child is streamed by its OWN statement
// under the SAME narrowing, and the note is assembled in Go, keyed by path.
//
// So this file runs the child streams as PREPASSES and holds a per-path index
// of what they returned. The candidate stream then stays what it was — one
// record at a time, no extra join, no extra rows.
//
// EVERY PREPASS IS CONDITIONAL. A query that names no `file.tags` never opens
// the tag stream; one that names no link property never opens the link stream;
// and `file.backlinks` — much the most expensive of the three — is derived only
// when something actually asked for it. A view that sorts by `file.mtime` reads
// three columns of the parent row and opens nothing at all, because FR-131 puts
// mtime/ctime/size ON the parent (Candidate.File) precisely so the commonest
// Bases view costs no child scan.
//
// RULING R-A IS UNTOUCHED. Every stream here is narrowed by a Selector — a
// record type, a kind, a path prefix — and decided in Go afterwards. No
// `file.*` predicate is expressible in a Selector, so none reaches SQL.
// ---------------------------------------------------------------------------

// maxBufferedChildRows bounds what a prepass may hold.
//
// FOR `note_links` THIS IS FR-132's OWN NUMBER (records.MaxBacklinkEdges,
// 200,000): the same table, the same edges, the bound the specification already
// states over them. It is read from that variable rather than copied, so a test
// that shrinks the bound shrinks this too and the two can never quote different
// figures.
//
// FOR `note_tags` IT IS THE SAME NUMBER APPLIED TO THE SIBLING CHILD TABLE, AND
// THAT IS AN EXTENSION RATHER THAN A REQUIREMENT — the specification bounds
// B1 (candidates), B2 (survivors), B3 (a summary's buffered column) and
// FR-132's edges, and names no bound for a tag prepass. An unbounded buffer
// here would be a fifth way to exhaust memory with no refusal attached, which
// is worse than a bound whose provenance is stated. It is recorded as a gap for
// ratification rather than presented as compliance.
func maxBufferedChildRows() int { return records.MaxBacklinkEdges }

// fileMetaSource is one query's assembled view of the child tables.
//
// A nil *fileMetaSource is legal and means "this query names no file property":
// meta() then still answers, from the parent row alone, so a formula that
// reaches an unanticipated file property gets a well-formed FileMeta rather
// than a nil dereference.
type fileMetaSource struct {
	// tags and links are keyed by the note's PATH — the key the candidate
	// stream and both child streams agree on. RecordID would not do: an
	// ordinary note has none (FR-005) and is exactly the case `file.*` exists
	// to serve.
	tags  map[string][]string
	links map[string][]records.FileLinkRow

	// backlinks is FR-132's derived inverse, built ONCE per query over the
	// caller's WORKSPACE scope. Building it per candidate would re-scan the
	// workspace once per note.
	backlinks *records.BacklinkIndex

	wantTags      bool
	wantLinks     bool
	wantBacklinks bool
}

// newFileMetaSource runs whichever prepasses this query actually needs.
//
// It returns a *RefusalError rather than a partial index, because FR-154's rule
// generalises: an answer computed over half the tags is a confidently wrong
// answer, and there is no honest way to render it.
func newFileMetaSource(ctx context.Context, d Deps, q *query) (*fileMetaSource, *RefusalError) {
	touched := q.touchedFileProperties()
	src := &fileMetaSource{
		wantTags:      touched[records.FileTagsProp],
		wantLinks:     touched[records.FileLinksProp] || touched[records.FileEmbedsProp],
		wantBacklinks: touched[records.FileBacklinksProp],
	}
	if !src.wantTags && !src.wantLinks && !src.wantBacklinks {
		return src, nil
	}

	sel := q.selector(d.PathPrefix)

	if src.wantTags {
		if r := src.loadTags(ctx, d, sel); r != nil {
			return nil, r
		}
	}
	if src.wantLinks {
		if r := src.loadLinks(ctx, d, sel); r != nil {
			return nil, r
		}
	}
	if src.wantBacklinks {
		if r := src.deriveBacklinks(ctx, d); r != nil {
			return nil, r
		}
	}
	return src, nil
}

func (s *fileMetaSource) loadTags(ctx context.Context, d Deps, sel propindex.Selector) *RefusalError {
	type ordered struct {
		elem int
		tag  string
	}
	staging := map[string][]ordered{}
	rows := 0
	limit := maxBufferedChildRows()

	err := d.Store.Tags(ctx, sel, func(h propindex.TagHit) error {
		rows++
		if rows > limit {
			// MID-SCAN, for near.go's reason: a bound that checks afterwards
			// has already paid for the memory it exists to refuse.
			return &childBufferError{table: "note_tags", property: records.FileTagsProp, count: rows, limit: limit}
		}
		staging[h.Path] = append(staging[h.Path], ordered{elem: h.Tag.Elem, tag: h.Tag.Tag})
		return nil
	})
	if err != nil {
		return childStreamRefusal(err, records.FileTagsProp)
	}

	s.tags = make(map[string][]string, len(staging))
	for path, list := range staging {
		// Sorted by the note's OWN element order, so `file.tags` renders in the
		// order the file wrote them. Map iteration is randomised and a stream's
		// row order is the planner's business, so neither can be relied on: an
		// unsorted column reorders between two identical calls and reads as
		// data changing.
		sort.SliceStable(list, func(i, j int) bool { return list[i].elem < list[j].elem })
		out := make([]string, 0, len(list))
		for _, o := range list {
			out = append(out, o.tag)
		}
		s.tags[path] = out
	}
	return nil
}

func (s *fileMetaSource) loadLinks(ctx context.Context, d Deps, sel propindex.Selector) *RefusalError {
	staging := map[string][]propindex.LinkRow{}
	rows := 0
	limit := maxBufferedChildRows()

	err := d.Store.Links(ctx, sel, func(h propindex.LinkHit) error {
		rows++
		if rows > limit {
			return &childBufferError{table: "note_links", property: records.FileLinksProp, count: rows, limit: limit}
		}
		staging[h.Path] = append(staging[h.Path], h.Link)
		return nil
	})
	if err != nil {
		return childStreamRefusal(err, records.FileLinksProp)
	}

	s.links = make(map[string][]records.FileLinkRow, len(staging))
	for path, list := range staging {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Elem < list[j].Elem })
		out := make([]records.FileLinkRow, 0, len(list))
		for _, r := range list {
			out = append(out, records.FileLinkRow{
				NotePath: path,
				Target:   r.Target,
				Heading:  r.Heading,
				Display:  r.Display,
				Raw:      r.Raw,
				Embed:    r.Embed,
			})
		}
		s.links[path] = out
	}
	return nil
}

// deriveBacklinks is FR-132, and the SCOPE is the requirement's own, not the
// view's.
//
// The selector carries the caller's PathPrefix and NOTHING ELSE — no record
// type, no kind. Obsidian's backlinks are vault-wide; the closest reading that
// does not cross FR-062's workspace boundary is "vault-wide within the caller's
// workspace scope", and narrowing by the view's own type would answer a
// different question: "which DEALS link here", presented as "what references
// this". A note's references do not depend on what the reader happened to be
// filtering for.
func (s *fileMetaSource) deriveBacklinks(ctx context.Context, d Deps) *RefusalError {
	scope := records.BacklinkScope{PathPrefix: d.PathPrefix}
	sel := propindex.Selector{PathPrefix: d.PathPrefix}

	ix, err := records.BuildBacklinkIndex(scope, func(visit func(records.FileLinkRow) error) error {
		return d.Store.Links(ctx, sel, func(h propindex.LinkHit) error {
			return visit(records.FileLinkRow{
				NotePath: h.Path,
				Target:   h.Link.Target,
				Heading:  h.Link.Heading,
				Display:  h.Link.Display,
				Raw:      h.Link.Raw,
				Embed:    h.Link.Embed,
			})
		})
	})
	if err != nil {
		var bound *records.BacklinkBoundError
		if errors.As(err, &bound) {
			p := problem(generated.EvaluationBoundExceeded, bound.Error(),
				"narrow the workspace scope, or drop file.backlinks from the query")
			p.Property = str(records.FileBacklinksProp)
			return refuse(p, err)
		}
		p := problem(generated.IndexUnavailable,
			fmt.Sprintf("the properties index could not derive %s: %v", records.FileBacklinksProp, err),
			"run knowledge_describe check_integrity")
		p.Property = str(records.FileBacklinksProp)
		return refuse(p, err)
	}
	s.backlinks = ix
	return nil
}

// meta assembles one candidate's FileMeta.
//
// It reads facts and nothing else: the parent row's path and three stat
// columns, the prepass indexes, and the candidate's own property key set. There
// is no store handle in scope here and there must never be one — that absence
// is what makes "no file.* predicate reaches SQL" a structural property rather
// than a rule someone has to remember.
func (s *fileMetaSource) meta(c propindex.Candidate) records.FileMeta {
	m := records.FileMeta{
		Path: c.Path,
		// FR-133's honest absence rides through untouched: Candidate.File
		// carries HasBirthTime, and a platform that records no birth time
		// leaves CtimeKnown false rather than substituting st_ctime.
		Mtime:      c.File.ModTime,
		MtimeKnown: c.File.Known,
		Ctime:      c.File.BirthTime,
		CtimeKnown: c.File.HasBirthTime,
		Size:       c.File.Size,
		SizeKnown:  c.File.Known,
		// FR-130: `file.properties` is the note's frontmatter KEY SET, read
		// from FR-021e's rows — which exist for every note, typed or not, so
		// this is a real storage source rather than a description.
		PropertyKeys:   append([]string(nil), c.PropOrder...),
		PropertyValues: propertyValueMap(c),
	}
	if s == nil {
		return m
	}
	if s.tags != nil {
		m.Tags = s.tags[c.Path]
	}
	if s.links != nil {
		m.Links, m.Embeds = records.SplitLinkRows(s.links[c.Path])
	}
	if s.backlinks != nil {
		// Apply sets BOTH the values and the derived flag, which is why it is
		// called rather than the fields being assigned: a caller that set the
		// values and forgot the flag would make a correct empty answer
		// indistinguishable from an uncomputed one.
		s.backlinks.Apply(&m)
	}
	return m
}

// propertyValueMap is FR-130's "the full map is a formula operand".
//
// It is the note's frontmatter as TEXT, never as typed values: it is never read
// by resolution or comparison — `file.properties` compares over the key set —
// and giving it typed values would invite a second, laxer decode path beside
// the comparator's.
func propertyValueMap(c propindex.Candidate) map[string]string {
	if len(c.PropOrder) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.PropOrder))
	for _, name := range c.PropOrder {
		sp, ok := c.Prop(name)
		if !ok {
			continue
		}
		parts := make([]string, 0, len(sp.Elems))
		for _, e := range sp.Elems {
			text := e.Raw
			if text == "" {
				text = e.Text
			}
			parts = append(parts, text)
		}
		out[name] = joinComma(parts)
	}
	return out
}

func joinComma(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}

// childBufferError aborts a prepass at the row that crosses the bound.
type childBufferError struct {
	table    string
	property string
	count    int
	limit    int
}

func (e *childBufferError) Error() string {
	return fmt.Sprintf(
		"%s buffers the %s child table and reached its row bound mid-scan at %s rows; the bound is %s",
		e.property, e.table, group3(e.count), group3(e.limit))
}

// childStreamRefusal turns a prepass failure into the response the model reads.
func childStreamRefusal(err error, property string) *RefusalError {
	var over *childBufferError
	if errors.As(err, &over) {
		p := problem(generated.EvaluationBoundExceeded, over.Error(),
			"narrow the scope to a collection or path, or drop "+property+" from the query")
		p.Property = str(property)
		return refuse(p, err)
	}
	p := problem(generated.IndexUnavailable,
		fmt.Sprintf("the properties index could not stream the child table behind %s: %v", property, err),
		"run knowledge_describe check_integrity")
	p.Property = str(property)
	return refuse(p, err)
}
