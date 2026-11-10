package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silent-failure-hunter SF-05 / type-design T4 / architect F04: an agent_md
// file that was published but whose read-back then failed is a durable part.
// The result is "partly saved" naming it, with the failed verification as the
// missing part and its cause kept — never "failed: nothing saved".
func TestWU3MetadataPublishedButUnverifiedIsPartlySaved(t *testing.T) {
	d, cfg := wu3Deps(t)
	id := wu3CreateAgent(t, d, cfg, "Readback fixture")
	before := wu3AgentRead(t, d, id)
	systools.FailAgentMetadataReadBackForTest(t)
	content := "# Published instructions\n"
	res := systools.NewAgentUpdateTool(d).Execute(context.Background(), map[string]any{
		"id": id, "revision": before["revision"], "agent_md": content,
	})
	require.True(t, res.IsError, res.ForLLM)
	body := wu3Outcome(t, res, "partly saved")
	assert.Equal(t, []any{"agent_md"}, body["changed_fields"], "the published file is a durable part")
	assert.Contains(t, body["missing_parts"], "read-back of AGENT.md")
	assert.Contains(t, body["reason"], "injected read-back failure", "the read error's cause is kept")
	got, err := os.ReadFile(filepath.Join(d.Home, "agents", id, "AGENT.md"))
	require.NoError(t, err)
	assert.Equal(t, content, string(got), "the file really was published")
}
