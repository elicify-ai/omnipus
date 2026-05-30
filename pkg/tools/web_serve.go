// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — unified web_serve tool.
//
// web_serve merges serve_workspace (Tier 1 static files) and run_in_workspace
// (Tier 3 dev servers) into a single tool. Mode is selected by the presence
// of the "command" argument:
//
//   - command absent  → static mode: registers a directory in ServedSubdirs,
//     returns /preview/<agent>/<token>/ URL.
//   - command present → dev mode: spawns a background dev server and registers
//     it in DevServerRegistry, returns /preview/<agent>/<token>/ URL.
//
// Both modes return a result with a "kind" field ("static" or "dev") so the
// SPA UI can pick the correct icon and warmup behavior.
//
// Auth: token-only (FR-023). The /preview/ path token IS the credential.
//
// Linux gate: static mode works everywhere; dev mode is Linux-only and returns
// Tier3UnsupportedMessage on other platforms.

package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// ToolNameWebServe is the canonical tool name for the unified web-serving tool.
const ToolNameWebServe = "web_serve"

// Tier3UnsupportedMessage is the literal IsError content returned on
// non-Linux platforms for dev mode. It MUST match the gateway's /dev/ 503 body
// so users see the same wording at the tool layer and the HTTP layer.
//
// Spec v4 US-6 acceptance scenario 5.
const Tier3UnsupportedMessage = "Tier 3 dev servers are Linux only"

// ServedSubdirsRegistry is the interface the web_serve tool uses to register a
// static directory. It is satisfied by *agent.ServedSubdirs; the interface
// lives here to avoid an import cycle (tools → agent would cycle since agent
// imports tools).
type ServedSubdirsRegistry interface {
	// Register creates a new registration. Returns (token, deadline, error).
	Register(agentID, absDir string, duration time.Duration) (token string, deadline time.Time, err error)
	// ActiveForAgent returns (token, deadline, true) if agentID already has
	// an active registration.
	ActiveForAgent(agentID string) (token string, deadline time.Time, ok bool)
}

// normalisePort accepts both float64 (JSON default) and integer types.
// Returns int32 on success, error on out-of-range.
func normalisePort(v any) (int32, error) {
	switch n := v.(type) {
	case float64:
		if n < 0 || n > 65535 {
			return 0, fmt.Errorf("port %v out of TCP range", n)
		}
		return int32(n), nil
	case int:
		if n < 0 || n > 65535 {
			return 0, fmt.Errorf("port %d out of TCP range", n)
		}
		return int32(n), nil
	case int32:
		return n, nil
	case int64:
		if n < 0 || n > 65535 {
			return 0, fmt.Errorf("port %d out of TCP range", n)
		}
		return int32(n), nil
	}
	return 0, fmt.Errorf("port must be a number, got %T", v)
}

// webServeDevServerStartupGrace is how long to wait after spawning the child
// before registering it. Long enough for the child to bind its port.
const webServeDevServerStartupGrace = 3 * time.Second

// WebServeDevConfig is the runtime config snapshot for web_serve dev mode.
// It replaces the RunInWorkspaceConfig that lived in the now-removed
// run_in_workspace.go.
type WebServeDevConfig struct {
	// Tier3Commands extends the baseline allow-list. Each entry is a full
	// "binary subcommand" string (e.g. "remix dev").
	Tier3Commands []string

	// PortRange is [min, max] inclusive. Default [18000, 18999].
	PortRange [2]int32

	// MaxConcurrent is the per-gateway active-server cap. Default 2.
	MaxConcurrent int32

	// EgressAllowList is the operator allow-list (audit hint only).
	EgressAllowList []string

	// AuditFailClosed controls behavior when audit-write fails. When true
	// (default via cfg.Sandbox.PathGuardAuditFailClosed) the tool refuses to
	// start without a guaranteed compliance trail. When false, the audit
	// failure is logged at Error and the dev server proceeds.
	AuditFailClosed bool
}

