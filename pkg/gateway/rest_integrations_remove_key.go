package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"syscall"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

const (
	removeKeyUndecided = "Choose your default and fallback search services before removing this key."
	removeKeyShared    = "This key is also used elsewhere. Give those connections their own key, then try again."
	removeKeyCustom    = "This service uses a key set outside Settings. Remove it from the configuration first."
)

// integrationChangeError carries only local, fixed user-facing text. Neither
// credential errors nor upstream responses are allowed onto this error path.
type integrationChangeError struct {
	status  int
	message string
}

func (e *integrationChangeError) Error() string { return e.message }

func (a *restAPI) handleSearchKeyRemoval(w http.ResponseWriter, r *http.Request, def integrationDef, body gen.IntegrationProviderUpdateRequest) {
	if def.kind != "search" || !def.requiresKey {
		jsonErr(w, http.StatusBadRequest, "only keyed search services have a saved key to remove")
		return
	}
	if body.ApiKey != nil || body.Active != nil || body.Fallback != nil {
		jsonErr(w, http.StatusBadRequest, "clear_api_key cannot be combined with api_key, active or fallback")
		return
	}
	searchDef, ok := config.SearchProviderDefByID(def.id)
	if !ok {
		jsonErr(w, http.StatusBadRequest, "unknown search service")
		return
	}

	state := searchKeyRemovalState{}
	a.configMu.Lock()
	// Lock order is gateway configMu -> loop config lock -> credential store.
	// Sysagent writers already use the loop lock. No load/SwapConfig may run
	// inside MutateConfig: those acquire the same non-reentrant loop lock.
	out, err := a.persistSearchKeyRemoval(searchDef, &state)
	if err == nil {
		// Even a deletion/environment failure must attempt to load the disabled
		// state. The durable config write is not rolled back across files.
		if applyErr := a.applyWrittenConfigLocked(out); applyErr != nil {
			logIntegrationChangeFailure(def.id, "config_apply", applyErr)
			state.failedStages = append(state.failedStages, "config_apply")
		}
	}
	a.configMu.Unlock()
	if err != nil {
		writeIntegrationChangeError(w, err, "Could not save the disabled configuration. The saved key was not removed.", def.id, "config_write")
		return
	}

	// The production reload itself needs configMu; never wait while holding it.
	confirmed, reloadErr := a.triggerReloadAndWaitOutcome()
	a.configMu.Lock()
	defer a.configMu.Unlock()
	post := a.agentLoop.GetConfig()
	state.enabled = searchDef.Enabled(&post.Tools.Web)
	state.runtimeDisabled = confirmed && reloadErr == nil && !state.enabled &&
		searchDef.APIKeyRef(&post.Tools.Web) == "" && !post.Tools.Web.UsableSearchProvider(def.id)
	if !state.runtimeDisabled {
		if reloadErr != nil {
			logIntegrationChangeFailure(def.id, "reload", reloadErr)
		}
		state.failedStages = append(state.failedStages, "reload")
	}
	if err := a.auditSearchKeyRemoval(r, def, state); err != nil {
		logIntegrationChangeFailure(def.id, "audit", err)
		state.failedStages = append(state.failedStages, "audit")
	}
	if len(state.failedStages) != 0 {
		jsonErr(w, http.StatusInternalServerError, state.failureMessage())
		return
	}
	a.writeIntegrationResponse(w, post)
}

// searchKeyRemovalState records completed stages, not an invented cross-file
// transaction. It never retains a secret or a free-form error.
type searchKeyRemovalState struct {
	keyRemoved      bool
	enabled         bool
	runtimeDisabled bool
	failedStages    []string
}

func (a *restAPI) persistSearchKeyRemoval(def config.SearchProviderDef, state *searchKeyRemovalState) ([]byte, error) {
	var out []byte
	err := a.agentLoop.MutateConfig(func(cfg *config.Config) error {
		written, err := a.writeConfigJSONLocked(func(raw map[string]any) error {
			if err := a.preflightSearchKeyRemoval(raw, cfg, def); err != nil {
				return err
			}
			section := ensureMap(raw, "tools", "web", def.Section)
			section["enabled"] = false
			section["api_key_ref"] = "" // Explicit: defaults must not restore it.
			// Publish the same web configuration to sysagent writers before
			// releasing the loop lock. Only the raw map is persisted, so absent
			// roles and the migration marker keep their original file shape.
			webBytes, err := json.Marshal(ensureMap(raw, "tools", "web"))
			if err != nil {
				return err
			}
			return json.Unmarshal(webBytes, &cfg.Tools.Web)
		})
		if err != nil {
			return err
		}
		out = written
		if err := a.removeStoredCredential(def.CredRef); err != nil {
			logIntegrationChangeFailure(def.ID, "credential_delete", err)
			state.failedStages = append(state.failedStages, "credential_delete")
		} else {
			state.keyRemoved = true
		}
		if err := os.Unsetenv(def.CredRef); err != nil {
			logIntegrationChangeFailure(def.ID, "environment_clear", err)
			state.failedStages = append(state.failedStages, "environment_clear")
		}
		return nil // Publish the persisted disabled state, including on partial failure.
	})
	return out, err
}

