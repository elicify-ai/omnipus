// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestRequestSecret_ProgramRecipient (order 33, Unit) — S-54
// (program card in web chat: the recipient is kind "program" with program_id
// and the GitHub (gh, git) display), S-55 (refused in a channel turn with the
// existing text — regression), S-56 (unknown program id message) and S-99
// (the chat card uses the program token rules). Also S-13's tool-side half:
// a private variable of a switched-on program is refused at recipient
// resolution.
//
// Oracle: FR-051's recipient rules, the spec message table, dataset D-1's
// too-short row. Expected values come from the spec, never from the code.
//
// RED shape: the `program` recipient form does not exist yet, so the S1
// input is built from a raw map (unknown keys decode silently today and land
// when GREEN adds the form) and the config's programs section is the
// RED-proposed JSON shape recorded in test-disputes.md section E (unknown
// config keys decode silently today). A card that cannot be raised is
// reported BLOCKED with the spec reference, never skipped. The wire
// program_id rides the marshalled display map: the generated
// SecretRecipientDisplay carries only kind/lines today, and gains
// program_id at GREEN.

package secretrequest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
)

// s1ProgramsCfgJSON is the RED-proposed config shape: the github preset and
// Dev switched on. GREEN reconciles the exact schema (the shape mirrors the
// spec's ProgramEntry contract table).
const s1ProgramsCfgJSON = `{
  "version": 1,
  "agents": {"list": [
    {"id": "dev", "name": "Dev", "programs": ["github"]}
  ]},
  "programs": {
    "entries": [
      {"id": "github", "kind": "preset", "display_name": "GitHub",
       "program_words": ["gh", "git"],
       "token_variables": ["GH_TOKEN", "GITHUB_TOKEN"],
       "network_sites": ["github.com", "api.github.com", "uploads.github.com",
                         "codeload.github.com", "objects.githubusercontent.com",
                         "raw.githubusercontent.com"],
       "git": {"host": "github.com", "username": "x-access-token"},
       "identity_check": {"program_word": "gh", "args": ["api", "user", "--jq", ".login"],
                          "timeout_seconds": 20, "max_output_bytes": 4096}}
    ],
    "web_chat_allowed": true
  }
}`

// s1ProgramsCfg builds the matrix fixture and swaps its config for the
// RED-proposed programs shape (the agent roster comes from the same JSON).
// S-13/S-85's Given — GitHub switched ON for Dev — is expressed on the agent
// roster entry and VERIFIED BY READ-BACK: AgentConfig has no Programs field
// at RED, so the fixture fatal-BLOCKs naming it rather than silently running
// the conflict oracle without its Given (CHECK F15).
func s1ProgramsCfg(t *testing.T) *matrixEnv {
	t.Helper()
	f := newMatrixEnv(t)
	var cfg config.Config
	require.NoError(t, json.Unmarshal([]byte(s1ProgramsCfgJSON), &cfg),
		"instrument: the RED-proposed config shape parses (unknown keys are dropped at RED)")
	// ADR-054: AgentsConfig.List is json:"—" — the live roster never decodes
	// from JSON. Build it in Go; GREEN's `programs` field on AgentConfig
	// rides the same literal.
	cfg.Agents.List = []config.AgentConfig{{ID: "dev", Name: "Dev"}}
	require.NotEmpty(t, cfg.Agents.List, "instrument: the dev agent rides the roster")

	// The Given: GitHub on for Dev (FR-042). The field is set through
	// reflection so the fixture compiles at RED, then read back — the Given
	// is only real when the live config says it.
	dev := reflect.ValueOf(&cfg.Agents.List[0]).Elem()
	programsField := dev.FieldByName("Programs")
	if !programsField.IsValid() || programsField.Kind() != reflect.Slice {
		t.Fatalf("BLOCKED: config.AgentConfig has no Programs field — required by FR-042 and S-13's Given " +
			"(GitHub switched on for Dev), S-54, S-85 and S-134; the conflict and lifecycle oracles cannot run without it")
	}
	programsField.Set(reflect.ValueOf([]string{"github"}))
	got, _ := dev.FieldByName("Programs").Interface().([]string)
	require.Equal(t, []string{"github"}, got,
		"read-back: GitHub is switched on for Dev (the Given is real, not assumed)")
	f.cfg = &cfg
	return f
}

