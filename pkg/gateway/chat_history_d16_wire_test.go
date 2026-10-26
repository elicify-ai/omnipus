package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Generated optional pointers decode both absent and null as nil. D16 requires
// actual omission on these carriers, so inspect the real bytes as well. This
// is a raw validation view, never a hand-written wire-format producer/type.
func historyD16RequireOmitted(t *testing.T, raw []byte, field, carrier string) {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.NotNil(t, fields, "D16 %s must be an actual JSON object", carrier)
	require.NotContains(t, fields, field, "D16 %s must omit %s entirely; null is not absence", carrier, field)
}

func historyD16ReplayUsageOmitted(t *testing.T, raw []byte) {
	t.Helper()
	var frame map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &frame))
	require.NotEmpty(t, frame["stats"], "real replay done stats are present before checking omission")
	historyD16RequireOmitted(t, frame["stats"], "session_tokens_total", "replay terminator stats")
	historyD16RequireOmitted(t, frame["stats"], "tokens", "replay terminator stats")
}
