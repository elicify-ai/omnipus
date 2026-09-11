// browser_ws_adr085_behaviour_test.go — ADR-085's three operator-facing
// promises, asserted by DRIVING THEM rather than by reading browser_ws.go's
// source.
//
// WHY THIS FILE EXISTS, given browser_ws_adr085_wiring_test.go already covers
// the same requirement ids. That file parses newBrowserWSHandler's and
// handleControl's source text and asserts the right call names appear in them.
// A source-parsing assertion passes whenever the right WORDS are present; it
// cannot tell whether the wiring WORKS, and it cannot fail for the reason the
// feature would fail in front of an operator. The three properties below are
// the ones the operator actually relies on, so each is driven end to end
// through production code and asserted on observable state — the stand-down
// latch the agent's own tool gate reads, the transcript the chat replays, the
// frame a connected client receives, and the audit record on disk:
//
//  1. THE OPERATOR GETS THE BROWSER BACK. A release must clear the FR-026a
//     stand-down latch, not merely lv.controller — clearing only the latter
//     leaves every browser tool deferring for the process's lifetime while the
//     panel says the wheel was handed back.
//  2. THE WAITING NOTICE REACHES THE USER. A real browser_handover must write
//     the `browser_handover_notice` transcript entry replay.go reads AND push
//     the live frame to connected chat clients.
//  3. THE RELEASE IS AUDITED. Every server-initiated release leaves a record,
//     so the trail cannot read as though the wheel was never given back.
package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newADR085BehaviourHandler builds a BrowserWSHandler over an AgentLoop with
// audit logging on and ONE registered agent, so al.BrowserManagerForAgent can
// mint a real (never-started, no Chromium) BrowserManager and register it in
// the loop's own browserMgrs map — which is what ReleaseBrowserWheelForPrompt
// iterates. Returns the handler, the loop and the audit directory.
//
// Deliberately a real newBrowserWSHandler call: that constructor is where the
// ADR-085 seams are registered, and every test below depends on that
// registration having happened for real rather than being installed by the
// test itself.
func newADR085BehaviourHandler(t *testing.T) (*BrowserWSHandler, *agent.AgentLoop, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	auditDir := filepath.Join(tmpDir, "system")

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Mode:     config.SandboxModeOff,
			AuditLog: true,
		},
	}
	cfg.Tools.Browser.LiveViewEnabled = true
	cfg.Tools.Browser.TakeControlEnabled = true
	// A configured, never-created exec path keeps every code path in this file
	// away from a real Chromium: nothing here calls Session()/ensureStarted, and
	// if a future edit ever does it fails fast on the os.Stat instead of
	// launching a browser on a developer's machine.
	cfg.Tools.Browser.ExecPath = filepath.Join(tmpDir, "no-such-chromium-binary")
	cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newBrowserWSHandler(al, "")
	return handler, al, auditDir
}

// registerChatWSSink attaches a webchat channel carrying a real *WSHandler
// with one connected client, exactly the chain broadcastStandDownNotice walks
// (agentLoop -> channel manager -> "webchat" -> webchatChannel.wsHandler).
// Returns the connected client's send channel so a test can read the frames it
// was pushed.
func registerChatWSSink(t *testing.T, al *agent.AgentLoop) chan []byte {
	t.Helper()
	sendCh := make(chan []byte, 8)
	wsh := &WSHandler{sessions: map[string]*wsConn{"viewer-conn": {sendCh: sendCh}}}
	cm := channels.NewManagerForTesting(nil)
	cm.RegisterChannel("webchat", newWebchatChannel(wsh))
	al.SetChannelManager(cm)
	return sendCh
}

// awaitFrameOfType drains sendCh until a frame with the given "type" arrives,
// failing on timeout. Other frame types are skipped rather than treated as the
// answer, so an unrelated broadcast can never make this pass or fail wrongly.
func awaitFrameOfType(t *testing.T, sendCh chan []byte, frameType string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case raw := <-sendCh:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame["type"] == frameType {
				return frame
			}
		case <-deadline:
			t.Fatalf("no %q frame delivered to the connected chat client within %s", frameType, timeout)
		}
	}
}

