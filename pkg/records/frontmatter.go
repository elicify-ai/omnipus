// Omnipus — ADR-068 D1: reading a record's frontmatter WITHOUT losing its lexical form.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHY IT DOES NOT UNMARSHAL INTO `any`
//
// The obvious implementation is `yaml.Unmarshal(block, &map[string]any{})`.
// That implementation cannot satisfy FR-020b, and the reason is worth stating
// because it is invisible at the call site:
//
//	yaml.Unmarshal([]byte("arr: 349.98"), &m)   //  m["arr"] is a float64
//
// By the time our code sees the value, 349.98 has already become
// 349.97999999999996 and the exactness promise is gone — not by our arithmetic,
// but by the decoder's. The same applies to `number`: DS-1 requires 2^53+1 to
// survive exactly, and an `any` decode of 9007199254740993 that YAML tags as an
// int is fine, but one written as 9007199254740993.0 is not.
//
// So we decode into yaml.Node, which preserves the SOURCE TEXT of every scalar,
// and every numeric type in this package parses from that text (decimal.go).
// Nothing here ever holds a float.
//
// This also gives us two things a map[string]any silently destroys:
//   - ARITY (FR-006): a yaml.Node knows whether the author wrote a sequence.
//     `map[string]any` conflates `x: a` and `x: [a]` only after you inspect the
//     dynamic type, and conflates `x: []` with `x:` not at all reliably.
//   - ABSENCE vs NULL vs EMPTY (FR-007): `x:` is !!null, `x: ""` is an empty
//     string, and `x` missing entirely is a third state. R-3 requires all three
//     to stay distinguishable.
// ---------------------------------------------------------------------------

// NodeKind is the shape a frontmatter value was written in.
type NodeKind int

const (
	// KindNull is an explicitly empty value — `status:` or `status: null`.
	// FR-007/R-3: this package treats an explicit null as ABSENT, because a key
	// with no value is not a value. It is tracked separately from a missing key
	// only so a report can say which of the two it saw.
	KindNull NodeKind = iota
	// KindScalar is a single value: text, a number, a date, a wikilink.
	KindScalar
	// KindSequence is a YAML list. This is what FR-006's arity check turns on.
	KindSequence
	// KindMapping is a nested mapping. No property type accepts one, so it
	// exists to be REPORTED: a mapping where a scalar was declared is a shape
	// error naming the expected shape, never a silent skip.
	KindMapping
)

func (k NodeKind) String() string {
	switch k {
	case KindNull:
		return "empty"
	case KindScalar:
		return "a single value"
	case KindSequence:
		return "a list"
	case KindMapping:
		return "a mapping"
	}
	return "unknown"
}

// Node is one frontmatter value in lexical form.
type Node struct {
	Kind NodeKind
	// Text is the scalar's source text with YAML quoting already removed but
	// no type conversion applied. For `arr: 349.98` this is "349.98".
	Text string
	// Tag is the YAML tag the decoder resolved (!!str, !!int, !!float, !!null).
	// It is informational: this package trusts the SCHEMA for typing, not the
	// document, because the document has no idea what the operator declared.
	Tag string
	// Quoted reports whether the scalar was written in quotes. FR-030 wants a
	// relation stored as a QUOTED wikilink, so this is how that is checked.
	Quoted bool
	// Block reports whether the scalar was written as a BLOCK scalar — `|` or
	// `>`. Its Text is then the FOLDED body, which no longer resembles the
	// bytes the operator wrote, so FR-030a's raw-text rule has to know: a
	// block scalar reading `[[Acme]]` is a multi-line string, not a link
	// (value.go's parseLinkValue).
	Block bool
	// RawSource is the node's own bytes, re-sliced out of the frontmatter
	// block.
	//
	// IT IS POPULATED ONLY WHERE FR-030a'S RAW-TEXT RULE FIRED — YAML parsed a
	// flow collection, the operator's bytes said `[[Target]]`, and this Node is
	// the wikilink those bytes name. Empty everywhere else, including on
	// ordinary scalars, whose Text already IS their source modulo YAML quoting.
	//
	// So `RawSource != ""` is the marker that this Node's SHAPE was decided by
	// the source rather than by the parser, and it is the only place the
	// original flow-sequence reading survives. Do not start populating it
	// generally without deciding what that marker then means.
	RawSource string
	// Items holds a sequence's elements, in order.
	Items []Node
	// Keys holds a mapping's keys in document order; Fields holds their values.
	Keys   []string
	Fields map[string]Node
	// Line is the 1-based line within the source file, for reporting.
	Line int
}

