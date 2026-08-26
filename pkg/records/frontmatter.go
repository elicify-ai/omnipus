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
	// KindMapping is a nested mapping — used by `money`'s explicit form.
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

	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		v := root.Content[i+1]
		key := k.Value
		if _, dup := fm.Values[key]; dup {
			// YAML keeps one of them and says nothing. We keep the FIRST (so
			// the result is deterministic) and report, because a duplicate key
			// means the file says two things and the reader cannot see which won.
			fm.Problems = append(fm.Problems, fmt.Sprintf("property %q is declared more than once in the frontmatter (line %d); the first occurrence is used", key, k.Line+startLine-1))
			continue
		}
		fm.Keys = append(fm.Keys, key)
		node, why := convertYAMLNodeFailure(v, startLine)
		if why != "" {
			// A bounded conversion refused. Fail the WHOLE frontmatter rather
			// than keeping a partial map: a note whose properties silently
			// went missing would validate as an ordinary note and vanish from
			// answers that still report complete — the exact defect ADR-068
			// exists to remove. Reported by name instead.
			return Frontmatter{Present: true, Values: map[string]Node{}},
				fmt.Errorf("frontmatter could not be read: %s (property %q, line %d)", why, key, k.Line+startLine-1)
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

// convertYAMLNode turns a yaml.Node into our lexical Node, resolving aliases so
// an anchor/alias pair does not read as an empty value.
// convertBudget bounds a single frontmatter conversion.
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
// ParseRecord's contract promises. Both limits are far above any hand-written
// frontmatter; the operator's own 751-note vault peaks in the low hundreds.
type convertBudget struct {
	nodes  int                 // remaining node allowance
	active map[*yaml.Node]bool // aliases currently being expanded, for cycle detection
	failed string              // non-empty once a limit was hit
}

const convertMaxNodes = 50_000

// convertYAMLNodeFailure is the ONLY conversion entry point. An earlier
// unbounded convertYAMLNode existed alongside it and was removed: two entry
// points meant a caller could take the unguarded one, which is how CRIT-001
// reached the tree in the first place.
//
// It converts and reports why it stopped, if it did. The
// caller turns a non-empty reason into a parse error so the note is reported
// by name rather than silently appearing to have empty frontmatter.
func convertYAMLNodeFailure(n *yaml.Node, lineOffset int) (Node, string) {
	b := &convertBudget{nodes: convertMaxNodes, active: map[*yaml.Node]bool{}}
	out := convertYAMLNodeBounded(n, lineOffset, b)
	return out, b.failed
}

func convertYAMLNodeBounded(n *yaml.Node, lineOffset int, b *convertBudget) Node {
	if n == nil || b.failed != "" {
		return Node{Kind: KindNull}
	}
	if b.nodes <= 0 {
		b.failed = "frontmatter expands to more than 50000 nodes; refusing to read it"
		return Node{Kind: KindNull}
	}
	b.nodes--

	if n.Kind == yaml.AliasNode && n.Alias != nil {
		if b.active[n.Alias] {
			b.failed = "frontmatter contains a YAML alias that refers to itself"
			return Node{Kind: KindNull}
		}
		b.active[n.Alias] = true
		out := convertYAMLNodeBounded(n.Alias, lineOffset, b)
		delete(b.active, n.Alias)
		return out
	}

	out := Node{Tag: n.Tag, Line: n.Line + lineOffset - 1}
	switch n.Kind {
	case yaml.ScalarNode:
		out.Text = n.Value
		out.Quoted = n.Style == yaml.SingleQuotedStyle || n.Style == yaml.DoubleQuotedStyle
		if n.Tag == "!!null" {
			out.Kind = KindNull
		} else {
			out.Kind = KindScalar
		}
	case yaml.SequenceNode:
		out.Kind = KindSequence
		out.Items = make([]Node, 0, len(n.Content))
		for _, c := range n.Content {
			out.Items = append(out.Items, convertYAMLNodeBounded(c, lineOffset, b))
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
			out.Fields[key] = convertYAMLNodeBounded(n.Content[i+1], lineOffset, b)
			if b.failed != "" {
				return Node{Kind: KindNull}
			}
		}
	default:
		out.Kind = KindNull
	}
	return out
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
