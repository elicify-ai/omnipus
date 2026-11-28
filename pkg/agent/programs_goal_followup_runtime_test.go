package agent

// Spec rev8 S109/I-2: actual Telegram set_goal -> stored goal -> real keeper
// -> real notifier inbound -> real consumer and bash child. No seeded lineage.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Observe the context at the process-edge model request, without changing it.
// The embedded provider mutex guards both scripts and these observations.
type programsGoalOriginProvider struct {
	*wu5ScriptedProvider
	origins []bus.Provenance
}

func (p *programsGoalOriginProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.origins = append(p.origins, tools.ToolTurnProvenance(ctx))
	p.mu.Unlock()
	return p.wu5ScriptedProvider.Chat(ctx, messages, definitions, model, options)
}

func (p *programsGoalOriginProvider) observed() []bus.Provenance {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bus.Provenance(nil), p.origins...)
}

func TestProgramProvenance_ActualGoalFollowup(t *testing.T) {
	t.Run("S109_goal_followup_from_a_telegram_created_goal", func(t *testing.T) {
		resetGoalTriggerStateForTest()
		t.Cleanup(resetGoalTriggerStateForTest)
		f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
			cfg.Sandbox.ToolPolicies["set_goal"] = "allow"
			cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", WorkspaceID: "sales", ProgramsAllowed: false}}
		})
		programsWireMessaging(t, f)
		sid := f.mainSession("sales", "dev")
		inst, ok := f.al.GetRegistry().GetAgent("dev")
		require.True(t, ok)
		observer := &programsGoalOriginProvider{wu5ScriptedProvider: f.provider}
		inst.Provider = observer
		positive, result := programsCountInput(t, f, sid, "s109-connected-control", programsWebInput())
		require.Equal(t, "16", positive, "instrument: real connected Dev web child receives the saved token")
		require.NotContains(t, result, "Connected programs were not used for this command:")
		beforeCreate := len(observer.observed())
		const definition = "Run the requested command for the Telegram-origin goal."
		const createCall = "s109-actual-set-goal"
		args := map[string]any{"definition": definition, "criteria": []any{map[string]any{"text": "The requested command has actually executed.", "judgment": "boolean"}}, "dod": []any{map[string]any{"text": "No secret value is disclosed.", "judgment": "boolean", "provenance": "stated"}}}
		f.provider.setScript(sid, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: createCall, Type: "function", Name: "set_goal", Arguments: args}}}, wu5Stop("goal registered; continuing later")})
		input := programsConnectorInput()
		input.SessionID, input.ChatID = sid, sid
		_, _, err := f.al.processMessage(context.Background(), input)
		require.NoError(t, err)
		var receipt map[string]json.RawMessage
		text := f.provider.resultForCall(sid, createCall)
		require.NoError(t, json.Unmarshal([]byte(text), &receipt), "instrument: actual set_goal accepted, not a policy/setup refusal: %s", text)
		var gid string
		require.NoError(t, json.Unmarshal(receipt["goal_id"], &gid))
		require.NotEmpty(t, gid)
		store := goal.NewStore(f.home)
		created, err := store.Get(gid)
		require.NoError(t, err)
		require.Equal(t, sid, created.OwnerID)
		require.Equal(t, sid, created.ActiveSessionID)
		require.Equal(t, definition, created.Definition)
		creationOrigins := observer.observed()[beforeCreate:]
		require.GreaterOrEqual(t, len(creationOrigins), 2)
		for _, actual := range creationOrigins[:2] {
			assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), actual, "S109 Given: actual goal creator and result-consumption turn are Telegram")
		}
		body, err := os.ReadFile(filepath.Join(store.Dir(), gid+".json"))
		require.NoError(t, err)
		var document map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &document))
		var storedOrigins []string
		if raw, present := document["provenance"]; present {
			assert.NoError(t, json.Unmarshal(raw, &storedOrigins))
		}
		assert.ElementsMatch(t, []string{"connector:telegram"}, storedOrigins, "S109/I-2: the actual goal retains its creator origin, not a test-stamped root")
		// Control the clock edge only. Do not rewrite goal activity, routing or
		// provenance, call a notifier directly, or replace the keeper/dispatcher.
		quiet := time.Duration(f.al.GetConfig().Planning.EffectiveGoalReminderQuietSeconds()) * time.Second
		f.al.goalQuietWindowSettle(time.Now().Add(quiet + time.Second))
		var wake bus.InboundMessage
		select {
		case wake = <-f.bus.InboundChan():
		case <-time.After(6 * time.Second):
			t.Fatal("actual keeper published no follow-up for the real active, quiet goal")
		}
		require.Equal(t, "system", wake.Channel)
		require.Equal(t, goalLoopFollowUpSenderID, wake.Sender.CanonicalID, "instrument: keeper-generated wake, not an unrelated async message")
		require.Equal(t, sid, wake.AsyncTranscriptSessionID)
		assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), wake.Provenance, "S109/I-2: actual goal keeper/notifier preserves the Telegram goal origin")
		path := filepath.Join(f.home, "workspaces", "sales", "work", "s109-followup-count")
		command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'S109_GOAL_CHILD_RAN'", path)
		const childCall = "s109-followup-child"
		f.provider.setScript(sid, []providers.LLMResponse{wu5BashCall(childCall, command), wu5Stop("goal follow-up command completed")})
		beforeFollowup := len(observer.observed())
		_, err = f.al.processSystemMessage(context.Background(), wake)
		require.NoError(t, err, "instrument: consume the same actual keeper wake through the real system-message path")
		f.waitForCalls(sid, 2, "actual goal-keeper follow-up command and result")
		result = f.provider.resultForCall(sid, childCall)
		require.Contains(t, result, "S109_GOAL_CHILD_RAN", "instrument: actual child ran; refusal cannot satisfy count0")
		rawCount, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "0", strings.TrimSpace(string(rawCount)), "S109: actual Telegram-created goal follow-up gets no program token")
		assert.Contains(t, result, programsTelegramNotice, "S109: full Telegram-off notice, not an unresolved notice")
		followupOrigins := observer.observed()[beforeFollowup:]
		require.GreaterOrEqual(t, len(followupOrigins), 2)
		for _, actual := range followupOrigins[:2] {
			assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), actual, "S109/I-2: actual receiving follow-up model command/result context")
		}
		t.Logf("actual Telegram goal -> keeper -> notifier -> consumer child_ran=true count=%s saved_origins=%v wake_origins=%v", strings.TrimSpace(string(rawCount)), storedOrigins, wake.Provenance.Origins())
	})
}