// Frontmatter is a note's parsed frontmatter block, key order preserved.
type Frontmatter struct {
	// Present reports whether the file had a frontmatter block at all. A note
	// with no frontmatter is an ordinary note (FR-005), never an error.
	Present bool
	Keys    []string
	Values  map[string]Node
	// Problems holds structural complaints that are not fatal to parsing but
	// must reach the caller — duplicate keys, most importantly, because YAML
	// resolves those by silently keeping one.
	Problems []string
}

// Get returns the node for a key and whether the key exists at all.
//
// This is the FR-007 seam: `ok == false` means ABSENT. A key present with an
// explicit null returns ok == true and Kind == KindNull, and callers that care
// about the R-3 distinction ask for the kind.
func (f Frontmatter) Get(key string) (Node, bool) {
	n, ok := f.Values[key]
	return n, ok
}

// ParseFrontmatter extracts and parses a note's YAML frontmatter block.
//
// It handles a UTF-8 BOM, CRLF line endings, `...` as a terminator, and a file
// whose frontmatter is the entire file (all four appear in DS-3's corpus).
//
// A file with no frontmatter is NOT an error — it returns Present=false. That
// is FR-005: a note that is not a record is an ordinary note.
func ParseFrontmatter(src []byte) (Frontmatter, error) {
	block, startLine, ok := extractFrontmatterBlock(src)
	if !ok {
		return Frontmatter{Present: false, Values: map[string]Node{}}, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(block, &doc); err != nil {
		return Frontmatter{Present: true, Values: map[string]Node{}}, fmt.Errorf("frontmatter is not valid YAML: %w", err)
	}

	fm := Frontmatter{Present: true, Values: map[string]Node{}}

	// An empty block decodes to a zero Node with no content.
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return fm, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fm, fmt.Errorf("frontmatter must be a mapping of properties, found %s", yamlKindName(root.Kind))
	}

	// ONE budget for the WHOLE DOCUMENT, threaded through every key.
	//
	// This is load-bearing and it is the whole reason the budget is created
	// here rather than inside the converter. A per-PROPERTY budget bounds
	// nothing: the properties of one note are unbounded in number, so a
	// document made of many individually-legal properties accumulated without
	// any limit at all. Measured on the per-property version: 10 KB of
	// frontmatter (1,000 properties, each expanding to ~8,200 nodes — every one
	// of them comfortably under the per-property allowance) allocated 842 MB
	// and reported NO error, and the trees stayed retained on Values. That is
	// ~83,000x, larger than the 40,000x the original amplification bug was
	// filed for and silent where the original at least died loudly.
	//
	// Do not move this back inside convertYAMLNodeFailure.
	budget := newConvertBudget()

	// FR-030a's instrument. The parser has already thrown away the brackets by
	// the time we see a node, so the decision "is this a wikilink?" has to be
	// able to go back to the operator's own bytes. This is that: the block, and
	// enough index to turn a yaml.Node's (Line, Column) back into a byte range.
	slicer := newSourceSlicer(block)

	// refuse fails the WHOLE frontmatter rather than keeping a partial map: a
	// note whose properties silently went missing would validate as an ordinary
	// note and vanish from answers that still report complete — the exact
	// defect ADR-068 exists to remove. Named, with the reason, instead.
	refuse := func(why, key string, line int) (Frontmatter, error) {
		return Frontmatter{Present: true, Values: map[string]Node{}},
			fmt.Errorf("frontmatter could not be read: %s (property %q, line %d)", why, key, line)
	}

	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		v := root.Content[i+1]
		key := k.Value
		line := k.Line + startLine - 1

		// Charge the key itself, so the budget covers the whole document and
		// not merely the values that reach the converter. The duplicate-key
		// path below converts nothing but still produces a Problems string;
		// left uncharged, a document of a hundred thousand duplicate keys would
		// build a hundred thousand report strings against a budget it never
		// touched.
		if !budget.charge() {
			return refuse(budget.failed, key, line)
		}
		if _, dup := fm.Values[key]; dup {
			// YAML keeps one of them and says nothing. We keep the FIRST (so
			// the result is deterministic) and report, because a duplicate key
			// means the file says two things and the reader cannot see which won.
			fm.Problems = append(fm.Problems, fmt.Sprintf("property %q is declared more than once in the frontmatter (line %d); the first occurrence is used", key, line))
			continue
		}
		fm.Keys = append(fm.Keys, key)
		node, why := convertYAMLNodeFailure(v, startLine, budget, slicer)
		if why != "" {
			return refuse(why, key, line)
		}
		fm.Values[key] = node
	}
	return fm, nil
}

