// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// version_test.go — US-14 ("nothing I wrote is ever silently lost", P0),
// FR-106, FR-107, FR-108, ADR-067 D14, spec tests 46/47/48/49.
//
// Every expected value below is derived from the specification, not from what
// version.go happens to do. Where that distinction matters the test says so in
// its own comment — in particular TestWrite_MetadataTouchStillWritable, which
// asserts a behaviour a mtime-based implementation would get WRONG in the
// permissive direction, and TestWrite_MtimePreservedChangeStillRefused, which
// asserts the one it would get wrong in the destructive direction. Neither
// alone pins the design; together they do.

import (
	"bytes"
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

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// writeFixture is one knowledge base plus a Writer over it, with a recording
// audit sink so every test can assert on FR-090 as well as on the write.
type writeFixture struct {
	t      *testing.T
	col    *Collection
	home   string
	sink   *recordingSink
	writer *Writer
}

const (
	fixtureAgentID   = "mia"
	fixtureUser      = "operator"
	fixtureWorkspace = "ws-alpha"
)

// newWriteFixture builds a real knowledge base in a temp dir with one note.
func newWriteFixture(t *testing.T, notes map[string]string) *writeFixture {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, MarkerDirName), 0o700); err != nil {
		t.Fatalf("create marker dir: %v", err)
	}
	for rel, body := range notes {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			t.Fatalf("create note dir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
			t.Fatalf("write note %q: %v", rel, err)
		}
	}
	col, err := OpenCollection(root)
	if err != nil {
		t.Fatalf("OpenCollection: %v", err)
	}
	sink := &recordingSink{}
	auditor, err := NewAuditor(sink)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	lockDir, err := LockDirFor(home, col.Root())
	if err != nil {
		t.Fatalf("LockDirFor: %v", err)
	}
	w, err := NewWriter(WriterConfig{
		Collection:  col,
		LockDir:     lockDir,
		Auditor:     auditor,
		Actor:       Actor{AgentID: fixtureAgentID, User: fixtureUser, SessionID: "sess-1"},
		WorkspaceID: fixtureWorkspace,
		LockBound:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	return &writeFixture{t: t, col: col, home: home, sink: sink, writer: w}
}

// abs is the on-disk path of a collection-relative note.
func (f *writeFixture) abs(rel string) string {
	f.t.Helper()
	return filepath.Join(f.col.Root(), filepath.FromSlash(rel))
}

// read returns the note's bytes as they are on disk right now.
func (f *writeFixture) read(rel string) []byte {
	f.t.Helper()
	b, err := os.ReadFile(f.abs(rel))
	if err != nil {
		f.t.Fatalf("read %q: %v", rel, err)
	}
	return b
}

// version is the current version of a note.
func (f *writeFixture) version(rel string) NoteVersion {
	f.t.Helper()
	v, err := ReadNoteVersion(f.col, rel)
	if err != nil {
		f.t.Fatalf("ReadNoteVersion(%q): %v", rel, err)
	}
	return v
}

// externalEdit simulates the operator editing the note in Obsidian or with the
// `ev` CLI — a writer that takes none of Omnipus's locks (D14 tier 3).
func (f *writeFixture) externalEdit(rel, body string) {
	f.t.Helper()
	if err := os.WriteFile(f.abs(rel), []byte(body), 0o600); err != nil {
		f.t.Fatalf("external edit %q: %v", rel, err)
	}
}

// ---------------------------------------------------------------------------
// The token itself
// ---------------------------------------------------------------------------

func TestVersionToken_DistinguishesContentAndIsStable(t *testing.T) {
	t.Parallel()

	// Derived from the contract: the token identifies one exact state of the
	// file's CONTENT. Same content, same token; different content, different
	// token. Both halves matter — a constant token would satisfy the second
	// half of nothing, and a random one would satisfy the first half of nothing.
	a1 := ComputeVersionToken([]byte("alpha"))
	a2 := ComputeVersionToken([]byte("alpha"))
	b := ComputeVersionToken([]byte("beta"))

	if a1 != a2 {
		t.Errorf("identical content produced different tokens: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Errorf("different content produced the same token %q", a1)
	}
	if !strings.HasPrefix(string(a1), "v1:") {
		t.Errorf("token %q is not encoding-versioned; a token that cannot be told from a corrupt one is unsafe to change later", a1)
	}
	// A single-byte difference must change the token — the failure mode this
	// rules out is a token derived from length alone.
	if ComputeVersionToken([]byte("alpha")) == ComputeVersionToken([]byte("alpho")) {
		t.Error("two contents of equal length share a token: the token is not content-derived")
	}
}

func TestReadNoteVersion_AbsentNoteHasAbsentToken(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t, nil)

	v, err := ReadNoteVersion(f.col, "new/note.md")
	if err != nil {
		t.Fatalf("ReadNoteVersion on a missing note must not error (create is a CAS too): %v", err)
	}
	if v.Exists {
		t.Error("a missing note reported Exists=true")
	}
	if v.Token != TokenAbsent {
		t.Errorf("missing note token = %q, want %q", v.Token, TokenAbsent)
	}
}

func TestReadNoteVersion_RefusesPathOutsideCollection(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t, map[string]string{"note.md": "body"})

	for _, rel := range []string{"../escape.md", "/etc/passwd"} {
		if _, err := ReadNoteVersion(f.col, rel); !errors.Is(err, ErrOutsideCollection) {
			t.Errorf("ReadNoteVersion(%q) error = %v, want ErrOutsideCollection", rel, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Spec test 46 — TestWrite_StaleVersionTokenRefused (US-14 AS-1, FR-106)
// ---------------------------------------------------------------------------

func TestWrite_StaleVersionTokenRefused(t *testing.T) {
	t.Parallel()

	const rel = "architecture/sandboxing.md"
	const original = "# Sandboxing\n\noriginal body\n"
	f := newWriteFixture(t, map[string]string{rel: original})

	// Given a note read at one version …
	opened := f.version(rel)

	// … and the note is modified by another program (Obsidian, ev, Syncthing).
	const externalBody = "# Sandboxing\n\nthe operator's own edit\n"
	f.externalEdit(rel, externalBody)

	// When a write using the ORIGINAL version is attempted …
	_, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("# Sandboxing\n\nthe agent's edit\n"),
		ExpectedVersion: opened.Token,
	})

	// … then the write is refused, with a typed error naming the path.
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("write error = %v, want *ConflictError", err)
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Error("conflict does not satisfy errors.Is(err, ErrVersionConflict)")
	}
	if conflict.Path != rel {
		t.Errorf("conflict path = %q, want %q — a conflict without the path is not actionable", conflict.Path, rel)
	}
	if !strings.Contains(conflict.Error(), rel) {
		t.Errorf("conflict message %q does not name the path", conflict.Error())
	}
	if conflict.Expected != opened.Token {
		t.Errorf("expected_version = %q, want the token the caller sent (%q)", conflict.Expected, opened.Token)
	}
	wantActual := ComputeVersionToken([]byte(externalBody))
	if conflict.Actual != wantActual {
		t.Errorf("actual_version = %q, want the token of the file as it now stands (%q)", conflict.Actual, wantActual)
	}

	// And the file on disk is unchanged — byte for byte, not merely "not the
	// agent's version".
	if got := string(f.read(rel)); got != externalBody {
		t.Errorf("a refused write changed the file on disk.\n got: %q\nwant: %q", got, externalBody)
	}
}

// ---------------------------------------------------------------------------
// Spec test 47 — TestWrite_MtimePreservedChangeStillDetected
// (US-14 AS-3, FR-107, D14 "mtime alone is insufficient")
// ---------------------------------------------------------------------------

func TestWrite_MtimePreservedChangeStillRefused(t *testing.T) {
	t.Parallel()

	const rel = "note.md"
	// The two bodies are the SAME LENGTH on purpose. Size and mtime are the two
	// pieces of metadata a stat() gives you; if either could tell these apart,
	// this test would pass against an implementation that never hashed the
	// content — which is exactly the implementation FR-107 forbids.
	const original = "the original body aaaa\n"
	f := newWriteFixture(t, map[string]string{rel: original})

	opened := f.version(rel)

	// An external writer that PRESERVES the modification time. This is not a
	// contrived case: Syncthing replicates source mtimes, `cp -p`, `rsync -a`
	// and `git checkout` all do the same, and on a 1-second-granularity volume
	// an ordinary edit inside the same tick is indistinguishable anyway.
	const externalBody = "the original body bbbb\n"
	f.externalEdit(rel, externalBody)
	if err := os.Chtimes(f.abs(rel), opened.ModTime, opened.ModTime); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}

	// Prove the premise rather than assuming it: if the mtime were not actually
	// restored, this test would pass for the wrong reason.
	after, err := os.Stat(f.abs(rel))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(opened.ModTime) {
		t.Fatalf("premise broken: mtime was not restored (%v vs %v); this test proves nothing about FR-107",
			after.ModTime(), opened.ModTime)
	}
	if len(original) != len(externalBody) {
		t.Fatalf("premise broken: the two bodies differ in length (%d vs %d), so size alone could detect the change and this test would not exercise FR-107",
			len(original), len(externalBody))
	}
	if after.Size() != opened.Size {
		t.Fatalf("premise broken: size changed (%d -> %d); this test must leave stat() unable to tell the two versions apart",
			opened.Size, after.Size())
	}

	_, err = f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("the agent's edit\n"),
		ExpectedVersion: opened.Token,
	})

	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("an mtime-preserving external change was NOT detected: err = %v. "+
			"FR-107: modification time alone must not be the detector", err)
	}
	if got := string(f.read(rel)); got != externalBody {
		t.Errorf("refused write still modified the file: %q", got)
	}
}

