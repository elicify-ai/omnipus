//go:build darwin || linux

package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// forgeRecordOfficeProcess is the fake converter for T-93: it forges an
// owner.json in the folder it can write (scratch), naming a separate harmless
// process as the launcher and a valid-shaped socket folder. §3.4 D4 stores no
// process-group number, so the forge does not carry one; the harmless
// process's PID as the launcher is what a trusted record would be stopped by.
// It first records the REAL launcher's identity, then writes the forge-ready
// marker and blocks, so recovery runs against the forged record while the
// real call is still mid-flight.
func forgeRecordOfficeProcess() int {
	raw, err := os.ReadFile("forge-plan.json")
	if err != nil {
		return 70
	}
	var plan struct {
		PID        int    `json:"pid"`
		Start      string `json:"start"`
		Executable string `json:"executable"`
		SocketDir  string `json:"socket_dir"`
	}
	if json.Unmarshal(raw, &plan) != nil {
		return 71
	}
	// unix.Exec keeps the launcher's PID, so this process IS the recorded
	// launcher. Its parent is the gateway. Both are recorded; the oracle is pid.
	receipt := map[string]any{"launcher_pid": os.Getppid(), "pid": os.Getpid()}
	data, err := json.Marshal(receipt)
	if err != nil {
		return 72
	}
	if os.WriteFile("forge-receipt.json", data, 0600) != nil {
		return 73
	}
	forged, err := json.Marshal(ownerRecord{
		State:        stateRunning,
		GatewayPID:   424242,
		GatewayStart: "forged-gateway-start",
		Launcher:     &procIdentity{PID: plan.PID, Start: plan.Start, Executable: plan.Executable},
		Executables:  []string{plan.Executable},
		SocketDir:    plan.SocketDir,
	})
	if err != nil {
		return 74
	}
	if os.WriteFile("owner.json", forged, 0600) != nil {
		return 75
	}
	if os.WriteFile("forge-ready", []byte("1"), 0600) != nil {
		return 76
	}
	time.Sleep(30 * time.Second) // canceled by recovery stopping the real launcher
	return 0
}

// forgedScratch is one mid-flight call whose scratch holds a forged record.
type forgedScratch struct {
	home         string
	call         *call
	harmlessPID  int
	socketDir    string
	converterPID int // the forged process; after unix.Exec this is the launcher's PID
	finished     <-chan struct{}
	convErr      func() error
}

// startForgedScratchCall runs a real call until the converter has planted the
// forged record in scratch. The trusted record is whatever publish wrote.
func startForgedScratchCall(t *testing.T) forgedScratch {
	t.Helper()
	harmless := exec.Command("/bin/sleep", "60")
	harmless.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, harmless.Start())
	harmlessPID := harmless.Process.Pid
	harmlessDone := make(chan struct{})
	go func() { _ = harmless.Wait(); close(harmlessDone) }()
	t.Cleanup(func() {
		_ = syscall.Kill(harmlessPID, syscall.SIGKILL)
		select {
		case <-harmlessDone:
		case <-time.After(5 * time.Second):
		}
	})

	socketID, err := randomID(4)
	require.NoError(t, err)
	socketRoot := os.TempDir()
	if runtime.GOOS == "darwin" {
		// Recovery accepts the macOS launcher's short, canonical socket path.
		socketRoot = "/private/tmp"
	}
	socketDir := filepath.Join(socketRoot, "o-"+socketID)
	require.NoError(t, os.Mkdir(socketDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(socketDir, "decoy"), []byte("must survive"), 0o600))
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })

	home := t.TempDir()
	conv, c := fakeOfficeCall(t, home, "forge_record")
	t.Cleanup(c.finish)
	plan, err := json.Marshal(map[string]any{
		"pid": harmlessPID, "start": mustProcessStart(t, harmlessPID), "executable": "/bin/sleep", "socket_dir": socketDir,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "forge-plan.json"), plan, 0o600))

	finished := make(chan struct{})
	var conversionErr error
	go func() {
		defer close(finished)
		_, _, conversionErr = conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
	}()
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		for {
			if data, readErr := os.ReadFile(filepath.Join(c.dir, "forge-ready")); readErr == nil && string(data) == "1" {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	select {
	case <-ready:
	case <-time.After(15 * time.Second):
		t.Fatal("T-93: the fake converter never forged the record")
	}
	receiptRaw, err := os.ReadFile(filepath.Join(c.dir, "forge-receipt.json"))
	require.NoError(t, err, "instrument: the fake recorded the real launcher")
	var receipt struct {
		ParentPID    int `json:"launcher_pid"`
		ConverterPID int `json:"pid"`
	}
	require.NoError(t, json.Unmarshal(receiptRaw, &receipt))
	// child_unix.go::Main replaces the launcher with the target via unix.Exec,
	// so the converter's own PID is the launcher the record named. Its parent
	// is the gateway, not the launcher.
	require.True(t, processAlive(receipt.ConverterPID), "instrument: the converter (the recorded launcher, after exec) is alive before recovery")
	require.NotEqual(t, receipt.ParentPID, receipt.ConverterPID, "instrument: the receipt distinguishes the parent from the converter")
	t.Cleanup(func() {
		_ = syscall.Kill(-receipt.ConverterPID, syscall.SIGKILL)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
		}
	})
	return forgedScratch{
		home: home, call: c, harmlessPID: harmlessPID, socketDir: socketDir,
		converterPID: receipt.ConverterPID, finished: finished,
		convErr: func() error { return conversionErr },
	}
}

