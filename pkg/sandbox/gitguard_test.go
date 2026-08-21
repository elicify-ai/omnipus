package sandbox_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// newProtectedWork creates a work dir with a .git subdir and returns a guard
// that protects it. The .git is materialized so canonicalization + read verbs
// exercise a real path.
func newProtectedWork(t *testing.T) (*sandbox.GitGuard, string) {
	t.Helper()
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	g := sandbox.NewGitGuard()
	if err := g.Protect(work); err != nil {
		t.Fatalf("Protect: %v", err)
	}
	return g, work
}

// ---- D17 operation policy (CheckGitOp) ------------------------------------

func TestGitSec_OpPolicy_AllowSet(t *testing.T) {
	for _, verb := range []string{"log", "blame", "show", "diff", "LOG", " diff "} {
		d := sandbox.CheckGitOp(verb)
		if !d.Allowed {
			t.Errorf("verb %q: want allowed, got denied (%s)", verb, d.PolicyRule)
		}
		if d.PolicyRule == "" {
			t.Errorf("verb %q: allow decision must carry a PolicyRule (SEC-17)", verb)
		}
	}
}

func TestGitSec_OpPolicy_DenySet(t *testing.T) {
	// Named deny-set plus fail-closed unknowns.
	for _, verb := range []string{"commit", "amend", "rebase", "rm", "reset", "checkout", "gc", "push", "config", "totally-unknown-verb", ""} {
		d := sandbox.CheckGitOp(verb)
		if d.Allowed {
			t.Errorf("verb %q: want denied (fail-closed), got allowed", verb)
		}
		if d.PolicyRule == "" {
			t.Errorf("verb %q: deny decision must carry a PolicyRule (SEC-17)", verb)
		}
	}
}

// ---- Exec guard: mutating git verbs are blocked ----------------------------

func TestGitSandbox_BlocksMutatingGitVerbs(t *testing.T) {
	g, work := newProtectedWork(t)
	cases := [][]string{
		{"git", "commit", "-m", "x"},
		{"git", "commit", "--amend", "--no-edit"},
		{"git", "rebase", "-i", "HEAD~3"},
		{"git", "rm", "--cached", "secret.txt"},
		{"git", "reset", "--hard", "HEAD~1"},
		{"git", "checkout", "HEAD~1", "--", "."},
		{"/usr/bin/git", "gc", "--prune=now"},
		{"git", "config", "core.hooksPath", "/tmp/evil"},
		{"git", "-C", work, "commit", "--amend"}, // -C into the repo from elsewhere
	}
	for _, argv := range cases {
		d := g.InspectExec(argv, work)
		if d.Allowed {
			t.Errorf("argv %v: want DENIED, got allowed", argv)
			continue
		}
		if d.PolicyRule == "" {
			t.Errorf("argv %v: deny must carry PolicyRule (SEC-17)", argv)
		}
	}
}

// TestGitSandbox_GlobalShortCConfig pins inspectGitArgs' handling of git's -c
// config-override flag in ALL three syntactic forms git(1) accepts, so the verb
// is correctly identified (and the deny on `commit --amend` fires) regardless
// of form. The glued short form "-c<name>=<value>" (one token, no space) was
// previously dropped into the unknown-option skip branch — functionally safe
// only by accident; this test pins the explicit parse (correctness m1).
func TestGitSandbox_GlobalShortCConfig(t *testing.T) {
	g, work := newProtectedWork(t)
	// Mutating verb must be DENIED in every -c form: the parser must consume
	// the config token and still land on `commit` as the verb.
	for _, argv := range [][]string{
		{"git", "-c", "user.email=foo@bar", "commit", "--amend"},                  // spaced (two-token)
		{"git", "-c=user.email=foo@bar", "commit", "--amend"},                     // -c=<name>=<value>
		{"git", "-cuser.email=foo@bar", "commit", "--amend"},                      // -c<name>=<value> (glued short form)
		{"git", "-cuser.email=foo@bar", "-c", "user.name=x", "commit", "--amend"}, // mixed repeats
	} {
		d := g.InspectExec(argv, work)
		if d.Allowed {
			t.Errorf("argv %v: want DENIED (verb must be parsed as commit), got allowed", argv)
			continue
		}
		if d.PolicyRule == "" {
			t.Errorf("argv %v: deny must carry PolicyRule (SEC-17)", argv)
		}
	}
	// Read verb must still be ALLOWED via the glued short form — proves the
	// verb is positively identified (not "no verb found → unconditional allow").
	for _, argv := range [][]string{
		{"git", "-cuser.email=foo@bar", "log"},
		{"git", "-c=user.email=foo@bar", "show"},
	} {
		d := g.InspectExec(argv, work)
		if !d.Allowed {
			t.Errorf("argv %v: want allowed (read verb), got denied (%s)", argv, d.PolicyRule)
		}
	}
}

