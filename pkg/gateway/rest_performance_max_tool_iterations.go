// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_performance_max_tool_iterations.go — the global tool-iteration limit
// ("Max tool calls per turn", issue #904,
// docs/internal/specs/tool-iteration-limit-spec.md) on the Performance
// surface:
//
//   - GET /api/v1/performance fields (value in force, saved state, saved raw);
//   - GET /api/v1/performance/max-tool-iterations/preview (read-only D11 list);
//   - the PUT /api/v1/performance write path with the D11/D16 consent rule,
//     the pre-token drift check, the under-configMu deciding check, agents
//     lowered first then the global, rollback on a mid-write failure, audit
//     of every change, and the registry reload (FR-005, FR-010, FR-021).
//
// Every number comes from pkg/config's resolver (config.ResolveMaxToolIterations,
// AgentDefaults.EffectiveGlobalMaxToolIterations, ValidateMaxToolIterationsBound);
// nothing here re-implements the rule.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Error codes and texts of the D11/D16 write path (spec, Machine-Verifiable
// Constraints and "D11/D16 write order").
const (
	maxToolIterationsDriftCode          = "max_tool_iterations_lowering_drift"
	maxToolIterationsLoweringFailedCode = "max_tool_iterations_lowering_failed"
	maxToolIterationsRollbackIncomplete = "max_tool_iterations_rollback_incomplete"
	// maxToolIterationsAgentsReadFailedCode: the agent store could not be
	// listed or an agent record could not be read while computing or
	// checking the affected set. Distinct from drift (409): nothing is known
	// to have changed, the server simply could not tell.
	maxToolIterationsAgentsReadFailedCode = "max_tool_iterations_agents_read_failed"
	// performanceReloadFailedCode: the save is committed (config.json and
	// any lowered agents are on disk, nothing rolled back) but not in force.
	// Two stages (PerformanceReloadFailedDetails.stage): refresh — the
	// in-memory config refresh failed, the running config was NOT swapped
	// and GET /performance still shows the old values; reload — the
	// in-memory config was swapped but the agent registry reload failed, so
	// agents' next turns keep the old limits. Either way the new values
	// apply after the next reload or restart.
	performanceReloadFailedCode           = "performance_reload_failed"
	maxToolIterationsAgentsReadMessage    = "could not read the agents"
	maxToolIterationsPreviewValueMessage  = "value must be between 1 and 1000"
	maxToolIterationsGlobalAuditResource  = "agents.defaults.max_tool_iterations"
	maxToolIterationsAgentAuditResourceFm = "agents.%s.max_tool_iterations"
)

// maxToolIterationsAgentStore is the slice of pkg/agentstore the D11 lowering
// uses. restAPI.limitAgentStore overrides it (tests inject I/O and revision
// faults, spec test row 13e); nil means agentstore.New(homePath).
type maxToolIterationsAgentStore interface {
	List() ([]config.AgentConfig, []string, error)
	ReadState(id string) (*agentstore.State, error)
	MutateState(id, expectedRevision string, mutate func(*config.AgentConfig) error, soul *string) (agentstore.MutationResult, error)
}

func (a *restAPI) maxToolIterationsStore() maxToolIterationsAgentStore {
	if a.limitAgentStore != nil {
		return a.limitAgentStore
	}
	return agentstore.New(a.homePath)
}

// applyPerformanceMaxToolIterations fills the global-limit fields of a
// PerformanceSettings response (GET and PUT share it): the value in force,
// the saved state and, for below_min/above_max only, the saved raw value.
func applyPerformanceMaxToolIterations(ps *gen.PerformanceSettings, cfg *config.Config) {
	g := cfg.Agents.Defaults.EffectiveGlobalMaxToolIterations()
	value := g.Value
	state := gen.MaxToolIterationsSavedState(g.SavedState)
	ps.MaxToolIterations = &value
	ps.MaxToolIterationsSavedState = &state
	if g.HasSavedRaw {
		raw := g.SavedRaw
		ps.MaxToolIterationsSavedRaw = &raw
	}
}

