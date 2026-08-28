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
