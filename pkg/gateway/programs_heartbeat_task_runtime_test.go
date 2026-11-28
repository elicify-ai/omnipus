package gateway

// Spec rev8 S124/I-2/A-1: the real heartbeat's independent origin survives an
// actual creator edit from Telegram. Every contributing origin must be allowed.
import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualHeartbeatTask(t *testing.T) {
	for _, tc := range []struct {
		name       string
		webAllowed bool
		count      string
	}{
		{"S124_independent_telegram_join_web_off_count0", false, "0"},
		{"S124_independent_telegram_join_web_on_count16", true, "16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newProgramsTaskFixture(t) // Actual connected web positive count16.
			cfg := x.al.GetConfig()
			cfg.Programs.WebChatAllowed = tc.webAllowed
			telegram := cfg.Channels["telegram"]
			telegram.ProgramsAllowed = true
			cfg.Channels["telegram"] = telegram
			const initial = "prepare the heartbeat's Ops command"
			const edited = "run printenv GH_TOKEN | wc -c"
			id := programsHeartbeatCreateTask(t, x, initial)
			programsTaskUseTelegramConversation(t, x, tc.name)
			atWrite := programsCaptureTaskWrite(t, x, id, func() {
				programsAgentEditTask(t, x, id, "s124-telegram-creator-edit", edited)
			})
			writerOrigins := x.provider.observedOrigins(x.dev)
			require.GreaterOrEqual(t, len(writerOrigins), 2, "instrument: actual Telegram creator edit and result consumption")
			for _, actual := range writerOrigins[:2] {
				assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), actual, "S124: actual editor turn is Telegram, not a manually joined carrier")
			}
			expected := []string{"independent", "connector:telegram"}
			programsAssertTaskDocument(t, atWrite, edited, expected, "S124 accepted creator edit's same in-lock persisted write")
			programsAssertTaskDocument(t, programsReadTaskDocument(t, x, id), edited, expected, "S124 after actual creator edit returns")
			count, result, sid := x.runBoard(t, id)
			programsAssertTaskRunOrigin(t, x, sid, expected)
			assert.Equal(t, tc.count, count, "S124/A-1: independent follows web; allowing Telegram alone must not authorize the task")
			if tc.webAllowed {
				assert.NotContains(t, result, "Connected programs were not used for this command:", "S124: all contributing origins are allowed")
			} else {
				// Exact M8 notice comes from the spec, not runtime output.
				assert.Contains(t, result, "Connected programs were not used for this command: programs are off for the web chat. A person can switch them on in Settings → Programs.", "S124/A-1: independent is governed by the web setting, not unresolved")
			}
			t.Logf("actual heartbeat/create/Telegram-edit/task child_ran=true web_allowed=%v count=%s", tc.webAllowed, count)
		})
	}
}

func TestProgramProvenance_ActualPersonTaskAuthority(t *testing.T) {
	t.Run("S124_telegram_agent_cannot_edit_person_task_prompt_or_provenance", func(t *testing.T) {
		x := newProgramsTaskFixture(t)
		programsTaskUseTelegramConversation(t, x, "s124-person-authority")
		const prompt = "person's unchanged original Ops instructions"
		id := x.personCreate(t, prompt)
		before := programsReadTaskDocument(t, x, id)
		programsAssertTaskDocument(t, before, prompt, []string{"independent"}, "S124 Given: actual person-created U")
		events := x.al.SubscribeEvents(256)
		defer x.al.UnsubscribeEvents(events.ID)
		const call = "s124-forbidden-person-task-edit"
		x.provider.setScript(x.dev, []providers.LLMResponse{
			{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "update_task", Arguments: map[string]any{"task_id": id, "prompt": "unapproved Telegram replacement"}}}},
			wu5StopGW("person task edit refused"),
		})
		input := bus.InboundMessage{Channel: "telegram", InstanceID: "telegram", SessionID: x.dev, ChatID: x.dev, Content: "change the person's Ops task prompt", Sender: bus.SenderInfo{CanonicalID: "telegram:s1", DisplayName: "S1 person"}, Metadata: map[string]string{"workspace_id": "sales", "agent_id": "dev"}, Provenance: bus.NewProvenance(bus.ConnectorOrigin("telegram"))}
		require.NoError(t, x.bus.PublishInbound(context.Background(), input))
		x.waitEnds(t, x.dev)
		var matched []agent.ToolExecEndPayload
		// TurnEnd follows the synchronously published tool outcome. This second
		// subscription retains the actual tool event that waitEnds consumes.
		for len(events.C) > 0 {
			event := <-events.C
			if event.Kind != agent.EventKindToolExecEnd {
				continue
			}
			outcome, ok := event.Payload.(agent.ToolExecEndPayload)
			require.True(t, ok)
			if outcome.SessionID == x.dev && string(outcome.ToolCallID) == call && outcome.Tool == "update_task" {
				matched = append(matched, outcome)
			}
		}
		require.Len(t, matched, 1, "instrument: one actual matching registered update_task result, not an inferred refusal")
		assert.True(t, matched[0].IsError, "S124: non-creator/non-assignee edit must be refused")
		assert.False(t, matched[0].Cancelled, "S124: authority refusal, not a cancelled tool")
		assert.Equal(t, matched[0].Result, x.provider.result(x.dev, call), "instrument: model consumed the actual refusal result")
		// Characterization of the existing authority wording from the frozen
		// TestTaskUpdate_OwnershipRejection; semantic refusal/no-write is S124.
		assert.Contains(t, matched[0].Result, "you can only update tasks you own or are assigned")
		after := programsReadTaskDocument(t, x, id)
		assert.Equal(t, before, after, "S124 authority regression: the complete actual task document is byte-identical")
		programsAssertTaskDocument(t, after, prompt, []string{"independent"}, "S124 person task after actual refused Telegram edit")
		origins := x.provider.observedOrigins(x.dev)
		require.GreaterOrEqual(t, len(origins), 2)
		for _, actual := range origins[:2] {
			assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), actual, "instrument: the actual refused caller was Telegram")
		}
		t.Logf("actual person task authority refused=%v cancelled=%v byte_identical=%v", matched[0].IsError, matched[0].Cancelled, string(before) == string(after))
	})
}