func yamlKindName(k yaml.Kind) string {
	switch k {
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a single value"
	case yaml.MappingNode:
		return "a mapping"
	}
	return "an unsupported YAML construct"
}

// convertBudget bounds ONE WHOLE FRONTMATTER DOCUMENT's conversion.
//
// THE SCOPE IS THE POINT. This used to be created per PROPERTY, which bounds
// nothing an attacker cares about: a note may carry any number of properties,
// so a document of many individually-legal properties accumulated freely.
// 10 KB of frontmatter allocated 842 MB with no error reported. The budget is
// therefore created once, in ParseFrontmatter, and threaded through every key
// of the document — including the keys the duplicate-key path never converts.
//
// TWO DISTINCT ATTACKS, ONE CONTROL. A YAML alias may point at an ancestor,
// so following aliases blindly recurses forever: a six-line note
// (`a: &x` / `  - *x`) produced `fatal error: stack overflow` with 94
// convertYAMLNode frames. That is a FATAL runtime error, not a panic —
// recover() cannot catch it, so one note in the operator's vault takes the
// whole gateway down. Separately, aliases that each reference several earlier
// aliases expand multiplicatively ("billion laughs"): 210 bytes of frontmatter
// produced 66,430 nodes and 8.3 MB of heap, roughly 40,000x, with each added
// line multiplying by nine again.
//
// A depth cap alone stops the cycle but not the amplification, and a visited
// set alone stops the cycle but not a legitimately deep document. So the
// budget counts NODES PRODUCED, which bounds both, and a cycle is additionally
// cut at the alias edge by the active-alias set so the error names the real
// cause rather than reporting an exhausted budget.
//
// Exceeding either limit is not silent: conversion stops and the caller marks
// the note unparseable, which is a finding a report names — the outcome
// ParseRecord's contract promises.
//
// WHY A NODE COUNT AND NOT A BYTE COUNT. A byte bound was considered and
// deliberately not added, on measurement rather than taste. Retained memory
// here is (nodes x per-node cost) + (unique scalar text). Measured per-node
// cost across the three shapes this converter can produce: 213 B/node for
// sequences of scalars, 378 B/node for mappings (each allocates a Go map),
// 294 B/node for alias-heavy text. So the node count already bounds the first
// term to within a 1.8x constant. The second term cannot be amplified at all:
// `out.Text = n.Value` copies a string HEADER, so a thousand aliases to one
// 200 KB scalar share a single backing array — measured, 8,200 aliases to a
// 200-byte scalar retained 294 B/node, not 200 B of text per node. Unique
// scalar text is therefore bounded by the source file's own size and is not an
// amplification. A second bound would add a second number to keep in agreement
// with this one without covering anything this one misses.
//
// What the measurement DID change is the number. 50,000 was chosen when the
// budget was per-property; per-document it authorises ~19 MB of retained tree
// for a single note at the worst measured shape. 20,000 caps that at ~7.6 MB
// while staying far above any real note: the operator's own 751-note vault
// peaks in the LOW HUNDREDS of nodes, so this is ~65x the observed ceiling and
// still admits a generated index note carrying a 5,000-item list.
type convertBudget struct {
	nodes  int                 // remaining node allowance for the whole document
	active map[*yaml.Node]bool // aliases currently being expanded, for cycle detection
	failed string              // non-empty once a limit was hit
}

const convertMaxNodes = 20_000

func newConvertBudget() *convertBudget {
	return &convertBudget{nodes: convertMaxNodes, active: map[*yaml.Node]bool{}}
}

