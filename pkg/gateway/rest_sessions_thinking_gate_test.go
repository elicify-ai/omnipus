// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for gate Boundary 3 - the REST transcript serializers
// (spec section 8.1 row for pkg/gateway/rest_sessions.go: "Gated strip -
// thinking entries and fields stripped unless the requesting principal's
// toggle is on"; FR-002; section 16 item 10; the machine-verifiable
// constraint "REST keys absent, not null").
//
// The pinned production symbols are pkg/gateway/rest_sessions.go::getSession
// and ::getSessionMessages, gated by
// pkg/gateway/thinking_gate.go::thinkingVisible against the requesting
// principal in ctxkey.UserContextKey / ctxkey.CLITokenContextKey.
//
// Oracles:
//   - toggle ON: the thinking entry is in the response with its
//     thinking_text (the redacted copy).
//   - toggle OFF: the entry is ABSENT - zero thinking keys anywhere in the
//     body (absent, never null); the rest of the transcript is untouched.
//   - CLI-token principal: hidden even with a real account named "cli"
//     whose row is ON (dataset row 3) - the request context carries
//     ctxkey.CLITokenContextKey, exactly as checkBearerAuth sets it.
//
// RED status: compile-fail on session.EntryTypeThinking/ThinkingText (the
// fixture's stored rows) and on config.UserConfig.ShowThinking.

func wpcSeedGatedSession(t *testing.T, api *restAPI) string {
	t.Helper()
	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared)
	meta, err := shared.NewSession(session.SessionTypeChat, "webchat", "01JXTESTAGENTSTARTTEST001")
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, shared.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Role: "user", Content: "show me the thinking turn", Timestamp: now,
	}))
	require.NoError(t, shared.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID:              "wpc-rest-think-1",
		Type:            session.EntryTypeThinking,
		ThinkingText:    "rest gate probe " + thinkingSentinelGateway + " tail",
		TurnID:          "turn-1",
		ElapsedMS:       800,
		ThinkingTokens:  33,
		ProviderSummary: true,
		Timestamp:       now,
	}))
	require.NoError(t, shared.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Role: "assistant", Content: "the answer", TurnID: "turn-1", Timestamp: now,
	}))
	return meta.ID
}

// wpcGatedRequest builds a GET messages request whose context carries the
// named principal, the same way the auth middleware injects identity.
func wpcGatedRequest(target string, showThinking bool, cliToken bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := r.Context()
	ctx = context.WithValue(ctx, ctxkey.UserContextKey{}, &config.UserConfig{
		Username:     "cli",
		ShowThinking: showThinking,
	})
	if cliToken {
		ctx = context.WithValue(ctx, ctxkey.CLITokenContextKey{}, true)
	}
	return r.WithContext(ctx)
}

func wpcCallMessages(t *testing.T, api *restAPI, sessionID string, showThinking, cliToken bool) string {
	t.Helper()
	w := httptest.NewRecorder()
	api.getSessionMessages(w, wpcGatedRequest("/api/v1/sessions/"+sessionID+"/messages", showThinking, cliToken), sessionID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	return w.Body.String()
}

func wpcCallDetail(t *testing.T, api *restAPI, sessionID string, showThinking, cliToken bool) string {
	t.Helper()
	w := httptest.NewRecorder()
	api.getSession(w, wpcGatedRequest("/api/v1/sessions/"+sessionID, showThinking, cliToken), sessionID)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	return w.Body.String()
}

func TestGetSessionMessages_ThinkingGate(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	sessionID := wpcSeedGatedSession(t, api)

	t.Run("toggle on sees the thinking entry", func(t *testing.T) {
		body := wpcCallMessages(t, api, sessionID, true, false)
		assert.Contains(t, body, "wpc-rest-think-1", "gate on: the thinking entry rides the response")
		assert.Contains(t, body, "rest gate probe", "gate on: the redacted thinking_text is present")
		assert.NotContains(t, body, thinkingSentinelGateway, "the stored copy is the redacted one")
	})

	t.Run("toggle off strips the entry - keys absent not null", func(t *testing.T) {
		body := wpcCallMessages(t, api, sessionID, false, false)
		assert.NotContains(t, body, "wpc-rest-think-1",
			"gate off: the entry's ABSENCE is the gate at REST (Boundary 3)")
		assert.NotContains(t, body, "rest gate probe", "gate off: no thinking text")
		assert.NotContains(t, body, "thinking_text", "gate off: zero thinking keys - absent, never null")
		assert.NotContains(t, body, thinkingSentinelGateway, "gate off: zero thinking bytes")
		assert.Contains(t, body, "the answer", "the rest of the transcript is untouched")
	})

	t.Run("cli token principal is hidden even with a real cli row on", func(t *testing.T) {
		body := wpcCallMessages(t, api, sessionID, true, true)
		assert.NotContains(t, body, "wpc-rest-think-1",
			"dataset row 3: the gate keys on the auth method, never the username string")
		assert.NotContains(t, body, "thinking_text", "cli token: zero thinking keys")
	})
}

func TestGetSession_ThinkingGate(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	sessionID := wpcSeedGatedSession(t, api)

	t.Run("toggle off strips thinking from the detail payload", func(t *testing.T) {
		body := wpcCallDetail(t, api, sessionID, false, false)
		assert.NotContains(t, body, "wpc-rest-think-1", "gate off: no thinking entry in the detail")
		assert.NotContains(t, body, "thinking_text", "gate off: zero thinking keys")
	})

	t.Run("toggle on includes thinking in the detail payload", func(t *testing.T) {
		body := wpcCallDetail(t, api, sessionID, true, false)
		assert.Contains(t, body, "wpc-rest-think-1", "gate on: the thinking entry is present")
	})
}