func TestGitSandbox_BlocksAmendExplanation(t *testing.T) {
	g, work := newProtectedWork(t)
	d := g.InspectExec([]string{"git", "commit", "--amend"}, work)
	if d.Allowed {
		t.Fatal("git commit --amend must be denied")
	}
	if !strings.Contains(strings.ToLower(d.Reason), "amend") {
		t.Errorf("amend deny reason should mention amend, got %q", d.Reason)
	}
}

// ---- Exec guard: read verbs are allowed (D17) -----------------------------

func TestGitSandbox_AllowsReadVerbs(t *testing.T) {
	g, work := newProtectedWork(t)
	for _, argv := range [][]string{
		{"git", "log", "--oneline"},
		{"git", "blame", "main.go"},
		{"git", "show", "HEAD"},
		{"git", "diff", "HEAD~1"},
		{"git", "-C", work, "log"},
		{"git", "--no-pager", "diff"},
	} {
		d := g.InspectExec(argv, work)
		if !d.Allowed {
			t.Errorf("argv %v: read verb must be ALLOWED, got denied (%s)", argv, d.PolicyRule)
		}
	}
}

// ---- Exec guard: direct .git path writes are blocked -----------------------

func TestGitSandbox_BlocksDirectGitPathWrite(t *testing.T) {
	g, work := newProtectedWork(t)
	parent := filepath.Dir(work)
	base := filepath.Base(work)
	cases := []struct {
		argv []string
		cwd  string
	}{
		{[]string{"rm", "-rf", ".git"}, work},
		{[]string{"rm", "-rf", filepath.Join(base, ".git")}, parent},
		{[]string{"rm", "-f", ".git/index"}, work},
		{[]string{"tee", ".git/config"}, work},
		{[]string{"mv", ".git/HEAD", "/tmp/x"}, work},
		{[]string{"truncate", "-s", "0", ".git/HEAD"}, work},
	}
	for _, c := range cases {
		d := g.InspectExec(c.argv, c.cwd)
		if d.Allowed {
			t.Errorf("argv %v (cwd %s): want DENIED, got allowed", c.argv, c.cwd)
		}
	}
}

// ---- Exec guard: shell wrappers -------------------------------------------

func TestGitSandbox_BlocksShellWrappers(t *testing.T) {
	g, work := newProtectedWork(t)
	for _, argv := range [][]string{
		{"sh", "-c", "git commit --amend --no-edit"},
		{"bash", "-c", "git rebase -i HEAD~2"},
		{"sh", "-c", "rm -rf .git"},
		{"bash", "-c", "echo hacked > .git/HEAD"},
		{"sh", "-c", "cd . && git reset --hard"},
	} {
		d := g.InspectExec(argv, work)
		if d.Allowed {
			t.Errorf("argv %v: shell wrapper must be DENIED, got allowed", argv)
		}
	}
}

// ---- Exec guard: negatives (must NOT interfere) ---------------------------

