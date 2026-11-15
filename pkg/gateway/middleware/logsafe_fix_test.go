// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogsafeWarn_KeepsStructuredFields: logsafeWarn must forward its
// key/value pairs as separate structured attributes (the planted-cookie
// guard's names/path/method/state_changing fields), not as one slice value
// that slog renders as !BADKEY — that loses every field of the warning an
// operator relies on to see a planted-cookie detection.
func TestLogsafeWarn_KeepsStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logsafeWarn("probe", "path", "/api/v1/agents", "state_changing", true, "names", []string{"csrf"})

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec), "one JSON record: %s", buf.String())
	assert.NotContains(t, rec, "!BADKEY", "the args must not collapse into one unkeyed value")
	assert.Equal(t, "/api/v1/agents", rec["path"])
	assert.Equal(t, true, rec["state_changing"], "booleans stay machine-readable")
	assert.Equal(t, "csrf", rec["names"], "safeLogArgs joins a []string into one sanitised string by design")
}
