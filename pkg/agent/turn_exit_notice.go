// turn_exit_notice.go: #1775 — the saved order of a stopped turn.
//
// A typed exit (user Stop, turn timeout) used to write its error notice to the
// transcript inside typedTurnExit, BEFORE runTurn's deferred
// preserveTruncatedAccumulator / finalizeStreamer wrote the interrupted
// partial answer. A reload therefore read notice, partial, while live had
// shown partial, notice. The live frame is still emitted at once; only the
// transcript write moves behind the partial, so the saved order is partial,
// notice, turn_canceled marker (the marker is written by Finish, which runs
// after flushExitNotice).
package agent

// queuedExitNotice is one typed-exit error awaiting its transcript write.
type queuedExitNotice struct { // not-wire-format: in-memory queue entry
	kind  string
	stage string
	llm   LLMError
}

// emitTurnExitFrame is emitTurnErrorFrame for a typed exit: the live frame goes
// out now, the transcript entry is queued for flushExitNotice.
func (al *AgentLoop) emitTurnExitFrame(ts *turnState, meta EventMeta, payloadStage, transcriptStage string, llm LLMError) {
	al.emitErrorEvent(ts, meta, payloadStage, llm)
	ts.queueExitNotice(EventKindError.String(), transcriptStage, llm)
}

// queueExitNotice holds the transcript write of a typed-exit error until
// flushExitNotice. A second queue in the same turn keeps the first (the exit
// that ended the turn wins), so the notice is never written twice.
func (ts *turnState) queueExitNotice(kind, stage string, llm LLMError) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.exitNotice == nil {
		ts.exitNotice = &queuedExitNotice{kind: kind, stage: stage, llm: llm}
	}
}

// persistExitNoticeThenCancel is the streamer's before-done callback (#1524,
// #1775): the queued stop notice first, then the turn_canceled marker, so a
// reload taken at done reads partial, notice, marker. Both writes are
// once-only, so the deferred finishAfterExitNotice finds nothing left to do.
func (ts *turnState) persistExitNoticeThenCancel() {
	ts.flushExitNotice()
	ts.persistCancelBeforeDone()
}

// finishAfterExitNotice is runTurn's Finish defer: it runs after
// finalizeStreamer has written the interrupted answer, writes the queued
// notice, and only then calls Finish, which appends the turn_canceled marker.
func (ts *turnState) finishAfterExitNotice() {
	ts.flushExitNotice()
	ts.Finish(ts.hardAbortRequested())
}

// flushExitNotice writes the queued notice, if any. It writes even when no answer exists: the notice is
// never lost.
func (ts *turnState) flushExitNotice() {
	ts.mu.Lock()
	notice := ts.exitNotice
	ts.exitNotice = nil
	ts.mu.Unlock()
	if notice == nil {
		return
	}
	ts.appendClassifiedError(notice.kind, notice.stage, notice.llm)
}
