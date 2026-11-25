// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED test — Skills & Tools redesign (#1963, epic #1519), fix round 4 part F,
// spec test 67 (TestPermissionsGet_UpgradeUnfinished, S-131, FR-084).
//
// Oracles, all transcribed from docs/internal/specs/skills-tools-redesign-spec.md
// as amended by b27c64109 (ruling arch/spec-gaps-1.md, 2026-11-25), never
// observed from an implementation:
//   - C-2 table row "PermissionSettings.upgrade_unfinished": a read-only array
//     of {agent_id, reason in [write_failed, unreadable]}, both required, no
//     other member; absent (not null, not []) when the list would be empty;
//     served by GET and by every PATCH response; not a member of the PATCH
//     body, so a body naming it gets the existing 400 for an unknown member;
//     source is the C-19 ledger, read on every request, never cached.
//   - C-19 "The notice": one entry {agent_id, reason: write_failed} per
//     `failures` entry, one entry {agent_id, reason: unreadable} per `skipped`
//     id ONLY while that agent's record file still exists under
//     entities/agents/ (checked at each read; deleting the record clears the
//     entry). No ledger or nothing to list: the member is absent.
//   - C-19 "A failed agent write": the run reports an Error naming the agent,
//     records failures[<id>] = write_failed in the ledger, the next start
//     retries only that agent and a successful write removes its `failures`
//     entry; an unreadable or malformed record is skipped permanently and its
//     id stays in the ledger's `skipped` list after `done`.
//   - S-131: the literal list [{mia, write_failed}, {jim, unreadable}] (so,
//     for this example, a failure entry comes before a skipped one).
//   - The ledger member names (`failures`: id to reason, `skipped`: ids) are
//     the ruling's option A2 and the C-19 text ("failures[<id>]", "the
//     ledger's `skipped` list").
//
// The member is read as raw JSON on purpose: the generated PermissionSettings
// type gains the member only when the backend regenerates the contracts, and a
// hand-written wire type is forbidden (Hard Constraint 8).
//
// RED reason (saved run ta/fix4-go-fix4f-09.log, 2026-11-25, final state of this
// file, against dev code at cc616bf74; b27c64109 changes only the spec, so its
// production code is the same; not a product PASS): the member is never served,
// so each positive subtest fails on its first assertion that the member is
// present ("Should be true", with the subtest's own message): the S-131 list,
// two failures, a skipped agent after done, the record-file check, a failure
// entry, every PATCH response, a refused PATCH's list, and the end-to-end
// subtest that runs the real upgrade through the seam (the run is real and
// returns its Error; the member is still not served). The five absence cases
// and the 400-refusal subtest PASS today: they are controls (the production
// decoder already refuses an unknown member with 400), kept so a probe that
// serves or accepts the member turns them red. Earlier runs of this test
// (fix4f-01..03) predate the seam edit and the split of the refusal subtest and
// are not its proof. The mutation probe this file must turn red ("list a
// skipped agent whose record file is gone") is CHECK's to run: deferred to
// CHECK.
//
// Spec-silent points kept out of the assertions: the order of several
// entries of the same kind, the singular wording for n = 1, and the exact
// wording of the 400 for an unknown member.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const upgradeLedgerRelPath = "entities/.skill-permissions-upgrade-ledger.json"

// The S-131 given: Mia's record write failed (it stays pending, a failures
// entry), Jim's record was skipped as unreadable at the snapshot.
const s131Ledger = `{"state":"in_progress","pending":["mia"],"in_flight":null,"failures":{"mia":"write_failed"},"skipped":["jim"]}`

const (
	legacyMiaRecord = `{"id":"mia","name":"Mia","skills":["plan"],"tools":{"builtin":{"policies":{"Skill":"ask"}}}}`
	// A truncated file: not valid JSON, so the snapshot skips it for good.
	malformedJimRecord = `{"id":"jim","tools":`
)

func writeUpgradeLedgerFixture(t *testing.T, home, literal string) {
	t.Helper()
	path := filepath.Join(home, upgradeLedgerRelPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(literal), 0o600))
}

func writeAgentRecordFixture(t *testing.T, home, id, literal string) string {
	t.Helper()
	path := filepath.Join(home, "entities", "agents", id+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(literal), 0o600))
	return path
}