// WebServeTool is the unified web-serving tool. One instance is registered per
// agent; both registries are shared process-wide (different lifecycles).
type WebServeTool struct {
	BaseTool
	// workspace is the agent's absolute workspace directory.
	workspace string
	// agentID is the agent this tool instance belongs to.
	agentID string
	// gatewayPreviewBaseURL is the preview listener base URL (e.g.
	// "http://localhost:5001"). Used to build absolute URLs in tool results.
	// MUST be the preview origin (different from SPA origin) per T-01.
	gatewayPreviewBaseURL string
	// served is the process-wide static registration map (Tier 1).
	served ServedSubdirsRegistry
	// devReg is the process-wide dev-server registry (Tier 3). May be nil on
	// non-Linux — the tool guards dev mode with a runtime.GOOS check.
	devReg *sandbox.DevServerRegistry
	// devCfg holds the dev mode configuration snapshot.
	devCfg WebServeDevConfig
	// proxy is the shared egress proxy. May be nil (graceful degradation).
	proxy *sandbox.EgressProxy
	// auditLogger may be nil in tests; production wiring passes a real logger.
	auditLogger *audit.Logger
	// minDuration / maxDuration clamp static-mode registration lifetimes.
	minDuration time.Duration
	maxDuration time.Duration
	// runMu guards the dev-mode validate-then-register path.
	runMu sync.Mutex
}

// NewWebServeTool constructs the unified web_serve tool for a given agent.
//
//   - workspace: absolute path to the agent's workspace root.
//   - agentID: the agent's ID (embedded in the URL).
//   - gatewayPreviewBaseURL: preview listener base URL (no trailing slash).
//   - served: ServedSubdirs registry for static mode.
//   - devReg: DevServerRegistry for dev mode (nil on non-Linux).
//   - devCfg: dev mode config.
//   - egressProxy: shared egress proxy (may be nil).
//   - auditLogger: audit logger (may be nil in tests).
//   - minDurSec / maxDurSec: static registration lifetime bounds (seconds).
func NewWebServeTool(
	workspace string,
	agentID string,
	gatewayPreviewBaseURL string,
	served ServedSubdirsRegistry,
	devReg *sandbox.DevServerRegistry,
	devCfg WebServeDevConfig,
	egressProxy *sandbox.EgressProxy,
	auditLogger *audit.Logger,
	minDurSec, maxDurSec int32,
) *WebServeTool {
	if minDurSec <= 0 {
		minDurSec = 60
	}
	if maxDurSec <= 0 {
		maxDurSec = 86400
	}
	if devCfg.PortRange == [2]int32{} {
		devCfg.PortRange = [2]int32{18000, 18999}
	}
	if devCfg.MaxConcurrent <= 0 {
		devCfg.MaxConcurrent = 2
	}
	return &WebServeTool{
		workspace:             workspace,
		agentID:               agentID,
		gatewayPreviewBaseURL: gatewayPreviewBaseURL,
		served:                served,
		devReg:                devReg,
		devCfg:                devCfg,
		proxy:                 egressProxy,
		auditLogger:           auditLogger,
		minDuration:           time.Duration(minDurSec) * time.Second,
		maxDuration:           time.Duration(maxDurSec) * time.Second,
	}
}

// SetAuditLogger satisfies auditLoggerAware.
func (t *WebServeTool) SetAuditLogger(logger *audit.Logger) {
	t.auditLogger = logger
}

func (t *WebServeTool) Name() string     { return ToolNameWebServe }
func (t *WebServeTool) Scope() ToolScope { return ScopeGeneral }

func (t *WebServeTool) Description() string {
	return "Serve a directory or run a dev server from the agent workspace. " +
		"Static mode (no command): registers the directory as a static website. " +
		"Dev mode (with command): starts a dev server (vite/next/astro/sveltekit dev) and proxies it. " +
		"Returns a /preview/<agent>/<token>/ URL. Dev mode is Linux only."
}

func (t *WebServeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Subdirectory within the agent workspace to serve.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Optional dev-server command (e.g. 'vite dev'). When set, web_serve runs as a dev server; when omitted, serves static files.",
			},
			"port": map[string]any{
				"type":        "integer",
				"description": "Optional bind port for dev mode. Auto-picked from the configured range when omitted.",
			},
			"duration_seconds": map[string]any{
				"type":        "integer",
				"description": "Optional registration lifetime in seconds. Static mode default = 1h, max 24h. Dev mode is governed by the dev-server registry idle/hard timeouts.",
			},
		},
		"required": []string{"path"},
	}
}

