package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
)

// persistIntegrationProviderUpdate keeps saving a key and writing its reference
// in the same gateway critical section used by removal and check snapshots.
// The normal save's store-before-config ordering remains unchanged.
func (a *restAPI) persistIntegrationProviderUpdate(w http.ResponseWriter, def integrationDef, apiKey string, write integrationRoleWrite) bool {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if write.keySet {
		if def.credRef == "" {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("%s does not accept an API key", def.displayName))
			return false
		}
		if _, err := a.storeCredential(def.credRef, apiKey); err != nil {
			slog.Error("integrations: credential store failed", "provider", def.id, "error", err)
			jsonErr(w, http.StatusServiceUnavailable,
				"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets")
			return false
		}
		if def.kind == "search" {
			// The key changed even if the subsequent config write fails.
			a.searchChecks.bumpGeneration(def.id)
		}
	}
	if err := a.updateConfigJSONLocked(func(raw map[string]any) error {
		return applyIntegrationRoles(raw, def, write)
	}); err != nil {
		slog.Error("integrations: config update failed", "provider", def.id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to save integration config")
		return false
	}
	return true
}
