package channels

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// R7-A01 / DESIGN §3: early Stop fences the exact accepted start before the
// guard-taking teardown. A refused owned start cannot turn into a permitted,
// anonymous, non-cancelled linked-device connection on the same snapshot.
func TestPairingStart_EarlyFenceCannotBecomeAnonymousLinkedStart(t *testing.T) {
	for _, fence := range []string{"exact operation", "unrelated owner", "no Stop"} {
		t.Run(fence, func(t *testing.T) {
			store := NewPairingAttempts()
			before := &config.Config{Channels: map[string]config.ChannelInstanceConfig{}}
			cfg := &config.Config{Channels: map[string]config.ChannelInstanceConfig{"whatsapp": {Type: "whatsapp", Enabled: true}}}
			order := store.LockChannel("whatsapp")
			a, err := order.Admit(PairingAdmissionConnect, &PairingOwner{SessionID: "chat-a", RequestID: "connect-a"})
			require.NoError(t, err)
			commit, err := PreparePairingConfig(before, cfg, []*PairingOrder{order}, map[string]string{"whatsapp": a.AttemptID})
			require.NoError(t, err)
			require.Empty(t, commit.Commit(true))
			order.Unlock()
			token, stamp, current := store.ReadyForSnapshot("whatsapp", cfg)
			require.True(t, current)
			require.Equal(t, a.AttemptID, token, "instrument: this real config commit readied its own operation")
			require.Equal(t, a.AttemptID, stamp.AttemptID)
			if fence != "no Stop" {
				selected := store.OwnedBy("chat-a")
				require.Len(t, selected, 1)
				if fence == "unrelated owner" {
					selected[0].Owner = &PairingOwner{SessionID: "chat-b", RequestID: "connect-b"}
				}
				store.FenceStarts(selected)
			}
			token, stamp, current = store.ReadyForSnapshot("whatsapp", cfg)
			order = store.LockChannel("whatsapp")
			accepted := order.AcceptsStartStamp(token, stamp)
			order.Unlock()
			if fence == "exact operation" {
				assert.False(t, current && accepted && !stamp.Cancelled, "fenced operation must not be accepted for a real connect; token=%q stamp=%+v", token, stamp)
				assert.False(t, store.IsReady("whatsapp", a.AttemptID), "Stop's early fence remains in force before BeginStop")
			} else {
				assert.True(t, current && accepted)
				assert.Equal(t, a.AttemptID, token)
				assert.Equal(t, a.AttemptID, stamp.AttemptID)
				assert.False(t, stamp.Cancelled)
			}
		})
	}
}
