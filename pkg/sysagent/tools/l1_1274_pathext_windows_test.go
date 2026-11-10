//go:build windows

package systools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Chief round-2 R02/R03 and Go 1.26.9 os/exec::pathExt are the oracle:
// an absent dot is supplied, entries are case-insensitive, empty segments are
// ignored, and the operator's list wins over the four-entry standard fallback.
// These Windows tests can also use the authorized scratch source-transport seam
// with the real helper body unchanged. That execution is component evidence,
// not native Windows or the full create-agent journey.
func TestL11274WindowsPATHEXTNormalizesEntries(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"undotted-upper", "EXE;CMD"},
		{"undotted-lower", "exe;cmd"},
		{"mixed-dotted-undotted", ".EXE;CMD"},
		{"mixed-undotted-dotted", "EXE;.CMD"},
		{"mixed-case", "eXe;cMd"},
		{"empty-segment", "EXE;;.CMD"},
		{"leading-trailing-empty", ";EXE;;CMD;"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATHEXT", tc.value)
			l11274CheckWindowsExtensions(t, []string{".cmd", ".CMD", ".exe", ".EXE"}, []string{".txt", ".bat", ".testcli"})
		})
	}
}

func TestL11274WindowsPATHEXTCustomAndDefaultControls(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		unset       bool
		allowed     []string
		denied      []string
	}{
		{"dotted-upper", ".EXE;.CMD", false, []string{".cmd", ".CMD", ".exe", ".EXE"}, []string{".txt", ".bat", ".testcli"}},
		{"dotted-lower", ".exe;.cmd", false, []string{".cmd", ".CMD", ".exe", ".EXE"}, []string{".txt", ".bat", ".testcli"}},
		{"custom-list", ".TESTCLI", false, []string{".testcli", ".TESTCLI"}, []string{".cmd", ".exe", ".txt"}},
		// Independent exact four-name oracle: chief R03; production
		// defaultPathextExtensions and Go's pathExt each publish this set.
		{"unset-default", "", true, []string{".COM", ".EXE", ".BAT", ".CMD", ".com", ".exe", ".bat", ".cmd"}, []string{".txt", ".testcli"}},
		{"empty-default", "", false, []string{".COM", ".EXE", ".BAT", ".CMD"}, []string{".txt", ".testcli"}},
		{"blank-default", " \t ", false, []string{".COM", ".EXE", ".BAT", ".CMD"}, []string{".txt", ".testcli"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATHEXT", tc.value)
			if tc.unset {
				// Setenv registered restoration of the original value before
				// Unsetenv, so the test does not leak its environment change.
				if err := os.Unsetenv("PATHEXT"); err != nil {
					t.Fatal(err)
				}
			}
			l11274CheckWindowsExtensions(t, tc.allowed, tc.denied)
		})
	}
}

func l11274CheckWindowsExtensions(t *testing.T, allowed, denied []string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	for _, group := range []struct {
		name string
		ext  []string
		want bool
	}{{"allow", allowed, true}, {"deny", denied, false}} {
		for _, extension := range group.ext {
			t.Run(group.name+extension, func(t *testing.T) {
				path := filepath.Join(home, "worker"+extension)
				// A real regular file; this helper's rule concerns Windows
				// executable extensions, not a CLI version/login handshake.
				if err := os.WriteFile(path, []byte("test executable-extension fixture\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				err := currentUserCanExecute(path)
				if got := err == nil; got != group.want {
					t.Errorf("PATHEXT=%q path=%q gotAllow=%v wantAllow=%v error=%v", os.Getenv("PATHEXT"), path, got, group.want, err)
				}
				if !group.want && err != nil {
					message := err.Error()
					if !strings.Contains(message, extension) || !strings.Contains(message, "PATHEXT") {
						t.Errorf("extension refusal lost its specific extension/PATHEXT reason: %q", message)
					}
				}
			})
		}
	}
}
