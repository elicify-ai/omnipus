package systools_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D2-A/FR-070: a model must not be offered a plaintext credential parameter.
// This tests the real tool schema, not just an unsupported Execute argument.
func TestWU4B_ThreeIntegrationSchemasOfferReferencesNotPlaintext(t *testing.T) {
	f := newWU4BFixture(t)
	cases := []struct {
		tool            tools.Tool
		refs, plaintext []string
	}{
		{systools.NewProviderConfigureTool(f.d), []string{"api_key_ref"}, []string{"api_key"}},
		{systools.NewMCPAddTool(f.d), []string{"env_refs"}, []string{"env"}},
		{systools.NewChannelConfigureTool(f.d), []string{"token_ref", "bot_token_ref", "app_token_ref", "app_secret_ref", "encrypt_key_ref", "verification_token_ref", "client_secret_ref", "access_token_ref", "crypto_passphrase_ref", "channel_secret_ref", "channel_access_token_ref", "secret_ref", "password_ref", "nickserv_password_ref", "sasl_password_ref"},
			[]string{"token", "bot_token", "app_token", "app_secret", "encrypt_key", "verification_token", "client_secret", "access_token", "crypto_passphrase", "channel_secret", "channel_access_token", "secret", "password", "nick_serv_password", "sasl_password"}},
	}
	for _, tc := range cases {
		t.Run(tc.tool.Name(), func(t *testing.T) {
			properties, ok := tc.tool.Parameters()["properties"].(map[string]any)
			require.True(t, ok, "tool parameters expose a real object schema")
			for _, ref := range tc.refs {
				if _, exists := properties[ref]; !exists {
					t.Fatalf("BLOCKED: %s.%s not implemented — required by D2-A / FR-070", tc.tool.Name(), ref)
				}
			}
			for _, field := range tc.plaintext {
				assert.NotContains(t, properties, field, "plaintext must not be advertised")
			}
		})
	}
}

func TestWU4B_ChannelRejectsEveryPlaintextCredentialFieldWithoutWriting(t *testing.T) {
	for _, field := range []string{"token", "bot_token", "app_token", "app_secret", "encrypt_key", "verification_token", "client_secret", "access_token", "crypto_passphrase", "channel_secret", "channel_access_token", "secret", "password", "nick_serv_password", "sasl_password"} {
		t.Run(field, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, "configure_channel")
			args[field] = "WU4B-PLAIN-CHANNEL-SENTINEL"
			before := f.snapshot(t)
			result := tool.Execute(context.Background(), args)
			f.unchanged(t, before)
			wu4bRefusal(t, result, "request a secret")
			assert.NotContains(t, result.ForLLM, "WU4B-PLAIN-CHANNEL-SENTINEL")
		})
	}
}

func wu4bWriteTool(f *wu4bFixture, name string) (tools.Tool, map[string]any) {
	switch name {
	case "configure_provider":
		f.cfg.Providers = []*config.ModelConfig{{Provider: "openai", Model: "fixture-model", APIBase: "https://provider.wu4b.invalid/v1", Protocol: "openai-compatible"}}
		return systools.NewProviderConfigureTool(f.d), map[string]any{"name": "openai"}
	case "add_mcp_server":
		return systools.NewMCPAddTool(f.d), map[string]any{"name": "fixture", "command": "fixture-command"}
	default:
		f.cfg.Channels["telegram"] = config.ChannelInstanceConfig{Type: "telegram", Enabled: false}
		return systools.NewChannelConfigureTool(f.d), map[string]any{"id": "telegram"}
	}
}

// BDD-6.8 applies at real registry dispatch, which performs schema validation
// before Execute. Removing plaintext from the advertised schema must not turn
// actionable request-a-secret guidance into a generic unknown-property error.
func TestWU4B_RegisteredIntegrationToolsRefusePlaintextWithSecretCardGuidance(t *testing.T) {
	for _, name := range []string{"configure_provider", "add_mcp_server", "configure_channel"} {
		t.Run(name, func(t *testing.T) {
			f := newWU4BFixture(t)
			tool, args, _ := wu4bMatchingSelection(t, f, name)
			switch name {
			case "configure_provider":
				args["api_key"] = "WU4B-REGISTERED-PLAINTEXT"
			case "add_mcp_server":
				args["env"] = map[string]any{"TOKEN": "WU4B-REGISTERED-PLAINTEXT"}
			case "configure_channel":
				args["token"] = "WU4B-REGISTERED-PLAINTEXT"
			}
			registry := tools.NewToolRegistry()
			registry.Register(tool)
			registered, exists := registry.Get(name)
			require.True(t, exists)
			require.Same(t, tool, registered, "instrument: real intended tool is registered")
			before := f.snapshot(t)
			result := registry.Execute(context.Background(), name, args)
			f.unchanged(t, before)
			assert.NotContains(t, result.ForLLM, "WU4B-REGISTERED-PLAINTEXT")
			assert.Contains(t, strings.ToLower(result.ForLLM), "request a secret", "the reachable dispatch path must retain BDD-6.8 guidance")
			wu4bRefusal(t, result, "request a secret")
		})
	}
}

// Every supplied plaintext shape is refused, never silently ignored (including
// empty/null/wrong-type inputs and plaintext mixed with references). The
// existing provider plaintext RED is retained separately, unchanged.
func TestWU4B_ThreeIntegrationToolsRefusePlaintextWithoutAnyWrite(t *testing.T) {
	for _, name := range []string{"configure_provider", "add_mcp_server", "configure_channel"} {
		for i, value := range []any{"WU4B-PLAINTEXT-SENTINEL", "", nil, 42} {
			t.Run(fmt.Sprintf("%s/shape_%d", name, i), func(t *testing.T) {
				f := newWU4BFixture(t)
				tool, args := wu4bWriteTool(f, name)
				switch name {
				case "configure_provider":
					args["api_key"] = value
				case "add_mcp_server":
					args["env"] = map[string]any{"TOKEN": value}
				case "configure_channel":
					args["token"] = value
				}
				f.persist(t)
				args = systools.WithCurrentRevisionForTest(tool, args)
				before := f.snapshot(t)
				result := tool.Execute(context.Background(), args)
				f.unchanged(t, before)
				wu4bRefusal(t, result, "request a secret")
				assert.NotContains(t, result.ForLLM, "WU4B-PLAINTEXT-SENTINEL")
			})
		}
	}
}
