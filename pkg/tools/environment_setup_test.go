package tools

// Behavioral tests for the generic environment_setup tool (founder decision
// GENERIC INSTALL OPTION A, 2026-09-18). Everything on the boundary is real:
// real child processes through the shared SessionManager, real installation
// areas through the production storage lifecycle (pkg/environmentsetup,
// adapted behind the tool's EnvironmentSetupStore seam exactly the way the
// pkg/agent wiring adapts it in production), and a real audit logger whose
// JSONL the tests read back. No mocks on the boundary.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestEnvironmentSetup' -p 1 ./pkg/tools/

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
)

// storageSeamStore adapts the production storage package to the tool's
// EnvironmentSetupStore seam — the same adaptation the pkg/agent wiring
// performs in production, duplicated here so tests exercise real storage
// semantics (real directories, real publication, real abort) rather than a
// pretend store.
type storageSeamStore struct{}

func (storageSeamStore) BeginInstall(appDataRoot, workspaceRoot, scope string) (EnvironmentSetupTarget, error) {
	target, err := environmentsetup.BeginInstall(appDataRoot, workspaceRoot, environmentsetup.Scope(scope))
	if err != nil {
		return nil, err
	}
	return storageSeamTarget{inner: target}, nil
}

type storageSeamTarget struct{ inner *environmentsetup.Target }

func (a storageSeamTarget) Scope() string           { return string(a.inner.Scope()) }
func (a storageSeamTarget) Prefix() string          { return a.inner.Prefix() }
func (a storageSeamTarget) Cache() string           { return a.inner.Cache() }
func (a storageSeamTarget) Tmp() string             { return a.inner.Tmp() }
func (a storageSeamTarget) WritableRoots() []string { return a.inner.Grant().Writable }

func (a storageSeamTarget) Commit() (string, string, error) {
	shared, err := a.inner.Commit()
	if err != nil {
		return "", "", err
	}
	return shared.Generation, shared.Dir, nil
}

func (a storageSeamTarget) Abort() error { return a.inner.Abort() }

// setupTestHarness builds one tool instance over an isolated OMNIPUS_HOME.
type setupTestHarness struct {
	home     string
	tool     *EnvironmentSetupTool
	auditDir string
}

func newSetupHarness(t *testing.T, admin bool) *setupTestHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("environment_setup tests use POSIX shell scripts")
	}
	home := t.TempDir()
	agentDir := filepath.Join(home, "agents", "testagent")
	require.NoError(t, os.MkdirAll(agentDir, 0o755))
	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{
		Home:            home,
		AgentWorkDir:    agentDir,
		Admin:           admin,
		GodMode:         true, // no kernel sandbox needed here; confinement is the sandbox package's own coverage
		Store:           storageSeamStore{},
		AuditFailClosed: true,
	})
	auditDir := t.TempDir()
	auditLog, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = auditLog.Close() })
	tool.SetAuditLogger(auditLog)
	return &setupTestHarness{home: home, tool: tool, auditDir: auditDir}
}

// ctx layers the turn facts the loop normally carries: agent, workspace, the
// workspace directory the tools root into, and the owning transcript session.
func (h *setupTestHarness) ctx(agentID, wsID, wsDir, transcript string) context.Context {
	ctx := WithToolContext(context.Background(), "cli", "chat")
	ctx = WithAgentID(ctx, agentID)
	ctx = WithWorkspaceID(ctx, wsID)
	if wsDir != "" {
		ctx = WithTurnWorkspaceDir(ctx, wsDir)
	}
	return WithTranscriptSessionID(ctx, transcript)
}

// auditContents reads back every JSONL entry the harness's logger wrote.
func (h *setupTestHarness) auditContents(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(h.auditDir, "*.jsonl"))
	require.NoError(t, err)
	all := make([]byte, 0, len(files)*1024)
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		all = append(all, b...)
	}
	return string(all)
}

