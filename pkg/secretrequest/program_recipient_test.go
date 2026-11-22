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

// continuationText finds the record's continuation surface by key name (any
// key containing "continuation", case-insensitive) and returns its text. The
// surface's exact wire name is GREEN's; a record with no continuation-shaped
// key is the caller's BLOCKED case.
func continuationText(m map[string]any) (string, bool) {
	for k, v := range m {
		if !strings.Contains(strings.ToLower(k), "continuation") {
			continue
		}
		switch tv := v.(type) {
		case string:
			return tv, tv != ""
		case map[string]any:
			for _, inner := range tv {
				if s, ok := inner.(string); ok && s != "" {
					return s, true
				}
			}
		}
	}
	return "", false
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
		// S-54's settlement clauses at the registry level. ("When the check
		// ends the card shows Connected as octocat" is the async-check face —
		// pinned by the gateway lifecycle rows with the fake gh; Dev's
		// programs staying unchanged is pinned by the parity rows.) The
		// submitted record is read through its marshalled form: the wire
		// fields (program_generation, the continuation) arrive at GREEN.
		f := s1ProgramsCfg(t)
		sid := f.newSession("dev", "")
		rec, err := f.reg.Create(context.Background(), sid, "dev", "web-card", "",
			s1Input(t, map[string]any{"purpose": "connect GitHub", "program": "github"}))
		if err != nil {
			t.Fatalf("BLOCKED: the program card cannot be raised — required by FR-051/S-54 (registry said: %v)", err)
		}
		submitted, serr := f.reg.Submit(rec.Card.RequestId, matrixOwner,
			valuesInput(map[string]string{rec.Card.Fields[0].Key: "tok_valid_12345"}))
		require.NoError(t, serr, "the valid submission settles the card: %v", serr)
		require.Equal(t, "submitted", submitted.Card.State, "the request is submitted")

		raw, merr := json.Marshal(submitted)
		require.NoError(t, merr)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		// program_generation: the generation the submission committed (spec
		// contract table; taken from the CardReceipt so it survives restart).
		genRaw, has := m["program_generation"]
		if !has {
			t.Fatalf("BLOCKED: the submitted request state carries no program_generation — required by S-54 and the contract table (record keys: %v)", sortedKeys(m))
		}
		gen, _ := genRaw.(float64)
		require.GreaterOrEqual(t, gen, float64(1), "program_generation is at least 1")
		// The continuation: Dev continues at once — the token is stored for
		// GitHub and the account check is running; it never claims a result.
		cont, hasCont := continuationText(m)
		if !hasCont {
			t.Fatalf("BLOCKED: the submitted record carries no continuation surface — required by S-54's continuation clause (record keys: %v)", sortedKeys(m))
		}
		require.Contains(t, cont, "stored for GitHub", "the continuation says the token is stored")
		require.Contains(t, cont, "account check is running", "the continuation says the check is running")
		require.NotContains(t, cont, "Connected", "the continuation does not claim a result")
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

		namesBefore, lerr := f.store.List()
		require.NoError(t, lerr, "instrument: the store lists")

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
		namesAfter, aerr := f.store.List()
		require.NoError(t, aerr)
		require.Equal(t, namesBefore, namesAfter, "nothing is stored")
	})

	t.Run("S55_channel_turn_refused_existing_text", func(t *testing.T) {
		f := s1ProgramsCfg(t)
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
}
