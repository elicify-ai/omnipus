// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/mcp"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// mcpConnectTimeout bounds each individual server connect attempt during
// ReconcileMCP so one unreachable/hanging server cannot stall reconciliation
// of every other server (or the caller — REST handlers and the hot-reload
// path both call ReconcileMCP with their own outer deadlines).
const mcpConnectTimeout = 15 * time.Second

type mcpRuntime struct {
	// initMu serializes concurrent calls to ReconcileMCP/ensureMCPInitialized
	// so only one reconcile pass runs at a time. Unlike sync.Once, ReconcileMCP
	// is NOT single-shot: every call re-diffs desired (config) against live
	// (manager) and connects/disconnects/re-registers as needed, and a
	// per-server connect failure never blocks the pass from completing
	// overall — it is recorded (see connectErrs/MCPServerStatus) and simply
	// retried on the next call.
	initMu  sync.Mutex
	mu      sync.Mutex
	manager *mcp.Manager
	initErr error
	// initialized is set to true once a successful init/reconcile has
	// completed so that subsequent ensureMCPInitialized calls skip the work
	// without holding initMu.
	initialized bool
	// closed is set once, under initMu, by AgentLoop.Close (before the
	// manager is taken and closed). ReconcileMCP/ensureMCPInitialized check
	// it immediately after acquiring initMu and return without touching the
	// manager or recording any state once it is true — teardown has already
	// started and no further connect/register work may run concurrently
	// with it.
	closed bool

	// centralMCP is the process-wide central MCP tool registry (populated on
	// connect, evicted on disconnect). Nil until SetCentralMCPRegistries is
	// called; every registration/eviction path below is nil-safe.
	centralMCP *tools.MCPRegistry
	// centralBuiltin is the process-wide builtin registry, used only for
	// reserved-name admission checks (ValidateMCPName) when registering into
	// centralMCP. Nil-safe like centralMCP.
	centralBuiltin *tools.BuiltinRegistry
	// connectErrs records the last connect error per server name, observed
	// during ReconcileMCP. A server absent from this map (or present with "")
	// has no recorded failure. Initialized lazily on first write.
	connectErrs map[string]string

	// credentialResolver resolves an MCP server's env-ref credential-store key
	// to its real secret value (add_mcp_server, pkg/sysagent/tools/mcp.go,
	// routes every `env` value through the credential store instead of
	// writing it to config.json in plaintext — see config.MCPServerConfig.
	// EnvRefs and mcp.ResolveServerEnvRefs). Nil until SetCredentialResolver
	// is called (tests, or a gateway that hasn't wired one); a server
	// carrying EnvRefs against a nil resolver fails resolution for that pass
	// and is skipped, exactly like an unresolvable relative env_file path —
	// see reconcileLocked.
	credentialResolver func(refKey string) (string, error)
}

func (r *mcpRuntime) setManager(manager *mcp.Manager) {
	r.mu.Lock()
	r.manager = manager
	r.initErr = nil
	r.mu.Unlock()
}

func (r *mcpRuntime) setInitErr(err error) {
	r.mu.Lock()
	r.initErr = err
	r.mu.Unlock()
}

func (r *mcpRuntime) getInitErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initErr
}

func (r *mcpRuntime) takeManager() *mcp.Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	manager := r.manager
	r.manager = nil
	return manager
}

func (r *mcpRuntime) hasManager() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manager != nil
}

// getManager returns the current manager without consuming it.
// Callers must not close or otherwise mutate the returned manager.
func (r *mcpRuntime) getManager() *mcp.Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manager
}

// setClosed marks the runtime as closed. Callers MUST hold initMu while
// calling this (see AgentLoop.Close) so the closed flag and the manager
// hand-off happen as one atomic step from ReconcileMCP's point of view.
func (r *mcpRuntime) setClosed() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
}

// isInitialized reports whether a reconcile pass has completed at least once.
func (r *mcpRuntime) isInitialized() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initialized
}

