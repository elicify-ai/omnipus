//go:build goolm && stdjson

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wu3GuardedRunContext boots a loop from the shipped seed and ceiling with the
// outbound-request guard switched on, then runs the production
// wireSystemTools. Only the model edge is a stub.
func wu3GuardedRunContext(t *testing.T) *runContextWithOptions {
	t.Helper()
	return wu3BootRunContext(t, func(cfg *config.Config) { cfg.Sandbox.SSRF.Enabled = true })
}

// wu3BootRunContext boots a loop from the shipped seed and ceiling, after
// tweak adjusts the config, and runs the production wireSystemTools. Only the
// model edge is a stub.
func wu3BootRunContext(t *testing.T, tweak func(*config.Config)) *runContextWithOptions {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", "0000000000000000000000000000000000000000000000000000000000000000")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	coreagent.SeedConfig(cfg)
	tweak(cfg)
	require.Empty(t, repairAndValidateToolPolicyCoverage(cfg))
	seedSystemAgentEagerSouls(cfg)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: "default", Name: "Default", Status: "active", IsDefault: true, CoreTeam: []string{"omnipus"},
	}))
	store := agentstore.New(home)
	for _, ac := range cfg.Agents.List {
		copyAgent := ac
		require.NoError(t, store.Create(ac.ID, &copyAgent))
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), data, 0o600))
	al, err := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &restMockProvider{})
	require.NoError(t, err)
	t.Cleanup(al.Close)
	api := &restAPI{agentLoop: al, homePath: home}
	credStore := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(credStore))
	rc := &runContextWithOptions{
		homePath: home, configPath: api.configPath(), cfg: al.GetConfig(), agentLoop: al,
		runningServices: &services{restAPIRef: api}, centralMCPReg: tools.NewMCPRegistry(), credStore: credStore,
	}
	rc.wireSystemTools()
	return rc
}

type wu4bGuardNetworkEdge func(*http.Request) (*http.Response, error)

func (fn wu4bGuardNetworkEdge) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

// pr-test-analyzer TC-03: the production wiring hands the boot's
// outbound-request guard to configure_provider. A cloud provider pointed at a
// loopback address is refused by that guard before any request; without the
// wiring the check would dial the address instead.
func TestWU3ProductionWiringGuardsProviderCheck(t *testing.T) {
	rc := wu3GuardedRunContext(t)
	require.NotNil(t, agent.GetSSRFChecker(rc.agentLoop), "control: the guard is on in this boot")
	inst, ok := rc.agentLoop.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	tool, ok := inst.Tools.Get("configure_provider")
	require.True(t, ok, "configure_provider is registered for Omnipus")
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "omnipus"), "default")
	lister, ok := inst.Tools.Get("list_providers")
	require.True(t, ok)
	var listed struct {
		Providers []map[string]any `json:"providers"`
	}
	listing := lister.Execute(ctx, map[string]any{"name": "openai"})
	require.NoError(t, json.Unmarshal([]byte(listing.ForLLM), &listed), listing.ForLLM)
	require.Len(t, listed.Providers, 1)
	// D2-A fixture adaptation: a prior real encrypted/bound card version is
	// selected. The production URLChecker/wireSystemTools remain untouched.
	recipient := credentials.CardRecipient{Kind: "provider_key", Provider: "openai", Endpoint: "https://127.0.0.1:1/v1", Protocol: "openai-compatible", Field: "api_key"}
	receipt, err := rc.credStore.CommitCard("wu4b-gateway-wiring-request", "wu4b-gateway-wiring-session", []credentials.CardValue{{BaseName: "opaque-wiring-ref", Recipient: recipient, Value: "sk-wu3fixwiringsecret00000000000"}}, time.Unix(1_800_000_000, 0))
	require.NoError(t, err)
	require.Len(t, receipt.References, 1)
	refsBefore, err := rc.credStore.CardSecretMetadata()
	require.NoError(t, err)
	require.Equal(t, recipient, refsBefore[receipt.References[0]].Recipient)
	var outbound atomic.Int32
	transport := http.DefaultTransport
	http.DefaultTransport = wu4bGuardNetworkEdge(func(*http.Request) (*http.Response, error) {
		outbound.Add(1)
		return nil, errors.New("unexpected outbound request: production URLChecker was bypassed")
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	result := tool.Execute(ctx, map[string]any{
		"name": "openai", "api_key_ref": receipt.References[0], "api_base": "https://127.0.0.1:1/v1",
		"expected_revision": listed.Providers[0]["revision"],
	})
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body), result.ForLLM)
	assert.Equal(t, "saved but not working", body["outcome"])
	assert.Contains(t, body["reason"], "outbound-request guard", "the wired guard refused the endpoint")
	assert.Zero(t, outbound.Load(), "real production guard rejects before the network process edge")
	assert.NotContains(t, result.ForLLM, "wu3fixwiringsecret")
	refsAfter, err := rc.credStore.CardSecretMetadata()
	require.NoError(t, err)
	assert.Equal(t, refsBefore, refsAfter, "selection must not rewrite stored values or bindings")
	value, err := rc.credStore.Get(receipt.References[0])
	require.NoError(t, err)
	assert.Equal(t, "sk-wu3fixwiringsecret00000000000", value)
	listing = lister.Execute(ctx, map[string]any{"name": "openai"})
	require.NoError(t, json.Unmarshal([]byte(listing.ForLLM), &listed), listing.ForLLM)
	require.Len(t, listed.Providers, 1)
	assert.Equal(t, receipt.References[0], listed.Providers[0]["api_key_ref"], "the saved consumer uses the prior approved bound reference")
}