func TestGitSandbox_AllowsUnrelated(t *testing.T) {
	g, work := newProtectedWork(t)
	other := t.TempDir() // a totally different dir, no protected repo

	cases := []struct {
		argv []string
		cwd  string
	}{
		{[]string{"git", "commit", "-m", "x"}, other}, // git in a non-protected dir
		{[]string{"cat", ".git/HEAD"}, work},          // reading .git is a D17 read
		{[]string{"ls", "-la"}, work},                 // benign command in the repo
		{[]string{"go", "build", "./..."}, work},      // build in the work tree
		{[]string{"git", "--version"}, work},          // no subcommand
		{[]string{"rm", "-rf", "build"}, work},        // deletes a non-.git path
		{[]string{"echo", "hello"}, work},             // trivial
	}
	for _, c := range cases {
		d := g.InspectExec(c.argv, c.cwd)
		if !d.Allowed {
			t.Errorf("argv %v (cwd %s): want ALLOWED, got denied (%s)", c.argv, c.cwd, d.PolicyRule)
		}
	}
}

func TestGitSandbox_InactiveGuardAllowsAll(t *testing.T) {
	g := sandbox.NewGitGuard() // nothing registered
	work := t.TempDir()
	for _, argv := range [][]string{
		{"git", "commit", "--amend"},
		{"rm", "-rf", ".git"},
		{"sh", "-c", "git rebase -i HEAD~1"},
	} {
		if d := g.InspectExec(argv, work); !d.Allowed {
			t.Errorf("inactive guard must allow everything; argv %v denied (%s)", argv, d.PolicyRule)
		}
	}
}

func TestGitSandbox_Unprotect(t *testing.T) {
	g, work := newProtectedWork(t)
	if d := g.InspectExec([]string{"git", "commit"}, work); d.Allowed {
		t.Fatal("expected deny while protected")
	}
	g.Unprotect(work)
	if d := g.InspectExec([]string{"git", "commit"}, work); !d.Allowed {
		t.Fatal("expected allow after Unprotect")
	}
}

// ---- File-tool write gate (non-exec surface) ------------------------------

func TestGitSandbox_CheckGitPathWrite(t *testing.T) {
	g, work := newProtectedWork(t)
	// Writes at/under the protected .git are denied.
	for _, p := range []string{
		filepath.Join(work, ".git", "config"),
		filepath.Join(work, ".git", "objects", "ab", "cd"),
		filepath.Join(work, ".git"),
	} {
		if d := g.CheckGitPathWrite(p); d.Allowed {
			t.Errorf("write to %s must be denied", p)
		} else if d.PolicyRule == "" {
			t.Errorf("deny for %s must carry PolicyRule", p)
		}
	}
	// Writes elsewhere in the work tree are allowed.
	for _, p := range []string{
		filepath.Join(work, "main.go"),
		filepath.Join(work, "src", "app.ts"),
	} {
		if d := g.CheckGitPathWrite(p); !d.Allowed {
			t.Errorf("write to %s must be allowed (not under .git), got %s", p, d.PolicyRule)
		}
	}
}

// ---- End-to-end enforcement through hardened_exec.Run ----------------------

func TestGitSandbox_RunBlocksAmendEndToEnd(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := sandbox.ProtectRepoWorkdir(work); err != nil {
		t.Fatalf("ProtectRepoWorkdir: %v", err)
	}
	t.Cleanup(func() { sandbox.UnprotectRepoWorkdir(work) })

	// Capture the audit entry emitted on deny.
	var got *audit.Entry
	sandbox.SetGitDenyAuditHook(func(e *audit.Entry) { got = e })
	t.Cleanup(func() { sandbox.SetGitDenyAuditHook(nil) })

	_, err := sandbox.Run(context.Background(),
		[]string{"git", "commit", "--amend", "--no-edit"},
		nil,
		sandbox.Limits{WorkspaceDir: work},
	)
	if !errors.Is(err, sandbox.ErrGitEvidenceProtected) {
		t.Fatalf("Run: want ErrGitEvidenceProtected, got %v", err)
	}

	// Audit proof: the deny produced a real, fully-populated entry.
	if got == nil {
		t.Fatal("expected a git_evidence_sandbox_block audit entry")
	}
	if got.Event != "git_evidence_sandbox_block" {
		t.Errorf("audit Event = %q", got.Event)
	}
	if got.Decision != audit.DecisionDeny {
		t.Errorf("audit Decision = %q, want deny", got.Decision)
	}
	if got.PolicyRule == "" {
		t.Error("audit entry must carry a PolicyRule (SEC-17)")
	}
	if got.Command == "" {
		t.Error("audit entry must record the blocked command")
	}
	if got.Details["git_dir"] == nil || got.Details["git_dir"] == "" {
		t.Error("audit entry must record the implicated .git dir")
	}
	t.Logf("AUDIT: event=%s decision=%s policy_rule=%q command=%q git_dir=%v",
		got.Event, got.Decision, got.PolicyRule, got.Command, got.Details["git_dir"])
}

