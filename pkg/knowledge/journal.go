// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// journal.go — the write journal that makes an interrupted rename recoverable
// (ADR-067 D10, D14; FR-103, FR-104, FR-105, FR-090; US-13 AS-4; H-4).
//
// # The problem this file exists for
//
// Renaming one note rewrites N other files. There is no filesystem primitive
// that renames a file and rewrites N others atomically, and the ADR is explicit
// that the guarantee is "journalled and crash-recoverable, not atomic" (D10).
// So the recovery has to be designed, not hoped for: the full set of intended
// changes is written down BEFORE the first byte of the operator's collection is
// touched, and what is written down is enough to finish the job from a cold
// start with no memory of what was in flight.
//
// # Forward-only. The journal completes, it never rolls back.
//
// The decision, and the reasons, in the order they mattered:
//
//  1. THE OPERATOR ASKED FOR THE RENAME. Rolling back turns a crash into a
//     silently refused instruction. Completing turns it into a slow one.
//
//  2. ROLLBACK RACES THE OTHER WRITERS. D14 tier 3 names four writers Omnipus
//     cannot lock out — Obsidian, Syncthing, git, editors. By the time a
//     recovery runs, the moved file and some of the rewrites may already have
//     replicated to a sync peer. Un-moving the file then fights replication;
//     finishing the move agrees with it.
//
//  3. FORWARD IS IDEMPOTENT AND CONVERGENT, ROLLBACK IS NOT. Every step here
//     records the hash of the file before AND after its own edit, so a step can
//     be classified from disk alone — already applied, not yet applied, or
//     changed by somebody else. Re-running a completed recovery is a no-op.
//     A rollback would need a second, inverse plan with the same property, and
//     the inverse of "rewrite a link" is only well-defined while nothing else
//     has edited that file.
//
//  4. THE INTERRUPTED STATE IS THE ONE OPERATORS ALREADY KNOW. The move is
//     performed FIRST and is the single atomic step in the whole operation, so
//     a crash leaves "file at its new name, some links still pointing at the
//     old one" — exactly the state Obsidian leaves behind when a note is
//     renamed outside it (D10 accepts that limitation explicitly), and exactly
//     what the graph's existing unresolved-link report already surfaces
//     (US-13 AS-5). It is broken in a legible, listable way, not a novel one.
//
// # Why disk is the only progress record
//
// A journal that also carried "steps 1..7 done" would be a SECOND source of
// truth about the collection, and the two can disagree — the process can die
// between renaming the file and recording that it did. Every question this
// file asks is therefore asked of the filesystem: is the file at its old name
// or its new one; does this note hash as before-the-edit or after-it. The
// journal carries only the plan, never the progress.
//
// The journal FILE's existence is the one bit of progress state, and it is
// deliberately a single bit: a journal on disk means an operation is
// incomplete. It is written before anything is touched and deleted only after
// every step verifies as applied.
//
// # Why edits are byte offsets and replacement text
//
// FR-105 requires that a note's frontmatter survive a rewrite with only the
// link value changed, byte-compared, and frontmatter in the reference
// collection carries comments, anchors and nested lists. Any implementation
// that PARSES the YAML and re-serialises it will lose some of that — comment
// placement and anchor names are not part of a YAML value. So nothing here
// parses YAML at all. A step is a list of (offset, exact old text, new text)
// triples over the raw bytes, applied by splicing. Everything outside the
// spliced spans is copied verbatim, which makes byte-stability a property of
// the mechanism rather than an assertion about a serialiser.
//
// It also makes replay independent of the collection: reproducing an edit
// needs no re-resolution, no note index, and no assumption that the rest of the
// collection still looks the way it did when the plan was made.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// journalVersion is bumped when the recorded shape changes in a way that makes
// an older record unusable. A journal of an unrecognised version is NOT
// discarded the way a stale manifest is — a manifest costs a rebuild, a journal
// describes half-applied edits to the operator's own files — so it is reported
// as unreadable and left on disk for a human.
const journalVersion = 1

