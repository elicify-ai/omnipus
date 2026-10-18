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

// F02 / chief disposition: a correctly attached mandatory Slack pair is
// credentials-present on BOTH read tools. Neither binding nor live platform
// authentication is a prerequisite for this configuration-presence result.
func TestWU4Selection_PairedSlackReadbackRecognizesCredentials(t *testing.T) {
	for _, id := range []string{"slack", "slack.selection"} {
		t.Run(id, func(t *testing.T) {
			f := newWU4BFixture(t)
			f.cfg.Channels[id] = config.ChannelInstanceConfig{Type: "slack", Enabled: false}
			f.persist(t)
			reg, sid, resumes := wu4bCardRegistry(t, f)
			card, err := reg.Create(context.Background(), sid, "omnipus", "wu4b-webchat", "", secretrequest.Input{
				Purpose: "Slack paired credential readback", Instance: id, Field: "token",
			})
			require.NoError(t, err)
			require.Equal(t, []gen.SecretRequestField{{Key: "bot_token", Label: "Bot token"}, {Key: "app_token", Label: "App token"}}, card.Card.Fields)
			values := map[string]string{"bot_token": "FAKE-SELECTION-SLACK-BOT", "app_token": "FAKE-SELECTION-SLACK-APP"}
			submitted, err := reg.Submit(card.Card.RequestId, "wu4b-owner", gen.SecretSubmitRequest{Values: &values})
			require.NoError(t, err)
			require.Len(t, submitted.Card.ReferenceNames, 2)
			require.Len(t, *resumes, 1, "real Submit produced one reference-only continuation")
			configure := systools.NewChannelConfigureTool(f.d)
			result := configure.Execute(context.Background(), systools.WithCurrentRevisionForTest(configure, map[string]any{
				"id": id, "bot_token_ref": submitted.Card.ReferenceNames[0], "app_token_ref": submitted.Card.ReferenceNames[1],
			}))
			wu3Success(t, result)
			wu3Outcome(t, result, "saved but not working")
			ch := f.cfg.Channels[id]
			require.Equal(t, config.ChannelInstanceConfig{Type: "slack", Enabled: false,
				BotTokenRef: submitted.Card.ReferenceNames[0], AppTokenRef: submitted.Card.ReferenceNames[1]}, ch,
				"fixture is genuinely unbound and contains only the two typed references, no legacy credential/identity escape")
			before := f.snapshot(t)
			row := selectionChannelRow(t, f.d, id)
			assert.Equal(t, "configured", row["status"], "F02: paired Slack references are credentials-present, not needs_credentials")
			assert.Equal(t, false, row["enabled"], "presence does not enable an unbound channel")
			testResult := systools.NewChannelTestTool(f.d).Execute(context.Background(), map[string]any{"id": id})
			tested := wu3Success(t, testResult)
			assert.Equal(t, id, tested["id"])
			assert.Equal(t, true, tested["success"], "F02: real test_channel must recognize the typed pair")
			assert.Equal(t, false, tested["enabled"], "this is not a live connection claim")
			assert.NotContains(t, tested["message"], "Use configure_channel", "a correct attachment must not prompt credential entry again")
			for _, value := range values {
				assert.NotContains(t, result.ForLLM, value)
				assert.NotContains(t, testResult.ForLLM, value)
			}
			f.unchanged(t, before)
			assert.Equal(t, 1, f.saves, "only the preceding reference attachment saves")
			assert.Zero(t, f.reloads, "readback is not a platform probe or live reload")
			assert.Zero(t, f.reconciles)
			assert.Zero(t, f.listings)
		})
	}
}

// F02 controls distinguish credential presence from both an always-configured
// implementation and a typed-ref-only replacement that loses legacy support.
func TestWU4Selection_ChannelReadbackPreservesLegacyAndAbsentControls(t *testing.T) {
	cases := []struct {
		name, wantStatus string
		channel          config.ChannelInstanceConfig
		wantSuccess      bool
	}{
		{"legacy_token", "configured", config.ChannelInstanceConfig{Type: "telegram", TokenRef: "legacy-token-ref"}, true},
		{"legacy_identity", "configured", config.ChannelInstanceConfig{Type: "telegram", Identity: &config.ChannelIdentity{Kind: "agent", ID: "omnipus"}}, true},
		{"absent_references", "needs_credentials", config.ChannelInstanceConfig{Type: "telegram"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newWU4BFixture(t)
			const id = "telegram.selection"
			f.cfg.Channels[id] = tc.channel
			f.persist(t)
			before := f.snapshot(t)
			row := selectionChannelRow(t, f.d, id)
			assert.Equal(t, tc.wantStatus, row["status"])
			testResult := systools.NewChannelTestTool(f.d).Execute(context.Background(), map[string]any{"id": id})
			tested := wu3Success(t, testResult)
			assert.Equal(t, id, tested["id"])
			assert.Equal(t, tc.wantSuccess, tested["success"])
			if tc.wantSuccess {
				assert.Equal(t, false, tested["enabled"])
				assert.NotContains(t, tested["message"], "Use configure_channel")
			} else {
				assert.Contains(t, tested["message"], "has no credentials", "absent-ref control proves the reader distinguishes missing credentials")
			}
			f.unchanged(t, before)
			assert.Zero(t, f.saves)
		})
	}
}

func selectionChannelRow(t *testing.T, deps *systools.Deps, id string) map[string]any {
	t.Helper()
	listed := wu3Success(t, systools.NewChannelListTool(deps).Execute(context.Background(), map[string]any{}))
	rows, ok := listed["channels"].([]any)
	require.True(t, ok, "real list_channels returns its channel rows")
	var found []map[string]any
	for _, value := range rows {
		row := wu3Object(t, value)
		if row["id"] == id {
			found = append(found, row)
		}
	}
	require.Len(t, found, 1, "readback must find the exact instance, not the platform default")
	return found[0]
}
