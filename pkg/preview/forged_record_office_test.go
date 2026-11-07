//go:build darwin || linux

package preview

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	launcherPID := os.Getppid() // Unix: the run-limited launcher is the parent.
	receipt := map[string]any{"launcher_pid": launcherPID, "pid": os.Getpid()}
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

// T-93 / §3.4 D3 (FR-051, founder Q18 = A, B-64): during a real call the
// converter plants a forged owner record in the folder it can write, naming a
// harmless live process, that process's group, and a valid-shaped socket
// folder. After a simulated crash, recovery must ignore the forged file: the
// harmless process and the socket folder survive, and the REAL launcher is
// stopped (Killed == 1). On the pre-amendment layout the forged file IS the
// record, so this test's RED is recovery acting on it.
func TestPreviewRecovery_ForgedRecordInScratchIsIgnored(t *testing.T) {
	// The separate harmless process, in its own group.
	harmless := exec.Command("/bin/sleep", "60")
	harmless.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, harmless.Start())
	harmlessPID := harmless.Process.Pid
	harmlessDone := make(chan struct{})
	go func() { _ = harmless.Wait(); close(harmlessDone) }()
	defer func() {
		_ = syscall.Kill(harmlessPID, syscall.SIGKILL)
		select {
		case <-harmlessDone:
		case <-time.After(5 * time.Second):
		}
	}()

	// The valid-shaped socket folder the forged record will name: a real,
	// uniquely named, test-owned folder that must survive recovery.
	socketID, err := randomID(4)
	require.NoError(t, err)
	socketDir := "/private/tmp/o-" + socketID
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

	var recoveryReport RecoveryReport
	finished := make(chan struct{})
	var conversionErr error
	go func() {
		defer close(finished)
		_, _, conversionErr = conv.run(context.Background(), []byte("authorized harmless input"), ".docx")
	}()

	// Wait for the forge, then simulate the crash: a DIFFERENT gateway's
	// startup recovery runs while this call is still mid-flight.
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
	// The real launcher's PID, recorded by the fake before forging, for the
	// in-run kill assertion and for cleanup.
	receiptRaw, err := os.ReadFile(filepath.Join(c.dir, "forge-receipt.json"))
	require.NoError(t, err, "instrument: the fake recorded the real launcher")
	var receipt struct {
		LauncherPID int `json:"launcher_pid"`
	}
	require.NoError(t, json.Unmarshal(receiptRaw, &receipt))
	require.True(t, processAlive(receipt.LauncherPID), "instrument: the real launcher is alive before recovery")
	t.Cleanup(func() { _ = syscall.Kill(-receipt.LauncherPID, syscall.SIGKILL) })

	recoveryReport = Recover(home, -1, "simulated-crash-other-gateway")

	// The forged file must have been IGNORED: the harmless process and its
	// named socket folder survive recovery.
	require.Never(t, func() bool { return !processAlive(harmlessPID) },
		time.Second, 20*time.Millisecond,
		"T-93: recovery acted on the converter's forged record and stopped the harmless process it names")
	require.Never(t, func() bool {
		_, statErr := os.Stat(socketDir)
		return statErr != nil
	}, time.Second, 20*time.Millisecond,
		"T-93: recovery acted on the forged socket folder and removed it")
	data, err := os.ReadFile(filepath.Join(socketDir, "decoy"))
	require.NoError(t, err)
	require.Equal(t, "must survive", string(data), "the forged socket folder's contents survive")

	// The kill path was reached IN THIS RUN: the real launcher is stopped, so
	// the call ends instead of hanging, and recovery counts exactly one kill.
	select {
	case <-finished:
		require.Error(t, conversionErr, "the recovery stop ends the conversion")
	case <-time.After(20 * time.Second):
		t.Fatal("T-93: the real launcher was never stopped — recovery did not act on the trusted record in this run")
	}
	require.Eventually(t, func() bool { return !processAlive(receipt.LauncherPID) },
		5*time.Second, 20*time.Millisecond, "T-93: the REAL launcher died in the same run")
	require.Equal(t, 1, recoveryReport.Killed, "exactly the real launcher's group is stopped")
}
