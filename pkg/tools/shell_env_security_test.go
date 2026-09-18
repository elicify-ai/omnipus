// shell_env_security_test.go — regression pins for the ADR-090 per-turn bash
// environment composition, written against runtime-final-review.md R1 (major,
// must-fix) and R4. R2/R3 disposition pins are added to this file together
// with the R2 signature change so every run compiles.
//
// Sentinel discipline: only obviously fake sentinel values are created; no
// real credential material is ever generated, and failure messages report key
// names and boolean checks, never a dump of the host environment.

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// Fake sentinel credentials. KEY NAMES are chosen so the v0.2 #155 child-env
// allowlist (allowedChildEnvKeys plus the LC_/XDG_/OMNIPUS_CHILD_ prefixes)
// denies both names.
const (
	sentinelKeyA = "OMNIPUS_REVIEW_SENTINEL_SECRET"
	sentinelValA = "sentinel-secret-value-not-real-A"
	sentinelKeyB = "SENTINEL_REVIEW_BEARER_TOKEN"
	sentinelValB = "sentinel-bearer-value-not-real-B"
)

// setHostSentinels plants the fake sentinel credentials in the TEST PROCESS
// environment — the environ a nil base would materialize on the leaky path.
func setHostSentinels(t *testing.T) {
	t.Helper()
	t.Setenv(sentinelKeyA, sentinelValA)
	t.Setenv(sentinelKeyB, sentinelValB)
}

// assertNoSentinelInEnv fails naming the leaked KEY only (never the value,
// never a dump of the environment).
func assertNoSentinelInEnv(t *testing.T, env []string) {
	t.Helper()
	for _, kv := range env {
		if strings.HasPrefix(kv, sentinelKeyA+"=") || strings.HasPrefix(kv, sentinelKeyB+"=") {
			t.Fatalf("host sentinel key leaked into the composed child env: %q", kv[:strings.IndexByte(kv, '=')])
		}
	}
}

// assertComposedEnvWithinScrubbedBaseline is the R1 contract: the composed
// environment may contain ONLY keys from the scrubbed child baseline
// (sandbox.ScrubGatewayEnv) plus the composed PATH entry itself.
func assertComposedEnvWithinScrubbedBaseline(t *testing.T, env []string) {
	t.Helper()
	scrubbedKeys := make(map[string]bool)
	for _, kv := range sandbox.ScrubGatewayEnv() {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			scrubbedKeys[kv[:eq]] = true
		}
	}
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		key := kv[:eq]
		if key != "PATH" && !scrubbedKeys[key] {
			t.Fatalf("composed env carries host key %q outside the scrubbed child baseline (R1 leak)", key)
		}
	}
}

// assertPATHShape checks the layering contract on the composed PATH: the
// workspace bin segment first, the inherited host PATH as the tail.
func assertPATHShape(t *testing.T, env []string, wsBin string) {
	t.Helper()
	pathValue, ok := envValue(env, "PATH")
	if !ok {
		t.Fatalf("composed env must carry PATH")
	}
	sep := string(os.PathListSeparator)
	if !strings.HasPrefix(pathValue, wsBin+sep) {
		t.Fatalf("composed PATH must lead with the workspace bin %q, got first segment %q", wsBin, firstSegment(pathValue, sep))
	}
	if hostPATH := os.Getenv("PATH"); hostPATH != "" && !strings.HasSuffix(pathValue, hostPATH) {
		t.Fatalf("composed PATH must keep the host PATH tail intact after the composed segments")
	}
}

func firstSegment(pathValue, sep string) string {
	if idx := strings.Index(pathValue, sep); idx >= 0 {
		return pathValue[:idx]
	}
	return pathValue
}