// JournalDirName is the directory, inside the collection's own marker
// directory, that holds pending journals.
//
// Inside the collection, deliberately. The journal describes the operator's
// files and must survive the index being deleted (E-15), the collection being
// moved to another machine, or Omnipus being reinstalled. `.omnipus-vault/` is
// already excluded from both walkers (scanSkippedDirNames), so nothing in here
// can become an indexed note, a link target or a backlink.
const JournalDirName = "journal"

// journalFileSuffix is the extension of a journal record.
const journalFileSuffix = ".journal.json"

// JournalOp names the kind of operation a journal describes. There is one
// today; the field exists so a later operation cannot be mistaken for a rename
// by a recovery that predates it.
type JournalOp string

// JournalOpRename is a rename or move of one file, with its inbound links.
const JournalOpRename JournalOp = "rename"

// Errors from the journal layer. Each is a sentinel because the caller's
// correct response differs: a mismatch means somebody else edited the file and
// a human must look, an indeterminate move means the collection is in a state
// this code refuses to guess about, and an unreadable journal means recovery
// cannot even be attempted.
var (
	// ErrJournalInvalid means a journal record is missing required fields, is
	// of an unrecognised version, or is not internally consistent.
	ErrJournalInvalid = errors.New("knowledge: invalid write journal")

	// ErrJournalEditMismatch means the bytes at an edit's offset are not the
	// bytes the plan recorded. The file was changed by somebody else; nothing
	// is written.
	ErrJournalEditMismatch = errors.New("knowledge: journal edit does not match file contents")

	// ErrJournalIndeterminate means the rename's source and destination are
	// both present, or both absent, so recovery cannot tell whether the move
	// happened. Refused loudly rather than guessed.
	ErrJournalIndeterminate = errors.New("knowledge: rename state is indeterminate")

	// ErrJournalIncomplete means a recovery ran but could not finish, because
	// at least one file no longer matches its recorded before- or after-state.
	// The journal is retained.
	ErrJournalIncomplete = errors.New("knowledge: write journal could not be completed")
)

// LinkEdit is one splice: replace the exact bytes Old, found at Offset, with
// New.
//
// Old is stored rather than merely a length so that applying an edit is
// self-verifying. An offset alone would happily overwrite whatever had moved
// into that position.
type LinkEdit struct {
	// Offset is the absolute byte offset of Old within the file.
	Offset int64 `json:"offset"`
	// Old is the exact source text being replaced, brackets included.
	Old string `json:"old"`
	// New is the text to put in its place.
	New string `json:"new"`
}

// JournalStep is every edit to one file, together with what that file must hash
// to before and after them.
//
// The two hashes are what make a step classifiable from disk with no other
// context: matching BeforeHash means "not yet applied", matching AfterHash
// means "already applied", matching neither means "somebody else got here".
type JournalStep struct {
	// RelPath is the file's collection-relative path AS OF THE PLAN.
	RelPath string `json:"rel_path"`
	// Subject marks the step that edits the file BEING RENAMED. Its location
	// depends on whether the move has happened yet, so recovery resolves it
	// against the journal's From/To rather than against RelPath.
	Subject bool `json:"subject,omitempty"`
	// BeforeHash is the hex SHA-256 of the file's contents before the edits.
	BeforeHash string `json:"before_hash"`
	// AfterHash is the hex SHA-256 of the file's contents after them.
	AfterHash string `json:"after_hash"`
	// Edits are the splices, sorted by ascending Offset and non-overlapping.
	Edits []LinkEdit `json:"edits"`
}

// Journal is one planned, not-yet-known-complete operation.
type Journal struct {
	// Version is journalVersion.
	Version int `json:"version"`
	// ID is a unique, sortable-by-creation identifier.
	ID string `json:"id"`
	// Op is what kind of operation this is.
	Op JournalOp `json:"op"`
	// Root is the collection root's resolved real path, recorded so a journal
	// found under the wrong collection is detectable rather than applied.
	Root string `json:"root"`
	// From is the collection-relative path being renamed away from.
	From string `json:"from"`
	// To is the collection-relative path being renamed to.
	To string `json:"to"`
	// CaseOnly marks a rename that differs from its source only in letter
	// case. On a case-insensitive filesystem both spellings name the same
	// file, so the ordinary "does the destination exist yet" test cannot
	// decide whether the move has happened, and a directory listing is
	// consulted instead.
	CaseOnly bool `json:"case_only,omitempty"`
	// CreatedAt is when the plan was written.
	CreatedAt time.Time `json:"created_at"`
	// Steps are the per-file rewrites, sorted by RelPath.
	Steps []JournalStep `json:"steps"`
}

