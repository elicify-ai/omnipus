//go:build goolm && stdjson

package gateway

// Order 36, Dataset D9 rows 1-4 (S-42, S-89; C-2, C-3, C-5, C-6, C-12, C-24).
// Oracle: skills-tools-redesign-spec.md D9 —
//   1 Full, Allow, verified, chat → the sentinel is in the first request (S-42)
//   2 Full, Ask, verified, chat, not approved → sentinel absent; the skill is
//     listed with its description (S-89)
//   3 Full, Allow, unverified, trust Ask, no approval → sentinel absent; listed
//     with its description (S-89)
//   4 row 3 after "Approve this version" → sentinel present from the NEXT turn
//
// These rows moved here from pkg/agent::TestFullSkills_SentinelAndBudget (CHECK
// round 3 finding N11). Two of their inputs are not plain config: the
// install-time verification result (C-5) exists only after the real registry
// review and install routes, and a person's "Approve this version" (C-24)
// exists only after the real trust-approval route. Both live in pkg/gateway,
// which a pkg/agent test cannot import. So, exactly as F22 does:
//   - "verified" comes ONLY from seedNativeD10VerifiedInstall (HandleSkills
//     review then install, an external registry serving the literal package
//     bytes), and an independent inventory read confirms it;
//   - "unverified" is the literal package file with no install record, and the
//     same independent inventory read confirms it;
//   - the person's approval comes ONLY from approveSkillViaPersonRoute (the real
//     POST /api/v1/skills/{name}/trust-approval for the current revision);
//   - the three config stores — sandbox.skill_policies (C-2), sandbox.skill_trust
//     (C-3) and sandbox.skill_visibility (C-6) — are written as literal JSON
//     into a copy of the live config and made live the way F22's seedNativeD10
//     does it (persist, AgentLoop.ReloadProviderAndConfig with the scripted
//     provider, re-bind each rebuilt agent's skill allowlist and grants;
//     tests/helpers/sandboxkeys). A config that does not carry a key ends the row
//     with BLOCKED "config does not carry the key"; no test-owned variable
//     exists for a stub to fill.
//
// Only the model is scripted. The skill menu, the Full injection, the consent
// gate and the request are production; the observer is the scripted model's own
// captured request. No expected value enters an input. GREEN, mutation and
// proof-of-failability are deferred to the independent CHECK.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/tests/helpers/sandboxkeys"
	"github.com/stretchr/testify/require"
)

const (
	d9Sentinel    = "ZEBRA-NOTES-3"
	d9Instruction = d9Sentinel + " hidden marker."
	d9Description = "Keeps short notes for the person."
	d9Package     = "---\nname: notes-helper\ndescription: " + d9Description + "\n---\n\n" + d9Instruction + "\n"
)

// d9ConsentRow states EVERY raw input of a row, including the ones the row's
// table line leaves open (trust for the verified rows): the open dimension is
// the shipped Ask, as in the verified D10 rows 1-4.
type d9ConsentRow struct {
	row        int
	scenario   string
	permission string // sandbox.skill_policies["notes-helper"] (C-2)
	trust      string // sandbox.skill_trust (C-3)
	verified   bool   // install-time verification (C-5)
}