// startAndWait runs action=run through ExecuteAsync and blocks until the
// terminal completion callback fires.
func (h *setupTestHarness) startAndWait(t *testing.T, ctx context.Context, args map[string]any) (start, completion *ToolResult) {
	t.Helper()
	done := make(chan *ToolResult, 1)
	start = h.tool.ExecuteAsync(ctx, args, func(_ context.Context, result *ToolResult) { done <- result })
	require.NotNil(t, start)
	select {
	case completion = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("environment_setup completion callback never fired")
	}
	require.NotNil(t, completion)
	return start, completion
}

// ES-FR-03/04: a new shared generation can use a helper from an earlier,
// already-published generation by bare name. The expected output is set by
// the first generic script, not inferred from setupEnv's implementation.
func TestEnvironmentSetup_SharedGenerationReusesEarlierRuntime(t *testing.T) {
	h := newSetupHarness(t, false)
	unlockPublished(t, h.home)
	ctx := h.ctx("mia", "", "", "t-shared-reuse-1")

	_, first := h.startAndWait(t, ctx, map[string]any{
		"command": `set -e
mkdir -p "$OMNIPUS_ENV_PREFIX/bin"
printf '#!/bin/sh\necho earlier-generation-helper\n' > "$OMNIPUS_ENV_PREFIX/bin/prior-helper"
chmod +x "$OMNIPUS_ENV_PREFIX/bin/prior-helper"`,
		"purpose": "install a generic shared helper",
		"scope":   "shared",
	})
	require.False(t, first.IsError, "first shared generation failed: %s", first.ForLLM)

	_, second := h.startAndWait(t, ctx, map[string]any{
		"command": `set -e
prior-helper > "$OMNIPUS_ENV_PREFIX/reused.txt"`,
		"purpose": "reuse earlier generic shared helper",
		"scope":   "shared",
	})
	require.False(t, second.IsError, "later shared generation could not use the earlier helper: %s", second.ForLLM)

	// The second target is the newest published generation. Its result file
	// proves PATH resolved the prior generation rather than a host command.
	var published struct {
		Generations []string `json:"generations"`
	}
	raw, err := os.ReadFile(environmentsetup.SharedPublishedSetPath(h.home))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &published))
	require.Len(t, published.Generations, 2)
	result, err := os.ReadFile(filepath.Join(environmentsetup.SharedGenerationDir(h.home, published.Generations[0]), "reused.txt"))
	require.NoError(t, err)
	assert.Equal(t, "earlier-generation-helper\n", string(result))
}

// extractSessionID decodes the leading session JSON of a start result.
func extractSessionID(t *testing.T, forLLM string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(forLLM))
	var resp ExecResponse
	require.NoError(t, dec.Decode(&resp), "start result must lead with the session JSON; got %q", forLLM)
	require.NotEmpty(t, resp.SessionID, "no sessionId in %q", forLLM)
	return resp.SessionID
}

// extractPrefix pulls the resolved prefix path out of a start result's
// notice line ("[environment_setup] prefix: <path>") — the tool's own
// statement of where the installation area is.
func extractPrefix(t *testing.T, forLLM string) string {
	t.Helper()
	for _, line := range strings.Split(forLLM, "\n") {
		if idx := strings.Index(line, "[environment_setup] prefix: "); idx >= 0 {
			return strings.TrimSpace(line[idx+len("[environment_setup] prefix: "):])
		}
	}
	t.Fatalf("start result states no prefix; got %q", forLLM)
	return ""
}

// writeWorkspaceRecord plants the workspace record the Admin cross-workspace
// path requires under <home>/workspaces/.
func writeWorkspaceRecord(t *testing.T, home, id string) {
	t.Helper()
	record := filepath.Join(home, "workspaces", id+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(record), 0o755))
	require.NoError(t, os.WriteFile(record, []byte(`{"id":"`+id+`"}`), 0o644))
}

// --- run: authorized own-workspace installation ------------------------------