// workspaceEnvFixture creates <workspace>/.omnipus/env/bin with a fake
// managed-python script — the conventional layout storage's RuntimeEnvPaths
// discovers — and returns both paths.
func workspaceEnvFixture(t *testing.T) (ws, wsBin string) {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wsBin = filepath.Join(ws, ".omnipus", "env", "bin")
	if err := os.MkdirAll(wsBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeManagedPython(t, wsBin)
	return ws, wsBin
}

// TestComposeExecutionEnvNilBaseStaysWithinScrubbedBaseline is the direct R1
// assertion: with a nil baseline and a generic layer present, the composition
// must materialize the SCRUBBED gateway baseline — never the raw host
// environ. RED on the pre-fix source: full os.Environ() was materialized and
// the sentinels (denied by the #155 allowlist) were carried into the child
// env.
func TestComposeExecutionEnvNilBaseStaysWithinScrubbedBaseline(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)
	_, wsBin := workspaceEnvFixture(t)

	rt := environmentsetup.RuntimeEnv{BinDirs: []string{wsBin}}
	env := composeExecutionEnv(nil, documentEnvLayer{}, rt)

	if env == nil {
		t.Fatalf("composition over a generic layer must materialize the scrubbed baseline, not stay nil")
	}
	// Sentinel check first: its failure names the leaked sentinel key, giving
	// an unmistakable RED, before the broader baseline-membership check.
	assertNoSentinelInEnv(t, env)
	assertComposedEnvWithinScrubbedBaseline(t, env)
	assertPATHShape(t, env, wsBin)
}

// TestComposeExecutionEnvNilBaseNothingToComposeReturnsNil pins the retained
// pre-ADR-090 behavior: a nil baseline with nothing to compose stays nil
// (documented byte-identical contract, retained by mandate).
func TestComposeExecutionEnvNilBaseNothingToComposeReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)

	got := composeExecutionEnv(nil, documentEnvLayer{}, environmentsetup.RuntimeEnv{})
	if got != nil {
		t.Fatalf("nil base with nothing to compose must return nil, got %d entries", len(got))
	}
}

// TestComposeExecutionEnvBrokenDocLayerWithGenericLayerStaysScrubbed covers
// the review's "ordinary" R1 combination: the document layer is BROKEN
// (truthful notice, never injected) while a generic workspace layer composes
// — the exact pairing that used to materialize the raw host environ.
func TestComposeExecutionEnvBrokenDocLayerWithGenericLayerStaysScrubbed(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)
	_, wsBin := workspaceEnvFixture(t)

	doc := documentEnvLayer{notice: "document runtime is not ready: fake reason"}
	rt := environmentsetup.RuntimeEnv{BinDirs: []string{wsBin}}

	env := composeExecutionEnv(nil, doc, rt)
	assertNoSentinelInEnv(t, env)
	assertComposedEnvWithinScrubbedBaseline(t, env)
	assertPATHShape(t, env, wsBin)
	// The broken doc layer must not inject its managed prefix either.
	if pathValue, _ := envValue(env, "PATH"); strings.Contains(pathValue, "toolchains/documents") {
		t.Fatalf("broken doc layer must keep its prefix out of the composed PATH")
	}
}

// TestComposeExecutionEnvDocReadyChildEnvStaysFiltered pins the doc-ready
// composition over a nil base: documentruntime.ChildEnvironment's filtered
// environment (non-nil by construction) carries no host sentinel and no
// generic injection changed it.
func TestComposeExecutionEnvDocReadyChildEnvStaysFiltered(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)

	layout := provisionLayout(t, home, "mia")
	doc := documentEnvLayer{layout: layout, ok: true}
	env := composeExecutionEnv(nil, doc, environmentsetup.RuntimeEnv{})

	assertNoSentinelInEnv(t, env)
	if pathValue, _ := envValue(env, "PATH"); !strings.HasPrefix(pathValue, layout.Bin) {
		t.Fatalf("doc-ready PATH must lead with the managed bin %q", layout.Bin)
	}
}

// TestComposeExecutionEnvBackgroundBaselineStaysScrubbed pins the background
// (non-god) spawn path: the sandboxLimitsEnv baseline is already scrubbed and
// the composition over it keeps it that way while preserving the proxy and
// npm injections that baseline exists to carry.
func TestComposeExecutionEnvBackgroundBaselineStaysScrubbed(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)
	ws, wsBin := workspaceEnvFixture(t)

	lim := sandbox.Limits{EgressProxyAddr: "127.0.0.1:9", WorkspaceDir: ws}
	base := sandboxLimitsEnv(lim)
	env := composeExecutionEnv(base, documentEnvLayer{}, environmentsetup.RuntimeEnv{BinDirs: []string{wsBin}})

	assertNoSentinelInEnv(t, env)
	for _, want := range []string{"HTTP_PROXY=http://127.0.0.1:9", "npm_config_cache=" + ws + "/.npm-cache"} {
		found := false
		for _, kv := range env {
			if kv == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("background baseline injection %q missing after composition", want)
		}
	}
	if pathValue, _ := envValue(env, "PATH"); !strings.HasPrefix(pathValue, wsBin+string(os.PathListSeparator)) {
		t.Fatalf("background composed PATH must lead with the workspace bin")
	}
}

