//go:build goolm && stdjson

package gateway

// Orders 34/47 / CHECK F22. C-12/C-24/C-26 and every literal D10 row.
// This replaces pkg/agent's unbound decision-returning adapter. There is no
// PermissionOpen/TrustOpen/Denied/AutoActive input and no returned StoredValue
// or Audit oracle. Every decision goes through the SAME native turn/Skill/
// install/approval path. Inputs are literal policy, content, verification,
// approval and clock facts; expectations never enter an input binding.
// Missing native verification/clock support is a per-row fatal obligation.
// Downstream assertions are not claimed executed when such a prerequisite is
// missing. GREEN, mutation and proof-of-failability defer to independent CHECK.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All positions are stated in every literal below, including irrelevant input
// dimensions. Current/approved revisions are derived from these distinct literal
// package bytes, never from a returned card or the installed listing.
// An empty approvedBody means no approval, not an inferred TrustOpen flag.
type d10RawInput struct {
	entry, global, own           string
	verified                     bool
	trust, currentBody           string
	approvedBody                 string
	auto, god                    bool
	allowanceOwner               string
	allowanceStart, allowanceEnd time.Duration // relative to 2026-11-22 12:00 UTC
	run, shelf, installPolicy    string
}

type d10Case struct {
	row              int
	name             string
	in               d10RawInput
	want             string
	actions, lengths []string
	operation        string
}

func nativeD10Cases() []d10Case {
	permission := []string{"allow_once", "allow_for", "deny"}
	trust := []string{"approve_version", "deny"}
	combined := []string{"approve_version", "approve_version_allow_for", "deny"}
	lengths := []string{"1h", "3h", "1d"}
	return []d10Case{
		{1, "global Ask verified", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "card", permission, lengths, ""},
		{2, "Auto settles permission", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", true, false, "", 0, 0, "chat", "installed", "ask"}, "loads", nil, nil, ""},
		{3, "this agent live allowance", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "test-agent", 0, time.Hour, "chat", "installed", "ask"}, "loads", nil, nil, ""},
		{4, "ended allowance", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "test-agent", 0, time.Hour, "chat", "installed", "ask"}, "card", permission, lengths, "advance-to-end"},
		{4, "only another agent allowance", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "peer-agent", 0, time.Hour, "chat", "installed", "ask"}, "card", permission, lengths, ""},
		{5, "trust still asks under Auto and God", d10RawInput{"load", "allow", "", false, "ask", nativeSkillR1, "", true, true, "", 0, 0, "chat", "installed", "ask"}, "card", trust, []string{}, ""},
		{6, "approved exact same revision", d10RawInput{"load", "allow", "", false, "ask", nativeSkillR1, nativeSkillR1, false, false, "", 0, 0, "chat", "installed", "ask"}, "loads", nil, nil, ""},
		{7, "approval for literal older R1 not current R2", d10RawInput{"load", "allow", "", false, "ask", nativeSkillR2, nativeSkillR1, false, false, "", 0, 0, "chat", "installed", "ask"}, "card", trust, []string{}, ""},
		{8, "person slash does not settle trust", d10RawInput{"slash", "allow", "", false, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "card", trust, []string{}, ""},
		{9, "person slash settles permission", d10RawInput{"slash", "ask", "", true, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "loads", nil, nil, ""},
		{10, "unattended needs person", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "", 0, 0, "unattended", "installed", "ask"}, "skipped", nil, nil, ""},
		{11, "unattended Auto first", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", true, false, "", 0, 0, "unattended", "installed", "ask"}, "loads", nil, nil, ""},
		{12, "same live card then global Deny", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "card", permission, lengths, "global-deny-answer"},
		{13, "both checks open", d10RawInput{"load", "ask", "", false, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "card", combined, lengths, ""},
		{14, "Auto settles only permission", d10RawInput{"load", "ask", "", false, "ask", nativeSkillR1, "", true, false, "", 0, 0, "chat", "installed", "ask"}, "card", trust, []string{}, ""},
		{15, "review approved current revision permission still asks", d10RawInput{"load", "ask", "", false, "ask", nativeSkillR1, nativeSkillR1, false, false, "", 0, 0, "chat", "installed", "ask"}, "card", permission, lengths, ""},
		{16, "review approved plus Auto", d10RawInput{"load", "ask", "", false, "ask", nativeSkillR1, nativeSkillR1, true, false, "", 0, 0, "chat", "installed", "ask"}, "loads", nil, nil, ""},
		{17, "new verified skill unattended", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "", 0, 0, "unattended", "installed", "ask"}, "skipped", nil, nil, ""},
		{18, "new built-in reconciles shipped Allow", d10RawInput{"load", "", "", true, "ask", "", "", false, false, "", 0, 0, "chat", "builtin", "ask"}, "loads", nil, nil, "builtin-first-sight"},
		{19, "project R2 older approval not enough", d10RawInput{"load", "ask", "", false, "ask", nativeSkillR2, nativeSkillR1, false, false, "", 0, 0, "chat", "project", "ask"}, "card", combined, lengths, "project-repeat"},
		{20, "real day answer then same agent run", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "card", permission, lengths, "day-then-runs"},
		{21, "own Ask and actual time answer", d10RawInput{"load", "ask", "ask", true, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "card", permission, lengths, "own-time-answer"},
		{22, "God never removes own Ask", d10RawInput{"load", "allow", "ask", true, "ask", nativeSkillR1, "", false, true, "", 0, 0, "chat", "installed", "ask"}, "card", permission, lengths, ""},
		{23, "unverified Ask install even with Auto", d10RawInput{"install", "ask", "", false, "ask", nativeInstallR1, "", true, false, "", 0, 0, "chat", "registry", "ask"}, "card", []string{"allow_once", "deny"}, []string{}, ""},
		{24, "verified Ask install keeps ordinary card", d10RawInput{"install", "ask", "", true, "ask", nativeInstallR1, "", true, false, "", 0, 0, "chat", "registry", "ask"}, "card", []string{"allow_once", "allow_for", "deny", "cancel"}, lengths, ""},
		{25, "Allow install still asks staged trust", d10RawInput{"install", "ask", "", false, "ask", nativeInstallR1, "", false, false, "", 0, 0, "chat", "registry", "allow"}, "card", trust, []string{}, ""},
		{26, "unattended unverified Ask install removes pin", d10RawInput{"install", "ask", "", false, "ask", nativeInstallR1, "", true, false, "", 0, 0, "unattended", "registry", "ask"}, "refused", nil, nil, ""},
		{26, "unattended verified Ask install removes pin", d10RawInput{"install", "ask", "", true, "ask", nativeInstallR1, "", true, false, "", 0, 0, "unattended", "registry", "ask"}, "refused", nil, nil, ""},
		{26, "unattended unverified Allow install removes pin", d10RawInput{"install", "ask", "", false, "ask", nativeInstallR1, "", false, false, "", 0, 0, "unattended", "registry", "allow"}, "refused", nil, nil, ""},
		{27, "same combined card then actual file change", d10RawInput{"load", "ask", "", false, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "card", combined, lengths, "revision-change-answer"},
		{28, "same HTTP answer replay", d10RawInput{"load", "ask", "", false, "ask", nativeSkillR1, "", false, false, "", 0, 0, "chat", "installed", "ask"}, "card", combined, lengths, "replay"},
		{29, "live allowance in unattended run", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "test-agent", 0, time.Hour, "unattended", "installed", "ask"}, "loads", nil, nil, ""},
		{30, "real same-home restart after allowance", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "test-agent", 0, time.Hour, "chat", "installed", "ask"}, "card", permission, lengths, "restart"},
		{31, "real own permission write ends allowance", d10RawInput{"load", "ask", "", true, "ask", nativeSkillR1, "", false, false, "test-agent", 0, time.Hour, "chat", "installed", "ask"}, "card", permission, lengths, "write-own-ask"},
	}
}

