// Omnipus — ADR-068 D16.2: the properties index, in pure-Go SQLite.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/records"
	_ "modernc.org/sqlite" // pure-Go SQLite: no CGo, already linked for WhatsApp and Matrix
)

// driverName is modernc.org/sqlite's registered name — the same string
// pkg/channels/matrix uses for the E2EE crypto store.
const driverName = "sqlite"

// schemaVersion is the shape of the tables below. It is stored in the file's
// `user_version` and a mismatch causes a full rebuild rather than a migration:
// the index is DERIVED, so re-deriving it is always cheaper and always more
// correct than teaching it to translate itself (FR-020a).
// Revision 2 (ADR-068 D24 / FR-131, FR-021e): three stat columns on `notes`,
// two new child tables (note_tags, note_links), and a raw property row for
// every frontmatter key of every note rather than only of typed ones. Every one
// of those is a change to what a note CONTRIBUTES, so an index written by
// version 1 holds less than a version-2 reader expects — and the difference is
// invisible at read time (a note with no tags and a note indexed before tags
// existed look identical). Hence the bump: the mismatch drops the tables and
// re-derives, which is always cheaper and always more correct than teaching a
// derived index to translate itself.
const schemaVersion = 2

// Index is the SQLite properties index.
type Index struct {
	db   *sql.DB
	path string
	rec  *Recorder

	mu        sync.Mutex
	needsFull bool
}

// Open opens or creates the properties index at path.
//
// The file is derived and disposable: deleting it is a supported, ordinary way
// to ask for a rebuild, and Open on a missing or version-mismatched file reports
// NeedsFullIndex so the caller re-derives from the notes.
func Open(ctx context.Context, path string, opts Options) (Store, error) {
	// The platform gate is asked EVEN HERE, on the build where it always says
	// yes. It costs a compile-time-constant branch, and it means the capability
	// question has exactly one answer in one place — rather than one answer on
	// the SQLite build and a different file's answer on the other, which is how
	// the two halves of a build gate drift apart.
	if err := records.RequirePropertyIndex(records.CapabilityOpenIndex); err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("propindex: open %q: %w", path, err)
	}
	// One connection. The index is a single file with a rollback journal, so a
	// reader and a writer cannot coexist on it; serialising here turns what
	// would be an intermittent SQLITE_BUSY into a queue. A Candidates stream
	// therefore holds the connection for its duration — bounded by B1 — and a
	// concurrent index write waits behind it.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ix := &Index{db: db, path: path, rec: opts.Recorder}
	if err := ix.init(ctx); err != nil {
		if cerr := db.Close(); cerr != nil {
			return nil, fmt.Errorf("%w (and closing the database failed: %v)", err, cerr)
		}
		return nil, err
	}
	return ix, nil
}

// ---------------------------------------------------------------------------
// SCHEMA
//
// Storage classes are chosen so that nothing INVITES SQLite to compare.
//
//   - Every value column is a BLOB. BLOB affinity applies no conversion at all,
//     so a stored '3' can never become the integer 3 and outrank a number that
//     is larger than it (R-1: `SELECT '3' > 2` is 1). It also means a stray
//     comparison against a text parameter simply fails to match instead of
//     silently coercing (R-12), which turns a mistake into a visible zero-row
//     answer rather than a confident wrong one.
//   - A number is stored as its exact decimal DIGITS, never as a REAL. FR-020b
//     is a promise about the whole path, and a REAL column is the one place the
//     path could break without anyone writing a float.
//   - A date is stored as strict ISO-8601 TEXT bytes, never an epoch integer.
//     Nothing orders it here: R-7 is violated four ways by SQLite's own date
//     handling, and `unixepoch('not-a-date')` returns NULL with NO error, which
//     would write a parse failure into the cell reserved for absence.
//   - record_id is a BLOB and carries NO collation. R-8: `CO-0142` and
//     `co-0142` are two distinct records; a NOCASE column would collide them on
//     a UNIQUE constraint, which is a data-loss refusal for a case nobody chose.
//     There is deliberately no UNIQUE index on it either — a vault may CONTAIN a
//     duplicate identifier, and an index that refuses to record one cannot
//     report it (FR-039).
//   - The three narrowing columns (record_type, kind, path) are TEXT, because
//     they are the only columns any emitted predicate ever touches.
//
// The child tables are WITHOUT ROWID, so each IS its primary-key b-tree keyed by
// (note_id, ...). A note's child rows are therefore contiguous in every access
// path the planner can choose, which is what makes one-record-at-a-time
// streaming assembly possible without an ORDER BY nobody is allowed to emit.
// ---------------------------------------------------------------------------

