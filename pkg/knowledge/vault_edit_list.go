// Omnipus — ADR-068 FR-040a/FR-040b: the list-valued frontmatter splice, and
// the scalar-write guard that stops author.go's existing scalar splice from
// silently deleting one.
//
// # Why this is a separate file from author.go
//
// author.go's own header says its two edits (SetProperty, AppendSectionAt)
// are "THE two edits (FR-105)" — singular set, deliberately closed at the
// time it was written. FR-040a reopens it: "the system MUST add a
// list-valued splice ... SetProperty is scalar-only and cannot satisfy
// FR-045 or many: true." That is new primitive surface, not a change to
// SetProperty's own contract, and it is added here rather than by editing
// author.go in place so that this file's additions and any concurrent work
// on author.go's OTHER primitives (replace_body, in this same package) do
// not collide on the same lines of the same file.
//
// # What "splice" means here, precisely
//
// Three shapes exist for a property's current value, and each is handled
// without touching what it does not need to:
//
//   - listStyleBlock — a YAML block sequence ("key:\n  - a\n  - b\n"). Adding
//     or removing ONE element rewrites only that element's own line (or
//     inserts/removes exactly one line); every other item's bytes, and the
//     indent and dash style they were written with, survive untouched.
//   - listStyleFlow — a single-line flow sequence ("key: [a, b]"). Add/remove
//     rewrites that one line; nothing else in the file moves.
//   - listStyleScalar / listStyleNone — no existing list to splice into.
//     SetPropertyList creates one (defaulting to block style, the more
//     common Obsidian convention and the one that reads as a one-line-per-
//     item diff on every future add). AddListValue promotes an absent key
//     the same way. Both REFUSE against an existing SCALAR rather than
//     guess whether the caller meant to replace or extend it (FR-042's
//     arity philosophy, applied one layer down).
//
// A shape this file cannot confidently parse — a nested mapping inside the
// sequence, an irregular indent, a folded block scalar — is
// listStyleUnsupported and every operation on it REFUSES, naming the reason,
// rather than writing a guess that could corrupt structure this file does
// not understand (the same "refuse, never coerce" rule set.go already lives
// by).
//
// # The two DEFINED idempotent outcomes (task directive, and FR-040a in
// spirit: a splice must not need the caller to already know what is there)
//
//   - Adding a value the list ALREADY holds: no bytes change. AddListValue
//     returns src unchanged, so EditNote's own byte-equality check reports
//     Changed: false — the same "same bytes in, same bytes out" contract
//     AddWikilink already uses for a link that's already there.
//   - Removing a value the list does NOT hold (or a property that is not
//     there at all): no bytes change, Changed: false, for the identical
//     reason — the caller's desired end state was already true.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// ErrMultiLineValue means a scalar write (set_property with a single value)
// targeted a key whose CURRENT value already spans more than one line — most
// commonly an existing list. author.go's SetProperty removes every
// continuation line belonging to the key it splices (that is how it replaces
// a value at all), so applying it here would silently delete every element
// but the one word the caller sent. FR-040b requires this refused instead,
// with the file left byte-identical.
var ErrMultiLineValue = errors.New("knowledge: scalar write would delete a multi-line value")

// ErrListShapeUnsupported means a property's current value is not one of the
// two list shapes this file understands (a block sequence or a single-line
// flow sequence), or is a plain scalar where a list operation was asked for.
// Refused rather than guessed: a nested mapping, an irregular indent, or a
// folded block scalar could be misread by any splice this file could write.
var ErrListShapeUnsupported = errors.New("knowledge: property's current value is not a list this tool can splice")

// listStyle names how an existing list is written, so a rewrite — or an
// insertion into it — can match it exactly (FR-040a: "preserving the
// source's existing list style").
type listStyle int

const (
	listStyleNone        listStyle = iota // key absent: nothing to match
	listStyleBlock                        // key:\n  - a\n  - b
	listStyleFlow                         // key: [a, b]
	listStyleScalar                       // key: a — a single value, not a list
	listStyleUnsupported                  // a shape this file refuses to guess at
)

// listSpan is what parseListSpan learns about one key's current value.
type listSpan struct {
	block fmBlock
	style listStyle
	// start/end bound the WHOLE key — header line plus continuation lines —
	// exactly as fmFindKey returns them. Both zero and found false when the
	// key is absent.
	start, end int
	found      bool
	// items holds the DECODED element text, in file order. Populated only
	// for listStyleBlock and listStyleFlow.
	items []string
	// indent is the block style's continuation-line indent ("  ", "", …),
	// carried so a new item matches the existing ones. Meaningless for other
	// styles.
	indent string
}

