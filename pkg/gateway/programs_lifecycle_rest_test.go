// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramCredential_SaveCheckStates (order 16) — S-01 (valid
// token connects: checking/generation 1, then connected with the account
// label octocat), S-03 (no gh on PATH → saved_not_checked), S-04 (check
// rejected → check_failed with GitHub's own words), S-05 (store failure keeps
// the previous token: 500, generation stays 1, label octocat), S-06 (check
// timeout: 20 s bound, process group gone), S-36 (no check defined, token
// still available); TestProgramReplaceCheckDisconnect (order 32) — S-51
// (replace, generation 2, label hubot), S-52 (check again, generation
// unchanged), S-53 (disconnect via DELETE /programs/{id}/credential:
// generation +1, switch stays on); and the REST face of the channel setting
// (S-45's PUT/GET contract — S-45's one-writer clauses live in order 26's
// TestProgramTurnEligibility pack).
//
// Every row drives the REAL production mux (pgPersonAPI): the person's
// browser session is the only caller (FR-028's person gate), the save route
// is PUT /programs/{id}/credential (rev-8 contract table; ProgramCredential-
// Request is { value, expected_generation? }) and disconnect is DELETE
// /programs/{id}/credential. The S1 routes are not registered at RED: each
// row probes its route first and reports routeMissing (BLOCKED) — never a
// skip. The fake gh implements the spec's own harness contract (rev 8 US-1
// Given): it prints the mapped account for the matching GH_TOKEN and exits 1
// with GitHub's error line otherwise; the timeout fake sleeps 30 s and
// records its pid. The process-group-gone assertion is Unix pgid semantics
// (spec H-17 treats Windows separately); on Windows the row still asserts the
// state, message and timing and only omits the pgid read.
//
// Oracle: US-1/US-4/US-7 scenario texts, the message table (rev 8 lines 682
// and 698), FR-028. Expected values come from the spec, never from the
// handlers.

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
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

// pgInstallFakeGh prepends a scripted fake gh to PATH for the calling
// (sub)test. The script still records every start to the fixture's marker so
// start-count assertions keep working. `sh` is the POSIX body, `cmd` the
// Windows one (both end with the fixture's start marker already written).
func pgInstallFakeGh(t *testing.T, f *pgGateFixture, shBody, cmdBody string) {
	t.Helper()
	bin := t.TempDir()
	name := "gh"
	body := "#!/bin/sh\necho started >> " + f.ghStarts + "\n" + shBody + "\n"
	if runtime.GOOS == "windows" {
		name += ".cmd"
		body = "@echo started >> " + f.ghStarts + "\r\n" + cmdBody + "\r\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// pgFakeGhAccounts renders the spec's fake gh (rev 8, US-1 Given): it prints
// the mapped account for the matching GH_TOKEN and exits 1 with GitHub's own
// error line on stderr otherwise.
func pgFakeGhAccounts(accounts map[string]string) (string, string) {
	tokens := make([]string, 0, len(accounts))
	for tok := range accounts {
		tokens = append(tokens, tok)
	}
	sort.Strings(tokens)
	var sh, cmd strings.Builder
	sh.WriteString("case \"$GH_TOKEN\" in")
	cmd.WriteString("@echo off\r\n")
	for _, tok := range tokens {
		fmt.Fprintf(&sh, "\n  %s) echo %s;;", tok, accounts[tok])
		fmt.Fprintf(&cmd, "if \"%%GH_TOKEN%%\"==\"%s\" (echo %s\r\nexit /b 0\r\n)\r\n", tok, accounts[tok])
	}
	sh.WriteString("\n  *) echo 'HTTP 401: Bad credentials' >&2; exit 1;;\nesac")
	cmd.WriteString("echo HTTP 401: Bad credentials 1>&2\r\nexit /b 1\r\n")
	return sh.String(), cmd.String()
}

// pgFakeGhSleep renders the S-06 timeout fake: it records its pid (Unix) and
// sleeps 30 s — past the spec's 20 s check bound.
func pgFakeGhSleep(pidFile string) (string, string) {
	sh := "echo $$ > " + pidFile + "\nsleep 30\n"
	cmd := "ping -n 31 127.0.0.1 >nul\r\n"
	return sh, cmd
}

// pgProgramsList reads GET /programs and returns the entries (ProgramList is
// { entries[], web_chat_allowed, revision } — rev 8 schema table). Decoded
// field-agnostically into maps: the generated carrier does not exist at RED.
func pgProgramsList(t *testing.T, f *pgGateFixture) []map[string]any {
	t.Helper()
	w := pgProgramsSessionCall(t, f, http.MethodGet, "/programs", "")
	require.Equal(t, http.StatusOK, w.Code, "GET /programs: %s", w.Body.String())
	var root map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &root), "GET /programs returns an object: %s", w.Body.String())
	raw, has := root["entries"]
	require.True(t, has, "GET /programs carries entries[] (ProgramList): %s", w.Body.String())
	arr, ok := raw.([]any)
	require.True(t, ok, "entries[] is an array: %s", w.Body.String())
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		em, ok := e.(map[string]any)
		require.True(t, ok, "each entry is an object: %s", w.Body.String())
		out = append(out, em)
	}
	return out
}

