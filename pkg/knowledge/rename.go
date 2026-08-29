// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// rename.go — renaming and moving a note without breaking the collection
// (ADR-067 D10; FR-103, FR-104, FR-105, FR-090; US-13, US-15).
//
// Renaming a note is not a filesystem operation with some bookkeeping attached.
// In a collection, a note's NAME is how every other note refers to it: 87% of
// the notes in the reference collection carry frontmatter wikilinks (D10), so a
// rename that only moves the file severs most of the structure of the operator's
// own writing, in their real files, with no error anywhere.
//
// So this file's job is: compute every consequence of the rename first, write
// the whole plan down (journal.go), and only then touch anything.
//
// # Four properties, and where each is enforced
//
// ONE PARSER. Discovery uses the resolver that already exists — BuildLinkGraph
// over links.go's scanner. Nothing here re-parses markdown or YAML. A second
// link parser would drift from the first, and the first is what decides whether
// a link is "inbound" in every other feature; two answers to that question is a
// rename that misses links the graph can see.
//
// ONE EDIT MECHANISM. A link is replaced by splicing over its recorded byte
// offset, inside its own recorded raw text, preserving the surrounding
// characters — including whitespace inside the brackets. Frontmatter is never
// parsed and never re-serialised, which is what makes FR-105 ("only the link
// value differs, byte-compared") structural rather than aspirational: comments,
// anchors and nested lists cannot be damaged by a rewriter that never reads
// them.
//
// CONTAINMENT ON THE REAL PATH. Every path this file writes through has been
// resolved with symlinks followed and re-checked against the collection root
// (FR-043), because every path here ultimately comes from text inside a file
// Omnipus did not write. A lexical check is defeated by one symlink.
//
// AMBIGUITY IS REPORTED, NEVER RESOLVED QUIETLY. If a rename would make a
// previously-unambiguous basename ambiguous, the plan says so, and by default
// refuses. The collection cannot tell you afterwards which note "[[Meeting]]"
// was supposed to mean.

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Errors from the rename planner. Sentinels, because each one calls for a
// different response from the caller: a missing source is a stale UI, an
// existing destination is a name clash the operator must resolve, and a new
// ambiguity is a decision only they can make.
var (
	// ErrRenameInvalidPath means From or To is empty, absolute, or does not
	// resolve inside the collection.
	ErrRenameInvalidPath = errors.New("knowledge: invalid rename path")

	// ErrRenameSourceMissing means From does not name an ordinary file in the
	// collection.
	ErrRenameSourceMissing = errors.New("knowledge: rename source not found")

	// ErrRenameSourceNotAddressable means From exists but is not part of the
	// collection the graph can see — a symbolic link, or something inside a
	// tool-state directory the walk deliberately excludes. Renaming it could
	// not be made link-safe, so it is refused rather than half-done.
	ErrRenameSourceNotAddressable = errors.New("knowledge: rename source is not an addressable note")

	// ErrRenameDestinationNotAddressable means To names a place the graph
	// could never see: a path that reaches its parent only through a symbolic
	// link. Declared separately from the source sentinel because the two sides
	// are protected by different things and only one of them was protected at
	// all — see the FR-044 block in Plan.
	ErrRenameDestinationNotAddressable = errors.New("knowledge: rename destination is not an addressable note path")

	// ErrRenameDestinationExists means To already names a different file.
	ErrRenameDestinationExists = errors.New("knowledge: rename destination already exists")

	// ErrRenameDestinationParentMissing means To's parent directory does not
	// exist. Named separately because "create the folder" is a different
	// action from "pick another name".
	ErrRenameDestinationParentMissing = errors.New("knowledge: rename destination directory does not exist")

	// ErrRenameCreatesAmbiguity means the rename would make a basename that
	// was unambiguous ambiguous. Refused unless the caller opts in.
	ErrRenameCreatesAmbiguity = errors.New("knowledge: rename would create an ambiguous note name")
)

// AmbiguityReport names a basename that more than one note would answer to
// after the rename, and every note that would answer to it.
//
// Candidates are in the resolver's own tie-break order (FR-040), so the first
// entry is the note a bare "[[Name]]" would actually reach — which is the
// single most useful fact for someone deciding whether to go ahead.
type AmbiguityReport struct {
	// Basename is the bare link name that becomes ambiguous.
	Basename string
	// Candidates is every collection-relative path it would match, in
	// tie-break order.
	Candidates []string
	// WasAmbiguous reports whether the name was already ambiguous before the
	// rename. A plan only refuses when this is false.
	WasAmbiguous bool
}

// AmbiguityError carries the report alongside the sentinel, so a caller that
// wants to offer "rename anyway" has the facts to show without re-planning.
type AmbiguityError struct {
	Report AmbiguityReport
}

