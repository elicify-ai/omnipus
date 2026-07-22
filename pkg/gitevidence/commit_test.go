// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
)

func TestGitEvidence_Commit_WriteSetScopedAndContentionSurfaced(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))

	// Two concurrent members share one work/ checkout (the subdir
	// isolation rung, per the G-15 scenario): member A owns a.txt, member
	// B owns b.txt. Both are dirty in the working tree.
	writeFile(t, dir, "a.txt", "A's change")
	writeFile(t, dir, "b.txt", "B's half-written change")

	res, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "1", AgentID: "member-a"}, []string{"a.txt"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Skipped {
		t.Fatalf("Commit unexpectedly skipped: %s", res.SkipReason)
	}
	if res.Hash == "" {
		t.Fatalf("Commit produced no hash")
	}
	if !contains(res.Committed, "a.txt") {
		t.Fatalf("Committed = %v, want to contain a.txt", res.Committed)
	}
	if len(res.Committed) != 1 {
		t.Fatalf("Committed = %v, want exactly [a.txt] (must not pull in b.txt)", res.Committed)
	}
	if !contains(res.Contention, "b.txt") {
		t.Fatalf("Contention = %v, want to contain b.txt (out-of-write-set dirty file must surface, never be swallowed)", res.Contention)
	}

	// b.txt must genuinely NOT be part of the commit — the working tree
	// still shows it as an untracked/dirty change afterward.
	wt, err := r.repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	status, err := wt.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st, ok := status["b.txt"]; !ok || st.Worktree == git.Unmodified && st.Staging == git.Unmodified {
		t.Fatalf("b.txt status after scoped commit = %+v, want still dirty/untracked", status["b.txt"])
	}
	if st := status["a.txt"]; st != nil && (st.Worktree != git.Unmodified || st.Staging != git.Unmodified) {
		t.Fatalf("a.txt status after its own commit = %+v, want clean", st)
	}
}

func TestGitEvidence_Commit_MessageEncodesTaskAttemptAgent(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "x.txt", "x")

	res, err := r.Commit(BoundaryAgent, CommitMeta{TaskID: "task-9", AttemptID: "attempt-2", AgentID: "ava"}, []string{"x.txt"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	commitObj, err := r.repo.CommitObject(hashOf(t, res.Hash))
	if err != nil {
		t.Fatalf("load commit: %v", err)
	}
	msg := commitObj.Message
	for _, want := range []string{"agent", "task-9", "attempt-2", "ava"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("commit message %q missing %q", msg, want)
		}
	}
}

func TestGitEvidence_Commit_EmptyWriteSetSkips(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "x.txt", "x")

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, nil)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !res.Skipped || res.SkipReason != "empty_write_set" {
		t.Fatalf("Commit(nil write-set) = %+v, want Skipped/empty_write_set", res)
	}
	head, err := r.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != "" {
		t.Fatalf("Head() = %q after a skipped commit, want still unborn", head)
	}
}

func TestGitEvidence_Commit_NoChangesSkips(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "x.txt", "x")
	if _, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"x.txt"}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	// Same write-set, nothing changed since.
	res, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "2"}, []string{"x.txt"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !res.Skipped || res.SkipReason != "no_changes" {
		t.Fatalf("Commit with an unchanged write-set = %+v, want Skipped/no_changes", res)
	}
}

func TestGitEvidence_Commit_SizeGuardTrips(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()), WithSizeGuard(SizeGuard{MaxFiles: 10, MaxBytes: 16}))
	writeFile(t, dir, "big.txt", strings.Repeat("x", 1024))

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"big.txt"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("Commit over the byte guard did not skip: %+v", res)
	}
	if !strings.Contains(res.SkipReason, "size_guard") {
		t.Fatalf("SkipReason = %q, want to mention size_guard", res.SkipReason)
	}
	head, err := r.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != "" {
		t.Fatalf("Head() = %q after a size-guard skip, want still unborn (nothing committed)", head)
	}
}

