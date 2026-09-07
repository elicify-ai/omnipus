// Write safety on the path production actually calls (ADR-067 US-14, D14
// tier 1, AC-14.1; FR-090, FR-106, FR-108).
//
// # Why this file exists separately from version_test.go
//
// version_test.go proves mutual exclusion for knowledge.Writer. Writer had, at
// the time these tests were written, zero production callers: every agent-
// reachable write went through author.EditNote / author.CreateNote, and those
// took no lock at all. The measured consequence was twelve concurrent writers
// all returning success with one surviving on disk — a guarantee tested
// against an object nothing ran, while the object everything ran had neither
// the lock nor a concurrency test.
//
// So the tests here address EditNote and the authoring tools BY NAME. A test
// that proves a property of the write path must exercise the write path the
// caller uses; proving it of a sibling is the same class of error as citing
// another package's tests, which D14 already forbids by name.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Shared fixture
// ---------------------------------------------------------------------------

// wsFixture is a collection plus the $OMNIPUS_HOME its lock directory lives
// under — the two things every write needs and neither of which the primitives
// invent for themselves.
type wsFixture struct {
	t       *testing.T
	col     *Collection
	root    string
	home    string
	lockDir string
}

func newWriteSafetyFixture(t *testing.T, notes map[string]string) *wsFixture {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, MarkerDirName), 0o700); err != nil {
		t.Fatalf("create marker dir: %v", err)
	}
	for rel, body := range notes {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}
	col, err := OpenCollection(root)
	if err != nil {
		t.Fatalf("OpenCollection: %v", err)
	}
	lockDir, err := LockDirFor(home, col.Root())
	if err != nil {
		t.Fatalf("LockDirFor: %v", err)
	}
	return &wsFixture{t: t, col: col, root: col.Root(), home: home, lockDir: lockDir}
}

func (f *wsFixture) read(rel string) string {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(rel)))
	if err != nil {
		f.t.Fatalf("read %q: %v", rel, err)
	}
	return string(raw)
}

func (f *wsFixture) token(rel string) string { return NoteContentVersion([]byte(f.read(rel))) }

func (f *wsFixture) lock() NoteLockConfig {
	return NoteLockConfig{LockDir: f.lockDir, Bound: 20 * time.Second}
}

// discardAudit satisfies AuthorAudit without keeping anything. Used only where
// the assertion is about concurrency rather than about the record.
type discardAudit struct{ mu sync.Mutex }

func (d *discardAudit) RecordKnowledgeWrite(AuthorAuditRecord) {
	d.mu.Lock()
	d.mu.Unlock() //nolint:staticcheck // the lock exists to make the sink race-free under -race
}

// ---------------------------------------------------------------------------
// AC-14.1 / US-14 AS-2, in one process, on EditNote
// ---------------------------------------------------------------------------

