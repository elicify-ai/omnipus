//go:build goolm && stdjson

package agent

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), Part L.
// TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome, the pkg/agent
// half: the entry points of C-11 that live in this package — E1 (the Skill
// load), E2 (the skill menu), E3 (the Skill search), E4 (the person's /name),
// E5 (delegation's requested_skill), E8 (the Full level) and E9 (ToolSearch's
// skill hits). E6 and E7 (file tools, kernel grant, bash helpers) live in
// pkg/tools and are the same-named test there
// (skill_entry_points_one_outcome_red_test.go).
//
// Oracle: skills-tools-redesign-spec.md C-11 ("Every entry point below consumes
// that outcome; none computes its own") with its E1–E9 rows, FR-066, C-12 (which
// approval settles which open check), C-14 (Full) and C-17 (ToolSearch), and
// Dataset D1 for the outcome of each stored combination. epRows below is a
// transcription of D1 rows 1–12 and 14 (spec lines "Dataset D1"); row 13 is the
// resolver's own tripwire and cannot be reached through stored data (the C-2
// self-heal fills every skill's global value first), so it is covered in
// pkg/tools::TestSkillPolicyResolution_Table only.
//
// What is observed. The test never calls the resolver and never names a
// production decision function. Each row writes the stored state where the
// product stores it (the global skill value, trust and God Mode in the loop's
// config; the agent's own value in the agent's persisted record; the grant; the
// skill folder), boots the REAL loop on it, and drives the real entry point the
// way a person or a model reaches it:
//
//	E1  a scripted model calls Skill{name} in a real turn (attended: the
//	    recording approver is the card; unattended: a real task run)
//	E2  a real turn's request carries (or does not carry) the skill's menu entry
//	E3  the Skill tool the loop registered, searched by query
//	E4  a real turn whose message is "/<slug> ..."
//	E5  the loop's steer launcher, launched with requested_skill
//	E8  a real turn with the skill stored at the Full level
//	E9  the ToolSearch tool the loop registered, searched by query, the skill
//	    stored at Hidden
//
// and compares what it did with the outcome the dataset states. An entry point
// that reads only the grant (the landing base), or computes its own answer,
// disagrees with the dataset on at least one row; one that reads the one
// decision cannot. The rows are chosen so that every stored layer decides at
// least one row (global, agent, trust, verified, God Mode, grant), so no single
// wrong answer to a layer passes.
//
// The skill folder, its description and its instructions sentinel are the same
// in every world; only the stored state differs. Verified rows use the built-in
// slug deep-research (C-5: a built-in skill is verified with no install record)
// and unverified rows the hand-copied notes-helper, which S-29 itself uses as
// its unverified example.
//
// Compile note: this file names no symbol that exists only in a tree carrying
// the backend. The stored keys (sandbox.skill_policies, sandbox.skill_trust,
// sandbox.skill_visibility, the agent's tools.skills.policies) are written as
// literal JSON through the real decoders and checked for being carried; the
// approval card's open checks are read by reflection. On a tree without the
// backend the stored values are dropped by the decoder, the entry points answer
// from the grant alone, and every row that the dataset says is not "allowed"
// fails with the entry point's own wrong behaviour; the first subtest also ends
// with BLOCKED naming the missing keys.
//
// E5 and the ask_pending rows. This test takes the allow, deny, trust-deny and
// not-granted rows of E5 (the delegate tool's own contract: "a hard request, not
// a hint"). The four ask_pending rows (D1#3, #6, #9, #10) are
// TestSkillPolicyResolution_DelegationAskPending in
// skill_delegation_ask_pending_red_test.go (Part M, team-lead's order of
// 2026-11-27); its header says what the spec fixes there and what it leaves
// open. The same section says E5 gives "the instructions, in the child"; no
// mechanism is named, so delivery of the instructions to the child is not
// asserted here either.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	epAgent       = "mia"
	epDescription = "Research sources across the web."
	epQuery       = "research sources"
	epSentinel    = "EP-ONE-OUTCOME-SENTINEL-7K"
	epCallID      = "Skill-0" // the id ScenarioProvider.WithToolCall gives its call
	epLoadHint    = "Load it with the Skill tool."
	epTrustTail   = "is unverified, and unverified skills are set to Deny in Skills & Tools."
)