const ddl = `
CREATE TABLE IF NOT EXISTS notes (
	note_id     INTEGER PRIMARY KEY,
	path        TEXT    NOT NULL,
	kind        TEXT    NOT NULL,
	record_type TEXT    NOT NULL,
	record_id   BLOB    NOT NULL,
	source_hash TEXT    NOT NULL,
	indexed_at  INTEGER NOT NULL,
	mtime       BLOB,
	ctime       BLOB,
	size        BLOB
);
CREATE UNIQUE INDEX IF NOT EXISTS notes_by_path ON notes(path);
CREATE INDEX IF NOT EXISTS notes_narrowing ON notes(record_type, kind, path);

CREATE TABLE IF NOT EXISTS note_props (
	note_id INTEGER NOT NULL,
	prop    TEXT    NOT NULL,
	elem    INTEGER NOT NULL,
	state   INTEGER NOT NULL,
	vtype   TEXT    NOT NULL,
	v_text  BLOB,
	v_num   BLOB,
	v_time  BLOB,
	v_link  BLOB,
	v_raw   BLOB    NOT NULL,
	quoted  INTEGER NOT NULL,
	PRIMARY KEY (note_id, prop, elem)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS note_relations (
	note_id INTEGER NOT NULL,
	prop    TEXT    NOT NULL,
	elem    INTEGER NOT NULL,
	target  BLOB    NOT NULL,
	heading BLOB    NOT NULL,
	display BLOB    NOT NULL,
	raw     BLOB    NOT NULL,
	PRIMARY KEY (note_id, prop, elem)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS note_tasks (
	note_id INTEGER NOT NULL,
	line    INTEGER NOT NULL,
	status  TEXT    NOT NULL,
	text    BLOB    NOT NULL,
	PRIMARY KEY (note_id, line)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS note_tags (
	note_id INTEGER NOT NULL,
	elem    INTEGER NOT NULL,
	tag     BLOB    NOT NULL,
	PRIMARY KEY (note_id, elem)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS note_links (
	note_id INTEGER NOT NULL,
	elem    INTEGER NOT NULL,
	target  BLOB    NOT NULL,
	heading BLOB    NOT NULL,
	display BLOB    NOT NULL,
	raw     BLOB    NOT NULL,
	embed   INTEGER NOT NULL,
	PRIMARY KEY (note_id, elem)
) WITHOUT ROWID;
`

func (ix *Index) init(ctx context.Context) error {
	// A derived index has nothing to lose in a crash — it rebuilds — so paying
	// for durability on every write buys nothing and costs the whole indexing
	// pass. The busy timeout matters more: it is what a second process waits on.
	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = OFF",
		"PRAGMA foreign_keys = OFF",
	} {
		if _, err := ix.exec(ctx, PhaseOpen, pragma); err != nil {
			return fmt.Errorf("propindex: %s: %w", pragma, err)
		}
	}

	var have int
	if err := ix.queryRow(ctx, PhaseOpen, "PRAGMA user_version").Scan(&have); err != nil {
		return fmt.Errorf("propindex: reading the schema version: %w", err)
	}
	fresh := have == 0
	if have != 0 && have != schemaVersion {
		if err := ix.dropAll(ctx); err != nil {
			return err
		}
		fresh = true
	}
	if _, err := ix.exec(ctx, PhaseOpen, ddl); err != nil {
		return fmt.Errorf("propindex: creating the schema: %w", err)
	}
	if fresh {
		// PRAGMA takes no bind parameter, so the version is formatted in. It is
		// a compile-time constant, not caller input.
		stmt := fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)
		if _, err := ix.exec(ctx, PhaseOpen, stmt); err != nil {
			return fmt.Errorf("propindex: stamping the schema version: %w", err)
		}
	}

	var notes int
	if err := ix.queryRow(ctx, PhaseOpen, "SELECT COUNT(*) FROM notes").Scan(&notes); err != nil {
		return fmt.Errorf("propindex: counting indexed notes: %w", err)
	}
	ix.needsFull = notes == 0
	return nil
}

func (ix *Index) dropAll(ctx context.Context) error {
	const drop = `
DROP TABLE IF EXISTS note_links;
DROP TABLE IF EXISTS note_tags;
DROP TABLE IF EXISTS note_tasks;
DROP TABLE IF EXISTS note_relations;
DROP TABLE IF EXISTS note_props;
DROP TABLE IF EXISTS notes;
`
	if _, err := ix.exec(ctx, PhaseOpen, drop); err != nil {
		return fmt.Errorf("propindex: discarding an incompatible index: %w", err)
	}
	return nil
}