// transcriptNotices returns every browser_handover_notice entry in a session's
// transcript.
func transcriptNotices(t *testing.T, store *session.UnifiedStore, chatID string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(chatID)
	require.NoError(t, err)
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.SystemSubtype == "browser_handover_notice" {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. The operator gets the browser back.
// ---------------------------------------------------------------------------

// TestBrowserWS_ControlRelease_UnlatchesTheAgentNotJustTheLock is Finding 7(b)
// asserted behaviourally, and it is the behavioural twin of the source-parsing
// TestHandleControlRelease_ClearsTheStandDownLatchNotJustTheLock.
//
// The distinction it exists to catch is invisible to IsControlled (which the
// pre-existing TestBrowserWS_HandleControl_TakeThenRelease_AllowedAndAudited
// asserts): ReleaseControl clears ONLY lv.controller, so the FR-026a
// stand-down latch survives it and mgr.Live().IsStoodDown — the exact read
// pkg/tools/browser's controlledResult gate performs before every browser tool
// call — stays true. The operator sees "released"; the agent stays locked out.
//
// BDD: Given an operator has taken the wheel through the panel, When they
// release it, Then the agent's own gate stops reporting the tab set as stood
// down.
func TestBrowserWS_ControlRelease_UnlatchesTheAgentNotJustTheLock(t *testing.T) {
	handler, al, _ := newADR085BehaviourHandler(t)
	wc, state := newControlTestFixtures(t)
	panelSet := state.panelSessionID

	handler.handleControl(wc, state, "viewer-ok", "user-c", marshalControlFrame(t, "take"), al.GetConfig())
	require.Equal(t, "controlling", readWCFrame(t, wc, 2*time.Second).State)
	require.True(t, state.mgr.Live().IsStoodDown(panelSet),
		"test setup: a human hold must stand the agent down, or this test proves nothing")

	handler.handleControl(wc, state, "viewer-ok", "user-c", marshalControlFrame(t, "release"), al.GetConfig())
	require.Equal(t, "released", readWCFrame(t, wc, 2*time.Second).State)

	assert.False(t, state.mgr.Live().IsStoodDown(panelSet),
		"Finding 7(b): the release must clear the FR-026a stand-down latch, not only lv.controller — "+
			"every browser tool reads IsStoodDown, so a surviving latch locks the agent out for the "+
			"rest of the process's life while the panel reports the wheel was handed back")
	assert.Empty(t, state.mgr.Live().Controller(panelSet),
		"the interactive lock must be cleared too")
}

// TestReleaseBrowserWheelForPrompt_GivesTheWheelBackAndAuditsIt is
// BROWSER-FR-029/FR-030/FR-050 driven through the production action the
// agent loop's release hook invokes on every operator prompt.
//
// It is the behavioural half of the registration the wiring test asserts
// structurally: the hook's BODY is proven to find the workspace's browser
// through the loop, clear every stood-down tab set on it, and write one audit
// record per release naming the operator and the chat the prompt landed on.
//
// BDD (D-G): Given an operator holds the browser, When they send a new prompt,
// Then the agent gets the wheel back — there is no hand-back button — and the
// trail records who gave it back and why.
func TestReleaseBrowserWheelForPrompt_GivesTheWheelBackAndAuditsIt(t *testing.T) {
	handler, al, auditDir := newADR085BehaviourHandler(t)

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), "mia", "")
	require.Equal(t, agent.BrowserResolveOK, outcome, "test setup: the agent must resolve a browser")
	require.NotNil(t, mgr)
	require.Contains(t, al.BrowserManagers(), mgr,
		"test setup: the manager must be registered on the loop — ReleaseBrowserWheelForPrompt "+
			"reaches browsers ONLY through al.BrowserManagers()")

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	chatID := meta.ID

	operatorSet := mgr.OperatorSessionID()
	require.True(t, mgr.Live().TakeControl(operatorSet, "human-viewer"),
		"test setup: the operator takes the wheel")
	require.True(t, mgr.Live().IsStoodDown(operatorSet))

	handler.ReleaseBrowserWheelForPrompt(context.Background(), chatID,
		agent.ReleaseActor{GatewayUserID: "daniel"})

	assert.False(t, mgr.Live().IsStoodDown(operatorSet),
		"FR-029: the operator's next prompt must return the wheel — the stand-down latch, not just "+
			"the lock, since the latch is what the agent's tool gate reads")

	rec := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserLiveControlReleased)
	assert.Equal(t, audit.SeverityInfo, rec.Severity)
	assert.Equal(t, "operator_prompt", rec.Fields["reason"],
		"FR-031a: a prompt release must be distinguishable from an idle expiry in the trail")
	assert.Equal(t, operatorSet, rec.Fields["session_id"],
		"the record must name the tab set that was actually released")
	assert.Equal(t, "human-viewer", rec.Fields["former_holder"],
		"the record must attribute the hold that ended")
	assert.Equal(t, chatID, rec.Fields["root_chat_session_id"],
		"FR-050: the record must name the chat whose prompt caused the release")
	assert.Equal(t, "daniel", rec.Fields["acting_user"],
		"FR-030: the gateway principal who sent the prompt is the acting user")
}

