package agent

// Spec rev8 S110 / I-2: actual web launch followed by a Telegram-origin
// delegate steer of the same helper. No seeded lineage or synthetic wake.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualExistingHelperSteer(t *testing.T) {
	t.Run("S110_steering_an_existing_helper_from_telegram", func(t *testing.T) {
		f, parent, work := programsDelegateFixture(t)
		positive, result := programsCountInput(t, f, parent, "s110-connected-control", programsWebInput())
		require.Equal(t, "16", positive, "instrument: the real connected Dev child receives the program")
		require.NotContains(t, result, "Connected programs were not used for this command:")
		child := programsDelegateFromInput(t, f, parent, "s110-web-launch", programsWebInput())
		before, err := f.al.GetSessionLifecycleStore().Load(child)
		require.NoError(t, err)
		require.Equal(t, []string{"web"}, before.Provenance, "Given: actual helper launched from the web, not a fixture-stamped carrier")
		require.Equal(t, session.LifecycleCompleted, before.State, "Given: the real initial turn is finished and no helper turn is active")

		const childCall, steerCall = "s110-steered-count", "s110-parent-steer"
		path := filepath.Join(work, "s110-steered-count")
		command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'S110_STEERED_CHILD_RAN'", path)
		f.provider.setScript(child, []providers.LLMResponse{wu5BashCall(childCall, command), wu5Stop("steered helper completed")})
		f.provider.setScript(parent, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: steerCall, Type: "function", Name: "delegate", Arguments: map[string]any{"action": "steer", "session_id": child, "text": "run printenv GH_TOKEN | wc -c"}}}}, wu5Stop("steer submitted")})
		input := programsConnectorInput()
		input.SessionID, input.ChatID = parent, parent
		_, _, err = f.al.processMessage(context.Background(), input)
		require.NoError(t, err)
		f.waitForCalls(parent, 2, "real Telegram parent's steer result")
		f.waitForCalls(child, 2, "actual same-helper steer consumption and bash result")
		joinGoalFixtureRuns(t, f.al)
		result = f.provider.resultForCall(child, childCall)
		require.Contains(t, result, "S110_STEERED_CHILD_RAN", "instrument: the new real helper command ran, not just the initial launch or a refused tool")
		count, err := os.ReadFile(path)
		require.NoError(t, err)
		after, err := f.al.GetSessionLifecycleStore().Load(child)
		require.NoError(t, err)
		require.Equal(t, child, after.SessionID, "steering keeps the actual child identity")
		t.Logf("actual steered helper child_ran=true count=%s before=%v after=%v", strings.TrimSpace(string(count)), before.Provenance, after.Provenance)
		assert.ElementsMatch(t, []string{"web", "connector:telegram"}, after.Provenance, "I-2: the actual instruction-changing steer monotonically adds its Telegram origin")
		assert.Equal(t, "0", strings.TrimSpace(string(count)), "S110: the real Telegram-fed helper gets no program token")
		assert.Contains(t, result, programsTelegramNotice, "S110: exact inherited Telegram-off notice; unresolved is not the specified outcome")
	})
}
