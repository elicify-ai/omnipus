// browser_ws_adr085_wiring_test.go — ADR-085's gateway end: the release
// registration (BROWSER-FR-029), the server-initiated release audit
// (FR-031a/FR-052) and the operator-visible waiting surface (FR-041 – FR-044).
//
// WHY THIS FILE EXISTS. Three seams shipped with no production registrar at
// all, so three features that pass their own unit tests were unreachable in a
// running gateway:
//
//   - AgentLoop.SetBrowserWheelReleaseHook had ZERO call sites in the module,
//     so no operator prompt ever returned the browser to the agent.
//   - LiveViewRegistry's ControlIdleReleaseHooks had none either, so a
//     server-initiated release left no audit record.
//   - Nothing wrote a `browser_handover_notice` transcript entry or emitted a
//     BrowserHandoverNoticeFrame, so an agent that handed the browser over for
//     a sign-in left the operator looking at a chat where nothing happened —
//     while pkg/gateway/replay.go had been reading that subtype all along.
//
// The registration assertions here are structural on purpose. A behavioural
// test of "is the hook registered" would have to drive a whole turn through
// the bus to observe it, and — decisively — every behavioural test of these
// components passed throughout the entire period they were dead, because each
// test wired the seam itself.

package gateway

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// newBrowserWSHandlerBody returns the source text of newBrowserWSHandler's
// body, for the registration assertions below.
func newBrowserWSHandlerBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("browser_ws.go")
	require.NoError(t, err, "reading browser_ws.go")
	src := string(raw)
	start := strings.Index(src, "func newBrowserWSHandler(")
	require.GreaterOrEqual(t, start, 0, "newBrowserWSHandler must exist in browser_ws.go")
	rest := src[start:]
	end := strings.Index(rest, "\n}\n")
	require.GreaterOrEqual(t, end, 0, "newBrowserWSHandler's body must be delimited")
	return rest[:end]
}

// TestNewBrowserWSHandler_RegistersEveryADR085Seam is the guard whose absence
// let all three features ship disconnected. The ADR-085 spec names this
// constructor as the registration point precisely because it is already handed
// the *agent.AgentLoop and therefore needs no gateway.go edit.
func TestNewBrowserWSHandler_RegistersEveryADR085Seam(t *testing.T) {
	body := newBrowserWSHandlerBody(t)

	for _, tc := range []struct{ call, why string }{
		{
			"SetBrowserWheelReleaseHook(",
			"BROWSER-FR-029: without this registration no operator prompt releases the browser " +
				"wheel, and with tools.browser.control_idle_release=0 the agent is locked out for " +
				"the process's lifetime",
		},
		{
			"SetGlobalControlReleaseHooks(",
			"BROWSER-FR-031a/FR-052: without this registration the idle/disabled sweeper releases " +
				"holds silently and the audit trail shows deferrals and handovers but never a release",
		},
		{
			"SetStandDownNoticeSink(",
			"BROWSER-FR-041/FR-042: without this registration a browser_handover produces no " +
				"operator-visible line at all, live or on replay",
		},
	} {
		assert.Contains(t, body, tc.call,
			"newBrowserWSHandler must register %s — %s", tc.call, tc.why)
	}
}

// TestHandleControlRelease_ClearsTheStandDownLatchNotJustTheLock is Finding
// 7(b), pinned at the source level because the difference between the two
// calls is invisible in any behavioural assertion that only checks
// IsControlled: ReleaseControl clears lv.controller and leaves the FR-026a
// latch standing, so the gate keeps deferring every browser tool after the
// operator has visibly given the wheel back.
func TestHandleControlRelease_ClearsTheStandDownLatchNotJustTheLock(t *testing.T) {
	raw, err := os.ReadFile("browser_ws.go")
	require.NoError(t, err)
	src := string(raw)
	start := strings.Index(src, "func (h *BrowserWSHandler) handleControl(")
	require.GreaterOrEqual(t, start, 0)
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}

	assert.Contains(t, body, "ReleaseStoodDown(",
		"the release action must clear the lock, the FR-026a latch and any handover-pending state "+
			"together")
	assert.NotContains(t, body, "Live().ReleaseControl(",
		"ReleaseControl clears ONLY lv.controller; used here it leaves the agent deferring forever "+
			"after the operator released the wheel (Finding 7b)")
}

// TestStandDownNoticeText_RendersBothProducers pins BROWSER-FR-041/FR-048a's
// body rules: the handover line carries the model-authored reason, an empty or
// whitespace-only reason produces no empty parenthetical, and every line tells
// the operator that sending a message returns the browser.
func TestStandDownNoticeText_RendersBothProducers(t *testing.T) {
	withReason := standDownNoticeText(browser.StandDownNotice{
		Producer: browser.StandDownByHandover, Reason: "please sign in to the bank",
	})
	assert.Contains(t, withReason, "please sign in to the bank",
		"FR-048a: the reason must reach the operator")
	assert.Contains(t, withReason, "(please sign in to the bank)",
		"the reason is rendered as a parenthetical, as plain text")

	blank := standDownNoticeText(browser.StandDownNotice{
		Producer: browser.StandDownByHandover, Reason: "   ",
	})
	assert.NotContains(t, blank, "()",
		"FR-048a: a whitespace-only reason must produce no empty parenthetical")
	assert.Contains(t, blank, "handed the browser over")

	take := standDownNoticeText(browser.StandDownNotice{Producer: browser.StandDownByTake})
	assert.Contains(t, take, "A person has taken control",
		"a human take and an agent handover must not read as the same event")

	for name, text := range map[string]string{"handover": withReason, "take": take} {
		assert.Contains(t, text, "send a message",
			"FR-041: the %s line must tell the operator that sending a message returns the browser",
			name)
	}
}