// TestWrite_MetadataTouchStillWritable is the other half of FR-107, and it is
// the half that pins the DESIGN rather than just the outcome.
//
// Spec §13 table row 1: "Read, write, unchanged in between → succeeds". A bare
// `touch`, a `git checkout` that restored identical bytes, or a backup restore
// changes the modification time and changes NOTHING the operator wrote. There
// is nothing to lose, so there is nothing to refuse.
//
// An implementation that folded mtime into the version token would fail here
// while passing every other test in this file. Refusing to save an agent's work
// to protect content that was never at risk is not the safe direction: false
// conflicts are what teach an operator to reach for a force flag.
func TestWrite_MetadataTouchStillWritable(t *testing.T) {
	t.Parallel()

	const rel = "note.md"
	const original = "unchanged content\n"
	f := newWriteFixture(t, map[string]string{rel: original})

	opened := f.version(rel)

	// Metadata-only change: content identical, modification time moved on.
	future := opened.ModTime.Add(90 * time.Second)
	if err := os.Chtimes(f.abs(rel), future, future); err != nil {
		t.Fatalf("touch: %v", err)
	}
	touched, err := os.Stat(f.abs(rel))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if touched.ModTime().Equal(opened.ModTime) {
		t.Fatalf("premise broken: mtime did not move, so this proves nothing")
	}

	const agentBody = "the agent's edit\n"
	res, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte(agentBody),
		ExpectedVersion: opened.Token,
	})
	if err != nil {
		t.Fatalf("a metadata-only touch caused a false conflict: %v. "+
			"The version token must be derived from content, not modification time", err)
	}
	if got := string(f.read(rel)); got != agentBody {
		t.Errorf("content on disk = %q, want %q", got, agentBody)
	}
	if res.Version != ComputeVersionToken([]byte(agentBody)) {
		t.Errorf("returned version %q is not the token of what was written", res.Version)
	}
}

