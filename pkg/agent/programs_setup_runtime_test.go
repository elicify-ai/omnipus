package agent

// S29: run the actual loop-wired environment_setup process, then poll/read the
// session it actually returned. Only the model responses are scripted.
import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestProgramEnvironmentSetup_ActualChild(t *testing.T) {
	t.Run("S29_environment_setup_children_get_the_token", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		sid := f.mainSession("sales", "dev")
		f.provider.setScript(sid, []providers.LLMResponse{
			{ToolCalls: []providers.ToolCall{{ID: "s29-run", Type: "function", Name: "environment_setup", Arguments: map[string]any{
				"action": "run", "purpose": "count program token bytes without printing the token", "command": "printf 'S29_BEGIN\\n'; printenv GH_TOKEN | wc -c; printf 'S29_CHILD_RAN\\nS29_END\\n'",
			}}}}, wu5Stop("setup started"),
		})
		programsWebTurn(t, f, sid, "count the connected-program token in the setup child")
		f.waitForCalls(sid, 2, "S29 actual setup run")
		setupID := extractWU5SetupSession(t, f.provider.resultForCall(sid, "s29-run"))
		require.NotEmpty(t, setupID, "the real setup run returned its actual session")
		var status tools.ExecResponse
		deadline := time.Now().Add(10 * time.Second)
		for i := 0; ; i++ {
			callID := fmt.Sprintf("s29-poll-%d", i)
			f.provider.setScript(sid, []providers.LLMResponse{
				{ToolCalls: []providers.ToolCall{{ID: callID, Type: "function", Name: "environment_setup", Arguments: map[string]any{"action": "poll", "session_id": setupID}}}}, wu5Stop("setup polled"),
			})
			programsWebTurn(t, f, sid, "poll the setup session")
			f.waitForCalls(sid, 2, "S29 real poll")
			require.NoError(t, json.NewDecoder(strings.NewReader(f.provider.resultForCall(sid, callID))).Decode(&status))
			if status.Status == "done" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("S29 setup child did not finish: status=%s error=%s", status.Status, status.Error)
			}
			time.Sleep(20 * time.Millisecond)
		}
		require.Zero(t, status.ExitCode, "the actual script finished successfully")
		f.provider.setScript(sid, []providers.LLMResponse{
			{ToolCalls: []providers.ToolCall{{ID: "s29-read", Type: "function", Name: "environment_setup", Arguments: map[string]any{"action": "read", "session_id": setupID}}}}, wu5Stop("setup read"),
		})
		programsWebTurn(t, f, sid, "read the setup script output")
		f.waitForCalls(sid, 2, "S29 real read")
		var read tools.ExecResponse
		require.NoError(t, json.NewDecoder(strings.NewReader(f.provider.resultForCall(sid, "s29-read"))).Decode(&read))
		_, script, found := strings.Cut(strings.ReplaceAll(read.Output, "\r\n", "\n"), "S29_BEGIN\n")
		require.True(t, found, "the actual child emitted the beginning control")
		script, _, found = strings.Cut(script, "\nS29_END")
		require.True(t, found, "the actual child completed the output control")
		require.Equal(t, "16\nS29_CHILD_RAN", script, "S29: exact script output; do not confuse the tool's generic installation guidance with child stdout")
	})
}
