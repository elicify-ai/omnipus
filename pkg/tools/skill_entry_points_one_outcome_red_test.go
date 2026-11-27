//go:build goolm && stdjson

package tools

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), Part L.
// TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome, the pkg/tools
// half: the two entry points of C-11 that live in this package, E6 (file tools
// and the kernel grant) and E7 (bash helper execution). E1–E5, E8 and E9 are
// entry points of pkg/agent and are the same-named test there
// (skill_entry_points_one_outcome_red_test.go).
//
// Oracle: skills-tools-redesign-spec.md C-11 ("Every entry point below consumes
// that outcome; none computes its own") with its E6 and E7 rows, FR-066, US-2
// AS9 ("refused as for an ungranted skill"), and Dataset D1 for the outcome of
// each stored combination (d1ResolutionTable, the table the resolver test
// drives). C-12 says which approval settles which open check, and C-5 what
// "verified" means (a built-in skill, or an install checked against a registry
// hash with no local changes since; a hand-copied folder is unverified).
//
// What is observed. The test never calls the resolver. For each Dataset D1 row
// it writes the row's stored state where the product stores it (the global skill
// value, trust and God Mode in config.json; the agent's own value in the agent's
// record; the grant; the skill folder), then asks the REAL entry point — the app
// guard's resolveTurnSkillAuthority, the kernel grant's turnSkillFolders, and the
// bash path guard's guardCommand — what it does with the skill's folder, and
// compares that with the outcome the dataset states. An entry point that reads
// only the grant (the landing base), or computes its own answer, disagrees with
// the dataset on at least one row; one that reads the one decision cannot. The
// rows are chosen so that each stored layer decides at least one row (global,
// agent, God Mode, trust, verified), so no single wrong answer to a layer passes.
//
// Row D1#13 (no entry on either side) is the resolver's own tripwire: the C-2
// self-heal fills every skill's global value before any entry point runs, so the
// state cannot be reached through stored data and is covered in
// TestSkillPolicyResolution_Table only.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// settledByAllowance states, for each Dataset D1 row whose outcome is
// ask_pending, whether a running timed allowance of the agent for the skill
// settles it. C-12: the permission check (an ordinary Ask) is settled by "a
// running timed allowance for this agent and this skill (C-26)"; the trust check
// is "never settled by Auto-approve, God Mode, a timed allowance, /name or
// Approve Once".
//
//	D1#3  permission Ask, verified skill        -> settled
//	D1#10 permission Ask, verified, God Mode on -> settled (the agent's own Ask survives God Mode)
//	D1#6  trust Ask open (unverified skill)     -> not settled (only a trust approval does)
//	D1#9  trust Ask open under God Mode         -> not settled (the trust floor survives God Mode)
var settledByAllowance = map[string]bool{"D1#3": true, "D1#10": true, "D1#6": false, "D1#9": false}

// d1RowID is the dataset row a table entry transcribes ("D1#7").
func d1RowID(row skillOutcomeInput) string { return strings.SplitN(row.name, " ", 2)[0] }

// entryPointProbeRows are the Dataset D1 rows an entry point can be probed with
// through real stored state: all but D1#13 (see the file header).
func entryPointProbeRows() []skillOutcomeInput {
	var rows []skillOutcomeInput
	for _, row := range d1ResolutionTable {
		if d1RowID(row) != "D1#13" {
			rows = append(rows, row)
		}
	}
	return rows
}

// seedProbeHome stores one Dataset D1 row. The skill is the built-in
// deep-research when the row's skill is verified (C-5: built-in is verified with
// no install record) and the hand-copied notes-helper, which S-29 itself uses as
// its unverified example, when it is not.
func seedProbeHome(t *testing.T, agentID string, row skillOutcomeInput) authorityHome {
	t.Helper()
	slug := "notes-helper"
	if row.verified {
		slug = "deep-research"
	}
	h := seedAuthorityHomeWith(t, authoritySeed{
		agentID:    agentID,
		slug:       slug,
		global:     row.global,
		trust:      row.trust,
		godMode:    row.godMode,
		notGranted: !row.granted,
	})
	if row.agentValue != "" {
		h.setAgentValue(t, row.agentValue)
	}
	return h
}