// maxToolIterationsLoweringSet computes, from the agent store, every agent
// whose own value is strictly above value (spec D11: equal or lower, and no
// own value, are excluded), in store order. The "own value" is read through
// the resolver's rule (<= 0 = none, D17) by comparing the stored field.
func maxToolIterationsLoweringSet(store maxToolIterationsAgentStore, value int) ([]gen.MaxToolIterationAgentChange, error) {
	agents, _, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]gen.MaxToolIterationAgentChange, 0)
	for i := range agents {
		own := config.ResolveMaxToolIterations(nil, &agents[i])
		if !own.HasOverride || own.Override <= value {
			continue
		}
		name := strings.TrimSpace(agents[i].Name)
		if name == "" {
			name = agents[i].ID
		}
		out = append(out, gen.MaxToolIterationAgentChange{
			AgentId:   agents[i].ID,
			AgentName: name,
			OldValue:  own.Override,
			NewValue:  value,
		})
	}
	return out, nil
}

// maxToolIterationsAffectedSet is the set a PUT to value would lower (D11),
// restricted by D20: only a value BELOW the global currently in force can
// lower anyone. A raise, or an unchanged value, affects no agent — capped
// agents (D1) keep their stored value — so it returns an empty list without
// reading the store. defaults may be nil (resolves like a missing global).
func maxToolIterationsAffectedSet(store maxToolIterationsAgentStore, defaults *config.AgentDefaults, value int) ([]gen.MaxToolIterationAgentChange, error) {
	if value >= defaults.EffectiveGlobalMaxToolIterations().Value {
		return []gen.MaxToolIterationAgentChange{}, nil
	}
	return maxToolIterationsLoweringSet(store, value)
}

// liveAgentDefaults returns the agents.defaults the D11/D16 decisions
// (preview, pre-check, deciding check, audit old value) run against: the
// in-memory copy with the global tool-iteration limit taken from config.json
// as SAVED — the value PUT /performance's own write builds on. After a
// refresh-stage performance_reload_failed the in-memory config still holds
// the old global while config.json holds the new one; deciding raise-vs-lower
// from the stale copy could call a lowering a raise and lower no agent
// without consent (D11). Falls back to the in-memory value (WARN) only when
// config.json cannot be read or parsed — the write itself then fails too.
// nil when no config is loaded.
func (a *restAPI) liveAgentDefaults() *config.AgentDefaults {
	if a.agentLoop == nil {
		return nil
	}
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		return nil
	}
	d := cfg.Agents.Defaults
	value, missing, err := savedGlobalMaxToolIterations(a.configPath())
	if err != nil {
		logsafeWarn("rest: tool-iteration limit: could not read the saved global from config.json; "+
			"using the in-memory value", "error_type", fmt.Sprintf("%T", err))
		return &d
	}
	d.MaxToolIterations = value
	d.MaxToolIterationsKeyMissing = missing
	return &d
}