// Execute dispatches to static or dev mode based on whether "command" is present.
func (t *WebServeTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	rawPath, _ := args["path"].(string)
	if rawPath == "" {
		return ErrorResult("path is required")
	}

	command, _ := args["command"].(string)
	command = strings.TrimSpace(command)

	if command == "" {
		return t.executeStatic(ctx, rawPath, args)
	}
	return t.executeDev(ctx, rawPath, command, args)
}

// executeStatic handles the static-file serving mode.
func (t *WebServeTool) executeStatic(ctx context.Context, rawPath string, args map[string]any) *ToolResult {
	// H4: fail-fast when the preview listener is not configured. An empty
	// gatewayPreviewBaseURL means the URL we'd return would have no scheme
	// or host, producing a silently-broken link for the agent. Operator must
	// set gateway.preview_listener_enabled=true and restart.
	if t.gatewayPreviewBaseURL == "" {
		return &ToolResult{
			IsError: true,
			ForLLM:  "preview disabled: gateway preview listener is not configured; web_serve cannot mint a working URL",
			ForUser: "Iframe preview is disabled in this gateway configuration. Enable gateway.preview_listener_enabled and restart.",
		}
	}

	// Resolve and validate the path within the workspace.
	absDir, err := ValidateWorkspacePath(rawPath, t.workspace, true, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("path rejected: %v", err))
	}

	// Parse optional duration; clamp to [min, max].
	duration := t.parseDuration(args)

	agentID := t.agentID
	if agentID == "" {
		agentID = ToolAgentID(ctx)
	}

	if t.served == nil {
		return ErrorResult("web_serve: static registry not configured")
	}

	// Register (atomically replaces any previous registration for this agent).
	token, deadline, regErr := t.served.Register(agentID, absDir, duration)
	if regErr != nil {
		return ErrorResult(fmt.Sprintf("web_serve: registration failed: %v", regErr))
	}

	path := fmt.Sprintf("/preview/%s/%s/", agentID, token)
	url := t.gatewayPreviewBaseURL + path

	return NewToolResult(fmt.Sprintf(
		`{"kind":"static","path":%q,"url":%q,"expires_at":%q}`,
		path,
		url,
		deadline.UTC().Format(time.RFC3339),
	))
}

// tier3BaselineAllowList is the hardcoded baseline of allowed Tier 3 dev-server
// commands. Each entry is a "binary subcommand" prefix. The validation rule
// in validateTier3Command checks that the agent-supplied command, once
// tokenised and normalised, has one of these entries as a prefix.
var tier3BaselineAllowList = []string{
	"next dev",
	"vite dev",
	"astro dev",
	"sveltekit dev",
	"npm run dev",
	"pnpm dev",
	"yarn dev",
}

// validateTier3Command checks that command is prefixed by an entry from the
// combined allow-list (baseline + operator-supplied Tier3Commands). Returns
// nil on success, or an error describing why the command was rejected.
//
// Validation rules (per deliverable spec):
//  1. Tokenise command on whitespace (strings.Fields — handles multiple spaces).
//  2. The first 1-2 tokens must form a prefix matching an allow-list entry.
//  3. Path-prefixed binaries (e.g. "/usr/bin/next") are rejected outright —
//     the binary token must be a bare name with no path separator.
//  4. Case-sensitive match.
//
// Reusing PolicyAuditor.EvaluateExec was considered but rejected: that
// function performs glob-match on the full command against an exec
// allow-list, whereas here we need an exact token-prefix match against a
// "binary subcommand" allow-list. Wiring a PolicyAuditor into WebServeTool
// would also couple tools → policy → (agent, config) and create import-cycle
// risk. A focused local validator is the architecturally cleaner choice.
// shellMetaChars lists characters that have special meaning in POSIX shells.
// Checked against the raw command string BEFORE tokenisation so that
// injection payloads embedded in newlines (e.g. "next dev\nbash") are caught
// even when strings.Fields would silently eat the separator.
const shellMetaChars = "&|;$`(){}!<>\\\n\r\t"

