//go:build goolm && stdjson

package gateway

// Fix round 4, part F: C-19 "When the new rules apply" and "A failed agent write"
// (spec at commit b27c64109): a waiting agent keeps its planned values.
//
// The spec text this file pins:
//
//	"From the moment the snapshot is durable, policy is resolved from the plan of
//	step 2, not from the files being rewritten: an agent still pending or
//	in_flight gets exactly its planned overrides."
//	"A failed agent write (for example a read-only file) leaves that agent pending
//	and records failures[<id>] = write_failed in the ledger; the run reports an
//	Error naming it; the plan keeps applying to it."
//	Step 4: "its record is written (skills.policies gains the planned overrides ...
//	and its old Skill entry is removed)".
//
// Why this file exists: the architect found no code outside pkg/skillperm that
// reads the plan, and the brief asked for a proof either way. The search, run on
// the backend head cc616bf74 (identical production code to b27c64109):
//
//	rg -n 'skillperm\.' --glob '!*_test.go' pkg cmd internal
//	  pkg/agent/project_skill_keys.go:49  skillperm.HealIfUpgraded(...)
//	  pkg/gateway/skills_boot.go:310      skillperm.SelfHeal(...)
//	  pkg/gateway/gateway.go:650          skillperm.Run(...)   (a failure is only logged)
//
// and the turn-time readers of a per-agent skill override take it from the agent's
// RECORD (pkg/tools/skill_menu_levels.go::buildSkillMenuView reads
// agentstore.Get(agentID).Tools.Skills; pkg/agent/skill_load_policy.go reads the
// roster entry the boot loaded from that record). A search is not a proof of
// behaviour, so this test drives the real thing.
//
// What is real: the old-shape files (literal old values, written below), the
// real skillperm.Run with its own write hook as the only fault, the real
// config loader and roster loader the gateway boot uses
// (config.LoadConfig, populateAgentsListFromEntityStoreStrict), the real agent
// loop, the real skill menu, the real Skill decision and the real approval
// registry. Only the model is scripted (nativeConsentScript).
//
// Fixture stand-ins: test-agent stands in for the spec's Mia; notes-helper (the
// native-harness skill) stands in for plan. The old values are literal inputs:
// the old global Skill value is allow and trust is the legacy allow_all, so the
// only way the waiting agent can be asked or refused is its own planned
// override. Where a row's expected outcome is a card, the card must be the
// skill's own (C-9/C-24: it carries the skill facts and the open permission
// check): a card that comes from some other gate is not the planned value.
//
// Rows (old per-agent Skill value; whether the agent's record write is faulted):
//
//	ask  / written   control: the plan was written; the agent asks, listed in menu
//	ask  / faulted   waiting agent: must ask exactly the same way
//	deny / written   control: the plan was written; refused, absent from the menu
//	deny / faulted   waiting agent: must be refused and absent from the menu
//
// Expected values come from the spec and from the literal inputs, never from the
// run: ask -> the Skill call raises the skill card and nothing loads before an
// answer; deny -> no card, the instructions never reach the model, and the
// skill is not listed in the menu (C-11: "a Deny removes the skill from the
// menu"); an Ask skill is listed (D9 row 2 pattern).
//
// RED reason, from the saved run ta/fix4-go-fix4f-08.log (head cc616bf74, direct
// exit 1, the two "record written" controls PASS, the two "record write failed"
// rows FAIL; fix4f-07 was a build failure of this file and is not a result):
//
//	ask / faulted:  a card IS raised, but not the skill's own: the first check
//	  (a card exists) passes and the second fails with "Expected value not to
//	  be nil ... C-9/C-24: an Ask on a skill is the skill's card, with the
//	  skill facts" (snap.Skill is nil). Inferred mechanism: the unchanged
//	  record still carries the old per-agent Skill entry, so the generic tool
//	  gate asks, and the plan (skills.policies notes-helper: ask) is not read.
//	deny / faulted: the call is still refused (log: tool_exec_skipped,
//	  "permission_denied (mid-turn policy change)"), so the no-card and
//	  nothing-reaches-the-model checks PASS, but by a gate other than the
//	  planned value; the planned Deny is not applied where the spec shows it
//	  first: the skill is still listed in the agent's menu ("Should be false
//	  ... C-11: a Deny removes the skill from the menu").
//
// So the run answers the brief's "prove it either way": at cc616bf74 a waiting
// agent does NOT get its planned values from anything outside pkg/skillperm;
// the backend must make turn-time resolution use the plan for a pending or
// in_flight agent (C-19). The controls show the written world is right.
//
// Mutation probes (deferred to CHECK, not run here): resolve a pending agent
// from its record only (what the run above shows today's code does); apply the
// plan to a finished agent as well; let a waiting agent ask when its plan says
// deny.
//
// Not covered, on purpose: the in_flight state (a stop between the ledger's
// in_flight mark and the record write is the stop-boundary test's job,
// TestSkillPermissionsUpgrade_StopAtEveryStep); project skills in the plan
// (this file uses an installed skill only).

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	waitingAgentID      = "test-agent"
	waitingSkillKey     = "notes-helper"
	waitingMenuText     = "Native consent fixture." // the description line of nativeSkillR1
	waitingInstructions = "F18-NOTES-R1-INSTRUCTIONS-9Z"
	waitingWriteFault   = "open test-agent.json: read-only file system"
)

func waitingReadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)
	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object), "parse %s", path)
	return object
}

func waitingChild(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	child, ok := parent[key].(map[string]any)
	require.True(t, ok, "%s must be an object, got %#v", key, parent[key])
	return child
}

// seedOldShapeHome writes the home of an install that predates the redesign:
// the old global Skill value allow, the legacy trust word allow_all, no
// skill_policies map, one installed skill and one custom agent whose own old
// Skill value is oldAgentSkill. The old values are literal inputs; the
// assertions at the end read them back so the fixture cannot drift.
func seedOldShapeHome(t *testing.T, oldAgentSkill string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	root := filepath.Join(home, "skills", waitingSkillKey)
	require.NoError(t, os.MkdirAll(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(nativeSkillR1), 0o600))

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "native-consent-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = nil
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.AuditLog = true
	cfg.Tools.Manifest.Compressed = false
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var literal map[string]any
	require.NoError(t, json.Unmarshal(raw, &literal))
	sandbox := waitingChild(t, literal, "sandbox")
	waitingChild(t, sandbox, "tool_policies")["Skill"] = "allow" // the old global Skill value
	sandbox["skill_trust"] = "allow_all"                         // the legacy trust word (C-3)
	for _, newShape := range []string{"skill_policies", "skill_visibility", "migrations"} {
		delete(sandbox, newShape) // keys that exist only after the upgrade
	}
	raw, err = json.Marshal(literal)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))

	record := config.AgentConfig{
		ID: waitingAgentID, Name: waitingAgentID, Type: config.AgentTypeCustom,
		Skills: []string{waitingSkillKey}, Tools: coreagent.NewCustomAgentToolsCfg(),
	}
	record.Tools.Builtin.Policies["Skill"] = config.ToolPolicy(oldAgentSkill)
	require.NoError(t, agentstore.New(home).Create(waitingAgentID, &record))

	// Instrument: the old values are on disk exactly as the row states them.
	oldConfig := waitingReadJSON(t, filepath.Join(home, "config.json"))
	oldSandbox := waitingChild(t, oldConfig, "sandbox")
	require.Equal(t, "allow", waitingChild(t, oldSandbox, "tool_policies")["Skill"], "fixture: old global Skill value")
	require.Equal(t, "allow_all", oldSandbox["skill_trust"], "fixture: legacy trust word")
	require.NotContains(t, oldSandbox, "skill_policies", "fixture: an old install has no per-skill map")
	oldRecord := waitingReadJSON(t, waitingAgentRecordFile(home))
	oldPolicies := waitingChild(t, waitingChild(t, waitingChild(t, oldRecord, "tools"), "builtin"), "policies")
	require.Equal(t, oldAgentSkill, oldPolicies["Skill"], "fixture: the agent's old Skill value")
	return home
}

func waitingAgentRecordFile(home string) string {
	return filepath.Join(home, "entities", "agents", waitingAgentID+".json")
}

// bootAfterUpgrade does what the gateway does after skillperm.Run: load the
// config with the production loader, load the roster from the agent records
// with the production roster loader, build the real loop with the scripted
// model and bind the agent's grants.
func bootAfterUpgrade(t *testing.T, home string) *nativeSkillHarness {
	t.Helper()
	cfg, err := config.LoadConfig(filepath.Join(home, "config.json"))
	require.NoError(t, err, "fixture: the production loader reads the upgraded config")
	require.NoError(t, populateAgentsListFromEntityStoreStrict(cfg, home), "fixture: the production roster loader")
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	script := &nativeConsentScript{tool: "Skill", args: map[string]any{"name": waitingSkillKey}}
	al := mustAgentLoop(t, cfg, msgBus, script)
	for _, record := range cfg.Agents.List {
		inst, ok := al.GetRegistry().GetAgent(record.ID)
		require.True(t, ok, "fixture: %s is registered", record.ID)
		inst.ContextBuilder.WithSkillAllowlist(record.Skills).WithSkillBindings(record.SkillGrants)
	}
	reg := newApprovalRegistryV2(64, 30*time.Second)
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	t.Cleanup(handler.Wait)
	return &nativeSkillHarness{
		timedCardHarness: &timedCardHarness{api: &restAPI{agentLoop: al, homePath: home, approvalReg: reg, auditor: al.AuditLogger()}, reg: reg, handler: handler},
		script:           script,
	}
}

