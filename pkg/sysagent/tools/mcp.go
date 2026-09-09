// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// mcpReconcileTimeout bounds how long add/remove_mcp_server wait for live MCP
// reconciliation (connect/disconnect + tool-registry sync) after a config
// write, matching the REST handlers' timeout for the same operation.
const mcpReconcileTimeout = 20 * time.Second

// mcpURLSchemeValid reports whether rawURL is acceptable for an sse/http MCP
// server endpoint. It mirrors the gateway's mcpURLSchemeValid
// (pkg/gateway/rest.go) and the SPA's isValidUrlScheme so the same contract is
// enforced regardless of whether the server is added via REST or this tool.
//
// Rules:
//   - https:// is always accepted.
//   - http:// is accepted only for loopback hosts (localhost, 127.x.x.x, ::1).
//   - Any other scheme is rejected.
func mcpURLSchemeValid(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return true
	case "http":
		host := strings.ToLower(parsed.Hostname())
		return host == "localhost" ||
			strings.HasPrefix(host, "127.") ||
			host == "::1" ||
			host == "[::1]"
	default:
		return false
	}
}

// mcpEnvCredKey returns the canonical credential-store key for one MCP
// server's env var secret. Format mirrors the channel tools' channelCredKey
// (pkg/sysagent/tools/channel.go): "mcp_<server>_<envKey>".
func mcpEnvCredKey(serverName, envKey string) string {
	return "mcp_" + serverName + "_" + envKey
}

// ---- system.mcp.add ----

type MCPAddTool struct{ deps *Deps }

func NewMCPAddTool(d *Deps) *MCPAddTool      { return &MCPAddTool{deps: d} }
func (t *MCPAddTool) Name() string           { return "add_mcp_server" }
func (t *MCPAddTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *MCPAddTool) Description() string {
	return "Add an MCP server to the configuration (tools.mcp.servers). The server is saved to config.json, a connection is attempted immediately, and the result reports live connection status and tool count. " +
		"Every key in env is stored in the encrypted credential store and referenced from config.json by a ref (mcp_<name>_<key>) — never written in plaintext, mirroring configure_channel's credential handling. " +
		"sse/http URLs must be https, or http only for loopback addresses (localhost, 127.x.x.x, ::1). " +
		"Adding a server when MCP is globally disabled and no other server is enabled will turn tools.mcp.enabled ON — an install-wide change; if another server is already enabled the global flag is left alone and the new server will not connect until an operator enables it. " +
		"A name that already exists is refused, not updated — remove it first with remove_mcp_server. " +
		"Tools the server exposes register with no tool policy and cannot be called until an operator assigns one.\n" +
		"Parameters: name (required), transport (stdio/sse/http; default stdio), command (required for stdio), url (required for sse/http), args, env."
}

func (t *MCPAddTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":      map[string]any{"type": "string"},
			"transport": map[string]any{"type": "string", "enum": []string{"stdio", "sse", "http"}},
			"command":   map[string]any{"type": "string"},
			"args":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"url":       map[string]any{"type": "string"},
			"env":       map[string]any{"type": "object"},
		},
		"required": []string{"name"},
	}
}

