// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Input is D1-A's tool input, never a submitted value or a wire type.
type Input struct {
	Purpose  string
	Endpoint string
	Provider string
	Server   string
	EnvKey   string
	Instance string
	Field    string
}

var envKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
var referencePartPattern = regexp.MustCompile(`[^a-z0-9_-]+`)

// ResolveRecipient is shared by card construction and selection. Only the
// server chooses the field keys, labels and concrete destination descriptor.
func ResolveRecipient(cfg *config.Config, in Input) ([]credentials.CardRecipient, []gen.SecretRequestField, gen.SecretRecipientDisplay, error) {
	var recipient credentials.CardRecipient
	forms := 0
	if in.Endpoint != "" || in.Provider != "" {
		forms++
	}
	if in.Server != "" || in.EnvKey != "" {
		forms++
	}
	if in.Instance != "" || in.Field != "" {
		forms++
	}
	if cfg == nil || forms != 1 || (in.Endpoint != "" && in.Provider != "") {
		return nil, nil, gen.SecretRecipientDisplay{}, errors.New("request_secret needs exactly one recipient: endpoint or provider, server + env_key, or instance + field")
	}
	switch {
	case in.Endpoint != "" || in.Provider != "":
		var err error
		recipient, err = resolveProvider(cfg, in)
		if err != nil {
			return nil, nil, gen.SecretRecipientDisplay{}, err
		}
	case in.Server != "":
		server, ok := cfg.Tools.MCP.Servers[in.Server]
		if !ok || !envKeyPattern.MatchString(in.EnvKey) {
			return nil, nil, gen.SecretRecipientDisplay{}, errors.New("choose an existing MCP server and an uppercase environment variable")
		}
		transport := server.Type
		if transport == "" {
			if server.Command != "" {
				transport = "stdio"
			} else {
				transport = "sse"
			}
		}
		if transport != "stdio" {
			return nil, nil, gen.SecretRecipientDisplay{}, errors.New("MCP environment secrets require a stdio server")
		}
		recipient = credentials.CardRecipient{Kind: "mcp_env", Server: in.Server, Transport: transport, Command: server.Command, Args: append([]string(nil), server.Args...), URL: server.URL, Field: in.EnvKey}
	case in.Instance != "":
		channel, ok := cfg.Channels[in.Instance]
		if !ok || !channelCredentialField(in.Field) {
			return nil, nil, gen.SecretRecipientDisplay{}, errors.New("choose an existing channel instance and a credential field")
		}
		// The fingerprint contains every non-credential setting. Changing an
		// account, host or endpoint while the card is open invalidates consent.
		copy := channel
		rv := reflect.ValueOf(&copy).Elem()
		for i := 0; i < rv.NumField(); i++ {
			if strings.HasSuffix(rv.Type().Field(i).Name, "Ref") {
				rv.Field(i).Set(reflect.Zero(rv.Field(i).Type()))
			}
		}
		raw, err := json.Marshal(copy)
		if err != nil {
			return nil, nil, gen.SecretRecipientDisplay{}, errors.New("could not describe channel recipient")
		}
		hash := sha256.Sum256(raw)
		recipient = credentials.CardRecipient{Kind: "channel_credential", Instance: in.Instance, ChannelType: channel.Type, Account: hex.EncodeToString(hash[:]), Field: in.Field}
	default:
		return nil, nil, gen.SecretRecipientDisplay{}, errors.New("recipient is incomplete")
	}
	key, label := "api_key", "API key"
	lines := []string{recipient.Endpoint, "Field: api_key"}
	if recipient.Kind == "mcp_env" {
		key, label = "value", recipient.Field
		lines = []string{"MCP server: " + recipient.Server, "Transport: " + recipient.Transport, "Command: " + recipient.Command, "Arguments: " + strings.Join(recipient.Args, " "), "Environment variable: " + recipient.Field}
	}
	if recipient.Kind == "channel_credential" {
		key, label = recipient.Field, strings.ReplaceAll(recipient.Field, "_", " ")
		lines = []string{"Channel instance: " + recipient.Instance, "Channel type: " + recipient.ChannelType, "Field: " + recipient.Field}
	}
	if recipient.Kind == "channel_credential" && recipient.ChannelType == "slack" && (recipient.Field == "token" || recipient.Field == "bot_token" || recipient.Field == "app_token") {
		// Chief ruling 2026-10-16: the server expands Slack's mandatory
		// credential pair; no agent-supplied field list exists (BDD-6.9).
		bot, app := recipient, recipient
		bot.Field, app.Field = "bot_token", "app_token"
		fields := []gen.SecretRequestField{{Key: "bot_token", Label: "Bot token"}, {Key: "app_token", Label: "App token"}}
		lines = []string{"Channel instance: " + recipient.Instance, "Channel type: slack", "Fields: bot_token, app_token"}
		return []credentials.CardRecipient{bot, app}, fields, gen.SecretRecipientDisplay{Kind: recipient.Kind, Lines: lines}, nil
	}
	return []credentials.CardRecipient{recipient}, []gen.SecretRequestField{{Key: key, Label: label}}, gen.SecretRecipientDisplay{Kind: recipient.Kind, Lines: lines}, nil
}