// TestReleaseBrowserWheelForPrompt_NoHoldWritesNoRecord is the negative half:
// a prompt arriving while nobody holds the wheel must leave NO release record.
// Without this, a trail full of phantom releases would read exactly like a
// working one, and the assertion above would pass on an implementation that
// audited unconditionally.
func TestReleaseBrowserWheelForPrompt_NoHoldWritesNoRecord(t *testing.T) {
	handler, al, auditDir := newADR085BehaviourHandler(t)

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), "mia", "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	require.False(t, mgr.Live().IsStoodDown(mgr.OperatorSessionID()),
		"test setup: nobody holds the wheel")

	handler.ReleaseBrowserWheelForPrompt(context.Background(), "no-such-chat",
		agent.ReleaseActor{GatewayUserID: "daniel"})

	for _, r := range readBrowserAuditRecords(t, auditDir) {
		assert.NotEqual(t, audit.EventBrowserLiveControlReleased, r.Event,
			"a prompt that released nothing must write no release record — an audit trail that says "+
				"the wheel was handed back when it never was is worse than none")
	}
}

// ---------------------------------------------------------------------------
// 2. The waiting notice actually reaches the user.
// ---------------------------------------------------------------------------

// TestBrowserHandover_WritesTheNoticeAndEmitsTheLiveFrame is SF-2, both halves,
// from the PRODUCTION emitter: nothing in this test installs the notice sink —
// newBrowserWSHandler's own registration is what must carry the notice from
// pkg/tools/browser into the gateway.
//
// The live-frame half is the one that had no behavioural proof at all. It is
// asserted here on a real connected chat client, through the real
// agentLoop -> channel manager -> webchat -> WSHandler resolution
// broadcastStandDownNotice performs, because an operator staring at a chat
// where nothing happened is precisely the reported defect.
//
// BDD (FR-041/FR-042/FR-043/FR-044): Given an agent hands the browser over for
// a sign-in, When the handover is performed, Then the operator sees one line
// carrying the reason — live now, and identically on reload.
func TestBrowserHandover_WritesTheNoticeAndEmitsTheLiveFrame(t *testing.T) {
	handler, al, _ := newADR085BehaviourHandler(t)
	sendCh := registerChatWSSink(t, al)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	chatID := meta.ID

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), "mia", "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	tabSet := mgr.PanelTabSetID(chatID)
	// What handleAttach records when a panel attaches — the mapping that lets a
	// notice raised deep inside pkg/tools/browser, which holds only the tab-set
	// key, land in the thread the operator is watching.
	handler.tabSetChats.Store(tabSet, chatID)

	// The production call browser_handover's tool body makes.
	transitioned, holdStart := mgr.Live().SetHandoverPending(tabSet, "please sign in to the bank")
	require.True(t, transitioned, "test setup: a first handover on a free tab set is a transition")
	require.NotZero(t, holdStart)

	wantID := standDownNoticeID(chatID, holdStart)

	// (a) PERSISTED. replay.go discriminates on the system subtype, and before
	//     this wiring existed nothing in the module ever wrote one.
	var notices []session.TranscriptEntry
	require.Eventually(t, func() bool {
		notices = transcriptNotices(t, store, chatID)
		return len(notices) == 1
	}, 3*time.Second, 20*time.Millisecond,
		"exactly one browser_handover_notice transcript entry must be written by the registered sink")
	n := notices[0]
	assert.Equal(t, session.EntryTypeSystem, n.Type,
		"FR-043a: the entry stays type `system`; system_subtype only narrows it")
	assert.Equal(t, wantID, n.ID,
		"FR-044: the persisted id is derived from (chat, hold start), so live and replay converge")
	assert.Contains(t, n.Content, "please sign in to the bank",
		"FR-048a: the model-authored reason must reach the operator")

	// (b) LIVE. The half that was wired but never proven: the frame must
	//     actually reach a connected client, with the SAME id as the entry so
	//     the SPA renders ONE message rather than two that merely read alike.
	frame := awaitFrameOfType(t, sendCh, "browser_handover_notice", 3*time.Second)
	assert.Equal(t, chatID, frame["session_id"],
		"the frame must be scoped to the chat the SPA routes it by")
	assert.Equal(t, wantID, frame["message_id"],
		"FR-044: the live frame and the persisted entry must carry the SAME message id")
	assert.Equal(t, n.Content, frame["text"],
		"the operator must not be shown one sentence live and a different one on reload")
}

