package agent

// S44 rev8: the carrier is a trusted connector-input fixture; both children,
// current config, stored token and ongoing environment are real. The actual
// connector producer is audited separately, not inferred from this fixture.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func programsConnectorBash(t *testing.T, f *wu5Fixture, sid, call string, args map[string]any) tools.ExecResponse {
	t.Helper()
	f.provider.setScript(sid, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "bash", Arguments: args}}}, wu5Stop("connector action completed")})
	input := programsConnectorInput()
	input.SessionID, input.ChatID = sid, sid
	_, _, err := f.al.processMessage(context.Background(), input)
	require.NoError(t, err)
	f.waitForCalls(sid, 2, "real connector-input bash action")
	var response tools.ExecResponse
	require.NoError(t, json.NewDecoder(strings.NewReader(f.provider.resultForCall(sid, call))).Decode(&response))
	require.Empty(t, response.Error)
	return response
}

func TestProgramRuntime_OngoingOriginSetting(t *testing.T) {
	t.Run("S44_running_environment_survives_setting_change_next_child_withheld", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, "tok_valid_12345")
		f.al.GetConfig().Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", Enabled: true, WorkspaceID: "sales", ProgramsAllowed: true}}
		stop := filepath.Join(f.home, "workspaces", "sales", "work", "s44-stop")
		require.NoError(t, os.MkdirAll(filepath.Dir(stop), 0700))
		report := filepath.Join(filepath.Dir(stop), "s44-emitted-lengths")
		// Observe the exact same child-computed length in its file and stdout.
		// A masking writer may retain a short suffix; S44 specifies the child's
		// environment, not a one-second buffer-drain latency guarantee.
		command := fmt.Sprintf("while [ ! -f %q ]; do n=${#GH_TOKEN}; printf '%%s\\n' \"$n\"; printf '%%s\\n' \"$n\" >> %q; sleep 1; done; printf '%%s\\n' \"${#GH_TOKEN}\"", stop, report)
		started := programsConnectorBash(t, f, sid, "s44-start", map[string]any{"command": command, "run_in_background": true})
		require.NotEmpty(t, started.SessionID, "actual ongoing child admitted")
		var lengths []string
		require.Eventually(t, func() bool {
			body, err := os.ReadFile(report)
			lengths = strings.Fields(string(body))
			return err == nil && len(lengths) > 0
		}, 5*time.Second, 10*time.Millisecond, "the original child actually emitted its first length")
		for _, line := range lengths {
			require.Equal(t, "15", line)
		}
		beforeCount := len(lengths)
		cfg := f.al.GetConfig()
		inst := cfg.Channels["telegram"]
		inst.ProgramsAllowed = false
		cfg.Channels["telegram"] = inst
		count, result := programsCountInput(t, f, sid, "s44-next", programsConnectorInput())
		require.Equal(t, "0", count, "S44: the next real child no longer receives the program variable")
		require.Contains(t, result, programsTelegramNotice)
		require.Eventually(t, func() bool {
			body, err := os.ReadFile(report)
			lengths = strings.Fields(string(body))
			return err == nil && len(lengths) > beforeCount
		}, 5*time.Second, 10*time.Millisecond, "the original child emits again after the setting is off")
		for _, line := range lengths {
			require.Equal(t, "15", line, "S44: an ongoing environment is not rewritten")
		}
		require.NoError(t, os.WriteFile(stop, []byte("finish"), 0600))
		completion := programsCompletion(t, events, sid)
		require.Contains(t, completion.Content, "15", "the original child still has its original environment at completion")
	})
	t.Run("S25_God_Mode_actual_child_count", func(t *testing.T) {
		if !sandbox.GodModeAvailable {
			t.Fatal("BLOCKED: this build has no God Mode support — required by S25")
		}
		previousAvailability := godModeAvailable.Load()
		SetGodModeAvailable(true) // Same public boot gate as the gateway, before construction.
		t.Cleanup(func() { setGodModeAvailable(previousAvailability) })
		f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) { cfg.Sandbox.GodMode = true })
		require.True(t, GodModeActive(f.al.GetConfig()), "Given: both God Mode activation gates are on")
		require.True(t, shellGodModeForAgentNoRewire(t, f.al, "dev"), "Given: the actual child tool was built in God Mode")
		require.Equal(t, "16", programsChildCount(t, f, "GH_TOKEN", "s25"), "S25: the actual God Mode child receives the saved token; no-egress-token creation is a separate mandatory clause")
	})
}
