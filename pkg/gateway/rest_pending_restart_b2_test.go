// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_pending_restart_b2_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order rows 9 and 10 (B2 + the #995 apply-failed list):
//
//   - TestPendingRestart_IncludesFilesystemModelAndProxy — Dataset D-4:
//     RestartGatedKeys must list sandbox.filesystem_model and
//     tools.exec.enable_proxy (FR-036) next to the existing entries, must
//     NOT list the hot-reload keys (prompt_injection_level, auto_approve)
//     or God Mode (O14), and a saved-back-to-running value clears the list.
//   - TestPendingRestart_ApplyFailedEntries — FR-062: a hot-reloaded key
//     whose last save did not apply (the #995 seam: a locked credential
//     store makes the live refresh fail after config.json is written) is
//     listed with reason "apply_failed" and its persisted_value; the entry
//     survives later reads (the banner "stays after Later") and is cleared
//     by a later successful apply of the same key.
//
// Oracle: the spec's D-4 dataset, the BDD scenarios "The server lists every
// restart-gated setting that was saved", "Defense level applies at once"
// (hot keys not listed), "Saving the running value again clears the banner"
// and "A stored write whose refresh failed and whose answer was lost is
// found by the saved-state check" (reason apply_failed + persisted_value).
//
// The pending-restart handler is a READ-side diff of the persisted
// config.json against the boot-frozen appliedConfig snapshot, so D-4's
// "key saved ≠ running" precondition is seeded the way the handler itself
// defines it — persisted bytes differing from the boot snapshot — through
// the shared newPendingRestartAPI harness; no write endpoint is involved in
// order 9. Order 10 drives the real PUT /config write path so the failed
// apply actually happens (locked-store seam from
// channel_configure_partial_failure_test.go).
//
// RED today: sandbox.filesystem_model and tools.exec.enable_proxy are
// missing from RestartGatedKeys (not listed), and no apply_failed record
// exists at all (a failed hot apply leaves the read unchanged).

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// b2Diffs decodes a pending-restart response into generic maps, reading the
// entry fields by wire name. Decoding by map (not by the generated struct)
// is deliberate for the RED half: gen.PendingRestartEntry has no Reason
// field until GREEN regenerates it, and a struct decode would silently drop
// the unknown key the spec requires.
func b2Diffs(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var diffs []map[string]any
	require.NoError(t, json.Unmarshal(body, &diffs), "pending-restart answers a JSON array")
	return diffs
}

// b2Find returns the entry with the given key, or nil.
func b2Find(diffs []map[string]any, key string) map[string]any {
	for _, d := range diffs {
		if d["key"] == key {
			return d
		}
	}
	return nil
}