// clearInitialized resets the initialized latch so the next
// ensureMCPInitialized call re-runs a full reconcile pass instead of taking
// the already-done fast path. Used by the ReloadProviderAndConfig hook when
// its ReconcileMCP call is canceled/times out: the pass never actually ran,
// so leaving initialized=true (from an earlier, unrelated successful pass)
// would strand the runtime on stale state until the next hot reload happens
// to succeed.
func (r *mcpRuntime) clearInitialized() {
	r.mu.Lock()
	r.initialized = false
	r.mu.Unlock()
}

// isClosed reports whether the runtime has been torn down by AgentLoop.Close.
func (r *mcpRuntime) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// setCentralRegistries wires the process-wide central registries. Either
// argument may be nil (nil-safe downstream: registration/eviction against a
// nil centralMCP is simply skipped).
func (r *mcpRuntime) setCentralRegistries(mcpReg *tools.MCPRegistry, builtinReg *tools.BuiltinRegistry) {
	r.mu.Lock()
	r.centralMCP = mcpReg
	r.centralBuiltin = builtinReg
	r.mu.Unlock()
}

// setCredentialResolver wires the credential-store lookup used to resolve
// MCP server EnvRefs at connect time. resolver may be nil (the initial
// state) — a server with EnvRefs then fails resolution until a non-nil
// resolver is set.
func (r *mcpRuntime) setCredentialResolver(resolver func(refKey string) (string, error)) {
	r.mu.Lock()
	r.credentialResolver = resolver
	r.mu.Unlock()
}

// getCredentialResolver returns the current credential-store resolver, or
// nil if none has been wired yet.
func (r *mcpRuntime) getCredentialResolver() func(refKey string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.credentialResolver
}

// centralRegistries returns the current central MCP and builtin registries.
// Either may be nil (before SetCentralMCPRegistries has been called).
func (r *mcpRuntime) centralRegistries() (*tools.MCPRegistry, *tools.BuiltinRegistry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.centralMCP, r.centralBuiltin
}

// setConnectErr records the last connect error observed for a server during
// ReconcileMCP. Passing a nil err clears any previously recorded failure
// (successful (re)connect). connectErrs is initialized lazily on first write.
func (r *mcpRuntime) setConnectErr(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.connectErrs == nil {
		r.connectErrs = make(map[string]string)
	}
	if err == nil {
		delete(r.connectErrs, name)
		return
	}
	r.connectErrs[name] = err.Error()
}

// getConnectErr returns the last recorded connect error message for a
// server, or "" when there is none.
func (r *mcpRuntime) getConnectErr(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.connectErrs == nil {
		return ""
	}
	return r.connectErrs[name]
}

// pruneConnectErrs clears every recorded connect error whose server name is
// NOT present in desired AND NOT in skipped. A disabled or deleted server
// must not keep reporting a stale "error" status (from an earlier failed
// connect attempt) forever — once it drops out of both sets, its error
// history is irrelevant. skipped carries server names that failed env_file
// resolution during THIS SAME desired-build pass (recorded via setConnectErr
// moments earlier): a resolution-skipped server is, by definition, absent
// from desired, so without skipped this call would immediately erase the
// very error it was just asked to record.
func (r *mcpRuntime) pruneConnectErrs(desired map[string]config.MCPServerConfig, skipped map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.connectErrs {
		if _, ok := desired[name]; ok {
			continue
		}
		if skipped[name] {
			continue
		}
		delete(r.connectErrs, name)
	}
}

// SetCentralMCPRegistries wires the process-wide central MCP registry
// (populated on connect, evicted on disconnect) and the central builtin
// registry (name-collision admission) into this AgentLoop's MCP runtime.
// Called once by the gateway before services start. Nil-safe — passing nil
// for either argument simply leaves central registration/eviction a no-op
// until a non-nil value is set.
func (al *AgentLoop) SetCentralMCPRegistries(mcpReg *tools.MCPRegistry, builtinReg *tools.BuiltinRegistry) {
	al.mcp.setCentralRegistries(mcpReg, builtinReg)
}

