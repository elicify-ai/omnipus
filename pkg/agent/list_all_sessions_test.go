// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// TestListAllSessions_PartialErrors verifies that ListAllSessions returns the
// sessions it could read along with per-agent errors for agents whose store is
// broken, rather than swallowing errors silently or failing entirely.
//
// BDD: Given two agents — one with a valid session store, one whose base
//
//	directory cannot be listed (permissions revoked) —
//	When ListAllSessions is called,
//	Then the returned slice contains the valid agent's session,
//	And the error slice contains exactly one error for the broken agent.
func TestListAllSessions_PartialErrors(t *testing.T) {
	if os.Getuid() == 0 {
		// Root bypasses DAC (Discretionary Access Control), so chmod 0o000 on
		// a directory does not prevent os.ReadDir from root. The failure-injection
		// this test relies on only works for non-privileged users.
		t.Skip("permission-based failure injection is ineffective under root; run as non-root")
	}
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{
					ID:        "agent-good",
					Name:      "Good Agent",
					Workspace: tmpDir,
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	// Wire a valid UnifiedStore for the "main" agent and create one session.
	goodStore, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUnifiedStore(main): %v", err)
	}
	if _, sessErr := goodStore.NewSession(session.SessionTypeChat, "webchat", "main"); sessErr != nil {
		t.Fatalf("NewSession: %v", sessErr)
	}
	mainAgent, ok := al.GetRegistry().GetAgent("main")
	if !ok {
		t.Fatal("main agent not found in registry")
	}
	mainAgent.Sessions = goodStore

	// Wire a broken UnifiedStore for "agent-good": create the store then remove
	// its base directory so ListSessions fails.
	brokenBaseDir := t.TempDir()
	brokenStore, err := session.NewUnifiedStore(brokenBaseDir)
	if err != nil {
		t.Fatalf("NewUnifiedStore(agent-good): %v", err)
	}
	// Remove all read permission on the base dir so os.ReadDir fails.
	if err := os.Chmod(brokenBaseDir, 0o000); err != nil {
		t.Fatalf("chmod brokenBaseDir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(brokenBaseDir, 0o700) }) // restore for temp-dir cleanup

	goodAgent, ok := al.GetRegistry().GetAgent("agent-good")
	if !ok {
		t.Fatal("agent-good not found in registry")
	}
	goodAgent.Sessions = brokenStore

	// Call ListAllSessions; expect one session (from main) and one error (from agent-good).
	metas, errs := al.ListAllSessions()

	if len(metas) != 1 {
		t.Errorf("expected 1 session from good store, got %d", len(metas))
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 partial error from broken store, got %d", len(errs))
	}
	if len(errs) > 0 {
		errMsg := errs[0].Error()
		if errMsg == "" {
			t.Error("partial error message must not be empty")
		}
	}
}
