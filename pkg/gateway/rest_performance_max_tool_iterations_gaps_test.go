// rest_performance_max_tool_iterations_gaps_test.go — #904 test gaps found by
// the 8-reviewer gate and CHECK, closed with spec-derived tests.
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md (Approved) — "API and
// Data" D11/D16 write order (steps 1–7 and the failure rules), FR-005,
// FR-010, FR-021; founder decisions D6, D16, D20, D21
// (docs/internal/specs/tool-iteration-limit-interview.md). Every expected
// value comes from those texts, never from running the handler.
//
// Fault injection uses the production seam restAPI.limitAgentStore
// (maxToolIterationsAgentStore: List / ReadState / MutateState) wrapped
// around the REAL temp-dir agent store, so every write that does happen is a
// real write and every "nothing written" assertion reads real bytes.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// mtiScriptStore wraps the real agent store with per-call hooks. Call numbers
// are 1-based and counted per method, so a test says "the 2nd lowering write"
// without depending on the order the handler walks the agents in.
type mtiScriptStore struct {
	*agentstore.Store
	mu sync.Mutex

	listCalls, readCalls, mutateCalls int
	wrote                             []string // ids of SUCCESSFUL MutateState calls, in order
	read                              []string // ids passed to ReadState, in order

	onList       func(n int) error
	onRead       func(n int, id string) error
	beforeMutate func(n int, id string) error
	afterMutate  func(n int, id string)
}

func (s *mtiScriptStore) List() ([]config.AgentConfig, []string, error) {
	s.mu.Lock()
	s.listCalls++
	n := s.listCalls
	s.mu.Unlock()
	if s.onList != nil {
		if err := s.onList(n); err != nil {
			return nil, nil, err
		}
	}
	return s.Store.List()
}

func (s *mtiScriptStore) ReadState(id string) (*agentstore.State, error) {
	s.mu.Lock()
	s.readCalls++
	n := s.readCalls
	s.read = append(s.read, id)
	s.mu.Unlock()
	if s.onRead != nil {
		if err := s.onRead(n, id); err != nil {
			return nil, err
		}
	}
	return s.Store.ReadState(id)
}

func (s *mtiScriptStore) MutateState(id, rev string, mutate func(*config.AgentConfig) error, soul *string) (agentstore.MutationResult, error) {
	s.mu.Lock()
	s.mutateCalls++
	n := s.mutateCalls
	s.mu.Unlock()
	if s.beforeMutate != nil {
		if err := s.beforeMutate(n, id); err != nil {
			return agentstore.MutationResult{}, err
		}
	}
	res, err := s.Store.MutateState(id, rev, mutate, soul)
	if err == nil {
		s.mu.Lock()
		s.wrote = append(s.wrote, id)
		s.mu.Unlock()
		if s.afterMutate != nil {
			s.afterMutate(n, id)
		}
	}
	return res, err
}

func (s *mtiScriptStore) mutates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutateCalls
}

// concurrentWrite simulates a writer that does not take configMu (spec: the
// system-agent tools serialise on the agent loop's lock, not configMu): it
// goes straight to the real store, bumping the record's revision.
func (s *mtiScriptStore) concurrentWrite(t *testing.T, id string, fn func(*config.AgentConfig)) {
	t.Helper()
	st, err := s.Store.ReadState(id)
	require.NoError(t, err)
	_, err = s.Store.MutateState(id, st.Revision, func(ac *config.AgentConfig) error { fn(ac); return nil }, nil)
	require.NoError(t, err, "test's concurrent writer failed")
}

// newMTIScriptAPI: global 300, A own 250, B own 280 (the spec's rollback and
// "Confirmed lowering" dataset setup) with the script store installed.
func newMTIScriptAPI(t *testing.T) (*restAPI, *mtiScriptStore, string) {
	t.Helper()
	api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250}, mtiAgent{id: "agent-b", own: 280})
	s := &mtiScriptStore{Store: agentstore.New(api.homePath)}
	api.limitAgentStore = s
	return api, s, attachTestAuditor(t, api)
}

func mtiOther(id string) string {
	if id == "agent-a" {
		return "agent-b"
	}
	return "agent-a"
}

func mtiPreviewAgents(t *testing.T, m map[string]any) any {
	t.Helper()
	p, ok := m["preview"].(map[string]any)
	require.True(t, ok, "409 body must carry a preview object: %v", m)
	assert.EqualValues(t, 200, p["value"], "preview.value is the requested global")
	return p["agents"]
}

