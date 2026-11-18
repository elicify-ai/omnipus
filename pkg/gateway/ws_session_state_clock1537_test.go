package gateway

// Oracle: helper-model-notices-spec.md Contract Changes / FR-008, chief Q1 A.
// Exercise the real emit and attach, not a test-built SessionStateFrame.
import (
	"context"
	"encoding/json"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionState_EmittedAtMsConnectionOpen(t *testing.T) {
	f := newAttachFixture(t, "q1-open-turn", "q1-open-message")
	wc := f.newConn("q1-open-client")
	before := q1FractionalClockReading()
	f.h.emitSessionState(wc, "")
	after := time.Now()
	frames := pagingDrain(t, wc)
	require.Len(t, frames, 1, "Q1 open control: actual connection-open emit produced exactly one frame")
	require.Equal(t, "session_state", frames[0].Type)
	assertSessionStateMillisecondInstant(t, frames[0].raw, before, after)
}

func TestSessionState_EmittedAtMsSnapshotAttach(t *testing.T) {
	f := newAttachFixture(t, "q1-attach-turn", "q1-attach-message")
	wc := f.newConn("q1-attach-client")
	before := q1FractionalClockReading()
	f.h.handleAttachSession(context.Background(), "q1-attach-client", f.sid, nil, wc)
	after := time.Now()
	frames := pagingDrain(t, wc)
	require.GreaterOrEqual(t, indexOfType(frames, "session_snapshot"), 0, "Q1 attach control: real snapshot, not an unscoped emit")
	state := indexOfType(frames, "session_state")
	require.GreaterOrEqual(t, state, 0, "Q1 attach control: actual attach emitted session_state")
	complete := indexOfType(frames, "catch_up_complete")
	require.Greater(t, complete, state, "Q1 attach control: successful catch-up follows the actual state")
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(frames[state].raw, &raw))
	require.JSONEq(t, strconv.Quote(f.sid), string(raw["session_id"]), "Q1 attach control: the state is addressed to the real session")
	assertSessionStateMillisecondInstant(t, frames[state].raw, before, after)
}

// Avoid a whole-second lower bound: sec*1000 must not pass merely because the
// test happened to start exactly on a second. No production clock is replaced.
func q1FractionalClockReading() time.Time {
	now := time.Now()
	for now.UnixMilli()%1000 == 0 {
		runtime.Gosched()
		now = time.Now()
	}
	return now
}

func assertSessionStateMillisecondInstant(t *testing.T, data []byte, before, after time.Time) {
	t.Helper()
	var frame map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &frame))
	raw, present := frame["emitted_at_ms"]
	if !present {
		t.Fatal("BLOCKED: session_state.emitted_at_ms not implemented — required by #1537 FR-008 / Contract Changes")
	}
	milliseconds, err := strconv.ParseInt(string(raw), 10, 64)
	require.NoError(t, err, "Q1 gateway: emitted_at_ms is a JSON int64, never a string, fraction or null")
	assert.GreaterOrEqual(t, milliseconds, before.UnixMilli(), "Q1 gateway: emitted_at_ms retains the real fractional emit instant, not sec*1000")
	assert.LessOrEqual(t, milliseconds, after.UnixMilli(), "Q1 gateway: emitted_at_ms is no later than the completed call")
	var emitted string
	require.NoError(t, json.Unmarshal(frame["emitted_at"], &emitted))
	second, err := time.Parse(time.RFC3339, emitted)
	require.NoError(t, err, "Q1 gateway: legacy emitted_at stays RFC3339")
	assert.Equal(t, second.Format(time.RFC3339), emitted, "Q1 gateway: legacy emitted_at stays whole-second RFC3339, without fractional digits")
	assert.Zero(t, second.Nanosecond(), "Q1 gateway: legacy emitted_at does not gain fractional precision")
	assert.Equal(t, time.UnixMilli(milliseconds).UTC().Truncate(time.Second), second.UTC(), "Q1 gateway: both clock fields name the same instant truncated to seconds")
}