// charge draws one node from the document's allowance, reporting whether the
// caller may proceed. It records the refusal reason on first exhaustion so
// every later call short-circuits against the SAME reason.
func (b *convertBudget) charge() bool {
	if b.failed != "" {
		return false
	}
	if b.nodes <= 0 {
		b.failed = fmt.Sprintf("frontmatter expands to more than %d nodes across the whole note; refusing to read it", convertMaxNodes)
		return false
	}
	b.nodes--
	return true
}

// convertYAMLNodeFailure is the ONLY conversion entry point. An earlier
// unbounded convertYAMLNode existed alongside it and was removed: two entry
// points meant a caller could take the unguarded one, which is how CRIT-001
// reached the tree in the first place.
//
// The budget is a PARAMETER, not something this function allocates. That is
// deliberate and is the fix for the accumulation defect: allocating it here is
// what made the bound per-property, and a signature that hands it in makes the
// document-wide scope visible at the call site instead of hidden one frame
// down.
//
// It converts and reports why it stopped, if it did. The caller turns a
// non-empty reason into a parse error so the note is reported by name rather
// than silently appearing to have empty frontmatter.
func convertYAMLNodeFailure(n *yaml.Node, lineOffset int, b *convertBudget, s *sourceSlicer) (Node, string) {
	out := convertYAMLNodeBounded(n, lineOffset, b, s)
	return out, b.failed
}

func convertYAMLNodeBounded(n *yaml.Node, lineOffset int, b *convertBudget, s *sourceSlicer) Node {
	if n == nil || !b.charge() {
		return Node{Kind: KindNull}
	}

	if n.Kind == yaml.AliasNode && n.Alias != nil {
		if b.active[n.Alias] {
			b.failed = "frontmatter contains a YAML alias that refers to itself"
			return Node{Kind: KindNull}
		}
		b.active[n.Alias] = true
		// The ALIAS's position, not the anchor's: an alias that expands to a
		// flow sequence has the anchor's bytes, and re-slicing at the alias
		// site would read `*x` rather than a wikilink. Passing nil declines the
		// FR-030a rule for aliased content instead of answering it wrongly —
		// an anchored `[[Target]]` reached through `*x` stays a sequence and is
		// reported as a shape fault, which is at least true.
		out := convertYAMLNodeBounded(n.Alias, lineOffset, b, nil)
		delete(b.active, n.Alias)
		return out
	}

	out := Node{Tag: n.Tag, Line: n.Line + lineOffset - 1}
	switch n.Kind {
	case yaml.ScalarNode:
		out.Text = n.Value
		out.Quoted = n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) != 0
		out.Block = n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0
		if n.Tag == "!!null" {
			out.Kind = KindNull
		} else {
			out.Kind = KindScalar
		}
	case yaml.SequenceNode:
		// FR-030a, first half — THE RAW SOURCE TEXT DECIDES WHAT A WIKILINK IS.
		//
		// `- [[Target]]` is legal YAML flow-list syntax, so the parser hands
		// back a sequence containing a sequence containing the scalar `Target`,
		// and every bracket the operator typed is gone. Downstream that becomes
		// "property holds a list where a single value belongs" — a shape
		// complaint about a shape the operator never wrote. It was the root
		// cause of most of 66 wrong-shape findings in the founder's vault.
		//
		// So before this subtree is converted at all, the operator's own bytes
		// are consulted. If they say exactly `[[Target]]`, this node IS that
		// wikilink and is emitted as the SCALAR it was written as. The parser's
		// reading is discarded, which is the whole ruling in one line.
		//
		// It happens HERE, at the frontmatter layer, rather than at the point a
		// relation is parsed, and that placement is load-bearing: ResolveProperty
		// checks ARITY before it looks at any value (FR-006, deliberately), so a
		// scalar `company: [[Acme]]` would be refused as "holds a list" and never
		// reach a value parser at all. Deciding the shape while the source is
		// still in hand is the only place the rule can be applied uniformly to
		// scalar and list properties both. It also means every consumer of a
		// Node — validation, filtering, the index — sees the same decided shape,
		// rather than each re-deriving it.
		//
		// Note what CANNOT be mistaken for a link: isExactWikilinkSource refuses
		// any inner `[`, `]`, `,` or newline, so a genuine nested list
		// (`[[a, b]]`) and a multi-line flow sequence both stay sequences. D14 is
		// untouched — nothing is rewritten on disk, ever.
		if raw, ok := s.flowWikilink(n); ok {
			out.Kind = KindScalar
			out.Text = raw
			out.RawSource = raw
			out.Tag = "!!str"
			return out
		}
		out.Kind = KindSequence
		out.Items = make([]Node, 0, len(n.Content))
		for _, c := range n.Content {
			out.Items = append(out.Items, convertYAMLNodeBounded(c, lineOffset, b, s))
			if b.failed != "" {
				return Node{Kind: KindNull}
			}
		}
	case yaml.MappingNode:
		out.Kind = KindMapping
		out.Fields = map[string]Node{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if _, dup := out.Fields[key]; dup {
				continue
			}
			out.Keys = append(out.Keys, key)
			out.Fields[key] = convertYAMLNodeBounded(n.Content[i+1], lineOffset, b, s)
			if b.failed != "" {
				return Node{Kind: KindNull}
			}
		}
	default:
		out.Kind = KindNull
	}
	return out
}

