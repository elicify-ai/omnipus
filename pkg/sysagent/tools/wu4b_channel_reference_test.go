package systools_test

import (
	"context"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exactly the current D1 card-selectable fields, not every future credential
// field on ChannelInstanceConfig. Slack's two fields use a real paired card in
// the next row. Mapping nick_serv_password to nickserv_password_ref follows the
// actual config field name; it is not a guessed reflection/suffix oracle.
func TestWU4B_ChannelAllSingleCardFieldsSelectTheirExactConfigReference(t *testing.T) {
	cases := []struct {
		platform, field, parameter string
		selected                   func(config.ChannelInstanceConfig) string
	}{
		{"telegram", "token", "token_ref", func(c config.ChannelInstanceConfig) string { return c.TokenRef }},
		{"feishu", "app_secret", "app_secret_ref", func(c config.ChannelInstanceConfig) string { return c.AppSecretRef }},
		{"feishu", "encrypt_key", "encrypt_key_ref", func(c config.ChannelInstanceConfig) string { return c.EncryptKeyRef }},
		{"feishu", "verification_token", "verification_token_ref", func(c config.ChannelInstanceConfig) string { return c.VerificationTokenRef }},
		{"dingtalk", "client_secret", "client_secret_ref", func(c config.ChannelInstanceConfig) string { return c.ClientSecretRef }},
		{"matrix", "access_token", "access_token_ref", func(c config.ChannelInstanceConfig) string { return c.AccessTokenRef }},
		{"matrix", "crypto_passphrase", "crypto_passphrase_ref", func(c config.ChannelInstanceConfig) string { return c.CryptoPassphraseRef }},
		{"line", "channel_secret", "channel_secret_ref", func(c config.ChannelInstanceConfig) string { return c.ChannelSecretRef }},
		{"line", "channel_access_token", "channel_access_token_ref", func(c config.ChannelInstanceConfig) string { return c.ChannelAccessTokenRef }},
		{"wecom", "secret", "secret_ref", func(c config.ChannelInstanceConfig) string { return c.SecretRef }},
		{"irc", "password", "password_ref", func(c config.ChannelInstanceConfig) string { return c.PasswordRef }},
		{"irc", "nick_serv_password", "nickserv_password_ref", func(c config.ChannelInstanceConfig) string { return c.NickServPasswordRef }},
		{"irc", "sasl_password", "sasl_password_ref", func(c config.ChannelInstanceConfig) string { return c.SASLPasswordRef }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			f := newWU4BFixture(t)
			id := tc.platform + ".wu4b"
			original := config.ChannelInstanceConfig{Type: tc.platform, Enabled: false, WorkspaceID: "unchanged-workspace"}
			f.cfg.Channels[id] = original
			ref := systools.IssueCardReferencesForTest(t, f.d, secretrequest.Input{Instance: id, Field: tc.field}, "WU4B-CHANNEL-VALUE")[0]
			tool := systools.NewChannelConfigureTool(f.d)
			f.persist(t)
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), systools.WithCurrentRevisionForTest(tool, map[string]any{"id": id, tc.parameter: ref}))
			wu3Success(t, result)
			wu3Outcome(t, result, "saved but not working")
			selected := f.cfg.Channels[id]
			assert.Equal(t, ref, tc.selected(selected), "exact credential field is actually reachable")
			assert.Equal(t, []string{ref}, nonemptyChannelRefs(selected))
			assert.Equal(t, original.Enabled, selected.Enabled)
			assert.Equal(t, original.WorkspaceID, selected.WorkspaceID)
			assert.Equal(t, before.credentials, f.snapshot(t).credentials)
			assert.Zero(t, f.reloads, "channel tool reports reload needed, never live authentication")
			assert.NotContains(t, result.ForLLM, "WU4B-CHANNEL-VALUE")
		})
	}
}

