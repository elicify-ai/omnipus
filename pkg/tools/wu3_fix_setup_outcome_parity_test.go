package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// completionOutcome decodes the FR-030 object on the completion's
// "Outcome:" line.
func completionOutcome(t *testing.T, completion *ToolResult) map[string]any {
	t.Helper()
	const marker = "\n\nOutcome: "
	i := strings.LastIndex(completion.ForLLM, marker)
	require.NotEqual(t, -1, i, "completion carries an outcome: %q", completion.ForLLM)
	var o map[string]any
	require.NoError(t, json.NewDecoder(strings.NewReader(completion.ForLLM[i+len(marker):])).Decode(&o))
	return o
}

// leadingOutcome decodes the session JSON a poll/read/kill result leads with.
func leadingOutcome(t *testing.T, res *ToolResult) map[string]any {
	t.Helper()
	var o map[string]any
	require.NoError(t, json.NewDecoder(strings.NewReader(res.ForLLM)).Decode(&o), res.ForLLM)
	return o
}

// code-reviewer IM-02 / silent-failure-hunter SF-06 / pr-test-analyzer TC-04 /
// architect F05: an installer that exits 0 but whose shared area cannot be
// published is "partly saved" in the completion — and every later poll, read
// and kill of that finished session must give the same verdict and error
// classification, never "working".
func TestWU3SetupPublicationFailureVerdictSurvivesPollReadKill(t *testing.T) {
	target := &lifecycleTarget{
		prefix:   pathForTempSubdir(t, "prefix"),
		commitFn: func() (string, string, error) { return "", "", fmt.Errorf("forced: publish refused") },
	}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-wu3-parity-1")
	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command": `echo ok > "$OMNIPUS_ENV_PREFIX/artifact.txt"`, "purpose": "parity", "scope": "shared",
	})
	require.True(t, completion.IsError)
	want := completionOutcome(t, completion)
	require.Equal(t, "partly saved", want["outcome"], completion.ForLLM)
	assert.Equal(t, []any{"published installation area"}, want["missing_parts"])
	sessionID := startSessionID(t, completion)

	for _, action := range []string{"poll", "read", "kill"} {
		res := h.tool.Execute(ctx, map[string]any{"action": action, "session_id": sessionID})
		got := leadingOutcome(t, res)
		assert.Equal(t, "done", got["status"], action)
		for _, field := range []string{"outcome", "reason", "check_scope", "missing_parts", "not_tested"} {
			assert.Equal(t, want[field], got[field], "%s: %s must match the completion", action, field)
		}
		assert.False(t, res.IsError, "%s: the status action itself succeeded; the verdict is in the outcome", action)
	}
}

// Parity controls: a clean, published exit-0 install is "working" in the
// completion and in every later status action; a nonzero exit is "partly
// saved" in all of them (only the completion is an error result — a status
// query that succeeded is not).
func TestWU3SetupVerdictParityControls(t *testing.T) {
	for _, tc := range []struct {
		name, command, outcome string
		isError                bool
	}{
		{"published exit 0", `echo ok > "$OMNIPUS_ENV_PREFIX/artifact.txt"`, "working", false},
		{"nonzero exit", `echo failed >&2; exit 4`, "partly saved", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLifecycleHarness(t, lifecycleStore{target: &lifecycleTarget{prefix: pathForTempSubdir(t, "prefix")}})
			ctx := h.ctx("mia", "alpha", "", "t-wu3-parity-"+strings.ReplaceAll(tc.name, " ", "-"))
			_, completion := h.startAndWait(t, ctx, map[string]any{"command": tc.command, "purpose": "parity", "scope": "shared"})
			assert.Equal(t, tc.isError, completion.IsError, completion.ForLLM)
			want := completionOutcome(t, completion)
			require.Equal(t, tc.outcome, want["outcome"])
			sessionID := startSessionID(t, completion)
			for _, action := range []string{"poll", "read", "kill"} {
				res := h.tool.Execute(ctx, map[string]any{"action": action, "session_id": sessionID})
				got := leadingOutcome(t, res)
				assert.Equal(t, want["outcome"], got["outcome"], action)
				assert.Equal(t, want["reason"], got["reason"], action)
				assert.False(t, res.IsError, "%s: the status action itself succeeded", action)
			}
		})
	}
}
