// Omnipus — ADR-068 D16.2b / FR-021, FR-064, D16.5: the store contract, the two
// bounds, and the write ordering.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package propindex

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE BOUNDS — FR-064, as respecified in revision 6
//
// One number was being asked to do two different jobs. They protect different
// resources, they fire at different moments, and each names a remedy that
// actually reduces the number it fired on.
// ---------------------------------------------------------------------------

const (
	// BoundNarrowedCandidates (B1) bounds WORK. It counts the narrowed candidate
	// population — the distinct RECORDS the narrowing predicates select — and it
	// is taken BEFORE any candidate is retrieved. It is deliberately the
	// supported vault size rather than a second independent number to reconcile.
	//
	// Its cost is stated rather than hidden: it can refuse a query whose filter
	// would have matched three records. The remedy it names is therefore SCOPE or
	// KIND — the dimensions the count actually ranges over — and never "add a
	// filter", because adding a filter does not change the number that fired.
	BoundNarrowedCandidates = 50_000

	// BoundSurvivors (B2) bounds MEMORY. It counts the records the comparator has
	// ACCEPTED, and it is a hard abort DURING evaluation — not a precondition,
	// because "the rows surviving the filter" is a quantity only the Go
	// comparator can produce, and it produces it by evaluating candidates.
	//
	// Revision 5 called this "counted before any row is materialised". It cannot
	// be, and saying so was how a bound came to be enforced after the work it
	// bounds.
	BoundSurvivors = 10_000
)

// BoundError is a refusal, not a warning. Neither bound is a politeness limit
// and neither may be relaxed on the ground that the properties index is assumed
// to make it cheap (ADR-068 D16.3a, condition C-3).
type BoundError struct {
	// Bound is "B1" or "B2".
	Bound string
	// Count is what was measured — B1's narrowed population, or the number of
	// survivors at the moment the abort fired.
	Count int
	Limit int
	// Remedy is the instruction the caller can actually act on.
	Remedy string
}

func (e *BoundError) Error() string {
	switch e.Bound {
	case "B1":
		return fmt.Sprintf(
			"this query narrows to %d records, above the %d the index will evaluate; %s",
			e.Count, e.Limit, e.Remedy)
	default:
		return fmt.Sprintf(
			"this query matched more than %d records (stopped at %d); %s",
			e.Limit, e.Count, e.Remedy)
	}
}

// IsBoundExceeded reports whether err is one of FR-064's two refusals.
func IsBoundExceeded(err error) bool {
	var be *BoundError
	return errors.As(err, &be)
}

// Options configures Open. It lives here rather than beside the SQLite
// implementation because BOTH halves of the platform gate take it, and a
// parameter type that exists on only one of them makes the refusing build fail
// to compile instead of refusing — which is the opposite of FR-020h.
type Options struct {
	// Recorder, when set, captures every statement the store executes. It is the
	// AC-8.10 control's observation point and a diagnostic seam; production
	// callers leave it nil.
	Recorder *Recorder
}

// The refusal on a build without SQLite is pkg/records.RequirePropertyIndex's,
// and it is returned UNCHANGED (nosqlite.go). This package deliberately declares
// no sentinel of its own: two refusals for one condition is how a caller comes to
// handle the one it knows about and treat the other as an ordinary failure.

// ---------------------------------------------------------------------------
// THE STORE
// ---------------------------------------------------------------------------

// Selector is everything this store is permitted to decide.
//
// Read the list twice, because it is the design: a record type, a note kind, a
// path prefix. Every one is set membership over an indexed column. NONE is a
// comparison governed by R-1..R-13, and no field of a Selector ever holds a
// property name or a property value. If a filter belongs in a Selector, the
// filter is being pushed into SQLite and ruling R-A has been broken.
type Selector struct {
	// RecordType selects the candidate population by declared record type.
	// Empty means every type, including notes that declare none.
	RecordType string
	// Kind narrows by note kind (KindNote / KindAttachment).
	Kind string
	// PathPrefix is workspace/collection scope (FR-060). It is a
	// CALLER-INDEPENDENT prefix built from an already-resolved root — never
	// caller text — and it is escaped before it reaches a LIKE pattern.
	PathPrefix string
}

