package config

import (
	"testing"
	"time"
)

// Founder decision 2026-09-14: the streaming silence limit is a per-model
// config value (`model_list[].stream_stall_timeout`, seconds) whose shipped
// default is five minutes. These expectations come from the decision, not
// from the implementation.
func TestEffectiveStreamStallTimeout(t *testing.T) {
	if DefaultStreamStallTimeoutSeconds != 300 {
		t.Fatalf("shipped default = %d seconds, want 300", DefaultStreamStallTimeoutSeconds)
	}

	var zero ModelConfig
	if got := zero.EffectiveStreamStallTimeout(); got != 5*time.Minute {
		t.Fatalf("zero-value row must resolve to 5m; got %s", got)
	}

	var negative ModelConfig
	negative.StreamStallTimeout = -30
	if got := negative.EffectiveStreamStallTimeout(); got != 5*time.Minute {
		t.Fatalf("negative row must resolve to the default, not a negative limit; got %s", got)
	}

	var overridden ModelConfig
	overridden.StreamStallTimeout = 90
	if got := overridden.EffectiveStreamStallTimeout(); got != 90*time.Second {
		t.Fatalf("explicit row value must win; got %s", got)
	}

	var one ModelConfig
	one.StreamStallTimeout = 1
	if got := one.EffectiveStreamStallTimeout(); got != 1*time.Second {
		t.Fatalf("1s is a legal explicit limit; got %s", got)
	}
}