func validateTier3Command(command string, operatorExtensions []string) error {
	// Reject shell metacharacters in the raw input before any tokenisation.
	// strings.Fields eats \n/\r/\t, so "next dev\nbash" would tokenise to
	// ["next","dev","bash"] and pass an allow-list prefix check. Scanning the
	// raw string first closes that bypass.
	for _, r := range shellMetaChars {
		if strings.ContainsRune(command, r) {
			return fmt.Errorf("command contains forbidden character %q", r)
		}
	}

	tokens := strings.Fields(command)
	if len(tokens) == 0 {
		return fmt.Errorf("empty command")
	}

	// Reject path-prefixed binaries — bare names only.
	if strings.ContainsRune(tokens[0], '/') {
		return fmt.Errorf("command binary %q must be a bare name (no path prefix)", tokens[0])
	}

	// skipSingleTokenOnce guards the sticky Warn for single-token operator extensions
	// that slip past config-load validation (e.g. mutated at runtime).
	var skipSingleTokenOnce sync.Once

	// Build the combined allow-list: baseline + operator extensions.
	// Defense-in-depth: skip single-token operator extensions at runtime with a
	// sticky Warn. Config-load validation (validateBootConfig) is the primary
	// line of defense; this skip handles edge cases such as extensions that were
	// mutated after boot (should not happen in normal operation).
	combined := make([]string, 0, len(tier3BaselineAllowList)+len(operatorExtensions))
	combined = append(combined, tier3BaselineAllowList...)
	for _, ext := range operatorExtensions {
		if len(strings.Fields(ext)) < 2 {
			skipSingleTokenOnce.Do(func() {
				slog.Warn("web_serve: skipping single-token operator Tier3Command extension at runtime; "+
					"fix cfg.Sandbox.Tier3Commands to use 'binary subcommand' format",
					"entry", ext)
			})
			continue
		}
		combined = append(combined, ext)
	}

	// Check whether the normalised command has an allow-list entry as a token
	// prefix. We normalise by re-joining the tokens from the entry (which may
	// itself be multi-token, e.g. "npm run dev") and checking that the
	// supplied command starts with exactly those tokens, each delimited by a
	// single space.
	for _, entry := range combined {
		entryTokens := strings.Fields(entry)
		if len(entryTokens) == 0 {
			continue
		}
		if len(tokens) < len(entryTokens) {
			continue
		}
		// Compare token-by-token up to len(entryTokens).
		match := true
		for i, et := range entryTokens {
			if tokens[i] != et {
				match = false
				break
			}
		}
		if match {
			return nil
		}
	}

	return fmt.Errorf("command %q is not in the Tier 3 allow-list", tokens[0])
}

