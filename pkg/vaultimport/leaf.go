// Omnipus — translating one Obsidian Base filter LEAF expression into an
// ADR-068 RecordFilter leaf, or refusing it by name.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
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
	// Reason is why an untranslatable expression could not be read, in words
	// the operator can act on. It is EMPTY for the shapes whose diagnosis is
	// written somewhere better informed than here — see
	// untranslatableExpressionReason.
	Reason string
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
	// reFormulaCompare matches a comparison against a COMPUTED property —
	// `formula.days_to_expiry <= 14`. It is checked BEFORE
	// reUntranslatableMarker, which still lists `\bformula\.`: everything
	// formula-shaped that is not exactly this one comparison shape (a method
	// call on a formula, a formula on both sides, a bare formula reference)
	// keeps being refused by name, and only the shape this importer can build
	// a real leaf for is let through.
	//
	// The name is carried through as the FULL dotted `formula.<name>`, which is
	// how every property position in the view format addresses a formula
	// (FR-140) — namespace.go gives it a real *Property wearing the formula's
	// declared type, so `<= 14` compares as a number rather than as text.
	reFormulaCompare = regexp.MustCompile(`^(formula\.[A-Za-z_][A-Za-z0-9_]*)\s*(==|!=|<=|>=|<|>)\s*(.+)$`)
	reContains       = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\.contains\(\s*(.+?)\s*\)$`)
	reCompare        = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(==|!=|<=|>=|<|>)\s*(.+)$`)
	reBareNegated    = regexp.MustCompile(`^!\s*([A-Za-z_][A-Za-z0-9_]*)$`)
	reBareIdent      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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

// ---------------------------------------------------------------------------
// A COMPOUND EXPRESSION IS NOT A LEAF, AND THE COMPARE PATTERN CANNOT TELL
//
// reCompare's right-hand side is `(.+)$` — it takes ALL the remaining text. So
// `status != "done" && priority > 3` matched it as ONE comparison whose literal
// was the whole string `"done" && priority > 3`. unquoteLiteral needs matching
// quotes at BOTH ends and there are none, so it came back as a bare, unquoted
// literal, and `!=` set Negate, which translate.go wraps as a tree `not`. The
// view then carried
//
//	NOT(status = '"done" && priority > 3')
//
// which no record's status ever equals, so the negation matches EVERY record —
// shipped CONVERTED, enabled, with zero losses recorded. A filter the operator
// wrote to narrow a view instead removed it, silently, and looked clean.
//
// The marker list did not catch it because it enumerates FUNCTION and FIELD
// shapes (`file.`, `formula.`, `today(`), not operators. So the operators are
// checked here, structurally, before any pattern runs — and only OUTSIDE quotes,
// because `vendor == "Smith && Sons"` is a legitimate literal that happens to
// contain the characters and refusing it would be a loss taken for nothing.
// ---------------------------------------------------------------------------

// containsUnquotedLogicalOperator reports whether the expression uses `&&` or
// `||` outside a quoted string literal — i.e. whether it is a COMPOUND
// expression rather than the single comparison every pattern below assumes.
//
// The scan is deliberately dumb about escapes: `.base` filter literals are
// short and this importer's job on anything it is not certain of is to refuse.
// A backslash-escaped quote would end the string early here and the expression
// would be refused; that is the safe direction.
func containsUnquotedLogicalOperator(s string) bool {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if i+1 < len(s) && (c == '&' || c == '|') && s[i+1] == c {
			return true
		}
	}
	return false
}