func (e *AmbiguityError) Error() string {
	return fmt.Sprintf("%s: %q would match %d notes: %s",
		ErrRenameCreatesAmbiguity.Error(), e.Report.Basename,
		len(e.Report.Candidates), strings.Join(e.Report.Candidates, ", "))
}

// Unwrap makes errors.Is(err, ErrRenameCreatesAmbiguity) work.
func (e *AmbiguityError) Unwrap() error { return ErrRenameCreatesAmbiguity }

// RenameRequest is one rename or move.
type RenameRequest struct {
	// From is the collection-relative path of the file to rename.
	From string
	// To is the collection-relative path it should have.
	To string
	// AllowAmbiguity lets the rename proceed even though it makes a
	// previously-unambiguous basename ambiguous. The ambiguity is reported
	// either way; this only decides whether it is fatal.
	AllowAmbiguity bool
}

// RenamePlan is everything the rename will do, computed before anything is
// touched.
type RenamePlan struct {
	// Journal is the record to write before applying. Nil for a no-op.
	Journal *Journal
	// NoOp reports that From and To name the same path exactly; there is
	// nothing to do and nothing is written.
	NoOp bool
	// CaseOnly reports a rename differing only in letter case.
	CaseOnly bool
	// Ambiguity is set when the destination basename would match more than
	// one note after the rename.
	Ambiguity *AmbiguityReport
	// LinksRewritten is the total number of links the plan changes.
	LinksRewritten int
	// Skipped carries every entry BuildLinkGraph could not read or address
	// while discovering this plan (FR-112, mirroring GraphTool's own
	// Incomplete/Skipped pair). It is the load-bearing reason Incomplete
	// exists: a note this collection could not read might hold a citation
	// TO or FROM the subject that this plan can never see and therefore can
	// never rewrite. Reporting it here is what keeps a rename over a
	// partly-unreadable collection from reading as complete when it is not.
	Skipped []SkippedEntry
	// Incomplete is true when Skipped is non-empty. A caller MUST NOT treat
	// a nil error as proof every citation was found and rewritten when this
	// is true — the honest reading is "rewrote everything this plan could
	// see", not "rewrote everything".
	Incomplete bool
}

// RenameAuditEvent is one auditable knowledge-base mutation or refusal
// (FR-090, US-15). It is emitted for every outcome, including refusals — "a
// refused write is audited as a refusal, not omitted".
type RenameAuditEvent struct {
	// Op is the operation name, "knowledge.rename".
	Op string
	// AgentID is the agent on whose behalf the write was attempted, when the
	// caller supplied one.
	AgentID string
	// Collection is the collection root's real path.
	Collection string
	// From and To are the collection-relative paths.
	From, To string
	// JournalID identifies the operation, empty when the plan never got as
	// far as a journal.
	JournalID string
	// Paths is every path the operation touched or intended to touch, sorted.
	Paths []string
	// Outcome is "applied", "noop", "refused" or "incomplete".
	Outcome string
	// Reason carries the refusal or failure text; empty on success.
	Reason string
	// At is when the event occurred.
	At time.Time
}

// Audit outcome values.
const (
	// RenameOutcomeApplied — the rename and every rewrite completed.
	RenameOutcomeApplied = "applied"
	// RenameOutcomeNoOp — nothing needed doing.
	RenameOutcomeNoOp = "noop"
	// RenameOutcomeRefused — the operation was refused before any file was
	// touched.
	RenameOutcomeRefused = "refused"
	// RenameOutcomeIncomplete — the operation started and could not finish;
	// the journal is retained.
	RenameOutcomeIncomplete = "incomplete"
)

// RenameAuditFunc receives one event per attempt.
type RenameAuditFunc func(RenameAuditEvent)

// RenameResult is the outcome of a completed (or attempted) rename.
type RenameResult struct {
	// From and To are the collection-relative paths.
	From, To string
	// NoOp reports that nothing needed doing.
	NoOp bool
	// CaseOnly reports a case-only relabel.
	CaseOnly bool
	// JournalID identifies the operation, empty for a no-op.
	JournalID string
	// FilesRewritten is how many files this run actually rewrote.
	FilesRewritten int
	// LinksRewritten is how many links the plan changed.
	LinksRewritten int
	// Touched is every path written or moved, sorted — the audit payload.
	Touched []string
	// Ambiguity is set when the rename created one and the caller allowed it.
	Ambiguity *AmbiguityReport
	// Recovery is the full per-step account, nil for a no-op.
	Recovery *RecoverResult
	// Skipped and Incomplete carry RenamePlan's own fields of the same name
	// through to the outcome — see RenamePlan.Incomplete. A nil error here
	// is not, by itself, proof the rename saw every citation.
	Skipped    []SkippedEntry
	Incomplete bool
}

