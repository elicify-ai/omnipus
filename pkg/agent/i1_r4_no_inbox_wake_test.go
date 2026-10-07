package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Silent C: exercise the real finishSteeredGoalTurn -> Notify -> bus writer,
// then land/fence its ordinary recipient before consuming the no-inbox wake.
// The brief's minimum is an operator WARN with session and kind, never compute
// or a fabricated claim that the body has been retained.
func TestI1R4NoInboxStoppedWakeWarns(t *testing.T) {
	for _, state := range []string{"landed_stop", "in_flight_stop"} {
		t.Run(state, func(t *testing.T) {
			h := i1R3RestartStopped(t)
			ls := h.al.GetSessionLifecycleStore()
			require.NoError(t, ls.Mutate(h.id, func(rec *session.LifecycleRecord) error {
				rec.State, rec.StopNote, rec.GoalRef = session.LifecycleRunning, nil, "i1-r4-goal"
				return nil
			}))
			inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
			require.True(t, ok)
			const content = "Private goal follow-up content must not appear in the warning."
			ts := &turnState{agent: inst, agentID: testDefaultAgentID,
				opts: processOptions{TranscriptSessionID: h.id, TranscriptStore: h.al.GetSessionStore()}}
			result := turnResult{followUps: []bus.InboundMessage{{Channel: "system", ChatID: "steer:" + h.id,
				SessionID: h.id, Content: content, Sender: bus.SenderInfo{CanonicalID: goalLoopFollowUpSenderID}}}}
			h.al.finishSteeredGoalTurn(ts, h.load(t), &result, nil)
			var wake bus.InboundMessage
			select {
			case wake = <-h.al.bus.InboundChan():
			case <-time.After(5 * time.Second):
				t.Fatal("actual goal follow-up publisher must produce the wake under test")
			}
			require.Equal(t, content, wake.Content)
			require.Equal(t, h.id, wake.AsyncTranscriptSessionID)
			require.Equal(t, goalLoopFollowUpSenderID, wake.Sender.CanonicalID)
			require.NotEmpty(t, wake.Metadata["steer_message_id"])
			inboxBefore, err := h.al.GetMessageInboxStore().Entries(h.id)
			require.NoError(t, err)
			require.Empty(t, inboxBefore, "producer must genuinely have no durable inbox row")
			require.NoError(t, ls.Mutate(h.id, func(rec *session.LifecycleRecord) error {
				if state == "landed_stop" {
					rec.State = session.LifecycleStopped
					rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: session.StopActorRestart,
						Cause: session.StopCauseRestart, BootSeq: h.al.bootEpochFor(), Seq: 1}
				} else {
					rec.Stop = &session.Stop{At: time.Now().UTC(), Generation: rec.Generation,
						By: session.Principal{Kind: "human", ID: "d2b-owner"}}
				}
				return nil
			}))
			journal, transcript := h.journal(t), h.prior
			path := filepath.Join(t.TempDir(), "held-wake.log")
			require.NoError(t, logger.EnableFileLogging(path))
			t.Cleanup(logger.DisableFileLogging)
			logger.WarnCF("agent", "i1-r4 warning capture control", map[string]any{"site": "warning_capture_probe"})
			response, wakeErr := h.al.processSteeredSystemWake(context.Background(), wake)
			require.NoError(t, wakeErr)
			assert.Empty(t, response)
			assert.Empty(t, h.provider.calls(), "stopped wake must never perform compute")
			assert.Equal(t, journal, h.journal(t))
			entries, err := h.al.GetSessionStore().ReadTranscript(h.id)
			require.NoError(t, err)
			assert.Equal(t, transcript, entries, "no consumption or synthetic content-retention claim")
			inboxAfter, err := h.al.GetMessageInboxStore().Entries(h.id)
			require.NoError(t, err)
			assert.Equal(t, inboxBefore, inboxAfter)
			logger.DisableFileLogging()
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), content, "operator warning must not expose the goal body")
			found, instrument := false, false
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var row map[string]any
				require.NoError(t, json.Unmarshal([]byte(line), &row))
				if row["site"] == "warning_capture_probe" && row["level"] == "warn" {
					instrument = true
				}
				if row["session_id"] == h.id && row["message_id"] == wake.Metadata["steer_message_id"] {
					found = true
					assert.Equal(t, "warn", row["level"], "no-inbox content loss must be visible at WARN")
					assert.Equal(t, "system:goal_loop", row["kind"], "existing sender category must identify the producer family")
				}
			}
			require.True(t, instrument, "log capture must first prove it can observe a known WARN")
			assert.True(t, found, "held no-inbox wake must be identified by exact session and message")
		})
	}
}
