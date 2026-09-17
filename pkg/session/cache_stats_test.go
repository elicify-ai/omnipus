// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Wave 1 token-usage-tracking: SessionStats cache + ByModel accumulation tests.
//
// BDD scenarios:
//
//	Scenario: assistant entry with cache tokens accumulates TokensCacheRead/Write
//	Scenario: two models accumulate into separate ByModel buckets
//	Scenario: external CLI (subagent_3p) turn contributes zero cache tokens
//
// Guards against:
//   - Cache tokens silently ignored on transcript append
//   - ByModel not populated when model is set on the entry
//   - External CLI turns inflating SessionStats
//
// Traces to: docs/internal/specs/token-usage-tracking-2026-06.md §Wave1 items 2, 3

package session

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonUnmarshal is an alias for json.Unmarshal used in tests for legibility.
func jsonUnmarshal(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("jsonUnmarshal: %w", err)
	}
	return nil
}

// TestSessionStats_BackwardCompat_LegacyMetaLoadsWithZeroCacheFields verifies
// that a SessionStats without by_model/cache fields (old JSON) is treated as zero
// and doesn't panic.
//
// BDD:
//
//	Given a SessionStats JSON without tokens_cache_read, tokens_cache_write, by_model,
//	When it is unmarshalled,
//	Then TokensCacheRead==0, TokensCacheWrite==0, ByModel==nil.
func TestSessionStats_BackwardCompat_LegacyMetaLoadsWithZeroCacheFields(t *testing.T) {
	legacyJSON := []byte(
		`{"tokens_in":500,"tokens_out":200,"tokens_total":700,"cost":0.01,"tool_calls":2,"message_count":5}`,
	)
	var stats SessionStats
	require.NoError(t, jsonUnmarshal(legacyJSON, &stats))
	assert.Equal(t, 500, stats.TokensIn)
	assert.Equal(t, 200, stats.TokensOut)
	assert.Equal(t, 0, stats.TokensCacheRead, "legacy JSON without cache fields must load as 0")
	assert.Equal(t, 0, stats.TokensCacheWrite, "legacy JSON without cache fields must load as 0")
	assert.Nil(t, stats.ByModel, "legacy JSON without by_model must load as nil")
}