// executeDev handles the dev-server mode.
func (t *WebServeTool) executeDev(ctx context.Context, rawPath, command string, args map[string]any) *ToolResult {
	// Linux gate.
	if runtime.GOOS != "linux" {
		return ErrorResult(Tier3UnsupportedMessage)
	}

	// H4: fail-fast when the preview listener is not configured.
	if t.gatewayPreviewBaseURL == "" {
		return &ToolResult{
			IsError: true,
			ForLLM:  "preview disabled: gateway preview listener is not configured; web_serve cannot mint a working URL",
			ForUser: "Iframe preview is disabled in this gateway configuration. Enable gateway.preview_listener_enabled and restart.",
		}
	}

	if t.devReg == nil {
		return ErrorResult("web_serve: dev-server registry not configured")
	}

	// Validate command against Tier 3 allow-list BEFORE any other work.
	if err := validateTier3Command(command, t.devCfg.Tier3Commands); err != nil {
		agentID := t.agentID
		if agentID == "" {
			agentID = ToolAgentID(ctx)
		}
		if auditResult := t.auditDevDeny(agentID, command, err.Error()); auditResult != nil {
			// AuditFailClosed=true and the audit write could not be recorded;
			// surface the audit-failure error rather than the normal deny.
			return auditResult
		}
		return ErrorResult(fmt.Sprintf("web_serve: command not permitted: %v", err))
	}

	// Resolve the workspace subdirectory.
	absDir, err := ValidateWorkspacePath(rawPath, t.workspace, true, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("path rejected: %v", err))
	}

	// Parse the port argument.
	var exposePort int32
	if portRaw, ok := args["port"]; ok && portRaw != nil {
		p, parseErr := normalisePort(portRaw)
		if parseErr != nil {
			return ErrorResult(fmt.Sprintf("invalid port: %v", parseErr))
		}
		exposePort = p
	} else {
		// Auto-pick from the configured range.
		exposePort = t.devCfg.PortRange[0]
	}

	// Port range check.
	if exposePort < t.devCfg.PortRange[0] || exposePort > t.devCfg.PortRange[1] {
		return ErrorResult(fmt.Sprintf(
			"port out of allowed range [%d, %d]",
			t.devCfg.PortRange[0], t.devCfg.PortRange[1],
		))
	}

	agentID := t.agentID
	if agentID == "" {
		agentID = ToolAgentID(ctx)
	}
	if agentID == "" {
		return ErrorResult("web_serve: missing agent id in context")
	}

	// Per-agent cap pre-check.
	t.runMu.Lock()
	existing := t.devReg.LookupByAgent(agentID)
	t.runMu.Unlock()
	if existing != nil {
		return ErrorResult(fmt.Sprintf(
			"dev server already running on this agent; previous registration expires at %s",
			existing.CreatedAt.Add(sandbox.HardTimeout).UTC().Format(time.RFC3339),
		))
	}

	// Build env: PORT + HOST force the dev server to bind loopback only.
	envSlice := []string{
		fmt.Sprintf("PORT=%d", exposePort),
		"HOST=127.0.0.1",
	}

	// HIGH-6: audit BEFORE spawning (fail-closed option).
	if auditErr := t.auditDevStart(ctx, agentID, command, exposePort); auditErr != nil {
		return auditErr
	}

	// Spawn the background child.
	cmd, spawnErr := t.spawnDevChild(command, absDir, envSlice, exposePort)
	if spawnErr != nil {
		return ErrorResult(fmt.Sprintf("web_serve: failed to start dev server: %v", spawnErr))
	}

	// Register. Kill orphan if registration fails.
	reg, regErr := t.devReg.Register(agentID, exposePort, cmd.Process.Pid, command, int(t.devCfg.MaxConcurrent))
	if regErr != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		orphanPid := cmd.Process.Pid
		go func() {
			waitErr := cmd.Wait()
			exitCode := -1
			if waitErr == nil {
				exitCode = 0
			} else if ee, ok := waitErr.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			}
			slog.Info("web_serve: orphaned child exited (registration failed)",
				"agent_id", agentID, "pid", orphanPid, "exit_code", exitCode, "error", waitErr)
		}()

		var capErr sandbox.GatewayCapError
		if errors.As(regErr, &capErr) {
			return ErrorResult(fmt.Sprintf(
				"too many concurrent dev servers (%d/%d); earliest expiry at %s",
				capErr.Current, capErr.Max, capErr.EarliestExpiry.UTC().Format(time.RFC3339),
			))
		}
		if errors.Is(regErr, sandbox.ErrPerAgentCap) {
			return ErrorResult("dev server already running on this agent")
		}
		return ErrorResult(fmt.Sprintf("web_serve: registration failed: %v", regErr))
	}

	// Reap goroutine: log exit outcome and clean up registration.
	bgPid := cmd.Process.Pid
	go func() {
		waitErr := cmd.Wait()
		exitCode := -1
		if waitErr == nil {
			exitCode = 0
		} else if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		slog.Info("web_serve: dev server exited",
			"agent_id", agentID, "pid", bgPid, "token", reg.Token,
			"exit_code", exitCode, "error", waitErr)
		t.devReg.Unregister(reg.Token)
	}()

	// Brief startup grace so the dev server binds before the first request.
	select {
	case <-ctx.Done():
	case <-time.After(webServeDevServerStartupGrace):
	}

	// H7: TCP-probe the bind address after the grace period. If the child
	// failed to bind (e.g. cross-agent port collision where another process
	// already holds the port), the probe fails immediately and we surface a
	// real IsError instead of returning a token URL that points at a corpse.
	// 50 ms is enough to distinguish "process dead / bind failed" from "bind
	// in progress" — we already waited 3 s above.
	probeAddr := fmt.Sprintf("127.0.0.1:%d", exposePort)
	probeConn, probeErr := net.DialTimeout("tcp", probeAddr, 50*time.Millisecond)
	if probeErr != nil {
		t.devReg.Unregister(reg.Token)
		return &ToolResult{
			IsError: true,
			ForLLM: fmt.Sprintf(
				"web_serve: dev server on port %d did not bind after %s grace period "+
					"(TCP probe failed: %v); likely port collision — choose a different port",
				exposePort, webServeDevServerStartupGrace, probeErr),
		}
	}
	_ = probeConn.Close()

	path := fmt.Sprintf("/preview/%s/%s/", agentID, reg.Token)
	// Build the URL by concatenating the base URL and the path. The base
	// URL is constructed upstream with a scheme (http/https), so the simple
	// concat is the canonical builder here; sandbox.BuildDevURL's dead first
	// line has been removed (H3/A4).
	url := t.gatewayPreviewBaseURL + path

	deadline := reg.CreatedAt.Add(sandbox.HardTimeout).UTC().Format(time.RFC3339)
	summary := fmt.Sprintf(
		"web_serve: dev server started. URL: %s. Command: %s. Port: %d. Token expires after 30 min idle / 4 h hard cap.",
		url,
		command,
		exposePort,
	)

	return NewToolResult(fmt.Sprintf(
		`{"kind":"dev","path":%q,"url":%q,"expires_at":%q,"command":%q,"port":%d,"_summary":%q}`,
		path, url, deadline, command, exposePort, summary,
	))
}

