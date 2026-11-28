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
// that covers its write (for a config refresh: any later successful refresh,
// which loads the whole file; for a registry-rebuild-stage key: a successful
// registry rebuild that read config.json after the write) or by a restart
// (the list lives in memory only).
//
// Writers record INSIDE the config write lock, before the response is sent,
// so a lost response can never leave a saved, unapplied key unlisted. A
// writer whose apply is a registry reload that runs after the lock records
// that reload's failure before it answers, judged against the newest
// successful apply (applyFailedRegistry).

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
	// write failed (configRefreshError). Any later successful refresh loads
	// the whole file, so it clears these entries.
	applyFailedSourceRefresh applyFailedSource = "config_refresh"
	// applyFailedSourceRegistryRebuild: a context settings write whose keys
	// are applied only by a registry rebuild (agents resolve their context
	// windows when they are built) was not applied. Only a successful
	// registry rebuild that read the write clears it; a refresh-only write
	// never does (G2-B1).
	applyFailedSourceRegistryRebuild applyFailedSource = "registry_rebuild"
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

// applyFailedEntry is one listed key, the apply path that failed for it, and
// the newest failed write that left it unapplied: ticket is that write's
// per-source ticket, readMark the config-read count taken with it (see
// applyFailedTicket).
type applyFailedEntry struct {
	path     configKeyPath
	source   applyFailedSource
	ticket   uint64
	readMark uint64
}

// applyFailedTicket identifies one recorded write, taken under the config
// write lock right after the write reached config.json.
//
// seq orders the writes of one source (nextTicket). readMark is
// reloadOutcomeTracker.configReadsCount() at the same moment: a registry
// rebuild whose applied config was stamped with a greater read sequence read
// config.json after this write, so it applied it (production stamps that
// read under the config write lock, gateway_reload.go::reloadConfigForSwap).
// readMark is 0 when no reload outcome tracker is wired.
type applyFailedTicket struct {
	seq      uint64
	readMark uint64
}

// applyFailedRegistry is the zero-value-usable set of failed-apply keys.
//
// G2-B1 (SFH G1-SFH-01, arch N-1): an outcome is judged against the newest
// SUCCESSFUL apply, never against the newest write. Per source, issued is the
// last ticket handed out and appliedThrough the highest ticket a successful
// apply of that source is known to cover. A failure of a write the watermark
// does not cover adds its keys to the list (the union of still-unapplied
// keys); a failure the watermark covers is dropped, because a newer
// successful apply already put the key in use (B5). A success removes only
// the entries whose newest failed write it covers.
//
// rebuildReadApplied is the highest config-read sequence a successful
// registry rebuild applied (rebuildApplied, the reload outcome tracker's
// success hook). It covers every registry-rebuild-stage write whose readMark
// is lower, whichever request triggered the rebuild.
type applyFailedRegistry struct {
	mu                 sync.Mutex
	entries            map[string]applyFailedEntry
	issued             map[applyFailedSource]uint64
	appliedThrough     map[applyFailedSource]uint64
	rebuildReadApplied uint64
}

// appliedByRegistryRebuild reports whether source's keys are put in use by
// a registry rebuild (ReloadProviderAndConfig), rather than by the config
// pointer swap of a refresh.
func appliedByRegistryRebuild(source applyFailedSource) bool {
	switch source {
	case applyFailedSourceRegistryRebuild, applyFailedSourcePromptGuard, applyFailedSourceRateLimits:
		return true
	}
	return false
}

// nextTicket starts a new write of source and returns its ticket sequence.
// Call it while the config write lock is held, right after the write.
func (r *applyFailedRegistry) nextTicket(source applyFailedSource) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.issued == nil {
		r.issued = make(map[applyFailedSource]uint64)
	}
	r.issued[source]++
	return r.issued[source]
}

