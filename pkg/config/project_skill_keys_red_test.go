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
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
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

// TestProjectSkillKeys_RealLoadAndWritersApplyTheWinnersKey is the native-path
// binding obligation for projectSkillWinnerOf (CHECK round 4, false-green
// table row 5). Two defects of TestProjectSkillKeys are recorded here and not
// edited away. (1) It calls one pure adapter with literals; its leaf "both
// stored values survive the loads unchanged" calls the adapter twice and
// compares the two returns, so it does not observe persistence and passes
// while a load rewrites config.json. (2) The seam is declared in package config
// but bound to module pkg/tools: an internal pkg/config test cannot import
// pkg/tools (pkg/tools imports pkg/config; a probe with go list -test reports
// "import cycle not allowed in test", a pkg/fileutil control does not), so no
// init() in this package can bind it as declared. At test-side reconciliation
// the seam and these leaves move to the package that owns the load-time winner
// decision (or are bound through an export_test setter and an external test
// package) and this leaf becomes a real check: seed the installed and project
// policies in config.json, load through the real Skill load for Mia and Ray,
// read config.json back byte for byte; CHECK then mutates the bound function
// and requires it to fail. Loud BLOCKED, never a skip.
func TestProjectSkillKeys_RealLoadAndWritersApplyTheWinnersKey(t *testing.T) {
	t.Fatal("BLOCKED: the real Skill load (C-11 E1) and the C-21/C-23 writers are " +
		"not shown to apply the key of the skill that actually wins " +
		"ResolveSkillName's precedence, with the stored installed and project " +
		"values read back unchanged from config.json — required by C-19, C-23 and " +
		"S-102; projectSkillWinnerOf is declared in package config but bound to " +
		"module pkg/tools, which an internal pkg/config test cannot import (import " +
		"cycle), so it cannot be bound as declared")
}