// SetCredentialResolver wires the credential-store lookup ReconcileMCP uses
// to resolve MCP server EnvRefs (see config.MCPServerConfig.EnvRefs and
// mcp.ResolveServerEnvRefs) into real secret values before connecting a
// stdio server. Typically wired to (*credentials.Store).Get, which has this
// exact signature — passed as a func rather than the concrete type so this
// package does not need to import pkg/credentials. Called once by the
// gateway before services start, alongside SetCentralMCPRegistries. Passing
// nil disarms resolution: any server with EnvRefs then fails to connect
// until a resolver is set again.
func (al *AgentLoop) SetCredentialResolver(resolver func(refKey string) (string, error)) {
	al.mcp.setCredentialResolver(resolver)
}

// MCPServersSnapshot returns a clone of the configured MCP servers map,
// safe for a caller to range over without racing the sysagent
// config-mutation path (MutateConfig/WithConfig, see pkg/sysagent/tools/mcp.go),
// which mutates al.cfg.Tools.MCP.Servers IN PLACE while holding al.mu.Lock.
// Values are config.MCPServerConfig structs, shallow-copied: a value's own
// inner slices/maps (Args/Env/Headers) are NOT deep-copied, so this is safe
// for read-only iteration (the REST-handler use case) but a caller must not
// mutate a returned entry's slice/map fields in place.
func (al *AgentLoop) MCPServersSnapshot() map[string]config.MCPServerConfig {
	al.mu.RLock()
	defer al.mu.RUnlock()
	servers := make(map[string]config.MCPServerConfig, len(al.cfg.Tools.MCP.Servers))
	for name, serverCfg := range al.cfg.Tools.MCP.Servers {
		servers[name] = serverCfg
	}
	return servers
}

// MCPWorkspacePath returns the workspace directory relative MCP server
// env_file paths are resolved against during reconciliation: the default
// agent's Home when set, otherwise cfg.AgentHomeBasePath(). Both
// reconcileLocked and the gateway's MCP-test handler resolve env_file
// against this single derivation so the two never drift apart.
func (al *AgentLoop) MCPWorkspacePath() string {
	al.mu.RLock()
	workspacePath := al.cfg.AgentHomeBasePath()
	al.mu.RUnlock()

	registry := al.GetRegistry()
	if defaultAgent := registry.GetDefaultAgent(); defaultAgent != nil && defaultAgent.Home != "" {
		workspacePath = defaultAgent.Home
	}
	return workspacePath
}

// ensureMCPInitialized loads MCP servers/tools once so both Run() and direct
// agent mode share the same initialization path. Returns early (without
// error) when tools.mcp.servers is empty, or when every configured entry has
// its own per-server Enabled bit off — a fast, lock-cheap bail-out that
// avoids taking initMu for the common "nothing to do" case. It deliberately
// does NOT also consult the global tools.mcp.enabled kill-switch here: that
// flag is honored downstream, in reconcileLocked's desired-set build, which
// is why a server with Enabled=true still gets a full reconcile pass (and
// correctly ends up disconnected) even when the global flag is off.
//
// The actual connect/register work is delegated to reconcileLocked (the core
// ReconcileMCP also calls), so all callers converge on one diff-and-connect
// implementation instead of duplicating the connect/register logic.
func (al *AgentLoop) ensureMCPInitialized(ctx context.Context) error {
	// Read under al.mu.RLock: al.cfg.Tools.MCP.Servers is the SAME map the
	// sysagent config-mutation path (MutateConfig/WithConfig) mutates in
	// place while holding al.mu.Lock — ranging over it unlocked risks a
	// fatal concurrent map read/write.
	al.mu.RLock()
	hasServers := len(al.cfg.Tools.MCP.Servers) > 0
	findValidServer := false
	if hasServers {
		for _, serverCfg := range al.cfg.Tools.MCP.Servers {
			if serverCfg.Enabled {
				findValidServer = true
				break
			}
		}
	}
	al.mu.RUnlock()

	if !hasServers {
		return nil
	}
	if !findValidServer {
		logger.DebugCF("agent", "no enabled MCP servers configured, skipping MCP initialization", nil)
		return nil
	}

	// Cheap fast path: skip taking initMu entirely once a previous pass has
	// already completed successfully.
	al.mcp.mu.Lock()
	alreadyDone := al.mcp.initialized
	al.mcp.mu.Unlock()
	if alreadyDone {
		return al.mcp.getInitErr()
	}

	al.mcp.initMu.Lock()
	defer al.mcp.initMu.Unlock()

	// Re-check under initMu: another caller may have completed
	// initialization (or the runtime may have been closed) while this call
	// was waiting for the lock.
	al.mcp.mu.Lock()
	alreadyDone = al.mcp.initialized
	al.mcp.mu.Unlock()
	if alreadyDone {
		return al.mcp.getInitErr()
	}
	if al.mcp.isClosed() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return al.reconcileLocked(ctx)
}

