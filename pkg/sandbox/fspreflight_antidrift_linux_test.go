//go:build linux

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-092 D7/FR-012 — Level 1's Linux-only second leg: a real
// Landlock-confined child, not just a Go-level rules comparison. The rest of
// this lane's anti-drift coverage (fspreflight_antidrift_test.go) proves the
// pre-flight verdict agrees with sandbox.DeriveKernelPolicy's RENDERED
// SandboxPolicy struct; this file proves the RENDERED policy, once actually
// Applied to a real process, produces the kernel behavior that struct
// claims to produce. Landlock is Linux-only — this file cannot run on
// macOS or Windows, and every test in it skips (never fails) when Landlock
// is unavailable or the process is root (root bypasses Landlock entirely).
//
// Pattern follows the existing redteam_master_key_test.go: re-exec the test
// binary as a sandboxed child via a sentinel env var, apply the policy
// inside the child, and report results over stdout rather than juggling
// multiple exit codes for multiple assertions.
package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// runPathGrantChild is the in-child (parent-of-the-real-test-subject) phase
// of TestAntiDrift_Linux_PathGrantEnforcedByKernel. It rebuilds the SAME
// FSPolicy the outer parent computed (deterministically, from the same
// env-var inputs) rather than having the outer parent serialize a
// SandboxPolicy over the wire — both processes run the identical code path
// (fspolicy.EffectiveFSPolicy -> DeriveKernelPolicy), so this re-derivation
// is exactly what a real turn does.
//
// It does NOT call backend.Apply(kernel) directly to test the grant — that
// was this test's first (wrong) shape, caught by running it for real: Apply
// (ApplyWithMode, forChild=false) is the GATEWAY's OWN boot-time
// self-restriction, which deliberately installs the filesystem rules
// UNEXCLUDED (linuxFilesystemRules's own doc comment: "the GATEWAY needs to
// read master.key... a CHILD must be able to do neither... boot
// (forChild=false) installs the rules unexcluded"). Calling Apply on a
// per-turn policy therefore never exercises DeniedPaths/DeniedNodes at all —
// it silently passed GRANT_WRITE, VICTIM_WRITE, AND SECRET_WRITE, including
// master.key, which is exactly the false-negative CLAUDE.md's Definition of
// Done warns about (a check that could not have detected the failure).
//
// The correct production entry point for a PER-TURN CHILD is sandbox.Run
// (pkg/sandbox/hardened_exec.go), the same function ExecTool actually calls
// to spawn a bash command: it locks a fresh OS thread, calls
// RestrictCurrentThreadWithPolicy(lim.KernelPolicy) — the forChild=true path
// that DOES apply ExpandRulesExcluding — and only THEN forks/execs. That
// requires a boot policy to already be Applied first (RestrictCurrentThreadWithPolicy
// requires lb.savedMode == ModeEnforce), exactly mirroring gateway startup
// (Apply the boot profile once) followed by a per-turn spawn (Run with
// KernelPolicy set) — so this function performs both steps, in that order.
func runPathGrantChild() {
	home := os.Getenv("OMNIPUS_ADR092_HOME")
	workDir := os.Getenv("OMNIPUS_ADR092_WORKDIR")
	grantFile := os.Getenv("OMNIPUS_ADR092_GRANT")
	if home == "" || workDir == "" || grantFile == "" {
		fmt.Fprintln(os.Stderr, "runPathGrantChild: missing required env vars")
		os.Exit(77)
	}

	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		fmt.Fprintf(os.Stderr, "runPathGrantChild: backend %q is not landlock\n", name)
		os.Exit(77)
	}

	// Step 1: the boot self-restriction (mirrors gateway startup). Content
	// is irrelevant beyond being a valid policy — RestrictCurrentThreadWithPolicy
	// is always called below with an explicit, non-nil per-turn policy, which
	// overrides whatever this boot policy grants.
	bootPolicy := sandbox.DefaultPolicy(home, nil, nil, nil, nil)
	if err := backend.Apply(bootPolicy); err != nil {
		fmt.Fprintln(os.Stderr, "runPathGrantChild: boot Apply failed:", err)
		os.Exit(77)
	}

	base, err := fspolicy.EffectiveFSPolicy(context.Background(), workDir, "", true, home, "self", "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "runPathGrantChild: EffectiveFSPolicy:", err)
		os.Exit(2)
	}
	base.PathGrants = []fspolicy.PathGrant{
		{Path: grantFile, Access: fspolicy.PathGrantAccessWrite},
	}
	if err := base.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "runPathGrantChild: Validate:", err)
		os.Exit(2)
	}

	kernel := sandbox.DeriveKernelPolicy(base, sandbox.TurnPolicyInput{
		HomePath: home,
		Model:    sandbox.FilesystemModelConfined,
	})

	victimFile := os.Getenv("OMNIPUS_ADR092_VICTIM")
	secretFile := os.Getenv("OMNIPUS_ADR092_SECRET")

	// Step 2: the actual test subject. One shell grandchild attempts all
	// three writes and reports each over its own stdout — sandbox.Run is
	// what installs the PER-TURN (forChild=true, ExpandRulesExcluding'd)
	// Landlock domain on the thread that forks it.
	script := fmt.Sprintf(`
w() { if echo x > "$1" 2>/dev/null; then echo "$2=ok"; else echo "$2=denied"; fi; }
w %q GRANT_WRITE
w %q VICTIM_WRITE
w %q SECRET_WRITE
`, grantFile, victimFile, secretFile)

	res, runErr := sandbox.Run(context.Background(),
		[]string{"/bin/sh", "-c", script},
		os.Environ(),
		sandbox.Limits{WorkspaceDir: workDir, KernelPolicy: &kernel})
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "runPathGrantChild: sandbox.Run failed:", runErr)
		os.Exit(2)
	}

	// Relay the grandchild's report lines verbatim — the outer parent parses
	// GRANT_WRITE=/VICTIM_WRITE=/SECRET_WRITE= from combined output.
	os.Stdout.Write(res.Stdout)
	os.Stderr.Write(res.Stderr)
	os.Exit(0)
}