// ---------------------------------------------------------------------------
// FR-030a — GOING BACK TO THE OPERATOR'S BYTES
//
// records.Node carries Kind/Text/Tag/Quoted/Items/Keys/Fields/Line and NO
// SOURCE SPAN. That is exactly why FR-030a is new work rather than a one-line
// predicate: by the time a value is typed, the brackets are gone and there is
// nothing left to read.
//
// yaml.Node carries Line AND Column, so the span is RECONSTRUCTIBLE — start at
// (Line, Column), scan forward to the matching close bracket. Two details make
// it less obvious than it sounds, and both are handled below:
//
//   - COLUMN IS A RUNE INDEX, NOT A BYTE INDEX. yaml.v3's scanner advances the
//     column once per character read, whatever its UTF-8 width. A frontmatter
//     line holding `société: [[Acme]]` therefore has a Column that is smaller
//     than the byte offset, and slicing on it directly would start mid-word.
//     offsetOf walks runes.
//   - THE END IS NOT GIVEN AT ALL. yaml.Node has no end position, so the close
//     has to be found by scanning — with bracket depth AND quote state, because
//     `[["a]b"]]` is one bracket pair with a `]` inside a string.
//
// The scan crosses newlines deliberately: a multi-line flow sequence is one
// node, and its raw source is the whole thing. isExactWikilinkSource then
// refuses it for containing a newline, which is the right answer for the right
// reason — the span was read correctly and the CONTENT is not a wikilink.
// ---------------------------------------------------------------------------

// sourceSlicer turns a yaml.Node's position back into the bytes it was written
// as. It reads the frontmatter BLOCK — the same bytes yaml.Unmarshal was given,
// after CRLF and BOM normalisation — so positions line up exactly.
type sourceSlicer struct {
	block []byte
	// lineStarts[n] is the byte offset of 1-based line n. Index 0 is unused and
	// holds 0, so a line number indexes it directly with no arithmetic to get
	// wrong.
	lineStarts []int
}

func newSourceSlicer(block []byte) *sourceSlicer {
	s := &sourceSlicer{block: block, lineStarts: []int{0, 0}}
	for i, b := range block {
		if b == '\n' {
			s.lineStarts = append(s.lineStarts, i+1)
		}
	}
	return s
}