// parseListSpan locates key inside src's frontmatter and classifies its
// current shape, without writing anything.
func parseListSpan(src []byte, key string) (listSpan, error) {
	block, err := fmParse(src)
	if err != nil {
		return listSpan{}, err
	}
	if !block.present {
		return listSpan{style: listStyleNone, block: block}, nil
	}
	start, end, found := fmFindKey(src, block, key)
	if !found {
		return listSpan{style: listStyleNone, block: block}, nil
	}
	span := listSpan{block: block, start: start, end: end, found: true}

	headerLine, headerEnd, _ := authorLineAt(src, start)
	prefix := key + ":"
	remainder := strings.TrimSpace(strings.TrimPrefix(string(headerLine), prefix))

	if headerEnd >= end {
		// A single physical line in the span: no continuation lines at all.
		if remainder == "" {
			// "key:" with nothing after it — an explicit null/empty scalar.
			// There is no way to tell "meant as an empty list" from "meant
			// as null" from the bytes alone, so this is a scalar: an add
			// promotes it to a fresh list (its own defined behaviour),
			// exactly as it would an absent key.
			span.style = listStyleScalar
			return span, nil
		}
		if strings.HasPrefix(remainder, "[") && strings.HasSuffix(remainder, "]") {
			items, ok := decodeFlowItems(remainder[1 : len(remainder)-1])
			if !ok {
				span.style = listStyleUnsupported
				return span, nil
			}
			span.style = listStyleFlow
			span.items = items
			return span, nil
		}
		span.style = listStyleScalar
		return span, nil
	}

	// Continuation lines exist. This is a block sequence ONLY when the
	// header carries no value of its own and every continuation line, at one
	// consistent indent, is a bare "- item" — anything else (a folded block
	// scalar, a nested mapping, a mixed indent) is refused rather than
	// guessed.
	if remainder != "" {
		span.style = listStyleUnsupported
		return span, nil
	}
	items := make([]string, 0, 4)
	indent := ""
	first := true
	for pos := headerEnd; pos < end; {
		line, next, ok := authorLineAt(src, pos)
		if !ok {
			break
		}
		text := string(line)
		trimmed := strings.TrimLeft(text, " \t")
		lineIndent := text[:len(text)-len(trimmed)]
		if trimmed != "-" && !strings.HasPrefix(trimmed, "- ") {
			span.style = listStyleUnsupported
			return span, nil
		}
		if first {
			indent = lineIndent
			first = false
		} else if lineIndent != indent {
			span.style = listStyleUnsupported
			return span, nil
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if looksLikeMappingEntry(item) {
			// "- key: value" is YAML's own shape for "this sequence holds
			// mappings, not scalars" — refused as unsupported rather than
			// decoded as if the colon were part of a plain string. Found by
			// mutation-testing the shape guard above: an indent-consistency
			// fixture happened to catch a two-item nested-mapping case for
			// an unrelated reason, masking that THIS check — the one meant
			// to catch it — did not exist yet.
			span.style = listStyleUnsupported
			return span, nil
		}
		decoded, ok := decodeScalarItem(item)
		if !ok {
			span.style = listStyleUnsupported
			return span, nil
		}
		items = append(items, decoded)
		pos = next
	}
	span.style = listStyleBlock
	span.items = items
	span.indent = indent
	return span, nil
}

// decodeFlowItems splits a flow sequence's INNER text ("a, b, "c, d"") on
// top-level commas — outside quotes — and decodes each element. It reports
// ok=false for anything it cannot confidently decode (an unterminated quote,
// a nested collection), rather than guess.
func decodeFlowItems(inner string) ([]string, bool) {
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return []string{}, true
	}
	var raws []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		switch {
		case quote != 0:
			cur.WriteByte(c)
			if c == '\\' && quote == '"' && i+1 < len(inner) {
				i++
				cur.WriteByte(inner[i])
				continue
			}
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
			cur.WriteByte(c)
		case c == ',':
			raws = append(raws, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if quote != 0 {
		return nil, false
	}
	raws = append(raws, strings.TrimSpace(cur.String()))
	out := make([]string, 0, len(raws))
	for _, raw := range raws {
		decoded, ok := decodeScalarItem(raw)
		if !ok {
			return nil, false
		}
		out = append(out, decoded)
	}
	return out, true
}

// looksLikeMappingEntry reports whether a block-sequence item's RAW (still
// quoted, if it was) text is an unquoted "key: value" pair — YAML's own
// shape for "this sequence holds mappings, not scalars". A quoted item may
// contain a literal colon-space without meaning this; only an item this file
// has not already recognised as quoted can be a mapping in disguise.
func looksLikeMappingEntry(item string) bool {
	if item == "" {
		return false
	}
	if item[0] == '"' || item[0] == '\'' {
		return false
	}
	if strings.HasSuffix(item, ":") {
		return true
	}
	return strings.Contains(item, ": ")
}

// decodeScalarItem strips YAML quoting from one flow- or block-sequence item,
// returning its VALUE (not its source spelling) and whether it could be
// decoded at all. A nested flow collection ("[a, b]" as an element) is
// refused rather than flattened.
func decodeScalarItem(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return unescapeDoubleQuoted(raw[1 : len(raw)-1])
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'"), true
	}
	if strings.ContainsAny(raw, "[]{}") {
		return "", false
	}
	return raw, true
}

func unescapeDoubleQuoted(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				return "", false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

// renderList renders "key" plus its list value in the given style, as
// complete lines ending in eol — ready to be spliced in verbatim.
func renderList(key string, style listStyle, indent string, values []string, eol string) (string, error) {
	switch style {
	case listStyleFlow:
		encoded := make([]string, 0, len(values))
		for _, v := range values {
			enc, err := authorEncodeScalar(v)
			if err != nil {
				return "", err
			}
			encoded = append(encoded, enc)
		}
		return key + ": [" + strings.Join(encoded, ", ") + "]" + eol, nil
	case listStyleBlock:
		if len(values) == 0 {
			// A block sequence has no continuation-line spelling for "zero
			// items" — render the flow-empty form on the key's own line,
			// the same way an operator clearing a list by hand would.
			return key + ": []" + eol, nil
		}
		var b strings.Builder
		b.WriteString(key + ":" + eol)
		for _, v := range values {
			enc, err := authorEncodeScalar(v)
			if err != nil {
				return "", err
			}
			b.WriteString(indent + "- " + enc + eol)
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("knowledge: internal: unrenderable list style %d", style)
	}
}

// spliceListSpan writes `rendered` (a complete "key: ...\n" or
// "key:\n  - a\n" block, its own lines already terminated) in place of key's
// current span — insert before the closing fence when the key is absent,
// replace the found span otherwise, and build a whole new frontmatter block
// when the note has none at all. Mirrors fmSpliceKey's three cases exactly,
// generalised from one line to N.
func spliceListSpan(src []byte, span listSpan, rendered string) ([]byte, error) {
	if !span.block.present {
		eol := authorDominantEOL(src)
		var out bytes.Buffer
		out.WriteString("---" + eol)
		out.WriteString(rendered)
		out.WriteString("---" + eol)
		if len(src) > 0 {
			out.WriteString(eol)
		}
		out.Write(src)
		return out.Bytes(), nil
	}
	if !span.found {
		var out bytes.Buffer
		out.Write(src[:span.block.innerEnd])
		out.WriteString(rendered)
		out.Write(src[span.block.innerEnd:])
		return out.Bytes(), nil
	}
	var out bytes.Buffer
	out.Write(src[:span.start])
	out.WriteString(rendered)
	out.Write(src[span.end:])
	return out.Bytes(), nil
}

// SetPropertyList returns an edit that replaces a property's ENTIRE list
// value, splicing only that property's own lines and leaving every other
// byte of the note untouched (FR-040, FR-040a).
//
// The rewrite matches the property's EXISTING list style — block sequence or
// single-line flow. A brand-new property, or one whose current value is a
// plain scalar, is written as a block sequence (this file's documented
// default); values may be empty, written as "key: []" so a declared-but-
// empty list stays distinguishable from the property's absence (R-3).
func SetPropertyList(key string, values []string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := authorValidatePropertyKey(key); err != nil {
			return nil, err
		}
		span, err := parseListSpan(src, key)
		if err != nil {
			return nil, err
		}
		if span.style == listStyleUnsupported {
			return nil, fmt.Errorf("%w: %q", ErrListShapeUnsupported, key)
		}
		style := span.style
		indent := span.indent
		if style == listStyleNone || style == listStyleScalar {
			style = listStyleBlock
			indent = "  "
		}
		rendered, rerr := renderList(key, style, indent, values, authorDominantEOL(src))
		if rerr != nil {
			return nil, rerr
		}
		return spliceListSpan(src, span, rendered)
	}
}

// AddListValue returns an edit that adds ONE value to a many-valued
// property, without the caller reading — or this file rewriting — the rest
// of the list: only the new item's own line is written (block style) or the
// single flow line is rewritten; every other element's bytes are untouched.
//
// See the file header for the two defined idempotent outcomes (absent key →
// a fresh one-item list; value already present → src returned unchanged).
func AddListValue(key, value string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := authorValidatePropertyKey(key); err != nil {
			return nil, err
		}
		if _, err := authorEncodeScalar(value); err != nil {
			return nil, err
		}
		span, err := parseListSpan(src, key)
		if err != nil {
			return nil, err
		}
		switch span.style {
		case listStyleScalar:
			return nil, fmt.Errorf("%w: %q currently holds a single value, not a list; "+
				"send set_property with a list value to convert it first", ErrListShapeUnsupported, key)
		case listStyleUnsupported:
			return nil, fmt.Errorf("%w: %q", ErrListShapeUnsupported, key)
		}
		for _, existing := range span.items {
			if existing == value {
				return src, nil // already present — defined no-op (Changed: false)
			}
		}
		style := span.style
		indent := span.indent
		if style == listStyleNone {
			style = listStyleBlock
			indent = "  "
		}
		newItems := append(append([]string(nil), span.items...), value)
		rendered, rerr := renderList(key, style, indent, newItems, authorDominantEOL(src))
		if rerr != nil {
			return nil, rerr
		}
		return spliceListSpan(src, span, rendered)
	}
}

// RemoveListValue returns an edit that removes ONE value from a many-valued
// property — the first occurrence, if the value is written more than once —
// touching only the removed item's own line (block style) or the single
// flow-sequence line; every other element's bytes are untouched.
//
// See the file header for the two defined idempotent outcomes (absent key,
// or value not present → src returned unchanged; last remaining element
// removed → the property is left declared and EMPTY, "key: []", never
// deleted — a property's required-ness is a schema question this file does
// not answer, and deleting the key would silently turn "present and empty"
// into "absent", a different validation finding).
func RemoveListValue(key, value string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := authorValidatePropertyKey(key); err != nil {
			return nil, err
		}
		span, err := parseListSpan(src, key)
		if err != nil {
			return nil, err
		}
		switch span.style {
		case listStyleNone:
			return src, nil // nothing to remove from — defined no-op
		case listStyleScalar:
			return nil, fmt.Errorf("%w: %q currently holds a single value, not a list; "+
				"send set_property with a list value to convert it first", ErrListShapeUnsupported, key)
		case listStyleUnsupported:
			return nil, fmt.Errorf("%w: %q", ErrListShapeUnsupported, key)
		}
		found := false
		newItems := make([]string, 0, len(span.items))
		for _, existing := range span.items {
			if !found && existing == value {
				found = true
				continue
			}
			newItems = append(newItems, existing)
		}
		if !found {
			return src, nil // not present — defined no-op (Changed: false)
		}
		rendered, rerr := renderList(key, span.style, span.indent, newItems, authorDominantEOL(src))
		if rerr != nil {
			return nil, rerr
		}
		return spliceListSpan(src, span, rendered)
	}
}

