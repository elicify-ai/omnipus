package channels

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

type restartableChannel struct {
	running bool
	starts  int
}

func (c *restartableChannel) Name() string { return "restartable" }
func (c *restartableChannel) Start(context.Context) error {
	c.starts++
	c.running = true
	return nil
}
func (c *restartableChannel) Stop(context.Context) error                      { c.running = false; return nil }
func (c *restartableChannel) Send(context.Context, bus.OutboundMessage) error { return nil }
func (c *restartableChannel) IsRunning() bool                                 { return c.running }
func (c *restartableChannel) IsAllowed(string) bool                           { return true }
func (c *restartableChannel) IsAllowedSender(bus.SenderInfo) bool             { return true }
func (c *restartableChannel) ReasoningChannelID() string                      { return "" }

// TestManager_StartChannel_RestartsAStoppedRegisteredChannel is Train 2 gate
// r1 architect F04: a channel stopped in place (Stop in the chat ended its
// pairing) stays registered with an unchanged config, so a reload never
// calls Start again. StartChannel is the explicit start an approved
// connect_channel uses; a running channel is left alone and an unknown name
// is refused.
func TestManager_StartChannel_RestartsAStoppedRegisteredChannel(t *testing.T) {
	m, err := NewManager(&config.Config{}, credentials.SecretBundle{}, bus.NewMessageBus(), nil)
	require.NoError(t, err)
	ch := &restartableChannel{running: true}
	m.RegisterChannel("whatsapp", ch)

	started, err := m.StartChannel("whatsapp")
	require.NoError(t, err)
	require.False(t, started, "a running channel is not restarted")
	require.Equal(t, 0, ch.starts)

	require.NoError(t, ch.Stop(context.Background()))
	started, err = m.StartChannel("whatsapp")
	require.NoError(t, err)
	require.True(t, started, "a stopped registered channel is started")
	require.Equal(t, 1, ch.starts)
	require.True(t, ch.IsRunning())

	_, err = m.StartChannel("not-registered")
	require.Error(t, err, "an unknown channel cannot be started here")
}
