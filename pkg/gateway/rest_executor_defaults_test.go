// rest_executor_defaults_test.go — coverage for GET /api/v1/agents/executor-defaults
// (Agent System ghost-text bug fix: exposes the REAL, byte-accurate per-CLI
// auto-applied flags that pkg/agent/runner/driver_{claude,codex,opencode}.go's
// buildArgs() actually appends, instead of leaving operators with only static
// HTML placeholder ghost-text in the Agent Profile UI).
//
// Drift-check design (agent-system-fixes-2 review, FIX 1): the original
// version of this file compared listExecutorDefaults' output against a SECOND,
// independently hand-maintained "want" slice — proving only that the two
// hardcoded copies agreed with each other, never that either matched the real
// drivers. TestListExecutorDefaults_{Claude,Codex,Opencode}MatchesRealBuildArgs
// below instead call the REAL buildArgs() on each driver (via the minimal
// cross-package wrappers in pkg/agent/runner/buildargs_crosspkg.go) with
// representative RunOptions that exercise every conditional flag, and assert
// the endpoint's AutoAppliedFlags entries appear, IN ORDER, in that real argv.
// A future driver edit that adds/removes/reorders/renames a flag — or changes
// a literal value listExecutorDefaults claims is fixed — fails these
// assertions instead of silently drifting from reality.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// executorDefaultsTestAPI builds a minimal restAPI. listExecutorDefaults is
// static reference data with no config/agent dependency, so an empty agent
// list is sufficient.
func executorDefaultsTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

func getExecutorDefaults(t *testing.T, api *restAPI) (int, []gen.ExecutorDefaults) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/executor-defaults", nil)
	api.HandleAgents(w, r)
	var resp []gen.ExecutorDefaults
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w.Code, resp
}

// findExecutorDefaultsEntry returns the entry for the given CLI, failing the
// test if absent.
func findExecutorDefaultsEntry(
	t *testing.T,
	entries []gen.ExecutorDefaults,
	cli gen.ExternalCliTool,
) gen.ExecutorDefaults {
	t.Helper()
	for _, e := range entries {
		if e.Cli == cli {
			return e
		}
	}
	t.Fatalf("no executor-defaults entry for cli %q", cli)
	return gen.ExecutorDefaults{}
}

// indexAtOrAfter returns the index of the first occurrence of token in
// haystack at or after from, or -1 if not found.
func indexAtOrAfter(haystack []string, from int, token string) int {
	for i := from; i < len(haystack); i++ {
		if haystack[i] == token {
			return i
		}
	}
	return -1
}

