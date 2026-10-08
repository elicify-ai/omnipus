package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Silent C / round 5: real no-inbox goal wakes warn; real durable parent
// reports are held with truthful INFO. An unrelated row cannot prove retention.
// Both landed and in-flight Stop keep body, consumption and compute untouched.
func TestI1R4NoInboxStoppedWakeWarns(t *testing.T) {
	for _, state := range []string{"landed_stop", "in_flight_stop"} {
		for _, backing := range []string{"no_entry", "matching_entry", "unrelated_entry"} {
			t.Run(state+"/"+backing, func(t *testing.T) {
				h := i1R3RestartStopped(t)
				wake := i1R5WakeForRetention(t, h, backing)
				inboxBefore, err := h.al.GetMessageInboxStore().Entries(h.id)
				require.NoError(t, err)
				if backing == "no_entry" {
					require.Empty(t, inboxBefore, "producer must genuinely have no durable inbox row")
				} else {
					require.Len(t, inboxBefore, 1)
					require.NotNil(t, inboxBefore[0].Message)
					if backing == "matching_entry" {
						require.Equal(t, wake.Metadata["steer_message_id"], messageIDOf(*inboxBefore[0].Message))
					} else {
						require.NotEqual(t, wake.Metadata["steer_message_id"], messageIDOf(*inboxBefore[0].Message))
					}
				}
				require.NoError(t, h.al.GetSessionLifecycleStore().Mutate(h.id, func(rec *session.LifecycleRecord) error {
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
				journal := h.journal(t)
				path := filepath.Join(t.TempDir(), "held-wake.log")
				previousLevel := logger.GetLevel()
				logger.SetLevel(logger.INFO)
				t.Cleanup(func() { logger.SetLevel(previousLevel) })
				require.NoError(t, logger.EnableFileLogging(path))
				t.Cleanup(logger.DisableFileLogging)
				logger.WarnCF("agent", "i1-r5 warning capture control", map[string]any{"site": "warning_capture_probe"})
				logger.InfoCF("agent", "i1-r5 info capture control", map[string]any{"site": "info_capture_probe"})
				response, wakeErr := h.al.processSteeredSystemWake(context.Background(), wake)
				require.NoError(t, wakeErr)
				assert.Empty(t, response)
				assert.Empty(t, h.provider.calls(), "stopped wake must never perform compute")
				assert.Equal(t, journal, h.journal(t))
				entries, err := h.al.GetSessionStore().ReadTranscript(h.id)
				require.NoError(t, err)
				assert.Equal(t, h.prior, entries, "no consumption or synthetic content-retention claim")
				inboxAfter, err := h.al.GetMessageInboxStore().Entries(h.id)
				require.NoError(t, err)
				assert.Equal(t, inboxBefore, inboxAfter)
				if backing == "matching_entry" {
					pending, _, _, drainErr := h.al.GetMessageInboxStore().Drain(h.id, "i1-r5-child", "", 10)
					require.NoError(t, drainErr)
					require.Len(t, pending, 1, "durable report must remain unacknowledged and available")
					assert.Equal(t, wake.Metadata["steer_message_id"], messageIDOf(pending[0]))
				}
				logger.DisableFileLogging()
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), wake.Content, "held-wake diagnostics must not expose the message body")
				found, instrument, infoInstrument := false, false, false
				for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
					var row map[string]any
					require.NoError(t, json.Unmarshal([]byte(line), &row))
					if row["site"] == "warning_capture_probe" && row["level"] == "warn" {
						instrument = true
					}
					if row["site"] == "info_capture_probe" && row["level"] == "info" {
						infoInstrument = true
					}
					if row["session_id"] != h.id || row["message_id"] != wake.Metadata["steer_message_id"] {
						continue
					}
					found = true
					if backing == "matching_entry" {
						assert.Equal(t, "info", row["level"], "durable pending report is not a content-loss warning")
						assert.Equal(t, "steer: wake held; retained pending", row["message"])
						assert.Equal(t, "async:message_parent:handback", row["kind"])
					} else {
						assert.Equal(t, "warn", row["level"], "no matching entry means the wake body is not retained")
						assert.Equal(t, "steer: wake held; content is not retained", row["message"])
						assert.Equal(t, "system:goal_loop", row["kind"])
					}
				}
				require.True(t, instrument, "log capture must prove it can observe a known WARN")
				require.True(t, infoInstrument, "log capture must also observe a known INFO before trusting that branch")
				assert.True(t, found, "held wake must be identified by exact session and message")
			})
		}
	}
}
