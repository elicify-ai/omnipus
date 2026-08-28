// Omnipus — ADR-068 D3.2 / spec FR-007, FR-008, FR-010, FR-022b: absence,
// negation, order, and SQL's operator vocabulary.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// SCOPE OF THIS FILE — read this before extending it
//
// This is NOT the query engine (FR-021/FR-022) and not the comparison truth
// table (spec §8). It is the slice of predicate behaviour that FR-007 and
// FR-008 require in order to be testable at all, built to the §8 rules so that
// whoever writes the full table later inherits a comparator that already obeys
// them rather than one they must first correct.
//
//	FR-007  An absent property is a DISTINCT state from every value.
//	FR-008  A negative filter INCLUDES records where the property is absent,
//	        unless the query excludes them explicitly.
//	FR-010  Enums order LEXICALLY (ruling R-E). Domain order is expressed by
//	        prefixing the values — `1-lead`, `2-qualified` — which is visible in
//	        the operator's own file and does exactly what it appears to do.
//
// ---------------------------------------------------------------------------
// THE OPERATOR VOCABULARY IS SQL'S, AND THE FILTER IS STILL AN OBJECT
//
// ADR-068 O-3 as amended, spec FR-022b / ruling R-B. The ten operators are
// `=`, `<>`, `<`, `<=`, `>`, `>=`, `LIKE`, `IN`, `IS NULL`, `IS NOT NULL`, each
// carrying SQL's own meaning. They replace the seven we invented — `eq`, `lt`,
// `lte`, `gt`, `gte`, `contains`, `is_absent` — and the argument is retrieval
// accuracy rather than style: our vocabulary has appeared in a model's training
// data zero times and SQL's an enormous number of times, so a model choosing
// `LIKE` is recalling where a model choosing `contains` was guessing.
//
// BOTH HALVES OF O-3 STILL HOLD. The filter is a STRUCTURED OBJECT and there is
// NO PARSER:
//
//	{property: "status", op: "=",    value: "open"}
//	{property: "tags",   op: "LIKE", value: "vend%"}
//	{property: "owner",  op: "IS NULL"}
//
// Nothing in this package recognises SQL text, and nothing should. A model
// fluent in SQL that reaches for `JOIN`, `BETWEEN` or `GROUP_CONCAT` puts it in
// the operator position, where it is REFUSED BY NAME with the supported set and
// the parameter that does the job — never parsed, never silently dropped, never
// an empty result set. See unsupportedRemedy and FR-022c's scoping note below.
//
// ---------------------------------------------------------------------------
// THERE IS EXACTLY ONE COMPARISON IMPLEMENTATION, AND IT IS NOT IN THIS FILE
//
// Every comparison this file needs is delegated to compare_oracle.go's
// `Comparator.Evaluate`, which implements spec §8's rules and is verified
// cell-by-cell by compare_truthtable_test.go.
//
// This file used to carry its own small comparator "just for FR-007/FR-008".
// That arrangement was the false-green shape §8 exists to prevent: the VERIFIED
// comparator sat off the query path while the UNVERIFIED one did the real
// filtering, so a truth table guaranteed the correctness of code nobody called.
// Do not reintroduce a second comparison path here for any reason. If the oracle
// does not express something filtering needs, that is a gap in the ORACLE — fix
// it there, under a numbered rule.
//
// The same prohibition applies one layer down: no predicate in this file may be
// pushed into SQL. The properties index NARROWS CANDIDATES; the comparator
// DECIDES (ruling R-A).
//
// ---------------------------------------------------------------------------
// WHAT THIS FILE DOES OWN: THE MATCHING LAYER
//
// Comparison and matching are two different things, and keeping them apart is
// what makes FR-008 and §8 R-2 both true at once.
//
// R-2 says: "A comparison where either side is absent is false, for every
// operator except `IS NULL`." FR-008 says a negative filter must INCLUDE absent
// records. Read carelessly these contradict: false, negated, is true — but then
// FR-008 would be an accident of double negation rather than a rule, and
// `ExcludeAbsent` would have nothing to switch off.
//
//	Comparator.Evaluate  — the §8 oracle. Absent compares false. Always.
//	Filter.Match         — asks the oracle, then applies FR-008's inclusion
//	                       rule as an EXPLICIT step on top.
//
// So "status is not done" includes the days nobody recorded a status — which is
// D3.2's whole point, the checkbox third state: "Days I did not meditate"
// currently omits every day with no value, precisely the days being asked about.
//
// `<>` IS NOT A SHORTHAND FOR THAT, and the distinction is ruled explicitly in
// §8 R-2 (review round 6, C-7). `<>` is a LEAF and `Negate` is a TREE. A `<>`
// leaf over an absent side is FALSE, because in SQL `x <> 'v'` over a NULL `x`
// excludes the row, and adopting SQL's names without SQL's semantics is exactly
// what ruling R-B forbids. To ask "which records are not `v`, including those
// that never said", negate an `=` leaf — which R-2 makes correct by
// construction at any depth — or write `any` over `<>` and `IS NULL`.
//
// ---------------------------------------------------------------------------
// AND THE ONE THAT LOOKS LIKE A BUG AND IS NOT
//
// A value the oracle could not compare — non-conforming (R-4), an unresolved
// relation (R-8), or an operator no rule defines for that type — does not match
// a NEGATIVE filter either. Not because the comparison came out true, but
// because a comparison that could not be made is REPORTED and the record
// EXCLUDED, rather than swept into an answer by double negation. FR-025/026
// exist so a caller sees the record named in the problem list; quietly counting
// a corrupt value as "not done" would be a silent wrong answer, which is the
// defect this ADR exists to remove.
//
// Absence is a legitimate state and IS re-included. A failed comparison is not.
// ---------------------------------------------------------------------------

