// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_reasoning_test.go covers the reasoning-channel publish path: a turn that
// produces reasoning content must publish it to the session's reasoning channel
// rather than the chat channel. Extracted from loop_test.go, which sits at its
// grandfathered size budget (scripts/budgets/files.txt) and may only shrink.
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestProcessMessage_PublishesReasoningContentToReasoningChannel(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &reasoningContentProvider{
		response:         "final answer",
		reasoningContent: "thinking trace",
	}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	chManager, err := channels.NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("Failed to create channel manager: %v", err)
	}
	chManager.RegisterChannel("telegram", &fakeChannel{id: "reason-chat"})
	al.SetChannelManager(chManager)

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "telegram",
		Sender: bus.SenderInfo{
			CanonicalID: "user1",
		},
		ChatID:  "chat1",
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "final answer" {
		t.Fatalf("processMessage() response = %q, want %q", response, "final answer")
	}

	// Two faults lived in this wait. It asserted on whatever arrived FIRST, so any
	// unrelated outbound publish ahead of the reasoning one failed the test with a
	// misleading "reasoning channel = ..." message about the wrong frame. And its 3s
	// budget is the short end of this package's range (2s-30s; 10s is used 24 times):
	// it expired at 3.03s on CI run for release 7df13b436 while the publish itself was
	// working -- the same test is green on fadf741ea and the only files that changed
	// between those tips are two TEST files, neither in pkg/agent, so nothing could
	// have stopped the publish happening. Late, not absent.
	//
	// Drain until the reasoning frame arrives, identified by its own chat id, and keep
	// every assertion on the matched frame. A reasoning publish that never happens
	// still fails; an unrelated frame no longer can.
	deadline := time.After(10 * time.Second)
	for {
		var outbound bus.OutboundMessage
		select {
		case outbound = <-msgBus.OutboundChan():
		case <-deadline:
			t.Fatal("expected reasoning content to be published to reasoning channel")
		}
		if outbound.ChatID != "reason-chat" {
			continue
		}
		if outbound.Channel != "telegram" {
			t.Fatalf("reasoning channel = %q, want %q", outbound.Channel, "telegram")
		}
		if outbound.Content != "thinking trace" {
			t.Fatalf("reasoning content = %q, want %q", outbound.Content, "thinking trace")
		}
		return
	}
}
