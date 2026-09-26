// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

// loop_mcp_header_refs_test.go — issue #638's connect-time half, driven
// through the production reconciliation path (loop_mcp.go::reconcileLocked,
// reached via AgentLoop.ReconcileMCP): a server whose HeaderRefs cannot be
// resolved (no credential resolver wired) must be SKIPPED with a recorded,
// diagnosable connect error — never connected without the configured secret,
// and never silently dropped. Mirrors the fail-closed shape of
// TestReconcileMCP_UnconnectableStdioServer.
//
// Red proof: the surviving CHECK mutant (ResolveServerHeaderRefs made to
// return cfg, nil immediately) turns the recorded error into a connect
// failure message instead — the errMsg-content assertions below name the
// exact expected-vs-actual difference; the mutation probes that demonstrated
// this are recorded in the delivery report.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestReconcileMCP_HeaderRefs_NilResolver_FailClosedSkip pins issue #638's
// fail-closed reconcile semantics: HeaderRefs present + no credential
// resolver wired → the server is skipped with a recorded connect error whose
// message names the missing resolver, and no live connection exists.
//
// BDD:
//
//	Given an enabled http MCP server carrying HeaderRefs and NO credential
//	  resolver wired on the agent loop
//	When  ReconcileMCP runs
//	Then  MCPServerStatus reports "error" with an errMsg naming the missing
//	  credential resolver, the live manager holds no connection for the
//	  server, and initialization still completes (a per-server credential
//	  failure must not block the loop).
func TestReconcileMCP_HeaderRefs_NilResolver_FailClosedSkip(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Tools.MCP.Enabled = true // global kill-switch: must be on for the server to be in `desired`
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"hdr-srv": {
			Enabled: true,
			Type:    "http",
			URL:     "http://127.0.0.1:1/mcp",
			HeaderRefs: map[string]string{
				"Authorization": "mcp_hdr-srv_header_Authorization",
			},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP returned a systemic error for a per-server credential failure: %v", err)
	}

	status, toolCount, errMsg := al.MCPServerStatus("hdr-srv")
	if status != "error" {
		t.Errorf("status = %q, want %q (the unresolvable server must be recorded, not silently skipped)", status, "error")
	}
	if !strings.Contains(errMsg, "no credential resolver is configured") {
		t.Errorf("errMsg = %q, want it to name the missing credential resolver (issue #638 fail-closed)", errMsg)
	}
	if toolCount != 0 {
		t.Errorf("toolCount = %d, want 0 (server must not have connected)", toolCount)
	}

	// No live connection may exist for the skipped server.
	if mgr := al.mcp.getManager(); mgr != nil {
		if _, ok := mgr.GetServer("hdr-srv"); ok {
			t.Error("hdr-srv is live in the MCP manager; want no connection for an unresolvable-ref server")
		}
	}

	al.mcp.mu.Lock()
	initialized := al.mcp.initialized
	al.mcp.mu.Unlock()
	if !initialized {
		t.Error("initialized = false, want true — a per-server credential failure must not block initialization")
	}
}

// TestReconcileMCP_HeaderRefs_ResolvableRef_ReachesConnect pins the inverse
// direction: a HeaderRefs entry the resolver CAN satisfy must not fail
// reconciliation at the credential step — the server proceeds to the real
// connect attempt (which fails here against the unreachable URL, recording a
// connect error that is NOT a credential-resolution error).
//
// BDD:
//
//	Given an enabled http MCP server carrying HeaderRefs and a wired
//	  credential resolver that can satisfy them
//	When  ReconcileMCP runs
//	Then  the recorded error (from the failed connect against the
//	  unreachable URL) does not mention credential resolution — the ref was
//	  resolved and the connection genuinely attempted.
func TestReconcileMCP_HeaderRefs_ResolvableRef_ReachesConnect(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"hdr-ok": {
			Enabled: true,
			Type:    "http",
			URL:     "http://127.0.0.1:1/mcp",
			HeaderRefs: map[string]string{
				"Authorization": "mcp_hdr-ok_header_Authorization",
			},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	al.mcp.setCredentialResolver(func(refKey string) (string, error) {
		return "Bearer gwsec-resolved-" + refKey, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP returned a systemic error: %v", err)
	}

	status, _, errMsg := al.MCPServerStatus("hdr-ok")
	if status != "error" {
		t.Errorf("status = %q, want %q (the connect attempt against 127.0.0.1:1 must fail and be recorded)", status, "error")
	}
	if strings.Contains(errMsg, "no credential resolver is configured") ||
		strings.Contains(errMsg, "resolving header credential") {
		t.Errorf("errMsg = %q, want a connect failure, not a credential-resolution failure — a satisfiable ref must not block the connect", errMsg)
	}
}
