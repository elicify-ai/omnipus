package discord

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/require"
)

func TestT09_DiscordStart_CachedGroupBypassesPersonDMOpening(t *testing.T) {
	// T-9 step 1 and S-11: an allowed cached group is posted to its own id,
	// not opened as if it were a person. Reuse only the external HTTP fixture.
	ch, network := t09Adapter(t, t09HTTPResponse{Status: 200, Body: `{"id":"777","channel_id":"222","content":"QA group"}`})
	require.NoError(t, ch.session.State.GuildAdd(&discordgo.Guild{ID: "qa-guild"}))
	require.NoError(t, ch.session.State.ChannelAdd(&discordgo.Channel{ID: "222", GuildID: "qa-guild", Type: 0}))
	err := ch.Send(context.Background(), t09StartMessage(t, "222", "QA group"))
	require.NoError(t, err, "T-9: cached supported group is a send target, not a person")
	calls := t09RequireRoutes(t, network, []string{t09Route("POST", "/channels/222/messages")})
	var message map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(calls[0].Body), &message))
	require.JSONEq(t, `"QA group"`, string(message["content"]))
}
