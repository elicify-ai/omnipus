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
	"github.com/elicify-ai/omnipus/pkg/childenv"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Input is D1-A's tool input, never a submitted value or a wire type.
type Input struct {
	Purpose  string `json:"purpose"`
	Endpoint string `json:"endpoint,omitempty"`
	Provider string `json:"provider,omitempty"`
	Server   string `json:"server,omitempty"`
	EnvKey   string `json:"env_key,omitempty"`
	Instance string `json:"instance,omitempty"`
	Field    string `json:"field,omitempty"`
	// Variable selects the agent-private kind. WorkspaceID and AgentID are
	// its owner pair; Registry.Create sets them from the trusted context (or
	// Omnipus's validated target), never from an ordinary agent's input.
	Variable    string `json:"variable,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
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
	if in.Variable != "" {
		forms++
	}
	if cfg == nil || forms != 1 || (in.Endpoint != "" && in.Provider != "") {
		return nil, nil, gen.SecretRecipientDisplay{}, errors.New("request_secret needs exactly one recipient: endpoint or provider, server + env_key, instance + field, or a private variable")
	}
	switch {
	case in.Variable != "":
		if err := childenv.CheckPrivateVariableName(in.Variable); err != nil {
			return nil, nil, gen.SecretRecipientDisplay{}, err
		}
		if in.WorkspaceID == "" || in.AgentID == "" {
			return nil, nil, gen.SecretRecipientDisplay{}, errors.New("a private variable belongs to one agent in one workspace; ask from that agent's chat in the workspace")
		}
		recipient = credentials.CardRecipient{Kind: credentials.KindAgentPrivate, WorkspaceID: in.WorkspaceID, AgentID: in.AgentID, Field: in.Variable}
		return []credentials.CardRecipient{recipient}, []gen.SecretRequestField{{Key: "value", Label: in.Variable}}, gen.SecretRecipientDisplay{Kind: recipient.Kind, Lines: recipientLines(recipient)}, nil
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
		// The ONE canonical channel recipient descriptor, used by card
		// creation and by every reference validator. Founder ruling
		// 2026-10-20 (Train 2 gate r1 M5, option A): a channel card's
		// consent scope is the platform account, its settings and the
		// credential field. Binding (WorkspaceID, Identity) and Enabled are
		// routing, governed by the person's approval of the connect_channel
		// step, so they are excluded here. Every other non-credential
		// setting is included: changing an account, host or endpoint while
		// the card is open invalidates consent.
		projection := channel
		projection.WorkspaceID = ""
		projection.Identity = nil
		projection.Enabled = false
		rv := reflect.ValueOf(&projection).Elem()
		for i := 0; i < rv.NumField(); i++ {
			if strings.HasSuffix(rv.Type().Field(i).Name, "Ref") {
				rv.Field(i).Set(reflect.Zero(rv.Field(i).Type()))
			}
		}
		raw, err := json.Marshal(projection)
		if err != nil {
			return nil, nil, gen.SecretRecipientDisplay{}, errors.New("could not describe channel recipient")
		}
		hash := sha256.Sum256(raw)
		recipient = credentials.CardRecipient{Kind: "channel_credential", Instance: in.Instance, ChannelType: channel.Type, Account: hex.EncodeToString(hash[:]), Field: in.Field}
	default:
		return nil, nil, gen.SecretRecipientDisplay{}, errors.New("recipient is incomplete")
	}
	key, label := "api_key", "API key"
	lines := recipientLines(recipient)
	if recipient.Kind == "mcp_env" {
		key, label = "value", recipient.Field
	}
	if recipient.Kind == "channel_credential" {
		key, label = recipient.Field, strings.ReplaceAll(recipient.Field, "_", " ")
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

// recipientLines are K2's display lines for one integration recipient. The
// agent-private kind is composed by DisplayLines, which knows display names.
func recipientLines(r credentials.CardRecipient) []string {
	switch r.Kind {
	case "mcp_env":
		return []string{"MCP server: " + r.Server, "Transport: " + r.Transport, "Command: " + r.Command, "Arguments: " + strings.Join(r.Args, " "), "Environment variable: " + r.Field}
	case "channel_credential":
		return []string{"Channel instance: " + r.Instance, "Channel type: " + r.ChannelType, "Field: " + r.Field}
	case credentials.KindAgentPrivate:
		return []string{r.AgentID + " in " + r.WorkspaceID, "Variable: " + r.Field}
	}
	return []string{r.Endpoint, "Field: " + r.Field}
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
	case credentials.KindAgentPrivate:
		// The digest keeps two pairs whose ids differ only in characters the
		// sanitizer folds from sharing one version family.
		h := sha256.Sum256([]byte(r.WorkspaceID + "\x00" + r.AgentID + "\x00" + r.Field))
		base = "private_" + r.Field + "_" + hex.EncodeToString(h[:6])
	}
	return referencePartPattern.ReplaceAllString(strings.ToLower(base), "_")
}
