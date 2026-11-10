package gateway

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSafeLogArgsEscapesUserDerivedStringsAndErrors(t *testing.T) {
	args := safeLogArgs("id\nspoofed", errors.New("secret\rvalue"), 42)

	assert.Equal(t, `id\nspoofed`, args[0], "strings must stay on one structured-log record")
	assert.Equal(t, `secret\rvalue`, args[1], "errors must stay on one structured-log record")
	assert.Equal(t, 42, args[2], "non-string values remain machine readable")
}

func TestSafeLogArgsPreservesNilAndTypedValues(t *testing.T) {
	// fix11 (CodeQL go/log-injection): []string and fmt.Stringer values now
	// end in safeLogString like strings and errors — a user-derived slice or
	// Stringer cannot forge a log record ending (the planted-cookie guard's
	// duplicated-names slice was the live taint path). nil and numbers stay
	// machine-readable.
	args := safeLogArgs(nil, 42, []string{"a\nb"}, sqlDuration(1500*time.Millisecond))

	assert.Nil(t, args[0])
	assert.Equal(t, 42, args[1], "non-string values remain machine readable")
	assert.Equal(t, `a\nb`, args[2], "string slices stay on one structured-log record")
	assert.Equal(t, "1.5s", args[3], "Stringer values stay on one structured-log record")
}

// sqlDuration is a fmt.Stringer stand-in for the update path.
type sqlDuration time.Duration

func (d sqlDuration) String() string { return time.Duration(d).String() }

func TestEscapedStringsContainNoRawLineBreaks(t *testing.T) {
	for _, raw := range safeLogArgs("line\nbreak", errors.New("line\rbreak")) {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("quoted log value has type %T, want string", raw)
		}
		assert.NotContains(t, value, "\n")
		assert.NotContains(t, value, "\r")
	}
}

// TestLogsafeHelpersKeepKeyValuePairs guards the variadic forwarding in the
// logsafe* helpers: passing the args slice without "..." made slog see one
// argument and log "!BADKEY=[...]" instead of the key/value pairs.
func TestLogsafeHelpersKeepKeyValuePairs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logsafeError("probe", "stage", "refresh\ninjected", "count", 2)

	out := buf.String()
	assert.NotContains(t, out, "BADKEY", "helpers must forward args as key/value pairs")
	assert.Contains(t, out, `stage=refresh\ninjected`)
	assert.Equal(t, 1, strings.Count(out, "\n"), "one record, no forged line")
	assert.Contains(t, out, "count=2")
}
