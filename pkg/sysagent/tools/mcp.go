// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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

type MCPAddTool struct {
	deps *Deps
	secretSelectionAudit
}

func NewMCPAddTool(d *Deps) *MCPAddTool      { return &MCPAddTool{deps: d} }
func (t *MCPAddTool) Name() string           { return "add_mcp_server" }
func (t *MCPAddTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *MCPAddTool) Description() string {
	return "Add an MCP server to the configuration (tools.mcp.servers). The server is saved to config.json, a connection is attempted immediately, and the result reports live connection status and tool count. " +
		"Every key in env is stored in the encrypted credential store and referenced from config.json by a ref (mcp_<name>_<key>) — never written in plaintext, mirroring configure_channel's credential handling. " +
		"sse/http URLs must be https, or http only for loopback addresses (localhost, 127.x.x.x, ::1). " +
		"Adding a server when MCP is globally disabled and no other server is enabled will turn tools.mcp.enabled ON — an install-wide change; if another server is already enabled the global flag is left alone and the new server will not connect until an operator enables it. " +
		"A name that already exists is refused, not updated — remove it first with remove_mcp_server. " +
		"Tools the server exposes register with no tool policy and cannot be called until an operator assigns one. " +
		"expected_revision must be \"absent\" (the name's revision from list_mcp_servers); the write is refused if a server of that name appeared since.\n" +
		"Parameters: name (required), expected_revision (required), transport (stdio/sse/http; default stdio), command (required for stdio), url (required for sse/http), args, env."
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
			"env_refs":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			expectedRevisionParam: map[string]any{
				"type":        "string",
				"description": "The server name's revision from list_mcp_servers: \"absent\" for a new server.",
			},
		},
		"required": []string{"name", expectedRevisionParam},
	}
}

// mcpAddToolExecute carries the shared state of Execute across its stages.
type mcpAddToolExecute struct {
	t                   *MCPAddTool
	ctx                 context.Context
	args                map[string]any
	name                string
	transport           string
	envRefs             map[string]string
	entry               config.MCPServerConfig
	attachment          bool
	unchanged           bool
	flippedGlobalEnable bool
	gatedGlobalDisabled bool
}

func (t *MCPAddTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	if refusal := tools.ToolArgumentRefusal(t, args); refusal != nil {
		return refusal
	}
	mat := &mcpAddToolExecute{t: t, ctx: ctx, args: args}
	if r0, stop := mat.validateInput(); stop {
		return r0
	}
	expected, missing := expectedRevisionArg(args, "list_mcp_servers")
	if missing != nil {
		return missing
	}
	writeErr := t.deps.withSharedResourceWrite(sharedMCPServer, mat.name, expected, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// A validated identical attachment has no publication or runtime side
		// effect. All other writes re-derive under the agent-loop lock too.
		if err := mat.prepareEntry(t.deps.GetCfg()); err != nil {
			return err
		}
		if mat.unchanged {
			return nil
		}
		return mat.persistConfig()
	})
	if r, ok := revisionConflictResult(writeErr, "list_mcp_servers"); ok {
		return r
	}
	if r, ok := referenceSelectionRefusal(writeErr); ok {
		return r
	}
	if writeErr != nil {
		var inputErr *mcpSetupError
		if errors.As(writeErr, &inputErr) {
			return tools.ErrorResult(errorJSON(inputErr.code, inputErr.message, "Read list_mcp_servers before retrying"))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", writeErr.Error(), "Stored credential values were not changed"))
	}
	if !mat.unchanged {
		refs := make([]string, 0, len(mat.envRefs))
		for _, key := range sortedStringKeys(mat.envRefs) {
			refs = append(refs, mat.envRefs[key])
		}
		t.selected(ctx, t.Name(), mat.name, refs)
	}
	return mat.reconcileAndRespond()
}