// TestEditNote_ConcurrentWritersNeverLoseAWriteTheyReportedAsSucceeded is the
// direct inversion of the defect: many writers, each appending its own
// section, each retrying on a version conflict.
//
// The assertion that matters is not "all of them eventually landed" — a
// last-writer-wins implementation can satisfy that by luck. It is that NO
// WRITER WAS TOLD IT SUCCEEDED AND THEN LOST. That is the sentence US-14 is
// written in ("nothing I wrote is ever silently lost") and it is the one an
// unlocked read-modify-write cannot satisfy: two writers read the same bytes,
// both pass the version comparison, both write, and the second's content was
// derived from a read that never saw the first.
//
// hookBeforeApplyWrite widens the compare-then-write window so an unlocked
// implementation loses reliably rather than occasionally. Without that, this
// test would pass against no lock at all on a fast enough machine, which is
// the false green the seam exists to remove.
func TestEditNote_ConcurrentWritersNeverLoseAWriteTheyReportedAsSucceeded(t *testing.T) {
	const rel = "shared.md"
	const writers = 8
	f := newWriteSafetyFixture(t, map[string]string{rel: "# Shared\n"})

	prev := hookBeforeApplyWrite
	hookBeforeApplyWrite = func() { time.Sleep(8 * time.Millisecond) }
	t.Cleanup(func() { hookBeforeApplyWrite = prev })

	audit := &discardAudit{}
	headings := make([]string, writers)
	for i := range headings {
		headings[i] = fmt.Sprintf("Writer %d", i)
	}

	var (
		mu        sync.Mutex
		succeeded []string
		fatal     []string
		start     sync.WaitGroup
		done      sync.WaitGroup
	)
	start.Add(1)
	for i := range writers {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			for attempt := 0; attempt < 200; attempt++ {
				_, err := EditNote(OSLinkFS(), f.col, EditNoteRequest{
					RelPath:       rel,
					Edits:         []NoteEdit{AppendSectionOnce(2, headings[i], "content")},
					ExpectVersion: f.token(rel),
					Audit:         audit,
					Actor:         AuthorActor{AgentID: "agent-" + strconv.Itoa(i)},
					Lock:          f.lock(),
				})
				if err == nil {
					mu.Lock()
					succeeded = append(succeeded, headings[i])
					mu.Unlock()
					return
				}
				if errors.Is(err, ErrVersionConflict) {
					continue // the protocol working: re-read and retry
				}
				mu.Lock()
				fatal = append(fatal, fmt.Sprintf("%s: %v", headings[i], err))
				mu.Unlock()
				return
			}
			mu.Lock()
			fatal = append(fatal, headings[i]+": gave up after 200 attempts")
			mu.Unlock()
		}(i)
	}
	start.Done()
	done.Wait()

	if len(fatal) != 0 {
		t.Fatalf("writers failed for reasons other than a version conflict: %v", fatal)
	}

	final := f.read(rel)
	var lost []string
	for _, h := range succeeded {
		if !strings.Contains(final, "## "+h) {
			lost = append(lost, h)
		}
	}
	if len(lost) != 0 {
		t.Fatalf("SILENT LOSS: %d of %d writers were told their write succeeded and it is not on disk: %v\n"+
			"final content:\n%s", len(lost), len(succeeded), lost, final)
	}
	if len(succeeded) != writers {
		t.Fatalf("succeeded = %d, want %d — every writer's edit is compatible with every other's", len(succeeded), writers)
	}

	// Positive control: the seam really did widen the window. If the sections
	// all landed because the whole race never happened, the assertion above
	// proves nothing.
	if !strings.Contains(final, "## Writer 0") || !strings.Contains(final, "## Writer 7") {
		t.Fatalf("the final note is missing sections from both ends of the run; the fixture did not do what the test assumes:\n%s", final)
	}
}

// ---------------------------------------------------------------------------
// FR-106 — the token is required, and a refusal hands back a usable one
// ---------------------------------------------------------------------------