func TestPendingRestart_IncludesFilesystemModelAndProxy(t *testing.T) {
	t.Run("D4 row 1 positive control: sandbox.mode is listed (existing gate)", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{"gateway": map[string]any{"host": "127.0.0.1", "port": 8080}},
			map[string]any{"gateway": map[string]any{"host": "127.0.0.1", "port": 8080}, "sandbox": map[string]any{"mode": "enforce"}})
		w := callPendingRestart(t, api)
		require.Equal(t, http.StatusOK, w.Code)
		entry := b2Find(b2Diffs(t, w.Body.Bytes()), "sandbox.mode")
		require.NotNil(t, entry, "the existing gate must stay listed")
	})

	t.Run("D4 row 2: sandbox.filesystem_model is listed with running and saved values", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{"sandbox": map[string]any{"filesystem_model": "open"}},
			map[string]any{"sandbox": map[string]any{"filesystem_model": "confined"}})
		w := callPendingRestart(t, api)
		require.Equal(t, http.StatusOK, w.Code)
		entry := b2Find(b2Diffs(t, w.Body.Bytes()), "sandbox.filesystem_model")
		require.NotNil(t, entry,
			"RED (D-4 row 2, FR-036): sandbox.filesystem_model is restart-gated but is missing "+
				"from RestartGatedKeys — a saved filesystem model never reaches the restart banner; "+
				"the policy is built at boot (sandbox_apply.go), so the operator must be told")
		require.Equal(t, "confined", entry["persisted_value"],
			"the entry carries the saved value")
		require.Equal(t, "open", entry["applied_value"],
			"the entry carries the value still in force (the boot value)")
	})

	t.Run("D4 row 3: tools.exec.enable_proxy is listed", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{"tools": map[string]any{"exec": map[string]any{"enable_proxy": false}}},
			map[string]any{"tools": map[string]any{"exec": map[string]any{"enable_proxy": true}}})
		w := callPendingRestart(t, api)
		require.Equal(t, http.StatusOK, w.Code)
		entry := b2Find(b2Diffs(t, w.Body.Bytes()), "tools.exec.enable_proxy")
		require.NotNil(t, entry,
			"RED (D-4 row 3, FR-036): tools.exec.enable_proxy is read when the agent loop is built "+
				"(loop_construct.go) — restart-gated, missing from RestartGatedKeys today")
		require.Equal(t, true, entry["persisted_value"])
	})

	t.Run("D4 row 4 positive control: sandbox.workspace_path_guard stays listed", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{},
			map[string]any{"sandbox": map[string]any{"workspace_path_guard": true}})
		w := callPendingRestart(t, api)
		require.NotNil(t, b2Find(b2Diffs(t, w.Body.Bytes()), "sandbox.workspace_path_guard"),
			"the badge correction must not drop an existing gate")
	})

	t.Run("D4 row 5 positive control: sandbox.allowed_paths stays listed", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{},
			map[string]any{"sandbox": map[string]any{"allowed_paths": []string{"/tmp/extra"}}})
		w := callPendingRestart(t, api)
		require.NotNil(t, b2Find(b2Diffs(t, w.Body.Bytes()), "sandbox.allowed_paths"),
			"the badge correction must not drop an existing gate")
	})

	t.Run("D4 rows 6-7 positive control: gateway.port and gateway.host stay listed", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{"gateway": map[string]any{"host": "127.0.0.1", "port": 8080}},
			map[string]any{"gateway": map[string]any{"host": "0.0.0.0", "port": 9090}})
		w := callPendingRestart(t, api)
		diffs := b2Diffs(t, w.Body.Bytes())
		require.NotNil(t, b2Find(diffs, "gateway.port"))
		require.NotNil(t, b2Find(diffs, "gateway.host"))
	})

	t.Run("D4 row 8: the hot-reload prompt_injection_level is NOT listed", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{},
			map[string]any{"sandbox": map[string]any{"prompt_injection_level": "high"}})
		w := callPendingRestart(t, api)
		require.Nil(t, b2Find(b2Diffs(t, w.Body.Bytes()), "sandbox.prompt_injection_level"),
			"Defense level applies at once — a hot key must stay out of the restart list")
	})

	t.Run("D4 row 9: the hot-reload auto_approve is NOT listed", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{},
			map[string]any{"sandbox": map[string]any{"auto_approve": true}})
		w := callPendingRestart(t, api)
		require.Nil(t, b2Find(b2Diffs(t, w.Body.Bytes()), "sandbox.auto_approve"),
			"auto-approve is hot — never a restart entry")
	})

	t.Run("D4 row 10: God Mode is excluded by design (O14)", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{},
			map[string]any{"sandbox": map[string]any{"god_mode": true, "god_mode_allowed": true}})
		w := callPendingRestart(t, api)
		for _, d := range b2Diffs(t, w.Body.Bytes()) {
			key, _ := d["key"].(string)
			require.NotContains(t, key, "god_mode",
				"God Mode stays excluded from the generic restart list (O14)")
		}
	})

	t.Run("D4 row 11: saving the running value again clears the list", func(t *testing.T) {
		api := newPendingRestartAPI(t,
			map[string]any{"gateway": map[string]any{"host": "127.0.0.1", "port": 8080}},
			map[string]any{"gateway": map[string]any{"host": "127.0.0.1", "port": 8080}})
		w := callPendingRestart(t, api)
		require.Equal(t, http.StatusOK, w.Code)
		diffs := b2Diffs(t, w.Body.Bytes())
		require.Empty(t, diffs, "a reverted save clears the banner (empty list)")
	})
}

