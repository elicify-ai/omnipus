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
	SessionDMScope        ConfigKey = "session.dm_scope"
	GatewayHost           ConfigKey = "gateway.host"
	GatewayPort           ConfigKey = "gateway.port"
	GatewayUsers          ConfigKey = "gateway.users"
	GatewayDevModeBypass  ConfigKey = "gateway.dev_mode_bypass"

	// GatewayPublicURL drives boot-frozen CORS/CSP/WS-origin fences
	// (CanonicalGatewayOrigin) — it MUST stay restart-gated (ADR-044).
	GatewayPublicURL ConfigKey = "gateway.public_url"
	// GatewayPreviewEnabled gates /preview/ and serve_web. Read live — it is
	// deliberately NOT in RestartGatedKeys (ADR-044, FR-006/FR-007).
	GatewayPreviewEnabled ConfigKey = "gateway.preview_enabled"
	// ToolsWebServeWarmup is the dotted JSON path of the web_serve warmup
	// timeout in config.json. The on-disk key is still named
	// `tools.run_in_workspace.warmup_timeout_seconds` for backwards
	// compatibility with deployed configs — it is only used by the dev-mode
	// branch of web_serve, but renaming the persisted key would require
	// every operator to migrate their config.json. The constant is named
	// after the current tool to keep restart-gated key tracking readable.
	ToolsWebServeWarmup ConfigKey = "tools.run_in_workspace.warmup_timeout_seconds"
)