// Validate reports whether a journal is usable at all.
//
// Called on write AND on load: a journal that arrives corrupt from disk must
// not be half-applied, and one that is inconsistent in memory must never reach
// disk in the first place.
func (j *Journal) Validate() error {
	if j == nil {
		return fmt.Errorf("%w: nil journal", ErrJournalInvalid)
	}
	if j.Version != journalVersion {
		return fmt.Errorf("%w: version %d is not %d", ErrJournalInvalid, j.Version, journalVersion)
	}
	if strings.TrimSpace(j.ID) == "" {
		return fmt.Errorf("%w: empty id", ErrJournalInvalid)
	}
	if strings.ContainsAny(j.ID, "/\\.") {
		// The ID becomes a filename. A separator or a dot in it would let a
		// journal name a path outside the journal directory.
		return fmt.Errorf("%w: id %q contains a path separator", ErrJournalInvalid, j.ID)
	}
	if j.Op != JournalOpRename {
		return fmt.Errorf("%w: unknown op %q", ErrJournalInvalid, j.Op)
	}
	if strings.TrimSpace(j.Root) == "" {
		return fmt.Errorf("%w: empty root", ErrJournalInvalid)
	}
	if j.From == "" || j.To == "" {
		return fmt.Errorf("%w: empty from/to", ErrJournalInvalid)
	}
	if j.From == j.To {
		return fmt.Errorf("%w: from and to are identical (%q)", ErrJournalInvalid, j.From)
	}
	seen := make(map[string]struct{}, len(j.Steps))
	subjects := 0
	for _, st := range j.Steps {
		if st.RelPath == "" {
			return fmt.Errorf("%w: step with empty path", ErrJournalInvalid)
		}
		if _, dup := seen[st.RelPath]; dup {
			return fmt.Errorf("%w: two steps for %q", ErrJournalInvalid, st.RelPath)
		}
		seen[st.RelPath] = struct{}{}
		if st.Subject {
			subjects++
		}
		if len(st.Edits) == 0 {
			return fmt.Errorf("%w: step %q has no edits", ErrJournalInvalid, st.RelPath)
		}
		if st.BeforeHash == "" || st.AfterHash == "" {
			return fmt.Errorf("%w: step %q is missing a hash", ErrJournalInvalid, st.RelPath)
		}
		if st.BeforeHash == st.AfterHash {
			return fmt.Errorf("%w: step %q claims edits that change nothing", ErrJournalInvalid, st.RelPath)
		}
		var prevEnd int64
		for i, e := range st.Edits {
			if e.Old == "" {
				return fmt.Errorf("%w: step %q edit %d replaces nothing", ErrJournalInvalid, st.RelPath, i)
			}
			if e.Offset < prevEnd {
				return fmt.Errorf("%w: step %q edit %d overlaps or is out of order", ErrJournalInvalid, st.RelPath, i)
			}
			prevEnd = e.Offset + int64(len(e.Old))
		}
	}
	if subjects > 1 {
		return fmt.Errorf("%w: %d subject steps", ErrJournalInvalid, subjects)
	}
	return nil
}

// stepPath returns where a step's file lives right now, given whether the move
// has already happened. Only the subject step moves; everything else is where
// the plan said it was.
func (j *Journal) stepPath(st JournalStep, moved bool) string {
	if st.Subject && moved {
		return j.To
	}
	return st.RelPath
}

