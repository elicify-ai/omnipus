package systools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WU-4b oracles: FR-064/065/067/070/085/088, BDD-6.14/11.9,
// chief D2-A/Q1-A/Q2-A and architect CONFIRM-A. The written RED plan predates
// test output. Only network/runtime/lock process edges are doubled. Real
// Execute, config persistence, revision gates, recipient metadata and encrypted
// credentials remain inside the test boundary. No production mutation here.
type wu4bFixture struct {
	d                                              *systools.Deps
	cfg                                            *config.Config
	configHeld, loopHeld                           bool
	saves, reloads, reconciles, listings, sequence int
	events                                         []string
}

func newWU4BFixture(t *testing.T) *wu4bFixture {
	t.Helper()
	d, cfg := wu3Deps(t)
	t.Cleanup(d.CredStore.Close)
	cfg.Agents.List = []config.AgentConfig{{ID: "omnipus", Name: "Omnipus", Type: config.AgentTypeCore, Tools: &config.AgentToolsCfg{}}}
	cfg.Channels = map[string]config.ChannelInstanceConfig{}
	require.NoError(t, d.CredStore.Set("unrelated-control", "WU4B-CONTROL-VALUE"))
	f := &wu4bFixture{d: d, cfg: cfg}
	d.LockConfigWrites = func() func() {
		require.False(t, f.configHeld, "config lock cannot be recursively acquired")
		f.configHeld = true
		f.events = append(f.events, "config-lock")
		return func() { f.configHeld = false; f.events = append(f.events, "config-unlock") }
	}
	mutate := d.MutateConfig
	d.MutateConfig = func(fn func(*config.Config) error) error {
		require.True(t, f.configHeld, "FR-088: config lock must precede agent-loop lock")
		f.loopHeld = true
		f.events = append(f.events, "loop-lock")
		defer func() { f.loopHeld = false; f.events = append(f.events, "loop-unlock") }()
		return mutate(fn)
	}
	save := d.SaveConfigLocked
	d.SaveConfigLocked = func(c *config.Config) error {
		require.True(t, f.configHeld)
		require.True(t, f.loopHeld)
		f.saves++
		f.events = append(f.events, "save")
		return save(c)
	}
	d.ReloadFunc = func() error { f.unlocked(t); f.reloads++; return nil }
	d.ReconcileMCP = func(context.Context) error { f.unlocked(t); f.reconciles++; return nil }
	d.MCPStatus = func(string) (string, int, string) { return "connected", 2, "" }
	d.ListProviderModels = func(context.Context, catalog.Protocol, string, string, providers.URLChecker) ([]string, error) {
		f.unlocked(t)
		f.listings++
		return []string{"fixture-model"}, nil
	}
	f.persist(t)
	return f
}

func (f *wu4bFixture) unlocked(t *testing.T) {
	t.Helper()
	require.False(t, f.configHeld, "runtime callback must follow config lock release")
	require.False(t, f.loopHeld, "runtime callback must follow agent-loop lock release")
}

func (f *wu4bFixture) persist(t *testing.T) {
	t.Helper()
	require.NoError(t, config.SaveConfig(f.d.ConfigPath, f.cfg))
}

// Literal recipients in the mismatch matrices are independent of the tool's
// comparator. Deliberately opaque reference names prove names are not authority.
func (f *wu4bFixture) bound(t *testing.T, recipient credentials.CardRecipient, value string) string {
	t.Helper()
	f.sequence++
	receipt, err := f.d.CredStore.CommitCard(fmt.Sprintf("wu4b-request-%d", f.sequence), "wu4b-session",
		[]credentials.CardValue{{BaseName: "opaque-test-family", Recipient: recipient, Value: value}}, time.Unix(1_800_000_000, 0))
	require.NoError(t, err)
	require.Len(t, receipt.References, 1)
	metadata, err := f.d.CredStore.CardSecretMetadata()
	require.NoError(t, err)
	require.Equal(t, recipient, metadata[receipt.References[0]].Recipient)
	return receipt.References[0]
}

type wu4bSnapshot struct {
	memory, config, credentials          []byte
	saves, reloads, reconciles, listings int
}

func (f *wu4bFixture) snapshot(t *testing.T) wu4bSnapshot {
	t.Helper()
	raw, err := json.Marshal(f.cfg)
	require.NoError(t, err)
	return wu4bSnapshot{memory: raw, config: wu3ReadFile(t, f.d.ConfigPath),
		credentials: wu3ReadFile(t, filepath.Join(f.d.Home, "credentials.json")),
		saves:       f.saves, reloads: f.reloads, reconciles: f.reconciles, listings: f.listings}
}