// parseDuration extracts duration_seconds from args, defaulting to maxDuration
// and clamping to [minDuration, maxDuration].
func (t *WebServeTool) parseDuration(args map[string]any) time.Duration {
	var duration time.Duration
	switch v := args["duration_seconds"].(type) {
	case float64:
		duration = time.Duration(int64(v)) * time.Second
	case int:
		duration = time.Duration(v) * time.Second
	case int64:
		duration = time.Duration(v) * time.Second
	default:
		duration = t.maxDuration
	}
	if duration < t.minDuration {
		duration = t.minDuration
	}
	if duration > t.maxDuration {
		duration = t.maxDuration
	}
	return duration
}

// proxyAddr returns the egress proxy addr, or "" when proxy is nil.
func (t *WebServeTool) proxyAddr() string {
	if t.proxy == nil {
		return ""
	}
	return t.proxy.Addr()
}

// spawnDevChild starts the dev-server background child.
func (t *WebServeTool) spawnDevChild(
	command string,
	workDir string,
	env []string,
	port int32,
) (*exec.Cmd, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	limits := sandbox.Limits{
		TimeoutSeconds:   0,
		MemoryLimitBytes: 0,
		WorkspaceDir:     workDir,
		EgressProxyAddr:  t.proxyAddr(),
	}

	return sandbox.SpawnBackgroundChild(parts, workDir, env, port, limits)
}

// auditDevNilOnce gates the sticky boot warning for a nil auditLogger on the
// deny path. One warning per process is enough — repeated denies with a nil
// logger would flood the log.
var auditDevNilOnce sync.Once

// auditDevDeny emits an audit entry when a dev-server command is rejected by
// the Tier 3 allow-list validator.
//
// When AuditFailClosed=true:
//   - nil auditLogger → returns a *ToolResult so the caller can surface a
//     distinct error message (the command is denied AND the audit trail is broken).
//   - write failure → same as nil logger: returns a *ToolResult.
//
// When AuditFailClosed=false:
//   - nil auditLogger → logs a one-time slog.Warn (sticky boot warning) and
//     returns nil so the caller proceeds with the normal deny response.
//   - write failure → logs slog.Error and returns nil.
//
// The ctx parameter is intentionally absent: the audit write is synchronous
// and the context cancellation would only matter for network-backed loggers
// which this codebase does not use. Removing it avoids the "ctx not used"
// lint warning and keeps the signature consistent across the audit helpers.
func (t *WebServeTool) auditDevDeny(agentID, command, reason string) *ToolResult {
	if t.auditLogger == nil {
		if t.devCfg.AuditFailClosed {
			slog.Error("web_serve: auditLogger is nil; cannot record deny — failing closed",
				"agent_id", agentID, "command", command, "audit_fail_closed", true)
			return &ToolResult{
				IsError: true,
				ForLLM:  "audit logger unavailable; command denied and audit trail broken — failing closed",
				ForUser: "Tier 3 requires audit logging; aborting",
			}
		}
		// H4-BK: audit explicitly disabled by operator (AuditLog=false).
		// Do NOT bump IncSkipped — skipped_counter tracks unexpected write
		// loss on a configured-but-failing logger, not a deliberate disable.
		auditDevNilOnce.Do(func() {
			slog.Warn("web_serve: auditLogger is nil; deny will not be recorded",
				"agent_id", agentID, "command", command)
		})
		return nil
	}
	logErr := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: audit.DecisionDeny,
		AgentID:  agentID,
		Tool:     ToolNameWebServe,
		Command:  command,
		Details: map[string]any{
			"reason":    reason,
			"workspace": t.workspace,
		},
	})
	if logErr == nil {
		return nil
	}
	if t.devCfg.AuditFailClosed {
		slog.Error("web_serve: audit write failed for command deny — failing closed",
			"agent_id", agentID, "command", command, "error", logErr, "audit_fail_closed", true)
		return &ToolResult{
			IsError: true,
			ForLLM:  "audit logger degraded; command denied and audit trail broken — failing closed",
			ForUser: "Tier 3 requires audit logging; aborting",
		}
	}
	// B1.2(e): write-failure path with AuditFailClosed=false. Same counter
	// bump — every silently-dropped audit row increments audit_skipped_total.
	audit.IncSkipped(ToolNameWebServe, audit.DecisionDeny)
	slog.Error("web_serve: audit write failed for command deny",
		"agent_id", agentID, "command", command, "error", logErr)
	return nil
}