// ReconcileMCP diffs cfg.Tools.MCP (desired) against the live manager
// (actual): connects added/changed enabled servers, disconnects
// removed/disabled ones, and — always — re-registers every live connection's
// tools into ALL per-agent registries AND the central MCPRegistry. That last
// step is unconditional so it also heals the per-agent registration wipe a
// freshly built AgentRegistry (ReloadProviderAndConfig) would otherwise
// cause. Per-server connect failures are recorded (surfaced via
// MCPServerStatus) and do not abort the pass; ReconcileMCP itself returns
// non-nil only on systemic failure (discovery misconfiguration) or a
// canceled/expired ctx, in which case the runtime is left untouched so the
// next call retries from scratch. Serialized with ensureMCPInitialized via
// al.mcp.initMu. Must NOT be called while holding al.mu.
//
// Unlike ensureMCPInitialized, this never skips on an already-initialized
// runtime — it always re-runs the diff, which is how config changes made
// after the first successful pass (REST/sysagent writes, hot reload) get
// picked up.
func (al *AgentLoop) ReconcileMCP(ctx context.Context) error {
	al.mcp.initMu.Lock()
	defer al.mcp.initMu.Unlock()

	if al.mcp.isClosed() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return al.reconcileLocked(ctx)
}

// reconcileLocked is the core diff-and-connect pass. Callers MUST hold
// al.mcp.initMu (see ReconcileMCP / ensureMCPInitialized) and MUST have
// already checked al.mcp.isClosed() and ctx.Err().
func (al *AgentLoop) reconcileLocked(ctx context.Context) error {
	// Snapshot the MCP config under al.mu.RLock with a deep copy of the
	// Servers map: al.cfg is the LIVE *config.Config pointer, and the
	// sysagent config-mutation path (MutateConfig/WithConfig, see
	// pkg/sysagent/tools/mcp.go) mutates cfg.Tools.MCP.Servers IN PLACE while
	// holding al.mu.Lock (adding/removing map entries directly rather than
	// swapping the whole *Config). Ranging over that same map here without a
	// lock is a fatal concurrent map read/write. ToolConfig/Discovery are
	// plain value fields (safe to copy by value); only the Servers map needs
	// cloning.
	al.mu.RLock()
	mcpCfg := al.cfg.Tools.MCP
	servers := make(map[string]config.MCPServerConfig, len(mcpCfg.Servers))
	for name, serverCfg := range mcpCfg.Servers {
		servers[name] = serverCfg
	}
	mcpCfg.Servers = servers
	al.mu.RUnlock()

	registry := al.GetRegistry()
	workspacePath := al.MCPWorkspacePath()

	// --- Build desired: enabled servers, gated by the global kill-switch
	// (an operator turning tools.mcp.enabled off must actively disconnect
	// everything live, not just stop connecting new ones), with relative
	// env_file paths resolved against the workspace exactly as
	// LoadFromMCPConfig resolves them at boot. The RESOLVED config is what
	// both ConnectServer and the drift-check below use, so the two stay
	// consistent — a workspace-path change alone legitimately triggers a
	// reconnect. skipped tracks server names that failed env_file resolution
	// THIS pass — they are absent from desired but their connect error was
	// just recorded, so pruneConnectErrs below must not immediately erase it. ---
	desired := make(map[string]config.MCPServerConfig, len(mcpCfg.Servers))
	skipped := make(map[string]bool)
	if mcpCfg.Enabled {
		for name, serverCfg := range mcpCfg.Servers {
			if !serverCfg.Enabled {
				continue
			}
			resolvedCfg, err := mcp.ResolveServerEnvFile(serverCfg, workspacePath)
			if err != nil {
				logger.WarnCF("agent", "Skipping MCP server: cannot resolve relative env_file without a workspace path",
					map[string]any{"server": name, "env_file": serverCfg.EnvFile})
				al.mcp.setConnectErr(name, fmt.Errorf("server %s: %w", name, err))
				skipped[name] = true
				continue
			}
			// Resolve any credential-store env refs (add_mcp_server routes
			// secrets through the credential store rather than config.json
			// plaintext — see config.MCPServerConfig.EnvRefs) into real
			// values, in memory only. A server with EnvRefs the resolver
			// cannot satisfy (store locked, resolver not wired, ref deleted
			// out from under it) is skipped rather than connected with a
			// missing secret.
			resolvedCfg, err = mcp.ResolveServerEnvRefs(resolvedCfg, al.mcp.getCredentialResolver())
			if err != nil {
				logger.WarnCF("agent", "Skipping MCP server: cannot resolve env credential reference(s)",
					map[string]any{"server": name, "error": err.Error()})
				al.mcp.setConnectErr(name, fmt.Errorf("server %s: %w", name, err))
				skipped[name] = true
				continue
			}
			desired[name] = resolvedCfg
		}
	}

	mgr := al.mcp.getManager()
	var live map[string]*mcp.ServerConnection
	if mgr != nil {
		live = mgr.GetServers()
	}

	// --- Removals: live but no longer desired, or desired config changed.
	// Runs BEFORE the discovery fail-fast below so a delete/disable always
	// takes effect even while tool discovery happens to be misconfigured. ---
	removed := make(map[string]bool, len(live))
	for name, conn := range live {
		desiredCfg, stillDesired := desired[name]
		if stillDesired && serverConfigEqual(conn.Config, desiredCfg) {
			continue
		}
		removed[name] = true
		al.unregisterServerTools(mgr, registry, name, conn)
		if err := mgr.DisconnectServer(name); err != nil {
			logger.WarnCF("agent", "Failed to disconnect MCP server during reconcile",
				map[string]any{"server": name, "error": err.Error()})
		}
	}

	al.mcp.pruneConnectErrs(desired, skipped)

	if mgr == nil && len(desired) == 0 {
		al.mcp.setInitErr(nil)
		al.mcp.mu.Lock()
		al.mcp.initialized = true
		al.mcp.mu.Unlock()
		return nil
	}

	// Discovery misconfiguration is the only systemic failure this pass can
	// hit. It only guards the connect/register phases below — removals above
	// already ran unconditionally so a delete/disable always takes effect
	// regardless of discovery config validity.
	if mcpCfg.Enabled && mcpCfg.Discovery.Enabled && !mcpCfg.Discovery.UseBM25 && !mcpCfg.Discovery.UseRegex {
		err := fmt.Errorf(
			"tool discovery is enabled but neither 'use_bm25' nor 'use_regex' is set to true in the configuration",
		)
		// Only latch initErr — which ensureMCPInitialized's fast path returns
		// verbatim without re-running reconcile — on the FIRST failure. Once a
		// pass has already succeeded once, a misconfiguration introduced later
		// must still be returned to THIS caller, but must not overwrite a nil
		// initErr and brick every subsequent turn behind a stale error via the
		// fast path; whatever MCP state is already live keeps working instead.
		if !al.mcp.isInitialized() {
			al.mcp.setInitErr(err)
		}
		return err
	}

	newManager := false
	if mgr == nil {
		// Reconcile connects servers individually below (see the connect
		// loop) rather than via LoadFromMCPConfig's all-or-nothing bulk
		// connect.
		mgr = mcp.NewManager()
		newManager = true
	}
	// ADR-066 D10: the live ingest bound applies to every server connected
	// in this pass (transports are built at connect time).
	mgr.SetIngestBoundBytes(al.cfg.Context.IngestBoundBytes)

	// --- Connects: desired but not (still) live, including changed servers
	// just disconnected above. Each server connects on its own goroutine
	// with its own bounded child context so one slow/hanging server cannot
	// delay every other server's connect within the same pass. ConnectServer
	// stores into the manager under its own lock, so concurrent calls for
	// different server names are safe. ---
	var connectWG sync.WaitGroup
	for name, serverCfg := range desired {
		if _, wasLive := live[name]; wasLive && !removed[name] {
			continue
		}
		connectWG.Add(1)
		go func(name string, serverCfg config.MCPServerConfig) {
			defer connectWG.Done()
			connectCtx, cancel := context.WithTimeout(ctx, mcpConnectTimeout)
			defer cancel()
			err := mgr.ConnectServer(connectCtx, name, serverCfg)
			if err != nil {
				logger.WarnCF("agent", "Failed to connect MCP server during reconcile",
					map[string]any{"server": name, "error": err.Error()})
			}
			al.mcp.setConnectErr(name, err)
		}(name, serverCfg)
	}
	connectWG.Wait()

	// --- Always: re-register every live connection's tools. This heals the
	// per-agent registry wipe a fresh AgentRegistry would otherwise cause on
	// hot reload, and keeps the central registry current even when nothing
	// above changed. ---
	finalLive := mgr.GetServers()
	uniqueTools, totalRegistrations := 0, 0
	for name, conn := range finalLive {
		u, t := al.registerServerTools(mgr, registry, name, conn, mcpCfg)
		uniqueTools += u
		totalRegistrations += t
	}
	logger.InfoCF("agent", "MCP reconciliation complete",
		map[string]any{
			"server_count":        len(finalLive),
			"unique_tools":        uniqueTools,
			"total_registrations": totalRegistrations,
		})

	if newManager {
		al.mcp.setManager(mgr)
	}
	al.mcp.setInitErr(nil)
	al.mcp.mu.Lock()
	al.mcp.initialized = true
	al.mcp.mu.Unlock()

	return nil
}

