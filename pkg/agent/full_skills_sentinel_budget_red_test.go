//go:build goolm && stdjson

package agent

// Order 36 — TestFullSkills_SentinelAndBudget (S-89, S-90; Dataset D9 rows
// 5–10; C-14). Oracle: skills-tools-redesign-spec.md D9 —
//   5 Full, Ask, unattended run → sentinel absent; the load is refused (the
//     load-refusal surface itself is order 50's path gate, D14)
//   6 Full, Deny → not listed at all, sentinel absent
//   7–10 the C-14 budget boundary (B = context − maxTokens −
//     ceil(context/20) − (pinnedCore + FullSkills); send when toolDefs +
//     message ≤ B) with FIXED token estimators, driven on the real loop.
//
// D9 rows 1–4 are pkg/gateway::TestFullSkills_VerifiedConsentInjection: their
// dataset inputs include the install-time verification result (C-5) and a
// person's revision approval (C-24), which exist only through the real review/
// install and trust-approval routes, and a pkg/agent test cannot import
// pkg/gateway (CHECK round 3 finding N11: "verification and approval seeds
// through the real review and trust routes as in F22").
//
// Every row here seeds ONLY the spec-named config stores — sandbox.skill_policies
// (C-2), sandbox.skill_visibility (C-6), sandbox.skill_trust (C-3) — as literal
// JSON made live through the loop's own reload (tests/helpers/sandboxkeys;
// AgentLoop.ReloadProviderAndConfig, the path that rebuilds every agent
// instance's snapshot). A config that does not carry a key stops the row with
// BLOCKED "config does not carry the key"; there is no test-owned variable a
// stub could assign. Rows
// 7–10 name no verification input in the dataset, so trust Allow keeps the
// unverified fixture from being stopped by the trust check (their subject is
// the budget). The budget rows run on the REAL loop with fixed estimators
// (fixLoopTokenEstimators), never a standalone comparator (CHECK round 1
// finding 7).
//
// RED today: the C-2/C-3/C-6 stores and the fixed-estimator input do not exist
// — every row fails loud (BLOCKED), never a skip.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/elicify-ai/omnipus/tests/helpers/sandboxkeys"

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

// Bind only a DIRECT named production estimator-input function on the loop.
// Primitive arguments let a native function bind without importing test-only
// types. It must control context_budget.go's real estimator consumers on the
// send path; model sends and refusal text below remain independent observers.
// There is no such native input at this revision: nil is a loud obligation.
var nativeLoopTokenEstimators func(al *AgentLoop, contextTokens, maxTokens, pinnedCore, fullSkills, toolDefs, message int)

func requireNativeEstimators(t *testing.T) {
	t.Helper()
	nativebinding.Require(t, "pkg/agent", nativeLoopTokenEstimators,
		"C-14/D9#7–10/S-90: actual contextBudget, pinnedCoreOverheadTokens, estimateToolDefsTokens and estimateMessageTokens inputs at the native send boundary")
}

func fixLoopTokenEstimators(t *testing.T, al *AgentLoop, e tokenEstimators) {
	t.Helper()
	requireNativeEstimators(t)
	nativeLoopTokenEstimators(al, e.contextTokens, e.maxTokens, e.pinnedCore, e.fullSkills, e.toolDefs, e.message)
}

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

	t.Setenv(config.EnvHome, tmp)
	t.Setenv(config.EnvConfig, filepath.Join(tmp, "config.json"))
	cfg.Agents.List[0] = persistSkillGrantFixture(t, tmp, cfg.Agents.List[0])
	cfg.Sandbox.ToolPolicies = map[string]string{"read_file": "allow", "write_file": "allow", "Skill": "allow"}
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

// store writes the row's literal config inputs for the skill into the
// spec-named stores of the loop's live config — C-2 sandbox.skill_policies,
// C-6 sandbox.skill_visibility, C-3 sandbox.skill_trust — as JSON made live
// through the loop's own reload. An empty value leaves that store untouched. The
// fixture's skill must be installed first (CHECK round 1 finding 7: assert the
// fixture state before the turn); a config that does not carry a key ends the
// row with BLOCKED.
func (h *d9Harness) store(required, policy, level, trust string) {
	h.t.Helper()
	_, err := os.Stat(filepath.Join(h.home, "skills", "notes-helper", "SKILL.md"))
	require.NoError(h.t, err, "fixture: the granted skill is installed")
	patch := map[string]any{}
	if policy != "" {
		patch["skill_policies"] = map[string]string{"notes-helper": policy}
	}
	if level != "" {
		patch["skill_visibility"] = map[string]string{"notes-helper": level}
	}
	if trust != "" {
		patch["skill_trust"] = trust
	}
	sandboxkeys.Store(h.t, h.al.GetConfig, reloadPublisher(h.al, h.provider), patch, required)
}

func TestFullSkills_SentinelAndBudget(t *testing.T) {
	t.Run("D9#5 S-89: Full Ask unattended — sentinel absent, the load is refused", func(t *testing.T) {
		h := newD9Harness(t)
		h.store("D9 row 5 (S-89; the load-refusal surface itself is order 50's path gate)", "ask", "full", "")

		turn := h.turn("session-full-5", "scheduled")
		require.True(t, turn.sent, "D9#5: the scheduled run starts its request")
		require.NotContains(t, turn.request, "ZEBRA-NOTES-3",
			"D9#5: an unattended run can never inject an unapproved Full skill")
	})

	t.Run("D9#6 S-89: Full Deny — not listed at all, sentinel absent", func(t *testing.T) {
		h := newD9Harness(t)
		h.store("D9 row 6 (S-89)", "deny", "full", "")

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
		requireNativeEstimators(t)
		h.store("D9 row 7 (a Full skill must exist to be charged)", "allow", "full", "allow")
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
		requireNativeEstimators(t)
		h.store("D9 row 8", "allow", "full", "allow")
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
		requireNativeEstimators(t)
		h.store("D9 row 9", "allow", "full", "allow")
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
		requireNativeEstimators(t)
		h.store("D9 row 10", "allow", "full", "allow")
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
