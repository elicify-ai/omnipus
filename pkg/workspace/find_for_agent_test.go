// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// TestFindForAgent_SingleWorkspace verifies the common case: an agent is a
// member of exactly one workspace's core_team, and FindForAgent returns that
// workspace's id.
func TestFindForAgent_SingleWorkspace(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-solo", `{"id":"ws-solo","core_team":["mia","jim"]}`)

	id, found := FindForAgent(home, "jim")
	if !found {
		t.Fatal("expected FindForAgent to find jim in ws-solo")
	}
	if id != "ws-solo" {
		t.Fatalf("FindForAgent id = %q, want %q", id, "ws-solo")
	}
}

// TestFindForAgent_NotFound verifies that an agent present in zero workspaces'
// core_team resolves to ("", false).
func TestFindForAgent_NotFound(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-a", `{"id":"ws-a","core_team":["mia"]}`)
	writeWS(t, home, "ws-b", `{"id":"ws-b","core_team":["jim"]}`)

	id, found := FindForAgent(home, "ray")
	if found {
		t.Fatalf("expected not found for an agent in no workspace, got id=%q", id)
	}
	if id != "" {
		t.Fatalf("expected empty id on not-found, got %q", id)
	}
}

// TestFindForAgent_EmptyAgentID verifies the empty-agentID guard short-circuits
// to not-found without scanning the directory.
func TestFindForAgent_EmptyAgentID(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-a", `{"id":"ws-a","core_team":["mia"]}`)

	id, found := FindForAgent(home, "")
	if found || id != "" {
		t.Fatalf("expected (\"\", false) for empty agentID, got (%q, %v)", id, found)
	}
}

// TestFindForAgent_NoWorkspacesDir verifies a missing workspaces/ directory
// (fresh install, nothing seeded yet) resolves to not-found rather than error.
func TestFindForAgent_NoWorkspacesDir(t *testing.T) {
	home := t.TempDir() // no workspaces/ subdirectory created

	id, found := FindForAgent(home, "mia")
	if found || id != "" {
		t.Fatalf("expected (\"\", false) when workspaces dir is absent, got (%q, %v)", id, found)
	}
}

// TestFindForAgent_AmbiguousMembership_DeterministicAndWarns proves that when
// an agent appears in TWO workspaces' core_team (a real, reachable state today
// since nothing enforces cross-workspace uniqueness — see FindForAgent's doc
// comment), the result is deterministic (first by sorted workspace id) and a
// WARN is logged naming the ambiguity rather than silently picking one.
func TestFindForAgent_AmbiguousMembership_DeterministicAndWarns(t *testing.T) {
	home := t.TempDir()
	// "ws-z" sorts after "ws-a" lexically, so "ws-a" must win regardless of
	// filesystem iteration order.
	writeWS(t, home, "ws-z", `{"id":"ws-z","core_team":["ambiguous-agent"]}`)
	writeWS(t, home, "ws-a", `{"id":"ws-a","core_team":["ambiguous-agent"]}`)

	// Capture WARN output via the established file-logging test hook (see
	// pkg/agent/switch_compress_test.go for the same pattern): logger has no
	// exported io.Writer sink for the console logger, so tests that must
	// assert on WARN content redirect through EnableFileLogging instead.
	logFile := filepath.Join(t.TempDir(), "find-for-agent-ambiguous.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	id, found := FindForAgent(home, "ambiguous-agent")
	if !found {
		t.Fatal("expected FindForAgent to find the ambiguous agent in at least one workspace")
	}
	if id != "ws-a" {
		t.Fatalf("FindForAgent id = %q, want deterministic first-by-sorted-id-order %q", id, "ws-a")
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)
	if !strings.Contains(logged, "more than one workspace") {
		t.Errorf("log file missing the ambiguous-membership WARN marker; got:\n%s", logged)
	}
	if !strings.Contains(logged, "ambiguous-agent") {
		t.Errorf("log file missing the agent_id; got:\n%s", logged)
	}
	if !strings.Contains(logged, "ws-a") || !strings.Contains(logged, "ws-z") {
		t.Errorf("log file must name BOTH claiming workspaces (ws-a, ws-z); got:\n%s", logged)
	}
	if !strings.Contains(logged, `"level":"warn"`) {
		t.Errorf("log file missing the warn level; got:\n%s", logged)
	}
}

// TestFindForAgent_MalformedFileDoesNotAbortScan verifies that a malformed
// workspace JSON file sitting alongside valid ones does not abort the whole
// scan — the malformed file is skipped and a valid match elsewhere is still
// found, mirroring ResolveDefaultID's malformed-file tolerance.
func TestFindForAgent_MalformedFileDoesNotAbortScan(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-broken", `{not valid json`)
	writeWS(t, home, "ws-good", `{"id":"ws-good","core_team":["ava"]}`)

	id, found := FindForAgent(home, "ava")
	if !found {
		t.Fatal("expected FindForAgent to find ava in ws-good despite a malformed sibling file")
	}
	if id != "ws-good" {
		t.Fatalf("FindForAgent id = %q, want %q", id, "ws-good")
	}
}

// TestFindForAgent_FallsBackToFilenameWhenIDFieldEmpty verifies that when the
// on-disk record's "id" field is empty/absent, the workspace id is derived
// from the filename (mirrors ResolveDefaultID's same fallback).
func TestFindForAgent_FallsBackToFilenameWhenIDFieldEmpty(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-from-filename", `{"core_team":["ray"]}`)

	id, found := FindForAgent(home, "ray")
	if !found {
		t.Fatal("expected FindForAgent to find ray")
	}
	if id != "ws-from-filename" {
		t.Fatalf("FindForAgent id = %q, want filename-derived %q", id, "ws-from-filename")
	}
}