// Renamer renames notes inside one collection.
//
// It holds no state between calls: every operation re-reads the collection, so
// two renames in a row cannot disagree about what the second one is starting
// from.
type Renamer struct {
	// FS is the filesystem surface used for every read. Nil means the real
	// filesystem.
	FS LinkFS
	// Root is the validated collection root.
	Root CollectionRoot
	// Store is where journals are written. Nil means the collection's default
	// journal directory.
	Store *JournalStore
	// AgentID is recorded in audit events, when the caller has one.
	AgentID string
	// Audit receives one event per attempt, including refusals.
	//
	// Nil is a WIRING DEFECT for anything an agent can reach, not a
	// configuration: FR-090 requires a record for every mutation and every
	// refusal, and the tool layer refuses to execute at all without a sink
	// (see authoring_tools.go's requireAudit). It stays a plain nilable field
	// here so a unit test can drive the planner without a sink; nothing an
	// agent can call reaches this type with a nil one.
	Audit RenameAuditFunc

	// Lock configures D14 tier-1 mutual exclusion for every file the rename
	// rewrites. It is handed to the journal store, which takes the lock
	// around each step's read-compare-write.
	Lock NoteLockConfig
}

func (r *Renamer) fs() LinkFS {
	if r.FS != nil {
		return r.FS
	}
	return OSLinkFS()
}

func (r *Renamer) store() *JournalStore {
	st := r.Store
	if st == nil {
		st = NewJournalStore(DefaultJournalDir(r.Root.Path()))
	}
	// The lock configuration travels with the renamer, not with the store, so
	// a caller that supplied its own store still gets tier-1 exclusion.
	st.Lock = r.Lock
	return st
}

func (r *Renamer) emit(ev RenameAuditEvent) {
	if r.Audit == nil {
		return
	}
	ev.Op = "knowledge.rename"
	ev.AgentID = r.AgentID
	ev.Collection = r.Root.Path()
	ev.At = time.Now().UTC()
	r.Audit(ev)
}

// Rename plans, journals and applies a rename in that order.
//
// The apply half is JournalStore.Recover — the same function a crash recovery
// calls, deliberately. A separate "happy path" applier would be code that only
// ever runs in production while the recovery path only ever runs in tests, and
// the two would drift in exactly the direction that matters least until the day
// it matters most.
func (r *Renamer) Rename(req RenameRequest) (*RenameResult, error) {
	// FR-104, the half that was built and never invoked: a rename that starts
	// on top of an interrupted one must finish the interrupted one FIRST.
	//
	// Planning reads the collection as it stands. If a previous rename died
	// half-way, "as it stands" is a collection where some inbound links point
	// at the old name and some at the new one, and a plan computed from that
	// is wrong for both halves — while a second journal lands on disk beside
	// the orphaned first, which nothing would ever read. Recovering first
	// makes the state the planner sees a state the planner can describe.
	//
	// A recovery that cannot complete is NOT swallowed: it is returned, so the
	// caller learns the collection is mid-operation rather than being handed a
	// plan built on a contradiction.
	if _, recErr := r.RecoverPending(); recErr != nil {
		return nil, fmt.Errorf("knowledge: a previous rename in this collection is incomplete and must be resolved first: %w", recErr)
	}

	plan, err := r.Plan(req)
	if err != nil {
		r.emit(RenameAuditEvent{
			From:    normalizeRel(req.From),
			To:      normalizeRel(req.To),
			Outcome: RenameOutcomeRefused,
			Reason:  err.Error(),
			Paths:   sortedUnique(normalizeRel(req.From), normalizeRel(req.To)),
		})
		return nil, err
	}
	if plan.NoOp {
		res := &RenameResult{From: normalizeRel(req.From), To: normalizeRel(req.To), NoOp: true}
		r.emit(RenameAuditEvent{From: res.From, To: res.To, Outcome: RenameOutcomeNoOp, Paths: sortedUnique(res.From)})
		return res, nil
	}

	j := plan.Journal
	store := r.store()
	// FR-104: the journal reaches disk, durably, before the first byte of the
	// operator's collection is touched.
	if err := store.Write(j); err != nil {
		r.emit(RenameAuditEvent{
			From: j.From, To: j.To, Outcome: RenameOutcomeRefused,
			Reason: err.Error(), Paths: j.Paths(),
		})
		return nil, err
	}

	rec, applyErr := store.Recover(r.fs(), r.Root, j)
	res := &RenameResult{
		From:           j.From,
		To:             j.To,
		CaseOnly:       j.CaseOnly,
		JournalID:      j.ID,
		LinksRewritten: plan.LinksRewritten,
		Ambiguity:      plan.Ambiguity,
		Recovery:       rec,
		Skipped:        plan.Skipped,
		Incomplete:     plan.Incomplete,
	}
	if rec != nil {
		res.Touched = rec.Touched
		for _, st := range rec.Steps {
			if st.Outcome == StepApplied {
				res.FilesRewritten++
			}
		}
	}
	if applyErr != nil {
		r.emit(RenameAuditEvent{
			From: j.From, To: j.To, JournalID: j.ID,
			Outcome: RenameOutcomeIncomplete, Reason: applyErr.Error(), Paths: j.Paths(),
		})
		return res, applyErr
	}
	r.emit(RenameAuditEvent{
		From: j.From, To: j.To, JournalID: j.ID,
		Outcome: RenameOutcomeApplied, Paths: rec.Touched,
	})
	return res, nil
}