// MCPServerStatus reports live status for one configured server.
// status is one of "connected" (present in the live manager), "error" (not
// live, but ReconcileMCP recorded a connect failure for it), or
// "disconnected" (neither). toolCount is the number of entries for this
// server in the central MCP registry (0 when the central registry has not
// been wired via SetCentralMCPRegistries). errMsg is the last recorded
// connect error, or "" when there is none.
func (al *AgentLoop) MCPServerStatus(name string) (status string, toolCount int, errMsg string) {
	errMsg = al.mcp.getConnectErr(name)

	if mgr := al.mcp.getManager(); mgr != nil {
		if _, ok := mgr.GetServer(name); ok {
			status = "connected"
		}
	}
	if status == "" {
		if errMsg != "" {
			status = "error"
		} else {
			status = "disconnected"
		}
	}

	if centralMCP, _ := al.mcp.centralRegistries(); centralMCP != nil {
		for _, entry := range centralMCP.Describe() {
			if entry.ServerID == name {
				toolCount++
			}
		}
	}

	return status, toolCount, errMsg
}

// registerServerTools registers all of one connected MCP server's tools into
// every agent's per-agent tool registry (Register, or RegisterHidden per
// serverIsDeferred) and into the central MCPRegistry (admission-checked
// against centralBuiltin). Extracted from the original ensureMCPInitialized
// inline loop so ReconcileMCP can reuse it both for newly connected servers
// and to unconditionally re-heal registries a fresh AgentRegistry may have
// dropped. Nil-safe: registry == nil skips per-agent registration; a nil
// centralMCP (before SetCentralMCPRegistries has run) skips central
// registration. Returns the tool count and total per-agent registrations
// performed, for the caller's summary log.
func (al *AgentLoop) registerServerTools(
	mgr *mcp.Manager,
	registry *AgentRegistry,
	serverName string,
	conn *mcp.ServerConnection,
	mcpCfg config.MCPConfig,
) (uniqueTools, totalRegistrations int) {
	if conn == nil {
		return 0, 0
	}
	uniqueTools = len(conn.Tools)

	// Determine whether this server's tools should be deferred (hidden).
	// Per-server "deferred" field takes precedence over the global Discovery.Enabled.
	serverCfg := mcpCfg.Servers[serverName]
	registerAsHidden := serverIsDeferred(mcpCfg.Discovery.Enabled, serverCfg)

	wrapped := make([]tools.Tool, 0, len(conn.Tools))
	var agentIDs []string
	if registry != nil {
		agentIDs = registry.ListAgentIDs()
	}

	for _, tool := range conn.Tools {
		mcpTool := tools.NewMCPTool(mgr, serverName, tool)
		wrapped = append(wrapped, mcpTool)
		toolName := mcpTool.Name()

		for _, agentID := range agentIDs {
			agent, ok := registry.GetAgent(agentID)
			if !ok {
				continue
			}

			// An unchanged server's tools are already present — the removal
			// pass above unregisters any changed/removed server's tools
			// first, so a name that is already registered here belongs to a
			// server whose config didn't change this pass. Skipping it
			// preserves a TTL-promoted (ToolSearch) tool's visibility instead
			// of resetting it to hidden on every reconcile, and eliminates a
			// Register/RegisterHidden log line + version bump on every pass
			// for servers that never changed. GetIncludingHidden is used
			// (not Get) because a hidden tool with an expired TTL must still
			// count as "present" here.
			if _, exists := agent.Tools.GetIncludingHidden(toolName); exists {
				continue
			}

			// #278 / FR-060: MCP-supplied tools MUST go through the hardened
			// entry points, which validate reserved names unconditionally —
			// including on a first, uncontested claim. Register/RegisterHidden
			// are deliberately permissive and must never be used here.
			if registerAsHidden {
				agent.Tools.RegisterHiddenMCP(mcpTool)
			} else {
				agent.Tools.RegisterMCP(mcpTool)
			}

			totalRegistrations++
			logger.DebugCF("agent", "Registered MCP tool",
				map[string]any{
					"agent_id": agentID,
					"server":   serverName,
					"tool":     tool.Name,
					"name":     toolName,
					"deferred": registerAsHidden,
				})
		}
	}

	centralMCP, centralBuiltin := al.mcp.centralRegistries()
	if centralMCP != nil {
		// No fingerprint metadata (equivalent to MCPServerOpts{} — rename
		// detection off): two stdio servers can legitimately share the same
		// Command (e.g. two npx-launched servers differing only in Args), and
		// registering by (transportType, endpoint) fingerprint would treat
		// that as the second server "renaming" the first, evicting it.
		// Reconcile's own config diff (the removal pass above) already
		// handles renames deterministically by name, so the registry's
		// separate fingerprint heuristic only adds false-positive evictions
		// here.
		collisions := centralMCP.RegisterServerTools(serverName, wrapped, centralBuiltin)
		if len(collisions) > 0 {
			logger.WarnCF("agent", "MCP tool collisions during central registration",
				map[string]any{"server": serverName, "collision_count": len(collisions)})
		}
	}

	return uniqueTools, totalRegistrations
}

