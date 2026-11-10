package config

import (
	"strings"
	"testing"
)

// Fix round 9 (issue #1340, founder ruling 2026-10-19): the global default
// fallback list agents.defaults.model_fallbacks is removed. A config.json that
// still carries it must load normally and log exactly one "removed setting,
// ignored" warning — never break the load, never drop it silently.
func TestLoadConfig_RetiredModelFallbacksWarnsOnceAndLoads(t *testing.T) {
	readLog := captureLoggerWarnings(t)
	data := []byte(`{"agents":{"defaults":{"model_fallbacks":["openai/gpt-4o-mini","anthropic/haiku"]}}}`)
	cfg, err := loadConfig(data)
	if err != nil || cfg == nil {
		t.Fatalf("a stale model_fallbacks key must not break the load: cfg=%v err=%v", cfg, err)
	}
	logged := readLog()
	if n := strings.Count(logged, "agents.defaults.model_fallbacks"); n != 1 {
		t.Fatalf("want exactly one warning naming agents.defaults.model_fallbacks, found %d; log:\n%s", n, logged)
	}
	if !strings.Contains(logged, "removed setting, ignored") {
		t.Errorf("the warning must say the setting is removed and ignored; log:\n%s", logged)
	}
}

// Control: a config without the key logs no such warning.
func TestLoadConfig_NoRetiredModelFallbacksNoWarning(t *testing.T) {
	readLog := captureLoggerWarnings(t)
	if _, err := loadConfig([]byte(`{"agents":{"defaults":{}}}`)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readLog(), "model_fallbacks") {
		t.Error("a config without model_fallbacks must not warn about it")
	}
}