// ---------------------------------------------------------------------------
// Item 2 — D16 step 4: the deciding check under configMu re-reads each
// confirmed agent's own value. Mutant that this kills: deleting the
// `state.Agent.MaxToolIterations != c.OldValue` re-check in
// rest_performance_max_tool_iterations.go::decideMaxToolIterationsLowering.
// ---------------------------------------------------------------------------

// Spec "D11/D16 write order" step 4: under configMu, BEFORE THE FIRST WRITE,
// read each confirmed agent's current own value; any mismatch → 409 with the
// fresh preview and NOTHING written. The change here lands after both List
// calls (pre-check and deciding) and before the deciding ReadState, so only
// the ReadState re-check can see it. "Nothing written" is asserted as ZERO
// MutateState calls through the seam — a lowering write that is attempted
// and then refused is still a write attempt the spec forbids at this step.
func TestPerformancePut_LoweringDrift_DecidingReadStateSeesChange(t *testing.T) {
	api, s, extraAudit := newMTIScriptAPI(t)
	changed := ""
	s.onRead = func(n int, id string) error {
		if n == 1 {
			changed = id
			s.concurrentWrite(t, id, func(ac *config.AgentConfig) { ac.MaxToolIterations = mtiOld[id] + 10 })
		}
		return nil
	}
	w := mtiPutPerf(t, api, mtiRollbackBody)

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	require.NotEmpty(t, changed, "the deciding check must read the confirmed agents' state (ReadState was never called)")
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, mtiDriftCode, m["code"])
	assert.Equal(t, mtiDriftMsg(200), m["error"])
	mtiAssertAgentChanges(t, mtiPreviewAgents(t, m), []mtiLowering{
		{changed, "Agent " + changed, mtiOld[changed] + 10, 200},
		{mtiOther(changed), "Agent " + mtiOther(changed), mtiOld[mtiOther(changed)], 200},
	})
	assert.Equal(t, 0, s.mutates(), "D16 step 4: a mismatch found under the lock writes NOTHING — no lowering write may be attempted")
	assert.EqualValues(t, mtiOld[changed]+10, mtiStoredOwn(t, api, changed))
	assert.EqualValues(t, mtiOld[mtiOther(changed)], mtiStoredOwn(t, api, mtiOther(changed)))
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api))
	assert.Empty(t, mtiSecurityChanges(t, api, extraAudit), "nothing written → no audit record")
}

// ---------------------------------------------------------------------------
// Item 3 — FR-021 branches not yet covered
// ---------------------------------------------------------------------------

// Spec step-5 failure rule: a REVISION CONFLICT from a writer that does not
// take configMu, after at least one agent was lowered → the already-lowered
// agent is rolled back (audited), the global is unchanged, and the answer is
// 409 MaxToolIterationsLoweringConflict with the fresh preview.
func TestPerformancePut_LoweringRollback_RevisionConflictMidWrite(t *testing.T) {
	api, s, extraAudit := newMTIScriptAPI(t)
	before := mtiSnapshotFiles(t, api)
	s.beforeMutate = func(n int, id string) error {
		if n == 2 {
			// Unrelated field: the revision moves, the own value does not.
			s.concurrentWrite(t, id, func(ac *config.AgentConfig) { ac.Description = "edited elsewhere" })
		}
		return nil
	}
	w := mtiPutPerf(t, api, mtiRollbackBody)

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	require.Len(t, s.wrote, 2, "one lowering write, then its rollback write (the 2nd agent's write hit the conflict)")
	first := s.wrote[0]
	assert.Equal(t, first, s.wrote[1], "the rollback restores the agent that was lowered")
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, mtiDriftCode, m["code"])
	mtiAssertAgentChanges(t, mtiPreviewAgents(t, m), []mtiLowering{
		{"agent-a", "Agent agent-a", 250, 200},
		{"agent-b", "Agent agent-b", 280, 200},
	})
	assert.EqualValues(t, 250, mtiStoredOwn(t, api, "agent-a"))
	assert.EqualValues(t, 280, mtiStoredOwn(t, api, "agent-b"))
	assert.Equal(t, before["config.json"], mtiSnapshotFiles(t, api)["config.json"], "the global is written only after every agent succeeded")

	recs := mtiSecurityChanges(t, api, extraAudit)
	require.Len(t, recs, 2, "audit = the lowering of %s + its rollback, nothing for the global: %v", first, recs)
	got := make([]string, 0, len(recs))
	for _, r := range recs {
		got = append(got, fmt.Sprintf("%v|%v|%v", r["resource"], r["old_value"], r["new_value"]))
	}
	res := fmt.Sprintf(maxToolIterationsAgentAuditResourceFm, first)
	assert.ElementsMatch(t, []string{
		fmt.Sprintf("%s|%d|200", res, mtiOld[first]),
		fmt.Sprintf("%s|200|%d", res, mtiOld[first]),
	}, got)
}

