//go:build goolm && stdjson

package config

// Order 49 — TestSkillPermissionsUpgrade_StopAtEveryStep (S-110; Dataset
// D13, all 11 rows; C-19). Oracle: skills-tools-redesign-spec.md —
//   S-110 Given: a pre-release data folder with global `Skill: deny`, Mia's
//        own `Skill: ask`, Jim with no `Skill` value, Mia granted `plan`
//        and mounted on W1 with project skill `notes`.
//   S-110 Then, after the stop and two more starts: the ledger says done,
//        `plan` and `deep-research` are globally Deny, Mia's overrides for
//        `plan` and `project:w1/notes` are Ask, Jim has no skill override,
//        and `Skill` is fixed Allow; the second and third starts write
//        nothing more.
//   D13: "Stop" means the test build's stop seam ends the process right
//        after the named boundary; every row's expected output is the
//        S-110 end state (row 4 is the R2-CRIT-001 case: global skill
//        values still Deny, from the snapshot, not from the fixed
//        `Skill: allow`).
//
// RED: the C-19 upgrade (its ledger, per-agent records, snapshot and
// config marker) does not exist. The seams below are that missing
// machinery; GREEN wires them to the real upgrade:
//   seedPreReleaseFolder — writes the S-110 Given as the legacy data folder.
//   runUpgradeStopAt     — one start of this release on `home`; boundary ""
//                          is a normal complete start, any other value stops
//                          right after that D13 boundary (rows 10 and 11
//                          inject their faults — Jim's read-only record,
//                          Ray's malformed record — during this pass).
//   readUpgradedState    — the state S-110's Then asserts.
//   upgradeStateFingerprint — the bytes of everything the upgrade may write
//                          (config.json, ledger, records, marker), for "the
//                          second and third starts write nothing more".
// The fixture's runner must also keep the ordering S-110 pins: the agent
// loop starts only after the upgrade (C-19), so no turn of Mia or Jim can
// load a skill between the starts — that ordering is GREEN's wiring, not
// asserted by this unit fixture.
//
// Nil seams keep the RED run loud (BLOCKED per row), never a skip.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// upgradedState is what S-110's Then asserts after the last start.
type upgradedState struct {
	LedgerDone          bool
	GlobalSkillPolicies map[string]string // sandbox.skill_policies
	MiaOverrides        map[string]string // Mia's own overrides
	JimOverrides        map[string]string // empty = none
	FixedSkillValue     string            // the legacy `Skill` value after the fix
	Notice              string            // the Skills-tab notice content
}

// assertS110EndState is S-110's Then, literally, with the global skill
// policies as a parameter (D13 row 9's person edit changes only that
// member).
func assertS110EndState(t *testing.T, st upgradedState, global map[string]string) {
	t.Helper()
	require.True(t, st.LedgerDone, "S-110: the ledger says done")
	require.Equal(t, global, st.GlobalSkillPolicies,
		"S-110: the global skill values after the last start")
	require.Equal(t, map[string]string{"plan": "ask", "project:w1/notes": "ask"},
		st.MiaOverrides, "S-110: Mia's overrides for plan and project:w1/notes are Ask")
	require.Empty(t, st.JimOverrides, "S-110: Jim has no skill override")
	require.Equal(t, "allow", st.FixedSkillValue, "S-110: Skill is fixed Allow")
}

// personEditsPlanToAsk is D13 row 9's between: a person edits
// sandbox.skill_policies.plan to ask in the file before the restart. A real
// raw-file edit, never through the loader.
func personEditsPlanToAsk(t *testing.T, home string) {
	t.Helper()
	p := filepath.Join(home, "config.json")
	raw, err := os.ReadFile(p)
	require.NoError(t, err, "the pre-release folder must carry a config.json")
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	sb, _ := m["sandbox"].(map[string]any)
	if sb == nil {
		sb = map[string]any{}
		m["sandbox"] = sb
	}
	sp, _ := sb["skill_policies"].(map[string]any)
	if sp == nil {
		sp = map[string]any{}
		sb["skill_policies"] = sp
	}
	sp["plan"] = "ask"
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, out, 0o600))
}

