package systools_test

import (
	"context"
	"encoding/json"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const selectionCustomEndpoint = "https://custom.selection.invalid/tenant/v1"
const selectionEndpointValue = "FAKE-SELECTION-ENDPOINT-SUBMITTED-VALUE"

// The expected normalized endpoint and catalog identity are literal inputs
// derived from the ruling, never computed by NormalizeEndpoint or the
// production recipient comparison. The real request tool/registry chooses the
// card fields and binding, then the real Submit publishes the value/receipt.
func selectionIssueEndpointCard(t *testing.T, endpoint string, want credentials.CardRecipient, provider, protocol string) (*wu4bFixture, string, string) {
	t.Helper()
	f := newWU4BFixture(t)
	oldRecipient := credentials.CardRecipient{Kind: "provider_key", Provider: provider, Protocol: protocol, Endpoint: want.Endpoint, Field: "api_key"}
	oldRef := f.bound(t, oldRecipient, "FAKE-SELECTION-ENDPOINT-OLD-VALUE")
	f.cfg.Providers = []*config.ModelConfig{{Provider: provider, Model: "endpoint-fixture-model", Protocol: protocol, APIBase: want.Endpoint, APIKeyRef: oldRef}}
	f.persist(t)
	beforeCard := f.snapshot(t)
	reg, sid, resumes := wu4bCardRegistry(t, f)
	request := tools.NewRequestSecretTool(func() *secretrequest.Registry { return reg }, nil)
	registry := tools.NewToolRegistry()
	registry.Register(request)
	registered, exists := registry.Get("request_secret")
	require.True(t, exists)
	require.Same(t, request, registered)
	ctx := tools.WithAgentID(context.Background(), "omnipus")
	ctx = tools.WithTranscriptSessionID(ctx, sid)
	ctx = tools.WithToolContext(ctx, "webchat", "wu4b-webchat")
	result := registry.ExecuteWithContext(ctx, "request_secret", map[string]any{
		"purpose": "Endpoint-only recipient selection", "endpoint": endpoint,
	}, "webchat", "wu4b-webchat", nil)
	require.False(t, result.IsError, result.ForLLM)
	require.True(t, result.ParksTurn, "real request_secret parks for the secure card")
	pending := reg.Snapshot("wu4b-owner", sid)
	require.Len(t, pending, 1)
	card, err := reg.Get(pending[0].RequestId, "wu4b-owner")
	require.NoError(t, err)
	require.Equal(t, []gen.SecretRequestField{{Key: "api_key", Label: "API key"}}, card.Card.Fields)
	require.Equal(t, []credentials.CardRecipient{want}, card.Recipients, "arbitrary endpoint consent has no invented provider/protocol; catalog consent retains both")
	assert.Contains(t, card.Card.Recipient.Lines, want.Endpoint)
	values := map[string]string{"api_key": selectionEndpointValue}
	submitted, err := reg.Submit(card.Card.RequestId, "wu4b-owner", gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	require.Equal(t, "submitted", submitted.Card.State)
	require.Len(t, submitted.Card.ReferenceNames, 1)
	ref := submitted.Card.ReferenceNames[0]
	metadata, err := f.d.CredStore.CardSecretMetadata()
	require.NoError(t, err)
	require.Equal(t, want, metadata[ref].Recipient)
	stored, err := f.d.CredStore.Get(ref)
	require.NoError(t, err)
	require.Equal(t, selectionEndpointValue, stored)
	require.Len(t, *resumes, 1)
	assert.Equal(t, []string{ref}, (*resumes)[0].Card.ReferenceNames)
	resume, err := json.Marshal((*resumes)[0])
	require.NoError(t, err)
	assert.NotContains(t, string(resume), selectionEndpointValue)
	assert.Contains(t, secretrequest.ResumeMessage((*resumes)[0]), ref)
	assert.NotContains(t, secretrequest.ResumeMessage((*resumes)[0]), selectionEndpointValue)
	afterCard := f.snapshot(t)
	assert.Equal(t, beforeCard.memory, afterCard.memory, "Submit does not select a consumer")
	assert.Equal(t, beforeCard.config, afterCard.config)
	assert.Equal(t, oldRef, f.cfg.Providers[0].APIKeyRef)
	assert.Zero(t, f.saves)
	assert.Zero(t, f.reloads)
	assert.Zero(t, f.listings)
	assert.Zero(t, f.reconciles)
	return f, ref, oldRef
}