// Spec step-6 failure rule: "if step 6 itself fails, all agents are rolled
// back the same way" → 500 max_tool_iterations_lowering_failed saying nothing
// was changed, every agent restored (audited), the global unchanged. The
// failure is made real: after the last lowering write, config.json is
// replaced by a directory so the global's read-modify-write cannot read it.
func TestPerformancePut_LoweringRollback_GlobalWriteFails(t *testing.T) {
	api, s, extraAudit := newMTIScriptAPI(t)
	cfgPath := api.configPath()
	orig, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	s.afterMutate = func(n int, _ string) {
		if n == 2 { // both agents lowered; the global write comes next
			require.NoError(t, os.Remove(cfgPath))
			require.NoError(t, os.Mkdir(cfgPath, 0o700))
		}
	}
	w := mtiPutPerf(t, api, mtiRollbackBody)
	// Restore the file before any further read.
	require.NoError(t, os.Remove(cfgPath))
	require.NoError(t, os.WriteFile(cfgPath, orig, 0o600))

	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	assert.Equal(t, "max_tool_iterations_lowering_failed", m["code"])
	msg, _ := m["error"].(string)
	assert.Contains(t, msg, "nothing was changed")
	require.Len(t, s.wrote, 4, "two lowering writes, then two rollback writes")
	assert.EqualValues(t, 250, mtiStoredOwn(t, api, "agent-a"), "A rolled back")
	assert.EqualValues(t, 280, mtiStoredOwn(t, api, "agent-b"), "B rolled back")
	assert.EqualValues(t, 300, mtiGetPerf(t, api)["max_tool_iterations"], "the global in force is unchanged")

	recs := mtiSecurityChanges(t, api, extraAudit)
	got := make([]string, 0, len(recs))
	for _, r := range recs {
		got = append(got, fmt.Sprintf("%v|%v|%v", r["resource"], r["old_value"], r["new_value"]))
	}
	ra := fmt.Sprintf(maxToolIterationsAgentAuditResourceFm, "agent-a")
	rb := fmt.Sprintf(maxToolIterationsAgentAuditResourceFm, "agent-b")
	assert.ElementsMatch(t, []string{
		ra + "|250|200", ra + "|200|250",
		rb + "|280|200", rb + "|200|280",
	}, got, "each lowering and each rollback audited; nothing for the global")
}

// ---------------------------------------------------------------------------
// Item 4 — FR-005: the REBUILT registry instance carries the new effective
// value (not merely "a reload was requested").
// ---------------------------------------------------------------------------

// mtiWireRealReload installs a reload that does what production's reload
// cycle does for the agent registry (gateway.go loadReloadConfig →
// executeReload → ReloadProviderAndConfig): re-read config.json, repopulate
// the roster from the agent store, rebuild the registry. Synchronous, so the
// handler's triggerReloadAndWait returns after the swap.
func mtiWireRealReload(t *testing.T, api *restAPI) *int {
	t.Helper()
	count := new(int)
	api.agentLoop.SetReloadFunc(func() error {
		*count++
		newCfg, err := config.LoadConfig(api.configPath())
		if err != nil {
			return err
		}
		if err := populateAgentsListFromEntityStoreStrict(newCfg, api.homePath); err != nil {
			return err
		}
		return api.agentLoop.ReloadProviderAndConfig(context.Background(), &restMockProvider{}, newCfg)
	})
	// Boot equivalence: the harness built the loop's first registry from an
	// in-memory config, so align it with config.json + the agent store once,
	// exactly as a boot would, before any instrument reading.
	require.NoError(t, api.agentLoop.TriggerReload())
	*count = 0
	return count
}

