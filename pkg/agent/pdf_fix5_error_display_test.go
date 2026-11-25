package agent

// M1 / FR-3 / S8 / §5.6 C1. The real refusal must cross the EXISTING
// trusted-message display contract live and on replay, or the SPA discards
// its model-specific text. This pairs with chat.pdf-fix5-error.test.ts, which
// exercises that same trust flag through the actual store. No new wire type.

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestPDFCatalogError_ModelIdentityReachesDisplayContract(t *testing.T) {
	const model = "fx/specific-pdf-reader"
	peer := newPdf1968Peer(t, pdf1968Step{content: "must not be called"})
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{pdf1968CapableRow("fx/catalog", model, nil)})
	home := filepath.Join(t.TempDir(), "home")
	al, agents, store, mediaStore := pdf1968NewPeerHarnessAt(t, peer, cat, nil, home, nil, pdf1968Pair{provider: "fx/catalog", model: model})
	events := pdf1968Capture(al)
	sid := pdf1968NewSession(t, store, "pdf1968-agent")
	ref := pdf1968StorePDF(t, mediaStore, "catalog.pdf", pdf1968TextPDF(t, 1, true))
	_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
	require.Error(t, err)
	require.Zero(t, peer.CallCount(), "FR-3: no provider submission without a model list")
	errs := events.modelListErrors()
	require.Len(t, errs, 1)
	wire := WireLLMError(errs[0])
	assert.Equal(t, "model_list_unavailable", wire.Code)
	assert.False(t, wire.Retryable)
	pdf1968AssertC1(t, wire.Message, model)
	// getLLMErrorDisplay consumes the code catalogue unless this existing
	// explicit trust marker is set. Merely naming the model in Message does
	// not prove the bubble can show it. Never broaden raw-provider trust.
	assert.Equal(t, true, wire.ProviderMessage != nil && *wire.ProviderMessage, "M1: live C1 must be marked as controller-authored display copy, not replaced with 'this model'")
	fresh, err := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fresh.Close()) })
	entries, err := fresh.ReadTranscript(sid)
	require.NoError(t, err)
	refusals := 0
	for _, entry := range entries {
		if entry.ErrorCode != "model_list_unavailable" {
			continue
		}
		refusals++
		pdf1968AssertC1(t, entry.Content, model)
		assert.True(t, entry.ProviderMessage, "M1: reopened replay must preserve the curated C1 display marker with its model")
	}
	assert.Equal(t, 1, refusals, "one persisted C1, not a duplicate error")
}
