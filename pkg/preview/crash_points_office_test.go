//go:build darwin || linux

package preview

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/stretchr/testify/require"
)

// T-95 / B-23, B-38: crash points are exercised by REALLY killing a helper
// gateway (SIGKILL) at four points of a live call, then running a fresh
// gateway's recovery over the home. No JSON fixture and no fake start time
// stands in for the crash: the child is a real process whose death really
// closes the launcher's stdin, and the launcher-exit claim is observed on the
// live process, per platform (Linux adds the parent-death signal; macOS
// relies on recovery as the backstop, §3.4 D7 context).

// The helper gateway child: argv[1] "__preview-gw", the stage in argv[2], the
// home in OMNIPUS_GW_HOME. It builds a real call, starts the REAL launcher
// with the test binary as the fake converter, advances to its stage, signals
// the parent via gw-stage.json, and blocks until the parent kills it.
func gatewayCrashChildMain(stage, home string) {
	self, err := os.Executable()
	if err != nil {
		os.Exit(60)
	}
	start, err := processStart(os.Getpid())
	if err != nil {
		os.Exit(61)
	}
	c, err := newCall(home, os.Getpid(), start)
	if err != nil {
		os.Exit(62)
	}
	// The scratch shape run() creates, so the fake converter can run.
	for _, d := range []string{"in", "out", "home", "tmp", "profile/user"} {
		if mkdirErr := os.MkdirAll(filepath.Join(c.dir, filepath.FromSlash(d)), 0o700); mkdirErr != nil {
			os.Exit(63)
		}
	}
	behavior := "crashgw_target"
	if stage == "exit81_between" {
		behavior = "retry_81_once"
		if os.WriteFile(filepath.Join(c.dir, "profile", "user", "registrymodifications.xcu"), []byte(profileSettings), 0o600) != nil {
			os.Exit(64)
		}
		if os.WriteFile(filepath.Join(c.dir, "in", "document.docx"), []byte("same authorized input"), 0o600) != nil {
			os.Exit(64)
		}
	}
	if os.WriteFile(filepath.Join(c.dir, "behavior"), []byte(behavior), 0600) != nil {
		os.Exit(64)
	}
	hooks := hooksFor(confinement{readWrite: []string{c.dir}})
	spec := launcher.Spec{
		Self:    self,
		Target:  self,
		Args:    []string{"--headless"},
		Env:     []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "HOME=" + filepath.Join(c.dir, "home"), "TMPDIR=" + filepath.Join(c.dir, "tmp")},
		Dir:     c.dir,
		Limits:  launcher.Limits{MemoryBytes: officeMemory, CPUSeconds: programCPUSeconds, FileSizeBytes: officeOutputLimit, OpenFiles: officeOpenFile, Processes: officeProcesses},
		Prepare: hooks.prepare,
		Start:   hooks.start,
		Stdout:  os.Stdout,
	}
	proc, err := launcher.Start(spec)
	if err != nil {
		os.Exit(65)
	}
	launcherStart, err := processStart(proc.PID)
	if err != nil {
		os.Exit(66)
	}
	if stage != "after_launcher_start" {
		id, idErr := identify(proc.PID, self)
		if idErr == nil {
			idErr = c.running(id, []string{self})
		}
		if idErr != nil {
			os.Exit(66)
		}
		if stage == "during_target" || stage == "exit81_between" {
			if goErr := proc.Go(); goErr != nil {
				os.Exit(67)
			}
			_ = proc.Stdin.Close()
		}
		if stage == "during_target" {
			waitForFile(filepath.Join(c.dir, "target-started"), 15*time.Second)
		}
		if stage == "exit81_between" {
			_ = proc.Wait()
			if rmErr := c.removeInside("out"); rmErr != nil {
				os.Exit(68)
			}
			if seedErr := reseedOfficeProfile(c); seedErr != nil {
				os.Exit(68)
			}
			if idleErr := c.idle(); idleErr != nil {
				os.Exit(68)
			}
		}
	}
	stageFile, err := json.Marshal(map[string]any{"launcher_pid": proc.PID, "launcher_start": launcherStart})
	if err != nil {
		os.Exit(69)
	}
	if os.WriteFile(filepath.Join(home, "gw-stage.json"), stageFile, 0600) != nil {
		os.Exit(69)
	}
	// Park until the parent SIGKILLs us (that is the crash). A bare
	// `select{}` would trip Go's deadlock detector and kill the helper
	// gateway before the parent's SIGKILL, so the timer keeps the runtime
	// aware of a live goroutine.
	for {
		time.Sleep(time.Hour)
	}
}

// crashGatewayTargetProcess is the fake converter target for the crash
// points: it marks that the target really started, then holds.
func crashGatewayTargetProcess() int {
	if os.WriteFile("target-started", []byte("1"), 0600) != nil {
		return 80
	}
	time.Sleep(30 * time.Second)
	return 0
}

