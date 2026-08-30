// Omnipus — ADR-068 D15.3 / spec §4.1.3: knowledge_read, a note or one section of
// one. THE LOGIC HALF — the tool ADAPTER is in tools.go.
//
// Same split as knowledge_describe.go, and the same guard enforces it:
// TestKnowledge_NoLanguageModelInTheGraphPath (links_test.go) confines
// pkg/tools to the tool-adapter files. This file holds the response shape,
// the render function and the pure byte/heading logic a section read needs,
// and imports no `pkg/tools`.
//
// ---------------------------------------------------------------------------
// THE VERSION TOKEN — WHY THIS ONE, AND NOT source_hash
//
// FR-074: every successful knowledge_read carries a version token knowledge_edit
// accepts unchanged (AC-R1), and obtaining one must never require sending a
// write expected to fail (AC-R2). That token is `knowledge.VersionToken`
// (version.go) via ComputeVersionToken — the SAME compare-and-swap primitive
// EditNote/Writer.WriteNote already consume through ExpectVersion. It is
// computed HERE, in this process, from the exact bytes this response is
// rendered from — never read back from an index — so the token and the
// content it describes can never disagree by construction.
//
// It is deliberately NOT propindex's `source_hash` (pkg/records/propindex/
// rows.go SourceHash: hex(SHA-256(bytes)), no prefix, no length suffix). The
// two solve DIFFERENT problems and encode differently on purpose:
//
//   - source_hash answers "do the SQLite properties index and the bleve text
//     index agree about this note's bytes" (D16.5) — a FRESHNESS comparison
//     between two DERIVED indexes, recomputed on every indexing pass and
//     never sent back to a caller.
//   - VersionToken answers "is the file on disk, right now, the exact bytes a
//     caller last read" — an optimistic-concurrency CAS token, read directly
//     off disk with no index in the loop at all, so it is correct even when
//     both indexes are stale, rebuilding, or absent (a SQLite-less build,
//     FR-020h — knowledge_read keeps working there precisely because its token
//     never touches the properties index).
//
// Sharing one hash between those two jobs would mean a write's safety
// depended on the derived index being fresh, which is exactly the freshness
// problem the token exists to be independent of. And the encodings already
// differ (versionTokenPrefix + a truncated, length-domain-separated digest,
// vs. a bare full hex digest) — collapsing them would either change a
// contract both WriteNote and propindex already ship, or silently produce two
// values that happen to look alike and compare unequal forever.
// ---------------------------------------------------------------------------
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ReadDefaultMaxBytes is FR-072a's default bound on a knowledge_read response's
// body/section content. 2.5x FR-127's 16,000-byte hard cap for knowledge_find,
// because a note is not a result set (FR-072a) — the version token, the
// typed-frontmatter block and every refusal string are OUTSIDE this bound and
// always emitted in full.
const ReadDefaultMaxBytes = 40_000

// The four members `include` accepts, spec §4.1.3's parameter table.
const (
	ReadIncludeFrontmatter = "frontmatter"
	ReadIncludeBody        = "body"
	ReadIncludeLinks       = "links"
	ReadIncludeBacklinks   = "backlinks"
)

// ReadIncludeOrder is every member `include` accepts, in the order they
// render. A list rather than a hardcoded render sequence so a test can assert
// the rendered order against this list rather than a transcription of it —
// the same device knowledge_describe's describeSectionOrder uses.
var ReadIncludeOrder = []string{ReadIncludeFrontmatter, ReadIncludeBody, ReadIncludeLinks, ReadIncludeBacklinks}

// ---------------------------------------------------------------------------
// The data the renderer projects
// ---------------------------------------------------------------------------

// ReadProperty is one frontmatter property, ready to render.
type ReadProperty struct {
	// Key is the property name exactly as the file spells it (document order
	// is Properties' slice order, not this field).
	Key string
	// Declared is true when the note's own schema (its `type:`, resolved
	// against this collection's declared record types) declares this
	// property. False for an ordinary note and for an undeclared key on a
	// record — both render their RAW lexical value, untyped.
	Declared bool
	// Value is the rendered value: the typed rendering (records.TypedValue.
	// String, comma-joined for a `many` property) when Declared, the raw
	// source text otherwise. "(no conforming value)" when every element was
	// non-conforming and none survived — the Findings below say why.
	Value string
	// Findings holds AC-R3's per-property violations, human-readable, empty
	// when the property is clean. A finding never blocks the read (AC-R3):
	// it is flagged in place, alongside the value it faults.
	Findings []string
}

