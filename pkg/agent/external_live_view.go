// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// external_live_view.go — the live view of a steered external-CLI helper turn
// (#1895).
//
// A native helper's text reaches its open chat through the webchat streamer:
// token frames while the text arrives and one done frame when the turn ends
// (turn_stream.go::finalizeStreamer). The external-CLI body wrote the same text
// to the transcript only, so a helper chat showed the answer and the failure
// only after a reload. These helpers give the external body the native shape:
// each CLI text segment is sent live under the id of the row it is saved as,
// and the body ends the turn with exactly one done (failed when the run
// failed), after one error frame for a failure nothing has announced yet.
package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// recordExternalOutput saves one external-CLI text segment as its own
// assistant row. For a steered helper turn (externalLiveOutput) the segment is
// first sent live under a freshly minted round id, and the row is saved under
// that same id, so a reload never shows the text twice. The round is then
// closed, so later rows of the run (a diff, a permission record, an error
// marker) never reuse the text's id.
func (ts *turnState) recordExternalOutput(al *AgentLoop, text, model string) {
	if !ts.externalLiveOutput {
		ts.appendIntermediateAssistantTranscript(text, model)
		return
	}
	ts.nextRoundMessageID()
	if streamer, ok := ts.acquireExternalStreamer(al); ok {
		ts.markLastStreamerProducedModel(model)
		if err := streamer.Update(ts.liveViewContext(), text); err != nil {
			// The row below is still saved; the client catches up by id.
			logger.DebugCF("agent", "External CLI live text update error (client may have disconnected)",
				map[string]any{"session_id": ts.transcriptSessionID, "error": err.Error()})
		}
	}
	ts.appendIntermediateAssistantTranscript(text, model)
	ts.closeExternalOutputRound()
}

// closeExternalOutputRound ends the current external text round: the next
// intermediate row mints its own id instead of reusing the text's.
func (ts *turnState) closeExternalOutputRound() {
	ts.mu.Lock()
	ts.currentMessageID = ""
	ts.mu.Unlock()
}

// acquireExternalStreamer obtains the turn's webchat streamer for the current
// round, stamped exactly like the native streaming call site does
// (loop_run_turn_attempt.go), and records it as the turn's last streamer.
func (ts *turnState) acquireExternalStreamer(al *AgentLoop) (bus.Streamer, bool) {
	if al == nil || al.bus == nil || ts.streamerChannel() != "webchat" || ts.transcriptSessionID == "" {
		return nil, false
	}
	streamer, ok := al.bus.GetStreamer(ts.liveViewContext(), ts.streamerChannel(), ts.opts.ChatID, ts.transcriptSessionID)
	if !ok {
		return nil, false
	}
	ts.stampStreamerProducerAgentID(streamer)
	ts.stampStreamerTurnID(streamer)
	ts.stampStreamerParentSpawnCallID(streamer)
	ts.stampStreamerGoalID(streamer)
	ts.stampStreamerMessageID(streamer)
	ts.setLastStreamer(streamer)
	return streamer, true
}

// liveViewContext is the turn's own context, or Background when none is set.
// A canceled run context must not stop the done frame of the turn it ended.
func (ts *turnState) liveViewContext() context.Context {
	if ts.ctx != nil {
		return ts.ctx
	}
	return context.Background()
}