// Paths returns every collection-relative path this journal touches, sorted and
// de-duplicated: the source, the destination, and every rewritten file.
//
// This is the set FR-090 / US-15 AS-2 require an audit record to carry — "the
// full set of touched paths, not just the renamed note".
func (j *Journal) Paths() []string {
	set := map[string]struct{}{j.From: {}, j.To: {}}
	for _, st := range j.Steps {
		set[st.RelPath] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// JournalStore is the on-disk set of pending journals for one collection.
type JournalStore struct {
	dir string

	// Lock configures D14 tier-1 mutual exclusion for every file this store
	// rewrites while recovering a journal.
	//
	// It is not decoration. A link rewrite is a read-modify-write over the
	// operator's notes, exactly like an edit, and the two used to share no
	// lock at all: an EditNote landing between ApplyStep's read and its write
	// was overwritten by content derived from the pre-edit read, and its
	// caller had already been told it succeeded. The content-hash
	// precondition narrows that window; it does not close it, because the
	// hash is checked before the write rather than atomically with it.
	//
	// CollectionRoot is filled in from the journal's own root at recovery
	// time, so a store configured once is correct for whichever collection it
	// is asked to recover.
	Lock NoteLockConfig
}

// DefaultJournalDir is where a collection's journals live: inside its own
// marker directory. See JournalDirName for why inside.
func DefaultJournalDir(collectionRoot string) string {
	return filepath.Join(MarkerDir(collectionRoot), JournalDirName)
}

// NewJournalStore returns a store over dir. The directory is created lazily,
// 0700, on the first write — a collection with no pending operation carries no
// journal directory at all.
func NewJournalStore(dir string) *JournalStore { return &JournalStore{dir: dir} }

// Dir returns the store's directory.
func (s *JournalStore) Dir() string { return s.dir }

// path is where one journal's record lives.
func (s *JournalStore) path(id string) string {
	return filepath.Join(s.dir, id+journalFileSuffix)
}

// newJournalID returns a lexicographically-sortable, collision-resistant id:
// a UTC timestamp to nanosecond precision followed by 8 random bytes. The
// timestamp makes `List` return journals oldest-first without reading them;
// the random tail makes two journals created in the same nanosecond distinct.
func newJournalID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fail closed. An id that is not unique would let one journal
		// overwrite another's record, which loses a plan for the operator's
		// files — worse than refusing to start the operation.
		return "", fmt.Errorf("knowledge: journal id entropy: %w", err)
	}
	// No dots: the id becomes a filename with journalFileSuffix appended, and
	// Validate refuses a dot outright so that no id can ever spell "..".
	now := time.Now().UTC()
	return fmt.Sprintf("%s%09dZ-%s", now.Format("20060102T150405"), now.Nanosecond(), hex.EncodeToString(b[:])), nil
}

// Write persists a journal. It MUST be called before any file the journal
// describes is touched, and it fsyncs before returning — a journal still in the
// page cache when the power goes is a journal that never existed.
func (s *JournalStore) Write(j *Journal) error {
	if err := j.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("knowledge: create journal dir: %w", err)
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("knowledge: encode journal: %w", err)
	}
	data = append(data, '\n')
	if err := fileutil.WriteFileAtomic(s.path(j.ID), data, 0o600); err != nil {
		return fmt.Errorf("knowledge: write journal: %w", err)
	}
	return nil
}

// Load reads one journal by id.
func (s *JournalStore) Load(id string) (*Journal, error) {
	if strings.ContainsAny(id, "/\\") || id == "" || strings.Contains(id, "..") {
		return nil, fmt.Errorf("%w: id %q", ErrJournalInvalid, id)
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrJournalInvalid, id, err)
	}
	if err := j.Validate(); err != nil {
		return nil, err
	}
	return &j, nil
}

// List returns every pending journal, oldest first.
//
// A record that will not parse is NOT skipped: it is returned as an error
// alongside the ones that did, because a journal nobody can read still means
// the collection may be half-rewritten, and that must reach a human rather than
// be counted as "no pending work".
func (s *JournalStore) List() ([]*Journal, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("knowledge: list journals: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), journalFileSuffix) {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), journalFileSuffix))
	}
	sort.Strings(names)

	var out []*Journal
	var problems []string
	for _, id := range names {
		j, loadErr := s.Load(id)
		if loadErr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", id, loadErr))
			continue
		}
		out = append(out, j)
	}
	if len(problems) > 0 {
		return out, fmt.Errorf("%w: unreadable journal(s): %s", ErrJournalInvalid, strings.Join(problems, "; "))
	}
	return out, nil
}