// savedGlobalMaxToolIterations reads agents.defaults.max_tool_iterations from
// config.json the way config loading does (pkg/config/max_tool_iterations.go
// ::applyMaxToolIterationsOnLoad): the value is bound by encoding/json, and
// the key counts as missing when agents.defaults has no exact
// "max_tool_iterations" member or it is null.
func savedGlobalMaxToolIterations(path string) (value int, missing bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	var typed struct { // not-wire-format: decode-only probe of config.json on disk; never crosses the gateway/SPA boundary.
		Agents struct {
			Defaults struct {
				MaxToolIterations int `json:"max_tool_iterations"`
			} `json:"defaults"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &typed); err != nil {
		return 0, false, err
	}
	var probe struct { // not-wire-format: decode-only probe of config.json on disk; never crosses the gateway/SPA boundary.
		Agents struct {
			Defaults map[string]json.RawMessage `json:"defaults"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return 0, false, err
	}
	v, ok := probe.Agents.Defaults["max_tool_iterations"]
	missing = !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null"))
	return typed.Agents.Defaults.MaxToolIterations, missing, nil
}

// agentsReadFailure is the 500 for a failed agent-store read on the lowering
// path; the cause is logged, not sent.
func agentsReadFailure(stage string, err error) *performanceWriteError {
	logsafeError("rest: PUT /performance: could not read the agents", "stage", stage, "error", err)
	code := maxToolIterationsAgentsReadFailedCode
	return &performanceWriteError{status: http.StatusInternalServerError,
		body: gen.ErrorResponse{Error: maxToolIterationsAgentsReadMessage, Code: &code}}
}

// HandleMaxToolIterationsPreview handles GET
// /api/v1/performance/max-tool-iterations/preview (operationId
// previewMaxToolIterationsLowering). Read-only; registered with adminWrap —
// the same gate as GET /performance, no step-up token (FR-018).
func (a *restAPI) HandleMaxToolIterationsPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("value"))
	value, err := strconv.Atoi(raw)
	if err != nil || config.ValidateMaxToolIterationsBound(value) != nil {
		jsonErr(w, http.StatusBadRequest, maxToolIterationsPreviewValueMessage)
		return
	}
	// D20: a raise (or the current value) previews an empty list.
	agents, err := maxToolIterationsAffectedSet(a.maxToolIterationsStore(), a.liveAgentDefaults(), value)
	if err != nil {
		logsafeError("rest: max-tool-iterations preview: list agents", "error", err)
		code := maxToolIterationsAgentsReadFailedCode
		writeJSON(w, http.StatusInternalServerError, gen.ErrorResponse{Error: maxToolIterationsAgentsReadMessage, Code: &code})
		return
	}
	jsonOK(w, gen.MaxToolIterationsLoweringPreview{Value: value, Agents: agents})
}

// maxToolIterationsGlobalUpdate is the validated global-limit part of a
// PerformanceSettingsUpdate.
type maxToolIterationsGlobalUpdate struct {
	value     int
	confirmed map[string]int // agent_id → old value the admin confirmed
}

// parseMaxToolIterationsUpdate runs the step-1 checks of the D11/D16 write
// order: the 1..1000 bound and a well-formed confirmed_lowering. Returns
// (nil, true) when the request does not touch the global; writes the 400 and
// returns ok=false on a malformed request.
func parseMaxToolIterationsUpdate(w http.ResponseWriter, req *gen.PerformanceSettingsUpdate) (*maxToolIterationsGlobalUpdate, bool) {
	if req.MaxToolIterations == nil {
		if req.ConfirmedLowering != nil && len(*req.ConfirmedLowering) > 0 {
			jsonErr(w, http.StatusBadRequest, "confirmed_lowering requires max_tool_iterations")
			return nil, false
		}
		return nil, true
	}
	if err := config.ValidateMaxToolIterationsBound(*req.MaxToolIterations); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	upd := &maxToolIterationsGlobalUpdate{value: *req.MaxToolIterations, confirmed: map[string]int{}}
	if req.ConfirmedLowering != nil {
		for _, c := range *req.ConfirmedLowering {
			if _, dup := upd.confirmed[c.AgentId]; dup {
				jsonErr(w, http.StatusBadRequest,
					fmt.Sprintf("confirmed_lowering lists agent %s more than once", c.AgentId))
				return nil, false
			}
			upd.confirmed[c.AgentId] = c.OldValue
		}
	}
	return upd, true
}

// matchesConfirmation compares the live affected set with the confirmation as
// a set keyed by agent_id — order-independent (spec G4).
func (u *maxToolIterationsGlobalUpdate) matchesConfirmation(live []gen.MaxToolIterationAgentChange) bool {
	if len(live) != len(u.confirmed) {
		return false
	}
	for _, c := range live {
		old, ok := u.confirmed[c.AgentId]
		if !ok || old != c.OldValue {
			return false
		}
	}
	return true
}