// validateInput checks common input only. Creation defaults must never be
// applied until the live revision-locked entry is known to be absent.
func (mat *mcpAddToolExecute) validateInput() (*tools.ToolResult, bool) {
	mat.name, _ = mat.args["name"].(string)
	if mat.name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", "")), true
	}
	if err := validateID(mat.name); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid server name: "+err.Error(), "")), true
	}
	if raw, supplied := mat.args["env_refs"]; supplied {
		var err error
		mat.envRefs, err = readReferenceMap(raw)
		if err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "Use request_secret for the saved server")), true
		}
	}
	return nil, false
}

func (mat *mcpAddToolExecute) prepareEntry(cfg *config.Config) error {
	if cfg == nil {
		return &mcpSetupError{code: "SAVE_FAILED", message: "MCP configuration is unavailable"}
	}
	current, exists := cfg.Tools.MCP.Servers[mat.name]
	mat.attachment = exists
	var err error
	if exists {
		if len(mat.envRefs) == 0 {
			return &mcpSetupError{code: "ALREADY_EXISTS", message: "MCP server already exists; only a non-empty env_refs attachment is allowed"}
		}
		if err = mat.checkAttachmentDescriptor(current); err != nil {
			return err
		}
	} else if current, err = mat.createEntry(); err != nil {
		return err
	}
	entry, err := mat.boundEntry(current)
	if err != nil {
		return err
	}
	mat.entry, mat.transport = entry, mcpEffectiveTransport(entry)
	mat.unchanged = exists && maps.Equal(current.EnvRefs, entry.EnvRefs)
	mat.gatedGlobalDisabled = !cfg.Tools.MCP.Enabled
	return nil
}

func (mat *mcpAddToolExecute) persistConfig() error {
	return mat.t.deps.WithConfig(func(cfg *config.Config) error {
		if err := mat.ctx.Err(); err != nil {
			return err
		}
		if err := mat.prepareEntry(cfg); err != nil {
			return err
		}
		if cfg.Tools.MCP.Servers == nil {
			cfg.Tools.MCP.Servers = make(map[string]config.MCPServerConfig)
		}
		cfg.Tools.MCP.Servers[mat.name] = mat.entry
		if !mat.attachment {
			mat.enableNewServerIfFresh(cfg)
		}
		return nil
	})
}

// Only creation retains the existing fresh-install enable behavior. Attachment
// must preserve both the server's Enabled field and the global kill switch.
func (mat *mcpAddToolExecute) enableNewServerIfFresh(cfg *config.Config) {
	if cfg.Tools.MCP.Enabled {
		return
	}
	for name, server := range cfg.Tools.MCP.Servers {
		if name != mat.name && server.Enabled {
			return
		}
	}
	cfg.Tools.MCP.Enabled = true
	mat.flippedGlobalEnable, mat.gatedGlobalDisabled = true, false
}