func mtiInstanceLimit(t *testing.T, api *restAPI, id string) int {
	t.Helper()
	inst, ok := api.agentLoop.GetRegistry().GetAgent(id)
	require.True(t, ok, "agent %s has no live instance in the registry", id)
	return inst.MaxIterations
}

func TestPerformancePut_MaxToolIterations_ReloadRebuildsInstances(t *testing.T) {
	t.Run("global PUT: an agent riding the global runs at the new global", func(t *testing.T) {
		api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"}, mtiAgent{id: "agent-b", own: 50})
		mtiWireRealReload(t, api)
		require.Equal(t, 200, mtiInstanceLimit(t, api, "agent-a"), "instrument: the live instance starts at the old global")
		require.Equal(t, http.StatusOK, mtiPutPerf(t, api, `{"max_tool_iterations":350}`).Code)
		assert.Equal(t, 350, mtiInstanceLimit(t, api, "agent-a"), "FR-005: next turn uses the new global (350)")
		assert.Equal(t, 50, mtiInstanceLimit(t, api, "agent-b"), "own value 50 stays in force under a raised global")
	})
	t.Run("agent PUT: the agent's rebuilt instance runs at its new own value", func(t *testing.T) {
		api := newMTIAPI(t, "200", mtiAgent{id: "agent-a"})
		mtiWireRealReload(t, api)
		require.Equal(t, 200, mtiInstanceLimit(t, api, "agent-a"), "instrument")
		w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":50}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, 50, mtiInstanceLimit(t, api, "agent-a"), "US-2 AS-1: the agent's next turn uses 50")
	})
	t.Run("D11 lowering: lowered and riding agents both run at the new global", func(t *testing.T) {
		api := newMTIAPI(t, "300", mtiAgent{id: "agent-a", own: 250}, mtiAgent{id: "agent-b"}, mtiAgent{id: "agent-c", own: 100})
		mtiWireRealReload(t, api)
		require.Equal(t, 250, mtiInstanceLimit(t, api, "agent-a"), "instrument")
		require.Equal(t, 300, mtiInstanceLimit(t, api, "agent-b"), "instrument")
		w := mtiPutPerf(t, api, `{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, 200, mtiInstanceLimit(t, api, "agent-a"), "lowered agent: effective 200")
		assert.Equal(t, 200, mtiInstanceLimit(t, api, "agent-b"), "agent riding the global: effective 200")
		assert.Equal(t, 100, mtiInstanceLimit(t, api, "agent-c"), "own 100 below the new global is unchanged")
	})
}

// ---------------------------------------------------------------------------
// Item 9 — D20: a RAISE never rewrites and needs no confirmation.
// ---------------------------------------------------------------------------

// D20: global 200, agent A stored 500 (capped, D1). Raising to 300 (still
// below 500): the preview is empty, a PUT without confirmed_lowering
// succeeds, A's stored 500 is untouched and A stays capped and flagged at
// the new global (effective 300, source global, override 500, ignored true).
func TestPerformancePut_D20_RaiseWithCappedAgentNeedsNoConfirmation(t *testing.T) {
	api := newMTIAPI(t, "200", mtiAgent{id: "agent-a", own: 500})
	pw := mtiPreviewViaMux(t, api, "?value=300", true)
	require.Equal(t, http.StatusOK, pw.Code, "body: %s", pw.Body.String())
	pm := mtiDecode(t, pw.Body.Bytes())
	assert.EqualValues(t, 300, pm["value"])
	mtiAssertAgentChanges(t, pm["agents"], nil)

	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusOK, w.Code, "a raise needs no confirmed_lowering (D20); body: %s", w.Body.String())
	_, lowered := mtiDecode(t, w.Body.Bytes())["max_tool_iterations_lowered_agents"]
	assert.False(t, lowered, "a raise lowers nobody")
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api))
	assert.EqualValues(t, 500, mtiStoredOwn(t, api, "agent-a"), "D20: a raise never rewrites an agent")
	mtiAssertLimitFields(t, mtiGetAgent(t, api, "agent-a"), 300, "global", 500, true)
}

// ---------------------------------------------------------------------------
// Item 10 — silent-failure paths
// ---------------------------------------------------------------------------

// A store READ failure under the lock is an I/O failure, not drift: 500 with
// an error code that is not the drift code, the cause logged at ERROR, and
// nothing written. (The spec's failure codes name I/O failures
// max_tool_iterations_lowering_*; the exact code for a step-4 read failure is
// not pinned — see the report's spec-gap note — so the assertion is "500,
// a code, and not the drift answer".)
func TestPerformancePut_Lowering_ReadStateErrorIs500NotDrift(t *testing.T) {
	logs := captureMTILogs(t)
	api, s, extraAudit := newMTIScriptAPI(t)
	s.onRead = func(n int, id string) error {
		if n == 1 {
			return errors.New("mti injected read failure")
		}
		return nil
	}
	w := mtiPutPerf(t, api, mtiRollbackBody)

	require.Equal(t, http.StatusInternalServerError, w.Code, "a read failure must not masquerade as drift; body: %s", w.Body.String())
	m := mtiDecode(t, w.Body.Bytes())
	code, _ := m["code"].(string)
	assert.NotEmpty(t, code, "the 500 must carry a machine-readable code")
	assert.NotEqual(t, mtiDriftCode, code)
	_, hasPreview := m["preview"]
	assert.False(t, hasPreview, "no drift preview on an I/O failure")
	assert.Equal(t, 0, s.mutates(), "nothing written")
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api))
	assert.Empty(t, mtiSecurityChanges(t, api, extraAudit))
	logged := false
	for _, l := range logs.errorLines() {
		if strings.Contains(l, "mti injected read failure") {
			logged = true
		}
	}
	assert.True(t, logged, "the cause must be logged at ERROR; ERROR lines: %v", logs.errorLines())
}

// A List failure is never an empty 409. Two places: the pre-check (before
// the step-up token) and the fresh list a drift answer is built from.
func TestPerformancePut_Lowering_ListFailureIs500(t *testing.T) {
	t.Run("pre-check list fails", func(t *testing.T) {
		api, s, _ := newMTIScriptAPI(t)
		s.onList = func(int) error { return errors.New("mti injected list failure") }
		w := mtiPutPerf(t, api, mtiRollbackBody)
		require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
		assert.NotEqual(t, mtiDriftCode, mtiDecode(t, w.Body.Bytes())["code"])
		assert.Equal(t, 0, s.mutates())
		assert.EqualValues(t, 300, mtiDiskGlobal(t, api))
	})
	t.Run("fresh list for the drift answer fails", func(t *testing.T) {
		api, s, _ := newMTIScriptAPI(t)
		s.onRead = func(n int, id string) error {
			if n == 1 {
				s.concurrentWrite(t, id, func(ac *config.AgentConfig) { ac.MaxToolIterations = mtiOld[id] + 10 })
			}
			return nil
		}
		s.onList = func(n int) error {
			if n >= 3 { // 1 = pre-check, 2 = deciding check, 3+ = the fresh preview
				return errors.New("mti injected list failure")
			}
			return nil
		}
		w := mtiPutPerf(t, api, mtiRollbackBody)
		require.Equal(t, http.StatusInternalServerError, w.Code,
			"a drift answer whose fresh list cannot be read must be a 500, never a 409 with an empty preview; body: %s", w.Body.String())
		assert.Equal(t, 0, s.mutates())
		assert.EqualValues(t, 300, mtiDiskGlobal(t, api))
	})
}

// Edge case "Reload fails after the global is written" + the reload-failure
// code the SPA keys "saved but not applied" on.
func TestPerformancePut_MaxToolIterations_ReloadFailureCode(t *testing.T) {
	api := newMTIAPI(t, "200")
	api.agentLoop.SetReloadFunc(func() error { return fmt.Errorf("injected reload failure") })
	w := mtiPutPerf(t, api, `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "performance_reload_failed", mtiDecode(t, w.Body.Bytes())["code"])
	assert.EqualValues(t, 300, mtiDiskGlobal(t, api), "the value IS saved; only the apply failed")
}

// ---------------------------------------------------------------------------
// Item 11 — enum parity: Go constants ↔ contract enum ↔ generated .Valid()
// ---------------------------------------------------------------------------

func mtiContractEnum(t *testing.T, schema string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "components", "schemas", schema+".yaml"))
	require.NoError(t, err)
	var doc struct {
		Enum []string `yaml:"enum"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Enum, "instrument: %s.yaml must declare an enum", schema)
	return doc.Enum
}

func TestMaxToolIterationsEnums_GoConstantsMatchContract(t *testing.T) {
	t.Run("MaxToolIterationsSource", func(t *testing.T) {
		goValues := []string{
			string(config.MaxToolIterationsSourceGlobal),
			string(config.MaxToolIterationsSourceAgent),
		}
		for _, v := range goValues {
			assert.True(t, gen.MaxToolIterationsSource(v).Valid(), "Go constant %q is not a valid generated enum value", v)
		}
		assert.ElementsMatch(t, mtiContractEnum(t, "MaxToolIterationsSource"), goValues,
			"every contract value must have a Go constant and vice versa")
		assert.False(t, gen.MaxToolIterationsSource("bogus").Valid(), "instrument: Valid() rejects an unknown value")
	})
	t.Run("MaxToolIterationsSavedState", func(t *testing.T) {
		goValues := []string{
			string(config.MaxToolIterationsSavedOK),
			string(config.MaxToolIterationsSavedMissing),
			string(config.MaxToolIterationsSavedBelowMin),
			string(config.MaxToolIterationsSavedAboveMax),
		}
		for _, v := range goValues {
			assert.True(t, gen.MaxToolIterationsSavedState(v).Valid(), "Go constant %q is not a valid generated enum value", v)
		}
		assert.ElementsMatch(t, mtiContractEnum(t, "MaxToolIterationsSavedState"), goValues,
			"every contract value must have a Go constant and vice versa")
		assert.False(t, gen.MaxToolIterationsSavedState("bogus").Valid(), "instrument")
	})
}

// ---------------------------------------------------------------------------
// Item 7 (gateway leg) — the env import happens at boot only, never on the
// config refresh that follows a REST write (D6: copy once, then ignore).
// ---------------------------------------------------------------------------

// mtiAllLogText returns everything both log paths captured (slog + the
// pkg/logger file sink), at every level.
func mtiAllLogText(c *mtiLogCapture) string {
	c.mu.Lock()
	all := c.buf.String()
	c.mu.Unlock()
	if raw, err := os.ReadFile(c.file); err == nil {
		all += "\n" + string(raw)
	}
	return all
}

// mtiEnvImportLine is the fixed prefix of the INFO line the one-time import
// logs when it copies the retired env var into config.json (D6/D17 "copied
// once" notice). Its presence is the observable sign an import ran.
const mtiEnvImportLine = "copied " + envMaxToolIterations + " into config.json"

// D6 "copy once, then ignore": the env import is a BOOT action. The property
// under test is "an env var still set does not overwrite the admin's saved
// value on a refresh load". The oracle is the saved value and the absence of
// an import — never the absence of the marker: PUT /performance writes the
// marker itself (D6, TestGateFix_PerformancePUT_WritesImportMarker).
//
// newMTIAPI's own load is this config path's boot load, taken with the env
// var UNSET, so every later load in the test is a refresh.
func TestEnvImport_NotOnRefresh_AdminSaveWins(t *testing.T) {
	t.Run("instrument: a boot load with the env var set does import and log it", func(t *testing.T) {
		logs := captureMTILogs(t)
		dir := t.TempDir()
		p := filepath.Join(dir, "config.json")
		cfgJSON := `{"version":` + fmt.Sprint(config.CurrentVersion) +
			`,"agents":{"defaults":{"workspace":` + mtiJSON(t, dir) + `,"max_tool_iterations":200}},"providers":[]}`
		require.NoError(t, os.WriteFile(p, []byte(cfgJSON), 0o600))
		t.Setenv(envMaxToolIterations, "80")
		cfg, err := config.LoadConfig(p)
		require.NoError(t, err)
		require.Equal(t, 80, cfg.Agents.Defaults.MaxToolIterations,
			"instrument: a boot load imports the env value (D5), so the refresh legs below can see an import")
		require.Contains(t, mtiAllLogText(logs), mtiEnvImportLine,
			"instrument: the log capture sees the import notice")
	})

	t.Run("admin saves 150 in Settings while the env var is set: 150 stays", func(t *testing.T) {
		logs := captureMTILogs(t)
		api := newMTIAPI(t, "200")
		t.Setenv(envMaxToolIterations, "80")
		w := mtiPutPerf(t, api, `{"max_tool_iterations":150}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.EqualValues(t, 150, mtiDecode(t, w.Body.Bytes())["max_tool_iterations"])
		assert.EqualValues(t, 150, mtiDiskGlobal(t, api), "the admin's save stays on disk")
		assert.EqualValues(t, 150, mtiGetPerf(t, api)["max_tool_iterations"], "the admin's save stays in force")
		assert.NotContains(t, mtiAllLogText(logs), mtiEnvImportLine, "no env import ran on the refresh")
		// The admin's PUT itself writes the marker with the global (D6,
		// TestGateFix_PerformancePUT_WritesImportMarker), so the retired env
		// var can never overwrite this save on a later boot.
		assert.Equal(t, true, mtiDiskDefaults(t, api)["max_tool_iterations_env_imported"],
			"the admin save ends the one-time env import (D6)")
	})

	// The leg that can see an import: a Settings write of ANOTHER field
	// refreshes the config without touching the global or writing the
	// marker, so only the boot-only rule keeps the env value out. The saved
	// admin value (200) must stay on disk and in force, and nothing may be
	// imported (no marker written, no import notice).
	t.Run("a refresh after a sibling write does not import over the saved value", func(t *testing.T) {
		logs := captureMTILogs(t)
		api := newMTIAPI(t, "200")
		t.Setenv(envMaxToolIterations, "80")
		w := mtiPutPerf(t, api, `{"goal_max_rounds":7}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.EqualValues(t, 200, mtiDiskGlobal(t, api), "the saved global is not overwritten by the env value 80")
		assert.EqualValues(t, 200, mtiGetPerf(t, api)["max_tool_iterations"], "the saved global stays in force")
		_, marker := mtiDiskDefaults(t, api)["max_tool_iterations_env_imported"]
		assert.False(t, marker, "no import ran, so nothing wrote the import marker")
		assert.NotContains(t, mtiAllLogText(logs), mtiEnvImportLine, "no env import ran on the refresh")
	})
}

// ---------------------------------------------------------------------------
// Item 8 (REST leg) — D21: PUT /api/v1/config is closed for the global and
// its import marker.
// ---------------------------------------------------------------------------

func mtiPutConfig(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleConfig(w, r)
	return w
}

func mtiDiskDefaults(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	agents, _ := m["agents"].(map[string]any)
	d, _ := agents["defaults"].(map[string]any)
	return d
}

func TestUpdateConfig_RefusesGlobalMaxToolIterations(t *testing.T) {
	const seeded = `250,"max_tool_iterations_env_imported":true`
	for _, tc := range []struct{ name, body string }{
		{"nested global", `{"agents":{"defaults":{"max_tool_iterations":5}}}`},
		{"dotted global", `{"agents.defaults.max_tool_iterations":5}`},
		{"nested marker", `{"agents":{"defaults":{"max_tool_iterations_env_imported":false}}}`},
		{"dotted marker", `{"agents.defaults.max_tool_iterations_env_imported":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMTIAPI(t, seeded)
			before := mtiSnapshotFiles(t, api)
			w := mtiPutConfig(t, api, tc.body)
			// Existing contract for a refused config path (blocked_paths.go):
			// 403 "<path> is a blocked path — use the dedicated endpoint".
			require.Equal(t, http.StatusForbidden, w.Code, "D21: the side door is closed; body: %s", w.Body.String())
			assert.Contains(t, mtiErrorText(t, w), "agents.defaults.max_tool_iterations")
			assert.Equal(t, before, mtiSnapshotFiles(t, api), "a refused PUT leaves config.json byte-identical")
			assert.EqualValues(t, 250, mtiGetPerf(t, api)["max_tool_iterations"])
		})
	}
	t.Run("other agents.defaults fields keep the global and the marker", func(t *testing.T) {
		api := newMTIAPI(t, seeded)
		w := mtiPutConfig(t, api, `{"agents":{"defaults":{"max_tokens":8192}}}`)
		require.Equal(t, http.StatusOK, w.Code, "a legitimate agents.defaults write is still allowed; body: %s", w.Body.String())
		d := mtiDiskDefaults(t, api)
		assert.EqualValues(t, 8192, d["max_tokens"], "the requested field landed")
		assert.EqualValues(t, 250, d["max_tool_iterations"], "the global survives a sibling write")
		assert.Equal(t, true, d["max_tool_iterations_env_imported"], "the import marker survives a sibling write")
		assert.EqualValues(t, 250, mtiGetPerf(t, api)["max_tool_iterations"])
	})
}
