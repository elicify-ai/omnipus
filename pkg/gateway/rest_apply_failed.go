// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_apply_failed.go — the in-memory list of hot-reloaded config keys whose
// last save reached config.json but did not apply to the running gateway
// (Settings redesign #1519, C-2 addition / FR-062, #995 / FR-102).
//
// GET /api/v1/config/pending-restart lists each key with reason
// "apply_failed"; its persisted_value is read from config.json at request
// time, never from this list. An entry is removed by a later successful apply
// of the key (for a config refresh: any later successful refresh, which loads
// the whole file) or by a restart (the list lives in memory only).
//
// Writers record INSIDE the config write lock, before the response is sent,
// so a lost response can never leave a saved, unapplied key unlisted.

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"sync"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// applyFailedSource names which apply path failed, so a later success of the
// same path clears exactly the entries it now applied.
type applyFailedSource string

const (
	// applyFailedSourceRefresh: the live config refresh after a config.json
	// write failed (configRefreshError, or a failed registry reload).
	applyFailedSourceRefresh applyFailedSource = "config_refresh"
	// applyFailedSourcePromptGuard: the prompt-guard hot reload failed.
	applyFailedSourcePromptGuard applyFailedSource = "prompt_guard"
	// applyFailedSourceRateLimits: the rate-limit hot apply failed.
	applyFailedSourceRateLimits applyFailedSource = "rate_limits"
)

// applyFailedRegistry is the zero-value-usable set of failed-apply keys.
type applyFailedRegistry struct {
	mu      sync.Mutex
	entries map[string]applyFailedSource
}

// record adds keys whose last save did not apply through source.
func (r *applyFailedRegistry) record(source applyFailedSource, keys []string) {
	if len(keys) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]applyFailedSource)
	}
	for _, k := range keys {
		r.entries[k] = source
	}
}

// clearSource removes every entry the now-successful source applied.
func (r *applyFailedRegistry) clearSource(source applyFailedSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, s := range r.entries {
		if s == source {
			delete(r.entries, k)
		}
	}
}

// clearKeys removes the given keys, whatever recorded them.
func (r *applyFailedRegistry) clearKeys(keys []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range keys {
		delete(r.entries, k)
	}
}

// keys returns the listed keys, sorted.
func (r *applyFailedRegistry) keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.entries))
	for k := range r.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// savedNotRefreshedText is the 500 text for a write that reached config.json
// whose live refresh failed (C-6 outcome table).
const (
	savedNotRefreshedCode = "saved_not_refreshed"
	savedNotRefreshedText = "the change was saved, but it is not in use yet; save again or restart the gateway to apply it"
)

// writeSavedNotRefreshed answers 500 ErrorResponse with code
// saved_not_refreshed and details.keys (FR-102).
func writeSavedNotRefreshed(w http.ResponseWriter, text string, keys []string) {
	if keys == nil {
		keys = []string{}
	}
	code := savedNotRefreshedCode
	details := map[string]any{"keys": keys}
	writeJSON(w, http.StatusInternalServerError, gen.ErrorResponse{Error: text, Code: &code, Details: &details})
}

// safeUpdateConfigJSONRecorded is safeUpdateConfigJSON for the Save-bar
// writers: when config.json was written but the live refresh failed
// (configRefreshError), the keys changedKeys reports are recorded as
// apply_failed while configMu is still held — before the caller answers.
// changedKeys is called only after mutate ran.
func (a *restAPI) safeUpdateConfigJSONRecorded(mutate func(m map[string]any) error, changedKeys func() []string) error {
	store := a.pairingOrderStore()
	a.configMu.Lock()
	defer a.configMu.Unlock()
	err := a.orderedConfigJSONLocked(store, nil, nil, mutate)
	var refreshErr *configRefreshError
	if errors.As(err, &refreshErr) {
		a.applyFailed.record(applyFailedSourceRefresh, changedKeys())
	}
	return err
}

// changedConfigKeys returns the dotted leaf paths whose value differs between
// before and after (objects are walked; any other value is a leaf).
func changedConfigKeys(before, after map[string]any) []string {
	var out []string
	var walk func(prefix string, b, a map[string]any)
	walk = func(prefix string, b, a map[string]any) {
		seen := make(map[string]struct{}, len(a)+len(b))
		for k := range a {
			seen[k] = struct{}{}
		}
		for k := range b {
			seen[k] = struct{}{}
		}
		for k := range seen {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			bv, bok := b[k]
			av, aok := a[k]
			bm, bIsMap := bv.(map[string]any)
			am, aIsMap := av.(map[string]any)
			if bok && aok && bIsMap && aIsMap {
				walk(path, bm, am)
				continue
			}
			if bok != aok || !reflect.DeepEqual(bv, av) {
				out = append(out, path)
			}
		}
	}
	walk("", before, after)
	sort.Strings(out)
	return out
}

// deepCopyJSONMap copies a decoded config.json map so a later in-place merge
// cannot change the copy.
func deepCopyJSONMap(m map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
