package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Oracle: #1274 chief F04 and the published default names. Exercise the real
// resolver used by each driver, then the OS PATH lookup at the process edge.
// Only the temp install is on PATH; no external CLI is executed or authenticated.
func TestL11274EmptyCLIPathSelectsEachDefaultOnIsolatedPATH(t *testing.T) {
	for _, tc := range []struct{ cli, binary string }{
		{"claude-code", "claude"}, {"codex", "codex"}, {"opencode", "opencode"},
	} {
		for _, pathInput := range []struct{ name, value string }{{"empty", ""}, {"whitespace", " \t "}} {
			t.Run(tc.cli+"/"+pathInput.name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("OMNIPUS_HOME", home)
				filename := tc.binary
				content := "#!/bin/sh\nexit 0\n"
				if runtime.GOOS == "windows" {
					filename, content = filename+".cmd", "@exit /b 0\r\n"
					t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
				}
				install := filepath.Join(home, filename)
				if err := os.WriteFile(install, []byte(content), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", home)
				selected := resolveCLIBinary(pathInput.value, DefaultCLIBinary(tc.cli))
				if selected != tc.binary {
					t.Fatalf("empty-path driver selection = %q, want the documented default %q", selected, tc.binary)
				}
				resolved, err := exec.LookPath(selected)
				if err != nil || resolved != install {
					t.Errorf("default binary lookup = %q, err=%v, want the isolated install %q", resolved, err, install)
				}
			})
		}
	}
}

func TestL11274ExplicitCLIPathOverridesDefaultSelection(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "explicit install", "chosen-cli")
			if got := resolveCLIBinary(path, DefaultCLIBinary(cli)); got != path {
				t.Errorf("explicit-path driver selection = %q, want %q (not the default binary)", got, path)
			}
		})
	}
}