// Installed verification is established only by the native REST review/install
// and a separate inventory read. The registry is the external input; no callback
// may override the loop's verification resolver or return a desired decision.

func TestSkillConsent_Table(t *testing.T) {
	t.Run("C-5 verified fixture uses native registry review install and independent inventory", func(t *testing.T) {
		h, _ := newNativeD10Harness(t, "allow", "allow")
		seedNativeD10VerifiedInstall(t, h, "notes-helper", nativeSkillR1)
	})
	t.Run("instrument actual unverified Allow load reaches the native model", func(t *testing.T) {
		h, _ := newNativeD10Harness(t, "allow", "allow")
		run := beginNativeD10(t, h, "load", "chat", "test-agent", "installed")
		finishNativeD10(t, h, run, true, false, nativeSkillR1)
		assert.Empty(t, h.reg.pendingApprovals())
	})
	t.Run("instrument real session-carrying project entry loads mounted R2 on successive turns", func(t *testing.T) {
		h, _ := newNativeD10Harness(t, "allow", "allow")
		seedNativeD10Project(t, h, nativeSkillR2)
		// Fix round 4, part E item 2: "an allowed project skill loads" needs the PROJECT key
		// allowed. The harness stores only the installed key `notes-helper: allow`, and by C-19
		// ("A project skill and an installed skill with the same slug have different keys", S-102)
		// that value does not reach `project:d10/notes-helper`; with no stored value the project key
		// starts at Ask (C-19, "Their starting permission is Ask"), which is pinned separately by
		// TestProjectSkillKey_StartsAtAskAtFirstSight. The instrument's own claim (a real session-
		// carrying project entry loads the mounted R2 on successive turns) is unchanged.
		storeNativeD10Permission(t, h, "project:d10/notes-helper", "allow")
		for range 2 {
			run := beginNativeD10(t, h, "load", "chat", "test-agent", "project")
			finishNativeD10(t, h, run, true, false, nativeSkillR2)
		}
		assert.Empty(t, h.reg.pendingApprovals())
	})
	t.Run("instrument staging observer sees a planted package and its cleanup", func(t *testing.T) {
		home := t.TempDir()
		pin := filepath.Join(home, "skills", ".staging", "instrument", "SKILL.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(pin), 0o700))
		require.NoError(t, os.WriteFile(pin, []byte(nativeInstallR1), 0o600))
		require.Equal(t, []string{pin}, nativeD10StagedFiles(t, home))
		require.NoError(t, os.RemoveAll(filepath.Join(home, "skills", ".staging")))
		require.Empty(t, nativeD10StagedFiles(t, home))
	})
	for _, tc := range nativeD10Cases() {
		t.Run(fmt.Sprintf("D10#%02d %s", tc.row, tc.name), func(t *testing.T) {
			h, key, clock, closeLoop := seedNativeD10(t, tc.in)
			applyNativeD10Before(t, h, key, clock, closeLoop, tc)
			run := beginNativeD10(t, h, tc.in.entry, tc.in.run, "test-agent", tc.in.shelf)
			if tc.want != "card" {
				finishNativeD10(t, h, run, tc.want == "loads", tc.want == "skipped", tc.in.currentBody)
				assert.Empty(t, h.reg.pendingApprovals())
				if tc.in.entry == "install" {
					assertNativeD10InstallRefused(t, h, run)
				}
				if tc.operation == "builtin-first-sight" {
					assertNativeD10Stored(t, h, key, "allow")
				}
				return
			}
			entry := h.pending(t, run)
			snap := h.handler.pendingApprovalSnapshot(entry)
			assert.Equal(t, tc.actions, snap.OfferedActions, "C-24 wire actions, not labels containing a duration")
			assert.Equal(t, tc.lengths, snap.AllowDurations, "C-26 lengths are a separate wire array")
			require.NotNil(t, snap.Skill, "C-9: every load/install card carries native skill facts, including verified installs")
			assert.Equal(t, key, snap.Skill.Key)
			assert.Equal(t, !tc.in.verified, snap.Skill.Unverified)
			assert.Equal(t, nativeD10Revision(t, tc.in.currentBody), snap.Skill.Revision, "card binds the independently staged/current literal bytes")
			if tc.in.entry == "install" {
				root := filepath.Join(h.api.homePath, "skills")
				pins := nativeD10StagedFiles(t, h.api.homePath)
				require.Len(t, pins, 1, "actual preparation pins one package before the native card")
				body, err := os.ReadFile(pins[0])
				require.NoError(t, err)
				assert.Equal(t, tc.in.currentBody, string(body))
				assert.NoDirExists(t, filepath.Join(root, "fresh-helper"))
			}
			applyNativeD10Answer(t, h, key, run, entry, clock, tc)
		})
	}
}

// This fixture owns the lifetime of each actual loop with sync.Once. The
// general mustAgentLoop fixture already registers Close at test end, so using
// it and also calling Close for D10#30 would close the old loop twice (including
// after a replacement loop opens the same home). Nothing replaces a resolver.
func newNativeD10Harness(t *testing.T, trust, permission string) (*nativeSkillHarness, func()) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	root := filepath.Join(home, "skills", "notes-helper")
	require.NoError(t, os.MkdirAll(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(nativeSkillR1), 0o600))
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "native-consent-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = nil
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.SkillTrust = config.SkillTrustLevel(trust)
	cfg.Sandbox.ToolPolicies["Skill"], cfg.Sandbox.ToolPolicies["install_skill"] = "allow", "ask"
	cfg.Tools.Manifest.Compressed = false
	require.NoError(t, json.Unmarshal([]byte(`{"skill_policies":{"notes-helper":"`+permission+`"}}`), &cfg.Sandbox))
	for _, id := range []string{"test-agent", "peer-agent"} {
		record := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Skills: []string{"notes-helper"}, Tools: coreagent.NewCustomAgentToolsCfg()}
		store := agentstore.New(home)
		require.NoError(t, store.Create(id, &record))
		persisted, err := store.Get(id)
		require.NoError(t, err)
		require.NotEmpty(t, persisted.SkillGrants["notes-helper"])
		cfg.Agents.List = append(cfg.Agents.List, *persisted)
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	sandbox, ok := document["sandbox"].(map[string]any)
	require.True(t, ok, "fixture config sandbox must be an object")
	sandbox["skill_policies"] = map[string]string{"notes-helper": permission}
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	script := &nativeConsentScript{tool: "Skill", args: map[string]any{"name": "notes-helper"}}
	al, closeLoop := ownedNativeD10Loop(t, cfg, msgBus, script)
	for _, record := range cfg.Agents.List {
		inst, ok := al.GetRegistry().GetAgent(record.ID)
		require.True(t, ok)
		inst.ContextBuilder.WithSkillAllowlist(record.Skills).WithSkillBindings(record.SkillGrants)
	}
	reg := newApprovalRegistryV2(64, 30*time.Second)
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	t.Cleanup(handler.Wait)
	h := &nativeSkillHarness{timedCardHarness: &timedCardHarness{api: &restAPI{agentLoop: al, homePath: home, approvalReg: reg, auditor: al.AuditLogger()}, reg: reg, handler: handler}, script: script}
	return h, closeLoop
}