// pgProgramEntryByID returns the entry with the given id (or program_id).
func pgProgramEntryByID(t *testing.T, f *pgGateFixture, id string) map[string]any {
	t.Helper()
	for _, e := range pgProgramsList(t, f) {
		if e["id"] == id || e["program_id"] == id {
			return e
		}
	}
	t.Fatalf("program %q is not in GET /programs", id)
	return nil
}

// pgEntryJSON renders an entry for failure messages.
func pgEntryJSON(e map[string]any) string {
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Sprintf("%v", e)
	}
	return string(b)
}

// pgPollEntryState polls GET /programs until the entry reaches the wanted
// state (the async check states end within seconds under the fake gh) and
// returns the entry.
func pgPollEntryState(t *testing.T, f *pgGateFixture, id, want string, within time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(within)
	var last map[string]any
	for {
		last = pgProgramEntryByID(t, f, id)
		if last["state"] == want {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("program %q never reached state %q within %s (last entry: %s)", id, want, within, pgEntryJSON(last))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// pgConnectGithub saves tok_valid_12345 through the real save route (PUT
// /programs/github/credential — rev 8) with the spec's octocat fake and waits
// for connected. BLOCKED (routeMissing) at RED.
func pgConnectGithub(t *testing.T, f *pgGateFixture) map[string]any {
	t.Helper()
	shBody, cmdBody := pgFakeGhAccounts(map[string]string{"tok_valid_12345": "octocat"})
	pgInstallFakeGh(t, f, shBody, cmdBody)
	w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential", `{"value":"tok_valid_12345"}`)
	if w.Code == http.StatusNotFound {
		routeMissing(t, w, "PUT /programs/github/credential (save the token)", "S1 contract table / S-01")
	}
	require.Equal(t, http.StatusOK, w.Code, "the save is processed: %s", w.Body.String())
	return pgPollEntryState(t, f, "github", "connected", 15*time.Second)
}

func TestProgramCredential_SaveCheckStates(t *testing.T) {
	f := pgPersonAPI(t)

	t.Run("S01_valid_token_connects", func(t *testing.T) {
		shBody, cmdBody := pgFakeGhAccounts(map[string]string{"tok_valid_12345": "octocat"})
		pgInstallFakeGh(t, f, shBody, cmdBody)
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential",
			`{"value":"tok_valid_12345"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential (save the token)", "S1 contract table / S-01")
		}
		// S-01 Then: state checking, generation 1; a later GET /programs has
		// state connected, account_label octocat.
		require.Equal(t, http.StatusOK, w.Code, "the save is processed: %s", w.Body.String())
		var saved map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &saved), "the save answers ProgramEntry: %s", w.Body.String())
		require.Equal(t, "checking", saved["state"], "the save answers state checking")
		require.Equal(t, float64(1), saved["generation"], "the first save is generation 1")
		entry := pgPollEntryState(t, f, "github", "connected", 15*time.Second)
		require.Equal(t, "octocat", entry["account_label"], "the check stored the account label octocat")
		// And: no response contains tok_valid_12345.
		require.NotContains(t, w.Body.String(), "tok_valid_12345", "the save response never carries the token")
		listBody := pgProgramsSessionCall(t, f, http.MethodGet, "/programs", "")
		require.NotContains(t, listBody.Body.String(), "tok_valid_12345", "GET /programs never carries the token")
	})

	t.Run("S03_program_missing_saved_not_checked", func(t *testing.T) {
		// Given NO gh on the gateway PATH: an empty PATH segment set wins over
		// the fixture's fake (the subtest's t.Setenv scopes to this row).
		t.Setenv("PATH", t.TempDir())
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential",
			`{"value":"tok_valid_12345"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential (no gh on PATH)", "S1 contract table / S-03")
		}
		require.Equal(t, http.StatusOK, w.Code, "the save is processed: %s", w.Body.String())
		entry := pgPollEntryState(t, f, "github", "saved_not_checked", 15*time.Second)
		require.Equal(t, "Saved, not checked: gh was not found on this computer.", entry["message"],
			"S-03's verbatim reason: %s", pgEntryJSON(entry))
		require.Equal(t, 0, f.ghStarted(), "nothing ran (no gh exists)")
	})

	t.Run("S04_check_rejected_check_failed", func(t *testing.T) {
		shBody, cmdBody := pgFakeGhAccounts(map[string]string{"tok_valid_12345": "octocat"})
		pgInstallFakeGh(t, f, shBody, cmdBody) // 401s for any other token
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential",
			`{"value":"tok_wrong_999999"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential (wrong token)", "S1 contract table / S-04")
		}
		require.Equal(t, http.StatusOK, w.Code, "the save is processed: %s", w.Body.String())
		entry := pgPollEntryState(t, f, "github", "check_failed", 15*time.Second)
		require.Equal(t, `The account check failed. GitHub said: "HTTP 401: Bad credentials".`, entry["message"],
			"S-04's verbatim message with GitHub's own words: %s", pgEntryJSON(entry))
		_, hasLabel := entry["account_label"]
		require.False(t, hasLabel, "a rejected check stores no account label: %s", pgEntryJSON(entry))
	})

	t.Run("S05_store_failure_keeps_previous_token", func(t *testing.T) {
		// Given connected (generation 1) — real connect with the octocat fake.
		entry := pgConnectGithub(t, f)
		require.Equal(t, float64(1), entry["generation"], "instrument: the Given is generation 1")
		// The next store write fails: the data directory goes read-only, so
		// the commit's atomic write cannot create its temp file (the
		// injection of test-disputes E6 — a real write failure, not a mock).
		t.Cleanup(func() { _ = os.Chmod(f.api.homePath, 0o700) })
		require.NoError(t, os.Chmod(f.api.homePath, 0o500), "instrument: the data dir is write-blocked")
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential",
			`{"value":"tok_other_67890"}`)
		require.NoError(t, os.Chmod(f.api.homePath, 0o700), "instrument: the data dir is writable again")
		require.Equal(t, http.StatusInternalServerError, w.Code,
			"the store failure answers 500: got %d %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "The token could not be saved. The previous token is still in use. Try again.",
			"the keep-previous message (message table)")
		after := pgProgramEntryByID(t, f, "github")
		require.Equal(t, "connected", after["state"], "the row still shows connected")
		require.Equal(t, "octocat", after["account_label"], "the row still shows octocat")
		require.Equal(t, float64(1), after["generation"], "generation stays 1")
	})

	t.Run("S06_check_timeout", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "gh-pid")
		shBody, cmdBody := pgFakeGhSleep(pidFile)
		pgInstallFakeGh(t, f, shBody, cmdBody)
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential",
			`{"value":"tok_valid_12345"}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /programs/github/credential (30s fake gh)", "S1 contract table / S-06")
		}
		require.Equal(t, http.StatusOK, w.Code, "the save is processed: %s", w.Body.String())
		// Then: within 25 s the state is check_failed with the timeout
		// message, and the process group is gone.
		entry := pgPollEntryState(t, f, "github", "check_failed", 25*time.Second)
		require.Equal(t, "The account check did not finish in 20 s.", entry["message"],
			"S-06's verbatim timeout message: %s", pgEntryJSON(entry))
		if runtime.GOOS == "windows" {
			t.Log("Windows: the pgid-gone read is Unix semantics (spec H-17); the state, message and 25 s bound above are asserted")
			return
		}
		raw, err := os.ReadFile(pidFile)
		require.NoError(t, err, "the fake recorded its pid")
		pid := 0
		_, ferr := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid)
		require.NoError(t, ferr, "the pid file holds an integer: %q", string(raw))
		require.NotZero(t, pid, "the fake recorded a real pid")
		err = unix.Kill(-pid, 0) // signal 0: existence probe of the whole group
		require.ErrorIs(t, err, unix.ESRCH,
			"the check's process group is gone after the timeout (probe of -%d)", pid)
	})

	t.Run("S36_no_check_defined_token_still_available", func(t *testing.T) {
		// Acme has no account check: create it first (contract: 201).
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs",
			`{"display_name":"Acme","program_words":["acme"],"token_variables":["ACME_TOKEN"],`+
				`"network_sites":["api.acme.example"]}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "POST /programs (create Acme)", "S1 contract table / S-36")
		}
		require.Equal(t, http.StatusCreated, w.Code, "Acme is created: %s", w.Body.String())
		var created map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created), "the create answers the entry: %s", w.Body.String())
		id, _ := created["id"].(string)
		if id == "" {
			id, _ = created["program_id"].(string)
		}
		require.NotEmpty(t, id, "the created entry carries its id: %s", w.Body.String())
		require.Equal(t, "not_connected", created["state"], "a new entry lists not_connected")
		// Save without any identity check defined: saved_not_checked, and the
		// token is available (the message table's none-branch).
		save := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/"+id+"/credential",
			`{"value":"acme_tok_123456"}`)
		require.Equal(t, http.StatusOK, save.Code, "the save is processed: %s", save.Body.String())
		entry := pgPollEntryState(t, f, id, "saved_not_checked", 15*time.Second)
		require.Equal(t, "Saved, not checked: this program has no account check.", entry["message"],
			"the no-check reason: %s", pgEntryJSON(entry))
		require.Equal(t, 0, f.ghStarted(), "no check ran (Acme defines none)")
	})
}

func TestProgramReplaceCheckDisconnect(t *testing.T) {
	f := pgPersonAPI(t)
	entry := pgConnectGithub(t, f) // BLOCKED (routeMissing) at RED; connected gen 1 octocat at GREEN
	genBefore := entry["generation"]

	// S-53's Given includes "on for Dev": switch GitHub on for Dev through the
	// shared agent route's new programs field.
	switchOn := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/agents/dev", `{"programs":["github"]}`)
	if switchOn.Code != http.StatusOK {
		t.Fatalf("BLOCKED: the shared /api/v1/agents/dev route does not accept the programs field — required by S-53's Given and S-85 (got %d %s)", switchOn.Code, switchOn.Body.String())
	}

	t.Run("S51_replace_generation2_label_hubot", func(t *testing.T) {
		// The spec's S-51 fake: hubot for tok_new_654321.
		shBody, cmdBody := pgFakeGhAccounts(map[string]string{
			"tok_valid_12345": "octocat", "tok_new_654321": "hubot"})
		pgInstallFakeGh(t, f, shBody, cmdBody)
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential",
			`{"value":"tok_new_654321"}`)
		require.Equal(t, http.StatusOK, w.Code, "the replace is processed: %s", w.Body.String())
		var saved map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &saved), "the replace answers ProgramEntry: %s", w.Body.String())
		require.Equal(t, float64(2), saved["generation"], "the replace is generation 2")
		replaced := pgPollEntryState(t, f, "github", "connected", 15*time.Second)
		require.Equal(t, "hubot", replaced["account_label"], "Connected as hubot")
		// printenv GH_TOKEN | wc -c = 15 rides the env integration harness
		// (FR-011 runtime; S-32's row covers that surface).
	})

	t.Run("S52_check_again_failed_generation_unchanged", func(t *testing.T) {
		shBody, cmdBody := pgFakeGhAccounts(map[string]string{}) // 401s for everything
		pgInstallFakeGh(t, f, shBody, cmdBody)
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/programs/github/check", `{}`)
		require.Equal(t, http.StatusAccepted, w.Code, "the re-check is accepted: %s", w.Body.String())
		failed := pgPollEntryState(t, f, "github", "check_failed", 15*time.Second)
		require.Contains(t, failed["message"], "HTTP 401: Bad credentials",
			"the re-check failed with GitHub's words: %s", pgEntryJSON(failed))
		require.Equal(t, genBefore, failed["generation"],
			"a failed re-check never changes the generation: %s vs %s", failed["generation"], genBefore)
	})

	t.Run("S53_disconnect_generation_plus_one_switch_stays_on", func(t *testing.T) {
		// Disconnect is DELETE /programs/{id}/credential (rev 8 route table),
		// never DELETE /programs/{id} (that is the entry delete — S-38's
		// preset row answers 422 there).
		before := pgProgramEntryByID(t, f, "github")
		w := pgProgramsSessionCall(t, f, http.MethodDelete, "/programs/github/credential", "")
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "DELETE /programs/github/credential (disconnect)", "S1 contract table / S-53")
		}
		require.Equal(t, http.StatusOK, w.Code, "the disconnect is processed: %s", w.Body.String())
		after := pgPollEntryState(t, f, "github", "not_connected", 15*time.Second)
		require.Equal(t, before["generation"].(float64)+1, after["generation"],
			"disconnect increments the generation: %v -> %v", before["generation"], after["generation"])
		// Dev's switch stays on (the disconnect keeps switches).
		dev := pgProgramsSessionCall(t, f, http.MethodGet, "/api/v1/agents/dev", "")
		require.Equal(t, http.StatusOK, dev.Code, "GET /api/v1/agents/dev: %s", dev.Body.String())
		var devMap map[string]any
		require.NoError(t, json.Unmarshal(dev.Body.Bytes(), &devMap), "the agent reads back: %s", dev.Body.String())
		programs, has := devMap["programs"]
		require.True(t, has, "the agent record carries its programs list (S-53 keeps switches): %s", dev.Body.String())
		require.Contains(t, fmt.Sprintf("%v", programs), "github",
			"Dev's switch stays on after the disconnect: %v", programs)
		// Dev's next command has no GH_TOKEN rides the env integration harness
		// (FR-011 runtime; S-32's row covers that surface). The dialog's
		// stays-valid-at-GitHub text is the UI face — programs.spec.ts S53.
	})

	t.Run("S45_channel_programs_setting_rest_face", func(t *testing.T) {
		// The REST contract row: PUT 200, GET reflects. (S-45's set_config
		// refusal and the routing-rebind reset are order 26's
		// TestProgramTurnEligibility clauses; the rebind rule itself is rev 8
		// line: rebinding sets programs_allowed false in the same config
		// write.)
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/channels/telegram/programs",
			`{"allowed":true,"revision":1}`)
		if w.Code == http.StatusNotFound {
			routeMissing(t, w, "PUT /channels/telegram/programs", "S1 contract table / S-45 REST face")
		}
		require.Equal(t, http.StatusOK, w.Code, "the setting is saved: %s", w.Body.String())
		g := pgProgramsSessionCall(t, f, http.MethodGet, "/channels/telegram/programs", "")
		require.Equal(t, http.StatusOK, g.Code, "GET /channels/telegram/programs: %s", g.Body.String())
		var setting map[string]any
		require.NoError(t, json.Unmarshal(g.Body.Bytes(), &setting), "the setting reads back: %s", g.Body.String())
		require.Equal(t, true, setting["allowed"], "GET reflects the switch")
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
