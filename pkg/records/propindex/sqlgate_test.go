// Omnipus — spec §7 test 39a / AC-8.10: the control that makes ruling R-A a
// PROPERTY rather than an intention.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE IS FIRST
//
// W1's exit criterion (a) says this test must exist and pass BEFORE anything
// else in W1 is accepted, and the reason is worth restating: revision 5's
// headline was "a single surviving SQL-side comparison reopens every violation",
// and that same revision deleted both of the only artifacts that could ever
// detect one. Review round 6 then found SEVEN surviving SQL-side evaluations in
// it, and nothing in the document would have caught an eighth.
//
// The assertion below is at the STORE boundary, not inside a query compiler, so
// it survives the compiler's deletion and cannot be satisfied by a comparator
// that is simply bypassed.
// ---------------------------------------------------------------------------

// forbidden is AC-8.10's list, each with the reason it is on it. A statement
// matching any of these outside the allow-list fails the build.
var forbidden = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a comparison operator", regexp.MustCompile(`(?i)(<=|>=|<>|!=|\s<\s|\s>\s|\s=\s|\bIS\s+NOT\b|\bIS\b)`)},
	{"LIKE", regexp.MustCompile(`(?i)\bLIKE\b`)},
	{"IN", regexp.MustCompile(`(?i)\bIN\s*\(`)},
	{"GROUP BY", regexp.MustCompile(`(?i)\bGROUP\s+BY\b`)},
	{"ORDER BY", regexp.MustCompile(`(?i)\bORDER\s+BY\b`)},
	{"an aggregate function", regexp.MustCompile(`(?i)\b(COUNT|SUM|TOTAL|AVG|MIN|MAX|GROUP_CONCAT)\s*\(`)},
	{"COLLATE", regexp.MustCompile(`(?i)\bCOLLATE\b`)},
}