func ownedNativeD10Loop(t *testing.T, cfg *config.Config, msgBus *bus.MessageBus, script *nativeConsentScript) (*agent.AgentLoop, func()) {
	t.Helper()
	ensureTestWorkspaceMembership(t, cfg)
	al, err := agent.NewAgentLoop(cfg, msgBus, script)
	require.NoError(t, err)
	seedDefaultDelegationGraph(t, testHarnessAgentIDs(cfg))
	installTestSessionLauncher(t, al)
	var once sync.Once
	closeLoop := func() { once.Do(func() { al.Stop(); closeAndAssertNoSteeredTurns(t, al) }) }
	t.Cleanup(closeLoop)
	return al, closeLoop
}

func seedNativeD10(t *testing.T, in d10RawInput) (*nativeSkillHarness, string, *scheduledAllowanceClock, func()) {
	t.Helper()
	h, closeLoop := newNativeD10Harness(t, in.trust, in.global)
	cfg := h.api.agentLoop.GetConfig()
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = in.auto, in.god
	cfg.Sandbox.ToolPolicies["install_skill"] = in.installPolicy
	key := "notes-helper"
	projectPath := ""
	if in.shelf == "builtin" {
		key = "plan"
		store := agentstore.New(h.api.homePath)
		// Fix round 4, part E item 1 (follow-up): the built-in's folder must exist BEFORE the grant
		// is saved. Every agent persist binds each granted name to the generation stamped in its
		// installed folder at that moment, and a name whose folder is absent stays unbound
		// (pkg/skills/identity.go::BindGrants: "never a refusal"; SD-30 "unbound = inert"). The
		// first draft granted `plan` and exported the folder after, so the grant was never bound
		// and the load was refused with SKILL_GRANT_STALE ("your grant is for a previous skill with
		// this name") before the first-sight question under test could be asked: an order fault in
		// the fixture, hidden until the roster fix let the turn run. A real update installs the
		// shipped folder first and binds the grants after it. Every D10#18 assertion is unchanged.
		if _, statErr := os.Stat(filepath.Join(h.api.homePath, "skills", key)); os.IsNotExist(statErr) {
			require.NoError(t, skills.ExportEmbeddedPackage(key, filepath.Join(h.api.homePath, "skills", key)))
		} else {
			require.NoError(t, statErr)
		}
		_, err := store.Update("test-agent", func(a *config.AgentConfig) error { a.Skills = append(a.Skills, key); return nil })
		require.NoError(t, err)
		persistedGrant, err := store.Get("test-agent")
		require.NoError(t, err)
		require.NotEmpty(t, persistedGrant.SkillGrants[key],
			"fixture: the built-in's grant is bound to its installed folder's generation, as the install path leaves it")
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "test-agent" {
				record, readErr := store.Get("test-agent")
				require.NoError(t, readErr)
				cfg.Agents.List[i] = *record
			}
		}
		var document map[string]any
		configuration, err := json.Marshal(cfg)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(configuration, &document))
		sandbox, ok := document["sandbox"].(map[string]any)
		require.True(t, ok, "fixture config sandbox must be an object")
		sandbox["skill_policies"] = map[string]string{} // no stored value for this newly shipped built-in
		sandbox["migrations"] = map[string]string{"skill_permissions": "done"}
		body, err := json.Marshal(document)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(h.api.homePath, "config.json"), body, 0o600))
		h.script.args = map[string]any{"name": key}
	} else if in.shelf == "project" {
		key = "project:d10/notes-helper"
		body := in.currentBody
		if in.approvedBody != "" {
			body = in.approvedBody
		}
		projectPath = seedNativeD10Project(t, h, body)
	} else if in.entry != "install" {
		path := filepath.Join(h.api.homePath, "skills", "notes-helper", "SKILL.md")
		if in.approvedBody != "" {
			require.NoError(t, os.WriteFile(path, []byte(in.approvedBody), 0o600))
			approved := approveSkillViaPersonRoute(t, h, key)
			expected := nativeD10Revision(t, in.approvedBody)
			require.Equal(t, expected, approved.Revision)
		}
		require.NoError(t, os.WriteFile(path, []byte(in.currentBody), 0o600))
		require.Equal(t, nativeD10Revision(t, in.currentBody), nativeD10Current(t, h, key))
	}
	if in.shelf != "builtin" {
		raw, err := json.Marshal(map[string]any{"skill_policies": map[string]string{key: in.global}})
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &cfg.Sandbox))
	}
	if in.own != "" {
		path := filepath.Join(h.api.homePath, "entities", "agents", "test-agent.json")
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var record map[string]any
		require.NoError(t, json.Unmarshal(raw, &record))
		// The store record is flat: the agent tool settings are the top-level tools member
		// (tests/helpers/agentrecord); tools_cfg is only the REST request name, so a value
		// planted there is dead data no reader ever sees.
		ownTools, _ := record["tools"].(map[string]any)
		if ownTools == nil {
			ownTools = map[string]any{}
			record["tools"] = ownTools
		}
		ownTools["skills"] = map[string]any{"policies": map[string]string{key: in.own}}
		raw, err = json.Marshal(record)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, raw, 0o600))
		require.Equal(t, map[string]any{key: in.own}, agentrecord.Policies(t, h.api.homePath, "test-agent", agentrecord.Skills),
			"the Given is planted at the carrier the store keeps, not in a place nothing reads")
		persisted, err := agentstore.New(h.api.homePath).Get("test-agent")
		require.NoError(t, err)
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "test-agent" {
				cfg.Agents.List[i] = *persisted
			}
		}
	}
	// Apply actual configuration to the live native registry, not a calculated
	// permission outcome. Canonical C-3 trust is never translated to legacy text.
	if in.shelf == "builtin" {
		// Native load/reconciliation is the first sight of a missing shipped key.
		loaded, err := config.LoadConfig(filepath.Join(h.api.homePath, "config.json"))
		if err != nil {
			t.Fatalf("BLOCKED: native C-3/C-2 config load for new built-in D10#18: %v", err)
		}
		// config.LoadConfig always empties agents.list (ADR-054;
		// pkg/config/legacy_agents_list.go), and the real reload path fills the roster again from
		// the agent store right after loading (pkg/gateway/gateway_reload.go, the config-watcher
		// poller: "repopulate cfg.Agents.List from the agent store", via
		// populateAgentsListFromEntityStoreStrict). Do the same here. The raw LoadConfig result
		// carries no agents, so reloading it leaves the loop without "test-agent" and every run
		// fails with "the requested agent is not available" before the first-sight question
		// under test can be asked — a fixture fault, not product behaviour (fix round 4, part E
		// item 1; the same fault and the same fix as the D10#30 restart fixture below, part D
		// item 6). Every D10#18 assertion is unchanged.
		require.NoError(t, populateAgentsListFromEntityStoreStrict(loaded, h.api.homePath),
			"the reload fills the roster from the agent store, as the production reload does")
		require.NotEmpty(t, loaded.Agents.List, "the reloaded config must carry the stored agents, as in production")
		cfg = loaded
	}
	assertNativeD10RawInputs(t, cfg, key, in)
	require.NoError(t, config.SaveConfig(filepath.Join(h.api.homePath, "config.json"), cfg))
	require.NoError(t, h.api.agentLoop.ReloadProviderAndConfig(context.Background(), h.script, cfg))
	for _, record := range cfg.Agents.List {
		inst, ok := h.api.agentLoop.GetRegistry().GetAgent(record.ID)
		require.True(t, ok)
		inst.ContextBuilder.WithSkillAllowlist(record.Skills).WithSkillBindings(record.SkillGrants)
	}
	if projectPath != "" && in.approvedBody != "" {
		prior := beginNativeD10(t, h, "load", "chat", "test-agent", "project")
		pending := h.pending(t, prior)
		snap := h.handler.pendingApprovalSnapshot(pending)
		require.NotNil(t, snap.Skill)
		require.Equal(t, key, snap.Skill.Key)
		require.Equal(t, nativeD10Revision(t, in.approvedBody), snap.Skill.Revision)
		answer := postToolApprovalRaw(t, h.api, pending.ApprovalID, `{"action":"approve_version"}`)
		require.Equal(t, http.StatusOK, answer.Code, "%s", answer.Body.String())
		finishNativeD10(t, h, prior, true, false, in.approvedBody)
		require.NoError(t, os.WriteFile(projectPath, []byte(in.currentBody), 0o600))
	}
	if in.entry == "install" {
		prepareNativeD10Install(t, h, in)
		key = "fresh-helper"
	} else if in.verified && in.shelf != "builtin" {
		// C-5 verification must be durably established by an actual install.
		// The only double is the external registry's matched-hash result.
		seedNativeD10VerifiedInstall(t, h, key, in.currentBody)
	}
	var clock *scheduledAllowanceClock
	if in.allowanceOwner != "" {
		clock = controlledTimedCardClock(t, h.timedCardHarness)
		clock.advance(in.allowanceStart)
		run := beginNativeD10(t, h, "load", "chat", in.allowanceOwner, in.shelf)
		entry := h.pending(t, run)
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_for","duration":"1h"}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		var result gen.ToolApprovalResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.NotNil(t, result.ExpiresAt)
		assert.Equal(t, time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC).Add(in.allowanceEnd), result.ExpiresAt.UTC())
		finishNativeD10(t, h, run, true, false, in.currentBody)
	}
	return h, key, clock, closeLoop
}

