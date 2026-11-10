package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// Delegate polling guard (issue #1306, UAT R1). The identical-success run
// (tool_failure_circuit_breaker.go: warn at toolRepeatWarnThreshold, end the
// turn at toolRepeatStopThreshold) only sees byte-identical calls, so a
// parent that ALTERNATES delegate status / peek / inbox on the same child
// never trips it: in the UAT a parent polled ~196 times in one turn until the
// per-turn tool-call limit ended it. The oscillation detector does not catch
// it either, because status carries an age ("started 5 min ago") that makes
// every result differ.
//
// This folds those polls into ONE repeat signature per child and per
// observed information: consecutive status/peek/inbox calls on the same
// child whose results carry no new information (identical after the age
// figures are blanked) count as one run, with exactly the same thresholds,
// warning and end-of-turn response as the identical-call guard. Any new
// information — a state change, a new message, new child activity — or any
// other call in between starts a new run. No time or turn budget is
// involved, and nothing is refused before dispatch.

// delegatePollActions are the read-only delegate actions a parent polls with.
var delegatePollActions = map[string]bool{"status": true, "peek": true, "inbox": true}

// delegatePollAgeRe matches the age figures status prints ("12 s", "5 min",
// "2 h", "1 d"), which change on every poll without being information.
var delegatePollAgeRe = regexp.MustCompile(`\b\d+ (s|min|h|d)\b`)

// delegatePollState is one polled child's record within a turn.
type delegatePollState struct {
	// last maps a polling action to its last normalized result.
	last map[string]string
	// epoch increments whenever a poll of this child returned something
	// its action had not returned before — that is new information.
	epoch int
}

// toolRepeatSignature returns the signature the identical-success run is
// keyed on for this call: for a delegate status/peek/inbox call on a child,
// "delegate-poll/<child>/<epoch>" (poll=true), where the epoch moves only
// when a poll brings new information — so alternating actions that keep
// returning what they returned before share one run. Any other call keeps
// its own signature.
func (ts *turnState) toolRepeatSignature(toolName string, args map[string]any, callSig, resultContent string) (sig string, poll bool) {
	if toolName != "delegate" {
		return callSig, false
	}
	action, _ := args["action"].(string)
	sessionID, _ := args["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if !delegatePollActions[action] || sessionID == "" {
		return callSig, false
	}
	info := toolResultLoopKey(delegatePollAgeRe.ReplaceAllString(resultContent, "N $1"))
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.delegatePolls == nil {
		ts.delegatePolls = make(map[string]*delegatePollState)
	}
	st := ts.delegatePolls[sessionID]
	if st == nil {
		st = &delegatePollState{last: make(map[string]string)}
		ts.delegatePolls[sessionID] = st
	}
	if prev, seen := st.last[action]; !seen || prev != info {
		if seen {
			st.epoch++ // this action now says something it did not say before
		}
		st.last[action] = info
	}
	return fmt.Sprintf("delegate-poll\x00%s\x00%d", sessionID, st.epoch), true
}

// toolPollRepeatWarnNotice is toolRepeatWarnNotice for delegate polling.
func toolPollRepeatWarnNotice(run int) string {
	return fmt.Sprintf(
		"\n\n[SYSTEM NOTICE: you have now polled this delegated session %d times in a row with no new "+
			"information. Polling again will not make progress on its own — act on what it already "+
			"returned, take a materially different step, or tell the user what you are waiting for. "+
			"At %d polls in a row with no new information this turn will be stopped.]",
		run, toolRepeatStopThreshold,
	)
}

// toolPollRepeatStopNotice is toolRepeatStopNotice for delegate polling.
func toolPollRepeatStopNotice(run int) string {
	return fmt.Sprintf(
		"I stopped this turn: I polled a delegated session %d times in a row without any new "+
			"information. Tell me how you would like to proceed, or ask me to try a different approach.",
		run,
	)
}