func TestGitEvidence_Commit_SizeGuardFileCountTrips(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()), WithSizeGuard(SizeGuard{MaxFiles: 1, MaxBytes: 0}))
	writeFile(t, dir, "a.txt", "a")
	writeFile(t, dir, "b.txt", "b")

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"a.txt", "b.txt"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !res.Skipped || !strings.Contains(res.SkipReason, "size_guard") {
		t.Fatalf("Commit over the file-count guard = %+v, want Skipped/size_guard", res)
	}
}

func TestGitEvidence_Commit_ExcludesRegisteredSecretsAndNeverCommitsThem(t *testing.T) {
	const secret = "sk-live-super-secret-token"
	redact := func(s string) string {
		return strings.ReplaceAll(s, secret, "[FILTERED]")
	}
	r, dir := newTestRepo(t, WithClock(fixedClock()), WithRedactor(redact))

	writeFile(t, dir, "config.env", "API_KEY="+secret)
	writeFile(t, dir, "clean.txt", "nothing sensitive here")

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"config.env", "clean.txt"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Skipped {
		t.Fatalf("Commit unexpectedly skipped entirely: %s", res.SkipReason)
	}
	if !contains(res.ExcludedForSecret, "config.env") {
		t.Fatalf("ExcludedForSecret = %v, want to contain config.env", res.ExcludedForSecret)
	}
	if contains(res.Committed, "config.env") {
		t.Fatalf("Committed = %v, must NOT contain config.env (secret must never be staged)", res.Committed)
	}
	if !contains(res.Committed, "clean.txt") {
		t.Fatalf("Committed = %v, want to contain clean.txt", res.Committed)
	}

	// The secret must not exist anywhere in the resulting commit's tree.
	commitObj, err := r.repo.CommitObject(hashOf(t, res.Hash))
	if err != nil {
		t.Fatalf("load commit: %v", err)
	}
	if _, fileErr := commitObj.File("config.env"); fileErr == nil {
		t.Fatalf("config.env made it into the commit tree despite matching the sensitive-value scan")
	}
}

func TestGitEvidence_Commit_AllPathsExcludedForSecretSkipsEntireCommit(t *testing.T) {
	const secret = "topsecret"
	redact := func(s string) string { return strings.ReplaceAll(s, secret, "[FILTERED]") }
	r, dir := newTestRepo(t, WithClock(fixedClock()), WithRedactor(redact))
	writeFile(t, dir, "only.txt", secret)

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"only.txt"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !res.Skipped || res.SkipReason != "all_paths_excluded_for_secrets" {
		t.Fatalf("Commit = %+v, want Skipped/all_paths_excluded_for_secrets", res)
	}
	head, err := r.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != "" {
		t.Fatalf("Head() = %q, want still unborn (a secret-only write-set must never land any commit)", head)
	}
}

// MIN-5: a secret written then deleted WITHIN the same boundary window
// (before any commit observes it) must never reach history at all — the
// scan runs against the working tree at commit time, so if the file is
// already gone by then there's nothing to catch, which is the safe
// outcome (never committed in the first place).
func TestGitEvidence_Commit_SecretWrittenThenDeletedBeforeCommitNeverReachesHistory(t *testing.T) {
	const secret = "sk-ephemeral-oops"
	redact := func(s string) string { return strings.ReplaceAll(s, secret, "[FILTERED]") }
	r, dir := newTestRepo(t, WithClock(fixedClock()), WithRedactor(redact))

	writeFile(t, dir, "scratch.txt", secret)
	deleteFile(t, dir, "scratch.txt")
	writeFile(t, dir, "real.txt", "the actual output")

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"scratch.txt", "real.txt"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Skipped {
		t.Fatalf("Commit unexpectedly skipped: %s", res.SkipReason)
	}
	if !contains(res.Committed, "real.txt") {
		t.Fatalf("Committed = %v, want real.txt", res.Committed)
	}
	commitObj, err := r.repo.CommitObject(hashOf(t, res.Hash))
	if err != nil {
		t.Fatalf("load commit: %v", err)
	}
	if _, fileErr := commitObj.File("scratch.txt"); fileErr == nil {
		t.Fatalf("scratch.txt (deleted before commit) unexpectedly present in the commit tree")
	}
}

