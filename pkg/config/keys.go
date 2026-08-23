// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// ConfigKey is a dotted path into the gateway's config.json. Using a typed
// string (rather than a raw string literal) gives every consumer that
// references a specific path — blockedPaths, RestartGatedKeys, getAtPath
// callers — compile-time protection against typos and rename drift.
type ConfigKey string

const (
	SandboxModeKey      ConfigKey = "sandbox.mode"
	SandboxAuditLog     ConfigKey = "sandbox.audit_log"
	SandboxAllowedPaths ConfigKey = "sandbox.allowed_paths"
	// SandboxGodMode and SandboxGodModeAllowed are god-mode (O14) keys. A
	// config-only enable requires a restart to take live effect (availability
	// is frozen at boot into agent.SetAllowGodMode's atomic), but these are
	// deliberately NOT in RestartGatedKeys: they can also apply live once god
	// mode is already available, and the generic pending-restart banner cannot
	// tell the two cases apart. The dedicated GodModeControl surface owns the
	// restart signal accurately instead — see rest_pending_restart.go and
	// rest_god_mode.go.
	SandboxGodMode        ConfigKey = "sandbox.god_mode"
	SandboxGodModeAllowed ConfigKey = "sandbox.god_mode_allowed"
	// SandboxWorkspacePathGuard is the operator-facing control for the
	// in-process path guard (ADR-068 §6) — the rule layer that decides,
	// before any child process is spawned, whether an agent's file tools and
	// the text of a bash command may reference paths outside the agent's own
	// home directory and its approved mounts.
	//
	// It is NOT SandboxModeKey. `sandbox.mode` selects how the KERNEL
	// enforces policy on an already-spawned child; this key gates a Go-side
	// text/path check that runs earlier and independently. Operators who
	// turned the kernel sandbox off and still saw commands refused were
	// hitting this rule, which until ADR-068 had no control at all. Any
	// surface that renders these two must label them distinctly.
	//
	// RESTART-GATED (corrected 2026-08-23, code review). An earlier version of
	// this comment claimed the value is re-read on every config reload and that
	// a pending-restart banner "would be a lie". That was wrong in the other
	// direction: a reload ends at AgentLoop.SwapConfig (pkg/agent/loop.go),
	// which swaps the *config.Config pointer and bumps configGen — it rebuilds
	// no tools. ExecTool captures restrictToWorkspace at CONSTRUCTION
	// (shell.go's ExecTool.restrictToWorkspace, set from
	// pkg/agent/instance.go), so a running gateway keeps the old value until
	// restarted. Listed in RestartGatedKeys (pkg/gateway/rest_pending_restart.go)
	// so the operator is told that, rather than watching a saved security
	// setting appear to take effect while the guard still enforces the old one.
	SandboxWorkspacePathGuard ConfigKey = "sandbox.workspace_path_guard"
	SessionDMScope            ConfigKey = "session.dm_scope"
	GatewayHost               ConfigKey = "gateway.host"
	GatewayPort               ConfigKey = "gateway.port"
	GatewayUsers              ConfigKey = "gateway.users"
	GatewayDevModeBypass      ConfigKey = "gateway.dev_mode_bypass"

	// GatewayPublicURL drives boot-frozen CORS/CSP/WS-origin fences
	// (CanonicalGatewayOrigin) — it MUST stay restart-gated (ADR-044).
	GatewayPublicURL ConfigKey = "gateway.public_url"
	// GatewayPreviewEnabled gates /preview/ and serve_web. Read live — it is
	// deliberately NOT in RestartGatedKeys (ADR-044, FR-006/FR-007).
	GatewayPreviewEnabled ConfigKey = "gateway.preview_enabled"
	// GatewayOrphanedTurnGraceSeconds bounds the orphan-foreground-turn
	// watchdog's grace period (ADR-045). Read live on every WS teardown — it
	// is deliberately NOT in RestartGatedKeys, matching GatewayPreviewEnabled's
	// precedent.
	GatewayOrphanedTurnGraceSeconds ConfigKey = "gateway.orphaned_turn_grace_seconds"
	// ToolsWebServeWarmup is the dotted JSON path of the web_serve warmup
	// timeout in config.json. The on-disk key is still named
	// `tools.run_in_workspace.warmup_timeout_seconds` for backwards
	// compatibility with deployed configs — it is only used by the dev-mode
	// branch of web_serve, but renaming the persisted key would require
	// every operator to migrate their config.json. The constant is named
	// after the current tool to keep restart-gated key tracking readable.
	ToolsWebServeWarmup ConfigKey = "tools.run_in_workspace.warmup_timeout_seconds"

	// AgentsList is the retired agents.list entity roster (ADR-054). It must
	// be blocked from the generic PUT /api/v1/config endpoint and rejected by
	// system.config.set — agent CRUD now goes exclusively through the agent
	// store / dedicated agent endpoints (POST/PUT/DELETE /api/v1/agents),
	// never through generic config mutation. NOTE: this is deliberately
	// narrower than "agents" — agents.defaults (including
	// agents.defaults.default_agent_id, D6.4) is a SETTING (D1) and MUST
	// remain writable via both surfaces; blocking the whole "agents" key
	// would make agents.defaults unwritable via PUT /api/v1/config, and
	// removing the "agents." prefix from knownConfigPrefixes would make it
	// unwritable via system.config.set too.
	AgentsList ConfigKey = "agents.list"
)
