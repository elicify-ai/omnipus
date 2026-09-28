// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_reasoning_turn_cancel_test.go reproduces a reasoning-trace drop: the
// reasoning-publish goroutine (loop_run_turn_response.go::
// agentLoopRunTurn.spawnReasoningPublish) is fire-and-forget, and runTurn
// cancels its own turnCtx via defer as soon as it returns. If the Go
// scheduler runs that goroutine only after the turn has already ended, the
// pre-fix code handed it the now-canceled turnCtx directly, so
// handleReasoning observed ctx.Err() != nil on entry and returned without
// publishing — the trace was simply never sent, with nothing to explain why.
// Previously only reproduced by injecting a 2s sleep into handleReasoning
// under a loaded CI run (see the dispatch this file's commit resolves); this
// test forces the same ordering deterministically, with no sleep, via the
// reasoningPublishScheduledHook seam.
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

func TestProcessMessage_ReasoningPublishedWhenGoroutineScheduledAfterTurnEnds(t *testing.T) {
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

	// Pin the reasoning-publish goroutine right at its start, before it
	// touches ctx or the bus at all.
	hookEntered := make(chan struct{})
	release := make(chan struct{})
	reasoningPublishScheduledHook = func() {
		close(hookEntered)
		<-release
	}
	t.Cleanup(func() { reasoningPublishScheduledHook = nil })

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

	// processMessage has now returned, so runTurn has already run its
	// "defer rz.rc.turnCancel()" and the turn's own context is canceled.
	// The reasoning-publish goroutine is still parked at the hook — release
	// it now, so it runs (or resumes running) strictly AFTER that
	// cancellation: exactly the "goroutine scheduled after turn end" case.
	select {
	case <-hookEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("reasoning-publish goroutine never reached the test hook")
	}
	close(release)

	deadline := time.After(10 * time.Second)
	for {
		var outbound bus.OutboundMessage
		select {
		case outbound = <-msgBus.OutboundChan():
		case <-deadline:
			t.Fatal("expected reasoning content to be published even though its goroutine ran after the turn ended")
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
