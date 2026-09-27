// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
)

// ADR-092 review finding B: the SPA's buildAgentUpdate refuses to send any
// field the agent's editable_fields does not mark editable, so the per-agent
// "Never auto-approve" switch could never be saved from the UI. The operator
// view must list auto_approve_disabled as editable, and a PUT carrying it
// must persist and read back.
func TestAgentUpdate_AutoApproveDisabledEditableAndPersisted(t *testing.T) {
	api, _ := newShellModeAuditAPI(t)

	w, before := getAgent(t, api, "agent-a")
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, before.EditableFields)
	var found bool
	for _, d := range *before.EditableFields {
		if d.Name == "auto_approve_disabled" {
			found = true
			assert.True(t, d.Editable, "the operator may set the per-agent Never auto-approve switch; reason=%v", d.Reason)
		}
	}
	require.True(t, found, "editable_fields must describe auto_approve_disabled, or the SPA refuses to send it")

	put := putAgentJSON(t, api, "agent-a", `{"auto_approve_disabled":true}`)
	require.Equal(t, http.StatusOK, put.Code, "body: %s", put.Body.String())

	w, after := getAgent(t, api, "agent-a")
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, after.AutoApproveDisabled)
	assert.True(t, *after.AutoApproveDisabled, "GET must read back the saved switch")

	onDisk, err := agentstore.New(api.homePath).Get("agent-a")
	require.NoError(t, err)
	assert.True(t, onDisk.AutoApproveDisabled, "the switch must be persisted to the agent's entity file")

	// And back off again: the operator may clear it (that only returns the
	// agent to the global default, never past it).
	put = putAgentJSON(t, api, "agent-a", `{"auto_approve_disabled":false}`)
	require.Equal(t, http.StatusOK, put.Code, "body: %s", put.Body.String())
	onDisk, err = agentstore.New(api.homePath).Get("agent-a")
	require.NoError(t, err)
	assert.False(t, onDisk.AutoApproveDisabled)
}
