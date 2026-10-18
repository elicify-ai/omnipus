// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-5 child-path and document-runtime RED tests: BDD-7.9 (every child path —
// foreground bash, background bash, environment_setup — sees the selected
// value, and the gateway process environment never does; FR-076) and
// BDD-7.10 (a private name containing TOKEN survives the document layer's
// strip list, while the runtime's own PYTHONPATH / NODE_PATH / OSL_SOCKET_PATH
// values are kept; R2-MAJ-007: private variables are appended AFTER all
// managed layers, so earlier injection would lose them).
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestWU5PrivateSecret_AllChildPaths_NeverGatewayEnv is BDD-7.9 / US-7 AS-1 /
// FR-076: Mia's Sales secret reaches her foreground bash, her background bash
// and her environment_setup run — and never the gateway process environment.
func TestWU5PrivateSecret_AllChildPaths_NeverGatewayEnv(t *testing.T) {
	requireWU5VariableAbsentFromHost(t)
	f := newWU5Fixture(t)
	sales := f.mainSession("sales", "mia")

	// Store through the real card flow (a web turn raises the card).
	f.provider.setScript(sales, []providers.LLMResponse{wu5RequestPrivateSecret(wu5Variable), wu5Stop("stored")})
	f.webTurn(sales, "store my weather token")
	f.waitForCalls(sales, 1, "card raised")
	card := f.pendingPrivateCard(sales, "mia", "sales")
	submitted := f.submitPrivate(cardToRecord(f, card), wu5SalesValue)
	f.resumeAfterSubmit(submitted.Card.RequestId)

	// Turn 1: foreground bash, background bash, and environment_setup's run.
	// The background bash is a REAL background call (run_in_background) and
	// writes its report INSIDE the turn's working directory — the workspace
	// work dir (<home>/workspaces/sales/work); a write outside it is refused
	// by the workspace path guard. The run reports its background session
	// id, which turn 2 then polls and reads.
	workDir := filepath.Join(f.home, "workspaces", "sales", "work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	bgReport := filepath.Join(workDir, "bg-report.txt")
	f.provider.setScript(sales, []providers.LLMResponse{
		wu5Bash(`printf 'fg=%s' "${#WEATHER_TOKEN}"`),
		{ToolCalls: []providers.ToolCall{{ID: "wu5-bash-bg", Type: "function", Name: "bash",
			Arguments: map[string]any{
				"command":           `printf 'bg=%s' "${#WEATHER_TOKEN}" > ` + bgReport,
				"run_in_background": true,
			}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu5-es-run", Type: "function", Name: "environment_setup",
			Arguments: map[string]any{"action": "run", "purpose": "probe the install child env",
				"command": `printf 'es=%s' "${#WEATHER_TOKEN}"`}}}},
		wu5Stop("child paths started"),
	})
	f.webTurn(sales, "check every child path")
	f.waitForCalls(sales, 4, "foreground and background bash plus the setup run")

	results := strings.Join(f.provider.toolResultsFor(sales), "\n")
	require.Contains(t, results, "fg=16", "foreground bash sees the selected value (BDD-7.9)")

	require.FileExists(t, bgReport, "background bash must have written its report")
	bgData, err := os.ReadFile(bgReport)
	require.NoError(t, err)
	require.Equal(t, "bg=16", string(bgData), "background bash sees the selected value (BDD-7.9)")

	// Turn 2: poll and read the environment_setup background session. The
	// session id comes from the run's OWN tool result (keyed by its call id
	// — the background bash result is ExecResponse JSON too, so the id is
	// what tells the two apart).
	setupSession := extractWU5SetupSession(t, f.provider.resultForCall(sales, "wu5-es-run"))
	require.NotEmpty(t, setupSession, "the environment_setup run must report its background session id")
	f.provider.setScript(sales, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "wu5-es-poll", Type: "function", Name: "environment_setup",
			Arguments: map[string]any{"action": "poll", "session_id": setupSession}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu5-es-poll2", Type: "function", Name: "environment_setup",
			Arguments: map[string]any{"action": "poll", "session_id": setupSession}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu5-es-poll3", Type: "function", Name: "environment_setup",
			Arguments: map[string]any{"action": "poll", "session_id": setupSession}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu5-es-read", Type: "function", Name: "environment_setup",
			Arguments: map[string]any{"action": "read", "session_id": setupSession}}}},
		wu5Stop("setup child checked"),
	})
	f.webTurn(sales, "read the install output")
	f.waitForCalls(sales, 5, "the setup child was polled and read")

	results = strings.Join(f.provider.toolResultsFor(sales), "\n")
	require.Contains(t, results, "es=16",
		"environment_setup's install child sees the selected value (BDD-7.9; FR-076 names environment_setup)")

	// The gateway process environment (this test process is the gateway) must
	// never hold the value — before or after all three child paths.
	require.Empty(t, os.Getenv(wu5Variable), "the gateway process environment must not contain the private variable (BDD-7.9: never the gateway)")
}

