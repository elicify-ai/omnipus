package tools_test

// Oracle: r17 §7.12.3 and scenario 19.16. Read a real existing file
// through the production read_file guard; do not test the notice literal alone.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetadataGuard_SuggestsSelectedGetAgent(t *testing.T) {
	ws := setupMetadataTestWorkspace(t)
	filename := filepath.Join(ws, "SOUL.md")
	const persona = "T8 protected persona positive control"
	require.NoError(t, os.WriteFile(filename, []byte(persona), 0o600))
	result := tools.NewReadFileTool(ws, false, 1024).Execute(context.Background(), map[string]any{"path": "SOUL.md"})
	require.True(t, result.IsError, "protected metadata is not returned: %s", result.ForLLM)
	assert.NotContains(t, result.ForLLM, persona)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &envelope))
	notice, ok := envelope["error"].(map[string]any)
	require.True(t, ok, "instrument: the structured metadata refusal is read")
	suggestion, ok := notice["suggestion"].(string)
	require.True(t, ok, "the guard supplies a readable supported alternative")
	assert.Contains(t, suggestion, `get_agent(fields=["soul"])`, "19.16 compare decoded text, not JSON-escaped bytes")
	assert.NotContains(t, result.ForLLM, "read_agent_metadata", "the guard must not direct the model to a retired tool")
}