// ReadLink is one resolved or unresolved wikilink, in knowledge_read's own
// rendering shape — a thin, renderer-owned projection of ResolvedLink so this
// file needs no knowledge of graph.go's resolution internals beyond the
// fields it prints.
type ReadLink struct {
	// To is the collection-relative target path. Empty when unresolved.
	To string
	// From is the collection-relative path of the note carrying the link.
	// Populated on both Links and Backlinks; the renderer decides which side
	// to print.
	From string
	// Form is the link exactly as written (ResolvedLink.Raw).
	Form       string
	Alias      string
	Heading    string
	Resolved   bool
	Reason     string
	Ambiguous  bool
	Candidates []string
	Line       int
}

// ReadData is everything one knowledge_read response is rendered from — the
// projection source, exactly as DescribeData is for knowledge_describe. Render
// reaches past this for nothing, which is what makes it testable with no
// filesystem at all.
type ReadData struct {
	// Path is the collection-relative path read.
	Path string
	// Version is FR-074's version token (ADR-067 D14 encoding — see the file
	// header). It is ALWAYS present on a successful read and is outside
	// max_bytes (FR-072a).
	Version string

	// TypeName is the note's declared record type ("" for an ordinary note —
	// FR-005). TypeRecognised is true only when TypeName names a schema this
	// collection actually declares; a declared-but-unknown type still reads
	// (AC-R3's posture extended to the type itself), with every property
	// rendered raw, same as an ordinary note.
	TypeName       string
	TypeRecognised bool

	// Included records which of ReadIncludeOrder the caller asked for, so the
	// renderer never manufactures a section that was not requested.
	Included map[string]bool

	// FrontmatterPresent is false for a note with no frontmatter block at
	// all — an ordinary note, never an error (FR-005).
	FrontmatterPresent bool
	// FrontmatterParseError is set when the block exists but its bytes could
	// not be read as YAML (FindingFrontmatterUnreadable's territory) or its
	// opening fence has no closing fence. The note still reads (mirrors
	// records.ParseRecord's own "never returns an error" contract): Body is
	// still the correct bytes, Properties is simply empty.
	FrontmatterParseError string
	// FrontmatterProblems holds structural complaints ParseFrontmatter itself
	// surfaces — duplicate keys, most importantly — that are not fatal to
	// parsing but must still reach the caller.
	FrontmatterProblems []string
	// Properties is in the file's own document order, one entry per
	// frontmatter key, present regardless of whether each is declared.
	Properties []ReadProperty

	// Section is the heading requested, "" for the whole note. IsSection
	// distinguishes an explicit empty request from "no section" — spec
	// US-9.2: "the token still covers the whole file" either way; only the
	// body window changes.
	Section   string
	IsSection bool
	// Body is the (possibly truncated) content returned. BodyTotalBytes is
	// the UNTRUNCATED length of what was selected — the whole note's body, or
	// just the section — so a truncated response still states the true size.
	Body           string
	BodyTotalBytes int
	BodyTruncated  bool
	// MaxBytes is the bound that was applied (ReadDefaultMaxBytes unless the
	// caller overrode it), stated in the header whether or not it bound
	// anything, so a caller reading the response never has to guess what was
	// asked for (FR-072a: "truncation is stated in the header, never silent").
	MaxBytes int

	Links     []ReadLink
	Backlinks []ReadLink
}

// ---------------------------------------------------------------------------
// The renderer
// ---------------------------------------------------------------------------

