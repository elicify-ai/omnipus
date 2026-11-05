package gateway

// T-30c / S-90 / FR-090. These tests drive the registered, authenticated PUT,
// not a copied rejection fixture or a test-only sanitizer. Independent oracles:
// channels-spec-r8 Contract Changes (safe-echo, first 20 + ..., 10/64/4096),
// T-2 (invalid_id) and S-90. Telegram keeps all validation local: no platform.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

func t30cSafeString(t *testing.T, field, value string) {
	t.Helper()
	for _, character := range value {
		require.True(t, character >= 0x20 && character <= 0x7e && !strings.ContainsRune("\"\\<>&", character),
			"S-90: %s contains unsafe U+%04X in %q; unsafe characters must be dropped, not escaped or substituted", field, character, value)
	}
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	require.Equal(t, len(value)+2, len(encoded), "S-90: every permitted string character serializes to exactly one byte, plus two quotes")
}

func t30cAssertProduced(t *testing.T, raw []byte, indices []int, hints []string, omitted int) gen.ChannelOutboundRejection {
	t.Helper()
	message, serverError := validateBodyAgainstSchema("ChannelOutboundRejection", raw)
	require.False(t, serverError, "instrument: the generated schema must compile: %s", message)
	require.Empty(t, message, "S-90(a): actual serialized rejection must satisfy generated safe-echo patterns and bounds")
	require.Less(t, len(raw), 4096, "S-90(c): measure the actual wire bytes, including the response newline")
	for _, character := range bytes.TrimSpace(raw) {
		require.True(t, character >= 0x20 && character <= 0x7e, "S-90(b): serialized JSON contains a non-printable byte 0x%02x", character)
	}
	require.NotContains(t, string(raw), "\\", "S-90(b): safe strings require no escaping expansion")

	var body gen.ChannelOutboundRejection
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Equal(t, "invalid_targets", string(body.Code))
	require.NotEmpty(t, body.Error, "S-90: a real rejection still explains the refusal")
	t30cSafeString(t, "error", body.Error)
	require.LessOrEqual(t, len(body.Error), 500)
	require.NotNil(t, body.TargetErrors)
	items := *body.TargetErrors
	require.Len(t, items, len(indices), "S-90(d): first ten invalid entries, not first ten request entries or deduplicated hints")
	for i, item := range items {
		require.Equal(t, indices[i], item.Index, "S-90(d): request position is the identifier")
		require.Equal(t, hints[i], item.Target, "Contract Changes: drop unsafe characters before taking first 20 safe characters + ...")
		require.Equal(t, "invalid_id", string(item.Code), "S-90: raw controls/non-numeric values remain invalid, even when their hints are empty")
		t30cSafeString(t, fmt.Sprintf("target_errors[%d].target", i), item.Target)
		require.LessOrEqual(t, len(item.Target), 23, "20 safe characters + three ASCII periods")
		require.NotNil(t, item.Reason, "S-90 cross-check: the indexed reason must reach the operator")
		require.NotEmpty(t, *item.Reason)
		t30cSafeString(t, fmt.Sprintf("target_errors[%d].reason", i), *item.Reason)
		require.LessOrEqual(t, len(*item.Reason), 120)
	}
	if omitted == 0 {
		require.Nil(t, body.OmittedCount, "contract: omitted_count is absent unless more than ten entries were invalid")
	} else {
		require.NotNil(t, body.OmittedCount)
		require.Equal(t, omitted, *body.OmittedCount, "S-90(d): every unlisted invalid request entry is counted exactly once")
	}
	return body
}

