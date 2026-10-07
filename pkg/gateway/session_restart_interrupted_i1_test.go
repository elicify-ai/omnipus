// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestRestartInterruptedRootChat_ListsAsInterrupted reproduces UAT row I1
// (train f0034f3): an ordinary root web chat with a turn in flight and only
// the user entry stored, cut off by kill -9, then the gateway boots again.
// The founder rule (2026-10-06) is that a session a restart cut off is
// interrupted — never still working. The boot sweep runs through the real
// PlanEngine.Start and the answer is read from GET /api/v1/sessions.
func TestRestartInterruptedRootChat_ListsAsInterrupted(t *testing.T) {
	api := newTestRestAPIWithPlans(t)

	// An ordinary root web chat, created through the real handler.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions",
		strings.NewReader(`{"agent_id":"`+testPlansAgentID+`","type":"chat"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/sessions"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var meta session.UnifiedMeta
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &meta))
	require.NotEmpty(t, meta.ID)

	// Only the user message was stored before the kill.
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID: "user-1", Role: "user", Content: "I1 marker. Write 400 animals.", Timestamp: time.Now().UTC(),
	}))

	// The lifecycle record exactly as the UAT home shows it after kill -9:
	// origin chat, human owner, state running with an execution identity.
	ls := session.NewLifecycleStore(t.TempDir())
	api.agentLoop.SetSessionMessagingStores(nil, ls)
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		Origin:         &session.Origin{Kind: session.OriginKindChat},
		ExecutionID:    &session.ExecutionIdentity{RunID: "run-before-kill9", BootSeq: 1},
		OwnerScopeKind: session.OwnerScopeHuman,
		AgentID:        meta.AgentID,
		WorkspaceID:    meta.WorkspaceID,
		CreatedAt:      time.Now().Add(-time.Minute),
	}))

	// Restart: the gateway boot runs the plan engine's boot sweep.
	pe := agent.NewPlanEngine(api.agentLoop, api.planStore, agent.GetTaskStore(api.agentLoop), agent.GetTaskExecutor(api.agentLoop))
	pe.SetLifecycleStore(ls)
	api.agentLoop.SetPlanEngine(pe)
	t.Cleanup(pe.Stop)
	require.NoError(t, pe.Start(context.Background()))

	page, code := u18DoListSessions(t, api, "")
	require.Equal(t, http.StatusOK, code)
	row := findSessionRow(page, meta.ID)
	require.NotNilf(t, row, "session %q must be listed", meta.ID)
	assert.Equal(t, "interrupted", row["lifecycle_state"],
		"a root chat whose turn a restart cut off must list as interrupted, not still working")
}