// TestStandDownNoticeID_IsDeterministicPerHold is BROWSER-FR-044. The id must
// be stable for one unbroken hold (so the live frame and the persisted entry
// converge on ONE message in the SPA, whose reducer drops a notice whose id it
// already holds) and different across holds and across chats.
func TestStandDownNoticeID_IsDeterministicPerHold(t *testing.T) {
	a := standDownNoticeID("chat-1", 1700000000000000000)
	assert.Equal(t, a, standDownNoticeID("chat-1", 1700000000000000000),
		"the same hold must yield the same id, or live and replay render two lines that merely read alike")
	assert.NotEqual(t, a, standDownNoticeID("chat-1", 1700000000000000001),
		"a later hold must yield a different id, or a genuine fresh takeover is suppressed")
	assert.NotEqual(t, a, standDownNoticeID("chat-2", 1700000000000000000),
		"two chats must not share a notice id")
}

// TestOnStandDownNotice_PersistsTheLineReplayCanRead is BROWSER-FR-043/FR-043a
// end to end through the production emitter: the persisted entry must carry
// the `browser_handover_notice` system subtype replay.go discriminates on —
// never a content prefix — and the same message id the live frame uses.
func TestOnStandDownNotice_PersistsTheLineReplayCanRead(t *testing.T) {
	h, al := newBrowserWSTestHandler(t, nil)
	store := al.GetSessionStore()
	require.NotNil(t, store, "test setup: the harness must provide a session store")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "test setup: creating the chat session")
	chatID := meta.ID

	const holdStart = int64(1700000000000000000)
	h.onStandDownNotice(browser.StandDownNotice{
		TabSetID:              "ws:w1/session:" + chatID,
		WorkspaceID:           "w1",
		OwnerSessionID:        chatID,
		Reason:                "please sign in to the bank",
		Producer:              browser.StandDownByHandover,
		HoldStartedAtUnixNano: holdStart,
	})

	entries, err := store.ReadTranscript(chatID)
	require.NoError(t, err)

	var notices []session.TranscriptEntry
	for _, e := range entries {
		if e.SystemSubtype == "browser_handover_notice" {
			notices = append(notices, e)
		}
	}
	require.Len(t, notices, 1,
		"exactly one browser_handover_notice entry must have been written; replay.go reads this "+
			"subtype and, before this wiring existed, nothing ever wrote it")
	n := notices[0]
	assert.Equal(t, session.EntryTypeSystem, n.Type,
		"FR-043a: the entry's type stays `system`; system_subtype only narrows it")
	assert.Equal(t, standDownNoticeID(chatID, holdStart), n.ID,
		"FR-044: the persisted id must be the SAME deterministic id the live frame carries")
	assert.Contains(t, n.Content, "please sign in to the bank",
		"the persisted line must carry the reason the operator needs to act on")
}

// TestOnStandDownNotice_PrefersTheAttachedPanelsChat proves the tab-set → chat
// resolution used when the stood-down tab set is the operator's WORKSPACE-owned
// set, which belongs to no chat of its own. Without the attach-time mapping the
// one notice an operator most needs — "a person took the wheel", raised on the
// very set their panel is attached to — would have nowhere to land.
func TestOnStandDownNotice_PrefersTheAttachedPanelsChat(t *testing.T) {
	h, al := newBrowserWSTestHandler(t, nil)
	store := al.GetSessionStore()
	require.NotNil(t, store)

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "test setup: creating the chat session")
	chatID := meta.ID

	const operatorSet = "ws:w1/operator"
	h.tabSetChats.Store(operatorSet, chatID)

	notice := browser.StandDownNotice{
		TabSetID:              operatorSet,
		WorkspaceID:           "w1",
		OwnerSessionID:        "", // a workspace-owned set names no chat
		Producer:              browser.StandDownByTake,
		HoldStartedAtUnixNano: 1700000000000000000,
	}
	require.Equal(t, chatID, h.chatSessionForTabSet(notice),
		"the notice must resolve to the chat whose panel attached this tab set")

	h.onStandDownNotice(notice)

	entries, err := store.ReadTranscript(chatID)
	require.NoError(t, err)
	found := false
	for _, e := range entries {
		if e.SystemSubtype == "browser_handover_notice" {
			found = true
			assert.Contains(t, e.Content, "A person has taken control",
				"a take must render the take copy, not the handover copy")
		}
	}
	assert.True(t, found, "the take notice must have landed in the attached panel's chat")

	// And with no mapping at all, nothing is invented: a workspace-owned set
	// no panel ever attached to has no thread to file against, and filing it
	// in an unrelated one would be worse than not filing it.
	assert.Empty(t, h.chatSessionForTabSet(browser.StandDownNotice{TabSetID: "ws:w9/operator"}),
		"an unknown workspace-owned tab set must resolve to no chat")
}