// PendingJournals lists operations that were started and not confirmed
// complete. A non-empty list is the "the collection is mid-rename" report
// US-13 AS-4 requires; the error return carries any record that would not
// parse, which is a louder problem than a pending one.
func (r *Renamer) PendingJournals() ([]*Journal, error) { return r.store().List() }

// RecoverPending carries every pending journal forward, oldest first, and
// returns one result per journal.
//
// A blocked journal does not stop the ones after it: they are independent
// operations, and finishing three of four is strictly better than finishing
// none. The returned error names every journal that could not be completed.
func (r *Renamer) RecoverPending() ([]*RecoverResult, error) {
	store := r.store()
	journals, listErr := store.List()
	var results []*RecoverResult
	var failures []string
	for _, j := range journals {
		rec, err := store.Recover(r.fs(), r.Root, j)
		if rec != nil {
			results = append(results, rec)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", j.ID, err))
			r.emit(RenameAuditEvent{
				From: j.From, To: j.To, JournalID: j.ID,
				Outcome: RenameOutcomeIncomplete, Reason: err.Error(), Paths: j.Paths(),
			})
			continue
		}
		r.emit(RenameAuditEvent{
			From: j.From, To: j.To, JournalID: j.ID,
			Outcome: RenameOutcomeApplied, Paths: rec.Touched,
		})
	}
	switch {
	case listErr != nil && len(failures) > 0:
		return results, fmt.Errorf("%w; %s", listErr, strings.Join(failures, "; "))
	case listErr != nil:
		return results, listErr
	case len(failures) > 0:
		return results, fmt.Errorf("%w: %s", ErrJournalIncomplete, strings.Join(failures, "; "))
	}
	return results, nil
}