// coveredLocked reports whether a successful apply already covers ticket.
func (r *applyFailedRegistry) coveredLocked(source applyFailedSource, ticket applyFailedTicket) bool {
	if ticket.seq <= r.appliedThrough[source] {
		return true
	}
	return appliedByRegistryRebuild(source) && ticket.readMark < r.rebuildReadApplied
}

// recordAt records the failed apply of the write ticket for keys, unless a
// successful apply already covers that write. Keys already listed stay
// listed (union); a key keeps the newest failed write that left it
// unapplied. It reports whether it recorded.
func (r *applyFailedRegistry) recordAt(source applyFailedSource, ticket applyFailedTicket, keys []configKeyPath) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.coveredLocked(source, ticket) {
		return false
	}
	if r.entries == nil {
		r.entries = make(map[string]applyFailedEntry)
	}
	for _, k := range keys {
		id := k.identity()
		e, ok := r.entries[id]
		if ok && e.source == source && e.ticket > ticket.seq {
			continue
		}
		r.entries[id] = applyFailedEntry{
			path: append(configKeyPath(nil), k...), source: source, ticket: ticket.seq, readMark: ticket.readMark,
		}
	}
	return true
}

// appliedAt records that an apply of source covering every write up to
// ticket sequence seq succeeded, and removes the entries of source whose
// newest failed write it covers.
func (r *applyFailedRegistry) appliedAt(source applyFailedSource, seq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appliedLocked(source, seq)
}

func (r *applyFailedRegistry) appliedLocked(source applyFailedSource, seq uint64) {
	if r.appliedThrough == nil {
		r.appliedThrough = make(map[applyFailedSource]uint64)
	}
	if seq > r.appliedThrough[source] {
		r.appliedThrough[source] = seq
	}
	for id, e := range r.entries {
		if e.source == source && e.ticket <= seq {
			delete(r.entries, id)
		}
	}
}

// clearSource records a successful apply of every write of source issued so
// far. Called under the config write lock (a successful refresh loads the
// whole file, so it covers every earlier write of its source).
func (r *applyFailedRegistry) clearSource(source applyFailedSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appliedLocked(source, r.issued[source])
}

// rebuildApplied is the reload outcome tracker's success hook: a registry
// rebuild whose applied config was read at configReadSeq succeeded, so every
// registry-rebuild-stage write with a lower readMark is in use. A read
// sequence of 0 (no stamp) proves nothing and changes nothing.
func (r *applyFailedRegistry) rebuildApplied(configReadSeq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if configReadSeq > r.rebuildReadApplied {
		r.rebuildReadApplied = configReadSeq
	}
	for id, e := range r.entries {
		if appliedByRegistryRebuild(e.source) && e.readMark < configReadSeq {
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
// apply_failed under source while configMu is still held — before the
// caller answers. changedKeys is called only after mutate ran.
//
// source names the apply path that puts the keys in use: a writer whose
// change is applied by a registry rebuild (context settings, prompt guard,
// rate limits) records under a registry-rebuild-stage source, so a later
// refresh-only write never clears it (B3, SFH-03, G2-B1); every other writer
// uses applyFailedSourceRefresh. It also returns the write's ticket, taken
// under the config write lock, for the writer's later reload outcome
// (recordAt / appliedAt). The ticket is zero when nothing was written.
func (a *restAPI) safeUpdateConfigJSONRecorded(
	source applyFailedSource, mutate func(m map[string]any) error, changedKeys func() []configKeyPath,
) (applyFailedTicket, error) {
	store := a.pairingOrderStore()
	a.configMu.Lock()
	defer a.configMu.Unlock()
	err := a.orderedConfigJSONLocked(store, nil, nil, mutate)
	var refreshErr *configRefreshError
	if err != nil && !errors.As(err, &refreshErr) {
		return applyFailedTicket{}, err
	}
	ticket := applyFailedTicket{
		seq:      a.applyFailed.nextTicket(source),
		readMark: a.reloadOutcome.configReadsCount(),
	}
	if refreshErr != nil {
		a.applyFailed.recordAt(source, ticket, changedKeys())
	}
	return ticket, err
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
