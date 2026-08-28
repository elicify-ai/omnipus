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

// RelationHit is one relation edge with its owning record.
type RelationHit struct {
	Path       string
	RecordType string
	RecordID   string
	SourceHash string
	Relation   RelationRow
}

// Store is the seam. The SQLite implementation and the no-SQLite refusal both
// satisfy it, so nothing above this package needs a build tag of its own.
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

	// NeedsFullIndex reports that the store holds nothing usable and the caller
	// must re-derive it from the notes — a fresh file, or one written by an
	// incompatible schema version. Deleting the file is always a legal way to
	// ask for this (FR-020a).
	NeedsFullIndex() bool

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
