package config

import (
	"fmt"
	"log/slog"
	"sync"
)

// RecapLimit describes one recap limit setting (effort-levels spec "Recap
// limits", FD-20, FD-21, FD-23, FR-074): its JSON key under agents.defaults,
// its shipped default and its allowed range.
type RecapLimit struct {
	Key     string
	Default int
	Min     int
	Max     int
}

// Recap limit keys and ranges. The REST writer, the settings screen and the
// getters below share these numbers.
var (
	RecapLimitMaxOutputTokens        = RecapLimit{"recap_max_output_tokens", 2048, 512, 16384}
	RecapLimitInputTokens            = RecapLimit{"recap_input_tokens", 2000, 500, 32000}
	RecapLimitSummaryWords           = RecapLimit{"recap_summary_words", 150, 50, 500}
	RecapLimitListItems              = RecapLimit{"recap_list_items", 5, 1, 10}
	RecapLimitFallbackContextChars   = RecapLimit{"recap_fallback_context_chars", 1500, 200, 8000}
	RecapLimitTimeoutSeconds         = RecapLimit{"recap_timeout_seconds", 60, 15, 300}
	RecapLimitStreamRetries          = RecapLimit{"recap_stream_retries", 2, 0, 5}
	RecapLimitRetryPauseMs           = RecapLimit{"recap_retry_pause_ms", 500, 100, 5000}
	RecapLimitCharsPerToken          = RecapLimit{"recap_chars_per_token", 4, 2, 8}
	RecapLimitMinSpacingSeconds      = RecapLimit{"bootstrap_recap_min_spacing_seconds", 1, 1, 60}
	RecapLimitIdleTimeoutMinutes     = RecapLimit{"idle_timeout_minutes", 30, 1, 1440}
	RecapLimitBootstrapMaxPerMinute  = RecapLimit{"bootstrap_recap_max_per_minute", 5, 1, 60}
	recapLimitsInScreenOrderForCheck = []RecapLimit{
		RecapLimitMaxOutputTokens, RecapLimitInputTokens, RecapLimitSummaryWords,
		RecapLimitListItems, RecapLimitFallbackContextChars, RecapLimitTimeoutSeconds,
		RecapLimitStreamRetries, RecapLimitRetryPauseMs, RecapLimitCharsPerToken,
		RecapLimitMinSpacingSeconds, RecapLimitIdleTimeoutMinutes, RecapLimitBootstrapMaxPerMinute,
	}
)

// RecapLimits returns every range-checked recap setting: the ten recap limits
// plus the two existing recap settings (idle_timeout_minutes,
// bootstrap_recap_max_per_minute).
func RecapLimits() []RecapLimit {
	out := make([]RecapLimit, len(recapLimitsInScreenOrderForCheck))
	copy(out, recapLimitsInScreenOrderForCheck)
	return out
}

// CheckRange returns the fixed refusal text when v is outside the range, or
// "" when v is allowed: "<key> must be between <min> and <max>".
func (l RecapLimit) CheckRange(v int) string {
	if v < l.Min || v > l.Max {
		return fmt.Sprintf("%s must be between %d and %d", l.Key, l.Min, l.Max)
	}
	return ""
}

// clampWarned records which (key, saved value) pairs already logged their
// clamp warning, so repeated use warns once per key.
var clampWarned sync.Map

// use returns the value in use for a saved value: the default when the key is
// missing, otherwise the saved value clamped to the range. A clamped value
// logs one warning naming the key, the saved value and the value in use; the
// saved configuration is never rewritten.
func (l RecapLimit) use(saved int, missing bool) int {
	if missing {
		return l.Default
	}
	inUse := saved
	switch {
	case saved < l.Min:
		inUse = l.Min
	case saved > l.Max:
		inUse = l.Max
	default:
		return saved
	}
	if _, seen := clampWarned.LoadOrStore(fmt.Sprintf("%s=%d", l.Key, saved), true); !seen {
		slog.Warn("recap setting outside its range; using the nearest allowed value",
			"key", l.Key, "saved", saved, "in_use", inUse, "min", l.Min, "max", l.Max)
	}
	return inUse
}