// finishExternalLiveView ends a steered external-CLI turn's live view: one
// error frame for a failure not yet announced, then exactly one done (marked
// failed when the run failed), with every text row already saved so the
// streamer writes nothing. A Stop or a steer interrupt (context.Canceled) is
// not a failure and gets no error frame, as on the native path. It returns
// the streamer's own finalize error, if any, but never the turn's latched
// transcript-save failure: that one is announced here and collected by the
// drain into turnResult.saveFailure, so it warns without failing the run
// (ARCHITECT-RULING-R2 R1-a, R1-d).
func (al *AgentLoop) finishExternalLiveView(ctx context.Context, ts *turnState, runErr error) error {
	failed := runErr != nil && !errors.Is(runErr, context.Canceled)
	if failed && !ts.externalErrorAnnounced.Load() {
		// A5: like every other external error site, the failure is saved in
		// the helper's chat as well as shown live, so a reload keeps it.
		llm := TranslateTurnError(runErr)
		al.emitExternalErrorFrame(ts, "external_cli", llm, "")
		ts.appendClassifiedError(EventKindError.String(), "external_cli", llm)
	}
	if failed {
		ts.markTurnFailed()
	}
	al.announceExternalSaveFailure(ts)
	ts.mu.RLock()
	active := ts.lastStreamer != nil
	ts.mu.RUnlock()
	if !active {
		// No text was streamed: the done carries no message id, since no
		// assistant row of this turn would match it.
		if _, ok := ts.acquireExternalStreamer(al); !ok {
			return nil
		}
	}
	ts.markLastStreamerTranscriptPersisted()
	err := ts.finalizeStreamer(ctx)
	// finalizeStreamer can latch a save failure of its own (the pending call
	// accounting flush), so read the latch after it and announce once more;
	// the announcement is once-only per turn.
	if saveErr := ts.callAccountingError(); saveErr != nil {
		al.announceExternalSaveFailure(ts)
		err = withoutSaveFailure(err, saveErr)
	}
	return err
}

