package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Oracles: chief's round-1 F1 (restore the exact unprocessed batch on save
// failure) and F2 (direct-response steering also keeps the cut-off mark).
func TestSteeringReviewRound1(t *testing.T) {
	t.Run("F1_save_failure", testSteeringReviewSaveFailure)
	t.Run("F2_direct_response_marker", testSteeringReviewDirectMarker)
}

func testSteeringReviewSaveFailure(t *testing.T) {
	for _, boundary := range []string{"natural_exit", "direct_response"} {
		for _, storage := range []string{"model_window", "transcript"} {
			t.Run(boundary+"/"+storage, func(t *testing.T) {
				const reply = "Recovered follow-ups answered once."
				provider := &truncationScriptedProvider{steps: []truncationScriptStep{
					{content: "The cut-off partial.", finishReason: "truncated"},
					{content: reply, finishReason: "stop"},
				}}
				al, inst, store, sid := newTruncationTestHarness(t, provider, 1, 0)
				key := "agent:" + inst.ID + ":session:" + sid
				batch := []steeringQueueItem{
					{message: providers.Message{Role: "user", Content: "First accepted follow-up."}, correlationID: "r1-first", transcriptEntryID: "r1-human-first"},
					{message: providers.Message{Role: "user", Content: "Second accepted follow-up."}, correlationID: "r1-second", transcriptEntryID: "r1-human-second"},
				}
				newer := steeringQueueItem{message: providers.Message{Role: "user", Content: "Later arrival while the save failed."}, correlationID: "r1-newer", transcriptEntryID: "r1-human-newer"}
				var restore func()
				var failedTS *turnState
				arm := func() {
					failedTS = al.getActiveTurnState(key)
					require.NotNil(t, failedTS)
					for _, item := range batch {
						require.NoError(t, al.enqueueSteeringItem(key, inst.ID, item))
					}
					if storage == "model_window" {
						restore = wu22BlockModelWrite(t, store, sid)
					} else {
						restore = steeringReviewBlockTranscript(t, store, sid)
					}
				}
				if boundary == "natural_exit" {
					naturalExitSteeringPollTestHook = arm
					t.Cleanup(func() { naturalExitSteeringPollTestHook = nil })
				} else {
					provider.steps[0].onCall = arm
				}
				newerQueued := false
				al.SetEventSyncTap(func(event Event) {
					if event.Kind == EventKindError && !newerQueued {
						newerQueued = true
						require.NoError(t, al.enqueueSteeringItem(key, inst.ID, newer))
					}
				})
				t.Cleanup(func() { al.SetEventSyncTap(nil) })

				_, err := al.runAgentLoop(context.Background(), inst, processOptions{
					SessionKey: key, Channel: "web", ChatID: sid, UserMessage: "Original instruction.",
					DefaultResponse: defaultResponse, TranscriptSessionID: sid, TranscriptStore: store,
					// The two views share the archive file. Skip model-history
					// writes only in this case to reach the transcript writer itself.
					NoHistory: storage == "transcript",
				})
				require.Error(t, err, "the real save failure must still reach the caller")
				var pathErr *os.PathError
				require.ErrorAs(t, err, &pathErr, "fault must be a real filesystem refusal")
				filename := "ordinal_rows.log"
				if storage == "transcript" {
					filename = "transcript.jsonl"
				}
				assert.Equal(t, filepath.Join(store.BaseDir(), sid, filename), pathErr.Path)
				require.NotNil(t, restore, "fault was armed after initial input admission")
				if storage == "transcript" {
					assert.NoError(t, failedTS.contextWindowError(), "instrument: model admission did not supply the failure")
					assert.Error(t, failedTS.callAccountingError(), "instrument: the real transcript append failed")
				} else {
					assert.Error(t, failedTS.contextWindowError(), "instrument: real model-window append failed")
				}
				assert.Equal(t, 1, provider.CallCount(), "no follow-up provider call after failed preservation")
				al.lastTurnResultMu.Lock()
				status := al.lastTurnResult.status
				al.lastTurnResultMu.Unlock()
				assert.Equal(t, TurnEndStatusError, status, "restoration must not hide the failed turn")
				require.True(t, newerQueued, "the real error path accepts the later arrival before restoration")
				want := append(append([]steeringQueueItem(nil), batch...), newer)
				al.steering.mu.Lock()
				queued := append([]steeringQueueItem(nil), al.steering.queues[key]...)
				al.steering.mu.Unlock()
				assert.Equal(t, want, queued, "restore original messages, receipts and entry ids ahead of newer input")

				restore()
				naturalExitSteeringPollTestHook = nil
				al.SetEventSyncTap(nil)
				continued, err := al.Continue(context.Background(), key, "web", sid, "")
				require.NoError(t, err)
				assert.Equal(t, reply, continued, "recovered storage allows the restored follow-ups to be answered")
				assert.Equal(t, 2, provider.CallCount())
				requests := provider.Requests()
				require.Len(t, requests, 2)
				history := inst.Sessions.GetHistory(key)
				position := -1
				for _, item := range want {
					assert.Equal(t, 1, countMessagesContaining(requests[1], item.message.Content), "each unprocessed input reaches the model once")
					assert.Equal(t, 1, countMessagesContaining(history, item.message.Content), "each input is admitted once")
					next := indexOfRole(history, "user", item.message.Content)
					assert.Greater(t, next, position, "original batch order survives the failed save")
					position = next
				}
				assert.Equal(t, 0, al.pendingSteeringCountForScope(key))
				again, err := al.Continue(context.Background(), key, "web", sid, "")
				require.NoError(t, err)
				assert.Empty(t, again, "no duplicate processing after recovery")
				assert.Equal(t, 2, provider.CallCount())
				entries, err := store.ReadTranscript(sid)
				require.NoError(t, err)
				assert.Equal(t, []string{reply}, nonEmpty(assistantTranscriptContents(entries)), "the recovery answer is saved in the same conversation")
			})
		}
	}
}

