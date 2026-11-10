// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"errors"
	"reflect"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

type channelReferenceBinding struct {
	field     string
	parameter string
	target    func(*config.ChannelInstanceConfig) *string
}

// Explicit pointers keep the public/card/config spellings independent of naming
// conventions. In particular nick_serv_password selects nickserv_password_ref.
var channelReferenceBindings = []channelReferenceBinding{
	{"token", "token_ref", func(c *config.ChannelInstanceConfig) *string { return &c.TokenRef }},
	{"bot_token", "bot_token_ref", func(c *config.ChannelInstanceConfig) *string { return &c.BotTokenRef }},
	{"app_token", "app_token_ref", func(c *config.ChannelInstanceConfig) *string { return &c.AppTokenRef }},
	{"app_secret", "app_secret_ref", func(c *config.ChannelInstanceConfig) *string { return &c.AppSecretRef }},
	{"encrypt_key", "encrypt_key_ref", func(c *config.ChannelInstanceConfig) *string { return &c.EncryptKeyRef }},
	{"verification_token", "verification_token_ref", func(c *config.ChannelInstanceConfig) *string { return &c.VerificationTokenRef }},
	{"client_secret", "client_secret_ref", func(c *config.ChannelInstanceConfig) *string { return &c.ClientSecretRef }},
	{"access_token", "access_token_ref", func(c *config.ChannelInstanceConfig) *string { return &c.AccessTokenRef }},
	{"crypto_passphrase", "crypto_passphrase_ref", func(c *config.ChannelInstanceConfig) *string { return &c.CryptoPassphraseRef }},
	{"channel_secret", "channel_secret_ref", func(c *config.ChannelInstanceConfig) *string { return &c.ChannelSecretRef }},
	{"channel_access_token", "channel_access_token_ref", func(c *config.ChannelInstanceConfig) *string { return &c.ChannelAccessTokenRef }},
	{"secret", "secret_ref", func(c *config.ChannelInstanceConfig) *string { return &c.SecretRef }},
	{"password", "password_ref", func(c *config.ChannelInstanceConfig) *string { return &c.PasswordRef }},
	{"nick_serv_password", "nickserv_password_ref", func(c *config.ChannelInstanceConfig) *string { return &c.NickServPasswordRef }},
	{"sasl_password", "sasl_password_ref", func(c *config.ChannelInstanceConfig) *string { return &c.SASLPasswordRef }},
}

type channelReferenceInput struct {
	references map[string]string
	settings   map[string]string
}

func readChannelReferenceInput(args map[string]any) (channelReferenceInput, error) {
	in := channelReferenceInput{references: make(map[string]string), settings: make(map[string]string)}
	for _, binding := range channelReferenceBindings {
		if raw, supplied := args[binding.parameter]; supplied {
			ref, valid := raw.(string)
			if !valid || strings.TrimSpace(ref) == "" {
				return in, errors.New("each channel reference must be a non-empty stored reference name")
			}
			in.references[binding.parameter] = ref
		}
	}
	for _, name := range []string{"bot_id", "app_id", "mode"} {
		if raw, supplied := args[name]; supplied {
			value, valid := raw.(string)
			if !valid {
				return in, errors.New("channel settings must be strings")
			}
			if value != "" {
				in.settings[name] = value
			}
		}
	}
	return in, nil
}

func (in channelReferenceInput) apply(ch *config.ChannelInstanceConfig) {
	for _, binding := range channelReferenceBindings {
		if ref, supplied := in.references[binding.parameter]; supplied {
			*binding.target(ch) = ref
		}
	}
	for name, value := range in.settings {
		switch name {
		case "bot_id":
			ch.BotID = value
		case "app_id":
			ch.AppID = value
		case "mode":
			ch.Mode = value
		}
	}
}

func channelReferenceRecipient(id, field string, ch config.ChannelInstanceConfig) (credentials.CardRecipient, error) {
	candidate := &config.Config{Channels: map[string]config.ChannelInstanceConfig{id: ch}}
	recipients, _, _, err := secretrequest.ResolveRecipient(candidate, secretrequest.Input{Instance: id, Field: field})
	if err == nil {
		for _, recipient := range recipients {
			if recipient.Field == field {
				return recipient, nil
			}
		}
	}
	return credentials.CardRecipient{}, &referenceSelectionError{err: errors.New("channel reference recipient could not be resolved; request a secret for its exact field")}
}

func channelDestinationChanged(id string, before, after config.ChannelInstanceConfig) bool {
	// This scalar field avoids Slack's paired-token expansion while reusing
	// the one canonical whole-account fingerprint, not a second projection.
	oldRecipient, oldErr := channelReferenceRecipient(id, "app_secret", before)
	newRecipient, newErr := channelReferenceRecipient(id, "app_secret", after)
	return oldErr != nil || newErr != nil || !reflect.DeepEqual(oldRecipient, newRecipient)
}

func (t *ChannelConfigureTool) validateChannelReferences(id string, before, after config.ChannelInstanceConfig, in channelReferenceInput) error {
	changed := channelDestinationChanged(id, before, after)
	// These Settings-only fields are not selectable by the current D1 card.
	// A non-secret change still cannot redirect a retained reference in them.
	if changed && (after.WebhookURLRef != "" || after.ServiceAccountJSONRef != "") {
		return retainedChannelRecipientRefusal()
	}
	for _, binding := range channelReferenceBindings {
		ref := *binding.target(&after)
		_, supplied := in.references[binding.parameter]
		if ref == "" || (!supplied && !changed) {
			continue
		}
		recipient, err := channelReferenceRecipient(id, binding.field, after)
		if err != nil {
			return err
		}
		if err := checkSecretReference(t.deps.CredStore, ref, recipient); err != nil {
			if !supplied && changed {
				return retainedChannelRecipientRefusal()
			}
			return err
		}
	}
	return nil
}

func retainedChannelRecipientRefusal() error {
	return &referenceSelectionError{err: errors.New("channel recipient change would redirect a retained credential; change the destination in Settings, then request a secret for that recipient")}
}