func newD9ConsentHarness(t *testing.T, in d9ConsentRow) *nativeSkillHarness {
	t.Helper()
	// The harness gets NO trust of its own (the omitted value every decoder
	// accepts): newNativeD10Harness casts its argument into the live config
	// without decoding it, and a trust word the product cannot decode yet (Ask,
	// C-3) would make that live config un-cloneable, so the publisher below would
	// fail as a fixture error instead of reporting the missing store. The row's
	// trust is written below, through the real decoder.
	h, _ := newNativeD10Harness(t, "", in.permission)
	if in.verified {
		seedNativeD10VerifiedInstall(t, h, "notes-helper", d9Package)
	} else {
		path := filepath.Join(h.api.homePath, "skills", "notes-helper", "SKILL.md")
		require.NoError(t, os.WriteFile(path, []byte(d9Package), 0o600))
		require.Equal(t, nativeD10Revision(t, d9Package), nativeD10Current(t, h, "notes-helper"),
			"fixture: the unverified package is the literal row bytes")
	}
	assertD9Inventory(t, h, in.verified)
	sandboxkeys.Store(t, h.api.agentLoop.GetConfig, d9ReloadPublisher(h), map[string]any{
		"skill_policies":   map[string]string{"notes-helper": in.permission},
		"skill_trust":      in.trust,
		"skill_visibility": map[string]string{"notes-helper": "full"},
	}, fmt.Sprintf("D9 row %d (%s; C-2 sandbox.skill_policies, C-3 sandbox.skill_trust, C-6 sandbox.skill_visibility)", in.row, in.scenario))
	return h
}

// d9ReloadPublisher makes a candidate config live exactly as seedNativeD10 does:
// persist it, reload the real loop with the harness's scripted provider (which
// rebuilds every agent instance), then re-bind each rebuilt agent's skill
// allowlist and grants, a harness binding that the reload rebuilds away.
func d9ReloadPublisher(h *nativeSkillHarness) func(*config.Config) error {
	return func(next *config.Config) error {
		if err := config.SaveConfig(filepath.Join(h.api.homePath, "config.json"), next); err != nil {
			return fmt.Errorf("persist the candidate config: %w", err)
		}
		if err := h.api.agentLoop.ReloadProviderAndConfig(context.Background(), h.script, next); err != nil {
			return err
		}
		for _, record := range next.Agents.List {
			inst, ok := h.api.agentLoop.GetRegistry().GetAgent(record.ID)
			if !ok {
				return fmt.Errorf("agent %q is not registered after the reload", record.ID)
			}
			inst.ContextBuilder.WithSkillAllowlist(record.Skills).WithSkillBindings(record.SkillGrants)
		}
		return nil
	}
}

// assertD9Inventory reads the row's verification input back the way a person's
// Skills page does, so "verified" and "unverified" are observed, not assumed.
func assertD9Inventory(t *testing.T, h *nativeSkillHarness, verified bool) {
	t.Helper()
	w := httptest.NewRecorder()
	h.api.HandleSkills(w, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)))
	require.Equal(t, http.StatusOK, w.Code, "independent native inventory: %s", w.Body.String())
	var listed []gen.Skill
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	for _, skill := range listed {
		if skill.Id == "notes-helper" {
			require.Equal(t, verified, skill.Verified, "fixture: the row's verification input as the real inventory reports it")
			return
		}
	}
	t.Fatal("fixture: the real inventory does not list notes-helper")
}

// d9ChatSession opens one real ordinary-root chat of the agent. Its native
// lifecycle record is an input (an attended human chat), not a favourable
// verdict: without it the real ancestry classifier correctly treats the run as
// damaged or unattended.
func d9ChatSession(t *testing.T, h *nativeSkillHarness) string {
	t.Helper()
	meta, err := h.api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "test-agent")
	require.NoError(t, err)
	require.NoError(t, h.api.agentLoop.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		AgentID: "test-agent", OwnerScopeKind: session.OwnerScopeHuman,
		Origin: &session.Origin{Kind: session.OriginKindChat},
	}))
	return meta.ID
}

// d9ChatTurn runs one real chat turn and returns the FIRST request the scripted
// model received in it, as the JSON the model was sent. A message that does not
// begin with "F18-CALL-" never makes the scripted model call a tool, so no
// Skill call can stand in for the injection (S-42: "without a Skill call"). The
// request is located by its position, then checked to carry this turn's own
// message, so a wrong position can never pass as an observation.
func d9ChatTurn(t *testing.T, h *nativeSkillHarness, sessionID, message string) string {
	t.Helper()
	before := len(h.script.captured(""))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := h.api.agentLoop.ProcessDirectWithChannelAs(ctx, "test-agent", message, "agent:test-agent:session:"+sessionID, "webchat", sessionID)
	require.NoError(t, err, "a real chat turn: a deadline or a wait for a card is not an injection decision")
	requests := h.script.captured("")
	require.Greater(t, len(requests), before, "the scripted model received this turn's request")
	raw, err := json.Marshal(requests[before])
	require.NoError(t, err)
	require.Contains(t, string(raw), message, "instrument: the captured request is this turn's first request")
	return string(raw)
}

