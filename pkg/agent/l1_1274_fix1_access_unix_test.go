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
// This is a direct create-tool journey, not just the source-predicate probe.
func TestL11274CLIPathRespectsCurrentUserExecuteAccess(t *testing.T) {
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
			fi, err := os.Stat(path)
			if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o601 {
				t.Fatalf("SETUP: exact regular-file permission counterexample: stat=%v, err=%v", fi, err)
			}
			var st unix.Stat_t
			if err := unix.Stat(path, &st); err != nil {
				t.Fatal(err)
			}
			if st.Uid != uint32(os.Geteuid()) {
				t.Fatalf("SETUP: counterexample owner uid=%d, effective uid=%d", st.Uid, os.Geteuid())
			}
			accessErr := unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS)
			t.Logf("independent OS access: uid=%d, mode=0601, current_user_executable=%v", os.Geteuid(), accessErr == nil)
			args := l11274CreateArgs("Created "+cli, cli, path)
			if accessErr == nil {
				// Root/capability-bearing users may execute a file with any x
				// bit. Both branches test the product; neither skips the case.
				l11274AssertCreated(t, f, f.execute(t, "create_agent", args), cli, path, "#6E8BA8")
			} else {
				if !errors.Is(accessErr, unix.EACCES) {
					t.Fatalf("SETUP: execute-access check failed for another reason: %v", accessErr)
				}
				before := l11274Fix1ReadResources(t, f)
				message := l11274ModelRefusalReason(t, f.execute(t, "create_agent", args))
				if !strings.Contains(message, "cli_path") || !l11274ReasonNamesPath(message, path) {
					t.Errorf("current-user execution refusal must identify cli_path %q, got %q", path, message)
				}
				lower := strings.ToLower(message)
				if !strings.Contains(lower, "not executable") && !strings.Contains(lower, "permission denied") && !strings.Contains(lower, "cannot execute") {
					t.Errorf("0601 refusal must explain the executable-access fault, got %q", message)
				}
				l11274Fix1AssertNoWrites(t, f, "created-"+cli, before)
			}
		})
	}
}