// RenderRead renders one response as compact text (FR-072). No JSON document
// is emitted here — the one narrowing FR-072 itself states (revision 5): a
// note's own body may legitimately contain a fenced JSON block, and that is
// the note's bytes, not this envelope's.
func RenderRead(d ReadData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — version %s\n", d.Path, d.Version)
	switch {
	case d.TypeName == "":
		// Ordinary note (FR-005): no TYPE line at all, rather than "TYPE: ".
	case d.TypeRecognised:
		fmt.Fprintf(&b, "TYPE: %s\n", d.TypeName)
	default:
		fmt.Fprintf(&b, "TYPE: %s (not a declared record type in this vault — read as an ordinary note)\n", d.TypeName)
	}

	if d.Included[ReadIncludeFrontmatter] {
		renderReadFrontmatter(&b, d)
	}
	if d.Included[ReadIncludeBody] {
		renderReadBody(&b, d)
	}
	if d.Included[ReadIncludeLinks] {
		renderReadLinks(&b, "LINKS", d.Links, false)
	}
	if d.Included[ReadIncludeBacklinks] {
		renderReadLinks(&b, "BACKLINKS", d.Backlinks, true)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func renderReadFrontmatter(b *strings.Builder, d ReadData) {
	if !d.FrontmatterPresent {
		if d.FrontmatterParseError != "" {
			fmt.Fprintf(b, "FRONTMATTER: could not be parsed — %s; the note still reads, shown as plain body\n",
				d.FrontmatterParseError)
			return
		}
		b.WriteString("FRONTMATTER: none — this note has no frontmatter block\n")
		return
	}
	fmt.Fprintf(b, "FRONTMATTER (%d):\n", len(d.Properties))
	width := 0
	for _, p := range d.Properties {
		if len(p.Key) > width {
			width = len(p.Key)
		}
	}
	for _, p := range d.Properties {
		line := fmt.Sprintf("  %-*s  %s", width, p.Key, p.Value)
		b.WriteString(strings.TrimRight(line, " ") + "\n")
		for _, f := range p.Findings {
			fmt.Fprintf(b, "    INVALID: %s\n", f)
		}
	}
	for _, prob := range d.FrontmatterProblems {
		fmt.Fprintf(b, "  PROBLEM: %s\n", prob)
	}
}

func renderReadBody(b *strings.Builder, d ReadData) {
	label := "BODY"
	if d.IsSection {
		label = fmt.Sprintf("SECTION %q", d.Section)
	}
	if d.BodyTruncated {
		fmt.Fprintf(b, "%s (%s of %s bytes, TRUNCATED — read narrower with section=<heading> or raise max_bytes):\n",
			label, group(len(d.Body)), group(d.BodyTotalBytes))
	} else {
		fmt.Fprintf(b, "%s (%s bytes):\n", label, group(d.BodyTotalBytes))
	}
	b.WriteString(d.Body)
	if !strings.HasSuffix(d.Body, "\n") {
		b.WriteString("\n")
	}
}

func renderReadLinks(b *strings.Builder, label string, links []ReadLink, backlinks bool) {
	fmt.Fprintf(b, "%s (%d)", label, len(links))
	if len(links) == 0 {
		b.WriteString(" — none\n")
		return
	}
	b.WriteString(":\n")
	for _, l := range links {
		arrow, peer := "->", l.To
		if backlinks {
			arrow, peer = "<-", l.From
		}
		switch {
		case !l.Resolved:
			fmt.Fprintf(b, "  %s (unresolved) %s", arrow, l.Form)
			if l.Reason != "" {
				fmt.Fprintf(b, " — %s", l.Reason)
			}
		default:
			fmt.Fprintf(b, "  %s %s", arrow, peer)
			if l.Alias != "" {
				fmt.Fprintf(b, " %q", l.Alias)
			}
			if l.Heading != "" {
				fmt.Fprintf(b, " #%s", l.Heading)
			}
		}
		if l.Ambiguous && len(l.Candidates) > 0 {
			fmt.Fprintf(b, " (ambiguous: also matches %s)", strings.Join(l.Candidates, ", "))
		}
		if l.Line > 0 {
			fmt.Fprintf(b, " (line %d)", l.Line)
		}
		b.WriteString("\n")
	}
}

// ---------------------------------------------------------------------------
// Section addressing
// ---------------------------------------------------------------------------

// matchSectionQuery normalises a caller-supplied `section` argument for
// comparison against Heading.Text, which is already marker- and
// whitespace-stripped. A caller may spell it "## Pricing" or "Pricing" —
// both must find the same heading, because the refusal string this tool
// emits (readSectionRefusalText) echoes headings WITH their markers, and an
// agent that copies that text back verbatim must not be refused a second
// time for spelling it the way it was just told to.
func matchSectionQuery(raw string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(raw), "#"))
}

