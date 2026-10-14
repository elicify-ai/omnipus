package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/tests/wu19envprobe"
)

// SpawnExe prepends "start", so the Go test flag parser is not the child entry
// point. Intercept only this private argv marker before the testing harness can
// collect anything; never run the whole test suite as a fixture child.
func init() {
	if i := slices.Index(os.Args, "--wu19-daemon-snapshot"); i >= 0 {
		if i+1 >= len(os.Args) {
			os.Exit(90)
		}
		conn, err := net.DialTimeout("tcp", os.Args[i+1], 10*time.Second)
		if err != nil {
			os.Exit(91)
		}
		if err := json.NewEncoder(conn).Encode(wu19envprobe.Capture()); err != nil {
			os.Exit(92)
		}
		ack := make([]byte, 1)
		if _, err := io.ReadFull(conn, ack); err != nil || ack[0] != 'x' {
			os.Exit(93)
		}
		if err := conn.Close(); err != nil {
			os.Exit(94)
		}
		os.Exit(0)
	}
}

// Characterization test requested by chief, CHECK F03: this pins the CURRENT
// named daemon boot exception, not a QA approval of its security policy or proof
// that a real gateway can unlock. Those remain security-lead/founder decisions.
func TestWU19_DaemonRestartChildBootEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("USER", "wu19-fixture-user")
	t.Setenv("OMNIPUS_HOME", filepath.Join(root, "parent-data-home"))
	t.Setenv("OMNIPUS_CHILD_WU19_CONTROL", "allowed-control")
	for _, key := range wu19envprobe.DeniedKeys() {
		t.Setenv(key, "wu19-innocuous-fixture")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	boot := []string{"OMNIPUS_MASTER_KEY", "OMNIPUS_KEY_FILE", "OMNIPUS_BEARER_TOKEN"}
	// Every subset of the three nonempty operator settings, plus explicitly
	// present-but-empty settings. This derives 2^3 + 1 cases, not a magic count.
	for mask := 0; mask <= 1<<len(boot); mask++ {
		t.Run(fmt.Sprintf("operator_settings_%d", mask), func(t *testing.T) {
			selectedHome := t.TempDir()
			wantBoot := map[string]string{"OMNIPUS_HOME": selectedHome}
			for i, key := range boot {
				t.Setenv(key, "")
				if mask < 1<<len(boot) && mask&(1<<i) != 0 {
					value := "wu19-operator-fixture-" + key
					t.Setenv(key, value)
					wantBoot[key] = value
				} else if mask != 1<<len(boot) {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			s, pid := wu19SpawnDaemonSnapshot(t, exe, selectedHome)
			t.Run("generic_positive_control", func(t *testing.T) {
				if s.PID != pid {
					t.Fatal("receipt is not from the actual detached daemon child")
				}
				for key, value := range map[string]string{"HOME": root, "PATH": os.Getenv("PATH"), "USER": "wu19-fixture-user", "OMNIPUS_CHILD_WU19_CONTROL": "allowed-control"} {
					if s.Values[key] != value {
						t.Errorf("daemon generic control %s got %q want %q", key, s.Values[key], value)
					}
				}
			})
			for key, want := range wantBoot {
				if got, ok := s.Values[key]; !ok || got != want {
					t.Errorf("F03: daemon boot addition %s missing/wrong; selected home and nonempty operator fixtures must be preserved", key)
				}
			}
			for _, key := range s.Keys {
				if wu19envprobe.GenericAllowedKey(key) {
					continue
				}
				if _, allowed := wantBoot[key]; allowed {
					continue
				}
				if runtime.GOOS == "windows" && key == "SYSTEMROOT" { // os/exec documented insertion.
					continue
				}
				t.Errorf("F03/FR-239: unexpected key %s reached daemon child (operator-empty/unset boot keys must also be absent)", key)
			}
		})
	}
}

func wu19SpawnDaemonSnapshot(t *testing.T, exe, home string) (wu19envprobe.Snapshot, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	pid, err := SpawnExe(home, exe, []string{"--wu19-daemon-snapshot", listener.Addr().String()})
	if err != nil {
		t.Fatalf("real SpawnExe failed: %v", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// No test leaves a detached child behind after a failed assertion.
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrNotExist) && err != os.ErrProcessDone {
			t.Errorf("daemon fixture cleanup kill failed: %v", err)
		}
	})
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("daemon snapshot handshake failed: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var s wu19envprobe.Snapshot
	if err := json.NewDecoder(conn).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	state, err := process.Wait()
	if err != nil || !state.Success() {
		t.Fatalf("daemon child did not complete cleanly: state=%v err=%v", state, err)
	}
	stored, err := readPID(home)
	if err != nil || stored != pid {
		t.Fatalf("SpawnExe selected-home PID contract: stored=%d child=%d err=%v", stored, pid, err)
	}
	return s, pid
}