// epRow is one Dataset D1 row. want is the dataset's outcome ("allow",
// "ask_pending", "deny"); reason says why a deny row is a deny; trustOpen says
// the open check of an ask_pending row is the trust check (the rest are the
// permission check).
type epRow struct {
	name       string
	granted    bool
	global     string
	agentValue string
	trust      string
	verified   bool
	godMode    bool
	want       string
	reason     string
	trustOpen  bool
}

// epRows transcribes Dataset D1 (spec: "Dataset D1: Effective skill permission").
var epRows = []epRow{
	{name: "D1#1 allow, none, trust ask, verified", granted: true, global: "allow", trust: "ask", verified: true, want: "allow"},
	{name: "D1#2 allow, agent deny, trust ask, verified", granted: true, global: "allow", agentValue: "deny", trust: "ask", verified: true, want: "deny", reason: "permission"},
	{name: "D1#3 global ask, agent allow, trust ask, verified", granted: true, global: "ask", agentValue: "allow", trust: "ask", verified: true, want: "ask_pending"},
	{name: "D1#4 global deny, none, trust allow, verified", granted: true, global: "deny", trust: "allow", verified: true, want: "deny", reason: "permission"},
	{name: "D1#5 allow, none, trust deny, unverified", granted: true, global: "allow", trust: "deny", want: "deny", reason: "trust"},
	{name: "D1#6 allow, none, trust ask, unverified", granted: true, global: "allow", trust: "ask", want: "ask_pending", trustOpen: true},
	{name: "D1#7 allow, none, trust allow, unverified", granted: true, global: "allow", trust: "allow", want: "allow"},
	{name: "D1#8 global ask, none, trust allow, unverified, God Mode", granted: true, global: "ask", trust: "allow", godMode: true, want: "allow"},
	{name: "D1#9 allow, none, trust ask, unverified, God Mode", granted: true, global: "allow", trust: "ask", godMode: true, want: "ask_pending", trustOpen: true},
	{name: "D1#10 allow, agent ask, trust ask, verified, God Mode", granted: true, global: "allow", agentValue: "ask", trust: "ask", verified: true, godMode: true, want: "ask_pending"},
	{name: "D1#11 allow, none, trust deny, built-in verified", granted: true, global: "allow", trust: "deny", verified: true, want: "allow"},
	{name: "D1#12 global deny, agent allow, trust allow, verified, God Mode", granted: true, global: "deny", agentValue: "allow", trust: "allow", verified: true, godMode: true, want: "allow"},
	{name: "D1#14 allow, none, trust ask, verified, not granted", granted: false, global: "allow", trust: "ask", verified: true, want: "deny", reason: "not_granted"},
}

func epSlug(row epRow) string {
	if row.verified {
		return "deep-research"
	}
	return "notes-helper"
}

// openChecks is what the card of an ask_pending row must say is open (C-12).
func (row epRow) openChecks() []string {
	if row.trustOpen {
		return []string{"trust"}
	}
	return []string{"permission"}
}

// epOptions are the per-entry-point differences between worlds.
type epOptions struct {
	visibility string // the skill's stored level ("" = none stored)
	skillCall  bool   // the scripted model calls Skill{name} before answering
	approve    bool   // what the recording approver answers a card
	unattended bool   // the production task-start wiring, for a real task run
}

// epWorld is one real loop holding one Dataset D1 row.
type epWorld struct {
	t        *testing.T
	row      epRow
	slug     string
	home     string
	al       *AgentLoop
	provider *testutil.ScenarioProvider
	approver *autoRecordingApprover
}

