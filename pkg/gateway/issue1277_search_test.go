package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// #1277 founder decision 2026-10-12 includes keyed web-search integrations.
// Keep the stored credential reference in the error surfaced to a user/agent;
// a generic "no API key" does not identify the secret the operator must fix.
func issue1277RequireSearchRefError(t *testing.T, cfg *config.Config, store *credentials.Store, ref string) {
	t.Helper()
	_, err := store.Get(ref)
	var missing *credentials.NotFoundError
	require.ErrorAs(t, err, &missing, "instrument: this is a missing entry, not a store fault")
	require.Equal(t, ref, missing.Name)
	tool, err := tools.NewWebSearchTool(tools.WebSearchToolOptions{
		Roles: func() *config.WebToolsConfig { return &cfg.Tools.Web },
	})
	require.NoError(t, err, "missing one integration key must not prevent the search tool from constructing")
	result := tool.Execute(t.Context(), map[string]any{"query": "Omnipus", "provider": "tavily"})
	require.True(t, result.IsError, "the bad search provider must not issue a request without its credential")
	require.Contains(t, result.ForLLM, "tavily")
	require.Contains(t, result.ForLLM, ref, "#1277: the visible search error must name the missing credential reference")
}
