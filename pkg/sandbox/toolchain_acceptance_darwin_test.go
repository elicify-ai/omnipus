//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealToolchainRunsUnderOpenModel is the acceptance test for ADR-060.
//
// Every other test in this package asserts a property of a rendered profile or a
// synthetic path. This one runs the ACTUAL binaries on the host under the ACTUAL
// profile, because the thing being fixed was never "a rule is missing" — it was
// "an agent cannot run node", and only running node answers that.
//
// It is deliberately tolerant about which tools exist (a CI box has no Homebrew)
// and deliberately intolerant about what happens when one does exist: a tool that
// is present and fails is a real failure, not an environment quirk.
//
// The confined half is what makes the open half evidence. Without it, a profile
// that accidentally granted everything would pass just as happily, and so would a
// machine whose tools happen to sit in /usr/bin. Measured on the author's machine
// the tools resolve through fnm, a Homebrew Intel prefix and pyenv shims — none of
// which the confined model can reach without hand-configuration, which is exactly
// the complaint that started this work.
func TestRealToolchainRunsUnderOpenModel(t *testing.T) {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "master.key"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := renderSeatbeltProfile(
		DefaultPolicyForModel(FilesystemModelOpen, home, nil, nil, nil, nil))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, tool := range []string{"node", "npm", "brew", "go", "git", "python3"} {
		path, lookErr := exec.LookPath(tool)
		if lookErr != nil {
			t.Logf("  SKIP %-8s (not installed)", tool)
			continue
		}
		arg := "--version"
		if tool == "go" {
			arg = "version"
		}
		cmd := exec.Command(seatbeltExecPath, "-p", profile, path, arg)
		cmd.Env = append(os.Environ(), "HOME="+os.Getenv("HOME"))
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Errorf("  FAIL %-8s (%s): %v\n%s", tool, path, runErr, out)
			continue
		}
		t.Logf("  OK   %-8s %s -> %s", tool, path, strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	}

	// Falsification: the SAME tools under the confined model, with no
	// allowed_exec_paths configured. If they run here too, the test above
	// proves nothing about the model.
	confined, err := renderSeatbeltProfile(
		DefaultPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil))
	if err != nil {
		t.Fatalf("render confined: %v", err)
	}
	for _, tool := range []string{"node", "npm", "brew"} {
		path, lookErr := exec.LookPath(tool)
		if lookErr != nil {
			continue
		}
		if err := exec.Command(seatbeltExecPath, "-p", confined, path, "--version").Run(); err == nil {
			t.Errorf("  UNEXPECTED %-8s ran under CONFINED with no exec paths configured; "+
				"the open-model result above is then not evidence of anything", tool)
		} else {
			t.Logf("  CONFINED-BLOCKED %-8s (as expected)", tool)
		}
	}

	// And the secret is still unreachable while all of that works.
	if err := exec.Command(seatbeltExecPath, "-p", profile, "/bin/cat",
		filepath.Join(home, "master.key")).Run(); err == nil {
		t.Error("master.key was readable — the toolchain works but the protection does not")
	}
}