// Plan computes the whole rename without touching anything.
func (r *Renamer) Plan(req RenameRequest) (*RenamePlan, error) {
	if !r.Root.Valid() {
		return nil, fmt.Errorf("%w: root not initialised", ErrCollectionRootInvalid)
	}
	fsys := r.fs()

	from := normalizeRel(req.From)
	to := normalizeRel(req.To)
	for _, side := range []struct{ label, p string }{{"from", from}, {"to", to}} {
		if side.p == "" || side.p == "." || side.p == ".." {
			return nil, fmt.Errorf("%w: %s is empty", ErrRenameInvalidPath, side.label)
		}
		if IsAbsoluteTarget(side.p) {
			return nil, fmt.Errorf("%w: %s %q is absolute", ErrRenameInvalidPath, side.label, side.p)
		}
		// A move must not cross into a tool-state directory, in EITHER
		// direction. ResolveContained below cannot catch this: .omnipus-vault/
		// and .obsidian/ are INSIDE the collection root, so containment holds
		// and the move is accepted.
		//
		// Into one, the note lands where no walker descends
		// (scanSkippedDirNames) — it vanishes from search, backlinks and the
		// orphan check at once. That is a hard delete with no trash entry, no
		// link accounting and no restore, reachable through an operation named
		// "move". Out of one, it is a restore that skips every check the real
		// restore path performs.
		//
		// This was previously masked by the incidental "destination directory
		// does not exist" refusal — which disappears the moment trash exists,
		// because trash lives at .omnipus-vault/trash/. CreateNote has always
		// refused these paths via authorRefuseReserved; the move path simply
		// never called it.
		if rerr := authorRefuseReserved(side.p); rerr != nil {
			return nil, fmt.Errorf("%s: %w", side.label, rerr)
		}
	}

	// FR-043 on both ends, on the real path, before anything else is decided.
	if _, fromErr := r.Root.ResolveContained(fsys, from); fromErr != nil {
		return nil, fmt.Errorf("%w: from: %v", ErrRenameInvalidPath, fromErr)
	}
	if _, toErr := r.Root.ResolveContained(fsys, to); toErr != nil {
		return nil, fmt.Errorf("%w: to: %v", ErrRenameInvalidPath, toErr)
	}

	// FR-044 on both ends, asked as a SEPARATE question from FR-043 above and
	// answered by the one implementation of it (contain.go).
	//
	// It cannot be folded into the containment call, and it cannot be deferred
	// to the lstat below. ResolveContained hands back the path with every
	// symlink already dereferenced, so an lstat of THAT can never observe that
	// a link was traversed: a link with a real note at the far end reports
	// "regular file" every time. Comparing the resolved path against the
	// lexical one is the only thing that detects it, and that comparison lives
	// in ResolveContainedNoSymlink.
	//
	// BOTH ENDS, because the two sides were protected by different amounts of
	// nothing:
	//
	//   - Source. The lstat/IsRegular check below used to carry a comment
	//     claiming it refused symlinks. It could not, for the reason above.
	//     A leaf symlink was in fact refused — but by the walk-membership
	//     backstop further down (a symlink is never in graph.Files()), which
	//     is an accident of ordering, not a guard. The test written for it
	//     passed on that backstop.
	//
	//   - Destination. There is no membership backstop on this side: a
	//     destination does not exist yet, so nothing can look for it in the
	//     walk. `move new_folder="Inbox/Sub"` with Inbox a symlink to Archive/
	//     was ACCEPTED, moved the note to Archive/Sub/, audited it as
	//     Inbox/Sub/ — and vault_read then refused to open the path the agent
	//     had just been told the note now had. That is the c06bb051 class of
	//     defect, on a path that also DELETES the note's old name.
	fromReal, err := r.Root.ResolveContainedNoSymlink(fsys, from)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrRenameSourceNotAddressable, from, err)
	}
	if _, toErr := r.Root.ResolveContainedNoSymlink(fsys, to); toErr != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrRenameDestinationNotAddressable, to, toErr)
	}

	if from == to {
		// Exactly the same path. Nothing to move, nothing to rewrite, and
		// nothing written — in particular no journal, because a journal whose
		// From equals its To could never be classified by MoveStateOf.
		return &RenamePlan{NoOp: true}, nil
	}
	caseOnly := strings.EqualFold(from, to)

	fromInfo, err := fsys.Lstat(fromReal)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrRenameSourceMissing, from, err)
	}
	if !fromInfo.Mode().IsRegular() {
		// A directory, device, fifo or socket. NOT a symlink — FR-044 is
		// enforced above, by ResolveContainedNoSymlink, and it has to be:
		// this comment used to say "a symlink or an irregular file", which was
		// false for the symlink half. fromReal came from ResolveContained,
		// with every link already followed, so this lstat was of the LINK'S
		// TARGET and reported a regular file for every symlink that pointed at
		// one. The check could only ever fire for a DANGLING link.
		//
		// It is a live check now — fromReal is the path the caller named —
		// which is why it is kept rather than deleted.
		return nil, fmt.Errorf("%w: %q is %s", ErrRenameSourceNotAddressable, from, fromInfo.Mode().String())
	}

	if destErr := r.checkDestination(fsys, to, caseOnly, fromInfo); destErr != nil {
		return nil, destErr
	}

	// Discovery reuses the graph. Nothing below re-parses a note.
	graph, err := BuildLinkGraph(fsys, r.Root)
	if err != nil {
		return nil, err
	}
	files := graph.Files()
	if !sliceHasString(files, from) {
		return nil, fmt.Errorf("%w: %q", ErrRenameSourceNotAddressable, from)
	}

	ambiguity := ambiguityAfterRename(files, from, to)
	if ambiguity != nil && !ambiguity.WasAmbiguous && !req.AllowAmbiguity {
		return nil, &AmbiguityError{Report: *ambiguity}
	}
	qualify := ambiguity != nil

	plan := &RenamePlan{CaseOnly: caseOnly, Ambiguity: ambiguity}
	// FR-112, applied to a write rather than a read: a note this graph could
	// not scan might hold the very citation this rename needs to rewrite (or
	// might itself be cited by the subject), and there is no way to tell from
	// here. Reporting it is what stands between "found nothing to rewrite in
	// it" and "could not tell whether there was anything to rewrite in it" —
	// the two read identically in LinksRewritten and must not read identically
	// in the plan.
	plan.Skipped = graph.Skipped()
	plan.Incomplete = len(plan.Skipped) > 0

	j := &Journal{
		Version:   journalVersion,
		Op:        JournalOpRename,
		Root:      r.Root.Path(),
		From:      from,
		To:        to,
		CaseOnly:  caseOnly,
		CreatedAt: time.Now().UTC(),
	}
	if j.ID, err = newJournalID(); err != nil {
		return nil, err
	}

	dirChanged := path.Dir(from) != path.Dir(to)
	for _, note := range graph.Notes() {
		noteDir := path.Dir(note)
		if note == from {
			noteDir = path.Dir(to)
		}
		if noteDir == "." {
			noteDir = ""
		}

		var edits []LinkEdit
		for _, rl := range graph.Links(note) {
			if rl.State != ResolveResolved {
				continue
			}
			pointsAtSubject := rl.To == from
			// A markdown link is spelled relative to the note that holds it,
			// so moving THAT note to another folder changes the correct
			// spelling of every markdown link in it, even the ones pointing
			// somewhere else entirely. Wikilinks are collection-relative and
			// are unaffected by the move.
			respellForMove := note == from && dirChanged && rl.Kind == LinkMarkdown
			if !pointsAtSubject && !respellForMove {
				continue
			}
			target := rl.To
			if pointsAtSubject {
				target = to
			}
			newRaw, ok := rewriteLinkRaw(rl, target, noteDir, qualify)
			if !ok || newRaw == rl.Raw {
				continue
			}
			edits = append(edits, LinkEdit{Offset: rl.Offset, Old: rl.Raw, New: newRaw})
		}
		if len(edits) == 0 {
			continue
		}
		sort.Slice(edits, func(i, k int) bool { return edits[i].Offset < edits[k].Offset })

		step, err := r.buildStep(fsys, note, note == from, edits)
		if err != nil {
			return nil, err
		}
		if step == nil {
			continue
		}
		j.Steps = append(j.Steps, *step)
		plan.LinksRewritten += len(edits)
	}
	sort.Slice(j.Steps, func(i, k int) bool { return j.Steps[i].RelPath < j.Steps[k].RelPath })

	if err := j.Validate(); err != nil {
		return nil, err
	}
	plan.Journal = j
	return plan, nil
}