func (f *wu4bFixture) unchanged(t *testing.T, before wu4bSnapshot) {
	t.Helper()
	after := f.snapshot(t)
	assert.Zero(t, bytes.Compare(before.memory, after.memory), "live consumer/config bytes must remain exactly unchanged")
	assert.Zero(t, bytes.Compare(before.config, after.config), "persisted config bytes must remain exactly unchanged")
	assert.Zero(t, bytes.Compare(before.credentials, after.credentials), "encrypted store bytes (values/bindings/receipts) must remain exactly unchanged")
	assert.Equal(t, before.saves, after.saves, "no config save on refusal/no-op")
	assert.Equal(t, before.reloads, after.reloads, "no reload on refusal/no-op")
	assert.Equal(t, before.reconciles, after.reconciles, "no reconciliation on refusal/no-op")
	assert.Equal(t, before.listings, after.listings, "no provider request on refusal/no-op")
}

func wu4bRefusal(t *testing.T, result *tools.ToolResult, reason string) {
	t.Helper()
	require.NotNil(t, result)
	require.True(t, result.IsError, "reference/plaintext refusal must be an error: %s", result.ForLLM)
	body := wu3Outcome(t, result, "failed: nothing saved")
	assert.Equal(t, []any{}, body["changed_fields"], "this call published no durable part")
	assert.Contains(t, strings.ToLower(result.ForLLM), reason, "meaningful reference/recipient/request-a-secret reason; Q2-A leaves unprescribed codes to implementer")
}

func wu4bMatchingSelection(t *testing.T, f *wu4bFixture, name string) (tools.Tool, map[string]any, string) {
	t.Helper()
	var tool tools.Tool
	var args map[string]any
	var recipient credentials.CardRecipient
	switch name {
	case "configure_provider":
		f.cfg.Providers = []*config.ModelConfig{{Provider: "openai", Model: "fixture-model", APIBase: "https://provider.wu4b.invalid/v1", Protocol: "openai-compatible"}}
		recipient = credentials.CardRecipient{Kind: "provider_key", Provider: "openai", Endpoint: "https://provider.wu4b.invalid/v1", Protocol: "openai-compatible", Field: "api_key"}
		tool = systools.NewProviderConfigureTool(f.d)
		args = map[string]any{"name": "openai"}
	case "add_mcp_server":
		f.cfg.Tools.MCP.Servers["fixture"] = config.MCPServerConfig{Type: "stdio", Command: "fixture-command", Args: []string{"--read-only"}, Enabled: true}
		recipient = credentials.CardRecipient{Kind: "mcp_env", Server: "fixture", Transport: "stdio", Command: "fixture-command", Args: []string{"--read-only"}, Field: "TOKEN"}
		tool = systools.NewMCPAddTool(f.d)
		args = map[string]any{"name": "fixture"}
	case "configure_channel":
		f.cfg.Channels["telegram"] = config.ChannelInstanceConfig{Type: "telegram", Enabled: false, BotID: "fixture-bot"}
		recipient = wu4bChannelRecipient(t, f, "telegram", "token")
		tool = systools.NewChannelConfigureTool(f.d)
		args = map[string]any{"id": "telegram"}
	default:
		t.Fatalf("unlisted WU-4b tool %s", name)
	}
	ref := f.bound(t, recipient, "WU4B-SELECTED-VALUE")
	switch name {
	case "configure_provider":
		args["api_key_ref"] = ref
	case "add_mcp_server":
		args["env_refs"] = map[string]any{"TOKEN": ref}
	case "configure_channel":
		args["token_ref"] = ref
	}
	f.persist(t)
	return tool, systools.WithCurrentRevisionForTest(tool, args), ref
}

func wu4bChannelRecipient(t *testing.T, f *wu4bFixture, id, field string) credentials.CardRecipient {
	t.Helper()
	recipients, _, _, err := secretrequest.ResolveRecipient(f.cfg, secretrequest.Input{Instance: id, Field: field})
	require.NoError(t, err)
	require.Len(t, recipients, 1)
	require.Equal(t, "channel_credential", recipients[0].Kind)
	require.Equal(t, id, recipients[0].Instance)
	require.Equal(t, field, recipients[0].Field)
	return recipients[0]
}
