// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WS RedirectFrame authorization RED pack (ADR-20260928 D9, frozen: a
// redirect is conversation-scoped, signed-in-owner, helper-only; the D1.7
// QUESTION bypass never authorizes another user's or another chat's
// redirect).
//
// Specification under test: handleRedirectFrame must authorize the named
// target against BOTH the connection's bound session hub (wc.boundHub.id ==
// f.SessionId, read under WSHandler.mu — the reader's chatID is the
// transport webchat UUID, never the session id) AND the persisted session
// owner (UnifiedMeta.Owner == wc.userID), before the redirect seam can be
// reached. Every denial is VISIBLE as an error frame (fail-closed) and
// leaves the parent, hub and target records unchanged.
//
// Oracle sources: the security-lead HIGH finding this pack pins (the two
// checks above are missing on the current handleRedirectFrame), the
// visibility contract in websocket_redirect.go's header, and the seam's
// documented visible dependency pkg/agent/stop_redirect_seam.go::
// ErrRedirectNotWired. No expected value was read off a passing run.
//
// Known limitation, stated rather than faked: WSHandler.agentLoop is the
// concrete *agent.AgentLoop, so this pack has no injectable seam to COUNT
// RedirectSessionTurn crossings. The error-text proxy below is sound while
// the seam's only reachable outcome is ErrRedirectNotWired: a denial whose
// frame is anything other than that sentinel's surface proves the seam was
// never reached. The backend should add a production call-count seam when
// wiring D2; until then this is the honest maximum the harness supports.
//
// The green run and mutation probes for this pack are CHECK's duty and are
// deferred per the RED role split.

package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// redirectIdleProvider is present only because constructing an agent loop
// requires one. This pack never runs a turn.
type redirectIdleProvider struct{}

func (redirectIdleProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "unused"}, nil
}

func (redirectIdleProvider) GetDefaultModel() string { return "redirect-auth-idle" }

type redirectAuthFixture struct {
	al        *agent.AgentLoop
	lifecycle *session.LifecycleStore
	h         *WSHandler
	wc        *wsConn
	ch        chan []byte
	reader    *wsHandlerReadLoop
	transport string // the connection's webchat UUID — deliberately NOT a session id
}

func newRedirectAuthFixture(t *testing.T, userID string) *redirectAuthFixture {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, redirectIdleProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)

	h := makeMinimalHandler()
	h.agentLoop = al
	h.msgBus = msgBus
	wc, ch := makeForwarderTestConn(32)
	wc.userID = userID
	f := &redirectAuthFixture{
		al: al, lifecycle: lifecycle, h: h, wc: wc, ch: ch,
		transport: "webchat-uuid-" + userID + "-transport",
	}
	// The production dispatcher entry (websocket.go's read loop calls
	// dispatchFrameOrRedirect), driven directly the way the U2 CancelFrame
	// pack drives dispatchFrame.
	f.reader = &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: f.transport}
	return f
}

// newChatRoot creates a chat root the way the ADR-093 web-Stop pack does:
// real session meta, real workspace tag, real lifecycle record.
func (f *redirectAuthFixture) newChatRoot(t *testing.T, owner string) string {
	t.Helper()
	meta, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, f.al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{
		WorkspaceID: strPtr(testHarnessWorkspaceMembershipID),
		Owner:       &owner,
	}))
	require.NoError(t, f.lifecycle.Persist(&session.LifecycleRecord{
		SessionID:      meta.ID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    testHarnessWorkspaceMembershipID,
		AgentID:        "mia",
		Origin:         &session.Origin{Kind: session.OriginKindChat},
	}))
	return meta.ID
}