func TestEnvironmentSetup_Run_OwnWorkspace_InstallsIntoPrefix(t *testing.T) {
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-own-1")

	// Multiline inline script: verifies cwd, installs an arbitrary executable
	// into the prefix bin, writes through the granted cache, and verifies the
	// installed artifact by running it.
	script := `set -e
[ "$(pwd)" = "$OMNIPUS_ENV_PREFIX" ] || { echo "cwd is not the prefix" >&2; exit 9; }
mkdir -p "$OMNIPUS_ENV_PREFIX/bin" "$OMNIPUS_ENV_CACHE" "$OMNIPUS_ENV_TMP"
printf '#!/bin/sh\necho generic-install-ok\n' > "$OMNIPUS_ENV_PREFIX/bin/probe"
chmod +x "$OMNIPUS_ENV_PREFIX/bin/probe"
echo cached > "$OMNIPUS_ENV_CACHE/artifact.txt"
"$OMNIPUS_ENV_PREFIX/bin/probe" > "$OMNIPUS_ENV_TMP/verify.txt"
`
	start, completion := h.startAndWait(t, ctx, map[string]any{
		"command": script,
		"purpose": "install an arbitrary probe tool",
	})
	require.False(t, start.IsError, "start refused: %s", start.ForLLM)
	require.False(t, completion.IsError, "install failed: %s", completion.ForLLM)

	assert.Contains(t, completion.ForLLM, "finished (exit code 0)")
	prefix := filepath.Join(wsDir, ".omnipus", "env")
	assert.Contains(t, start.ForLLM, prefix, "start notice must state the resolved prefix")
	assert.Contains(t, completion.ForLLM, prefix, "completion notice must state the area")

	// The installed artifacts really landed in the granted area — and only
	// the session machinery knows the session by its ID.
	data, err := os.ReadFile(filepath.Join(prefix, "bin", "probe"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "generic-install-ok")
	_, err = os.Stat(filepath.Join(prefix, "cache", "artifact.txt"))
	require.NoError(t, err)

	// Poll after completion reports the terminal state.
	sessionID := extractSessionID(t, start.ForLLM)
	poll := h.tool.Execute(ctx, map[string]any{"action": "poll", "session_id": sessionID})
	assert.Contains(t, poll.ForLLM, `"status":"done"`)

	// Audit: an allow entry naming the purpose (and thereby the run).
	auditAll := h.auditContents(t)
	assert.Contains(t, auditAll, `"decision":"allow"`)
	assert.Contains(t, auditAll, `"purpose":"install an arbitrary probe tool"`)
}

func TestEnvironmentSetup_Run_UnknownPackageScript_NoCatalogue(t *testing.T) {
	// "frobnicate 9.2.1" exists in no package catalogue — and must not need
	// to: the command IS the dependency knowledge. A generic script that
	// installs any arbitrary tool must run exactly as supplied.
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-unknown-1")

	script := `set -e
mkdir -p "$OMNIPUS_ENV_PREFIX/bin"
printf '#!/bin/sh\necho frobnicate-9.2.1-ready\n' > "$OMNIPUS_ENV_PREFIX/bin/frobnicate"
chmod +x "$OMNIPUS_ENV_PREFIX/bin/frobnicate"
"$OMNIPUS_ENV_PREFIX/bin/frobnicate"
`
	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command": script,
		"purpose": "install frobnicate from an inline script",
	})
	require.False(t, completion.IsError, "unknown-package install must not be refused: %s", completion.ForLLM)
	assert.Contains(t, completion.ForLLM, "frobnicate-9.2.1-ready", "the script's own output must surface verbatim")
	assert.Contains(t, completion.ForLLM, "finished (exit code 0)")
}

// --- authority: cross-workspace targets (ES-FR-02/ES-BDD-03/ES-BDD-10) -------