// TestEditNote_RefusesAWriteCarryingNoVersionToken is FR-106's first clause:
// "MUST require a version token for every write".
//
// A write with no token used to be accepted outright — the check was gated on
// the token being non-empty — which made compare-and-swap opt-in and made the
// tool description's "leave unset if you have not read the note" an
// instruction to switch it off.
//
// The refusal must also be ESCAPABLE, or requiring the token would simply make
// the tool unusable for a note the caller has never read: it carries the
// note's current token, so the caller retries once and proceeds.
func TestEditNote_RefusesAWriteCarryingNoVersionToken(t *testing.T) {
	const rel = "note.md"
	const original = "# Note\n\nbody\n"
	f := newWriteSafetyFixture(t, map[string]string{rel: original})

	_, err := EditNote(OSLinkFS(), f.col, EditNoteRequest{
		RelPath: rel,
		Edits:   []NoteEdit{AppendSectionOnce(2, "Added", "x")},
		Audit:   &discardAudit{},
		Actor:   AuthorActor{AgentID: "a"},
		Lock:    f.lock(),
	})
	if err == nil {
		t.Fatal("a write with no version token was ACCEPTED; FR-106 requires a token on every write")
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v (%T), want *ConflictError so the caller can act on it", err, err)
	}
	if conflict.Path != rel {
		t.Errorf("conflict path = %q, want %q", conflict.Path, rel)
	}
	if conflict.Expected != "" {
		t.Errorf("expected_version = %q, want empty — the caller sent none", conflict.Expected)
	}
	if want := ComputeVersionToken([]byte(original)); conflict.Actual != want {
		t.Errorf("actual_version = %q, want %q — without it the caller cannot retry", conflict.Actual, want)
	}
	if got := f.read(rel); got != original {
		t.Errorf("the note changed despite the refusal:\n%q", got)
	}

	// The escape hatch really works: retrying with the token the refusal
	// carried succeeds.
	if _, retryErr := EditNote(OSLinkFS(), f.col, EditNoteRequest{
		RelPath:       rel,
		Edits:         []NoteEdit{AppendSectionOnce(2, "Added", "x")},
		ExpectVersion: string(conflict.Actual),
		Audit:         &discardAudit{},
		Actor:         AuthorActor{AgentID: "a"},
		Lock:          f.lock(),
	}); retryErr != nil {
		t.Fatalf("retrying with the token the refusal handed back failed: %v", retryErr)
	}
	if !strings.Contains(f.read(rel), "## Added") {
		t.Error("the retry reported success and changed nothing")
	}
}

// TestEditNote_StaleTokenRefusalNamesBothVersions is FR-106's second clause and
// AC-14.2's actionable half. The refusal must say what the note IS now, not
// only that it is not what the caller thought.
func TestEditNote_StaleTokenRefusalNamesBothVersions(t *testing.T) {
	const rel = "note.md"
	f := newWriteSafetyFixture(t, map[string]string{rel: "original\n"})
	stale := f.token(rel)

	const external = "someone else's later edit\n"
	if err := os.WriteFile(filepath.Join(f.root, rel), []byte(external), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := EditNote(OSLinkFS(), f.col, EditNoteRequest{
		RelPath:       rel,
		Edits:         []NoteEdit{AppendSectionOnce(2, "Added", "x")},
		ExpectVersion: stale,
		Audit:         &discardAudit{},
		Actor:         AuthorActor{AgentID: "a"},
		Lock:          f.lock(),
	})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v (%T), want *ConflictError", err, err)
	}
	if string(conflict.Expected) != stale {
		t.Errorf("expected_version = %q, want the caller's stale token %q", conflict.Expected, stale)
	}
	if want := ComputeVersionToken([]byte(external)); conflict.Actual != want {
		t.Errorf("actual_version = %q, want the current token %q", conflict.Actual, want)
	}
	if got := f.read(rel); got != external {
		t.Errorf("the external write was overwritten:\n%q", got)
	}
}

// ---------------------------------------------------------------------------
// FR-090 — an unaudited write is refused, on the agent-reachable path
// ---------------------------------------------------------------------------