// ---------------------------------------------------------------------------
// FR-106 — a write MUST carry a token
// ---------------------------------------------------------------------------

func TestWrite_MissingVersionTokenRefused(t *testing.T) {
	t.Parallel()

	const rel = "note.md"
	const original = "original\n"
	f := newWriteFixture(t, map[string]string{rel: original})

	_, err := f.writer.WriteNote(WriteRequest{
		Path:    rel,
		Content: []byte("no token supplied\n"),
		// ExpectedVersion deliberately empty.
	})

	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("a write with no version token was not refused: err = %v (FR-106 requires a token on every write)", err)
	}
	if conflict.Expected != "" {
		t.Errorf("expected_version = %q, want empty — the contract says it is absent when the caller sent none", conflict.Expected)
	}
	wire := conflict.Wire()
	if wire.ExpectedVersion != nil {
		t.Errorf("wire expected_version = %q, want omitted", *wire.ExpectedVersion)
	}
	if got := string(f.read(rel)); got != original {
		t.Errorf("a tokenless write modified the file: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Create is a compare-and-swap too
// ---------------------------------------------------------------------------

func TestWrite_CreateUsesAbsentTokenAndRefusesWhenTheNoteAppeared(t *testing.T) {
	t.Parallel()

	const rel = "inbox/new-note.md"
	f := newWriteFixture(t, nil)

	// A note that does not exist reads as TokenAbsent; creating with it works.
	before := f.version(rel)
	const body = "# New\n"
	res, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte(body),
		ExpectedVersion: before.Token,
	})
	if err != nil {
		t.Fatalf("create with the absent token failed: %v", err)
	}
	if !res.Created {
		t.Error("WriteResult.Created = false for a note that did not exist")
	}
	if got := string(f.read(rel)); got != body {
		t.Errorf("created content = %q, want %q", got, body)
	}

	// A SECOND creator still holding the absent token must be refused: two
	// agents creating the same note is the same lost-write bug as two agents
	// editing one, and it is the case a naive "create if missing" silently
	// overwrites.
	_, err = f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("# A different new note\n"),
		ExpectedVersion: TokenAbsent,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("creating over a note that appeared was not refused: %v", err)
	}
	if got := string(f.read(rel)); got != body {
		t.Errorf("the refused second create overwrote the first: %q", got)
	}
}