// newEPWorld stores the row's state where the product stores it and boots the
// real loop on it. The shipped built-in skills are pointed at an empty
// directory so no machine-specific built-in folder stands in for the fixture's.
func newEPWorld(t *testing.T, row epRow, opts epOptions) *epWorld {
	t.Helper()
	slug := epSlug(row)
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv(config.EnvBuiltinSkills, t.TempDir())
	withGodModeAvailable(t, row.godMode)

	dir := filepath.Join(home, "skills", slug)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body := "---\nname: " + slug + "\ndescription: " + epDescription + "\n---\n\n" + epSentinel + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))

	// The agent's own value is literal JSON through the real decoder, so a tree
	// that has no such field drops it instead of failing to compile.
	var agentCfg config.AgentConfig
	if row.agentValue != "" {
		own := `{"tools":{"skills":{"policies":{"` + slug + `":"` + row.agentValue + `"}}}}`
		require.NoError(t, json.Unmarshal([]byte(own), &agentCfg), "decode the agent's own skill value")
	}
	agentCfg.ID = epAgent
	agentCfg.Skills = []string{slug}
	if !row.granted {
		agentCfg.Skills = []string{"some-other-skill"}
	}
	persisted := persistSkillGrantFixture(t, home, agentCfg)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "ep-scripted"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = []config.AgentConfig{persisted}
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, row.godMode
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.ToolPolicies["Skill"] = "allow" // C-0: the infrastructure tool is fixed Allow.
	cfg.Tools.Manifest.Compressed = false       // discovery of tools is not this test's subject.
	cfg.Planning.GoalMaxRounds = 1              // a task run ends after its one scripted call.
	stored := map[string]any{"skill_policies": map[string]string{slug: row.global}, "skill_trust": row.trust}
	if opts.visibility != "" {
		stored["skill_visibility"] = map[string]string{slug: opts.visibility}
	}
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	if err := json.Unmarshal(encoded, &cfg.Sandbox); err != nil {
		// A tree whose config decoder does not take the stored skill keys of this row
		// (the landing base rejects the trust value "ask": its vocabulary is
		// block_unverified, warn_unverified, allow_all) does not implement C-11's inputs.
		// Say so, loudly, and print the decoder's own words, so another cause stays visible.
		t.Fatalf("BLOCKED: the config decoder does not take the stored skill keys of row %q (sandbox.skill_policies %q, sandbox.skill_trust %q, sandbox.skill_visibility %q) — required by C-2/C-6/C-11/C-13 and Dataset D1; the decoder said: %v",
			row.name, row.global, row.trust, opts.visibility, err)
	}

	provider := testutil.NewScenario()
	if opts.skillCall {
		provider.WithToolCall("Skill", `{"name":"`+slug+`"}`)
	}
	provider.WithText("done")
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	if opts.unattended {
		installTestSessionLauncher(t, al) // the production task-start wiring: session task id + lifecycle record
	}
	approver := &autoRecordingApprover{approve: opts.approve}
	al.SetToolApprover(approver)

	mia, ok := al.GetRegistry().GetAgent(epAgent)
	require.True(t, ok, "fixture: the loop has the agent %q", epAgent)
	mia.ContextBuilder.WithSkillAllowlist(persisted.Skills).WithSkillBindings(persisted.SkillGrants)
	_, resolved := mia.ContextBuilder.ResolveSkillFullForWorkspace("", slug)
	require.Equal(t, row.granted, resolved,
		"fixture: the skill is bound to the agent's real installed generation exactly when the row says it is granted")
	return &epWorld{t: t, row: row, slug: slug, home: home, al: al, provider: provider, approver: approver}
}

// turn runs one direct turn as the agent.
func (w *epWorld) turn(content, sessionKey string) {
	w.t.Helper()
	_, err := w.al.processDirectPrimaryForTest(context.Background(), content, sessionKey)
	require.NoError(w.t, err, "the turn ran")
}

// skillCards are the approval requests the loop raised for a Skill call.
func (w *epWorld) skillCards() []PolicyApprovalReq {
	var cards []PolicyApprovalReq
	for _, req := range w.approver.requests() {
		if req.ToolName == "Skill" {
			cards = append(cards, req)
		}
	}
	return cards
}