// Replace, rather than chmod, so the filesystem fault also works under root.
// Preserve all original transcript bytes for the real recovery continuation.
func steeringReviewBlockTranscript(t *testing.T, store *session.UnifiedStore, sid string) func() {
	t.Helper()
	path := filepath.Join(store.BaseDir(), sid, "transcript.jsonl")
	backup := filepath.Join(t.TempDir(), "original-transcript.jsonl")
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if file != nil {
		require.NoError(t, file.Close())
	}
	require.Error(t, err, "instrument: exact transcript append path refuses writes")
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	return restore
}

func testSteeringReviewDirectMarker(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, partial, finish string
		wantTruncated                 bool
	}{
		{name: "partial", partial: "Cut-off answer.", finish: "truncated", wantTruncated: true},
		{name: "auto_continued_partial", prefix: "Earlier part. ", partial: "Last partial.", finish: "length", wantTruncated: true},
		{name: "empty_cutoff", finish: "max_tokens", wantTruncated: true},
		{name: "complete_answer_control", partial: "Complete answer.", finish: "stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const reply = "Separate follow-up answer."
			var al *AgentLoop
			steps := []truncationScriptStep{}
			if tc.prefix != "" {
				steps = append(steps, truncationScriptStep{content: tc.prefix, finishReason: "truncated"})
			}
			steps = append(steps, truncationScriptStep{content: tc.partial, finishReason: tc.finish, onCall: func() {
				require.NoError(t, al.Steer(providers.Message{Role: "user", Content: boundaryFollowUp}))
			}}, truncationScriptStep{content: reply, finishReason: "stop"})
			provider := &truncationScriptedProvider{steps: steps}
			loop, inst, store, sid := newTruncationTestHarness(t, provider, 10, 0)
			al = loop
			answer := runBoundaryTurn(t, al, inst, store, sid)
			assert.Equal(t, reply, answer)
			requests := provider.Requests()
			require.Len(t, requests, len(steps))
			whole := tc.prefix + tc.partial
			if whole != "" {
				assert.Equal(t, []string{whole}, followUpBoundaryAnswers(requests[len(requests)-1]), "kept partial appears once, without a continuation chain")
				assert.Equal(t, []string{whole, reply}, followUpBoundaryAnswers(inst.Sessions.GetHistory("steer-boundary-"+sid)))
			}
			entries, err := store.ReadTranscript(sid)
			require.NoError(t, err)
			var answers []session.TranscriptEntry
			for _, entry := range entries {
				if entry.Role == "assistant" && (entry.Content != "" || entry.Truncated) {
					answers = append(answers, entry)
				}
			}
			require.Len(t, answers, 2, "both the original reply and follow-up have their own saved outcome")
			assert.Equal(t, whole, answers[0].Content)
			assert.Equal(t, tc.wantTruncated, answers[0].Truncated, "derive the direct answer's mark from its actual finish reason")
			wantReason := ""
			if tc.wantTruncated {
				wantReason = "max_output_tokens"
			}
			assert.Equal(t, wantReason, answers[0].TruncationReason)
			assert.Equal(t, reply, answers[1].Content)
			assert.False(t, answers[1].Truncated, "the follow-up never inherits the mark")
			assert.Empty(t, answers[1].TruncationReason)
			assert.NotEqual(t, answers[0].ID, answers[1].ID)
		})
	}
}