// NeedsFullIndex reports that the store holds no notes and the caller must
// derive them.
func (ix *Index) NeedsFullIndex() bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.needsFull
}

// Close releases the database.
func (ix *Index) Close() error {
	if err := ix.db.Close(); err != nil {
		return fmt.Errorf("propindex: closing %q: %w", ix.path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// WRITE PATH
// ---------------------------------------------------------------------------

// UpsertNote replaces everything the index holds for one path.
func (ix *Index) UpsertNote(ctx context.Context, rows NoteRows) error {
	return ix.UpsertNotes(ctx, []NoteRows{rows})
}

// UpsertNotes writes a batch in ONE transaction.
//
// It exists because a rebuild is the batch case and one transaction per note
// turns a rebuild into one commit per note. The single-note path routes through
// here so there is one write path to reason about, not two.
func (ix *Index) UpsertNotes(ctx context.Context, batch []NoteRows) (err error) {
	if len(batch) == 0 {
		return nil
	}
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("propindex: beginning a write: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = fmt.Errorf("%w (and rolling back failed: %v)", err, rbErr)
			}
		}
	}()

	for _, rows := range batch {
		if rows.Path == "" {
			return errors.New("propindex: a note row with no path cannot be indexed")
		}
		if err = ix.upsertOne(ctx, tx, rows); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("propindex: committing %d note(s): %w", len(batch), err)
	}

	ix.mu.Lock()
	ix.needsFull = false
	ix.mu.Unlock()
	return nil
}

func (ix *Index) upsertOne(ctx context.Context, tx *sql.Tx, rows NoteRows) error {
	id, found, err := ix.noteIDTx(ctx, tx, rows.Path)
	if err != nil {
		return err
	}
	now := time.Now().UnixNano()
	if found {
		if err := ix.deleteChildren(ctx, tx, id); err != nil {
			return err
		}
		const q = `UPDATE notes SET kind = ?, record_type = ?, record_id = ?, source_hash = ?, indexed_at = ?, mtime = ?, ctime = ?, size = ? WHERE note_id = ?`
		if _, err := ix.execTx(ctx, tx, PhaseWrite, q,
			rows.Kind, rows.RecordType, []byte(rows.RecordID), rows.SourceHash, now,
			nanoTimeColumn(rows.MtimeNanos),
			ctimeColumn(rows.CtimeNanos, rows.HasCtime),
			sizeColumn(rows.Size, rows.StatKnown()),
			id); err != nil {
			return fmt.Errorf("propindex: updating %q: %w", rows.Path, err)
		}
	} else {
		const q = `INSERT INTO notes (path, kind, record_type, record_id, source_hash, indexed_at, mtime, ctime, size) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
		res, err := ix.execTx(ctx, tx, PhaseWrite, q,
			rows.Path, rows.Kind, rows.RecordType, []byte(rows.RecordID), rows.SourceHash, now,
			nanoTimeColumn(rows.MtimeNanos),
			ctimeColumn(rows.CtimeNanos, rows.HasCtime),
			sizeColumn(rows.Size, rows.StatKnown()))
		if err != nil {
			return fmt.Errorf("propindex: inserting %q: %w", rows.Path, err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("propindex: identifying the row just written for %q: %w", rows.Path, err)
		}
	}

	const insProp = `INSERT INTO note_props (note_id, prop, elem, state, vtype, v_text, v_num, v_time, v_link, v_raw, quoted) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, p := range rows.Props {
		if _, err := ix.execTx(ctx, tx, PhaseWrite, insProp,
			id, p.Prop, p.Elem, int(p.State), string(p.Type),
			nullBlob(p.Text), nullBlob(p.Num), nullBlob(p.Time), nullBlob(p.Link),
			[]byte(p.Raw), boolInt(p.Quoted),
		); err != nil {
			return fmt.Errorf("propindex: property %q of %q: %w", p.Prop, rows.Path, err)
		}
	}

	const insRel = `INSERT INTO note_relations (note_id, prop, elem, target, heading, display, raw) VALUES (?, ?, ?, ?, ?, ?, ?)`
	for _, r := range rows.Relations {
		if _, err := ix.execTx(ctx, tx, PhaseWrite, insRel,
			id, r.Prop, r.Elem, []byte(r.Target), []byte(r.Heading), []byte(r.Display), []byte(r.Raw),
		); err != nil {
			return fmt.Errorf("propindex: relation %q of %q: %w", r.Prop, rows.Path, err)
		}
	}

	const insTask = `INSERT INTO note_tasks (note_id, line, status, text) VALUES (?, ?, ?, ?)`
	for _, t := range rows.Tasks {
		if _, err := ix.execTx(ctx, tx, PhaseWrite, insTask,
			id, t.Line, t.Status, []byte(t.Text),
		); err != nil {
			return fmt.Errorf("propindex: task at %s:%d: %w", rows.Path, t.Line, err)
		}
	}

	const insTag = `INSERT INTO note_tags (note_id, elem, tag) VALUES (?, ?, ?)`
	for _, g := range rows.Tags {
		if _, err := ix.execTx(ctx, tx, PhaseWrite, insTag,
			id, g.Elem, []byte(g.Tag),
		); err != nil {
			return fmt.Errorf("propindex: tag %q of %q: %w", g.Tag, rows.Path, err)
		}
	}

	const insLink = `INSERT INTO note_links (note_id, elem, target, heading, display, raw, embed) VALUES (?, ?, ?, ?, ?, ?, ?)`
	for _, l := range rows.Links {
		if _, err := ix.execTx(ctx, tx, PhaseWrite, insLink,
			id, l.Elem, []byte(l.Target), []byte(l.Heading), []byte(l.Display), []byte(l.Raw), boolInt(l.Embed),
		); err != nil {
			return fmt.Errorf("propindex: link to %q in %q: %w", l.Target, rows.Path, err)
		}
	}
	return nil
}

// DeleteNote removes a note and every child row it owns.
func (ix *Index) DeleteNote(ctx context.Context, path string) (err error) {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("propindex: beginning a delete: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = fmt.Errorf("%w (and rolling back failed: %v)", err, rbErr)
			}
		}
	}()

	id, found, err := ix.noteIDTx(ctx, tx, path)
	if err != nil {
		return err
	}
	if !found {
		// Deleting a note the index never held is not an error: the vault is the
		// source of truth and the index is allowed to be behind it.
		return tx.Commit()
	}
	if err = ix.deleteChildren(ctx, tx, id); err != nil {
		return err
	}
	if _, err = ix.execTx(ctx, tx, PhaseWrite, `DELETE FROM notes WHERE note_id = ?`, id); err != nil {
		return fmt.Errorf("propindex: deleting %q: %w", path, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("propindex: committing the delete of %q: %w", path, err)
	}
	return nil
}

// RefreshNoteStat is FR-136: the metadata-only correction a content-unchanged
// sync skip can afford.
//
// WHAT IT DELIBERATELY DOES NOT DO is the whole design. It does not re-parse the
// note, does not touch a single child row, and does not move source_hash or
// indexed_at. Moving source_hash would forge agreement with the text index that
// D16.5's write ordering exists precisely to detect the ABSENCE of — an index
// reporting "these two agree" because this method said so, rather than because
// the same bytes were seen twice.
//
// ONLY WHERE THEY DIFFER, and the difference is decided on the RENDERED column
// bytes rather than on the caller's integers. That is not fussiness: the columns
// hold ISO-8601 text at nanosecond precision and exact decimal digits, so two
// inputs that render identically ARE identical as far as this index is
// concerned, and an UPDATE for them would report `changed` for a change that
// does not exist. The bool is what a caller counts, so a false positive in it is
// a wrong number in someone's sync report.
//
// ctime is untouched. The walk that drives a refresh carries size and mtime
// only, so there is nothing here to refresh a birth time from — and a birth time
// does not change, so the value written at index time remains correct. Three
// columns are written at index time; two are refreshed.
func (ix *Index) RefreshNoteStat(ctx context.Context, path string, size, mtimeNanos int64) (changed bool, err error) {
	if path == "" {
		return false, errors.New("propindex: RefreshNoteStat called with an empty path")
	}
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("propindex: beginning a stat refresh: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = fmt.Errorf("%w (and rolling back failed: %v)", err, rbErr)
			}
		}
	}()

	var (
		id                 int64
		haveMtime, haveSze []byte
	)
	scanErr := ix.queryRowTx(ctx, tx, PhaseWrite,
		`SELECT note_id, mtime, size FROM notes WHERE path = ?`, path).Scan(&id, &haveMtime, &haveSze)
	switch {
	case errors.Is(scanErr, sql.ErrNoRows):
		// A path this store does not hold. Refreshing the metadata of a note the
		// index has never seen is not an error and not a silent success either:
		// nothing changed, and the caller is told exactly that. The vault is the
		// source of truth and the index is allowed to be behind it — the same
		// posture DeleteNote takes for the same reason.
		if err = tx.Commit(); err != nil {
			return false, fmt.Errorf("propindex: closing the stat refresh of %q: %w", path, err)
		}
		return false, nil
	case scanErr != nil:
		err = fmt.Errorf("propindex: reading the stored stat of %q: %w", path, scanErr)
		return false, err
	}

	wantMtime := nanoTimeColumn(mtimeNanos)
	wantSize := sizeColumn(size, mtimeNanos != 0)
	if sameColumn(haveMtime, wantMtime) && sameColumn(haveSze, wantSize) {
		if err = tx.Commit(); err != nil {
			return false, fmt.Errorf("propindex: closing the stat refresh of %q: %w", path, err)
		}
		return false, nil
	}

	if _, err = ix.execTx(ctx, tx, PhaseWrite,
		`UPDATE notes SET mtime = ?, size = ? WHERE note_id = ?`, wantMtime, wantSize, id); err != nil {
		err = fmt.Errorf("propindex: refreshing the stat of %q: %w", path, err)
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("propindex: committing the stat refresh of %q: %w", path, err)
	}
	return true, nil
}