// SetPropertyScalarChecked is author.go's SetProperty guarded by FR-040b: the
// write is REFUSED, and the file left byte-identical, when the property's
// CURRENT value is a list — because SetProperty's splice replaces every
// byte belonging to the old value, and a scalar write over what turns out
// to be a list would otherwise silently delete every element but the one
// word the caller sent.
//
// The guard is decided by parseListSpan's STYLE classification, not by
// counting physical lines in the span. A block sequence ("key:\n  - a\n  -
// b\n") always spans more than one line and a naive line count catches it,
// but a flow sequence ("key: [a, b]") is exactly ONE line — a caller
// carrying two, three, or three hundred values on that single line looked,
// to a line-count guard, indistinguishable from an ordinary one-line
// scalar, and the guard let the overwrite through. Whether a property's
// current value is destroyed by this call must not depend on which YAML
// style it happens to be written in.
func SetPropertyScalarChecked(key, value string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		span, err := parseListSpan(src, key)
		if err != nil {
			return nil, err
		}
		switch span.style {
		case listStyleBlock:
			lines := lineCountInSpan(src, span.start, span.end)
			return nil, fmt.Errorf(
				"%w: %q currently spans %d lines; a scalar write would delete them — "+
					"no change made. Send a list value instead", ErrMultiLineValue, key, lines)
		case listStyleFlow:
			return nil, fmt.Errorf(
				"%w: %q currently holds a %d-item list ([%s]); a scalar write would delete "+
					"them — no change made. Send a list value instead",
				ErrMultiLineValue, key, len(span.items), strings.Join(span.items, ", "))
		case listStyleUnsupported:
			return nil, fmt.Errorf(
				"%w: %q currently holds a value this tool cannot confidently parse as a plain "+
					"scalar; no change made. Send a list value instead, or edit the file directly",
				ErrMultiLineValue, key)
		}
		return SetProperty(key, value)(src)
	}
}

// lineCountInSpan counts physical lines in src[start:end), relying on
// fmFindKey's own guarantee that a span always ends exactly on a line
// boundary — so counting line terminators (both "\n" and the "\n" half of
// "\r\n") equals the number of lines in the span.
func lineCountInSpan(src []byte, start, end int) int {
	return bytes.Count(src[start:end], []byte("\n"))
}