func TestWrite_NoteDeletedUnderneathIsRefusedWithNoActualVersion(t *testing.T) {
	t.Parallel()

	const rel = "note.md"
	f := newWriteFixture(t, map[string]string{rel: "body\n"})
	opened := f.version(rel)

	// The operator deleted the note in Obsidian while the agent held its token.
	if err := os.Remove(f.abs(rel)); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("the agent's edit\n"),
		ExpectedVersion: opened.Token,
	})

	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("writing over a deleted note was not refused: %v. "+
			"Recreating it silently would resurrect a note the operator deleted", err)
	}
	if conflict.Actual != "" {
		t.Errorf("actual_version = %q, want empty when the file has been deleted (contract)", conflict.Actual)
	}
	if wire := conflict.Wire(); wire.ActualVersion != nil {
		t.Errorf("wire actual_version = %q, want omitted for a deleted file", *wire.ActualVersion)
	}
	if _, statErr := os.Lstat(f.abs(rel)); !os.IsNotExist(statErr) {
		t.Error("the refused write recreated the deleted note")
	}
}

// ---------------------------------------------------------------------------
// Contract shape (Hard Constraint #8 / KnowledgeConflictError.yaml)
// ---------------------------------------------------------------------------

func TestConflictError_WireMatchesContract(t *testing.T) {
	t.Parallel()

	e := &ConflictError{Path: "a/b.md", Expected: "v1:aaaa", Actual: "v1:bbbb"}
	wire := e.Wire()

	if string(wire.Code) != "knowledge_version_conflict" {
		t.Errorf("code = %q, want knowledge_version_conflict (the contract's single enum value)", wire.Code)
	}
	if ConflictCode != "knowledge_version_conflict" {
		t.Errorf("ConflictCode = %q, want knowledge_version_conflict", ConflictCode)
	}
	if wire.Path != "a/b.md" {
		t.Errorf("path = %q, want a/b.md", wire.Path)
	}
	if wire.Error == "" {
		t.Error("error message is empty; the contract requires a human-readable message")
	}
	if wire.ExpectedVersion == nil || *wire.ExpectedVersion != "v1:aaaa" {
		t.Errorf("expected_version = %v, want v1:aaaa", wire.ExpectedVersion)
	}
	if wire.ActualVersion == nil || *wire.ActualVersion != "v1:bbbb" {
		t.Errorf("actual_version = %v, want v1:bbbb", wire.ActualVersion)
	}
}

// ---------------------------------------------------------------------------
// THE TEST THAT MATTERS — concurrent writers, in-process, under -race
// (US-14 AS-2, spec §13 table row 4)
// ---------------------------------------------------------------------------