// findHeadingSpan locates one section's byte span within the FULL note
// content (frontmatter included) — Heading.Offset is content-relative, not
// body-relative, because ExtractHeadings/the note scanner tracks the
// frontmatter block itself and simply never emits a heading from inside it.
//
// The span runs from the heading's own line to the next heading at the SAME
// OR SHALLOWER level, or to the end of the note — the ordinary "a section
// includes its own subsections" reading of ATX heading levels.
//
// ok is false when no heading's Text matches query under
// matchSectionQuery's normalisation; the caller is a refusal in that case.
func findHeadingSpan(content []byte, headings []Heading, query string) (start, end int, ok bool) {
	want := matchSectionQuery(query)
	idx := -1
	for i, h := range headings {
		if h.Text == want {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, 0, false
	}
	target := headings[idx]
	start = int(target.Offset)
	end = len(content)
	for j := idx + 1; j < len(headings); j++ {
		if headings[j].Level <= target.Level {
			end = int(headings[j].Offset)
			break
		}
	}
	return start, end, true
}

// readSectionRefusalText is spec §4.1.3's refusal wording: "no section '##
// Pricing' in Deals/Acme.md; headings: ## Summary, ## Terms, ## Notes" — the
// headings are rendered WITH their own level markers, so an agent that
// echoes one back verbatim names a heading matchSectionQuery accepts.
func readSectionRefusalText(path, requested string, headings []Heading) string {
	if len(headings) == 0 {
		return fmt.Sprintf("no section '%s' in %s; this note has no headings", requested, path)
	}
	names := make([]string, len(headings))
	for i, h := range headings {
		names[i] = strings.Repeat("#", h.Level) + " " + h.Text
	}
	return fmt.Sprintf("no section '%s' in %s; headings: %s", requested, path, strings.Join(names, ", "))
}

// readSectionError is the typed refusal findHeadingSpan's failure becomes.
// Typed (rather than fmt.Errorf'd inline) so a test can construct the exact
// same string via readSectionRefusalText without re-deriving it from a
// formatted error's text.
type readSectionError struct {
	path      string
	requested string
	headings  []Heading
}

func (e *readSectionError) Error() string {
	return readSectionRefusalText(e.path, e.requested, e.headings)
}

// ---------------------------------------------------------------------------
// Frontmatter projection (AC-R3: a schema violation is flagged, never a
// refusal to read)
// ---------------------------------------------------------------------------

// projectReadProperties renders every frontmatter key in the file's own
// document order. A key the schema declares is resolved and typed
// (records.ResolveProperty — the exact function validate.go's own
// schema-conformance check uses, so this can never disagree with
// knowledge_describe's check_integrity about what is wrong with a value). A key
// the schema does not declare — including every key on an ordinary note,
// which declares no schema at all — renders its raw lexical text, untyped.
func projectReadProperties(rec records.Record, fm records.Frontmatter, schema *records.Schema) []ReadProperty {
	out := make([]ReadProperty, 0, len(fm.Keys))
	for _, key := range fm.Keys {
		node := fm.Values[key]
		prop := ReadProperty{Key: key}
		if schema != nil {
			if p, ok := schema.Property(key); ok {
				prop.Declared = true
				pv := records.ResolveProperty(rec, p)
				prop.Value = renderPropertyValue(pv)
				for _, f := range pv.Findings {
					prop.Findings = append(prop.Findings, findingText(f))
				}
				out = append(out, prop)
				continue
			}
		}
		prop.Value = renderRawNode(node)
		out = append(out, prop)
	}
	return out
}

// renderPropertyValue renders a resolved, typed property's conforming
// values — comma-joined for a `many` property, exactly one for a scalar.
// Values is already filtered to the conforming elements (PropertyValue's own
// contract): a non-conforming element is reported via Findings, never mixed
// into this string.
func renderPropertyValue(pv records.PropertyValue) string {
	if len(pv.Values) == 0 {
		if pv.State == records.StateNonConforming {
			return "(no conforming value)"
		}
		return ""
	}
	parts := make([]string, len(pv.Values))
	for i, v := range pv.Values {
		parts[i] = v.String()
	}
	return strings.Join(parts, ", ")
}

// findingText renders one records.Finding in place, alongside the property
// it faults — the reason, the expected shape and the permitted set (FR-011),
// exactly as Finding.String renders them, minus the path and property name
// this response has already stated once (the property line above it).
func findingText(f records.Finding) string {
	s := f.Reason
	if f.Expected != "" {
		s += "; expected " + f.Expected
	}
	if len(f.Permitted) > 0 {
		s += "; permitted values are " + strings.Join(f.Permitted, ", ")
	}
	if f.ElementIndex >= 0 {
		s = fmt.Sprintf("[%d] %s", f.ElementIndex, s)
	}
	return s
}

// renderRawNode renders one frontmatter value with NO type interpretation —
// what an undeclared key, or any key on an ordinary note, gets. It never
// drops information silently: a mapping (a shape no property type accepts)
// still renders its own key names rather than vanishing from the response.
func renderRawNode(n records.Node) string {
	switch n.Kind {
	case records.KindNull:
		return "(empty)"
	case records.KindScalar:
		return n.Text
	case records.KindSequence:
		parts := make([]string, 0, len(n.Items))
		for _, it := range n.Items {
			parts = append(parts, rawNodeScalar(it))
		}
		return strings.Join(parts, ", ")
	case records.KindMapping:
		return "(mapping: " + strings.Join(n.Keys, ", ") + ")"
	default:
		return n.Text
	}
}

// rawNodeScalar renders one element of a raw sequence. A nested sequence
// cannot appear here (frontmatter.go's Node has no sequence-of-sequence
// case in the property-key positions ParseFrontmatter builds), but the
// fallback stays honest rather than panicking if that ever changes.
func rawNodeScalar(n records.Node) string {
	switch n.Kind {
	case records.KindScalar:
		return n.Text
	case records.KindNull:
		return "(empty)"
	case records.KindMapping:
		return "(mapping: " + strings.Join(n.Keys, ", ") + ")"
	default:
		return "(nested list)"
	}
}

// ---------------------------------------------------------------------------
// Byte-safe truncation (FR-072a)
// ---------------------------------------------------------------------------

// truncateUTF8 cuts s to at most n bytes without splitting a multi-byte rune,
// walking back at most 3 bytes (the longest a UTF-8 continuation run gets).
func truncateUTF8(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
