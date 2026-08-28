// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// default_model_boot_regression_test.go — regression coverage for the
// CRITICAL UAT-found data-integrity defect: PUT /api/v1/providers/default-model
// returned 200 and persisted a pair to config.json that the boot path could
// never resolve (config.GetModelConfig requires the pair to equal a row's
// legacy singular Model field exactly, a field the PUT never writes), so the
// running process silently rolled back to its previous in-memory state while
// disk kept the new pair — and the NEXT restart never bound at all
// (createStartupProvider -> providers.CreateProvider -> "default model ...
// not found in providers").
//
// Two invariants, one test each:
//
//	(a) A pair the PUT accepts with 200 must actually take effect — proven
//	    here by feeding the config.json the PUT wrote through the REAL
//	    config loader (config.LoadConfig, exactly what gateway.go's boot
//	    path uses) and then the REAL boot provider-construction call
//	    (providers.CreateProvider, exactly what createStartupProvider
//	    calls) — never a re-parse of the test's own in-memory cfg struct.
//	(b) A pair the PUT cannot apply must be REJECTED (non-2xx) and persist
//	    NOTHING — config.json must be byte-identical before and after.
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestDefaultModel_PutThenRealConfigLoaderBoots is invariant (a): a
// default-model pair drawn from a row's Models[] list (never its legacy
// singular Model field) that the PUT accepts with 200 must produce a
// config.json that the REAL config loader reads back AND that the REAL boot
// provider-construction call can turn into a working provider — not just a
// config struct this test built in memory.
//
// Before the fix this test FAILS at the providers.CreateProvider step with
// exactly the UAT-reported error: `default model "mockgw/mock/model-a" not
// found in providers: model "mock/model-a" not found in providers for
// provider "mockgw"` — because config.GetModelConfig (the pre-fix boot
// resolver) requires the pair to equal the row's OWN Model field ("default"
// here), never its Models[] list.
func TestDefaultModel_PutThenRealConfigLoaderBoots(t *testing.T) {
	rows := []*config.ModelConfig{
		{
			Name: "mockgw", Provider: "mockgw", Model: "default",
			Models:  []string{"mock/model-a"},
			APIBase: "https://mock.example/v1", Protocol: "openai-compatible",
		},
	}
	api, tmpDir, _ := newDefaultModelAPI(t, config.DefaultModel{}, rows, &restMockProvider{})

	// The PUT: the pair the Providers card would offer for a connected
	// custom row — the row's Models[] pick, not its legacy Model field.
	w := doDefaultModel(api, http.MethodPut, `{"provider":"mockgw","model":"mock/model-a"}`)
	require.Equal(t, 200, w.Code, "PUT must accept a pair the row legitimately serves; body=%s", w.Body.String())

	// Invariant (a), step 1: the REAL config loader — not the test's own cfg
	// struct — must read the persisted config.json back without error.
	cfgPath := filepath.Join(tmpDir, "config.json")
	loaded, err := config.LoadConfig(cfgPath)
	require.NoError(t, err, "config.json written by a successful PUT must load via the real loader")
	require.Equal(t, config.DefaultModel{Provider: "mockgw", Model: "mock/model-a"},
		loaded.Agents.Defaults.DefaultModel,
		"the loaded config must carry the exact pair the PUT wrote")

	// Invariant (a), step 2: the REAL boot provider-construction call
	// (createStartupProvider's own call, unwrapped) must succeed against the
	// config the real loader produced — this is the exact call that panicked
	// at process restart in the UAT repro.
	_, modelID, err := providers.CreateProvider(loaded)
	require.NoError(t, err,
		"providers.CreateProvider (the boot path createStartupProvider calls) must resolve "+
			"a pair the default-model PUT accepted with 200 — a 200 that does not survive "+
			"a restart is the exact defect this test guards")
	assert.Equal(t, "mock/model-a", modelID,
		"the boot provider must be asked to serve the EXACT requested model, not the row's "+
			"own stored Model field ('default')")
}

// TestDefaultModel_PutRejectsUnapplicablePair_ConfigUnchanged is invariant
// (b): a pair the resolver cannot apply (a known, non-custom, non-local
// catalog row whose served catalog does not carry the model, and whose
// Models[] list does not either) must be rejected with a non-2xx status and
// must persist NOTHING — config.json must be byte-identical before and
// after the rejected PUT. Without this invariant, tightening the resolver
// alone would still allow a write-then-reject race to corrupt the file
// (invariant (a) closes the "200 lies" half of the bug; this closes the
// "silently corrupts disk anyway" half).
func TestDefaultModel_PutRejectsUnapplicablePair_ConfigUnchanged(t *testing.T) {
	rows := []*config.ModelConfig{
		{Name: "cloud", Provider: "cloudprov", Model: "model-in-catalog", APIBase: "https://api.cloudprov.example/v1"},
	}
	api, tmpDir, _ := newDefaultModelAPI(t, config.DefaultModel{}, rows, &restMockProvider{})
	api.providerCatalog = defaultModelTestCatalog(t)

	cfgPath := filepath.Join(tmpDir, "config.json")
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var beforeParsed map[string]any
	require.NoError(t, json.Unmarshal(before, &beforeParsed))

	// "not-in-catalog" is in neither cloudprov's Models[] (empty here) nor
	// the served test catalog's model list for cloudprov — this pair cannot
	// be applied by any of ResolveDefaultModelRow's four rungs.
	w := doDefaultModel(api, http.MethodPut, `{"provider":"cloudprov","model":"not-in-catalog"}`)
	require.NotEqual(t, 200, w.Code, "an unapplicable pair must not 200; body=%s", w.Body.String())
	assert.True(t, w.Code >= 400 && w.Code < 500,
		"an unapplicable pair is a client error (400), not a server error; got %d", w.Code)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var afterParsed map[string]any
	require.NoError(t, json.Unmarshal(after, &afterParsed))
	assert.Equal(t, beforeParsed, afterParsed,
		"a rejected default-model PUT must persist NOTHING — config.json must be unchanged")

	// Belt-and-braces: the in-memory config the gateway is serving must also
	// still carry the zero pair — no accidental in-memory mutation either.
	live := api.agentLoop.GetConfig()
	assert.True(t, live.Agents.Defaults.DefaultModel.IsZero(),
		"a rejected PUT must not change the live in-memory default model")
}
