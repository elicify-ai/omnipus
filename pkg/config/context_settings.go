// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

// ContextSettings — ADR-066 D9: the global context-budget controls, persisted
// under the single `context` key of config.json and seeded by DefaultConfig.
//
// Field names mirror the generated wire type (contracts/components/schemas/
// ContextSettings.yaml, pinned by the A-CONTRACT amendment §1.1) so the
// settings handler (T066-16) maps 1:1 with no translation table. The one
// exception is WarnThreshold, which is config-internal only — it is NOT on
// the wire (spec §1.1); adding it would need its own contract commit.
//
// This is NOT a wire type: the gateway converts to/from the generated
// ContextSettings at the handler boundary (Constraint #8).
//
// Validation (the 400 rules in the contract's ContextSettingsUpdate
// description) is the handler's job; this struct only stores what was
// accepted. Every consumer reads it through the live config per call
// (FR-010) — there is no opt-out and no boot-time snapshot.
type ContextSettings struct {
	// McpResultCap is the per-result cap (chars) for a successful MCP tool
	// result entering the window (D4 "mcp" surface). Default 62,500;
	// ceiling 150,000.
	McpResultCap int `json:"mcp_result_cap"`

	// BuiltinSuccessCap is the per-result cap (chars) for a successful
	// builtin tool result, hydrated attachment, recall page or delegate
	// report (D4 "builtin-success" surface). Default 64,000; ceiling 150,000.
	BuiltinSuccessCap int `json:"builtin_success_cap"`

	// BuiltinFailureCap is the per-result cap (chars) for a failed, denied
	// or skipped tool result — builtin or MCP (D4 "builtin-failure"
	// surface). Default 10,000; ceiling 150,000.
	BuiltinFailureCap int `json:"builtin_failure_cap"`

	// WarnThreshold is the per-result size (chars) above which the choke
	// point logs one WARN naming the producer (FR-010). Default 25,000.
	// Config-internal: not served by /settings/context.
	WarnThreshold int `json:"warn_threshold"`

	// AbsoluteTriggerChars is the absolute tool-result share trigger (chars)
	// for the mid-turn window check (D6); the token share is this ÷ 2.5.
	// Default 400,000.
	AbsoluteTriggerChars int `json:"absolute_trigger_chars"`

	// IngestBoundBytes is the maximum bytes read from any network or
	// subprocess source at ingest (D10). Must stay strictly below
	// 8,388,608 (0.8 × the archive line size). Default 8,000,000.
	IngestBoundBytes int `json:"ingest_bound_bytes"`

	// DefaultContextWindow is the global default context window (tokens),
	// D2 rung 3 — the single home of this setting. nil when unset. Clamped
	// to the model's capability on resolution.
	DefaultContextWindow *int `json:"default_context_window,omitempty"`

	// ModelOverrides are the per-(provider, model) context-window overrides
	// (D2 rung 2). Always a non-nil slice so the wire echoes [] rather than
	// null.
	ModelOverrides []ContextModelOverride `json:"model_overrides"`
}

// ContextModelOverride pins the context window (tokens) for one
// (provider, model) pair — D2 rung 2 of the window-resolution ladder.
type ContextModelOverride struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ContextWindow int    `json:"context_window"`
}

// Seeded ContextSettings values (ADR-066 D4/D6/D10 defaults, B-44). These
// are install-time seed data an operator edits afterward — not fallbacks:
// a config.json that omits the `context` section, or any field within it,
// inherits these because loadConfig unmarshals over DefaultConfig().
const (
	DefaultMcpResultCap         = 62_500
	DefaultBuiltinSuccessCap    = 64_000
	DefaultBuiltinFailureCap    = 10_000
	DefaultContextWarnThreshold = 25_000
	DefaultAbsoluteTriggerChars = 400_000
	DefaultIngestBoundBytes     = 8_000_000
)

// DefaultContextSettings returns the seeded ContextSettings for a fresh
// install. DefaultContextWindow is unset (nil) and ModelOverrides is an
// empty, non-nil slice.
func DefaultContextSettings() ContextSettings {
	return ContextSettings{
		McpResultCap:         DefaultMcpResultCap,
		BuiltinSuccessCap:    DefaultBuiltinSuccessCap,
		BuiltinFailureCap:    DefaultBuiltinFailureCap,
		WarnThreshold:        DefaultContextWarnThreshold,
		AbsoluteTriggerChars: DefaultAbsoluteTriggerChars,
		IngestBoundBytes:     DefaultIngestBoundBytes,
		DefaultContextWindow: nil,
		ModelOverrides:       []ContextModelOverride{},
	}
}
