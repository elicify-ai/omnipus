//go:build !windows

package gitevidence

// H55 v2 (#1960 S1), finding F-5 of the S1 delta CHECK
// (/workspace/logs/1960/check-s1-delta/check-report.md) with the behavioural oracle of
// the architect's ruling (/workspace/logs/1960/spec-s1/ruling-check-delta.md, item 3):
// a receive-pack on the evidence repository must never run a program chosen by global,
// system or GIT_CONFIG_GLOBAL config. The row states that behaviour. It compares no
// setting's value: which local value achieves it is the ruling's mechanism question
// (it accepts a local no-op pin and rejects an empty one), and a wrong value that lets
// the global program run fails this row all the same.
//
// The fixture is the ruling's. A scratch evidence repository opened by Open gets an
// objects/info/alternates entry that points at a second scratch repository (written
// directly, as fixture setup). A global config file (GIT_CONFIG_GLOBAL) sets
// core.alternateRefsCommand to a script that appends to a marker file. A clone pushes
// a new ref to the evidence repository. Expected: the marker does not exist and the
// push outcome is the one the receive pins allow for a new ref (received, ref created,
// HEAD unchanged; the same outcome the F6 row expects). The control is the same
// fixture without the pin, an ordinary git init repository: it must write the marker,
// which shows the row can see the failure. Linux and macOS only: the script is POSIX
// sh, and native Windows is the ruling's known platform gap.
import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// h55v2Quote single-quotes a path for a POSIX shell command line: git runs
// core.alternateRefsCommand through the shell. A path with a single quote cannot be
// quoted this way, which is a fixture failure, never a skipped row.
func h55v2Quote(t *testing.T, path string) string {
	t.Helper()
	if strings.ContainsRune(path, '\'') {
		t.Fatalf("instrument: the scratch path %q holds a single quote and cannot be used in a shell command line", path)
	}
	return "'" + path + "'"
}

func TestGitEvidence_H55V2_CD3_GlobalAlternateRefsCommandDoesNotRunInTheEvidenceRepository(t *testing.T) {
	git := h55v2Git(t)
	repo, dir := newTestRepo(t)
	writeFile(t, dir, "h55v2-evidence.txt", "H55 v2 evidence\n")
	res, err := repo.Commit(BoundaryTask, CommitMeta{TaskID: "h55v2-cd3", AgentID: "dev"}, []string{"h55v2-evidence.txt"})
	if err != nil || res.Skipped {
		t.Fatalf("fixture: engine Commit failed or was skipped (err=%v)", err)
	}
	head := res.Hash

	home, scratch := t.TempDir(), t.TempDir()
	marker := filepath.Join(scratch, "alternate-refs-ran")
	script := filepath.Join(scratch, "alternate-refs.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$1\" >> "+h55v2Quote(t, marker)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	globalCfg := filepath.Join(scratch, "global.cfg")
	if out, code := h55v2Run(t, git, "", home, nil, "config", "--file", globalCfg, "core.alternateRefsCommand", h55v2Quote(t, script)); code != 0 {
		t.Fatalf("fixture: the global config file: exit %d: %s", code, out)
	}
	globalEnv := []string{"GIT_CONFIG_GLOBAL=" + globalCfg}
	if out, code := h55v2Run(t, git, "", home, globalEnv, "config", "--global", "--get", "core.alternateRefsCommand"); code != 0 || !strings.Contains(out, script) {
		t.Fatalf("instrument: git must read the global core.alternateRefsCommand from GIT_CONFIG_GLOBAL and name the script %q, got %q (exit %d)", script, out, code)
	}

	// The second scratch repository, and the alternates entry of a repository's git
	// directory that points at its objects. git must report it as an alternate, or the
	// fixture is not the ruling's.
	alt := filepath.Join(scratch, "alternate.git")
	if out, code := h55v2Run(t, git, "", home, nil, "init", "-q", "--bare", alt); code != 0 {
		t.Fatalf("fixture: the alternate repository: exit %d: %s", code, out)
	}
	addAlternate := func(gitDir string) {
		t.Helper()
		info := filepath.Join(gitDir, "objects", "info")
		if err := os.MkdirAll(info, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(filepath.Join(alt, "objects")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, code := h55v2Run(t, git, "", home, nil, "--git-dir="+gitDir, "count-objects", "-v")
		if code != 0 || !strings.Contains(out, "alternate: ") || !strings.Contains(out, "alternate.git/objects") {
			t.Fatalf("instrument: git must report the alternates entry of %s as an alternate: exit %d: %s", gitDir, code, out)
		}
	}

	sender := filepath.Join(t.TempDir(), "sender")
	if out, code := h55v2Run(t, git, "", home, nil, "clone", "-q", dir, sender); code != 0 {
		t.Fatalf("fixture: clone of the evidence repository: exit %d: %s", code, out)
	}
	push := func(target string) (string, int) {
		return h55v2Run(t, git, sender, home, globalEnv, "push", target, "HEAD:refs/heads/x")
	}

	// Control: the global program runs when an ordinary repository with an alternate
	// receives the same push under the same global config.
	control := filepath.Join(scratch, "control")
	if out, code := h55v2Run(t, git, "", home, nil, "init", "-q", control); code != 0 {
		t.Fatalf("fixture: the control repository: exit %d: %s", code, out)
	}
	addAlternate(filepath.Join(control, ".git"))
	if out, code := push(control); code != 0 {
		t.Fatalf("instrument: the control push of a new ref must be received: exit %d: %s", code, out)
	}
	ran, readErr := os.ReadFile(marker)
	if readErr != nil || !strings.Contains(string(ran), "alternate.git") {
		t.Fatalf("instrument: the global core.alternateRefsCommand must run in an ordinary repository that has an alternate, or the absence of its marker below proves nothing (marker=%q err=%v)", ran, readErr)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("fixture: the control's marker could not be removed: %v", err)
	}

	// The evidence repository: no program runs, the push is still received.
	addAlternate(filepath.Join(dir, ".git"))
	if out, code := push(dir); code != 0 {
		t.Fatalf("instrument: the push of a new ref into the evidence repository must be received (the outcome follows the receive pins), or no program could have run: exit %d: %s", code, out)
	}
	if out, code := h55v2Run(t, git, dir, home, nil, "rev-parse", "--verify", "-q", "refs/heads/x"); code != 0 || out != head {
		t.Fatalf("instrument: refs/heads/x must exist in the evidence repository at %s after the push, got %q (exit %d)", head, out, code)
	}
	body, readErr := os.ReadFile(marker)
	switch {
	case readErr == nil:
		t.Errorf("ruling-check-delta item 3: the program named by the global core.alternateRefsCommand ran in the evidence repository (it wrote the marker):\n%s", body)
	case !errors.Is(readErr, os.ErrNotExist):
		t.Fatalf("instrument: the marker could not be checked: %v", readErr)
	}
	if out, code := h55v2Run(t, git, dir, home, nil, "rev-parse", "HEAD"); code != 0 || out != head {
		t.Errorf("FR-152: the evidence HEAD must be unchanged at %s, got %q (exit %d)", head, out, code)
	}
}