func TestGitSandbox_RunAllowsReadEndToEnd(t *testing.T) {
	// A read verb against a protected repo must not be blocked by the guard.
	// (It may still fail to spawn if git is absent — we only assert the guard
	// did not veto it, i.e. the error is not ErrGitEvidenceProtected.)
	work := t.TempDir()
	if ignoredErr := os.MkdirAll(filepath.Join(work, ".git"), 0o755); ignoredErr != nil {
		_ = ignoredErr
	}
	if err := sandbox.ProtectRepoWorkdir(work); err != nil {
		t.Fatalf("Protect: %v", err)
	}
	t.Cleanup(func() { sandbox.UnprotectRepoWorkdir(work) })

	_, err := sandbox.Run(context.Background(),
		[]string{"git", "log", "--oneline"}, nil,
		sandbox.Limits{WorkspaceDir: work, TimeoutSeconds: 5},
	)
	if errors.Is(err, sandbox.ErrGitEvidenceProtected) {
		t.Fatalf("read verb must NOT be blocked by git guard, got %v", err)
	}
}

// ---- MIN-6 nested-repo detection ------------------------------------------

func TestGitSec_NestedRepoDetected_At(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	res, err := sandbox.DetectNestedRepo(work)
	if err != nil {
		t.Fatalf("DetectNestedRepo: %v", err)
	}
	if !res.Nested || !res.OursSkipped {
		t.Fatalf("want nested+skipped, got %+v", res)
	}
	if res.JudgeContract != sandbox.JudgeRung1Unavailable {
		t.Errorf("JudgeContract = %q, want %q", res.JudgeContract, sandbox.JudgeRung1Unavailable)
	}
	if res.PlannerNote == "" || res.OwnerNotice == "" || res.Reason == "" {
		t.Errorf("degraded contract must signal planner/owner/reason: %+v", res)
	}
	if res.UserGitDir == "" {
		t.Error("UserGitDir must be set")
	}
	t.Logf("DEGRADED SIGNAL: judge=%q planner=%q owner=%q", res.JudgeContract, res.PlannerNote, res.OwnerNotice)
}

func TestGitSec_NestedRepoDetected_Above(t *testing.T) {
	// User .git ABOVE the work dir (e.g. a repo the user cloned and we run in a subdir).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	work := filepath.Join(root, "sub", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	res, err := sandbox.DetectNestedRepo(work)
	if err != nil {
		t.Fatalf("DetectNestedRepo: %v", err)
	}
	if !res.Nested {
		t.Fatalf("want nested (user .git above), got %+v", res)
	}
	if res.UserRepoRoot != root {
		// canonicalization may resolve symlinks (e.g. macOS /var); compare via EvalSymlinks-tolerant contains
		if !strings.HasSuffix(res.UserRepoRoot, filepath.Base(root)) {
			t.Errorf("UserRepoRoot = %q, want %q", res.UserRepoRoot, root)
		}
	}
}

func TestGitSec_NestedRepoNone(t *testing.T) {
	// A temp dir with no .git anywhere up to root. Use a nested subdir so the
	// immediate ancestors are clean (t.TempDir has no .git).
	work := filepath.Join(t.TempDir(), "a", "b", "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	res, err := sandbox.DetectNestedRepo(work)
	if err != nil {
		t.Fatalf("DetectNestedRepo: %v", err)
	}
	if res.Nested || res.OursSkipped {
		t.Fatalf("want not-nested, got %+v (a stray .git exists above the temp dir?)", res)
	}
	if res.JudgeContract != "" {
		t.Errorf("no degraded contract expected, got JudgeContract=%q", res.JudgeContract)
	}
}