// Delete removes a journal record. Called only once every step verifies as
// applied.
func (s *JournalStore) Delete(id string) error {
	err := os.Remove(s.path(id))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("knowledge: delete journal: %w", err)
	}
	return nil
}

// StepOutcome is what happened to one step.
type StepOutcome string

const (
	// StepApplied — the file matched BeforeHash and was rewritten.
	StepApplied StepOutcome = "applied"
	// StepAlreadyApplied — the file already matched AfterHash. A previous run
	// got this far; nothing was written.
	StepAlreadyApplied StepOutcome = "already_applied"
	// StepConflict — the file matched neither hash, or an edit's recorded old
	// text was not there. Somebody else changed it. Nothing was written.
	StepConflict StepOutcome = "conflict"
	// StepMissing — the file is gone. Nothing was written.
	StepMissing StepOutcome = "missing"
)

// StepResult is the outcome of one step, with the path it was applied at (which
// differs from the step's recorded path for the subject of a move).
type StepResult struct {
	// RelPath is where the file was looked for.
	RelPath string
	// Outcome is what happened.
	Outcome StepOutcome
	// Detail explains a conflict or a missing file.
	Detail string
}

// applyEdits splices a step's edits into src.
//
// Every edit is verified against the bytes actually present before anything is
// produced, and the whole result is built or none of it is. Everything outside
// the spliced spans is copied byte-for-byte, which is what makes FR-105's
// frontmatter guarantee structural: the rewriter cannot damage what it never
// re-serialises.
func applyEdits(src []byte, edits []LinkEdit) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(len(src))
	var prev int64
	for i, e := range edits {
		end := e.Offset + int64(len(e.Old))
		if e.Offset < prev {
			return nil, fmt.Errorf("%w: edit %d at offset %d overlaps the previous edit", ErrJournalInvalid, i, e.Offset)
		}
		if e.Offset < 0 || end > int64(len(src)) {
			return nil, fmt.Errorf("%w: edit %d spans [%d,%d) of a %d-byte file", ErrJournalEditMismatch, i, e.Offset, end, len(src))
		}
		if string(src[e.Offset:end]) != e.Old {
			return nil, fmt.Errorf("%w: edit %d at offset %d expected %q, found %q",
				ErrJournalEditMismatch, i, e.Offset, e.Old, string(src[e.Offset:end]))
		}
		out.Write(src[prev:e.Offset])
		out.WriteString(e.New)
		prev = end
	}
	out.Write(src[prev:])
	return out.Bytes(), nil
}

