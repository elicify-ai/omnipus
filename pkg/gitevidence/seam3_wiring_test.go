// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors
//
// SEAM 3 reconciliation tests (worktree wf/w1-seams): W1-git's
// gitevidence.Open/Commit wired to W1-gitsec's sandbox.ProtectRepoWorkdir
// exec-block and pkg/audit.SecretScanner (DoD-11 — ONE secret guard).
//
// Build tags: goolm,stdjson (CGO_ENABLED=0).
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestGitEvidence_Seam3' -p 1 ./pkg/gitevidence/

package gitevidence

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestGitEvidence_Seam3_OpenArmsExecBlockGuard proves Open registers the
// work dir with pkg/sandbox's process-wide DefaultGitGuard, so a `git
// commit`/`rm -rf .git` invocation against it is denied at the exec
// chokepoint (D17 FR-152 caveat 3) without the caller having to remember to
// call sandbox.ProtectRepoWorkdir separately.
func TestGitEvidence_Seam3_OpenArmsExecBlockGuard(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%s) = %v, want nil error", dir, err)
	}
	t.Cleanup(func() { sandbox.UnprotectRepoWorkdir(dir) })

	gitDir := filepath.Join(dir, ".git")
	if !sandbox.IsProtectedGitPath(gitDir) {
		t.Fatalf("IsProtectedGitPath(%s) = false, want true after Open — sandbox.ProtectRepoWorkdir was not armed", gitDir)
	}

	// A read verb is still allowed (D17 allow-set).
	if d := sandbox.InspectExecForGit([]string{"git", "log"}, dir); !d.Allowed {
		t.Fatalf("InspectExecForGit(git log) = denied (%+v), want allowed (D17 read allow-set)", d)
	}

	// A mutating verb against the protected repo is denied.
	if d := sandbox.InspectExecForGit([]string{"git", "commit", "-m", "tamper"}, dir); d.Allowed {
		t.Fatalf("InspectExecForGit(git commit) = allowed, want denied — Open must arm the exec-block guard for %s", gitDir)
	}

	// A shell-wrapped attempt to blow away the evidence repo is denied too.
	if d := sandbox.InspectExecForGit([]string{"bash", "-c", "rm -rf .git"}, dir); d.Allowed {
		t.Fatalf("InspectExecForGit(rm -rf .git via shell) = allowed, want denied for protected repo %s", gitDir)
	}

	_ = r
}

// TestGitEvidence_Seam3_SecretScannerExcludesAndSupersedesRedactor proves
// WithSecretScanner is the preferred, superseding secret guard (DoD-11 — ONE
// guard on commit, not two): when both WithSecretScanner and WithRedactor
// are configured on the same Repo, ONLY the scanner branch runs — a content
// pattern the redactor alone would have flagged is NOT excluded once a
// scanner is present, and a pattern the scanner's own engine recognizes IS
// excluded (never staged, never reaches the commit tree).
func TestGitEvidence_Seam3_SecretScannerExcludesAndSupersedesRedactor(t *testing.T) {
	scanner, err := audit.NewSecretScanner(nil, []string{`sk-scanner-[a-z0-9]+`})
	if err != nil {
		t.Fatalf("NewSecretScanner: %v", err)
	}
	// A redactor that would flag a DIFFERENT marker than the scanner's
	// pattern — present purely to prove it is bypassed once a scanner is
	// configured (supersede, not "both run").
	redactorCalled := false
	redact := func(s string) string {
		redactorCalled = true
		if strings.Contains(s, "sk-redactor-only") {
			return "[FILTERED]"
		}
		return s
	}

	r, dir := newTestRepo(t, WithClock(fixedClock()), WithSecretScanner(scanner), WithRedactor(redact))

	writeFile(t, dir, "scanner-secret.txt", "token=sk-scanner-abc123")
	writeFile(t, dir, "redactor-only-secret.txt", "token=sk-redactor-only-xyz")
	writeFile(t, dir, "clean.txt", "nothing sensitive here")

	res, err := r.Commit(BoundaryTask, CommitMeta{TaskID: "t1"}, []string{
		"scanner-secret.txt", "redactor-only-secret.txt", "clean.txt",
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Skipped {
		t.Fatalf("Commit unexpectedly skipped entirely: %s", res.SkipReason)
	}
	if !contains(res.ExcludedForSecret, "scanner-secret.txt") {
		t.Fatalf("ExcludedForSecret = %v, want to contain scanner-secret.txt (scanner pattern match)", res.ExcludedForSecret)
	}
	if contains(res.Committed, "scanner-secret.txt") {
		t.Fatalf("Committed = %v, must NOT contain scanner-secret.txt", res.Committed)
	}
	// The redactor-only marker is NOT excluded — proves the scanner
	// supersedes the redactor entirely (the redactor's own callback is
	// never even invoked while a scanner is configured).
	if !contains(res.Committed, "redactor-only-secret.txt") {
		t.Fatalf("Committed = %v, want to contain redactor-only-secret.txt (redactor must be superseded, not consulted, when a scanner is set)", res.Committed)
	}
	if !contains(res.Committed, "clean.txt") {
		t.Fatalf("Committed = %v, want to contain clean.txt", res.Committed)
	}
	if redactorCalled {
		t.Fatalf("the legacy WithRedactor callback was invoked even though WithSecretScanner was configured — DoD-11 requires exactly ONE guard to run, not both")
	}

	commitObj, err := r.repo.CommitObject(hashOf(t, res.Hash))
	if err != nil {
		t.Fatalf("load commit: %v", err)
	}
	if _, fileErr := commitObj.File("scanner-secret.txt"); fileErr == nil {
		t.Fatalf("scanner-secret.txt made it into the commit tree despite matching the scanner pattern")
	}
}
