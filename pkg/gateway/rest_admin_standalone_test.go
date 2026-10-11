// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Navigation FR-038 / "one main per agent per workspace": Admin lives in the
// DEFAULT workspace. The old /admin/chat route minted a standalone, workspace-less
// Admin session on every visit; the server no longer accepts one.

func adminSessionCount(t *testing.T, env *u1Env) int {
	t.Helper()
	metas, err := env.store(t).ListSessions()
	require.NoError(t, err)
	n := 0
	for _, m := range metas {
		if m.Type != session.SessionTypeMain && len(m.AgentIDs) > 0 && m.AgentIDs[0] == "admin" {
			n++
		}
	}
	return n
}

func TestAdminStandalone_RESTCreateWithoutWorkspaceIsRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, "01JADMINSTANDALONEWS000001", true, "mia")

	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions", `{"type":"chat","agent_id":"admin"}`)

	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Contains(t, body, "default workspace")
	assert.Zero(t, adminSessionCount(t, env), "no standalone Admin session may be minted")
}

func TestAdminStandalone_RESTCreateInANonDefaultWorkspaceIsRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, "01JADMINSTANDALONEWS000001", true, "mia")
	env.seedWorkspace(t, "01JADMINSTANDALONEWS000002", false, "mia")

	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions",
		`{"type":"chat","agent_id":"admin","workspace_id":"01JADMINSTANDALONEWS000002"}`)

	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Zero(t, adminSessionCount(t, env))
}

// Controls: an Admin chat INSIDE the default workspace, and any other agent's
// workspace-less chat, are still created.
func TestAdminStandalone_ControlsStillCreate(t *testing.T) {
	env := u1NewEnv(t, false)
	const def = "01JADMINSTANDALONEWS000001"
	env.seedWorkspace(t, def, true, "mia")

	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions",
		`{"type":"chat","agent_id":"admin","workspace_id":"`+def+`"}`)
	require.Equal(t, http.StatusCreated, code, body)
	var s gen.Session
	require.NoError(t, json.Unmarshal([]byte(body), &s))
	require.NotNil(t, s.WorkspaceId)
	assert.Equal(t, def, *s.WorkspaceId)

	code, body = sf1Do(env, http.MethodPost, "/api/v1/sessions", `{"type":"chat","agent_id":"mia"}`)
	assert.Equal(t, http.StatusCreated, code, "another agent's workspace-less chat is unchanged: %s", body)
}

func TestAdminStandalone_WebSocketFirstMessageWithoutWorkspaceIsRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, "01JADMINSTANDALONEWS000001", true, "mia")
	h := newWSHandler(bus.NewMessageBus(), env.api.agentLoop, "")
	t.Cleanup(h.Wait)
	wc := makeTestConn()

	h.handleChatMessage(context.Background(), "chat-admin", "", "hello admin", "admin", nil, "", "", false, wc)

	frames := drainConnFrames(wc)
	assert.Contains(t, frames, `"type":"error"`, frames)
	assert.True(t, strings.Contains(frames, "default workspace"), frames)
	assert.Zero(t, adminSessionCount(t, env), "no standalone Admin session may be minted over the WebSocket either")
}
