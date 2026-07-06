// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

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
	return "Add an MCP server to the configuration (tools.mcp.servers). The server is persisted to config.json and connects on next reload.\n" +
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

func (t *MCPAddTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
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

	if err := t.deps.WithConfig(func(cfg *config.Config) error {
		if cfg.Tools.MCP.Servers == nil {
			cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
		}
		if _, exists := cfg.Tools.MCP.Servers[name]; exists {
			return fmt.Errorf("mcp server %q already exists", name)
		}
		cfg.Tools.MCP.Servers[name] = entry
		return nil
	}); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return tools.ErrorResult(errorJSON("ALREADY_EXISTS", err.Error(),
				"Use list_mcp_servers to see existing servers, or remove it first with remove_mcp_server"))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"name":      name,
		"transport": transport,
		"enabled":   true,
		"note":      "Server added to config. It connects on the next agent-loop reload.",
	}))
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

func (t *MCPRemoveTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
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

	return tools.NewToolResult(successJSON(map[string]any{
		"name":    name,
		"removed": true,
		"note":    "Server removed from config. Its tools are unregistered on the next agent-loop reload.",
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
