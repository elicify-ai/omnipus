// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// working_dir_e2e_test.go closes the gap a review pass flagged: prior tests
// verify FindForAgentPreferring's tie-break logic in isolation (pkg/workspace)
// and the "## Working Directory" prompt block in isolation (buildDynamicContext,
// working_dir_wiring_test.go), and a pre-existing test verifies real write_file
// placement for the SINGLE-membership case (workspace_team_reroot_test.go) —
// but nothing drives one real turn, with the agent belonging to TWO
// workspaces and a genuine channel-bound workspace_id, and asserts BOTH the
// advertised prompt block AND the actual write_file placement agree. This is
// the one test that would catch the two call sites (loop.go's re-rooting,
// loop_env.go's injector) drifting apart in practice, not just in theory.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestRunTurn_MultiMembership_AdvertisementMatchesEnforcement proves that for
// a real, channel-bound turn, the agent belonging to two workspaces' CoreTeam
// gets told about (and actually writes to) the SAME one — the turn's own
// channel-bound workspace — not an arbitrary sorted-first pick.
func TestRunTurn_MultiMembership_AdvertisementMatchesEnforcement(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	// "ws-a-current" sorts before "ws-z-other", so a plain FindForAgent (no
	// preference) would pick "ws-a-current" regardless of which workspace this
	// turn is actually bound to — making this test load-bearing only if the
	// bound instance's workspace ("ws-z-other") is the one that actually wins.
	workspacesDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(workspacesDir, 0o755))
	writeWSRecord := func(id, name string) {
		rec := map[string]any{"id": id, "core_team": []string{"main"}, "name": name}
		data, err := json.Marshal(rec)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, id+".json"), data, 0o644))
	}
	writeWSRecord("ws-a-current", "Workspace A")
	writeWSRecord("ws-z-other", "Workspace Z")

	// processMessage resolves processOptions.WorkspaceID from the transcript
	// session's OWN stored meta.WorkspaceID first, falling back to
	// msg.Metadata["workspace_id"] for a session with no stored meta yet (see
	// loop.go's processMessage, "M4: bind the active workspace into this
	// turn"). Setting it via inbound metadata is the simplest way to give a
	// FRESH turn a genuine, non-empty ts.opts.WorkspaceID — the exact signal
	// FindForAgentPreferring uses to break the tie.
	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"tie-broken-write","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentWorkspaceDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			// "main" here is an ordinary, explicitly-registered agent id — no
			// implicit sentinel anymore — chosen to match the pre-existing
			// agentWorkspaceDir path and core_team JSON literals above.
			List: []config.AgentConfig{{ID: "main", Home: agentWorkspaceDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): write_file needs
	// an explicit agent-level grant, or it fails closed to "deny" and the
	// multi-membership tie-break under test never gets exercised.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	msg := bus.InboundMessage{
		Channel:    "webchat",
		ChatID:     "chat-1",
		Content:    "please write proof.txt for me",
		SessionKey: "test-session-multi-membership",
		Metadata:   map[string]string{"workspace_id": "ws-z-other"},
	}

	ctx := context.Background()
	reply, _, err := al.processMessage(ctx, msg)
	require.NoError(t, err, "processMessage must succeed")
	assert.Equal(t, "done", reply)

	// --- Enforcement: the file must land under the BOUND workspace's work/ dir ---
	zFile := filepath.Join(workspacesDir, "ws-z-other", "work", "proof.txt")
	got, readErr := os.ReadFile(zFile)
	require.NoError(
		t,
		readErr,
		"write_file must land under the channel-bound workspace (ws-z-other), got err reading %s",
		zFile,
	)
	assert.Equal(t, "tie-broken-write", string(got))

	aFile := filepath.Join(workspacesDir, "ws-a-current", "work", "proof.txt")
	_, aStatErr := os.Stat(aFile)
	assert.True(t, os.IsNotExist(aStatErr),
		"write_file must NOT land under the OTHER (non-bound) workspace (%s) it also belongs to", aFile)

	// --- Advertisement: the prompt sent to the LLM must name the SAME workspace ---
	msgs := provider.LastMessages()
	require.NotEmpty(t, msgs, "expected at least one message sent to the provider")
	systemPrompt := msgs[0].Content
	assert.Contains(t, systemPrompt, "## Working Directory")
	assert.Contains(t, systemPrompt, "Workspace Z", "prompt must name the channel-bound workspace")
	assert.NotContains(t, systemPrompt, "Workspace A", "prompt must NOT name the other, non-bound workspace")
}