// TestWrite_ConcurrentWriters_ExactlyOneWins is the in-process half of the
// double-writer guarantee.
//
// Two writers hold the SAME starting token and race. The spec's requirement is
// not "the file ends up in a sane state" — it is that exactly one reports
// success and the loser is TOLD. A loser whose content vanished without an
// error is the precise definition of a silently lost write.
//
// Non-vacuity: hookBeforeApplyWrite widens the compare-then-write window to
// milliseconds. Without it both goroutines would almost always serialise by
// accident and the test would pass against an implementation holding no lock at
// all. With it, removing the lock makes both writers pass the comparison and
// both write — and the "exactly one" assertion fails.
func TestWrite_ConcurrentWriters_ExactlyOneWins(t *testing.T) {
	const rel = "contended.md"
	const original = "original\n"
	f := newWriteFixture(t, map[string]string{rel: original})
	start := f.version(rel)

	// Widen the compare-then-write window. Restored before the test ends.
	prev := hookBeforeApplyWrite
	hookBeforeApplyWrite = func() { time.Sleep(40 * time.Millisecond) }
	t.Cleanup(func() { hookBeforeApplyWrite = prev })

	bodies := []string{
		"writer A wrote this whole note and nothing else\n",
		"writer B wrote this whole note and nothing else\n",
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = make([]error, len(bodies))
		gate    = make(chan struct{})
	)
	for i, body := range bodies {
		wg.Add(1)
		go func(i int, body string) {
			defer wg.Done()
			<-gate
			_, err := f.writer.WriteNote(WriteRequest{
				Path:            rel,
				Content:         []byte(body),
				ExpectedVersion: start.Token,
			})
			mu.Lock()
			results[i] = err
			mu.Unlock()
		}(i, body)
	}
	close(gate)
	wg.Wait()

	var winners, conflicts int
	for i, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrVersionConflict):
			conflicts++
		default:
			t.Errorf("writer %d failed with an untyped error: %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 — two winners means one write was silently lost (US-14 AS-2)", winners)
	}
	if conflicts != len(bodies)-1 {
		t.Fatalf("conflicts = %d, want %d — the loser must be TOLD, not silently dropped", conflicts, len(bodies)-1)
	}

	// The file must be exactly one writer's content: never a mix, never a
	// partial line from each.
	got := string(f.read(rel))
	matched := -1
	for i, body := range bodies {
		if got == body {
			matched = i
		}
	}
	if matched < 0 {
		t.Fatalf("file content is neither writer's bytes — interleaved or torn:\n%q", got)
	}
	if results[matched] != nil {
		t.Errorf("the content on disk belongs to writer %d, but that writer was told it FAILED (%v)", matched, results[matched])
	}
	for i := range bodies {
		if i == matched {
			continue
		}
		var conflict *ConflictError
		if !errors.As(results[i], &conflict) {
			t.Errorf("loser %d error = %v, want *ConflictError", i, results[i])
			continue
		}
		if conflict.Path != rel {
			t.Errorf("loser %d conflict path = %q, want %q", i, conflict.Path, rel)
		}
		if conflict.Actual != ComputeVersionToken([]byte(got)) {
			t.Errorf("loser %d actual_version = %q, want the winner's token %q — the loser must be able to re-read, merge and retry",
				i, conflict.Actual, ComputeVersionToken([]byte(got)))
		}
	}
}

// ---------------------------------------------------------------------------
// Spec test 48 — TestWrite_ConcurrentCrossProcess_ExactlyOneWins
// Re-executes the test binary as real OS processes, matching pkg/entity.
// ---------------------------------------------------------------------------

const (
	xprocChildEnv   = "OMNIPUS_KNOWLEDGE_XPROC_CHILD"
	xprocRootEnv    = "OMNIPUS_KNOWLEDGE_XPROC_ROOT"
	xprocHomeEnv    = "OMNIPUS_KNOWLEDGE_XPROC_HOME"
	xprocRelEnv     = "OMNIPUS_KNOWLEDGE_XPROC_REL"
	xprocTokenEnv   = "OMNIPUS_KNOWLEDGE_XPROC_TOKEN"
	xprocBodyEnv    = "OMNIPUS_KNOWLEDGE_XPROC_BODY"
	xprocBarrierEnv = "OMNIPUS_KNOWLEDGE_XPROC_BARRIER"
	xprocPeersEnv   = "OMNIPUS_KNOWLEDGE_XPROC_PEERS"
	xprocDelayEnv   = "OMNIPUS_KNOWLEDGE_XPROC_DELAY_MS"

	xprocExitWon      = 0
	xprocExitConflict = 3
	xprocExitBroken   = 4
)

