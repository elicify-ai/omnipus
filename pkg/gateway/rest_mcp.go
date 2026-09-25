// rest_mcp.go: MCP server CRUD, live status, tools, and connectivity test

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/mcp"
)

// --- MCP Servers ---

// HandleMCPServers handles GET/POST /api/v1/mcp-servers and DELETE /api/v1/mcp-servers/{id}.
// GET returns McpServer[] shaped from cfg.Tools.MCP.Servers (contracts/components/schemas/McpServer.yaml).
// POST accepts McpServerCreate (contracts/components/schemas/McpServerCreate.yaml) and
// returns the newly created McpServer entry.
func (a *restAPI) HandleMCPServers(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/mcp-servers")
	sub = strings.TrimPrefix(sub, "/")

	// Determine if path is /{id}/tools.
	parts := strings.SplitN(sub, "/", 2)
	serverID := parts[0]
	subSuffix := ""
	if len(parts) == 2 {
		subSuffix = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && sub == "":
		a.listMCPServers(w, r)

	case r.Method == http.MethodPost && sub == "":
		a.addMCPServer(w, r)

	case r.Method == http.MethodDelete && sub != "" && subSuffix == "":
		a.deleteMCPServer(w, r, serverID)

	case r.Method == http.MethodGet && serverID != "" && subSuffix == "tools":
		a.listMCPServerTools(w, serverID)

	case r.Method == http.MethodPost && serverID != "" && subSuffix == "test":
		a.testMCPServer(w, r, serverID)

	case r.Method == http.MethodPatch && serverID != "" && subSuffix == "":
		a.patchMCPServer(w, r, serverID)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// mcpLiveStatus maps AgentLoop.MCPServerStatus's live status string
// ("connected"|"error"|"disconnected") to the generated McpServerStatus enum,
// alongside the live tool_count (entries this server has in the central MCP
// registry; 0 when unset or not connected). addMCPServer, patchMCPServer, and
// listMCPServers all derive status/tool_count through this single path so the
// three endpoints never disagree on what "connected" means.
func (a *restAPI) mcpLiveStatus(name string) (gen.McpServerStatus, int) {
	status, toolCount, _ := a.agentLoop.MCPServerStatus(name)
	switch status {
	case "connected":
		return gen.McpServerStatusConnected, toolCount
	case "error":
		return gen.McpServerStatusError, toolCount
	default:
		return gen.McpServerStatusDisconnected, toolCount
	}
}

// listMCPServers reads configured MCP servers from config and returns them as
// McpServer[] (contracts/components/schemas/McpServer.yaml). G6: status comes from
// mcpLiveStatus (AgentLoop.MCPServerStatus), the SAME live-reconciliation state
// addMCPServer/patchMCPServer/deleteMCPServer read back after a config write — a
// server actually connected via the live manager reports "connected", a server
// that failed to connect reports "error", and anything else (disabled,
// reconciliation never ran) reports "disconnected". tool_count is sourced from
// a.mcpRegistry directly (matching GET /mcp-servers/{id}/tools) rather than
// mcpLiveStatus's own tool_count — see the inline comment below. tools (sorted
// tool names) is populated from the same a.mcpRegistry pass, and omitted when
// the server has no registered tools.
func (a *restAPI) listMCPServers(w http.ResponseWriter, _ *http.Request) {
	// MCPServersSnapshot (not GetConfig().Tools.MCP.Servers) — ranging the live
	// map directly races the sysagent config-mutation path, which mutates it
	// in place while holding the agent loop's write lock.
	servers := a.agentLoop.MCPServersSnapshot()
	result := make([]gen.McpServer, 0, len(servers))
	for name, srv := range servers {
		transport := gen.McpServerTransportStdio
		switch srv.Type {
		case "sse":
			transport = gen.McpServerTransportSse
		case "http":
			transport = gen.McpServerTransportHttp
		}
		enabled := srv.Enabled

		// Status comes from the same live-reconciliation state add/patch/delete
		// read back (mcpLiveStatus), but tool_count is sourced from a.mcpRegistry
		// directly — as before this fix — rather than mcpLiveStatus's own
		// tool_count (which reads the AgentLoop's central registry). In production
		// these are the SAME registry instance (gateway.go wires both to
		// centralMCPReg), but keeping this path independent matches the existing
		// GET /mcp-servers/{id}/tools contract, which also reads a.mcpRegistry.
		status, _ := a.mcpLiveStatus(name)
		toolCount := 0
		var toolNames []string
		if a.mcpRegistry != nil {
			for _, e := range a.mcpRegistry.Describe() {
				if e.ServerID == name {
					toolCount++
					toolNames = append(toolNames, e.Name)
				}
			}
		}

		entry := gen.McpServer{
			Id:        name,
			Name:      name,
			Transport: transport,
			Status:    status,
			ToolCount: toolCount,
			Enabled:   &enabled,
		}
		if len(toolNames) > 0 {
			sort.Strings(toolNames)
			entry.Tools = &toolNames
		}
		// Non-secret config fields for edit pre-fill (#437).
		if srv.Command != "" {
			c := srv.Command
			entry.Command = &c
		}
		if srv.URL != "" {
			u := srv.URL
			entry.Url = &u
		}
		if len(srv.Args) > 0 {
			a := append([]string(nil), srv.Args...)
			entry.Args = &a
		}
		if srv.EnvFile != "" {
			ef := srv.EnvFile
			entry.EnvFile = &ef
		}
		// env/headers: return KEYS ONLY — values may be secrets (Authorization, API keys).
		// Union srv.Env (legacy plaintext) with srv.EnvRefs (credential-store-backed,
		// the path add_mcp_server/addMCPServer route every new secret through) — a
		// server added via either of those only ever populates EnvRefs, so reading
		// srv.Env alone (BUG 2) made such a server appear to have NO environment
		// configuration at all. A key present in both is reported once.
		if len(srv.Env) > 0 || len(srv.EnvRefs) > 0 {
			keySet := make(map[string]struct{}, len(srv.Env)+len(srv.EnvRefs))
			for k := range srv.Env {
				keySet[k] = struct{}{}
			}
			for k := range srv.EnvRefs {
				keySet[k] = struct{}{}
			}
			keys := make([]string, 0, len(keySet))
			for k := range keySet {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			entry.EnvKeys = &keys
		}
		// Issue #638: names from BOTH sources — literal Headers (pre-#638
		// install) and HeaderRefs (credential-store-backed, everything the
		// REST create/patch path has written since) — are reported, so a
		// ref-backed server still shows its header configuration in the UI
		// edit pre-fill. Names only; values never cross the wire either way.
		if len(srv.Headers) > 0 || len(srv.HeaderRefs) > 0 {
			nameSet := make(map[string]struct{}, len(srv.Headers)+len(srv.HeaderRefs))
			for k := range srv.Headers {
				nameSet[k] = struct{}{}
			}
			for k := range srv.HeaderRefs {
				nameSet[k] = struct{}{}
			}
			names := make([]string, 0, len(nameSet))
			for k := range nameSet {
				names = append(names, k)
			}
			sort.Strings(names)
			entry.HeaderNames = &names
		}
		result = append(result, entry)
	}
	// Sort for deterministic response order.
	sort.Slice(result, func(i, j int) bool { return result[i].Id < result[j].Id })
	jsonOK(w, result)
}

// listMCPServerTools handles GET /api/v1/mcp-servers/{id}/tools.
// Returns the tool names registered by the given MCP server via McpServerToolsResponse.
// If the server has no registered tools (e.g. not yet connected), returns an empty list.
// 404 if serverID is not present in the config.
func (a *restAPI) listMCPServerTools(w http.ResponseWriter, serverID string) {
	// MCPServersSnapshot (not GetConfig().Tools.MCP.Servers) — indexing the
	// live map directly races the sysagent config-mutation path, which
	// mutates it in place while holding the agent loop's write lock.
	servers := a.agentLoop.MCPServersSnapshot()
	if _, exists := servers[serverID]; !exists {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("mcp server %q not found", serverID))
		return
	}
	toolNames := make([]string, 0)
	if a.mcpRegistry != nil {
		for _, entry := range a.mcpRegistry.Describe() {
			if entry.ServerID == serverID {
				toolNames = append(toolNames, entry.Name)
			}
		}
	}
	sort.Strings(toolNames)
	jsonOK(w, gen.McpServerToolsResponse{Tools: toolNames})
}

// addMCPServer handles POST /api/v1/mcp-servers.
// Accepts McpServerCreate (contracts/components/schemas/McpServerCreate.yaml).
// Transport must be one of: stdio, sse, http (enforced by enum validation).
// After the config write, live-reconciles the MCP manager (AgentLoop.ReconcileMCP)
// so the server actually connects and its tools register before the response is
// built — status/tool_count reflect the real outcome, not a hardcoded placeholder.
// Returns the new McpServer entry shaped per contracts/components/schemas/McpServer.yaml.
func (a *restAPI) addMCPServer(w http.ResponseWriter, r *http.Request) {
	var req gen.McpServerCreate
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "McpServerCreate", &req, validateEnabled) {
		return
	}
	if req.Name == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if err := validateEntityID(req.Name); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid server name")
		return
	}
	transport := string(req.Transport)
	if transport == "" {
		transport = "stdio"
	}
	// Validate transport against the contract enum (stdio | sse | http).
	// The "websocket" value is not supported — reject it early rather than storing
	// a config entry that the MCP manager will refuse at connection time.
	switch transport {
	case "stdio", "sse", "http":
		// valid
	default:
		jsonErr(
			w,
			http.StatusBadRequest,
			fmt.Sprintf("invalid transport %q: must be one of stdio, sse, http", transport),
		)
		return
	}
	// Per-transport field validation: stdio requires command; sse/http require url.
	// The MCP manager (pkg/mcp/manager.go ConnectServer) hard-fails on missing
	// cfg.URL for sse/http and missing cfg.Command for stdio — catch it here so the
	// error surfaces as a 422 rather than a silent connection failure.
	switch transport {
	case "stdio":
		if req.Command == nil || *req.Command == "" {
			jsonErr(w, http.StatusUnprocessableEntity, "command is required for stdio transport")
			return
		}
	case "sse", "http":
		if req.Url == nil || *req.Url == "" {
			jsonErr(w, http.StatusUnprocessableEntity, "url is required for sse/http transport")
			return
		}
		// Mirror SPA isValidUrlScheme: https always accepted; http only for
		// loopback (localhost, 127.x.x.x, ::1). Any other http:// URL is
		// rejected so the SPA validation cannot be bypassed via direct API call.
		if !mcpURLSchemeValid(*req.Url) {
			jsonErr(w, http.StatusUnprocessableEntity,
				"url must use https, or http for loopback addresses only (localhost, 127.x.x.x, ::1)")
			return
		}
	}
	// Duplicate-name pre-check, BEFORE any credential-store write (mirrors the
	// add_mcp_server tool fix, pkg/sysagent/tools/mcp.go): without this, a name
	// collision was only discovered inside the config-write closure below,
	// AFTER every env value had already been written to the credential store
	// under mcp_<name>_<key> — silently overwriting the existing server's live
	// credentials with the (rejected) request's values. This read is a
	// snapshot, not a lock — the in-closure check further down stays as the
	// authoritative concurrency guard for the race window between this check
	// and the write.
	if _, exists := a.agentLoop.MCPServersSnapshot()[req.Name]; exists {
		jsonErr(w, http.StatusConflict, fmt.Sprintf("mcp server %q already exists", req.Name))
		return
	}
	// Route env secrets into the encrypted credential store instead of
	// writing them into config.json in plaintext — the same mechanism
	// add_mcp_server (the tool) already uses (mcpEnvCredKey, "mcp_<name>_<key>").
	// A partial failure here (some keys stored, one fails) leaves orphaned but
	// harmless credential entries and no config write — no dangling ref is
	// possible.
	var envRefs map[string]string
	if req.Env != nil && len(*req.Env) > 0 {
		envRefs = make(map[string]string, len(*req.Env))
		for key, value := range *req.Env {
			credKey := mcpEnvCredKey(req.Name, key)
			if _, err := a.storeCredential(credKey, value); err != nil {
				slog.Error("rest: add mcp server: store env credential", "server", req.Name, "env_key", key, "error", err)
				jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not store env credential %q: %v", key, err))
				return
			}
			envRefs[key] = credKey
		}
	}
	// Issue #638: header values (Authorization, Proxy-Authorization, Cookie,
	// and any other request header the operator sets) are credentials — they
	// go to the encrypted credential store exactly like env values above, and
	// only their NAMES land in config.json, under header_refs. The literal
	// headers block this used to write (`entry["headers"] = *req.Headers`) is
	// gone: persisting the values there was the #638 leak. Same partial-
	// failure semantics as env: a mid-loop store failure leaves earlier
	// entries stored but harmless (no config write happens) and 500s.
	var headerRefs map[string]string
	if req.Headers != nil && len(*req.Headers) > 0 {
		headerRefs = make(map[string]string, len(*req.Headers))
		for key, value := range *req.Headers {
			credKey := mcpHeaderCredKey(req.Name, key)
			if _, err := a.storeCredential(credKey, value); err != nil {
				slog.Error("rest: add mcp server: store header credential", "server", req.Name, "header", key, "error", err)
				jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not store header credential %q: %v", key, err))
				return
			}
			headerRefs[key] = credKey
		}
	}
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		tools, _ := m["tools"].(map[string]any)
		if tools == nil {
			tools = map[string]any{}
			m["tools"] = tools
		}
		mcp, _ := tools["mcp"].(map[string]any)
		if mcp == nil {
			mcp = map[string]any{}
			tools["mcp"] = mcp
		}
		servers, _ := mcp["servers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			mcp["servers"] = servers
		}
		if _, exists := servers[req.Name]; exists {
			return fmt.Errorf("mcp server %q already exists", req.Name)
		}
		// Adding a server is explicit operator intent to use MCP — flip the global
		// kill-switch on in the same write so ReconcileMCP's desired set isn't
		// forced empty by a still-false tools.mcp.enabled (default on fresh
		// installs). PATCH/delete deliberately do NOT touch this flag: turning
		// MCP off globally is a separate, explicit action.
		mcp["enabled"] = true
		entry := map[string]any{
			"enabled": true,
			"type":    transport,
		}
		// Write the correct config field for each transport so the MCP manager
		// (pkg/mcp/manager.go ConnectServer) can connect: stdio uses cfg.Command,
		// sse/http use cfg.URL.
		switch transport {
		case "stdio":
			entry["command"] = *req.Command
		case "sse", "http":
			entry["url"] = *req.Url
		}
		if req.Args != nil && len(*req.Args) > 0 {
			entry["args"] = *req.Args
		}
		// Env is deliberately never persisted here — envRefs (credential-store
		// references, resolved to real values only in memory at connect time by
		// pkg/mcp.ResolveServerEnvRefs) is the only place a value from this
		// request's env lands. This keeps addMCPServer's on-disk shape
		// identical to add_mcp_server's (the tool).
		if len(envRefs) > 0 {
			entry["env_refs"] = envRefs
		}
		if req.EnvFile != nil && *req.EnvFile != "" {
			entry["env_file"] = *req.EnvFile
		}
		// Issue #638: only the ref NAMES are persisted; the values live in
		// the encrypted credential store (written above the closure). The
		// literal `entry["headers"] = *req.Headers` write is deleted — that
		// plaintext persistence was the leak the issue closes.
		if len(headerRefs) > 0 {
			entry["header_refs"] = headerRefs
		}
		servers[req.Name] = entry
		return nil
	}); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			// Race-guard fired: a same-named server was created concurrently,
			// between the pre-check above and this write. Roll back any env
			// credentials this (rejected) request wrote above — leaving them
			// in place would hijack the credentials of the server that won
			// the race, exactly the bug this fix closes.
			for envKey, credKey := range envRefs {
				if delErr := a.removeStoredCredential(credKey); delErr != nil {
					slog.Warn("rest: add mcp server: name-collision race — failed to roll back env credential",
						"server", req.Name, "env_key", envKey, "cred_key", credKey, "error", delErr)
				}
			}
			// Same rollback for #638's header credentials: a rejected create
			// must not leave its header secrets stored under the winning
			// server's deterministic keys.
			for headerName, credKey := range headerRefs {
				if delErr := a.removeStoredCredential(credKey); delErr != nil {
					slog.Warn("rest: add mcp server: name-collision race — failed to roll back header credential",
						"server", req.Name, "header", headerName, "cred_key", credKey, "error", delErr)
				}
			}
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		slog.Error("rest: add mcp server", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// Config write succeeded — reconcile the live manager so the server actually
	// connects instead of sitting "disconnected" until a full gateway
	// restart. Best-effort: a reconcile failure/timeout does not undo the config
	// write or fail the request — the operator can retry via PATCH/reload, and the
	// response status below honestly reflects whatever the live state ended up as.
	rctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	if err := a.agentLoop.ReconcileMCP(rctx); err != nil {
		slog.Warn("rest: add mcp server: live reconcile failed", "server", req.Name, "error", err)
	}
	cancel()
	// Map the transport string to the generated enum value for the response.
	var respTransport gen.McpServerTransport
	switch transport {
	case "sse":
		respTransport = gen.McpServerTransportSse
	case "http":
		respTransport = gen.McpServerTransportHttp
	default:
		respTransport = gen.McpServerTransportStdio
	}
	status, toolCount := a.mcpLiveStatus(req.Name)
	resp := gen.McpServer{
		Id:        req.Name,
		Name:      req.Name,
		Transport: respTransport,
		Status:    status,
		ToolCount: toolCount,
	}
	jsonCreated(w, resp)
}

