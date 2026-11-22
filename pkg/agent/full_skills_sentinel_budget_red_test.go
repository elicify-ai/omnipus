//go:build goolm && stdjson

package agent

// Order 36 — TestFullSkills_SentinelAndBudget (S-42, S-89, S-90; Dataset D9,
// all ten rows; C-14). Oracle: skills-tools-redesign-spec.md D9 —
//   1 Full, Allow, verified, chat → sentinel in first request (S-42)
//   2 Full, Ask, verified, not approved → sentinel absent; listed with
//     description (S-89)
//   3 Full, Allow, unverified, trust Ask, no approval → sentinel absent (S-89)
//   4 row 3 after "Approve this version" → sentinel present from NEXT turn
//   5 Full, Ask, unattended run → sentinel absent; the load is refused (the
//     load-refusal surface itself is order 50's path gate, D14)
//   6 Full, Deny → not listed at all, sentinel absent
//   7–10 the C-14 budget boundary (B = context − maxTokens −
//     ceil(context/20) − (pinnedCore + FullSkills); send when toolDefs +
//     message ≤ B) with FIXED token estimators, driven on the real loop.
//
// CHECK round 1 finding 7: the earlier rows 1 and 2 shared one fixture that
// set NONE of the raw D9 inputs (no policy, no level, no verification, no
// approval), so a correct deterministic implementation could not satisfy
// both rows' opposite sentinel expectations, and the RED log showed
// missing-policy errors with a zero tool surface instead of a configured
// acceptance fixture. Every row now seeds its own raw D9 state explicitly
// through the seedSkillState seam and asserts the observable fixture state
// before the turn; rows 3–6 are added; the budget rows run on the REAL loop
// with fixed estimators (fixLoopTokenEstimators) instead of a standalone
// boolean comparator.
//
// RED today: none of the C-5/C-12/C-24 state stores or the fixed-estimator
// injection exists — every row fails loud (BLOCKED) at its first seam,
// never a skip.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"

	"github.com/stretchr/testify/require"
)

type capturingProvider struct {
	calls     int
	messages  []json.RawMessage
	toolNames [][]string
}

func (m *capturingProvider) Chat(ctx context.Context, msgs []providers.Message, toolDefs []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	m.calls++
	if raw, err := json.Marshal(msgs); err == nil {
		m.messages = append(m.messages, raw)
	}
	names := make([]string, 0, len(toolDefs))
	for _, td := range toolDefs {
		names = append(names, td.Function.Name)
	}
	m.toolNames = append(m.toolNames, names)
	return &providers.LLMResponse{Content: "done"}, nil
}

func (m *capturingProvider) GetDefaultModel() string { return "sentinel-test-model" }

// d9SkillState is the raw D9 state one row seeds. Zero values mean "leave
// unset" for strings/bools except where the row names the value; the test
// always passes the row's full input explicitly (D10's rule: defaults are
// never implied).
type d9SkillState struct {
	policy   string // "allow" | "ask" | "deny" — the stored global skill permission
	level    string // "full" | "name_only" | "name_and_description" | "hidden" — the stored level
	verified bool   // the stored install-time verification result (C-5)
	trust    string // "allow" | "ask" | "deny" — sandbox.skill_trust ("" leaves config untouched)
	approved bool   // a stored C-24 revision approval for the skill's current revision
}

// seedSkillState is the test-local seam for the raw D9 stores: the global
// skill permission (sandbox.skill_policies), the level (C-6 visibility
// store), the verification result (C-5), the trust floor (config) and the
// revision approval (C-24). GREEN wires it to the real stores; nil keeps
// the RED run loud (BLOCKED), never a skip.
var seedSkillState func(t testing.TB, home, slug string, st d9SkillState)

// tokenEstimators is the fixed-estimator set a budget row installs on the
// real loop (D9 rows 7–10: "fixed token estimators in the test").
type tokenEstimators struct {
	contextTokens int
	maxTokens     int
	pinnedCore    int
	fullSkills    int
	toolDefs      int
	message       int
}

// fixLoopTokenEstimators is the test-local seam that pins the loop's C-14
// token estimators for one test, so the budget boundary is deterministic on
// the real send path. GREEN wires it to the production estimator injection;
// nil keeps the RED run loud (BLOCKED), never a skip.
var fixLoopTokenEstimators func(t testing.TB, al *AgentLoop, e tokenEstimators)

