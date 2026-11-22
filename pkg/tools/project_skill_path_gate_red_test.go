//go:build goolm && stdjson

package tools

// Order 50 — TestProjectSkillPathGate (S-111; Dataset D14 rows 1–11; C-25).
// Oracle: skills-tools-redesign-spec.md —
//   C-25: a project-skill folder is gated for the acting agent by the
//        outcome of key `project:<W>/<slug>`: `allowed` → open as today;
//        `ask_pending` → refused with "`<slug>` is a project skill this
//        agent may not use yet. Approve it when it is loaded, or change its
//        permission in Skills & Tools."; `deny` → refused naming the
//        setting, as for installed skills.
//   The gate covers reading (every path-taking tool), grep and search,
//   directory listings, and running a helper from the folder; it runs
//   before the own-tree exception and every broader grant; one answer for
//   case/symlink spellings (path identity); an outer mount's skill folders
//   stay gated when an inner mount is reached; the editor exception allows
//   read only — never a load, never a helper run; changes apply from the
//   next turn.
//   S-111's positive control: reading the ordinary file W1/README.md works
//   in every row.
//
// Harness: the fixture files are written for real (a mount root with
// `.claude/skills/notes/SKILL.md`, `helper.sh` and `README.md`); the action
// itself runs through projectSkillPathAction below — GREEN wires it to the
// real action sites (the path-taking tools, grep, fspolicy.VisibleDirectoryEntries,
// loadableSkillReadOrRun) with the gate consulted as C-25 places it. Nil
// keeps the RED run loud (BLOCKED per row), never a skip.
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const gateAskPendingText = "notes is a project skill this agent may not use yet. Approve it when it is loaded, or change its permission in Skills & Tools."

// seedGateFixture writes the D14/S-111 fixture: mount root W1 with the
// project skill folder, its helper and an ordinary file.
func seedGateFixture(t *testing.T, home string) (mountRoot string) {
	t.Helper()
	mountRoot = filepath.Join(home, "W1")
	skillDir := filepath.Join(mountRoot, ".claude", "skills", "notes")
	require.NoError(t, os.MkdirAll(skillDir, 0o700))
	write := func(rel, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(mountRoot, rel), []byte(body), 0o600))
	}
	write(filepath.Join(".claude", "skills", "notes", "SKILL.md"),
		"---\nname: notes\ndescription: Project notes.\n---\n\nGATE-SENTINEL notes instructions.\n")
	write(filepath.Join(".claude", "skills", "notes", "helper.sh"), "#!/bin/sh\necho helper\n")
	write("README.md", "An ordinary file.\n")
	return mountRoot
}

type pathGateRow struct {
	name        string
	outcome     string // the skill's outcome for Mia: "deny", "ask_pending", "allowed"
	action      string // "read", "grep", "list", "run-helper", "alias-read", "workdir-read", "nested-read"
	editSkill   string // "" (Deny) or "allow" — the editor exception (row 10)
	turnPhase   string // "this" or "next" (row 11)
	expectOK    bool   // whether the action must succeed
	assertRef   string // "" or the exact refusal text the result must carry
	assertGrep  string // for grep: a marker the result must NOT contain
	assertList  string // for list: a name the result must NOT list
	assertClean string // for the editor exception: a marker that must never load
}

func TestProjectSkillPathGate(t *testing.T) {
	rows := []pathGateRow{
		{name: "D14#1: read_file of a denied skill's SKILL.md is refused naming the setting", outcome: "deny", action: "read", expectOK: false, assertRef: "Deny"},
		{name: "D14#2: read of an ask_pending skill is refused with the C-25 text", outcome: "ask_pending", action: "read", expectOK: false, assertRef: gateAskPendingText},
		{name: "D14#3: read of an allowed skill works", outcome: "allowed", action: "read", expectOK: true},
		{name: "D14#4: grep over W1 with the skill denied contributes no line from the skill folder", outcome: "deny", action: "grep", assertGrep: "GATE-SENTINEL"},
		{name: "D14#5: listing .claude/skills with the skill denied does not list notes", outcome: "deny", action: "list", assertList: "notes"},
		{name: "D14#6: running the skill's helper.sh is refused when denied", outcome: "deny", action: "run-helper", expectOK: false},
		{name: "D14#7: a different case or symlink spelling of the folder gets the same answer — refused", outcome: "deny", action: "alias-read", expectOK: false},
		{name: "D14#8: with the work dir W1 itself, the gate still refuses — it runs before the own-tree exception", outcome: "deny", action: "workdir-read", expectOK: false},
		{name: "D14#9: an inner mount's own gated skill folder is refused through the outer mount", outcome: "deny", action: "nested-read", expectOK: false},
		{
			name:    "D14#10: the editor exception (edit_skill Allow) lets the read work, the helper is still refused and nothing loads",
			outcome: "deny", action: "read", editSkill: "allow", expectOK: true, assertClean: "GATE-SENTINEL",
		},
		{name: "D14#11: a permission change applies from the next turn — refused this turn, works next", outcome: "deny", action: "read", turnPhase: "next", expectOK: true},
	}
	require.Len(t, rows, 11, "D14: the fixture must carry rows 1-11 of the dataset")

	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			if projectSkillPathAction == nil {
				t.Fatal("BLOCKED: the C-25 project-skill path gate is not implemented — no action " +
					"site consults the gated project-skill folders resolveTurnSkillAuthority " +
					"computes; required by S-111 and Dataset D14 rows 1-11")
			}
			home := t.TempDir()
			mountRoot := seedGateFixture(t, home)

			res := projectSkillPathAction(t, home, mountRoot, row.outcome, row.action, row.editSkill, row.turnPhase)
			if row.expectOK {
				require.True(t, res.OK, "D14: the action must succeed, got refusal: %s", res.Refusal)
			} else {
				require.False(t, res.OK, "D14: the action must be refused, output: %s", res.Output)
			}
			if row.assertRef != "" {
				require.Contains(t, res.Refusal, row.assertRef,
					"D14: the refusal carries the expected text, got: %s", res.Refusal)
			}
			if row.assertGrep != "" {
				require.NotContains(t, res.Output, row.assertGrep,
					"D14: the result must not carry the gated skill's content")
			}
			if row.assertList != "" {
				require.NotContains(t, res.Output, row.assertList,
					"D14: the listing must not carry the gated skill folder")
			}
			if row.assertClean != "" {
				require.NotContains(t, res.Output, row.assertClean,
					"D14#10: the editor exception reads for editing but never loads the skill")
			}

			// S-111's positive control: the ordinary file reads in every row.
			ctrl := projectSkillPathAction(t, home, mountRoot, row.outcome, "read-control", row.editSkill, row.turnPhase)
			require.True(t, ctrl.OK, "S-111: reading W1/README.md must work in every row, got: %s", ctrl.Refusal)
		})
	}
}

// gateActionResult is one gated action's observable outcome.
type gateActionResult struct {
	OK      bool
	Output  string // the action's output (grep result, listing, file content)
	Refusal string // the refusal text when OK is false
}

// projectSkillPathAction performs one D14 action for Mia against the
// fixture at mountRoot, with the skill's outcome and the editor exception
// as given; turnPhase "next" evaluates after a permission change applied.
// GREEN wires it to the real action sites; nil keeps the RED run loud
// (BLOCKED), never a skip.
var projectSkillPathAction func(t testing.TB, home, mountRoot, outcome, action, editSkill, turnPhase string) gateActionResult
