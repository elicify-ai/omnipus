//go:build darwin || linux

package agent

import (
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Oracle: chief F01, CHECK's 0601 counterexample, and POSIX current-user
// access. An other-execute bit does not grant the file's owner execution.
// r17 area 4: the program comes from Settings, so the journey is a run with a
// saved 0601 program; a process that cannot execute it is refused with the
// CheckFailed text before the driver runs.
func TestL11274SavedProgramRespectsCurrentUserExecuteAccess(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			path := l11274Executable(t, f.home, cli, true)
			// Positive instrument: same current user can execute a 0700 file.
			if err := unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS); err != nil {
				t.Fatalf("SETUP: current-user execute-access positive control: %v", err)
			}
			// 0601 deliberately keeps other-x while removing owner-x. Chmod
			// makes the counterexample independent of the process umask.
			if err := os.Chmod(path, 0o601); err != nil {
				t.Fatal(err)
			}
			accessErr := unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS)
			t.Logf("independent OS access: uid=%d, mode=0601, current_user_executable=%v", os.Geteuid(), accessErr == nil)
			f.setProgram(t, cli, path)
			result := f.execute(t, "create_agent", l11274CreateArgs("Created "+cli, cli))
			id := l11274AssertCreated(t, f, result, cli, "#6E8BA8")
			if accessErr == nil {
				// Root/capability-bearing users may execute a file with any x
				// bit; there is nothing to refuse for them. The case is not skipped:
				// creation above still ran against the real tool.
				return
			}
			if !errors.Is(accessErr, unix.EACCES) {
				t.Fatalf("SETUP: execute-access check failed for another reason: %v", accessErr)
			}
			body := l11274ResultObject(t, result)
			warning, _ := body["program_warning"].(string)
			if body["runnable"] != false || !strings.Contains(warning, "does not pass its checks") || !strings.Contains(warning, "not_executable") {
				t.Errorf("0601 program must report runnable=false with the not_executable check, got %s", result.ForLLM)
			}
			fake, _ := l11274DriverSeam(t, f)
			logs := t11Q48CaptureRefusalLogs(t)
			refused := l11274DelegateAndReadRefusal(t, f, id, "must not run a 0601 program")
			if !strings.Contains(refused, "not_executable") || l11274ReasonNamesPath(refused, path) {
				t.Errorf("Q48: run refusal must name the failed check but omit the saved path %q, got %q", path, refused)
			}
			if got := len(fake.RecordedRunOpts()); got != 0 {
				t.Errorf("a refused run reached the driver %d times, want 0", got)
			}
			t11Q48AssertServerOnlyPath(t, logs, refused, "not_executable", path)
		})
	}
}