// mcpURLSchemeValid reports whether rawURL is acceptable for an sse/http MCP
// server endpoint. It mirrors the SPA's isValidUrlScheme function
// (src/components/skills/McpServerModal.tsx) so the contract described in
// McpServerCreate.yaml is enforced server-side and cannot be bypassed via
// direct API calls.
//
// Rules:
//   - https:// is always accepted.
//   - http:// is accepted only for loopback hosts: "localhost", any 127.x.x.x
//     address, or "::1" / "[::1]".
//   - Any other scheme (http:// to a public host, ws://, ftp://, etc.) is rejected.
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

// deleteMCPServer handles DELETE /api/v1/mcp-servers/{id}. Removes the server
// from config, then live-reconciles the MCP manager (AgentLoop.ReconcileMCP) so
// a connected server is actually disconnected and its tools evicted from the
// central/per-agent registries, rather than lingering live until restart.
func (a *restAPI) deleteMCPServer(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	found := false
	// removedEnvRefs captures the outgoing entry's EnvRefs (credential-store
	// keys, mcp_<name>_<envKey>) so they can be cleaned up AFTER the config
	// write succeeds — mirrors remove_mcp_server's (the tool) own ordering:
	// deleting the credential entries before the config write is confirmed
	// would risk destroying secrets for a removal that then fails to persist.
	var removedEnvRefs map[string]string
	// Issue #638: the server's ref-backed header secrets are cleaned up
	// exactly like its env secrets below — after the config write confirms.
	var removedHeaderRefs map[string]string
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		tools, _ := m["tools"].(map[string]any)
		if tools == nil {
			return nil
		}
		mcp, _ := tools["mcp"].(map[string]any)
		if mcp == nil {
			return nil
		}
		servers, _ := mcp["servers"].(map[string]any)
		if servers == nil {
			return nil
		}
		if existing, exists := servers[id]; exists {
			// Round-trip through the typed struct to pull out EnvRefs cleanly
			// (mirrors patchMCPServer's own existing-entry round-trip).
			if raw, mErr := json.Marshal(existing); mErr == nil {
				var current config.MCPServerConfig
				if uErr := json.Unmarshal(raw, &current); uErr == nil {
					removedEnvRefs = current.EnvRefs
					removedHeaderRefs = current.HeaderRefs
				}
			}
			delete(servers, id)
			found = true
		}
		return nil
	}); err != nil {
		slog.Error("rest: delete mcp server", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	if !found {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("mcp server %q not found", id))
		return
	}
	// Config write succeeded — clean up any credential-store entries this
	// server's env values were stored under (BUG 2 fix). Without this, every
	// server added via add_mcp_server/addMCPServer (which only ever populate
	// EnvRefs, never plaintext Env) left its secrets permanently orphaned in
	// the credential store on delete — remove_mcp_server (the tool) already
	// does this cleanup; the REST path did not. Best-effort: a cleanup
	// failure does not undo the already-committed config removal, it only
	// means an orphaned credential-store entry survives under a name nothing
	// references any more.
	for envKey, credKey := range removedEnvRefs {
		if err := a.removeStoredCredential(credKey); err != nil {
			slog.Warn("rest: delete mcp server: failed to delete env credential",
				"server", id, "env_key", envKey, "cred_key", credKey, "error", err)
		}
	}
	// Issue #638: same cleanup for the ref-backed header secrets.
	for headerName, credKey := range removedHeaderRefs {
		if err := a.removeStoredCredential(credKey); err != nil {
			slog.Warn("rest: delete mcp server: failed to delete header credential",
				"server", id, "header", headerName, "cred_key", credKey, "error", err)
		}
	}
	// Config write succeeded — reconcile the live manager so the removed server is
	// actually disconnected (DisconnectServer) and its tools evicted from the
	// central/per-agent registries, rather than lingering connected until restart.
	rctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	if err := a.agentLoop.ReconcileMCP(rctx); err != nil {
		slog.Warn("rest: delete mcp server: live reconcile failed", "server", id, "error", err)
	}
	cancel()
	jsonOK(w, map[string]string{"status": "removed", "id": id})
}

