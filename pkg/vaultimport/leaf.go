// Omnipus — translating one Obsidian Base filter LEAF expression into an
// ADR-068 RecordFilter leaf, or refusing it by name.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"regexp"
	"strings"
)

// leafKind is what parseLeaf decided about one expression string.
type leafKind int

const (
	leafUntranslatable leafKind = iota
	leafTypeLiteral             // `type == "X"` — consumed into the view's own `type:`, never emitted as a filter
	leafFilter                  // a genuine, translatable RecordFilter leaf
)

// parsedLeaf is parseLeaf's result.
type parsedLeaf struct {
	Kind        leafKind
	TypeLiteral string
	Filter      RawLeaf
}

// RawLeaf is one translated filter leaf BEFORE its literal is tagged against
// a schema property's declared type — that tagging happens in view_write.go,
// once the target view's record-type schema is known.
type RawLeaf struct {
	Property string
	Op       string // one of eq/lt/lte/gt/gte/contains/is_absent — RecordFilterOp's wire spelling
	Values   []string
	Negate   bool
	// Truthy marks a leaf that came from Obsidian's BARE-PROPERTY test
	// (`archived` on its own, not `archived == true`). It is translated as a
	// negated `is_absent` — "has a value" — and that is only FAITHFUL for
	// property types whose every present value is truthy. For a type that
	// admits a present-but-falsy value (`false`, `0`) the two differ, and
	// they differ in the forbidden direction: "has a value" returns MORE
	// rows than "is truthy". The flag exists so buildFilterLeafNode, which
	// is the only place that knows the property's declared type, can refuse
	// exactly those cases by name (FR-105).
	Truthy bool
}

// ---------------------------------------------------------------------------
// WHAT THIS RECOGNISES, AND WHY THE UNTRANSLATABLE-MARKER CHECK RUNS FIRST
//
// Obsidian's Base filter grammar is a real expression language: function
// calls (`file.inFolder(...)`, `date(x).year`, `today()`), computed
// `formula.*` properties, method calls (`.contains(...)`, `.isType(...)`,
// `.length`). This package does not implement that language — ADR-068 O-3
// forbids a text-query parser on the PRODUCT side, and building one here,
// one-shot, would just be the same parser moved one file over. What it does
// instead is pattern-match the small set of shapes real Base filters use for
// things our RecordFilter CAN express (`prop == "literal"`, `prop.contains(
// "literal")`, a bare `prop` presence check) and refuse everything else BY
// NAME, verbatim, rather than approximating it.
//
// The marker check runs before every other pattern for exactly one reason:
// `formula.days_to_expiry <= 14` and `venture != ""` both LOOK like an
// ordinary comparison to the compare regex below (dots are legal in a
// property-name token), and matching it there would silently manufacture a
// `RecordFilter{property: "formula.days_to_expiry", op: lte, ...}` against a
// property no schema will ever declare — caught later by
// ValidateViewAgainstSchemas, but caught as a REJECTION of the whole file
// rather than as a named, reported loss of one clause. Refusing it here,
// first, keeps the rest of the AND-group intact.
// ---------------------------------------------------------------------------

var (
	reUntranslatableMarker = regexp.MustCompile(`\bfile\.|\bformula\.|\btoday\s*\(|\bdate\s*\(|\bnow\s*\(|\.isType\s*\(|\.length\b|\.year\b|\.month\b|\.day\b|\.week\b`)
	reContains             = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\.contains\(\s*(.+?)\s*\)$`)
	reCompare              = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(==|!=|<=|>=|<|>)\s*(.+)$`)
	reBareNegated          = regexp.MustCompile(`^!\s*([A-Za-z_][A-Za-z0-9_]*)$`)
	reBareIdent            = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// nonPropertyBareWords are bare tokens that look like an identifier to
// reBareIdent but are LITERALS, not property references. Without this,
// `true` on its own reads as "the property named true has a value" — a
// clause about a property no schema declares, which is refused later but
// refused under the wrong name, and named wrongly in the report a human
// reads.
var nonPropertyBareWords = map[string]bool{
	"true": true, "false": true, "null": true, "undefined": true,
	// `type` is a property, but it is the DISCRIMINATOR: it is consumed
	// into the view's own `type:` and is never a filter clause.
	"type": true,
}