// sameColumn reports whether a stored column already holds what would be
// written.
//
// NULL and an empty blob are the same thing here, because both mean "this index
// has no value for it" — and a store that reported a change for the difference
// between the two would count a refresh every single pass over a note whose stat
// is unknown.
func sameColumn(have []byte, want any) bool {
	w, ok := want.([]byte)
	if !ok {
		return len(have) == 0
	}
	return string(have) == string(w)
}

func (ix *Index) deleteChildren(ctx context.Context, tx *sql.Tx, id int64) error {
	for _, q := range []string{
		`DELETE FROM note_props WHERE note_id = ?`,
		`DELETE FROM note_relations WHERE note_id = ?`,
		`DELETE FROM note_tasks WHERE note_id = ?`,
		`DELETE FROM note_tags WHERE note_id = ?`,
		`DELETE FROM note_links WHERE note_id = ?`,
	} {
		if _, err := ix.execTx(ctx, tx, PhaseWrite, q, id); err != nil {
			return fmt.Errorf("propindex: clearing child rows: %w", err)
		}
	}
	return nil
}

func (ix *Index) noteIDTx(ctx context.Context, tx *sql.Tx, path string) (int64, bool, error) {
	var id int64
	err := ix.queryRowTx(ctx, tx, PhaseWrite, `SELECT note_id FROM notes WHERE path = ?`, path).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("propindex: looking up %q: %w", path, err)
	}
	return id, true, nil
}

