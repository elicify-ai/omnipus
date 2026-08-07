//go:build !windows

package tools

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// session_ownership_test.go covers M5 (live UAT 2026-07-31): background bash
// job ids were resolved from a flat/global registry with no access scoping
// to the session that created them. A root chat session could poll — and,
// confirmed by investigation for this fix (not merely theoretical), KILL —
// a background job belonging to a completely different, unrelated session
// just by knowing its short session_id. Root cause: shell.go's
// getSessionArg (shared by executePoll/executeRead/executeKill) called
// t.sessionManager.Get(sessionID) — a flat, unscoped map lookup — with no
// comparison against the caller's own ToolTranscriptSessionID(ctx) /
// session.OwnerSessionID anywhere in that path.
//
// The fix (SessionManager.GetOwned, session.go) gates exactly that
// caller-facing lookup. It deliberately must NOT affect the legitimate
// cancel cascade (KillAllForSessions, used by stop_plan/delegate cancel to
// reach a delegated child's background jobs from its parent) — that path
// scans by OwnerSessionID membership directly and never calls Get/GetOwned
// at all. Both directions are proven below, against real OS processes
// (build-tagged !windows per this package's existing pidAlive precedent in
// session_adr057_unix_test.go, whose helpers — adr057TranscriptCtx,
// startRealBackgroundSleep, pidAlive — this file reuses directly, real
// store-backed state, not a spy).

// TestBash_CrossSessionPollReadKill_Denied is the RED-then-GREEN proof for
// the vulnerability itself: an attacker context with a completely different
// ToolTranscriptSessionID must be denied poll/read/kill on a victim's
// session_id — with the SAME "session not found" message a genuinely-missing
// id would produce (never a distinguishable "forbidden" response that would
// let an attacker confirm the session exists at all) — and the victim's
// real OS process and session status must be COMPLETELY UNAFFECTED by the
// denied kill attempt (the positive lower bound: not just "the tool call
// returned an error", but "the victim process is still actually alive").
func TestBash_CrossSessionPollReadKill_Denied(t *testing.T) {
	tool, _ := newBashTool(t, false)
	tool.godMode = true

	nonce := time.Now().UnixNano()
	victimOwnerID := fmt.Sprintf("victim-owner-%d", nonce)
	attackerID := fmt.Sprintf("attacker-%d", nonce)
	require.NotEqual(t, victimOwnerID, attackerID, "test fixture must use distinct owner/attacker ids")

	victimCtx := adr057TranscriptCtx(t, victimOwnerID)
	victimSessionID, victimPID := startRealBackgroundSleep(t, tool, victimCtx, 20)

	attackerCtx := adr057TranscriptCtx(t, attackerID)

	// --- poll denied ---
	pollResult := tool.Execute(attackerCtx, map[string]any{
		"action":     "poll",
		"session_id": victimSessionID,
	})
	require.True(t, pollResult.IsError, "cross-session poll must be denied, got ForLLM=%q", pollResult.ForLLM)
	assert.Contains(t, pollResult.ForLLM, "session not found",
		"denial must read identically to a genuinely-missing session_id — never a distinguishable "+
			"'forbidden' response that would let an attacker confirm the session exists")

	// --- read denied ---
	readResult := tool.Execute(attackerCtx, map[string]any{
		"action":     "read",
		"session_id": victimSessionID,
	})
	require.True(t, readResult.IsError, "cross-session read must be denied, got ForLLM=%q", readResult.ForLLM)
	assert.Contains(t, readResult.ForLLM, "session not found")

	// --- kill denied — the destructive-action half confirmed exploitable ---
	killResult := tool.Execute(attackerCtx, map[string]any{
		"action":     "kill",
		"session_id": victimSessionID,
	})
	require.True(t, killResult.IsError, "cross-session kill must be denied, got ForLLM=%q", killResult.ForLLM)
	assert.Contains(t, killResult.ForLLM, "session not found")

	// Positive lower bound: the victim's REAL OS process must still be
	// alive, and its session must still report "running" — proving the
	// denied kill attempt had NO effect whatsoever, not merely that the
	// tool call's return value looked like an error.
	assert.True(t, pidAlive(victimPID),
		"victim's real PID must still be alive — a denied cross-session kill must have zero effect")
	victimSession, err := tool.sessionManager.Get(victimSessionID)
	require.NoError(t, err)
	assert.Equal(t, "running", victimSession.GetStatus(),
		"victim's session must still report running — a denied cross-session kill must not touch it")

	// --- legitimate same-owner access still works (regression safety) ---
	selfPollResult := tool.Execute(victimCtx, map[string]any{
		"action":     "poll",
		"session_id": victimSessionID,
	})
	require.False(t, selfPollResult.IsError, "the OWNING session's own poll must still succeed, got ForLLM=%q",
		selfPollResult.ForLLM)
	assert.Contains(t, selfPollResult.ForLLM, "running")

	selfKillResult := tool.Execute(victimCtx, map[string]any{
		"action":     "kill",
		"session_id": victimSessionID,
	})
	require.False(t, selfKillResult.IsError, "the OWNING session's own kill must still succeed, got ForLLM=%q",
		selfKillResult.ForLLM)
	require.Eventually(t, func() bool { return !pidAlive(victimPID) }, 3*time.Second, 50*time.Millisecond,
		"the owning session's legitimate kill must actually terminate the real OS process")
	assert.Equal(t, "killed", victimSession.GetStatus())
}