// hashBytes is the hex SHA-256 used for every before/after comparison. It is
// the same primitive the manifest uses for change detection (ManifestEntry.Hash)
// and the same one D14 names as the version token's basis: content, never
// mtime, because Syncthing preserves source mtimes and several filesystems have
// one-second granularity, so a sub-second external write is invisible to mtime.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ApplyStep applies one journal step at relPath, or reports why it did not.
//
// It is the unit both a first run and a recovery loop over — deliberately the
// same function, so the recovery path cannot drift away from the path that
// normally runs and only be exercised by a test.
//
// Containment (FR-043) is re-checked here, on the REAL path, for every step. A
// journal is a file on disk, and a file on disk is attacker-controllable text:
// nothing may be written through a path that does not resolve inside the
// collection root, however that path arrived.
func ApplyStep(fsys LinkFS, root CollectionRoot, relPath string, st JournalStep) StepResult {
	res := StepResult{RelPath: relPath}
	if !root.Valid() {
		res.Outcome = StepConflict
		res.Detail = "collection root not initialised"
		return res
	}
	// FR-044, and it has to be asked HERE, of the LEXICAL path, or it is not
	// asked at all. ResolveContained resolves every symlink on the way, so by
	// the time it returns, a step aimed at a symlink has become a step aimed
	// at whatever the symlink points to — and an lstat of that answers
	// "regular file" and writes straight through the link. The guard that used
	// to sit below, on the resolved path, could only ever fire for a DANGLING
	// symlink, and the test written for it was passing on a containment
	// refusal instead. An unreachable guard is worse than no guard; this file
	// already says so about performMove's destination check, one function
	// away.
	lexPath := lexicalJoin(root, relPath)
	if !isWithinOrEqual(root.Path(), lexPath) {
		res.Outcome = StepConflict
		res.Detail = fmt.Errorf("%w: %q", ErrOutsideCollection, relPath).Error()
		return res
	}
	if linfo, lerr := fsys.Lstat(lexPath); lerr == nil && linfo.Mode()&fs.ModeSymlink != 0 {
		res.Outcome = StepConflict
		res.Detail = "path is a symbolic link"
		return res
	}
	realPath, err := root.ResolveContained(fsys, relPath)
	if err != nil {
		res.Outcome = StepConflict
		res.Detail = err.Error()
		return res
	}
	info, err := fsys.Lstat(realPath)
	if err != nil {
		res.Outcome = StepMissing
		res.Detail = err.Error()
		return res
	}
	if !info.Mode().IsRegular() {
		res.Outcome = StepConflict
		res.Detail = "path is not a regular file"
		return res
	}

	src, err := readWholeFile(fsys, realPath)
	if err != nil {
		res.Outcome = StepConflict
		res.Detail = err.Error()
		return res
	}
	switch hashBytes(src) {
	case st.AfterHash:
		res.Outcome = StepAlreadyApplied
		return res
	case st.BeforeHash:
		// fall through and apply
	default:
		res.Outcome = StepConflict
		res.Detail = "file contents match neither the recorded before-state nor the after-state"
		return res
	}

	updated, err := applyEdits(src, st.Edits)
	if err != nil {
		res.Outcome = StepConflict
		res.Detail = err.Error()
		return res
	}
	if got := hashBytes(updated); got != st.AfterHash {
		// Cannot happen for a plan this package produced; asserted anyway
		// because the alternative is writing bytes nobody planned.
		res.Outcome = StepConflict
		res.Detail = fmt.Sprintf("rewritten contents hash %s, plan expected %s", got, st.AfterHash)
		return res
	}
	if err := fileutil.WriteFileAtomic(realPath, updated, info.Mode().Perm()); err != nil {
		res.Outcome = StepConflict
		res.Detail = err.Error()
		return res
	}
	res.Outcome = StepApplied
	return res
}

// readWholeFile reads a file through the LinkFS surface.
func readWholeFile(fsys LinkFS, realPath string) ([]byte, error) {
	f, err := fsys.Open(realPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MoveState is whether the journal's file move has happened yet.
type MoveState string

const (
	// MoveNotDone — the file is still at From.
	MoveNotDone MoveState = "not_done"
	// MoveDone — the file is at To.
	MoveDone MoveState = "done"
	// MoveIndeterminate — both names exist as different files, or neither
	// exists. Recovery refuses rather than guessing.
	MoveIndeterminate MoveState = "indeterminate"
)

// MoveStateOf decides, from the filesystem alone, whether a journal's move has
// happened.
//
// The case-only branch is the one that is easy to get wrong and impossible to
// notice: on a case-insensitive filesystem "Note.md" and "note.md" both stat
// successfully and report the same inode whether or not the relabel has
// happened, so the ordinary exists/missing test answers "done" every time and a
// case-only rename would be silently skipped forever. The directory's own
// spelling of the entry is the only thing that distinguishes the two states.
func MoveStateOf(root CollectionRoot, j *Journal) (MoveState, error) {
	fromLex := lexicalJoin(root, j.From)
	toLex := lexicalJoin(root, j.To)

	if j.CaseOnly {
		name, found, err := directoryEntryName(filepath.Dir(toLex), path.Base(j.To))
		if err != nil {
			return MoveIndeterminate, err
		}
		switch {
		case !found:
			return MoveIndeterminate, nil
		case name == path.Base(j.To):
			return MoveDone, nil
		case name == path.Base(j.From):
			return MoveNotDone, nil
		default:
			return MoveIndeterminate, nil
		}
	}

	_, fromErr := os.Lstat(fromLex)
	_, toErr := os.Lstat(toLex)
	switch {
	case fromErr == nil && toErr != nil:
		// toErr non-nil is the EXPECTED reading here, not a failure: the
		// destination not existing is precisely what "the move has not
		// happened yet" means. Returning it as an error would abort a
		// recoverable rename on its normal starting state.
		return MoveNotDone, nil //nolint:nilerr // see above: toErr is the answer, not a fault
	case fromErr != nil && toErr == nil:
		return MoveDone, nil
	default:
		// Both present (a hard link, a collision, or a case fold this code
		// did not classify) or both absent (the file was deleted under us).
		// Neither is a state to guess about, so neither is guessed about.
		return MoveIndeterminate, nil
	}
}

// directoryEntryName finds the on-disk spelling of an entry whose name folds to
// want. It is how a case-only rename's progress is observed, and it works
// identically on case-sensitive and case-insensitive filesystems.
func directoryEntryName(dir, want string) (string, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, fmt.Errorf("knowledge: read %q: %w", dir, err)
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), want) {
			return e.Name(), true, nil
		}
	}
	return "", false, nil
}