// writeHelperScript gives the skill folder the helper script a skill ships.
func (h authorityHome) writeHelperScript(t *testing.T) {
	t.Helper()
	dir := filepath.Join(h.folder, "scripts")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

// helperForms are the two ways bash uses a skill's own files (UAT-1 F-8): it runs
// a helper and it reads the instructions.
func (h authorityHome) helperForms() []string {
	return []string{"sh " + h.folder + "/scripts/x.sh", "cat " + h.folder + "/SKILL.md"}
}

// bashGuardRefusal is what the bash path guard says to command for this agent's
// turn: the empty string means the command is admitted. The home, the agent and
// the working directory are the real ones the guard reads; nothing is run.
func (h authorityHome) bashGuardRefusal(t *testing.T, restrict bool, command string) string {
	t.Helper()
	t.Setenv(config.EnvHome, h.home)
	agentHome := filepath.Join(h.home, "agents", h.agentID)
	require.NoError(t, os.MkdirAll(agentHome, 0o755))
	tool, err := NewExecTool(agentHome, restrict)
	require.NoError(t, err)
	return tool.guardCommand(WithAgentID(context.Background(), h.agentID), command, agentHome)
}

// requireFolderState asserts the skill's folder is (or is not) in the app guard
// set and in the kernel grant list.
func requireFolderState(t *testing.T, h authorityHome, loadable bool, why string) {
	t.Helper()
	if loadable {
		h.requireLoadable(t, why)
		return
	}
	h.requireNotLoadable(t, why)
}

// requireHelperState asserts the bash guard admits (or refuses) running a helper
// of the skill and reading its instructions, under both restrict settings (the
// path leg is unconditional: God Mode and restrict=false never skip it).
func requireHelperState(t *testing.T, h authorityHome, usable bool, why string) {
	t.Helper()
	h.writeHelperScript(t)
	for _, restrict := range []bool{true, false} {
		for _, command := range h.helperForms() {
			msg := h.bashGuardRefusal(t, restrict, command)
			if usable {
				require.Empty(t, msg, "%s: %q is admitted (restrict=%v)", why, command, restrict)
				continue
			}
			require.NotEmpty(t, msg, "%s: %q is refused (restrict=%v)", why, command, restrict)
		}
	}
}

// TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome (pkg/tools half:
// E6 and E7).
//
// Traces to: BDD S-85, S-86, S-87, US-2 AS9, ST-19; spec C-11 (E6, E7), C-12,
// FR-066; Dataset D1.
func TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome(t *testing.T) {
	const agentID = "agent-mia"

	// The instrument: a table with no loadable row, no refused row or no Ask row
	// could not tell "reads the one outcome" from "refuses everything" or "admits
	// everything".
	counts := map[string]int{}
	for _, row := range entryPointProbeRows() {
		counts[row.wantOutcome]++
	}
	for _, outcome := range []string{"allow", "ask_pending", "deny"} {
		require.NotZero(t, counts[outcome], "the probe rows must include %s rows, or the entry points could pass by refusing (or admitting) everything", outcome)
	}

	t.Run("E6 file tools and the kernel grant: a skill's folder is loadable exactly when its outcome is allowed", func(t *testing.T) {
		for _, row := range entryPointProbeRows() {
			t.Run(row.name, func(t *testing.T) {
				h := seedProbeHome(t, agentID, row)
				require.False(t, h.appGuard().readAll, "the agent has no edit_skill, so the editor read exception cannot admit the folder")

				requireFolderState(t, h, row.wantOutcome == "allow",
					"E6 (spec "+row.name+"): the folder is in the app guard set and the kernel grant list exactly when the outcome is allowed")

				if row.wantOutcome == "ask_pending" {
					settled, stated := settledByAllowance[d1RowID(row)]
					require.True(t, stated, "%s is an ask_pending row: the test must state whether a timed allowance settles it (C-12)", d1RowID(row))
					allowSkillForAgent(t, agentID, h.slug)
					requireFolderState(t, h, settled,
						"E6 after a 1-hour allowance for this agent (C-12, C-26): the folder opens only where the open check is the permission part")
				}
			})
		}
	})

	t.Run("E7 helper execution: bash runs and reads a skill's files exactly when its folder is loadable", func(t *testing.T) {
		for _, row := range entryPointProbeRows() {
			t.Run(row.name, func(t *testing.T) {
				h := seedProbeHome(t, agentID, row)
				requireHelperState(t, h, row.wantOutcome == "allow",
					"E7 (spec "+row.name+"): the helper set is the E6 set, so bash follows the same outcome")

				if row.wantOutcome == "ask_pending" {
					settled, stated := settledByAllowance[d1RowID(row)]
					require.True(t, stated, "%s is an ask_pending row: the test must state whether a timed allowance settles it (C-12)", d1RowID(row))
					allowSkillForAgent(t, agentID, h.slug)
					requireHelperState(t, h, settled,
						"E7 after a 1-hour allowance for this agent (C-12, C-26): bash follows the same answer as the file tools")
				}
			})
		}
	})

	t.Run("E6/E7 editor exception: read-all never adds a denied skill's folder to the loadable set or the helper set", func(t *testing.T) {
		// C-11 "Editor exception, kept separate": an agent allowed to edit skills
		// can read every skill folder for authoring, but the read "never adds a
		// folder to the E7 helper set"; the skill's own Deny stops it being used.
		h := seedAuthorityHomeWith(t, authoritySeed{agentID: "agent-omnipus", slug: "notes-helper", global: "allow", editor: true})
		h.setAgentValue(t, "deny")
		require.True(t, h.appGuard().readAll, "the editor keeps the read-all authority")
		requireFolderState(t, h, false, "the editor exception does not put a denied skill's folder in the loadable set (app guard and kernel grant)")
		requireHelperState(t, h, false, "the editor exception does not let bash run or read a denied skill's folder as a helper")
	})
}
