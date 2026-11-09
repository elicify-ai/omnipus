// stop_checkpoint.go: #1534 (founder 2026-10-31, Q3 = A) — the saved stopped
// answer ends where the browser's screen stopped.
//
// The web client sends, inside its first Stop, how many Unicode code points of
// one wire message it had applied to its store (contracts CancelFrame.shown).
// The winning Stop claim stores that checkpoint on the turn
// (cancel_stop.go::claimCancel) and every writer of the stopped answer asks
// stopKeepFor how much of its message to keep. No checkpoint (an older client,
// /stop, REST, channel commands, agent stop tools) keeps today's rule: the
// answer is cut where the server received the Stop.
//
// Design: /workspace/logs/1534/arch-decision.md §2 and §4.

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// StopShown is what the stopping client's screen showed of the turn being
// stopped. It is a plain engine type, not a wire type: the gateway copies the
// generated CancelFrame.shown record into it.
type StopShown struct {
	// TurnID is the turn the client's tokens carried; empty when unknown.
	TurnID string
	// MessageID is the wire message_id of the last token the client applied;
	// empty when nothing of the turn had been shown.
	MessageID string
	// CodePoints counts the Unicode code points the client applied for
	// MessageID (an invalid UTF-8 byte counts as one).
	CodePoints int
}

// stopCheckpoint is the checkpoint as the winning Stop claim resolved it
// against the turn. Written once under cancelMu by claimCancel.
type stopCheckpoint struct {
	shown StopShown
	// usable is false when the checkpoint does not describe this turn (turn
	// mismatch or a message the turn never minted): writers then keep the
	// receipt cut.
	usable bool
	// index is the position of shown.MessageID among the turn's minted
	// message ids; -1 when shown.MessageID is empty.
	index int
}

// recordMintedMessageIDLocked appends a newly minted round message id to the
// turn's ordered list. Caller holds ts.mu.
func (ts *turnState) recordMintedMessageIDLocked(id string) {
	ts.mintedMessageIDs = append(ts.mintedMessageIDs, id)
}

// mintedIndex returns the position of id among the turn's minted message ids,
// or -1 when the turn never minted it.
func (ts *turnState) mintedIndex(id string) int {
	if id == "" {
		return -1
	}
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	for i, minted := range ts.mintedMessageIDs {
		if minted == id {
			return i
		}
	}
	return -1
}

// resolveStopCheckpoint checks a client checkpoint against this turn. A
// checkpoint for another turn, or naming a message this turn never minted, is
// kept as unusable and logged with ids and counts only, never answer text.
func (ts *turnState) resolveStopCheckpoint(shown StopShown) *stopCheckpoint {
	cp := &stopCheckpoint{shown: shown, usable: true, index: -1}
	if shown.TurnID != "" && shown.TurnID != ts.turnID {
		cp.usable = false
		logger.WarnCF("agent", "stop_checkpoint_turn_mismatch", map[string]any{
			"turn_id":       ts.turnID,
			"shown_turn_id": shown.TurnID,
			"message_id":    shown.MessageID,
			"code_points":   shown.CodePoints,
		})
		return cp
	}
	if shown.MessageID == "" {
		return cp
	}
	cp.index = ts.mintedIndex(shown.MessageID)
	if cp.index < 0 {
		cp.usable = false
		logger.WarnCF("agent", "stop_checkpoint_unknown_message", map[string]any{
			"turn_id":     ts.turnID,
			"message_id":  shown.MessageID,
			"code_points": shown.CodePoints,
		})
	}
	return cp
}

// stopKeepFor reports how many code points of messageID's whole text the
// stopped answer keeps. limited is false when no usable client checkpoint was
// claimed or the message was complete before the shown one; the writer then
// keeps today's receipt cut.
func (ts *turnState) stopKeepFor(messageID string) (keep int, limited bool) {
	ts.cancelMu.Lock()
	cp := ts.stopCheckpoint
	ts.cancelMu.Unlock()
	if cp == nil || !cp.usable {
		return 0, false
	}
	if cp.shown.MessageID == "" {
		// Nothing of this turn was shown: every unsaved message keeps nothing.
		return 0, true
	}
	if messageID == cp.shown.MessageID {
		return max(cp.shown.CodePoints, 0), true
	}
	idx := ts.mintedIndex(messageID)
	if idx < 0 || idx < cp.index {
		// Minted before the shown message: the client applied all of it.
		return 0, false
	}
	// Minted after the shown message: the screen showed none of it.
	return 0, true
}

// cutStopAnswer applies the client checkpoint to messageID's whole text. A
// checkpoint ahead of the server's text keeps the server's text (the cut
// never adds text) and warns with counts only.
func (ts *turnState) cutStopAnswer(messageID, text string) string {
	keep, limited := ts.stopKeepFor(messageID)
	if !limited {
		return text
	}
	cut, ahead := CutToCodePoints(text, keep)
	if ahead {
		logger.WarnCF("agent", "stop_checkpoint_ahead", map[string]any{
			"turn_id":     ts.turnID,
			"message_id":  messageID,
			"code_points": keep,
		})
	}
	return cut
}

// CutToCodePoints keeps the first keep code points of text, counting each
// invalid UTF-8 byte as one code point (the browser receives each such byte
// as one U+FFFD). ahead is true when text holds fewer than keep code points;
// text is then returned whole.
func CutToCodePoints(text string, keep int) (cut string, ahead bool) {
	if keep <= 0 {
		return "", false
	}
	n := 0
	for i := range text {
		if n == keep {
			return text[:i], false
		}
		n++
	}
	return text, n < keep
}