// TestAuthoringTools_EveryMutationIsRefusedWithoutAnAuditSink is US-15 AS-1
// and AS-3 turned into a structural guarantee rather than a convention.
//
// FR-090 says every mutation AND every refusal is recorded. A nil sink that
// silently no-ops makes an unrecorded write reachable by construction, and the
// only enforcement in the package sat on knowledge.Writer, which nothing
// called. Enforcement belongs on the path an agent reaches.
//
// The refusal itself cannot be audited — there is nowhere to write it — which
// is exactly why the operation must not proceed.
func TestAuthoringTools_EveryMutationIsRefusedWithoutAnAuditSink(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	a4Note(t, root, "note.md", "# Note\n\nbody\n")
	a4Note(t, root, "Target.md", "# Target\n")
	before := a4Read(t, root, "note.md")

	deps := AuthoringDeps{Home: home} // no Audit: the wiring defect under test

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"knowledge_create", map[string]any{"collection": "KB", "path": "new.md", "body": "x"}},
		{"knowledge_link", map[string]any{"collection": "KB", "path": "note.md", "target": "Target"}},
		{"knowledge_set_property", map[string]any{"collection": "KB", "path": "note.md", "name": "s", "value": "v"}},
		{"knowledge_append_section", map[string]any{"collection": "KB", "path": "note.md", "heading": "H", "content": "c"}},
		{"knowledge_rename", map[string]any{"collection": "KB", "path": "note.md", "new_name": "renamed.md"}},
		{"knowledge_move", map[string]any{"collection": "KB", "path": "note.md", "new_folder": "sub"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			res := a4Tool(t, deps, tc.tool).Execute(a4Ctx("ava", ws), tc.args)
			if res == nil || !res.IsError {
				t.Fatalf("%s was ALLOWED to write with no audit sink; FR-090 requires a record of every mutation", tc.tool)
			}
			if !strings.Contains(res.ForLLM, "FR-090") {
				t.Errorf("%s refusal does not say why: %q", tc.tool, res.ForLLM)
			}
		})
	}

	if got := a4Read(t, root, "note.md"); got != before {
		t.Errorf("an unaudited refusal still changed the note:\n%q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "new.md")); err == nil {
		t.Error("knowledge_create wrote a note with no audit sink")
	}
	if _, err := os.Stat(filepath.Join(root, "renamed.md")); err == nil {
		t.Error("knowledge_rename moved a note with no audit sink")
	}

	// Positive control: with a sink, the same calls work. Without this the
	// refusals above are indistinguishable from tools that never write.
	okDeps, rec := a4Deps(home)
	ok := a4Tool(t, okDeps, "knowledge_append_section").Execute(a4Ctx("ava", ws), map[string]any{
		"collection": "KB", "path": "note.md", "heading": "H", "content": "c",
		"expect_version": a4Version(t, root, "note.md"),
	})
	if ok.IsError {
		t.Fatalf("positive control failed: %s", ok.ForLLM)
	}
	if len(rec.applied()) == 0 {
		t.Error("the positive control wrote nothing to the audit sink")
	}
}

// ---------------------------------------------------------------------------
// D14 tier 1 across the rename path
// ---------------------------------------------------------------------------