// parseLeaf translates one leaf expression string (already trimmed of a
// leading `- ` list marker by the YAML decoder).
func parseLeaf(expr string) parsedLeaf {
	s := strings.TrimSpace(expr)
	if s == "" {
		return parsedLeaf{Kind: leafUntranslatable}
	}
	if reUntranslatableMarker.MatchString(s) {
		return parsedLeaf{Kind: leafUntranslatable}
	}

	if m := reContains.FindStringSubmatch(s); m != nil {
		prop, arg := m[1], m[2]
		lit, quoted, empty := unquoteLiteral(arg)
		if prop == "type" || !quoted || empty {
			// `type.contains(...)` never occurs and would not mean
			// anything; a non-string / empty argument to `contains` is not
			// a shape this translator recognises.
			return parsedLeaf{Kind: leafUntranslatable}
		}
		return parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: prop, Op: "contains", Values: []string{lit}}}
	}

	if m := reCompare.FindStringSubmatch(s); m != nil {
		prop, op, rhsRaw := m[1], m[2], strings.TrimSpace(m[3])
		if strings.ContainsAny(rhsRaw, "()") {
			// Defensive: a call on the right-hand side we did not already
			// catch by name (e.g. a future Base function). Refuse rather
			// than mis-parse.
			return parsedLeaf{Kind: leafUntranslatable}
		}
		lit, _, empty := unquoteLiteral(rhsRaw)
		if prop == "type" {
			if op == "==" && !empty {
				return parsedLeaf{Kind: leafTypeLiteral, TypeLiteral: lit}
			}
			// `type != "X"` (exclude one type) has no representation: our
			// filter is a positive equality/comparison surface, not a set
			// difference over the discriminator itself.
			return parsedLeaf{Kind: leafUntranslatable}
		}
		if empty {
			// `prop != ""` / `prop == ""`: Obsidian's own semantics for an
			// UNDEFINED property compared against the empty string are not
			// documented anywhere this importer can rely on, and our
			// engine has no empty-string literal comparison for text
			// (eq/ne are refused on text entirely) or for any other type
			// (there is no valid empty date/integer/decimal/enum
			// literal). Approximating it as `is_absent` would silently
			// change which records match, so it is refused by name
			// instead.
			return parsedLeaf{Kind: leafUntranslatable}
		}
		var wireOp string
		switch op {
		case "==":
			wireOp = "eq"
		case "!=":
			wireOp = "eq"
		case "<":
			wireOp = "lt"
		case "<=":
			wireOp = "lte"
		case ">":
			wireOp = "gt"
		case ">=":
			wireOp = "gte"
		}
		return parsedLeaf{Kind: leafFilter, Filter: RawLeaf{
			Property: prop, Op: wireOp, Values: []string{lit}, Negate: op == "!=",
		}}
	}

	if m := reBareNegated.FindStringSubmatch(s); m != nil {
		prop := m[1]
		if nonPropertyBareWords[prop] {
			return parsedLeaf{Kind: leafUntranslatable}
		}
		// `!prop` is Obsidian's FALSY check: absent, `false`, `0` or `""`.
		// Ours is `is_absent`: absent, and `""` (FR-007a makes an empty
		// string absent). Ours is therefore a strict SUBSET of Obsidian's —
		// it can return FEWER rows on a checkbox or a number, never more,
		// which is the direction FR-105 permits. It needs no Truthy flag
		// because narrowing is not the failure this importer refuses.
		return parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: prop, Op: "is_absent"}}
	}

	if reBareIdent.MatchString(s) {
		if nonPropertyBareWords[s] {
			return parsedLeaf{Kind: leafUntranslatable}
		}
		// A bare property name is Obsidian's TRUTHY check. The nearest
		// equivalent our engine has is a negated `is_absent` — "has a
		// value" — and the two agree only where every present value is
		// truthy. They disagree on `false` and `0`, and there in the one
		// direction FR-105 forbids: "has a value" matches the record
		// Obsidian's truthy test rejects.
		//
		// The type is not known here (parseLeaf sees one expression string
		// and no schema), so the leaf is MARKED rather than decided, and
		// buildFilterLeafNode — which does know — refuses it by name for
		// the types that admit a present-but-falsy value.
		return parsedLeaf{Kind: leafFilter, Filter: RawLeaf{Property: s, Op: "is_absent", Negate: true, Truthy: true}}
	}

	return parsedLeaf{Kind: leafUntranslatable}
}

// unquoteLiteral strips a double- or single-quoted string literal, reporting
// whether it WAS quoted (a bare `true`/`false`/number is not) and whether
// the resulting literal is empty.
func unquoteLiteral(raw string) (value string, quoted bool, empty bool) {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			inner := s[1 : len(s)-1]
			return inner, true, inner == ""
		}
	}
	return s, false, s == ""
}
