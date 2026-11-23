package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH-R5-1 / TEXT.md section 4: the human row was saved before the refusal's
// append fails. Inspect the actual caller, event bus, strict archive and log.
func TestOpenCodeR5_HelperRefusalSaveFailureHasSeparateNonTerminalWarning(t *testing.T) {
	f := newOpencodeR5FinishingFixture(t)
	sid, parent := f.record.SessionID, f.record.SteeredBy.SteeringSessionID
	store := f.al.ResolveSessionStore(sid)
	before := qaReceiptReopenTranscript(t, store, sid)
	parentBefore := qaReceiptReopenTranscript(t, f.al.ResolveSessionStore(parent), parent)
	inboxBefore, err := f.al.GetMessageInboxStore().Entries(parent)
	require.NoError(t, err)
	logPath := opencodeR5AgentLog(t)
	restore, cause := opencodeR5BlockRefusalArchive(t, f.al, sid)
	require.True(t, f.al.deliverHumanHelperInput(f.message))
	events := f.events.take()
	restore()
	assert.Equal(t, before, qaReceiptReopenTranscript(t, store, sid), "SFH-R5-1: retain the saved answer and human input; do not claim the refused append succeeded")
	opencodeR5AssertAgentCause(t, logPath, cause)
	var originals, warnings int
	for _, event := range events {
		assert.NotEqual(t, parent, event.Meta.SessionKey, "SFH-R5-1: no refusal/save warning to parent")
		if event.Kind != EventKindError {
			continue
		}
		p, ok := event.Payload.(ErrorPayload)
		require.True(t, ok)
		assert.Equal(t, sid, p.SessionID)
		if p.Code == opencodeR4Code {
			originals++
			assert.Equal(t, opencodeR4Chat("OpenCode"), p.Message, "SFH-R5-1: preserve the classified original refusal")
			continue
		}
		warnings++
		// Approved non-catalogue literal; task-chat's variant would falsely
		// claim the already-saved human message was not saved.
		const want = "I couldn't save the refusal above, so it may be missing after a reload. Copy it before reloading."
		text := p.Message
		if p.ChatNotice != nil {
			text = p.ChatNotice.Content
			assert.Equal(t, sid, p.ChatNotice.SessionId)
			assert.Equal(t, "system", p.ChatNotice.Role)
			assert.Equal(t, string(generated.WsFrameTypeReplayMessage), p.ChatNotice.Type)
			assert.NotEmpty(t, p.ChatNotice.Id)
		}
		assert.Equal(t, want, text, "SFH-R5-1: exact approved helper-chat save warning")
		opencodeR5AssertNonTerminalPayload(t, p)
	}
	assert.Equal(t, 1, originals, "SFH-R5-1: original refusal exactly once")
	assert.Equal(t, 1, warnings, "SFH-R5-1: missing separate unsaved-refusal warning")
	inboxAfter, err := f.al.GetMessageInboxStore().Entries(parent)
	require.NoError(t, err)
	assert.Equal(t, inboxBefore, inboxAfter)
	assert.Equal(t, parentBefore, qaReceiptReopenTranscript(t, f.al.ResolveSessionStore(parent), parent))
	f.assertUnchanged(t)
}

