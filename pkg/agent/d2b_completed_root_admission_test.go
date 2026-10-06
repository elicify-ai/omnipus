package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// User expectation (D2b ruling): when the answer finishes, the chat says Done,
// not Working, and it remains available in the sidebar with its conversation.
func TestD2bRootTurn_CompletesWithoutArchivingOrHidingChat(t *testing.T) {
	h := newD2bRoot(t)
	reply, err := h.humanTurn(t, "Finish this root chat turn.")
	require.NoError(t, err, "a normal human root turn must execute successfully")
	assert.Equal(t, d2bReply, reply, "return the actual turn's answer to the user")
	observations := h.provider.calls()
	require.Len(t, observations, 1, "one human instruction must run exactly one provider round")
	h.assertAdmittedRound(t, observations[0], 1, "")

	rec := h.load(t)
	// Keep these non-fatal so the same RED receipt also tests visibility.
	assert.Equal(t, session.LifecycleCompleted, rec.State, "D2b: a finished root answer must stop claiming Working")
	assert.Equal(t, session.LifecycleDisplayDone, session.LifecycleStateToDisplay(rec.State), "D2b: completed chat displays Done")
	assert.Equal(t, 1, rec.Generation, "finishing a first turn must not create a second round")
	h.assertChatVisible(t)
	entries, readErr := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, readErr)
	require.Len(t, entries, len(h.prior)+2, "one human input and one final answer must extend the existing chat")
	assert.Equal(t, h.prior, entries[:len(h.prior)], "the original conversation must remain intact")
	assert.Equal(t, "user", entries[len(h.prior)].Role)
	assert.Equal(t, "Finish this root chat turn.", entries[len(h.prior)].Content)
	assert.Equal(t, "assistant", entries[len(h.prior)+1].Role)
	assert.Equal(t, d2bReply, entries[len(h.prior)+1].Content)
	t.Logf("D2b completion: lifecycle=%s display=%s; chat remains active and listed", rec.State, session.LifecycleStateToDisplay(rec.State))
}

// User expectation (D2b case 1): a completed chat reused by a recurring job or
// heartbeat actually runs again, as a headless system entry, in a NEW round.
// The old ADR-093 automatic-wake prohibition still protects STOPPED, not this
// explicit completed-root scheduling exception supplied by the dispatcher.
func TestD2bCompletedRoot_ScheduledAndHeartbeatReviveOneRound(t *testing.T) {
	for _, kind := range []string{"scheduled", "heartbeat"} {
		t.Run(kind, func(t *testing.T) {
			h := newD2bRoot(t)
			h.seedState(t, session.LifecycleCompleted)
			before := h.journal(t)
			prompt := "Perform the next " + kind + " round in this same chat."
			reply, err := h.scheduledTurn(kind, prompt)
			require.NoError(t, err, "D2b: a reused COMPLETED root must run the scheduled/heartbeat instruction, not refuse it as terminal")
			assert.Equal(t, d2bReply, reply)
			observations := h.provider.calls()
			require.Len(t, observations, 1, "one automatic entry must execute exactly once")
			o := observations[0]
			h.assertAdmittedRound(t, o, 2, h.id) // completed generation 1 -> new round 2
			assert.Equal(t, prompt, o.lastUser, "the admitted round must carry this job's actual instruction")
			assert.False(t, o.userInitiated, "automatic revival must not pretend a human resumed the chat")
			assert.Empty(t, o.userID, "the automatic turn must not borrow a human gateway principal")
			assert.True(t, o.autoDenyAsk, "scheduled revival retains headless approvals, not human admission options")
			assert.True(t, o.jobPresent, "real schedule identity must reach the provider seam")
			name := "D2b recurring job"
			if kind == "heartbeat" {
				name = "heartbeat:" + testHarnessWorkspaceMembershipID + ":" + testDefaultAgentID
			}
			assert.Equal(t, ScheduledJobInfo{JobID: "d2b-" + kind, JobName: name}, o.job)
			after := h.load(t)
			assert.Equal(t, 2, after.Generation, "one automatic entry must mint only one new lifecycle round")
			assert.Equal(t, h.id, after.ResumedFrom, "the resumed round belongs to the SAME chat")
			h.assertHistoryPreserved(t, before)
			h.assertChatVisible(t)
			entries, readErr := h.al.GetSessionStore().ReadTranscript(h.id)
			require.NoError(t, readErr)
			require.Len(t, entries, len(h.prior)+2, "the job must append exactly its input and one final answer")
			assert.Equal(t, "user", entries[len(h.prior)].Role)
			assert.Equal(t, prompt, entries[len(h.prior)].Content)
			assert.Equal(t, "assistant", entries[len(h.prior)+1].Role)
			assert.Equal(t, d2bReply, entries[len(h.prior)+1].Content)
			t.Logf("D2b %s: provider entered running generation=%d, headless=true; same session resumed once", kind, o.record.Generation)
		})
	}
}

