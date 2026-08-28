// Omnipus — ADR-068 D21.2: what an index document is made of, field by field.
//
// Before this file, `indexDoc` was five fields — path, name, kind, offset,
// body — and FRONTMATTER WAS NOT STRIPPED. `status: prospect` reached the index
// as the loose prose tokens "status" and "prospect" sitting in the body, which
// has two consequences and D21.2 names both:
//
//   - a field query on a property key was IMPOSSIBLE, not slow. There was no
//     field to query.
//   - BM25F-style weighting had nothing to weight. A title match could not
//     outrank a passing body mention, because there was no title field.
//
// And the honesty layer still reported Complete: true, because completeness
// meant "the index held every file", never "every relevant fact was found".
//
// # THE CLOSED MAPPING, AND WHY PROPERTY NAMES DO NOT REOPEN IT
//
// buildIndexMapping declares Dynamic=false, IndexDynamic=false,
// StoreDynamic=false. Property names are runtime data an operator invents. The
// obvious reading of "index property keys as fields" is therefore a dynamic
// mapping — and ADR-068's D16 revision table is a record of storage decisions
// made by assuming, four times.
//
// SO THE MAPPING STAYS CLOSED AND THE RUNTIME DATA BECOMES TERMS, NEVER FIELD
// NAMES. A note declaring `status: prospect` contributes the TERM "status" to
// the fixed field `prop_key`, the TERM "status=prospect" to the fixed field
// `prop`, and the text "prospect" to the fixed field `prop_value`. Ten thousand
// distinct property names across a vault add ten thousand terms to three
// fields; they add no fields, no mapping entries and no drift-guard surface.
// This is the ordinary inverted-index answer to a high-cardinality attribute
// and it is why no dynamic mapping is needed here at all.
//
// # ONE PARSER, NOT TWO
//
// The frontmatter is parsed by pkg/records.ParseFrontmatter — the same parser
// the typed record layer uses — rather than by a line scanner written here.
// pkg/records imports nothing from Omnipus, so the direction knowledge→records
// is acyclic; pkg/records/propindex already depends on both. The alternative is
// a second frontmatter parser whose disagreements with the first would surface
// as a record that the index cannot find and the record layer says exists.
// ADR-068 spends several pages on exactly that class of divergence.
//
// Headings are extracted by driving the package's OWN noteScanner (links.go),
// for the same reason: it already knows that a "# comment" inside a fenced code
// block is not a heading, and that frontmatter contains none. A second set of
// fence rules would drift from the first.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"bytes"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

const (
	// maxIndexedPropertyKeys bounds how many TOP-LEVEL frontmatter properties
	// one note contributes to the fielded index.
	//
	// It is a bound rather than a preference: noteFields are replicated onto
	// every segment of a note (see indexNote), so an unbounded property count
	// multiplies by the segment count. pkg/records already caps the YAML node
	// budget at 20,000 for the whole document, which bounds this indirectly —
	// but "indirectly" means a change over there silently changes the memory
	// profile over here.
	//
	// 512 is far past any real note. A note that exceeds it is indexed with its
	// first 512 properties and the truncation is LOGGED with the path, never
	// silently absorbed: an operator whose property is missing from a field
	// query has to be able to find out why.
	maxIndexedPropertyKeys = 512

	// maxIndexedPropertyValueBytes bounds one property's contribution to the
	// prop_value text. A frontmatter value can be a pasted document; indexing
	// it as a property value would put it in the index twice, once here and
	// once in body.
	maxIndexedPropertyValueBytes = 4 << 10

	// maxIndexedPropertyValues bounds the elements taken from one sequence
	// property, for the same reason maxIndexedPropertyKeys bounds the keys.
	maxIndexedPropertyValues = 256

	// propPairSeparator joins a property key to its value in the `prop` field.
	// "=" is chosen because it is what a person writing a filter would type,
	// and the field is keyword-analysed so the whole string is one term.
	propPairSeparator = "="
)

// noteFields is the NOTE-level fielded data — the part that identifies the note
// rather than one segment of it.
//
// It is replicated onto every segment document, which is deliberate. Search
// collapses a note's segments into one hit scored by its BEST segment, so a
// title or property that lived only on segment 0 would be invisible to a query
// that matched segment 3, and the collapsed hit would be ranked as though the
// note had no title at all. Replication costs bytes only for notes over
// IndexSegmentSize (512 KiB), which is a handful of notes in any real vault.
type noteFields struct {
	// Title is the note's title: the frontmatter's own `title`, else the first
	// level-1 heading, else the filename stem. Never empty.
	Title string
	// PropKeys is one entry per top-level frontmatter property, case-folded.
	// This is the field that makes D21.2's exit criterion — a field query on a
	// property key — possible at all.
	PropKeys []string
	// PropValues is the property values as prose, for the `en` analyzer.
	PropValues string
	// Props is one `key=value` term per scalar property (and per element of a
	// sequence property), both halves case-folded.
	Props []string
	// Truncated reports that a bound above was hit, so the caller can say so
	// rather than shipping a quietly partial document.
	Truncated string
}

