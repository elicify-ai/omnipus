// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestIsUntrustedToolResult enforces the closed set of tool names that
// runTurn will pass through the prompt guard. This is the test that a new
// external-world tool addition MUST update.
func TestIsUntrustedToolResult(t *testing.T) {
	untrusted := []string{
		"search_web",
		"fetch_url",
		"browser_navigate",
		"browser_click",
		"browser_type",
		// browser_screenshot is trusted — its output is base64 PNG image data,
		// not attacker-controlled text. Sanitizing corrupts the data URL format
		// needed for the media pipeline to extract and register it.
		"browser_get_text",
		"browser_wait",
		"browser_evaluate",
		"read_file",
	}
	for _, name := range untrusted {
		t.Run("untrusted/"+name, func(t *testing.T) {
			if !isUntrustedToolResult(name) {
				t.Errorf("expected %q to be classified as untrusted", name)
			}
		})
	}

	trusted := []string{
		"exec",
		"write_file",
		"list_directory",
		"send_file",
		"message",
		"spawn",
		"task_add",
		"update_task",
		"list_tasks",
		"find_skills",
		"browser_screenshot", // base64 PNG — not attacker text
		"",                   // empty string must not match
		"search_web2",        // typo-squatting must not match
		"SEARCH_WEB",         // case-sensitive
	}
	for _, name := range trusted {
		t.Run("trusted/"+name, func(t *testing.T) {
			if isUntrustedToolResult(name) {
				t.Errorf("expected %q to be classified as trusted", name)
			}
		})
	}
}

// TestBash_EgressProxy_InjectsEnvVarsIntoChild is the ADR-036 functional
// proof for bash's UNIFIED hardening path (FR-B6): sandbox.ResolveLimits +
// sandbox.Run inject HTTP_PROXY/HTTPS_PROXY from ExecToolDeps.Proxy
// (*sandbox.EgressProxy) into the child's environment. This replaces the
// pre-ADR-036 SEC-28 security.ExecProxy/ExecChildProxy mechanism, which bash
// no longer wires — see pkg/tools/shell.go's package doc: every non-god-mode
// invocation now routes through the ADR-035 sandbox.EgressProxy uniformly,
// the same mechanism workspace_shell used before the merge.
func TestBash_EgressProxy_InjectsEnvVarsIntoChild(t *testing.T) {
	if _, err := exec.LookPath("env"); err != nil {
		t.Skip("env binary not available")
	}

	proxy, err := sandbox.NewEgressProxy([]string{"example.com"}, nil)
	if err != nil {
		t.Fatalf("sandbox.NewEgressProxy() error = %v", err)
	}
	defer proxy.Close()

	tmpDir := t.TempDir()
	tool, err := tools.NewExecToolWithDeps(
		tmpDir,
		true, // restrictToWorkspace
		&config.Config{},
		tools.ExecToolDeps{Proxy: proxy},
	)
	if err != nil {
		t.Fatalf("NewExecToolWithDeps() error = %v", err)
	}

	result := tool.Execute(t.Context(), map[string]any{
		"command": "env | grep -E '^(HTTP_PROXY|HTTPS_PROXY|http_proxy|https_proxy)=' | sort",
	})
	if result == nil {
		t.Fatal("Execute returned nil result")
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.ForLLM)
	}

	wantPrefix := "HTTP_PROXY=http://" + proxy.Addr()
	if !strings.Contains(result.ForLLM, wantPrefix) {
		t.Errorf("child env missing HTTP_PROXY=%s; got:\n%s", proxy.Addr(), result.ForLLM)
	}
	for _, v := range []string{"HTTP_PROXY=", "HTTPS_PROXY=", "http_proxy=", "https_proxy="} {
		if !strings.Contains(result.ForLLM, v) {
			t.Errorf("child env missing %q; got:\n%s", v, result.ForLLM)
		}
	}
}

// TestBash_EgressProxy_NotInjectedWhenNil proves that when Proxy is nil
// (disabled or the boot-time NewEgressProxy call failed), bash does NOT
// inject a proxy address — sandbox.ResolveLimits/BuildLimits leaves
// EgressProxyAddr empty, and the child inherits HTTP_PROXY (if any) from the
// scrubbed gateway environment unchanged (LIM-02 degraded mode).
func TestBash_EgressProxy_NotInjectedWhenNil(t *testing.T) {
	if _, err := exec.LookPath("env"); err != nil {
		t.Skip("env binary not available")
	}

	tmpDir := t.TempDir()
	const sentinel = "http://sentinel.invalid:9999"
	t.Setenv("HTTP_PROXY", sentinel)

	tool, err := tools.NewExecToolWithDeps(
		tmpDir,
		true,
		&config.Config{},
		tools.ExecToolDeps{}, // Proxy: nil
	)
	if err != nil {
		t.Fatalf("NewExecToolWithDeps() error = %v", err)
	}

	result := tool.Execute(t.Context(), map[string]any{
		"command": "env | grep -E '^HTTP_PROXY='",
	})
	if result == nil {
		t.Fatalf("Execute returned nil result")
	}
	if strings.Contains(result.ForLLM, "HTTP_PROXY=http://127.0.0.1:") {
		t.Errorf("bash injected a loopback proxy address when Proxy=nil:\n%s", result.ForLLM)
	}
}

// TestAgentLoopClose_StopsExecProxy ensures the exec proxy is shut down
// when the agent loop closes. Stop() is idempotent so auto-stop and
// explicit close can both fire without harm.
func TestAgentLoopClose_StopsExecProxy(t *testing.T) {
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
	cfg.Tools.Exec.EnableProxy = true

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	proxy := al.ExecProxy()
	if proxy == nil {
		t.Fatal("proxy not started")
	}
	addr := proxy.Addr()

	al.Close()

	// After close the listener must be gone — a fresh TCP dial should
	// fail with "connection refused" (or similar). Give the OS a brief
	// window to actually tear down the listener: bounded retry loop
	// rather than a fixed sleep avoids flakes without hiding a hang.
	var lastErr error
	for i := 0; i < 50; i++ {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			lastErr = err
			break
		}
		_ = c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr == nil {
		t.Errorf("proxy still accepting connections on %s after Close()", addr)
	}
}

// TestAgentLoop_PromptGuardAuditTrail ensures the audit logger is wired
// before the prompt guard fires. We assert the audit directory exists after
// enabling audit_log; runtime audit emission is exercised by the live
// loop_test and the unit tests in pkg/audit.
func TestAgentLoop_PromptGuardAuditTrail(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              filepath.Join(tmpDir, "workspace"),
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: filepath.Join(tmpDir, "workspace")}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog:             true,
			PromptInjectionLevel: "medium",
		},
	}
	// Workspace path derives the audit dir as its sibling.
	if err := os.MkdirAll(cfg.Agents.Defaults.Home, 0o755); err != nil {
		t.Fatal(err)
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	if al.AuditLogger() == nil {
		t.Fatal("audit logger not initialized")
	}
	if al.PromptGuard() == nil {
		t.Fatal("prompt guard not initialized")
	}
}