// TestRenameLinkRewrite_TakesTheNotesWriteLock proves the rename's per-file
// rewrite runs inside the SAME lock a note edit takes.
//
// It matters because the two are the same kind of operation on the same bytes:
// ApplyStep reads a note, splices its links and writes it back. Sharing no lock
// with EditNote means an edit landing inside that window is overwritten by
// content derived from the pre-edit read — and its caller has already been
// told it succeeded.
//
// Rather than race the two and hope to catch a sub-millisecond interleaving,
// this holds the note's lock and asserts the rewrite BLOCKS on it: a step that
// cannot get the lock within the bound reports a bounded failure (FR-108)
// instead of writing. A rewrite that takes no lock sails straight through.
func TestRenameLinkRewrite_TakesTheNotesWriteLock(t *testing.T) {
	const inbound = "inbound.md"
	f := newWriteSafetyFixture(t, map[string]string{
		"Old Note.md": "# Old\n",
		inbound:       "Refers to [[Old Note]].\n",
	})
	cr, err := NewCollectionRoot(OSLinkFS(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	before := f.read(inbound)

	held := make(chan struct{})
	release := make(chan struct{})
	var renameErr error
	var res *RenameResult
	var wg sync.WaitGroup

	// Hold the inbound note's lock, then run the rename against a short bound.
	go func() {
		_ = WithNoteWriteLock(NoteLockConfig{CollectionRoot: f.root, LockDir: f.lockDir, Bound: 20 * time.Second},
			inbound, func() error {
				close(held)
				<-release
				return nil
			})
	}()
	<-held

	wg.Add(1)
	go func() {
		defer wg.Done()
		r := &Renamer{
			FS:    OSLinkFS(),
			Root:  cr,
			Store: NewJournalStore(DefaultJournalDir(f.root)),
			// The bound must separate two different waits, and 150ms did not.
			// The rename takes UNCONTENDED locks of its own (the source note
			// among them) before it ever reaches the link rewrite whose lock
			// this test deliberately holds. Under -race the uncontended work
			// alone can exceed 150ms, so the rename failed with a bare
			// LockTimeoutError naming "Old Note.md" instead of the blocked
			// inbound step — the assertion below then read as a product defect
			// when it was the instrument being too tight to tell the two waits
			// apart. CI caught it (twice, through its flake filter) where a
			// local run without -race never did.
			//
			// A generous bound costs this test that much wall-clock ONLY on the
			// contended path, which is the path under test and always exhausts
			// it; the uncontended acquisitions still return immediately.
			Lock: NoteLockConfig{LockDir: f.lockDir, Bound: 3 * time.Second},
		}
		res, renameErr = r.Rename(RenameRequest{From: "Old Note.md", To: "Renamed.md"})
	}()
	wg.Wait()
	close(release)

	if renameErr == nil {
		t.Fatal("the rename rewrote a note whose write lock was held by someone else — the rewrite path takes no lock")
	}
	if !errors.Is(renameErr, ErrJournalIncomplete) {
		t.Fatalf("rename error = %v, want an incomplete-journal error naming the blocked step", renameErr)
	}
	if res == nil || res.Recovery == nil || len(res.Recovery.Conflicts) == 0 {
		t.Fatalf("res = %+v, want at least one blocked step", res)
	}
	if !strings.Contains(res.Recovery.Conflicts[0].Detail, "gave up waiting") {
		t.Errorf("blocked step detail = %q, want a bounded lock-wait failure (FR-108)",
			res.Recovery.Conflicts[0].Detail)
	}
	if got := f.read(inbound); got != before {
		t.Errorf("the blocked step wrote anyway:\n%q", got)
	}
	// The journal survives, so the operation is completable (FR-104).
	pending, listErr := NewJournalStore(DefaultJournalDir(f.root)).List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(pending) != 1 {
		t.Fatalf("pending journals = %d, want 1 retained for recovery", len(pending))
	}
}

// ---------------------------------------------------------------------------
// AC-14.1 across real OS processes, on EditNote
// ---------------------------------------------------------------------------

const (
	xprocModeEnv    = "OMNIPUS_KNOWLEDGE_XPROC_MODE"
	xprocModeEdit   = "edit"
	xprocEditRelEnv = "OMNIPUS_KNOWLEDGE_XPROC_EDIT_REL"
	xprocEditHeadEn = "OMNIPUS_KNOWLEDGE_XPROC_EDIT_HEADING"
)

// TestEditNote_ConcurrentCrossProcess_ExactlyOneWins is AC-14.1 for the write
// path production calls.
//
// D14: "citing another package's tests does not transfer the guarantee." The
// same reasoning applies one level down — citing a SIBLING API's cross-process
// test does not transfer it either, and that was the state of the write path:
// the Writer had this test and no callers; EditNote had callers and no test.
//
// POSIX only: fileutil.WithFlock is a documented no-op on Windows.
func TestEditNote_ConcurrentCrossProcess_ExactlyOneWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.WithFlock is a no-op on Windows; cross-process exclusion is POSIX-only (ADR-054 §5)")
	}
	if os.Getenv(xprocChildEnv) != "" {
		t.Skip("child mode")
	}

	const rel = "contended.md"
	const original = "# Contended\n"
	f := newWriteSafetyFixture(t, map[string]string{rel: original})
	token := f.token(rel)

	barrier := t.TempDir()
	const peers = 4
	type outcome struct {
		code int
		out  string
	}
	results := make([]outcome, peers)
	var wg sync.WaitGroup
	for i := range peers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0],
				"-test.run", "^TestEditNote_ConcurrentCrossProcess_ExactlyOneWins$", "-test.v=false")
			cmd.Env = append(os.Environ(),
				xprocChildEnv+"=1",
				xprocModeEnv+"="+xprocModeEdit,
				xprocRootEnv+"="+f.root,
				xprocHomeEnv+"="+f.home,
				xprocEditRelEnv+"="+rel,
				xprocTokenEnv+"="+token,
				xprocEditHeadEn+"="+fmt.Sprintf("Process %d", i),
				xprocBarrierEnv+"="+barrier,
				xprocPeersEnv+"="+strconv.Itoa(peers),
				xprocDelayEnv+"=80",
			)
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					code = exitErr.ExitCode()
				} else {
					code = -1
				}
			}
			results[i] = outcome{code: code, out: string(out)}
		}(i)
	}
	wg.Wait()

	var winners, conflicts int
	for i, r := range results {
		switch r.code {
		case xprocExitWon:
			winners++
		case xprocExitConflict:
			conflicts++
		default:
			t.Errorf("process %d exited %d (want %d or %d)\n%s", i, r.code, xprocExitWon, xprocExitConflict, r.out)
		}
	}
	if winners != 1 {
		t.Fatalf("cross-process winners = %d, want exactly 1 — two winners means one process's edit was silently lost (US-14 AS-2, AC-14.1)", winners)
	}
	if conflicts != peers-1 {
		t.Fatalf("cross-process conflicts = %d, want %d", conflicts, peers-1)
	}

	// Exactly one process's section is present, and the original survives as a
	// prefix: the file is one writer's output, not a torn blend of several.
	got := f.read(rel)
	if !strings.HasPrefix(got, original) {
		t.Fatalf("the note is not the original plus one append — torn or interleaved:\n%q", got)
	}
	sections := 0
	for i := range peers {
		if strings.Contains(got, fmt.Sprintf("## Process %d", i)) {
			sections++
		}
	}
	if sections != 1 {
		t.Fatalf("the note carries %d process sections, want exactly 1:\n%q", sections, got)
	}
}