// extractNoteFields reads the head of a note and returns its note-level fields
// together with bodyStart — the offset of the first body byte, i.e. the first
// byte AFTER the frontmatter block's closing fence line.
//
// bodyStart is how frontmatter stops reaching the body field. It is a byte
// offset rather than a rewritten buffer because IndexHit.Offset is absolute and
// FR-050a's query-time excerpt re-read depends on it: the caller records
// offset+bodyStart for segment 0 and the re-read lands on the first line of
// prose instead of on "---".
//
// head is the note's first buffer, not the whole note. A frontmatter block
// larger than one buffer cannot be located here, and the honest outcome is
// bodyStart=0 with no properties — the note is still indexed in full, and its
// YAML is still findable as prose exactly as it was before this change. That is
// a documented degradation, not a silent one: the caller logs it.
//
// It NEVER fails. Unparseable frontmatter is an ordinary note with no
// properties: refusing to index a note because its YAML is malformed would
// remove it from search entirely, which is a far worse answer than indexing its
// text and reporting no properties.
func extractNoteFields(head []byte, relPath string) (nf noteFields, bodyStart int) {
	nf.Title = stemTitle(relPath)

	blk, err := fmParse(head)
	switch {
	case err != nil:
		// The block opened and its closing fence is not in this buffer. Every
		// byte we can see is metadata, so scanning it for a "# " heading would
		// promote a frontmatter tag line to the note's title. The one thing
		// worth recovering is a literal top-level `title:` line, which is what
		// the query-time title derivation has always done here.
		nf.Title = literalTitleLine(head, nf.Title)
		nf.Truncated = "the frontmatter block has no closing fence within the note's first buffer, " +
			"so no properties were indexed"
		return nf, 0
	case !blk.present:
		// An ordinary note with no frontmatter. Nothing to strip, nothing to
		// parse, and the first level-1 heading is the title.
		nf.Title = titleFromBody(head, nf.Title)
		return nf, 0
	}

	// innerEnd is the first byte of the closing fence LINE; the body starts
	// after that line's terminator.
	_, afterFence, ok := authorLineAt(head, blk.innerEnd)
	if !ok {
		afterFence = len(head)
	}
	bodyStart = afterFence

	fm, parseErr := records.ParseFrontmatter(head[:afterFence])
	if parseErr == nil && fm.Present {
		nf.applyFrontmatter(fm)
	}

	if t := frontmatterTitle(fm); t != "" {
		nf.Title = t
	} else {
		nf.Title = titleFromBody(head[bodyStart:], nf.Title)
	}
	return nf, bodyStart
}

// frontmatterTitle returns the frontmatter's own `title` scalar, or "".
func frontmatterTitle(fm records.Frontmatter) string {
	n, ok := fm.Get("title")
	if !ok || n.Kind != records.KindScalar {
		return ""
	}
	return strings.TrimSpace(n.Text)
}

// titleFromBody returns the first level-1 ATX heading in body, or fallback.
//
// It scans lines directly rather than calling ExtractHeadings because it runs
// on the head of every indexed note and only ever needs the first one; the
// fence rules do not change the answer for a "# " on the note's first content
// line, which is where a title heading is.
func titleFromBody(body []byte, fallback string) string {
	rest := body
	for len(rest) > 0 {
		line, next, ok := authorLineAt(rest, 0)
		if !ok {
			break
		}
		trimmed := strings.TrimRight(string(line), " \t\r")
		if after, found := strings.CutPrefix(trimmed, "# "); found {
			if v := strings.TrimSpace(after); v != "" {
				return v
			}
		}
		rest = rest[next:]
	}
	return fallback
}

// literalTitleLine finds a top-level `title:` line by reading lines, for the
// one case the YAML parser cannot reach: a frontmatter block whose closing
// fence is beyond the buffer we were given.
//
// Top-level means column zero. An indented `title:` belongs to some other
// mapping, and promoting it would name the note after a field of one of its
// properties.
func literalTitleLine(head []byte, fallback string) string {
	rest := head
	for len(rest) > 0 {
		line, next, ok := authorLineAt(rest, 0)
		if !ok {
			break
		}
		trimmed := strings.TrimRight(string(line), " \t\r")
		if after, found := strings.CutPrefix(trimmed, "title:"); found {
			if v := strings.Trim(strings.TrimSpace(after), `"'`); v != "" {
				return v
			}
		}
		rest = rest[next:]
	}
	return fallback
}

