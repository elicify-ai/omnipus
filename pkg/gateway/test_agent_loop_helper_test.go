//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// mustAgentLoop is a gateway test helper that calls agent.NewAgentLoop and
// fatals on error. Reduces boilerplate after the return-type change (FR-029a).
//
// Registers t.Cleanup so background goroutines (retention sweepers, idle
// tickers, recap pipeline, browser manager) are shut down before t.TempDir()
// is removed. Without this, those goroutines occasionally write to the temp
// dir during teardown and TempDir's RemoveAll fails with
// "directory not empty", causing flaky failures under -count=N or parallel
// package runs.
//

func mustAgentLoop(
	t *testing.T,
	cfg *config.Config,
	msgBus *bus.MessageBus,
	provider providers.LLMProvider,
) *agent.AgentLoop {
	t.Helper()
	al, err := agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("agent.NewAgentLoop: %v", err)
	}
	t.Cleanup(al.Close)
	return al
}