// TestComposeExecutionEnvGodBaselinePreservesOperatorEnv pins the retained
// god-mode semantics (R4): the operator environment survives the composition
// by design; only gateway credential material is stripped beforehand.
func TestComposeExecutionEnvGodBaselinePreservesOperatorEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)
	t.Setenv("SENTINEL_REVIEW_OPERATOR_PREF", "operator-pref-survives-god-mode")
	t.Setenv("OMNIPUS_MASTER_KEY", "fake-master-key-not-real")
	t.Setenv("OMNIPUS_KEY_FILE", "/tmp/fake-keyfile-sentinel")
	t.Setenv("OMNIPUS_BEARER_TOKEN", "fake-bearer-not-real")
	_, wsBin := workspaceEnvFixture(t)

	env := composeExecutionEnv(scrubbedEnv(os.Environ()), documentEnvLayer{}, environmentsetup.RuntimeEnv{BinDirs: []string{wsBin}})

	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "OMNIPUS_MASTER_KEY="),
			strings.HasPrefix(kv, "OMNIPUS_KEY_FILE="),
			strings.HasPrefix(kv, "OMNIPUS_BEARER_TOKEN="):
			t.Fatalf("gateway credential key leaked on the god path: %q", kv[:strings.IndexByte(kv, '=')])
		}
	}
	foundOperatorKey := false
	for _, kv := range env {
		if kv == "SENTINEL_REVIEW_OPERATOR_PREF=operator-pref-survives-god-mode" {
			foundOperatorKey = true
			break
		}
	}
	if !foundOperatorKey {
		t.Fatalf("god mode must preserve the operator environment by design; operator sentinel key missing")
	}
	assertPATHShape(t, env, wsBin)
}

// --- real-child assertions (foreground + background) -------------------------

// TestBashForegroundChildEnvScrubbedWhenGenericLayerComposesWithoutDocLayer
// proves R1 through a REAL sandboxed foreground child: doc layer absent
// (documentRuntime nil), workspace .omnipus/env present — the composition
// must not hand the child the host environ.
func TestBashForegroundChildEnvScrubbedWhenGenericLayerComposesWithoutDocLayer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)
	ws, wsBin := workspaceEnvFixture(t)

	// No SetDocumentRuntime: the doc layer is absent entirely (documentEnvLayer{}).
	tool, err := NewExecToolWithDeps(ws, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	command := `printf 'sentinelA=%s\nsentinelB=%s\npath=%s\n' "$OMNIPUS_REVIEW_SENTINEL_SECRET" "$SENTINEL_REVIEW_BEARER_TOKEN" "$PATH"`
	result := tool.Execute(bashCtx(t), map[string]any{"command": command})
	if result.IsError {
		t.Fatalf("command must run: %s", result.ForLLM)
	}
	assertChildEnvReport(t, result.ForLLM, wsBin)
}