func nonemptyChannelRefs(c config.ChannelInstanceConfig) []string {
	refs := make([]string, 0, len(c.CredentialRefs()))
	for _, ref := range c.CredentialRefs() {
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

// BDD-6.9/D2-A: the server-expanded mandatory Slack pair must make it through
// configure_channel, not just exist in the card schema or encrypted store.
func TestWU4B_SlackPairedCardReferencesReachBothConsumerFields(t *testing.T) {
	f := newWU4BFixture(t)
	f.cfg.Channels["slack.wu4b"] = config.ChannelInstanceConfig{Type: "slack", Enabled: false}
	f.persist(t)
	reg, sid, _ := wu4bCardRegistry(t, f)
	card, err := reg.Create(context.Background(), sid, "omnipus", "wu4b-webchat", "", secretrequest.Input{Purpose: "Slack bot and application credentials", Instance: "slack.wu4b", Field: "token"})
	require.NoError(t, err)
	require.Equal(t, []gen.SecretRequestField{{Key: "bot_token", Label: "Bot token"}, {Key: "app_token", Label: "App token"}}, card.Card.Fields)
	values := map[string]string{"bot_token": "WU4B-SLACK-BOT-VALUE", "app_token": "WU4B-SLACK-APP-VALUE"}
	submitted, err := reg.Submit(card.Card.RequestId, "wu4b-owner", gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	require.Len(t, submitted.Card.ReferenceNames, 2)
	before := f.snapshot(t)
	tool := systools.NewChannelConfigureTool(f.d)
	result := tool.Execute(context.Background(), systools.WithCurrentRevisionForTest(tool, map[string]any{
		"id": "slack.wu4b", "bot_token_ref": submitted.Card.ReferenceNames[0], "app_token_ref": submitted.Card.ReferenceNames[1],
	}))
	wu3Success(t, result)
	wu3Outcome(t, result, "saved but not working")
	assert.Equal(t, config.ChannelInstanceConfig{Type: "slack", Enabled: false, BotTokenRef: submitted.Card.ReferenceNames[0], AppTokenRef: submitted.Card.ReferenceNames[1]}, f.cfg.Channels["slack.wu4b"])
	assert.Equal(t, before.credentials, f.snapshot(t).credentials)
	assert.NotContains(t, string(f.snapshot(t).config), values["bot_token"])
	assert.NotContains(t, result.ForLLM, values["app_token"])
}

func TestWU4B_ChannelRejectsEachChangedRecipientDimension(t *testing.T) {
	for _, dimension := range []string{"instance", "platform", "account", "credential_field"} {
		t.Run(dimension, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, "configure_channel")
			r := wu4bChannelRecipient(t, f, "telegram", "token")
			switch dimension {
			case "instance":
				r.Instance = "telegram.other"
			case "platform":
				r.ChannelType = "discord"
			case "account":
				r.Account = "other-account-fingerprint"
			case "credential_field":
				r.Field = "app_secret"
			}
			args["token_ref"] = f.bound(t, r, "WU4B-WRONG-CHANNEL-RECIPIENT")
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), args)
			f.unchanged(t, before)
			wu4bRefusal(t, result, "recipient")
		})
	}
}

func TestWU4B_ChannelChangedLiveAccountRequiresNewCardDespiteCurrentRevision(t *testing.T) {
	for _, dimension := range []string{"bot_id", "app_id", "mode", "endpoint"} {
		t.Run(dimension, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, "configure_channel")
			c := f.cfg.Channels["telegram"]
			switch dimension {
			case "bot_id":
				c.BotID = "different-bot"
			case "app_id":
				c.AppID = "different-application"
			case "mode":
				c.Mode = "different-mode"
			case "endpoint":
				c.BaseURL = "https://other-channel.wu4b.invalid"
			}
			f.cfg.Channels["telegram"] = c
			f.persist(t)
			args = systools.WithCurrentRevisionForTest(tool, args)
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), args)
			f.unchanged(t, before)
			wu4bRefusal(t, result, "recipient")
		})
	}
}
