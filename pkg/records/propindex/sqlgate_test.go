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

	"github.com/elicify-ai/omnipus/pkg/records"
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
// operatorSpacing canonicalises whitespace around comparison operators before
// anything is matched.
//
// THIS IS NOT COSMETIC. Without it the guard was BLIND to any comparison
// written without spaces: the `\s=\s`, `\s<\s` and `\s>\s` alternatives in
// `forbidden` each require whitespace on both sides, so
//
//	WHERE p.v_text=?      -- slipped through, entirely undetected
//	WHERE p.v_num>?       -- slipped through
//	WHERE p.v_num<10      -- slipped through
//	ON p.note_id=n.note_id AND p.v_text='x'   -- slipped through
//
// were all reported CLEAN by a guard whose whole purpose is to catch exactly
// those four statements. It passed twelve deliberate violation fixtures and was
// blind to the same violations one space narrower — the shape Stage 1 named:
// synthetic fixtures carry the spacing their author happened to type, and real
// code carries whatever the person writing it typed.
//
// Normalising first means the allow-list gets the same benefit: `record_type=?`
// canonicalises to `record_type = ?` and is recognised as the legitimate
// narrowing predicate it is, rather than flagged for the spacing.
var operatorSpacing = regexp.MustCompile(`<=|>=|<>|!=|=|<|>`)

func normaliseOperators(s string) string {
	return operatorSpacing.ReplaceAllString(s, " $0 ")
}

