// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: the explicit 2026-10-06 PLAIN STOP REGRESSION dispatch: ordinary web
// Stop must stop the chat AND its helpers via ONE StopSession, and a helper
// hand-back must not start another parent turn afterwards. Expectations were
// recorded before assertions in the accompanying test-plan.md receipt.
//
// The older one-stop memo, CancelFrame scope description and D9 scope tests
// preserve session-only Stop. This pack intentionally exposes their conflict
// with the dispatch's later plain-web-Stop requirement; it does not rewrite
// those tests/contracts or adopt their old oracle to manufacture a green.
// User-visible expectation: Stop leaves no helper doing work in this chat,
// does not stop a different chat, and does not resume this one by itself.
//
// Real boundary: first-human WS message -> real bus/admission -> registered
// delegate -> real helper/grandchild lifecycle and provider -> actual browser
// scope-OMITTED CancelFrame -> real StopSession/owner settlement. Only the
// external provider is scripted. GREEN and mutation proof are deferred to CHECK.
func TestPlainWebStop_CancelsRealHelperTreeAndLeavesOtherChatRunning(t *testing.T) {
	f := newPlainStopUATFixture(t, true)
	require.Len(t, f.helperIDs, 3, "SETUP: direct helper, sibling and genuine delegated grandchild")
	_, unrelated := f.firstHuman(t, plainStopUnrelatedPrompt)
	require.Eventually(t, func() bool { return f.provider.contextFor(unrelated) != nil }, busDeliveryTimeout, 10*time.Millisecond,
		"SETUP: isolation control must be doing real provider work")
	unrelatedBefore, err := f.lifecycle.Load(unrelated)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, unrelatedBefore.State)
	require.NoError(t, f.provider.contextFor(unrelated).Err())

	f.plainWebStop(t)
	stopped := f.awaitSettled(t, append([]string{f.rootID}, f.helperIDs...), true)
	unrelatedAfter, err := f.lifecycle.Load(unrelated)
	require.NoError(t, err)
	assert.Equal(t, unrelatedBefore, unrelatedAfter, "Stop of this chat must not select or change a different chat")
	assert.NoError(t, f.provider.contextFor(unrelated).Err(), "the unrelated chat's actual provider must still be running")
	if !stopped {
		return // awaitSettled already reported exact running helper states as RED.
	}
	for _, id := range append([]string{f.rootID}, f.helperIDs...) {
		rec, loadErr := f.lifecycle.Load(id)
		require.NoError(t, loadErr)
		assert.Equal(t, session.LifecycleStopped, rec.State, "ordinary Stop must land every selected owner stopped: %s", id)
		assert.Equal(t, context.Canceled, f.provider.contextFor(id).Err(), "actual helper/parent work must have shut down: %s", id)
		assert.False(t, rec.Terminal(), "stopped work is resumable, never a terminal completion/failure")
		assert.Equal(t, f.before[id].Generation, rec.Generation, "Stop cannot mint a replacement generation")
		assert.Equal(t, f.before[id].ExecutionID, rec.ExecutionID, "original owner, not a replacement execution, must land Stop")
		assert.Nil(t, rec.Stop, "owner landing spends the current stop fence")
		require.NotNil(t, rec.StopNote, "stopped owner keeps its durable stop reason")
		cause := session.StopCauseCascade
		if id == f.rootID {
			cause = session.StopCauseStop
		}
		assert.Equal(t, cause, rec.StopNote.Cause, "named chat is stopped directly; its helper tree is cascaded")
		assert.Nil(t, rec.FinalDelivery, "Stop cannot manufacture a terminal helper hand-back")
	}
}

