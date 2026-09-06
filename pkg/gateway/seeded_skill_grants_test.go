// Omnipus — ADR-074 D4 seeded_skill_grants marker tests (judgment-first spec
// tests 16 and 16c): the marker persists into config.json idempotently (second
// boot is a byte-level no-op) and is stripped from the GET /api/v1/config
// response while the disk file carries it.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestPersistSeededSkillGrants_WriteOnceThenByteIdenticalNoOp verifies the
// marker lands in config.json exactly once: the first call writes it, the
// second call (same markers — the second-boot shape) leaves the file
// byte-identical and untouched (spec test 16's "second boot byte-identical",
// at the file level).
func TestPersistSeededSkillGrants_WriteOnceThenByteIdenticalNoOp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath,
		[]byte(`{"version":1,"agents":{"defaults":{}},"providers":[]}`), 0o600))

	markers := []string{coreagent.SkillsMigrationDefineDone}
	require.NoError(t, persistSeededSkillGrants(configPath, markers))

	afterFirst, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(afterFirst, &m))
	assert.Equal(t, []any{coreagent.SkillsMigrationDefineDone}, m["seeded_skill_grants"],
		"first persist must write the marker into config.json")
	assert.Equal(t, float64(1), m["version"], "every other key must be preserved as-is")

	// Second boot: same markers → no write at all, file byte-identical.
	require.NoError(t, persistSeededSkillGrants(configPath, markers))
	afterSecond, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, afterFirst, afterSecond,
		"a second persist with identical markers must leave config.json byte-identical")
}

// TestHandleConfigGET_StripsSeededSkillGrants is spec test 16c (US-4 S6 /
// R2-04): the seeded_skill_grants marker is internal-only bookkeeping — it must
// be absent from the GET /api/v1/config response while config.json on disk
// carries it.
func TestHandleConfigGET_StripsSeededSkillGrants(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Marker present in the live config (as after a real boot's SeedConfig)…
	api.agentLoop.GetConfig().SeededSkillGrants = []string{coreagent.SkillsMigrationDefineDone}
	// …and durably recorded on disk.
	configPath := filepath.Join(api.homePath, "config.json")
	require.NoError(t, persistSeededSkillGrants(configPath,
		[]string{coreagent.SkillsMigrationDefineDone}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, onWire := resp["seeded_skill_grants"]
	assert.False(t, onWire,
		"seeded_skill_grants is internal-only and must be stripped from the config response")

	// The disk file still carries it — the strip is wire-only, not a delete.
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	assert.Equal(t, []any{coreagent.SkillsMigrationDefineDone}, onDisk["seeded_skill_grants"],
		"config.json on disk must keep the marker")
}
