// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"time"
)

// duration is a time.Duration that JSON-unmarshals from either a human-friendly
// string ("24h", "5s", "500ms") or a bare number interpreted as SECONDS — so
// config.json stays readable for the hour/minute/second TTLs FR-195 exposes
// (needs_input_ttl, cancel_grace, wake_debounce, idle_quiet_window). Marshals
// back as the canonical string form. A zero value means "unset" (the Effective*
// resolvers substitute the ADR default).
type duration time.Duration

// MarshalJSON emits the duration as time.Duration's canonical string form
// (e.g. "24h0m0s"), so a round-tripped config.json stays human-readable.
func (d duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts either a JSON string ("24h", "5s", "500ms") parsed via
// time.ParseDuration, or a JSON number interpreted as whole seconds (mirroring
// PlanningConfig's *_seconds convention so a bare 5 means 5s).
func (d *duration) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*d = 0
			return nil
		}
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = duration(parsed)
		return nil
	}
	// Bare number → seconds (PlanningConfig convention).
	var secs float64
	if err := json.Unmarshal(b, &secs); err != nil {
		return err
	}
	*d = duration(time.Duration(secs * float64(time.Second)))
	return nil
}

// String returns the canonical time.Duration string form.
func (d duration) String() string { return time.Duration(d).String() }

// ADR-053 §8 operability — the session_messaging config section (FR-195's 21
// keys). Defaults mirror the ADR §Contract Surface "Caps" and the constants in
// pkg/session/message_inbox.go / pkg/agent/async_notifier.go. Applied by
// validateBootConfig (validator.go) and seeded by DefaultConfig (defaults.go).
const (
	// Kill-switch trio (FR-196). enabled=false neuters all messaging live
	// (consumer no-ops, tools fail-closed); wake_enabled=false disables only
	// the wake path; adjudication_enabled=false neuters the goal/plan trigger.
	DefaultSessionMessagingEnabled             = true
	DefaultSessionMessagingWakeEnabled         = true
	DefaultSessionMessagingAdjudicationEnabled = true

	// Child-send caps (message_inbox.go defaults).
	DefaultSMChildSendRatePerMinute = 10
	DefaultSMChildSendBodyBytes     = 32 * 1024
	DefaultSMChildSendMaxDepth      = 5

	// Inbox ceilings (D15).
	DefaultSMInboxUnackedMax     = 200
	DefaultSMInboxPerTypeCeiling = 20

	// Steer/respond caps (message_inbox.go defaults).
	DefaultSMSteerRatePerMinute = 6
	DefaultSMSteerBodyBytes     = 16 * 1024

	// Timing.
	DefaultSMCancelGrace   = 5 * time.Second
	DefaultSMNeedsInputTTL = 24 * time.Hour

	// Wake debounce/rate (async_notifier.go constants).
	DefaultSMWakeDebounce   = 15 * time.Second
	DefaultSMWakeMaxPerHour = 4

	// Idle quiet window — parent turns quieter than this are not woken.
	DefaultSMIdleQuietWindow = 30 * time.Second

	// Verifier/round bounds (mirrors PlanningConfig but exposed here per
	// FR-195's enumeration; the adjudication subsystem reads its own
	// verifier key separately per the FR-195 note).
	DefaultSMAttemptsMax    = 3
	DefaultSMJudgeRoundsMax = 20

	// Token budget for the session-messaging plane's own accounting.
	DefaultSMTokenBudget = 0 // 0 = no per-plane token cap (FR-195 token_budget)

	// Retention horizons (days). 0 = retain indefinitely (sweep is advisory).
	DefaultSMMessageRetention     = 30
	DefaultSMAuditRetention       = 90
	DefaultSMUndeliveredRetention = 7
)

