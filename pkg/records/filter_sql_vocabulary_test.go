// Omnipus — spec FR-022b / FR-022c / FR-022d, ADR-068 O-3 as amended: SQL's
// operator vocabulary, and the refusal that meets everything SQL has that we do
// not.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE GUARDS
//
// Ruling R-B replaced seven invented operators with ten of SQL's, and the whole
// argument for doing it is that a model has seen SQL an enormous number of times
// and has seen our vocabulary zero times. That argument cuts both ways: a model
// fluent enough in SQL to reach for `LIKE` unprompted is fluent enough to reach
// for `JOIN`, `BETWEEN`, `COALESCE` and a subquery — and ruling R-C says every
// one of them MUST be refused NAMING THE SUPPORTED SET, never parsed, never
// silently dropped, and never answered with an empty result.
//
// FR-022c is SCOPED, because as first written it could not be satisfied without
// the parser O-3 forbids. The filter is `{property, op, value}` with `op` drawn
// from a closed ten-member enum, so:
//
//	AN OPERATOR outside the ten arrives in `op` and is refusable BY NAME.
//	A PARAMETER the request does not declare is refusable BY NAME.
//	A PROPERTY the schema does not declare is refused by FR-024.
//	A SQL FRAGMENT INSIDE `value` has nowhere else to arrive: it is treated as a
//	  text literal and never recognised as SQL.
//
// The fourth is the one the spec flags as imperfect, and it is stated here as it
// is stated there rather than papered over — see the subquery test's comment.
// ---------------------------------------------------------------------------

const sqlVocabFixture = `
schema_version: 1
type: deal
properties:
  name:   { type: text }
  stage:  { type: enum, values: [lead, qualified, won] }
  amount: { type: decimal }
  closed: { type: date }
  tags:   { type: text, many: true }
`

func sqlVocabSchema(t *testing.T) *Schema {
	t.Helper()
	set := loadSet(t, map[string]string{"deal.yaml": sqlVocabFixture})
	sc, ok := set.Get("deal")
	if !ok {
		t.Fatalf("fixture schema did not load")
	}
	return sc
}