func resolveProvider(cfg *config.Config, in Input) (credentials.CardRecipient, error) {
	r := credentials.CardRecipient{Kind: "provider_key", Field: "api_key", Provider: in.Provider}
	if in.Endpoint != "" {
		endpoint, err := NormalizeEndpoint(in.Endpoint)
		if err != nil {
			return r, err
		}
		r.Endpoint = endpoint
		if doc := providers.ProviderCatalog().Document(); doc != nil {
			for _, row := range doc.Providers {
				known, parseErr := NormalizeEndpoint(row.API)
				if parseErr == nil && known == endpoint {
					r.Provider, r.Protocol = row.ID, string(row.Protocol)
					break
				}
			}
		}
		return r, nil
	}
	row := &config.ModelConfig{Provider: in.Provider}
	for _, candidate := range cfg.Providers {
		if candidate != nil && candidate.Provider == in.Provider {
			row = candidate
			break
		}
	}
	ep, err := providers.ResolveEndpoint(row)
	if err != nil {
		return r, errors.New("provider endpoint could not be resolved; choose a configured provider or an endpoint")
	}
	r.Endpoint, err = NormalizeEndpoint(ep.API)
	r.Protocol = string(ep.Protocol)
	return r, err
}

// NormalizeEndpoint refuses URL credentials and fragments: neither belongs on
// a value-free destination card. Paths and query strings remain exact.
func NormalizeEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return "", errors.New("endpoint must be an http or https URL without credentials, query or fragment")
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), nil
}

func channelCredentialField(field string) bool {
	switch field {
	case "token", "bot_token", "app_token", "app_secret", "encrypt_key", "verification_token", "client_secret", "access_token", "crypto_passphrase", "channel_secret", "channel_access_token", "secret", "password", "nick_serv_password", "sasl_password":
		return true
	}
	return false
}

// ReferenceBase is a server-chosen version family, never a caller-provided
// credential-store key. Endpoint-only cards use a stable endpoint digest.
func ReferenceBase(r credentials.CardRecipient) string {
	base := r.Provider + "_api_key"
	switch r.Kind {
	case "provider_key":
		if r.Provider == "" {
			h := sha256.Sum256([]byte(r.Endpoint))
			base = "endpoint_" + hex.EncodeToString(h[:8]) + "_api_key"
		}
	case "mcp_env":
		base = "mcp_" + r.Server + "_" + r.Field
	case "channel_credential":
		base = r.ChannelType + "_" + r.Instance + "_" + r.Field
	}
	return referencePartPattern.ReplaceAllString(strings.ToLower(base), "_")
}
