package agent

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 12.
// TestSkillLoad_AskUnattendedRefused: a skill load whose effective value is
// Ask in an unattended chain is skipped with the unattended text and raises
// no card; with Auto-approve on it loads (Dataset D1 rows 10-11).
//
// Oracle: skills-tools-redesign-spec.md S-12 ("the load is refused with
// 'Not run: needs approval, and task and plan runs never ask.' … no
// approval card appears anywhere"), C-12 rows 10-11 (Auto checked before
// the refusal) and FR-017. The transcript text itself already exists for
// task/plan runs (pkg/agent/tool_denial.go::unattendedDenial); the missing
// behaviour is the SKILL LOAD reaching that decision through the C-11
// resolver (its outcome is ask_pending and nothing settles it).
//
// Test-harness note (per the dispatch brief): a scripted fake-model loop
// exists in this package (scriptedToolProvider, eventbus_test.go) and a
// full unattended-run harness would drive the whole task executor; the
// decision under test is the loop boundary's skill-load denial, so this
// test pins it through the seam below and says so. GREEN wires
// skillLoadUnattendedDenialOf to the C-11 consumer at that boundary; nil
// keeps the RED run loud (BLOCKED).

import (
	"testing"
)

// skillLoadUnattendedDenialOf is the test-local seam for the loop's
// skill-load decision in an unattended chain: given the skill's effective
// outcome ("ask_pending" from the C-11 resolver) and whether Auto-approve
// applies, it reports the denial the turn records. denied=false means the
// load runs; transcriptText is the denial's transcript line ("" when the
// load runs). GREEN wires it to the production boundary; nil keeps the RED
// run loud (BLOCKED).
var skillLoadUnattendedDenialOf func(outcome string, autoApprove bool) (denied bool, transcriptText string)

// TestSkillLoad_AskUnattendedRefused (order 12).
// Traces to: BDD S-12; spec C-12 rows 10-11, FR-017; Dataset D1 rows 10-11.
func TestSkillLoad_AskUnattendedRefused(t *testing.T) {
	if skillLoadUnattendedDenialOf == nil {
		t.Fatal("BLOCKED: the unattended skill-load decision (C-11 outcome " +
			"reaching the loop's denial path) is not implemented — required by " +
			"skills-tools-redesign-spec S-12, C-12 rows 10-11 and FR-017")
	}
	const wantText = "Not run: needs approval, and task and plan runs never ask."

	t.Run("D1#10 Ask with Auto off is skipped with the unattended text", func(t *testing.T) {
		denied, text := skillLoadUnattendedDenialOf("ask_pending", false)
		if !denied {
			t.Fatalf("S-12: an Ask skill load in an unattended run must be skipped")
		}
		if text != wantText {
			t.Fatalf("S-12: the refusal carries the exact unattended text; got %q want %q", text, wantText)
		}
	})

	t.Run("D1#11 Auto on loads — Auto is checked before the refusal", func(t *testing.T) {
		denied, _ := skillLoadUnattendedDenialOf("ask_pending", true)
		if denied {
			t.Fatalf("C-12 row 11: Auto-approve settles the Ask before the unattended refusal; the load must run")
		}
	})

	t.Run("an allowed skill loads without a card", func(t *testing.T) {
		denied, _ := skillLoadUnattendedDenialOf("allowed", false)
		if denied {
			t.Fatalf("S-13: an allowed load runs unattended with no card")
		}
	})
}