// lexicalJoin builds the path a syscall should use for a collection-relative
// path.
//
// Deliberately NOT the symlink-resolved path, for one reason: a resolved path
// is a DERIVED path, and a destructive syscall must be handed the name the
// caller actually asked for.
//
// HONEST SCOPE, because an earlier revision of this comment claimed more than
// was true. It claimed that on a case-insensitive filesystem EvalSymlinks
// returns the DIRECTORY's spelling, making a case-only rename a silent no-op.
// Measured on macOS APFS: it does not — EvalSymlinks preserves the requested
// letter case. So this is NOT what protects the case-only rename (MoveStateOf's
// case-only branch is, and that one IS covered by a test that dies when it is
// removed).
//
// What remains is a deliberate defensive choice with no test that can
// distinguish it today, stated as such rather than dressed up: a destructive
// syscall is handed the name the caller asked for, not a path derived by
// resolving symlinks and re-appending a missing remainder. The behaviours
// coincide on every case this package can currently reach, because MoveStateOf
// refuses before performMove whenever the destination name exists.
//
// Containment is proven separately, on the real path, by ResolveContained;
// this is only the name the kernel is handed afterwards.
func lexicalJoin(root CollectionRoot, rel string) string {
	return filepath.Join(root.Path(), filepath.FromSlash(rel))
}

// RecoverOutcome is how a recovery ended.
type RecoverOutcome string

const (
	// RecoverCompleted — every step is applied and the journal is deleted.
	RecoverCompleted RecoverOutcome = "completed"
	// RecoverBlocked — at least one step could not be applied. The journal is
	// retained so the operation stays visible and re-runnable.
	RecoverBlocked RecoverOutcome = "blocked"
)

// RecoverResult is the full account of one recovery, and is also what a first
// run returns — the two are the same code path.
type RecoverResult struct {
	// JournalID identifies the operation.
	JournalID string
	// Outcome is completed or blocked.
	Outcome RecoverOutcome
	// MoveState is what the move looked like when recovery started.
	MoveState MoveState
	// MovePerformed reports whether this run did the move itself.
	MovePerformed bool
	// Steps is one result per planned step, in plan order.
	Steps []StepResult
	// Touched is every path this run actually wrote or moved, sorted. It is
	// the audit payload US-15 AS-2 requires.
	Touched []string
	// Conflicts is the subset of Steps that blocked the operation.
	Conflicts []StepResult
}