// GetRecapMaxOutputTokens returns recap_max_output_tokens (default 2048).
func (d *AgentDefaults) GetRecapMaxOutputTokens() int {
	return RecapLimitMaxOutputTokens.use(d.RecapMaxOutputTokens, d.RecapMaxOutputTokens == 0)
}

// GetRecapInputTokens returns recap_input_tokens (default 2000).
func (d *AgentDefaults) GetRecapInputTokens() int {
	return RecapLimitInputTokens.use(d.RecapInputTokens, d.RecapInputTokens == 0)
}

// GetRecapSummaryWords returns recap_summary_words (default 150).
func (d *AgentDefaults) GetRecapSummaryWords() int {
	return RecapLimitSummaryWords.use(d.RecapSummaryWords, d.RecapSummaryWords == 0)
}

// GetRecapListItems returns recap_list_items (default 5).
func (d *AgentDefaults) GetRecapListItems() int {
	return RecapLimitListItems.use(d.RecapListItems, d.RecapListItems == 0)
}

// GetRecapFallbackContextChars returns recap_fallback_context_chars (default 1500).
func (d *AgentDefaults) GetRecapFallbackContextChars() int {
	return RecapLimitFallbackContextChars.use(d.RecapFallbackContextChars, d.RecapFallbackContextChars == 0)
}

// GetRecapTimeoutSeconds returns recap_timeout_seconds (default 60).
func (d *AgentDefaults) GetRecapTimeoutSeconds() int {
	return RecapLimitTimeoutSeconds.use(d.RecapTimeoutSeconds, d.RecapTimeoutSeconds == 0)
}

// GetRecapStreamRetries returns recap_stream_retries (default 2). A stored 0
// is a valid choice (no retries); only a missing key means the default.
func (d *AgentDefaults) GetRecapStreamRetries() int {
	if d.RecapStreamRetries == nil {
		return RecapLimitStreamRetries.Default
	}
	return RecapLimitStreamRetries.use(*d.RecapStreamRetries, false)
}

// GetRecapRetryPauseMs returns recap_retry_pause_ms (default 500).
func (d *AgentDefaults) GetRecapRetryPauseMs() int {
	return RecapLimitRetryPauseMs.use(d.RecapRetryPauseMs, d.RecapRetryPauseMs == 0)
}

// GetRecapCharsPerToken returns recap_chars_per_token (default 4).
func (d *AgentDefaults) GetRecapCharsPerToken() int {
	return RecapLimitCharsPerToken.use(d.RecapCharsPerToken, d.RecapCharsPerToken == 0)
}

// GetBootstrapRecapMinSpacingSeconds returns bootstrap_recap_min_spacing_seconds (default 1).
func (d *AgentDefaults) GetBootstrapRecapMinSpacingSeconds() int {
	return RecapLimitMinSpacingSeconds.use(d.BootstrapRecapMinSpacingSeconds, d.BootstrapRecapMinSpacingSeconds == 0)
}

// GetIdleTimeoutMinutes returns the idle timeout (default 30, range 1-1440).
// The restart catch-up uses the same wait (FD-21).
func (d *AgentDefaults) GetIdleTimeoutMinutes() int {
	return RecapLimitIdleTimeoutMinutes.use(d.IdleTimeoutMinutes, d.IdleTimeoutMinutes <= 0)
}

// GetBootstrapRecapMaxPerMinute returns the catch-up rate limit (default 5, range 1-60).
func (d *AgentDefaults) GetBootstrapRecapMaxPerMinute() int {
	return RecapLimitBootstrapMaxPerMinute.use(d.BootstrapRecapMaxPerMinute, d.BootstrapRecapMaxPerMinute <= 0)
}
