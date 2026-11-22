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
	"strconv"
	"strings"
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

// configKeyPath is one config key as its path segments. The segments are
// kept separate so a literal member name that contains dots (stored verbatim
// by the generic config write) is never re-read as a nested path (S2); the
// dotted form is only the display key on the wire.
type configKeyPath []string

// String is the dotted display key (details.keys, pending-restart key).
func (p configKeyPath) String() string { return strings.Join(p, ".") }

// identity is an unambiguous map key for p: each segment Go-quoted, so no
// member name can make two different paths collide.
func (p configKeyPath) identity() string {
	quoted := make([]string, len(p))
	for i, seg := range p {
		quoted[i] = strconv.Quote(seg)
	}
	return strings.Join(quoted, ",")
}

// canonicalConfigKeys turns code-defined canonical dotted keys (fixed
// literals such as "sandbox.prompt_injection_level") into paths. Never use it
// on member names read from a request or from config.json.
func canonicalConfigKeys(dotted ...string) []configKeyPath {
	out := make([]configKeyPath, 0, len(dotted))
	for _, d := range dotted {
		out = append(out, configKeyPath(strings.Split(d, ".")))
	}
	return out
}

// dottedConfigKeys returns the display keys of paths, never nil.
func dottedConfigKeys(paths []configKeyPath) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, p.String())
	}
	return out
}

// applyFailedEntry is one listed key and the apply path that failed for it.
type applyFailedEntry struct {
	path   configKeyPath
	source applyFailedSource
}

// applyFailedRegistry is the zero-value-usable set of failed-apply keys.
type applyFailedRegistry struct {
	mu      sync.Mutex
	entries map[string]applyFailedEntry
}

// record adds keys whose last save did not apply through source.
func (r *applyFailedRegistry) record(source applyFailedSource, keys []configKeyPath) {
	if len(keys) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]applyFailedEntry)
	}
	for _, k := range keys {
		r.entries[k.identity()] = applyFailedEntry{path: append(configKeyPath(nil), k...), source: source}
	}
}

// clearSource removes every entry the now-successful source applied.
func (r *applyFailedRegistry) clearSource(source applyFailedSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range r.entries {
		if e.source == source {
			delete(r.entries, id)
		}
	}
}

// keys returns the listed keys, sorted by display key then identity.
func (r *applyFailedRegistry) keys() []configKeyPath {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]configKeyPath, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, append(configKeyPath(nil), e.path...))
	}
	sortConfigKeyPaths(out)
	return out
}

// sortConfigKeyPaths orders paths by display key, ties by identity.
func sortConfigKeyPaths(paths []configKeyPath) {
	sort.Slice(paths, func(i, j int) bool {
		si, sj := paths[i].String(), paths[j].String()
		if si != sj {
			return si < sj
		}
		return paths[i].identity() < paths[j].identity()
	})
}

// savedNotRefreshedText is the 500 text for a write that reached config.json
// whose live refresh failed (C-6 outcome table).
const (
	savedNotRefreshedCode = "saved_not_refreshed"
	savedNotRefreshedText = "the change was saved, but it is not in use yet; save again or restart the gateway to apply it"
)

// writeSavedNotRefreshed answers 500 ErrorResponse with code
// saved_not_refreshed and details.keys (FR-102).
func writeSavedNotRefreshed(w http.ResponseWriter, text string, keys []configKeyPath) {
	code := savedNotRefreshedCode
	details := map[string]any{"keys": dottedConfigKeys(keys)}
	writeJSON(w, http.StatusInternalServerError, gen.ErrorResponse{Error: text, Code: &code, Details: &details})
}

// safeUpdateConfigJSONRecorded is safeUpdateConfigJSON for the Save-bar
// writers: when config.json was written but the live refresh failed
// (configRefreshError), the keys changedKeys reports are recorded as
// apply_failed while configMu is still held — before the caller answers.
// changedKeys is called only after mutate ran.
func (a *restAPI) safeUpdateConfigJSONRecorded(
	mutate func(m map[string]any) error, changedKeys func() []configKeyPath,
) error {
	return a.safeUpdateConfigJSONRecordedAs(applyFailedSourceRefresh, mutate, changedKeys)
}