// checkDestination refuses a destination that is occupied by a different file,
// or whose parent directory does not exist.
//
// The case-only branch is the trap pkg/library documents at length: on a
// case-insensitive filesystem an existence check for "note.md" SUCCEEDS by
// folding onto the existing "Note.md", and is indistinguishable by return value
// from a genuine collision. Treating that success as "the destination is taken"
// rejects a legitimate relabel; treating it as "free" would let a rename
// silently delete a different file on a case-sensitive one. The only correct
// question is whether the entry found IS the file being renamed, which
// os.SameFile answers from the inode rather than from the name.
func (r *Renamer) checkDestination(fsys LinkFS, to string, caseOnly bool, fromInfo fs.FileInfo) error {
	toLex := lexicalJoin(r.Root, to)

	parent := filepath.Dir(toLex)
	if pi, err := fsys.Lstat(parent); err != nil || !pi.IsDir() {
		return fmt.Errorf("%w: %q", ErrRenameDestinationParentMissing, path.Dir(to))
	}

	name, found, err := directoryEntryName(parent, path.Base(to))
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	// Identity is decided by inode, not by name — the only question a
	// case-insensitive filesystem answers honestly. The caseOnly conjunct is
	// what keeps a HARD LINK from qualifying: two genuinely different names
	// for one inode are a real collision, and renaming one onto the other
	// destroys a name the operator created.
	occupant, statErr := fsys.Lstat(filepath.Join(parent, name))
	if caseOnly && statErr == nil && os.SameFile(fromInfo, occupant) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrRenameDestinationExists, to)
}

// buildStep reads the note, verifies every edit against the bytes actually
// there, and records the before and after hashes.
//
// The verification happens HERE, at plan time, so a plan that could not be
// applied is never written to disk in the first place.
func (r *Renamer) buildStep(fsys LinkFS, note string, subject bool, edits []LinkEdit) (*JournalStep, error) {
	// The same two guards the rest of the write path uses, on a path that
	// happens to arrive from the walk today. Relying on the CALLER's guarantee
	// is what left every other site in this class open: the walk never yields
	// a symlinked path, so ResolveContained looked adequate here right up
	// until someone plans a step for a path that came from somewhere else.
	abs, err := r.Root.ResolveContainedNoSymlink(fsys, note)
	if err != nil {
		return nil, fmt.Errorf("knowledge: rewrite %q: %w", note, err)
	}
	// ReadNoteContent, not a bare Open plus read: a dematerialised note (iCloud
	// Drive, OneDrive Files-On-Demand, an rclone VFS) stats at its real size
	// and reads back nothing on a clean EOF, and a plan computed from those
	// zero bytes reports "edit does not match a 0-byte file" — blaming the
	// plan for the filesystem. FR-111's classification names the real cause.
	src, err := ReadNoteContent(fsys, abs)
	if err != nil {
		return nil, fmt.Errorf("knowledge: read %q: %w", note, err)
	}
	updated, err := applyEdits(src, edits)
	if err != nil {
		return nil, fmt.Errorf("knowledge: plan rewrite of %q: %w", note, err)
	}
	before := hashBytes(src)
	after := hashBytes(updated)
	if before == after {
		return nil, nil
	}
	return &JournalStep{
		RelPath:    note,
		Subject:    subject,
		BeforeHash: before,
		AfterHash:  after,
		Edits:      edits,
	}, nil
}