func assertNativeD10RawInputs(t *testing.T, cfg *config.Config, key string, in d10RawInput) {
	t.Helper()
	assert.Equal(t, in.auto, cfg.Sandbox.AutoApprove)
	assert.Equal(t, in.god, cfg.Sandbox.GodMode)
	assert.Equal(t, in.trust, string(cfg.Sandbox.SkillTrust))
	if in.entry == "install" {
		require.Equal(t, in.installPolicy, cfg.Sandbox.ToolPolicies["install_skill"], "literal install tool permission, not a permission-open flag")
		return // C-12 install rows have no skill-load permission input.
	}
	raw, err := json.Marshal(cfg.Sandbox)
	require.NoError(t, err)
	var sandbox map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &sandbox))
	if len(sandbox["skill_policies"]) == 0 {
		t.Fatal("BLOCKED: native sandbox.skill_policies input is not implemented — required by C-2/C-12 and every literal D10 raw global permission; do not collapse it into PermissionOpen")
	}
	var policies map[string]string
	require.NoError(t, json.Unmarshal(sandbox["skill_policies"], &policies))
	if in.shelf == "builtin" {
		assert.Equal(t, "allow", policies[key], "C-2 newly shipped built-in reconciles to Allow")
	} else {
		require.Equal(t, in.global, policies[key])
	}
	for _, record := range cfg.Agents.List {
		if record.ID != "test-agent" {
			continue
		}
		raw, err := json.Marshal(record)
		require.NoError(t, err)
		var entity map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &entity))
		// A marshalled AgentConfig is flat: tools is the agent tool settings member.
		own := map[string]json.RawMessage{}
		if len(entity["tools"]) != 0 {
			require.NoError(t, json.Unmarshal(entity["tools"], &own))
		}
		var skillsInput struct {
			Policies map[string]string `json:"policies"`
		} // not-wire-format: native stored entity fixture
		if len(own["skills"]) != 0 {
			require.NoError(t, json.Unmarshal(own["skills"], &skillsInput))
		}
		require.Equal(t, in.own, skillsInput.Policies[key], "literal agent-own permission must survive the real store/config decoder")
	}
}

func nativeD10Revision(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))
	result, err := skills.ContentRevision(dir)
	require.NoError(t, err)
	return result
}
func nativeD10Current(t *testing.T, h *nativeSkillHarness, key string) string {
	t.Helper()
	revision, err := skills.NewSkillWriter(filepath.Join(h.api.homePath, "skills")).SkillRevision(key)
	require.NoError(t, err)
	return revision
}

// This is the literal project input shared by the observer instrument and D10#19,
// not a returned decision. No installed same-name package may satisfy this test.
func seedNativeD10Project(t *testing.T, h *nativeSkillHarness, body string) string {
	t.Helper()
	require.NoError(t, os.RemoveAll(filepath.Join(h.api.homePath, "skills", "notes-helper")))
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(root, ".claude", "skills", "notes-helper")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	file := filepath.Join(dir, "SKILL.md")
	require.NoError(t, os.WriteFile(file, []byte(body), 0o600))
	require.NoError(t, workspace.SaveRecord(h.api.homePath, workspace.Workspace{ID: "d10", Name: "D10 project", Status: "active", CoreTeam: []string{"test-agent", "peer-agent"}}))
	_, err = workspace.EnsureWorkDir(h.api.homePath, "d10")
	require.NoError(t, err)
	_, warning, err := workspace.CreateMount(h.api.homePath, "d10", "project", root)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, []string{root}, workspace.AllowedMountRoots(h.api.homePath, "d10"))
	return file
}

