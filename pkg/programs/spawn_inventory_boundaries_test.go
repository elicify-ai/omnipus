package programs

// S-106 / CHECK R06: the per-site inventory and the helper-aware discovery the
// first spawn guard left as loud blockers. The two original rows now assert
// the behaviour — a second launch in a listed function is flagged by its own
// count, and the launch of a received command is a site of the receiving
// function — and the rows below are their twins and controls. The limits that
// remain (one function body at a time, no cross-package launchers, no argv
// runners) are listed in spawn_guard_discovery_test.go.

import (
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramSpawnGuard_InventoryBoundaries(t *testing.T) {
	t.Run("new_launch_in_same_listed_function", func(t *testing.T) {
		src := `package agent
import "os/exec"
func NewProcessHook() {
    _ = exec.Command("go", "version").Run()
    _ = exec.Command("gh", "api", "user").Run()
}`
		sites := scanFileForSpawnSites(t, token.NewFileSet(), "pkg/agent/hook_process.go", src)
		require.Len(t, sites, 2, "instrument: both actual launch sites are discovered")
		requireViolationNaming(t, spawnClassify(sites),
			"pkg/agent/hook_process.go::NewProcessHook holds 2 child-start launches but its table row lists 1",
			"a second launch in the listed non-program function (the row exempts the one launch it lists)")
	})
	t.Run("exactly_the_listed_launch_in_the_listed_function_passes", func(t *testing.T) {
		// Control for the count rule: the same function with the one launch its
		// row lists is clean, so the rule cannot be an always-fail.
		src := `package agent
import "os/exec"
func NewProcessHook() {
    _ = exec.Command("go", "version").Run()
}`
		sites := scanFileForSpawnSites(t, token.NewFileSet(), "pkg/agent/hook_process.go", src)
		require.Len(t, sites, 1)
		require.Empty(t, spawnClassify(sites), "the one listed launch of the listed function is classified")
	})
	t.Run("a_listed_function_that_dropped_a_launch_is_flagged", func(t *testing.T) {
		// The other direction: the row lists two launches for runUnconstrained,
		// so a function holding only one leaves the inventory stale.
		src := `package tools
import (
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/programs"
)
func runUnconstrained(set programs.ResolvedPrograms) error {
	cmd := exec.Command("sh", "-c", "true")
	_ = programs.Attach(cmd, set)
	return cmd.Run()
}`
		sites := scanFileForSpawnSites(t, token.NewFileSet(), "pkg/tools/shell.go", src)
		require.Len(t, sites, 1)
		requireViolationNaming(t, spawnClassify(sites),
			"pkg/tools/shell.go::runUnconstrained holds 1 child-start launches but its table row lists 2",
			"a listed function holding fewer launches than its row lists")
	})
	t.Run("helper_constructs_launcher_receives_command", func(t *testing.T) {
		src := `package agent
import "os/exec"
func makeChild() *exec.Cmd { return exec.Command("gh", "api", "user") }
func startChild(cmd *exec.Cmd) error { return cmd.Run() }`
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 1, "the launch of the received command is the one site; the constructor-only helper makeChild holds no launch")
		require.Equal(t, "startChild", sites[0].Func, "the site belongs to the function that starts the received command")
		require.False(t, sites[0].AttachSameCommand, "no Attach exists on the planted helper-split path")
	})
}

func TestProgramSpawnGuard_HelperDiscovery(t *testing.T) {
	// One site per launch of a RECEIVED *exec.Cmd, judged inside the function
	// that starts it; a function that only configures or only constructs a
	// command holds no launch.
	one := func(t *testing.T, src, wantFunc string, wantAttached bool) {
		t.Helper()
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 1)
		require.Equal(t, wantFunc, sites[0].Func)
		require.Equal(t, wantAttached, sites[0].AttachSameCommand,
			"same-command Attach before the launch of %s (line %d)", sites[0].Func, sites[0].Line)
	}
	t.Run("receiving_helper_that_attaches_before_its_launch_is_compliant", func(t *testing.T) {
		one(t, `package programs
import "os/exec"
func startAttached(cmd *exec.Cmd, set ResolvedPrograms) error {
	if err := Attach(cmd, set); err != nil {
		return err
	}
	return cmd.Start()
}`, "startAttached", true)
	})
	t.Run("late_attach_in_the_receiving_helper_blesses_nothing", func(t *testing.T) {
		one(t, `package programs
import "os/exec"
func startThenAttach(cmd *exec.Cmd, set ResolvedPrograms) error {
	err := cmd.Start()
	_ = Attach(cmd, set)
	return err
}`, "startThenAttach", false)
	})
	t.Run("sandbox_sink_launch_of_a_received_command_is_a_site", func(t *testing.T) {
		one(t, `package tools
import (
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)
func startViaSandbox(cmd *exec.Cmd, lim sandbox.Limits) error {
	return sandbox.StartLockedWithPolicy(cmd, lim.KernelPolicy)
}`, "startViaSandbox", false)
	})
	t.Run("aliased_os_exec_parameter_is_recognised", func(t *testing.T) {
		one(t, `package agent
import exe "os/exec"
func startAliased(c *exe.Cmd) error { return c.Run() }`, "startAliased", false)
	})
	t.Run("parameter_defaulted_from_a_constructor_counts_once", func(t *testing.T) {
		// The constructor pass owns this launch; the parameter pass must not
		// count it a second time.
		one(t, `package agent
import "os/exec"
func defaulted(cmd *exec.Cmd) error {
	if cmd == nil {
		cmd = exec.Command("go", "version")
	}
	return cmd.Run()
}`, "defaulted", false)
	})
	t.Run("configuring_or_ignoring_a_command_holds_no_launch", func(t *testing.T) {
		src := `package agent
import (
	"os/exec"

	other "example.com/other"
)
type runner struct{}
func (r *runner) Run() error { return nil }
func configure(cmd *exec.Cmd)            { cmd.Env = nil; cmd.Dir = "." }
func notACommand(cmd *runner) error      { return cmd.Run() }
func foreignCmd(cmd *other.Cmd) error    { return cmd.Run() }
func inspect(cmd *exec.Cmd) string       { return cmd.Path }`
		require.Empty(t, scanOneSpawnSource(t, src),
			"only a launch of a received *exec.Cmd is a site: configuring one, a different Cmd type and a non-command Run are not")
	})
}

