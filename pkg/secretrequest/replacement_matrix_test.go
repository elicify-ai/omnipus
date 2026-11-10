package secretrequest_test

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// BDD-6.14 / FR-067/072: the 4a seam stores a replacement version without
// changing the integration's selected reference. A later approved
// connect_channel selection is explicitly WU-4b/WU-5 scope, not faked here.
func TestSecretMatrix_IntegrationReplacementDoesNotSelectOrOverwriteV1(t *testing.T) {
	f := newMatrixEnv(t)
	// These exact version names are BDD-6.14's specified fixture.
	const selected = "telegram_tg-1_bot_token.v1"
	const replacement = "telegram_tg-1_bot_token.v2"
	f.cfg.Channels = map[string]config.ChannelInstanceConfig{"tg-1": {Type: "telegram", TokenRef: selected}}
	rec := f.create(f.newSession("omnipus", ""), "omnipus", secretrequest.Input{Purpose: "Replace Telegram token", Instance: "tg-1", Field: "bot_token"})
	_, err := f.store.CommitCard("earlier-selected-request", rec.Card.SessionId, []credentials.CardValue{{
		BaseName: "telegram_tg-1_bot_token", Recipient: rec.Recipients[0], Value: "FAKE-SELECTED-V1",
	}}, time.Now())
	require.NoError(t, err)
	out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"bot_token": "FAKE-UNUSED-V2"}))
	require.NoError(t, err)
	require.Equal(t, "submitted", out.Card.State)
	require.Equal(t, []string{replacement}, out.Card.ReferenceNames)
	require.NoError(t, f.reg.Stop(rec.Card.SessionId))
	require.Equal(t, selected, f.cfg.Channels["tg-1"].TokenRef, "submit/Stop cannot select a replacement integration secret")
	old, err := f.store.Get(selected)
	require.NoError(t, err)
	require.Equal(t, "FAKE-SELECTED-V1", old, "in-use version must not be overwritten")
	newValue, err := f.store.Get(replacement)
	require.NoError(t, err)
	require.Equal(t, "FAKE-UNUSED-V2", newValue, "Stop after submit must not delete the unused replacement")
	names, err := f.store.List()
	require.NoError(t, err)
	require.Equal(t, []string{selected, replacement}, names)
}