// ProcessDirectWithChannelAs intentionally omits SessionID. A project web-chat
// must instead enter the actual dispatcher with its session/workspace identity.
// Each owned run joins before done is sent, so successive project probes cannot
// leave a previous dispatcher competing for the next inbound or final outbound.
func runNativeD10ProjectChat(ctx context.Context, h *nativeSkillHarness, owner, sessionID, message string) (resultErr error) {
	runCtx, cancel := context.WithCancel(ctx)
	runExited := make(chan struct{})
	var runErr error
	go func() {
		defer close(runExited)
		runErr = h.api.agentLoop.Run(runCtx)
	}()
	defer func() {
		cancel()
		<-runExited
		if resultErr == nil {
			resultErr = runErr
		}
	}()
	if err := h.handler.msgBus.PublishInbound(runCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "d10-chat", SessionID: sessionID,
		SessionKey: "agent:" + owner + ":session:" + sessionID,
		Sender:     bus.SenderInfo{CanonicalID: "d10-person"}, Content: message,
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": owner, "workspace_id": "d10"},
	}); err != nil {
		return err
	}
	select {
	case reply := <-h.handler.msgBus.OutboundChan():
		if reply.SessionID != sessionID {
			return fmt.Errorf("fixture UNVERIFIED: expected final for %s, got %s", sessionID, reply.SessionID)
		}
		return nil
	case <-runExited:
		if runErr != nil {
			return runErr
		}
		return fmt.Errorf("fixture UNVERIFIED: native dispatcher exited before the project's final outbound")
	case <-runCtx.Done():
		return runCtx.Err()
	}
}

func beginNativeD10(t *testing.T, h *nativeSkillHarness, entry, attendance, owner, shelf string) nativeSkillRun {
	t.Helper()
	h.seq++
	prompt := fmt.Sprintf("F18-CALL-D10-%d", h.seq)
	var meta *session.UnifiedMeta
	var err error
	if attendance == "unattended" {
		meta, err = h.api.agentLoop.GetSessionStore().NewScheduledSession(owner)
	} else {
		meta, err = h.api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", owner)
	}
	require.NoError(t, err)
	if shelf == "project" {
		id := "d10"
		require.NoError(t, h.api.agentLoop.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &id}))
	}
	if attendance == "chat" {
		require.NoError(t, h.api.agentLoop.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning, AgentID: owner, OwnerScopeKind: session.OwnerScopeHuman, Origin: &session.Origin{Kind: session.OriginKindChat}}))
	}
	message := prompt
	if entry == "slash" {
		message, prompt = "/notes-helper", ""
	} // native slash, never a Skill call pretending to be one
	ctx, cancel := context.WithCancel(context.Background())
	done, joined := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(joined)
		var err error
		if attendance == "unattended" {
			_, err = h.api.agentLoop.ProcessScheduled(ctx, owner, meta.ID, message, "scheduled", meta.ID)
		} else if shelf == "project" {
			err = runNativeD10ProjectChat(ctx, h, owner, meta.ID, message)
		} else {
			_, err = h.api.agentLoop.ProcessDirectWithChannelAs(ctx, owner, message, "agent:"+owner+":session:"+meta.ID, "webchat", meta.ID)
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("fixture UNVERIFIED: D10 native turn did not join")
		}
	})
	return nativeSkillRun{prompt: prompt, done: done}
}

func finishNativeD10(t *testing.T, h *nativeSkillHarness, run nativeSkillRun, loads, skipped bool, body string) {
	t.Helper()
	select {
	case err := <-run.done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("fixture UNVERIFIED: D10 native turn did not complete; a deadline is not refusal")
	}
	requests := h.script.captured(run.prompt)
	if run.prompt != "" || loads {
		require.NotEmpty(t, requests, "the scripted no-paid model actually observed the turn")
	}
	// A denied slash trust card may correctly stop before the first model call.
	var seen strings.Builder
	for _, request := range requests {
		for _, message := range request {
			seen.WriteString(message.Content)
			seen.WriteByte('\n')
			for _, part := range message.SystemParts {
				seen.WriteString(part.Text)
				seen.WriteByte('\n')
			}
		}
	}
	if loads && body == "" {
		golden := filepath.Join(t.TempDir(), "plan")
		require.NoError(t, skills.ExportEmbeddedPackage("plan", golden))
		raw, err := os.ReadFile(filepath.Join(golden, "SKILL.md"))
		require.NoError(t, err)
		parts := strings.SplitN(string(raw), "---", 3)
		require.Len(t, parts, 3)
		assert.Contains(t, seen.String(), strings.TrimSpace(parts[2]), "native built-in load includes its complete embedded instruction body")
	}
	// The complete instruction body comes from the literal fixture, not from
	// the resolver result. This observes content, not just a successful return.
	if body != "" {
		parts := strings.SplitN(body, "---", 3)
		require.Len(t, parts, 3)
		instructions := strings.TrimSpace(parts[2])
		require.NotEmpty(t, instructions)
		if loads {
			assert.Contains(t, seen.String(), instructions)
		} else {
			assert.NotContains(t, seen.String(), instructions)
		}
	}
	if !loads {
		assert.NotContains(t, seen.String(), "F18-NOTES-R1-INSTRUCTIONS-9Z")
		assert.NotContains(t, seen.String(), "F18-NOTES-R2-INSTRUCTIONS-7Q")
		assert.NotContains(t, seen.String(), "F18-FRESH-R1-INSTRUCTIONS-4K")
		assert.NotContains(t, seen.String(), "F18-FRESH-R2-INSTRUCTIONS-5M")
	}
	if skipped {
		var results []string
		for _, request := range requests {
			for _, m := range request {
				if m.Role == "tool" {
					results = append(results, m.Content)
				}
			}
		}
		assert.Contains(t, results, "Not run: needs approval, and task and plan runs never ask.", "C-12 exact unattended result, not just completed-without-a-card")
	}
}

