package runner

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// envSnapshotStub writes a stub CLI that dumps its OWN process environment to
// envFile (one KEY=VALUE per line), prints a single valid claude result line so
// the driver's stream parser terminates cleanly, and exits 0. The driver runs it
// as the child process, so envFile captures exactly the env the child inherited.
func envSnapshotStub(t *testing.T, envFile string) string {
	t.Helper()
	// `env` (or the shell's `export -p`) enumerates the child's environment. Using
	// /usr/bin/env keeps it POSIX and avoids quoting pitfalls.
	body := "#!/bin/sh\n" +
		"env > '" + envFile + "'\n" +
		`printf '%s\n' '{"type":"result","subtype":"success","result":"ok"}'` + "\n" +
		"exit 0\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "stub-cli")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("envSnapshotStub: %v", err)
	}
	return path
}

// readChildEnvKeys parses the env dump written by envSnapshotStub into a set of
// the environment variable NAMES the child process saw.
func readChildEnvKeys(t *testing.T, envFile string) map[string]string {
	t.Helper()
	f, err := os.Open(envFile)
	if err != nil {
		t.Fatalf("readChildEnvKeys: open %s: %v", envFile, err)
	}
	defer func() { _ = f.Close() }()

	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if eq := strings.IndexByte(line, '='); eq > 0 {
			out[line[:eq]] = line[eq+1:]
		}
	}
	return out
}

// TestExternalCLIChild_DoesNotInheritMasterKey is the CRITICAL/security
// regression test for the master-key env leak (Spec-4 FR-5.3 / SEC-23).
//
// Before the fix, the drivers' buildEnv started from os.Environ() and the
// dispatch site (external_dispatch.go) left RunOptions.Env empty, so the spawned
// external CLI inherited the ENTIRE gateway environment — including
// OMNIPUS_MASTER_KEY, the credential-vault master key. ScrubGatewayEnvForRunner
// (the runner-credential allowlist) existed but was never called.
//
// The fix: external_dispatch.go sets RunOptions.Env = ScrubGatewayEnvForRunner()
// and the drivers' buildEnv uses a non-nil RunOptions.Env as the COMPLETE child
// env (buildChildEnv) — no os.Environ() fallback. This test drives a real child
// through the claude driver with that exact wiring and asserts:
//
//  1. OMNIPUS_MASTER_KEY is NOT present in the child env.
//  2. No other gateway secret (here a sentinel) is present either.
//  3. Every key the child DID inherit is an allowlisted key — the child env is a
//     strict subset of the scrubbed allowlist, never a superset.
//  4. A legitimately-allowlisted runner credential (ANTHROPIC_API_KEY) DOES reach
//     the child, proving the scrub is not over-aggressive.
func TestExternalCLIChild_DoesNotInheritMasterKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}

	// Set the master key + a sentinel secret in the PARENT (gateway) env. These
	// MUST NOT reach the child. Also set an allowlisted runner credential that
	// SHOULD reach the child.
	const sentinelKey = "OMNIPUS_TEST_SENTINEL_SECRET"
	t.Setenv("OMNIPUS_MASTER_KEY", "deadbeef00000000000000000000000000000000000000000000000000000000")
	t.Setenv(sentinelKey, "must-not-leak")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-allowlisted-key")
	// PATH must survive so the stub's `env` / `sh` resolve in the child.
	t.Setenv("PATH", os.Getenv("PATH"))

	envFile := filepath.Join(t.TempDir(), "child.env")
	stub := envSnapshotStub(t, envFile)

	orig := claudeBinName
	claudeBinName = stub
	t.Cleanup(func() { claudeBinName = orig })

	// This is the SAME value the production dispatch site passes as RunOptions.Env.
	scrubbed := sandbox.ScrubGatewayEnvForRunner()

	d := NewClaudeDriver(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := d.Run(ctx, RunOptions{Input: "task", Env: scrubbed})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	drain(t, ch)

	childEnv := readChildEnvKeys(t, envFile)

	// (1) The master key must NOT be in the child env.
	if v, ok := childEnv["OMNIPUS_MASTER_KEY"]; ok {
		t.Fatalf("SECURITY LEAK: OMNIPUS_MASTER_KEY reached the external-CLI child (value=%q); "+
			"the gateway master key must never be inherited by a third-party binary", v)
	}
	// (2) The sentinel gateway secret must NOT be in the child env either.
	if _, ok := childEnv[sentinelKey]; ok {
		t.Fatalf("SECURITY LEAK: gateway secret %q reached the external-CLI child", sentinelKey)
	}

	// (3) Nothing must leak THROUGH the driver from the parent (gateway) env: every
	// key present in BOTH the parent env and the child env must be an allowlisted
	// key. This is the precise no-leak property — it ignores vars the child's own
	// shell injects (PWD, SHLVL, _ , OLDPWD), which are NOT inherited from the
	// gateway and therefore carry no gateway secret.
	allowed := make(map[string]struct{}, len(scrubbed))
	for _, kv := range scrubbed {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			allowed[kv[:eq]] = struct{}{}
		}
	}
	parentKeys := make(map[string]struct{})
	for _, kv := range os.Environ() {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			parentKeys[kv[:eq]] = struct{}{}
		}
	}
	// Vars the child's own /bin/sh injects (their values reflect the child's
	// context, not the gateway's) — these carry no gateway secret and are not part
	// of the driver's inheritance path.
	shellInjected := map[string]struct{}{
		"PWD": {}, "SHLVL": {}, "_": {}, "OLDPWD": {},
	}
	for k := range childEnv {
		if _, shell := shellInjected[k]; shell {
			continue
		}
		_, fromParent := parentKeys[k]
		_, isAllowed := allowed[k]
		if fromParent && !isAllowed {
			t.Fatalf("SECURITY LEAK: child inherited parent env key %q that is NOT in the "+
				"scrubbed allowlist — a gateway env var leaked through the driver", k)
		}
	}

	// (4) An allowlisted runner credential MUST reach the child (scrub is not
	// over-aggressive — the CLI still authenticates to its model provider).
	if v := childEnv["ANTHROPIC_API_KEY"]; v != "sk-ant-allowlisted-key" {
		t.Fatalf("allowlisted runner credential ANTHROPIC_API_KEY did not reach the child intact; got %q", v)
	}
}

