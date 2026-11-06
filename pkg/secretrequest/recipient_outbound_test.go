package secretrequest_test

// T-42 / FR-058 / S-62: posting policy is not credential-consent scope.
// Oracles: channels-spec-r8 persisted-configuration projection and S-62 cases
// 1–3. The real registry, credential/session stores and ValidateReference are
// exercised; only the existing matrix fixture's continuation transport is fake.
// Config mutation represents the state after a successful outbound writer;
// writer authorization/persistence belongs to T-13/T-14/T-22, not this package.

import (
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

func t42Cfg(outbound *config.ChannelOutboundConfig, baseURL string) *config.Config {
	return &config.Config{Channels: map[string]config.ChannelInstanceConfig{
		"telegram": {
			Type: "telegram", Enabled: true, WorkspaceID: matrixWorkspace,
			Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"},
			BaseURL:  baseURL, Outbound: outbound,
		},
	}}
}

func t42Recipients(t *testing.T, cfg *config.Config) []credentials.CardRecipient {
	t.Helper()
	recipients, _, _, err := secretrequest.ResolveRecipient(cfg, secretrequest.Input{
		Instance: "telegram", Field: "bot_token",
	})
	require.NoError(t, err)
	require.Len(t, recipients, 1, "instrument: exactly the requested Telegram token recipient resolves")
	return recipients
}

type t42Change struct {
	name          string
	before, after *config.ChannelOutboundConfig
}

func t42PostingChanges() []t42Change {
	return []t42Change{
		{"policy only", &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"telegram:222"}},
			&config.ChannelOutboundConfig{Policy: "reply_only", Targets: []string{"telegram:222"}}},
		{"targets only", &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"telegram:222"}},
			&config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"telegram:333"}}},
		{"setting first appears", nil,
			&config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"telegram:222"}}},
	}
}

func TestT42_OutboundOnlyProjectionIsIdentical_S62(t *testing.T) {
	for _, tc := range t42PostingChanges() {
		t.Run(tc.name, func(t *testing.T) {
			before := t42Recipients(t, t42Cfg(tc.before, "https://api.telegram.org"))
			after := t42Recipients(t, t42Cfg(tc.after, "https://api.telegram.org"))
			require.Equal(t, before, after, "FR-058: outbound is excluded, even when first introduced")
		})
	}
}

func t42OpenCard(t *testing.T, outbound *config.ChannelOutboundConfig) (*matrixEnv, secretrequest.Record) {
	t.Helper()
	f := newMatrixEnv(t)
	f.cfg.Channels = t42Cfg(outbound, "https://api.telegram.org").Channels
	rec := f.create(f.newSession("omnipus", ""), "omnipus", secretrequest.Input{
		Purpose: "Telegram token", Instance: "telegram", Field: "bot_token",
	})
	require.Len(t, rec.Card.Fields, 1)
	require.Equal(t, "bot_token", rec.Card.Fields[0].Key)
	return f, rec
}

func t42SetOutbound(f *matrixEnv, outbound *config.ChannelOutboundConfig) {
	inst := f.cfg.Channels["telegram"]
	inst.Outbound = outbound
	f.cfg.Channels["telegram"] = inst
}

func t42Submit(t *testing.T, f *matrixEnv, rec secretrequest.Record) secretrequest.Record {
	t.Helper()
	out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"bot_token": matrixValue}))
	require.NoError(t, err, "S-62 case 1: an open card remains submittable after a posting-only change")
	require.Equal(t, "submitted", out.Card.State)
	require.Len(t, out.Card.ReferenceNames, 1)
	value, err := f.store.Get(out.Card.ReferenceNames[0])
	require.NoError(t, err)
	require.Equal(t, matrixValue, value, "success commits the requested token, not just a submitted state")
	receipt, exists, err := f.store.CardReceipt(rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists, "success has a durable commit receipt")
	require.Equal(t, out.Card.ReferenceNames, receipt.References)
	return out
}

func TestT42_OutboundOnlyChangeKeepsAnOpenCardSubmittable_S62(t *testing.T) {
	for _, tc := range t42PostingChanges() {
		t.Run(tc.name, func(t *testing.T) {
			f, rec := t42OpenCard(t, tc.before)
			t42SetOutbound(f, tc.after)
			out := t42Submit(t, f, rec)
			metadata, err := f.store.CardSecretMetadata()
			require.NoError(t, err)
			require.Equal(t, rec.Recipients[0], metadata[out.Card.ReferenceNames[0]].Recipient,
				"the stored consent remains exactly the recipient shown on the open card")
		})
	}
}

func TestT42_OutboundOnlyChangeKeepsStoredReferenceSelectable_S62(t *testing.T) {
	for _, tc := range t42PostingChanges() {
		t.Run(tc.name, func(t *testing.T) {
			f, rec := t42OpenCard(t, tc.before)
			out := t42Submit(t, f, rec)
			ref := out.Card.ReferenceNames[0]
			require.NoError(t, secretrequest.ValidateReference(f.store, ref, t42Recipients(t, f.cfg)[0]),
				"instrument: this stored reference is selectable before the change")
			t42SetOutbound(f, tc.after)
			require.NoError(t, secretrequest.ValidateReference(f.store, ref, t42Recipients(t, f.cfg)[0]),
				"S-62 case 2: the same explicit reference selection remains authorized")
		})
	}
}

func TestT42_ConsentScopeChangeStillRefusesOpenCard_S62(t *testing.T) {
	f, rec := t42OpenCard(t, nil)
	inst := f.cfg.Channels["telegram"]
	inst.BaseURL = "https://other-account.example"
	f.cfg.Channels["telegram"] = inst
	_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"bot_token": matrixValue}))
	requireMatrixFailure(t, err, http.StatusConflict, "refused")
	var refusal *secretrequest.RequestError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "secret recipient changed; request a new card", refusal.Message,
		"S-62 case 3: host-scope protection keeps its existing refusal")
	requireNoMatrixCommit(t, f, rec.Card.RequestId)
}

func TestT42_ConsentScopeChangeStillRefusesStoredReference_S62(t *testing.T) {
	f, rec := t42OpenCard(t, nil)
	out := t42Submit(t, f, rec)
	ref := out.Card.ReferenceNames[0]
	require.NoError(t, secretrequest.ValidateReference(f.store, ref, t42Recipients(t, f.cfg)[0]))
	inst := f.cfg.Channels["telegram"]
	inst.BaseURL = "https://other-account.example"
	f.cfg.Channels["telegram"] = inst
	require.EqualError(t, secretrequest.ValidateReference(f.store, ref, t42Recipients(t, f.cfg)[0]),
		"credential reference was not stored for this exact recipient; request a secret for this recipient",
		"S-62 case 3: changed host still cannot select an old consented reference")
}