// requestText is the text of the i-th model request: every message's content.
func (w *epWorld) requestText(i int) string {
	w.t.Helper()
	requests := w.provider.AllRequests()
	require.Greater(w.t, len(requests), i, "fixture: the turn sent request #%d to the model", i)
	var b strings.Builder
	for _, m := range requests[i] {
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// allRequestText is the text of every model request of the world.
func (w *epWorld) allRequestText() string {
	w.t.Helper()
	var b strings.Builder
	for i := range w.provider.AllRequests() {
		b.WriteString(w.requestText(i))
	}
	return b.String()
}

// toolResult is the content of the tool-result message the loop sent back to
// the model for callID, from the latest request that carries it.
func (w *epWorld) toolResult(callID string) (string, bool) {
	requests := w.provider.AllRequests()
	for i := len(requests) - 1; i >= 0; i-- {
		for _, m := range requests[i] {
			if m.Role == "tool" && m.ToolCallID == callID {
				return m.Content, true
			}
		}
	}
	return "", false
}

// tool returns the tool the real loop registered for the agent under name.
func (w *epWorld) tool(name string) tools.Tool {
	w.t.Helper()
	inst, ok := w.al.GetRegistry().GetAgent(epAgent)
	require.True(w.t, ok, "the loop has the agent %q", epAgent)
	tool, ok := inst.Tools.Get(name)
	require.True(w.t, ok, "the real loop registers %s for %q", name, epAgent)
	return tool
}

// loadRows are the skill.call audit rows of mode "load" the loop has written.
func (w *epWorld) loadRows() []map[string]any {
	w.t.Helper()
	var rows []map[string]any
	for _, entry := range readAuditEvents(w.t, filepath.Join(w.al.homePath, "system", "audit.jsonl")) {
		details, _ := entry["details"].(map[string]any)
		if entry["event"] == string(audit.EventSkillCall) && details["mode"] == "load" {
			rows = append(rows, entry)
		}
	}
	return rows
}

// epOpenChecks reads the open checks the approval card names (C-9: the card's
// skill object carries open_checks, an array of "permission" and "trust"). It
// reads them by reflection so this file compiles on a tree whose approval
// request has no such field; there the card has no facts and the test ends with
// BLOCKED.
func epOpenChecks(t *testing.T, req PolicyApprovalReq) []string {
	t.Helper()
	field := reflect.ValueOf(req).FieldByName("Skill")
	if !field.IsValid() {
		t.Fatalf("BLOCKED: PolicyApprovalReq has no Skill field — required by C-9/C-12 (the approval card carries the skill's open checks)")
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			t.Fatalf("a Skill load's approval request carries no skill facts (C-9: key, revision, open checks)")
		}
		field = field.Elem()
	}
	checks := field.FieldByName("OpenChecks")
	if checks.IsValid() && checks.Kind() == reflect.Pointer && !checks.IsNil() {
		checks = checks.Elem() // an optional array of the generated type
	}
	if !checks.IsValid() || checks.Kind() != reflect.Slice {
		t.Fatalf("BLOCKED: the approval card's skill object has no OpenChecks list — required by C-9/C-12")
	}
	open := make([]string, 0, checks.Len())
	for i := 0; i < checks.Len(); i++ {
		open = append(open, fmt.Sprint(checks.Index(i).Interface()))
	}
	sort.Strings(open)
	return open
}

// epRefusalMessage is the sentence a refused Skill call carries. A refusal
// reaches the model as a JSON envelope ({"error":..., "message":"<sentence>
// Tool execution denied by policy.", "reason":..., "tool":...}), and
// encoding/json writes "&" inside it as the six characters \u0026 — so a sentence
// the spec words with an ampersand ("Skills & Tools", S-29) is read from the decoded
// message, not from the raw bytes (the first run of this file read the raw bytes and
// failed S-29's own sentence for that reason: a defect of this test, not of the
// product). A result that is not such an envelope is its own message.
func epRefusalMessage(text string) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err == nil && envelope.Message != "" {
		return envelope.Message
	}
	return text
}

