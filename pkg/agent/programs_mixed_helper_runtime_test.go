package agent

// Spec rev8 S123: web-off/Telegram-on forbids BOTH mixed steering orders,
// including after reconstruction from the helper's actual persisted lineage.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualMixedHelperSteering(t *testing.T) {
	for _, row := range []struct {
		name                string
		webFirst, doRestart bool
	}{
		{"S123_web_launch_telegram_steer", true, false},
		{"S123_telegram_launch_web_steer", false, false},
		{"S123_web_launch_restart_telegram_steer", true, true},
		{"S123_telegram_launch_restart_web_steer", false, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			x := newProgramsMixedHelperFixture(t)
			positive, result := programsCountInput(t, x.f, x.parent, row.name+"-connected-control", programsWebInput())
			require.Equal(t, "16", positive, "instrument: actual connected Dev/web command is usable before disabling web")
			require.NotContains(t, result, "Connected programs were not used for this command:")
			x.f.al.GetConfig().Programs.WebChatAllowed = false
			launchInput, steerInput := programsWebInput(), programsConnectorInput()
			initialOrigin := "web"
			if !row.webFirst {
				launchInput, steerInput = steerInput, launchInput
				initialOrigin = "connector:telegram"
			}
			child := programsDelegateFromInput(t, x.f, x.parent, row.name+"-launch", launchInput)
			before, err := x.f.al.GetSessionLifecycleStore().Load(child)
			require.NoError(t, err)
			require.Equal(t, session.LifecycleCompleted, before.State, "Given: the actual initial helper is idle, no running turn is fabricated")
			require.Equal(t, []string{initialOrigin}, before.Provenance, "Given: actual launch producer stored exactly its launching input")
			require.Equal(t, x.parent, before.SteeringSessionID())
			if row.doRestart {
				x.restart(t)
				reloaded, reloadErr := x.f.al.GetSessionLifecycleStore().Load(child)
				require.NoError(t, reloadErr)
				require.Equal(t, before, reloaded, "S123: the actual helper/edge/lineage survive runtime restart unchanged")
			}
			path := filepath.Join(x.work, row.name+"-count")
			const childCall, steerCall = "s123-steered-count", "s123-parent-steer"
			command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'S123_STEERED_CHILD_RAN'", path)
			x.f.provider.setScript(child, []providers.LLMResponse{wu5BashCall(childCall, command), wu5Stop("mixed steer count completed")})
			x.f.provider.setScript(x.parent, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: steerCall, Type: "function", Name: "delegate", Arguments: map[string]any{"action": "steer", "session_id": child, "text": "run printenv GH_TOKEN | wc -c"}}}}, wu5Stop("mixed steer submitted")})
			observedBefore := len(x.observer.observed(child))
			steerInput.SessionID, steerInput.ChatID = x.parent, x.parent
			_, _, err = x.f.al.processMessage(context.Background(), steerInput)
			require.NoError(t, err)
			x.f.waitForCalls(x.parent, 2, "actual parent's registered delegate steer result")
			x.f.waitForCalls(child, 2, "actual helper consumes opposite-origin steer and executes its command")
			joinGoalFixtureRuns(t, x.f.al)
			result = x.f.provider.resultForCall(child, childCall)
			require.Contains(t, result, "S123_STEERED_CHILD_RAN", "instrument: the new actual command ran, not initial launch or a refused child")
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			after, err := x.f.al.GetSessionLifecycleStore().Load(child)
			require.NoError(t, err)
			require.Equal(t, child, after.SessionID)
			assert.ElementsMatch(t, []string{"web", "connector:telegram"}, after.Provenance, "S123/I-2: both actual instruction origins are durable, order independent")
			assert.Equal(t, "0", strings.TrimSpace(string(raw)), "S123: both actual mixed orders withhold while web is off")
			assert.Contains(t, result, "Connected programs were not used for this command: programs are off for the web chat. A person can switch them on in Settings → Programs.", "S123: exact web-off notice, never unresolved or Telegram-off")
			actual := x.observer.observed(child)[observedBefore:]
			require.GreaterOrEqual(t, len(actual), 2)
			expected := bus.NewProvenance(bus.OriginWeb, bus.ConnectorOrigin("telegram"))
			for _, origin := range actual[:2] {
				assert.Equal(t, expected, origin, "S123: exact real receiving command/result provenance, not a test-joined carrier")
			}
			t.Logf("actual mixed helper web_first=%t restart_before_steer=%t command_ran=true count=%s before=%v after=%v receiving=%v", row.webFirst, row.doRestart, strings.TrimSpace(string(raw)), before.Provenance, after.Provenance, actual[:2])
		})
	}
}
