// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_settings_retention_guard_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order row 1 (TestUpdateConfig_RetentionGuardByEffect,
// CRIT-002 / FR-044, Dataset D-12) and row 6a (TestUpdateConfig_
// PerformanceGuardByEffect, #936 / FR-100, Dataset D-16 REST rows).
//
// Oracle: docs/internal/specs/settings-redesign-spec.md — "Keep forever
// survives any replace of its parent block", "Writes that leave retention
// unchanged still save", "A raw echo of the whole configuration meets the
// existing credential fence first", "Performance values cannot be changed
// around Settings", Dataset D-12 (fixtures F1/F2, kinds Refused / Accepted /
// Fence) and D-16. Expected statuses and messages come from the spec's
// Machine-Verifiable Constraints, never from the code.
//
// RED (c2f8f8e735 behaviour): updateConfig has no retention guard and no
// Performance guard, and `storage` is not in blockedPaths, so every D-12
// row marked RED stores the change today (200 instead of 400) — the
// D-12 red log of this file is the spec's required failing run
// (logs/settings-1519/ta/d12-red.log).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// retentionMsgR is the spec's exact refusal message for the retention guard
// (Machine-Verifiable Constraints; D-12 "Message R").
const retentionMsgR = "retention settings can only be changed through PUT /api/v1/settings/retention"

// performanceMsgR is the spec's exact refusal message for the Performance
// guard (FR-100: 400 "naming PUT /api/v1/performance").
const performanceMsgR = "performance settings can only be changed through PUT /api/v1/performance"

// retentionForeverProbe decodes config.json's storage.retention the way
// encoding/json binds it, so the test can read memory_retros_forever before
// the struct carries it. not-wire-format: decode-only probe of config.json on
// disk; never crosses the gateway/SPA boundary.
type retentionForeverProbe struct { // not-wire-format: decode-only probe of config.json on disk; never crosses the gateway/SPA boundary.
	Storage struct {
		Retention struct {
			MemoryRetrosForever *bool `json:"memory_retros_forever"`
		} `json:"retention"`
	} `json:"storage"`
}

// retentionEffective is the four effective retention values the guard must
// protect (FR-044: RetentionSessionDays(), RetentionMemoryRetrosDays(),
// memory_retros_forever, IsDisabled()), read from a FRESH config load so a
// stale in-memory copy can never mask a lost change.
type retentionEffective struct {
	sessionDays     int
	memoryRetroDays int
	forever         bool
	keepEverything  bool
}

// readRetentionEffective loads config.json fresh (config.LoadConfig — the way
// the next boot will see it) and derives the four effective values. The
// defaults are the spec's (config_retention.go accessors: ≤0 → 90 / 180);
// memory_retros_forever is read through the raw probe because the struct
// field is part of this feature's GREEN.
func readRetentionEffective(t *testing.T, path string) retentionEffective {
	t.Helper()
	cfg, err := config.LoadConfig(path)
	require.NoError(t, err, "config.json must still load after the write attempt")

	forever := false
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var probe retentionForeverProbe
	require.NoError(t, json.Unmarshal(raw, &probe), "config.json must stay valid JSON")
	if probe.Storage.Retention.MemoryRetrosForever != nil {
		forever = *probe.Storage.Retention.MemoryRetrosForever
	}
	return retentionEffective{
		sessionDays:     cfg.Storage.Retention.RetentionSessionDays(),
		memoryRetroDays: cfg.Storage.Retention.RetentionMemoryRetrosDays(),
		forever:         forever,
		keepEverything:  cfg.Storage.Retention.IsDisabled(),
	}
}