// preCheckMaxToolIterationsDrift is step 2: the drift check that runs BEFORE
// the step-up token is consumed, so a stale dialog costs no password re-entry.
// Returns false after writing the response.
func (a *restAPI) preCheckMaxToolIterationsDrift(w http.ResponseWriter, upd *maxToolIterationsGlobalUpdate) bool {
	live, err := maxToolIterationsAffectedSet(a.maxToolIterationsStore(), a.liveAgentDefaults(), upd.value)
	if err != nil {
		werr := agentsReadFailure("pre-check", err)
		writeJSON(w, werr.status, werr.body)
		return false
	}
	if !upd.matchesConfirmation(live) {
		d := driftError(upd.value, live)
		writeJSON(w, d.status, d.body)
		return false
	}
	return true
}

// loweringTarget is one agent the deciding check (step 4) cleared for
// lowering, with the revision read under configMu.
type loweringTarget struct {
	change   gen.MaxToolIterationAgentChange
	revision string
}

// loweredAgent is one agent step 5 lowered, with the revision the lowering
// write returned (the rollback's precondition).
type loweredAgent struct {
	change   gen.MaxToolIterationAgentChange
	revision string
}

// errLoweringValueChanged marks a lowering write whose record no longer holds
// the confirmed old value (a writer that does not take configMu slipped in
// between the deciding check and the write). Treated like a revision conflict.
var errLoweringValueChanged = errors.New("agent's own tool-iteration limit changed")

// performanceWriteOutcome carries what the locked write produced.
type performanceWriteOutcome struct {
	lowered       []gen.MaxToolIterationAgentChange
	globalChanged bool
	// notApplied is set when every write is committed (config.json and the
	// lowered agents) but the in-memory refresh after the config.json write
	// failed: the caller answers performance_reload_failed, like a failed
	// registry reload, and nothing is rolled back.
	notApplied error
	// appliedEpoch is the pending-apply epoch the successful in-memory
	// refresh covered (performancePendingApply.epochNow under configMu);
	// meaningful only when notApplied is nil.
	appliedEpoch uint64
}

// performanceWriteError is a fully-formed HTTP failure from the locked write.
type performanceWriteError struct {
	status int
	body   any
}

// decideMaxToolIterationsLowering is step 4, run under configMu before the
// first write: recompute the affected set (empty on a raise, D20), compare it
// with the confirmation, and read each confirmed agent's revision and own
// value from the store. A mismatch → 409 with the fresh list and nothing
// written (D16). A store read that FAILS is not drift: it is logged and
// answered 500 max_tool_iterations_agents_read_failed, nothing written.
func decideMaxToolIterationsLowering(store maxToolIterationsAgentStore, defaults *config.AgentDefaults,
	upd *maxToolIterationsGlobalUpdate,
) ([]loweringTarget, *performanceWriteError) {
	live, err := maxToolIterationsAffectedSet(store, defaults, upd.value)
	if err != nil {
		return nil, agentsReadFailure("deciding check: list", err)
	}
	if !upd.matchesConfirmation(live) {
		return nil, driftError(upd.value, live)
	}
	targets := make([]loweringTarget, 0, len(live))
	for _, c := range live {
		state, readErr := store.ReadState(c.AgentId)
		if readErr != nil {
			return nil, agentsReadFailure("deciding check: read agent "+c.AgentId, readErr)
		}
		if state == nil || state.Agent == nil || state.Agent.MaxToolIterations != c.OldValue {
			return nil, freshDriftError(store, defaults, upd.value)
		}
		targets = append(targets, loweringTarget{change: c, revision: state.Revision})
	}
	return targets, nil
}

