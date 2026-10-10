package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClearCommand_RegisteredWithoutAliasAndNewStaysRetired(t *testing.T) {
	var clear *Definition
	for _, d := range BuiltinDefinitions() {
		d := d
		assert.NotEqual(t, "new", d.Name, "/new is retired (FR-031)")
		assert.NotContains(t, d.Aliases, "new")
		assert.NotContains(t, d.Aliases, "clear")
		if d.Name == "clear" {
			clear = &d
		}
	}
	require.NotNil(t, clear, "/clear must be a canonical server command")
	assert.Empty(t, clear.Aliases)
	assert.False(t, clear.Hidden)
	assert.Equal(t, DeliveryAgent, clear.EffectiveDelivery(), "the server executes /clear")
	assert.NotNil(t, clear.Handler)
}

func runClear(t *testing.T, rt *Runtime) (string, error) {
	t.Helper()
	var reply string
	err := clearHandler()(context.Background(), Request{Reply: func(s string) error { reply = s; return nil }}, rt)
	return reply, err
}

func TestClearHandler_Outcomes(t *testing.T) {
	reply, err := runClear(t, &Runtime{ClearHistory: func() error { return nil }})
	require.NoError(t, err)
	assert.Contains(t, reply, "Context cleared")

	reply, err = runClear(t, &Runtime{ClearHistory: func() error { return &ClearRefusedError{Reason: "only in a main or extra chat"} }})
	require.NoError(t, err)
	assert.Equal(t, "only in a main or extra chat", reply, "a refusal explains itself to the person")

	boom := errors.New("disk failed")
	_, err = runClear(t, &Runtime{ClearHistory: func() error { return boom }})
	assert.ErrorIs(t, err, boom, "a real failure is returned, never reported as cleared")

	reply, err = runClear(t, &Runtime{})
	require.NoError(t, err)
	assert.Equal(t, unavailableMsg, reply)
}
