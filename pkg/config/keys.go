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
	SandboxModeKey       ConfigKey = "sandbox.mode"
	SandboxAuditLog      ConfigKey = "sandbox.audit_log"
	SandboxAllowedPaths  ConfigKey = "sandbox.allowed_paths"
	SessionDMScope       ConfigKey = "session.dm_scope"
	GatewayPort          ConfigKey = "gateway.port"
	GatewayUsers         ConfigKey = "gateway.users"
	GatewayDevModeBypass ConfigKey = "gateway.dev_mode_bypass"

	// GatewayPreviewPort and related keys are preview-listener restart-gated keys (FR-027b).
	GatewayPreviewPort            ConfigKey = "gateway.preview_port"
	GatewayPreviewHost            ConfigKey = "gateway.preview_host"
	GatewayPreviewOrigin          ConfigKey = "gateway.preview_origin"
	GatewayPublicURL              ConfigKey = "gateway.public_url"
	GatewayPreviewListenerEnabled ConfigKey = "gateway.preview_listener_enabled"
	// ToolsWebServeWarmup is the dotted JSON path of the web_serve warmup
	// timeout in config.json. The on-disk key is still named
	// `tools.run_in_workspace.warmup_timeout_seconds` for backwards
	// compatibility with deployed configs — it is only used by the dev-mode
	// branch of web_serve, but renaming the persisted key would require
	// every operator to migrate their config.json. The constant is named
	// after the current tool to keep restart-gated key tracking readable.
	ToolsWebServeWarmup ConfigKey = "tools.run_in_workspace.warmup_timeout_seconds"
)