// Operator is a comparison, spelled as SQL spells it (FR-022b).
//
// Negation of a SUBTREE is a separate flag on Filter rather than a set of
// `NOT_` operators, so there is exactly one place negation is applied and
// exactly one place FR-008's inclusion rule can be forgotten. `<>` is not that
// flag — see the header note.
type Operator string

const (
	// OpEqual is exact. On text and enum labels it is CASE-INSENSITIVE
	// (FR-011a, ruling R-D), folded with full Unicode case folding.
	OpEqual Operator = "="
	// OpNotEqual is SQL's `<>`. It is governed by R-2 like every other
	// operator: over an absent side it is FALSE, not true.
	OpNotEqual Operator = "<>"

	OpLess           Operator = "<"
	OpLessOrEqual    Operator = "<="
	OpGreater        Operator = ">"
	OpGreaterOrEqual Operator = ">="

	// OpLike is SQL's LIKE: `%` and `_` are the wildcards, `\` is the escape,
	// the match is ANCHORED to the whole value, and it is case-insensitive.
	// Against a `many` property it matches an ELEMENT by pattern (R-9).
	OpLike Operator = "LIKE"
	// OpIn is set membership. Its value is a NON-EMPTY array (FR-022d); a
	// single-element array means the same as `=`.
	OpIn Operator = "IN"

	// OpIsNull replaces the invented `is_absent` and keeps its exemption: it is
	// one of the two operators absence does not make false, and it is exempt
	// from FR-008's inclusion rule. It takes NO value.
	OpIsNull Operator = "IS NULL"
	// OpIsNotNull is OpIsNull's complement and shares its exemption. It takes
	// NO value.
	OpIsNotNull Operator = "IS NOT NULL"
)

// Operators is FR-022b's closed ten, in the order a refusal lists them.
var Operators = []Operator{
	OpEqual, OpNotEqual,
	OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual,
	OpLike, OpIn,
	OpIsNull, OpIsNotNull,
}

// OperatorNames is Operators as strings, for a refusal message.
func OperatorNames() []string {
	names := make([]string, 0, len(Operators))
	for _, o := range Operators {
		names = append(names, string(o))
	}
	return names
}

