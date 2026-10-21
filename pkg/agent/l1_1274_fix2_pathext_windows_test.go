//go:build windows

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Chief R02: the real create path must accept dotted/undotted PATHEXT
// equivalently, while still refusing outside extensions. Codex represents the
// shared validator; the original standard-list controls cover all three CLIs.
func TestL11274WindowsCreateNormalizesPATHEXT(t *testing.T) {
	for _, setting := range []struct{ name, value string }{
		{"undotted-upper", "EXE;CMD"},
		{"undotted-lower", "exe;cmd"},
		{"mixed-dotted-undotted", ".EXE;CMD"},
		{"mixed-undotted-dotted", "EXE;.CMD"},
		{"mixed-case", "eXe;cMd"},
		{"empty-segment", "EXE;;.CMD"},
		{"leading-trailing-empty", ";EXE;;CMD;"}} {
		t.Run(setting.name, func(t *testing.T) {
			for _, extension := range []string{".cmd", ".CMD", ".exe", ".EXE", ".txt"} {
				t.Run(extension, func(t *testing.T) {
					l11274WindowsCreateWithPATHEXT(t, setting.value, false, extension, extension != ".txt")
				})
			}
		})
	}
}

// Chief R03: operator PATHEXT overrides the standard list. Unset/blank uses
// exactly .COM;.EXE;.BAT;.CMD (the code's defaultPathextExtensions and the
// independent Go 1.26.9 pathExt default), not every existing extension.
func TestL11274WindowsCreateHonorsCustomAndDefaultPATHEXT(t *testing.T) {
	for _, setting := range []struct {
		name, value string
		unset       bool
		allowed     []string
		denied      []string
	}{
		{"custom-list", ".TESTCLI", false, []string{".testcli", ".TESTCLI"}, []string{".cmd", ".exe", ".txt"}},
		{"unset-default", "", true, []string{".COM", ".EXE", ".BAT", ".CMD", ".com", ".exe", ".bat", ".cmd"}, []string{".txt", ".testcli"}},
		{"empty-default", "", false, []string{".COM", ".EXE", ".BAT", ".CMD"}, []string{".txt", ".testcli"}},
		{"blank-default", " \t ", false, []string{".COM", ".EXE", ".BAT", ".CMD"}, []string{".txt", ".testcli"}},
	} {
		t.Run(setting.name, func(t *testing.T) {
			for _, group := range []struct {
				name       string
				extensions []string
				allow      bool
			}{{"allow", setting.allowed, true}, {"deny", setting.denied, false}} {
				for _, extension := range group.extensions {
					t.Run(group.name+extension, func(t *testing.T) {
						l11274WindowsCreateWithPATHEXT(t, setting.value, setting.unset, extension, group.allow)
					})
				}
			}
		})
	}
}

func l11274WindowsCreateWithPATHEXT(t *testing.T, value string, unset bool, extension string, allow bool) {
	t.Helper()
	f := newL11274Fixture(t, "", false)
	t.Setenv("PATHEXT", value)
	if unset {
		if err := os.Unsetenv("PATHEXT"); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(f.home, "worker"+extension)
	// No helper that forces the standard PATHEXT, and no negative LookPath
	// precondition: the product must receive the operator's actual setting.
	if err := os.WriteFile(path, []byte("fake external program fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := l11274Fix1ReadResources(t, f)
	result := f.execute(t, "create_agent", l11274CreateArgs("Created codex", "codex", path))
	if allow {
		l11274AssertCreated(t, f, result, "codex", path, "#6E8BA8")
	} else {
		message := l11274FailureDetails(t, result, "INVALID_INPUT")
		if !strings.Contains(message, "cli_path") || !l11274ReasonNamesPath(message, path) {
			t.Errorf("PATHEXT refusal must identify the exact supplied path %q, got %q", path, message)
		}
		if !strings.Contains(strings.ToLower(message), "extension") || !strings.Contains(message, "PATHEXT") {
			t.Errorf("PATHEXT refusal must explain the extension constraint, got %q", message)
		}
		l11274Fix1AssertNoWrites(t, f, "created-codex", before)
	}
}
