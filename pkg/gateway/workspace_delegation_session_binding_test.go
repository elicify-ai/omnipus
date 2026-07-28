// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for a MEDIUM-severity UAT bug: the Team tab's delegation-
// edge graph editor showed "Saved just now" for a freshly-wired jim->worker
// edge, but a live delegation attempt through Jim was rejected with
// delegation_denied/trust_set, as if the edge never existed.
//
// Root cause: NOT the REST write (it genuinely persists — see
// TestWorkspaceDelegation_EdgeWiredViaTeamTabPersistsForLiveSession below), and
// NOT the runtime enforcement gate (buildDelegationDenyChecker /
// findDelegationEdge in pkg/agent/loop.go already read the workspace's
// Delegation[] edges fresh from disk on every call — see
// pkg/agent/delegation_wiring_test.go's TestDelegationGraphFlipsWithoutRebuild
// and worker_delegation_test.go's TestSeededGraph_BaseToWorkerAllowed).
//
// The actual break was one layer up, in how a chat session's WORKSPACE BINDING
// is chosen: handleChatMessage (websocket.go) stamped session.MetaPatch{
// WorkspaceID} on a session's meta ONLY the first time a non-empty workspace_id
// arrived ("the first binding wins"), even though the SPA's chat.ts sendMessage
// resends the CURRENTLY active workspace_id on every single outbound frame.
// pkg/agent/loop.go's per-turn workspace resolution (workspaceID :=
// meta.WorkspaceID, feeding resolveEffectiveWorkspaceID -> findDelegationEdge)
// reads that session meta FRESH every turn — so the bug was never a cache in
// the traditional sense, it was a write-side "sticky first bind" that silently
// discarded every later message's live workspace_id. An ongoing chat session
// therefore kept enforcing (and advertising) delegation against whichever
// workspace it was bound to on its FIRST message, no matter which workspace's
// Team tab the operator had since edited — exactly matching the reported
// symptom ("a new session would have worked, this one didn't").
//
// Fixed in websocket.go's handleChatMessage: the session's WorkspaceID now
// tracks the live value on every message (rebinds whenever the incoming,
// non-empty workspace_id differs from what's currently stored), while still
// never blanking an existing binding when a later message carries none at all.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// writeDelegationTestWorkspaceRecord writes a minimal on-disk workspace record
// so workspace.Exists / handleChatMessage's binding validation observe it under
// home. Mirrors m4_workspace_validation_test.go's writeWorkspaceRecord (kept
// local to avoid a cross-file rename of that shared helper).
func writeDelegationTestWorkspaceRecord(t *testing.T, home, id string, isDefault bool) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	rec := map[string]any{"id": id, "is_default": isDefault}
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644))
}

// TestWorkspaceDelegation_EdgeWiredViaTeamTabPersistsForLiveSession is the
// task's requested end-to-end regression: it drives the EXACT two-PUT sequence
// WorkspaceTeamTab.tsx's saveFn performs (core_team PUT, then the delegation
// edges PUT) — i.e. the same path the Team-tab UI's "Saved just now" confirms —
// then, in the SAME chat session (no reload, no new session), confirms the
// jim->worker edge is immediately visible to workspace.ReadDelegation: the
// exact call findDelegationEdge (pkg/agent/loop.go) makes to decide the
// delegation_denied/trust_set outcome. This proves the write path is NOT a
// false positive and the read path is NOT stale for an in-workspace session.
func TestWorkspaceDelegation_EdgeWiredViaTeamTabPersistsForLiveSession(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)

	// "worker" exists in the config roster (mirrors coreagent.IDWorker, the
	// seeded general-purpose worker) but starts OFF this workspace's team —
	// matching TestDefaultWorkspaceSeeder_TeamAndEdges: the default workspace
	// seeder deliberately drops every ->worker edge because "worker" is not
	// part of the default team roster, so wiring it is always a manual,
	// UI-driven Team-tab edit.
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(
		cfg.Agents.List,
		config.AgentConfig{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
	)

	// Step 1 (mirrors updateWorkspace(workspaceId, {core_team: members})):
	// add "worker" to the team BEFORE the edge, exactly as WorkspaceTeamTab's
	// saveFn orders its two PUTs.
	wUp := httptest.NewRecorder()
	rUp := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id,
		strings.NewReader(`{"core_team":["jim","ava","ray","planner","worker"]}`))
	rUp.Header.Set("Content-Type", "application/json")
	rUp.URL.Path = "/api/v1/workspaces/" + id
	api.HandleWorkspaces(wUp, rUp)
	require.Equal(t, http.StatusOK, wUp.Code, "core_team PUT: body=%s", wUp.Body.String())

	// Step 2 (mirrors updateWorkspaceDelegation(workspaceId, edges)): wire the
	// jim->worker delegation edge the Team-tab graph editor draws.
	w := putDelegation(t, api, id,
		`{"edges":[{"from_agent":"jim","to_agent":"worker","modes":["direct","task"]}]}`)
	require.Equal(t, http.StatusOK, w.Code, "delegation PUT: body=%s", w.Body.String())

	// Immediately (same test "session" — no reload, no wait) read the edge back
	// via the SAME function the runtime delegation gate calls.
	edges, err := workspace.ReadDelegation(api.homePath, id)
	require.NoError(t, err)
	found := false
	for _, e := range edges {
		if e.FromAgent == "jim" && e.ToAgent == "worker" {
			found = true
		}
	}
	assert.True(t, found,
		"jim->worker must be immediately readable via workspace.ReadDelegation right after the Team-tab save — "+
			"this is the exact call findDelegationEdge makes to authorize a live delegation attempt")
}