// withoutSaveFailure returns err with the turn's latched transcript-save
// failure removed: nil when that was all err held, otherwise the remaining
// errors. It never drops anything else. Only the latched value itself is
// removed (identity, not errors.Is, which would match any wrapper that merely
// contains it), and only errors.Join nodes are taken apart and rebuilt; every
// other wrapper is kept whole, even one that also wraps the save failure, so
// an unrelated cause inside it survives. finalizeStreamer returns the latched
// value bare or joined with the flush and streamer-finalize errors, which is
// the shape removed here.
func withoutSaveFailure(err, saveErr error) error {
	if err == nil || saveErr == nil {
		return err
	}
	if sameErrorValue(err, saveErr) {
		return nil
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || reflect.TypeOf(err) != joinErrorType {
		return err
	}
	var rest []error
	for _, e := range joined.Unwrap() {
		if e = withoutSaveFailure(e, saveErr); e != nil {
			rest = append(rest, e)
		}
	}
	return errors.Join(rest...)
}

// joinErrorType is the dynamic type errors.Join returns for a non-empty list.
var joinErrorType = reflect.TypeOf(errors.Join(errors.New("join probe")))

// sameErrorValue reports whether a and b are the same error value, guarding
// against a panic on an uncomparable dynamic type.
func sameErrorValue(a, b error) bool {
	t := reflect.TypeOf(a)
	return t == reflect.TypeOf(b) && t.Comparable() && a == b //nolint:errorlint // identity is the point: errors.Is would also match a wrapper holding other causes
}

// emitExternalErrorFrame emits the typed error frame for an external-CLI exit
// and records that this turn's failure was announced. Like the native
// emitErrorEvent it addresses the run's OWN session (u9ToolExecSessionIDs) and
// honours the typed-error audience; for a steered helper that is the helper's
// chat, where its viewers are subscribed, not the cascade root's (#1895,
// ADR-091 D1). ChatID falls back to the turn's chat id when the options carry
// none, as the external exits always did. detail is the frame's technical
// details: only the runner-error path passes its safe #1857 text
// (SanitizedRunnerError.technicalDetail); every other caller passes "", so a
// translated error's own Detail never reaches the wire from here.
func (al *AgentLoop) emitExternalErrorFrame(ts *turnState, stage string, llm LLMError, detail string) {
	ts.externalErrorAnnounced.Store(true)
	sessionID := u9ToolExecSessionIDs(ts)
	if al.audienceFor(context.Background(), steer.BoundaryTypedErrorFrame, sessionID) == steer.AudienceNone {
		return
	}
	chatID := ts.opts.ChatID
	if chatID == "" {
		chatID = ts.chatID
	}
	al.emitEvent(EventKindError, ts.eventMeta("runTurn", "turn.error"), ErrorPayload{
		Stage:     stage,
		ChatID:    chatID,
		SessionID: sessionID,
		Code:      string(llm.Code),
		Message:   llm.Message,
		Detail:    detail,
	})
}

// deferExternalLiveDoneForResume holds back the done of a steered external-CLI
// turn that ends, by a steer interrupt or naturally, with a person's or
// parent's instruction still queued: the post-turn drain resumes the
// conversation with it at once. A done here would tell every viewer the helper
// is idle while the resumed run works, until its first output (#1895). The
// natural end covers an instruction queued without interrupt while the tool
// was still starting (#1899 B). It reports whether the done was held back.
// Every steered body call is followed by drainSteeredTurn, whose
// settleDeferredExternalLiveDone sends the held done if no resumed run starts.
//
// Only the live-stream ownership is released now (as the abandoned-turn path
// of finalizeStreamer does), so the resumed run's streamer can own the live
// slot; the turn's text rows are already saved. A Stop, a failure, or an end
// with nothing queued keeps the immediate done.
func (al *AgentLoop) deferExternalLiveDoneForResume(ts *turnState, sessionID string, runErr error) bool {
	if (runErr != nil && !errors.Is(runErr, context.Canceled)) || ts.stopRequested.Load() ||
		executionStopPending(ts.opts.executionDisposition) ||
		al.steering == nil || al.steering.scopeEmpty(sessionID) {
		return false
	}
	ts.mu.RLock()
	s := ts.lastStreamer
	ts.mu.RUnlock()
	if releaser, ok := s.(streamOwnershipReleaser); ok {
		releaser.ReleaseStreamOwnership()
	}
	ts.externalLiveDoneDeferred.Store(true)
	return true
}

// settleDeferredExternalLiveDone sends the done held back by
// deferExternalLiveDoneForResume when the drain ends with that turn as its
// last run, i.e. no resumed run followed it (the session was stopped, the
// queue was taken elsewhere, or the resume was refused). A resumed run that did
// start ends its own live view, so a superseded turn's held done is never sent.
// Only an interrupted or successful turn is held back, so the done is sent as
// not failed and with no error frame. A failure to finalize is returned for
// the caller to fold into the turn's outcome, as finishExternalLiveView's own
// caller does.
func (al *AgentLoop) settleDeferredExternalLiveDone(ctx context.Context, last *turnState) error {
	if last == nil || !last.externalLiveDoneDeferred.Swap(false) {
		return nil
	}
	// A4: the viewers' done handler closes whatever turn is active in the
	// chat. When another execution now owns the session (a revival, or any
	// other live turn on it), the old turn's done would end that turn's
	// working state, so it is not sent; the newer run's own live view and
	// done close the chat. Its stream ownership was already released.
	//
	// SFH-R2-1: when ownership cannot be determined (the record is
	// unreadable) and no other execution's turn is registered, the done IS
	// published: holding it would leave the chat working until a reload,
	// while a replacement that has not registered yet re-opens the working
	// state with its first frame. The lookup error is logged, not returned:
	// it is not a failure of the helper's work.
	o := al.sessionOwnership(last)
	if o.registeredOther {
		if o.loadErr != nil {
			al.logOwnershipUnknown(last, "suppressed_registered_owner", o.loadErr)
		}
		return nil
	}
	if o.loadErr == nil && o.recordOther {
		return nil
	}
	if o.loadErr != nil {
		al.logOwnershipUnknown(last, "done_published", o.loadErr)
	}
	return al.finishExternalLiveView(ctx, last, nil)
}

// executionOwnership is what is known about who owns a turn's session.
// registeredOther is positive evidence: a live turn of a different execution
// is registered for the same chat session. recordOther means the lifecycle
// record, read successfully, names a different execution. loadErr is set when
// the record could not be read, so recordOther is unknown.
type executionOwnership struct {
	registeredOther bool
	recordOther     bool
	loadErr         error
}

// sessionOwnership reads both sources of ownership for ts's session. A turn of
// ts's own execution (the original turn or a continuation of it) never counts
// as another owner.
func (al *AgentLoop) sessionOwnership(ts *turnState) executionOwnership {
	var o executionOwnership
	claim := al.tsExecutionClaim(ts, "")
	if sessionID := ts.transcriptSessionID; sessionID != "" {
		al.activeTurnStates.Range(func(_, value any) bool {
			other, ok := value.(*turnState)
			if !ok || other == ts || other.transcriptSessionID != sessionID || !other.IsAlive() {
				return true
			}
			if al.tsExecutionClaim(other, "") == claim {
				return true
			}
			o.registeredOther = true
			return false
		})
	}
	store := al.GetSessionLifecycleStore()
	if store == nil || claim.RunID == "" || claim.SessionID == "" {
		return o
	}
	rec, err := store.Load(claim.SessionID)
	switch {
	case err != nil:
		o.loadErr = fmt.Errorf("load lifecycle record %q: %w", claim.SessionID, err)
	case rec == nil:
		o.loadErr = fmt.Errorf("load lifecycle record %q: no readable record", claim.SessionID)
	default:
		o.recordOther = !claim.matches(rec)
	}
	return o
}

// otherExecutionOwnsSession reports whether an execution other than ts's own
// now owns ts's session (SFH-R2-1, three-valued): owned=true when a live turn
// of a different execution is registered (whatever the record read did) or the
// record names a different execution; owned=false with a nil error when the
// record matches and no other turn is live; owned=false with the wrapped read
// error when the record could not be read and no other turn is live, which
// means unknown.
func (al *AgentLoop) otherExecutionOwnsSession(ts *turnState) (owned bool, err error) {
	o := al.sessionOwnership(ts)
	switch {
	case o.registeredOther:
		return true, nil
	case o.loadErr != nil:
		return false, o.loadErr
	default:
		return o.recordOther, nil
	}
}

// logOwnershipUnknown records, once per decision, that a turn's session
// ownership could not be read and what was done about it (SFH-R2-1).
func (al *AgentLoop) logOwnershipUnknown(ts *turnState, decision string, loadErr error) {
	claim := al.tsExecutionClaim(ts, "")
	logger.ErrorCF("agent", "external-cli: execution ownership unknown", map[string]any{
		"session_id": ts.transcriptSessionID,
		"turn_id":    ts.turnID,
		"run_id":     claim.RunID,
		"generation": claim.Generation,
		"boot_seq":   claim.BootSeq,
		"decision":   decision,
		"error":      loadErr.Error(),
	})
}

// announceExternalSaveFailure makes a transcript-save failure latched on an
// external-CLI turn visible once (SFH-1): the typed transcript_save_failed
// notice to the helper's own chat, a saved row where the store accepts it, and
// an error-level log with the cause. The raw store error stays in the log. It
// does not mark the turn failed (ruling R1-d): the turn's answer stands.
// The live frame is sent only when no other execution owns the session: while
// one does, the viewers' error handler would end its working state (A4), and
// when ownership is unknown the notice is saved only (SFH-R2-1). The saved
// row shows it on the next load either way.
func (al *AgentLoop) announceExternalSaveFailure(ts *turnState) {
	saveErr := ts.callAccountingError()
	if saveErr == nil || !ts.externalSaveFailureAnnounced.CompareAndSwap(false, true) {
		return
	}
	logger.ErrorCF("agent", "external-cli: a helper message could not be saved to the transcript",
		map[string]any{"session_id": ts.transcriptSessionID, "turn_id": ts.turnID, "error": saveErr.Error()})
	llm := LLMError{Code: CodeTranscriptSaveFailed, Message: defaultUserMessage(CodeTranscriptSaveFailed)}
	owned, ownerErr := al.otherExecutionOwnsSession(ts)
	switch {
	case ownerErr != nil:
		al.logOwnershipUnknown(ts, "save_notice_saved_only", ownerErr)
	case !owned:
		al.emitExternalErrorFrame(ts, "session_save", llm, "")
	}
	ts.appendClassifiedError(EventKindError.String(), "session_save", llm)
}