// TestFilter_FR022c_UnsupportedSQLIsRefusedNamingTheSupportedSet is ruling R-C's
// primary surface: a SQL construct in the OPERATOR position.
//
// Every case below is refused with (a) the construct named, (b) all ten
// supported operators listed, and (c) the parameter or operator that actually
// does the job. Never a parse error, never an empty result.
func TestFilter_FR022c_UnsupportedSQLIsRefusedNamingTheSupportedSet(t *testing.T) {
	sc := sqlVocabSchema(t)
	rec := ParseRecord("d.md", []byte("---\ntype: deal\nname: Acme\nstage: won\namount: 100.00\n---\n"))

	cases := []struct {
		op     Operator
		remedy string // a distinctive fragment of the remedy this construct earns
		why    string
	}{
		{"JOIN", "`join`", "a model reaching for a relation writes JOIN"},
		{"LEFT JOIN", "`join`", "and the qualified forms of it"},
		{"BETWEEN", "`>=`", "a range is two leaves here, and the refusal must say so"},
		{"GROUP BY", "`group_by`", "grouping is a parameter, not an operator"},
		{"GROUP_CONCAT", "`group_by`", "the aggregate spelling of the same reach"},
		{"HAVING", "`group_by`", "filtering groups"},
		{"ORDER BY", "`sort`", "ordering is a parameter"},
		{"SUM", "`aggregate`", "a total is a parameter"},
		{"COUNT(*)", "`aggregate`", "with the call syntax attached, which must not defeat the lookup"},
		{"COALESCE(stage,'lead')", "`IS NULL`", "absence is first-class here, which is the actual answer to COALESCE"},
		{"CASE", "`any`", "branching is expressed as alternatives"},
		{"EXISTS", "`IS NOT NULL`", "presence is a first-class question"},
		{"NOT LIKE", "`not`", "negation is a tree, not an operator"},
		{"NOT IN", "`not`", "same"},
		{"ILIKE", "case-insensitive", "matching is already case-insensitive, so ILIKE is redundant rather than missing"},
		{"REGEXP", "`LIKE`", "pattern matching has a supported spelling"},
		{"~", "`LIKE`", "and its operator form"},
		{"IS DISTINCT FROM", "`<>`", "the NULL-safe comparison SQL needs and this design does not"},
		{"!=", "`<>`", "the other spelling of not-equal; refused rather than silently accepted"},
		{"==", "`=`", "and the other spelling of equal"},
		{"CAST", "schema", "nothing is coerced at query time"},
		{"LOWER", "case-insensitive", "folding is not something the caller has to ask for"},
		{"SUBSTR", "`LIKE`", "substring is LIKE with %"},
		{"UNION", "`any`", "alternatives are a tree"},
		{"DISTINCT", "one row per record", "results are already de-duplicated"},
		{"LIMIT", "`page_size`", "paging is a parameter"},
	}

	for _, tc := range cases {
		t.Run(string(tc.op), func(t *testing.T) {
			f := Filter{Property: "stage", Op: tc.op, Literal: "won"}

			// It is refused at VALIDATE, before any record is read...
			_, _, err := f.Validate(sc)
			if err == nil {
				t.Fatalf("FR-022c: `%s` must be REFUSED; it validated clean (%s)", tc.op, tc.why)
			}
			var qe *QueryError
			if !errors.As(err, &qe) {
				t.Fatalf("the refusal must be a *QueryError the caller can read; got %T: %v", err, err)
			}
			msg := err.Error()

			// ...naming the construct...
			if !strings.Contains(msg, string(tc.op)) {
				t.Errorf("the refusal must NAME what was rejected.\n  got: %s", msg)
			}
			// ...listing every supported operator...
			if len(qe.Supported) != len(Operators) {
				t.Errorf("FR-022c: the refusal listed %d supported operators, want all %d.\n  got: %s",
					len(qe.Supported), len(Operators), msg)
			}
			for _, op := range Operators {
				if !strings.Contains(msg, string(op)) {
					t.Errorf("the refusal must list the supported operator %q.\n  got: %s", op, msg)
				}
			}
			// ...and naming the thing that does the job instead.
			if qe.Remedy == "" {
				t.Fatalf("FR-022c: `%s` was refused with no remedy. The requirement is that the refusal names the parameter that does the job (%s).\n  got: %s",
					tc.op, tc.why, msg)
			}
			if !strings.Contains(msg, tc.remedy) {
				t.Errorf("the remedy for `%s` must mention %q — %s.\n  got: %s", tc.op, tc.remedy, tc.why, msg)
			}

			// And NEVER a silent empty result: Match refuses too, without
			// touching the record.
			res, err := f.Match(sc, rec)
			if err == nil {
				t.Fatalf("FR-022c: `%s` through Match returned no error; a silent empty result is exactly what the rule forbids (Matched=%v)",
					tc.op, res.Matched)
			}
			if res.Matched || len(res.ComparisonProblems) != 0 || len(res.Problems) != 0 {
				t.Errorf("a refused query must produce no match and no per-record problems; got %v/%d/%d",
					res.Matched, len(res.ComparisonProblems), len(res.Problems))
			}
		})
	}
}

// TestFilter_FR022c_UnknownParameterIsRefusedNamingTheAccepted is ruling R-C's
// second clause: `where:`, `sql:`, `having:` and their kin arrive as PARAMETER
// names, not as operators, and they are refused by name too.
//
// The accepted list is the caller's, because this package does not own the
// request shape — the tool handler that decoded the parameter does. A hardcoded
// list here would go stale the first time a parameter was added, which is the
// slow way for a refusal to start lying.
func TestFilter_FR022c_UnknownParameterIsRefusedNamingTheAccepted(t *testing.T) {
	accepted := []string{"filter", "join", "group_by", "aggregate", "sort", "page", "page_size"}

	for _, param := range []string{"where", "sql", "having", "select", "order_by", "limit"} {
		err := UnsupportedParameterError(param, accepted)
		msg := err.Error()
		if !strings.Contains(msg, param) {
			t.Errorf("the refusal must NAME the parameter that was rejected.\n  got: %s", msg)
		}
		for _, name := range accepted {
			if !strings.Contains(msg, name) {
				t.Errorf("the refusal must list the accepted parameter %q.\n  got: %s", name, msg)
			}
		}
		if len(err.Supported) != len(Operators) {
			t.Errorf("FR-022c: the parameter refusal must also list the supported operators; got %d", len(err.Supported))
		}
	}
	// The ones that map onto a real feature carry the remedy too.
	for _, tc := range []struct{ param, remedy string }{
		{"having", "`group_by`"},
		{"order_by", "`sort`"},
		{"limit", "`page_size`"},
	} {
		if got := UnsupportedParameterError(tc.param, accepted).Remedy; !strings.Contains(got, tc.remedy) {
			t.Errorf("the remedy for the parameter %q must mention %q; got %q", tc.param, tc.remedy, got)
		}
	}
}