// waitingCall runs one real Skill call by the agent and reports what happened:
// the card it raised (nil when it raised none, in which case the call ran to
// its end and results holds every tool result the model then saw).
func waitingCall(t *testing.T, h *nativeSkillHarness) (entry *approvalEntry, results []string, run nativeSkillRun) {
	t.Helper()
	run = h.begin(t)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if rows := h.reg.pendingApprovals(); len(rows) > 0 {
			require.Len(t, rows, 1, "one waiting call raises one card")
			return rows[0], nil, run
		}
		select {
		case err := <-run.done:
			require.NoError(t, err, "fixture: a real turn error is not a policy decision")
			for _, request := range h.script.captured(run.prompt) {
				for _, message := range request {
					if message.Role == "tool" {
						results = append(results, message.Content)
					}
				}
			}
			return nil, results, run
		case <-timer.C:
			t.Fatal("fixture UNVERIFIED: the Skill call reached neither a card nor completion")
		case <-tick.C:
		}
	}
}

// everythingTheModelWasSent is the JSON of every request the scripted model
// received in this home, whatever the prompt.
func everythingTheModelWasSent(t *testing.T, h *nativeSkillHarness) string {
	t.Helper()
	h.script.mu.Lock()
	defer h.script.mu.Unlock()
	var all strings.Builder
	for _, requests := range h.script.requests {
		for _, request := range requests {
			raw, err := json.Marshal(request)
			require.NoError(t, err)
			all.Write(raw)
		}
	}
	return all.String()
}

func TestSkillUpgrade_WaitingAgentKeepsItsPlannedValues(t *testing.T) {
	rows := []struct {
		name    string
		old     string // the agent's old per-agent Skill value (literal input)
		faulted bool   // the upgrade's write of the agent's record fails
	}{
		{"ask, record written (control)", "ask", false},
		{"ask, record write failed: the waiting agent", "ask", true},
		{"deny, record written (control)", "deny", false},
		{"deny, record write failed: the waiting agent", "deny", true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			home := seedOldShapeHome(t, row.old)
			before, err := os.ReadFile(waitingAgentRecordFile(home))
			require.NoError(t, err)

			var fault func(agent string) error
			if row.faulted {
				fault = func(agent string) error {
					if agent == waitingAgentID {
						return errors.New(waitingWriteFault)
					}
					return nil
				}
			}
			runErr := runSkillUpgrade(t, home, fault)
			after, err := os.ReadFile(waitingAgentRecordFile(home))
			require.NoError(t, err)
			if row.faulted {
				require.Error(t, runErr, "C-19: a failed agent write makes the run report an Error")
				require.ErrorContains(t, runErr, waitingAgentID, "the Error names the agent")
				require.Equal(t, string(before), string(after), "fixture: the failed write left the agent's record as it was")
			} else {
				require.NoError(t, runErr, "fixture: an unfaulted upgrade finishes")
				planned := waitingChild(t, waitingChild(t, waitingReadJSON(t, waitingAgentRecordFile(home)), "tools"), "skills")
				assert.Equal(t, map[string]any{waitingSkillKey: row.old}, waitingChild(t, planned, "policies"),
					"C-19 step 4: the written record carries the planned override, the old Skill value for the granted skill")
			}
			// Instrument: the config step (step 3) ran in both worlds, so the global
			// per-skill value is the old global Skill value and the only way the
			// agent is asked or refused is its own planned override.
			sandbox := waitingChild(t, waitingReadJSON(t, filepath.Join(home, "config.json")), "sandbox")
			require.Equal(t, "allow", waitingChild(t, sandbox, "skill_policies")[waitingSkillKey],
				"C-19 step 3: each skill's global value is the old global Skill value")

			h := bootAfterUpgrade(t, home)

			// The skill menu (C-11): an Ask skill is listed, a Deny skill is not.
			menu := d9ChatTurn(t, h, d9ChatSession(t, h), "C19-WAITING-MENU-TURN")
			listed := strings.Contains(menu, waitingMenuText)

			entry, results, run := waitingCall(t, h)

			switch row.old {
			case "ask":
				assert.True(t, listed, "C-11: an Ask skill is listed in the agent's menu with its description")
				if assert.NotNil(t, entry, "C-12: the planned Ask raises a card; the call ran to its end instead with tool results %q", results) {
					snap := h.handler.pendingApprovalSnapshot(entry)
					if assert.NotNil(t, snap.Skill, "C-9/C-24: an Ask on a skill is the skill's card, with the skill facts") {
						assert.Equal(t, waitingSkillKey, snap.Skill.Key)
						assert.Contains(t, snap.Skill.OpenChecks, "permission", "C-12: the open check is the skill's permission")
						assert.NotContains(t, snap.Skill.OpenChecks, "trust", "trust is allow, so no trust check is open")
					}
					w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
					require.Equal(t, 200, w.Code, "%s", w.Body.String())
					h.finish(t, run, false)
				}
			case "deny":
				assert.False(t, listed, "C-11: a Deny removes the skill from the menu")
				assert.Nil(t, entry, "C-12: a planned Deny refuses the load; it raises no card")
				if entry != nil {
					w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
					require.Equal(t, 200, w.Code, "%s", w.Body.String())
					h.finish(t, run, false)
				}
			}
			assert.NotContains(t, strings.Join(results, "\n"), waitingInstructions,
				"the instructions are not the result of a call the plan does not allow")
			assert.NotContains(t, everythingTheModelWasSent(t, h), waitingInstructions,
				"C-11/FR-066: the instructions of a skill that is not allowed never reach the model")
		})
	}
}