// TestWorkspaceDelegation_SessionRebindsToCurrentWorkspace is the root-cause
// regression: a chat session that was FIRST bound to workspace A must adopt
// workspace B once the operator continues the SAME session (no new
// session_id) after switching their active workspace to B — e.g. because they
// went to B's Team tab, wired a delegation edge, and returned to the same
// chat thread. Before the fix, handleChatMessage's "first binding wins" rule
// left the session stuck on A forever, so a delegation gate consulting that
// session's bound workspace would enforce/advertise against A's graph even
// though the operator's edit (and current attention) was on B.
func TestWorkspaceDelegation_SessionRebindsToCurrentWorkspace(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	const wsA = "01JXWORKSPACEAAAAAAAAAAAAAA1"
	const wsB = "01JXWORKSPACEBBBBBBBBBBBBBB2"
	writeDelegationTestWorkspaceRecord(t, home, wsA, true)
	writeDelegationTestWorkspaceRecord(t, home, wsB, false)

	wc := makeTestConn()

	// Message 1: mint a session while workspace A is active.
	handler.handleChatMessage(context.Background(), "chat-rebind", "", "first", "", nil, "", wsA, false, wc)
	var sessionID string
	select {
	case msg := <-msgBus.InboundChan():
		sessionID = msg.SessionID
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first bus.InboundMessage")
	}
	require.NotEmpty(t, sessionID, "handleChatMessage must mint a session")

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)
	meta1, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	require.Equal(t, wsA, meta1.WorkspaceID, "session must bind to A on its first message")

	// Message 2: SAME session — the operator has since switched their active
	// workspace to B (e.g. to wire a delegation edge on B's Team tab) and
	// continues the same chat thread rather than starting a new one.
	handler.handleChatMessage(context.Background(), "chat-rebind", sessionID, "second", "", nil, "", wsB, false, wc)
	select {
	case <-msgBus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second bus.InboundMessage")
	}

	meta2, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, wsB, meta2.WorkspaceID,
		"the session must track the LIVE active workspace (B) rather than staying stuck on its first "+
			"binding (A) — pkg/agent/loop.go reads this exact field fresh every turn to resolve the "+
			"delegation graph, so a stale binding here silently strands delegation edges wired on the "+
			"new workspace")
}

// TestWorkspaceDelegation_SessionRebind_IgnoresAbsentWorkspaceID proves the fix
// is not a blanket "always overwrite": a later message that carries NO
// workspace_id at all (e.g. a plain non-workspace-scoped chat message, or a
// channel message with no concept of "active workspace") must NOT blank out an
// existing binding.
func TestWorkspaceDelegation_SessionRebind_IgnoresAbsentWorkspaceID(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	home := t.TempDir()
	handler.home = home

	const wsA = "01JXWORKSPACEAAAAAAAAAAAAAA3"
	writeDelegationTestWorkspaceRecord(t, home, wsA, true)

	wc := makeTestConn()

	handler.handleChatMessage(context.Background(), "chat-keep", "", "first", "", nil, "", wsA, false, wc)
	var sessionID string
	select {
	case msg := <-msgBus.InboundChan():
		sessionID = msg.SessionID
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first bus.InboundMessage")
	}
	require.NotEmpty(t, sessionID)

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)

	// Message 2: same session, but this frame carries no workspace_id.
	handler.handleChatMessage(context.Background(), "chat-keep", sessionID, "second", "", nil, "", "", false, wc)
	select {
	case <-msgBus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second bus.InboundMessage")
	}

	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(
		t,
		wsA,
		meta.WorkspaceID,
		"an absent workspace_id on a later message must not clear the existing binding",
	)
}
