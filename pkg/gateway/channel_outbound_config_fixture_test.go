package gateway

// WP-2's T-14/T-19 persisted-state checks must not depend on the WP-1 T-07
// test file. This is the same reader body as readConfigChannelsFile there,
// with a distinct name so both packs can compile together without collision.
// No request, fixture state, persistence oracle or error handling is changed.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func readOutboundConfigChannelsFile(t *testing.T, f *wu6Fixture) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(f.f1277.configPath)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	channels, _ := doc["channels"].(map[string]any)
	require.NotNil(t, channels, "instrument: config file must carry channels")
	return channels
}