// TestAntiDrift_Linux_PathGrantEnforcedByKernel is the mandatory Linux-only
// second leg (FR-012). It asserts THREE things inside one real,
// Landlock-confined child:
//
//  1. A write to the PathGrant's exact path SUCCEEDS — the grant is real
//     kernel enforcement, not merely a struct field.
//
//  2. A write into another agent's home (home/agents/victim/…) is DENIED —
//     the cross-agent carve-out KernelDeniedPathsFor explicitly computes
//     (fspreflight_antidrift_test.go's own Level 1 matrix already checks
//     this against the RENDERED policy via sandbox.ExpandRulesExcluding;
//     this is that same case proven against the REAL kernel).
//
//     NOT tested here, on purpose: an ordinary, ungranted sibling under
//     $OMNIPUS_HOME that is neither the secret set nor a per-turn coarse
//     root (e.g. home/some-other-dir/f.txt). DefaultPolicyForModel grants
//     $OMNIPUS_HOME RWX as ONE tree; DeriveKernelPolicy's DeniedPaths only
//     excludes the secret set and per-turn cross-agent siblings, never
//     narrows the kernel grant down to WorkDir-only. Confining a bash
//     child's REACH to WorkDir/AllowedRoots/PathGrants specifically is the
//     APP-LAYER pre-flight's job (this file's Go-level sibling and
//     guardCommand, L4-owned) — the kernel's own confinement, as this
//     codebase is architected today, is "the whole home minus the secret
//     set and other agents/workspaces," not "this turn's work dir." An
//     earlier version of this test asserted the narrower claim and it was
//     FALSE — caught by actually running it (see runPathGrantChild's own
//     doc comment for the walk that proved it), not by reasoning about the
//     code. Asserting it here would be a false claim this test can prove.
//
//  3. A write to master.key (the secret set) is DENIED even though the
//     policy in this same turn carries an active, unrelated PathGrant — the
//     mandatory adversarial case (C-5/FR-037): the secret set is never
//     widenable, proven against the real kernel, not only against the
//     Go-level rendering (fspreflight_antidrift_test.go's
//     TestAntiDrift_SecretSet_NeverWidenable already covers that half).
func TestAntiDrift_Linux_PathGrantEnforcedByKernel(t *testing.T) {
	if os.Getenv("OMNIPUS_ADR092_CHILD") == "1" {
		runPathGrantChild()
		return // unreachable
	}

	if os.Getuid() == 0 {
		t.Skip("must run as non-root — root bypasses Landlock entirely")
	}
	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		t.Skipf("Landlock backend not available (backend=%q) — cannot exercise the Level 1 second leg on this kernel", name)
	}
	_ = backend

	home := nonTmpHome(t)
	workDir := filepath.Join(home, "agents", "self")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}
	grantDir := filepath.Join(home, "granted")
	if err := os.MkdirAll(grantDir, 0o755); err != nil {
		t.Fatalf("mkdir grantDir: %v", err)
	}
	grantFile := filepath.Join(grantDir, "widened.txt")

	// Another agent's home, pre-created as an EXISTING sibling directory —
	// KernelDeniedPathsFor enumerates disk to find siblings (its own doc
	// comment), so a not-yet-created sibling would never appear in the deny
	// list at all (unreachable for lack of any rule, a weaker case than a
	// real install where other agents' homes already exist).
	victimDir := filepath.Join(home, "agents", "victim")
	if err := os.MkdirAll(victimDir, 0o755); err != nil {
		t.Fatalf("mkdir victimDir: %v", err)
	}
	victimFile := filepath.Join(victimDir, "SOUL.md")

	secretFile := filepath.Join(home, "master.key")
	if err := os.WriteFile(secretFile, []byte(strings.Repeat("a1b2c3d4", 8)), 0o600); err != nil {
		t.Fatalf("seed master.key: %v", err)
	}

	// gosec rationale (out of gosec scope; documented): intentional self-exec,
	// same pattern as redteam_master_key_test.go.
	cmd := exec.Command(os.Args[0],
		"-test.run=TestAntiDrift_Linux_PathGrantEnforcedByKernel",
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		"OMNIPUS_ADR092_CHILD=1",
		"OMNIPUS_ADR092_HOME="+home,
		"OMNIPUS_ADR092_WORKDIR="+workDir,
		"OMNIPUS_ADR092_GRANT="+grantFile,
		"OMNIPUS_ADR092_VICTIM="+victimFile,
		"OMNIPUS_ADR092_SECRET="+secretFile,
	)
	out, err := cmd.CombinedOutput()
	outStr := string(out)

	if strings.Contains(outStr, "runPathGrantChild: backend") && strings.Contains(outStr, "is not landlock") {
		t.Skipf("Landlock unavailable in child:\n%s", outStr)
	}
	// Exit 77 from any other early-skip leg (missing env, Apply failure).
	if exitErr, ok := errAsExit(err); ok && exitErr == 77 {
		t.Skipf("child skipped (exit 77):\n%s", outStr)
	}
	if err != nil {
		if _, ok := errAsExit(err); !ok {
			t.Fatalf("child failed to spawn: %v\n%s", err, outStr)
		}
	}

	results := parseChildReport(outStr)

	if got, want := results["GRANT_WRITE"], "ok"; got != want {
		t.Errorf("GRANT_WRITE = %q, want %q — the PathGrant was not actually kernel-enforced\nfull output:\n%s", got, want, outStr)
	}
	if got, want := results["VICTIM_WRITE"], "denied"; got != want {
		t.Errorf("VICTIM_WRITE = %q, want %q — another agent's home must stay kernel-denied (the cross-agent carve-out)\nfull output:\n%s", got, want, outStr)
	}
	if got, want := results["SECRET_WRITE"], "denied"; got != want {
		t.Errorf("SECRET_WRITE = %q, want %q — FR-037 GAP: the secret set was reachable despite an unrelated active PathGrant in the same turn\nfull output:\n%s", got, want, outStr)
	}
}

func errAsExit(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true
	}
	return 0, false
}

func parseChildReport(out string) map[string]string {
	results := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k == "GRANT_WRITE" || k == "VICTIM_WRITE" || k == "SECRET_WRITE" {
			results[k] = v
		}
	}
	return results
}