// testMCPServer handles POST /api/v1/mcp-servers/{id}/test (G7).
// Opens a temporary MCP manager, attempts to connect to the configured server,
// and reports the result, then closes the temporary manager. The test
// connection itself changes no persistent state beyond the heal described
// below. Returns McpServerTestResponse: success=true on live connection,
// success=false (HTTP 200) when the server is unreachable or misconfigured
// (including a relative env_file the gateway cannot resolve).
//
// Heal on success: a successful test proves the server is reachable, so if it
// is enabled in config, the global tools.mcp.enabled kill-switch is ALSO on,
// and it is not yet connected in the live manager (e.g. it failed to connect
// at boot, or was added before the kill-switch was flipped on), this triggers
// a real AgentLoop.ReconcileMCP pass to bring the live state in line with what
// the test just proved works — a manual "Test" click on a stuck server
// doubles as an unstick, instead of leaving the operator to separately toggle
// enabled off/on to force a reconnect. When the global flag is off, no
// reconcile can bring the server live (ReconcileMCP's desired set is forced
// empty), so the success message says so instead of silently doing nothing.
// This is the ONLY state change the endpoint makes, and only follows from
// success; a failed test never triggers reconciliation.
func (a *restAPI) testMCPServer(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	// MCPServersSnapshot (not GetConfig().Tools.MCP.Servers) — ranging/indexing
	// the live map directly races the sysagent config-mutation path, which
	// mutates it in place while holding the agent loop's write lock.
	servers := a.agentLoop.MCPServersSnapshot()
	srv, exists := servers[id]
	if !exists {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("mcp server %q not found", id))
		return
	}

	// Resolve a relative env_file against the same workspace path production
	// reconciliation uses, so the throwaway test connection sees the same
	// environment a real connect would — otherwise "Test" could report success
	// for a server that fails to actually connect once reconciled (or vice
	// versa). A resolution error is a test failure, not a 500: it is exactly
	// the misconfiguration the test button exists to surface.
	resolvedSrv, err := mcp.ResolveServerEnvFile(srv, a.agentLoop.MCPWorkspacePath())
	if err != nil {
		jsonOK(w, gen.McpServerTestResponse{
			Success: false,
			Message: fmt.Sprintf("env_file: %s", err.Error()),
		})
		return
	}
	// Resolve any credential-store env refs the same way production
	// reconciliation does (pkg/agent/loop_mcp.go's reconcileLocked) — a
	// server added via add_mcp_server carries EnvRefs, not literal Env, so
	// without this the throwaway connection would report a misleading failure.
	if a.credStore != nil {
		resolvedSrv, err = mcp.ResolveServerEnvRefs(resolvedSrv, a.credStore.Get)
	} else {
		resolvedSrv, err = mcp.ResolveServerEnvRefs(resolvedSrv, nil)
	}
	if err != nil {
		jsonOK(w, gen.McpServerTestResponse{
			Success: false,
			Message: fmt.Sprintf("env credential reference: %s", err.Error()),
		})
		return
	}
	// Issue #638: resolve credential-store header refs the same way — an
	// sse/http server carries its Authorization/Cookie/Proxy-Authorization
	// as HeaderRefs, and a Test click must exercise the connection exactly
	// as production would make it.
	if a.credStore != nil {
		resolvedSrv, err = mcp.ResolveServerHeaderRefs(resolvedSrv, a.credStore.Get)
	} else {
		resolvedSrv, err = mcp.ResolveServerHeaderRefs(resolvedSrv, nil)
	}
	if err != nil {
		jsonOK(w, gen.McpServerTestResponse{
			Success: false,
			Message: fmt.Sprintf("header credential reference: %s", err.Error()),
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpMgr := mcp.NewManager()
	defer func() {
		if err := tmpMgr.Close(); err != nil {
			slog.Warn("rest: test mcp server: close temp manager", "id", id, "error", err)
		}
	}()

	if err := tmpMgr.ConnectServer(ctx, id, resolvedSrv); err != nil {
		resp := gen.McpServerTestResponse{
			Success: false,
			Message: fmt.Sprintf("connection failed: %s", err.Error()),
		}
		jsonOK(w, resp)
		return
	}

	conn, ok := tmpMgr.GetServer(id)
	if !ok {
		resp := gen.McpServerTestResponse{
			Success: false,
			Message: "connected but server entry missing",
		}
		jsonOK(w, resp)
		return
	}

	toolCount := len(conn.Tools)
	toolNames := make([]string, toolCount)
	for i, t := range conn.Tools {
		toolNames[i] = t.Name
	}
	sort.Strings(toolNames)

	msg := fmt.Sprintf("connected successfully; %d tool(s) available", toolCount)

	// Heal: the throwaway connection just proved the server is reachable. If
	// config still has it enabled, the global kill-switch is on, and the LIVE
	// manager doesn't have it up (status != "connected"), run a real reconcile
	// so this success is not wasted — the operator's next GET reflects a
	// genuinely connected server instead of "disconnected"/"error" despite the
	// test that just passed. When the global flag is off, ReconcileMCP's
	// desired set would be empty regardless of this server's own Enabled bit,
	// so a reconcile here would be a silent no-op — skip it and say so instead.
	if srv.Enabled {
		if !a.agentLoop.GetConfig().Tools.MCP.Enabled {
			msg += " (MCP is globally disabled — enable tools.mcp.enabled to connect)"
		} else if status, _, _ := a.agentLoop.MCPServerStatus(id); status != "connected" {
			rctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			if err := a.agentLoop.ReconcileMCP(rctx); err != nil {
				slog.Warn("rest: test mcp server: heal reconcile failed", "id", id, "error", err)
			}
			cancel()
		}
	}

	resp := gen.McpServerTestResponse{
		Success:   true,
		Message:   msg,
		ToolCount: &toolCount,
		Tools:     &toolNames,
	}
	jsonOK(w, resp)
}

// errMCPNotFound is the sentinel a config-mutating closure returns when the
// requested MCP server id is absent, letting callers map it to 404 via errors.Is
// while leaving real config I/O / parse failures to surface as 500.
var errMCPNotFound = errors.New("mcp server not found")

// patchMCPServer handles PATCH /api/v1/mcp-servers/{id} (G8).
// Merges the provided (non-nil) fields from McpServerUpdate into the stored config
// entry; omitted fields are preserved. Validates transport-specific constraints
// when URL is changed. An explicit {enabled: true} also flips the global
// tools.mcp.enabled kill-switch on in the same write (mirrors addMCPServer;
// {enabled: false} and any other field never touch the global flag). After the
// config write, live-reconciles the MCP manager (AgentLoop.ReconcileMCP) so the
// live connection is reconnected with the new config (or disconnected, if the
// patch disabled it) before the response is built.
func (a *restAPI) patchMCPServer(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	var req gen.McpServerUpdate
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "McpServerUpdate", &req, validateEnabled) {
		return
	}

	// Validate new URL if provided.
	if req.Url != nil && *req.Url != "" {
		if !mcpURLSchemeValid(*req.Url) {
			jsonErr(w, http.StatusUnprocessableEntity,
				"url must use https, or http for loopback addresses only (localhost, 127.x.x.x, ::1)")
			return
		}
	}

	var updatedEntry config.MCPServerConfig
	mcpPatchValidationMsg := ""

	// errMCPNotFound is returned ONLY by the closure when the server id is absent,
	// so the dispatch below can distinguish a genuine 404 from a config I/O/parse
	// failure (e.g. unreadable or corrupt config.json) that aborts safeUpdateConfigJSON
	// before the closure runs — those must surface as 500, not a misleading 404.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		tools, _ := m["tools"].(map[string]any)
		if tools == nil {
			return errMCPNotFound
		}
		mcpSection, _ := tools["mcp"].(map[string]any)
		if mcpSection == nil {
			return errMCPNotFound
		}
		servers, _ := mcpSection["servers"].(map[string]any)
		if servers == nil {
			return errMCPNotFound
		}
		existing, ok := servers[id]
		if !ok {
			return errMCPNotFound
		}

		// Round-trip the existing entry through JSON to get a typed MCPServerConfig.
		raw, err := json.Marshal(existing)
		if err != nil {
			return fmt.Errorf("marshal existing entry: %w", err)
		}
		var current config.MCPServerConfig
		if uerr := json.Unmarshal(raw, &current); uerr != nil {
			return fmt.Errorf("unmarshal existing entry: %w", uerr)
		}

		// Merge only the fields that the caller provided (non-nil pointer).
		// req.Env is handled separately, below the transport-consistency
		// validation, so a validation failure never leaves an orphaned
		// credential-store write behind (see the Env block's own comment).
		if req.Enabled != nil {
			current.Enabled = *req.Enabled
		}
		if req.Command != nil {
			current.Command = *req.Command
		}
		if req.Url != nil {
			current.URL = *req.Url
		}
		if req.Args != nil {
			current.Args = *req.Args
		}
		if req.EnvFile != nil {
			current.EnvFile = *req.EnvFile
		}
		// req.Headers is handled in the credential block below (issue #638):
		// values go to the encrypted store, only names land in config.json.

		// Transport-consistency on the MERGED result (transport itself is immutable
		// via PATCH): stdio uses command (no url); sse/http use url (no command).
		if current.Type == "stdio" && strings.TrimSpace(current.URL) != "" {
			mcpPatchValidationMsg = "stdio servers must not set a url"
			return fmt.Errorf("validation: %s", mcpPatchValidationMsg)
		}
		if (current.Type == "sse" || current.Type == "http") && strings.TrimSpace(current.Command) != "" {
			mcpPatchValidationMsg = "sse/http servers must not set a command"
			return fmt.Errorf("validation: %s", mcpPatchValidationMsg)
		}

		// Route env secrets into the encrypted credential store instead of
		// writing them into config.json in plaintext (BUG 2 fix) — the same
		// mechanism add_mcp_server (the tool) and addMCPServer already use.
		// Placed AFTER the transport-consistency validation above so a
		// request that fails validation never reaches a credential-store
		// write in the first place — no rollback needed.
		//
		// Collision handling: the credential key is deterministic
		// (mcpEnvCredKey == "mcp_<name>_<key>"), so storing a new literal
		// value for a key that was already ref-backed simply overwrites that
		// same credential-store entry in place — the new literal value wins,
		// exactly as add_mcp_server's description promises, and nothing is
		// orphaned. Each key provided in this PATCH's env is deleted from
		// current.Env (it is superseded by its ref) — env keys NOT mentioned
		// in this PATCH, and any pre-existing EnvRefs entries for other keys,
		// are left untouched.
		if req.Env != nil && len(*req.Env) > 0 {
			if current.EnvRefs == nil {
				current.EnvRefs = make(map[string]string, len(*req.Env))
			}
			for key, value := range *req.Env {
				credKey := mcpEnvCredKey(id, key)
				if _, credErr := a.storeCredential(credKey, value); credErr != nil {
					return fmt.Errorf("store env credential %q: %w", key, credErr)
				}
				current.EnvRefs[key] = credKey
				if current.Env != nil {
					delete(current.Env, key)
				}
			}
		}

		// Issue #638: header values are credentials - same treatment as env
		// directly above. Each provided header's value is stored (or overwritten
		// in place for an already ref-backed key) and its name lands in
		// HeaderRefs; the literal copy in current.Headers is deleted (superseded
		// by its ref). Headers NOT mentioned in an incoming PATCH, literal or
		// ref-backed alike, are left untouched - mirroring the env block's merge
		// semantics. Deliberate asymmetry vs the pre-#638 merge
		// (`current.Headers = *req.Headers`): an empty headers map in the
		// request no longer CLEARS all headers; per-header updates and adds are
		// unchanged (send the header with its new value), so the only lost
		// operation is "clear everything at once", which the UI can express
		// per-header. Placed after the transport-consistency validation for the
		// same reason env's block is: a request that fails validation never
		// reaches a credential-store write.
		if req.Headers != nil && len(*req.Headers) > 0 {
			if current.HeaderRefs == nil {
				current.HeaderRefs = make(map[string]string, len(*req.Headers))
			}
			for key, value := range *req.Headers {
				credKey := mcpHeaderCredKey(id, key)
				if _, credErr := a.storeCredential(credKey, value); credErr != nil {
					return fmt.Errorf("store header credential %q: %w", key, credErr)
				}
				current.HeaderRefs[key] = credKey
				if current.Headers != nil {
					delete(current.Headers, key)
				}
			}
		}

		// Rebuild the map entry from the updated struct so the JSON shape is
		// consistent with what addMCPServer writes.
		updated, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("marshal updated entry: %w", err)
		}
		var updatedMap map[string]any
		if err := json.Unmarshal(updated, &updatedMap); err != nil {
			return fmt.Errorf("unmarshal updated entry: %w", err)
		}
		servers[id] = updatedMap
		updatedEntry = current

		// An explicit PATCH {enabled: true} is operator intent to (re)connect
		// this server, exactly like addMCPServer's own auto-enable — flip the
		// global kill-switch on in the same write so ReconcileMCP's desired set
		// isn't forced empty by a still-false tools.mcp.enabled (the
		// upgraded-install trap: an operator re-enables a server that was
		// disabled before the global flag existed, Test succeeds, but nothing
		// ever connects because the flag itself was never on). Any other PATCH
		// — including {enabled: false} — leaves the global flag untouched.
		if req.Enabled != nil && *req.Enabled {
			mcpSection["enabled"] = true
		}
		return nil
	}); err != nil {
		if mcpPatchValidationMsg != "" {
			jsonErr(w, http.StatusUnprocessableEntity, mcpPatchValidationMsg)
			return
		}
		if errors.Is(err, errMCPNotFound) {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("mcp server %q not found", id))
			return
		}
		slog.Error("rest: patch mcp server", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// Config write succeeded — reconcile the live manager so an edited server
	// (changed command/url/args/env/headers, or toggled enabled) is reconnected
	// with the new config, or disconnected if the patch disabled it, instead of
	// the live connection silently drifting from what config.json now says.
	rctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	if err := a.agentLoop.ReconcileMCP(rctx); err != nil {
		slog.Warn("rest: patch mcp server: live reconcile failed", "server", id, "error", err)
	}
	cancel()

	transport := gen.McpServerTransportStdio
	switch updatedEntry.Type {
	case "sse":
		transport = gen.McpServerTransportSse
	case "http":
		transport = gen.McpServerTransportHttp
	}
	enabled := updatedEntry.Enabled
	status, toolCount := a.mcpLiveStatus(id)
	resp := gen.McpServer{
		Id:        id,
		Name:      id,
		Transport: transport,
		Status:    status,
		ToolCount: toolCount,
		Enabled:   &enabled,
	}
	jsonOK(w, resp)
}
