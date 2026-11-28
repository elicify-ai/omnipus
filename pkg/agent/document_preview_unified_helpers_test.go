//go:build goolm && stdjson

package agent

// Helpers for the office-preview tests rewritten to the one PDF mechanism
// (founder decision 2026-11-28: uploads, read_file and preview_document take
// the #1968 per-attempt decision; limits come only from config.json
// pdf_limits). They build a turn whose loop carries a catalogue (PDF and
// image yes/no only) and config limits, and send one attempt's request
// through the production preparation path (prepareAttemptRequest).

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/stretchr/testify/require"
)

// officeUnifiedTurn returns a run turn whose loop has cat and, when limits
// is non-nil, the config pdf_limits override {provider: limits} on top of
// the shipped defaults.
func officeUnifiedTurn(t *testing.T, cat *catalog.Catalog, provider string, limits map[string]any) *agentLoopRunTurn {
	t.Helper()
	cfg := &config.Config{}
	if limits != nil {
		cfg.PDFLimits = officeUnifiedLimits(t, provider, limits)
	}
	al := &AgentLoop{cfg: cfg}
	if cat != nil {
		al.SetCapabilityCatalog(cat)
	}
	ts := newTurnState(&AgentInstance{ID: "office-unified"}, processOptions{}, turnEventScope{})
	return &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background(), chatPDFs: &chatPDFRegistry{}}
}

// officeUnifiedLimits is a config pdf_limits table with one provider
// override, decoded through the real config decoder.
func officeUnifiedLimits(t *testing.T, provider string, limits map[string]any) *config.PDFLimitsConfig {
	t.Helper()
	rows := []pdf1968ProviderRow{{id: provider, limits: limits}}
	table := pdf1968LimitTable(t, nil, rows)
	// pdf1968LimitTable neutralizes the shipped defaults; these tests keep
	// them and state only the override.
	table.Default = config.PDFLimitsRow{}
	return table
}

// officeUnifiedSend prepares one attempt's request for provider/model
// through the production path and returns what the provider would receive.
func officeUnifiedSend(t *testing.T, rt *agentLoopRunTurn, provider, model string, messages []providers.Message) []providers.Message {
	t.Helper()
	prepared, err := rt.prepareAttemptRequest(context.Background(), nil, provider, model, messages, nil, nil, false)
	require.NoError(t, err)
	return prepared.messages
}
