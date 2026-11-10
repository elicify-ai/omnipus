//go:build goolm && stdjson

package gateway

import (
	"context"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wu3AgentIDs(t *testing.T, home string) []string {
	t.Helper()
	agents, skipped, err := agentstore.New(home).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "an unreadable record would hide a new agent")
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	return ids
}

// CHECK F01 through a real Omnipus turn: the registry refuses a create_agent
// call that misses a schema-required field, and one with a wrong-typed field,
// before Execute runs. The model edge must still receive the FR-030
// nothing-saved envelope with the registry's diagnostic, and no agent may be
// saved. A valid call in the same turn is the control that the path executes.
func TestWU3RegistrySchemaRefusalEnvelopeThroughRealOmnipusTurn(t *testing.T) {
	call := func(id, args string) providers.ToolCall {
		return providers.ToolCall{ID: id, Function: &providers.FunctionCall{Name: "create_agent", Arguments: args}}
	}
	provider := testutil.NewScenario().
		WithToolCall("ToolSearch", `{"names":["create_agent"]}`).
		WithToolCalls([]providers.ToolCall{
			call("missing", `{"description":"no name","model":"scripted-model"}`),
			call("wrongtype", `{"name":42,"description":"typed","model":"scripted-model"}`),
			call("valid", `{"name":"Registry Control","description":"control","soul":"Help.","model":"scripted-model"}`),
		}).
		WithText("done")
	api := wu3WiredAPI(t, provider)
	_, err := api.agentLoop.GetSessionStore().GetOrCreateMainSession("default", "omnipus")
	require.NoError(t, err)
	mainID, err := session.MainSessionID("default", "omnipus")
	require.NoError(t, err)
	before := wu3AgentIDs(t, api.homePath)

	_, err = api.agentLoop.ProcessDirectAs(context.Background(), "omnipus", "Create the confirmed colleagues.", "agent:omnipus:session:"+mainID)
	require.NoError(t, err)

	for _, id := range []string{"missing", "wrongtype"} {
		body := wu3ToolResultFromTurn(t, provider, id)
		assert.Equal(t, "failed: nothing saved", body["outcome"], id)
		assert.Equal(t, []any{}, body["changed_fields"], id)
		assert.Equal(t, []any{}, body["not_tested"], id)
		assert.Equal(t, []any{}, body["missing_parts"], id)
		_, scope := body["check_scope"].(string)
		assert.True(t, scope, "%s: check_scope is a string", id)
		assert.Contains(t, body["reason"], "invalid arguments for tool", "%s keeps the registry diagnostic", id)
	}
	valid := wu3ToolResultFromTurn(t, provider, "valid")
	newID, ok := valid["id"].(string)
	require.True(t, ok, "control: the valid call executed: %v", valid)
	want := append(append([]string{}, before...), newID)
	sort.Strings(want)
	assert.Equal(t, want, wu3AgentIDs(t, api.homePath), "only the valid control saved an agent")
}