func (t *MCPAddTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	// Reject path-traversal / separator characters in the server name — the name
	// is the map key in config and must be a clean identifier.
	if err := validateID(name); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid server name: "+err.Error(), ""))
	}

	transport, _ := args["transport"].(string)
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "stdio", "sse", "http":
	default:
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("invalid transport %q: must be one of stdio, sse, http", transport), ""))
	}

	command, _ := args["command"].(string)
	urlStr, _ := args["url"].(string)
	switch transport {
	case "stdio":
		if command == "" {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"command is required for stdio transport", ""))
		}
	case "sse", "http":
		if urlStr == "" {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"url is required for sse/http transport", ""))
		}
		if !mcpURLSchemeValid(urlStr) {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"url must use https, or http for loopback addresses only (localhost, 127.x.x.x, ::1)", ""))
		}
	}

	// Coerce optional args/env into typed values.
	argList := stringSliceArg(args["args"])
	envMap := stringMapArg(args["env"])

	// Duplicate-name pre-check: reject an existing server name
	// BEFORE any credential-store write. Without this, a name collision was
	// only discovered inside the WithConfig closure below — AFTER Phase 1 had
	// already written every env value to the credential store under
	// mcp_<name>_<key>, silently overwriting the ALREADY-CONFIGURED server's
	// live credentials with whatever the (rejected) request supplied, even
	// though the call as a whole reports ALREADY_EXISTS and appears to have
	// changed nothing. This read is a snapshot, not a lock — the in-closure
	// check further down is kept as the authoritative concurrency guard for
	// the (now much rarer) race where a same-named server is created between
	// this check and the WithConfig call.
	if cfg := t.deps.GetCfg(); cfg != nil {
		if _, exists := cfg.Tools.MCP.Servers[name]; exists {
			return tools.ErrorResult(errorJSON("ALREADY_EXISTS",
				fmt.Sprintf("mcp server %q already exists", name),
				"Use list_mcp_servers to see existing servers, or remove it first with remove_mcp_server"))
		}
	}

	// Route every env value through the encrypted credential store
	// instead of writing it into config.json in plaintext, mirroring
	// configure_channel's channelCredKey pattern (pkg/sysagent/tools/channel.go).
	// The whole `env` map is treated as secret-shaped rather than trying to
	// guess which keys are sensitive (GITHUB_TOKEN, *_API_KEY, ...) and which
	// are benign (PATH, NODE_ENV, ...) by name — there is no reliable way to
	// tell them apart, and over-protecting a benign value costs nothing.
	//
	// Phase 1 (before the config write, same ordering configure_channel
	// uses): store each value under mcp_<name>_<key>. A partial failure here
	// (some keys stored, one fails) leaves orphaned but harmless credential
	// entries and no config write — no dangling ref is possible.
	var envRefs map[string]string
	if len(envMap) > 0 {
		if t.deps.CredStore == nil {
			return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
				"credential store is not available",
				"Ensure the credential store is unlocked before adding an MCP server with env values",
			))
		}
		envRefs = make(map[string]string, len(envMap))
		for key, value := range envMap {
			credKey := mcpEnvCredKey(name, key)
			if err := t.deps.CredStore.Set(credKey, value); err != nil {
				return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
					fmt.Sprintf("Failed to store env credential %q: %s", key, err.Error()),
					"Check that the credential store is unlocked",
				))
			}
			envRefs[key] = credKey
		}
	}

	// Phase 2: persist the config entry. Env is deliberately left unset here —
	// EnvRefs is the only place a value added through this tool lands. The
	// real values are resolved back from the credential store at connect
	// time (pkg/mcp.ResolveServerEnvRefs, pkg/agent/loop_mcp.go's
	// reconcileLocked) so the plaintext secret exists only in memory, only
	// for the duration of a connect attempt.
	entry := config.MCPServerConfig{
		Enabled: true,
		Type:    transport,
		Command: command,
		URL:     urlStr,
		Args:    argList,
		EnvRefs: envRefs,
	}

	// flippedGlobalEnable/gatedGlobalDisabled record which of the two
	// auto-enable outcomes below happened, so the result note can be honest
	// about it (set inside the WithConfig closure, read after it returns).
	var flippedGlobalEnable, gatedGlobalDisabled bool

	if err := t.deps.WithConfig(func(cfg *config.Config) error {
		if cfg.Tools.MCP.Servers == nil {
			cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
		}
		if _, exists := cfg.Tools.MCP.Servers[name]; exists {
			return fmt.Errorf("mcp server %q already exists", name)
		}
		cfg.Tools.MCP.Servers[name] = entry
		// Adding a server only to have it silently ignored by a disabled global
		// kill-switch (tools.mcp.enabled defaults to false on a fresh install)
		// would reproduce the exact "saved but never connects" bug this tool
		// exists to avoid — but only for the fresh-install case. If the flag is
		// off AND another server is already enabled under it, that is an
		// operator's deliberate kill-switch, not an unconfigured default, and
		// must not be silently overridden by adding one more server.
		if !cfg.Tools.MCP.Enabled {
			otherEnabled := false
			for otherName, otherSrv := range cfg.Tools.MCP.Servers {
				if otherName == name {
					continue
				}
				if otherSrv.Enabled {
					otherEnabled = true
					break
				}
			}
			if otherEnabled {
				gatedGlobalDisabled = true
			} else {
				cfg.Tools.MCP.Enabled = true
				flippedGlobalEnable = true
			}
		}
		return nil
	}); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			// Race-guard fired: a same-named server was created concurrently,
			// between the pre-check above and this WithConfig call. Any env
			// credentials written in Phase 1 above were written under THIS
			// call's name/key combination and must be rolled back now — they
			// belong to a request that is being rejected, and leaving them in
			// place would hijack the credentials of the server that won the
			// race, exactly like the bug this whole fix closes. Best-effort:
			// a delete failure here is logged, not fatal — the request still
			// fails with ALREADY_EXISTS either way.
			for envKey, credKey := range envRefs {
				if delErr := t.deps.CredStore.Delete(credKey); delErr != nil {
					slog.Warn("add_mcp_server: name-collision race — failed to roll back env credential",
						"server", name, "env_key", envKey, "cred_key", credKey, "error", delErr)
				}
			}
			return tools.ErrorResult(errorJSON("ALREADY_EXISTS", err.Error(),
				"Use list_mcp_servers to see existing servers, or remove it first with remove_mcp_server"))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}

	// Trigger live reconciliation so the server actually connects (and its
	// tools get registered into the per-agent and central registries) instead
	// of only being persisted to config.json. Nil in tests or when the
	// gateway hasn't wired live MCP reconciliation.
	var reconcileErr error
	reconcileAttempted := t.deps.ReconcileMCP != nil
	if reconcileAttempted {
		rctx, cancel := context.WithTimeout(ctx, mcpReconcileTimeout)
		reconcileErr = t.deps.ReconcileMCP(rctx)
		cancel()
		if reconcileErr != nil {
			slog.Warn("add_mcp_server: live MCP reconciliation failed", "server", name, "error", reconcileErr)
		}
	}

	result := map[string]any{
		"name":      name,
		"transport": transport,
		"enabled":   true,
	}
	var note string
	if t.deps.MCPStatus != nil {
		status, toolCount, errMsg := t.deps.MCPStatus(name)
		result["status"] = status
		switch status {
		case "connected":
			result["tool_count"] = toolCount
			note = fmt.Sprintf(
				"Server connected; %d tool(s) registered and awaiting tool-policy assignment.", toolCount)
		case "error":
			// The config write DID succeed — only the live connection failed.
			note = fmt.Sprintf(
				"Server saved but connection failed: %s. Fix the config and try again.", errMsg)
		default: // "disconnected"
			note = "Server saved but not currently connected " +
				"(server disabled, or live reconciliation is unavailable)."
		}
	} else if reconcileAttempted && reconcileErr != nil {
		// Reconciliation actually ran and failed, but there is no MCPStatus
		// readback to report a live status from. Say so explicitly rather
		// than falling back to the "connects on next reload" note below,
		// which would silently swallow a real reconcile error.
		note = fmt.Sprintf(
			"Server saved but live reconciliation failed: %s. Connection status is unknown.", reconcileErr)
	} else {
		// Either live reconciliation isn't wired at all (tests, or a
		// partially wired gateway), or it ran and succeeded but there is no
		// MCPStatus readback available. Reload now actually connects MCP
		// servers (see AgentLoop.ReconcileMCP), so this note remains
		// accurate even without a live status readback.
		note = "Server added to config. It connects on the next agent-loop reload."
	}

	// Surface the global-kill-switch outcome honestly: an operator reading
	// only the note above would otherwise have no way to tell a genuine
	// connect failure apart from "saved, but MCP is globally disabled and
	// nothing was even attempted."
	switch {
	case flippedGlobalEnable:
		note += " Global MCP enable was off — turned on."
	case gatedGlobalDisabled:
		note += " MCP is globally disabled — an operator must enable tools.mcp.enabled for this server to connect."
	}
	result["note"] = note

	return tools.NewToolResult(successJSON(result))
}