// SessionMessagingConfig holds the 21 session_messaging keys (FR-195). All
// live-reload-capable: the consumer and tools read Enabled/WakeEnabled per
// event via the Effective* resolvers (never a boot-time snapshot), so an
// operator flipping session_messaging.enabled in config.json takes effect
// without a restart (FR-196, SC-015). The numeric tunables resolve to the
// ADR §Contract Surface defaults when zero (no magic constants in the
// messaging path, SC-015).
type SessionMessagingConfig struct {
	// --- Kill-switch trio (FR-196) ---
	Enabled             bool `json:"enabled,omitempty"`
	WakeEnabled         bool `json:"wake_enabled,omitempty"`
	AdjudicationEnabled bool `json:"adjudication_enabled,omitempty"`

	// --- Child-send caps (3) ---
	ChildSendRatePerMinute int `json:"child_send_rate,omitempty"`
	ChildSendBody          int `json:"child_send_body,omitempty"`
	ChildSendDepth         int `json:"child_send_depth,omitempty"`

	// --- Inbox ceilings (2) ---
	InboxUnackedMax     int `json:"inbox_unacked_max,omitempty"`
	InboxPerTypeCeiling int `json:"inbox_per_type_ceiling,omitempty"`

	// --- Steer/respond caps (2) ---
	SteerRatePerMinute int `json:"steer_rate,omitempty"`
	SteerBody          int `json:"steer_body,omitempty"`

	// --- Timing (2) ---
	CancelGrace   duration `json:"cancel_grace,omitempty"`
	NeedsInputTTL duration `json:"needs_input_ttl,omitempty"`

	// --- Wake debounce/rate (2) ---
	WakeDebounce   duration `json:"wake_debounce,omitempty"`
	WakeMaxPerHour int      `json:"wake_max_per_hour,omitempty"`

	// --- Idle quiet window (1) ---
	IdleQuietWindow duration `json:"idle_quiet_window,omitempty"`

	// --- Token budget (1) ---
	TokenBudget int `json:"token_budget,omitempty"`

	// --- Round/attempt bounds (2) ---
	AttemptsMax    int `json:"attempts_max,omitempty"`
	JudgeRoundsMax int `json:"judge_rounds_max,omitempty"`

	// --- Retention horizons in days (3) ---
	MessageRetention     int `json:"message_retention,omitempty"`
	AuditRetention       int `json:"audit_retention,omitempty"`
	UndeliveredRetention int `json:"undelivered_retention,omitempty"`
}

// EffectiveEnabled reports whether the session-messaging plane is live. When
// false, the consumer no-ops and the tools fail-closed (FR-196 global kill
// switch). Read live per event — never cached at boot.
func (c SessionMessagingConfig) EffectiveEnabled() bool {
	return c.Enabled
}

// EffectiveWakeEnabled reports whether the bounded typed-wake path is live
// (FR-196). When false, messages still reach the durable inbox but no wake
// fires. Read live per event.
func (c SessionMessagingConfig) EffectiveWakeEnabled() bool {
	return c.WakeEnabled
}

// EffectiveChildSendRatePerMinute resolves the child-send rate cap (SC-015:
// every numeric tunable resolves from a key, no magic constants).
func (c SessionMessagingConfig) EffectiveChildSendRatePerMinute() int {
	if c.ChildSendRatePerMinute >= 1 {
		return c.ChildSendRatePerMinute
	}
	return DefaultSMChildSendRatePerMinute
}

// EffectiveChildSendBodyBytes resolves the child-send body cap.
func (c SessionMessagingConfig) EffectiveChildSendBodyBytes() int {
	if c.ChildSendBody >= 1 {
		return c.ChildSendBody
	}
	return DefaultSMChildSendBodyBytes
}

// EffectiveChildSendMaxDepth resolves the child-send hop-depth cap.
func (c SessionMessagingConfig) EffectiveChildSendMaxDepth() int {
	if c.ChildSendDepth >= 1 {
		return c.ChildSendDepth
	}
	return DefaultSMChildSendMaxDepth
}

// EffectiveInboxUnackedMax resolves the per-session unacked cap.
func (c SessionMessagingConfig) EffectiveInboxUnackedMax() int {
	if c.InboxUnackedMax >= 1 {
		return c.InboxUnackedMax
	}
	return DefaultSMInboxUnackedMax
}

// EffectiveInboxPerTypeCeiling resolves the D15 per-child question+blocker
// ceiling.
func (c SessionMessagingConfig) EffectiveInboxPerTypeCeiling() int {
	if c.InboxPerTypeCeiling >= 1 {
		return c.InboxPerTypeCeiling
	}
	return DefaultSMInboxPerTypeCeiling
}

// EffectiveSteerRatePerMinute resolves the steer/respond rate cap.
func (c SessionMessagingConfig) EffectiveSteerRatePerMinute() int {
	if c.SteerRatePerMinute >= 1 {
		return c.SteerRatePerMinute
	}
	return DefaultSMSteerRatePerMinute
}

// EffectiveSteerBodyBytes resolves the steer/respond body cap.
func (c SessionMessagingConfig) EffectiveSteerBodyBytes() int {
	if c.SteerBody >= 1 {
		return c.SteerBody
	}
	return DefaultSMSteerBodyBytes
}

// EffectiveCancelGrace resolves the cooperative-stop grace window.
func (c SessionMessagingConfig) EffectiveCancelGrace() time.Duration {
	if c.CancelGrace > 0 {
		return time.Duration(c.CancelGrace)
	}
	return DefaultSMCancelGrace
}

// EffectiveNeedsInputTTL resolves the needs_input park TTL (G-6).
func (c SessionMessagingConfig) EffectiveNeedsInputTTL() time.Duration {
	if c.NeedsInputTTL > 0 {
		return time.Duration(c.NeedsInputTTL)
	}
	return DefaultSMNeedsInputTTL
}
