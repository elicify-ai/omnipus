// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// omnipus_team_write_test.go — pins the team-membership refusals on the
// TOOL write path. Admin is retired (omnipus-system-agent FR-008); the
// membership boundary it carried now belongs to Omnipus, which may sit on the
// DEFAULT workspace's team only (FR-003), and to the hidden System Agents
// (ADR-090 FR-006):
//
//   - create_workspace / update_workspace must REJECT a core_team that
//     puts Omnipus on a non-default workspace, or introduces a hidden
//     System Agent, BEFORE any write lands;
//   - an explicit delegation arg with an Omnipus endpoint on a team without
//     Omnipus must be rejected with INVALID_DELEGATION_EDGE (endpoint not
//     on team);
//   - the heal rule: an update that REMOVES a (forged, pre-existing)
//     Omnipus entry from a non-default workspace must succeed — the refusal
//     gates putting Omnipus there, not a brick that wedges any workspace a
//     tampered record ever touched.
//
// This is the surface that BYPASSES the REST validator
// (gateway.rest_workspaces.go's validateCoreTeamMembers): sanitizeCoreTeam
// only dedupes, so without a tool-side check an agent-driven
// update_workspace could land exactly the membership the API refuses.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// assertWorkspaceBytes reads the workspace record and returns its bytes.
func assertWorkspaceBytes(t *testing.T, home, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "workspaces", id+".json"))
	if err != nil {
		t.Fatalf("read workspace %s: %v", id, err)
	}
	return string(data)
}

// errorField extracts error.<field> from a parsed error body (see parseError).
func errorField(t *testing.T, body map[string]any, field string) string {
	t.Helper()
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("error body missing error object: %#v", body)
	}
	s, _ := errObj[field].(string)
	return s
}

// TestWorkspaceCreate_RejectsOmnipusTeamBeforeWrites — the whole-list rule on
// create: an initial core_team containing Omnipus is INVALID_INPUT naming
// Omnipus, and NOTHING is persisted (no workspace record at all).
func TestWorkspaceCreate_RejectsOmnipusTeamBeforeWrites(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "mia", "jim", "omnipus")

	result := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Omnipus Team", "core_team": []any{"mia", "omnipus", "jim"},
	})
	if !result.IsError {
		t.Fatal("create_workspace with omnipus on a non-default core_team unexpectedly succeeded")
	}
	parsed := parseError(t, result.ForLLM)
	if code := errorField(t, parsed, "code"); code != "INVALID_INPUT" {
		t.Fatalf("error code = %q, want INVALID_INPUT", code)
	}
	if msg := strings.ToLower(errorField(t, parsed, "message")); !strings.Contains(msg, "omnipus") {
		t.Fatalf("error message must name the offending member, got: %q", errorField(t, parsed, "message"))
	}

	entries, err := os.ReadDir(filepath.Join(home, "workspaces"))
	if err == nil && len(entries) > 0 {
		t.Fatalf("rejected create persisted %d workspace record(s); the rejection must be zero-write", len(entries))
	}
}

// TestWorkspaceCreate_RejectsHiddenSystemAgentTeam — the same rule keeps its
// hold on hidden System Agents: "judge" is excluded from teams even though
// it is NOT registered in this test's live config (the rule is roster-based,
// not config-presence-based).
func TestWorkspaceCreate_RejectsHiddenSystemAgentTeam(t *testing.T) {
	deps, _ := newTestDepsWithHomeAndAgents(t, "mia")

	result := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Judge Team", "core_team": []any{"mia", "judge"},
	})
	if !result.IsError {
		t.Fatal("create_workspace with judge on core_team unexpectedly succeeded")
	}
	parsed := parseError(t, result.ForLLM)
	if msg := strings.ToLower(errorField(t, parsed, "message")); !strings.Contains(msg, "judge") {
		t.Fatalf("error message must name the offending member, got: %q", errorField(t, parsed, "message"))
	}
}

// TestWorkspaceUpdate_RejectsIntroducingOmnipusBeforeWrites — the delta rule on
// update: introducing Omnipus onto an existing non-default team is rejected and
// the stored record is byte-identical afterwards.
func TestWorkspaceUpdate_RejectsIntroducingOmnipusBeforeWrites(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "mia", "jim", "omnipus")
	created := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Ordinary", "core_team": []any{"mia", "jim"},
	})
	if created.IsError {
		t.Fatalf("create failed: %s", created.ForLLM)
	}
	id := workspaceID(t, created.ForLLM)
	before := assertWorkspaceBytes(t, home, id)

	result := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": id, "revision": currentWorkspaceRevision(t, home, id),
		"core_team": []any{"mia", "jim", "omnipus"},
	})
	if !result.IsError {
		t.Fatal("update_workspace introducing omnipus onto a non-default core_team unexpectedly succeeded")
	}
	parsed := parseError(t, result.ForLLM)
	if msg := strings.ToLower(errorField(t, parsed, "message")); !strings.Contains(msg, "omnipus") {
		t.Fatalf("error message must name the offending member, got: %q", errorField(t, parsed, "message"))
	}

	if after := assertWorkspaceBytes(t, home, id); after != before {
		t.Fatal("rejected update changed workspace bytes — the rejection must be zero-write")
	}
}