func nullBlob(s string) any {
	if s == "" {
		return nil
	}
	return []byte(s)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// AllPaths visits every path this store holds, with its kind and source hash.
//
// Unlike every statement below this comment, this query carries NO narrowing
// WHERE clause and is not measured against B1 — it is the sync pipeline's own
// maintenance walk (store.go's doc comment on the interface method explains
// why), not a caller-shaped query.
func (ix *Index) AllPaths(ctx context.Context, visit func(path, kind, sourceHash string) error) (err error) {
	rows, err := ix.query(ctx, PhaseRead, `SELECT path, kind, source_hash FROM notes`)
	if err != nil {
		return fmt.Errorf("propindex: listing indexed paths: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("propindex: closing the path listing: %w", cerr)
		}
	}()

	for rows.Next() {
		var path, kind, hash string
		if err := rows.Scan(&path, &kind, &hash); err != nil {
			return fmt.Errorf("propindex: reading an indexed path: %w", err)
		}
		if err := visit(path, kind, hash); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("propindex: the path listing ended early: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// READ PATH — every statement below is narrowing, and only narrowing.
// ---------------------------------------------------------------------------

const (
	remedyB1 = "narrow the scope to a collection or path prefix, or narrow the kind"
	remedyB2 = "add or tighten a filter, or use the aggregate-only path"
)

// escapeLikePrefix turns a resolved path prefix into a LIKE pattern.
//
// The pattern is built HERE, from an already-resolved root, and the escape
// character is declared on the statement itself. A caller cannot inject a
// wildcard through it, which is the whole reason AC-8.10 permits `path LIKE ?`
// at all: the pattern is caller-INDEPENDENT.
func escapeLikePrefix(prefix string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(prefix) + "%"
}

// narrowing builds the WHERE clause. Read what it can produce: three equality
// or prefix predicates over three indexed columns, and nothing else. There is no
// code path here that can take a property name or a property value, because no
// caller can hand it one — Selector has no field for it.
func narrowing(alias string, sel Selector) (string, []any) {
	var clauses []string
	var args []any
	if sel.RecordType != "" {
		clauses = append(clauses, alias+".record_type = ?")
		args = append(args, sel.RecordType)
	}
	if sel.Kind != "" {
		clauses = append(clauses, alias+".kind = ?")
		args = append(args, sel.Kind)
	}
	if sel.PathPrefix != "" {
		clauses = append(clauses, alias+`.path LIKE ? ESCAPE '\'`)
		args = append(args, escapeLikePrefix(sel.PathPrefix))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// CountCandidates is B1 — FR-064's evaluation bound, taken before anything is
// retrieved.
//
// This is the ONE aggregate the design emits, and it is emitted over the
// narrowing predicates only. It totals nothing the operator asked about: it
// counts rows of `notes`, which is a population, not an answer. Every aggregate
// the operator CAN ask for — count, sum, and the rest — runs in Go over the
// candidate stream, one record visited once, because a join fan-out made
// `COUNT(*)` return 2 and `SUM` return 200 where the truth was 1 and 100.
func (ix *Index) CountCandidates(ctx context.Context, sel Selector) (int, error) {
	where, args := narrowing("notes", sel)
	q := `SELECT COUNT(*) FROM notes` + where
	var n int
	if err := ix.queryRow(ctx, PhaseRead, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("propindex: counting candidates: %w", err)
	}
	return n, nil
}

// candidateColumns carries the PARENT row's stat metadata alongside its
// properties.
//
// mtime/ctime/size ride on `notes`, so adding them to this SELECT costs no
// extra rows: it is one more column on a row that was already being read, not a
// second child in the join. That distinction is the whole of FR-131's assembly
// rule — the tags and links, which ARE children, get their own statements
// below.
const candidateColumns = `n.note_id, n.path, n.record_type, n.record_id, n.source_hash, ` +
	`n.mtime, n.ctime, n.size, ` +
	`p.prop, p.elem, p.state, p.vtype, p.v_text, p.v_num, p.v_time, p.v_link, p.v_raw, p.quoted`

// Candidates streams the narrowed population, one record at a time.
func (ix *Index) Candidates(ctx context.Context, sel Selector, visit func(Candidate) (Verdict, error)) error {
	n, err := ix.CountCandidates(ctx, sel)
	if err != nil {
		return err
	}
	if n > BoundNarrowedCandidates {
		return &BoundError{Bound: "B1", Count: n, Limit: BoundNarrowedCandidates, Remedy: remedyB1}
	}

	where, args := narrowing("n", sel)
	q := `SELECT ` + candidateColumns + ` FROM notes AS n LEFT JOIN note_props AS p ON p.note_id = n.note_id` + where
	return ix.streamCandidates(ctx, q, args, visit)
}

func (ix *Index) streamCandidates(ctx context.Context, q string, args []any, visit func(Candidate) (Verdict, error)) (err error) {
	rows, err := ix.query(ctx, PhaseRead, q, args...)
	if err != nil {
		return fmt.Errorf("propindex: streaming candidates: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("propindex: closing the candidate stream: %w", cerr)
		}
	}()

	var (
		cur       *Candidate
		curID     int64
		finished  = make(map[int64]struct{})
		survivors int
	)

	// flush hands the assembled record to the comparator. It is the ONLY place
	// B2 is counted, because B2 counts what the COMPARATOR accepted — not what
	// the index selected, and not what the driver returned.
	flush := func() error {
		if cur == nil {
			return nil
		}
		verdict, verr := visit(*cur)
		cur = nil
		if verr != nil {
			return verr
		}
		if verdict == Accepted {
			survivors++
			if survivors > BoundSurvivors {
				return &BoundError{Bound: "B2", Count: survivors, Limit: BoundSurvivors, Remedy: remedyB2}
			}
		}
		return nil
	}

	for rows.Next() {
		var (
			id                              int64
			path, recordType, sourceHash    string
			recordID                        []byte
			mtime, ctime, size              []byte
			prop, vtype                     sql.NullString
			elem, state, quoted             sql.NullInt64
			vText, vNum, vTime, vLink, vRaw []byte
		)
		if err := rows.Scan(&id, &path, &recordType, &recordID, &sourceHash,
			&mtime, &ctime, &size,
			&prop, &elem, &state, &vtype, &vText, &vNum, &vTime, &vLink, &vRaw, &quoted); err != nil {
			return fmt.Errorf("propindex: reading a candidate row: %w", err)
		}

		if cur == nil || id != curID {
			if err := flush(); err != nil {
				return err
			}
			// The child tables are WITHOUT ROWID and keyed on note_id, so a
			// record's rows are contiguous in every access path the planner can
			// choose. That is an assumption about the engine, so it is CHECKED
			// rather than trusted: a record arriving twice means the assumption
			// broke, and half a record silently reaching the comparator is
			// exactly the quiet wrong answer this design exists to remove.
			if _, seen := finished[id]; seen {
				return fmt.Errorf(
					"propindex: rows for %q arrived interleaved with another record's; "+
						"the index cannot assemble a record safely and the query is refused", path)
			}
			finished[id] = struct{}{}
			cur = &Candidate{
				Path:       path,
				RecordType: recordType,
				RecordID:   string(recordID),
				SourceHash: sourceHash,
				File:       decodeFileMeta(mtime, ctime, size),
				Props:      map[string]StoredProp{},
			}
			curID = id
		}
		if !prop.Valid {
			// A LEFT JOIN row for a note with no declared properties. The record
			// still exists and is still a candidate — FR-005 and D6's flat case.
			continue
		}
		addPropRow(cur, prop.String, elem, state, vtype, vText, vNum, vTime, vLink, vRaw, quoted)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("propindex: the candidate stream ended early: %w", err)
	}
	return flush()
}

func addPropRow(
	cur *Candidate, name string,
	elem, state sql.NullInt64, vtype sql.NullString,
	vText, vNum, vTime, vLink, vRaw []byte, quoted sql.NullInt64,
) {
	sp, ok := cur.Props[name]
	if !ok {
		sp = StoredProp{Name: name, Type: records.PropertyType(vtype.String)}
		cur.PropOrder = append(cur.PropOrder, name)
	}
	if elem.Int64 == StateRowElem {
		sp.State = records.PropertyState(state.Int64)
		// The state row's raw/text columns carry DIAGNOSTIC TEXT for a
		// non-conforming property — what the note held, and the shape that was
		// expected (rows.go's nonConformingEvidence). They are read here and
		// deliberately NOT appended to Elems: a value that failed to parse has
		// no value (R-4), and putting this text where Typed() could decode it
		// would hand the comparator an operand the note does not contain.
		sp.Got = string(vRaw)
		sp.Expected = string(vText)
	} else {
		sp.Elems = append(sp.Elems, StoredElem{
			SourcePos: int(elem.Int64),
			Text:      string(vText),
			Num:       string(vNum),
			Time:      string(vTime),
			Link:      string(vLink),
			Raw:       string(vRaw),
			Quoted:    quoted.Int64 == 1,
		})
	}
	cur.Props[name] = sp
}

// Tasks streams FR-076a's checkbox rows within the same narrowing.
func (ix *Index) Tasks(ctx context.Context, sel Selector, visit func(TaskHit) error) (err error) {
	where, args := narrowing("n", sel)
	q := `SELECT n.path, n.source_hash, t.line, t.status, t.text ` +
		`FROM notes AS n JOIN note_tasks AS t ON t.note_id = n.note_id` + where

	rows, err := ix.query(ctx, PhaseRead, q, args...)
	if err != nil {
		return fmt.Errorf("propindex: streaming tasks: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("propindex: closing the task stream: %w", cerr)
		}
	}()

	for rows.Next() {
		var (
			hit  TaskHit
			text []byte
		)
		if err := rows.Scan(&hit.Path, &hit.SourceHash, &hit.Task.Line, &hit.Task.Status, &text); err != nil {
			return fmt.Errorf("propindex: reading a task row: %w", err)
		}
		hit.Task.Text = string(text)
		if err := visit(hit); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("propindex: the task stream ended early: %w", err)
	}
	return nil
}

// Relations streams the relation child table within the same narrowing.
//
// It returns EDGES, not answers. Reachability, hop counting and the inverse
// direction are computed in Go over what this yields — the store's only
// contribution is assembling a record's `many` values into one row set, and the
// fan-out that assembly creates is de-duplicated on the Go side.
func (ix *Index) Relations(ctx context.Context, sel Selector, visit func(RelationHit) error) (err error) {
	where, args := narrowing("n", sel)
	q := `SELECT n.path, n.record_type, n.record_id, n.source_hash, r.prop, r.elem, r.target, r.heading, r.display, r.raw ` +
		`FROM notes AS n JOIN note_relations AS r ON r.note_id = n.note_id` + where

	rows, err := ix.query(ctx, PhaseRead, q, args...)
	if err != nil {
		return fmt.Errorf("propindex: streaming relations: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("propindex: closing the relation stream: %w", cerr)
		}
	}()

	for rows.Next() {
		var (
			hit                           RelationHit
			recordID                      []byte
			target, heading, display, raw []byte
		)
		if err := rows.Scan(&hit.Path, &hit.RecordType, &recordID, &hit.SourceHash,
			&hit.Relation.Prop, &hit.Relation.Elem, &target, &heading, &display, &raw); err != nil {
			return fmt.Errorf("propindex: reading a relation row: %w", err)
		}
		hit.RecordID = string(recordID)
		hit.Relation.Target = string(target)
		hit.Relation.Heading = string(heading)
		hit.Relation.Display = string(display)
		hit.Relation.Raw = string(raw)
		if err := visit(hit); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("propindex: the relation stream ended early: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// THE PER-CHILD STREAMS — FR-131's named assembly strategy
//
// READ THIS BEFORE ADDING A JOIN. `note_props`, `note_tags` and `note_links`
// are three children of one parent, and the obvious way to assemble a note from
// all three is three LEFT JOINs in one statement. That statement returns their
// CARTESIAN PRODUCT: a note with 30 properties, 10 tags and 40 links yields
// 30 x 10 x 40 = 12,000 rows where it yields 30 today. At B1's 50,000-candidate
// ceiling that is not a slowdown, it is a hang — and every aggregate computed
// over it is wrong by the same factor, which is exactly the defect D16.6 fixed
// once already (COUNT(*) returned 2 and SUM returned 200 where the truth was 1
// and 100).
//
// So each child table is streamed by its OWN statement under the SAME narrowing
// WHERE clause, and the caller assembles per note in Go. That is the pattern
// Tasks and Relations already follow; Tags and Links join them unchanged.
// TestSQLGate_NoStatementJoinsTwoChildTables enforces it mechanically, because
// a comment cannot stop the next person from writing the convenient join.
// ---------------------------------------------------------------------------

// Tags streams the note_tags child table within the same narrowing.
//
// It yields the rows behind `file.tags` (FR-130). The rows arrive grouped by
// note in every access path the planner can choose — note_tags is WITHOUT ROWID
// and keyed on (note_id, elem) — but the caller is not required to depend on
// that: assembling into a map keyed by Path is correct whatever order they come
// in, and no ORDER BY is emitted (ruling R-A forbids one).
func (ix *Index) Tags(ctx context.Context, sel Selector, visit func(TagHit) error) (err error) {
	where, args := narrowing("n", sel)
	q := `SELECT n.path, n.source_hash, g.elem, g.tag ` +
		`FROM notes AS n JOIN note_tags AS g ON g.note_id = n.note_id` + where

	rows, err := ix.query(ctx, PhaseRead, q, args...)
	if err != nil {
		return fmt.Errorf("propindex: streaming tags: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("propindex: closing the tag stream: %w", cerr)
		}
	}()

	for rows.Next() {
		var (
			hit TagHit
			tag []byte
		)
		if err := rows.Scan(&hit.Path, &hit.SourceHash, &hit.Tag.Elem, &tag); err != nil {
			return fmt.Errorf("propindex: reading a tag row: %w", err)
		}
		hit.Tag.Tag = string(tag)
		if err := visit(hit); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("propindex: the tag stream ended early: %w", err)
	}
	return nil
}

// Links streams the note_links child table within the same narrowing.
//
// It yields the rows behind `file.links` and `file.embeds` — ONE stream for
// both, partitioned in Go on the `embed` flag (records.SplitLinkRows). Two
// streams would be two statements over the same table differing only in a
// predicate on `embed`, and `embed` is a value column: a predicate on it is a
// comparison, and comparisons are the Go comparator's (ruling R-A, FR-135).
//
// It is also the edge stream FR-132's backlinks are derived from. The inverse
// direction is computed in Go over what this yields and is stored nowhere,
// which is FR-032's precedent applied unchanged.
func (ix *Index) Links(ctx context.Context, sel Selector, visit func(LinkHit) error) (err error) {
	where, args := narrowing("n", sel)
	q := `SELECT n.path, n.source_hash, l.elem, l.target, l.heading, l.display, l.raw, l.embed ` +
		`FROM notes AS n JOIN note_links AS l ON l.note_id = n.note_id` + where

	rows, err := ix.query(ctx, PhaseRead, q, args...)
	if err != nil {
		return fmt.Errorf("propindex: streaming links: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("propindex: closing the link stream: %w", cerr)
		}
	}()

	for rows.Next() {
		var (
			hit                           LinkHit
			target, heading, display, raw []byte
			embed                         int64
		)
		if err := rows.Scan(&hit.Path, &hit.SourceHash, &hit.Link.Elem,
			&target, &heading, &display, &raw, &embed); err != nil {
			return fmt.Errorf("propindex: reading a link row: %w", err)
		}
		hit.Link.Target = string(target)
		hit.Link.Heading = string(heading)
		hit.Link.Display = string(display)
		hit.Link.Raw = string(raw)
		hit.Link.Embed = embed == 1
		if err := visit(hit); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("propindex: the link stream ended early: %w", err)
	}
	return nil
}