func TestSkillPermissionsUpgrade_StopAtEveryStep(t *testing.T) {
	rows := []struct {
		name     string
		boundary string
		between  func(t *testing.T, home string)
		endState func(t *testing.T, st upgradedState)
		notice   string // a name the notice must list (D13 rows 10, 11)
	}{
		{name: "D13#1: stopped after the lock, before the snapshot — the snapshot is written on the next start from the live files", boundary: "lock-before-snapshot"},
		{name: "D13#2: stopped after the snapshot, ledger not yet — the existing snapshot is used, never rewritten", boundary: "snapshot"},
		{name: "D13#3: stopped after the ledger was written", boundary: "ledger"},
		{name: "D13#4 R2-CRIT-001: stopped after config.json was written — global skill values still Deny, from the snapshot, not from the fixed Skill: allow", boundary: "config-written"},
		{name: "D13#5: stopped with Mia named in_flight, her record not written", boundary: "in-flight-before-write"},
		{name: "D13#6: stopped after Mia's record was written, ledger not retired — her record is written once more with the same values", boundary: "record-written"},
		{name: "D13#7: stopped with all agents done, ledger not done", boundary: "before-done"},
		{name: "D13#8: stopped after the ledger said done, config marker not written — the marker is rewritten from the ledger", boundary: "marker"},
		{
			name:     "D13#9: row 4, then a person edits sandbox.skill_policies.plan to ask before the restart — plan stays ask, never overwritten",
			boundary: "config-written", between: personEditsPlanToAsk,
			endState: func(t *testing.T, st upgradedState) {
				assertS110EndState(t, st, map[string]string{"plan": "ask", "deep-research": "deny"})
			},
		},
		{
			name:     "D13#10: Jim's record read-only — Jim pending, an Error naming Jim, the next start retries Jim only",
			boundary: "jim-record-readonly", notice: "Jim",
		},
		{
			name:     "D13#11: Ray's record malformed — skipped permanently with a Warn and listed in the notice",
			boundary: "ray-record-malformed", notice: "Ray",
		},
	}
	require.Len(t, rows, 11, "D13: the fixture must carry every row of the dataset")

	ran := 0
	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			if seedPreReleaseFolder == nil || runUpgradeStopAt == nil || readUpgradedState == nil || upgradeStateFingerprint == nil {
				t.Fatal("BLOCKED: the C-19 skill-permissions upgrade (legacy Skill values, ledger, " +
					"per-agent records, snapshot, config marker) is not implemented — required by " +
					"S-110 and Dataset D13 (all 11 stop boundaries)")
			}
			home := t.TempDir()
			seedPreReleaseFolder(t, home)
			// The first start stops right after the row's boundary (rows 10
			// and 11 inject their fault during this pass).
			runUpgradeStopAt(t, home, row.boundary)
			if row.between != nil {
				row.between(t, home)
			}
			// Started again — twice.
			runUpgradeStopAt(t, home, "")
			fp := upgradeStateFingerprint(t, home)
			runUpgradeStopAt(t, home, "")
			require.Equal(t, fp, upgradeStateFingerprint(t, home),
				"S-110: the second and third starts write nothing more")

			st := readUpgradedState(t, home)
			if row.endState != nil {
				row.endState(t, st)
			} else {
				assertS110EndState(t, st, map[string]string{"plan": "deny", "deep-research": "deny"})
			}
			if row.notice != "" {
				require.Contains(t, st.Notice, row.notice,
					"D13: the Skills-tab notice lists the affected agent")
			}
			ran++
		})
	}
	t.Run("D13: every row of the dataset ran — no dimension is fixed by the test itself", func(t *testing.T) {
		if ran != 11 {
			t.Fatalf("D13: only %d of the 11 rows ran — no dimension may be fixed by the test itself", ran)
		}
	})
}

// The seams GREEN wires to the real upgrade. Declared after the table so
// the RED failure above names the missing behaviour first.
var (
	seedPreReleaseFolder    func(t testing.TB, home string)
	runUpgradeStopAt        func(t testing.TB, home, boundary string)
	readUpgradedState       func(t testing.TB, home string) upgradedState
	upgradeStateFingerprint func(t testing.TB, home string) []byte
)