func segments(stmt string) []string {
	flat := strings.Join(strings.Fields(normaliseOperators(stmt)), " ")
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

// violation is one forbidden construct found in one segment of one statement.
type violation struct {
	construct string
	segment   string
}

// violations is THE guard, extracted from the test that drives it.
//
// It is a function rather than a loop inside a test because a guard nobody can
// call twice cannot be shown to FIRE. Every check below used to live inline in
// TestQuery_NoComparisonIsDelegatedToSQL, which meant the only evidence it
// worked was that it had never complained — indistinguishable from a guard that
// cannot complain. TestSQLGate_TheGuardFiresAndDoesNotCryWolf now drives it
// over both polarities, and the live test calls this same function, so the
// thing proven sensitive is the thing production is checked with.
func violations(stmt string) []violation {
	var out []violation
	for _, seg := range segments(stmt) {
		if isAllowed(seg) {
			continue
		}
		for _, f := range forbidden {
			if f.re.MatchString(seg) {
				out = append(out, violation{construct: f.name, segment: seg})
			}
		}
	}
	return out
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
	// FR-131's two new per-child streams. They are in the corpus for the same
	// reason every other read path is: a statement that is never emitted is
	// never inspected, and the control would then be watching a schema two
	// tables smaller than the one production queries.
	if err := store.Tags(ctx, sel, func(TagHit) error { return nil }); err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if err := store.Links(ctx, sel, func(LinkHit) error { return nil }); err != nil {
		t.Fatalf("Links: %v", err)
	}

	// The EVALUATION path, not only the store's own methods. This is the entry
	// point a query actually arrives through, and it is the one a future
	// optimisation would be tempted to push a predicate into — so it must be in
	// the corpus, or the control watches the layer nobody calls.
	sc := plantSchema(t)
	for _, filters := range [][]records.Filter{
		{{Property: "condition", Op: records.OpEqual, Literal: "growing", LiteralGiven: true}},
		{{Property: "cuttings", Op: records.OpGreater, Literal: "2", LiteralGiven: true}},
		{{Property: "species", Op: records.OpLike, Literal: "Mon%", LiteralGiven: true}},
		{{Property: "labels", Op: records.OpIn, Literals: []string{"indoor", "humid"}}},
		{{Property: "planted", Op: records.OpIsNull}},
		{{Property: "condition", Op: records.OpEqual, Literal: "growing", LiteralGiven: true, Negate: true}},
	} {
		if _, err := Evaluate(ctx, store, Query{
			Selector: Selector{PathPrefix: "garden/"},
			Schema:   sc,
			Filters:  filters,
		}, nil); err != nil {
			t.Fatalf("Evaluate(%+v): %v", filters, err)
		}
	}

	stmts := rec.InPhase(PhaseRead)
	if len(stmts) == 0 {
		t.Fatal("the recorder captured no read-path statements; the control is watching nothing")
	}
	for _, stmt := range stmts {
		for _, v := range violations(stmt) {
			t.Errorf(
				"ruling R-A: the properties index emitted %s outside AC-8.10's narrowing allow-list.\n"+
					"  segment:   %q\n  statement: %s\n"+
					"SQLite must NARROW. Our own comparator DECIDES.",
				v.construct, v.segment, stmt)
		}
	}
}

// ---------------------------------------------------------------------------
// THE GUARD'S OWN SENSITIVITY — both polarities
//
// Stage 1's standing rule: a guard that has never been SEEN to fire is
// indistinguishable from a guard that cannot fire, and the previous revision's
// instrumentation was deleted precisely because nobody could tell the
// difference. The other half matters just as much and is easier to forget: a
// guard that flags a legitimate shape gets DISABLED by the first person who
// needs that shape, and then it protects nothing at all.
//
// So both directions are pinned. The fixtures below do NOT replace the live
// recorder test above — fixtures never carry the incidental shapes real code
// does, which is exactly how a previous guard passed nine synthetic cases and
// stayed blind to the real defect. They are here to prove the machinery is
// sensitive, and to make a future tightening that would break production fail
// HERE first, where the reason is written down.
// ---------------------------------------------------------------------------

// mustReject are statements ruling R-A forbids. Each names the rule it breaks.
var mustReject = []struct {
	why  string
	stmt string
}{
	{
		why:  "R-1: a value comparison in SQL ranks any text above any number ('3' > 2 is 1)",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE p.v_text = ?`,
	},
	{
		why:  "a forbidden operator sitting NEXT TO an allow-listed one is still forbidden — this is why segments are checked, not whole statements",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE n.record_type = ? AND p.v_num > ?`,
	},
	{
		why:  "R-5/R-E: ORDER BY over a value column sorts alphabetically, not in declared order",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id ORDER BY p.v_text`,
	},
	{
		why:  "grouping is the comparator's, in Go — SQLite would group by raw bytes and split `Won`/`won`/`WON`",
		stmt: `SELECT p.v_text FROM note_props AS p GROUP BY p.v_text`,
	},
	{
		why:  "an aggregate over a VALUE totals an answer; the join fan-out made COUNT return 2 and SUM 200 where the truth was 1 and 100",
		stmt: `SELECT COUNT(*), SUM(p.v_num) FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id`,
	},
	{
		why:  "SQLite's LIKE is unanchored and folds ASCII only: 'ACME' LIKE '%acme%' is 1",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE p.v_text LIKE ?`,
	},
	{
		why:  "COLLATE is a comparison rule chosen in SQL; R-8 makes CO-0142 and co-0142 two distinct records",
		stmt: `SELECT n.path FROM notes AS n WHERE n.record_id = ? COLLATE NOCASE`,
	},
	{
		why:  "FR-021b's state flag is not SQLite's to interpret; set membership over it is still a decision",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE p.state IN (0, 1)`,
	},
	{
		why:  "R-2/R-3: absence is a distinguishable state decided in Go, and SQL's NULL semantics are the ones that get it wrong",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE p.v_time IS NOT NULL`,
	},
	{
		why:  "R-7: ordering dates in SQL, where unixepoch('not-a-date') returns NULL with no error",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE p.v_time <= ?`,
	},
	{
		why:  "a LIKE over the path with no ESCAPE clause admits a caller-supplied wildcard",
		stmt: `SELECT n.path FROM notes AS n WHERE n.path LIKE ?`,
	},
	{
		why:  "a join on anything but the note_id/note_id pair is outside the child-table exception",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.prop = n.record_type`,
	},

	// The four below are the SAME violations as above with the spaces removed.
	// Every one of them slipped through undetected until normaliseOperators was
	// added — the guard's `\s=\s` / `\s<\s` / `\s>\s` alternatives all required
	// whitespace, so a comparison one space narrower was invisible to a control
	// whose entire purpose is to catch it. They are pinned separately from their
	// spaced twins so that removing the normalisation fails HERE, loudly, rather
	// than quietly restoring the blind spot.
	{
		why:  "an UNSPACED equality is still an equality",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE p.v_text=?`,
	},
	{
		why:  "an unspaced `>` is still an ordering comparison",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE p.v_num>?`,
	},
	{
		why:  "an unspaced `<` against a literal, with no placeholder to make it conspicuous",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id = n.note_id WHERE p.v_num<10`,
	},
	{
		why:  "unspaced throughout, including the join — the shape a formatter or a hand-written query most plausibly produces",
		stmt: `SELECT n.path FROM notes AS n JOIN note_props AS p ON p.note_id=n.note_id WHERE p.v_text='x'`,
	},

	// Two more shapes a reviewer would expect to be caught and which no fixture
	// covered: a correlated subquery hiding the comparison one level down, and a
	// SQL function used to do the comparing.
	{
		why:  "a correlated subquery is still SQL deciding — the comparison is one level down, not absent",
		stmt: `SELECT n.path FROM notes AS n WHERE n.record_type = ? AND EXISTS (SELECT 1 FROM note_props AS p WHERE p.note_id = n.note_id AND p.v_text = ?)`,
	},
	{
		why:  "a scalar function doing the matching is a comparison SQLite evaluates, whatever it is spelled",
		stmt: `SELECT n.path FROM notes AS n WHERE instr(n.path, ?) > 0`,
	},
}

// mustAccept are the shapes production actually emits, pinned as NEGATIVE
// fixtures. A guard that rejects one of these would be deleted by whoever needs
// it, so it must fail here first, with the reason attached.
var mustAccept = []struct {
	why  string
	stmt string
}{
	{
		why:  "FR-064's B1: a COUNT(*) over the narrowing predicates only — it counts a POPULATION, not an answer",
		stmt: `SELECT COUNT(*) FROM notes WHERE notes.record_type = ? AND notes.kind = ? AND notes.path LIKE ? ESCAPE '\'`,
	},
	{
		why:  "B1 with no narrowing at all",
		stmt: `SELECT COUNT(*) FROM notes`,
	},
	{
		why:  "the candidate stream: a child-table join of the note_id/note_id shape, plus the three narrowing predicates",
		stmt: `SELECT n.note_id, n.path, n.record_type, n.record_id, n.source_hash, p.prop, p.elem, p.state, p.vtype, p.v_text, p.v_num, p.v_time, p.v_link, p.v_raw, p.quoted FROM notes AS n LEFT JOIN note_props AS p ON p.note_id = n.note_id WHERE n.record_type = ? AND n.kind = ? AND n.path LIKE ? ESCAPE '\'`,
	},
	{
		why:  "FR-076a's checkbox rows, same narrowing, same child-table join shape",
		stmt: `SELECT n.path, n.source_hash, t.line, t.status, t.text FROM notes AS n JOIN note_tasks AS t ON t.note_id = n.note_id WHERE n.kind = ?`,
	},
	{
		why:  "relation EDGES, returned for Go to compute reachability over",
		stmt: `SELECT n.path, n.record_type, n.record_id, n.source_hash, r.prop, r.elem, r.target, r.heading, r.display, r.raw FROM notes AS n JOIN note_relations AS r ON r.note_id = n.note_id WHERE n.record_type = ?`,
	},
	{
		why:  "FR-131's tag stream: the SAME admitted parent/child join shape over a new child table, and the same narrowing predicates",
		stmt: `SELECT n.path, n.source_hash, g.elem, g.tag FROM notes AS n JOIN note_tags AS g ON g.note_id = n.note_id WHERE n.kind = ?`,
	},
	{
		why:  "FR-131's link stream: one statement for links AND embeds, because partitioning on `embed` in SQL would be a predicate over a value column",
		stmt: `SELECT n.path, n.source_hash, l.elem, l.target, l.heading, l.display, l.raw, l.embed FROM notes AS n JOIN note_links AS l ON l.note_id = n.note_id WHERE n.path LIKE ? ESCAPE '\'`,
	},
	{
		why:  "a scope-only query narrows on the path prefix alone",
		stmt: `SELECT COUNT(*) FROM notes WHERE notes.path LIKE ? ESCAPE '\'`,
	},
}

// TestSQLGate_TheGuardFiresAndDoesNotCryWolf drives the guard over both
// polarities.
func TestSQLGate_TheGuardFiresAndDoesNotCryWolf(t *testing.T) {
	t.Run("it fires on every forbidden shape", func(t *testing.T) {
		for _, tc := range mustReject {
			if got := violations(tc.stmt); len(got) == 0 {
				t.Errorf(
					"the guard did NOT fire on a statement ruling R-A forbids.\n"+
						"  statement: %s\n  rule:      %s\n"+
						"A guard that has never been seen to fire is indistinguishable from one that "+
						"cannot. Seven SQL-side comparisons survived the revision whose headline was "+
						"this ruling, and nothing in it would have caught an eighth.",
					tc.stmt, tc.why)
			}
		}
	})

	t.Run("it accepts every shape production emits", func(t *testing.T) {
		for _, tc := range mustAccept {
			if got := violations(tc.stmt); len(got) > 0 {
				t.Errorf(
					"the guard flagged a LEGITIMATE narrowing statement as a violation.\n"+
						"  statement: %s\n  why it is allowed: %s\n  flagged:   %+v\n"+
						"This is how a guard gets disabled: the first person who needs this shape "+
						"deletes the check rather than the exception. If the allow-list genuinely "+
						"needs to narrow, change THIS fixture deliberately and say why.",
					tc.stmt, tc.why, got)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// THE COLUMN HALF — AC-8.10 part (b), extended by FR-135
//
// AC-8.10's construct list catches an operator. This half catches a COLUMN: no
// predicate in any read-path statement may mention a column that holds a value
// or a piece of file metadata, whatever operator it is written with. A
// comparison spelled with something the construct list happens not to enumerate
// passes that check and fails this one.
//
// IT IS A PARTITION, NOT A DENYLIST, and that is the whole point of the rewrite.
// A flat list of forbidden column names is VACUOUS in the direction that
// matters: delete "mtime" from it and nothing fails, because no statement
// mentions mtime in a predicate today and the check simply stops looking for a
// thing that was never there. That is a guard reporting green about a rule it no
// longer enforces — the exact shape §1.3 exists to remove, and the exact reason
// the previous revision's "extension" of this control on the construct half only
// was called a two-thirds omission.
//
// So every column of every table is classified into exactly one of two maps,
// checked against the DDL itself. Removing a column from valueColumns does not
// relax the guard; it makes the column UNCLASSIFIED, and
// TestSQLGate_EveryColumnIsClassified fails naming it. Adding a column to the
// schema without classifying it fails the same way — which is what FR-135 is,
// stated as a mechanism rather than as an instruction to remember.
// ---------------------------------------------------------------------------

// predicateColumns is the CLOSED set of columns a read-path predicate may
// mention, each with the reason it narrows rather than decides.
//
// Adding a row here is a specification change with the same two obligations
// narrowingDimensions carries, and for the same reason: a predicate the
// CountCandidates statement cannot apply makes B1 bound a different population
// from the one the stream visits.
var predicateColumns = map[string]string{
	"record_type": "set membership over an indexed column; the Selector's own dimension (D16.2b)",
	"kind":        "note-kind narrowing; same argument as record_type",
	"path":        "workspace/collection scope (FR-060), as a caller-INDEPENDENT prefix built here from a resolved root",
	"note_id":     "the child-table join anchor — an equality between a parent and its child, admitted by D16.6a Ruling 2 as assembly, not comparison",
}

// valueColumns is every other column in the schema: columns NO predicate may
// mention, each with the reason.
//
// The three stat columns are here because FR-135 puts them here in as many
// words: "stat metadata is a comparison target and therefore the Go
// comparator's, never a SQL predicate's". `file.mtime > 2026-01-01` is a
// comparison over a date, governed by R-7, and SQLite's own date handling
// violates R-7 four ways — `unixepoch('not-a-date')` returns NULL with NO error,
// which would write a parse failure into the cell reserved for absence.
var valueColumns = map[string]string{
	// note_props — property values and the state that distinguishes a
	// non-conforming one from an absent one.
	"prop":   "a property NAME; a predicate over it is the pushdown ruling R-A forbids",
	"elem":   "an element position; only the assembler orders these, and never SQLite",
	"state":  "FR-021b's three-state flag is not SQLite's to interpret; set membership over it is still a decision",
	"vtype":  "the declared type; typing is the schema's and the comparator's",
	"v_text": "R-1: SQLite ranks any text above any number ('3' > 2 is 1)",
	"v_num":  "FR-020b: stored as exact decimal DIGITS in a BLOB; a SQL comparison would coerce or fail silently",
	"v_time": "R-7: SQLite's date handling violates it four ways, one of them returning NULL with no error",
	"v_link": "a wikilink resolves through FR-031, in Go, against the vault",
	"v_raw":  "the source text, including a non-conforming value's diagnostic evidence — R-4 says it has no value",
	"quoted": "lexical form, for the decode path; nothing compares it",

	// notes — identity, freshness and FR-131's stat metadata.
	"record_id":   "R-8: CO-0142 and co-0142 are two distinct records; no collation, and no SQL comparison",
	"source_hash": "the freshness token is COMPARED BETWEEN TWO INDEXES in Go (FR-020c), never inside one of them",
	"indexed_at":  "bookkeeping; no query narrows on when a row was written",
	"mtime":       "FR-135: stat metadata is a comparison target, and `file.mtime` is a date under R-7 — the Go comparator's",
	"ctime":       "FR-135, and FR-133 on top of it: absence here is a PLATFORM fact, and SQL's NULL semantics get absence wrong (R-2/R-3)",
	"size":        "FR-135: `file.size` is an integer comparison under R-1, decided in Go over exact decimal digits",
	// The two derivation tokens. They are read ONLY by the indexer's own
	// maintenance walk (AllPaths, which emits no WHERE clause at all) and
	// compared in Go against the schema set that is currently loaded. They are
	// classified as value columns deliberately, and the classification is a
	// real constraint rather than a formality: `declared_type` looks exactly
	// like a narrowing column and is not one. Narrowing on it would answer
	// `type=company` with every note that CLAIMS to be a company whether or not
	// a `company` type exists, which is FR-005 inverted — `record_type` is the
	// column that says what a note actually resolved to, and it is the only one
	// a query may narrow on.
	"declared_type": "the type a note's frontmatter CLAIMS, resolved or not; narrowing on it would answer `type=` with notes of an undeclared type (FR-005). Compared in Go by the indexer only",
	"schema_fp":     "the fingerprint of the schema this row was derived from; the indexer compares it in Go against the loaded schema set, and no query has any business mentioning it",

	// note_tasks.
	"line":   "a source line number; a renderer's coordinate, not a filter target",
	"status": "FR-076a's checkbox state; the comparator decides what open and done mean",
	"text":   "the checkbox's text — a value, with every R-1/R-9 problem any other text has",

	// note_relations and note_links share these shapes.
	"target":  "FR-135: a link target, resolved through FR-031 in Go against the vault",
	"heading": "part of a link value",
	"display": "part of a link value",
	"raw":     "a link's source text, quoted by reports and decoded in Go",
	"embed":   "FR-135: the `file.links` / `file.embeds` partition is made in Go (records.SplitLinkRows); a predicate on it is a comparison",

	// note_tags.
	"tag": "FR-135: `file.tags` is a text comparison under R-9's fold rules, which SQLite does not implement for non-ASCII at all",
}

// createTable pulls one table's name and body out of the DDL.
var createTable = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS (\w+) \((.*?)\n\)`)

// schemaColumns reads the column names out of the store's OWN ddl constant.
//
// Reading the DDL rather than a hand-kept list is what makes the classification
// test detect a column that was ADDED without being classified. A list copied by
// hand describes a memory of the schema, and the schema is what the predicates
// are written against.
func schemaColumns(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	tables := createTable.FindAllStringSubmatch(ddl, -1)
	if len(tables) == 0 {
		t.Fatal("no CREATE TABLE statements were found in ddl; this guard reads the schema and just read nothing")
	}
	for _, tbl := range tables {
		for _, line := range strings.Split(tbl[2], "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) == 0 || strings.EqualFold(fields[0], "PRIMARY") {
				continue
			}
			out[strings.TrimSuffix(fields[0], ",")] = tbl[1]
		}
	}
	return out
}

// TestSQLGate_EveryColumnIsClassified is the CAUSE half of AC-8.10's column
// control, and the thing that makes the effect half non-vacuous.
func TestSQLGate_EveryColumnIsClassified(t *testing.T) {
	columns := schemaColumns(t)
	if len(columns) < 10 {
		t.Fatalf("schemaColumns found only %d columns; the DDL parser is not reading the schema", len(columns))
	}

	for col, table := range columns {
		_, narrows := predicateColumns[col]
		_, isValue := valueColumns[col]
		switch {
		case narrows && isValue:
			t.Errorf("column %s.%s is in BOTH classifications; it must be in exactly one", table, col)
		case !narrows && !isValue:
			t.Errorf(
				"column %s.%s is in NEITHER classification.\n\n"+
					"Every column of this schema is one of two things and the guard needs to be told which:\n"+
					"  * a NARROWING column — set membership over an indexed column, or a parent/child join\n"+
					"    anchor. Add it to predicateColumns WITH the reason, and make sure narrowing()\n"+
					"    applies it to BOTH the CountCandidates statement and the candidate stream, or B1\n"+
					"    bounds a different population from the one that is visited.\n"+
					"  * a VALUE or METADATA column — anything a comparison could be made against. Add it to\n"+
					"    valueColumns WITH the reason, and it becomes a column no predicate may mention.\n\n"+
					"IF YOU ARRIVED HERE BY DELETING A ROW FROM valueColumns: that is the mutation this test\n"+
					"exists to catch. Deleting the row does not relax the rule, it removes the classification,\n"+
					"and TestSQLGate_ReadPathTouchesNoValueColumn would then have silently stopped checking\n"+
					"that column — which is a guard reporting green about something it no longer enforces\n"+
					"(FR-135; the same two-thirds omission D16.6a blocked on one revision ago).",
				table, col)
		}
	}

	for col := range predicateColumns {
		if _, ok := columns[col]; !ok {
			t.Errorf("predicateColumns names %q, which is not a column of this schema; the classification must describe the DDL, not a memory of it", col)
		}
	}
	for col := range valueColumns {
		if _, ok := columns[col]; !ok {
			t.Errorf("valueColumns names %q, which is not a column of this schema; the classification must describe the DDL, not a memory of it", col)
		}
	}
}

// TestSQLGate_ReadPathTouchesNoValueColumn is the EFFECT half.
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
	if err := store.Relations(ctx, sel, func(RelationHit) error { return nil }); err != nil {
		t.Fatalf("Relations: %v", err)
	}
	// FR-131's two new streams. A statement that is never emitted is never
	// inspected, so the corpus must reach them or this half of the guard
	// watches two fewer tables than the schema has.
	if err := store.Tags(ctx, sel, func(TagHit) error { return nil }); err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if err := store.Links(ctx, sel, func(LinkHit) error { return nil }); err != nil {
		t.Fatalf("Links: %v", err)
	}

	stmts := rec.InPhase(PhaseRead)
	if len(stmts) == 0 {
		t.Fatal("the recorder captured no read-path statements; this half of the control is watching nothing")
	}
	for _, stmt := range stmts {
		i := strings.Index(strings.ToUpper(stmt), " WHERE ")
		if i < 0 {
			continue
		}
		where := stmt[i:]
		for col, why := range valueColumns {
			if regexp.MustCompile(`\b` + col + `\b`).MatchString(where) {
				t.Errorf("a value column %q appears in a predicate: %s\n  why it may not: %s", col, stmt, why)
			}
		}
	}
}

// TestSQLGate_NoStatementJoinsTwoChildTables is FR-131's assembly rule, enforced.
//
// `note_props`, `note_tags`, `note_links`, `note_tasks` and `note_relations` are
// five children of one parent. Joining any two of them in one statement returns
// their CARTESIAN PRODUCT — a note with 30 properties, 10 tags and 40 links
// yields 12,000 rows where it yields 30 today — and every aggregate computed
// over that product is wrong by the same factor. D16.6 fixed this exact fan-out
// once already, when COUNT(*) returned 2 and SUM returned 200 where the truth
// was 1 and 100. At B1's 50,000-candidate ceiling the second occurrence is a
// hang rather than a wrong number.
//
// The rule is checked mechanically because it is a CONVENIENT mistake: assembling
// a whole note from one statement is the obvious thing to write, it looks
// tidier than four streams, and on a three-note test fixture it produces the
// right answer.
func TestSQLGate_NoStatementJoinsTwoChildTables(t *testing.T) {
	rec := NewRecorder()
	store, _ := openIndex(t, Options{Recorder: rec})
	ctx := context.Background()
	mustUpsert(t, store, plantNote(t, 1, "growing"))
	rec.Reset()

	sel := Selector{RecordType: "plant", PathPrefix: "garden/"}
	if _, err := store.CountCandidates(ctx, sel); err != nil {
		t.Fatalf("CountCandidates: %v", err)
	}
	collect(t, store, sel)
	if err := store.Tasks(ctx, sel, func(TaskHit) error { return nil }); err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if err := store.Relations(ctx, sel, func(RelationHit) error { return nil }); err != nil {
		t.Fatalf("Relations: %v", err)
	}
	if err := store.Tags(ctx, sel, func(TagHit) error { return nil }); err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if err := store.Links(ctx, sel, func(LinkHit) error { return nil }); err != nil {
		t.Fatalf("Links: %v", err)
	}

	children := []string{"note_props", "note_tags", "note_links", "note_tasks", "note_relations"}
	stmts := rec.InPhase(PhaseRead)
	if len(stmts) == 0 {
		t.Fatal("the recorder captured no read-path statements; this guard is watching nothing")
	}
	sawChild := false
	for _, stmt := range stmts {
		var mentioned []string
		for _, child := range children {
			if regexp.MustCompile(`\b` + child + `\b`).MatchString(stmt) {
				mentioned = append(mentioned, child)
			}
		}
		if len(mentioned) > 0 {
			sawChild = true
		}
		if len(mentioned) > 1 {
			t.Errorf(
				"one statement joins %v — %d child tables of the same parent.\n"+
					"  statement: %s\n"+
					"That is their CARTESIAN PRODUCT: 30 properties x 10 tags x 40 links is 12,000 rows for "+
					"a note that yields 30, and every aggregate over it is wrong by the same factor. FR-131 "+
					"requires each child table to be streamed by its OWN statement under the shared "+
					"narrowing, assembled per note in Go — the Tasks/Relations/Tags/Links pattern.",
				mentioned, len(mentioned), stmt)
		}
	}
	if !sawChild {
		t.Fatal("no captured statement mentioned any child table; this guard would pass over an empty corpus")
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
		// The WHERE clauses agreeing is only HALF of "the same population".
		// The other half is the JOIN KIND, and it is invisible to the
		// comparison below: turning `LEFT JOIN` into `JOIN` leaves both WHERE
		// clauses byte-identical while silently dropping every note that has no
		// property rows — which is FR-005's ordinary note, the MAJORITY of every
		// real vault. B1 would then count a population containing them and the
		// stream would visit one that does not.
		//
		// Asserted here, next to the WHERE comparison, because the two failures
		// are the same failure and a reader who finds one should find the other.
		for _, s := range stmts {
			if strings.Contains(s, "note_props") && !strings.Contains(s, "LEFT JOIN") {
				t.Errorf(
					"selector %+v: the candidate stream joins note_props with an INNER join.\n"+
						"  statement: %s\n"+
						"Every note with no property rows disappears from the answer — FR-005's ordinary "+
						"note, which is most of a real vault — while B1 goes on counting them. The WHERE "+
						"clauses stay identical, so nothing else here would notice.", sel, s)
			}
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