// runCrossProcessEditChild performs one contending EditNote and returns the
// exit code the parent interprets.
func runCrossProcessEditChild() int {
	root := os.Getenv(xprocRootEnv)
	home := os.Getenv(xprocHomeEnv)
	rel := os.Getenv(xprocEditRelEnv)
	token := os.Getenv(xprocTokenEnv)
	heading := os.Getenv(xprocEditHeadEn)
	barrier := os.Getenv(xprocBarrierEnv)
	peers, _ := strconv.Atoi(os.Getenv(xprocPeersEnv))
	delayMS, _ := strconv.Atoi(os.Getenv(xprocDelayEnv))

	col, err := OpenCollection(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "edit child: OpenCollection: %v\n", err)
		return xprocExitBroken
	}
	lockDir, err := LockDirFor(home, col.Root())
	if err != nil {
		fmt.Fprintf(os.Stderr, "edit child: LockDirFor: %v\n", err)
		return xprocExitBroken
	}
	if delayMS > 0 {
		hookBeforeApplyWrite = func() { time.Sleep(time.Duration(delayMS) * time.Millisecond) }
	}
	if err := waitAtBarrier(barrier, peers); err != nil {
		fmt.Fprintf(os.Stderr, "edit child: barrier: %v\n", err)
		return xprocExitBroken
	}
	if _, err := EditNote(OSLinkFS(), col, EditNoteRequest{
		RelPath:       rel,
		Edits:         []NoteEdit{AppendSectionOnce(2, heading, "written by "+strconv.Itoa(os.Getpid()))},
		ExpectVersion: token,
		Audit:         &discardAudit{},
		Actor:         AuthorActor{AgentID: "child-" + strconv.Itoa(os.Getpid())},
		Lock:          NoteLockConfig{LockDir: lockDir, Bound: 20 * time.Second},
	}); err != nil {
		if errors.Is(err, ErrVersionConflict) {
			return xprocExitConflict
		}
		fmt.Fprintf(os.Stderr, "edit child: EditNote: %v\n", err)
		return xprocExitBroken
	}
	return xprocExitWon
}