// TestSessionManager_LegitimateCascadeStillReachesDescendant_AfterOwnershipFix
// is the MANDATORY positive control: the ownership gate added to
// getSessionArg/GetOwned must not break the legitimate cancel cascade
// (stop_plan/delegate cancel killing a delegated CHILD's background jobs
// from its PARENT) — because that cascade is a completely separate code
// path (KillAllForSessions scans by OwnerSessionID membership directly, and
// never calls Get/GetOwned), but this is proven directly here rather than
// merely inferred, since breaking it would re-open the grandchild leak this
// release already fixed elsewhere.
//
// A real child background process is spawned under a distinct child session
// id; the parent (root) cascades a kill over the resolved descendant set
// {rootID, childID} — exactly like TestSessionManager_KillAllForSessions_
// CascadesOverDescendantSetRealPIDs in session_adr057_unix_test.go, repeated
// here in the SAME file as the ownership-denial proof so both directions of
// the M5 fix are visible together — and the child's real OS process must
// actually be terminated.
func TestSessionManager_LegitimateCascadeStillReachesDescendant_AfterOwnershipFix(t *testing.T) {
	tool, _ := newBashTool(t, false)
	tool.godMode = true

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("m5-cascade-root-%d", nonce)
	childID := fmt.Sprintf("m5-cascade-child-%d", nonce)

	childSessionID, childPID := startRealBackgroundSleep(t, tool, adr057TranscriptCtx(t, childID), 20)

	require.True(t, pidAlive(childPID), "precondition: child's real PID must be alive before the cascade runs")

	// The parent (root) has never itself polled/killed the child's
	// session_id directly (that would rightly be denied by GetOwned, per
	// the test above) — the legitimate mechanism is the descendant-set
	// cascade, called exactly as stop_plan/delegate cancel call it: with
	// the resolved {rootID, childID} set, never a single exact match, and
	// never through getSessionArg/GetOwned at all.
	killed, failed := tool.sessionManager.KillAllForSessions([]string{rootID, childID})
	assert.Equal(t, 1, killed, "the cascade must kill exactly the child's background job")
	assert.Equal(t, 0, failed)

	require.Eventually(t, func() bool { return !pidAlive(childPID) }, 3*time.Second, 50*time.Millisecond,
		"the child's real PID must actually be terminated by the legitimate cascade — the ownership fix "+
			"added to the caller-facing poll/read/kill path must not have broken it")

	childSession, err := tool.sessionManager.Get(childSessionID)
	require.NoError(t, err)
	assert.Equal(t, "canceled", childSession.GetStatus())
}