// ambiguityAfterRename reports whether the destination's bare link name would
// match more than one note once the rename has happened.
//
// Both sides are computed with the SAME index the resolver uses, so "ambiguous"
// here means exactly what it means at resolution time (FR-041) rather than
// something a second, approximate rule decided.
func ambiguityAfterRename(files []string, from, to string) *AmbiguityReport {
	post := make([]string, 0, len(files))
	for _, f := range files {
		if f == from {
			post = append(post, to)
			continue
		}
		post = append(post, f)
	}
	key := bareLinkKey(to)
	preMatches := NewNoteIndex(files).byBase[key]
	postMatches := NewNoteIndex(post).byBase[key]
	if len(postMatches) <= 1 {
		return nil
	}
	return &AmbiguityReport{
		Basename:     key,
		Candidates:   append([]string(nil), postMatches...),
		WasAmbiguous: len(preMatches) > 1,
	}
}

// bareLinkKey is the name a bare "[[Name]]" would use for a path: the basename,
// without the markdown extension when there is one.
func bareLinkKey(rel string) string {
	base := path.Base(rel)
	if IsMarkdownPath(rel) {
		return trimMarkdownExt(base)
	}
	return base
}

// rewriteLinkRaw produces the replacement source text for one link.
//
// It rewrites INSIDE the original raw text rather than reconstructing the link
// from its parsed parts, so an alias, an anchor, a block reference, an embed
// marker, a link title and even the whitespace someone typed inside the
// brackets all survive untouched. Reconstruction would normalise all of that,
// and a rename that reformats links it did not need to change is a rename that
// produces a large, unreviewable diff over the operator's whole collection.
func rewriteLinkRaw(rl ResolvedLink, targetRel, noteDir string, qualify bool) (string, bool) {
	if rl.Kind == LinkWikilink {
		return rewriteWikilinkRaw(rl.Raw, wikiTargetSpelling(rl.Target, targetRel, qualify))
	}
	return rewriteMarkdownRaw(rl.Raw, targetRel, noteDir)
}

// wikiTargetSpelling chooses how the new target is written inside a wikilink,
// preserving the shape the author used.
//
// Two shapes are preserved and one is imposed. Preserved: whether the link was
// a bare name or a collection-relative path, and whether it carried the ".md"
// extension. Imposed: when the new basename would be ambiguous, the link is
// written as a full path even if the original was bare — because a bare link
// would otherwise silently resolve by tie-break to whichever note sorts first,
// which may not be the one the rename just produced.
func wikiTargetSpelling(oldTarget, newRel string, qualify bool) string {
	hadSlash := strings.Contains(oldTarget, "/")
	hadMarkdownExt := trimMarkdownExt(oldTarget) != oldTarget

	name := newRel
	if !hadSlash && !qualify {
		name = path.Base(newRel)
	}
	if IsMarkdownPath(name) && !hadMarkdownExt {
		name = trimMarkdownExt(name)
	}
	return name
}

// rewriteWikilinkRaw replaces only the target portion of "[[target#anchor|alias]]".
func rewriteWikilinkRaw(raw, newTarget string) (string, bool) {
	open := strings.Index(raw, "[[")
	closeIdx := strings.LastIndex(raw, "]]")
	if open < 0 || closeIdx < open+2 {
		return "", false
	}
	inner := raw[open+2 : closeIdx]
	end := len(inner)
	if i := strings.IndexAny(inner, "#|"); i >= 0 {
		end = i
	}
	head := inner[:end]
	if strings.TrimSpace(head) == "" {
		return "", false
	}
	lead := head[:len(head)-len(strings.TrimLeft(head, " \t"))]
	trail := head[len(strings.TrimRight(head, " \t")):]
	return raw[:open+2] + lead + newTarget + trail + inner[end:] + raw[closeIdx:], true
}

// rewriteMarkdownRaw replaces only the path portion of "[text](dest#anchor "title")".
func rewriteMarkdownRaw(raw, targetRel, noteDir string) (string, bool) {
	head, dest, ok := splitMarkdownRaw(raw)
	if !ok {
		return "", false
	}
	newDest, ok := rewriteDestination(dest, targetRel, noteDir)
	if !ok {
		return "", false
	}
	return head + newDest + ")", true
}