// parseLeaf translates one leaf expression string (already trimmed of a
// leading `- ` list marker by the YAML decoder).
func parseLeaf(expr string) parsedLeaf {
	s := strings.TrimSpace(expr)
	if s == "" {
		return parsedLeaf{Kind: leafUntranslatable}
	}
	if containsUnquotedLogicalOperator(s) {
		return parsedLeaf{Kind: leafUntranslatable}
	}
	if m := reFormulaCompare.FindStringSubmatch(s); m != nil {
		if leaf, ok := formulaCompareLeaf(m[1], m[2], strings.TrimSpace(m[3])); ok {
			return leaf
		}
		return parsedLeaf{Kind: leafUntranslatable}
	}
	if reUntranslatableMarker.MatchString(s) {
		return parsedLeaf{Kind: leafUntranslatable, Reason: untranslatableExpressionReason(s)}
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

// formulaCompareLeaf builds the leaf for `formula.<name> OP literal`.
//
// It is deliberately the SAME shape check the ordinary compare path makes and
// no looser: a right-hand side containing a call is refused rather than
// mis-parsed, and an empty literal is refused because a formula's absent result
// and the empty string are not the same thing to compare against (R-2 versus
// FR-007a). What it does NOT do is decide whether the formula exists or whether
// the literal suits its type — neither is knowable here, where there is one
// expression string and no formula set. buildFormulaLeafNode decides both,
// once the view's record type has resolved and the base's `formulas:` block has
// been translated against it.
// ---------------------------------------------------------------------------
// THE RIGHT-HAND SIDE MUST BE A LITERAL, AND CHECKING THAT IS NOT PEDANTRY
//
// `formula.age > formula.threshold` and `formula.age > cutoff_days` both match
// the comparison pattern above, and unquoteLiteral hands back an UNQUOTED
// operand for each — so without the check below this function built
//
//	{property: formula.age, op: >, value: "formula.threshold"}
//
// a comparison of a number against the eighteen-character TEXT
// `formula.threshold`. The view format has no property-to-property operator at
// all: `value` is always a literal. So the clause the operator wrote is not
// expressible, and the only honest answers are to refuse it or to invent one.
// It is refused, by name, and the view is disabled — which is what the founder's
// `is_stale: … formula.age > 30 …` would have needed had it been written the
// other way round.
// ---------------------------------------------------------------------------

// reFormulaComparableLiteral matches the operand spellings that are genuinely
// LITERALS when written unquoted: a number (with an optional sign and decimal
// part) and the two booleans. Everything else unquoted names something.
var reFormulaComparableLiteral = regexp.MustCompile(`^(-?\d+(\.\d+)?|true|false)$`)

func formulaCompareLeaf(name, op, rhsRaw string) (parsedLeaf, bool) {
	if strings.ContainsAny(rhsRaw, "()") {
		return parsedLeaf{}, false
	}
	lit, quoted, empty := unquoteLiteral(rhsRaw)
	if empty {
		return parsedLeaf{}, false
	}
	if !quoted && !reFormulaComparableLiteral.MatchString(strings.TrimSpace(rhsRaw)) {
		// An unquoted operand that is not a number or a boolean NAMES
		// something — another formula, another property, a `file.*` field.
		// See the block comment above.
		return parsedLeaf{}, false
	}
	var wireOp string
	switch op {
	case "==", "!=":
		wireOp = "eq"
	case "<":
		wireOp = "lt"
	case "<=":
		wireOp = "lte"
	case ">":
		wireOp = "gt"
	case ">=":
		wireOp = "gte"
	default:
		return parsedLeaf{}, false
	}
	return parsedLeaf{Kind: leafFilter, Filter: RawLeaf{
		Property: name, Op: wireOp, Values: []string{lit}, Negate: op == "!=",
	}}, true
}

// ---------------------------------------------------------------------------
// SAYING WHY A FUNCTION EXPRESSION WENT — WITHOUT WRITING A SECOND PARSER
//
// `date(close_date).year == today().year` is an ordinary "closing this year"
// filter, and reUntranslatableMarker used to refuse it in SILENCE: the loss
// line carried the operator's own text and no diagnosis at all, so the founder
// was told a clause went and never why. That is the FR-107 defect, not the
// refusal — the refusal is correct and stays.
//
// The temptation here is to teach this file what `date(x).year` MEANS, so it
// can translate it. That is how a codebase ends up with two parsers for one
// syntax, which then disagree, and the disagreement surfaces as a view that
// returns the wrong rows rather than as a compile error. This product has
// exactly ONE expression parser — records.ParseFormula — so the diagnosis is
// asked of that one and quoted verbatim. Nothing here decides what an
// expression means; it decides only which of two sentences to print.
//
// WHAT THE ANSWER IS TODAY. `.year` and `.month` ARE in the grammar — FR-143's
// own revision clause was exercised on 2026-09-01 (PIN REVISION 1) and the Date
// type field family was adopted whole, with its diff. So the second sentence
// below is the one Deals.base's "Closing This Month" now reaches, and it is no
// longer the end of the road: an expression the grammar reads and types as a
// TRUTH VALUE is carried as a formula this importer AUTHORS for the view. What
// this function still writes is the refusal for everything outside that narrow
// shape — see expressionFilterCandidate for the boundary and
// expressionNotCarriedAsFormula for the sentence.
// ---------------------------------------------------------------------------

// filterIsNotAnExpression is the shared opening of both sentences below, and it
// is deliberately one literal: report.go's closed gap table classifies a loss
// by matching substrings of the reason this importer wrote, so a sentence that
// exists in two spellings is a gap shape that catches half its own cases.
const filterIsNotAnExpression = "a view's filter compares a PROPERTY against a literal, and this clause is an EXPRESSION"

// untranslatableExpressionReason explains an expression the leaf patterns could
// not read, or returns "" for the shapes whose diagnosis belongs elsewhere.
//
// The `formula.` namespace is the deliberate hole. A `formula.<name>` clause
// that cannot be built is refused by buildFormulaLeafNode, which knows the
// base's formula set and the view's record type and can therefore say something
// specific ("the base file declares no formula named X", "operator > is not
// defined on a LIST"). Printing this file's generic sentence on top of that
// would name the wrong gap — a formula problem reported as a function-call
// problem — so the formula namespace is left exactly as it was found.
func untranslatableExpressionReason(expr string) string {
	if strings.Contains(expr, formulaNamespace) {
		return ""
	}
	if _, err := records.ParseFormula(expr); err != nil {
		return fmt.Sprintf("%s. Handed to the formula grammar — the ONE expression parser this product has, so that this refusal is the grammar's own and not a second parser's opinion — it is refused there too: %s",
			filterIsNotAnExpression, err)
	}
	return expressionNotCarriedAsFormula(fileNamespaceExpressionRefusal)
}

// fileNamespaceExpressionRefusal is the one reason parseLeaf itself can give
// for declining to author a formula: the clause is written in the `file.`
// namespace.
//
// It is a REFUSAL RATHER THAN A GAP. FR-134 already gives `file.inFolder`,
// `file.hasTag` and `file.hasLink` a normative translation, built by
// records.TranslateFileMethod and emitted as ordinary filter leaves
// (translate.go's reFileMethod). Authoring a formula for a `file.*` expression
// would give this product a SECOND translation of the same file metadata —
// two paths that can disagree about one question, with the disagreement
// surfacing as a view returning the wrong rows rather than as a build failure.
// One translation per construct is worth more than one more imported clause.
const fileNamespaceExpressionRefusal = "it is written in the `file.` namespace, whose meaning is FR-134's normative translation (`file.inFolder`, `file.hasTag`, `file.hasLink`, emitted as ordinary filter leaves) rather than a formula this importer invents — carrying it as a formula would give this product two different translations of the same file metadata, free to disagree"

// expressionNotCarriedAsFormula is the sentence for an expression the grammar
// READS and this importer still would not carry, naming which of the
// containment conditions it failed.
//
// It opens with filterIsNotAnExpression for the same reason every sentence in
// this file does: report.go's closed gap table classifies a loss by matching
// substrings of the reason the importer wrote, so the shared opening is the
// coupling that keeps these losses in the `.base`-expression bucket instead of
// falling into UNCLASSIFIED.
func expressionNotCarriedAsFormula(why string) string {
	return fmt.Sprintf("%s. The formula grammar reads it, but a filter leaf holds a property, an operator and a literal, and the only way an expression reaches one is as a `formula.<name>` the view declares. This importer WILL author that formula for such a clause (FR-140), under conditions it keeps deliberately narrow, and this clause fails one of them: %s",
		filterIsNotAnExpression, why)
}

// ---------------------------------------------------------------------------
// WHAT MAY BECOME AN AUTHORED FORMULA, DECIDED IN TWO PLACES
//
// expressionFilterCandidate decides only what can be decided from ONE
// EXPRESSION STRING — it has no schema, no record type, no formula set and no
// budget. It admits a clause into the candidate pool; it never accepts one.
// Everything that needs context is decided in view_write.go's
// synthesiseFilterFormulas: the static TYPE (which must be a truth value), the
// per-view FR-146 budget, and the name's freedom from collision with a formula
// the operator wrote.
//
// The two namespaces excluded here are excluded for two different reasons and
// neither is "we did not get round to it":
//
//	formula.   Already a computed property. A `formula.<name>` clause that
//	           cannot be built is diagnosed by buildFormulaLeafNode, which
//	           knows the base's formula set and can say something specific;
//	           wrapping one formula inside another this importer wrote would
//	           bury that diagnosis under a name the operator never saw.
//	file.      FR-134 owns it. See fileNamespaceExpressionRefusal.
// ---------------------------------------------------------------------------

// expressionFilterCandidate reports whether one filter expression is a
// CANDIDATE for the authored-formula path.
//
// IT IS EXACTLY THE MARKER BRANCH'S OWN CONDITION, NARROWED, and it is written
// that way on purpose. parseLeaf refuses a marker-matched expression; this
// function names the subset of those refusals that may instead become a
// formula, so the candidate pool can never be WIDER than what parseLeaf was
// already refusing. Nothing that used to translate as a leaf can start
// travelling this path, and nothing outside the function/field shapes the
// marker enumerates can enter it — a bare `true`, a `type != "x"` set
// difference and a `.contains` on the discriminator are all refused before
// they get here, by the same code that always refused them.
//
// translate.go, not parseLeaf, is where this is consulted: the answer produces
// a rawKindExpression node, which is a TREE-level object, and parseLeaf's
// contract — one expression string in, one leaf-or-refusal out — is left as it
// was.
func expressionFilterCandidate(expr string) bool {
	s := strings.TrimSpace(expr)
	if s == "" {
		return false
	}
	if !reUntranslatableMarker.MatchString(s) {
		return false
	}
	if strings.Contains(s, formulaNamespace) || strings.Contains(s, fileNamespacePrefix) {
		return false
	}
	if _, err := records.ParseFormula(s); err != nil {
		return false
	}
	return true
}

// fileNamespacePrefix is FR-130's reserved prefix for a file-metadata property.
// records.IsFileNamespace answers for a whole property NAME; this is a
// substring test over an expression, which is a different question.
const fileNamespacePrefix = "file."
