// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_memory_recap.go — the Memory settings recap slots and limits
// (effort-levels spec FR-072, FR-074, S-60, S-79, S-80, S-81): the recap
// provider and effort, and the ranged recap limits with their shipped
// defaults and refusal sentence.

import (
	"fmt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

type recapLimitField struct {
	limit config.RecapLimit
	value func(*gen.MemorySettings) **int
	read  func(*config.AgentDefaults) int
}

// recapLimitFields lists every ranged Memory setting, request pointer and
// effective getter together, so the writer, the refusal and the read can
// never disagree on a key.
var recapLimitFields = []recapLimitField{
	{config.RecapLimitMaxOutputTokens, func(m *gen.MemorySettings) **int { return &m.RecapMaxOutputTokens }, (*config.AgentDefaults).GetRecapMaxOutputTokens},
	{config.RecapLimitInputTokens, func(m *gen.MemorySettings) **int { return &m.RecapInputTokens }, (*config.AgentDefaults).GetRecapInputTokens},
	{config.RecapLimitSummaryWords, func(m *gen.MemorySettings) **int { return &m.RecapSummaryWords }, (*config.AgentDefaults).GetRecapSummaryWords},
	{config.RecapLimitListItems, func(m *gen.MemorySettings) **int { return &m.RecapListItems }, (*config.AgentDefaults).GetRecapListItems},
	{config.RecapLimitFallbackContextChars, func(m *gen.MemorySettings) **int { return &m.RecapFallbackContextChars }, (*config.AgentDefaults).GetRecapFallbackContextChars},
	{config.RecapLimitTimeoutSeconds, func(m *gen.MemorySettings) **int { return &m.RecapTimeoutSeconds }, (*config.AgentDefaults).GetRecapTimeoutSeconds},
	{config.RecapLimitStreamRetries, func(m *gen.MemorySettings) **int { return &m.RecapStreamRetries }, (*config.AgentDefaults).GetRecapStreamRetries},
	{config.RecapLimitRetryPauseMs, func(m *gen.MemorySettings) **int { return &m.RecapRetryPauseMs }, (*config.AgentDefaults).GetRecapRetryPauseMs},
	{config.RecapLimitCharsPerToken, func(m *gen.MemorySettings) **int { return &m.RecapCharsPerToken }, (*config.AgentDefaults).GetRecapCharsPerToken},
	{config.RecapLimitMinSpacingSeconds, func(m *gen.MemorySettings) **int { return &m.BootstrapRecapMinSpacingSeconds }, (*config.AgentDefaults).GetBootstrapRecapMinSpacingSeconds},
	{config.RecapLimitIdleTimeoutMinutes, func(m *gen.MemorySettings) **int { return &m.IdleTimeoutMinutes }, (*config.AgentDefaults).GetIdleTimeoutMinutes},
	{config.RecapLimitBootstrapMaxPerMinute, func(m *gen.MemorySettings) **int { return &m.BootstrapRecapMaxPerMinute }, (*config.AgentDefaults).GetBootstrapRecapMaxPerMinute},
}

// recapLimitRefusal returns "<key> must be between <min> and <max>" for the
// first sent value outside its range, or "" when every sent value fits.
func recapLimitRefusal(req *gen.MemorySettings) string {
	for _, f := range recapLimitFields {
		v := *f.value(req)
		if v != nil && (*v < f.limit.Min || *v > f.limit.Max) {
			return fmt.Sprintf("%s must be between %d and %d", f.limit.Key, f.limit.Min, f.limit.Max)
		}
	}
	return ""
}

// writeRecapSettings stores the sent recap limits, provider and effort
// under agents.defaults; omitted keys keep their stored values.
func writeRecapSettings(defaults map[string]any, req *gen.MemorySettings) {
	for _, f := range recapLimitFields {
		if v := *f.value(req); v != nil {
			defaults[f.limit.Key] = *v
		}
	}
	if req.RecapProvider != nil {
		defaults["recap_provider"] = *req.RecapProvider
	}
	if req.RecapReasoningEffort != nil {
		defaults["recap_reasoning_effort"] = *req.RecapReasoningEffort
	}
}

// applyRecapSettingsRead fills the effective recap limits (shipped defaults
// when unset, S-79), the recap provider and the stored recap effort.
func applyRecapSettingsRead(resp *gen.MemorySettings, d *config.AgentDefaults) {
	for _, f := range recapLimitFields {
		v := f.read(d)
		*f.value(resp) = &v
	}
	provider := d.RecapProvider
	resp.RecapProvider = &provider
	resp.RecapReasoningEffort = wireEffort(d.RecapReasoningEffort)
}