// epRequireRefusal asserts text is the refusal of a deny row: it names the
// setting (C-11 E1, C-12 "the refusal names the setting"), is not the
// unattended "needs approval" sentence (a Deny refuses before either check), and
// never carries the skill's instructions. The two negative checks read the raw
// bytes (the stronger reading); the sentences are read from the raw bytes and from
// the decoded message together (see epRefusalMessage).
func epRequireRefusal(t *testing.T, row epRow, slug, text, where string) {
	t.Helper()
	require.NotContains(t, text, epSentinel, "%s: a refused skill's instructions never reach the model: %q", where, text)
	require.NotContains(t, text, "needs approval", "%s: a Deny refuses before either check, so it is not an approval refusal: %q", where, text)
	readable := text + "\n" + epRefusalMessage(text)
	switch row.reason {
	case "trust":
		require.Contains(t, readable, slug, "%s: the refusal names the skill: %q", where, text)
		require.Contains(t, readable, epTrustTail, "%s: S-29's refusal for an unverified skill under trust Deny: %q", where, text)
	case "permission":
		// S-09: "refused naming the skill setting" — the skill, and the Deny it is set to.
		require.Contains(t, readable, slug, "%s: the refusal names the skill: %q", where, text)
		require.Contains(t, readable, "Deny", "%s: the refusal names the setting that denies it: %q", where, text)
	case "not_granted":
		require.Contains(t, strings.ToLower(readable), "not granted", "%s: S-14 refuses it as not granted: %q", where, text)
	default:
		t.Fatalf("test defect: deny row %q names no reason", row.name)
	}
}

// epSkillLine returns the first ToolSearch result line that starts with the
// skill label and slug, the way S-96 words it ("a result line starts with ...").
func epSkillLine(out, slug string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[skill] "+slug) {
			return line, true
		}
	}
	return "", false
}