// allowed is AC-8.10's named allow-list of NARROWING predicates, and nothing
// else. Each entry names why it narrows rather than decides.
//
// Adding an entry here is a SPECIFICATION change requiring the argument AC-8.2
// demands — not a test edit. Two entries carry a note because they are the ones
// a reader will challenge:
//
//   - The child-table join predicate. AC-8.10's table names "the relation child
//     table's rec_id join predicate", and gives as its reason "assembles a
//     record's `many` values into one row set; the fan-out is de-duplicated in
//     Go". That reason is a property of a CHILD TABLE, not of relations
//     specifically, and the property child table needs it for exactly the same
//     purpose. The form permitted here is therefore narrower than the reason and
//     wider than the literal words: an equality between two `note_id` columns of
//     a parent and its child, with no other operand shape admitted.
//   - COUNT(*) over the narrowing predicates. FR-064's B1 requires it in as many
//     words — "a COUNT(*) over the narrowing predicates only; one index-bound
//     aggregate, genuinely cheap and genuinely pre-retrieval" — while AC-8.10's
//     list bans aggregates without excepting it. The two are in literal
//     conflict. It is admitted here because B1 is unimplementable without it and
//     because it totals nothing the operator asked about: it counts rows of
//     `notes`, which is a population, not an answer. Every aggregate the
//     operator CAN request runs in Go. This conflict is reported upward rather
//     than resolved quietly.
var allowed = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(\w+\.)?record_type\s=\s\?$`),
	regexp.MustCompile(`^\s*(\w+\.)?kind\s=\s\?$`),
	regexp.MustCompile(`^\s*(\w+\.)?path\sLIKE\s\?\sESCAPE\s'\\\\?'$`),
	regexp.MustCompile(`^\s*\w+\.note_id\s=\s\w+\.note_id$`),
	regexp.MustCompile(`^\s*COUNT\(\*\)$`),
	regexp.MustCompile(`^\s*LIMIT\s\?(\sOFFSET\s\?)?$`),
}

// segments splits a statement into the pieces the allow-list is checked
// against: the boolean conjuncts of its WHERE clause, its join predicate, its
// select list and its tail. Splitting rather than whole-statement matching is
// deliberate — a check that passed because a forbidden operator sat next to an
// allow-listed one would be no check at all.
func segments(stmt string) []string {
	flat := strings.Join(strings.Fields(stmt), " ")
	var out []string
	for _, part := range regexp.MustCompile(`(?i)\bAND\b|\bON\b|\bWHERE\b|\bSELECT\b|\bFROM\b|\bJOIN\b`).Split(flat, -1) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isAllowed(seg string) bool {
	for _, re := range allowed {
		if re.MatchString(seg) {
			return true
		}
	}
	return false
}

// TestQuery_NoComparisonIsDelegatedToSQL is spec §7 test 39a.
func TestQuery_NoComparisonIsDelegatedToSQL(t *testing.T) {
	rec := NewRecorder()
	store, _ := openIndex(t, Options{Recorder: rec})
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		mustUpsert(t, store, plantNote(t, i, []string{"seedling", "growing", "dormant"}[i%3]))
	}
	rec.Reset()

	// Exercise every read path the store has. A statement that is never emitted
	// is never inspected, so the corpus below must reach all of them.
	sel := Selector{RecordType: "plant", Kind: KindNote, PathPrefix: "garden/"}
	if _, err := store.CountCandidates(ctx, sel); err != nil {
		t.Fatalf("CountCandidates: %v", err)
	}
	collect(t, store, sel)
	collect(t, store, Selector{})
	collect(t, store, Selector{Kind: KindNote})
	if err := store.Tasks(ctx, sel, func(TaskHit) error { return nil }); err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if err := store.Relations(ctx, sel, func(RelationHit) error { return nil }); err != nil {
		t.Fatalf("Relations: %v", err)
	}

	stmts := rec.InPhase(PhaseRead)
	if len(stmts) == 0 {
		t.Fatal("the recorder captured no read-path statements; the control is watching nothing")
	}
	for _, stmt := range stmts {
		for _, seg := range segments(stmt) {
			for _, f := range forbidden {
				if f.re.MatchString(seg) && !isAllowed(seg) {
					t.Errorf(
						"ruling R-A: the properties index emitted %s outside AC-8.10's narrowing allow-list.\n"+
							"  segment:   %q\n  statement: %s\n"+
							"SQLite must NARROW. Our own comparator DECIDES.",
						f.name, seg, stmt)
				}
			}
		}
	}
}

// TestSQLGate_ReadPathTouchesNoValueColumn is the second half of the same
// control, from the other direction.
//
// AC-8.10 lists constructs. This lists COLUMNS: no predicate may mention a
// column that holds a property value. A query that compared `v_text` with an
// operator the list above happens not to spell would pass that check and fail
// this one.
func TestSQLGate_ReadPathTouchesNoValueColumn(t *testing.T) {
	rec := NewRecorder()
	store, _ := openIndex(t, Options{Recorder: rec})
	ctx := context.Background()
	mustUpsert(t, store, plantNote(t, 1, "growing"))
	rec.Reset()

	sel := Selector{RecordType: "plant", PathPrefix: "garden/"}
	collect(t, store, sel)
	if err := store.Tasks(ctx, sel, func(TaskHit) error { return nil }); err != nil {
		t.Fatalf("Tasks: %v", err)
	}

	valueColumns := []string{"v_text", "v_num", "v_time", "v_link", "v_raw", "state", "target", "record_id", "status", "text"}
	for _, stmt := range rec.InPhase(PhaseRead) {
		i := strings.Index(strings.ToUpper(stmt), " WHERE ")
		if i < 0 {
			continue
		}
		where := stmt[i:]
		for _, col := range valueColumns {
			if regexp.MustCompile(`\b` + col + `\b`).MatchString(where) {
				t.Errorf("a value column %q appears in a predicate: %s", col, stmt)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// THE CAUSE, NOT ONLY THE EFFECT
//
// The two tests below close a watch-point found by the SQL-pushdown audit, and
// the reason it is worth two tests rather than one is worth stating.
//
// The audit passed on a structural ground, not a behavioural one: a typed
// predicate is UNEXPRESSIBLE here, because Selector has exactly three fields and
// none of them can hold a property name or a property value. That is a stronger
// guarantee than "no query currently pushes a predicate down" — it is "no query
// CAN".
//
// The guarantee has exactly one seam: a fourth field. And the first thing that
// breaks when one appears is not the no-comparison ruling — it is the BOUND.
// CountCandidates queries `notes`; Candidates queries `notes LEFT JOIN
// note_props`. They agree today only because the WHERE clause is generated once,
// by narrowing(), and the join is LEFT so it cannot drop a row. A property
// predicate cannot be applied by the count (there is no join to apply it to), so
// the counted population and the retrieved population would DIVERGE — silently,
// with FR-064's bound enforced against a different set from the one returned.
//
// A wrong number nobody would see. So:
//
//   - TestSelector_... asserts the CAUSE: the field set itself, by name.
//   - TestSQLGate_TheCountedPopulationIsTheRetrievedPopulation asserts the
//     EFFECT directly, at the recorder, and would fire even if someone added a
//     field AND updated the allow-list without thinking about the count.
//
// ---------------------------------------------------------------------------