func (a *restAPI) preflightSearchKeyRemoval(raw map[string]any, cfg *config.Config, def config.SearchProviderDef) error {
	web := ensureMap(raw, "tools", "web")
	if webRoleString(web, "roles_migrated_at") == "" {
		return &integrationChangeError{http.StatusConflict, removeKeyUndecided}
	}
	section := ensureMap(web, def.Section)
	ref, _ := section["api_key_ref"].(string)
	liveRef := def.APIKeyRef(&cfg.Tools.Web)
	if (ref != "" && ref != def.CredRef) || (liveRef != "" && liveRef != def.CredRef) {
		return &integrationChangeError{http.StatusConflict, removeKeyCustom}
	}
	omit := []string{"tools", "web", def.Section, "api_key_ref"}
	if credentialReferenceUsedElsewhere(raw, def.CredRef, omit, nil) {
		return &integrationChangeError{http.StatusConflict, removeKeyShared}
	}
	live, err := json.Marshal(cfg)
	if err != nil {
		logIntegrationChangeFailure(def.ID, "config_inspect", err)
		return &integrationChangeError{http.StatusInternalServerError, "Could not inspect the current configuration."}
	}
	var liveRaw map[string]any
	if decodeErr := json.Unmarshal(live, &liveRaw); decodeErr != nil {
		logIntegrationChangeFailure(def.ID, "config_inspect", decodeErr)
		return &integrationChangeError{http.StatusInternalServerError, "Could not inspect the current configuration."}
	}
	if credentialReferenceUsedElsewhere(liveRaw, def.CredRef, omit, nil) {
		return &integrationChangeError{http.StatusConflict, removeKeyShared}
	}
	// Get authenticates the entry before any write. Delete alone cannot
	// distinguish a corrupt/wrong-key entry from a valid encrypted entry.
	_, err = a.savedIntegrationCredential(def.CredRef)
	var missing *credentials.NotFoundError
	if err != nil && !errors.As(err, &missing) {
		logIntegrationChangeFailure(def.ID, "credential_read", err)
		return &integrationChangeError{http.StatusServiceUnavailable, "Could not read the saved key from the credential store. Unlock or repair it, then try again."}
	}
	return nil
}

// credentialReferenceUsedElsewhere covers nested models, channels, mailboxes,
// voice and marketplaces via *_ref fields, and MCP's env_refs map. It checks
// both persisted and live config so a removed/unpublished reference cannot be
// mistaken for exclusive ownership. Only the addressed search ref is omitted.
func credentialReferenceUsedElsewhere(value any, ref string, omit, path []string) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			childPath := append(slices.Clone(path), key)
			if slices.Equal(childPath, omit) {
				continue
			}
			if strings.HasSuffix(key, "_ref") {
				if name, ok := child.(string); ok && name == ref {
					return true
				}
			}
			if key == "env_refs" {
				if refs, ok := child.(map[string]any); ok {
					for _, name := range refs {
						if name == ref {
							return true
						}
					}
				}
			}
			if credentialReferenceUsedElsewhere(child, ref, omit, childPath) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if credentialReferenceUsedElsewhere(child, ref, omit, append(slices.Clone(path), "[]")) {
				return true
			}
		}
	}
	return false
}

func (a *restAPI) savedIntegrationCredential(ref string) (string, error) {
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return "", err
		}
		defer store.Close()
	}
	return store.Get(ref)
}

func (a *restAPI) auditSearchKeyRemoval(r *http.Request, def integrationDef, state searchKeyRemovalState) error {
	logger := a.agentLoop.AuditLogger()
	if logger == nil {
		return errors.New("audit logger unavailable")
	}
	values := map[string]any{
		"provider": def.id, "kind": def.kind, "action": "remove_key",
		"key_removed": state.keyRemoved, "roles_changed": false,
		"enabled": state.enabled, "outcome": "removed",
	}
	if len(state.failedStages) != 0 {
		values["outcome"] = "partial_failure"
		values["failed_stage"] = state.failedStages[0]
	}
	return audit.EmitSecuritySettingChangeChecked(r.Context(), logger, "integrations.provider",
		map[string]any{"provider": def.id}, values)
}

func (state searchKeyRemovalState) failureMessage() string {
	message := "Could not remove the saved key."
	if state.keyRemoved {
		message = "The saved key was removed."
	}
	if state.runtimeDisabled {
		message += " The service has been switched off."
	} else {
		message += " The disabled configuration was saved, but config reload and runtime switching-off were not confirmed."
	}
	return message + " Failed stages: " + strings.Join(state.failedStages, ", ") + ". Try again."
}

func writeIntegrationChangeError(w http.ResponseWriter, err error, fallback, service, stage string) {
	var change *integrationChangeError
	if errors.As(err, &change) {
		jsonErr(w, change.status, change.message)
		return
	}
	logIntegrationChangeFailure(service, stage, err)
	jsonErr(w, http.StatusInternalServerError, fallback)
}

// Log only typed, non-secret causes. Free-form error text and filesystem paths
// may contain sensitive configuration, so they never reach this boundary's log.
func logIntegrationChangeFailure(service, stage string, err error) {
	cause := fmt.Sprintf("%T", err)
	var errno syscall.Errno
	switch {
	case errors.Is(err, credentials.ErrStoreLocked):
		cause = "credential_store_locked"
	case errors.Is(err, credentials.ErrWrongKey):
		cause = "credential_authentication_failed"
	case errors.Is(err, os.ErrPermission):
		cause = "permission_denied"
	case errors.As(err, &errno):
		cause = errno.Error()
	}
	slog.Error("integration change failed", "service", service, "stage", stage, "cause", cause)
}