func applyNativeD10Before(t *testing.T, h *nativeSkillHarness, key string, clock *scheduledAllowanceClock, closeLoop func(), tc d10Case) {
	t.Helper()
	switch tc.operation {
	case "advance-to-end":
		clock.advance(tc.in.allowanceEnd - tc.in.allowanceStart)
	case "restart":
		// A native same-home loop restart, not an AfterRestart flag. Full gateway
		// RunContext/platform certification remains in F17/F21 and ST-29/30.
		closeLoop()
		h.handler.Wait()
		cfg := &config.Config{}
		require.NoError(t, json.Unmarshal(readNativeConfig(t, h.api.homePath), cfg))
		// Fix round 4, part D item 6: config.json never carries the agent list (ADR-054: agents
		// live in the entity store), so a loop built from the parsed file alone has NO agents and
		// the restart check then looks at an empty roster. A real boot reads config.json and then
		// fills the roster from the entity store before it builds the loop
		// (pkg/gateway/gateway_boot_roster.go::populateAgentsListFromEntityStoreStrict, the same
		// bridge reload uses); seed it the same way, so the restarted loop holds the agents the
		// earlier native installs and permission writes stored. Same oracle as before.
		require.NoError(t, populateAgentsListFromEntityStoreStrict(cfg, h.api.homePath),
			"a restart loads its roster from the entity store exactly as boot does")
		require.NotEmpty(t, cfg.Agents.List, "instrument: the restarted loop must have agents to hold the allowance state")
		msgBus := bus.NewMessageBus()
		t.Cleanup(msgBus.Close)
		h.api.agentLoop, _ = ownedNativeD10Loop(t, cfg, msgBus, h.script)
		h.api.auditor = h.api.agentLoop.AuditLogger()
		for _, record := range cfg.Agents.List {
			inst, ok := h.api.agentLoop.GetRegistry().GetAgent(record.ID)
			require.True(t, ok)
			inst.ContextBuilder.WithSkillAllowlist(record.Skills).WithSkillBindings(record.SkillGrants)
		}
		if tc.in.verified {
			// No re-injection on restart: C-5 verification is stored by the
			// earlier native install and must survive in the fresh inventory.
			assertNativeD10VerifiedInventory(t, h.api, key, tc.in.currentBody)
		}
		h.reg = newApprovalRegistryV2(64, 30*time.Second)
		h.handler = newWSHandler(msgBus, h.api.agentLoop, "")
		h.handler.approvalRegV2 = h.reg
		h.api.approvalReg = h.reg
		h.api.agentLoop.SetToolApprover(newPolicyApproverAdapter(h.reg, h.handler))
		t.Cleanup(h.handler.Wait)
	case "write-own-ask":
		before := nativeD10AllowanceEnded(t, h)
		require.Empty(t, before)
		w := putAgentSkills(t, h.api, "test-agent", `{"policies":{"`+key+`":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		ended := nativeD10AllowanceEnded(t, h)
		require.Len(t, ended, 1, "one real allowance-ended audit, not an adapter string")
		// The container is unspecified by C-26 (content only): read through the one shared helper
		// TestTimedAllowance_EndsOnPermissionChange uses, so both tests hold every writer to the
		// same content (fix round 4, part D item 5).
		fields := allowanceEndedPayload(ended[0])
		assert.Equal(t, "test-agent", fields["agent_id"])
		assert.Contains(t, nativeAuditScalarValues(fields), key, "C-26 requires the exact skill subject; no absent carrier's subject-field name is invented")
		// C-26 "Audit" and S-128 say the entry names "the changed key". For a skill permission the
		// changed key is the skill's key as stored in the permission map (the same bare key the
		// "agent and the tool or skill key" wording of the same paragraph uses). The spec defines no
		// "skill:" form for this field, so the bare key is the spec-derived value; a prefixed form
		// is an implementation choice the spec does not carry (spec gap, reported in fix4-report.md).
		assert.Equal(t, key, fields["key"], "the actual changed permission key, as the permission map stores it (C-26 Audit: \"the changed key\")")
		assert.Equal(t, "ask", fields["new"])
		assert.Equal(t, "admin", fields["actor"])
		assert.Equal(t, "permission_changed", fields["reason"])
	}
}

func applyNativeD10Answer(t *testing.T, h *nativeSkillHarness, key string, run nativeSkillRun, entry *approvalEntry, clock *scheduledAllowanceClock, tc d10Case) {
	t.Helper()
	beforeConfig := readNativeConfig(t, h.api.homePath)
	beforeTrust, err := os.ReadFile(trustApprovalsPath(h.api))
	if err != nil {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	action := `{"action":"deny"}`
	switch tc.operation {
	case "global-deny-answer":
		t.Setenv("OMNIPUS_BEARER_TOKEN", permissionsTestBearer)
		routes := routeTable{}
		h.api.registerAdditionalEndpoints(routes)
		w := patchPermissions(t, h.api, routes, `{"skill_policies":{"`+key+`":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		storedAfterWrite := readNativeConfig(t, h.api.homePath)
		w = postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_once"}`)
		assertNativeSkillRefusal(t, w, http.StatusConflict, "This skill is now set to Deny.")
		assert.Equal(t, storedAfterWrite, readNativeConfig(t, h.api.homePath), "the stale answer writes no permission")
		after, err := os.ReadFile(trustApprovalsPath(h.api))
		if err != nil {
			require.ErrorIs(t, err, os.ErrNotExist)
		}
		assert.Equal(t, beforeTrust, after)
		finishNativeD10(t, h, run, false, false, tc.in.currentBody)
		assertNativeD10WaitingRefusal(t, h, run, "This skill is now set to Deny.")
		return
	case "revision-change-answer":
		require.NoError(t, os.WriteFile(filepath.Join(h.api.homePath, "skills", "notes-helper", "SKILL.md"), []byte(nativeSkillR2), 0o600))
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version"}`)
		assertNativeSkillRefusal(t, w, http.StatusConflict, "The skill changed after this request.")
		after, err := os.ReadFile(trustApprovalsPath(h.api))
		if err != nil {
			require.ErrorIs(t, err, os.ErrNotExist)
		}
		assert.Equal(t, beforeTrust, after)
		assert.Equal(t, beforeConfig, readNativeConfig(t, h.api.homePath))
		finishNativeD10(t, h, run, false, false, tc.in.currentBody)
		assertNativeD10WaitingRefusal(t, h, run, "The skill changed after this request.")
		return
	case "replay":
		action = `{"action":"approve_version"}`
	case "day-then-runs", "own-time-answer":
		if clock == nil {
			clock = controlledTimedCardClock(t, h.timedCardHarness)
		}
		action = `{"action":"allow_for","duration":"1d"}`
	case "project-repeat":
		action = `{"action":"approve_version"}`
	}
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, action)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	loads := tc.operation == "replay" || tc.operation == "day-then-runs" || tc.operation == "own-time-answer" || tc.operation == "project-repeat"
	finishNativeD10(t, h, run, loads, false, tc.in.currentBody)
	assert.Equal(t, beforeConfig, readNativeConfig(t, h.api.homePath), "card answer never writes stored permission")
	if !loads {
		afterTrust, err := os.ReadFile(trustApprovalsPath(h.api))
		if err != nil {
			require.ErrorIs(t, err, os.ErrNotExist)
		}
		assert.Equal(t, beforeTrust, afterTrust, "Deny writes no durable trust approval")
	}
	if tc.operation == "replay" {
		assertFreshSkillTrust(t, h.api.homePath, key, true)
		trustAfter := readNativeTrustBytes(t, h)
		count := nativeD10TrustAudit(t, h, key)
		require.Equal(t, 1, count)
		replay := postToolApprovalRaw(t, h.api, entry.ApprovalID, action)
		assertNativeSkillRefusal(t, replay, http.StatusNotFound, existingApprovalNotFoundText(entry.ApprovalID))
		assert.Equal(t, trustAfter, readNativeTrustBytes(t, h))
		assert.Equal(t, count, nativeD10TrustAudit(t, h, key))
	}
	if tc.operation == "day-then-runs" || tc.operation == "own-time-answer" {
		var response gen.ToolApprovalResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.NotNil(t, response.ExpiresAt)
		assert.Equal(t, clock.wallNow().Add(24*time.Hour), response.ExpiresAt.UTC())
		assertNativeD10TimedAudit(t, h, entry, key, clock.wallNow().Add(24*time.Hour))
		clock.advance(time.Minute)
		next := beginNativeD10(t, h, "load", "unattended", "test-agent", tc.in.shelf)
		finishNativeD10(t, h, next, true, false, tc.in.currentBody)
		peer := beginNativeD10(t, h, "load", "unattended", "peer-agent", tc.in.shelf)
		finishNativeD10(t, h, peer, false, true, tc.in.currentBody)
		assert.Equal(t, beforeConfig, readNativeConfig(t, h.api.homePath))
	}
	if tc.operation == "project-repeat" {
		// Approval removes trust only. Ask must still yield a permission card on
		// the SAME literal revision; editing again reopens the combined card.
		next := beginNativeD10(t, h, "load", "chat", "test-agent", "project")
		pending := h.pending(t, next)
		snap := h.handler.pendingApprovalSnapshot(pending)
		assert.Equal(t, []string{"allow_once", "allow_for", "deny"}, snap.OfferedActions)
		require.Equal(t, http.StatusOK, postToolApprovalRaw(t, h.api, pending.ApprovalID, `{"action":"deny"}`).Code)
		finishNativeD10(t, h, next, false, false, tc.in.currentBody)
		const thirdRevision = "---\nname: notes-helper\ndescription: Third independent project revision.\n---\n\nF22-PROJECT-R3-INSTRUCTIONS-6J\n"
		mounts := workspace.AllowedMountRoots(h.api.homePath, "d10")
		require.Len(t, mounts, 1)
		require.NoError(t, os.WriteFile(filepath.Join(mounts[0], ".claude", "skills", "notes-helper", "SKILL.md"), []byte(thirdRevision), 0o600))
		third := beginNativeD10(t, h, "load", "chat", "test-agent", "project")
		pending = h.pending(t, third)
		snap = h.handler.pendingApprovalSnapshot(pending)
		assert.Equal(t, []string{"approve_version", "approve_version_allow_for", "deny"}, snap.OfferedActions)
		assert.Equal(t, []string{"1h", "3h", "1d"}, snap.AllowDurations)
		require.NotNil(t, snap.Skill)
		assert.Equal(t, key, snap.Skill.Key)
		assert.Equal(t, nativeD10Revision(t, thirdRevision), snap.Skill.Revision)
		require.Equal(t, http.StatusOK, postToolApprovalRaw(t, h.api, pending.ApprovalID, `{"action":"deny"}`).Code)
		finishNativeD10(t, h, third, false, false, thirdRevision)
	}
	if tc.in.entry == "install" {
		assert.NoDirExists(t, filepath.Join(h.api.homePath, "skills", "fresh-helper"))
		assert.Empty(t, nativeD10StagedFiles(t, h.api.homePath), "denying a real install card discards the actual pin")
		assert.NoFileExists(t, trustApprovalsPath(h.api))
	}
	assert.Empty(t, h.reg.pendingApprovals())
}

// seedNativeD10VerifiedInstall drives HandleSkills -> registry staging ->
// native review -> native publication. No resolver callback or hand-authored
// verification store can satisfy it. Replacing the existing fixture uses its
// real revision; review and independent inventory must agree on the literal
// matched package. No native API name or private metadata format is invented.
func seedNativeD10VerifiedInstall(t *testing.T, h *nativeSkillHarness, key, body string) {
	t.Helper()
	previous := h.api.skillRegistry
	h.api.skillRegistry = &nativeD10Registry{body: body, verified: true}
	defer func() { h.api.skillRegistry = previous }()
	revision := nativeD10Current(t, h, key)
	category := gen.SkillCategory("research")
	replace := true
	req := gen.SkillInstallRequest{Slug: &key, Revision: &revision, Category: &category, ReplaceLocalChanges: &replace}
	post := func(path string, req gen.SkillInstallRequest) *httptest.ResponseRecorder {
		raw, err := json.Marshal(req)
		require.NoError(t, err)
		r := withAdminRole(httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw))))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.api.HandleSkills(w, r)
		return w
	}
	staged := post("/api/v1/skills/install/review", req)
	require.Equal(t, http.StatusOK, staged.Code, "BLOCKED: C-5/C-13 native verified install review is required for the D10 raw verified input: %s", staged.Body.String())
	var review gen.SkillInstallReview
	require.NoError(t, json.Unmarshal(staged.Body.Bytes(), &review))
	require.NotEmpty(t, review.ReviewId)
	require.Equal(t, key, review.Name)
	require.True(t, review.Verified, "external matched-hash result must reach actual staged review")
	require.Equal(t, "matched", string(review.Verification))
	require.Equal(t, nativeD10Revision(t, body), review.Revision)
	req.ReviewId = &review.ReviewId
	published := post("/api/v1/skills/install", req)
	require.Equal(t, http.StatusOK, published.Code, "actual reviewed publication: %s", published.Body.String())
	assertNativeD10VerifiedInventory(t, h.api, key, body)
}