// freshDriftError recomputes the affected set and returns the 409 carrying
// it; a failed recomputation is a 500, never a 409 with an empty list.
func freshDriftError(store maxToolIterationsAgentStore, defaults *config.AgentDefaults, value int) *performanceWriteError {
	fresh, err := maxToolIterationsAffectedSet(store, defaults, value)
	if err != nil {
		return agentsReadFailure("drift: recompute the list", err)
	}
	return driftError(value, fresh)
}

func driftError(value int, live []gen.MaxToolIterationAgentChange) *performanceWriteError {
	if live == nil {
		live = []gen.MaxToolIterationAgentChange{}
	}
	return &performanceWriteError{status: http.StatusConflict, body: gen.MaxToolIterationsLoweringConflict{
		Error: fmt.Sprintf("the agents affected by lowering the limit to %d changed since the preview; "+
			"review the updated list and confirm again", value),
		Code:    maxToolIterationsDriftCode,
		Preview: gen.MaxToolIterationsLoweringPreview{Value: value, Agents: live},
	}}
}

// setAgentMaxToolIterations writes one agent's own value from `from` to `to`
// under the given revision; the mutate refuses when the record no longer
// holds `from`.
func setAgentMaxToolIterations(store maxToolIterationsAgentStore, id, revision string, from, to int) (string, error) {
	now := time.Now().UTC()
	res, err := store.MutateState(id, revision, func(ac *config.AgentConfig) error {
		if ac.MaxToolIterations != from {
			return errLoweringValueChanged
		}
		ac.MaxToolIterations = to
		ac.UpdatedAt = &now
		return nil
	}, nil)
	if err != nil {
		return "", err
	}
	return res.Revision, nil
}

func isLoweringConflict(err error) bool {
	return errors.Is(err, agentstore.ErrRevisionConflict) || errors.Is(err, errLoweringValueChanged)
}

// lowerAgents is step 5: lower each target, auditing each. On the first
// failure it returns the agents already lowered and the error.
func (a *restAPI) lowerAgents(ctx context.Context, store maxToolIterationsAgentStore, targets []loweringTarget) ([]loweredAgent, string, error) {
	done := make([]loweredAgent, 0, len(targets))
	for _, t := range targets {
		rev, err := setAgentMaxToolIterations(store, t.change.AgentId, t.revision, t.change.OldValue, t.change.NewValue)
		if err != nil {
			return done, t.change.AgentName, err
		}
		a.auditMaxToolIterationsChange(ctx, fmt.Sprintf(maxToolIterationsAgentAuditResourceFm, t.change.AgentId),
			t.change.OldValue, t.change.NewValue)
		done = append(done, loweredAgent{change: t.change, revision: rev})
	}
	return done, "", nil
}

// rollbackLoweredAgents restores every agent lowered in this request to its
// old value (FR-021), auditing each successful restore. Returns the agents
// that could NOT be restored, each logged at ERROR.
func (a *restAPI) rollbackLoweredAgents(ctx context.Context, store maxToolIterationsAgentStore, done []loweredAgent) []gen.MaxToolIterationAgentChange {
	var stuck []gen.MaxToolIterationAgentChange
	for _, d := range done {
		_, err := setAgentMaxToolIterations(store, d.change.AgentId, d.revision, d.change.NewValue, d.change.OldValue)
		if err != nil {
			logsafeError("rest: PUT /performance: could not restore an agent's tool-iteration limit after a failed lowering",
				"agent_id", d.change.AgentId, "old", d.change.OldValue, "current", d.change.NewValue, "error", err)
			stuck = append(stuck, d.change)
			continue
		}
		a.auditMaxToolIterationsChange(ctx, fmt.Sprintf(maxToolIterationsAgentAuditResourceFm, d.change.AgentId),
			d.change.NewValue, d.change.OldValue)
	}
	return stuck
}