// StoredElem is one persisted element of one property, as it comes back out.
type StoredElem struct {
	// SourcePos is the element's position among the property's SOURCE elements —
	// the position the operator sees in their own file, not an index into a
	// filtered slice.
	SourcePos int
	Text      string
	Num       string
	Time      string
	Link      string
	Raw       string
	Quoted    bool
}

// StoredProp is one persisted property of one candidate.
type StoredProp struct {
	Name string
	// State is FR-021b's three-state flag, read back from its own column. It is
	// the reason a non-conforming value and an absent property are still
	// distinguishable after a round trip through storage.
	State records.PropertyState
	Type  records.PropertyType
	Elems []StoredElem

	// Got and Expected are DIAGNOSTIC TEXT for a non-conforming property: what
	// the note actually held, and the shape that would have been accepted. Both
	// are empty for a conforming or absent property.
	//
	// THEY ARE NOT VALUES AND MUST NEVER BE COMPARED. A non-conforming value has
	// no value (R-4) — that is the rule, and these fields do not soften it. They
	// exist so a problem list can say "arr is '50k' where a decimal is required
	// — write 50000" instead of "arr does not conform", which names the fault
	// and withholds every fact needed to fix it. They are deliberately NOT in
	// Elems, so Typed() cannot decode them into an operand.
	Got      string
	Expected string
}

// Candidate is one record the narrowing predicates selected — and nothing more.
//
// It is NOT a result. Whether it belongs in an answer is the comparator's
// decision, taken in Go, over the values below.
type Candidate struct {
	Path       string
	RecordType string
	RecordID   string
	// SourceHash is FR-020c's freshness token as this index saw it. The caller
	// compares it against the hash the TEXT index holds for the same document;
	// unequal, missing or empty means "the two indexes disagree" — never "the
	// properties index is stale", because the comparison establishes
	// disagreement, not which side is behind.
	SourceHash string

	// File is FR-131's stat metadata, decoded from the three `notes` columns:
	// `file.mtime`, `file.ctime` and `file.size`.
	//
	// It rides on the PARENT row, so it costs no extra rows in the candidate
	// stream. Every comparison over it happens in the Go comparator, like every
	// other comparison — the columns exist so the values can be RETRIEVED, never
	// so a predicate can mention them (FR-135, and the column half of AC-8.10's
	// guard names all three by name).
	//
	// A note whose walk carried no stat has a zero FileMeta: `Known` false,
	// which is ABSENT, not 1970.
	File FileMeta

	// Props is keyed by property name. PropOrder preserves the schema's
	// declaration order so a report reads the way the operator wrote it.
	Props     map[string]StoredProp
	PropOrder []string
}

// Prop returns one stored property.
func (c Candidate) Prop(name string) (StoredProp, bool) {
	p, ok := c.Props[name]
	return p, ok
}

// Verdict is what the comparator tells the stream about a candidate.
type Verdict bool

const (
	// Accepted means the comparator kept the record. Accepted records count
	// against B2.
	Accepted Verdict = true
	// Rejected means the comparator excluded it. A rejection costs work and no
	// memory, which is why B1 bounds candidates and B2 bounds survivors.
	Rejected Verdict = false
)

// TaskHit is one checkbox row with the note context a renderer needs.
type TaskHit struct {
	Path       string
	SourceHash string
	Task       TaskRow
}

// TagHit is one tag with the note context a renderer needs — FR-130's
// `file.tags`, streamed by its own statement (FR-131).
type TagHit struct {
	Path       string
	SourceHash string
	Tag        TagRow
}

// LinkHit is one outgoing wikilink or embed with its owning note.
//
// ONE hit type for both `file.links` and `file.embeds`: the two differ only by
// TagRow's sibling flag `LinkRow.Embed`, and partitioning them in Go
// (records.SplitLinkRows) keeps `embed` out of every predicate. It is also the
// edge FR-132's backlinks are derived from — the inverse direction is computed
// over this stream and stored nowhere.
type LinkHit struct {
	Path       string
	SourceHash string
	Link       LinkRow
}

