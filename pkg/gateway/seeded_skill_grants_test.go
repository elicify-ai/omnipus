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
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