// loweringFailure builds the response for a failure after writes began
// (step 5 or 6): roll back, then 500 rollback_incomplete if any restore
// failed; else 409 (conflict, fresh list) or 500 lowering_failed (I/O).
// cause is always logged at ERROR; on rollback_incomplete it is also sent in
// details.cause.
func (a *restAPI) loweringFailure(ctx context.Context, store maxToolIterationsAgentStore, defaults *config.AgentDefaults,
	upd *maxToolIterationsGlobalUpdate, done []loweredAgent, failingAgent string, cause error,
) *performanceWriteError {
	target := "the global limit"
	if failingAgent != "" {
		target = "agent " + failingAgent
	}
	logsafeError("rest: PUT /performance: tool-iteration lowering failed; rolling back the agents already lowered",
		"failed_at", target, "lowered_before_failure", len(done), "value", upd.value, "error", cause)
	if stuck := a.rollbackLoweredAgents(ctx, store, done); len(stuck) > 0 {
		parts := make([]string, 0, len(stuck))
		for _, s := range stuck {
			parts = append(parts, fmt.Sprintf("%s (now %d, was %d)", s.AgentName, s.NewValue, s.OldValue))
		}
		// Each unrestored agent already has its own ERROR line
		// (rollbackLoweredAgents); this one ties them to the original cause.
		logsafeError("rest: PUT /performance: rollback incomplete after a failed tool-iteration lowering",
			"failed_at", target, "not_restored_count", len(stuck), "cause", cause)
		code := maxToolIterationsRollbackIncomplete
		details := map[string]any{"cause": cause.Error()}
		return &performanceWriteError{status: http.StatusInternalServerError, body: gen.ErrorResponse{
			Error: "limit not changed; could not restore " + strings.Join(parts, ", ") +
				" — set their limits again on each agent's profile",
			Code:    &code,
			Details: &details,
		}}
	}
	if isLoweringConflict(cause) {
		return freshDriftError(store, defaults, upd.value)
	}
	code := maxToolIterationsLoweringFailedCode
	return &performanceWriteError{status: http.StatusInternalServerError, body: gen.ErrorResponse{
		Error: fmt.Sprintf("could not lower the tool-iteration limit of %s: the setting could not be saved; nothing was changed. Details are in the server log.", target),
		Code:  &code,
	}}
}

func (a *restAPI) auditMaxToolIterationsChange(ctx context.Context, resource string, oldValue, newValue int) {
	if a.agentLoop == nil {
		return
	}
	if err := audit.EmitSecuritySettingChange(ctx, a.agentLoop.AuditLogger(), resource, oldValue, newValue); err != nil {
		logsafeError("rest: tool-iteration limit audit write failed", "resource", resource, "error", err)
	}
}

