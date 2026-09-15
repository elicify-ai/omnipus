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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// executorDefaultsTestAPI builds a minimal restAPI. listExecutorDefaults is
// static reference data with no config/agent dependency, so an empty agent
// list is sufficient.
func executorDefaultsTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
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
