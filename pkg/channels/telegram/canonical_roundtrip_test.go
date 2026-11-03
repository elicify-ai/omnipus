package telegram

// canonical_roundtrip_test.go — T-10b, the parser half of the canonicalizer
// contract (channels-spec-r8, spec line "the Telegram adapter's
// parseTelegramChatID applied to the stored bare id returns exactly the chat
// id and topic that were validated (test T-10b)"; S-14 r2 rows, S-15;
// FR-012's numeric rules: chat id signed 64-bit non-zero, topic 1 … 2^31−1
// and only after a negative chat id).
//
// Oracle: dataset D1's ACCEPTED telegram rows (S-15 rows 16–22i). For every
// canonical value the canonicalizer accepts, the stored bare id (the
// canonical value without its "telegram:" prefix) must parse back to exactly
// the chat id and topic the row validated — and a topic target's topic is
// never 0 (the send path sets MessageThreadID from it).
//
// This pins EXISTING parser behaviour the spec relies on (the canonicalizer
// owns the sign/zero/topic rules and is tested in pkg/channels; the parser is
// deliberately lenient there). Positive control: passes today.
//
// Characterization note: expected values derive from D1's accepted rows, not
// from running the parser; the parser's own lenient edges ("007" parses as 7)
// are out of scope — D1 refuses them upstream and this file tests only the
// round trip on accepted canonical forms.

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestT10b_ParseTelegramChatID_RoundTripsAcceptedCanonicalBareIDs(t *testing.T) {
	rows := []struct {
		name   string
		bareID string // canonical value minus the "telegram:" prefix
		chatID int64
		topic  int
	}{
		{"person", "123456", 123456, 0},
		{"group supergraph", "-1001234567890", -1001234567890, 0},
		{"topic", "-1001234567890/45", -1001234567890, 45},
		{"int64 max", "9223372036854775807", math.MaxInt64, 0},
		{"int64 min", "-9223372036854775808", math.MinInt64, 0},
		{"topic one", "-100/1", -100, 1},
		{"topic int32 max", "-100/2147483647", -100, math.MaxInt32},
		{"int64 min with topic int32 max", "-9223372036854775808/2147483647", math.MinInt64, math.MaxInt32},
	}
	for _, r := range rows {
		chatID, topic, err := parseTelegramChatID(r.bareID)
		require.NoError(t, err, "T-10b %s: accepted bare id %q parses", r.name, r.bareID)
		require.Equal(t, r.chatID, chatID, "T-10b %s: chat id round trip", r.name)
		require.Equal(t, r.topic, topic, "T-10b %s: topic round trip", r.name)
		if r.topic != 0 {
			require.NotZero(t, topic, "T-10b %s: a topic target's MessageThreadID is never 0", r.name)
		}
	}
}
