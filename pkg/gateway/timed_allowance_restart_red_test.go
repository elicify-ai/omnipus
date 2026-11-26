//go:build goolm && stdjson

package gateway

// Part H (round 10), spec scenario S-113 and its end-to-end row ST-26 (test 52,
// TestSkillApproval_TimedAllowanceReachesScheduledRun): "A restart ends the time allowance but
// keeps the version approval", on the actual gateway composition root.
//
// Why this is its own file. The two placeholders that stood at the end of
// timed_allowance_scheduled_red_test.go were unconditional BLOCKED failures written while no
// restart could be driven. S-113 is a different scenario from S-109 (trust Ask, an UNVERIFIED
// skill with its revision R1 not approved, one agent with an own Deny, a combined card, a restart),
// and the restart is the point: the S-122 fixture (timed_allowance_native_lifecycle_red_test.go,
// newNativeGatewayOver) already stops and starts the real composition root over one home, one
// credential key and one scripted model edge, so the same fixture carries this scenario. What is
// real: the approval registry and handler the gateway wired, the skill tool, the unattended
// classification of a task run (TaskExecutor.ExecuteTask), the agent store, the trust file and the
// audit log. What is injected: the scripted model (no paid call) and the allowance clock.
//
// What this does not cover. The restart is the composition root's stop and start over the same
// home (setupAndStartServices, as S-122): the boot preamble (data model init and the one-time
// skill-permissions upgrade, C-19) is not repeated here. S-110 is the scenario that owns it; its
// test, TestSkillPermissionsUpgrade_StopAtEveryStep, is still gated on a seam no test binds
// (reported in the Part H pack), so nothing pins the upgrade at boot at this head. This file does
// not certify that the gateway STARTS the allowance expiry sweep either (see
// timed_allowance_native_lifecycle_red_test.go).
// Mutation probes and GREEN acceptance belong to independent CHECK.
//
// Oracles come from the spec: S-113's Given/When/Then, C-12/C-24 (which checks a card answers and
// which actions it offers), C-26 (the allowance is in memory and a restart ends it) and D10 rows 15
// and 30/31. C-11 row E1 says only that `deny` is "refused naming the setting"; that fixes no exact
// sentence for an own Deny, so Ray's refusal is asserted to name the skill and Deny, and that spec
// gap is reported.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	s113Skill    = "notes-helper"
	s113R1Marker = "F18-NOTES-R1-INSTRUCTIONS-9Z"
	s113R2Marker = "F18-NOTES-R2-INSTRUCTIONS-7Q"
)

// s113Gateway is the S-113 Given on the real composition root: trust Ask, unverified notes-helper
// (a plain folder, never hash-verified) at global Ask with revision R1 not approved, granted to Mia
// (no own value) and to Ray (own value Deny), Auto-approve off.
type s113Gateway struct {
	fx     *nativeGatewayFixture
	script *scheduledAllowanceScript
	home   string
}

func newS113Gateway(t *testing.T) *s113Gateway {
	t.Helper()
	require.Contains(t, nativeSkillR1, s113R1Marker, "instrument: the marker names the instructions of R1")
	require.Contains(t, nativeSkillR2, s113R2Marker, "instrument: the marker names the instructions of R2")
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	dir := filepath.Join(home, "skills", s113Skill)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(nativeSkillR1), 0o600))
	cfg := config.DefaultConfig()
	cfg.Gateway.Host, cfg.Gateway.Port = "127.0.0.1", 0
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scheduled-allowance-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = nil
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.SkillTrust = config.SkillTrustLevel("ask")
	cfg.Sandbox.ToolPolicies["Skill"] = "allow"
	cfg.Sandbox.AuditLog = true
	cfg.Tools.Manifest.Compressed = false // Discovery is not the permission boundary.
	cfg.Planning.GoalMaxRounds = 1        // a task run ends after its one scripted tool call (runRealTask).
	require.NoError(t, json.Unmarshal([]byte(`{"skill_policies":{"`+s113Skill+`":"ask"}}`), &cfg.Sandbox))
	store := agentstore.New(home)
	for _, id := range []string{"mia", "ray"} {
		record := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Skills: []string{s113Skill}, Tools: coreagent.NewCustomAgentToolsCfg()}
		require.NoError(t, store.Create(id, &record), "real persistence binds the actual installed generation")
		if id == "ray" {
			plantOwnSkillPolicy(t, home, id, s113Skill, "deny")
		}
		persisted, err := store.Get(id)
		require.NoError(t, err)
		require.NotEmpty(t, persisted.SkillGrants[s113Skill], "actual generation grant, not a name-only allowlist")
		cfg.Agents.List = append(cfg.Agents.List, *persisted)
	}
	script := &scheduledAllowanceScript{skill: s113Skill}
	return &s113Gateway{fx: newNativeGatewayOver(t, home, cfg, script), script: script, home: home}
}