// TestBuildChildEnv_NilFallsBackToOsEnviron verifies the buildChildEnv contract
// directly: a nil caller env inherits the parent process env (direct/test
// callers), while a non-nil caller env is used verbatim as the COMPLETE child
// env (no os.Environ() merge — that is the leak-prevention guarantee).
func TestBuildChildEnv_NilFallsBackToOsEnviron(t *testing.T) {
	t.Setenv("OMNIPUS_MASTER_KEY", "leak-canary")

	// nil → inherits parent env (canary present). This path is for direct/test
	// callers that do not flow through the scrubbed dispatch site.
	got := buildChildEnv(nil)
	if !envContainsKey(got, "OMNIPUS_MASTER_KEY") {
		t.Fatal("buildChildEnv(nil) must inherit the parent env (os.Environ fallback)")
	}

	// non-nil → used verbatim; the master-key canary must NOT appear.
	scrubbed := []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=sk-x"}
	got = buildChildEnv(scrubbed)
	if envContainsKey(got, "OMNIPUS_MASTER_KEY") {
		t.Fatal("buildChildEnv(non-nil) must NOT fall back to os.Environ(); the master key leaked")
	}
	if len(got) != len(scrubbed) {
		t.Fatalf(
			"buildChildEnv(non-nil) must return exactly the caller env; got %d entries, want %d",
			len(got),
			len(scrubbed),
		)
	}

	// A non-nil but EMPTY caller env yields an empty child env ("inherit nothing"),
	// NOT a fallback to os.Environ().
	if got := buildChildEnv([]string{}); len(got) != 0 {
		t.Fatalf("buildChildEnv(empty non-nil) must yield an empty env; got %d entries", len(got))
	}
}