// deadGateway is a process that has already exited, plus a start time that is
// not that process's start. gatewayAlive is false on both checks (§3.4 D3).
func deadGateway(t *testing.T) (int, string) {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "60")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	realStart := mustProcessStart(t, pid)
	require.NoError(t, cmd.Process.Kill())
	_, _ = cmd.Process.Wait()
	require.False(t, processAlive(pid), "instrument: the stand-in gateway is dead")
	return pid, "gone-" + realStart
}

// retireTrustedGateway rewrites the gateway identity on the call folder's
// owner.json (<id>/owner.json, one level above work, §3.4 D1) so the record
// names a gateway that is gone. The launcher identity publish wrote is kept.
// publish itself is not used: on the pre-D1 layout it writes the scratch file
// and would replace the forge, hiding the bug this test exists to catch.
// A missing file means that layout, and the rewrite is skipped. The returned
// PID is the launcher publish recorded, or 0 when there was no call-folder record.
func retireTrustedGateway(t *testing.T, c *call) int {
	t.Helper()
	path := filepath.Join(filepath.Dir(c.dir), ownerFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	require.NoError(t, err)
	var rec ownerRecord
	require.NoError(t, json.Unmarshal(data, &rec))
	require.Equal(t, stateRunning, rec.State, "instrument: running was published before the forge")
	require.NotNil(t, rec.Launcher, "instrument: the published record names the launcher")
	recorded := rec.Launcher.PID
	pid, start := deadGateway(t)
	require.False(t, gatewayAlive(pid, start), "instrument: the stand-in gateway is not alive")
	rec.GatewayPID = pid
	rec.GatewayStart = start
	out, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o600))
	return recorded
}

// trustedCallFolder is the folder whose owner.json recovery reads. On the D1
// layout that is the call folder, one level above work. On the pre-D1 layout
// the only record is inside the scratch folder, which is c.dir.
func trustedCallFolder(c *call) string {
	above := filepath.Dir(c.dir)
	if st, err := os.Stat(filepath.Join(above, ownerFile)); err == nil && !st.IsDir() {
		return above
	}
	return c.dir
}

// T-93 / §3.4 D3 (FR-051, founder Q18 = A, B-64): during a real call the
// converter plants a forged owner record in the folder it can write. Recovery
// reads only the call folder's record. With that record's gateway gone, the
// harmless process and the forged socket folder survive and the real launcher
// is stopped (Killed == 1). A live matching gateway is the keep-and-warn
// branch D3 keeps: the record stays and nothing is stopped.
func TestPreviewRecovery_ForgedRecordInScratchIsIgnored(t *testing.T) {
	t.Run("dead_gateway_stops_the_real_launcher", func(t *testing.T) {
		fx := startForgedScratchCall(t)
		recorded := retireTrustedGateway(t, fx.call)
		if recorded != 0 {
			require.Equal(t, fx.converterPID, recorded,
				"instrument: unix.Exec keeps the launcher PID, so the published launcher is the converter")
		}
		report := Recover(fx.home, -1, "simulated-crash-other-gateway")

		require.Never(t, func() bool { return !processAlive(fx.harmlessPID) },
			time.Second, 20*time.Millisecond,
			"T-93: recovery acted on the converter's forged record and stopped the harmless process it names")
		require.Never(t, func() bool {
			_, statErr := os.Stat(fx.socketDir)
			return statErr != nil
		}, time.Second, 20*time.Millisecond,
			"T-93: recovery acted on the forged socket folder and removed it")
		data, err := os.ReadFile(filepath.Join(fx.socketDir, "decoy"))
		require.NoError(t, err)
		require.Equal(t, "must survive", string(data), "the forged socket folder's contents survive")

		select {
		case <-fx.finished:
			require.Error(t, fx.convErr(), "the recovery stop ends the conversion")
		case <-time.After(20 * time.Second):
			t.Fatal("T-93: the real launcher was never stopped — recovery did not act on the trusted record in this run")
		}
		require.Eventually(t, func() bool { return !processAlive(fx.converterPID) },
			5*time.Second, 20*time.Millisecond, "T-93: the REAL launcher died in the same run")
		require.Equal(t, 1, report.Killed, "exactly the real launcher's group is stopped")
	})

	// D3 keeps the existing keep-and-warn branch: a live owner gateway (two
	// gateways sharing a home) is not stopped. The warning sentence is that
	// branch's existing text.
	t.Run("live_gateway_is_kept_with_the_warning", func(t *testing.T) {
		fx := startForgedScratchCall(t)
		callFolder := trustedCallFolder(fx.call)
		report := Recover(fx.home, -1, "simulated-crash-other-gateway")
		want := fmt.Sprintf("preview folder %s kept: its gateway (pid %d) is still running — two gateways share this home",
			filepath.Base(callFolder), os.Getpid())
		require.Contains(t, report.Warnings, want,
			"D3: a live matching gateway is kept and warned, not stopped; killed=%d removed=%d warnings=%v",
			report.Killed, report.Removed, report.Warnings)
		require.Equal(t, 0, report.Killed, "D3 keep: nothing is stopped")
		require.Equal(t, 0, report.Removed, "D3 keep: the call folder is not removed")
		_, err := os.Stat(callFolder)
		require.NoError(t, err, "D3 keep: the call folder stays")
		require.Never(t, func() bool {
			return !processAlive(fx.harmlessPID) || !processAlive(fx.converterPID)
		}, time.Second, 20*time.Millisecond, "D3 keep: a live gateway stops nothing")
		_, err = os.Stat(fx.socketDir)
		require.NoError(t, err, "D3 keep: the forged socket folder survives")
	})
}