// plantOwnSkillPolicy writes an agent's own skill value at the carrier the store keeps, the
// top-level tools.skills.policies of the flat entity record (tests/helpers/agentrecord). The
// REST request name tools_cfg is not a stored key, so a value planted there would be dead data;
// the read-back through the independent reader proves the Given sits where the product reads it.
func plantOwnSkillPolicy(t *testing.T, home, id, key, value string) {
	t.Helper()
	path := agentrecord.Path(home, id)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var literal map[string]any
	require.NoError(t, json.Unmarshal(raw, &literal))
	toolsMember, _ := literal["tools"].(map[string]any)
	if toolsMember == nil {
		toolsMember = map[string]any{}
		literal["tools"] = toolsMember
	}
	toolsMember["skills"] = map[string]any{"policies": map[string]string{key: value}}
	raw, err = json.Marshal(literal)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	require.Equal(t, map[string]any{key: value}, agentrecord.Policies(t, home, id, agentrecord.Skills),
		"the Given is planted at the carrier the store keeps, not in a place nothing reads")
}

// harness wraps a started gateway in the scheduled-run harness: its real task runs, its real chat
// and its model-request reader. The harness is given the gateway's own loop, registry and REST
// handler; nothing in it decides a permission.
func (g *s113Gateway) harness(al *agent.AgentLoop, rs *services) *scheduledAllowanceHarness {
	return &scheduledAllowanceHarness{
		al: al, api: rs.restAPIRef, reg: rs.restAPIRef.approvalReg,
		script: g.script, home: g.home, skill: s113Skill, marker: s113R1Marker,
	}
}

// requireGiven makes the Given an observation: the real boot bound both grants, the stored
// permissions are what the scenario states (read raw from disk, not through the product's view),
// and R1 is not approved for a fresh reader.
func (g *s113Gateway) requireGiven(t *testing.T, al *agent.AgentLoop) {
	t.Helper()
	for _, id := range []string{"mia", "ray"} {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok, "fixture: %s is on the real roster", id)
		_, found := inst.ContextBuilder.ResolveSkillFullForWorkspace("", s113Skill)
		require.True(t, found, "fixture: the real boot, not the test, bound %s's grant", id)
	}
	stored := g.storedSandbox(t)
	assert.Equal(t, "ask", stored.SkillTrust, "Given: trust Ask")
	assert.Equal(t, "ask", stored.SkillPolicies[s113Skill], "Given: notes-helper at global Ask")
	assert.Empty(t, agentrecord.Policies(t, g.home, "mia", agentrecord.Skills), "Given: Mia has no own value")
	assert.Equal(t, map[string]any{s113Skill: "deny"}, agentrecord.Policies(t, g.home, "ray", agentrecord.Skills), "Given: Ray's own value is Deny")
	assertFreshSkillTrust(t, g.home, s113Skill, false)
}

type s113Sandbox struct {
	SkillTrust    string            `json:"skill_trust"`
	SkillPolicies map[string]string `json:"skill_policies"`
}

// storedSandbox reads the stored global trust and skill permissions straight from config.json.
func (g *s113Gateway) storedSandbox(t *testing.T) s113Sandbox {
	t.Helper()
	var stored struct {
		Sandbox s113Sandbox `json:"sandbox"`
	}
	require.NoError(t, json.Unmarshal(readNativeConfig(t, g.home), &stored))
	return stored.Sandbox
}