// unregisterServerTools removes one server's tools from every agent's
// per-agent tool registry and evicts the server from the central MCPRegistry.
// It does NOT disconnect the server from the manager — ReconcileMCP calls
// manager.DisconnectServer separately, after this returns, matching the
// registered order (unregister the tools, THEN tear down the connection).
func (al *AgentLoop) unregisterServerTools(
	mgr *mcp.Manager,
	registry *AgentRegistry,
	serverName string,
	conn *mcp.ServerConnection,
) {
	if registry != nil && conn != nil {
		agentIDs := registry.ListAgentIDs()
		for _, tool := range conn.Tools {
			name := tools.NewMCPTool(mgr, serverName, tool).Name()
			for _, agentID := range agentIDs {
				agent, ok := registry.GetAgent(agentID)
				if !ok {
					continue
				}
				agent.Tools.Unregister(name)
			}
		}
	}

	if centralMCP, _ := al.mcp.centralRegistries(); centralMCP != nil {
		centralMCP.EvictServer(serverName)
	}
}

// serverIsDeferred reports whether an MCP server's tools should be registered
// as hidden (deferred/discovery mode).
//
// The per-server Deferred field takes precedence over the global discoveryEnabled
// default. When Deferred is nil, discoveryEnabled is used as the fallback.
func serverIsDeferred(discoveryEnabled bool, serverCfg config.MCPServerConfig) bool {
	if !discoveryEnabled {
		return false
	}
	if serverCfg.Deferred != nil {
		return *serverCfg.Deferred
	}
	return true
}

