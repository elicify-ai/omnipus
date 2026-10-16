package gateway

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// TestLeakGuard_ChildProcessReportsEveryLocation re-executes this test binary
// in a throwaway directory. The child plants one leak, redirects stderr the
// way a gateway boot does, and exits. The parent requires a nonzero exit and
// a visible guard message. It must not write into the real package directory.
func TestLeakGuard_ChildProcessReportsEveryLocation(t *testing.T) {
	if probe := os.Getenv("OMNIPUS_LEAK_CHILD"); probe != "" {
		leakGuardChild(t, probe)
		return
	}
	for _, probe := range []string{
		"tasks/stderr-probe",
		".milestones_migrated",
		".planning_status_migrated",
		"unreadable-tasks",
		"fail-and-tasks",
	} {
		t.Run(probe, func(t *testing.T) {
			dir := t.TempDir()
			// The unreadable probe denies directory listing. Put it back before
			// the temp dir is deleted (cleanups run last-in, first-out).
			t.Cleanup(func() {
				if err := restoreUnreadableTasks(dir); err != nil {
					t.Errorf("restore tasks access: %v", err)
				}
			})
			cmd := exec.Command(os.Args[0],
				"-test.run=^TestLeakGuard_ChildProcessReportsEveryLocation$",
				"-test.count=1", "-test.v")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "OMNIPUS_LEAK_CHILD="+probe)
			out, err := cmd.CombinedOutput()
			text := string(out)
			if err == nil {
				t.Fatalf("child exited 0; want the package guard to fail\n%s", text)
			}
			if !strings.Contains(text, "gateway TestMain:") {
				t.Fatalf("guard message not visible in child output:\n%s", text)
			}
			switch probe {
			case "unreadable-tasks":
				if !strings.Contains(text, "inspection failed") {
					t.Fatalf("unreadable tasks directory was not reported:\n%s", text)
				}
			case "fail-and-tasks":
				if !strings.Contains(text, "--- FAIL:") {
					t.Fatalf("expected the deliberate test failure in the child output:\n%s", text)
				}
				want := filepath.Join("tasks", "stderr-probe")
				if !strings.Contains(text, want) {
					t.Fatalf("leak path %s missing beside the test failure:\n%s", want, text)
				}
			default:
				want := filepath.FromSlash(probe)
				if !strings.Contains(text, want) {
					t.Fatalf("leak %s missing from the guard message:\n%s", want, text)
				}
			}
		})
	}
}

func leakGuardChild(t *testing.T, probe string) {
	t.Helper()
	panicLog := filepath.Join(t.TempDir(), "panic.log")
	cleanup, err := logger.InitPanic(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	switch probe {
	case "unreadable-tasks":
		if err := plantUnreadableTasks(); err != nil {
			t.Fatal(err)
		}
	case "fail-and-tasks":
		if err := os.MkdirAll("tasks", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("tasks", "stderr-probe"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Error("deliberate failure alongside a package leak")
	default:
		native := filepath.FromSlash(probe)
		if dir := filepath.Dir(native); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(native, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
