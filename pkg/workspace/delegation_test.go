// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeWS writes a raw workspace JSON file under home/workspaces/<id>.json.
func writeWS(t *testing.T, home, id, body string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// writeDelegationStoreRaw writes a raw delegation-store record for id. Raw
// (rather than SaveDelegation) so a test can persist a legacy on-disk shape
// SaveDelegation would normalise away.
func writeDelegationStoreRaw(t *testing.T, home, id, body string) {
	t.Helper()
	dir := DelegationStoreDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir delegation store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write delegation store: %v", err)
	}
}

func TestReadDelegation_ReturnsEdges(t *testing.T) {
	home := t.TempDir()
	// The workspace record exists (ReadDelegation still requires it — a
	// delegation check that cannot locate its governing workspace fails
	// closed) but carries NO edges. Edges live in the delegation store; see
	// delegationstore.go for why they cannot live in the record.
	writeWS(t, home, "ws1", `{"id":"ws1"}`)
	// DEL-14 (greenfield): no legacy-mode migration — modes are read as
	// written, so this fixture uses the current {direct, task} vocabulary. The
	// drop-a-legacy-mode case lives in TestReadDelegation_DropsLegacyModeEdge.
	writeDelegationStoreRaw(t, home, "ws1", `{
		"workspace_id":"ws1",
		"delegation":[
			{"from_agent":"mia","to_agent":"ray","modes":["direct"],"depth":3},
			{"from_agent":"jim","to_agent":"ava"}
		]
	}`)

	edges, err := ReadDelegation(home, "ws1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}
	if edges[0].FromAgent != "mia" || edges[0].ToAgent != "ray" {
		t.Fatalf("edge 0 mismatch: %+v", edges[0])
	}
	if len(edges[0].Modes) != 1 || edges[0].Modes[0] != ModeDirect {
		t.Fatalf("edge 0 modes mismatch (want [%q]): %+v", ModeDirect, edges[0].Modes)
	}
	if edges[0].Depth == nil || *edges[0].Depth != 3 {
		t.Fatalf("edge 0 depth mismatch: %+v", edges[0].Depth)
	}
	// Absent modes/depth → nil (= all modes / inherit).
	if edges[1].Modes != nil || edges[1].Depth != nil {
		t.Fatalf("edge 1 should have nil modes/depth, got: %+v", edges[1])
	}
}

// TestReadDelegation_DropsLegacyModeEdge is DEL-14's read-side regression: an
// edge carrying a retired mode ("background") is NOT migrated to "direct" — it
// fails ValidateShape and loadDelegationStore drops it (WARN), so it authorizes
// nothing. The healthy edge beside it is kept.
func TestReadDelegation_DropsLegacyModeEdge(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws1", `{"id":"ws1"}`)
	writeDelegationStoreRaw(t, home, "ws1", `{
		"workspace_id":"ws1",
		"delegation":[
			{"from_agent":"mia","to_agent":"ray","modes":["background"]},
			{"from_agent":"jim","to_agent":"ava"}
		]
	}`)

	edges, err := ReadDelegation(home, "ws1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected the legacy-mode edge to be dropped, leaving 1 edge; got %d: %+v", len(edges), edges)
	}
	if edges[0].FromAgent != "jim" || edges[0].ToAgent != "ava" {
		t.Fatalf("the surviving edge must be the healthy jim→ava one, got: %+v", edges[0])
	}
}

func TestReadDelegation_EmptyEdgesIsNotError_StoreAbsent(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws1", `{"id":"ws1"}`) // no delegation store file at all

	edges, err := ReadDelegation(home, "ws1")
	if err != nil {
		t.Fatalf("a workspace with no delegation store record must not error: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(edges))
	}
}

// TestReadDelegation_FailsClosedOnUntrustedStoreRecord: a delegation record
// that exists but cannot be trusted is a HARD error, never an empty graph.
// The gate turns the error into a denial; an empty graph would also deny
// today, but silently — and any future caller that distinguishes "no edges"
// from "cannot tell" would then fall open.
func TestReadDelegation_FailsClosedOnUntrustedStoreRecord(t *testing.T) {
	cases := map[string]string{
		"malformed JSON":                 `{not valid json`,
		"workspace_id disagrees with id": `{"workspace_id":"other","delegation":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeWS(t, home, "ws1", `{"id":"ws1"}`)
			writeDelegationStoreRaw(t, home, "ws1", body)
			if _, err := ReadDelegation(home, "ws1"); err == nil {
				t.Fatal("an untrusted delegation record must be a hard error (fail closed at the gate)")
			}
		})
	}
}

// TestDelegationEdge_LegacyModesAreNotMigrated is the DEL-14 (greenfield)
// regression: the retired 3-value vocabulary (await/background) is NOT
// rewritten on decode. An edge persisted with a legacy mode decodes AS
// WRITTEN, and the mode is refused by Valid()/ValidateShape — so the store
// reader DROPS the edge rather than silently granting the collapsed "direct"
// mode. There is no compatibility migration path.
func TestDelegationEdge_LegacyModesAreNotMigrated(t *testing.T) {
	cases := []struct {
		name string
		json string
		want []DelegationMode
	}{
		{
			name: "legacy await+background+task is kept verbatim, not collapsed",
			json: `{"from_agent":"a","to_agent":"b","modes":["await","background","task"]}`,
			want: []DelegationMode{"await", "background", "task"},
		},
		{
			name: "already-current direct+task is an idempotent passthrough",
			json: `{"from_agent":"a","to_agent":"b","modes":["direct","task"]}`,
			want: []DelegationMode{ModeDirect, ModeTask},
		},
		{
			name: "absent modes stays nil",
			json: `{"from_agent":"a","to_agent":"b"}`,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e DelegationEdge
			if err := json.Unmarshal([]byte(tc.json), &e); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(e.Modes) != len(tc.want) {
				t.Fatalf("Modes = %+v, want %+v", e.Modes, tc.want)
			}
			for i, m := range tc.want {
				if e.Modes[i] != m {
					t.Fatalf("Modes[%d] = %q, want %q (full: %+v)", i, e.Modes[i], m, e.Modes)
				}
			}
		})
	}

	// A legacy mode is not merely un-migrated: it is REFUSED, so
	// loadDelegationStore drops the edge and it authorizes nothing.
	var legacy DelegationEdge
	if err := json.Unmarshal([]byte(`{"from_agent":"a","to_agent":"b","modes":["background"]}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy edge: %v", err)
	}
	if legacy.Modes[0].Valid() {
		t.Fatalf("legacy mode %q must not be valid (valid: direct, task)", legacy.Modes[0])
	}
	if err := legacy.ValidateShape(); err == nil {
		t.Fatal("ValidateShape must refuse a legacy mode so the reader drops the edge (DEL-14, no migration)")
	}
}

func TestReadDelegation_MissingWorkspaceErrors(t *testing.T) {
	home := t.TempDir()
	if _, err := ReadDelegation(home, "does-not-exist"); err == nil {
		t.Fatal("expected an error for a missing workspace (fail-closed at caller)")
	}
}

func TestReadDelegation_MalformedJSONErrors(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws1", `{not valid json`)
	if _, err := ReadDelegation(home, "ws1"); err == nil {
		t.Fatal("expected a parse error for malformed JSON")
	}
}

func TestReadDelegation_RejectsUnsafeID(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"../escape", "a/b", `a\b`, "..", ""} {
		_, err := ReadDelegation(home, id)
		if !errors.Is(err, ErrInvalidWorkspaceID) {
			t.Fatalf("ReadDelegation(%q): want ErrInvalidWorkspaceID, got %v", id, err)
		}
	}
}