// ---- system.mcp.remove ----

type MCPRemoveTool struct{ deps *Deps }

func NewMCPRemoveTool(d *Deps) *MCPRemoveTool   { return &MCPRemoveTool{deps: d} }
func (t *MCPRemoveTool) Name() string           { return "remove_mcp_server" }
func (t *MCPRemoveTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *MCPRemoveTool) Description() string {
	return "Remove an MCP server from the configuration (tools.mcp.servers) and persist the change.\n" +
		"Parameters: name (required), confirm (bool, must be true)."
}

func (t *MCPRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":    map[string]any{"type": "string"},
			"confirm": map[string]any{"type": "boolean"},
		},
		"required": []string{"name", "confirm"},
	}
}

func (t *MCPRemoveTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	confirm, _ := args["confirm"].(bool)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	if err := validateID(name); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid server name: "+err.Error(), ""))
	}
	if !confirm {
		return tools.ErrorResult(errorJSON("CONFIRMATION_REQUIRED",
			"confirm must be true to remove an MCP server", ""))
	}

	// removedEnvRefs captures the outgoing entry's EnvRefs (credential-store
	// keys, mcp_<name>_<envKey>) so they can be cleaned up AFTER the config
	// write succeeds — deleting the credential entries before the config
	// write is confirmed would risk destroying secrets for a removal that
	// then fails to persist.
	var removedEnvRefs map[string]string
	if err := t.deps.WithConfig(func(cfg *config.Config) error {
		if cfg.Tools.MCP.Servers == nil {
			return fmt.Errorf("NOT_FOUND")
		}
		existing, exists := cfg.Tools.MCP.Servers[name]
		if !exists {
			return fmt.Errorf("NOT_FOUND")
		}
		removedEnvRefs = existing.EnvRefs
		delete(cfg.Tools.MCP.Servers, name)
		return nil
	}); err != nil {
		if strings.Contains(err.Error(), "NOT_FOUND") {
			return tools.ErrorResult(errorJSON("MCP_SERVER_NOT_FOUND",
				fmt.Sprintf("No MCP server named %q", name),
				"Use list_mcp_servers to see configured servers"))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}

	// Clean up any credential-store entries add_mcp_server created for this
	// server's env values — best-effort: a failure here does not
	// undo the config removal (the server is already gone either way), it
	// only means an orphaned credential-store entry survives under a name
	// nothing references any more.
	var credCleanupFailed bool
	if len(removedEnvRefs) > 0 {
		if t.deps.CredStore == nil {
			credCleanupFailed = true
			slog.Warn("remove_mcp_server: credential store unavailable, env credentials orphaned",
				"server", name, "count", len(removedEnvRefs))
		} else {
			for envKey, credKey := range removedEnvRefs {
				if err := t.deps.CredStore.Delete(credKey); err != nil {
					credCleanupFailed = true
					slog.Warn("remove_mcp_server: failed to delete env credential",
						"server", name, "env_key", envKey, "cred_key", credKey, "error", err)
				}
			}
		}
	}

	// Trigger live reconciliation so the server is actually disconnected (and
	// its tools evicted from the per-agent and central registries) instead of
	// only being dropped from config.json. Nil in tests or when the gateway
	// hasn't wired live MCP reconciliation.
	note := "Server removed from config. Its tools are unregistered on the next agent-loop reload."
	if t.deps.ReconcileMCP != nil {
		rctx, cancel := context.WithTimeout(ctx, mcpReconcileTimeout)
		reconcileErr := t.deps.ReconcileMCP(rctx)
		cancel()
		if reconcileErr != nil {
			// The config removal DID succeed — only the live disconnect is in
			// doubt. Do not claim "disconnected and removed" when reconcile
			// itself errored; that would assert a live-side outcome we did
			// not actually observe.
			slog.Warn("remove_mcp_server: live MCP reconciliation failed", "server", name, "error", reconcileErr)
			note = fmt.Sprintf("Removed from config; live disconnect may not have completed: %s", reconcileErr)
		} else {
			note = "Server disconnected and removed."
		}
	}

	result := map[string]any{
		"name":    name,
		"removed": true,
		"note":    note,
	}
	if credCleanupFailed {
		result["cred_cleanup_warning"] = "the server was removed but one or more of its stored env " +
			"credentials could not be deleted and may remain orphaned in the credential store"
	}
	return tools.NewToolResult(successJSON(result))
}

