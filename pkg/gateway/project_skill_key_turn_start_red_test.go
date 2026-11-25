// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestProjectSkillKey_SlugAddedAfterMountGetsAskAtNextTurn: Skills & Tools
// C-19 key table (spec line 387): a project key is created "when Omnipus
// first reads W's project shelf (turn start or reconcile)", starting at Ask
// (ST-Q3 B) with the level name_and_description. A skill folder dropped onto
// the shelf after the mount, with no reload in between, gets its key at the
// next turn: the Skill call then raises the ordinary permission card instead
// of failing closed on a missing key.
func TestProjectSkillKey_SlugAddedAfterMountGetsAskAtNextTurn(t *testing.T) {
	h, _ := newNativeD10Harness(t, "allow", "allow")
	file := seedNativeD10Project(t, h, nativeSkillR1)

	// The C-19 upgrade is done, as on every upgraded install.
	configPath := filepath.Join(h.api.homePath, "config.json")
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	sandbox, ok := document["sandbox"].(map[string]any)
	require.True(t, ok)
	sandbox["migrations"] = map[string]string{"skill_permissions": "done"}
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, raw, 0o600))

	// A skill folder dropped onto the mounted shelf after the mount.
	lateDir := filepath.Join(filepath.Dir(filepath.Dir(file)), "late-notes")
	require.NoError(t, os.MkdirAll(lateDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(lateDir, "SKILL.md"),
		[]byte("---\nname: late-notes\ndescription: Notes dropped onto the shelf later\n---\n\nLATE-NOTES-INSTRUCTIONS-3P\n"), 0o600))
	const key = "project:d10/late-notes"
	_, stored := h.api.agentLoop.GetConfig().Sandbox.SkillPolicies[key]
	require.False(t, stored, "precondition: the late slug has no key before the turn")

	h.script.args = map[string]any{"name": "late-notes"}
	run := beginNativeD10(t, h, "load", "chat", "test-agent", "project")
	entry := h.pending(t, run)
	snap := h.handler.pendingApprovalSnapshot(entry)
	require.NotNil(t, snap.Skill, "the load raises the skill's own card")
	assert.Equal(t, key, snap.Skill.Key)

	raw, err = os.ReadFile(configPath)
	require.NoError(t, err)
	var onDisk struct {
		Sandbox struct {
			SkillPolicies   map[string]string `json:"skill_policies"`
			SkillVisibility map[string]string `json:"skill_visibility"`
		} `json:"sandbox"`
	}
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	assert.Equal(t, "ask", onDisk.Sandbox.SkillPolicies[key], "C-19: the new key is stored at Ask")
	assert.Equal(t, config.VisibilityNameAndDescription, onDisk.Sandbox.SkillVisibility[key])
	assert.Equal(t, "ask", h.api.agentLoop.GetConfig().Sandbox.SkillPolicies[key], "the live config holds the stored key")

	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	select {
	case err := <-run.done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not finish after the card was answered")
	}
}
