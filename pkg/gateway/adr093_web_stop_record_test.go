// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 test plan, MAJ-001 characterisation: web Stop on a chat root that
// has delegated leaves the root record running, with a Stop marker for the
// current generation, and does not write state "cancelled".
//
// Oracle: docs/internal/architecture/ADR-093-open-conversation-must-keep-
// delegation.md, test-plan row "Web Stop on a chat root that has delegated."
// The expected state is that row, not whatever handleCancel does today.

package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// adr093IdleProvider is present only because constructing an agent loop
// requires one. This test never runs a turn.
type adr093IdleProvider struct{}

func (adr093IdleProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "unused"}, nil
}

func (adr093IdleProvider) GetDefaultModel() string { return "adr093-idle" }

// TestAdr093WebStop_DelegatedChatStaysRunningWithCurrentStop drives the web
// Stop button (handleCancel) on a chat root that already has a child. The
// root stays running and carries a Stop for its current generation.
func TestAdr093WebStop_DelegatedChatStaysRunningWithCurrentStop(t *testing.T) {
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
	al := mustAgentLoop(t, cfg, msgBus, adr093IdleProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)

	// The production canceller, including the live-turn callback web Stop
	// installs at boot (gateway_boot.go::wireSteerDeps). A canceller without
	// that callback would only stamp and would hide a cancelled write.
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	const workspaceID = "ws-adr093-web-stop"
	if setMetaErr := al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: strPtr(workspaceID)}); setMetaErr != nil {
		t.Fatalf("SetMeta workspace: %v", setMetaErr)
	}
	parent := &session.LifecycleRecord{
		SessionID:      meta.ID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    workspaceID,
		AgentID:        "mia",
		Origin:         &session.Origin{Kind: session.OriginKindChat},
	}
	if persistErr := lifecycle.Persist(parent); persistErr != nil {
		t.Fatalf("persist parent: %v", persistErr)
	}
	if _, launchErr := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: meta.ID,
		TargetAgentID:     "mia",
		Task:              "Prepare the spreadsheet",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	}); launchErr != nil {
		t.Fatalf("delegate from the chat (the root must already have a child): %v", launchErr)
	}

	h := makeMinimalHandler()
	h.agentLoop = al
	h.msgBus = msgBus
	wc, _ := makeForwarderTestConn(32)
	wc.userID = "user-adr093"
	h.handleCancel(wc, meta.ID)

	got, err := lifecycle.Load(meta.ID)
	if err != nil {
		t.Fatalf("Load parent after web Stop: %v", err)
	}
	if got.State != session.LifecycleRunning {
		t.Fatalf("parent state after web Stop = %q, want running — ADR-093 test plan: web Stop on a delegated chat root does not cancel the record", got.State)
	}
	if got.Stop == nil || got.Stop.Generation != got.Generation {
		t.Fatalf("parent Stop after web Stop = %+v, generation %d — want a Stop marker for the current generation", got.Stop, got.Generation)
	}
	// The delegation itself must still be on record: this is the "has
	// delegated" row, not a Stop on a chat that never launched anything.
	records, err := lifecycle.List(session.LifecycleFilter{SteeringSessionID: meta.ID})
	if err != nil {
		t.Fatalf("List children: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("children of the chat after web Stop = %d, want 1 (the root had delegated)", len(records))
	}
}