// User expectation (D2b case 2): pressing Stop holds. A later recurring job or
// heartbeat is visibly refused, cannot spend model work, and cannot revive or
// overwrite the stopped round. The diagnostic contract is the existing typed
// Dispatch cancellation (ADR-091 I-6), not an invented product sentence.
func TestD2bStoppedRoot_ScheduledAndHeartbeatRefuseWithoutRevival(t *testing.T) {
	for _, kind := range []string{"scheduled", "heartbeat"} {
		t.Run(kind, func(t *testing.T) {
			h := newD2bRoot(t)
			h.seedState(t, session.LifecycleStopped)
			before := h.journal(t)
			stopped := h.load(t)
			reply, err := h.scheduledTurn(kind, "Do not override the user's Stop.")
			require.Error(t, err, "D2b: a STOPPED root must return a visible refusal to the job caller")
			assert.ErrorIs(t, err, steer.ErrDispatchCancelled, "refusal must specifically identify Stop, not an unrelated setup/provider/store failure")
			assert.Contains(t, err.Error(), steer.ErrDispatchCancelled.Error(), "the job caller must receive the stopped-generation diagnostic")
			assert.Empty(t, reply, "a refused automatic entry must not manufacture an answer")
			assert.Len(t, h.provider.calls(), 0, "Stop must prevent provider work altogether")
			assert.Equal(t, stopped, h.load(t), "automatic entry must not clear Stop, stamp an execution, or mint a round")
			assert.Equal(t, before, h.journal(t), "the stopped lifecycle history must remain byte-for-byte unchanged")
			h.assertHistoryPreserved(t, before)
			h.assertChatVisible(t)
			t.Logf("D2b %s refusal: %v; provider calls=0; stopped generation unchanged", kind, err)
		})
	}
}

// User expectation (D2b case 3 / ADR-093 D4): typing in a completed chat still
// continues that conversation exactly once; it is not a new chat or a steered
// child. Normal turn completion is asserted separately above so a PASS here
// honestly means the existing human-revival path already works on the base.
func TestD2bCompletedRoot_HumanMessageContinuesSameChatOnce(t *testing.T) {
	h := newD2bRoot(t)
	h.seedState(t, session.LifecycleCompleted)
	before := h.journal(t)
	prompt := "Continue this completed conversation."
	reply, err := h.humanTurn(t, prompt)
	require.NoError(t, err, "ADR-093: a human message must continue the completed root")
	assert.Equal(t, d2bReply, reply)
	observations := h.provider.calls()
	require.Len(t, observations, 1, "one human input must run once, without a second steered dispatch")
	o := observations[0]
	h.assertAdmittedRound(t, o, 2, h.id)
	assert.Equal(t, prompt, o.lastUser)
	assert.True(t, o.userInitiated)
	assert.Equal(t, "d2b-owner", o.userID)
	assert.False(t, o.autoDenyAsk, "a real human continuation is not converted into a scheduled job")
	assert.False(t, o.jobPresent)
	after := h.load(t)
	assert.Equal(t, 2, after.Generation, "human continuation must mint only one new round")
	assert.Equal(t, h.id, after.ResumedFrom)
	assert.Nil(t, after.SteeredBy, "human continuation must remain an ordinary root")
	h.assertHistoryPreserved(t, before)
	h.assertChatVisible(t)
	entries, readErr := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, readErr)
	require.Len(t, entries, len(h.prior)+2, "human continuation must append one input and one answer")
	assert.Equal(t, prompt, entries[len(h.prior)].Content)
	assert.Equal(t, "assistant", entries[len(h.prior)+1].Role)
	assert.Equal(t, d2bReply, entries[len(h.prior)+1].Content)
	t.Logf("D2b human continuation: provider entered running generation=%d; same chat and immutable old history preserved", o.record.Generation)
}

func (h *d2bRoot) assertAdmittedRound(t *testing.T, o d2bProviderObservation, generation int, resumedFrom string) {
	t.Helper()
	require.NoError(t, o.readErr, "provider must observe a real saved lifecycle record")
	require.NotNil(t, o.record)
	assert.True(t, o.registered, "work must run through real turn registration")
	assert.Equal(t, h.id, o.record.SessionID)
	assert.Equal(t, generation, o.record.Generation, "new round must exist BEFORE provider work")
	assert.Equal(t, resumedFrom, o.record.ResumedFrom)
	assert.Equal(t, session.LifecycleRunning, o.record.State, "provider must run on the admitted live round, not the completed/stopped one")
	assert.Nil(t, o.record.SteeredBy, "scheduled/human admission must not manufacture a worker edge")
	require.NotNil(t, o.record.ExecutionID, "real admission must stamp an execution identity")
	assert.NotEmpty(t, o.record.ExecutionID.RunID)
	assert.Equal(t, h.al.bootEpochFor(), o.record.ExecutionID.BootSeq, "use the real minted boot epoch")
}

func (h *d2bRoot) assertChatVisible(t *testing.T) {
	t.Helper()
	// Reopening rules out an in-memory false green after a status write.
	fresh, err := session.NewUnifiedStore(h.al.GetSessionStore().BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, fresh.Close()) })
	meta, err := fresh.GetMeta(h.id)
	require.NoError(t, err, "D2b: completed/stopped chat must not be deleted")
	assert.Equal(t, session.StatusActive, meta.Status, "D2b: completed lifecycle is NOT an archived or hidden chat")
	assert.Equal(t, session.SessionTypeChat, meta.Type)
	assert.Equal(t, "d2b-owner", meta.Owner, "completion/revival must retain account ownership")
	assert.Empty(t, meta.ParentSessionID, "same chat must remain a sidebar root")
	page, listErrs := h.al.ListAllSessions(0, 0, "", false)
	assert.Empty(t, listErrs, "sidebar root-list boundary must succeed")
	ids := make([]string, 0, len(page.Sessions))
	for _, row := range page.Sessions {
		ids = append(ids, row.ID)
	}
	assert.Equal(t, []string{h.id}, ids, "D2b: exactly the original chat must remain in the visible root list")
}