func TestEnvironmentSetup_CrossWorkspace_NonAdmin_Refused(t *testing.T) {
	h := newSetupHarness(t, false)
	alphaDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(alphaDir, 0o755))
	writeWorkspaceRecord(t, h.home, "beta")
	ctx := h.ctx("mia", "alpha", alphaDir, "t-x-1")

	start := h.tool.Execute(ctx, map[string]any{
		"command":          "echo nope",
		"purpose":          "probe cross-workspace authority",
		"target_workspace": "beta",
	})
	require.NotNil(t, start)
	require.True(t, start.IsError, "non-Admin cross-workspace target must be refused")
	assert.Contains(t, start.ForLLM, "only Admin can target another workspace")

	// The audit trail shows WHO tried to reach WHICH workspace.
	auditAll := h.auditContents(t)
	assert.Contains(t, auditAll, `"decision":"deny"`)
	assert.Contains(t, auditAll, `"target_workspace":"beta"`)

	// No installation area was created anywhere for the refused request.
	_, err := os.Stat(filepath.Join(alphaDir, ".omnipus"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "refused request must not create an area")
}

func TestEnvironmentSetup_Admin_ExistingWorkspace_Targeted(t *testing.T) {
	h := newSetupHarness(t, true)
	writeWorkspaceRecord(t, h.home, "beta")
	betaDir := filepath.Join(h.home, "workspaces", "beta")
	require.NoError(t, os.MkdirAll(betaDir, 0o755))
	adminDir := filepath.Join(h.home, "workspaces", "admin-home")
	require.NoError(t, os.MkdirAll(adminDir, 0o755))
	// Admin runs inside its own workspace context and targets beta — without
	// membership, purely on the server-derived role.
	ctx := h.ctx("admin", "admin-home", adminDir, "t-a-1")

	start, completion := h.startAndWait(t, ctx, map[string]any{
		"command":          `echo targeted > "$OMNIPUS_ENV_PREFIX/where.txt"`,
		"purpose":          "install into beta on behalf of a workspace member",
		"target_workspace": "beta",
	})
	require.False(t, completion.IsError, "Admin targeting an existing workspace must succeed: %s", completion.ForLLM)
	// The artifact must land in the area the tool REPORTED (the tool states
	// its resolved prefix in the start notice), and that area must be inside
	// beta — beta's internal layout is the input lane's to evolve.
	prefix := extractPrefix(t, start.ForLLM)
	assert.Contains(t, prefix, filepath.Join("workspaces", "beta"),
		"the admin-targeted install must root inside beta")
	data, err := os.ReadFile(filepath.Join(prefix, "where.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "targeted")
}

func TestEnvironmentSetup_Admin_NonexistentWorkspace_Refused(t *testing.T) {
	h := newSetupHarness(t, true)
	adminDir := filepath.Join(h.home, "workspaces", "admin-home")
	require.NoError(t, os.MkdirAll(adminDir, 0o755))
	ctx := h.ctx("admin", "admin-home", adminDir, "t-a-2")

	start := h.tool.Execute(ctx, map[string]any{
		"command":          "echo x",
		"purpose":          "probe nonexistent workspace",
		"target_workspace": "ghostws",
	})
	require.NotNil(t, start)
	require.True(t, start.IsError, "Admin targeting a workspace with no record must be refused")
	assert.Contains(t, start.ForLLM, "does not exist")
}

func TestEnvironmentSetup_HostPathTarget_Refused(t *testing.T) {
	// Even Admin cannot smuggle a host path through target_workspace: the
	// strict identifier pattern refuses every path-shaped value.
	h := newSetupHarness(t, true)
	adminDir := filepath.Join(h.home, "workspaces", "admin-home")
	require.NoError(t, os.MkdirAll(adminDir, 0o755))
	ctx := h.ctx("admin", "admin-home", adminDir, "t-path-1")

	for _, bad := range []string{"/etc", "../outside", "sub/dir", ".", "..", "ws name"} {
		start := h.tool.Execute(ctx, map[string]any{
			"command":          "echo x",
			"purpose":          "probe path-shaped target",
			"target_workspace": bad,
		})
		require.NotNil(t, start, bad)
		require.True(t, start.IsError, "path-shaped target %q must be refused", bad)
		assert.Contains(t, start.ForLLM, "not a valid workspace identifier", bad)
	}
}

// --- run validation refusals --------------------------------------------------

func TestEnvironmentSetup_RunValidation_Refusals(t *testing.T) {
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-v-1")

	cases := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{"missing command", map[string]any{"purpose": "p"}, "command is required"},
		{"blank command", map[string]any{"command": "   ", "purpose": "p"}, "command is required"},
		{"missing purpose", map[string]any{"command": "echo x"}, "purpose is required"},
		{"blank purpose", map[string]any{"command": "echo x", "purpose": "  "}, "purpose is required"},
		{"oversized purpose", map[string]any{"command": "echo x", "purpose": strings.Repeat("p", 501)}, "purpose must be at most 500 characters"},
		{"zero timeout", map[string]any{"command": "echo x", "purpose": "p", "timeout_seconds": float64(0)}, "timeout_seconds must be between"},
		{"over-ceiling timeout", map[string]any{"command": "echo x", "purpose": "p", "timeout_seconds": float64(4000)}, "timeout_seconds must be between"},
		{"non-integer timeout", map[string]any{"command": "echo x", "purpose": "p", "timeout_seconds": "soon"}, "timeout_seconds must be a number"},
		{"invalid scope", map[string]any{"command": "echo x", "purpose": "p", "scope": "global"}, `scope must be "workspace" or "shared"`},
		{"shared scope with workspace target", map[string]any{"command": "echo x", "purpose": "p", "scope": "shared", "target_workspace": "alpha"}, "target_workspace selects a workspace-scope destination"},
		{"unknown action", map[string]any{"action": "deploy", "command": "echo x", "purpose": "p"}, "unknown action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := h.tool.Execute(ctx, tc.args)
			require.NotNil(t, start)
			require.True(t, start.IsError, "expected refusal, got: %s", start.ForLLM)
			assert.Contains(t, start.ForLLM, tc.wantErr)
		})
	}
	// Nothing was started: no area exists under the workspace.
	_, err := os.Stat(filepath.Join(wsDir, ".omnipus"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "refused runs must not create an installation area")
}

// setupDenyAuditEntries parses the harness's audit JSONL into entries.
func setupDenyAuditEntries(t *testing.T, h *setupTestHarness) []audit.Entry {
	t.Helper()
	var entries []audit.Entry
	for _, line := range strings.Split(strings.TrimSpace(h.auditContents(t)), "\n") {
		if line == "" {
			continue
		}
		var e audit.Entry
		require.NoError(t, json.Unmarshal([]byte(line), &e), "bad audit line: %s", line)
		entries = append(entries, e)
	}
	return entries
}

// assertDenyAuditBounded fails the test if the raw serialized audit record
// embeds the oversized payload or exceeds the bounded size — the whole-record
// guarantee behind the per-field preview bound.
func assertDenyAuditBounded(t *testing.T, rawLine string, payload string) {
	t.Helper()
	assert.Less(t, len(rawLine), 2048,
		"whole serialized deny record must stay bounded")
	assert.NotContains(t, rawLine, payload[:600],
		"no field of the deny record may embed the oversized payload")
}

// TestEnvironmentSetup_DenyAudit_OversizedCommandBounded: the deny path runs
// BEFORE the command-size cap, so its audit entry must record a bounded
// preview of the oversized command plus the original length — never the
// payload itself.
func TestEnvironmentSetup_DenyAudit_OversizedCommandBounded(t *testing.T) {
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-denyaudit-1")

	oversized := strings.Repeat("x", environmentSetupMaxCommandBytes+65)
	res := h.tool.Execute(ctx, map[string]any{
		"command": oversized,
		"purpose": "p",
	})
	require.True(t, res.IsError, "oversized command must be refused")
	assert.Contains(t, res.ForLLM, "command must be at most")

	lines := strings.Split(strings.TrimSpace(h.auditContents(t)), "\n")
	require.Len(t, lines, 1, "exactly one deny entry must be audited")
	var entry audit.Entry
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
	assert.Equal(t, audit.DecisionDeny, entry.Decision)
	assert.Contains(t, entry.Command, " [audit preview truncated; ")
	assert.Contains(t, entry.Command, fmt.Sprintf(" %d of %d bytes omitted]",
		len(oversized)-environmentSetupAuditDenyPreviewBytes, len(oversized)),
		"the record must state the original length")
	assertDenyAuditBounded(t, lines[0], oversized)
}

// TestEnvironmentSetup_DenyAudit_OversizedTargetBoundedEverywhere: on the
// disallowed-target path the reason echoes the requested target_workspace, so
// command, reason, and target_workspace must ALL ride the bound — no field of
// the serialized record carries the raw oversized string.
func TestEnvironmentSetup_DenyAudit_OversizedTargetBoundedEverywhere(t *testing.T) {
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-denyaudit-2")

	bigTarget := strings.Repeat("t", 4096)
	res := h.tool.Execute(ctx, map[string]any{
		"command":          "echo ok",
		"purpose":          "p",
		"scope":            "workspace",
		"target_workspace": bigTarget,
	})
	require.True(t, res.IsError, "invalid target must be refused")
	assert.Contains(t, res.ForLLM, "not a valid workspace identifier")

	entries := setupDenyAuditEntries(t, h)
	require.Len(t, entries, 1, "exactly the disallowed-target deny is audited")
	entry := entries[0]
	assert.Equal(t, audit.DecisionDeny, entry.Decision)
	assert.Equal(t, "echo ok", entry.Command,
		"a within-cap command stays verbatim on the deny entry")

	targetDetail, ok := entry.Details["target_workspace"].(string)
	require.True(t, ok, "target_workspace detail must be a string")
	assert.Contains(t, targetDetail, " [audit preview truncated; ")
	reason, ok := entry.Details["reason"].(string)
	require.True(t, ok, "reason detail must be a string")
	assert.Contains(t, reason, " [audit preview truncated; ",
		"the reason echoes the oversized target, so it must carry the truncation marker")

	assertDenyAuditBounded(t, h.auditContents(t), bigTarget)
}

func TestEnvironmentSetup_StoreNotWired_Refused(t *testing.T) {
	home := t.TempDir()
	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{
		Home:         home,
		AgentWorkDir: filepath.Join(home, "agents", "x"),
		GodMode:      true,
		// Store deliberately nil: installation is unavailable.
	})
	ctx := WithToolContext(context.Background(), "cli", "chat")
	ctx = WithAgentID(ctx, "mia")
	ctx = WithTranscriptSessionID(ctx, "t-nostore")
	start := tool.Execute(ctx, map[string]any{"command": "echo x", "purpose": "p"})
	require.NotNil(t, start)
	require.True(t, start.IsError)
	assert.Contains(t, start.ForLLM, "storage is not wired")
}