// d9Harness is one continuing loop with the granted notes-helper skill
// installed (its instructions carry the sentinel).
type d9Harness struct {
	t        *testing.T
	al       *AgentLoop
	provider *capturingProvider
	home     string
}

func newD9Harness(t *testing.T) *d9Harness {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "sentinel-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 2,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp, Skills: []string{"notes-helper"}}},
		},
	}
	dir := filepath.Join(tmp, "skills", "notes-helper")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body := "---\nname: notes-helper\ndescription: Notes helper.\n---\n\nZEBRA-NOTES-3 hidden marker.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))

	provider := &capturingProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	return &d9Harness{t: t, al: al, provider: provider, home: tmp}
}

// d9Turn is one captured turn: whether a request reached the model, the
// FIRST request body of the turn, and the loop's response text.
type d9Turn struct {
	sent     bool
	request  string
	response string
}

// turn runs one chat turn (run names the D10 run dimension: "chat" or
// "scheduled" — scheduled is the unattended run of D9 row 5, with
// AutoDenyAsk as the attendance tests pin it).
func (h *d9Harness) turn(session, run string) d9Turn {
	h.t.Helper()
	beforeCalls, beforeMsgs := h.provider.calls, len(h.provider.messages)
	agent := h.al.registry.primaryChatAgentForTest()
	require.NotNil(h.t, agent)
	opts := processOptions{
		SessionKey: session, Channel: "cli", ChatID: "direct",
		UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
	}
	if run == "scheduled" {
		opts.Channel, opts.ChatID, opts.AutoDenyAsk = "scheduled", "", true
	}
	resp, err := h.al.runAgentLoop(context.Background(), agent, opts)
	require.NoError(h.t, err)
	out := d9Turn{response: resp}
	if h.provider.calls > beforeCalls {
		out.sent = true
		require.Greater(h.t, len(h.provider.messages), beforeMsgs, "a sent turn captured its request")
		out.request = string(h.provider.messages[beforeMsgs])
	}
	return out
}

// requireSeeded asserts the OBSERVABLE fixture state before the turn (CHECK
// round 1 finding 7: assert the fixture state before the turn): the skill is
// installed and, when the row names trust, the config carries it.
func (h *d9Harness) requireSeeded(st d9SkillState) {
	h.t.Helper()
	_, err := os.Stat(filepath.Join(h.home, "skills", "notes-helper", "SKILL.md"))
	require.NoError(h.t, err, "fixture: the granted skill is installed")
	if st.trust != "" {
		require.Equal(h.t, config.SkillTrustLevel(st.trust), h.al.GetConfig().Sandbox.SkillTrust,
			"fixture: the seeded trust floor is the config's stored value")
	}
}

// requireStateSeam fails loud when the raw-D9 store seam is unwired.
func requireStateSeam(t *testing.T, row string) {
	t.Helper()
	if seedSkillState == nil {
		t.Fatal("BLOCKED: the raw D9 skill-state stores (C-5 verification, C-6 level, " +
			"C-12/C-21 global skill permission, C-24 revision approval) are not implemented — " + row)
	}
}