// TestBashForegroundChildEnvScrubbedWhenDocRuntimeBroken proves R1 with a
// WIRED but BROKEN document runtime (corrupt converter registry): the
// truthful notice must accompany the result and the child env must stay
// scrubbed while the generic layer composes.
func TestBashForegroundChildEnvScrubbedWhenDocRuntimeBroken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)
	ws, wsBin := workspaceEnvFixture(t)

	layout := provisionLayout(t, home, "mia")
	registry := documentruntime.ConverterServiceRegistryDir(layout)
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registry, "services.rdb"), []byte(`<components>`), 0o644); err != nil {
		t.Fatal(err)
	}
	tool, err := NewExecToolWithDeps(ws, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.SetDocumentRuntime(layout)

	command := `printf 'sentinelA=%s\nsentinelB=%s\npath=%s\n' "$OMNIPUS_REVIEW_SENTINEL_SECRET" "$SENTINEL_REVIEW_BEARER_TOKEN" "$PATH"`
	result := tool.Execute(bashCtx(t), map[string]any{"command": command})
	if result.IsError {
		t.Fatalf("command must run despite the broken optional doc runtime: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "document runtime is not ready") {
		t.Fatalf("broken doc runtime must be reported truthfully: %s", result.ForLLM)
	}
	assertChildEnvReport(t, result.ForLLM, wsBin)
}

// TestBashBackgroundChildEnvStaysScrubbed pins the background spawn path end
// to end: already scrubbed before this lane's fix (sandboxLimitsEnv baseline)
// and must stay scrubbed after it, with the generic layer still composing.
func TestBashBackgroundChildEnvStaysScrubbed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	setHostSentinels(t)
	ws, wsBin := workspaceEnvFixture(t)

	tool, err := NewExecToolWithDeps(ws, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	tool.sessionManager = &SessionManager{sessions: make(map[string]*ProcessSession)}
	t.Cleanup(func() { tool.sessionManager.KillAll() })

	command := `printf 'sentinelA=%s\nsentinelB=%s\npath=%s\n' "$OMNIPUS_REVIEW_SENTINEL_SECRET" "$SENTINEL_REVIEW_BEARER_TOKEN" "$PATH" > env-report.txt`
	result := tool.Execute(bashCtx(t), map[string]any{"command": command, "run_in_background": true})
	if result.IsError {
		t.Fatalf("background start must succeed: %s", result.ForLLM)
	}

	report := filepath.Join(ws, "env-report.txt")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(report); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background child never wrote its env report")
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	assertChildEnvReport(t, string(data), wsBin)
}

// assertChildEnvReport checks a child-printed env report: sentinels UNSET,
// workspace bin present on the child PATH, host PATH tail intact. Values are
// fake sentinels; the failure path never prints the whole report.
func assertChildEnvReport(t *testing.T, output, wsBin string) {
	t.Helper()
	// The child prints `sentinelA=%s` with plain `$VAR` expansion: unset
	// expands EMPTY, so a leak shows up as a non-empty value. The prefix
	// check requires both lines present AND empty.
	if !strings.HasPrefix(output, "sentinelA=\nsentinelB=\n") {
		t.Fatalf("child env report shows a non-empty sentinel line — R1 leak through the real child")
	}
	pathValue := extractEnvVar(output, "path=")
	if !strings.Contains(pathValue, wsBin) {
		t.Fatalf("child PATH must still carry the workspace env bin %q (composition must keep working)", wsBin)
	}
	if hostPATH := os.Getenv("PATH"); hostPATH != "" && !strings.HasSuffix(pathValue, hostPATH) {
		t.Fatalf("child PATH must keep the host PATH tail intact")
	}
}

// TestScrubbedEnvGodModeCrossCheckAgainstChildAllowlist is the R4 disposition
// pin: everything god mode strips (the three gateway credential keys) is ALSO
// stripped by the real boundary — the v0.2 #155 child-env allowlist. If the
// allowlist ever starts passing one of those keys through, this fires before
// the god-mode scrub's narrower list can drift below the authority. God mode
// PRESERVING the operator environment is intentional and pinned separately.
func TestScrubbedEnvGodModeCrossCheckAgainstChildAllowlist(t *testing.T) {
	t.Setenv("OMNIPUS_MASTER_KEY", "fake-master-key-not-real")
	t.Setenv("OMNIPUS_KEY_FILE", "/tmp/fake-keyfile-sentinel")
	t.Setenv("OMNIPUS_BEARER_TOKEN", "fake-bearer-not-real")
	t.Setenv("SENTINEL_REVIEW_OPERATOR_PREF", "operator-pref-survives-god-mode")

	godScrubbed := scrubbedEnv(os.Environ())
	for _, kv := range godScrubbed {
		switch {
		case strings.HasPrefix(kv, "OMNIPUS_MASTER_KEY="),
			strings.HasPrefix(kv, "OMNIPUS_KEY_FILE="),
			strings.HasPrefix(kv, "OMNIPUS_BEARER_TOKEN="):
			t.Fatalf("god-mode scrub failed to strip a gateway credential key")
		}
	}
	allowlisted := sandbox.ScrubGatewayEnv()
	for _, kv := range allowlisted {
		if strings.HasPrefix(kv, "OMNIPUS_MASTER_KEY=") || strings.HasPrefix(kv, "OMNIPUS_KEY_FILE=") || strings.HasPrefix(kv, "OMNIPUS_BEARER_TOKEN=") {
			t.Fatalf("child-env allowlist passes a gateway credential key; the god-mode three-key scrub has drifted below the real boundary")
		}
	}
	found := false
	for _, kv := range godScrubbed {
		if kv == "SENTINEL_REVIEW_OPERATOR_PREF=operator-pref-survives-god-mode" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("god mode must preserve the operator environment by design; benign operator key was stripped")
	}
}

// --- R2/R3 policy pins (with the single-snapshot signature) ------------------

// ctxForWorkspace builds the turn context the policy pins use.
func ctxForWorkspace(workspace string) context.Context {
	return WithTurnWorkspaceDir(WithAgentID(context.Background(), "mia"), workspace)
}

// TestTurnKernelPolicyUsesCallerResolvedRuntimeSnapshot pins R2: the kernel
// policy rides the SAME resolved RuntimeEnv the environment composition used.
// Passing the zero RuntimeEnv — the resolution-failure snapshot — while the
// workspace env IS resolvable on disk must compose NO generic rules: a second,
// later re-read would let the env PATH carry bins the kernel never granted
// (EACCES on installed tools) or vice versa. Re-introducing the internal
// re-resolution inside augmentKernelPolicy makes this test fail (mutation M3).
func TestTurnKernelPolicyUsesCallerResolvedRuntimeSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	ws, _ := workspaceEnvFixture(t)

	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })

	tool, err := NewExecToolWithDeps(ws, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	ctx := ctxForWorkspace(ws)

	// The resolution-failure snapshot (zero RuntimeEnv) composes no generic
	// rules — even though the workspace env exists on disk and a re-read
	// would find it.
	zeroSnapshot := environmentsetup.RuntimeEnv{}
	policy, err := tool.turnKernelPolicy(ctx, ws, zeroSnapshot, documentEnvLayer{})
	if err != nil {
		t.Fatalf("zero-snapshot policy derivation failed: %v", err)
	}
	for _, rule := range policy.FilesystemRules {
		if strings.Contains(rule.Path, ".omnipus") {
			t.Fatalf("zero-snapshot policy must compose no generic rules, got %q", rule.Path)
		}
	}

	// The resolved snapshot composes the workspace env rules the env
	// composition's PATH segments actually need: read+execute on the env
	// subtree.
	resolved, err := runtimeEnvTurn(ws)
	if err != nil {
		t.Fatalf("resolve runtime env: %v", err)
	}
	policy, err = tool.turnKernelPolicy(ctx, ws, resolved, documentEnvLayer{})
	if err != nil {
		t.Fatalf("resolved-snapshot policy derivation failed: both snapshots ride one read: %v", err)
	}
	found := false
	for _, rule := range policy.FilesystemRules {
		if rule.Path == filepath.Join(ws, ".omnipus", "env") &&
			rule.Access&sandbox.AccessRead != 0 && rule.Access&sandbox.AccessExecute != 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("resolved snapshot must grant read+execute on the workspace env subtree")
	}
}