// answerCombinedCard is S-113's first When: Mia's chat raises the one combined card (both checks
// open, C-12), and it is answered "Approve this version and allow for 1 day" over the real HTTP
// handler. It holds the first Then bullet: applied.trust_approved, applied.timed_allowance and
// applied.call_released true, expires_at exactly one day after the answer, the waiting call
// released with the real instructions.
func (g *s113Gateway) answerCombinedCard(t *testing.T, h *scheduledAllowanceHarness, clock *scheduledAllowanceClock) (*approvalEntry, gen.ToolApprovalResponse) {
	t.Helper()
	entry, done := h.pendingFromRealChat(t)
	card := approvalSkill(entry)
	require.NotNil(t, card, "C-9: a skill card names its skill")
	assert.Equal(t, s113Skill, card.Key)
	assert.True(t, card.Unverified, "C-9: the card names the skill as unverified")
	assert.ElementsMatch(t, []string{"permission", "trust"}, card.OpenChecks, "C-12: both checks open give one combined card")
	actions, durations := offeredActions(entry)
	assert.Equal(t, []string{"approve_version", "approve_version_allow_for", "deny"}, actions)
	assert.Equal(t, []string{"1h", "3h", "1d"}, durations)
	end := clock.wallNow().Add(24 * time.Hour)
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"1d"}`)
	// assertNativeSkillAnswer holds applied == {call_released, trust_approved, timed_allowance: all
	// true}, a present expires_at and grant_recorded true (S-113 Then, first bullet).
	response := assertNativeSkillAnswer(t, w, entry, true)
	assert.True(t, end.Equal(*response.ExpiresAt), "expires_at is one day after the answer: want %s, got %s", end, response.ExpiresAt)
	select {
	case err := <-done:
		require.NoError(t, err, "the original waiting call is released")
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting call was not released by the answer")
	}
	h.assertModelResult(t, true)
	// One skill.trust_approved (R1) entry and one allowance entry (Mia, notes-helper, 1d) exist.
	assertNativeSkillAudit(t, &nativeSkillHarness{timedCardHarness: &timedCardHarness{api: h.api}}, entry, "1d", response.ExpiresAt)
	return entry, response
}

// scheduledDenied runs one real task of owner and requires the S-113 refusal for an own Deny: the
// run is refused, nothing of the skill reaches any model request, and the refusal the model
// receives names the skill and Deny. C-11 row E1 says only "refused naming the setting", so no
// exact sentence is asserted (reported to team-lead as a spec gap).
//
// Where the refusal is read. S-113 says the task "is refused naming his own Deny" and does not say
// where that refusal is shown, so the test reads it at the one place every run exposes and the
// product cannot bypass: the tool result of the Skill call in the model's next request
// (modelToolResult). The first version of this helper read the persisted transcript instead and
// failed in the run of 2026-11-26: for a denied load the persisted entries held only the task
// prompt, an empty tool-call entry and the final answer, none naming the skill or Deny. That is
// recorded in the Part H pack as an observation, not asserted either way, because no spec
// sentence requires the refusal to be persisted.
func (h *scheduledAllowanceHarness) scheduledDenied(t *testing.T, owner string) {
	t.Helper()
	texts := h.scheduledTexts(t, owner, false)
	refusal := h.modelToolResult(t)
	t.Logf("observed: %s's refusal as the model received it: %s", owner, refusal)
	assert.Contains(t, refusal, h.skillKey(), "S-113: %s's refusal names the skill; the model received %q", owner, refusal)
	assert.Contains(t, refusal, "Deny", "S-113: %s's refusal names the setting that refuses him, Deny; the model received %q", owner, refusal)
	// The Given has trust Ask, so the sentence that names the trust setting ("`notes-helper` is
	// unverified, and unverified skills are set to Deny in Skills & Tools.", spec S-29, the
	// trust-Deny load) would name the wrong setting for Ray: his own Deny is what refuses him.
	assert.NotContains(t, refusal, "unverified", "S-113: %s's refusal names his own Deny, not the trust setting (trust is Ask)", owner)
	// Refused by the permission, not skipped as an unattended run (the text Mia gets after the restart).
	assert.NotContains(t, refusal, scheduledWeatherRefusal, "S-113: %s is refused by the permission, not skipped as unattended", owner)
	assert.NotContains(t, texts, scheduledWeatherRefusal, "S-113: %s is refused by the permission, not skipped as unattended", owner)
}

// assertKeptAfterRestart holds what S-113 says a restart leaves: the R1 trust approval and the
// two audit entries of the answer; and what ST-26 says the whole run never touches: the global
// value stays Ask, the agents' own values are as given, and no policy-change audit entry exists.
func (g *s113Gateway) assertKeptAfterRestart(t *testing.T, h *scheduledAllowanceHarness, entry *approvalEntry, response gen.ToolApprovalResponse) {
	t.Helper()
	assertFreshSkillTrust(t, g.home, s113Skill, true)
	assertNativeSkillAudit(t, &nativeSkillHarness{timedCardHarness: &timedCardHarness{api: h.api}}, entry, "1d", response.ExpiresAt)
	assert.Equal(t, "ask", g.storedSandbox(t).SkillPolicies[s113Skill], "the global permission is still Ask")
	assert.Empty(t, agentrecord.Policies(t, g.home, "mia", agentrecord.Skills))
	assert.Equal(t, map[string]any{s113Skill: "deny"}, agentrecord.Policies(t, g.home, "ray", agentrecord.Skills))
	for _, row := range readAllAuditEntries(t, filepath.Join(g.home, "system")) {
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "sandbox.skill_policies", "ST-26: no policy-change audit entry exists")
	}
}

// assertChatCardsAfterRestart is S-113's last Then bullet: in a chat after the restart Mia gets a
// permission card only, not a trust card, until a file of notes-helper changes (R2), after which
// she gets a combined card again. Each card is denied, so nothing is stored by it.
func (g *s113Gateway) assertChatCardsAfterRestart(t *testing.T, h *scheduledAllowanceHarness) {
	t.Helper()
	writer := skills.NewSkillWriter(filepath.Join(g.home, "skills"))
	r1, err := writer.SkillRevision(s113Skill)
	require.NoError(t, err)

	entry, done := h.pendingFromRealChat(t)
	card := approvalSkill(entry)
	require.NotNil(t, card)
	assert.Equal(t, []string{"permission"}, card.OpenChecks, "the R1 trust approval is still stored, so the card asks for permission only")
	assert.Equal(t, r1, card.Revision)
	actions, durations := offeredActions(entry)
	assert.Equal(t, []string{"allow_once", "allow_for", "deny"}, actions)
	assert.Equal(t, []string{"1h", "3h", "1d"}, durations)
	g.denyCard(t, h, entry, done)

	require.NoError(t, os.WriteFile(filepath.Join(g.home, "skills", s113Skill, "SKILL.md"), []byte(nativeSkillR2), 0o600))
	r2, err := writer.SkillRevision(s113Skill)
	require.NoError(t, err)
	require.NotEqual(t, r1, r2, "instrument: a changed file is a new revision")
	h.marker = s113R2Marker
	entry, done = h.pendingFromRealChat(t)
	card = approvalSkill(entry)
	require.NotNil(t, card)
	assert.ElementsMatch(t, []string{"permission", "trust"}, card.OpenChecks, "after R2 she gets a combined card again")
	assert.Equal(t, r2, card.Revision)
	actions, durations = offeredActions(entry)
	assert.Equal(t, []string{"approve_version", "approve_version_allow_for", "deny"}, actions)
	assert.Equal(t, []string{"1h", "3h", "1d"}, durations)
	g.denyCard(t, h, entry, done)
	assertFreshSkillTrust(t, g.home, s113Skill, false)
}

// denyCard answers the card Deny over the real handler and requires the waiting call to end with
// the refusal: the instructions of the harness's current revision reach no model request.
func (g *s113Gateway) denyCard(t *testing.T, h *scheduledAllowanceHarness, entry *approvalEntry, done <-chan error) {
	t.Helper()
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	select {
	case err := <-done:
		require.NoError(t, err, "the refused call ends the turn without an error")
	case <-time.After(5 * time.Second):
		t.Fatal("the denied call did not finish")
	}
	h.assertModelResult(t, false)
}

func testScheduledAllowanceGatewayRestart(t *testing.T) {
	g := newS113Gateway(t)
	al, rs, stop := g.fx.start()
	h := g.harness(al, rs)
	g.requireGiven(t, al)
	nativebinding.Require(t, "pkg/agent", injectScheduledAllowanceClock,
		"C-26/S-113: named loop-to-ApprovalGrantStore wall/monotonic input; actual card/handler/store are not replaceable")
	clock := &scheduledAllowanceClock{wall: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)}
	injectScheduledAllowanceClock(al, clock.wallNow, clock.elapsedNow)

	entry, response := g.answerCombinedCard(t, h, clock)
	// Before the restart Mia's task loads it with no card, and Ray's own Deny refuses his (ST-26).
	h.scheduled(t, "mia", true)
	h.scheduledDenied(t, "ray")
	// Instrument: the allowance is still live at the instant of the stop (the injected clock reads
	// inside its day), so only the restart can explain its absence afterwards.
	require.True(t, al.ApprovalGrants().IsSkillAllowed("mia", s113Skill), "the allowance is live just before the restart")
	require.False(t, al.ApprovalGrants().IsSkillAllowed("ray", s113Skill), "the allowance is Mia's alone")
	require.True(t, clock.wallNow().Before(*response.ExpiresAt), "the restart, not the clock, ends it: the day has not passed")

	stop()
	g.fx.reopenCredentials()
	after, restarted, _ := g.fx.start()
	h = g.harness(after, restarted)
	// After the restart the allowance has ended (C-26: it lives in memory only): Mia's task skips
	// it with the unattended text while the R1 approval is still stored; Ray is still refused by
	// his own Deny.
	assert.False(t, after.ApprovalGrants().IsSkillAllowed("mia", s113Skill), "S-113: a restart ends the time allowance")
	h.scheduled(t, "mia", false)
	h.scheduledDenied(t, "ray")
	g.assertKeptAfterRestart(t, h, entry, response)
	g.assertChatCardsAfterRestart(t, h)
}