// --- session lifecycle: kill / timeout / read / ownership --------------------

func TestEnvironmentSetup_Kill_TerminatesRunningInstall(t *testing.T) {
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-kill-1")

	done := make(chan *ToolResult, 1)
	start := h.tool.ExecuteAsync(ctx, map[string]any{
		"command": "sleep 30 && echo should-never-run",
		"purpose": "long install that will be killed",
	}, func(_ context.Context, result *ToolResult) { done <- result })
	require.False(t, start.IsError, "start refused: %s", start.ForLLM)
	sessionID := extractSessionID(t, start.ForLLM)

	poll := h.tool.Execute(ctx, map[string]any{"action": "poll", "session_id": sessionID})
	assert.Contains(t, poll.ForLLM, `"status":"running"`)

	kill := h.tool.Execute(ctx, map[string]any{"action": "kill", "session_id": sessionID})
	require.False(t, kill.IsError, "kill failed: %s", kill.ForLLM)

	select {
	case completion := <-done:
		assert.Contains(t, completion.ForLLM, "was killed")
		assert.NotContains(t, completion.ForLLM, "should-never-run")
	case <-time.After(20 * time.Second):
		t.Fatal("kill did not produce a terminal completion")
	}
	// The session is terminal and labeled for what it is.
	after := h.tool.Execute(ctx, map[string]any{"action": "poll", "session_id": sessionID})
	assert.Contains(t, after.ForLLM, `"status":"killed"`)
}

