// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestConfig_NoContextWindowDefaultKey — ADR-066 spec test 22 (US-11, FR-004),
// the `summarize_token_percent` half owned by T066-03. The `context_window`
// half (AgentDefaults.ContextWindow + OMNIPUS_AGENTS_DEFAULTS_CONTEXT_WINDOW)
// is asserted by T066-09 once the D2 resolver lands.
//
// The legacy summariser's percentage knob survived the ADR-028 decommission
// only to scale the timeout-recovery trim trigger. ADR-066 D6 makes every
// consumer read the one budget B, so the knob has no reader left: the Go
// field, its JSON key and its env var are gone, and a stale key in an
// operator's config.json is ignored without error (greenfield rule — no
// migration, no rejection).
func TestConfig_NoContextWindowDefaultKey(t *testing.T) {
	t.Run("AgentDefaults has no SummarizeTokenPercent field", func(t *testing.T) {
		typ := reflect.TypeOf(AgentDefaults{})
		if _, found := typ.FieldByName("SummarizeTokenPercent"); found {
			t.Fatal("AgentDefaults.SummarizeTokenPercent must be deleted (FR-004)")
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if strings.Contains(f.Tag.Get("json"), "summarize_token_percent") {
				t.Fatalf("field %s still carries the summarize_token_percent JSON key", f.Name)
			}
			if strings.Contains(f.Tag.Get("env"), "SUMMARIZE_TOKEN_PERCENT") {
				t.Fatalf("field %s still carries the SUMMARIZE_TOKEN_PERCENT env var", f.Name)
			}
		}
	})

	t.Run("stale summarize_token_percent key is silently ignored", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		raw := `{"version": 1, "agents": {"defaults": {"summarize_token_percent": 75, "max_tokens": 1234}}}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("a stale summarize_token_percent key must load without error, got: %v", err)
		}
		if cfg.Agents.Defaults.MaxTokens != 1234 {
			t.Fatalf("sibling keys must still load; max_tokens = %d", cfg.Agents.Defaults.MaxTokens)
		}
		if _, stashed := cfg.UnknownFields["summarize_token_percent"]; stashed {
			t.Fatal("a nested stale key must not be round-tripped as a top-level unknown field")
		}
	})

	t.Run("saved config never re-emits the key", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := SaveConfig(path, DefaultConfig()); err != nil {
			t.Fatal(err)
		}
		out, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "summarize_token_percent") {
			t.Fatal("SaveConfig must not write summarize_token_percent")
		}
	})

	// --- the `context_window` half (T066-09, FR-004): the single home of the
	// global default is ContextSettings.DefaultContextWindow; the old
	// agents.defaults.context_window key and its env var are gone, and a
	// stale key in an operator's config.json is ignored without error.
	t.Run("AgentDefaults has no ContextWindow field", func(t *testing.T) {
		typ := reflect.TypeOf(AgentDefaults{})
		if _, found := typ.FieldByName("ContextWindow"); found {
			t.Fatal("AgentDefaults.ContextWindow must be deleted (FR-004) — the single home is ContextSettings.DefaultContextWindow")
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if strings.Contains(f.Tag.Get("json"), "context_window") {
				t.Fatalf("field %s still carries the context_window JSON key", f.Name)
			}
			if strings.Contains(f.Tag.Get("env"), "CONTEXT_WINDOW") {
				t.Fatalf("field %s still carries the CONTEXT_WINDOW env var", f.Name)
			}
		}
	})

	t.Run("stale agents.defaults.context_window key is silently ignored", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		raw := `{"version": 1, "agents": {"defaults": {"context_window": 131072, "max_tokens": 1234}}}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("a stale context_window key must load without error, got: %v", err)
		}
		if cfg.Agents.Defaults.MaxTokens != 1234 {
			t.Fatalf("sibling keys must still load; max_tokens = %d", cfg.Agents.Defaults.MaxTokens)
		}
		if cfg.Context.DefaultContextWindow != nil {
			t.Fatalf("a stale agents.defaults.context_window must NOT migrate into context.default_context_window (greenfield, no migration); got %d", *cfg.Context.DefaultContextWindow)
		}
	})

	t.Run("OMNIPUS_AGENTS_DEFAULTS_CONTEXT_WINDOW has no effect", func(t *testing.T) {
		t.Setenv("OMNIPUS_AGENTS_DEFAULTS_CONTEXT_WINDOW", "4096")
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(`{"version": 1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Context.DefaultContextWindow != nil {
			t.Fatalf("the retired env var must not populate any window; got %d", *cfg.Context.DefaultContextWindow)
		}
	})

	t.Run("saved config never re-emits agents.defaults.context_window", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := SaveConfig(path, DefaultConfig()); err != nil {
			t.Fatal(err)
		}
		out, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatal(err)
		}
		if agents, ok := doc["agents"].(map[string]any); ok {
			if defaults, ok := agents["defaults"].(map[string]any); ok {
				if _, has := defaults["context_window"]; has {
					t.Fatal("SaveConfig must not write agents.defaults.context_window")
				}
			}
		}
	})

	t.Run("config.example.json carries no agents.defaults.context_window", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join("..", "..", "config", "config.example.json"))
		if err != nil {
			t.Skipf("example config not readable from the package dir: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if agents, ok := doc["agents"].(map[string]any); ok {
			if defaults, ok := agents["defaults"].(map[string]any); ok {
				if _, has := defaults["context_window"]; has {
					t.Fatal("config/config.example.json still documents the retired agents.defaults.context_window key")
				}
			}
		}
	})
}

// TestDefaultConfig_ContextSettings — ADR-066 B-44 (US-11.AC1 "read
// defaults"), FR-010 and FR-036's config half: the seeded ContextSettings a
// fresh install reads, under the one key `context`, with the wire field names
// pinned by the A-CONTRACT amendment (§1.1). `warn_threshold` is
// config-internal only and deliberately NOT on the wire.
func TestDefaultConfig_ContextSettings(t *testing.T) {
	cs := DefaultConfig().Context

	want := ContextSettings{
		McpResultCap:         62_500,
		BuiltinSuccessCap:    64_000,
		BuiltinFailureCap:    10_000,
		WarnThreshold:        25_000,
		AbsoluteTriggerChars: 400_000,
		IngestBoundBytes:     8_000_000,
		DefaultContextWindow: nil,
		ModelOverrides:       []ContextModelOverride{},
	}
	if !reflect.DeepEqual(cs, want) {
		t.Fatalf("DefaultConfig().Context = %+v, want %+v", cs, want)
	}

	// Every operator-facing field carries the contract's JSON key, and the
	// section itself is the single `context` key on Config.
	typ := reflect.TypeOf(ContextSettings{})
	wantTags := map[string]string{
		"McpResultCap":         "mcp_result_cap",
		"BuiltinSuccessCap":    "builtin_success_cap",
		"BuiltinFailureCap":    "builtin_failure_cap",
		"WarnThreshold":        "warn_threshold",
		"AbsoluteTriggerChars": "absolute_trigger_chars",
		"IngestBoundBytes":     "ingest_bound_bytes",
		"DefaultContextWindow": "default_context_window,omitempty",
		"ModelOverrides":       "model_overrides",
	}
	for name, tag := range wantTags {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Errorf("ContextSettings is missing field %s", name)
			continue
		}
		if got := f.Tag.Get("json"); got != tag {
			t.Errorf("ContextSettings.%s json tag = %q, want %q", name, got, tag)
		}
	}
	cf, ok := reflect.TypeOf(Config{}).FieldByName("Context")
	if !ok {
		t.Fatal("Config must carry a Context ContextSettings field")
	}
	if got := cf.Tag.Get("json"); got != "context" {
		t.Fatalf("Config.Context json tag = %q, want %q", got, "context")
	}

	// A config.json with no `context` section (or a partial one) inherits the
	// seed for every field it does not name — settings live in ONE place
	// (FR-010) and an old file never yields zero caps.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"version": 1, "context": {"mcp_result_cap": 150000, "model_overrides": [{"provider": "openai", "model": "gpt-5", "context_window": 32768}]}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Context
	if got.McpResultCap != 150_000 {
		t.Errorf("mcp_result_cap = %d, want 150000", got.McpResultCap)
	}
	if got.BuiltinSuccessCap != 64_000 || got.BuiltinFailureCap != 10_000 || got.WarnThreshold != 25_000 ||
		got.AbsoluteTriggerChars != 400_000 || got.IngestBoundBytes != 8_000_000 || got.DefaultContextWindow != nil {
		t.Errorf("unnamed fields must keep the seed; got %+v", got)
	}
	wantOv := []ContextModelOverride{{Provider: "openai", Model: "gpt-5", ContextWindow: 32_768}}
	if !reflect.DeepEqual(got.ModelOverrides, wantOv) {
		t.Errorf("model_overrides = %+v, want %+v", got.ModelOverrides, wantOv)
	}
}
