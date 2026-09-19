package gateway

import (
	"errors"
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