// applyFrontmatter turns parsed frontmatter into the three property fields.
func (nf *noteFields) applyFrontmatter(fm records.Frontmatter) {
	var values strings.Builder
	keys := fm.Keys
	if len(keys) > maxIndexedPropertyKeys {
		keys = keys[:maxIndexedPropertyKeys]
		nf.Truncated = "the note declares more than the indexable number of frontmatter properties; " +
			"only the first were indexed"
	}

	nf.PropKeys = make([]string, 0, len(keys))
	for _, key := range keys {
		folded := records.FoldKey(strings.TrimSpace(key))
		if folded == "" {
			continue
		}
		nf.PropKeys = append(nf.PropKeys, folded)

		node, ok := fm.Values[key]
		if !ok {
			continue
		}
		for _, raw := range flattenNodeValues(node) {
			if len(raw) > maxIndexedPropertyValueBytes {
				raw = raw[:maxIndexedPropertyValueBytes]
				nf.Truncated = "a frontmatter value was longer than the indexable length and was truncated"
			}
			if values.Len() > 0 {
				values.WriteByte(' ')
			}
			values.WriteString(raw)
			nf.Props = append(nf.Props, folded+propPairSeparator+records.FoldKey(raw))
		}
	}
	nf.PropValues = values.String()
	if len(nf.PropKeys) == 0 {
		nf.PropKeys = nil
	}
}

// flattenNodeValues returns the scalar texts a frontmatter value contributes.
//
// A scalar contributes itself; a sequence contributes each element; a nested
// mapping contributes its scalar leaves. A nested mapping's KEYS are not
// property keys — a property key is a top-level name, and promoting `address.city`
// to a queryable key would invent an addressing scheme no other part of the
// system understands.
//
// An explicit null contributes nothing: `status:` with no value is not the
// value "null", and indexing it as one would make a query for the text "null"
// match every note with an empty property.
func flattenNodeValues(n records.Node) []string {
	out := make([]string, 0, 1)
	appendNode(&out, n, 0)
	return out
}

// appendNode is flattenNodeValues' recursion. depth bounds it against a deeply
// nested mapping; pkg/records already bounds the node COUNT, but a bound on
// count is not a bound on stack.
func appendNode(out *[]string, n records.Node, depth int) {
	const maxDepth = 8
	if depth > maxDepth || len(*out) >= maxIndexedPropertyValues {
		return
	}
	switch n.Kind {
	case records.KindScalar:
		if v := strings.TrimSpace(n.Text); v != "" {
			*out = append(*out, v)
		}
	case records.KindSequence:
		for _, item := range n.Items {
			appendNode(out, item, depth+1)
		}
	case records.KindMapping:
		for _, key := range n.Keys {
			appendNode(out, n.Fields[key], depth+1)
		}
	case records.KindNull:
		// Deliberately nothing. See the doc comment.
	}
}

// ---------------------------------------------------------------------------
// Headings, extracted incrementally
// ---------------------------------------------------------------------------

// headingCollector yields the headings of a note SEGMENT BY SEGMENT, using the
// package's own noteScanner so the rules about fenced code blocks and
// frontmatter are the same rules links.go and the graph use.
//
// It exists because ScanNote accumulates every link and heading of a whole note
// in memory, and the indexing path is the one place in this package that has
// promised peak memory bounded by IndexSegmentSize rather than by file size
// (FR-034a). This drains BOTH slices after every segment, so the scanner's
// retention is bounded by one segment's worth of headings rather than by the
// note's.
type headingCollector struct {
	sc *noteScanner
}

func newHeadingCollector() *headingCollector {
	return &headingCollector{sc: &noteScanner{line: 1, atLineStart: true}}
}

// feed processes one segment's bytes and returns the heading TEXT found in it,
// space-joined, ready for the `headings` field.
//
// base is the segment's absolute offset in the note.
//
// It does NOT need to be told whether the segment starts on a line boundary.
// indexNote's line-aware cut usually makes it one, but when a single line is
// longer than a whole segment the cut is hard — and in that case this method's
// own final feed for the previous segment was made with complete=false, which
// is precisely what leaves noteScanner.atLineStart false across the seam. The
// state is carried by the scanner rather than restated by the caller, so the
// two cannot disagree.
func (h *headingCollector) feed(seg []byte, base int64) string {
	pos := 0
	for pos < len(seg) {
		nl := bytes.IndexByte(seg[pos:], '\n')
		if nl < 0 {
			h.sc.feed(seg[pos:], base+int64(pos), false)
			break
		}
		h.sc.feed(seg[pos:pos+nl], base+int64(pos), true)
		pos += nl + 1
	}

	var b strings.Builder
	for _, hd := range h.sc.headings {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(hd.Text)
	}
	// Drain both. The links are not wanted here at all, and leaving them to
	// accumulate would make the scanner's retention a property of the note's
	// size — the exact thing FR-034a forbids on this path.
	h.sc.headings = h.sc.headings[:0]
	h.sc.links = h.sc.links[:0]
	return b.String()
}
