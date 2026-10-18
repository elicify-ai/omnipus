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
	"sort"
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
		cfg := a.secretsConfig(r.Context())
		consumers := configReferenceConsumers(cfg)
		home := a.secretsHome()
		for _, item := range inventory {
			entries = append(entries, secretNameEntry(cfg, home, item, inventory, consumers[item.Reference]))
		}
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
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 1 || fields["available_in_channel_turns"] == nil || decoder.Decode(&update) != nil {
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
	jsonOK(w, secretNameEntry(cfg, a.secretsHome(), updated, inventory, configReferenceConsumers(cfg)[reference]))
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

func requestUsername(r *http.Request) string {
	if user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && user != nil {
		return user.Username
	}
	return ""
}

// secretNameEntry builds one K6 entry. Private versions are selected or
// superseded by the store's selection (FR-086). An integration version is
// selected when configuration references it, superseded when a newer version
// of the same destination is referenced, and stored otherwise (FR-072).
func secretNameEntry(cfg *config.Config, home string, item credentials.CardInventoryEntry, all []credentials.CardInventoryEntry, consumers []string) gen.SecretNameEntry {
	recipient := item.Secret.Recipient
	entry := gen.SecretNameEntry{
		Reference: item.Reference,
		Kind:      gen.SecretNameEntryKind(recipient.Kind),
		Version:   int64(item.Secret.Version),
		Consumers: append([]string{}, consumers...),
	}
	entry.Recipient.Kind = gen.SecretNameEntryRecipientKind(recipient.Kind)
	entry.Recipient.Lines = secretrequest.DisplayLines(cfg, home, recipient)
	if recipient.Kind == credentials.KindAgentPrivate {
		entry.Owner = &struct {
			AgentId     string `json:"agent_id"`
			WorkspaceId string `json:"workspace_id"`
		}{AgentId: recipient.AgentID, WorkspaceId: recipient.WorkspaceID}
		available := item.Options.AvailableInChannelTurns
		entry.AvailableInChannelTurns = &available
		entry.State = gen.SecretNameEntryStateSuperseded
		if item.Selected {
			entry.State = gen.SecretNameEntryStateSelected
		}
		return entry
	}
	entry.State = gen.SecretNameEntryStateStored
	if len(consumers) > 0 {
		entry.State = gen.SecretNameEntryStateSelected
		return entry
	}
	for _, other := range all {
		if other.Reference != item.Reference && other.Secret.Version > item.Secret.Version &&
			sameDestination(other.Secret.Recipient, recipient) && len(configReferenceConsumersFor(cfg, other.Reference)) > 0 {
			entry.State = gen.SecretNameEntryStateSuperseded
			break
		}
	}
	return entry
}

func sameDestination(a, b credentials.CardRecipient) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return bytes.Equal(ra, rb)
}

func configReferenceConsumersFor(cfg *config.Config, reference string) []string {
	return configReferenceConsumers(cfg)[reference]
}

// configReferenceConsumers maps every reference name the configuration uses
// to value-free labels of where it is used: any string field named *_ref and
// every value of an env_refs map, labelled by its configuration path.
func configReferenceConsumers(cfg *config.Config) map[string][]string {
	out := map[string][]string{}
	if cfg == nil {
		return out
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return out
	}
	var tree any
	if json.Unmarshal(raw, &tree) != nil {
		return out
	}
	var walk func(node any, path string, inRefs bool)
	walk = func(node any, path string, inRefs bool) {
		switch typed := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				child := typed[key]
				label := key
				if path != "" {
					label = path + "." + key
				}
				if value, ok := child.(string); ok && value != "" && (inRefs || strings.HasSuffix(key, "_ref")) {
					out[value] = append(out[value], label)
					continue
				}
				walk(child, label, key == "env_refs")
			}
		case []any:
			for i, child := range typed {
				walk(child, path+"["+strconv.Itoa(i)+"]", false)
			}
		}
	}
	walk(tree, "", false)
	return out
}