// s1Input builds a recipient Input from a raw map so the S1 `program` form
// compiles at RED (unknown keys are dropped silently today) and decodes once
// GREEN adds the form.
func s1Input(t *testing.T, m map[string]any) secretrequest.Input {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	var in secretrequest.Input
	require.NoError(t, json.Unmarshal(raw, &in))
	return in
}

// sortedKeys lists a decoded record's keys (for BLOCKED diagnostics).
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// s1ProgramGeneration reads the specified field on the object supplied by the
// caller. Callers pass Card or CardReceipt, never the containing internal Record.
// No continuation-named persisted fields are required or searched.
func s1ProgramGeneration(t *testing.T, value any, surface string) float64 {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	var state map[string]any
	require.NoError(t, json.Unmarshal(raw, &state))
	generation, ok := state["program_generation"].(float64)
	if !ok {
		t.Fatalf("BLOCKED: %s lacks program_generation — required by S-54/Contract Changes (keys %v)", surface, sortedKeys(state))
	}
	require.GreaterOrEqual(t, generation, float64(1))
	return generation
}

func TestRequestSecret_ProgramRecipient(t *testing.T) {
	t.Run("S56_unknown_program_id", func(t *testing.T) {
		f := s1ProgramsCfg(t)
		_, _, _, err := secretrequest.ResolveRecipient(f.cfg,
			s1Input(t, map[string]any{"purpose": "connect", "program": "nope"}))
		require.Error(t, err, "an unknown program id is refused")
		require.Contains(t, err.Error(), "nope is not a program in Settings → Programs.",
			"the refusal is the spec message (got %q)", err.Error())
	})

	t.Run("S54_program_recipient_resolves_with_program_id", func(t *testing.T) {
		f := s1ProgramsCfg(t)
		recipients, _, display, err := secretrequest.ResolveRecipient(f.cfg,
			s1Input(t, map[string]any{"purpose": "connect GitHub", "program": "github"}))
		require.NoError(t, err, "the github program recipient resolves (FR-051): %v", err)
		require.Len(t, recipients, 1)
		require.Equal(t, "program", recipients[0].Kind, "the recipient kind is program")

		require.Equal(t, "program", display.Kind, "the display names the program kind")
		require.NotEmpty(t, display.Lines, "the display carries lines (never empty)")
		label := ""
		for _, line := range display.Lines {
			label += line + " "
		}
		require.Contains(t, label, "GitHub", "the display says GitHub")
		require.Contains(t, label, "gh, git", "the display carries the words (S-54: GitHub (gh, git))")

		// program_id rides the wire display; the generated struct gains the
		// field only at GREEN, so this reads the marshalled form.
		rawDisplay, derr := json.Marshal(display)
		require.NoError(t, derr)
		var dm map[string]any
		require.NoError(t, json.Unmarshal(rawDisplay, &dm))
		require.Equal(t, "github", dm["program_id"],
			"program_id is required when kind is program — never inferred (R4-M4)")
	})

	t.Run("S13_variable_conflict_with_connected_program", func(t *testing.T) {
		f := s1ProgramsCfg(t)
		_, _, _, err := secretrequest.ResolveRecipient(f.cfg,
			s1Input(t, map[string]any{"purpose": "ask", "variable": "GH_TOKEN",
				"workspace_id": "sales", "agent_id": "dev"}))
		require.Error(t, err, "a variable owned by a switched-on program is refused")
		require.Contains(t, err.Error(),
			"GH_TOKEN is used by the program GitHub, which is switched on for this agent. Switch GitHub off first, or use another name.",
			"the refusal is the spec's private-variable conflict message (got %q)", err.Error())
	})

	t.Run("S54_submission_generation_and_continuation", func(t *testing.T) {
		// Observe the actual delivery seam captured by matrixEnv.options.Resume,
		// and the actual sole message producer. Check-running timing/settlement
		// needs the gateway's held-check integration, named separately below.
		f := s1ProgramsCfg(t)
		agentBefore, err := json.Marshal(f.cfg.Agents.List)
		require.NoError(t, err)
		sid := f.newSession("dev", "")
		rec, err := f.reg.Create(context.Background(), sid, "dev", "web-card", "",
			s1Input(t, map[string]any{"purpose": "connect GitHub", "program": "github"}))
		if err != nil {
			t.Fatalf("BLOCKED: the program card cannot be raised — required by FR-051/S-54 (registry said: %v)", err)
		}
		require.NotEmpty(t, rec.Card.Fields)
		submitted, err := f.reg.Submit(rec.Card.RequestId, matrixOwner,
			valuesInput(map[string]string{rec.Card.Fields[0].Key: "tok_valid_12345"}))
		require.NoError(t, err)
		require.Equal(t, "submitted", submitted.Card.State)
		receipt, exists, err := f.store.CardReceipt(rec.Card.RequestId)
		require.NoError(t, err)
		require.True(t, exists, "the exact submitted card has a committed receipt")
		require.Equal(t, sid, receipt.SessionID)
		require.Equal(t, submitted.Card.ReferenceNames, receipt.References)
		committed := s1ProgramGeneration(t, receipt, "CardReceipt")
		require.Equal(t, committed, s1ProgramGeneration(t, submitted.Card, "submitted.Card"))
		require.Len(t, f.attempts, 1, "the real Options.Resume seam receives this card, not a continuation-shaped field")
		delivered := f.attempts[0]
		require.Equal(t, submitted.Card, delivered.Card)
		require.Equal(t, committed, s1ProgramGeneration(t, delivered.Card, "Resume argument.Card"))
		message := secretrequest.ResumeMessage(delivered)
		require.Contains(t, strings.ToLower(message), "stored for github")
		require.Contains(t, strings.ToLower(message), "account check is running")
		require.NotContains(t, strings.ToLower(message), "connected as")
		requireValueFree(t, "actual ResumeMessage", message, "tok_valid_12345")
		agentAfter, err := json.Marshal(f.cfg.Agents.List)
		require.NoError(t, err)
		require.Equal(t, string(agentBefore), string(agentAfter), "this exact submission leaves Dev programs unchanged")
		require.Len(t, f.reg.Snapshot(matrixOwner, sid), 1)
		require.Equal(t, committed, s1ProgramGeneration(t, f.reg.Snapshot(matrixOwner, sid)[0], "snapshot Card"))
		boot := secretrequest.NewRegistry(f.options())
		require.NoError(t, boot.Recover())
		restored, err := boot.Get(rec.Card.RequestId, matrixOwner)
		require.NoError(t, err)
		require.Equal(t, committed, s1ProgramGeneration(t, restored.Card, "recovered Card"))
	})

	t.Run("S99_chat_card_uses_program_token_rules", func(t *testing.T) {
		f := s1ProgramsCfg(t)
		sid := f.newSession("dev", "")
		rec, err := f.reg.Create(context.Background(), sid, "dev", "web-card", "",
			s1Input(t, map[string]any{"purpose": "connect GitHub", "program": "github"}))
		if err != nil {
			t.Fatalf("BLOCKED: the program card cannot be raised — required by FR-051/S-99 (registry said: %v)", err)
		}
		require.Equal(t, "pending", rec.Card.State)
		require.NotEmpty(t, rec.Card.Fields, "the card declares its field (K1)")

		canary := programstore.SeedCanary(t, f.store)
		before := programstore.Snapshot(t, f.store, canary, rec.Card.RequestId)

		_, submitErr := f.reg.Submit(rec.Card.RequestId, matrixOwner,
			valuesInput(map[string]string{rec.Card.Fields[0].Key: "abc123"}))
		require.Error(t, submitErr, "the too-short value is refused by the program rules")
		var reqErr *secretrequest.RequestError
		require.ErrorAs(t, submitErr, &reqErr)
		require.Equal(t, http.StatusBadRequest, reqErr.Status, "400 with the too-short message")
		require.Contains(t, reqErr.Message, "The token is too short. A token has at least 8 characters.",
			"the message is D-1's verbatim too-short text")

		// The request stays pending; nothing is stored.
		after, gerr := f.reg.Get(rec.Card.RequestId, matrixOwner)
		require.NoError(t, gerr, "the request is still readable")
		require.Equal(t, "pending", after.Card.State, "the request stays pending")
		require.Equal(t, before, programstore.Snapshot(t, f.store, canary, rec.Card.RequestId), "same too-short submission preserves value/kind/selection/generation/metadata/known receipts")
	})

	t.Run("S55_channel_turn_refused_existing_text", func(t *testing.T) {
		// This pre-existing refusal precedes recipient resolution. No Programs
		// field/catalog is a prerequisite for keeping the channel guard alive.
		f := newMatrixEnv(t)
		f.cfg.Agents.List = append(f.cfg.Agents.List, config.AgentConfig{ID: "dev", Name: "Dev"})
		sid := f.newSession("dev", "")
		origin := tools.NewTurnOrigin()
		origin.AddChannelPerson("a person", "telegram:1")
		ctx := tools.WithTurnOrigin(
			tools.WithAgentID(
				tools.WithTranscriptSessionID(
					tools.WithToolContext(context.Background(), "telegram", sid),
					sid),
				"dev"),
			origin,
		)
		tool := tools.NewRequestSecretTool(func() *secretrequest.Registry { return f.reg }, nil)
		result := tool.Execute(ctx, map[string]any{"purpose": "connect GitHub", "program": "github"})
		require.True(t, result.IsError, "the card is refused in a channel turn")
		require.Contains(t, result.ForLLM, "open the web app and ask",
			"the refusal is the existing channel-turn card text: %s", result.ForLLM)
	})
	t.Run("S54_held_check_resume_before_completion", func(t *testing.T) {
		c := s1HeldCardCheck(t)
		f := c.f
		rec := f.create(f.newSession("dev", ""), "dev", s1Input(t, map[string]any{"purpose": "connect GitHub", "program": "github"}))
		submitted, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{rec.Card.Fields[0].Key: "tok_valid_12345"}))
		require.NoError(t, err, "submission and continuation must return while the check is held")
		c.started(t)
		require.Equal(t, "submitted", submitted.Card.State)
		require.Equal(t, float64(1), s1ProgramGeneration(t, submitted.Card, "same submitted card"))
		id, ok := secretrequest.ProgramID(submitted)
		require.True(t, ok)
		require.Equal(t, "github", id)
		require.Len(t, f.attempts, 1, "exactly one actual continuation delivery before release")
		require.Equal(t, submitted.Card, f.attempts[0].Card)
		message := strings.ToLower(secretrequest.ResumeMessage(f.attempts[0]))
		require.Contains(t, message, "stored for github")
		require.Contains(t, message, "account check is running")
		require.NotContains(t, message, "connected as")
		requireValueFree(t, "held-check continuation", message, "tok_valid_12345")
		require.Equal(t, "checking", c.svc.Connection(id).State, "the check is STILL held after continuation")
		c.release(t)
		require.Equal(t, "connected", c.svc.Connection(id).State)
		require.Len(t, f.attempts, 1, "settlement must not continue the agent a second time")
	})
	t.Run("S54_same_card_check_settles_Connected_as_octocat_after_reload", func(t *testing.T) {
		c := s1HeldCardCheck(t)
		f := c.f
		sid := f.newSession("dev", "")
		rec := f.create(sid, "dev", s1Input(t, map[string]any{"purpose": "connect GitHub", "program": "github"}))
		submitted, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{rec.Card.Fields[0].Key: "tok_valid_12345"}))
		require.NoError(t, err)
		c.started(t)
		receipt, found, err := f.store.CardReceipt(rec.Card.RequestId)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, float64(1), s1ProgramGeneration(t, receipt, "same card receipt"))
		boot := secretrequest.NewRegistry(f.options())
		require.NoError(t, boot.Recover())
		restored, err := boot.Get(rec.Card.RequestId, matrixOwner)
		require.NoError(t, err)
		require.Equal(t, submitted.Card, restored.Card, "reload restores THIS exact card and its pinned generation")
		c.release(t)
		id, ok := secretrequest.ProgramID(restored)
		require.True(t, ok)
		require.Equal(t, "github", id)
		conn := c.svc.Connection(id)
		require.Equal(t, "connected", conn.State)
		require.Equal(t, "octocat", conn.AccountLabel)
		require.Equal(t, float64(conn.Generation), s1ProgramGeneration(t, restored.Card, "settled card"), "same-card generation, never an unrelated connection")
		require.Len(t, f.attempts, 1, "recovery and settlement do not duplicate continuation")
		// This verifies backend association/recovery, not rendered text. The
		// real card's Connected-as text is tested on the UI boundary separately.
	})
}
