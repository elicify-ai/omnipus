//go:build linux || darwin

package browser

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRequireBrowserOrFail_DeclaredChrome covers the CI measurement resolver,
// not CDP readiness. POSIX executable shims replace only the process boundary;
// the real helper runs in a child test process so its t.Fatal remains observable.
func TestRequireBrowserOrFail_DeclaredChrome(t *testing.T) {
	if os.Getenv("OMNIPUS_BROWSER_RESOLUTION_CHILD") == "1" {
		fmt.Printf("RESOLVED_CHROME=%s\n", requireBrowserOrFail(t))
		return
	}

	root := t.TempDir()
	chromiumDir := filepath.Join(root, "early-path")
	googleDir := filepath.Join(root, "later-path")
	declaredDir := filepath.Join(root, "declared chrome")
	for _, dir := range []string{chromiumDir, googleDir, declaredDir} {
		require.NoError(t, os.Mkdir(dir, 0o755))
	}
	chromium := filepath.Join(chromiumDir, "chromium-browser")
	google := filepath.Join(googleDir, "google-chrome")
	declared := filepath.Join(declaredDir, "chrome")
	broken := filepath.Join(declaredDir, "broken-chrome")
	for path, body := range map[string]string{
		chromium: "chromium-browser:--version\n",
		google:   "google-chrome:--version\n",
		declared: "declared:--version\n",
		broken:   "broken:--version\n",
	} {
		exit := 0
		if path == broken {
			exit = 1
		}
		writeExecutable(t, path, fmt.Sprintf(
			"#!/bin/sh\n[ \"$1\" = --version ] || exit 2\nprintf %q >> \"$OMNIPUS_BROWSER_RESOLUTION_PROBES\"\nexit %d\n",
			body, exit,
		))
	}

	pin := func(path string) *string { return &path }
	cases := []struct {
		name       string
		pin        *string
		wantPath   string
		wantError  string
		wantProbes string
	}{
		// Characterization of the pre-fix pick requested by the failure brief:
		// without a pin, chromium-browser wins even when google-chrome exists.
		{name: "unconfigured_PATH_pick", wantPath: chromium, wantProbes: "chromium-browser:--version\n"},
		{name: "declared_binary_ignores_hostile_PATH", pin: &declared, wantPath: declared, wantProbes: "declared:--version\n"},
		{name: "empty_pin_fails_without_fallback", pin: pin(""), wantError: "OMNIPUS_BROWSER_TEST_CHROME must be a non-empty absolute path"},
		{name: "relative_pin_fails_without_fallback", pin: pin("google-chrome"), wantError: "OMNIPUS_BROWSER_TEST_CHROME must be a non-empty absolute path"},
		{name: "missing_pin_fails_without_fallback", pin: pin(filepath.Join(declaredDir, "absent-chrome")), wantError: "requireBrowserOrFail: declared Chrome --version failed"},
		{name: "broken_pin_fails_without_fallback", pin: &broken, wantError: "requireBrowserOrFail: declared Chrome --version failed", wantProbes: "broken:--version\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probes := filepath.Join(t.TempDir(), "probes")
			cmd := exec.Command(os.Args[0], "-test.run=^TestRequireBrowserOrFail_DeclaredChrome$", "-test.v")
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if key != "PATH" && key != "OMNIPUS_BROWSER_TEST_CHROME" &&
					key != "OMNIPUS_BROWSER_RESOLUTION_CHILD" && key != "OMNIPUS_BROWSER_RESOLUTION_PROBES" {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env,
				"PATH="+chromiumDir+string(os.PathListSeparator)+googleDir,
				"OMNIPUS_BROWSER_RESOLUTION_CHILD=1",
				"OMNIPUS_BROWSER_RESOLUTION_PROBES="+probes,
			)
			if tc.pin != nil {
				cmd.Env = append(cmd.Env, "OMNIPUS_BROWSER_TEST_CHROME="+*tc.pin)
			}
			output, err := cmd.CombinedOutput()
			if tc.wantError != "" {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr, "declared unusable Chrome must FAIL, not resolve or skip: %s", output)
				require.Equal(t, 1, exitErr.ExitCode(), "t.Fatal must yield test failure")
				require.Contains(t, string(output), tc.wantError)
				require.NotContains(t, string(output), "--- SKIP")
				require.NotContains(t, string(output), "RESOLVED_CHROME=")
			} else {
				require.NoError(t, err, "%s", output)
				var resolved []string
				for _, line := range strings.Split(string(output), "\n") {
					if path, ok := strings.CutPrefix(line, "RESOLVED_CHROME="); ok {
						resolved = append(resolved, path)
					}
				}
				require.Equal(t, []string{tc.wantPath}, resolved, "must return the exact selected executable")
				t.Logf("resolved exact Chrome: %s", tc.wantPath)
			}
			gotProbes, readErr := os.ReadFile(probes)
			if tc.wantProbes == "" {
				require.True(t, errors.Is(readErr, os.ErrNotExist), "invalid pin must not probe PATH or download: %v", readErr)
			} else {
				require.NoError(t, readErr)
				require.Equal(t, tc.wantProbes, string(gotProbes), "only the selected executable may be probed")
			}
		})
	}
}