func isKnownOperator(op Operator) bool {
	for _, k := range Operators {
		if k == op {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// FR-022c — an unsupported SQL construct is REFUSED, naming what IS supported
//
// Ruling R-C. A model fluent in SQL will reach for `JOIN`, `BETWEEN`, `EXISTS`,
// `GROUP_CONCAT`, `COALESCE`, `CASE` or a function call. FR-022c is SCOPED to
// what a `{property, op, value}` object can actually express, because as it was
// first written it could not be satisfied without the parser O-3 forbids:
//
//	AN OPERATOR outside the closed ten arrives in `op` and is refused BY NAME,
//	listing the ten and naming the parameter that does the job. That covers
//	`JOIN`, `BETWEEN`, `EXISTS`, `GROUP_CONCAT`, `ILIKE`, `REGEXP`, `!=`, and
//	anything else a model puts in the operator position — including a whole
//	`COALESCE(...)` or `CASE ...` fragment, which is refused as an operator
//	rather than parsed.
//
//	A PARAMETER the request does not declare (`where:`, `sql:`, `having:`) is
//	refused by name, listing the accepted parameters. UnsupportedParameterError
//	is that refusal; the request layer supplies its own accepted-name list,
//	because this package does not own the request shape.
//
//	A PROPERTY the schema does not declare is refused by FR-024, listing the
//	valid property names. This is where `COALESCE(status,'open')` in the
//	PROPERTY position lands, and the refusal names the wrong problem ("unknown
//	property"). That is imperfect and is stated rather than papered over: it is
//	non-silent, it lists real alternatives, and closing the gap properly would
//	need the parser.
//
//	A SQL FRAGMENT INSIDE `value` is treated as a TEXT LITERAL and never
//	recognised as SQL. Against a typed property it does not parse, and the query
//	is refused naming the offending value and the shape that would have been
//	accepted — see Validate's literal step, which records why an up-front
//	refusal is used there rather than a per-record problem.
//
// In no case is there a parse error, a silent empty result, or a partial
// evaluation with the unsupported clause dropped. Dropping the clause is the
// failure mode that matters: it returns a plausible answer to a different
// question.
// ---------------------------------------------------------------------------

// sqlConstructRemedy maps a SQL construct a model may reach for onto the thing
// in this system that does the job. Keys are upper-cased and whitespace-collapsed.
//
// ADR-068 D0 forbids hardcoded DOMAIN vocabulary — record types, property names,
// enum values. This is not that: it is SQL's own grammar, which is fixed by the
// language and not by anybody's vault.
var sqlConstructRemedy = map[string]string{
	"JOIN":       "follow a relation with the `join` parameter",
	"LEFT JOIN":  "follow a relation with the `join` parameter",
	"RIGHT JOIN": "follow a relation with the `join` parameter",
	"INNER JOIN": "follow a relation with the `join` parameter",
	"OUTER JOIN": "follow a relation with the `join` parameter",
	"CROSS JOIN": "follow a relation with the `join` parameter",
	"FULL JOIN":  "follow a relation with the `join` parameter",

	"GROUP BY":     "group with the `group_by` parameter",
	"GROUP_CONCAT": "group with the `group_by` parameter",
	"STRING_AGG":   "group with the `group_by` parameter",
	"HAVING":       "group with `group_by`, then filter on the grouped property",

	"ORDER BY": "order with the `sort` parameter",
	"LIMIT":    "page with the `page_size` parameter",
	"OFFSET":   "page with the `page` parameter",

	"SUM":   "total with the `aggregate` parameter",
	"COUNT": "total with the `aggregate` parameter",
	"AVG":   "total with the `aggregate` parameter",
	"MIN":   "total with the `aggregate` parameter",
	"MAX":   "total with the `aggregate` parameter",
	"TOTAL": "total with the `aggregate` parameter",

	"BETWEEN": "write two leaves: one `>=` and one `<=`",

	"SELECT":    "the filter is a structured object, not a query string; give `property`, `op` and `value`",
	"WITH":      "the filter is a structured object, not a query string; give `property`, `op` and `value`",
	"EXISTS":    "test a property directly with `IS NOT NULL`, or follow the relation with `join`",
	"NOT EXISTS": "test a property directly with `IS NULL`, or follow the relation with `join`",
	"UNION":     "run the queries separately, or express the alternatives as `any`",
	"INTERSECT": "express the conditions as `all`",
	"EXCEPT":    "express the exclusion as `not`",
	"DISTINCT":  "results are already one row per record",

	"COALESCE": "absence is first-class here: use `IS NULL` or `IS NOT NULL`",
	"IFNULL":   "absence is first-class here: use `IS NULL` or `IS NOT NULL`",
	"NVL":      "absence is first-class here: use `IS NULL` or `IS NOT NULL`",
	"CASE":     "express each branch as its own leaf under `any`",
	"IIF":      "express each branch as its own leaf under `any`",

	"NOT LIKE": "wrap a `LIKE` leaf in `not`",
	"NOT IN":   "wrap an `IN` leaf in `not`",
	"ILIKE":    "use `LIKE` — matching is already case-insensitive",
	"REGEXP":   "use `LIKE` with `%` and `_`",
	"RLIKE":    "use `LIKE` with `%` and `_`",
	"GLOB":     "use `LIKE` with `%` and `_`",
	"MATCH":    "use `LIKE` with `%` and `_`, or search with plain words",
	"~":        "use `LIKE` with `%` and `_`",
	"~*":       "use `LIKE` with `%` and `_`",

	"IS DISTINCT FROM":     "use `<>`; absence is asked about with `IS NULL`",
	"IS NOT DISTINCT FROM": "use `=`; absence is asked about with `IS NULL`",
	"==":                   "use `=`",
	"!=":                   "use `<>`",
	"<>ALL":                "use `IN` with an array value, wrapped in `not`",
	"ANY":                  "use `IN` with an array value",
	"ALL":                  "use `IN` with an array value, wrapped in `not`",
	"SOME":                 "use `IN` with an array value",
	"LIKE ANY":             "use `IN` with an array value, or one `LIKE` leaf per pattern under `any`",

	"CAST":      "types are declared in the schema; nothing is coerced at query time",
	"CONVERT":   "types are declared in the schema; nothing is coerced at query time",
	"LOWER":     "matching is already case-insensitive; compare the value directly",
	"UPPER":     "matching is already case-insensitive; compare the value directly",
	"SUBSTR":    "use `LIKE` with `%`",
	"SUBSTRING": "use `LIKE` with `%`",
	"INSTR":     "use `LIKE` with `%`",
	"POSITION":  "use `LIKE` with `%`",
}

// unsupportedRemedy names the parameter or operator that does the job, for a
// construct that arrived in the operator position.
//
// It matches on the construct's leading words, longest first, after upper-casing
// and collapsing whitespace, and it cuts the first token at `(` so that
// `COALESCE(status,'open')` and `SUM(amount)` resolve. It NEVER parses: it looks
// a name up in a fixed table and returns "" when the name is not there. The
// caller still lists the supported operators, so an unrecognised construct is
// refused just as loudly — it simply gets no bespoke sentence.
func unsupportedRemedy(op Operator) string {
	fields := strings.Fields(strings.ToUpper(string(op)))
	if len(fields) == 0 {
		return ""
	}
	if i := strings.IndexRune(fields[0], '('); i > 0 {
		fields[0] = fields[0][:i]
	}
	for n := len(fields); n >= 1; n-- {
		if n > 4 {
			continue
		}
		if remedy, ok := sqlConstructRemedy[strings.Join(fields[:n], " ")]; ok {
			return remedy
		}
	}
	return ""
}

// Filter is one predicate over one property.
type Filter struct {
	// Property is the declared property name, scoped to the record type
	// (FR-009). It is validated against the schema BEFORE evaluation (FR-023).
	Property string
	Op       Operator
	// Negate inverts the predicate over the WHOLE leaf, and it is the tree-level
	// negation FR-008's inclusion rule attaches to. It is NOT `<>` — see the
	// header note; `status != done` written as `{Op: "=", Negate: true}` includes
	// the records that never said, and `{Op: "<>"}` does not.
	Negate bool
	// Literal is the comparison value in LEXICAL form — the same text a
	// frontmatter file would hold — so it is parsed by exactly the same code
	// path as a record's own value (§8 R-12: the rules apply identically
	// whether the value came from a query literal or from a record).
	//
	// It is unused by `IN` (which uses Literals) and forbidden for `IS NULL` and
	// `IS NOT NULL` (FR-022d).
	Literal string
	// LiteralGiven records that the caller supplied a value AT ALL, which a bare
	// string cannot express: FR-022d refuses a present `value` on `IS NULL`, and
	// the empty string is a legitimate value for `=`. The zero value is "not
	// given", so a filter built without thinking about it cannot be refused by
	// accident.
	LiteralGiven bool
	// Literals is `IN`'s candidate set. FR-022d: it MUST be non-empty, because
	// an empty `IN` list matches nothing and would return zero records for a
	// query the caller believes selects something — the silent empty result
	// FR-022c prohibits, arriving through a different door.
	Literals []string
	// ExcludeAbsent turns FR-008's inclusion off. It is opt-IN, so the default
	// behaviour is the one the requirement mandates and forgetting the field
	// cannot produce the wrong answer.
	ExcludeAbsent bool
}

// QueryError is a rejected query. FR-024: a query naming an unknown property or
// enum value is REJECTED with the valid names listed — it must not return an
// empty result set, because "no matches" and "you spelled it wrong" are
// indistinguishable to the caller and the second is far more common.
type QueryError struct {
	Property string
	Reason   string
	// ValidNames lists what would have been accepted in the position that was
	// wrong: property names for an unknown property, enum values for an unknown
	// value, parameter names for an unknown parameter.
	ValidNames []string
	// Supported lists FR-022b's operators that would have worked. FR-022c:
	// "in every case the refusal lists the supported operators."
	Supported []string
	// Remedy names the parameter or operator that does the job instead.
	Remedy string
}

func (e *QueryError) Error() string {
	var b strings.Builder
	b.WriteString(e.Reason)
	if len(e.ValidNames) > 0 {
		b.WriteString("; valid names are " + strings.Join(e.ValidNames, ", "))
	}
	if e.Remedy != "" {
		b.WriteString("; " + e.Remedy)
	}
	if len(e.Supported) > 0 {
		b.WriteString("; supported operators are " + strings.Join(e.Supported, ", "))
	}
	return b.String()
}

// UnsupportedParameterError is FR-022c's second clause: a parameter name the
// request schema does not declare is refused BY NAME, listing the accepted
// parameters. It exists here, next to the operator refusal, so that both halves
// of the same requirement read the same way and neither can quietly become a
// silent drop.
//
// The accepted list is the CALLER's, because this package does not own the
// request shape — the tool handler that decoded `where:` or `sql:` does. Passing
// it in is what keeps this refusal honest: a hardcoded list here would go stale
// the first time a parameter was added.
func UnsupportedParameterError(name string, accepted []string) *QueryError {
	return &QueryError{
		Reason:     fmt.Sprintf("%q is not a parameter this query accepts", name),
		ValidNames: append([]string(nil), accepted...),
		Remedy:     unsupportedRemedy(Operator(name)),
		Supported:  OperatorNames(),
	}
}

// Validate checks a filter against a record type's schema before any record is
// touched (FR-023). It returns the declared property and the parsed literal
// values — none for `IS NULL`/`IS NOT NULL`, one for a scalar operator, and
// `IN`'s whole candidate set.
//
// The order of the checks is the order the comparator applies its rules, so an
// up-front refusal and a per-record backstop can never describe the same
// situation two different ways:
//
//	operator known  -> property declared -> value SHAPE (FR-022d)
//	 -> type disposition -> arity (R-13) -> pattern sanity (FR-022a)
//	 -> the literals parse
func (f Filter) Validate(schema *Schema) (*Property, []TypedValue, error) {
	if !isKnownOperator(f.Op) {
		return nil, nil, &QueryError{
			Property:  f.Property,
			Reason:    fmt.Sprintf("%q is not a supported operator", string(f.Op)),
			Supported: OperatorNames(),
			Remedy:    unsupportedRemedy(f.Op),
		}
	}

	prop, ok := schema.Property(f.Property)
	if !ok {
		return nil, nil, &QueryError{
			Property:   f.Property,
			Reason:     fmt.Sprintf("record type %q has no property %q", schema.Type, f.Property),
			ValidNames: schema.PropertyNames(),
			Supported:  OperatorNames(),
			Remedy:     unsupportedRemedy(Operator(f.Property)),
		}
	}

	if err := f.validateValueShape(); err != nil {
		return nil, nil, err
	}

	// FR-024 applied to the OPERATOR as well as the property name.
	//
	// Without this, a filter naming an operator no rule defines for the property
	// was ACCEPTED here and then every record returned Matched=false plus one
	// identical comparison problem. A caller asking `name LIKE "Acme"` on a date
	// property got an empty answer and 5,000 copies of the same complaint
	// instead of one refusal naming the operators that would have worked — the
	// silently-empty result FR-024 exists to end, arriving through the operator
	// rather than the property name.
	//
	// TYPE FIRST, THEN ARITY, mirroring Comparator.Evaluate exactly. Checking
	// arity first would refuse `when LIKE '2026%'` on a `many date` property as
	// an arity problem when the real answer is that `LIKE` is not defined for a
	// date at either arity.
	switch f.Op {
	case OpIsNull, OpIsNotNull:
		// R-3 preempts the disposition for every type and every arity.
		return prop, nil, nil
	}
	if !operatorDefinedForType[prop.Type][f.Op] {
		return nil, nil, &QueryError{
			Property:   f.Property,
			Reason:     fmt.Sprintf("operator %q is not defined for a %s property", string(f.Op), prop.Type),
			ValidNames: definedOperatorNames(prop.Type),
			Supported:  definedOperatorNames(prop.Type),
			Remedy:     unsupportedRemedy(f.Op),
		}
	}
	if prop.Many && isOrderingOperator(f.Op) {
		return nil, nil, &QueryError{
			Property: f.Property,
			// The oracle's own R-13 sentence, reused verbatim so the up-front
			// refusal and the per-record backstop cannot describe one rule in
			// two different ways.
			Reason: arityRefusalDetail(f.Op,
				PropertyValue{Property: prop}, PropertyValue{Property: prop}),
			ValidNames: []string{string(OpEqual), string(OpNotEqual), string(OpIn), string(OpLike), string(OpIsNull), string(OpIsNotNull)},
			Supported:  []string{string(OpEqual), string(OpNotEqual), string(OpIn), string(OpLike), string(OpIsNull), string(OpIsNotNull)},
		}
	}

	// FR-022a — a `LIKE` pattern that constrains nothing is a whole-table result
	// returned as though it were a filtered one. The justification is
	// engine-independent: `''` and `'%'` match every value, which is true of
	// `LIKE` in any implementation.
	if f.Op == OpLike && likePatternMatchesEverything(f.Literal) {
		return nil, nil, &QueryError{
			Property:   f.Property,
			Reason:     fmt.Sprintf("the LIKE pattern %q matches every value, so it filters nothing", f.Literal),
			ValidNames: []string{string(OpIsNotNull)},
			Supported:  definedOperatorNames(prop.Type),
			Remedy:     "to ask which records have a value at all, use `IS NOT NULL`",
		}
	}

	// The literals go through ParseValue — the same function a record's own value
	// goes through — so an enum literal outside the declared set is rejected here
	// with the permitted values listed (FR-011 + FR-024).
	//
	// WHY AN UP-FRONT REFUSAL RATHER THAN A PER-RECORD PROBLEM. FR-022c says a
	// SQL fragment smuggled into `value` is treated as a text literal and lands
	// in the problem list under R-4. It IS treated as a text literal — nothing
	// here recognises SQL — but the refusal is raised once, here, instead of
	// once per candidate record. Both satisfy the requirement's actual demand
	// (never a parse error, never a silent empty result); one refusal naming the
	// offending value and the shape that would have been accepted is strictly
	// more actionable than N copies of the same complaint, and it is the posture
	// FR-024 takes for every other malformed input. Recorded here rather than
	// left to be discovered as a divergence.
	literals := f.literalTexts()
	parsed := make([]TypedValue, 0, len(literals))
	for _, text := range literals {
		lit, verr := ParseValue(prop, Node{Kind: KindScalar, Text: text})
		if verr != nil {
			return nil, nil, &QueryError{
				Property:   f.Property,
				Reason:     fmt.Sprintf("the value %q is not a valid %s: %s", text, prop.Type, verr.Reason),
				ValidNames: verr.Permitted,
				Supported:  definedOperatorNames(prop.Type),
			}
		}
		parsed = append(parsed, lit)
	}
	return prop, parsed, nil
}

// validateValueShape is FR-022d: three of the ten operators do not fit the
// `{property, op, value}` leaf, and each is refused by name rather than
// silently coerced.
func (f Filter) validateValueShape() error {
	switch f.Op {
	case OpIsNull, OpIsNotNull:
		if f.LiteralGiven || f.Literal != "" || len(f.Literals) > 0 {
			return &QueryError{
				Property: f.Property,
				// `null` is not permitted as a spelling of "no value" either,
				// and the reason is specific to this system: null is
				// indistinguishable from "the caller sent JSON null as the thing
				// to compare against", in a design whose central distinction is
				// absence (FR-007). Accepting it would put the ambiguity inside
				// the operator that exists to resolve it. The wire schema
				// enforces the JSON-null half; this enforces the Go half.
				Reason:    fmt.Sprintf("`%s` takes no value", string(f.Op)),
				Supported: OperatorNames(),
			}
		}
	case OpIn:
		if len(f.Literals) == 0 {
			return &QueryError{
				Property:  f.Property,
				Reason:    "`IN` takes a non-empty array of values",
				Supported: OperatorNames(),
				Remedy:    "give at least one value; a single-element array means the same as `=`",
			}
		}
	default:
		if len(f.Literals) > 0 {
			return &QueryError{
				Property:  f.Property,
				Reason:    fmt.Sprintf("`%s` takes a single value, not an array", string(f.Op)),
				Supported: OperatorNames(),
				Remedy:    "use `IN` for a set of candidate values",
			}
		}
	}
	return nil
}

// literalTexts is the lexical values this filter compares against: `IN`'s whole
// set, one value for a scalar operator, none for the unary two.
func (f Filter) literalTexts() []string {
	switch f.Op {
	case OpIsNull, OpIsNotNull:
		return nil
	case OpIn:
		return f.Literals
	}
	return []string{f.Literal}
}

// MatchResult is a filter's verdict on one record.
type MatchResult struct {
	// Matched is whether the record belongs in the result set.
	Matched bool
	// State is what the record actually said about the property. A caller
	// building a completeness verdict needs this to distinguish "excluded
	// because it did not match" from "excluded because it was corrupt".
	State PropertyState
	// Problems names anything the filter could not evaluate, in RECORD terms —
	// each Finding carries the record path, the property and the offending
	// value, which is what FR-026's "names the offending records" requires.
	Problems []Finding
	// ComparisonProblems carries the oracle's own rule-level verdicts (§8) — an
	// unresolved relation, an enum value the property does not declare, an
	// operator no rule defines for that type. They are kept separate from
	// Problems rather than flattened because they name a RULE, not a record, and
	// a caller reporting incompleteness needs both halves.
	ComparisonProblems []ComparisonProblem
}

// Match evaluates a filter against one record using the zero Comparator.
//
// The zero Comparator has no RelationResolver, so relation and person
// comparisons report CompareRelationUnresolved rather than silently comparing
// link text. Callers that can resolve relations use MatchWith.
func (f Filter) Match(schema *Schema, rec Record) (MatchResult, error) {
	return f.MatchWith(Comparator{}, schema, rec)
}

// MatchWith evaluates a filter against one record using a caller-supplied
// comparator.
//
// The order of the four cases below IS the requirement, in code:
//
//  1. `IS NULL` and `IS NOT NULL` answer directly — they are the two operators
//     absence does not make false (FR-007, §8 R-3), and both are exempt from
//     FR-008's inclusion rule.
//  2. A comparison the oracle COULD NOT MAKE is reported and excluded, whether
//     or not the filter is negated (see the header note).
//  3. ABSENT compares false (§8 R-2), and then FR-008 puts it BACK IN for a
//     negative filter unless the caller opted out.
//  4. Otherwise the oracle's answer stands, negated if asked.
func (f Filter) MatchWith(c Comparator, schema *Schema, rec Record) (MatchResult, error) {
	prop, literals, err := f.Validate(schema)
	if err != nil {
		return MatchResult{}, err
	}

	left := ResolveProperty(rec, prop)
	res := MatchResult{State: left.State, Problems: left.Findings}

	right := literalOperand(prop, literals)
	answer, cproblems := c.Evaluate(f.Op, left, right)
	res.ComparisonProblems = cproblems

	// (1) the unary two — FR-007 / R-3. A property holding a corrupt value is
	// NOT absent: something is written there, it is just wrong.
	switch f.Op {
	case OpIsNull, OpIsNotNull:
		res.Matched = answer != f.Negate
		return res, nil
	}

	// (2) the oracle could not compare — R-4, R-8, or an operator no rule
	// defines. Excluded and reported, NEVER re-included by negation.
	if left.State == StateNonConforming || len(cproblems) > 0 {
		res.Matched = false
		return res, nil
	}

	// (3) absent — §8 R-2 already gave the comparison's verdict; FR-008 now
	// decides whether the RECORD is nonetheless included.
	//
	// NOTE the `answer` on the next line, and do not "simplify" it to a literal
	// false. R-2 belongs to the oracle. An earlier version of this function
	// hardcoded false here — arithmetically identical while R-2 holds, and a
	// SECOND IMPLEMENTATION of R-2 all the same. It was caught by mutating the
	// oracle's R-2 branch and watching every filter test go on passing: the
	// rule was verified in the oracle and ignored on the path that uses it,
	// which is the exact arrangement §8 exists to prevent.
	if left.State == StateAbsent {
		res.Matched = answer
		if f.Negate {
			// FR-008: a negative filter includes the absent record — which,
			// given R-2's false, is the negation itself. ExcludeAbsent is the
			// explicit opt-out the requirement provides for.
			res.Matched = !answer && !f.ExcludeAbsent
		}
		return res, nil
	}

	// (4) the oracle's answer.
	res.Matched = answer != f.Negate
	return res, nil
}

// literalOperand wraps the query's literal values as the right-hand operand.
//
// The literal's property is a clone of the declared one with `many` set from the
// OPERATOR rather than from the schema, and both halves matter:
//
//   - `IN` carries a SET, so its operand is `many` and holds every candidate.
//     R-9's element-wise rule then does membership without a second code path:
//     any element of the record's value matching any element of the set is a
//     match, which is exactly what SQL's `IN` means.
//   - Every other operator carries ONE value even when the property it filters
//     is a list, so `many` is off. Leaving it on would make `tags = 'vendor'` a
//     list-against-list comparison for no reason.
//
// No literal at all (the `IS NULL` case, which reads only the left operand)
// yields an absent operand rather than a zero PropertyValue, so nothing
// downstream has to tolerate a nil Property.
func literalOperand(prop *Property, literals []TypedValue) PropertyValue {
	clone := *prop
	clone.Many = len(literals) > 1
	if len(literals) == 0 {
		return PropertyValue{Property: &clone, State: StateAbsent}
	}
	return PropertyValue{Property: &clone, State: StatePresent, Values: append([]TypedValue(nil), literals...)}
}

// Compare is a three-valued ordering VIEW of the oracle, for callers and tests
// that want -1/0/+1 rather than one boolean per operator.
//
// It owns no semantics. Every answer below comes from Comparator.Evaluate; this
// function only asks it three questions and reads the replies. ok=false means
// "the oracle does not order these two values" — different comparison domains
// (R-1), an operator no rule defines for the type (a relation has no order), or
// any reported problem.
//
// ENUM ORDERS HERE NOW, and it did not before. Under declared-position ordering
// two bare TypedValues carried no set between them, so R-5's precondition was
// genuinely unsatisfied and the oracle refused. Ruling R-E makes the order
// LEXICAL: it needs no declared set, so `won` and `blocked` from unrelated
// vocabularies order like the strings they are. That is the ruling's own
// trade — a domain order is expressed by prefixing the values, in the operator's
// own file, where they can see it.
func Compare(a, b TypedValue) (cmp int, ok bool) {
	var c Comparator
	left := singletonOperand(a)
	right := singletonOperand(b)

	for _, probe := range []struct {
		op  Operator
		cmp int
	}{
		{OpEqual, 0},
		{OpLess, -1},
		{OpGreater, 1},
	} {
		answer, problems := c.Evaluate(probe.op, left, right)
		if len(problems) > 0 {
			return 0, false
		}
		if answer {
			return probe.cmp, true
		}
	}
	// Not equal, not less, not greater, and nothing reported: the oracle
	// declined to relate them at all (R-1's different comparison domains).
	return 0, false
}

// singletonOperand wraps one typed value as a scalar operand. The synthesised
// property carries only the declared type, which is all the oracle reads once
// enum ordering is lexical.
func singletonOperand(v TypedValue) PropertyValue {
	return PropertyValue{
		Property: &Property{Type: v.Type},
		State:    StatePresent,
		Values:   []TypedValue{v},
	}
}

// SortValuesBySortKey orders value spellings by §8 R-5's sort key.
//
// RENAMED AND REVERSED from `SortByEnumOrder(prop, values)`, revision 7's ruling
// R-E. Enum values no longer sort by declared position — there is no ordinal to
// sort by, and the `*Property` argument the old signature took had nothing left
// to answer. A domain order is expressed by prefixing the values (`1-lead`,
// `2-qualified`), which is visible in the operator's own file.
//
// R-5's ordering rule in full, and every clause is load-bearing:
//
//	(b) the order is BYTE-LEXICAL over the value string — the same order
//	    SQLite's BINARY collation would produce, computed by us;
//	(c) the sort KEY is the FOLDED form, not the raw bytes. Byte-lexical order
//	    over raw values puts every capitalised value before every lowercase one
//	    ("Won" < "lost" is TRUE on raw bytes and FALSE folded), so a corpus that
//	    FR-011 deliberately permits to hold `Won`, `won` and `WON` as ONE value
//	    would render them in THREE separate places while `group_by` collapsed
//	    them into one group. Sorting on the folded key makes ordering, equality
//	    and grouping agree, which is the only combination a reader can reason
//	    about;
//	(d) TIES on the folded key are broken by RAW BYTE ORDER, so the order is
//	    total and deterministic across runs (R-11, ruling O-5). An implementation
//	    that leaves ties to Go's map iteration order breaks SC-014's
//	    byte-identical-across-rebuild assertion.
//
// The tie-break belongs HERE and NOT in the `<` operator — see textualAnswer's
// note. A sort must resolve a tie; an operator must not invent one.
//
// value.go's FoldLess IS that total order, and this function delegates to it
// rather than restating it. Two copies of an ordering rule is exactly the
// arrangement §8 exists to prevent: one gets a tie-break added and the other
// does not, and nothing fails.
func SortValuesBySortKey(values []string) {
	sort.SliceStable(values, func(i, j int) bool {
		return FoldLess(values[i], values[j])
	})
}
