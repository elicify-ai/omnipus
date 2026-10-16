package secretrequest_test

import (
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// BDD-6.3/D1 and BDD-6.16: consent binds the server-derived recipient.
// This exercises the pre-submit revalidation seam delivered in 4a; approved
// selection and mailbox/private consumers remain named later-unit boundaries.
func TestSecretMatrix_RecipientChangesRefuseWithNoWrite(t *testing.T) {
	cases := []struct {
		name   string
		input  secretrequest.Input
		mutate func(*config.Config)
	}{
		{"provider_host", secretrequest.Input{Purpose: matrixPurpose, Provider: "openai"}, func(cfg *config.Config) { cfg.Providers[0].APIBase = "https://evil.example/v1" }},
		{"provider_scheme", secretrequest.Input{Purpose: matrixPurpose, Provider: "openai"}, func(cfg *config.Config) { cfg.Providers[0].APIBase = "http://api.openai.com/v1" }},
		{"mcp_command", secretrequest.Input{Purpose: "MCP token", Server: "notion", EnvKey: "TOKEN"}, func(cfg *config.Config) {
			s := cfg.Tools.MCP.Servers["notion"]
			s.Command = "sh"
			cfg.Tools.MCP.Servers["notion"] = s
		}},
		{"mcp_arguments", secretrequest.Input{Purpose: "MCP token", Server: "notion", EnvKey: "TOKEN"}, func(cfg *config.Config) {
			s := cfg.Tools.MCP.Servers["notion"]
			s.Args = append(s.Args, "other-account")
			cfg.Tools.MCP.Servers["notion"] = s
		}},
		{"mcp_transport", secretrequest.Input{Purpose: "MCP token", Server: "notion", EnvKey: "TOKEN"}, func(cfg *config.Config) {
			s := cfg.Tools.MCP.Servers["notion"]
			s.Type = "sse"
			cfg.Tools.MCP.Servers["notion"] = s
		}},
		{"channel_account_endpoint", secretrequest.Input{Purpose: "Telegram token", Instance: "tg-1", Field: "bot_token"}, func(cfg *config.Config) {
			c := cfg.Channels["tg-1"]
			c.BaseURL = "https://other-account.example"
			cfg.Channels["tg-1"] = c
		}},
		{"channel_deleted", secretrequest.Input{Purpose: "Telegram token", Instance: "tg-1", Field: "bot_token"}, func(cfg *config.Config) { delete(cfg.Channels, "tg-1") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newMatrixEnv(t)
			f.cfg.Providers = []*config.ModelConfig{{Provider: "openai", APIBase: matrixEndpoint}}
			f.cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{"notion": {Type: "stdio", Command: "npx", Args: []string{"notion-mcp"}}}
			f.cfg.Channels = map[string]config.ChannelInstanceConfig{"tg-1": {Type: "telegram", BaseURL: "https://api.telegram.org"}}
			rec := f.create(f.newSession("omnipus", ""), "omnipus", tc.input)
			tc.mutate(f.cfg)
			_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{rec.Card.Fields[0].Key: matrixValue}))
			requireMatrixFailure(t, err, http.StatusConflict, "refused")
			requireNoMatrixCommit(t, f, rec.Card.RequestId)
		})
	}
}

// D1 controls: an unchanged recipient and a trailing slash are the same
// destination, whereas the host/scheme rows above are not.
func TestSecretMatrix_ProviderTrailingSlashPreservesRecipientConsent(t *testing.T) {
	f := newMatrixEnv(t)
	f.cfg.Providers = []*config.ModelConfig{{Provider: "openai", APIBase: matrixEndpoint}}
	rec := f.create(f.newSession("omnipus", ""), "omnipus", secretrequest.Input{Purpose: matrixPurpose, Provider: "openai"})
	require.Equal(t, matrixEndpoint, rec.Recipients[0].Endpoint)
	f.cfg.Providers[0].APIBase = matrixEndpoint + "/"
	out := submitMatrix(t, f, rec)
	metadata, err := f.store.CardSecretMetadata()
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.Equal(t, matrixEndpoint, metadata[out.Card.ReferenceNames[0]].Recipient.Endpoint)
	require.Equal(t, "api_key", metadata[out.Card.ReferenceNames[0]].Recipient.Field)
}
