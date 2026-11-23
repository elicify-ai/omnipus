package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Source for R4-GW-A/B/C: team-lead addition, 2026-11-23, founder Q13 B,
// OC-1899-3. Board task-run chat remains REFUSED in this landing, but an
// existing-session refusal must be addressed and saved there for reload.
// TEXT.md §3 now supplies the exact non-catalogue sentence. Keep it literal,
// independently of the gateway implementation and generated error catalogue.
const opencodeR4TaskChatTemplate = "I couldn't deliver your message: this task runs in {cli} and doesn't accept chat messages here. To change the work, open Tasks and stop the task or its plan before editing the task."

func opencodeR4TaskChatSentence(cli string) string {
	display := map[string]string{"claude-code": "Claude Code", "codex": "Codex", "opencode": "OpenCode"}[cli]
	return strings.ReplaceAll(opencodeR4TaskChatTemplate, "{cli}", display)
}

func TestOpenCodeR4_GatewayA_TaskRunWorkerRefusalIsSessionTagged(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		for _, explicit := range []bool{true, false} {
			name := cli + "/owner_resolved"
			if explicit {
				name = cli + "/explicit_worker"
			}
			t.Run(name, func(t *testing.T) {
				f := opencodeR4GatewayFixture(t, cli)
				f.send(t, f.sid, explicit)
				refusals, frameTypes := opencodeR4GatewayFrames(t, f.conn)
				f.assertNoAdmission(t)
				assert.NotContains(t, frameTypes, string(generated.WsFrameTypeSessionStarted), "Q13 B: no replacement chat may be created")
				// CHECK round5 F3: A must independently catch a live-only/no-save refusal.
				saved := opencodeR5ErrorRows(qa2ReopenHelperTranscript(t, f.handler.agentLoop.ResolveSessionStore(f.sid), f.sid))
				if assert.Len(t, saved, 1, "GatewayA: the live refusal must also survive a fresh-store read") {
					assert.Equal(t, opencodeR4TaskChatSentence(cli), saved[0].Content)
					assert.Empty(t, saved[0].ErrorCode, "task-chat intake refusal is uncoded")
					assert.NotEmpty(t, saved[0].ID, "saved refusal has its own durable identity")
				}
				if assert.Len(t, refusals, 1, "Q13 B: a task-run chat frame gets one visible refusal, never silence or acceptance") {
					assert.Equal(t, &f.sid, refusals[0].SessionId, "Q13 B: error must identify the addressed task-run session")
					assert.Equal(t, opencodeR4TaskChatSentence(cli), refusals[0].Message, "Q13 B: exact approved TEXT.md section 3 refusal")
				}
			})
		}
	}
}

func TestOpenCodeR4_GatewayB_TaskRunWorkerRefusalSurvivesReload(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		for _, explicit := range []bool{true, false} {
			name := cli + "/owner_resolved"
			if explicit {
				name = cli + "/explicit_worker"
			}
			t.Run(name, func(t *testing.T) {
				f := opencodeR4GatewayFixture(t, cli)
				f.send(t, f.sid, explicit)
				live, _ := opencodeR4GatewayFrames(t, f.conn)
				f.assertNoAdmission(t)
				// Read a freshly opened actual store, not the original cache.
				entries := qa2ReopenHelperTranscript(t, f.handler.agentLoop.ResolveSessionStore(f.sid), f.sid)
				var saved []session.TranscriptEntry
				for _, entry := range entries {
					if entry.Type == session.EntryTypeSystem && entry.Status == "error" {
						saved = append(saved, entry)
					}
				}
				if assert.Len(t, saved, 1, "Q13 B: exactly one refusal row must survive a fresh transcript-store read") {
					assert.Equal(t, opencodeR4TaskChatSentence(cli), saved[0].Content, "Q13 B: save the exact approved task-chat refusal")
				}
				var replayed []generated.ReplayErrorFrame
				_, err := streamReplay(context.Background(), f.sid, entries, computeReplayStats(entries), func(frame any) error {
					raw, marshalErr := json.Marshal(frame)
					if marshalErr != nil {
						return marshalErr
					}
					var envelope wsTypeOnly
					if decodeErr := json.Unmarshal(raw, &envelope); decodeErr != nil {
						return decodeErr
					}
					if envelope.Type == string(generated.WsFrameTypeReplayError) {
						var refusal generated.ReplayErrorFrame
						if decodeErr := json.Unmarshal(raw, &refusal); decodeErr != nil {
							return decodeErr
						}
						replayed = append(replayed, refusal)
					}
					return nil
				}, nil, nil, nil, f.handler.agentLoop.GetSessionLifecycleStore())
				require.NoError(t, err)
				if assert.Len(t, replayed, 1, "Q13 B: actual history replay must show one saved refusal after reload") && len(saved) == 1 {
					assert.Equal(t, f.sid, replayed[0].SessionId)
					assert.NotEmpty(t, saved[0].ID, "saved refusal has a durable identity")
					assert.Equal(t, saved[0].ID, replayed[0].EntryId)
					assert.Equal(t, opencodeR4TaskChatSentence(cli), replayed[0].Message, "Q13 B: replay the exact approved refusal")
					assert.Equal(t, saved[0].Content, replayed[0].Message, "live/durable/replay text must agree with the approved wording")
					if assert.Len(t, live, 1) {
						assert.Equal(t, live[0].Message, replayed[0].Message)
					}
				}
			})
		}
	}
}

// R4-GW-C is a regression control: a frame without a session must remain
// refused and must never mint a standalone worker chat. Baseline PASS is valid.
func TestOpenCodeR4_GatewayC_WorkerWithoutSessionStillRefuses(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := opencodeR4GatewayFixture(t, cli)
			before, err := f.handler.agentLoop.GetSessionStore().ListSessions()
			require.NoError(t, err)
			f.send(t, "", true)
			refusals, frameTypes := opencodeR4GatewayFrames(t, f.conn)
			f.assertNoAdmission(t)
			if assert.Len(t, refusals, 1, "control: no-session worker frame must still be refused") {
				assert.Nil(t, refusals[0].SessionId, "no requested session exists to tag")
				assert.Contains(t, strings.ToLower(refusals[0].Message), "worker")
			}
			assert.NotContains(t, frameTypes, string(generated.WsFrameTypeSessionStarted))
			after, err := f.handler.agentLoop.GetSessionStore().ListSessions()
			require.NoError(t, err)
			assert.Equal(t, before, after, "control: worker refusal must not create or alter a session")
		})
	}
}