// offsetOf converts a 1-based (line, column) into a byte offset into the block.
// Column is a RUNE index — see the header — so this walks runes rather than
// adding column-1 to the line start.
func (s *sourceSlicer) offsetOf(line, col int) (int, bool) {
	if s == nil || line < 1 || line >= len(s.lineStarts) || col < 1 {
		return 0, false
	}
	start := s.lineStarts[line]
	rest := s.block[start:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	runes := 0
	for i := range string(rest) {
		if runes == col-1 {
			return start + i, true
		}
		runes++
	}
	if runes == col-1 {
		return start + len(rest), true
	}
	return 0, false
}

// flowWikilink returns the node's raw source text when — and only when — the
// operator wrote a wikilink there and YAML read it as a flow collection.
//
// The gate is the BYTE at the node's own start position, not yaml.Style. A
// block sequence's items begin at `-` and a scalar begins at its first
// character, so requiring `[` admits exactly the flow collections FR-030a is
// about and does not depend on how the decoder happens to populate a style
// bitmask.
func (s *sourceSlicer) flowWikilink(n *yaml.Node) (string, bool) {
	if s == nil || n == nil {
		return "", false
	}
	start, ok := s.offsetOf(n.Line, n.Column)
	if !ok || start >= len(s.block) || s.block[start] != '[' {
		return "", false
	}
	end, ok := s.matchingBracket(start)
	if !ok {
		return "", false
	}
	raw := string(s.block[start : end+1])
	if !isExactWikilinkSource(raw) {
		return "", false
	}
	return raw, true
}

// matchingBracket returns the index of the bracket closing the one at start.
//
// It tracks quote state as well as depth, because a quoted element may contain
// an unbalanced bracket: `["a]b"]` closes at the LAST `]`, not the one inside
// the string. Without that, the span would be short and the raw text would be
// a truncated lie about what the operator wrote — worse than declining to
// answer, because it would still look like a well-formed answer.
//
// A run to the end of the block with the depth still open returns false: an
// unterminated flow sequence is a YAML error the decoder has its own words for,
// and this function does not invent a span for it.
func (s *sourceSlicer) matchingBracket(start int) (int, bool) {
	depth := 0
	var quote byte
	for i := start; i < len(s.block); i++ {
		c := s.block[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++ // a backslash escape inside a double-quoted scalar
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i, true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

// isExactWikilinkSource is FR-030a's grammar over RAW SOURCE TEXT: the bytes
// must be exactly a wikilink and nothing else.
//
// It is STRICTER than ParseWikilink, and every extra refusal is one the ruling
// names or requires:
//
//	`,`         a genuine nested list. FR-030a's own words: "a genuine nested
//	            list (`[[a, b]]`, raw text with a comma) can never be mistaken
//	            for a link". Without this clause `[[a, b]]` would become a link
//	            to a note called "a, b" — the mirror image of the defect the
//	            ruling exists to fix, and harder to see.
//	`[` / `]`   `[[a],[b]]` is two lists. ParseWikilink alone accepts it: it
//	            only rejects an inner `[[` or `]]`, and this has neither, so it
//	            would yield a target of `a],[b`.
//	newline     a multi-line flow sequence. The span was read correctly; the
//	            content simply is not a wikilink, and a note name spanning two
//	            lines is not a thing.
//
// A QUOTED scalar is not subject to any of this and must not be. `"[[a, b]]"`
// is a link to "a, b": the operator quoted it, so YAML never read a list, there
// is no parser accident to correct, and D5.1's quoting convention is what our
// own writes produce. That path never reaches this function.
func isExactWikilinkSource(raw string) bool {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "[[") || !strings.HasSuffix(s, "]]") || len(s) <= 4 {
		return false
	}
	if strings.ContainsAny(s[2:len(s)-2], "[],\n\r") {
		return false
	}
	_, ok := ParseWikilink(s)
	return ok
}

// extractFrontmatterBlock returns the YAML between the opening `---` and the
// closing `---`/`...`, plus the 1-based source line the block starts on.
func extractFrontmatterBlock(src []byte) (block []byte, startLine int, ok bool) {
	s := bytes.TrimPrefix(src, []byte("\xef\xbb\xbf")) // UTF-8 BOM

	// The opening fence must be the very first line.
	first, rest, found := splitLine(s)
	if !isFence(first) {
		return nil, 0, false
	}
	if !found {
		// A file consisting of nothing but "---" has no block to speak of.
		return nil, 0, false
	}

	var buf bytes.Buffer
	for {
		l, next, more := splitLine(rest)
		if isFence(l) || isDocEnd(l) {
			return buf.Bytes(), 2, true
		}
		buf.Write(bytes.TrimSuffix(l, []byte("\r")))
		buf.WriteByte('\n')
		if !more {
			// Unterminated frontmatter. DS-3 includes "a file whose
			// frontmatter is the entire file", so this is the normal case for
			// such a note, not a malformation: treat what we have as the block.
			return buf.Bytes(), 2, true
		}
		rest = next
	}
}

// splitLine returns the first line (without its newline), the remainder, and
// whether a newline was actually found.
func splitLine(s []byte) (line, rest []byte, found bool) {
	i := bytes.IndexByte(s, '\n')
	if i < 0 {
		return s, nil, false
	}
	return s[:i], s[i+1:], true
}

func isFence(line []byte) bool {
	return strings.TrimRight(string(line), " \t\r") == "---"
}

func isDocEnd(line []byte) bool {
	return strings.TrimRight(string(line), " \t\r") == "..."
}
