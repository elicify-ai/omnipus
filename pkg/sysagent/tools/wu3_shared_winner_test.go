package systools_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BDD-11.9 provider/helper row: both writers read r1; A commits r2; B's old
// token cannot overwrite A or trigger a provider probe. Real token round-trip.
func TestWU3TwoProviderWritersPreserveWinnerAndRefuseStalePlan(t *testing.T) {
	d, _ := wu3Deps(t)
	calls := 0
	wu3Network(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return wu3HTTPResponse(r, 200, `{"models":[{"name":"fixture-model"}]}`), nil
	})
	tool := systools.NewProviderConfigureTool(d)
	wu3Success(t, tool.Execute(context.Background(), map[string]any{"name": "ollama", "api_base": "http://initial.wu3.invalid/v1", "expected_revision": "absent"}))
	r1 := wu3SharedRow(t, d, "provider", "ollama")["revision"]
	require.IsType(t, "", r1, "FR-088 first confirmed plan carries a real revision")
	wu3Success(t, tool.Execute(context.Background(), map[string]any{"name": "ollama", "api_base": "http://winner.wu3.invalid/v1", "expected_revision": r1}))
	winner := wu3SharedRow(t, d, "provider", "ollama")
	assert.NotEqual(t, r1, winner["revision"])
	before := wu3ReadFile(t, d.ConfigPath)
	probesBefore := calls
	result := tool.Execute(context.Background(), map[string]any{"name": "ollama", "api_base": "http://loser.wu3.invalid/v1", "expected_revision": r1})
	body := wu3Error(t, result, "CONFLICT")
	assert.Contains(t, body["message"], "ollama")
	assert.Equal(t, before, wu3ReadFile(t, d.ConfigPath))
	assert.Equal(t, winner, wu3SharedRow(t, d, "provider", "ollama"))
	assert.Equal(t, probesBefore, calls, "conflict causes zero upstream side effect")
}

// An absent token is not authority to recreate a removed-and-recreated server.
// MCP's existing duplicate guard is not a substitute for the CONFLICT result.
func TestWU3MCPAbsentTokenRejectsConcurrentAddWithoutTouchingCredentials(t *testing.T) {
	d, _ := wu3Deps(t)
	tool := systools.NewMCPAddTool(d)
	wu3Success(t, tool.Execute(context.Background(), map[string]any{"name": "notion", "transport": "stdio", "command": "winner-command", "expected_revision": "absent"}))
	before := wu3ReadFile(t, d.ConfigPath)
	require.NoError(t, d.CredStore.Set("control-ref", "wu3-control-value"))
	credPath := filepath.Join(d.Home, "credentials.json")
	credsBefore := wu3ReadFile(t, credPath)
	result := tool.Execute(context.Background(), map[string]any{"name": "notion", "transport": "stdio", "command": "loser-command", "env": map[string]any{"KEY": "wu3-loser-value"}, "expected_revision": "absent"})
	body := wu3Error(t, result, "CONFLICT")
	assert.Contains(t, body["message"], "notion")
	assert.Equal(t, before, wu3ReadFile(t, d.ConfigPath))
	assert.Equal(t, credsBefore, wu3ReadFile(t, credPath))
}