// narrowingDimensions is the closed set of dimensions this store is permitted to
// narrow on, with the reason each one is structural rather than a comparison.
//
// Adding a row here is a DELIBERATE act with two obligations attached, and the
// test's failure message states both. It is an allow-list rather than a flat
// prohibition because a genuinely structural dimension could be legitimate — a
// collection identifier, say — and a guard that blocks the good case as hard as
// the bad one gets deleted by the first person who needs the good case.
var narrowingDimensions = map[string]string{
	"RecordType": "set membership over an indexed column; no property value is compared (D16.2b)",
	"Kind":       "note-kind narrowing; same argument as RecordType",
	"PathPrefix": "workspace/collection scope (FR-060); the LIKE pattern is built here from a resolved root, never from caller text",
}

// TestSelector_HasNoFieldThroughWhichAPredicateCouldReachSQL asserts the field
// set of Selector by name.
func TestSelector_HasNoFieldThroughWhichAPredicateCouldReachSQL(t *testing.T) {
	typ := reflect.TypeOf(Selector{})
	if typ.Kind() != reflect.Struct {
		t.Fatalf("Selector is a %s, not a struct; this guard reads its fields", typ.Kind())
	}

	seen := make(map[string]bool, typ.NumField())
	for i := range typ.NumField() {
		f := typ.Field(i)
		seen[f.Name] = true

		if _, ok := narrowingDimensions[f.Name]; !ok {
			t.Errorf(
				"Selector has grown a field %q (%s), and that field is the seam through which a "+
					"typed predicate reaches SQL.\n\n"+
					"Two things must be decided BEFORE it is added, and neither is decided by adding it:\n"+
					"  1. Is it structural narrowing — a set membership over an indexed column — or is it a "+
					"COMPARISON? Ruling R-A (ADR-068 D16.6) is that SQLite narrows and our own comparator "+
					"decides. SQLite's defaults contradict ten of the thirteen comparison rules, nine of them "+
					"silently, and it folds no non-ASCII case at all.\n"+
					"  2. What happens to CountCandidates/Candidates AGREEMENT? CountCandidates queries "+
					"`notes`; Candidates queries `notes LEFT JOIN note_props`. A predicate the count cannot "+
					"apply makes the counted population and the retrieved population diverge — and "+
					"CountCandidates feeds FR-064's B1 bound, so the bound would then be enforced against a "+
					"different set from the one returned. That is a wrong number with no error channel.\n\n"+
					"If it IS structural narrowing, add it to narrowingDimensions with the reason, and make "+
					"sure narrowing() applies it to BOTH queries.",
				f.Name, f.Type)
		}
		if f.Type.Kind() != reflect.String {
			t.Errorf("Selector.%s is a %s; every narrowing dimension is a single scalar value bound "+
				"into one placeholder. A slice or a map is a predicate wearing a struct field.",
				f.Name, f.Type.Kind())
		}
	}

	for name := range narrowingDimensions {
		if !seen[name] {
			t.Errorf("narrowingDimensions lists %q but Selector no longer has it; "+
				"the allow-list must describe the type, not a memory of it", name)
		}
	}
}