// TestWrite_ConcurrentCrossProcess_ExactlyOneWins is spec test 48.
//
// The in-process test above proves the striped mutex. It proves NOTHING about
// two gateways, or a gateway and a CLI, against one collection: each process
// has its own address space and therefore its own, entirely separate mutex.
// D14 is explicit that citing another package's cross-process tests does not
// transfer the guarantee, so this forks real OS processes, exactly as
// pkg/entity/store_crossprocess_test.go does.
//
// POSIX only: fileutil.WithFlock is a documented no-op on Windows, so the
// cross-process half of tier 1 does not exist there (see version.go's header
// and ADR-054 §5).
func TestWrite_ConcurrentCrossProcess_ExactlyOneWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.WithFlock is a no-op on Windows; cross-process exclusion is POSIX-only (ADR-054 §5)")
	}
	if os.Getenv(xprocChildEnv) != "" {
		t.Skip("child mode")
	}

	const rel = "contended.md"
	f := newWriteFixture(t, map[string]string{rel: "original\n"})
	start := f.version(rel)

	barrier := t.TempDir()
	const peers = 4
	bodies := make([]string, peers)
	for i := range bodies {
		bodies[i] = fmt.Sprintf("process %d wrote this whole note and nothing else\n", i)
	}

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
			cmd := exec.Command(os.Args[0], "-test.run", "^TestWrite_ConcurrentCrossProcess_ExactlyOneWins$", "-test.v=false")
			cmd.Env = append(os.Environ(),
				xprocChildEnv+"=1",
				xprocRootEnv+"="+f.col.Root(),
				xprocHomeEnv+"="+f.home,
				xprocRelEnv+"="+rel,
				xprocTokenEnv+"="+string(start.Token),
				xprocBodyEnv+"="+bodies[i],
				xprocBarrierEnv+"="+barrier,
				xprocPeersEnv+"="+strconv.Itoa(peers),
				xprocDelayEnv+"=60",
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
		t.Fatalf("cross-process winners = %d, want exactly 1 — two winners means one process's write was silently lost (US-14 AS-2, AC-14.1)", winners)
	}
	if conflicts != peers-1 {
		t.Fatalf("cross-process conflicts = %d, want %d", conflicts, peers-1)
	}

	got := string(f.read(rel))
	found := false
	for _, body := range bodies {
		if got == body {
			found = true
		}
	}
	if !found {
		t.Fatalf("file content is no single process's bytes — torn or interleaved:\n%q", got)
	}
}

// TestMain routes child processes of the cross-process test into the child
// body before the test framework runs anything else. The child cannot use
// t.Fatal — the testing.T it would fail is not the one the parent observes —
// so it communicates its outcome through the exit code.
func TestMain(m *testing.M) {
	if os.Getenv(xprocChildEnv) != "" {
		// Two child bodies now: the Writer's, and EditNote's. The second
		// exists because the first proves cross-process exclusion for an API
		// with no production callers, which is a guarantee held by the wrong
		// object. See write_safety_test.go.
		if os.Getenv(xprocModeEnv) == xprocModeEdit {
			os.Exit(runCrossProcessEditChild())
		}
		os.Exit(runCrossProcessWriteChild())
	}
	os.Exit(m.Run())
}

// runCrossProcessWriteChild performs one contending write and returns the exit
// code the parent interprets.
func runCrossProcessWriteChild() int {
	root := os.Getenv(xprocRootEnv)
	home := os.Getenv(xprocHomeEnv)
	rel := os.Getenv(xprocRelEnv)
	token := VersionToken(os.Getenv(xprocTokenEnv))
	body := os.Getenv(xprocBodyEnv)
	barrier := os.Getenv(xprocBarrierEnv)
	peers, _ := strconv.Atoi(os.Getenv(xprocPeersEnv))
	delayMS, _ := strconv.Atoi(os.Getenv(xprocDelayEnv))

	col, err := OpenCollection(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "child: OpenCollection: %v\n", err)
		return xprocExitBroken
	}
	auditor, err := NewAuditor(&recordingSink{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "child: NewAuditor: %v\n", err)
		return xprocExitBroken
	}
	lockDir, err := LockDirFor(home, col.Root())
	if err != nil {
		fmt.Fprintf(os.Stderr, "child: LockDirFor: %v\n", err)
		return xprocExitBroken
	}
	w, err := NewWriter(WriterConfig{
		Collection: col,
		LockDir:    lockDir,
		Auditor:    auditor,
		Actor:      Actor{AgentID: "child-" + strconv.Itoa(os.Getpid())},
		LockBound:  20 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "child: NewWriter: %v\n", err)
		return xprocExitBroken
	}

	// Widen the compare-then-write window inside the child, so that an
	// implementation with no cross-process lock genuinely produces two winners
	// rather than serialising by luck.
	if delayMS > 0 {
		hookBeforeApplyWrite = func() { time.Sleep(time.Duration(delayMS) * time.Millisecond) }
	}

	if err := waitAtBarrier(barrier, peers); err != nil {
		fmt.Fprintf(os.Stderr, "child: barrier: %v\n", err)
		return xprocExitBroken
	}

	if _, err := w.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte(body),
		ExpectedVersion: token,
	}); err != nil {
		if errors.Is(err, ErrVersionConflict) {
			return xprocExitConflict
		}
		fmt.Fprintf(os.Stderr, "child: write: %v\n", err)
		return xprocExitBroken
	}
	return xprocExitWon
}