func TestFullSkills_VerifiedConsentInjection(t *testing.T) {
	t.Run("instrument: successive real chat turns each capture their own first request and the observer sees a sentinel", func(t *testing.T) {
		h, _ := newNativeD10Harness(t, "", "allow")
		assertD9Inventory(t, h, false)
		id := d9ChatSession(t, h)
		first := d9ChatTurn(t, h, id, "D9-INSTRUMENT-TURN-1")
		second := d9ChatTurn(t, h, id, "D9-INSTRUMENT-TURN-2 "+d9Sentinel)
		require.NotContains(t, first, "D9-INSTRUMENT-TURN-2", "turn 1's first request is not turn 2's")
		require.NotContains(t, first, d9Sentinel, "the observer reports absence when the text was never sent")
		require.Contains(t, second, d9Sentinel, "the observer reports presence when the text was sent")
	})

	t.Run("D9#1 S-42: a Full Allow verified skill's instructions are in the first request", func(t *testing.T) {
		h := newD9ConsentHarness(t, d9ConsentRow{row: 1, scenario: "S-42", permission: "allow", trust: "ask", verified: true})

		first := d9ChatTurn(t, h, d9ChatSession(t, h), "D9-ROW-1-TURN-1")
		require.Contains(t, first, d9Instruction,
			"D9#1: the whole instructions, sentinel included, are in the first request without a Skill call")
	})

	t.Run("D9#2 S-89: a Full Ask verified skill, not approved, is listed with its description and never injected", func(t *testing.T) {
		h := newD9ConsentHarness(t, d9ConsentRow{row: 2, scenario: "S-89", permission: "ask", trust: "ask", verified: true})

		first := d9ChatTurn(t, h, d9ChatSession(t, h), "D9-ROW-2-TURN-1")
		require.NotContains(t, first, d9Sentinel, "D9#2: no instructions leak without approval")
		require.Contains(t, first, "notes-helper", "D9#2: the skill is listed")
		require.Contains(t, first, d9Description, "D9#2: the listing carries the description")
	})

	t.Run("D9#3 S-89: a Full Allow unverified skill under trust Ask with no approval is listed and never injected", func(t *testing.T) {
		h := newD9ConsentHarness(t, d9ConsentRow{row: 3, scenario: "S-89", permission: "allow", trust: "ask", verified: false})

		first := d9ChatTurn(t, h, d9ChatSession(t, h), "D9-ROW-3-TURN-1")
		require.NotContains(t, first, d9Sentinel, "D9#3: an unverified skill under trust Ask is not injected without approval")
		require.Contains(t, first, "notes-helper", "D9#3: the skill is listed")
		require.Contains(t, first, d9Description, "D9#3: the listing carries the description")
	})

	t.Run("D9#4 S-89: after Approve this version the instructions are in the request from the next turn", func(t *testing.T) {
		h := newD9ConsentHarness(t, d9ConsentRow{row: 4, scenario: "S-89", permission: "allow", trust: "ask", verified: false})
		id := d9ChatSession(t, h)

		before := d9ChatTurn(t, h, id, "D9-ROW-4-TURN-1")
		require.NotContains(t, before, d9Sentinel, "D9#4 setup: row 3's state holds, nothing is injected before the approval")

		// The person's "Approve this version": the real trust route for the
		// current revision, never a seeded record.
		approveSkillViaPersonRoute(t, h, "notes-helper")

		after := d9ChatTurn(t, h, id, "D9-ROW-4-TURN-2")
		require.Contains(t, after, d9Instruction, "D9#4: the approved version's instructions are injected from the next turn")
	})
}