// writePerformanceLocked is steps 4–6 of the D11/D16 write order, all under
// configMu: the deciding drift/revision check before any write, the agents
// lowered first, then config.json (the global plus any other performance
// fields in this request) written once. A failure after writes began rolls
// the agents back and leaves the global unchanged. A failed in-memory
// refresh after the write marks the changed fields pending apply (stage
// refresh) under the same lock.
func (a *restAPI) writePerformanceLocked(ctx context.Context, upd *maxToolIterationsGlobalUpdate,
	changed []gen.PerformanceReloadFailedDetailsChangedFields, mutate func(m map[string]any) error,
) (performanceWriteOutcome, *performanceWriteError) {
	a.configMu.Lock()
	defer a.configMu.Unlock()

	var out performanceWriteOutcome
	store := a.maxToolIterationsStore()
	defaults := a.liveAgentDefaults()
	var targets []loweringTarget
	oldGlobal := 0
	if upd != nil {
		var werr *performanceWriteError
		if targets, werr = decideMaxToolIterationsLowering(store, defaults, upd); werr != nil {
			return out, werr
		}
		g := defaults.EffectiveGlobalMaxToolIterations()
		oldGlobal = g.Value
		if g.HasSavedRaw {
			oldGlobal = g.SavedRaw
		}
		out.globalChanged = g.SavedState != config.MaxToolIterationsSavedOK || g.Value != upd.value
	}

	done, failing, err := a.lowerAgents(ctx, store, targets)
	if err != nil {
		return out, a.loweringFailure(ctx, store, defaults, upd, done, failing, err)
	}
	written, writeErr := a.writeConfigJSONLocked(mutate)
	if writeErr != nil {
		// writeConfigJSONLocked only fails reading, parsing, serializing or
		// writing config.json (a filesystem or JSON error — no refresh or
		// credential text can reach it), so the real cause is logged here
		// for the operator while the response keeps the fixed cause class.
		cause := configWriteFailure(writeErr)
		logsafeError("rest: PUT /performance: could not write config.json",
			"cause", cause.Error(), "error", writeErr)
		if len(done) > 0 {
			return out, a.loweringFailure(ctx, store, defaults, upd, done, "", cause)
		}
		return out, &performanceWriteError{status: http.StatusInternalServerError,
			body: gen.ErrorResponse{Error: "could not update performance settings: " + cause.Error()}}
	}
	if refreshErr := a.applyWrittenConfigLocked(written); refreshErr != nil {
		// config.json is already written: the save is committed, so rolling
		// the agents back would contradict the written global and "nothing
		// was changed" would be false. refreshErr is never logged or
		// returned here: its text can carry a credential reference NAME
		// (from refreshConfigAndRewireServices' credential resolution;
		// CodeQL clear-text-logging on PR #932). Only the fixed stage leaves
		// this function; refreshConfigAndRewireServices logs its load,
		// roster and credential causes itself.
		logsafeError("rest: PUT /performance: config.json written but the in-memory refresh failed; "+
			"keeping the lowered agents (load/roster/credential causes are logged by refreshConfigAndRewireServices)",
			"stage", "refresh", "lowered_agents", len(done))
		out.notApplied = errPerformanceRefreshFailed
		a.pendingApply.mark(gen.PerformanceReloadFailedDetailsStageRefresh, changed,
			a.reloadOutcome.startedCount(), a.reloadOutcome.configReadsCount())
		a.commitPerformanceOutcome(ctx, &out, upd, done, oldGlobal)
		return out, nil
	}
	// The running config now reflects everything on disk: remember the
	// pending-apply epoch it covers, so a registry reload that succeeds
	// afterwards may clear a pending_apply marked no later than this.
	out.appliedEpoch = a.pendingApply.epochNow()
	a.commitPerformanceOutcome(ctx, &out, upd, done, oldGlobal)
	return out, nil
}

// errPerformanceRefreshFailed stands in for a configRefreshError in the
// outcome: fixed text, no credential-derived detail.
var errPerformanceRefreshFailed = errors.New("config.json written but the in-memory refresh failed")

// configWriteFailure maps a non-refresh updateConfigJSONLocked failure to a
// fixed, credential-free description (the cause class only): the file
// could not be read, parsed or written.
func configWriteFailure(err error) error {
	var pathErr *fs.PathError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.Is(err, fs.ErrPermission):
		return errors.New("config.json: permission denied")
	case errors.Is(err, fs.ErrNotExist):
		return errors.New("config.json: file not found")
	case errors.As(err, &syntaxErr):
		return errors.New("config.json: invalid JSON")
	case errors.As(err, &pathErr):
		return fmt.Errorf("config.json: %s failed", pathErr.Op)
	default:
		return errors.New("config.json could not be written")
	}
}

// commitPerformanceOutcome records a committed write: the lowered agents in
// the outcome and the audit record for a changed global.
func (a *restAPI) commitPerformanceOutcome(ctx context.Context, out *performanceWriteOutcome,
	upd *maxToolIterationsGlobalUpdate, done []loweredAgent, oldGlobal int,
) {
	for _, d := range done {
		out.lowered = append(out.lowered, d.change)
	}
	if upd != nil && out.globalChanged {
		a.auditMaxToolIterationsChange(ctx, maxToolIterationsGlobalAuditResource, oldGlobal, upd.value)
	}
}