// newHelper launches a GENUINE steered helper under root through the
// production Launch path (real SteeredBy edge — never a hand-built record)
// and stamps UnifiedMeta.Owner so the ownership oracle is explicit.
func (f *redirectAuthFixture) newHelper(t *testing.T, rootID, owner string) string {
	t.Helper()
	launched, err := agent.NewSteerLauncher(f.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: rootID,
		TargetAgentID:     "mia",
		Task:              "helper fixture work until stopped",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})
	require.NoError(t, err, "launch a real helper under %s", rootID)
	require.NoError(t, f.al.GetSessionStore().SetMeta(launched.SessionID, session.MetaPatch{Owner: &owner}))
	meta, err := f.al.GetSessionStore().GetMeta(launched.SessionID)
	require.NoError(t, err)
	require.NotNil(t, meta, "helper meta must exist after the owner stamp")
	require.Equal(t, owner, meta.Owner, "fixture must persist the owner it stamps: %s", launched.SessionID)
	rec, err := f.lifecycle.Load(launched.SessionID)
	require.NoError(t, err)
	require.NotNil(t, rec.SteeredBy, "fixture helper must carry a real SteeredBy edge")
	return launched.SessionID
}

func (f *redirectAuthFixture) bind(t *testing.T, sessionID string) {
	t.Helper()
	bindTestConnToSession(f.h, f.transport, sessionID, f.wc)
	require.True(t, f.h.connBoundToSession(f.wc, sessionID), "fixture bind must take effect")
}

func (f *redirectAuthFixture) sendRedirect(t *testing.T, sessionID, instruction string) {
	t.Helper()
	data, err := json.Marshal(generated.RedirectFrame{
		Type:        string(generated.WsFrameTypeRedirect),
		SessionId:   sessionID,
		Instruction: instruction,
	})
	require.NoError(t, err)
	f.reader.dispatchFrameOrRedirect(data, wsTypeOnly{Type: string(generated.WsFrameTypeRedirect)})
}

// redirectSnapshot loads the named records so a denial can prove nothing
// moved. Values are deep-compared afterwards with require.Equal.
func redirectSnapshot(t *testing.T, lifecycle *session.LifecycleStore, ids ...string) map[string]*session.LifecycleRecord {
	t.Helper()
	snap := make(map[string]*session.LifecycleRecord, len(ids))
	for _, id := range ids {
		rec, err := lifecycle.Load(id)
		require.NoError(t, err, "snapshot %s", id)
		snap[id] = rec
	}
	return snap
}

func redirectRequireUnchanged(t *testing.T, lifecycle *session.LifecycleStore, before map[string]*session.LifecycleRecord) {
	t.Helper()
	for id, was := range before {
		rec, err := lifecycle.Load(id)
		require.NoError(t, err, "reload %s", id)
		require.Equal(t, was, rec, "denied redirect must leave the record unchanged: %s", id)
	}
}

// requireRefusalNotSeam asserts the frame is a VISIBLE error frame whose
// message is an authorization refusal — NOT the redirect seam's surface.
// Sound today because the seam's only reachable outcome is the
// ErrRedirectNotWired sentinel wrapped as "Redirect failed: …" (see the
// file-header limitation note).
func requireRefusalNotSeam(t *testing.T, msg string) {
	t.Helper()
	require.NotContains(t, msg, "Redirect failed:",
		"an authorization denial must be refused BEFORE the redirect seam — a seam-surface error means the request was forwarded")
	require.NotContains(t, msg, agent.ErrRedirectNotWired.Error(),
		"an authorization denial must never surface the ErrRedirectNotWired dependency")
}