// auditDevStart emits an audit entry before spawning the child (HIGH-6).
// Returns a non-nil *ToolResult only when the dev server must be aborted.
//
// When AuditFailClosed=true:
//   - nil auditLogger → returns a *ToolResult so the caller aborts the spawn
//     (the command is blocked AND the audit trail is broken).
//   - write failure → same as nil logger: returns a *ToolResult.
//
// When AuditFailClosed=false:
//   - nil auditLogger → audit explicitly disabled by operator; return nil and
//     allow the spawn to proceed. No IncSkipped — this is a deliberate
//     configuration choice, not a failure (see H4-BK).
//   - write failure → logs slog.Error, bumps IncSkipped, and returns nil.
func (t *WebServeTool) auditDevStart(ctx context.Context, agentID, command string, port int32) *ToolResult {
	if t.auditLogger == nil {
		if t.devCfg.AuditFailClosed {
			// CRIT-BK-1: mirror the deny-path shape — refuse to spawn when the
			// operator demands a compliance trail and no logger is wired.
			slog.Error("web_serve: auditLogger is nil; cannot record dev-server start — failing closed",
				"agent_id", agentID, "command", command, "audit_fail_closed", true)
			return &ToolResult{
				IsError: true,
				ForLLM:  "audit logger unavailable; dev server start denied — failing closed",
				ForUser: "Tier 3 requires audit logging; aborting",
			}
		}
		// H4-BK: audit explicitly disabled by operator (AuditLog=false).
		// Do NOT bump IncSkipped — skipped_counter tracks unexpected write
		// loss on a configured-but-failing logger, not a deliberate disable.
		return nil
	}
	logErr := t.auditLogger.Log(&audit.Entry{
		Event:    audit.EventExec,
		Decision: audit.DecisionAllow,
		AgentID:  agentID,
		Tool:     ToolNameWebServe,
		Command:  command,
		Details: map[string]any{
			"expose_port":      port,
			"egress_allowlist": t.devCfg.EgressAllowList,
			"workspace":        t.workspace,
		},
	})
	if logErr == nil {
		return nil
	}
	if t.devCfg.AuditFailClosed {
		slog.Error("web_serve: audit logger degraded; refusing to run trusted-prompt feature",
			"agent_id", agentID, "command", command, "error", logErr, "audit_fail_closed", true)
		return &ToolResult{
			IsError: true,
			ForLLM:  "audit logger degraded; refusing to run trusted-prompt feature without compliance trail",
			ForUser: "Tier 3 requires audit logging; aborting",
		}
	}
	// B1.2(e): same counter bump on the allow-side write failure.
	audit.IncSkipped(ToolNameWebServe, audit.DecisionAllow)
	slog.Error("web_serve: audit write failed (continuing — audit_fail_closed=false)",
		"agent_id", agentID, "command", command, "error", logErr)
	return nil
}
