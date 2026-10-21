package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// SFH HIGH: fail the actual notice append once, then permit the deferred
// finalizer's writes. Permanent write failures would hide the provenance bug.
// Preserve the real gateway tap rather than replacing delivery with a recorder.
func TestIterationLimitNoticeSaveFailure(t *testing.T) {
	for _, followUp := range []bool{false, true} {
		name := "without_followup"
		if followUp {
			name = "with_automatic_followup"
		}
		t.Run(name, func(t *testing.T) {
			h := newCeilingLiveHarness(t, 2, true)
			fault := &iterationNoticeFault{
				path:   filepath.Join(h.store.BaseDir(), h.sid, "transcript.jsonl"),
				backup: filepath.Join(t.TempDir(), "saved-transcript.jsonl"),
				scope:  "agent:mia:session:" + h.sid,
			}
			h.al.SetEventSyncTap(func(event agent.Event) {
				h.handler.hubSyncTap(event)
				fault.observe(event)
			})
			t.Cleanup(func() { assert.NoError(t, fault.restore()) })
			h.send(t, "Read the two notes.", 0)
			h.awaitTool(t, 0)
			const input = "CEILING-FAILED-SAVE-FOLLOWUP: use the completed notes."
			if followUp {
				h.send(t, input, 1)
				h.awaitSteers(t, 1)
			}
			h.tool.open(0)
			h.awaitTool(t, 1)
			h.tool.open(1)
			wantOutbound := agent.MessageNotSavedReply
			if followUp {
				wantOutbound = ceilingLiveAnswer
			}
			select {
			case outbound := <-h.outbound:
				assert.Equal(t, wantOutbound, outbound.Content, "failure or accepted follow-up is explicitly accounted for")
			case <-time.After(busDeliveryTimeout):
				t.Fatal("notice-write failure stranded the worker's outbound disposition")
			}
			h.openRelay()
			select {
			case delivery := <-h.delivered:
				require.NoError(t, delivery.err)
			case <-time.After(busDeliveryTimeout):
				t.Fatal("failure-path outbound Send did not finish")
			}
			armed, restored, probeErr, setupErr, saveErrors, failedTurn, errorText := fault.snapshot()
			require.True(t, armed, "the real append was obstructed after tool 2 was stored")
			require.Error(t, probeErr, "instrument: writing the obstructed file really fails")
			var pathErr *os.PathError
			require.ErrorAs(t, probeErr, &pathErr)
			require.NoError(t, setupErr, "fault setup and recovery must not manufacture the failure")
			require.True(t, restored, "later writes must be possible, exposing any unjustified assistant fallback")
			assert.Equal(t, 1, saveErrors, "one save failure is surfaced, not swallowed or silently retried")
			assert.Equal(t, agent.MessageNotSavedReply, errorText)
			require.NotEmpty(t, failedTurn)
			entries, err := h.store.ReadTranscript(h.sid)
			require.NoError(t, err)
			var narration []string
			for _, entry := range entries {
				assert.NotEqual(t, terminalAcceptanceCapNotice, entry.Content,
					"an unsaved harness notice cannot reappear through any assistant/system fallback (role=%s model=%s)", entry.Role, entry.Model)
				if entry.Role == "assistant" && strings.HasPrefix(entry.Content, "Reading note ") {
					narration = append(narration, entry.Content)
				}
			}
			assert.Equal(t, []string{"Reading note 1.", "Reading note 2."}, narration, "completed model narration remains intact")
			frames := terminalAcceptanceCollect(t, h.conn)
			var visibleErrors, failedDone int
			var liveAnswer strings.Builder
			for _, frame := range frames {
				switch frame.Type {
				case "token":
					var token generated.TokenFrame
					require.NoError(t, json.Unmarshal(frame.Raw, &token))
					assert.NotContains(t, token.Content, terminalAcceptanceCapNotice)
					if strings.Contains(token.Content, ceilingLiveAnswer) {
						liveAnswer.WriteString(token.Content)
					}
				case "replay_message":
					var notice generated.ReplayMessageFrame
					require.NoError(t, json.Unmarshal(frame.Raw, &notice))
					assert.NotEqual(t, terminalAcceptanceCapNotice, notice.Content, "a failed save is not presented as a saved system notice")
				case "error":
					var notice generated.ErrorFrame
					require.NoError(t, json.Unmarshal(frame.Raw, &notice))
					if notice.Message == agent.MessageNotSavedReply {
						visibleErrors++
					}
				case "done":
					var done generated.DoneFrame
					require.NoError(t, json.Unmarshal(frame.Raw, &done))
					if done.TurnId != nil && *done.TurnId == failedTurn && done.Stats != nil {
						require.NotNil(t, done.Stats.TurnFailed)
						assert.True(t, *done.Stats.TurnFailed)
						failedDone++
					}
				}
			}
			assert.Equal(t, 1, visibleErrors, "the existing save-error notification is actually delivered")
			assert.Equal(t, 1, failedDone, "the failed notice does not turn into a successful completion")
			requests := h.provider.snapshot()
			if followUp {
				require.Len(t, requests, 3, "the accepted follow-up is drained, not silently discarded")
				ceilingLiveAssertContext(t, requests[2], []string{input})
				assert.Equal(t, ceilingLiveAnswer, liveAnswer.String(), "the follow-up still arrives live exactly once")
			} else {
				assert.Len(t, requests, 2, "a save error alone cannot restart model/tool work")
				assert.Empty(t, liveAnswer.String())
			}
			assert.Equal(t, int32(2), h.tool.calls.Load())
		})
	}
}