// waitAtBarrier makes every child arrive at the write at roughly the same
// instant: each drops a file, then waits for all of them to appear.
func waitAtBarrier(dir string, peers int) error {
	if dir == "" || peers <= 1 {
		return nil
	}
	self := filepath.Join(dir, strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(self, []byte("ready"), 0o600); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		if len(entries) >= peers {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("barrier timed out")
}

// ---------------------------------------------------------------------------
// Spec test 49 / FR-108 — the lock wait is bounded and errors rather than hangs
// ---------------------------------------------------------------------------

func TestWrite_LockWaitIsBoundedAtTheFileLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.WithFlock is a no-op on Windows; there is no file lock to contend for")
	}

	const rel = "note.md"
	const original = "original\n"
	f := newWriteFixture(t, map[string]string{rel: original})
	start := f.version(rel)

	// Shorten the bound so the test is quick; the requirement is that the wait
	// is BOUNDED and errors, not that the bound is five seconds.
	const bound = 250 * time.Millisecond
	w := f.newWriterWithBound(bound)

	lockPath, err := noteLockPathFor(w.lockDir, rel)
	if err != nil {
		t.Fatalf("lockPathFor: %v", err)
	}

	held := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- fileutil.WithFlock(lockPath, func() error {
			close(held)
			<-releaseHolder
			return nil
		})
	}()
	<-held
	t.Cleanup(func() {
		close(releaseHolder)
		<-holderDone
	})

	began := time.Now()
	_, err = w.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("the agent's edit\n"),
		ExpectedVersion: start.Token,
	})
	elapsed := time.Since(began)

	var lockErr *LockTimeoutError
	if !errors.As(err, &lockErr) {
		t.Fatalf("write against a held lock returned %v, want *LockTimeoutError (FR-108: error rather than hang)", err)
	}
	if !errors.Is(err, ErrLockTimeout) {
		t.Error("lock timeout does not satisfy errors.Is(err, ErrLockTimeout)")
	}
	if lockErr.Path != rel {
		t.Errorf("lock timeout path = %q, want %q", lockErr.Path, rel)
	}
	if elapsed < bound {
		t.Errorf("gave up after %v, before the %v bound elapsed", elapsed, bound)
	}
	if elapsed > 10*bound {
		t.Errorf("took %v to give up on a %v bound — the wait is not effectively bounded", elapsed, bound)
	}
	if got := string(f.read(rel)); got != original {
		t.Errorf("a timed-out write modified the file: %q", got)
	}
	// FR-090: the refusal is on the record.
	entry := f.sink.mustFind(t, "lock_timeout")
	if entry.Decision != "deny" {
		t.Errorf("lock-timeout audit decision = %q, want deny", entry.Decision)
	}
}

func TestWrite_LockWaitIsBoundedAtTheInProcessMutex(t *testing.T) {
	const rel = "note.md"
	f := newWriteFixture(t, map[string]string{rel: "original\n"})
	start := f.version(rel)

	const bound = 200 * time.Millisecond
	w := f.newWriterWithBound(bound)

	// Hold the process-wide striped mutex for this note directly, so the write
	// cannot get past the in-process half of tier 1.
	mu := noteWriteLocks.Get(noteLockKey(w.col.Root(), rel))
	mu.Lock()
	t.Cleanup(mu.Unlock)

	began := time.Now()
	_, err := w.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("edit\n"),
		ExpectedVersion: start.Token,
	})
	elapsed := time.Since(began)

	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("write against a held in-process mutex returned %v, want ErrLockTimeout", err)
	}
	if elapsed < bound {
		t.Errorf("gave up after %v, before the %v bound", elapsed, bound)
	}
	if elapsed > 10*bound {
		t.Errorf("took %v to give up on a %v bound", elapsed, bound)
	}
}

// newWriterWithBound builds a second Writer over the same fixture with a
// different lock bound.
func (f *writeFixture) newWriterWithBound(bound time.Duration) *Writer {
	f.t.Helper()
	auditor, err := NewAuditor(f.sink)
	if err != nil {
		f.t.Fatalf("NewAuditor: %v", err)
	}
	lockDir, err := LockDirFor(f.home, f.col.Root())
	if err != nil {
		f.t.Fatalf("LockDirFor: %v", err)
	}
	w, err := NewWriter(WriterConfig{
		Collection:  f.col,
		LockDir:     lockDir,
		Auditor:     auditor,
		Actor:       Actor{AgentID: fixtureAgentID, User: fixtureUser},
		WorkspaceID: fixtureWorkspace,
		LockBound:   bound,
	})
	if err != nil {
		f.t.Fatalf("NewWriter: %v", err)
	}
	return w
}