func TestT30c_ProductionRejection_SanitizesBoundsAndRequestPositions(t *testing.T) {
	controls := "\x00\t\n" + strings.Repeat("\x7f", 20) + strings.Repeat("a", 233) // exactly 256 raw characters
	escaping := strings.Repeat("\"\\<>&１２３", 30) + "safe"                          // 244 characters; unsafe characters all drop, safe suffix survives
	type rejectionCase struct {
		name    string
		targets []string
		indices []int
		hints   []string
		omitted int
	}
	cases := make([]rejectionCase, 0, 7) // three fixed cases plus four specified count boundaries
	cases = append(cases, []rejectionCase{
		{"control-heavy-max-raw", []string{"111", controls, "222", "prefix" + strings.Repeat("\x7f", 20) + "suffix"}, []int{1, 3}, []string{strings.Repeat("a", 20) + "...", "prefixsuffix"}, 0},
		{"escaping-heavy", []string{"111", escaping}, []int{1}, []string{"safe"}, 0},
		{"short-envelope", []string{strings.Repeat("\x7f", 20)}, []int{0}, []string{""}, 0},
	}...)
	// Boundary around the ten-error envelope, plus the schema maximum of 64.
	// All entries have the same hint but remain distinct request positions.
	// Full 256-character raw values also expose a removed server hint bound.
	for _, count := range []int{9, 10, 11, 64} {
		targets := make([]string, count)
		for i := range targets {
			targets[i] = strings.Repeat("x", 256)
		}
		shown := min(count, 10)
		indices, hints := make([]int, shown), make([]string, shown)
		for i := range indices {
			indices[i], hints[i] = i, strings.Repeat("x", 20)+"..."
		}
		cases = append(cases, rejectionCase{fmt.Sprintf("invalid-batch-%d", count), targets, indices, hints, count - shown})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, deps := t14Fixture(t) // includes the known-working authenticated route control
			for _, target := range tc.targets {
				require.LessOrEqual(t, utf8.RuneCountInString(target), 256, "instrument: exercise a schema-valid request, not an overlong-input refusal")
			}
			beforeDisk, err := os.ReadFile(f.f1277.configPath)
			require.NoError(t, err)
			beforeLive, err := f.al.GetConfig().Clone()
			require.NoError(t, err)
			revision := t14ToolRevision(t, deps, "telegram")
			rec := t14Put(t, f, "telegram", "agent_may_start", tc.targets, revision)
			require.Equal(t, http.StatusBadRequest, rec.Code, "S-90: actual production rejection must be reached: %s", rec.Body.String())
			require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
			t30cAssertProduced(t, rec.Body.Bytes(), tc.indices, tc.hints, tc.omitted)
			afterDisk, err := os.ReadFile(f.f1277.configPath)
			require.NoError(t, err)
			require.Equal(t, beforeDisk, afterDisk, "S-90: rejection commits no file bytes")
			require.Equal(t, beforeLive.Channels, f.al.GetConfig().Channels, "S-90: rejection publishes no policy change")
			// Optional artifact export happens AFTER every assertion. The serial
			// bridge script creates a fresh directory, then drives the real TS
			// decoder only after this Go test process has exited successfully.
			if tc.name == "short-envelope" {
				if path := os.Getenv("CHANNELS_REJECTION_EXPORT_PATH"); path != "" {
					require.NoError(t, os.WriteFile(path, rec.Body.Bytes(), 0o600))
				}
			}
		})
	}
}

func TestT30c_RejectionSchemaInstrument_RejectsUnsafeStringsAndExcessBounds(t *testing.T) {
	// Positive and deliberate-invalid schema controls ensure an empty validator
	// result is meaningful. This verifies the instrument, not the Go producer.
	valid := map[string]any{
		"error": "invalid targets", "code": "invalid_targets",
		"target_errors": []any{map[string]any{"index": 0, "target": "safe", "code": "invalid_id", "reason": "invalid id"}},
	}
	serialize := func(t *testing.T, body map[string]any) []byte {
		t.Helper()
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		return raw
	}
	message, serverError := validateBodyAgainstSchema("ChannelOutboundRejection", serialize(t, valid))
	require.False(t, serverError, message)
	require.Empty(t, message, "instrument: schema accepts a safe control")
	for _, field := range []string{"error", "target", "reason"} {
		t.Run("unsafe-"+field, func(t *testing.T) {
			var altered map[string]any
			require.NoError(t, json.Unmarshal(serialize(t, valid), &altered))
			if field == "error" {
				altered[field] = "unsafe\x7f"
			} else {
				items, ok := altered["target_errors"].([]any)
				require.True(t, ok, "instrument: the cloned valid body contains a target_errors array")
				require.Len(t, items, 1, "instrument: the valid schema control has one target error")
				item, ok := items[0].(map[string]any)
				require.True(t, ok, "instrument: the cloned target error is an object")
				item[field] = "unsafe\x7f"
			}
			msg, internal := validateBodyAgainstSchema("ChannelOutboundRejection", serialize(t, altered))
			require.False(t, internal, msg)
			require.NotEmpty(t, msg, "instrument: generated safe-echo pattern must refuse DEL in %s", field)
		})
	}
	for _, field := range []string{"error", "target", "reason", "target_errors", "omitted_count"} {
		t.Run("excess-bound-"+field, func(t *testing.T) {
			var altered map[string]any
			require.NoError(t, json.Unmarshal(serialize(t, valid), &altered))
			items, ok := altered["target_errors"].([]any)
			require.True(t, ok, "instrument: the cloned valid body contains a target_errors array")
			require.Len(t, items, 1, "instrument: the valid schema control has one target error")
			item, ok := items[0].(map[string]any)
			require.True(t, ok, "instrument: the cloned target error is an object")
			switch field {
			case "error":
				altered[field] = strings.Repeat("e", 501)
			case "target":
				item[field] = strings.Repeat("t", 25) // generated maxLength is 24; actual hints are at most 23
			case "reason":
				item[field] = strings.Repeat("r", 121)
			case "target_errors":
				items := make([]any, 11)
				for i := range items {
					items[i] = item
				}
				altered[field] = items
			case "omitted_count":
				altered[field] = 0 // absent or >= 1, never present as zero
			}
			msg, internal := validateBodyAgainstSchema("ChannelOutboundRejection", serialize(t, altered))
			require.False(t, internal, msg)
			require.NotEmpty(t, msg, "instrument: generated schema must reject the invalid %s bound", field)
		})
	}
}