func assertNativeD10VerifiedInventory(t *testing.T, api *restAPI, key, body string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(api.homePath, "skills", key, "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, body, string(raw), "native publication preserves the complete registry bytes")
	w := httptest.NewRecorder()
	api.HandleSkills(w, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)))
	require.Equal(t, http.StatusOK, w.Code, "independent native inventory: %s", w.Body.String())
	var listed []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	for _, skill := range listed {
		if skill.Id == key {
			require.True(t, skill.Verified, "C-5 install-time verification must survive into the actual independent read; a true install reply alone is not evidence")
			require.Equal(t, nativeD10Revision(t, body), skill.Revision)
			return
		}
	}
	t.Fatalf("BLOCKED: C-5 native inventory does not list reviewed verified fixture %s", key)
}

// Install setup controls only the external registry's package/result. The actual
// installer stages, verifies its preparation contract and publishes/cleans pins.
type nativeD10Registry struct {
	body     string
	verified bool
}

func (*nativeD10Registry) Name() string { return "d10-local" }
func (*nativeD10Registry) Search(context.Context, string, int) ([]skills.SearchResult, error) {
	return nil, nil
}
func (*nativeD10Registry) GetSkillMeta(_ context.Context, slug string) (*skills.SkillMeta, error) {
	return &skills.SkillMeta{Slug: slug, RegistryName: "d10-local"}, nil
}
func (r *nativeD10Registry) DownloadAndInstall(_ context.Context, _ string, _ string, target string) (*skills.InstallResult, error) {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(r.body), 0o600); err != nil {
		return nil, err
	}
	return &skills.InstallResult{Version: "1.0.0", Verified: r.verified}, nil
}
func prepareNativeD10Install(t *testing.T, h *nativeSkillHarness, in d10RawInput) {
	t.Helper()
	manager := skills.NewRegistryManager()
	manager.AddRegistry(&nativeD10Registry{body: in.currentBody, verified: in.verified})
	installer := tools.NewInstallSkillTool(manager, filepath.Join(h.api.homePath, "skills"))
	if _, ok := any(installer).(tools.ApprovalPreparer); !ok {
		t.Fatal("BLOCKED: native install pre-card staging is not implemented — required by C-12 rows 6–8/D10#23–26; no seeded approval or fake installer")
	}
	h.api.agentLoop.RegisterTool(installer)
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	ceiling := map[string]config.ToolPolicy{}
	for key, value := range h.api.agentLoop.GetConfig().Sandbox.ToolPolicies {
		ceiling[key] = config.ToolPolicy(value)
	}
	ceiling["install_skill"] = config.ToolPolicy(in.installPolicy)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: ceiling})
	h.script.tool, h.script.args = "install_skill", map[string]any{"slug": "fresh-helper", "registry": "d10-local"}
}
func assertNativeD10Stored(t *testing.T, h *nativeSkillHarness, key, value string) {
	t.Helper()
	var configDocument map[string]any
	require.NoError(t, json.Unmarshal(readNativeConfig(t, h.api.homePath), &configDocument))
	sandbox, ok := configDocument["sandbox"].(map[string]any)
	require.True(t, ok, "stored config sandbox must be an object")
	policies, ok := sandbox["skill_policies"].(map[string]any)
	require.True(t, ok, "BLOCKED: actual stored skill policy map required by C-2/D10#18")
	assert.Equal(t, value, policies[key])
}
func assertNativeD10TimedAudit(t *testing.T, h *nativeSkillHarness, entry *approvalEntry, key string, end time.Time) {
	t.Helper()
	count := 0
	for _, record := range readAllAuditEntries(t, filepath.Join(h.api.homePath, "system")) {
		fields, _ := record["fields"].(map[string]any)
		if record["event"] != audit.EventToolPolicyAskGranted || fields["approval_id"] != entry.ApprovalID {
			continue
		}
		count++
		assert.Equal(t, "test-agent", fields["agent_id"])
		assert.Equal(t, "admin", fields["approver_user_id"])
		assert.Equal(t, "allow_for", fields["action"])
		assert.Equal(t, "1d", fields["duration"])
		assert.Contains(t, nativeAuditScalarValues(fields), key, "exact skill subject, not any timed event")
		rawEnd, ok := fields["expires_at"].(string)
		require.True(t, ok)
		got, err := time.Parse(time.RFC3339Nano, rawEnd)
		require.NoError(t, err)
		assert.Equal(t, end, got.UTC())
	}
	assert.Equal(t, 1, count)
}

