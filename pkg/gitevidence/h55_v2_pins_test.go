package gitevidence

// H55 v2 (#1960 S1), row R10 of the architect's second ruling
// (/workspace/logs/1960/spec-s1/ruling-H55-v2.md, section (3)): the evidence
// repository keeps four receive settings in its OWN local config -
// core.bare=false, receive.denyCurrentBranch=refuse, receive.denyNonFastForwards=true,
// receive.denyDeletes=true - so that no global setting can switch git's refusal of a
// push off (section (1): local config beats global, system, GIT_CONFIG_GLOBAL and an
// insteadOf target). The row: the four settings are present after Open of a new
// repository, after Open of an existing one where they were changed or removed, and
// after an engine Commit. Section (1) of the ruling also says the pins are
// "re-asserted after every engine Commit"; the changed-behind-the-handle rows below
// read that sentence.
//
// The values are read with installed Git from the file Open maintains, the way
// receive-pack reads them, never through the engine's own pin code. Expected values
// are the ruling's. Each row can fail: the instrument checks prove the reader sees a
// removed or flipped value before Open or Commit is asked to put it back, so a pass
// cannot come from a value that was never lost.
//
// No row here for the shape of any further pin value the rulings leave open. What
// core.alternateRefsCommand does is a behavioural row of its own
// (h55_v2_alternate_refs_test.go, ruling-check-delta.md item 3); F6 (a global hook) is
// in h55_v2_hooks_test.go.
import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// h55v2Pin is one local setting of the ruling's R10. Boolean values are read with
// git's own boolean parsing, so what is compared is what git resolves.
type h55v2Pin struct {
	key, want, flipped string
	boolean            bool
}

var h55v2Pins = []h55v2Pin{
	{"core.bare", "false", "true", true},
	{"receive.denyCurrentBranch", "refuse", "ignore", false},
	{"receive.denyNonFastForwards", "true", "false", true},
	{"receive.denyDeletes", "true", "false", true},
}

func h55v2Git(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("installed Git is required to read the repository's config the way receive-pack reads it: %v", err)
	}
	return path
}

// h55v2Run runs installed Git with an explicit environment (no inherited global or
// system config) and returns its combined output and exit code. An empty home gets a
// fresh empty one.
func h55v2Run(t *testing.T, git, dir, home string, env []string, args ...string) (string, int) {
	t.Helper()
	if home == "" {
		home = t.TempDir()
	}
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}, env...)
	if root := os.Getenv("SystemRoot"); root != "" {
		cmd.Env = append(cmd.Env, "SystemRoot="+root)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out)), 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("git %v could not run: %v", args, err)
	}
	return strings.TrimSpace(string(out)), exit.ExitCode()
}

func h55v2Config(dir string) string { return filepath.Join(dir, ".git", "config") }

// h55v2Read returns the value git resolves for one pin from the repository's own
// config file, and whether it is set at all.
func h55v2Read(t *testing.T, git, dir string, p h55v2Pin) (string, bool) {
	t.Helper()
	args := []string{"config", "--file", h55v2Config(dir)}
	if p.boolean {
		args = append(args, "--type=bool")
	}
	args = append(args, "--get", p.key)
	out, code := h55v2Run(t, git, "", "", nil, args...)
	switch code {
	case 0:
		return out, true
	case 1:
		return "", false
	default:
		t.Fatalf("git %v exited %d: %s", args, code, out)
		return "", false
	}
}

// h55v2Damage changes the repository's own config file with installed Git, outside
// the engine, the way something else on the machine could: each pin is removed or
// flipped. Exit 5 of the removal means the key was not there, which is the state the
// row wants. The reader must then see the damage.
func h55v2Damage(t *testing.T, git, dir string, pins []h55v2Pin, mode string) {
	t.Helper()
	for _, p := range pins {
		args := []string{"config", "--file", h55v2Config(dir)}
		if mode == "removed" {
			args = append(args, "--unset", p.key)
		} else {
			args = append(args, p.key, p.flipped)
		}
		out, code := h55v2Run(t, git, "", "", nil, args...)
		if code != 0 && (mode != "removed" || code != 5) {
			t.Fatalf("fixture: git %v exited %d: %s", args, code, out)
		}
		got, set := h55v2Read(t, git, dir, p)
		switch {
		case mode == "removed" && set:
			t.Fatalf("instrument: %s must read as unset after it was removed, got %q", p.key, got)
		case mode == "flipped" && (!set || got != p.flipped):
			t.Fatalf("instrument: %s must read as %q after it was flipped, got %q (set=%t)", p.key, p.flipped, got, set)
		}
	}
}