// TestOperatorTakeover_RaisesTheNoticeThroughTheRegisteredSink is the other
// FR-041 producer, driven the same way: a human taking the wheel must produce
// the take copy — not the handover copy — in the chat whose panel is attached
// to the tab set. A workspace-owned tab set names no chat of its own, so this
// is the case that depends entirely on the attach-time mapping.
func TestOperatorTakeover_RaisesTheNoticeThroughTheRegisteredSink(t *testing.T) {
	handler, al, _ := newADR085BehaviourHandler(t)
	sendCh := registerChatWSSink(t, al)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	chatID := meta.ID

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), "mia", "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	tabSet := mgr.OperatorSessionID()
	handler.tabSetChats.Store(tabSet, chatID)

	require.True(t, mgr.Live().TakeControl(tabSet, "human-viewer"))

	var notices []session.TranscriptEntry
	require.Eventually(t, func() bool {
		notices = transcriptNotices(t, store, chatID)
		return len(notices) == 1
	}, 3*time.Second, 20*time.Millisecond,
		"a human take must raise exactly one operator-visible line")
	assert.Contains(t, notices[0].Content, "A person has taken control",
		"a take and an agent handover must not read as the same event")

	frame := awaitFrameOfType(t, sendCh, "browser_handover_notice", 3*time.Second)
	assert.Equal(t, notices[0].ID, frame["message_id"])
}

// ---------------------------------------------------------------------------
// 3. The release is audited.
// ---------------------------------------------------------------------------

// TestSweeperReleaseObservers_WriteDistinctAuditRecords is SF-3's gateway half:
// the two functions newBrowserWSHandler hands to
// browser.SetGlobalControlReleaseHooks must each write a record, under DISTINCT
// event names — conflating them loses the ability to tell "nobody was watching"
// from "an operator switched the feature off".
//
// Scope note, stated rather than implied: the pkg/tools/browser half — that the
// FR-031a/FR-052 sweeper really calls the process-wide hooks — is proven
// behaviourally by TestSweeper_ReportsToTheGlobalReleaseHooks in
// pkg/tools/browser/live_standdown_notice_test.go. The sweeper's own ticker is
// a 30s unexported constant, so the only thing this package can add behind it
// without a production seam is what the registered functions DO, which is this.
func TestSweeperReleaseObservers_WriteDistinctAuditRecords(t *testing.T) {
	handler, _, auditDir := newADR085BehaviourHandler(t)

	handler.onSweeperIdleRelease("ws:w1/operator", "viewer-idle")
	handler.onSweeperDisabledRelease("ws:w1/session:chat-2", "viewer-disabled")

	idle := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserControlIdleRelease)
	assert.Equal(t, "ws:w1/operator", idle.Fields["session_id"])
	assert.Equal(t, "viewer-idle", idle.Fields["former_holder"])

	disabled := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserControlDisabledRelease)
	assert.Equal(t, "ws:w1/session:chat-2", disabled.Fields["session_id"])
	assert.Equal(t, "viewer-disabled", disabled.Fields["former_holder"])

	require.NotEqual(t, audit.EventBrowserControlIdleRelease, audit.EventBrowserControlDisabledRelease,
		"the two outcomes must never share one event name")
}

// TestStandDownNoticeFrame_FieldNamesMatchTheContract keeps the field names
// this file asserts on honest against the generated wire type: they are read
// out of a decoded JSON map above, so a rename in the contract would otherwise
// turn every assertion here into a comparison against nil.
func TestStandDownNoticeFrame_FieldNamesMatchTheContract(t *testing.T) {
	raw, err := json.Marshal(generated.BrowserHandoverNoticeFrame{
		Type:      string(generated.WsFrameTypeBrowserHandoverNotice),
		SessionId: "chat-1",
		MessageId: "browser-handover-chat-1-1",
		Text:      "a line",
	})
	require.NoError(t, err)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(raw, &frame))
	for _, field := range []string{"type", "session_id", "message_id", "text"} {
		assert.Contains(t, frame, field,
			"the generated BrowserHandoverNoticeFrame must carry %q — the SPA and the tests above "+
				"both read it by that name", field)
	}
}