// A genuine unstopped hand-back is driven FIRST as a control. The stopped
// case then releases the same provider boundary after the parent owner lands
// stopped. The explicit-human continuation is a FIFO barrier on the real
// parent worker: all helper publication/wakes precede it. Assertions count
// actual provider requests and persisted answers, not a short quiet sleep.
func TestPlainWebStop_LateHelperHandbackDoesNotRestartParent(t *testing.T) {
	t.Run("instrument_control_unstopped_helper_really_wakes_parent", func(t *testing.T) {
		f := newPlainStopUATFixture(t, false)
		require.Len(t, f.helperIDs, 1)
		require.Len(t, f.provider.requestsFor(f.rootID), 2, "SETUP: delegate request + original parent wait")
		close(f.provider.rootFinish)
		require.Eventually(t, func() bool {
			return f.al.GetActiveTurnBySession("agent:mia:session:"+f.rootID) == nil
		}, busDeliveryTimeout, 10*time.Millisecond, "CONTROL: original parent turn must exit before the helper hands back")
		close(f.provider.leafFinish)
		require.True(t, f.awaitSettled(t, f.helperIDs, false), "CONTROL: real helper must retire its owner and finish its upward publication")
		require.Eventually(t, func() bool {
			return plainStopAnswerCount(t, f, plainStopHandbackReply) == 1
		}, busDeliveryTimeout, 10*time.Millisecond, "CONTROL: the genuine helper hand-back must reach a new parent turn")
		require.Len(t, f.provider.requestsFor(f.rootID), 3, "CONTROL: one and only one additional parent provider request")
		child, err := f.lifecycle.Load(f.helperIDs[0])
		require.NoError(t, err)
		require.Equal(t, session.LifecycleCompleted, child.State)
		require.NotNil(t, child.FinalDelivery)
		require.True(t, plainStopInboxAcked(t, f, child.FinalDelivery.MessageID),
			"CONTROL: the real wake consumer must acknowledge the genuine helper final")
		t.Logf("CONTROL verified: unstopped helper=%s real parent requests=3 exact hand-back answer count=1", child.SessionID)
	})
	t.Run("stopped_parent_requires_new_human_input_not_helper_handback", func(t *testing.T) {
		f := newPlainStopUATFixture(t, false)
		require.Len(t, f.provider.requestsFor(f.rootID), 2, "SETUP: delegate request + original parent wait")
		f.plainWebStop(t)
		require.True(t, f.awaitSettled(t, []string{f.rootID}, true), "parent's original owner must land Stop before the provider's late helper response")
		close(f.provider.leafFinish)
		// Natural completion is accepted only as a diagnostic on the unfixed
		// base. Scope-fix implementations cancel the helper instead. In both
		// cases this wait joins the actual durable publication, not a mock.
		require.True(t, f.awaitSettled(t, f.helperIDs, false), "helper tail must finish before the FIFO human barrier")
		require.NoError(t, f.conn.WriteJSON(generated.MessageFrame{Type: "message", SessionId: strPtr(f.rootID),
			Content: plainStopContinuePrompt, AgentId: strPtr("mia"), ClientMessageId: strPtr("plain-stop-explicit-continue"),
			Metadata: map[string]any{"workspace_id": testHarnessWorkspaceMembershipID}}))
		require.Eventually(t, func() bool {
			return plainStopAnswerCount(t, f, plainStopContinueReply) == 1
		}, busDeliveryTimeout, 10*time.Millisecond, "Stop is resumable: a NEW HUMAN message must continue this chat")
		requests := f.provider.requestsFor(f.rootID)
		assert.Len(t, requests, 3,
			"PLAIN STOP REGRESSION: no helper hand-back may start another parent turn after Stop; want exactly delegate + original wait + explicit human Continue, actual requests=%+v", requests)
		assert.Equal(t, 0, plainStopAnswerCount(t, f, plainStopHandbackReply),
			"PLAIN STOP REGRESSION: user stopped this chat; a late helper result must not create the automatic parent answer %q", plainStopHandbackReply)
		t.Logf("POST-STOP actual parent provider requests=%d, automatic hand-back answers=%d", len(requests), plainStopAnswerCount(t, f, plainStopHandbackReply))
	})
}

func plainStopAnswerCount(t *testing.T, f *plainStopUATFixture, answer string) int {
	t.Helper()
	entries, err := f.al.GetSessionStore().ReadTranscript(f.rootID)
	require.NoError(t, err, "read the actual durable parent transcript")
	count := 0
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == answer {
			count++
		}
	}
	return count
}

func plainStopInboxAcked(t *testing.T, f *plainStopUATFixture, messageID string) bool {
	t.Helper()
	entries, err := f.inbox.Entries(f.rootID)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Kind == session.InboxEntryAck {
			for _, acked := range entry.AckedIDs {
				if acked == messageID {
					return true
				}
			}
		}
	}
	return false
}
