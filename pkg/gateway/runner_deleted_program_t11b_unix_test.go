//go:build !windows

package gateway

// Founder Q12 A / S4: re-check a saved program on every connection test.
// A valid legacy/default binary must not conceal a deleted saved executable.
import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerTestEndpoint_DeletedSavedProgramDoesNotFallBack_S4(t *testing.T) {
	program, marker := t11bConnectionProgram(t, t.TempDir(), "saved-claude", "claude-code saved-version")
	fallback, fallbackMarker := t11bConnectionProgram(t, t.TempDir(), "legacy-claude", "claude-code legacy-version")
	api := t11bConnectionAPI(t, program)
	t11bSaveProgram(t, api, "claude-code", program)
	code, control := postRunnerTest(t, api, "ext-claude")
	require.Equal(t, http.StatusOK, code)
	require.True(t, control.Ok, "instrument: the same real saved program works before deletion")
	before, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "--version\n", string(before))

	raw, err := json.Marshal(map[string]any{"kind": "external-cli", "cli": "claude-code", "cli_path": fallback})
	require.NoError(t, err)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		return json.Unmarshal(raw, cfg.Agents.List[2].Subagents.Executor)
	}))
	require.NoError(t, os.Remove(program))
	code, refused := postRunnerTest(t, api, "ext-claude")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, refused.Ok, "S4: a healthy legacy program does not repair the invalid saved Settings path")
	assert.Equal(t, gen.RunnerTestResponseReasonMissingBinary, refused.Reason)
	assert.Equal(t, t11bMissingProgramText("Claude Code", program), refused.Message)
	assert.NoFileExists(t, fallbackMarker, "S4: no legacy subprocess may start, including --version")
	after, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "no extra execution of the deleted saved file")
}
