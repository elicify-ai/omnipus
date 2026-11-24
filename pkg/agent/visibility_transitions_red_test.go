//go:build goolm && stdjson

package agent

// Order 37 — TestVisibilityTransitions (S-91, S-92, S-38, S-43; Dataset D11,
// all six rows; C-6/C-15). Oracle: skills-tools-redesign-spec.md D11 —
//   1 loaded tool, level → hidden: stays in that chat; not in new chats (S-91)
//   2 read_file full → hidden: not in new chats (S-92; its ToolSearch half is
//     order 56's capture surface)
//   3 hidden → full: definition from next turn (S-38)
//   4 permission → deny: gone from next turn, also in that chat (S-40)
//   5 deny → allow: listed per level from next turn (S-40)
//   6 skill level → name_only: next turn's menu shows name only (S-43)
//
// Harness: the real loop with the recording provider (order 36's harness).
// Rows 4 and 5 seed the REAL persisted policy store (config.json
// sandbox.tool_policies) — no seam, they assert what the next turn resolves.
// Rows 1, 2, 3 and 6 write the spec-named visibility stores (C-6/C-15:
// sandbox.tool_visibility / sandbox.skill_visibility) as literal JSON through
// the loop's own reload (tests/helpers/sandboxkeys decodes the key into a copy
// of the product's sandbox section and makes the copy live through
// AgentLoop.ReloadProviderAndConfig — the path that rebuilds every agent
// instance's snapshot, as a person's settings change does in production).
// A config that does not carry the key stops the row with BLOCKED "config does
// not carry the key"; there is no test-owned variable a stub could assign
// (CHECK round 3 finding N11).
//
// CHECK round 1 finding 3: rows 1, 2, 3 and 6 previously only nil-checked a
// seam, so a no-op GREEN seed passed them without ever changing visibility or
// running a turn. Each is a REAL transition on ONE continuing loop: the
// pre-transition state is captured and asserted first, the stored level
// changes between captured turns, and the per-row outcome is asserted on later
// turns of the same loop (old-chat retention, new-chat absence, next-turn
// definition/menu). An unaffected ordinary tool is the control in row 1. A
// GREEN that does not make the next turn consult the stored level fails these
// rows at their request assertions.
import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/tests/helpers/sandboxkeys"

	"github.com/stretchr/testify/require"
)

// visibilityLoop boots the real loop whose ceiling carries basePolicies (the
// in-memory global ceiling the compositor resolves against), runs one turn,
// and returns the tool names of the request. Rows 4 and 5 use it as a
// one-shot state probe.
func visibilityLoop(t *testing.T, session string, basePolicies map[string]string) []string {
	t.Helper()
	h := newTransitionsHarness(t, basePolicies)
	return h.turn(session)
}

// transitionsHarness is ONE continuing loop: every turn() call runs on the
// same agent loop and provider, so a stored change between turns is the only
// thing that can explain a different request (CHECK round 1 finding 3).
type transitionsHarness struct {
	t        *testing.T
	al       *AgentLoop
	provider *capturingProvider
}

// storeVisibility writes one stored level (C-6 vocabulary: hidden, name_only,
// name_and_description, full) into sandbox.<store> of the loop's live config:
// "tool_visibility" for a tool or external server, "skill_visibility" for a
// skill. The write is literal JSON made live through the loop's own reload; a
// config that does not carry the key ends the row with BLOCKED, never a skip.
func (h *transitionsHarness) storeVisibility(store, key, level, required string) {
	h.t.Helper()
	sandboxkeys.Store(h.t, h.al.GetConfig, reloadPublisher(h.al, h.provider),
		map[string]any{store: map[string]string{key: level}}, required)
}

func newTransitionsHarness(t *testing.T, basePolicies map[string]string, skills ...string) *transitionsHarness {
	t.Helper()
	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "skills"), 0o700))
	for _, slug := range skills {
		dir := filepath.Join(tmp, "skills", slug)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: "+slug+"\ndescription: Notes helper.\n---\n\nD11 notes instructions.\n"), 0o600))
	}

	// A name-only Skills list is not a usable installed grant (r15). Bind the
	// actual fixture generation through the real entity store before boot.
	t.Setenv(config.EnvHome, tmp)
	t.Setenv(config.EnvConfig, filepath.Join(tmp, "config.json"))
	granted := persistSkillGrantFixture(t, tmp, config.AgentConfig{ID: "mia", Home: tmp, Skills: skills})
	provider := &capturingProvider{}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "transitions-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 1,
			},
			List: []config.AgentConfig{granted},
		},
	}
	// Explicit Given, not an accidental empty ceiling. write_file is the
	// unaffected ordinary control; read_file's Deny in row 4 still wins.
	cfg.Sandbox.ToolPolicies = map[string]string{"read_file": "allow", "write_file": "allow", "Skill": "allow"}
	for key, value := range basePolicies {
		cfg.Sandbox.ToolPolicies[key] = value
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	return &transitionsHarness{t: t, al: al, provider: provider}
}