// TestAugmentKernelPolicyKeepsTurnWorkspaceWriteOverEnvSubtree pins the
// deliberate non-stripping property (review test-gap #2, R3): the generic
// layer's rules must NOT strip the covering workspace write rule — Landlock
// grants by union, so stripping it would silently revoke write across the
// whole turn workspace.
func TestAugmentKernelPolicyKeepsTurnWorkspaceWriteOverEnvSubtree(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	ws, _ := workspaceEnvFixture(t)

	base := sandbox.SandboxPolicy{FilesystemRules: []sandbox.PathRule{
		{Path: ws, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute},
	}}
	tool, err := NewExecToolWithDeps(ws, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := runtimeEnvTurn(ws)
	if err != nil {
		t.Fatalf("resolve runtime env: %v", err)
	}
	augmented, err := tool.augmentKernelPolicy(base, ws, rt, documentEnvLayer{})
	if err != nil {
		t.Fatalf("augment failed: %v", err)
	}

	if !anyRuleGrantsWriteOver(augmented.FilesystemRules, ws) {
		t.Fatalf("turn workspace write rule must survive augmentation (stripping it revokes the whole workspace)")
	}
	if !anyRuleGrantsWriteOver(augmented.FilesystemRules, filepath.Join(ws, ".omnipus", "env")) {
		t.Fatalf("workspace env subtree must remain writable through the preserved workspace rule")
	}
	if len(augmented.UnixSocketRules) != 1 || augmented.UnixSocketRules[0].Path != filepath.Clean(ws) {
		t.Fatalf("augment must keep exactly the cwd socket rule: %+v", augmented.UnixSocketRules)
	}
}

// TestAugmentKernelPolicyStripsWriteOnSharedStoreAncestorRule pins the R3
// DISPOSITION — the conservative broad strip — and its honest limitation:
//
//  1. With a base write rule that is an ANCESTOR of a published shared
//     generation (operator-misconfiguration posture: e.g. write over
//     $OMNIPUS_HOME), the strip clears that ancestor's write. If it was the
//     ONLY write coverage for the turn cwd inside that tree, augmentation
//     FAILS LOUDLY (os.ErrPermission) rather than silently spawning with the
//     wider boot profile — the availability cost of not weakening shared-store
//     isolation, surfaced as an honest refusal.
//  2. With a normal posture — a separate workspace write rule alongside the
//     ancestor — augmentation succeeds, the ancestor's write stays stripped
//     over the generation, and the workspace write survives.
//
// No rule-subtraction machinery is attempted: a rule split that is wrong
// under lexical-vs-resolved path mismatch would silently leave write on a
// path resolving INTO the store — worse than this availability cost.
func TestAugmentKernelPolicyStripsWriteOnSharedStoreAncestorRule(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvHome, home)
	unlockPublished(t, home)

	// A real published shared generation through storage's lifecycle API, so
	// the fixture cannot drift from the runtime reader's layout.
	target, err := environmentsetup.BeginInstall(home, "", environmentsetup.ScopeShared)
	if err != nil {
		t.Fatalf("begin shared install: %v", err)
	}
	writeFakeManagedPython(t, filepath.Join(target.Prefix(), "bin"))
	published, err := target.Commit()
	if err != nil {
		t.Fatalf("commit shared generation: %v", err)
	}

	// The turn workspace INSIDE home: an ancestor rule over home covers both
	// the workspace and the shared store.
	ws := filepath.Join(home, "workspaces", "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	rt, err := runtimeEnvTurn(ws)
	if err != nil {
		t.Fatalf("resolve runtime env: %v", err)
	}
	if len(rt.Shared) == 0 {
		t.Fatalf("fixture: published generation missing from the runtime view")
	}
	tool, err := NewExecToolWithDeps(ws, true, nil, ExecToolDeps{GodMode: false})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Ancestor-only write coverage: the strip removes the only write
	// coverage for the cwd → loud refusal, never a silent wider spawn.
	ancestorOnly := sandbox.SandboxPolicy{FilesystemRules: []sandbox.PathRule{
		{Path: home, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute},
	}}
	_, err = tool.augmentKernelPolicy(ancestorOnly, ws, rt, documentEnvLayer{})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("stripped-ancestor-only coverage must fail loudly with os.ErrPermission, got %v", err)
	}

	// 2. Normal posture: separate workspace write rule survives the strip.
	normal := sandbox.SandboxPolicy{FilesystemRules: []sandbox.PathRule{
		{Path: home, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute},
		{Path: ws, Access: sandbox.AccessRead | sandbox.AccessWrite | sandbox.AccessExecute},
	}}
	augmented, err := tool.augmentKernelPolicy(normal, ws, rt, documentEnvLayer{})
	if err != nil {
		t.Fatalf("normal posture must augment cleanly: %v", err)
	}
	if anyRuleGrantsWriteOver(augmented.FilesystemRules, published.Dir) {
		t.Fatalf("no rule may carry write over the shared generation after the ancestor strip")
	}
	if !anyRuleGrantsWriteOver(augmented.FilesystemRules, ws) {
		t.Fatalf("workspace write rule must survive the ancestor strip")
	}
}

// anyRuleGrantsWriteOver reports whether any final rule carries WRITE over
// path (helper for the R3 pins; mirrors grantsWriteOver across a rule list).
func anyRuleGrantsWriteOver(rules []sandbox.PathRule, path string) bool {
	for _, rule := range rules {
		if grantsWriteOver(rule, path) {
			return true
		}
	}
	return false
}