// splitMarkdownRaw cuts "[text](dest)" into everything up to and including the
// opening parenthesis, and the destination between the parentheses.
func splitMarkdownRaw(raw string) (head, dest string, ok bool) {
	i := 0
	if strings.HasPrefix(raw, "!") {
		i = 1
	}
	if i >= len(raw) || raw[i] != '[' {
		return "", "", false
	}
	depth := 0
	textEnd := -1
	for k := i; k < len(raw); k++ {
		switch raw[k] {
		case '\\':
			k++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				textEnd = k
			}
		}
		if textEnd >= 0 {
			break
		}
	}
	if textEnd < 0 || textEnd+1 >= len(raw) || raw[textEnd+1] != '(' {
		return "", "", false
	}
	if raw[len(raw)-1] != ')' {
		return "", "", false
	}
	return raw[:textEnd+2], raw[textEnd+2 : len(raw)-1], true
}

// rewriteDestination replaces the path inside a markdown destination, keeping
// its anchor, its optional title and its angle-bracket form.
func rewriteDestination(dest, targetRel, noteDir string) (string, bool) {
	lead := dest[:len(dest)-len(strings.TrimLeft(dest, " \t"))]
	body := dest[len(lead):]

	if strings.HasPrefix(body, "<") {
		end := strings.Index(body, ">")
		if end < 0 {
			return "", false
		}
		oldPath, anchor := splitAnchor(body[1:end])
		newPath := destinationPath(oldPath, targetRel, noteDir)
		// Inside angle brackets nothing needs escaping except ">" itself,
		// which cannot appear in a path this package would produce.
		return lead + "<" + newPath + anchor + ">" + body[end+1:], true
	}

	cut := len(body)
	if sp := strings.IndexAny(body, " \t"); sp >= 0 {
		cut = sp
	}
	oldPath, anchor := splitAnchor(body[:cut])
	rest := body[cut:]
	newPath := destinationPath(oldPath, targetRel, noteDir)
	return lead + encodeDestinationPath(newPath, oldPath) + anchor + rest, true
}

// splitAnchor separates "path#anchor" into its two halves, keeping the "#" with
// the anchor so an empty anchor round-trips as nothing.
func splitAnchor(s string) (pathPart, anchor string) {
	if i := strings.Index(s, "#"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}

// destinationPath computes the new relative spelling of a markdown link's
// target from the directory the containing note will be in, preserving whether
// the original carried the markdown extension.
func destinationPath(oldSpelling, targetRel, noteDir string) string {
	decoded := oldSpelling
	if d, err := url.PathUnescape(oldSpelling); err == nil {
		decoded = d
	}
	hadMarkdownExt := trimMarkdownExt(decoded) != decoded

	rel := relFromDir(noteDir, targetRel)
	if IsMarkdownPath(rel) && !hadMarkdownExt {
		rel = trimMarkdownExt(rel)
	}
	return rel
}

// relFromDir spells target relative to baseDir, both collection-relative and
// slash-separated. Pure string arithmetic: it never touches the filesystem, so
// it cannot be misled by a symlink and gives the same answer on every platform.
func relFromDir(baseDir, target string) string {
	baseDir = strings.Trim(baseDir, "/")
	if baseDir == "" || baseDir == "." {
		return target
	}
	b := strings.Split(baseDir, "/")
	t := strings.Split(target, "/")
	i := 0
	for i < len(b) && i < len(t)-1 && b[i] == t[i] {
		i++
	}
	parts := make([]string, 0, len(b)-i+len(t)-i)
	for k := i; k < len(b); k++ {
		parts = append(parts, "..")
	}
	parts = append(parts, t[i:]...)
	return strings.Join(parts, "/")
}

// encodeDestinationPath percent-encodes the new path when it must be encoded to
// stay a valid markdown destination, or when the original was encoded.
//
// The set is deliberately minimal and fixed: only the characters that would
// otherwise terminate or reshape the destination, plus "%" itself so an
// encoding pass is never ambiguous. Everything else — including non-ASCII — is
// left alone, because a rename that mangles a note's name into percent noise is
// a rename an operator cannot read.
func encodeDestinationPath(newPath, oldSpelling string) string {
	mustEncode := strings.ContainsAny(newPath, " ()<>")
	if !mustEncode {
		if decoded, err := url.PathUnescape(oldSpelling); err == nil && decoded != oldSpelling {
			mustEncode = true
		}
	}
	if !mustEncode {
		return newPath
	}
	var b strings.Builder
	for i := 0; i < len(newPath); i++ {
		switch c := newPath[i]; c {
		case ' ':
			b.WriteString("%20")
		case '(':
			b.WriteString("%28")
		case ')':
			b.WriteString("%29")
		case '<':
			b.WriteString("%3C")
		case '>':
			b.WriteString("%3E")
		case '%':
			b.WriteString("%25")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func sliceHasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func sortedUnique(items ...string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return dedupeStrings(out)
}
