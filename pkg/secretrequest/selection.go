// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"errors"
	"reflect"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// ValidateReference permits selection only when a readable stored version was
// consented to for this exact recipient. A plausible name is never authority.
// The caller holds the shared configuration write lock while deriving want.
func ValidateReference(store *credentials.Store, ref string, want credentials.CardRecipient) error {
	if store == nil || ref == "" {
		return errors.New("credential reference is unavailable; request a secret for this recipient")
	}
	metadata, err := store.CardSecretMetadata()
	if err != nil {
		return errors.New("credential reference metadata could not be read; nothing was selected")
	}
	bound, exists := metadata[ref]
	if !exists || bound.Version < 1 || !sameSelectionRecipient(bound.Recipient, want) {
		return errors.New("credential reference was not stored for this exact recipient; request a secret for this recipient")
	}
	value, err := store.Get(ref)
	if err != nil || value == "" {
		return errors.New("credential reference value is unavailable; nothing was selected")
	}
	return nil
}

func sameSelectionRecipient(bound, want credentials.CardRecipient) bool {
	switch want.Kind {
	case "provider_key":
		endpoint, err := NormalizeEndpoint(want.Endpoint)
		if err != nil {
			return false
		}
		storedEndpoint, err := NormalizeEndpoint(bound.Endpoint)
		if err != nil {
			return false
		}
		bound.Endpoint, want.Endpoint = storedEndpoint, endpoint
		// D1-A: an arbitrary endpoint-only card records exactly its endpoint
		// and field, without inventing a provider/protocol identity. A known
		// catalog endpoint records both and must match both on selection.
		if bound.Provider == "" && bound.Protocol == "" {
			bound.Provider, bound.Protocol = want.Provider, want.Protocol
		}
	case "mcp_env", "channel_credential":
	default:
		return false
	}
	return reflect.DeepEqual(bound, want)
}