// serverConfigEqual reports whether two MCPServerConfig values are
// equivalent for reconcileLocked's removal-pass drift check. It differs from
// a literal reflect.DeepEqual in exactly one way: a nil Args/Env/Headers and
// an empty (len-0, non-nil) one compare equal. Config loaded from JSON round
// -trips through `omitempty` on those three fields — a server saved with
// Args: []string{} comes back from disk as Args: nil — so a literal
// DeepEqual would read that as config drift and force a one-time
// disconnect+reconnect of every such server on the very next reload, even
// though nothing an operator actually set changed. All other fields
// (Enabled, Command, EnvFile, Type, URL) are compared exactly. Deferred is a
// *bool: compared by pointee, not pointer identity — nil vs. non-nil is a
// real difference, and two non-nil pointers are equal iff *a == *b.
func serverConfigEqual(a, b config.MCPServerConfig) bool {
	if a.Enabled != b.Enabled || a.Command != b.Command || a.EnvFile != b.EnvFile ||
		a.Type != b.Type || a.URL != b.URL {
		return false
	}
	if !stringSlicesEqualIgnoringNilVsEmpty(a.Args, b.Args) {
		return false
	}
	if !stringMapsEqualIgnoringNilVsEmpty(a.Env, b.Env) {
		return false
	}
	if !stringMapsEqualIgnoringNilVsEmpty(a.EnvRefs, b.EnvRefs) {
		return false
	}
	if !stringMapsEqualIgnoringNilVsEmpty(a.Headers, b.Headers) {
		return false
	}
	if (a.Deferred == nil) != (b.Deferred == nil) {
		return false
	}
	return a.Deferred == nil || *a.Deferred == *b.Deferred
}

// stringSlicesEqualIgnoringNilVsEmpty compares two string slices by content;
// nil and an empty non-nil slice are equal (len(nil) == 0 == len([]string{})
// falls out of the length check below without special-casing either one).
func stringSlicesEqualIgnoringNilVsEmpty(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stringMapsEqualIgnoringNilVsEmpty compares two string-keyed maps by
// content; nil and an empty non-nil map are equal for the same reason as
// stringSlicesEqualIgnoringNilVsEmpty.
func stringMapsEqualIgnoringNilVsEmpty(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
