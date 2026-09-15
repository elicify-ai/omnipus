// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Given credentials registered on a config (ADR-004 boot step 6)
// When text reaches the gateway log or the Verbose-chat detail
// Then every registered credential longer than 3 bytes is scrubbed there too,
// by the same rule as the config's own replacer; a re-registration replaces
// the published set (a rotated credential is no longer matched, its successor
// is); registering nothing removes it; and the gateway log file itself carries
// the scrub marker in the credential's place.
func TestRegisterSensitiveValues_PublishesTheScrubberForLogsAndDetail(t *testing.T) {
	t.Cleanup(logger.SetSensitiveValueReplacer(nil))
	const first, second = "sk-live-PUBLISH-first-1", "sk-live-PUBLISH-second-2"

	cfg := &Config{}
	cfg.RegisterSensitiveValues([]string{first, "abc"})
	in := "key=" + first + " short=abc"
	if got, want := logger.ScrubSensitiveValues(in), "key=[FILTERED] short=abc"; got != want {
		t.Errorf("published scrub = %q, want %q", got, want)
	}
	if got, own := logger.ScrubSensitiveValues(in), cfg.SensitiveDataReplacer().Replace(in); got != own {
		t.Errorf("published scrub %q disagrees with the config's own replacer %q", got, own)
	}

	cfg.RegisterSensitiveValues([]string{second})
	if got, want := logger.ScrubSensitiveValues(first+" "+second), first+" [FILTERED]"; got != want {
		t.Errorf("after re-registration scrub = %q, want %q", got, want)
	}

	cfg.RegisterSensitiveValues(nil)
	if got := logger.ScrubSensitiveValues(second); got != second {
		t.Errorf("after registering nothing scrub = %q, want the text as given", got)
	}

	cfg.RegisterSensitiveValues([]string{second})
	path := filepath.Join(t.TempDir(), "gateway.log")
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)
	logger.WarnCF("test", "provider refused the call",
		map[string]any{"error": "Incorrect API key provided: " + second})
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(b), second) || !strings.Contains(string(b), "Incorrect API key provided: [FILTERED]") {
		t.Errorf("gateway log line does not carry the scrub marker in the credential's place:\n%s", b)
	}
}