// TestWorkspaceUpdate_RemovingForgedOmnipusEntryHeals — the delta rule cuts the
// other way too: a record that ALREADY contains omnipus (only reachable by
// direct tampering — every writer now refuses to introduce it) can still be
// healed by an update whose team simply omits it. The exclusion gates
// INTRODUCED members; it must not wedge every workspace a tampered record
// ever touched.
func TestWorkspaceUpdate_RemovingForgedOmnipusEntryHeals(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "mia", "jim")
	id := "01JXADR090HEALFORGED0001"
	wsPath := filepath.Join(home, "workspaces", id+".json")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	forged := `{"id":"` + id + `","name":"Tampered","status":"active","core_team":["mia","omnipus"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(wsPath, []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}

	result := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": id, "revision": currentWorkspaceRevision(t, home, id),
		"core_team": []any{"mia", "jim"},
	})
	if result.IsError {
		t.Fatalf("healing update (dropping forged omnipus entry) failed: %s", result.ForLLM)
	}

	after := assertWorkspaceBytes(t, home, id)
	if strings.Contains(after, "omnipus") {
		t.Fatalf("healed workspace still lists omnipus: %s", after)
	}
}

// TestWorkspaceCreate_ExplicitDelegationOmnipusEndpointRejected — the
// delegation surface: an explicit edge with an Omnipus endpoint is
// INVALID_DELEGATION_EDGE (Omnipus is not on this team and never can be), and
// the rejection is zero-write.
func TestWorkspaceCreate_ExplicitDelegationOmnipusEndpointRejected(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "mia", "jim", "omnipus")

	result := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Graph", "core_team": []any{"mia", "jim"},
		"delegation": []any{map[string]any{
			"from_agent": "jim", "to_agent": "omnipus", "modes": []any{"direct"},
		}},
	})
	if !result.IsError {
		t.Fatal("create_workspace with a jim->omnipus delegation edge unexpectedly succeeded")
	}
	parsed := parseError(t, result.ForLLM)
	if code := errorField(t, parsed, "code"); code != "INVALID_DELEGATION_EDGE" {
		t.Fatalf("error code = %q, want INVALID_DELEGATION_EDGE", code)
	}

	entries, err := os.ReadDir(filepath.Join(home, "workspaces"))
	if err == nil && len(entries) > 0 {
		t.Fatalf("rejected create persisted %d workspace record(s)", len(entries))
	}
}

// TestWorkspaceUpdate_ExplicitDelegationOmnipusEndpointRejected — same
// guarantee on update: an explicit graph naming Omnipus as an endpoint fails
// against the team set and leaves both the record and any delegation store
// untouched.
func TestWorkspaceUpdate_ExplicitDelegationOmnipusEndpointRejected(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "mia", "jim", "omnipus")
	created := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Graph", "core_team": []any{"mia", "jim"},
	})
	if created.IsError {
		t.Fatalf("create failed: %s", created.ForLLM)
	}
	id := workspaceID(t, created.ForLLM)
	before := assertWorkspaceBytes(t, home, id)
	delegationPath := filepath.Join(home, "entities", "delegation", id+".json")
	delegationBefore, beforeErr := os.ReadFile(delegationPath)
	if beforeErr != nil && !errors.Is(beforeErr, os.ErrNotExist) {
		t.Fatal(beforeErr)
	}

	result := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": id, "revision": currentWorkspaceRevision(t, home, id),
		"delegation": []any{map[string]any{
			"from_agent": "omnipus", "to_agent": "jim", "modes": []any{"direct"},
		}},
	})
	if !result.IsError {
		t.Fatal("update_workspace with an omnipus->jim delegation edge unexpectedly succeeded")
	}
	parsed := parseError(t, result.ForLLM)
	if code := errorField(t, parsed, "code"); code != "INVALID_DELEGATION_EDGE" {
		t.Fatalf("error code = %q, want INVALID_DELEGATION_EDGE", code)
	}

	if after := assertWorkspaceBytes(t, home, id); after != before {
		t.Fatal("rejected delegation update changed workspace bytes")
	}
	delegationAfter, afterErr := os.ReadFile(delegationPath)
	if afterErr != nil && !errors.Is(afterErr, os.ErrNotExist) {
		t.Fatal(afterErr)
	}
	if errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) || string(delegationBefore) != string(delegationAfter) {
		t.Fatal("rejected delegation update changed delegation storage")
	}
}

// TestWorkspaceCreate_OrdinaryTeamStillSucceeds — the control: the exclusion
// must not over-reach. An ordinary team of chat colleagues and staff roles
// still creates cleanly and still seeds its default delegation edges.
func TestWorkspaceCreate_OrdinaryTeamStillSucceeds(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "mia", "jim", "ava", "worker")

	result := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":      "Ordinary Team",
		"core_team": []any{"mia", "jim", "ava", "worker"},
	})
	if result.IsError {
		t.Fatalf("ordinary-team create failed: %s", result.ForLLM)
	}
	id := workspaceID(t, result.ForLLM)

	body := parseSuccess(t, result.ForLLM)
	team, _ := body["core_team"].([]any)
	if len(team) != 4 {
		t.Fatalf("core_team round-trip lost members: %#v", body["core_team"])
	}
	if _, statErr := os.Stat(filepath.Join(home, "workspaces", id+".json")); statErr != nil {
		t.Fatalf("workspace record not persisted: %v", statErr)
	}
}