func TestPendingRestart_ApplyFailedEntries(t *testing.T) {
	// Boot with log_level unset (applied) and the same on disk (persisted):
	// a clean install, empty diff.
	api := newPendingRestartAPI(t,
		map[string]any{"gateway": map[string]any{"host": "127.0.0.1", "port": 8080}},
		map[string]any{"gateway": map[string]any{"host": "127.0.0.1", "port": 8080}})

	// The #995 seam: a real LOCKED credential store makes the live refresh
	// fail AFTER config.json is written (the write stands, the running
	// settings keep the old value) — the exact state the apply_failed record
	// exists to report.
	locked := credentials.NewStore(filepath.Join(t.TempDir(), "locked-credentials.json"))
	previous := api.credStore
	api.credStore = locked

	wPut := putConfigBody(api, `{"gateway":{"log_level":"debug"}}`)
	_ = wPut // the write's own answer shape is order 14's subject; here only
	// the fact that the file now differs from the running settings matters.

	// Restore the store so a later write CAN apply (the "Later / fix it" step).
	api.credStore = previous

	t.Run("the failed hot apply is listed with reason apply_failed", func(t *testing.T) {
		w := callPendingRestart(t, api)
		require.Equal(t, http.StatusOK, w.Code)
		entry := b2Find(b2Diffs(t, w.Body.Bytes()), "gateway.log_level")
		require.NotNil(t, entry,
			"RED (FR-062): the saved gateway.log_level did not apply (running settings still hold "+
				"the old value) but nothing lists it — the saved-not-refreshed state is invisible")
		require.Equal(t, "apply_failed", entry["reason"],
			"the entry must carry reason apply_failed (not restart_required)")
		require.Equal(t, "debug", entry["persisted_value"],
			"the entry carries the stored Debug value (BDD: persisted_value holding the stored value)")
	})

	t.Run("the entry stays listed on later reads (stays after Later)", func(t *testing.T) {
		// The person chooses Later, opens Gateway, reloads the page — each of
		// those is another read of this endpoint.
		for i := 0; i < 2; i++ {
			w := callPendingRestart(t, api)
			entry := b2Find(b2Diffs(t, w.Body.Bytes()), "gateway.log_level")
			require.NotNil(t, entry,
				"the apply_failed entry must survive page reloads and section changes")
			require.Equal(t, "apply_failed", entry["reason"])
		}
	})

	t.Run("a later successful apply of the key clears the failed entry", func(t *testing.T) {
		// The store is restored; the person saves the same key again and this
		// time the hot apply succeeds.
		wPut2 := putConfigBody(api, `{"gateway":{"log_level":"warning"}}`)
		require.NotEqual(t, http.StatusInternalServerError, wPut2.Code,
			"with the store restored the refresh succeeds; body=%s", wPut2.Body.String())

		w := callPendingRestart(t, api)
		entry := b2Find(b2Diffs(t, w.Body.Bytes()), "gateway.log_level")
		require.Nil(t, entry,
			"a later successful apply of the key removes the apply_failed entry (FR-062)")
	})

	t.Run("positive control: a hot apply that succeeds never lists the key", func(t *testing.T) {
		w := callPendingRestart(t, api)
		require.Nil(t, b2Find(b2Diffs(t, w.Body.Bytes()), "gateway.log_level"),
			"a successfully applied hot key is never in the restart list")
	})
}