// TestOpencodeDriver_PromptDeliveredExactlyOnce is the M3 regression test,
// updated for ADR-032: opencode's `run` command has no `--prompt` flag (a real
// `opencode run --help` shows the message is a POSITIONAL argument —
// `opencode run [message..]`). The prompt must still be delivered through
// exactly one channel: buildArgs now supplies it as the positional argument
// immediately after "run"; Run sets stdin to an empty reader. This test
// asserts the positional carries the prompt exactly once (the stdin side is
// covered by Run's empty-reader contract) and that no `--prompt` flag is ever
// emitted (that flag does not exist on the real CLI and would error).
func TestOpencodeDriver_PromptDeliveredExactlyOnce(t *testing.T) {
	d := NewOpencodeDriver(nil)
	const prompt = "do the thing"
	args := d.buildArgs(RunOptions{Input: prompt})

	if len(args) < 2 || args[0] != "run" || args[1] != prompt {
		t.Fatalf("prompt must be the positional argument immediately after \"run\"; args=%v", args)
	}
	occurrences := 0
	for _, a := range args {
		if a == "--prompt" {
			t.Fatalf("--prompt is not a real opencode flag and must never be emitted; args=%v", args)
		}
		if a == prompt {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf(
			"prompt value appears %d times in args (must be exactly once — no double-prompt); args=%v",
			occurrences,
			args,
		)
	}

	// An empty input must add no stray positional value — "run" is directly
	// followed by the next flag, not an empty string.
	emptyArgs := d.buildArgs(RunOptions{Input: ""})
	if len(emptyArgs) > 0 && emptyArgs[0] == "run" && len(emptyArgs) > 1 && emptyArgs[1] == "" {
		t.Fatalf("empty input must not produce a stray positional arg; args=%v", emptyArgs)
	}
}

// TestOpencodeDriver_BuildArgs_ModelGuardedByProviderModelShape asserts the
// --model flag is only added when the model string is shaped like
// "provider/model" (ADR-032 fix C) — a bare model name (as configured for
// claude/codex agents) must be omitted rather than passed as garbage.
func TestOpencodeDriver_BuildArgs_ModelGuardedByProviderModelShape(t *testing.T) {
	d := NewOpencodeDriver(nil)

	withShape := d.buildArgs(RunOptions{Input: "task", Model: "anthropic/claude-sonnet-4.6"})
	if !containsSeq(withShape, "--model", "anthropic/claude-sonnet-4.6") {
		t.Errorf("provider/model-shaped model must be passed via --model; args=%v", withShape)
	}

	bareModel := d.buildArgs(RunOptions{Input: "task", Model: "claude-sonnet-4.6"})
	for _, a := range bareModel {
		if a == "--model" {
			t.Errorf("bare (non provider/model) model string must NOT produce --model; args=%v", bareModel)
		}
	}

	empty := d.buildArgs(RunOptions{Input: "task", Model: ""})
	for _, a := range empty {
		if a == "--model" {
			t.Errorf("empty model must NOT produce --model; args=%v", empty)
		}
	}
}

// TestOpencodeDriver_BuildArgs_NoSessionFlagForFreshRun asserts opts.RunID is
// never passed via -s/--session — opencode documents that flag as "session id
// to continue" (an existing session), and opts.RunID here is always a freshly
// generated dispatch identifier opencode has never seen (ADR-032 fix C).
func TestOpencodeDriver_BuildArgs_NoSessionFlagForFreshRun(t *testing.T) {
	d := NewOpencodeDriver(nil)
	args := d.buildArgs(RunOptions{Input: "task", RunID: "ext-1234567890"})
	for _, a := range args {
		if a == "--session" || a == "-s" {
			t.Fatalf("a fresh RunID must not be passed via --session/-s; args=%v", args)
		}
	}
}

// TestOpencodeDriver_BuildArgs_AutoApprovePosture asserts the non-interactive
// auto-approve flag is always present (ADR-032 fix D) so a headless run never
// stalls waiting on an interactive permission prompt with no TTY to answer it.
func TestOpencodeDriver_BuildArgs_AutoApprovePosture(t *testing.T) {
	d := NewOpencodeDriver(nil)
	args := d.buildArgs(RunOptions{Input: "task"})
	found := false
	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --dangerously-skip-permissions (ADR-032 fix D auto posture); args=%v", args)
	}
}

// TestCodexDriver_TurnCompletedDoesNotEmitEnd is the M4 regression test (unit).
//
// Before the fix, every `turn.completed` event produced an EventKindEnd, so a
// multi-turn codex run emitted multiple End signals and corrupted the streamed
// transcript. turn.completed must be consumed (ok=false) — the single End is
// synthesized once at true completion. This asserts the parseLine contract.
func TestCodexDriver_TurnCompletedDoesNotEmitEnd(t *testing.T) {
	d := NewCodexDriver(nil)
	turnCount := 0
	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}`)

	ev, ok := d.parseLine(line, "run-x", &turnCount, defaultMaxTurns)
	if ok {
		t.Fatalf("turn.completed must be consumed (ok=false); got ok=true, ev=%+v", ev)
	}
	if ev.Kind == EventKindEnd {
		t.Fatal("turn.completed must NOT emit EventKindEnd (M4) — End fires once at true completion")
	}
	if turnCount != 1 {
		t.Fatalf("turn.completed must still increment the turn counter; got %d, want 1", turnCount)
	}
}

// TestCodexDriver_MultiTurnYieldsSingleEnd is the M4 regression test (integration
// over the offline parse path). A codex stream with TWO turn.completed events must
// yield exactly ONE terminal End, synthesized at completion — not one per turn.
func TestCodexDriver_MultiTurnYieldsSingleEnd(t *testing.T) {
	stream := "" +
		`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"first"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}` + "\n" +
		`{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"second"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":12,"output_tokens":6}}` + "\n"

	events := ParseCodexStreamJSON([]byte(stream), "run-multi")

	endCount := 0
	for _, ev := range events {
		if ev.Kind == EventKindEnd {
			endCount++
		}
	}
	if endCount != 1 {
		t.Fatalf("multi-turn codex stream must yield exactly ONE End (M4); got %d. events=%+v", endCount, events)
	}
	// The single End must be the LAST event (true completion).
	if events[len(events)-1].Kind != EventKindEnd {
		t.Fatalf("the terminal End must be the final event; last=%+v", events[len(events)-1])
	}
}

func envContainsKey(env []string, key string) bool {
	for _, kv := range env {
		if eq := strings.IndexByte(kv, '='); eq > 0 && kv[:eq] == key {
			return true
		}
	}
	return false
}
