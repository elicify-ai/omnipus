// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_refusal.go — a refusal shown and saved in one session's own chat
// when there is no turn to carry it (ARCHITECT-RULING-R4 D2, founder Q13 B):
// a person's message into an external-CLI helper that has no CLI run in
// flight, and a person's message into an external worker's Board task chat.
// The turn-bound equivalents are emitExternalErrorFrame and
// appendClassifiedError; these paths have no turnState.

package agent

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// errNoLiveExternalConversation is the identity of BDD-05.6's refusal "this
// session has no live external CLI conversation to deliver to", so a
// person's message refused for it is classified by identity, never by text.
var errNoLiveExternalConversation = errors.New("external-cli steer: no live external CLI conversation")

// noLiveConversationError is that refusal: its authored sentence (shown to
// the calling agent as written, pkg/tools.refusalTexter) with the identity
// above.
type noLiveConversationError struct{ authoredRefusalError }

func (e *noLiveConversationError) Unwrap() error { return errNoLiveExternalConversation }

// AppendSessionRefusal saves one refusal row in sessionID's transcript: a
// system entry with status "error", so history replay shows it at its place
// after a reload (pkg/gateway/replay.go::buildReplayErrorFrame), and the live
// frame and the saved row say the same. code may be empty for an Omnipus
// sentence that has no catalogue code; a coded slotted sentence is kept only
// when it is that code's sentence with every slot filled, exactly as the
// turn-bound writer decides (persistErrorTranscript). It is the one writer
// for refusals that have no turn: the helper-chat refusal below and the
// gateway's task-chat refusal both save through it.
func (al *AgentLoop) AppendSessionRefusal(sessionID, agentID string, code LLMErrorCode, message string) (session.TranscriptEntry, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return session.TranscriptEntry{}, fmt.Errorf("save refusal: empty session id")
	}
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return session.TranscriptEntry{}, fmt.Errorf("save refusal in session %s: no session store owns it", sessionID)
	}
	content, providerMessage := message, false
	if code != "" {
		content, providerMessage = assembledCatalogueSentence(code, message)
	}
	entry := session.TranscriptEntry{
		ID:              uuid.New().String(),
		Type:            session.EntryTypeSystem,
		AgentID:         strings.TrimSpace(agentID),
		Content:         content,
		Timestamp:       time.Now().UTC(),
		ErrorCode:       string(code),
		ErrorRetryable:  code != "" && isRetryable(code),
		ProviderMessage: providerMessage,
		Status:          "error",
	}
	if err := store.AppendTranscriptStrict(sessionID, entry); err != nil {
		return session.TranscriptEntry{}, fmt.Errorf("save refusal in session %s: %w", sessionID, err)
	}
	return entry, nil
}

// humanHelperRefusal reports whether err refused a person's message to the
// external-CLI helper rec because its CLI conversation cannot take it — gone
// (errExternalResumeUnavailable) or, after its CLI run started, not running
// now (errNoLiveExternalConversation) — and if so returns that refusal as
// delegation_not_resumable (W14 form 1) with the helper's CLI fact. In the
// finishing window the run has ended and its driver is released at that
// turn's exit (N6), so "its previous conversation can't be resumed" is true
// for this message too (ruling D2). A helper whose CLI never started has no
// previous conversation, so that sentence would be untrue: it is not
// classified here.
func (al *AgentLoop) humanHelperRefusal(err error, rec *session.LifecycleRecord) (LLMError, bool) {
	if rec == nil || !rec.Is3P {
		return LLMError{}, false
	}
	gone := errors.Is(err, errExternalResumeUnavailable)
	idleAfterStart := errors.Is(err, errNoLiveExternalConversation) && rec.ExternalRunStarted
	if !gone && !idleAfterStart {
		return LLMError{}, false
	}
	return TranslateTurnError(newResumeUnavailableRefusal(al.executorCLIOf(rec.AgentID), err)), true
}

// helperRefusalNotSavedText is the notice shown right after a helper-chat
// refusal that could not be saved (the person's own message was saved), so
// the person knows it may be missing after a reload (SFH-R5-1). It is a
// separate message-scoped notice, never a turn's end. Approved wording:
// prompt-r4 TEXT.md §4, helper chat.
const helperRefusalNotSavedText = "I couldn't save the refusal above, so it may be missing after a reload. Copy it before reloading."

// refuseHumanHelperInput shows a classified refusal of msg in the helper's
// own chat — one typed error event addressed to the helper session, never to
// its parent, scoped to the person's message so it ends no turn (SFH-R5-2) —
// and saves it as the row after the person's saved message. It changes no
// lifecycle state, queues nothing and starts nothing. When the save fails, a
// second message-scoped notice tells the person the refusal was not saved
// (SFH-R5-1), and the save error is returned for the caller's log.
func (al *AgentLoop) refuseHumanHelperInput(msg bus.InboundMessage, agentID string, llm LLMError) error {
	meta := EventMeta{SessionKey: msg.SessionID, TracePath: "steer.human_input"}
	al.emitEvent(EventKindError, meta, ErrorPayload{
		Stage:               "human_input",
		ChatID:              msg.ChatID,
		SessionID:           msg.SessionID,
		Code:                string(llm.Code),
		Message:             llm.Message,
		RefusedInput:        true,
		RefusedInputEntryID: msg.TranscriptEntryID,
	})
	_, err := al.AppendSessionRefusal(msg.SessionID, agentID, llm.Code, llm.Message)
	if err != nil {
		al.emitEvent(EventKindError, meta, ErrorPayload{
			Stage:               "human_input_not_saved",
			ChatID:              msg.ChatID,
			SessionID:           msg.SessionID,
			Message:             helperRefusalNotSavedText,
			RefusedInput:        true,
			RefusedInputEntryID: msg.TranscriptEntryID,
		})
	}
	return err
}
