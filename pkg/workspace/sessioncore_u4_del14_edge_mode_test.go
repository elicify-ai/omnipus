// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U4 — DEL-14, delegation edge mode (§Explicit DELETE
// Requirements).
//
// RED pack, qa-lead. DEL-14 removes pkg/workspace/delegation.go::legacyModeDirect
// and the old-mode migration in DelegationEdge.UnmarshalJSON, ruling: "current
// direct/task edge validation. No legacy sequence/mode rewrite." Under the
// founder's greenfield rule (no upgrade path) a persisted legacy
// ("await"/"background") mode is refused by current validation, never silently
// mapped to "direct".

package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDelegationEdge_LegacyMode_RefusedNotMigrated covers DEL-14's behaviour
// half at the edge validator: a legacy mode ("await"/"background") must fail
// current direct/task validation, not be silently rewritten to "direct".
//
// RED today: UnmarshalJSON maps "await"→"direct" (legacyModeDirect), so
// ValidateShape accepts the edge.
func TestDelegationEdge_LegacyMode_RefusedNotMigrated(t *testing.T) {
	t.Run("legacy mode is refused", func(t *testing.T) {
		var e DelegationEdge
		if err := json.Unmarshal([]byte(`{"from_agent":"a","to_agent":"b","modes":["await"]}`), &e); err != nil {
			// An unmarshal-level refusal is also an acceptable visible refusal.
			return
		}
		if err := e.ValidateShape(); err == nil {
			t.Fatalf("legacy mode %q validated cleanly (Modes=%v) — DEL-14 requires current direct/task "+
				"validation to REFUSE a legacy mode, never silently map it to \"direct\"",
				"await", e.Modes)
		}
	})

	t.Run("current modes still validate", func(t *testing.T) {
		var e DelegationEdge
		if err := json.Unmarshal([]byte(`{"from_agent":"a","to_agent":"b","modes":["direct","task"]}`), &e); err != nil {
			t.Fatalf("unmarshal current edge: %v", err)
		}
		if err := e.ValidateShape(); err != nil {
			t.Fatalf("current direct/task edge refused: %v — the guard must not break valid edges", err)
		}
	})
}

// TestLoadDelegation_LegacyModeEdge_RefusedNotMapped covers DEL-14 end-to-end
// through the store reader: a persisted edge carrying a legacy mode must not be
// materialized as "direct" (which UnmarshalJSON does today), and must be
// dropped by current validation instead.
//
// RED today: LoadDelegation returns one edge with Modes ["direct"] — the legacy
// "await" was silently mapped, so the store serves an authorization the
// operator never wrote in the current vocabulary.
func TestLoadDelegation_LegacyModeEdge_RefusedNotMapped(t *testing.T) {
	home := t.TempDir()
	dir := DelegationStoreDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir delegation store: %v", err)
	}

	writeStore := func(t *testing.T, id, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	writeStore(t, "ws-legacy", `{"workspace_id":"ws-legacy","delegation":[{"from_agent":"a","to_agent":"b","modes":["await"]}]}`)
	writeStore(t, "ws-current", `{"workspace_id":"ws-current","delegation":[{"from_agent":"a","to_agent":"b","modes":["direct"]}]}`)

	legacy, ok := LoadDelegation(home, "ws-legacy")
	if !ok {
		t.Fatal("LoadDelegation(ws-legacy): ok = false, want ok (the record is readable)")
	}
	if len(legacy) != 0 {
		t.Fatalf("LoadDelegation(ws-legacy) = %+v, want no edges — a legacy-mode edge must be refused by "+
			"current validation, not silently mapped to \"direct\" (DEL-14)", legacy)
	}

	current, ok := LoadDelegation(home, "ws-current")
	if !ok || len(current) != 1 {
		t.Fatalf("LoadDelegation(ws-current) = %+v ok=%v, want exactly one current edge", current, ok)
	}
}

// TestWorkspace_NoLegacyModeMigrationSymbol is DEL-14's K (source) deletion
// guard for pkg/workspace/delegation.go: the legacyModeDirect symbol must be
// gone. (DelegationEdge.UnmarshalJSON may survive only as a non-migrating
// decode, so its presence alone is not asserted here — the behaviour above
// proves the migration is gone.)
//
// RED today: delegation.go still declares legacyModeDirect.
func TestWorkspace_NoLegacyModeMigrationSymbol(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "delegation.go"))
	if err != nil {
		t.Fatalf("read delegation.go: %v", err)
	}
	if strings.Contains(string(src), "legacyModeDirect") {
		t.Fatal("pkg/workspace/delegation.go still declares legacyModeDirect — DEL-14 requires this " +
			"legacy-mode migration symbol to be deleted outright (greenfield: no upgrade path)")
	}
}