// TestSQLGate_TheCountedPopulationIsTheRetrievedPopulation asserts the effect.
//
// B1 is a precondition: it is counted before anything is retrieved, and it is
// only a bound on the work that follows if it counts the SAME population that
// work will visit. This compares the two statements' WHERE clauses directly,
// with the table alias normalised away, and requires them identical.
func TestSQLGate_TheCountedPopulationIsTheRetrievedPopulation(t *testing.T) {
	for _, sel := range []Selector{
		{},
		{RecordType: "plant"},
		{Kind: KindNote},
		{PathPrefix: "garden/"},
		{RecordType: "plant", Kind: KindNote, PathPrefix: "garden/"},
	} {
		rec := NewRecorder()
		store, _ := openIndex(t, Options{Recorder: rec})
		mustUpsert(t, store, plantNote(t, 1, "growing"))
		rec.Reset()

		if _, err := store.CountCandidates(t.Context(), sel); err != nil {
			t.Fatalf("CountCandidates(%+v): %v", sel, err)
		}
		collect(t, store, sel)

		stmts := rec.InPhase(PhaseRead)
		var counted, retrieved string
		var sawCount, sawStream bool
		for _, s := range stmts {
			switch {
			case strings.Contains(s, "COUNT(*)"):
				counted, sawCount = normaliseWhere(s), true
			case strings.Contains(s, "note_props"):
				retrieved, sawStream = normaliseWhere(s), true
			}
		}
		// Both statements must have been SEEN. A comparison of two empty strings
		// is the shape of a guard that passes because it inspected nothing.
		if !sawCount || !sawStream {
			t.Fatalf("selector %+v: captured count=%v stream=%v from %d statements; "+
				"this comparison needs both", sel, sawCount, sawStream, len(stmts))
		}
		if counted != retrieved {
			t.Errorf(
				"selector %+v: the population B1 COUNTS is not the population the stream RETRIEVES.\n"+
					"  counted:   %q\n  retrieved: %q\n"+
					"FR-064's B1 is a hard precondition on the work that follows. Counting one set and "+
					"visiting another makes it a bound on nothing, and the disagreement is silent.",
				sel, counted, retrieved)
		}
	}
}

// normaliseWhere extracts a statement's WHERE clause with the table alias
// removed, so `notes.record_type = ?` and `n.record_type = ?` compare equal —
// the alias is the one difference between the two statements that carries no
// meaning.
func normaliseWhere(stmt string) string {
	flat := strings.Join(strings.Fields(stmt), " ")
	i := strings.Index(strings.ToUpper(flat), " WHERE ")
	if i < 0 {
		return ""
	}
	where := flat[i+len(" WHERE "):]
	return regexp.MustCompile(`\b(notes|n)\.`).ReplaceAllString(where, "")
}

// TestSQLGate_OnlyOnePathToTheDriver is what makes the recorder unavoidable.
//
// A recorder you can go around reports green while the thing it watches happens
// somewhere else. This reads the package's own source and fails if any file but
// sqlgate.go reaches database/sql's execution methods directly.
func TestSQLGate_OnlyOnePathToTheDriver(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	direct := regexp.MustCompile(`\.(ExecContext|QueryContext|QueryRowContext|Exec|Query|QueryRow)\(`)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || name == "sqlgate.go" {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if direct.MatchString(line) {
				t.Errorf(
					"%s:%d reaches the driver directly, bypassing the AC-8.10 recorder:\n\t%s\n"+
						"every statement must go through sqlgate.go",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