func TestGitEvidence_Commit_StagesDeletionWithinWriteSet(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "gone.txt", "will be deleted")
	if _, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"gone.txt"}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	deleteFile(t, dir, "gone.txt")
	res, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: "2"}, []string{"gone.txt"})
	if err != nil {
		t.Fatalf("Commit deletion: %v", err)
	}
	if res.Skipped {
		t.Fatalf("Commit of a staged deletion unexpectedly skipped: %s", res.SkipReason)
	}
	if !contains(res.Committed, "gone.txt") {
		t.Fatalf("Committed = %v, want gone.txt (the deletion)", res.Committed)
	}
	commitObj, err := r.repo.CommitObject(hashOf(t, res.Hash))
	if err != nil {
		t.Fatalf("load commit: %v", err)
	}
	if _, fileErr := commitObj.File("gone.txt"); fileErr == nil {
		t.Fatalf("gone.txt still present in tree after a commit staging its deletion")
	}
}

func TestGitEvidence_Commit_DirectoryWriteSetExpandsRecursively(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))
	writeFile(t, dir, "src/a.go", "package a")
	writeFile(t, dir, "src/nested/b.go", "package nested")
	writeFile(t, dir, "outside.txt", "not in the write-set")

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"src"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !contains(res.Committed, "src/a.go") || !contains(res.Committed, "src/nested/b.go") {
		t.Fatalf("Committed = %v, want both files under src/", res.Committed)
	}
	if !contains(res.Contention, "outside.txt") {
		t.Fatalf("Contention = %v, want outside.txt", res.Contention)
	}
}

func TestGitEvidence_Commit_SerializesConcurrentCallers(t *testing.T) {
	r, dir := newTestRepo(t, WithClock(fixedClock()))

	const n = 20
	files := make([]string, n)
	for i := 0; i < n; i++ {
		files[i] = "f" + string(rune('a'+i)) + ".txt"
		writeFile(t, dir, files[i], "seed")
	}

	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(f string) {
			_, err := r.Commit(BoundaryAttempt, CommitMeta{TaskID: "t1", AttemptID: f}, []string{f})
			errCh <- err
		}(files[i])
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent Commit: %v", err)
		}
	}

	// The index must not be corrupted: every file should now show clean.
	wt, err := r.repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	status, err := wt.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, f := range files {
		if st := status[f]; st != nil && (st.Worktree != git.Unmodified || st.Staging != git.Unmodified) {
			t.Fatalf("file %s status after concurrent serialized commits = %+v, want clean", f, st)
		}
	}
}

// MAJOR-3 / MIN-5 (fail-closed): a Commit with NO secret guard wired (neither
// WithSecretScanner nor WithRedactor) MUST refuse rather than let a potential
// secret pass into history. Previously fileHasSecret returned (false, "") on
// a nil scanner+redactor, so every file was staged unconditionally. The
// guard is in Commit itself (defense-in-depth) so a Phase-2 caller can't
// forget the scanner.
func TestGitEvidence_Commit_NoGuardConfiguredRefuses(t *testing.T) {
	// Open WITHOUT any scanner/redactor option — must NOT use newTestRepo,
	// which prepends a no-op redactor default.
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%s) = %v, want nil error", dir, err)
	}
	writeFile(t, dir, "x.txt", "harmless content")

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{"x.txt"})
	if err == nil {
		t.Fatalf("Commit with no guard configured = %+v nil error, want a non-nil error (MIN-5 fail-closed)", res)
	}
	if !strings.Contains(err.Error(), "no secret scanner") {
		t.Errorf("error = %q, want it to mention no secret scanner configured", err.Error())
	}
	head, herr := r.Head()
	if herr != nil {
		t.Fatalf("Head: %v", herr)
	}
	if head != "" {
		t.Fatalf("Head() = %q after a refused commit, want still unborn (nothing committed)", head)
	}
}