// RelationHit is one relation edge with its owning record.
type RelationHit struct {
	Path       string
	RecordType string
	RecordID   string
	SourceHash string
	Relation   RelationRow
}

// IndexedNote is one row of AllPaths' maintenance walk: everything the indexer
// needs to decide whether the stored row is still current, and nothing else.
//
// It is a STRUCT rather than four positional strings because that decision now
// takes more than one comparison, and a callback of four same-typed parameters
// is one that gets called with two of them swapped. It carries no properties,
// no relations and no tasks: this is the indexer's own walk, deliberately
// outside FR-064's bounds, and it must never grow into something a
// knowledge_find-shaped query could be served from.
type IndexedNote struct {
	// Path is the collection-relative path — this row's identity.
	Path string
	// Kind is KindNote or KindAttachment.
	Kind string
	// SourceHash is the note's content freshness token, "" for an attachment
	// (whose bytes are never opened, FR-039a).
	SourceHash string

	// DeclaredType and SchemaFingerprint are the SECOND freshness token, and
	// they are the reason this walk returns a struct at all.
	//
	// A row is derived from the note's bytes AND from the schema its `type:`
	// names, and comparing only the first meant a schema change was never
	// applied to the notes it governs — a newly created record type left every
	// note that already declared it invisible to every query, permanently,
	// while the answer still reported COMPLETE. NoteRows' own comment on these
	// two fields has the full account; what matters here is that the indexer
	// can perform the comparison WITHOUT re-reading or re-parsing the note,
	// which is the whole economy of the hash-equal skip.
	DeclaredType      string
	SchemaFingerprint string
}

