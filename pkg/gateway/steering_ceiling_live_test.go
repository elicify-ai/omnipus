package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Round-1 C1/I1/I2: unlike the earlier split fixtures this composes the actual
// worker, manager fallback, gateway, outbound relay, socket and durable store.
// The three-input case uses normal budget 1 / hard safety bound 2 so a third
// input accepted during tool 2 genuinely stays queued across the hard boundary.
func TestSteeringCeilingGatewayDrain(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		streaming                       bool
		budget, firstBatch, secondBatch int
	}{
		{"plain_budget_2", false, 2, 1, 0},
		{"streaming_control_budget_2", true, 2, 1, 0},
		{"plain_multiple_followups_at_hard_bound", false, 1, 2, 1},
		{"streaming_multiple_followups_control", true, 1, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCeilingLiveHarness(t, tc.budget, tc.streaming)
			h.send(t, "Read two notes, one at a time.", 0)
			h.awaitTool(t, 0)
			var inputs []string
			for i := 1; i <= tc.firstBatch; i++ {
				text := fmt.Sprintf("CEILING-FOLLOWUP-%d: keep this instruction in order.", i)
				inputs = append(inputs, text)
				h.send(t, text, i)
			}
			h.awaitSteers(t, tc.firstBatch)
			originals := ceilingLiveInputEntries(t, h, inputs)
			h.tool.open(0)
			h.awaitTool(t, 1)
			for i := tc.firstBatch + 1; i <= tc.firstBatch+tc.secondBatch; i++ {
				text := fmt.Sprintf("CEILING-FOLLOWUP-%d: keep this instruction in order.", i)
				inputs = append(inputs, text)
				h.send(t, text, i)
			}
			if tc.secondBatch > 0 {
				h.awaitSteers(t, tc.secondBatch)
				originals = ceilingLiveInputEntries(t, h, inputs)
			}
			h.tool.open(1)
			select {
			case message := <-h.outbound:
				require.Equal(t, ceilingLiveAnswer, message.Content, "the worker drains before publishing its final outbound answer")
			case <-time.After(busDeliveryTimeout):
				t.Fatal("automatic worker continuation never produced its outbound answer")
			}
			// Hold asynchronous Send until both turns have run. No manual notice
			// Send is allowed to hide a stale previous-turn delivery flag.
			h.openRelay()
			select {
			case delivery := <-h.delivered:
				require.NoError(t, delivery.err)
				require.Equal(t, ceilingLiveAnswer, delivery.message.Content)
			case <-time.After(busDeliveryTimeout):
				t.Fatal("real outbound Send did not settle")
			}
			requests := h.provider.snapshot()
			require.Len(t, requests, 3, "two original rounds plus one fresh turn; no resend or self-renewal")
			assert.Equal(t, int32(2), h.tool.calls.Load(), "no completed tool is replayed")
			ceilingLiveAssertContext(t, requests[2], inputs)
			inst, found := h.al.GetRegistry().GetAgent("mia")
			require.True(t, found)
			history := inst.Sessions.GetHistory("agent:mia:session:" + h.sid)
			ceilingLiveAssertInputOrder(t, history, inputs)
			assert.Equal(t, originals, ceilingLiveInputEntries(t, h, inputs), "accepted entry/client identities remain unchanged")
			var ends []agent.Event
			for len(h.sub.C) > 0 {
				event := <-h.sub.C
				if event.Kind == agent.EventKindTurnEnd {
					ends = append(ends, event)
				}
			}
			require.Len(t, ends, 2, "both actual turns settle, with nothing left stranded")
			assert.NotEqual(t, ends[0].Meta.TurnID, ends[1].Meta.TurnID)
			frames := terminalAcceptanceCollect(t, h.conn)
			ceilingLiveAssertDelivery(t, h, frames, ends[0].Meta.TurnID, ends[1].Meta.TurnID)
		})
	}
}

func ceilingLiveInputEntries(t *testing.T, h *ceilingLiveHarness, inputs []string) []session.TranscriptEntry {
	t.Helper()
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	var found []session.TranscriptEntry
	for _, entry := range entries {
		for _, input := range inputs {
			if entry.Content == input {
				assert.Equal(t, "user", entry.Role)
				assert.NotEmpty(t, entry.ID)
				found = append(found, entry)
			}
		}
	}
	require.Len(t, found, len(inputs), "each accepted follow-up has exactly one durable original")
	for i, entry := range found {
		assert.Equal(t, inputs[i], entry.Content, "accepted input order is stable")
		assert.Equal(t, fmt.Sprintf("ceiling-live-input-%d", i+1), entry.ClientMessageID)
	}
	return found
}

func ceilingLiveAssertDelivery(t *testing.T, h *ceilingLiveHarness, frames []collectedFrame, originalTurn, answerTurn string) {
	t.Helper()
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	var notices, answers []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Content == terminalAcceptanceCapNotice {
			notices = append(notices, entry)
			assert.Equal(t, session.EntryTypeSystem, entry.Type)
			assert.Equal(t, "system", entry.Role)
			assert.Empty(t, entry.Model)
			assert.Equal(t, originalTurn, entry.TurnID)
		}
		if entry.Content == ceilingLiveAnswer {
			answers = append(answers, entry)
			assert.Equal(t, "assistant", entry.Role)
			assert.Equal(t, answerTurn, entry.TurnID)
		}
	}
	require.Len(t, notices, 1)
	require.Len(t, answers, 1)
	var liveNotices []generated.ReplayMessageFrame
	var answerText strings.Builder
	for _, frame := range frames {
		switch frame.Type {
		case "replay_message":
			var notice generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(frame.Raw, &notice))
			if notice.Content == terminalAcceptanceCapNotice {
				liveNotices = append(liveNotices, notice)
				assert.Equal(t, "system", notice.Role)
				assert.Nil(t, notice.Model)
				require.NotNil(t, notice.Id)
				assert.Equal(t, notices[0].ID, *notice.Id)
			}
		case "token":
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(frame.Raw, &token))
			assert.NotContains(t, token.Content, terminalAcceptanceCapNotice, "a system notice never returns as model tokens")
			if strings.Contains(token.Content, ceilingLiveAnswer) {
				answerText.WriteString(token.Content)
				require.NotNil(t, token.MessageId, "live answer identifies its durable output")
				require.NotNil(t, token.TurnId)
				assert.Equal(t, answers[0].ID, *token.MessageId)
				assert.Equal(t, answerTurn, *token.TurnId)
			}
		}
	}
	assert.Len(t, liveNotices, 1, "exactly one unattributed system notice is visible")
	assert.Equal(t, ceilingLiveAnswer, answerText.String(), "the drained answer arrives live exactly once; frames=%v", collectedFrameTypes(frames))
}