// safeUpdateConfigJSONRecordedAs is safeUpdateConfigJSONRecorded with the
// apply path named: a writer whose change is applied by its own reload
// (prompt guard, rate limits) records under its own source, so only its own
// later successful apply clears the entry (B3, SFH-03).
func (a *restAPI) safeUpdateConfigJSONRecordedAs(
	source applyFailedSource, mutate func(m map[string]any) error, changedKeys func() []configKeyPath,
) error {
	store := a.pairingOrderStore()
	a.configMu.Lock()
	defer a.configMu.Unlock()
	err := a.orderedConfigJSONLocked(store, nil, nil, mutate)
	var refreshErr *configRefreshError
	if errors.As(err, &refreshErr) {
		a.applyFailed.record(source, changedKeys())
	}
	return err
}

// changedConfigKeys returns the leaf paths whose value differs between
// before and after (objects are walked, also when only one side has one; any
// other value is a leaf). Each member name is one segment, whatever it
// contains, so a literal dotted name stays a single segment (S2).
func changedConfigKeys(before, after map[string]any) []configKeyPath {
	var out []configKeyPath
	var walk func(prefix configKeyPath, b, a map[string]any)
	walk = func(prefix configKeyPath, b, a map[string]any) {
		seen := make(map[string]struct{}, len(a)+len(b))
		for k := range a {
			seen[k] = struct{}{}
		}
		for k := range b {
			seen[k] = struct{}{}
		}
		for k := range seen {
			path := append(append(configKeyPath(nil), prefix...), k)
			bv, bok := b[k]
			av, aok := a[k]
			bm, bIsMap := bv.(map[string]any)
			am, aIsMap := av.(map[string]any)
			if bIsMap || aIsMap {
				// A section added, removed or replaced by a scalar is listed
				// by its leaves, so details.keys names dotted leaf keys
				// (gateway.log_level, not gateway).
				if bIsMap != aIsMap && ((bok && !bIsMap && bv != nil) || (aok && !aIsMap && av != nil)) {
					out = append(out, path)
				}
				if bm == nil {
					bm = map[string]any{}
				}
				if am == nil {
					am = map[string]any{}
				}
				walk(path, bm, am)
				continue
			}
			if bok != aok || !reflect.DeepEqual(bv, av) {
				out = append(out, path)
			}
		}
	}
	walk(nil, before, after)
	sortConfigKeyPaths(out)
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

// performancePendingApplyConfigKeys maps a PUT /performance pending_apply
// field to the config key it saved (C-6 outcome table: a saved-but-not-applied
// 200 is listed as apply_failed too). The pending_apply state is the single
// record; it is marked inside the config write lock and cleared by the
// performance writer's own rules.
var performancePendingApplyConfigKeys = map[gen.PerformancePendingApplyChangedFields]string{
	gen.PerformancePendingApplyChangedFieldsMaxParallelAgents:         "performance.max_parallel_agents",
	gen.PerformancePendingApplyChangedFieldsToolsOnDemand:             "tools.manifest.compressed",
	gen.PerformancePendingApplyChangedFieldsGoalMaxRounds:             "planning.goal_max_rounds",
	gen.PerformancePendingApplyChangedFieldsGoalReminderQuietSeconds:  "planning.goal_reminder_quiet_seconds",
	gen.PerformancePendingApplyChangedFieldsGoalStalledCheckinEnabled: "planning.goal_stalled_checkin_enabled",
	gen.PerformancePendingApplyChangedFieldsGoalStalledCheckinMinutes: "planning.goal_stalled_checkin_minutes",
	gen.PerformancePendingApplyChangedFieldsMaxToolIterations:         "agents.defaults.max_tool_iterations",
}

// performancePendingApplyKeys returns the config keys of the current
// PUT /performance pending_apply, or nil when nothing is pending.
func (a *restAPI) performancePendingApplyKeys() []string {
	snap := a.pendingApply.snapshot()
	if snap == nil {
		return nil
	}
	keys := make([]string, 0, len(snap.ChangedFields))
	for _, f := range snap.ChangedFields {
		if k, ok := performancePendingApplyConfigKeys[f]; ok {
			keys = append(keys, k)
		}
	}
	return keys
}