// Store is the seam. The SQLite implementation and the no-SQLite refusal both
// satisfy it, so nothing above this package needs a build tag of its own.
//
// It is over the twelve-method line `interfacebloat` draws, deliberately.
// Splitting it would mean splitting the PLATFORM GATE with it: the whole reason
// this interface exists is that one type satisfies it on a build with SQLite and
// another refuses on a build without, and two halves means two gates that can
// drift apart — which is the failure FR-020h names. The method count is also not
// an accident of design: FR-131 requires each child table to be streamed by its
// OWN statement, so a new child table IS a new method, and the alternative to
// twelve methods is the multi-child join that D16.6 already fixed once.
//
//nolint:interfacebloat // one interface IS the platform gate (FR-020h); one method per child stream is FR-131's assembly rule.
type Store interface {
	// UpsertNote replaces everything the index holds for one path, in one
	// transaction. It is the FIRST of D16.5's three writes.
	UpsertNote(ctx context.Context, rows NoteRows) error

	// DeleteNote removes a note and every child row it owns.
	DeleteNote(ctx context.Context, path string) error

	// CountCandidates is B1, taken before anything is retrieved.
	CountCandidates(ctx context.Context, sel Selector) (int, error)

	// Candidates streams the narrowed population one record at a time, calling
	// visit once per record. visit returns Accepted for a record the comparator
	// kept; the stream aborts with a B2 BoundError once accepted records exceed
	// BoundSurvivors.
	//
	// It holds one record in memory at a time. That is FR-066b, and it is
	// asserted by measured peak RSS at both bounds rather than by inspection.
	Candidates(ctx context.Context, sel Selector, visit func(Candidate) (Verdict, error)) error

	// Tasks streams FR-076a's checkbox rows within the same narrowing.
	Tasks(ctx context.Context, sel Selector, visit func(TaskHit) error) error

	// Relations streams the relation child table within the same narrowing, so
	// reachability is computed in Go over the edges rather than by asking SQLite
	// to decide anything about them.
	Relations(ctx context.Context, sel Selector, visit func(RelationHit) error) error

	// Tags streams the note_tags child table within the same narrowing —
	// FR-130's `file.tags`.
	//
	// Links streams note_links, behind `file.links`, `file.embeds` and (by
	// derivation) `file.backlinks`.
	//
	// THEY ARE SEPARATE METHODS FOR A REASON, and it is FR-131's named assembly
	// strategy rather than an accident of interface design. `note_props`,
	// `note_tags` and `note_links` are three children of one parent, and joining
	// them in one statement returns their CARTESIAN PRODUCT — 30 properties x
	// 10 tags x 40 links is 12,000 rows for a note that yields 30 today. At
	// B1's 50,000-candidate ceiling that is a hang, and every aggregate over it
	// is wrong by the same factor. D16.6 fixed this exact fan-out once already.
	//
	// So each child is streamed by its OWN statement under the SAME narrowing,
	// and a caller that wants a whole note assembles the streams in Go, keyed by
	// Path. A future convenience method that returns "everything about a note"
	// must be built that way too — never by adding a second child to the
	// candidate join.
	Tags(ctx context.Context, sel Selector, visit func(TagHit) error) error
	Links(ctx context.Context, sel Selector, visit func(LinkHit) error) error

	// RefreshNoteStat updates ONLY the stat columns of one note, only where they
	// differ, and reports whether anything actually changed.
	//
	// It is FR-136's half of FR-131. `git checkout`, rsync, `touch` and an
	// iCloud resync all move a file's mtime while leaving its bytes identical,
	// so a sync that skips on hash equality alone freezes `file.mtime` at the
	// last CONTENT change — and `sort by file.mtime desc`, the commonest Bases
	// view there is, then returns a plausible, stable, WRONG ordering with no
	// error anywhere. An attachment, whose bytes are never re-read at all, would
	// freeze at first index forever.
	//
	// IT DOES NOT re-parse the note, write a child row, touch source_hash or
	// touch indexed_at. Those omissions are the point rather than an
	// optimisation: this is the correction a content-unchanged skip can afford,
	// and moving source_hash would forge agreement with the text index that
	// D16.5's whole write ordering exists to detect the ABSENCE of.
	//
	// A path the store does not hold updates nothing and returns (false, nil) —
	// the vault is the source of truth and the index is allowed to be behind it,
	// the same posture DeleteNote takes.
	//
	// THE BOOL IS LOAD-BEARING, and it is why this is not a void method. It is
	// what lets a caller COUNT metadata-only refreshes, and therefore what makes
	// "the mtime is right" distinguishable from "the mtime is right because we
	// re-indexed the whole note" — which is the bug FR-136 exists to prevent and
	// which a void return would leave unobservable.
	//
	// CTIME IS REFRESHED, BUT ONLY UPWARDS — and the asymmetry is the whole of
	// FR-133 applied to a refresh.
	//
	// This method's first version took size and mtime only, on the reasoning
	// that a birth time does not change so the value written at index time
	// stays correct. That reasoning is sound and is not what the two extra
	// parameters are for. They are for the note whose row was written with NO
	// birth time at all — every note indexed before the walk carried one, and
	// every note on a pass where the platform could not produce one. Such a row
	// is only ever re-written by a full UpsertNote, and a content-unchanged skip
	// never performs one, so without this the value would stay NULL until the
	// next schema-version rebuild.
	//
	// hasCtime FALSE therefore means "the caller has nothing to offer", NEVER
	// "clear what is stored". A Linux pass with no statx birth-time support must
	// not erase the value a macOS pass over the same synced vault wrote —
	// FR-133's absence is the absence of a FACT, and one platform not knowing
	// something is not evidence that another platform's answer was wrong.
	RefreshNoteStat(ctx context.Context, path string, size, mtimeNanos, ctimeNanos int64, hasCtime bool) (bool, error)

	// NeedsFullIndex reports that the store holds nothing usable and the caller
	// must re-derive it from the notes — a fresh file, or one written by an
	// incompatible schema version. Deleting the file is always a legal way to
	// ask for this (FR-020a).
	NeedsFullIndex() bool

	// AllPaths visits every path the store currently holds, in no particular
	// order, with everything the indexer needs to decide whether that row is
	// still current (see IndexedNote).
	//
	// This is the sync pipeline's OWN maintenance walk — the exact analog of a
	// text-index manifest's Entries loop — and it is deliberately NOT subject
	// to B1/B2 (FR-064's bounds protect a QUERY's narrowed population; this
	// walk exists to let the indexer detect a note that left the vault, which
	// requires seeing every row the store holds, however many there are).
	// Nothing about it is exposed to a caller outside the indexing pipeline: it
	// carries no properties, no relations, no tasks, and it must never be
	// reached from a knowledge_find-shaped query.
	AllPaths(ctx context.Context, visit func(IndexedNote) error) error

	// Close releases the database.
	Close() error
}