// epCheckStoredKeysCarried is the instrument of the whole test: a world that
// stores every key must show every key in the loop's live configuration and the
// agent's own value in the agent's persisted record. A tree that drops one ends
// the test with BLOCKED, not with a wrong "allowed".
func epCheckStoredKeysCarried(t *testing.T) {
	t.Helper()
	row := epRows[2] // D1#3 stores a global value, the agent's own value, trust and (below) a level.
	w := newEPWorld(t, row, epOptions{visibility: "full"})
	encoded, err := json.Marshal(w.al.GetConfig().Sandbox)
	require.NoError(t, err)
	var stored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &stored))
	var missing []string
	for _, key := range []string{"skill_policies", "skill_trust", "skill_visibility"} {
		if len(stored[key]) == 0 {
			missing = append(missing, "sandbox."+key)
		}
	}
	record, err := json.Marshal(w.al.GetConfig().Agents.List)
	require.NoError(t, err)
	var agents []struct {
		ID    string `json:"id"`
		Tools *struct {
			Skills *struct {
				Policies map[string]string `json:"policies"`
			} `json:"skills"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(record, &agents))
	own := ""
	for _, a := range agents {
		if a.ID == epAgent && a.Tools != nil && a.Tools.Skills != nil {
			own = a.Tools.Skills.Policies[w.slug]
		}
	}
	if own != row.agentValue {
		missing = append(missing, "agents.<id>.tools.skills.policies")
	}
	if len(missing) > 0 {
		t.Fatalf("BLOCKED: the loop's configuration does not carry %s — required by C-2/C-6/C-11 (the stored skill values every entry point consumes)", strings.Join(missing, ", "))
	}
}

// epCheckE1Attended: E1, a model calls Skill{name} in an attended turn.
func epCheckE1Attended(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{skillCall: true})
	w.turn("Load "+w.slug+".", "ep-e1-attended")
	cards := w.skillCards()
	result, reached := w.toolResult(epCallID)

	switch row.want {
	case "allow":
		require.Empty(t, cards, "E1 allowed: the load needs no card")
		require.True(t, reached, "fixture: the Skill call's result reached a later model request")
		require.Contains(t, result, epSentinel, "E1 allowed: the load returns the instructions")
	case "ask_pending":
		require.Len(t, cards, 1, "E1 ask_pending, interactive: one approval card")
		require.Equal(t, row.openChecks(), epOpenChecks(t, cards[0]), "E1: the card names exactly the open check (C-12)")
		require.NotContains(t, w.allRequestText(), epSentinel, "E1 ask_pending: nothing is loaded before the person decides")
	case "deny":
		require.Empty(t, cards, "E1 deny: refused with no card (C-12)")
		require.True(t, reached, "fixture: the Skill call's refusal reached a later model request")
		epRequireRefusal(t, row, w.slug, result, "E1")
	}
}

// epCheckE1Approved: E1, the person approves the card of a permission-only Ask:
// "Approve Once" settles the permission check (C-12), so the load goes through.
func epCheckE1Approved(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{skillCall: true, approve: true})
	w.turn("Load "+w.slug+".", "ep-e1-approved")
	require.Len(t, w.skillCards(), 1, "E1 ask_pending, interactive: one approval card")
	result, reached := w.toolResult(epCallID)
	require.True(t, reached, "fixture: the Skill call's result reached a later model request")
	require.Contains(t, result, epSentinel, "E1: approving the permission check loads the skill")
}

// epCheckE1Unattended: E1, the same call in a real task run, which never asks.
func epCheckE1Unattended(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{skillCall: true, unattended: true})
	sessionID := runRealUnattendedTask(t, w.al, epAgent, "Load "+w.slug+".")
	require.Empty(t, w.approver.requests(), "an unattended run never asks (C-12)")

	requests := w.provider.AllRequests()
	require.GreaterOrEqual(t, len(requests), 2, "fixture: the Skill call's result reached a later model request")
	var toolResults []string
	for _, m := range requests[1] {
		if m.Role == "tool" {
			toolResults = append(toolResults, m.Content)
		}
	}
	require.Len(t, toolResults, 1, "one result for the one Skill call")

	switch row.want {
	case "allow":
		require.Contains(t, toolResults[0], epSentinel, "E1 allowed, unattended: the load returns the instructions")
		require.NotContains(t, toolResults[0], unattendedSkillRefusal, "an allowed load is not refused")
	case "ask_pending":
		entries, err := w.al.GetSessionStore().ReadTranscript(sessionID)
		require.NoError(t, err)
		var denied []string
		for _, entry := range entries {
			for _, call := range entry.ToolCalls {
				if text, ok := call.Result["text"].(string); ok && call.Tool == "Skill" && call.Status == "denied" {
					denied = append(denied, text)
				}
			}
		}
		require.Equal(t, []string{unattendedSkillRefusal}, denied, "E1 ask_pending, unattended: the one refusal is C-12's sentence")
		require.NotContains(t, w.allRequestText(), epSentinel, "E1 ask_pending, unattended: nothing is loaded")
	case "deny":
		epRequireRefusal(t, row, w.slug, toolResults[0], "E1 unattended")
	}
}

// epCheckE2: the skill menu of a real turn.
func epCheckE2(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{})
	w.turn("hello", "ep-e2")
	menu := w.requestText(0)
	if row.want == "deny" {
		require.NotContains(t, menu, w.slug, "E2: a skill whose outcome is deny is not listed")
		return
	}
	require.Contains(t, menu, w.slug, "E2: a skill whose outcome is not deny is listed")
	require.Contains(t, menu, epDescription, "E2: at its default level (name and description) the menu carries the description")
}

// epCheckE3: the Skill tool's search.
func epCheckE3(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{})
	res := w.tool("Skill").Execute(execCtx(epAgent, "ep-e3"), map[string]any{"query": epQuery})
	found := strings.Contains(res.ForLLM, `"name":"`+w.slug+`"`)
	if row.want == "deny" {
		require.False(t, found, "E3: a skill whose outcome is deny is not found: %s", res.ForLLM)
		return
	}
	require.False(t, res.IsError, "E3: the search answers: %s", res.ForLLM)
	require.True(t, found, "E3: a skill whose outcome is not deny is found: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, epSentinel, "E3: a search never returns the instructions")
}

// epCheckE4: the person's own /name.
func epCheckE4(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{})
	w.turn("/"+w.slug+" go", "ep-e4")
	cards := w.skillCards()
	loaded := strings.Contains(w.allRequestText(), epSentinel)

	switch {
	case row.want == "allow":
		require.Empty(t, cards, "E4 allowed: no card")
		require.True(t, loaded, "E4 allowed: the person's /name loads the skill")
	case row.want == "ask_pending" && !row.trustOpen:
		require.Empty(t, cards, "E4: the person asking by name is the consent for an ordinary skill Ask (C-12), so no card")
		require.True(t, loaded, "E4: an ordinary Ask loads on the person's /name")
	case row.want == "ask_pending":
		require.Len(t, cards, 1, "E4: a trust Ask is not bypassed by /name: the trust card comes first (ST-Q2 A)")
		require.Equal(t, row.openChecks(), epOpenChecks(t, cards[0]), "E4: the card names the trust check")
		require.False(t, loaded, "E4: nothing is loaded before the person decides the trust card")
	default:
		require.Empty(t, cards, "E4 deny: refused with no card")
		require.False(t, loaded, "E4: a Deny is not bypassed by the person's /name")
	}
}

// epSteeredRecords counts the lifecycle records that carry a steering edge: the
// children a delegation launched (the steerer's own root record has no edge).
func epSteeredRecords(t *testing.T, al *AgentLoop) int {
	t.Helper()
	records, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	require.NoError(t, err)
	steered := 0
	for _, rec := range records {
		if rec.SteeredBy != nil {
			steered++
		}
	}
	return steered
}

// epCheckE5: delegation's requested_skill, decided for the child. The launch is
// the production delegate launch (a steering session, one minted boot epoch and
// the lifecycle stores, as the sibling tests of steer_launcher_test.go build
// them); the child is the same agent, which the launcher allows.
func epCheckE5(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{})
	lifecycle := session.NewLifecycleStore(filepath.Join(w.home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(w.home, "session_messages"))
	w.al.SetSessionMessagingStores(inbox, lifecycle)
	epochDir := filepath.Join(w.home, "boot_epoch")
	require.NoError(t, os.MkdirAll(epochDir, 0o700))
	boot := session.NewBootEpochStore(epochDir)
	epoch, err := boot.Mint()
	require.NoError(t, err)
	require.NotZero(t, epoch, "fixture: one genuine boot epoch, as the gateway mints at boot")
	w.al.SetBootEpochStore(boot)
	steerer, err := w.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", epAgent)
	require.NoError(t, err, "fixture: the steering session")
	require.Zero(t, epSteeredRecords(t, w.al), "fixture: no child exists before the launch")

	res, err := NewSteerLauncher(w.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer.ID,
		TargetAgentID:     epAgent,
		Task:              "Use the skill.",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "ep-e5"},
		RequestedSkill:    w.slug,
	})

	if row.want == "allow" {
		require.NoError(t, err, "E5 allowed: the delegation launches")
		require.NotEmpty(t, res.SessionID, "E5 allowed: a child session was started")
		require.Equal(t, 1, epSteeredRecords(t, w.al), "E5 allowed: the child's steering edge is recorded")
		return
	}
	require.Error(t, err, "E5 deny, %s: C-11 has every entry point consume the one outcome and a Deny refuses; requested_skill is a hard request, so the whole delegation must be refused — the launch started child session %q instead", row.name, res.SessionID)
	require.Equal(t, steer.LaunchResult{}, res, "E5 deny: no child session")
	require.Zero(t, epSteeredRecords(t, w.al), "E5 deny: no child was recorded for a refused launch")
	// C-12: a Deny refuses "and the refusal names the setting" — a skill's
	// setting is named by the skill.
	require.Contains(t, err.Error(), w.slug, "E5 deny: the refusal names the skill whose setting refuses it: %v", err)
	if row.reason == "not_granted" {
		require.ErrorIs(t, err, tools.ErrRequestedSkillDenied, "E5: a skill the child is not granted is the existing 'denied' refusal (characterization of ADR-072 D9)")
	}
}

// epCheckE8: the Full level.
func epCheckE8(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{visibility: "full"})
	w.turn("hello", "ep-e8")
	request := w.requestText(0)
	switch row.want {
	case "allow":
		require.Contains(t, request, epSentinel, "E8: a Full skill whose outcome is allowed is injected from the first turn")
	case "ask_pending":
		require.NotContains(t, request, epSentinel, "E8: Full never counts as consent: an ask_pending skill is not injected")
		require.Contains(t, request, w.slug, "E8: an ask_pending Full skill is listed as name and short description")
		require.Contains(t, request, epDescription, "E8: an ask_pending Full skill is listed as name and short description")
	case "deny":
		require.NotContains(t, request, epSentinel, "E8: a denied Full skill is not injected")
		require.NotContains(t, request, w.slug, "E8: a denied Full skill is not listed")
	}
}

// epCheckE9: ToolSearch's skill hits, the skill stored at Hidden.
func epCheckE9(t *testing.T, row epRow) {
	t.Helper()
	w := newEPWorld(t, row, epOptions{visibility: "hidden"})
	res := w.tool("ToolSearch").Execute(execCtx(epAgent, "ep-e9"), map[string]any{"query": epQuery})
	require.False(t, res.IsError, "ToolSearch answers a query: %s", res.ForLLM)
	line, found := epSkillLine(res.ForLLM, w.slug)
	if row.want == "deny" {
		require.False(t, found, "E9: a skill whose outcome is deny is not found: %s", res.ForLLM)
	} else {
		require.True(t, found, "E9: a skill whose outcome is not deny is found, at any level including Hidden: %s", res.ForLLM)
		require.Contains(t, line, epLoadHint, "E9: the hit says how to load it")
	}
	require.NotContains(t, res.ForLLM, epSentinel, "E9: a search never returns the instructions")
	require.Empty(t, w.loadRows(), "E9: a search never loads a skill")
}

// TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome (pkg/agent half:
// E1–E5, E8 and E9).
//
// Traces to: BDD S-12, S-13, S-14, S-29, S-30, S-31, S-96, US-2 AS9, ST-19;
// spec C-11 (E1–E5, E8, E9), C-12, C-14, C-17, FR-066; Dataset D1.
func TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome(t *testing.T) {
	t.Run("instrument the stored skill keys are carried by the loop's configuration", epCheckStoredKeysCarried)

	// The instrument of every table below: a table with no allowed row, no
	// ask_pending row or no deny row could not tell "reads the one outcome" from
	// "allows everything" or "refuses everything".
	counts := map[string]int{}
	for _, row := range epRows {
		counts[row.want]++
	}
	for _, outcome := range []string{"allow", "ask_pending", "deny"} {
		require.NotZero(t, counts[outcome], "the rows must include %s rows", outcome)
	}

	byEntryPoint := []struct {
		name  string
		check func(*testing.T, epRow)
		rows  func(epRow) bool
	}{
		{"E1 Skill load, attended: the outcome decides between the instructions, a card and a refusal", epCheckE1Attended, nil},
		{"E1 Skill load, approved: approving a permission-only Ask loads the skill", epCheckE1Approved,
			func(r epRow) bool { return r.want == "ask_pending" && !r.trustOpen }},
		{"E1 Skill load, unattended task run: Ask is refused with C-12's sentence and nothing is asked", epCheckE1Unattended,
			func(r epRow) bool {
				return strings.HasPrefix(r.name, "D1#1 ") || strings.HasPrefix(r.name, "D1#2 ") || strings.HasPrefix(r.name, "D1#3 ") || strings.HasPrefix(r.name, "D1#5 ") || strings.HasPrefix(r.name, "D1#6 ") || strings.HasPrefix(r.name, "D1#14 ")
			}},
		{"E2 skill menu: listed exactly when the outcome is not deny", epCheckE2, nil},
		{"E3 Skill search: found exactly when the outcome is not deny", epCheckE3, nil},
		{"E4 the person's /name: consent for an ordinary Ask, never for a Deny or a trust Ask", epCheckE4, nil},
		{"E5 delegation requested_skill: decided for the child (the ask_pending rows are TestSkillPolicyResolution_DelegationAskPending)", epCheckE5,
			func(r epRow) bool { return r.want != "ask_pending" }},
		{"E8 Full level: injected exactly when the outcome is allowed", epCheckE8, nil},
		{"E9 ToolSearch skill hits: found exactly when the outcome is not deny, never loaded", epCheckE9, nil},
	}
	for _, entry := range byEntryPoint {
		t.Run(entry.name, func(t *testing.T) {
			for _, row := range epRows {
				if entry.rows != nil && !entry.rows(row) {
					continue
				}
				t.Run(row.name, func(t *testing.T) { entry.check(t, row) })
			}
		})
	}
}
