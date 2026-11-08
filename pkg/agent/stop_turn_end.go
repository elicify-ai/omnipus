// stop_turn_end.go: #1534 — a user Stop ends the WHOLE turn (founder
// decision Q1 = A, 2026-10-31): after the Stop claim no further provider
// call or tool call of the turn runs, nothing the provider delivers after
// the claim is saved, and the saved answer ends where the screen stopped.
//
// The claim itself is cancel_stop.go::claimCancel(stopOnly=true), which sets
// ts.stopRequested and cuts the live streamer (CutoffAtStop). This file holds
// the turn-loop half: the text an in-flight provider attempt showed before
// the claim, and the one exit the loop takes once it sees the claim.

package agent

// recordAttemptShown keeps the visible text the current provider attempt has
// streamed, but only while no Stop is claimed: a delta delivered after the
// claim is published (the browser discards it) and never becomes part of the
// answer. Called from runProviderAttempt's stream callback and tail flush.
func (ts *turnState) recordAttemptShown(shown *string, delta string) {
	if ts.stopRequested.Load() {
		return
	}
	*shown += delta
}

// setStopShown stores what the provider attempt that just returned showed
// before a Stop claim. A success returned before any claim owns its own text
// (the response carries it), so it stores nothing and a later exit cannot
// add that text a second time.
func (ts *turnState) setStopShown(shown string, success bool) {
	if success && !ts.stopRequested.Load() {
		shown = ""
	}
	ts.mu.Lock()
	ts.stopShown = shown
	ts.mu.Unlock()
}

// takeStopShown returns and clears the pre-Stop text of the last provider
// attempt that the answer does not yet hold.
func (ts *turnState) takeStopShown() string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	shown := ts.stopShown
	ts.stopShown = ""
	return shown
}

// endTurnAtStop ends the turn once a user Stop is claimed, without another
// provider round, tool call or steering continuation. The answer is what the
// screen showed: an auto-continued answer's accumulated text plus the
// current round's pre-Stop text. A response that came back after the claim
// is ignored entirely — its content, narration and tool calls. The turn then
// finalizes normally; Finish writes the turn_canceled record that labels the
// stopped answer interrupted.
func (cn *agentLoopRunTurnConductorRunIterations) endTurnAtStop() agentLoopRunTurnConductorRunIterationsFlow {
	ts := cn.rc.rx.rr.rq.ri.rf.rt.ts
	answer := ts.takeStopShown()
	if ts.hadContinuation() {
		answer = ts.appendToAccumulator(answer)
		ts.resolveContinuation()
	}
	cn.rc.rx.finalContent = answer
	cn.rc.rx.finalIsModelAnswer = answer != ""
	return agentLoopRunTurnConductorRunIterationsBreakL1
}