// extractWU5SetupSession pulls the environment_setup background session id
// out of the run's own tool result (test plumbing): the result leads with
// the session JSON (tools.ExecResponse), and that id is exactly what the
// poll/read calls address.
func extractWU5SetupSession(t *testing.T, runResult string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(runResult))
	var resp tools.ExecResponse
	require.NoError(t, dec.Decode(&resp), "the run result must lead with the session JSON; got %q", runResult)
	return resp.SessionID
}

// TestWU5PrivateSecret_DocumentRuntime_NamesSurvive is BDD-7.10 / R2-MAJ-007
// with the document runtime enabled: WEATHER_TOKEN (name contains TOKEN, so
// the document layer's inherited-input strip list targets it) must be present
// in the child — private variables are appended AFTER all managed layers —
// while PYTHONPATH, NODE_PATH and OSL_SOCKET_PATH keep the runtime's own
// values (computed here from the same real composer — the property the spec
// states: the runtime's values survive the private append).
func TestWU5PrivateSecret_DocumentRuntime_NamesSurvive(t *testing.T) {
	requireWU5VariableAbsentFromHost(t)
	f := newWU5FixtureWithDocRuntime(t)
	sales := f.mainSession("sales", "mia")

	// Derive the runtime's own values from the real composer (the expected
	// side of the spec's "keep the runtime's values" property).
	layout, err := documentruntime.ResolveLayout(f.home, documentruntime.ManifestRevision, "shared")
	require.NoError(t, err)
	docEnv := documentruntime.ChildEnvironment(nil, layout)
	wantPY, okPY := docEnvValue(docEnv, "PYTHONPATH")
	wantNode, okNode := docEnvValue(docEnv, "NODE_PATH")
	wantOSL, okOSL := docEnvValue(docEnv, "OSL_SOCKET_PATH")
	require.True(t, okPY && wantPY != "", "instrument: the document runtime must set PYTHONPATH for this property to be checkable")
	require.True(t, okNode && wantNode != "", "instrument: the document runtime must set NODE_PATH for this property to be checkable")
	require.True(t, okOSL, "instrument: the document runtime must set OSL_SOCKET_PATH for this property to be checkable")

	// Store through the real card flow.
	f.provider.setScript(sales, []providers.LLMResponse{wu5RequestPrivateSecret(wu5Variable), wu5Stop("stored")})
	f.webTurn(sales, "store my weather token")
	f.waitForCalls(sales, 1, "card raised")
	card := f.pendingPrivateCard(sales, "mia", "sales")
	submitted := f.submitPrivate(cardToRecord(f, card), wu5SalesValue)
	f.resumeAfterSubmit(submitted.Card.RequestId)

	f.provider.setScript(sales, []providers.LLMResponse{
		wu5Bash(`printf 'tok=%s|py=%s|node=%s|osl=%s' "${#WEATHER_TOKEN}" "$PYTHONPATH" "$NODE_PATH" "$OSL_SOCKET_PATH"`),
		wu5Stop("document runtime checked"),
	})
	f.webTurn(sales, "print the document runtime environment")
	f.waitForCalls(sales, 2, "the document-runtime command ran")

	results := strings.Join(f.provider.toolResultsFor(sales), "\n")
	require.Contains(t, results, "tok=16",
		"WEATHER_TOKEN must survive the document layer (BDD-7.10; appended after all managed layers, R2-MAJ-007)")
	require.Contains(t, results, "py="+wantPY, "PYTHONPATH keeps the runtime's value (BDD-7.10)")
	require.Contains(t, results, "node="+wantNode, "NODE_PATH keeps the runtime's value (BDD-7.10)")
	require.Contains(t, results, "osl="+wantOSL, "OSL_SOCKET_PATH keeps the runtime's value (BDD-7.10)")
}

// docEnvValue extracts one key's value from a composed env slice.
func docEnvValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 && kv[:i] == key {
			return kv[i+1:], true
		}
	}
	return "", false
}
