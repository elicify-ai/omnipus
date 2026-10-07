// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// dwLogLines enables file logging for the test and returns a reader of the
// JSON lines written so far.
func dwLogLines(t *testing.T) func() []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dw.log")
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)
	return func() []string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

// requireErrorLevelLine fails unless some log line carries the error id AND
// is at error level (#458: invariant breaks must not be a bare WARN).
func requireErrorLevelLine(t *testing.T, lines []string, errorID string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, `"error_id":"`+errorID+`"`) {
			if !strings.Contains(l, `"level":"error"`) {
				t.Fatalf("%s logged below error level: %s", errorID, l)
			}
			return
		}
	}
	t.Fatalf("no log line with error_id %s; lines: %v", errorID, lines)
}

// TestWireDelegationInjectors_DW001NilRegistryIsErrorLevel (#458): a nil live
// registry is an invariant break and must surface at error level with the
// DW-001 id, while still failing safe (empty block).
func TestWireDelegationInjectors_DW001NilRegistryIsErrorLevel(t *testing.T) {
	al, cb := wireTestLoopWithGraphAndMaxDepth(t, "jim", 0)
	lines := dwLogLines(t)

	orig := al.GetRegistry()
	al.mu.Lock()
	al.registry = nil
	al.mu.Unlock()
	t.Cleanup(func() { // al.Close dereferences the registry
		al.mu.Lock()
		al.registry = orig
		al.mu.Unlock()
	})

	got := cb.delegationInjector("")
	if got != "" {
		t.Fatalf("fail-safe: expected empty delegation block, got %q", got)
	}
	requireErrorLevelLine(t, lines(), "DW-001")
}

// TestWireDelegationInjectors_DW002AgentAbsentIsErrorLevel (#458): an agent
// missing from its own live registry mid-turn is an invariant break.
func TestWireDelegationInjectors_DW002AgentAbsentIsErrorLevel(t *testing.T) {
	al, cb := wireTestLoopWithGraphAndMaxDepth(t, "jim", 0)
	lines := dwLogLines(t)

	if !al.GetRegistry().RemoveAgent("jim") {
		t.Fatal("setup: RemoveAgent(jim) returned false")
	}

	got := cb.delegationInjector("")
	if got != "" {
		t.Fatalf("fail-safe: expected empty delegation block, got %q", got)
	}
	requireErrorLevelLine(t, lines(), "DW-002")
}