// ---------------------------------------------------------------------------
// THE WRITE ORDERING — ADR-068 D16.5, re-derived in revision 7
// ---------------------------------------------------------------------------

// IndexNote performs the three writes in the ONE order that makes every partial
// failure detectable:
//
//	SQLite row (with its source_hash) -> bleve document -> manifest entry.
//
// Revision 6 specified bleve first and claimed both directions were caught.
// Trace it: a failure after bleve and before SQLite leaves the SQLite row and
// the manifest BOTH at the old hash, so they compare EQUAL and the answer is
// reported COMPLETE over a stale row. That is the reachable case and it was the
// undetected one.
//
// Putting the writer that can fail FIRST costs false positives, and that is the
// direction to err in. A record flagged "possibly stale" while SQLite is
// actually ahead of bleve is a caveat on a correct answer; a record reported
// fresh while it is stale is the failure this design exists to remove.
//
//	| failure point            | sqlite | bleve | compare | detected?          |
//	|--------------------------|--------|-------|---------|--------------------|
//	| before the SQLite write  | old    | old   | equal   | nothing was written|
//	| SQLite write fails       | old    | new   | differ  | yes                |
//	| after SQLite, before bleve| new   | old   | differ  | yes                |
//	| after bleve, before manifest| new | new   | equal   | yes, for the
//	|                          |        |       |         | comparison that
//	|                          |        |       |         | matters: the two
//	|                          |        |       |         | indexes agree and
//	|                          |        |       |         | SyncWith re-indexes
//	|                          |        |       |         | on a missing entry |
//	| all three complete       | new    | new   | equal   | correct            |
//
// writeText and writeManifest are the caller's — this package does not import
// pkg/knowledge, and must not: the ordering is the contract, not the writer.
func IndexNote(
	ctx context.Context,
	store Store,
	rows NoteRows,
	writeText func(context.Context) error,
	writeManifest func(context.Context) error,
) error {
	if store == nil {
		return errors.New("propindex: IndexNote called with no store")
	}
	if rows.Path == "" {
		return errors.New("propindex: IndexNote called with an empty path")
	}
	if err := store.UpsertNote(ctx, rows); err != nil {
		return fmt.Errorf("propindex: properties row for %q: %w", rows.Path, err)
	}
	if writeText != nil {
		if err := writeText(ctx); err != nil {
			return fmt.Errorf("propindex: text index for %q: %w", rows.Path, err)
		}
	}
	if writeManifest != nil {
		if err := writeManifest(ctx); err != nil {
			return fmt.Errorf("propindex: manifest entry for %q: %w", rows.Path, err)
		}
	}
	return nil
}

// Freshness is the verdict of comparing the two indexes for one returned hit.
type Freshness int

const (
	// FreshnessAgree — the two indexes have seen the same bytes.
	FreshnessAgree Freshness = iota
	// FreshnessDisagree — they have not. The reason string says "the two indexes
	// disagree", NOT "the properties index is stale": the comparison establishes
	// disagreement, not which side is behind, and claiming the second is a
	// precision the mechanism does not have.
	FreshnessDisagree
	// FreshnessUnknown — one side carries no hash at all. An empty hash is
	// unknown freshness, which is FLAGGED, never assumed fresh. It is also what
	// pkg/knowledge deliberately stores for an attachment, whose bytes it must
	// not open.
	FreshnessUnknown
)

// CompareFreshness implements FR-020c's comparison for one returned hit.
func CompareFreshness(indexHash, textHash string) Freshness {
	if indexHash == "" || textHash == "" {
		return FreshnessUnknown
	}
	if indexHash == textHash {
		return FreshnessAgree
	}
	return FreshnessDisagree
}

// Reason renders the problem-list entry for a non-agreeing hit.
func (f Freshness) Reason() string {
	switch f {
	case FreshnessDisagree:
		return "the two indexes disagree about this note; it may have changed since it was indexed"
	case FreshnessUnknown:
		return "the freshness of this note is unknown; one of the two indexes holds no content hash for it"
	}
	return ""
}