func TestEnvironmentSetup_Timeout_KillsAndRelabels(t *testing.T) {
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-to-1")

	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command":         "sleep 30",
		"purpose":         "install that will time out",
		"timeout_seconds": float64(1),
	})
	assert.Contains(t, completion.ForLLM, "timed out")
}

func TestEnvironmentSetup_Read_BoundedOutput(t *testing.T) {
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-read-1")

	// ~2MB of output exceeds the 1MB session buffer: the read must come back
	// bounded with the truncation marker, not unbounded.
	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command": "head -c 2097152 /dev/zero | tr '\\0' 'X'",
		"purpose": "noisy install",
	})
	require.False(t, completion.IsError, "noisy install failed: %s", completion.ForLLM)
	sessionID := startSessionID(t, completion)

	read := h.tool.Execute(ctx, map[string]any{"action": "read", "session_id": sessionID})
	dec := json.NewDecoder(strings.NewReader(read.ForLLM))
	var resp ExecResponse
	require.NoError(t, dec.Decode(&resp))
	assert.Contains(t, resp.Output, outputTruncateMarker)
	assert.LessOrEqual(t, len(resp.Output), maxOutputBufferSize+len(outputTruncateMarker),
		"read output must stay bounded")
}

// startSessionID recovers the session ID from a completion result — the
// completion text leads with the session ID in its summary line.
func startSessionID(t *testing.T, completion *ToolResult) string {
	t.Helper()
	const marker = "Background session "
	i := strings.Index(completion.ForLLM, marker)
	require.NotEqual(t, -1, i, "completion must name the session: %q", completion.ForLLM)
	rest := completion.ForLLM[i+len(marker):]
	j := strings.Index(rest, " ")
	require.NotEqual(t, -1, j)
	return rest[:j]
}