// h55v2AssertPins requires all four values, each reported on its own.
func h55v2AssertPins(t *testing.T, git, dir, when string) {
	t.Helper()
	for _, p := range h55v2Pins {
		got, set := h55v2Read(t, git, dir, p)
		switch {
		case !set:
			t.Errorf("ruling-H55-v2 R10 (%s): %s must be set in the evidence repository's own .git/config", when, p.key)
		case got != p.want:
			t.Errorf("ruling-H55-v2 R10 (%s): %s = %q in the evidence repository's own .git/config, want %q", when, p.key, got, p.want)
		}
	}
}

// R10, a new repository: Open writes the four values.
func TestGitEvidence_H55V2_R10_PinsPresentAfterOpenOfANewRepository(t *testing.T) {
	git := h55v2Git(t)
	_, dir := newTestRepo(t)
	h55v2AssertPins(t, git, dir, "after Open of a new repository")
}

// R10, an existing repository whose local config lost or changed a value: Open puts
// it back. Each key alone and all four together, removed and flipped, so a writer
// that restores only when everything is missing, or forgets one key, is caught.
func TestGitEvidence_H55V2_R10_PinsRestoredOnOpenOfAnExistingRepository(t *testing.T) {
	git := h55v2Git(t)
	type damaged struct {
		name string
		pins []h55v2Pin
	}
	cases := make([]damaged, 0, 1+len(h55v2Pins))
	cases = append(cases, damaged{"all_four", h55v2Pins})
	for _, p := range h55v2Pins {
		cases = append(cases, damaged{strings.ReplaceAll(p.key, ".", "_"), []h55v2Pin{p}})
	}
	for _, mode := range []string{"removed", "flipped"} {
		for _, tc := range cases {
			t.Run(mode+"_"+tc.name, func(t *testing.T) {
				_, dir := newTestRepo(t)
				h55v2Damage(t, git, dir, tc.pins, mode)
				if _, err := Open(dir, WithRedactor(noOpRedactor)); err != nil {
					t.Fatalf("Open of the existing repository: %v", err)
				}
				h55v2AssertPins(t, git, dir, fmt.Sprintf("after Open of a repository where %s were %s", tc.name, mode))
			})
		}
	}
}

// R10, after an engine Commit: the commit really happened (so the check is not made
// on a repository the engine never wrote to) and the four values are there, after a
// first and a second commit; and when the values were changed behind the open
// handle's back before a commit, the commit leaves them as the ruling says they are
// re-asserted ("re-asserted after every engine Commit").
func TestGitEvidence_H55V2_R10_PinsPresentAfterAnEngineCommit(t *testing.T) {
	git := h55v2Git(t)
	commit := func(t *testing.T, repo *Repo, dir, name string) {
		t.Helper()
		writeFile(t, dir, name, "H55 v2 evidence boundary\n"+name+"\n")
		res, err := repo.Commit(BoundaryTask, CommitMeta{TaskID: "h55v2-r10", AgentID: "dev"}, []string{name})
		if err != nil {
			t.Fatalf("engine Commit of %s: %v", name, err)
		}
		if res.Skipped || res.Hash == "" {
			t.Fatalf("instrument: the engine Commit of %s must create a commit (skipped=%t reason=%q hash=%q), or the pins were never checked after a commit", name, res.Skipped, res.SkipReason, res.Hash)
		}
	}

	t.Run("intact_through_two_commits", func(t *testing.T) {
		repo, dir := newTestRepo(t)
		commit(t, repo, dir, "h55v2-evidence-1.txt")
		h55v2AssertPins(t, git, dir, "after the first engine Commit")
		commit(t, repo, dir, "h55v2-evidence-2.txt")
		h55v2AssertPins(t, git, dir, "after the second engine Commit")
	})

	for _, mode := range []string{"removed", "flipped"} {
		t.Run("all_four_"+mode+"_behind_the_open_handle", func(t *testing.T) {
			repo, dir := newTestRepo(t)
			h55v2Damage(t, git, dir, h55v2Pins, mode)
			commit(t, repo, dir, "h55v2-evidence-3.txt")
			h55v2AssertPins(t, git, dir, "after an engine Commit on a repository whose four values were "+mode+" after Open")
		})
	}
}