func nativeD10TrustAudit(t *testing.T, h *nativeSkillHarness, key string) int {
	t.Helper()
	count := 0
	for _, record := range readAllAuditEntries(t, filepath.Join(h.api.homePath, "system")) {
		fields, _ := record["fields"].(map[string]any)
		if record["event"] == "skill.trust_approved" && fields["key"] == key {
			count++
			assert.Equal(t, nativeD10Revision(t, nativeSkillR1), fields["revision"])
			assert.Equal(t, "admin", fields["actor"])
		}
	}
	return count
}
func nativeD10AllowanceEnded(t *testing.T, h *nativeSkillHarness) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, record := range readAllAuditEntries(t, filepath.Join(h.api.homePath, "system")) {
		if record["event"] == "approval.allowance_ended" {
			records = append(records, record)
		}
	}
	return records
}

func assertNativeD10WaitingRefusal(t *testing.T, h *nativeSkillHarness, run nativeSkillRun, reason string) {
	t.Helper()
	var refusals []string
	for _, request := range h.script.captured(run.prompt) {
		for _, message := range request {
			if message.Role == "tool" {
				refusals = append(refusals, message.Content)
			}
		}
	}
	require.Len(t, refusals, 1, "one actual waiting-call refusal reaches the next model request")
	var denial gen.PermissionDenied
	require.NoError(t, json.Unmarshal([]byte(refusals[0]), &denial))
	assert.Equal(t, "permission_denied", denial.Error)
	assert.Equal(t, "Skill", denial.Tool)
	assert.True(t, denial.Permanent)
	require.NotEmpty(t, denial.Reason)
	require.NotEmpty(t, denial.Message)
	// C-24 prescribes the identical sentence, not a new PermissionDenied field
	// encoding. The existing contract permits a classification code in Reason
	// and puts model-facing guidance in Message. Require the whole sentence in
	// one of those actual fields; a substring of arbitrary JSON is insufficient.
	assert.Contains(t, []string{denial.Reason, denial.Message}, reason, "C-24: the actual waiting call receives the exact re-check reason")
}

func nativeD10StagedFiles(t *testing.T, home string) []string {
	t.Helper()
	root := filepath.Join(home, "skills")
	require.DirExists(t, root, "known-positive actual installed shelf before trusting an absent staging container")
	_, err := os.Stat(filepath.Join(root, ".staging"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return nativeStagedSkillFiles(t, root)
}

func assertNativeD10InstallRefused(t *testing.T, h *nativeSkillHarness, run nativeSkillRun) {
	t.Helper()
	var results []string
	for _, request := range h.script.captured(run.prompt) {
		for _, message := range request {
			if message.Role == "tool" {
				results = append(results, message.Content)
			}
		}
	}
	require.Len(t, results, 1, "the native installer call is actually refused, not omitted or merely card-free")
	var refusal gen.PermissionDenied
	require.NoError(t, json.Unmarshal([]byte(results[0]), &refusal))
	assert.Equal(t, "permission_denied", refusal.Error)
	assert.Equal(t, "install_skill", refusal.Tool)
	assert.True(t, refusal.Permanent)
	assert.NotEmpty(t, refusal.Reason, "D10#26 does not prescribe the load-only unattended sentence for install; use the generated refusal carrier")
	assert.NoDirExists(t, filepath.Join(h.api.homePath, "skills", "fresh-helper"))
	assert.Empty(t, nativeD10StagedFiles(t, h.api.homePath), "actual staging inventory, including a removed empty staging container")
	assert.NoFileExists(t, trustApprovalsPath(h.api))
}
