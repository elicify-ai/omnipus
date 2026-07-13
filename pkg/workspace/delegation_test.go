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

func TestReadDelegation_ReturnsEdges(t *testing.T) {
	home := t.TempDir()
	// Edge 0 is persisted under the legacy 3-value vocabulary ("background") to
	// double as a migration-transparency check: ReadDelegation must read it back
	// as the collapsed "direct" mode via DelegationEdge.UnmarshalJSON, with no
	// separate migration step. Dedicated migration-decode cases (multi-value,
	// idempotency) live in TestDelegationEdge_UnmarshalJSON_MigratesLegacyModes.
	writeWS(t, home, "ws1", `{
		"id":"ws1",
		"delegation":[
			{"from_agent":"mia","to_agent":"ray","modes":["background"],"depth":3},
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
		t.Fatalf("edge 0 modes mismatch (legacy \"background\" must migrate to %q): %+v", ModeDirect, edges[0].Modes)
	}
	if edges[0].Depth == nil || *edges[0].Depth != 3 {
		t.Fatalf("edge 0 depth mismatch: %+v", edges[0].Depth)
	}
	// Absent modes/depth → nil (= all modes / inherit).
	if edges[1].Modes != nil || edges[1].Depth != nil {
		t.Fatalf("edge 1 should have nil modes/depth, got: %+v", edges[1])
	}
}

// TestDelegationEdge_UnmarshalJSON_MigratesLegacyModes is the dedicated
// migration-decode coverage requested for the mode collapse: legacy
// await/background values must map to ModeDirect (deduped when both appear
// on the same edge), task passes through unchanged, and decoding an
// already-migrated edge is an idempotent no-op.
func TestDelegationEdge_UnmarshalJSON_MigratesLegacyModes(t *testing.T) {
	cases := []struct {
		name string
		json string
		want []DelegationMode
	}{
		{
			name: "legacy await+background+task dedupes to direct+task",
			json: `{"from_agent":"a","to_agent":"b","modes":["await","background","task"]}`,
			want: []DelegationMode{ModeDirect, ModeTask},
		},
		{
			name: "legacy await-only migrates to direct",
			json: `{"from_agent":"a","to_agent":"b","modes":["await"]}`,
			want: []DelegationMode{ModeDirect},
		},
		{
			name: "legacy background-only migrates to direct",
			json: `{"from_agent":"a","to_agent":"b","modes":["background"]}`,
			want: []DelegationMode{ModeDirect},
		},
		{
			name: "already-migrated direct+task is an idempotent passthrough",
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
}

func TestReadDelegation_EmptyEdgesIsNotError(t *testing.T) {
	home := t.TempDir()
	writeWS(t, home, "ws1", `{"id":"ws1"}`) // no delegation key

	edges, err := ReadDelegation(home, "ws1")
	if err != nil {
		t.Fatalf("a workspace with no edges must not error: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(edges))
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
