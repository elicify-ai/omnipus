package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Silent B: helper-local authority, independent of caller filtering. Any
// accidentally admitted detached turn is joined before checking durable state;
// legitimate non-human input must remain an unhandled no-op.
func TestI1R4RevivalRequiresHumanTurn(t *testing.T) {
	for _, name := range []string{"system_channel", "steer_message_metadata"} {
		t.Run(name, func(t *testing.T) {
			h := i1R3RestartStopped(t)
			msg := i1R4HumanMessage(h)
			route, _, err := h.al.resolveMessageRoute(msg)
			require.NoError(t, err)
			if name == "system_channel" {
				msg.Channel = "system"
			} else {
				msg.Metadata["steer_message_id"] = "synthetic-system-wake"
			}
			before := h.journal(t)
			handled, reviveErr := h.al.reviveInactiveInbound(route, msg)
			require.NoError(t, reviveErr, "non-human input must not attempt ordinary revival")
			assert.False(t, handled, "system input cannot own an ordinary human continuation")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.True(t, h.al.WaitForActiveRequestsContext(ctx), "join any wrongly admitted test turn before inspecting its effects")
			assert.Equal(t, before, h.journal(t))
			assert.Empty(t, h.provider.calls())
		})
	}
}
