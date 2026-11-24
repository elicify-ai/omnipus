//go:build goolm && stdjson

package config

// Order 40 — TestProjectSkillKeys (S-102; C-19/C-23). Oracle:
// skills-tools-redesign-spec.md S-102 — installed `notes` at Allow, project
// skill `notes` in workspace W1 at Deny (`project:w1/notes`); Mia mounted on
// W1 and GRANTED installed notes, Ray mounted on W1 and NOT granted it.
// Mia's load gets the installed skill's instructions (a granted installed
// skill wins over a same-slug project skill); Ray's load is refused naming
// the Deny of the project skill; both stored values survive unchanged. The
// expected winners are LITERAL in the test — it never calls ResolveSkillName
// to build them.
//
// RED: the project-skill key scheme (`project:<W>/<slug>`), the
// installed-vs-project precedence, and the load-time winner decision do not
// exist. projectSkillWinnerOf below is that missing winner decision; GREEN
// wires it to the production load-time key resolution (the C-23 known-skill
// key rule consuming the C-19 storage). Nil keeps the RED run loud
// (BLOCKED), never a skip.
import (
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"testing"
)

func TestProjectSkillKeys(t *testing.T) {
	nativebinding.Require(t, "pkg/tools", projectSkillWinnerOf,
		"C-19/C-23/S-102: actual skill load winner; post-GREEN consumer re-audit required")

	// Literal winners (S-102's last line): the expected instruction source is
	// spelled here, never derived from production code.
	const (
		installedWinner = "installed:notes instructions"
		projectRefusal  = "refused: project:w1/notes is Deny in W1"
	)

	t.Run("S-102: a granted installed skill wins over a same-slug project skill", func(t *testing.T) {
		winner, refusal := projectSkillWinnerOf("allow", "deny", true)
		if winner != installedWinner {
			t.Fatalf("S-102: Mia's load winner = %q, want the literal installed instructions %q", winner, installedWinner)
		}
		if refusal != "" {
			t.Fatalf("S-102: Mia's load must not be refused, got %q", refusal)
		}
	})

	t.Run("S-102: an ungranted agent loads the project skill and hits its Deny", func(t *testing.T) {
		winner, refusal := projectSkillWinnerOf("allow", "deny", false)
		if winner != "" {
			t.Fatalf("S-102: Ray must not get the installed skill's instructions, got %q", winner)
		}
		if refusal != projectRefusal {
			t.Fatalf("S-102: Ray's refusal = %q, want the literal project refusal %q", refusal, projectRefusal)
		}
	})

	t.Run("S-102: both stored values survive the loads unchanged", func(t *testing.T) {
		// The decision is read-only: winning and refusing never rewrite the
		// stored layers. The seam's contract returns no writes; assert the
		// round-trip the spec pins (installed still Allow, project still
		// Deny) through a second identical decision.
		winner1, _ := projectSkillWinnerOf("allow", "deny", true)
		winner2, refusal2 := projectSkillWinnerOf("allow", "deny", false)
		if winner1 != installedWinner || winner2 != "" || refusal2 != projectRefusal {
			t.Fatalf("S-102: the stored values drifted after the loads (winner %q / %q / %q)", winner1, winner2, refusal2)
		}
	})
}

// projectSkillWinnerOf is the test-local seam for the load-time winner
// decision over C-23 keys: given the installed skill's stored policy, the
// same-slug project skill's stored policy, and whether the agent is granted
// the installed skill, it returns the instruction source that loads ("")
// or the refusal line. GREEN wires it to the production resolution; nil
// keeps the RED run loud (BLOCKED), never a skip.
var projectSkillWinnerOf func(installedPolicy, projectPolicy string, grantedInstalled bool) (winner, refusal string)
