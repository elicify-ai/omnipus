package agent

// Oracle: r17 §7.11 / §7.12.3 / scenario 19.15. The ordinary model
// entry, offered-tool check, policy refusal, result delivery and audit stay
// real. Only the provider response sequence is a process-edge fixture.
import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetiredToolNames_RealLoopMessageAndAudit(t *testing.T) {
	for _, name := range []string{"get_agent_tools", "read_agent_metadata"} {
		t.Run(name, func(t *testing.T) {
			f := newWU5Fixture(t)
			dir := t.TempDir()
			lg, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, MaxSizeBytes: 1024 * 1024, RetentionDays: 1})
			require.NoError(t, err)
			f.al.auditLogger = lg
			t.Cleanup(func() { f.al.auditLogger = nil; require.NoError(t, lg.Close()) })
			sid := f.mainSession("sales", "mia")
			callID := "t8-retired-" + name
			f.provider.setScript(sid, []providers.LLMResponse{
				{ToolCalls: []providers.ToolCall{{ID: callID, Type: "function", Name: name, Arguments: map[string]any{}}}},
				{Content: "T8 retired-name response received", FinishReason: "stop"},
			})
			f.webTurn(sid, "Read the agent's definition")
			assert.Equal(t, 2, f.provider.callCount(sid), "actual tool result returns to the model before it finishes")
			want := fmt.Sprintf("tool %q was renamed to %q — call %q instead", name, "get_agent", "get_agent")
			assert.Equal(t, want, f.provider.resultForCall(sid, callID), "19.15 exact renamed result, never generic policy_denied")
			require.NoError(t, lg.Close())
			entries := readAuditEvents(t, filepath.Join(dir, "audit.jsonl"))
			var related []map[string]any
			for _, entry := range entries {
				if entry["tool"] == name {
					related = append(related, entry)
				}
			}
			require.NotEmpty(t, related, "instrument: the real refusal must produce a named audit record")
			data, err := json.Marshal(related)
			require.NoError(t, err)
			assert.Contains(t, string(data), "retired_tool_name", "19.15 audit carries the retired_tool_name reason")
		})
	}
}