// indexSeqAtOrAfter returns the starting index of the first contiguous
// occurrence of seq in haystack at or after from, or -1 if not found.
func indexSeqAtOrAfter(haystack []string, from int, seq []string) int {
	if len(seq) == 0 {
		return -1
	}
	for i := from; i+len(seq) <= len(haystack); i++ {
		match := true
		for j, s := range seq {
			if haystack[i+j] != s {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// assertRealArgsMatchEndpoint walks the REAL argv produced by a driver's
// buildArgs() left-to-right, confirming every entry listExecutorDefaults
// advertises for that CLI appears, in order:
//   - a LITERAL entry (no "<placeholder>" text, e.g. "--output-format
//     stream-json" or "exec") must appear as an exact, contiguous token
//     sequence — this is a byte-accuracy check on both the flag name and its
//     fixed value.
//   - a CONDITIONAL entry (contains "<...>", e.g. "--model <configured
//     model> (only when a model is configured)") only requires its leading
//     flag token (the first whitespace-separated word) to appear — the value
//     is runtime-dependent and documented as a template, not literal argv
//     text.
//
// Because each lookup only searches at-or-after the cursor left by the
// previous entry, this also enforces ORDERING: a driver change that reorders
// two flags (e.g. moving codex's --sandbox before --ask-for-approval, or
// moving it after `exec`) fails here even though every individual flag is
// still present.
func assertRealArgsMatchEndpoint(t *testing.T, cli string, realArgs []string, wantEntries []string) {
	t.Helper()
	cursor := 0
	for _, entry := range wantEntries {
		fields := strings.Fields(entry)
		require.NotEmptyf(t, fields, "cli %s: empty AutoAppliedFlags entry", cli)
		conditional := strings.Contains(entry, "<")
		if conditional {
			idx := indexAtOrAfter(realArgs, cursor, fields[0])
			require.GreaterOrEqualf(
				t,
				idx,
				0,
				"cli %s: conditional flag %q (from advertised entry %q) not found in real buildArgs() output %v at/after position %d — driver_%s.go's buildArgs may have drifted from listExecutorDefaults",
				cli,
				fields[0],
				entry,
				realArgs,
				cursor,
				cli,
			)
			cursor = idx + 1
			continue
		}
		idx := indexSeqAtOrAfter(realArgs, cursor, fields)
		require.GreaterOrEqualf(
			t,
			idx,
			0,
			"cli %s: literal flag sequence %v (from advertised entry %q) not found in real buildArgs() output %v at/after position %d — driver_%s.go's buildArgs may have drifted from listExecutorDefaults",
			cli,
			fields,
			entry,
			realArgs,
			cursor,
			cli,
		)
		cursor = idx + len(fields)
	}
}

// TestListExecutorDefaults_ReturnsAllThreeCLIs proves the endpoint returns
// exactly one entry per supported subagent_3p external CLI, each with a
// non-empty, ordered flag list and a non-empty note.
func TestListExecutorDefaults_ReturnsAllThreeCLIs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, entries := getExecutorDefaults(t, api)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, entries, 3)

	seen := map[gen.ExternalCliTool]gen.ExecutorDefaults{}
	for _, e := range entries {
		seen[e.Cli] = e
		assert.NotEmpty(t, e.AutoAppliedFlags, "cli %q must have a non-empty flag list", e.Cli)
		assert.NotEmpty(t, e.Notes, "cli %q must have a non-empty notes field", e.Cli)
	}
	for _, cli := range []gen.ExternalCliTool{
		gen.ExternalCliToolClaudeCode,
		gen.ExternalCliToolCodex,
		gen.ExternalCliToolOpencode,
	} {
		_, ok := seen[cli]
		assert.True(t, ok, "expected an entry for cli %q", cli)
	}
}

// TestListExecutorDefaults_ClaudeMatchesRealBuildArgs cross-checks the claude
// entry against the REAL ClaudeDriver.buildArgs() output (ADR-032 fix C/D,
// issue #488): -p, --output-format stream-json, --verbose, --no-chrome, a
// conditional --model, --dangerously-skip-permissions (unconditional as of
// 2026-07-05, reversing the original FR-5.3/US-5 acceptEdits stance), and a
// conditional --max-turns — in that order, verified against a live call with
// both conditional fields set. A second call with neither field set confirms
// both stay ABSENT, matching the endpoint's "(only when ...)" documentation.
func TestListExecutorDefaults_ClaudeMatchesRealBuildArgs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	claude := findExecutorDefaultsEntry(t, entries, gen.ExternalCliToolClaudeCode)
	require.NotEmpty(t, claude.AutoAppliedFlags)

	configured := runner.BuildClaudeArgs(runner.RunOptions{
		Input: "task", Model: "test-model-x", MaxTurns: 7,
	})
	assertRealArgsMatchEndpoint(t, "claude", configured, claude.AutoAppliedFlags)

	found := false
	for _, a := range configured {
		if a == "--dangerously-skip-permissions" {
			found = true
		}
	}
	assert.True(t, found, "issue #488: claude driver now passes --dangerously-skip-permissions unconditionally")

	bare := runner.BuildClaudeArgs(runner.RunOptions{Input: "task"})
	for _, a := range bare {
		assert.NotEqual(t, "--model", a, "conditional --model must be absent when RunOptions.Model is empty")
		assert.NotEqual(t, "--max-turns", a, "conditional --max-turns must be absent when RunOptions.MaxTurns is 0")
	}
}

// TestListExecutorDefaults_CodexMatchesRealBuildArgs cross-checks the codex
// entry against the REAL CodexDriver.buildArgs() output: --ask-for-approval
// never MUST precede the exec subcommand (a global codex flag), --json,
// --sandbox workspace-write, --skip-git-repo-check, --color never all
// follow, then the conditional -m and -C flags — verified against a live
// call with model and working directory both set. A second call with
// neither set confirms both stay ABSENT.
// --dangerously-bypass-approvals-and-sandbox must never appear (FR-5.3).
func TestListExecutorDefaults_CodexMatchesRealBuildArgs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	codex := findExecutorDefaultsEntry(t, entries, gen.ExternalCliToolCodex)
	require.NotEmpty(t, codex.AutoAppliedFlags)

	configured := runner.BuildCodexArgs(runner.RunOptions{
		Input: "task", Model: "gpt-5-codex", WorkDir: "/tmp/agent-workdir",
	})
	assertRealArgsMatchEndpoint(t, "codex", configured, codex.AutoAppliedFlags)

	for _, a := range configured {
		assert.NotEqual(t, "--dangerously-bypass-approvals-and-sandbox", a,
			"FR-5.3: codex driver never passes --dangerously-bypass-approvals-and-sandbox")
	}

	// Flag-ordering regression (ADR-032): --ask-for-approval is a GLOBAL codex
	// flag and errors if placed after `exec`; --sandbox is an exec-subcommand
	// flag and must follow it.
	execIdx := indexAtOrAfter(configured, 0, "exec")
	approvalIdx := indexAtOrAfter(configured, 0, "--ask-for-approval")
	sandboxIdx := indexAtOrAfter(configured, 0, "--sandbox")
	require.NotEqual(t, -1, execIdx, "expected \"exec\" subcommand in real argv; got %v", configured)
	require.NotEqual(t, -1, approvalIdx, "expected --ask-for-approval in real argv; got %v", configured)
	require.NotEqual(t, -1, sandboxIdx, "expected --sandbox in real argv; got %v", configured)
	assert.Less(t, approvalIdx, execIdx, "--ask-for-approval must precede the exec subcommand; got %v", configured)
	assert.Greater(t, sandboxIdx, execIdx, "--sandbox must follow the exec subcommand; got %v", configured)

	bare := runner.BuildCodexArgs(runner.RunOptions{Input: "task"})
	for _, a := range bare {
		assert.NotEqual(t, "-m", a, "conditional -m must be absent when RunOptions.Model is empty")
		assert.NotEqual(t, "-C", a, "conditional -C must be absent when RunOptions.WorkDir is empty")
	}
}

