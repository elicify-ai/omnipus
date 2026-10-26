// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

// HandleSecrets serves K6 (GET /api/v1/secrets: value-free secret listing)
// and K49 (PATCH /api/v1/secrets/{reference}/options: a private variable's
// "available in channel conversations" option, FR-241). Authentication runs
// at the route boundary. No route here resolves or returns a value.
func (a *restAPI) HandleSecrets(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/secrets")
	switch {
	case rest == "" || rest == "/":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.listSecrets(w, r)
	case strings.HasSuffix(rest, "/options") && strings.Count(rest, "/") == 2:
		if r.Method != http.MethodPatch {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.updateSecretOptions(w, r, strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/options"))
	default:
		jsonErr(w, http.StatusNotFound, "not found")
	}
}

func (a *restAPI) secretStore() (*credentials.Store, bool, error) {
	if a.agentLoop == nil {
		return nil, false, errors.New("secret store is unavailable")
	}
	return a.agentLoop.CardSecretStore()
}

func (a *restAPI) listSecrets(w http.ResponseWriter, r *http.Request) {
	store, exists, err := a.secretStore()
	if err != nil {
		jsonErr(w, http.StatusServiceUnavailable, "the secret store is unavailable")
		return
	}
	entries := []gen.SecretNameEntry{}
	if exists {
		inventory, invErr := store.CardInventory()
		if invErr != nil {
			jsonErr(w, http.StatusServiceUnavailable, "the secret store could not be read")
			return
		}
		entries = secretrequest.NameEntries(a.secretsConfig(r.Context()), a.secretsHome(), inventory)
	}
	jsonOK(w, entries)
}

func (a *restAPI) updateSecretOptions(w http.ResponseWriter, r *http.Request, reference string) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read the options update")
		return
	}
	var update gen.SecretOptionsUpdateRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var fields map[string]json.RawMessage
	// The contract's value is a non-null boolean (SecretOptionsUpdateRequest).
	// Go's decoder leaves a bool unchanged for JSON null, so null would
	// silently write false: accept only the literal tokens true and false.
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 1 || !isJSONBoolean(fields["available_in_channel_turns"]) || decoder.Decode(&update) != nil {
		jsonErr(w, http.StatusBadRequest, "send exactly {\"available_in_channel_turns\": true|false}")
		return
	}
	store, exists, err := a.secretStore()
	if err != nil {
		jsonErr(w, http.StatusServiceUnavailable, "the secret store is unavailable")
		return
	}
	if !exists {
		jsonErr(w, http.StatusNotFound, "secret reference not found")
		return
	}
	a.configMu.Lock()
	updated, err := store.SetPrivateChannelAvailability(reference, update.AvailableInChannelTurns)
	a.configMu.Unlock()
	switch {
	case errors.Is(err, credentials.ErrCardSecretNotFound):
		jsonErr(w, http.StatusNotFound, "secret reference not found")
		return
	case errors.Is(err, credentials.ErrNotPrivateSecret):
		jsonErr(w, http.StatusUnprocessableEntity, "only an agent's private variable has this option")
		return
	case err != nil:
		jsonErr(w, http.StatusServiceUnavailable, "the option could not be saved")
		return
	}
	audit.Emit(r.Context(), a.agentLoop.AuditLogger(), "secret.options.updated", audit.SeverityInfo, map[string]any{
		"reference": reference, "option": "available_in_channel_turns",
		"value": strconv.FormatBool(update.AvailableInChannelTurns), "user": requestUsername(r),
		"workspace_id": updated.Secret.Recipient.WorkspaceID, "agent_id": updated.Secret.Recipient.AgentID,
	})
	inventory, _ := store.CardInventory()
	cfg := a.secretsConfig(r.Context())
	jsonOK(w, secretrequest.NameEntry(cfg, a.secretsHome(), updated, inventory, secretrequest.ReferenceConsumers(cfg)))
}

// isJSONBoolean reports whether raw is exactly the JSON literal true or false.
func isJSONBoolean(raw json.RawMessage) bool {
	value := string(bytes.TrimSpace(raw))
	return value == "true" || value == "false"
}

func (a *restAPI) secretsConfig(ctx context.Context) *config.Config {
	if cfg := configFromContext(ctx); cfg != nil {
		return cfg
	}
	return a.agentLoop.GetConfig()
}

// secretsHome is where workspace records (display names) live.
func (a *restAPI) secretsHome() string {
	return config.OmnipusHomeDir()
}