// ---- system.mcp.list ----

type MCPListTool struct{ deps *Deps }

func NewMCPListTool(d *Deps) *MCPListTool     { return &MCPListTool{deps: d} }
func (t *MCPListTool) Name() string           { return "list_mcp_servers" }
func (t *MCPListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *MCPListTool) Description() string {
	return "List all MCP servers configured in tools.mcp.servers with their transport, enabled state, and live connection status " +
		"(connected/error/disconnected, from the same live readback add_mcp_server uses). Environment variables are never returned — " +
		"they may hold credential-store references or, for servers configured before that mechanism existed, real secrets. " +
		"No parameters required."
}

func (t *MCPListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *MCPListTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	cfg := t.deps.GetCfg()
	type serverInfo struct {
		Name      string `json:"name"`
		Transport string `json:"transport"`
		Enabled   bool   `json:"enabled"`
		Command   string `json:"command,omitempty"`
		URL       string `json:"url,omitempty"`
		// Status is live connection state (connected/error/disconnected),
		// sourced the same way add_mcp_server's readback is, so a
		// caller can tell "configured" apart from "actually connected".
		// Omitted (empty) when deps.MCPStatus is not wired (tests, or a
		// gateway that hasn't wired live MCP status).
		Status string `json:"status,omitempty"`
	}
	servers := make([]serverInfo, 0, len(cfg.Tools.MCP.Servers))
	for name, srv := range cfg.Tools.MCP.Servers {
		transport := srv.Type
		if transport == "" {
			// Default inference matches the MCP manager: stdio if a command is set,
			// sse if a url is set.
			if srv.Command != "" {
				transport = "stdio"
			} else if srv.URL != "" {
				transport = "sse"
			}
		}
		info := serverInfo{
			Name:      name,
			Transport: transport,
			Enabled:   srv.Enabled,
			Command:   srv.Command,
			URL:       srv.URL,
		}
		if t.deps.MCPStatus != nil {
			status, _, _ := t.deps.MCPStatus(name)
			info.Status = status
		}
		servers = append(servers, info)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return tools.NewToolResult(successJSON(map[string]any{"servers": servers}))
}
