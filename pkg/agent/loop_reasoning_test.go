// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Effort-levels FR-017 retires the reasoning-channel path. Preserve this
// test's delivery check at the real worker boundary: the ordinary answer must
// reach the chat, and private reasoning must reach no outbound destination.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestProcessMessage_PublishesOnlyOrdinaryReplyWhenReasoningIsPresent(t *testing.T) {
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
	t.Cleanup(msgBus.Close)
	provider := &reasoningContentProvider{
		response:         "final answer",
		reasoningContent: "thinking trace",
	}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	chManager, err := channels.NewManager(&config.Config{}, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("Failed to create channel manager: %v", err)
	}
	chManager.RegisterChannel("telegram", &fakeChannel{id: "reason-chat"})
	al.SetChannelManager(chManager)

	worker := newSessionWorker("agent:mia:telegram:chat1", al, func() {})
	t.Cleanup(worker.cancel)
	worker.processTurn(context.Background(), bus.InboundMessage{
		Channel: "telegram",
		Sender:  bus.SenderInfo{CanonicalID: "user1"},
		ChatID:  "chat1", Content: "hello",
		Metadata: map[string]string{"agent_id": "mia"},
	})

	ordinaryReplies := 0
	check := func(outbound bus.OutboundMessage) {
		if strings.Contains(outbound.Content, "thinking trace") {
			t.Errorf("private reasoning reached outbound %s/%s: %q", outbound.Channel, outbound.ChatID, outbound.Content)
		}
		if outbound.Channel == "telegram" && outbound.ChatID == "chat1" {
			ordinaryReplies++
			if outbound.Content != "final answer" {
				t.Errorf("ordinary reply = %q, want exactly %q", outbound.Content, "final answer")
			}
		}
	}
	deadline := time.After(10 * time.Second)
	for ordinaryReplies == 0 {
		select {
		case outbound := <-msgBus.OutboundChan():
			check(outbound)
		case <-deadline:
			t.Fatal("expected the ordinary final answer on telegram/chat1")
		}
	}
	// processTurn has returned: inspect every remaining frame, not only a
	// frame selected by destination, so a separate reasoning leak also fails.
	for {
		select {
		case outbound := <-msgBus.OutboundChan():
			check(outbound)
		default:
			if ordinaryReplies != 1 {
				t.Fatalf("ordinary replies = %d, want exactly 1", ordinaryReplies)
			}
			return
		}
	}
}
