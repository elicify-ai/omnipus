package gateway

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeLogArgsEscapesUserDerivedStringsAndErrors(t *testing.T) {
	args := safeLogArgs("id\nspoofed", errors.New("secret\rvalue"), 42)

	assert.Equal(t, `id\nspoofed`, args[0], "strings must stay on one structured-log record")
	assert.Equal(t, `secret\rvalue`, args[1], "errors must stay on one structured-log record")
	assert.Equal(t, 42, args[2], "non-string values remain machine readable")
}

func TestSafeLogArgsPreservesNilAndTypedValues(t *testing.T) {
	args := safeLogArgs(nil, []string{"a"})

	assert.Nil(t, args[0])
	assert.Equal(t, []string{"a"}, args[1])
}

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
