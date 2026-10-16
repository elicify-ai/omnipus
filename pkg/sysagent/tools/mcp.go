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
	"github.com/elicify-ai/omnipus/pkg/credentials"
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
			"env":       map[string]any{"type": "object"},
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
	command             string
	urlStr              string
	envRefs             map[string]string
	expectedRevision    string
	entry               config.MCPServerConfig
	flippedGlobalEnable bool
	gatedGlobalDisabled bool
}

func (t *MCPAddTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	mat := &mcpAddToolExecute{t: t, ctx: ctx, args: args}

	if r0, stop := mat.validateInput(); stop {
		return r0
	}
	expected, missing := expectedRevisionArg(args, "list_mcp_servers")
	if missing != nil {
		return missing
	}
	mat.expectedRevision = expected

	// FR-085: the duplicate check, the credential writes and the config write
	// all run under the gateway config write lock, after the revision check —
	// a refused or colliding add never writes a credential.
	var stopResult *tools.ToolResult
	writeErr := t.deps.withSharedResourceWrite(sharedMCPServer, mat.name, mat.expectedRevision, func() error {
		if r0, stop := mat.prepareEntry(); stop {
			stopResult = r0
			return nil
		}
		if r0, stop := mat.persistConfig(); stop {
			stopResult = r0
		}
		return nil
	})
	if r, ok := revisionConflictResult(writeErr, "list_mcp_servers"); ok {
		return r
	}
	if writeErr != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", writeErr.Error(), ""))
	}
	if stopResult != nil {
		return stopResult
	}
	return mat.reconcileAndRespond()
}

// validateInput validates the server name, transport, and transport-specific connection fields.
func (mat *mcpAddToolExecute) validateInput() (*tools.ToolResult, bool) {
	mat.name, _ = mat.args["name"].(string)
	if mat.name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", "")), true
	}
	// Reject path-traversal / separator characters in the server name — the name
	// is the map key in config and must be a clean identifier.
	if err := validateID(mat.name); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid server name: "+err.Error(), "")), true
	}

	mat.transport, _ = mat.args["transport"].(string)
	if mat.transport == "" {
		mat.transport = "stdio"
	}
	switch mat.transport {
	case "stdio", "sse", "http":
	default:
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("invalid transport %q: must be one of stdio, sse, http", mat.transport), "")), true
	}

	mat.command, _ = mat.args["command"].(string)
	mat.urlStr, _ = mat.args["url"].(string)
	switch mat.transport {
	case "stdio":
		if mat.command == "" {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"command is required for stdio transport", "")), true
		}
	case "sse", "http":
		if mat.urlStr == "" {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"url is required for sse/http transport", "")), true
		}
		if !mcpURLSchemeValid(mat.urlStr) {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"url must use https, or http for loopback addresses only (localhost, 127.x.x.x, ::1)", "")), true
		}
	}
	return nil, false
}

// prepareEntry checks for duplicates, stores environment credentials, and builds the persisted server entry.
func (mat *mcpAddToolExecute) prepareEntry() (*tools.ToolResult, bool) {
	argList := stringSliceArg(mat.args["args"])
	envMap := stringMapArg(mat.args["env"])

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
	if cfg := mat.t.deps.GetCfg(); cfg != nil {
		if _, exists := cfg.Tools.MCP.Servers[mat.name]; exists {
			return tools.ErrorResult(errorJSON("ALREADY_EXISTS",
				fmt.Sprintf("mcp server %q already exists", mat.name),
				"Use list_mcp_servers to see existing servers, or remove it first with remove_mcp_server")), true
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
	// (some keys stored, one fails) leaves those credential entries stored and
	// no config write — no dangling ref is possible — and the result reports
	// them as "partly saved" (FR-030).

	if len(envMap) > 0 {
		if mat.t.deps.CredStore == nil {
			return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
				"credential store is not available",
				"Ensure the credential store is unlocked before adding an MCP server with env values",
			)), true
		}
		mat.envRefs = make(map[string]string, len(envMap))
		for key, value := range envMap {
			credKey := mcpEnvCredKey(mat.name, key)
			if err := mcpCredentialSet(mat.t.deps.CredStore, credKey, value); err != nil {
				msg := fmt.Sprintf("Failed to store env credential %q: %s", key, err.Error())
				if len(mat.envRefs) == 0 {
					return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED", msg,
						"Check that the credential store is unlocked",
					)), true
				}
				// Earlier env credentials of this call ARE stored: a durable
				// side effect, so the result is "partly saved", naming them.
				return mat.partialCredentialResult("CREDENTIAL_SAVE_FAILED", msg,
					"some env credentials were stored before credential "+key+" failed; the server entry was not saved",
					mat.storedCredentialRefs(), []string{"env credential " + key, "mcp server entry"}), true
			}
			mat.envRefs[key] = credKey
		}
	}

	// Phase 2: persist the config entry. Env is deliberately left unset here —
	// EnvRefs is the only place a value added through this tool lands. The
	// real values are resolved back from the credential store at connect
	// time (pkg/mcp.ResolveServerEnvRefs, pkg/agent/loop_mcp.go's
	// reconcileLocked) so the plaintext secret exists only in memory, only
	// for the duration of a connect attempt.
	mat.entry = config.MCPServerConfig{
		Enabled: true,
		Type:    mat.transport,
		Command: mat.command,
		URL:     mat.urlStr,
		Args:    argList,
		EnvRefs: mat.envRefs,
	}
	return nil, false
}

