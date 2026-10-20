// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import "github.com/elicify-ai/omnipus/pkg/tools"

// RefuseArgumentNames is the registry's opt-in no-write preflight. It receives
// names only, so a rejected plaintext value cannot enter validation or logging.
func (t *ProviderConfigureTool) RefuseArgumentNames(names []string) *tools.ToolResult {
	for _, name := range names {
		if name == "api_key" {
			return plaintextSecretRefusal()
		}
	}
	return nil
}

func (t *MCPAddTool) RefuseArgumentNames(names []string) *tools.ToolResult {
	for _, name := range names {
		if name == "env" {
			return plaintextSecretRefusal()
		}
	}
	return nil
}

// RefuseArgumentNames gives connect_channel the same names-only plaintext
// refusal as configure_channel, sharing its credential-field inventory, so a
// plaintext credential argument is refused before native history, approval,
// events and the registry's argument log (gate r1 M2, security T2-S3).
func (*ChannelConnectTool) RefuseArgumentNames(names []string) *tools.ToolResult {
	return (&ChannelConfigureTool{}).RefuseArgumentNames(names)
}

func (t *ChannelConfigureTool) RefuseArgumentNames(names []string) *tools.ToolResult {
	for _, name := range names {
		switch name {
		case "token", "bot_token", "app_token", "app_secret", "encrypt_key", "verification_token",
			"client_secret", "access_token", "crypto_passphrase", "channel_secret", "channel_access_token",
			"secret", "password", "nick_serv_password", "nickserv_password", "sasl_password",
			"webhook_url", "service_account_json":
			return plaintextSecretRefusal()
		}
	}
	return nil
}

func plaintextSecretRefusal() *tools.ToolResult {
	return tools.ErrorResult(errorJSON("PLAINTEXT_SECRET", "Plaintext secrets are refused; request a secret with request_secret first",
		"Pass the returned recipient-bound reference to the configuration tool, never the value"))
}