func TestFullSkills_SentinelAndBudget(t *testing.T) {
	t.Run("D9#1 S-42: a Full Allow verified skill's sentinel is in the first request", func(t *testing.T) {
		h := newD9Harness(t)
		requireStateSeam(t, "required by D9 row 1 (S-42)")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "allow", level: "full", verified: true, trust: "allow",
		})
		h.requireSeeded(d9SkillState{trust: "allow"})

		turn := h.turn("session-full-1", "chat")
		require.True(t, turn.sent, "D9#1: the request is sent")
		require.Contains(t, turn.request, "ZEBRA-NOTES-3",
			"D9#1: the Full skill's instructions (sentinel) are injected into the first request")
	})

	t.Run("D9#2 S-89: Full Ask verified, not approved — listed with description, never injected", func(t *testing.T) {
		h := newD9Harness(t)
		requireStateSeam(t, "required by D9 row 2 (S-89)")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "ask", level: "full", verified: true, trust: "ask",
		})
		h.requireSeeded(d9SkillState{trust: "ask"})

		turn := h.turn("session-full-2", "chat")
		require.True(t, turn.sent, "D9#2: the chat turn still reaches the model")
		require.NotContains(t, turn.request, "ZEBRA-NOTES-3",
			"D9#2: no instructions leak without approval")
		require.Contains(t, turn.request, "notes-helper", "D9#2: the skill is listed")
		require.Contains(t, turn.request, "Notes helper.",
			"D9#2: the listing carries the description")
	})

	t.Run("D9#3 S-89: Full Allow unverified under trust Ask, no approval — sentinel absent", func(t *testing.T) {
		h := newD9Harness(t)
		requireStateSeam(t, "required by D9 row 3 (S-89)")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "allow", level: "full", verified: false, trust: "ask",
		})
		h.requireSeeded(d9SkillState{trust: "ask"})

		turn := h.turn("session-full-3", "chat")
		require.True(t, turn.sent, "D9#3: the turn runs; the trust gate decides the load")
		require.NotContains(t, turn.request, "ZEBRA-NOTES-3",
			"D9#3: an unverified skill under trust Ask is not injected without approval")
	})

	t.Run("D9#4 S-89: after Approve this version the sentinel is present from the next turn", func(t *testing.T) {
		h := newD9Harness(t)
		requireStateSeam(t, "required by D9 rows 3 and 4 (S-89)")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "allow", level: "full", verified: false, trust: "ask",
		})
		h.requireSeeded(d9SkillState{trust: "ask"})

		first := h.turn("session-full-4", "chat")
		require.True(t, first.sent, "D9#4 setup: the first turn runs")
		require.NotContains(t, first.request, "ZEBRA-NOTES-3",
			"D9#4 setup: row 3's state holds — nothing is injected before the approval")

		// The person's "Approve this version" writes the C-24 revision approval.
		seedSkillState(t, h.home, "notes-helper", d9SkillState{approved: true})

		second := h.turn("session-full-4", "chat")
		require.True(t, second.sent, "D9#4: the next turn runs")
		require.Contains(t, second.request, "ZEBRA-NOTES-3",
			"D9#4: the approved version's instructions are injected FROM THE NEXT TURN")
	})

	t.Run("D9#5 S-89: Full Ask unattended — sentinel absent, the load is refused", func(t *testing.T) {
		h := newD9Harness(t)
		requireStateSeam(t, "required by D9 row 5 (S-89; the load-refusal surface itself is order 50's path gate)")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "ask", level: "full", verified: false,
		})
		h.requireSeeded(d9SkillState{})

		turn := h.turn("session-full-5", "scheduled")
		require.True(t, turn.sent, "D9#5: the scheduled run starts its request")
		require.NotContains(t, turn.request, "ZEBRA-NOTES-3",
			"D9#5: an unattended run can never inject an unapproved Full skill")
	})

	t.Run("D9#6 S-89: Full Deny — not listed at all, sentinel absent", func(t *testing.T) {
		h := newD9Harness(t)
		requireStateSeam(t, "required by D9 row 6 (S-89)")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "deny", level: "full",
		})
		h.requireSeeded(d9SkillState{})

		turn := h.turn("session-full-6", "chat")
		require.True(t, turn.sent, "D9#6: the turn runs; the Deny removes the skill from the surface")
		require.NotContains(t, turn.request, "notes-helper",
			"D9#6: a denied skill is not even listed")
		require.NotContains(t, turn.request, "ZEBRA-NOTES-3",
			"D9#6: and nothing is injected")
	})

	// D9 rows 7-10: the C-14 budget boundary ON THE REAL LOOP with fixed
	// estimators (CHECK round 1 finding 7: not a standalone comparator). B =
	// context − maxTokens − ceil(context/20) − (pinnedCore + FullSkills);
	// send when toolDefs + message ≤ B. Row 7's exact numbers are D9#7's.
	t.Run("D9#7 S-90: budget B=1600 with toolDefs 1400 + message 200 sends the request", func(t *testing.T) {
		h := newD9Harness(t)
		if fixLoopTokenEstimators == nil {
			t.Fatal("BLOCKED: the fixed-estimator injection for the C-14 budget check is not " +
				"implemented — the loop must stop sending when the Full skills no longer fit " +
				"(D9 rows 7-10, S-90, ST-Q1 A)")
		}
		requireStateSeam(t, "required by D9 row 7 (a Full skill must exist to be charged)")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "allow", level: "full", verified: true, trust: "allow",
		})
		fixLoopTokenEstimators(t, h.al, tokenEstimators{
			contextTokens: 8000, maxTokens: 1000, pinnedCore: 2000,
			fullSkills: 3000, toolDefs: 1400, message: 200,
		})

		turn := h.turn("session-budget-7", "chat")
		require.True(t, turn.sent, "D9#7: B = 1600 = 1400 + 200 — the request is sent")
		require.Contains(t, turn.request, "ZEBRA-NOTES-3",
			"D9#7: the fitting Full skill is charged once and injected")
	})

	t.Run("D9#8 S-90: one token over (Full skills 3001) stops the turn with the C-14 text", func(t *testing.T) {
		h := newD9Harness(t)
		if fixLoopTokenEstimators == nil {
			t.Fatal("BLOCKED: the fixed-estimator injection for the C-14 budget check is not implemented — required by D9 row 8")
		}
		requireStateSeam(t, "required by D9 row 8")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "allow", level: "full", verified: true, trust: "allow",
		})
		fixLoopTokenEstimators(t, h.al, tokenEstimators{
			contextTokens: 8000, maxTokens: 1000, pinnedCore: 2000,
			fullSkills: 3001, toolDefs: 1400, message: 200,
		})

		turn := h.turn("session-budget-8", "chat")
		require.False(t, turn.sent,
			"D9#8: B = 1599 < 1600 — no request reaches the model")
		require.Contains(t, turn.response, "Set some of them to Name and short description in Skills & Tools",
			"D9#8: the chat shows the C-14 reason (S-90)")
	})

	t.Run("D9#9 S-90: a model switch to a smaller context stops sending", func(t *testing.T) {
		h := newD9Harness(t)
		if fixLoopTokenEstimators == nil {
			t.Fatal("BLOCKED: the fixed-estimator injection for the C-14 budget check is not implemented — required by D9 row 9")
		}
		requireStateSeam(t, "required by D9 row 9")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "allow", level: "full", verified: true, trust: "allow",
		})
		fixLoopTokenEstimators(t, h.al, tokenEstimators{
			contextTokens: 8000, maxTokens: 1000, pinnedCore: 2000,
			fullSkills: 3000, toolDefs: 1400, message: 200,
		})
		require.True(t, h.turn("session-budget-9", "chat").sent,
			"D9#9 setup: model A fits")

		// Switching to smaller model B: the fixed context estimator becomes
		// model B's 6,500 — the deterministic stand-in for the real switch.
		fixLoopTokenEstimators(t, h.al, tokenEstimators{
			contextTokens: 6500, maxTokens: 1000, pinnedCore: 2000,
			fullSkills: 3000, toolDefs: 1400, message: 200,
		})

		turn := h.turn("session-budget-9", "chat")
		require.False(t, turn.sent, "D9#9: the next turn on model B is not sent")
		require.Contains(t, turn.response, "Set some of them to Name and short description in Skills & Tools",
			"D9#9: the chat shows the C-14 reason (S-90)")
	})

	t.Run("D9#10 S-90: a Full skill growing past the limit stops the next turn", func(t *testing.T) {
		h := newD9Harness(t)
		if fixLoopTokenEstimators == nil {
			t.Fatal("BLOCKED: the fixed-estimator injection for the C-14 budget check is not implemented — required by D9 row 10")
		}
		requireStateSeam(t, "required by D9 row 10")
		seedSkillState(t, h.home, "notes-helper", d9SkillState{
			policy: "allow", level: "full", verified: true, trust: "allow",
		})
		fixLoopTokenEstimators(t, h.al, tokenEstimators{
			contextTokens: 8000, maxTokens: 1000, pinnedCore: 2000,
			fullSkills: 3000, toolDefs: 1400, message: 200,
		})
		require.True(t, h.turn("session-budget-10", "chat").sent,
			"D9#10 setup: it fits before the growth")

		// The skill's content grows past the limit between turns.
		fixLoopTokenEstimators(t, h.al, tokenEstimators{
			contextTokens: 8000, maxTokens: 1000, pinnedCore: 2000,
			fullSkills: 3200, toolDefs: 1400, message: 200,
		})

		require.False(t, h.turn("session-budget-10", "chat").sent,
			"D9#10: the next turn is not sent")
	})
}