// reconcileAndRespond reconciles the live server state and returns an honest connection result.
func (mat *mcpAddToolExecute) reconcileAndRespond() *tools.ToolResult {
	// Trigger live reconciliation so the server actually connects (and its
	// tools get registered into the per-agent and central registries) instead
	// of only being persisted to config.json. Nil in tests or when the
	// gateway hasn't wired live MCP reconciliation.
	var reconcileErr error
	reconcileAttempted := mat.t.deps.ReconcileMCP != nil && !mat.unchanged
	if reconcileAttempted {
		rctx, cancel := context.WithTimeout(mat.ctx, mcpReconcileTimeout)
		reconcileErr = mat.t.deps.ReconcileMCP(rctx)
		cancel()
		if reconcileErr != nil {
			slog.Warn("add_mcp_server: live MCP reconciliation failed", "server", mat.name, "error", reconcileErr)
		}
	}

	result := map[string]any{
		"name":           mat.name,
		"transport":      mat.transport,
		"enabled":        mat.entry.Enabled,
		"revision":       mcpServerRevision(mat.t.deps.GetCfg(), mat.name),
		"changed_fields": mat.changedFields(),
	}
	scope := "live connection: the server's status after reconciliation and the number of tools it registered"
	if mat.unchanged {
		scope = "existing live connection status read back; identical references were not changed or reconciled"
	}
	notTested := []string{"calling the server's tools", "tool policies (none are assigned yet)"}
	if mat.attachment {
		notTested[1] = "tool-policy assignments"
	}
	var note string
	var verdict changeOutcome
	if mat.t.deps.MCPStatus != nil {
		status, toolCount, errMsg := mat.t.deps.MCPStatus(mat.name)
		result["status"] = status
		switch status {
		case "connected":
			result["tool_count"] = toolCount
			note = fmt.Sprintf(
				"Server connected; %d tool(s) registered and awaiting tool-policy assignment.", toolCount)
			if mat.attachment {
				note = fmt.Sprintf("Server connected; %d tool(s) registered. Existing tool assignments and policies are unchanged.", toolCount)
			}
			verdict = working(scope, notTested...)
		case "error":
			// The config write DID succeed — only the live connection failed.
			note = fmt.Sprintf(
				"Server saved but connection failed: %s. Fix the config and try again.", errMsg)
			verdict = savedNotWorking("connection failed: "+errMsg, scope, notTested...)
		default: // "disconnected"
			note = "Server saved but not currently connected " +
				"(server disabled, or live reconciliation is unavailable)."
			verdict = savedNotWorking("the server is not connected", scope, notTested...)
		}
	} else if reconcileAttempted && reconcileErr != nil {
		// Reconciliation actually ran and failed, but there is no MCPStatus
		// readback to report a live status from.
		note = fmt.Sprintf(
			"Server saved but live reconciliation failed: %s. Connection status is unknown.", reconcileErr)
		verdict = savedNotWorking("live reconciliation failed: "+reconcileErr.Error(), "", "the live connection")
	} else {
		// Live status readback is not wired (tests, or a partially wired
		// gateway): the connection was not checked.
		note = "Server added to config. It connects on the next agent-loop reload."
		verdict = savedNotWorking("not checked: live connection status is unavailable", "", "the live connection")
	}

	// Surface the global-kill-switch outcome honestly.
	switch {
	case mat.flippedGlobalEnable:
		note += " Global MCP enable was off — turned on."
		result["changed_fields"] = append(mat.changedFields(), "tools.mcp.enabled")
	case mat.gatedGlobalDisabled:
		note += " MCP is globally disabled — an operator must enable tools.mcp.enabled for this server to connect."
		if verdict.Outcome == outcomeWorking {
			verdict = savedNotWorking("MCP is globally disabled", scope, notTested...)
		}
	}
	if !mat.entry.Enabled {
		note += " The saved server remains disabled."
		verdict = savedNotWorking("the saved server is disabled", scope, notTested...)
	}
	if !mat.attachment && len(mat.envRefs) == 0 {
		if verdict.Outcome != outcomeWorking {
			verdict = savedNotWorking("server saved without credential references; credential setup is not finished if this server needs a secret. "+verdict.Reason,
				verdict.CheckScope, verdict.NotTested...)
		}
		note += " No credential references were provided; request a secret card if this server needs credentials, then approve reference attachment."
	}
	if mat.unchanged {
		if mat.t.deps.MCPStatus == nil {
			note = "References already match; nothing was changed. Live connection status was not checked."
		} else {
			note = "References already match; nothing was changed. " + note
		}
	}
	result["note"] = note

	return changeSuccess(result, verdict)
}

// changedFields lists only the durable parts this call actually wrote.
func (mat *mcpAddToolExecute) changedFields() []string {
	if mat.unchanged {
		return []string{}
	}
	if mat.attachment {
		return []string{"env_refs"}
	}
	fields := []string{"mcp_server_entry"}
	if len(mat.envRefs) > 0 {
		fields = append(fields, "env_refs")
	}
	return fields
}

// ---- system.mcp.remove ----

type MCPRemoveTool struct{ deps *Deps }

func NewMCPRemoveTool(d *Deps) *MCPRemoveTool   { return &MCPRemoveTool{deps: d} }
func (t *MCPRemoveTool) Name() string           { return "remove_mcp_server" }
func (t *MCPRemoveTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *MCPRemoveTool) Description() string {
	return "Remove an MCP server from the configuration (tools.mcp.servers) and persist the change. " +
		"expected_revision is the server's revision from list_mcp_servers; the removal is refused if the server changed since.\n" +
		"Parameters: name (required), confirm (bool, must be true), expected_revision (required)."
}

