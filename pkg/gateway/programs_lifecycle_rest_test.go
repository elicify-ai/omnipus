// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramCredential_SaveCheckStates (order 16) — S-01 (valid
// token connects: checking/generation 1, then connected with the account
// label), S-03 (program missing → saved_not_checked), S-04 (check rejected
// → check_failed with GitHub's own words), S-05 (store failure keeps the
// previous token), S-06 (check timeout), S-36 (no check defined, token still
// available); TestProgramReplaceCheckDisconnect (order 32) — S-51 (replace,
// generation 2, label hubot), S-52 (check again, generation unchanged),
// S-53 (disconnect: generation +1, switch stays on); S-45's REST face (the
// channel programs setting has one writer; the routing rebind resets it);
// and TestProgramError_PassThrough (order 35) — S-32 (program error passed
// through).
//
// Level Integration over the REAL production mux (pgPersonAPI): the person's
// browser session is the only caller (FR-028's person gate). The S1 routes
// (POST /programs/{id}/credential, POST /programs/{id}/check, DELETE
// /programs/{id}, PUT /channels/{id}/programs) are not registered at RED:
// each subtest probes its route first and reports routeMissing (BLOCKED)
// — never a skip — with the spec's verbatim expectations recorded for GREEN
// reconciliation. The fake-gh harness (states, generations, account labels,
// process groups) rides the same GREEN seams.
//
// Oracle: US-1/US-4/US-7 scenario texts, the message table, FR-028. Expected
// values come from the spec, never from the handlers.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pgProgramsSessionCall sends one person-session call to the production
// chain (cookie + CSRF, the only caller the person gate admits).
func pgProgramsSessionCall(t *testing.T, f *pgGateFixture, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	pgAttach(f, req, "", true, true)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

func TestProgramCredential_SaveCheckStates(t *testing.T) {
	f := pgPersonAPI(t)

	t.Run("S01_valid_token_connects", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs/github/credential",
			`{"value":"tok_valid_12345"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/credential (save the token)", "S1 contract table / S-01")
		}
		// GREEN asserts here (spec S-01): 201 with state "checking" and
		// generation 1; a later GET /programs shows state "connected" with
		// account_label "octocat" (fake gh); and no response, log line or
		// audit row contains tok_valid_12345.
		require.Fail(t, "GREEN: assert the save response state/generation and the connected row")
	})

	t.Run("S03_program_missing_saved_not_checked", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs/github/credential",
			`{"value":"tok_valid_12345"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/credential (no gh on PATH)", "S1 contract table / S-03")
		}
		require.Fail(t, "GREEN: assert saved_not_checked with \"Saved, not checked: gh was not found on this computer.\"")
	})

	t.Run("S04_check_rejected_check_failed", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs/github/credential",
			`{"value":"tok_wrong_999999"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/credential (wrong token)", "S1 contract table / S-04")
		}
		require.Fail(t, "GREEN: assert check_failed with the verbatim message `The account check failed. GitHub said: \"HTTP 401: Bad credentials\".` and no account label")
	})

	t.Run("S05_store_failure_keeps_previous_token", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs/github/credential",
			`{"value":"tok_other_67890"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/credential (store failure injection)", "S1 contract table / S-05")
		}
		require.Fail(t, "GREEN: inject the failing store write (test-disputes E), assert 500 with the keep-previous message, generation stays 1, label octocat, and printenv still 16")
	})

	t.Run("S06_check_timeout", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs/github/credential",
			`{"value":"tok_valid_12345"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/credential (30s fake gh)", "S1 contract table / S-06")
		}
		require.Fail(t, "GREEN: assert within 25s state check_failed with \"The account check did not finish in 20 s.\" and the process group is gone")
	})

	t.Run("S36_no_check_defined_token_still_available", func(t *testing.T) {
		// Acme without an account check: the custom entry must exist first.
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs",
			`{"display_name":"Acme","program_words":["acme"],"token_variables":["ACME_TOKEN"],`+
				`"network_sites":["api.acme.example"]}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs (create Acme)", "S1 contract table / S-36")
		}
		require.Fail(t, "GREEN: create Acme without identity_check, save acme_tok_123456, assert saved_not_checked with the no-check message and printenv ACME_TOKEN = 16")
	})
}

func TestProgramReplaceCheckDisconnect(t *testing.T) {
	f := pgPersonAPI(t)

	t.Run("S51_replace_generation2_label_hubot", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs/github/credential",
			`{"value":"tok_new_654321"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/credential (replace)", "S1 contract table / S-51")
		}
		require.Fail(t, "GREEN: connected generation 1 first (fake gh hubot for tok_new_654321), then assert \"Connected as hubot\", generation 2, printenv = 15")
	})

	t.Run("S52_check_again_failed_generation_unchanged", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs/github/check", `{}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs/github/check", "S1 contract table / S-52")
		}
		require.Fail(t, "GREEN: connected first, then fake gh exits 1, assert check_failed and the generation unchanged")
	})

	t.Run("S53_disconnect_generation_plus_one_switch_stays_on", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodDelete, "/programs/github", "")
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "DELETE /programs/github", "S1 contract table / S-53")
		}
		require.Fail(t, "GREEN: connected and on for Dev first, then assert not_connected, generation +1, printenv has no GH_TOKEN, Dev's programs stays [\"github\"], and the dialog states the token stays valid at GitHub until revoked there")
	})

	t.Run("S45_channel_programs_setting_one_writer", func(t *testing.T) {
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/channels/telegram/programs",
			`{"allowed":true,"revision":1}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /channels/telegram/programs", "S1 contract table / S-45")
		}
		// GREEN asserts here (spec S-45): set_config cannot write
		// channels.telegram.programs_allowed (refused, unchanged); a routing
		// rebind of telegram through PUT /api/v1/channels/telegram/routing
		// leaves the switch off after that same write.
		require.Fail(t, "GREEN: assert the set_config refusal and the rebind reset")
	})
}

func TestProgramError_PassThrough(t *testing.T) {
	t.Run("S32_program_error_passed_through", func(t *testing.T) {
		// Given GitHub on for Dev; the fake gh prints
		// `HTTP 403: Resource not accessible by integration` and exits 1.
		// When Dev runs `gh api repos/o/r` — the agent exec runtime is the
		// env integration slice's harness (FR-011); at RED the agent env
		// builder cannot hand program variables to the shell harness at all.
		t.Fatalf("BLOCKED: the agent environment builder cannot attach program variables to the bash harness (FR-011 runtime) — required by S-32's `gh api repos/o/r` run (expect: exactly `HTTP 403: Resource not accessible by integration` and exit code 1, nothing rewritten)")
	})
}