func waitForFile(path string, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The helper gateway mode of this test binary. Distinct argv pattern, so it
// never collides with the launcher/extracttext/fake-converter modes.
func init() {
	if len(os.Args) >= 3 && os.Args[1] == "__preview-gw" {
		gatewayCrashChildMain(os.Args[2], os.Getenv("OMNIPUS_GW_HOME"))
	}
}

// d1TargetStartedGlob is where §3.4 D1 puts the converter's "I started"
// marker: the working folder is <id>/work, under tmp/preview-calls, not
// beside owner.json. A glob of <id>/target-started matches nothing, so an
// empty result would pass without looking.
func d1TargetStartedGlob(home string) string {
	return filepath.Join(home, "tmp", "preview-calls", "*", "work", "target-started")
}

func TestPreviewRecovery_CrashPointsKillHelperGateway(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	selfStart := mustProcessStart(t, os.Getpid())
	for _, stage := range []struct {
		name                         string
		targetMayStart               bool
		launcherAliveAtCrash         bool
		launcherExitsWithoutRecovery bool
		wantKilled                   int
	}{
		// after launcher start / after running, before the go-byte: the
		// target's marker never appears; the launcher exits on stdin close.
		{"after_launcher_start", false, true, true, 0},
		{"after_running_before_gobyte", false, true, true, 0},
		// during the target: on macOS the launcher stays alive until recovery
		// stops it (no parent-death signal); Linux's parent-death signal ends
		// it, so recovery removes a dead-identity folder without a kill.
		{"during_target", true, true, runtime.GOOS != "darwin", 1},
		// between the two exit-81 starts: the first launcher already exited;
		// the record reads preparing and nothing is stopped.
		{"exit81_between", true, false, false, 0},
	} {
		t.Run(stage.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			require.NoError(t, os.MkdirAll(home, 0o700))
			child := exec.Command(exe, "__preview-gw", stage.name)
			child.Env = append(os.Environ(), "OMNIPUS_GW_HOME="+home)
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
			require.NoError(t, child.Start())
			childDone := make(chan struct{})
			go func() { _ = child.Wait(); close(childDone) }()

			// Wait for the stage, then the crash.
			var stageInfo struct {
				LauncherPID   int    `json:"launcher_pid"`
				LauncherStart string `json:"launcher_start"`
			}
			deadline := time.Now().Add(30 * time.Second)
			for {
				raw, readErr := os.ReadFile(filepath.Join(home, "gw-stage.json"))
				if readErr == nil && json.Unmarshal(raw, &stageInfo) == nil && stageInfo.LauncherPID != 0 {
					break
				}
				if time.Now().After(deadline) {
					_ = child.Process.Kill()
					<-childDone
					t.Fatalf("T-95 %s: the helper gateway never reached its stage", stage.name)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if stage.launcherAliveAtCrash {
				require.True(t, processAlive(stageInfo.LauncherPID), "instrument: the launcher is alive at the crash point")
			}
			require.NoError(t, child.Process.Kill(), "the helper gateway is SIGKILLed — a real crash")
			<-childDone

			// Pre-go-byte stages: the target's marker never appears (the call
			// folder id is generated, so discover any marker under the root).
			if !stage.targetMayStart {
				// §3.4 D1: the converter's cwd is <id>/work, so the marker is
				// preview-calls/<id>/work/target-started. A glob of <id>/target-started
				// matches nothing and "absent" would pass without looking.
				markers, err := filepath.Glob(d1TargetStartedGlob(home))
				require.NoError(t, err)
				require.Empty(t, markers,
					"T-95: the target never ran before the go-byte — its marker must be absent")
			}

			// The launcher-exit claim, observed on the live process.
			if stage.launcherExitsWithoutRecovery {
				require.Eventually(t, func() bool { return !processAlive(stageInfo.LauncherPID) },
					10*time.Second, 20*time.Millisecond,
					"T-95: the gateway's death closes the launcher's stdin and the launcher exits on its own")
			}

			// The trusted record survived the crash at the call folder, one
			// level above the scratch the converter could write (§3.4 D1).
			records, _ := filepath.Glob(filepath.Join(home, "tmp", "preview-calls", "*", ownerFile))
			require.Len(t, records, 1,
				"T-95 §3.4 D1: the trusted record sits at <id>/owner.json, outside scratch, after the crash")

			// A fresh gateway's startup recovery decides the folder.
			report := Recover(home, os.Getpid(), selfStart)
			require.Equal(t, 1, report.Removed, "T-95: the crashed call's folder is removed")
			require.Empty(t, report.Warnings, "T-95: the crash leaves nothing to diagnose")
			require.Equal(t, stage.wantKilled, report.Killed, "T-95: exactly the processes the record proves are stopped")
			require.Eventually(t, func() bool { return !processAlive(stageInfo.LauncherPID) },
				10*time.Second, 20*time.Millisecond,
				"T-95: after recovery the launcher is gone on every stage (stopped, or already exited)")
			leftovers, _ := os.ReadDir(scratchRoot(home))
			require.Empty(t, leftovers, "T-95: no call folder remains after recovery")
		})
	}
}

// TestPreviewCrashMarkerGlobFindsAWorkMarker is the positive control for
// T-95's "the marker never appears" check. The same glob must find a marker
// that really exists at <id>/work/target-started; otherwise an empty result
// proves nothing.
func TestPreviewCrashMarkerGlobFindsAWorkMarker(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(home, "tmp", "preview-calls", "probe", "work", "target-started")
	require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0o700))
	require.NoError(t, os.WriteFile(marker, []byte("1"), 0o600))
	found, err := filepath.Glob(d1TargetStartedGlob(home))
	require.NoError(t, err)
	require.Equal(t, []string{marker}, found,
		"T-95 instrument: the marker glob finds target-started at <id>/work when one exists")
}