// upgradeUnfinishedServed reads the member out of one response body. The
// second result is false when the member is absent from the object (a JSON
// null or an empty array is present, and is returned as such, so the
// "absent, not empty" rule stays checkable).
func upgradeUnfinishedServed(t *testing.T, w *httptest.ResponseRecorder) ([]map[string]any, bool) {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &object), "the read model is one JSON object")
	raw, present := object["upgrade_unfinished"]
	if !present {
		return nil, false
	}
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(raw, &entries), "upgrade_unfinished is an array of objects, got %s", raw)
	if entries == nil {
		entries = []map[string]any{}
	}
	return entries, true
}

func requireUpgradeUnfinishedAbsent(t *testing.T, w *httptest.ResponseRecorder, why string) {
	t.Helper()
	entries, present := upgradeUnfinishedServed(t, w)
	require.False(t, present, "%s: the member must be absent, not null or an empty array (C-2); got %v", why, entries)
}

func TestPermissionsGet_UpgradeUnfinished(t *testing.T) {
	t.Run("ledger failures and skipped ids map to write_failed and unreadable, the literal S-131 list", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		writeAgentRecordFixture(t, api.homePath, "mia", legacyMiaRecord)
		writeAgentRecordFixture(t, api.homePath, "jim", malformedJimRecord)
		writeUpgradeLedgerFixture(t, api.homePath, s131Ledger)

		entries, present := upgradeUnfinishedServed(t, getPermissions(t, api, routes))
		require.True(t, present, "GET /security/permissions must serve upgrade_unfinished while the ledger holds a failure and a skipped agent (S-131, C-2)")
		require.Equal(t, []map[string]any{
			{"agent_id": "mia", "reason": "write_failed"},
			{"agent_id": "jim", "reason": "unreadable"},
		}, entries, "S-131: exactly the failure first, then the skipped agent, each with only agent_id and reason")
	})

	t.Run("several failed agents are each listed once as write_failed", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		writeAgentRecordFixture(t, api.homePath, "mia", legacyMiaRecord)
		writeAgentRecordFixture(t, api.homePath, "ray", `{"id":"ray","name":"Ray","skills":[]}`)
		writeUpgradeLedgerFixture(t, api.homePath,
			`{"state":"in_progress","pending":["mia","ray"],"in_flight":null,"failures":{"mia":"write_failed","ray":"write_failed"},"skipped":[]}`)

		entries, present := upgradeUnfinishedServed(t, getPermissions(t, api, routes))
		require.True(t, present, "two failures entries must be served (C-19 'The notice')")
		// The spec fixes no order between entries of the same kind.
		require.ElementsMatch(t, []map[string]any{
			{"agent_id": "mia", "reason": "write_failed"},
			{"agent_id": "ray", "reason": "write_failed"},
		}, entries, "one entry per failures entry, no duplicates, no extra members")
	})

	t.Run("a skipped agent stays listed after the upgrade is done, while its record exists", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		writeAgentRecordFixture(t, api.homePath, "jim", malformedJimRecord)
		// C-19 step 5: ledger {state: done} with its `skipped` list kept.
		writeUpgradeLedgerFixture(t, api.homePath, `{"state":"done","pending":[],"in_flight":null,"failures":{},"skipped":["jim"]}`)

		entries, present := upgradeUnfinishedServed(t, getPermissions(t, api, routes))
		require.True(t, present, "a skipped agent is skipped permanently; the notice stays after done (C-19 step 5, 'A failed agent write')")
		require.Equal(t, []map[string]any{{"agent_id": "jim", "reason": "unreadable"}}, entries)
	})

	t.Run("a skipped agent is listed only while its record file exists, checked at each read", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		jim := writeAgentRecordFixture(t, api.homePath, "jim", malformedJimRecord)
		writeUpgradeLedgerFixture(t, api.homePath, `{"state":"done","pending":[],"in_flight":null,"failures":{},"skipped":["jim"]}`)

		entries, present := upgradeUnfinishedServed(t, getPermissions(t, api, routes))
		require.True(t, present, "control: Jim's record exists, so the notice lists him")
		require.Equal(t, []map[string]any{{"agent_id": "jim", "reason": "unreadable"}}, entries)

		require.NoError(t, os.Remove(jim), "the person deletes Jim's record file")
		requireUpgradeUnfinishedAbsent(t, getPermissions(t, api, routes),
			"after Jim's record file is deleted the ledger still names him, but the entry is cleared (C-19 'The notice', S-131)")
	})

	t.Run("a failure entry is not filtered by the record check", func(t *testing.T) {
		// The record-exists rule is stated for skipped ids only. A failures
		// entry is listed as the ledger holds it (C-19: one entry per failures
		// entry); Mia's record file exists here, as it does for a real failed
		// write (the write failed, the file is still there).
		api, routes := newPermissionsAPI(t)
		writeAgentRecordFixture(t, api.homePath, "mia", legacyMiaRecord)
		writeUpgradeLedgerFixture(t, api.homePath,
			`{"state":"in_progress","pending":["mia"],"in_flight":null,"failures":{"mia":"write_failed"},"skipped":[]}`)

		entries, present := upgradeUnfinishedServed(t, getPermissions(t, api, routes))
		require.True(t, present, "a failures entry is served (C-19 'The notice': one entry per failures entry)")
		require.Equal(t, []map[string]any{{"agent_id": "mia", "reason": "write_failed"}}, entries)
	})

	t.Run("the member is absent when there is nothing to list", func(t *testing.T) {
		cases := []struct {
			name   string
			ledger string // "" = no ledger file at all
			files  map[string]string
		}{
			{name: "no ledger at all (a fresh install, or one that never needed the upgrade)"},
			{name: "a done ledger with empty failures and skipped",
				ledger: `{"state":"done","pending":[],"in_flight":null,"failures":{},"skipped":[]}`},
			{name: "a done ledger written before the amendment (no failures or skipped members)",
				ledger: `{"state":"done","pending":[],"in_flight":null}`},
			{name: "an agent that is merely pending or in flight, with no failures entry",
				ledger: `{"state":"in_progress","pending":["mia"],"in_flight":"mia","failures":{},"skipped":[]}`,
				files:  map[string]string{"mia": legacyMiaRecord}},
			{name: "a skipped id whose record file was never there",
				ledger: `{"state":"done","pending":[],"in_flight":null,"failures":{},"skipped":["ghost"]}`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				api, routes := newPermissionsAPI(t)
				for id, literal := range tc.files {
					writeAgentRecordFixture(t, api.homePath, id, literal)
				}
				if tc.ledger != "" {
					writeUpgradeLedgerFixture(t, api.homePath, tc.ledger)
				}
				requireUpgradeUnfinishedAbsent(t, getPermissions(t, api, routes), tc.name)
			})
		}
	})

	t.Run("every PATCH response serves the member too", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		writeAgentRecordFixture(t, api.homePath, "mia", legacyMiaRecord)
		writeAgentRecordFixture(t, api.homePath, "jim", malformedJimRecord)
		writeUpgradeLedgerFixture(t, api.homePath, s131Ledger)

		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		entries, present := upgradeUnfinishedServed(t, w)
		require.True(t, present, "C-2: upgrade_unfinished is served by GET and by every PATCH response")
		require.Equal(t, []map[string]any{
			{"agent_id": "mia", "reason": "write_failed"},
			{"agent_id": "jim", "reason": "unreadable"},
		}, entries)

		// The same request with nothing to list serves no member.
		api2, routes2 := newPermissionsAPI(t)
		requireUpgradeUnfinishedAbsent(t, patchPermissions(t, api2, routes2, `{"skill_policies":{"plan":"deny"}}`),
			"no ledger: the PATCH response carries no member either")
	})

	t.Run("a PATCH body naming upgrade_unfinished gets 400 and stores nothing", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		writeAgentRecordFixture(t, api.homePath, "mia", legacyMiaRecord)
		writeUpgradeLedgerFixture(t, api.homePath, s131Ledger)
		configBefore, err := os.ReadFile(api.configPath())
		require.NoError(t, err)
		ledgerBefore, err := os.ReadFile(filepath.Join(api.homePath, upgradeLedgerRelPath))
		require.NoError(t, err)

		for name, body := range map[string]string{
			"alone, naming an agent the ledger does not hold":       `{"upgrade_unfinished":[{"agent_id":"ghost","reason":"unreadable"}]}`,
			"alone, an empty list":                                  `{"upgrade_unfinished":[]}`,
			"next to a valid change (the whole request is refused)": `{"skill_trust":"deny","upgrade_unfinished":[]}`,
		} {
			w := patchPermissions(t, api, routes, body)
			require.Equal(t, http.StatusBadRequest, w.Code, "%s: the member is read-only and not a setting of the PATCH body (C-2); body: %s", name, w.Body.String())
		}

		configAfter, err := os.ReadFile(api.configPath())
		require.NoError(t, err)
		require.Equal(t, string(configBefore), string(configAfter), "a refused PATCH stores nothing in config.json")
		ledgerAfter, err := os.ReadFile(filepath.Join(api.homePath, upgradeLedgerRelPath))
		require.NoError(t, err)
		require.Equal(t, string(ledgerBefore), string(ledgerAfter), "a refused PATCH never edits the ledger")

		// Trust did not move: the valid member next to upgrade_unfinished was
		// refused with the rest of the body.
		require.Contains(t, getPermissions(t, api, routes).Body.String(), `"skill_trust":"ask"`,
			"the valid member next to upgrade_unfinished was not applied either")
	})

	t.Run("a refused PATCH naming upgrade_unfinished leaves the served list the ledger's own", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		writeAgentRecordFixture(t, api.homePath, "mia", legacyMiaRecord)
		writeAgentRecordFixture(t, api.homePath, "jim", malformedJimRecord)
		writeUpgradeLedgerFixture(t, api.homePath, s131Ledger)

		w := patchPermissions(t, api, routes, `{"upgrade_unfinished":[{"agent_id":"ghost","reason":"unreadable"}]}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "control: the body is refused; body: %s", w.Body.String())

		entries, present := upgradeUnfinishedServed(t, getPermissions(t, api, routes))
		require.True(t, present, "the list is served from the ledger (C-2, S-131)")
		require.Equal(t, []map[string]any{
			{"agent_id": "mia", "reason": "write_failed"},
			{"agent_id": "jim", "reason": "unreadable"},
		}, entries, "the list comes from the ledger, never from a request body (no ghost)")
	})

	// S-131 end to end through the real upgrade: a failed write is recorded,
	// the restart retries only that agent and clears it, the skipped agent
	// stays after done, and deleting its record clears the last entry. The
	// legacy records are literal; the fault is the upgrade's own write hook.
	t.Run("the real upgrade records the failure and the skipped agent; a restart clears only the retried one", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		home := api.homePath
		writeAgentRecordFixture(t, home, "mia", legacyMiaRecord)
		jim := writeAgentRecordFixture(t, home, "jim", malformedJimRecord)

		first := runSkillUpgrade(t, home, func(agent string) error {
			if agent == "mia" {
				return errors.New("open mia.json: read-only file system")
			}
			return nil
		})
		require.Error(t, first, "C-19: a failed agent write makes the run report an Error")
		require.ErrorContains(t, first, "mia", "the Error names the agent")

		entries, present := upgradeUnfinishedServed(t, getPermissions(t, api, routes))
		require.True(t, present, "after the failed run the notice lists the failed and the skipped agent (S-131)")
		require.Equal(t, []map[string]any{
			{"agent_id": "mia", "reason": "write_failed"},
			{"agent_id": "jim", "reason": "unreadable"},
		}, entries)

		// Omnipus restarts with Mia's file writable (no fault this time).
		require.NoError(t, runSkillUpgrade(t, home, nil), "the next start retries only the pending agent and finishes")
		entries, present = upgradeUnfinishedServed(t, getPermissions(t, api, routes))
		require.True(t, present, "Jim is skipped permanently, so the notice survives done")
		require.Equal(t, []map[string]any{{"agent_id": "jim", "reason": "unreadable"}}, entries,
			"a successful write removes Mia's failures entry; only Jim is listed (S-131)")

		require.NoError(t, os.Remove(jim), "the person deletes Jim's record file")
		requireUpgradeUnfinishedAbsent(t, getPermissions(t, api, routes), "after Jim's record file is deleted nothing is left to list (S-131)")
	})
}