// persistConfig persists the server and handles global-enable and concurrent-name outcomes.
func (mat *mcpAddToolExecute) persistConfig() (*tools.ToolResult, bool) {
	// flippedGlobalEnable/gatedGlobalDisabled record which of the two
	// auto-enable outcomes below happened, so the result note can be honest
	// about it (set inside the WithConfig closure, read after it returns).

	if err := mat.t.deps.WithConfig(func(cfg *config.Config) error {
		if cfg.Tools.MCP.Servers == nil {
			cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
		}
		if _, exists := cfg.Tools.MCP.Servers[mat.name]; exists {
			return fmt.Errorf("mcp server %q already exists", mat.name)
		}
		cfg.Tools.MCP.Servers[mat.name] = mat.entry
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
				if otherName == mat.name {
					continue
				}
				if otherSrv.Enabled {
					otherEnabled = true
					break
				}
			}
			if otherEnabled {
				mat.gatedGlobalDisabled = true
			} else {
				cfg.Tools.MCP.Enabled = true
				mat.flippedGlobalEnable = true
			}
		}
		return nil
	}); err != nil {
		// Any env credentials written in Phase 1 belong to a request that is
		// being rejected (a name-collision race or a save failure): remove
		// them so the failure stores nothing and never hijacks the
		// credentials of a same-named server.
		if rollbackFailed := mat.rollbackEnvCredentials(); len(rollbackFailed) > 0 {
			return mat.partialCredentialResult("SAVE_FAILED", "the server was not saved: "+err.Error(),
				"the server entry was not saved, but stored env credentials could not be removed: "+
					strings.Join(rollbackFailed, ", "), rollbackFailed, []string{"mcp server entry"}), true
		}
		if strings.Contains(err.Error(), "already exists") {
			return tools.ErrorResult(errorJSON("ALREADY_EXISTS", err.Error(),
				"Use list_mcp_servers to see existing servers, or remove it first with remove_mcp_server")), true
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), "")), true
	}
	return nil, false
}

// partialCredentialResult is the "partly saved" refusal of an add whose server
// entry was not saved while env credentials (stored, named by reference, never
// by value) remain durable.
func (mat *mcpAddToolExecute) partialCredentialResult(code, msg, reason string, stored, missing []string) *tools.ToolResult {
	return changeErrorWithOutcome(map[string]any{
		"success":            false,
		"error":              map[string]any{"code": code, "message": msg},
		"name":               mat.name,
		"changed_fields":     []string{"env_credentials"},
		"stored_credentials": stored,
	}, partlySaved(reason, "", missing))
}

// storedCredentialRefs lists, sorted, the credential references this call has
// stored so far.
func (mat *mcpAddToolExecute) storedCredentialRefs() []string {
	refs := make([]string, 0, len(mat.envRefs))
	for _, ref := range mat.envRefs {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

// rollbackEnvCredentials deletes the env credentials this call stored and
// returns the credential keys it could not delete (each also logged).
func (mat *mcpAddToolExecute) rollbackEnvCredentials() []string {
	var failed []string
	for envKey, credKey := range mat.envRefs {
		if mat.t.deps.CredStore == nil {
			failed = append(failed, credKey)
			continue
		}
		if delErr := mcpCredentialDelete(mat.t.deps.CredStore, credKey); delErr != nil {
			slog.Warn("add_mcp_server: failed to roll back env credential",
				"server", mat.name, "env_key", envKey, "cred_key", credKey, "error", delErr)
			failed = append(failed, credKey)
		}
	}
	sort.Strings(failed)
	return failed
}

// reconcileAndRespond reconciles the live server state and returns an honest connection result.
func (mat *mcpAddToolExecute) reconcileAndRespond() *tools.ToolResult {
	// Trigger live reconciliation so the server actually connects (and its
	// tools get registered into the per-agent and central registries) instead
	// of only being persisted to config.json. Nil in tests or when the
	// gateway hasn't wired live MCP reconciliation.
	var reconcileErr error
	reconcileAttempted := mat.t.deps.ReconcileMCP != nil
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
		"enabled":        true,
		"revision":       mcpServerRevision(mat.t.deps.GetCfg(), mat.name),
		"changed_fields": mat.changedFields(),
	}
	const scope = "live connection: the server's status after reconciliation and the number of tools it registered"
	notTested := []string{"calling the server's tools", "tool policies (none are assigned yet)"}
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
	result["note"] = note

	return changeSuccess(result, verdict)
}

// changedFields lists the durable parts add_mcp_server wrote.
func (mat *mcpAddToolExecute) changedFields() []string {
	fields := []string{"mcp_server_entry"}
	if len(mat.envRefs) > 0 {
		fields = append(fields, "env_credentials")
	}
	return fields
}

// mcpCredentialSet stores one add_mcp_server env credential. A variable only so
// a test can fail a later write after an earlier one succeeded
// (FailMCPCredentialWriteAfterForTest); production never reassigns it.
var mcpCredentialSet = func(store *credentials.Store, name, value string) error {
	return store.Set(name, value)
}

// mcpCredentialDelete removes one env credential during add_mcp_server's
// rollback. A variable only so a test can fail the rollback deterministically
// (FailMCPCredentialRollbackForTest); production never reassigns it.
var mcpCredentialDelete = func(store *credentials.Store, name string) error {
	return store.Delete(name)
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