// ---------------------------------------------------------------------------
// Containment and construction
// ---------------------------------------------------------------------------

func TestWrite_PathOutsideCollectionRefusedAndAudited(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t, map[string]string{"note.md": "body\n"})

	for _, rel := range []string{"../escape.md", "/etc/passwd", ""} {
		_, err := f.writer.WriteNote(WriteRequest{
			Path:            rel,
			Content:         []byte("x"),
			ExpectedVersion: TokenAbsent,
		})
		if !errors.Is(err, ErrOutsideCollection) {
			t.Errorf("WriteNote(%q) error = %v, want ErrOutsideCollection", rel, err)
		}
	}
	if n := f.sink.countWithReason("outside_collection"); n != 3 {
		t.Errorf("audited containment refusals = %d, want 3 (FR-090: EVERY refusal)", n)
	}
}

func TestWrite_SymlinkTargetRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privilege on Windows")
	}
	t.Parallel()

	f := newWriteFixture(t, map[string]string{"real.md": "body\n"})
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("someone else's file\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := f.abs("link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}

	_, err := f.writer.WriteNote(WriteRequest{
		Path:            "link.md",
		Content:         []byte("written through a symlink\n"),
		ExpectedVersion: TokenAbsent,
	})
	if !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("writing through a symlink returned %v, want ErrNotRegularFile (FR-044)", err)
	}
	body, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("read outside file: %v", readErr)
	}
	if string(body) != "someone else's file\n" {
		t.Errorf("the write followed the symlink and modified a file outside the collection: %q", body)
	}
}

func TestNewWriter_RefusesAnUnauditableOrAnonymousWriter(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t, nil)
	auditor, err := NewAuditor(f.sink)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	lockDir, err := LockDirFor(f.home, f.col.Root())
	if err != nil {
		t.Fatalf("LockDirFor: %v", err)
	}
	good := WriterConfig{Collection: f.col, LockDir: lockDir, Auditor: auditor, Actor: Actor{AgentID: "mia"}}

	if _, err := NewWriter(good); err != nil {
		t.Fatalf("the control configuration must construct, otherwise the negative cases prove nothing: %v", err)
	}

	noAuditor := good
	noAuditor.Auditor = nil
	if _, err := NewWriter(noAuditor); !errors.Is(err, ErrAuditUnavailable) {
		t.Errorf("writer with no auditor: err = %v, want ErrAuditUnavailable (FR-090 must be unbypassable)", err)
	}

	anonymous := good
	anonymous.Actor = Actor{}
	if _, err := NewWriter(anonymous); !errors.Is(err, ErrWriterMisconfigured) {
		t.Errorf("writer with no actor: err = %v, want ErrWriterMisconfigured (US-15 AS-1: the record must name the agent)", err)
	}

	noLockDir := good
	noLockDir.LockDir = ""
	if _, err := NewWriter(noLockDir); !errors.Is(err, ErrWriterMisconfigured) {
		t.Errorf("writer with no lock dir: err = %v, want ErrWriterMisconfigured", err)
	}

	noCollection := good
	noCollection.Collection = nil
	if _, err := NewWriter(noCollection); !errors.Is(err, ErrWriterMisconfigured) {
		t.Errorf("writer with no collection: err = %v, want ErrWriterMisconfigured", err)
	}
}

func TestWrite_PreservesExistingPermissionBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	t.Parallel()

	const rel = "note.md"
	f := newWriteFixture(t, map[string]string{rel: "body\n"})
	if err := os.Chmod(f.abs(rel), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	start := f.version(rel)

	if _, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("edited\n"),
		ExpectedVersion: start.Token,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(f.abs(rel))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode after write = %v, want 0644 — the operator's files keep the operator's shape", got)
	}
}

// TestWrite_TemporaryFileNeverSurvivesInTheCollection guards the atomic-write
// mechanism: a temp file left behind in the operator's vault would be indexed,
// synced and shown in Obsidian.
func TestWrite_TemporaryFileNeverSurvivesInTheCollection(t *testing.T) {
	t.Parallel()

	const rel = "sub/note.md"
	f := newWriteFixture(t, map[string]string{rel: "body\n"})
	start := f.version(rel)

	if _, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("edited\n"),
		ExpectedVersion: start.Token,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(f.abs(rel)))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "omnipus-write") {
			t.Errorf("temporary file %q left behind in the collection", e.Name())
		}
	}
	if !bytes.Equal(f.read(rel), []byte("edited\n")) {
		t.Errorf("content = %q, want %q", f.read(rel), "edited\n")
	}
}
