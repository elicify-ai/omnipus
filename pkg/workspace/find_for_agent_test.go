// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
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

// TestFindForAgentPreferring_DisambiguatesMultiMembership proves the core
// contract: when an agent belongs to more than one workspace's core_team,
// passing the CURRENT turn's own workspace id as preferredWsID wins over
// FindForAgent's arbitrary sorted-first pick.
func TestFindForAgentPreferring_DisambiguatesMultiMembership(t *testing.T) {
	home := t.TempDir()
	// "ws-a" sorts before "ws-z", so a plain FindForAgent would pick "ws-a".
	writeWS(t, home, "ws-a", `{"id":"ws-a","core_team":["multi-agent"]}`)
	writeWS(t, home, "ws-z", `{"id":"ws-z","core_team":["multi-agent"]}`)

	id, found := FindForAgentPreferring(home, "multi-agent", "ws-z")
	if !found {
		t.Fatal("expected a match")
	}
	if id != "ws-z" {
		t.Fatalf("FindForAgentPreferring id = %q, want the preferred %q", id, "ws-z")
	}
}

// TestFindForAgentPreferring_FallsBackWhenPreferredNotAMember proves that a
// preferredWsID the agent does NOT actually belong to is ignored — it must
// not override or spoof membership, only disambiguate a real one.
func TestFindForAgentPreferring_FallsBackWhenPreferredNotAMember(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-real", `{"id":"ws-real","core_team":["solo-agent"]}`)
	writeWS(t, home, "ws-unrelated", `{"id":"ws-unrelated","core_team":["someone-else"]}`)

	id, found := FindForAgentPreferring(home, "solo-agent", "ws-unrelated")
	if !found {
		t.Fatal("expected a match via fallback to FindForAgent")
	}
	if id != "ws-real" {
		t.Fatalf("FindForAgentPreferring id = %q, want fallback to the agent's real workspace %q", id, "ws-real")
	}
}

// TestFindForAgentPreferring_EmptyPreferredMatchesFindForAgent proves that an
// empty preferredWsID (e.g. a delegated sub-turn, which has no turn-bound
// workspace_id) behaves identically to plain FindForAgent — no behavior
// change for callers with nothing to prefer.
func TestFindForAgentPreferring_EmptyPreferredMatchesFindForAgent(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-solo", `{"id":"ws-solo","core_team":["jim"]}`)

	want, wantFound := FindForAgent(home, "jim")
	got, gotFound := FindForAgentPreferring(home, "jim", "")
	if got != want || gotFound != wantFound {
		t.Fatalf(
			"FindForAgentPreferring(empty preferred) = (%q, %v), want FindForAgent's (%q, %v)",
			got, gotFound, want, wantFound,
		)
	}
}

// TestFindForAgentPreferring_InvalidPreferredIDFallsBack proves a
// traversal-unsafe preferredWsID (e.g. "../etc") is rejected by safeID and
// falls back to FindForAgent rather than being used to build a file path.
func TestFindForAgentPreferring_InvalidPreferredIDFallsBack(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-solo", `{"id":"ws-solo","core_team":["jim"]}`)

	id, found := FindForAgentPreferring(home, "jim", "../../etc")
	if !found || id != "ws-solo" {
		t.Fatalf(
			"FindForAgentPreferring with unsafe preferred id = (%q, %v), want fallback (%q, true)",
			id,
			found,
			"ws-solo",
		)
	}
}

// TestLoadTitle_ReturnsNameAndDescription verifies the happy path.
func TestLoadTitle_ReturnsNameAndDescription(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws-titled", `{"id":"ws-titled","name":"My Workspace","description":"For the important stuff"}`)

	name, desc, ok := LoadTitle(home, "ws-titled")
	if !ok {
		t.Fatal("expected LoadTitle to succeed")
	}
	if name != "My Workspace" {
		t.Errorf("name = %q, want %q", name, "My Workspace")
	}
	if desc != "For the important stuff" {
		t.Errorf("description = %q, want %q", desc, "For the important stuff")
	}
}

// TestLoadTitle_MissingFileReturnsNotOK verifies a nonexistent workspace id
// fails cleanly rather than panicking.
func TestLoadTitle_MissingFileReturnsNotOK(t *testing.T) {
	home := t.TempDir()

	name, desc, ok := LoadTitle(home, "ws-does-not-exist")
	if ok || name != "" || desc != "" {
		t.Fatalf("expected (\"\", \"\", false) for a missing workspace, got (%q, %q, %v)", name, desc, ok)
	}
}

// TestLoadTitle_UnsafeIDRejected verifies a traversal-unsafe id is rejected
// before it can be used to build a file path.
func TestLoadTitle_UnsafeIDRejected(t *testing.T) {
	home := t.TempDir()

	name, desc, ok := LoadTitle(home, "../../etc/passwd")
	if ok || name != "" || desc != "" {
		t.Fatalf("expected (\"\", \"\", false) for an unsafe id, got (%q, %q, %v)", name, desc, ok)
	}
}