// turn runs one turn on the given session and returns the tool names of that
// turn's request. Sessions are chats: a first use of a session key is a new
// chat, a reuse is the continuing chat.
func (h *transitionsHarness) turn(session string) []string {
	h.t.Helper()
	before := len(h.provider.toolNames)
	agent := h.al.registry.primaryChatAgentForTest()
	require.NotNil(h.t, agent)
	_, err := h.al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey: session, Channel: "cli", ChatID: "direct",
		UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
	})
	require.NoError(h.t, err)
	require.Greater(h.t, len(h.provider.toolNames), before, "the turn must have sent one request")
	return h.provider.toolNames[len(h.provider.toolNames)-1]
}

// requestOf returns the raw request body of the harness's most recent turn —
// the surface the skill menu is asserted against.
func (h *transitionsHarness) requestOf() string {
	h.t.Helper()
	require.NotEmpty(h.t, h.provider.messages, "a captured request is required")
	return string(h.provider.messages[len(h.provider.messages)-1])
}

func TestVisibilityTransitions(t *testing.T) {
	t.Run("D11#4 S-40: a denied tool is not in the turn's surface", func(t *testing.T) {
		names := visibilityLoop(t, "session-vis-4", map[string]string{"read_file": "deny"})
		require.NotContains(t, names, "read_file", "D11#4: the denied tool is gone from the next turn's request")
	})

	t.Run("D11#5 S-40: deny → allow lists the tool per its level from the next turn", func(t *testing.T) {
		names := visibilityLoop(t, "session-vis-5", map[string]string{"read_file": "allow"})
		require.Contains(t, names, "read_file", "D11#5: the re-allowed tool is listed again")
	})

	// CHECK round 1 finding 3: a real transition on one continuing loop —
	// captured before/after, an unaffected control tool, old-chat retention
	// and new-chat absence all on the same loop.
	t.Run("D11#1 S-91: a loaded tool demoted to hidden stays in that chat, not in new chats", func(t *testing.T) {
		h := newTransitionsHarness(t, nil)

		loaded := h.turn("chat-a")
		require.Contains(t, loaded, "read_file",
			"fixture: the tool is in the surface before the demotion")
		var control string
		for _, n := range loaded {
			if n != "read_file" {
				control = n
				break
			}
		}
		require.NotEmpty(t, control, "fixture: an unaffected tool is present to serve as the control")

		h.storeVisibility("tool_visibility", "read_file", "hidden", "D11 row 1 and S-91 (C-6/C-15 sandbox.tool_visibility)")

		require.Contains(t, h.turn("chat-a"), "read_file",
			"D11#1: the loaded tool STAYS in the chat that already loaded it")
		fresh := h.turn("chat-b")
		require.NotContains(t, fresh, "read_file",
			"D11#1: a NEW chat does not get the demoted tool")
		require.Contains(t, fresh, control,
			"D11#1 control: the unaffected tool is untouched in the new chat")
	})

	t.Run("D11#2 S-92: a built-in demoted to hidden is not in new chats' requests", func(t *testing.T) {
		h := newTransitionsHarness(t, nil)

		require.Contains(t, h.turn("chat-a"), "read_file",
			"fixture: the tool is in the surface before the demotion")

		h.storeVisibility("tool_visibility", "read_file", "hidden",
			"D11 row 2 and S-92 (C-6/C-15 sandbox.tool_visibility; the ToolSearch half is order 56's capture surface)")

		require.NotContains(t, h.turn("chat-b"), "read_file",
			"D11#2: the first new chat after the demotion omits the tool")
		require.NotContains(t, h.turn("chat-c"), "read_file",
			"D11#2: a second new chat omits it too — not in NEW CHATS")
	})

	t.Run("D11#3 S-38: hidden → full restores the definition from the next turn", func(t *testing.T) {
		h := newTransitionsHarness(t, nil)
		h.storeVisibility("tool_visibility", "read_file", "hidden",
			"D11 row 3 and S-38 (C-6/C-15 sandbox.tool_visibility: the demoted starting state)")

		require.NotContains(t, h.turn("chat-a"), "read_file",
			"fixture: the hidden tool is out of the surface")

		h.storeVisibility("tool_visibility", "read_file", "full",
			"D11 row 3 and S-38 (C-6/C-15 sandbox.tool_visibility: the level returns to full)")

		require.Contains(t, h.turn("chat-a"), "read_file",
			"D11#3: the definition is back from the NEXT TURN after the level returns to full")
	})

	t.Run("D11#6 S-43: a skill demoted to name_only shows name only in the next turn's menu", func(t *testing.T) {
		h := newTransitionsHarness(t, nil, "notes-helper")

		h.turn("chat-skill")
		first := h.requestOf()
		require.Contains(t, first, "notes-helper",
			"fixture: the granted skill is listed by name")
		require.Contains(t, first, "Notes helper.",
			"fixture: the default level lists the description too (name_and_description, C-6)")

		h.storeVisibility("skill_visibility", "notes-helper", "name_only",
			"D11 row 6 and S-43 (C-6/C-15 sandbox.skill_visibility; the menu line itself is order 11's surface)")

		h.turn("chat-skill")
		second := h.requestOf()
		require.Contains(t, second, "notes-helper",
			"D11#6: the skill is still listed — by name")
		require.NotContains(t, second, "Notes helper.",
			"D11#6: name_only means NAME ONLY — the description is gone from the menu")
	})
}