// d12Fixture is one valid config.json that differs only in storage.retention
// (Dataset D-12 preamble). F1 exists at the baseline; F2's switch is part of
// this feature's GREEN but the raw file is already valid JSON today.
//
// The fixture stores gateway.port 8080 because D-12 rows 5, 7, 10 and 11
// state their port oracle as an absolute value ("Port stays 8080" /
// "Port 8081"): the assertion is only meaningful against a STORED 8080.
// Without a stored port, config.LoadConfig applies its shipped default
// (5000, pkg/config/defaults.go), and "Port stays 8080" would compare
// against a number the fixture never held (observed in the backend
// branch's failing run: expected 8080, actual 5000). Both fixtures carry
// the same gateway object, so they still differ only in storage.retention.
func d12Fixture(t *testing.T, api *restAPI, fixture string) retentionEffective {
	t.Helper()
	var ret string
	switch fixture {
	case "F1":
		// F1 (legacy, exists at the baseline): effective 30 / 400 / forever
		// off / legacy keep-everything on.
		ret = `"retention":{"session_days":30,"memory_retros_days":400,"disabled":true}`
	case "F2":
		// F2 (new switch): effective 30 / 400 / Keep forever on / legacy off.
		ret = `"retention":{"session_days":30,"memory_retros_days":400,"memory_retros_forever":true}`
	default:
		t.Fatalf("unknown D-12 fixture %q", fixture)
	}
	doc := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"gateway":{"port":8080},"storage":{` + ret + `}}`
	require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "config.json"), []byte(doc), 0o600))
	want := retentionEffective{sessionDays: 30, memoryRetroDays: 400}
	switch fixture {
	case "F1":
		want.keepEverything = true
	case "F2":
		want.forever = true
	}
	got := readRetentionEffective(t, filepath.Join(api.homePath, "config.json"))
	require.Equal(t, want, got, "fixture %s must decode to its stated effective values", fixture)
	return got
}

// putConfigBody sends one PUT /api/v1/config with body through the real
// updateConfig handler and returns the recorder.
func putConfigBody(api *restAPI, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.updateConfig(w, r)
	return w
}

// requireByteIdenticalConfig asserts config.json was not touched at all
// (FR-044: "config.json byte-identical").
func requireByteIdenticalConfig(t *testing.T, api *restAPI, before []byte) {
	t.Helper()
	after, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after),
		"the refused write must leave config.json byte-identical")
}

// requireD12Refused asserts one Refused row: 400 with the retention message,
// byte-identical file, and the four effective values equal to the fixture's
// after a fresh load (D-12 Refused kind).
func requireD12Refused(t *testing.T, api *restAPI, w *httptest.ResponseRecorder, before []byte, want retentionEffective, where string) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, w.Code,
		"%s: the write must be refused with 400; body=%s", where, w.Body.String())
	require.Contains(t, w.Body.String(), retentionMsgR,
		"%s: the refusal must name the retention endpoint so a script knows the one write path", where)
	requireByteIdenticalConfig(t, api, before)
	require.Equal(t, want, readRetentionEffective(t, filepath.Join(api.homePath, "config.json")),
		"%s: the four effective retention values must equal the fixture's after a fresh load", where)
}

// requireD12Accepted asserts one Accepted row: 200, the body's intended
// change is stored (checked by the caller via a fresh load), and the four
// effective values equal the fixture's. No byte-equality check — the file
// changed on purpose (D-12 Accepted kind).
func requireD12Accepted(t *testing.T, api *restAPI, w *httptest.ResponseRecorder, want retentionEffective, where string) {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code,
		"%s: a write that leaves retention unchanged must be accepted; body=%s", where, w.Body.String())
	require.Equal(t, want, readRetentionEffective(t, filepath.Join(api.homePath, "config.json")),
		"%s: the four effective retention values must equal the fixture's after a fresh load", where)
}

// d12Fences parses the body returned by GET /api/v1/config (the raw
// whole-config echo D-12 rows 14–15 need). getConfig serializes the running
// config.Config, so the echo carries "providers" — which the credential
// fence refuses before the retention guard is ever reached.
func rawConfigEcho(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	api.getConfig(w)
	require.Equal(t, http.StatusOK, w.Code, "GET /config must answer for the echo fixture; body=%s", w.Body.String())
	var echo map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &echo))
	require.Contains(t, echo, "providers",
		"the echo fixture must carry providers so the credential fence is the first fence met")
	return echo
}

// TestUpdateConfig_RetentionGuardByEffect — order 1. One subtest per D-12
// row × fixture. Refused rows are RED on today's code with F1 (the spec's
// required failing run); F2 rows join the same red run because the guard is
// the same missing code.
func TestUpdateConfig_RetentionGuardByEffect(t *testing.T) {
	t.Run("D12 row 1 leaf change drops the parent's other members", func(t *testing.T) {
		for _, fixture := range []string{"F1", "F2"} {
			t.Run(fixture, func(t *testing.T) {
				api := newTestRestAPIWithHome(t)
				want := d12Fixture(t, api, fixture)
				before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				// The one-level merge replaces the whole retention object,
				// so this leaf write would drop disabled / memory_retros_days
				// (F1) or memory_retros_forever (F2).
				w := putConfigBody(api, `{"storage":{"retention":{"session_days":90}}}`)
				requireD12Refused(t, api, w, before, want, "D-12 row 1 "+fixture)
			})
		}
	})

	t.Run("D12 row 2 replace storage.retention with an empty object", func(t *testing.T) {
		for _, fixture := range []string{"F1", "F2"} {
			t.Run(fixture, func(t *testing.T) {
				api := newTestRestAPIWithHome(t)
				want := d12Fixture(t, api, fixture)
				before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				w := putConfigBody(api, `{"storage":{"retention":{}}}`)
				requireD12Refused(t, api, w, before, want, "D-12 row 2 "+fixture)
			})
		}
	})

	t.Run("D12 row 3 replace storage with null", func(t *testing.T) {
		for _, fixture := range []string{"F1", "F2"} {
			t.Run(fixture, func(t *testing.T) {
				api := newTestRestAPIWithHome(t)
				want := d12Fixture(t, api, fixture)
				before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				// collectPaths sees only "storage" — no leaf under it — so a
				// blocked-path approach could never catch this shape; the
				// guard compares effective values.
				w := putConfigBody(api, `{"storage":null}`)
				requireD12Refused(t, api, w, before, want, "D-12 row 3 "+fixture)
			})
		}
	})

	t.Run("D12 row 4 storage replaced with a wrong type does not decode", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		d12Fixture(t, api, "F1")
		before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		for _, body := range []string{`{"storage":"x"}`, `{"storage":[]}`} {
			w := putConfigBody(api, body)
			require.Equal(t, http.StatusBadRequest, w.Code,
				"a merged result that does not decode must be refused with 400; body=%s", w.Body.String())
			require.Contains(t, w.Body.String(), "this write does not decode:",
				"the decode refusal must say so (D-12 row 4 pins the shape of this refusal); body=%s", w.Body.String())
			requireByteIdenticalConfig(t, api, before)
		}
	})

	t.Run("D12 row 5 permitted multi-section write that changes retention", func(t *testing.T) {
		for _, fixture := range []string{"F1", "F2"} {
			t.Run(fixture, func(t *testing.T) {
				api := newTestRestAPIWithHome(t)
				want := d12Fixture(t, api, fixture)
				before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				w := putConfigBody(api, `{"gateway":{"port":8081},"storage":{"retention":{"session_days":30,"memory_retros_days":400}}}`)
				requireD12Refused(t, api, w, before, want, "D-12 row 5 "+fixture)
				// Port stays 8080: nothing in the body is written.
				cfg, err := config.LoadConfig(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				require.Equal(t, 8080, cfg.Gateway.Port, "Port must stay 8080; nothing in a refused write lands")
			})
		}
	})

	t.Run("D12 row 6 key casing that changes the decoded value", func(t *testing.T) {
		t.Run("F1 DISABLED false", func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			want := d12Fixture(t, api, "F1")
			before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
			require.NoError(t, err)
			// encoding/json matches field names without regard to case, so
			// "DISABLED":false would turn the legacy switch off.
			w := putConfigBody(api, `{"storage":{"retention":{"session_days":30,"memory_retros_days":400,"DISABLED":false}}}`)
			requireD12Refused(t, api, w, before, want, "D-12 row 6 F1")
		})
		t.Run("F2 MEMORY_RETROS_FOREVER false", func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			want := d12Fixture(t, api, "F2")
			before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
			require.NoError(t, err)
			w := putConfigBody(api, `{"storage":{"retention":{"session_days":30,"memory_retros_days":400,"MEMORY_RETROS_FOREVER":false}}}`)
			requireD12Refused(t, api, w, before, want, "D-12 row 6 F2")
		})
	})

	t.Run("D12 row 7 mixed allowed change plus retention change", func(t *testing.T) {
		for _, fixture := range []string{"F1", "F2"} {
			t.Run(fixture, func(t *testing.T) {
				api := newTestRestAPIWithHome(t)
				want := d12Fixture(t, api, fixture)
				before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				w := putConfigBody(api, `{"gateway":{"port":8081},"storage":null}`)
				requireD12Refused(t, api, w, before, want, "D-12 row 7 "+fixture)
				cfg, err := config.LoadConfig(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				require.Equal(t, 8080, cfg.Gateway.Port, "Port must stay 8080; refused before any write")
			})
		}
	})

	t.Run("D12 row 8 day counts echoed with one flag silently lost", func(t *testing.T) {
		for _, fixture := range []string{"F1", "F2"} {
			t.Run(fixture, func(t *testing.T) {
				api := newTestRestAPIWithHome(t)
				want := d12Fixture(t, api, fixture)
				before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				// F1 would lose the legacy flag; F2 would lose Keep forever.
				w := putConfigBody(api, `{"storage":{"retention":{"session_days":30,"memory_retros_days":400}}}`)
				requireD12Refused(t, api, w, before, want, "D-12 row 8 "+fixture)
			})
		}
	})

	t.Run("D12 row 9 sibling key only is accepted", func(t *testing.T) {
		for _, fixture := range []string{"F1", "F2"} {
			t.Run(fixture, func(t *testing.T) {
				api := newTestRestAPIWithHome(t)
				want := d12Fixture(t, api, fixture)
				w := putConfigBody(api, `{"storage":{"other":1}}`)
				requireD12Accepted(t, api, w, want, "D-12 row 9 "+fixture)
				// The intended change is stored: storage.other == 1, and the
				// merge keeps retention.
				raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				var doc map[string]any
				require.NoError(t, json.Unmarshal(raw, &doc))
				storage, _ := doc["storage"].(map[string]any)
				require.NotNil(t, storage, "storage must stay an object")
				assert.Equal(t, float64(1), storage["other"], "storage.other must be stored")
				require.Contains(t, storage, "retention", "the merge keeps retention")
			})
		}
	})

	t.Run("D12 row 10 unrelated section is accepted", func(t *testing.T) {
		for _, fixture := range []string{"F1", "F2"} {
			t.Run(fixture, func(t *testing.T) {
				api := newTestRestAPIWithHome(t)
				want := d12Fixture(t, api, fixture)
				w := putConfigBody(api, `{"gateway":{"port":8081}}`)
				requireD12Accepted(t, api, w, want, "D-12 row 10 "+fixture)
				cfg, err := config.LoadConfig(filepath.Join(api.homePath, "config.json"))
				require.NoError(t, err)
				require.Equal(t, 8081, cfg.Gateway.Port, "the intended Port change must be stored")
			})
		}
	})

	t.Run("D12 row 11 permitted multi-section write that echoes retention as stored", func(t *testing.T) {
		t.Run("F1", func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			want := d12Fixture(t, api, "F1")
			// Positive control for row 5: every retention member present and
			// equal to the fixture, so the effective values do not change.
			w := putConfigBody(api, `{"gateway":{"port":8081},"storage":{"retention":{"session_days":30,"memory_retros_days":400,"disabled":true}}}`)
			requireD12Accepted(t, api, w, want, "D-12 row 11 F1")
			cfg, err := config.LoadConfig(filepath.Join(api.homePath, "config.json"))
			require.NoError(t, err)
			require.Equal(t, 8081, cfg.Gateway.Port, "the intended Port change must be stored")
		})
		t.Run("F2", func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			want := d12Fixture(t, api, "F2")
			w := putConfigBody(api, `{"gateway":{"port":8081},"storage":{"retention":{"session_days":30,"memory_retros_days":400,"memory_retros_forever":true}}}`)
			requireD12Accepted(t, api, w, want, "D-12 row 11 F2")
			cfg, err := config.LoadConfig(filepath.Join(api.homePath, "config.json"))
			require.NoError(t, err)
			require.Equal(t, 8081, cfg.Gateway.Port, "the intended Port change must be stored")
		})
	})

	t.Run("D12 row 12 dotted key name that no config field has", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		want := d12Fixture(t, api, "F2")
		// The effect decides, not the spelling: the decoder ignores an
		// unknown member, retention stays as stored.
		w := putConfigBody(api, `{"storage":{"retention.memory_retros_forever":false}}`)
		requireD12Accepted(t, api, w, want, "D-12 row 12 F2")
	})

	t.Run("D12 row 13 harmless alternate casing is accepted", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		want := d12Fixture(t, api, "F2")
		// "Storage" sorts before "storage", so the later canonical member —
		// which holds memory_retros_forever: true — wins on decode; the
		// guard decodes the same bytes the file gets.
		w := putConfigBody(api, `{"Storage":{"Retention":{"memory_retros_forever":false}}}`)
		requireD12Accepted(t, api, w, want, "D-12 row 13 F2")
	})

	t.Run("D12 row 14 raw whole-config echo meets the credential fence first", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		d12Fixture(t, api, "F2")
		before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		echo := rawConfigEcho(t, api)
		body, err := json.Marshal(echo)
		require.NoError(t, err)
		w := putConfigBody(api, string(body))
		// Fence row: this is evidence for the credential fence only, not for
		// the retention guard — 403 because the echo carries providers.
		require.Equal(t, http.StatusForbidden, w.Code,
			"a raw GET /config echo carries providers and must meet the existing credential fence; body=%s", w.Body.String())
		requireByteIdenticalConfig(t, api, before)
		require.Equal(t, true, readRetentionEffective(t, filepath.Join(api.homePath, "config.json")).forever,
			"Keep forever is still on after the fenced write")
	})

	t.Run("D12 row 15 whole-config echo with retention removed still meets the fence", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		d12Fixture(t, api, "F2")
		before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		echo := rawConfigEcho(t, api)
		storage, ok := echo["storage"].(map[string]any)
		require.True(t, ok, "the echo must carry storage for this fixture shape")
		storage["retention"] = map[string]any{}
		body, err := json.Marshal(echo)
		require.NoError(t, err)
		w := putConfigBody(api, string(body))
		require.Equal(t, http.StatusForbidden, w.Code,
			"the whole-config echo keeps providers and must stay a 403 from the credential fence; body=%s", w.Body.String())
		requireByteIdenticalConfig(t, api, before)
		require.Equal(t, true, readRetentionEffective(t, filepath.Join(api.homePath, "config.json")).forever,
			"Keep forever survives — because of the fence, not the guard (D-12 row 15)")
	})
}

// TestUpdateConfig_PerformanceGuardByEffect — order 6a, #936 / FR-100,
// Dataset D-16 REST rows: PUT /config must refuse any body whose merged
// result changes an effective Performance value, naming PUT /api/v1/performance.
//
// Row 4 (tools.manifest.compressed) is the exception since the Skills & Tools
// redesign (#1963, C-22 row 3): the key left the Performance guard list and
// PUT /config refuses it with the C-22 text instead; see the leaf.
func TestUpdateConfig_PerformanceGuardByEffect(t *testing.T) {
	putPerformance := func(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := putConfigBody(api, body)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"the Performance guard must refuse with 400; body=%s", w.Body.String())
		require.Contains(t, w.Body.String(), performanceMsgR,
			"the refusal must name PUT /api/v1/performance (FR-100); body=%s", w.Body.String())
		return w
	}

	t.Run("D16 row 1 max_parallel_agents leaf", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		putPerformance(t, api, `{"performance":{"max_parallel_agents":64}}`)
		requireByteIdenticalConfig(t, api, before)
	})

	t.Run("D16 row 2 performance parent replace with null changes a stored value", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		// A stored performance value makes the null replace a change by
		// effect ("400 when a stored value would change", D-16 row 2).
		seed := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"performance":{"max_parallel_agents":4}}`
		require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "config.json"), []byte(seed), 0o600))
		before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		putPerformance(t, api, `{"performance":null}`)
		requireByteIdenticalConfig(t, api, before)
	})

	t.Run("D16 row 3 planning.goal_max_rounds leaf", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		putPerformance(t, api, `{"planning":{"goal_max_rounds":99}}`)
		requireByteIdenticalConfig(t, api, before)
	})

	// Row 4 is re-aimed by the Skills & Tools redesign (#1963, epic #1519), C-22
	// row 3 "Moves the guard": PUT /config refuses tools.manifest.compressed
	// with "This setting was replaced by levels in Skills & Tools." and the
	// Settings entry for the key (the FR-100 guard list) is removed in the same
	// change, so the leaf no longer expects performanceMsgR. C-21: the new
	// refusal replaces the one that names Settings → Limits, and C-22 drops the
	// switch from PUT /api/v1/performance, so a refusal that names that
	// endpoint sends the caller nowhere. Kept: that the write is refused and
	// that config.json stays byte-identical. Not kept: the exact 400. C-22 names
	// no status and the 400 came from the Settings guard this row leaves; ruled
	// 2026-11-28 (team-lead) that any 4xx is accepted for the PUT /config
	// refusal of a retired tool-loading key.
	t.Run("D16 row 4 tools.manifest.compressed leaf", func(t *testing.T) {
		const c22Refusal = "This setting was replaced by levels in Skills & Tools."
		api := newTestRestAPIWithHome(t)
		before, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		w := putConfigBody(api, `{"tools":{"manifest":{"compressed":false}}}`)
		require.True(t, w.Code >= http.StatusBadRequest && w.Code < http.StatusInternalServerError,
			"a change to tools.manifest.compressed must be refused with a client error (4xx; C-22 names no status); got %d, body=%s", w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), c22Refusal,
			"C-22 row 3: the refusal must carry the spec's text; body=%s", w.Body.String())
		assert.NotContains(t, w.Body.String(), "/api/v1/performance",
			"C-21/C-22: the refusal must not name the retired Settings guard's endpoint (PUT /api/v1/performance); body=%s", w.Body.String())
		requireByteIdenticalConfig(t, api, before)
	})

	t.Run("D16 row 9 positive control: stored values echoed unchanged are accepted", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		// A permitted payload, not a raw GET /config echo (that meets the
		// credential fence with 403): the stored performance and planning
		// objects, unchanged, plus a benign gateway change.
		seed := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"performance":{"max_parallel_agents":4},"planning":{"goal_max_rounds":6}}`
		require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "config.json"), []byte(seed), 0o600))
		w := putConfigBody(api, `{"performance":{"max_parallel_agents":4},"planning":{"goal_max_rounds":6},"gateway":{"log_level":"debug"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"echoing the stored Performance values unchanged must stay accepted; body=%s", w.Body.String())
		cfg, err := config.LoadConfig(filepath.Join(api.homePath, "config.json"))
		require.NoError(t, err)
		require.Equal(t, 4, cfg.Performance.MaxParallelAgents, "the effective value must be unchanged")
		require.Equal(t, 6, cfg.Planning.GoalMaxRounds, "the effective value must be unchanged")
	})
}
