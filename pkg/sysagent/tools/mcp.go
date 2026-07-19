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

// ---- system.mcp.add ----

type MCPAddTool struct{ deps *Deps }

func NewMCPAddTool(d *Deps) *MCPAddTool      { return &MCPAddTool{deps: d} }
func (t *MCPAddTool) Name() string           { return "add_mcp_server" }
func (t *MCPAddTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *MCPAddTool) Description() string {
	return "Add an MCP server to the configuration (tools.mcp.servers). The server is saved to config.json, a connection is attempted immediately, and the result reports live connection status and tool count.\n" +
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

	entry := config.MCPServerConfig{
		Enabled: true,
		Type:    transport,
		Command: command,
		URL:     urlStr,
		Args:    argList,
		Env:     envMap,
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
	if t.deps.MCPStatus != nil {
		status, toolCount, errMsg := t.deps.MCPStatus(name)
		result["status"] = status
		switch status {
		case "connected":
			result["tool_count"] = toolCount
			result["note"] = fmt.Sprintf(
				"Server connected; %d tool(s) registered and awaiting tool-policy assignment.", toolCount)
		case "error":
			// The config write DID succeed — only the live connection failed.
			result["note"] = fmt.Sprintf(
				"Server saved but connection failed: %s. Fix the config and try again.", errMsg)
		default: // "disconnected"
			result["note"] = "Server saved but not currently connected " +
				"(server disabled, or live reconciliation is unavailable)."
		}
	} else if reconcileAttempted && reconcileErr != nil {
		// Reconciliation actually ran and failed, but there is no MCPStatus
		// readback to report a live status from. Say so explicitly rather
		// than falling back to the "connects on next reload" note below,
		// which would silently swallow a real reconcile error.
		result["note"] = fmt.Sprintf(
			"Server saved but live reconciliation failed: %s. Connection status is unknown.", reconcileErr)
	} else {
		// Either live reconciliation isn't wired at all (tests, or a
		// partially wired gateway), or it ran and succeeded but there is no
		// MCPStatus readback available. Reload now actually connects MCP
		// servers (see AgentLoop.ReconcileMCP), so this note remains
		// accurate even without a live status readback.
		result["note"] = "Server added to config. It connects on the next agent-loop reload."
	}

	// Surface the global-kill-switch outcome honestly: an operator reading
	// only the note above would otherwise have no way to tell a genuine
	// connect failure apart from "saved, but MCP is globally disabled and
	// nothing was even attempted."
	switch {
	case flippedGlobalEnable:
		result["note"] = result["note"].(string) + " Global MCP enable was off — turned on."
	case gatedGlobalDisabled:
		result["note"] = result["note"].(string) +
			" MCP is globally disabled — an operator must enable tools.mcp.enabled for this server to connect."
	}

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

	if err := t.deps.WithConfig(func(cfg *config.Config) error {
		if cfg.Tools.MCP.Servers == nil {
			return fmt.Errorf("NOT_FOUND")
		}
		if _, exists := cfg.Tools.MCP.Servers[name]; !exists {
			return fmt.Errorf("NOT_FOUND")
		}
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

	return tools.NewToolResult(successJSON(map[string]any{
		"name":    name,
		"removed": true,
		"note":    note,
	}))
}

// ---- system.mcp.list ----

type MCPListTool struct{ deps *Deps }

func NewMCPListTool(d *Deps) *MCPListTool     { return &MCPListTool{deps: d} }
func (t *MCPListTool) Name() string           { return "list_mcp_servers" }
func (t *MCPListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *MCPListTool) Description() string {
	return "List all MCP servers configured in tools.mcp.servers with their transport and enabled state. No parameters required."
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
		servers = append(servers, serverInfo{
			Name:      name,
			Transport: transport,
			Enabled:   srv.Enabled,
			Command:   srv.Command,
			URL:       srv.URL,
		})
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return tools.NewToolResult(successJSON(map[string]any{"servers": servers}))
}