func TestEnvironmentSetup_ForeignSession_CannotPollReadKill(t *testing.T) {
	h := newSetupHarness(t, false)
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ownerCtx := h.ctx("mia", "alpha", wsDir, "t-owner")
	foreignCtx := h.ctx("mia", "alpha", wsDir, "t-foreign")

	start := h.tool.ExecuteAsync(ownerCtx, map[string]any{
		"command": "sleep 30",
		"purpose": "ownership probe",
	}, func(_ context.Context, _ *ToolResult) {})
	require.False(t, start.IsError)
	sessionID := extractSessionID(t, start.ForLLM)

	for _, action := range []string{"poll", "read", "kill"} {
		res := h.tool.Execute(foreignCtx, map[string]any{"action": action, "session_id": sessionID})
		require.NotNil(t, res, action)
		require.True(t, res.IsError, "foreign %s must be refused", action)
		// Same indistinguishable not-found a truly unknown session gets — no
		// existence oracle for other transcripts' jobs.
		assert.Contains(t, res.ForLLM, "session not found", action)
	}
	// The owner can still manage it; clean up the sleeper.
	kill := h.tool.Execute(ownerCtx, map[string]any{"action": "kill", "session_id": sessionID})
	require.False(t, kill.IsError, "owner kill failed: %s", kill.ForLLM)
}

// --- shared scope: publication and abort -------------------------------------