// Recover carries a journal forward to completion, from any partial state.
//
// It is safe to call repeatedly: a fully applied journal produces
// RecoverCompleted with every step reported as already applied, and deletes the
// record. It is the single apply path — a first rename calls exactly this after
// writing its plan.
func (s *JournalStore) Recover(fsys LinkFS, root CollectionRoot, j *Journal) (*RecoverResult, error) {
	if err := j.Validate(); err != nil {
		return nil, err
	}
	if !root.Valid() {
		return nil, fmt.Errorf("%w: root not initialised", ErrCollectionRootInvalid)
	}
	if root.Path() != j.Root {
		// A journal found under a collection it was not written for must
		// never be applied to it: the paths inside would mean different files.
		return nil, fmt.Errorf("%w: journal %s belongs to %q, not %q", ErrJournalInvalid, j.ID, j.Root, root.Path())
	}

	res := &RecoverResult{JournalID: j.ID, Outcome: RecoverCompleted}

	state, err := MoveStateOf(root, j)
	if err != nil {
		return nil, err
	}
	res.MoveState = state
	lock := s.Lock
	lock.CollectionRoot = root.Path()

	switch state {
	case MoveNotDone:
		// The subject file's own lock, so a concurrent edit of the note being
		// renamed cannot be reading it while it moves out from under them.
		if err := WithNoteWriteLock(lock, j.From, func() error {
			return performMove(fsys, root, j)
		}); err != nil {
			return nil, err
		}
		res.MovePerformed = true
		res.Touched = append(res.Touched, j.From, j.To)
	case MoveDone:
		// Nothing to do; the rewrites still may need finishing.
	default:
		return nil, fmt.Errorf("%w: %q and %q (journal %s)", ErrJournalIndeterminate, j.From, j.To, j.ID)
	}

	for _, st := range j.Steps {
		at := j.stepPath(st, true) // the move is done by this point, by construction
		// One lock per step, taken and released around that file's whole
		// read-compare-write. Never two at once: the steps are independent
		// files and holding them all would be a lock-ordering problem with no
		// benefit.
		var r StepResult
		if lerr := WithNoteWriteLock(lock, at, func() error {
			r = ApplyStep(fsys, root, at, st)
			return nil
		}); lerr != nil {
			r = StepResult{RelPath: at, Outcome: StepConflict, Detail: lerr.Error()}
		}
		res.Steps = append(res.Steps, r)
		switch r.Outcome {
		case StepApplied:
			res.Touched = append(res.Touched, at)
		case StepAlreadyApplied:
			// Already correct on disk; not touched by this run.
		default:
			res.Conflicts = append(res.Conflicts, r)
		}
	}

	sort.Strings(res.Touched)
	res.Touched = dedupeStrings(res.Touched)

	if len(res.Conflicts) > 0 {
		res.Outcome = RecoverBlocked
		return res, fmt.Errorf("%w: journal %s: %d of %d file(s) could not be rewritten",
			ErrJournalIncomplete, j.ID, len(res.Conflicts), len(j.Steps))
	}
	if err := s.Delete(j.ID); err != nil {
		return res, err
	}
	return res, nil
}

// performMove renames the subject file, with containment proven on the real
// path for both ends and the syscall issued against the requested spelling.
func performMove(fsys LinkFS, root CollectionRoot, j *Journal) error {
	if _, err := root.ResolveContained(fsys, j.From); err != nil {
		return fmt.Errorf("knowledge: rename source: %w", err)
	}
	if _, err := root.ResolveContained(fsys, j.To); err != nil {
		return fmt.Errorf("knowledge: rename destination: %w", err)
	}
	fromLex := lexicalJoin(root, j.From)
	toLex := lexicalJoin(root, j.To)
	if !isWithinOrEqual(root.Path(), fromLex) || !isWithinOrEqual(root.Path(), toLex) {
		return fmt.Errorf("%w: %q -> %q", ErrOutsideCollection, j.From, j.To)
	}
	// Deliberately NO "is the destination a symlink" check here. It would be
	// unreachable: MoveStateOf already refuses any journal whose destination
	// NAME exists at all — a symlink included, because it lstats — so
	// performMove only ever runs with the destination absent. An unreachable
	// guard is worse than no guard, because the test written for it passes
	// forever while proving nothing (mutation M15 confirmed exactly that).
	if err := os.Rename(fromLex, toLex); err != nil {
		return fmt.Errorf("knowledge: rename %q -> %q: %w", j.From, j.To, err)
	}
	return nil
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