func TestRedirectFrameAuthorizationScope(t *testing.T) {
	t.Run("bound_helper_scope_refuses_other_session_of_same_owner", func(t *testing.T) {
		f := newRedirectAuthFixture(t, "alice")
		root := f.newChatRoot(t, "alice")
		bound := f.newHelper(t, root, "alice")
		other := f.newHelper(t, root, "alice") // same owner, different helper chat
		f.bind(t, bound)
		before := redirectSnapshot(t, f.lifecycle, root, bound, other)

		f.sendRedirect(t, other, "continue with the export")

		frame := drainFrame(t, f.ch)
		require.Equal(t, string(generated.WsFrameTypeError), frame.Type, "the refusal must be a visible error frame")
		requireRefusalNotSeam(t, frame.Message)
		require.True(t, f.h.connBoundToSession(f.wc, bound), "the denial must not disturb the connection's hub binding")
		redirectRequireUnchanged(t, f.lifecycle, before)
	})

	t.Run("owner_refusal_even_when_attached_to_foreign_session", func(t *testing.T) {
		f := newRedirectAuthFixture(t, "alice")
		root := f.newChatRoot(t, "bob")
		foreign := f.newHelper(t, root, "bob") // UnifiedMeta.Owner = bob
		// Alice is ATTACHED to bob's helper hub — the scope check alone would
		// pass. The persisted owner is the second, independent gate.
		f.bind(t, foreign)
		before := redirectSnapshot(t, f.lifecycle, root, foreign)

		f.sendRedirect(t, foreign, "continue with the export")

		frame := drainFrame(t, f.ch)
		require.Equal(t, string(generated.WsFrameTypeError), frame.Type, "the refusal must be a visible error frame")
		requireRefusalNotSeam(t, frame.Message)
		require.True(t, f.h.connBoundToSession(f.wc, foreign), "the denial must not disturb the connection's hub binding")
		redirectRequireUnchanged(t, f.lifecycle, before)
	})

	t.Run("unauthenticated_connection_refused", func(t *testing.T) {
		f := newRedirectAuthFixture(t, "")
		root := f.newChatRoot(t, "alice")
		helper := f.newHelper(t, root, "alice")
		f.bind(t, helper)
		before := redirectSnapshot(t, f.lifecycle, root, helper)

		f.sendRedirect(t, helper, "continue with the export")

		frame := drainFrame(t, f.ch)
		require.Equal(t, string(generated.WsFrameTypeError), frame.Type, "the refusal must be a visible error frame")
		require.Contains(t, frame.Message, "authenticated",
			"the refusal must name the missing authentication, not a generic failure")
		requireRefusalNotSeam(t, frame.Message)
		redirectRequireUnchanged(t, f.lifecycle, before)
	})

	// NOT a success claim: this pins the legit path reaching the seam and
	// surfacing its documented visible dependency (ErrRedirectNotWired) —
	// the D2 composition will replace that error, and this case guards the
	// authorization fix against over-refusing the legitimate redirect.
	t.Run("own_bound_owned_helper_reaches_redirect_seam_visible_dependency", func(t *testing.T) {
		f := newRedirectAuthFixture(t, "alice")
		root := f.newChatRoot(t, "alice")
		helper := f.newHelper(t, root, "alice")
		f.bind(t, helper)

		f.sendRedirect(t, helper, "export the CSV")

		frame := drainFrame(t, f.ch)
		require.Equal(t, string(generated.WsFrameTypeError), frame.Type,
			"the unwired seam must surface a visible error, never a faked success")
		require.Contains(t, frame.Message, "Redirect failed:",
			"a seam crossing surfaces as the seam's error surface")
		require.Contains(t, frame.Message, agent.ErrRedirectNotWired.Error(),
			"the seam's documented visible dependency must be surfaced verbatim")
	})

	// The instruction is opaque continuation text: only f.SessionId names the
	// redirect target. A foreign session id inside the instruction must never
	// alter targeting — the outcome is byte-identical to any other
	// instruction, and the named-but-unbound helper is untouched. Limitation
	// noted: while the seam is unwired this pins the frame-layer contract;
	// the record-unchanged guard below becomes load-bearing once D2 lands.
	t.Run("instruction_is_opaque_not_a_target", func(t *testing.T) {
		f := newRedirectAuthFixture(t, "alice")
		root := f.newChatRoot(t, "alice")
		helper := f.newHelper(t, root, "alice")
		unbound := f.newHelper(t, root, "alice")
		f.bind(t, helper)
		before := redirectSnapshot(t, f.lifecycle, root, helper, unbound)

		f.sendRedirect(t, helper, "continue the export, then hand off to session "+unbound)

		frame := drainFrame(t, f.ch)
		require.Equal(t, string(generated.WsFrameTypeError), frame.Type)
		require.Contains(t, frame.Message, agent.ErrRedirectNotWired.Error(),
			"an instruction mentioning another session id must reach the seam as opaque text — same visible dependency, no retargeting")
		require.True(t, f.h.connBoundToSession(f.wc, helper), "the binding must be unchanged")
		redirectRequireUnchanged(t, f.lifecycle, before)
	})
}