// TestListExecutorDefaults_OpencodeMatchesRealBuildArgs cross-checks the
// opencode entry against the REAL OpencodeDriver.buildArgs() output: run
// --format json, a conditional --model (only for a "provider/model"-shaped
// value), --dangerously-skip-permissions (the deliberate ADR-032 fix D
// posture for THIS CLI only), and the trailing "--" end-of-options separator
// as the LAST auto-applied entry. A second call with a bare (non-"provider/
// model"-shaped) model value confirms --model stays ABSENT.
func TestListExecutorDefaults_OpencodeMatchesRealBuildArgs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	opencode := findExecutorDefaultsEntry(t, entries, gen.ExternalCliToolOpencode)
	require.NotEmpty(t, opencode.AutoAppliedFlags)

	configured := runner.BuildOpencodeArgs(runner.RunOptions{
		Input: "task", Model: "anthropic/claude-3-5-sonnet",
	})
	assertRealArgsMatchEndpoint(t, "opencode", configured, opencode.AutoAppliedFlags)

	last := opencode.AutoAppliedFlags[len(opencode.AutoAppliedFlags)-1]
	assert.Equal(t, "--", last, "the -- end-of-options separator must be the last advertised AUTO-APPLIED flag (N-1)")
	// The real argv has ONE more token after "--": the prompt itself
	// (opts.Input), delivered as the positional argument immediately
	// following the separator — this is intentional (see buildArgs' own
	// doc comment) and is documented in the endpoint's "notes" field, not
	// the auto_applied_flags list, so it is correctly absent from
	// opencode.AutoAppliedFlags. Assert the REAL argv's shape matches that
	// design exactly: "--" is the second-to-last token, and the prompt is
	// the last.
	sepIdx := indexAtOrAfter(configured, 0, "--")
	require.NotEqual(t, -1, sepIdx, "expected \"--\" end-of-options separator in real argv; got %v", configured)
	require.Equal(t, len(configured)-2, sepIdx,
		"\"--\" must be the second-to-last token, immediately followed by the prompt only; got %v", configured)
	assert.Equal(t, "task", configured[len(configured)-1],
		"the prompt (opts.Input) must be the single positional argument after \"--\"")

	bareModel := runner.BuildOpencodeArgs(runner.RunOptions{Input: "task", Model: "not-provider-shaped"})
	for _, a := range bareModel {
		assert.NotEqual(t, "--model", a,
			"a Model not shaped like \"provider/model\" must be omitted, not passed as garbage")
	}
}

// TestListExecutorDefaults_MethodNotAllowed proves POST is rejected — this
// is read-only reference data, not a writable resource.
func TestListExecutorDefaults_MethodNotAllowed(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-defaults", nil)
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestListExecutorDefaults_DoesNotShadowAgentLookup proves "executor-defaults"
// as a reserved static path segment does not accidentally swallow requests
// for a real agent whose ID happens to be a normal (different) string — i.e.
// the generic agent GET path still works for an unrelated agent ID.
func TestListExecutorDefaults_DoesNotShadowAgentLookup(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/some-other-agent-id", nil)
	api.HandleAgents(w, r)
	// Not found (no such agent) — proves this fell through to the generic
	// getAgent path rather than being intercepted as executor-defaults.
	assert.Equal(t, http.StatusNotFound, w.Code)
}