// SFH-R5-2 producer regression. This input has NO original client-message id;
// it must not invent one or fall back to an uncorrelated turn-ending error.
// An existing ChatNotice or the explicit refused-input marker identifies a
// non-terminal event; the real hub/reducer are exercised independently.
func TestOpenCodeR5_FinishingWindowRefusalUsesNonTerminalOwnSessionCarrier(t *testing.T) {
	f := newOpencodeR5FinishingFixture(t)
	sid, parent := f.record.SessionID, f.record.SteeredBy.SteeringSessionID
	inboxBefore, err := f.al.GetMessageInboxStore().Entries(parent)
	require.NoError(t, err)
	require.True(t, f.al.deliverHumanHelperInput(f.message))
	rows := qaReceiptReopenTranscript(t, f.al.ResolveSessionStore(sid), sid)
	var answers, refusals, human []session.TranscriptEntry
	for _, row := range rows {
		if row.ID == f.message.TranscriptEntryID {
			human = append(human, row)
		}
		if row.ID == "r5-original-answer" {
			answers = append(answers, row)
		}
		if row.ErrorCode == opencodeR4Code {
			refusals = append(refusals, row)
		}
	}
	require.Len(t, human, 1, "C2: retain the actual refused human row")
	assert.Equal(t, f.message.Content, human[0].Content, "refusing input preserves the saved human text; C2 displayed-row reconciliation is checked at the gateway/consumer")
	require.Len(t, answers, 1)
	assert.Equal(t, opencodeR5HealthyAnswer, answers[0].Content)
	require.Len(t, refusals, 1, "SFH-R5-2: fresh history contains both healthy answer and distinct refusal")
	assert.Equal(t, opencodeR4Chat("OpenCode"), refusals[0].Content)
	var seen []ErrorPayload
	for _, event := range f.events.take() {
		assert.NotEqual(t, parent, event.Meta.SessionKey)
		if event.Kind == EventKindError {
			p, ok := event.Payload.(ErrorPayload)
			require.True(t, ok)
			seen = append(seen, p)
		}
	}
	require.Len(t, seen, 1)
	assert.Equal(t, opencodeR4Code, seen[0].Code)
	assert.Equal(t, opencodeR4Chat("OpenCode"), seen[0].Message)
	assert.Equal(t, sid, seen[0].SessionID)
	opencodeR5AssertNonTerminalPayload(t, seen[0])
	opencodeR5AssertRefusedInputEntry(t, seen[0], f.message.TranscriptEntryID)
	if seen[0].ChatNotice != nil {
		n := seen[0].ChatNotice
		assert.Equal(t, string(generated.WsFrameTypeReplayMessage), n.Type)
		assert.Equal(t, "system", n.Role)
		assert.Equal(t, sid, n.SessionId)
		assert.Equal(t, refusals[0].Content, n.Content)
		require.NotNil(t, n.Id)
		assert.Equal(t, refusals[0].ID, *n.Id, "SFH-R5-2: live notice and saved refusal share one durable identity")
		assert.Empty(t, n.ClientMessageId, "never manufacture an original client id for this input")
	}
	inboxAfter, err := f.al.GetMessageInboxStore().Entries(parent)
	require.NoError(t, err)
	assert.Equal(t, inboxBefore, inboxAfter)
	f.assertUnchanged(t)
}

// TEST-ANALYSIS gap 2 / architect N2: whether never-started input is queued
// or separately refused is deferred; saying an earlier conversation is lost
// is not. Do not invent a new never-started sentence or admission policy.
func TestOpenCodeR5_NeverStartedHelperAndNativeWorkerDoNotGetLostConversationRefusal(t *testing.T) {
	al, _ := opencodeR4Loop(t, "opencode")
	_, processes := opencodeR4ProcessEdge(t, al)
	child := launchDelegateTo(t, al, delegateExtCLIAgentID, "never-started helper")
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.False(t, rec.ExternalRunStarted, "instrument: real Launch has not dispatched a CLI")
	require.Nil(t, al.externalRunSessionIfPresent(child.SessionID))
	positive := *rec
	positive.ExternalRunStarted = true
	llm, classified := al.humanHelperRefusal(errNoLiveExternalConversation, &positive)
	require.True(t, classified, "instrument: same classifier recognizes the specified prior-start positive")
	require.Equal(t, opencodeR4Code, string(llm.Code))
	_, classified = al.humanHelperRefusal(errNoLiveExternalConversation, rec)
	assert.False(t, classified, "gap 2: a never-started helper has no previous CLI conversation")
	native := positive
	native.Is3P = false
	_, classified = al.humanHelperRefusal(errExternalResumeUnavailable, &native)
	assert.False(t, classified, "native workers must never acquire external lost-conversation classification")
	observed := opencodeR4Observe(t, al, child.SessionID)
	msg := opencodeR4SaveHuman(t, al, child.SessionID, "input before first CLI run")
	require.True(t, al.deliverHumanHelperInput(msg), "exercise the real caller as well as its classifier")
	for _, event := range observed.take() {
		if p, ok := event.Payload.(ErrorPayload); ok {
			assert.NotEqual(t, opencodeR4Code, p.Code)
			assert.NotEqual(t, opencodeR4Chat("OpenCode"), p.Message)
		}
	}
	select {
	case reply := <-al.bus.OutboundChan():
		assert.NotEqual(t, opencodeR4Chat("OpenCode"), reply.Content)
		assert.NotContains(t, reply.Content, "previous conversation can't be resumed")
	default: // Queuing without a refusal is an explicitly deferred allowed choice.
	}
	for _, row := range qaReceiptReopenTranscript(t, al.ResolveSessionStore(child.SessionID), child.SessionID) {
		assert.NotEqual(t, opencodeR4Code, row.ErrorCode)
		assert.NotEqual(t, opencodeR4Chat("OpenCode"), row.Content)
	}
	assert.Zero(t, processes.Load(), "no replacement process was requested by the non-resume classifier")
}