// TestFilter_FR022c_SQLInsideAValueIsATextLiteralAndIsRefusedLoudly is the
// clause the spec itself flags as imperfect, tested as it is specified rather
// than as anyone would prefer it worked.
//
// A subquery, a function call, `COALESCE(...)` and `CASE ...` HAVE NOWHERE TO
// ARRIVE in a `{property, op, value}` object except inside `value` or inside
// `property`. Detecting SQL inside a value string would require RECOGNISING SQL,
// which is the parser ADR-068 O-3 forbids, so:
//
//   - In the PROPERTY position it lands on FR-024's unknown-property refusal,
//     which names the wrong problem ("unknown property"). That is imperfect and
//     is stated rather than papered over: it is non-silent, it lists real
//     alternatives, and closing the gap properly would need the parser.
//   - In the VALUE position it is treated as a TEXT LITERAL and never recognised
//     as SQL. Against a typed property it does not parse, and the query is
//     refused naming the offending value and the shape that would have been
//     accepted.
//
// Both are REAL, REACHABLE, NON-SILENT answers, which is what FR-022c actually
// demands. What is asserted here is that neither is a parse error and neither is
// an empty result.
func TestFilter_FR022c_SQLInsideAValueIsATextLiteralAndIsRefusedLoudly(t *testing.T) {
	sc := sqlVocabSchema(t)
	rec := ParseRecord("d.md", []byte("---\ntype: deal\nname: Acme\nstage: won\namount: 100.00\n---\n"))

	t.Run("a subquery in the VALUE position, against a typed property", func(t *testing.T) {
		f := Filter{Property: "amount", Op: OpGreater, Literal: "(SELECT max(amount) FROM deals)"}
		_, err := f.Match(sc, rec)
		if err == nil {
			t.Fatal("FR-022c: a subquery smuggled into `value` must not produce a silent empty result")
		}
		msg := err.Error()
		if !strings.Contains(msg, "(SELECT max(amount) FROM deals)") {
			t.Errorf("the refusal must quote the OFFENDING VALUE so the caller can see what was read.\n  got: %s", msg)
		}
		if !strings.Contains(msg, "decimal") {
			t.Errorf("the refusal must name the declared type the value failed to be.\n  got: %s", msg)
		}
		// It must read as a VALUE-SHAPE refusal, not as a parse error: nothing
		// here parsed SQL or tried to.
		for _, forbidden := range []string{"syntax error", "parse error", "unexpected token"} {
			if strings.Contains(strings.ToLower(msg), forbidden) {
				t.Errorf("FR-022c: never a parse error. The value is a TEXT LITERAL that failed to be a decimal.\n  got: %s", msg)
			}
		}
	})

	t.Run("a function call in the PROPERTY position lands on FR-024", func(t *testing.T) {
		for _, property := range []string{"COALESCE(stage,'lead')", "CASE WHEN amount > 0 THEN 1 END", "max(amount)"} {
			f := Filter{Property: property, Op: OpEqual, Literal: "won"}
			_, err := f.Match(sc, rec)
			if err == nil {
				t.Fatalf("FR-024: %q in the property position must be refused, never answered with zero records", property)
			}
			msg := err.Error()
			// It lists REAL alternatives, which is what makes an imperfect
			// refusal still useful.
			for _, name := range []string{"name", "stage", "amount", "closed", "tags"} {
				if !strings.Contains(msg, name) {
					t.Errorf("the refusal must list the declared property names; %q missing.\n  got: %s", name, msg)
				}
			}
		}
	})

	t.Run("SQL in the value of a TEXT property is compared as text, not executed", func(t *testing.T) {
		// Against a text property the fragment parses fine — it is text — and
		// is then compared as the literal string it is. Nothing evaluates it,
		// and the record does not match, which is the correct answer to "is this
		// deal's name the string `(SELECT ...)`?".
		f := Filter{Property: "name", Op: OpEqual, Literal: "(SELECT name FROM deals LIMIT 1)"}
		res, err := f.Match(sc, rec)
		if err != nil {
			t.Fatalf("a SQL fragment against a TEXT property is a legitimate text literal; got %v", err)
		}
		if res.Matched {
			t.Fatal("the fragment must be compared as text, and this record's name is `Acme`")
		}
		// The proof that it was treated as a literal and not as SQL: a record
		// whose name IS that string matches.
		odd := ParseRecord("odd.md", []byte("---\ntype: deal\nname: \"(SELECT name FROM deals LIMIT 1)\"\n---\n"))
		res, err = f.Match(sc, odd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Matched {
			t.Fatal("a text literal is compared literally; a record whose name IS that string must match")
		}
	})
}

// TestFilter_FR022d_TheThreeOperatorsThatDoNotTakeAScalarValue is FR-022d: three
// of the ten do not fit the `{property, op, value}` leaf, and each is refused by
// name rather than silently coerced.
func TestFilter_FR022d_TheThreeOperatorsThatDoNotTakeAScalarValue(t *testing.T) {
	sc := sqlVocabSchema(t)

	t.Run("IS NULL and IS NOT NULL take NO value", func(t *testing.T) {
		for _, op := range []Operator{OpIsNull, OpIsNotNull} {
			// The correct shape validates.
			if _, _, err := (Filter{Property: "stage", Op: op}).Validate(sc); err != nil {
				t.Fatalf("`stage %s` with no value must validate; got %v", op, err)
			}
			// A present value is refused, naming the operator.
			for _, bad := range []Filter{
				{Property: "stage", Op: op, Literal: "won"},
				// The empty string is a VALUE, and `LiteralGiven` is how a bare
				// Go string carries the distinction a JSON `null` cannot: the
				// central distinction of this design is absence (FR-007), and
				// accepting `null` as a spelling of "no value" would put that
				// ambiguity inside the operator that exists to resolve it.
				{Property: "stage", Op: op, Literal: "", LiteralGiven: true},
				{Property: "stage", Op: op, Literals: []string{"won"}},
			} {
				_, _, err := bad.Validate(sc)
				if err == nil {
					t.Fatalf("FR-022d: `%s` with a value must be refused (literal=%q given=%v literals=%v)",
						op, bad.Literal, bad.LiteralGiven, bad.Literals)
				}
				if !strings.Contains(err.Error(), string(op)) {
					t.Errorf("the refusal must name the operator.\n  got: %s", err)
				}
			}
		}
	})

	t.Run("IN takes a NON-EMPTY array", func(t *testing.T) {
		if _, _, err := (Filter{Property: "stage", Op: OpIn, Literals: []string{"won"}}).Validate(sc); err != nil {
			t.Fatalf("FR-022d: a single-element IN is valid and means the same as `=`; got %v", err)
		}
		if _, _, err := (Filter{Property: "stage", Op: OpIn, Literals: []string{"won", "lead"}}).Validate(sc); err != nil {
			t.Fatalf("a multi-element IN must validate; got %v", err)
		}
		// The empty array is `LIKE ''`'s sibling: it matches nothing, so it
		// silently returns zero records for a query the caller believes selects
		// something.
		_, _, err := (Filter{Property: "stage", Op: OpIn}).Validate(sc)
		if err == nil {
			t.Fatal("FR-022d: an empty `IN` array must be refused; it is a silent empty result arriving through a different door")
		}
		if !strings.Contains(err.Error(), string(OpIn)) {
			t.Errorf("the refusal must name `IN`.\n  got: %s", err)
		}
		if !strings.Contains(err.Error(), string(OpEqual)) {
			t.Errorf("the refusal must say a single-element array means the same as `=`.\n  got: %s", err)
		}
	})

	t.Run("a scalar operator refuses an array", func(t *testing.T) {
		for _, op := range []Operator{OpEqual, OpNotEqual, OpGreater, OpLike} {
			_, _, err := (Filter{Property: "stage", Op: op, Literals: []string{"won", "lead"}}).Validate(sc)
			if err == nil {
				t.Fatalf("`%s` takes a single value, not an array; the array must be refused", op)
			}
			if !strings.Contains(err.Error(), string(OpIn)) {
				t.Errorf("the refusal must name `IN` as the operator for a set of values.\n  got: %s", err)
			}
		}
	})
}

// TestFilter_FR022a_APatternThatFiltersNothingIsRefused is FR-022a.
//
// A `LIKE` pattern of `''` or `'%'` matches every value, which is true of LIKE
// in ANY implementation — the justification is engine-independent. A whole-table
// result returned as though it were a filtered one is the failure; the refusal
// names `IS NOT NULL`, which is what the caller almost certainly meant.
func TestFilter_FR022a_APatternThatFiltersNothingIsRefused(t *testing.T) {
	sc := sqlVocabSchema(t)

	for _, pattern := range []string{"", "%", "%%", "%%%"} {
		_, _, err := (Filter{Property: "name", Op: OpLike, Literal: pattern}).Validate(sc)
		if err == nil {
			t.Fatalf("FR-022a: `name LIKE %q` matches every value and must be refused", pattern)
		}
		if !strings.Contains(err.Error(), string(OpIsNotNull)) {
			t.Errorf("the refusal must name `IS NOT NULL` as the operator that was probably meant.\n  got: %s", err)
		}
	}
	// A pattern that constrains ANYTHING is fine, including one that is almost
	// all wildcard.
	for _, pattern := range []string{"a", "%a%", "_", "%_%", `\%`} {
		if _, _, err := (Filter{Property: "name", Op: OpLike, Literal: pattern}).Validate(sc); err != nil {
			t.Errorf("`name LIKE %q` constrains the value and must be accepted; got %v", pattern, err)
		}
	}
}

// TestFilter_FR022b_TheVocabularyIsExactlySQLsTen is the ruling itself, asserted
// as a property of the package rather than as prose.
//
// The old seven — `eq`, `lt`, `lte`, `gt`, `gte`, `contains`, `is_absent` — must
// not be accepted under their old spellings, because an operator that is
// silently accepted under two names is an operator whose refusal cannot be
// trusted.
func TestFilter_FR022b_TheVocabularyIsExactlySQLsTen(t *testing.T) {
	want := []string{"=", "<>", "<", "<=", ">", ">=", "LIKE", "IN", "IS NULL", "IS NOT NULL"}
	if len(Operators) != len(want) {
		t.Fatalf("FR-022b declares exactly %d operators; the package has %d: %v", len(want), len(Operators), OperatorNames())
	}
	for i, w := range want {
		if string(Operators[i]) != w {
			t.Errorf("operator %d is %q, want %q (order matters: it is the order a refusal lists them)", i, Operators[i], w)
		}
	}

	sc := sqlVocabSchema(t)
	// The retired vocabulary is refused like any other unknown operator. It is
	// NOT aliased: a model that guesses `contains` is told the ten it can use,
	// which is the only way it learns.
	for _, retired := range []Operator{"eq", "lt", "lte", "gt", "gte", "contains", "is_absent", "neq", "ne"} {
		_, _, err := (Filter{Property: "stage", Op: retired, Literal: "won"}).Validate(sc)
		if err == nil {
			t.Errorf("the retired operator %q must be refused, not aliased onto its SQL replacement", retired)
			continue
		}
		var qe *QueryError
		if !errors.As(err, &qe) || len(qe.Supported) != len(Operators) {
			t.Errorf("the refusal for %q must list all ten supported operators; got %v", retired, err)
		}
	}
	// Case matters, because the operator set is a closed enum of exact strings
	// and not a fuzzy match. A near-miss is refused rather than guessed at.
	for _, nearMiss := range []Operator{"like", "Like", "in", "is null", "IS  NULL", " = "} {
		if _, _, err := (Filter{Property: "stage", Op: nearMiss, Literal: "won"}).Validate(sc); err == nil {
			t.Errorf("%q is not one of the ten and must be refused rather than normalised", nearMiss)
		}
	}
}