func (t *MCPRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":    map[string]any{"type": "string"},
			"confirm": map[string]any{"type": "boolean"},
			expectedRevisionParam: map[string]any{
				"type":        "string",
				"description": "The server's revision from list_mcp_servers.",
			},
		},
		"required": []string{"name", "confirm", expectedRevisionParam},
	}
}

func (t *MCPRemoveTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
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
	expected, missing := expectedRevisionArg(args, "list_mcp_servers")
	if missing != nil {
		return missing
	}

	// removedEnvRefs captures the outgoing entry's EnvRefs (credential-store
	// keys, mcp_<name>_<envKey>) so they can be cleaned up AFTER the config
	// write succeeds — deleting the credential entries before the config
	// write is confirmed would risk destroying secrets for a removal that
	// then fails to persist.
	var removedEnvRefs map[string]string
	if err := t.deps.withSharedResourceWrite(sharedMCPServer, name, expected, func() error {
		return t.deps.WithConfig(func(cfg *config.Config) error {
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
		})
	}); err != nil {
		if r, ok := revisionConflictResult(err, "list_mcp_servers"); ok {
			return r
		}
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
	const scope = "live disconnect: reconciliation after the config removal"
	note := "Server removed from config. Its tools are unregistered on the next agent-loop reload."
	verdict := savedNotWorking("not checked: live reconciliation is unavailable", "", "the live disconnect")
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
			verdict = savedNotWorking("live disconnect not confirmed: "+reconcileErr.Error(), scope)
		} else {
			note = "Server disconnected and removed."
			verdict = working(scope)
		}
	}

	result := map[string]any{
		"name":           name,
		"removed":        true,
		"note":           note,
		"revision":       revisionAbsent,
		"changed_fields": []string{"mcp_server_entry"},
	}
	if credCleanupFailed {
		result["cred_cleanup_warning"] = "the server was removed but one or more of its stored env " +
			"credentials could not be deleted and may remain orphaned in the credential store"
		verdict = partlySaved("the server was removed but some of its stored env credentials could not be deleted",
			verdict.CheckScope, []string{"env credential deletion"}, verdict.NotTested...)
	}
	return changeSuccess(result, verdict)
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
	return map[string]any{"type": "object", "properties": map[string]any{
		"name": map[string]any{
			"type": "string",
			"description": "Optional exact server name. A configured server is returned alone; one that is not " +
				"configured comes back as {name, revision: \"absent\"} (FR-088).",
		},
	}}
}

func (t *MCPListTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	only, _ := args["name"].(string)
	only = strings.TrimSpace(only)
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
		// Revision is the FR-088 token remove_mcp_server's expected_revision
		// must carry (secret-free: names, transport, command/args or URL,
		// enabled state and credential reference names only).
		Revision string `json:"revision"`
	}
	servers := make([]serverInfo, 0, len(cfg.Tools.MCP.Servers))
	for name, srv := range cfg.Tools.MCP.Servers {
		if only != "" && name != only {
			continue
		}
		// Default inference matches the MCP manager: stdio if a command is
		// set, sse if a url is set.
		transport := mcpEffectiveTransport(srv)
		info := serverInfo{
			Name:      name,
			Transport: transport,
			Enabled:   srv.Enabled,
			Command:   srv.Command,
			URL:       srv.URL,
			Revision:  mcpServerRevision(cfg, name),
		}
		if t.deps.MCPStatus != nil {
			status, _, _ := t.deps.MCPStatus(name)
			info.Status = status
		}
		servers = append(servers, info)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	if only != "" && len(servers) == 0 {
		// FR-088: a named server that is not configured has the token "absent".
		return tools.NewToolResult(successJSON(map[string]any{
			"servers": []map[string]any{{"name": only, "revision": revisionAbsent}},
		}))
	}
	return tools.NewToolResult(successJSON(map[string]any{"servers": servers}))
}