func TestProgramSpawnGuard_LauncherCallers(t *testing.T) {
	// A caller that hands its constructed command to a launcher helper has
	// launched it; the caller's own same-command Attach decides compliance.
	// startCurrentCheck is the table's launcher method of the account check.
	caller := func(t *testing.T, body string) []spawnSite {
		t.Helper()
		return scanOneSpawnSource(t, "package programs\nimport \"os/exec\"\n"+
			"func (s *Service) plantCaller(set ResolvedPrograms) {\n"+body+"\n}")
	}
	t.Run("attached_command_handed_to_a_launcher_method_is_a_compliant_site", func(t *testing.T) {
		sites := caller(t, "cmd := exec.Command(\"gh\", \"api\", \"user\")\n_ = Attach(cmd, set)\ns.startCurrentCheck(nil, ProgramEntry{}, nil, cmd)")
		require.Len(t, sites, 1)
		require.Equal(t, "plantCaller", sites[0].Func)
		require.True(t, sites[0].AttachSameCommand, "the hand-over follows the same-command Attach")
	})
	t.Run("unattached_command_handed_to_a_launcher_method_is_a_violation_site", func(t *testing.T) {
		sites := caller(t, "cmd := exec.Command(\"gh\", \"api\", \"user\")\ns.startCurrentCheck(nil, ProgramEntry{}, nil, cmd)")
		require.Len(t, sites, 1)
		require.False(t, sites[0].AttachSameCommand, "no Attach before the hand-over")
	})
	t.Run("attach_after_the_hand_over_blesses_nothing", func(t *testing.T) {
		sites := caller(t, "cmd := exec.Command(\"gh\", \"api\", \"user\")\ns.startCurrentCheck(nil, ProgramEntry{}, nil, cmd)\n_ = Attach(cmd, set)")
		require.Len(t, sites, 1)
		require.False(t, sites[0].AttachSameCommand, "an Attach after the launcher call blesses nothing")
	})
	t.Run("a_call_that_is_not_a_listed_launcher_is_no_launch", func(t *testing.T) {
		sites := caller(t, "cmd := exec.Command(\"gh\", \"api\", \"user\")\ns.configureCheck(cmd)")
		require.Empty(t, sites, "handing a command to a function the table does not list as a launcher starts nothing")
	})
}

func TestProgramSpawnGuard_TableRowsAreLive(t *testing.T) {
	t.Run("a_row_whose_launches_are_not_discovered_is_flagged", func(t *testing.T) {
		// The state before launcher discovery existed: the scan finds nothing in
		// check.go, so the mandatory runCheck row (and its launcher) matches no
		// site and could never fail. The stale-row rule must say so.
		root := repoRootForSpawnGuard(t)
		all := spawnScan(t, root, nil)
		require.Empty(t, spawnStaleRows(all), "control: the full tree matches every table row, so the rule is not an always-fail")
		var rest []spawnSite
		for _, s := range all {
			if s.File != "pkg/programs/check.go" {
				rest = append(rest, s)
			}
		}
		stale := spawnStaleRows(rest)
		requireViolationNaming(t, stale,
			"table row pkg/programs/check.go::runCheck (mandatory-s1) matches no discovered launch",
			"the mandatory account-check row with no discovered launch")
		requireViolationNaming(t, stale,
			"table row pkg/programs/check.go::startCurrentCheck (launcher) matches no discovered launch",
			"the launcher row with no discovered launch")
	})
	t.Run("the_account_check_row_fails_when_it_hands_over_an_unattached_command", func(t *testing.T) {
		// The row that was vacuous is now one a site can fail: runCheck hands its
		// command to the launcher without an Attach, and the mandatory rule says so.
		src := `package programs
import "os/exec"
func (s *Service) runCheck() {
	cmd := exec.Command("gh", "api", "user")
	s.startCurrentCheck(nil, ProgramEntry{}, nil, cmd)
}`
		sites := scanFileForSpawnSites(t, token.NewFileSet(), "pkg/programs/check.go", src)
		require.Len(t, sites, 1)
		requireViolationNaming(t, spawnClassify(sites),
			"pkg/programs/check.go:5 (runCheck) does not attach program variables through a same-command programs.Attach before the launch",
			"the account check handing an unattached command to its launcher")
	})
	t.Run("the_account_check_row_passes_when_it_attaches_before_the_hand_over", func(t *testing.T) {
		// Control for the previous row: the same function with the Attach.
		src := `package programs
import "os/exec"
func (s *Service) runCheck(set ResolvedPrograms) {
	cmd := exec.Command("gh", "api", "user")
	_ = Attach(cmd, set)
	s.startCurrentCheck(nil, ProgramEntry{}, nil, cmd)
}`
		sites := scanFileForSpawnSites(t, token.NewFileSet(), "pkg/programs/check.go", src)
		require.Len(t, sites, 1)
		require.Empty(t, spawnClassify(sites), "attach, then hand over: compliant, one launch, as the row lists")
	})
}