// unlockSharedTree reverses storage's read-only lockdown so t.TempDir cleanup
// can remove the published tree (0555 dirs / 0444 files resist RemoveAll).
func unlockSharedTree(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			// Best-effort unlock: a path the walk could not stat is skipped,
			// and the walk continues so later paths still get unlocked.
			if err == nil {
				if d.IsDir() {
					_ = os.Chmod(path, 0o755)
				} else {
					_ = os.Chmod(path, 0o644)
				}
			}
			return nil
		})
	})
}

func TestEnvironmentSetup_SharedScope_Publishes_OnSuccess(t *testing.T) {
	h := newSetupHarness(t, false)
	unlockSharedTree(t, filepath.Join(h.home, "toolchains"))
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-shared-1")

	script := `set -e
mkdir -p "$OMNIPUS_ENV_PREFIX/bin"
printf '#!/bin/sh\necho shared-ok\n' > "$OMNIPUS_ENV_PREFIX/bin/sharedtool"
chmod +x "$OMNIPUS_ENV_PREFIX/bin/sharedtool"
echo cached > "$OMNIPUS_ENV_CACHE/c.txt"
`
	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command": script,
		"purpose": "publish a shared tool",
		"scope":   "shared",
	})
	require.False(t, completion.IsError, "shared install failed: %s", completion.ForLLM)
	assert.Contains(t, completion.ForLLM, "published: generation")

	// The publication record lists the generation (newest first).
	pubData, err := os.ReadFile(environmentsetup.SharedPublishedSetPath(h.home))
	require.NoError(t, err, "published.json must exist after a successful shared install")
	var pub struct {
		Generations []string `json:"generations"`
	}
	require.NoError(t, json.Unmarshal(pubData, &pub))
	require.NotEmpty(t, pub.Generations)

	// The published tree holds the installed artifacts, locked read-only.
	genDir := filepath.Join(h.home, "toolchains", "environment", "shared", pub.Generations[0])
	data, err := os.ReadFile(filepath.Join(genDir, "bin", "sharedtool"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "shared-ok")
	info, err := os.Stat(filepath.Join(genDir, "bin", "sharedtool"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o555), info.Mode().Perm(), "published executables keep the execute bit; write is removed")
	_, err = os.Stat(filepath.Join(genDir, ".omnipus-install.json"))
	require.NoError(t, err, "published generation carries the completeness marker")
	marker, err := os.Stat(filepath.Join(genDir, ".omnipus-install.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o444), marker.Mode().Perm(), "published non-executable files are read-only")
}

func TestEnvironmentSetup_SharedScope_Aborts_OnFailure(t *testing.T) {
	h := newSetupHarness(t, false)
	unlockSharedTree(t, filepath.Join(h.home, "toolchains"))
	wsDir := filepath.Join(h.home, "workspaces", "alpha")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	ctx := h.ctx("mia", "alpha", wsDir, "t-shared-2")

	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command": "echo boom >&2; exit 3",
		"purpose": "failing shared install",
		"scope":   "shared",
	})
	assert.Contains(t, completion.ForLLM, "finished (exit code 3)")
	assert.Contains(t, completion.ForLLM, "NOT published")

	// Nothing was published: no published.json (or no generations in it), and
	// the failed staging generation was removed.
	pubPath := environmentsetup.SharedPublishedSetPath(h.home)
	pubData, readErr := os.ReadFile(pubPath)
	if readErr == nil {
		var pub struct {
			Generations []string `json:"generations"`
		}
		require.NoError(t, json.Unmarshal(pubData, &pub))
		assert.Empty(t, pub.Generations, "a failed install must not publish")
	}
	sharedDir := filepath.Join(h.home, "toolchains", "environment", "shared")
	entries, err := os.ReadDir(sharedDir)
	if err == nil {
		for _, e := range entries {
			assert.False(t, strings.HasPrefix(e.Name(), "gen-") && e.IsDir(),
				"no unpublished generation directory may remain (found %s)", e.Name())
		}
	}
}
