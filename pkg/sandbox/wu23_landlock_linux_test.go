//go:build linux

package sandbox

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func wu23Platform() string { return "Landlock" }

func wu23KernelCommand(t *testing.T, policy SandboxPolicy, argv []string) *exec.Cmd {
	t.Helper()
	if _, ok := NewLinuxBackend(); !ok {
		t.Fatal("BLOCKED: real Landlock unavailable — D8 requires a non-skip Linux receipt")
	}
	// This private test-child transport carries a Go policy, not gateway JSON.
	// Gob preserves its exported fields without changing production wire tags.
	var encoded bytes.Buffer
	if encodeErr := gob.NewEncoder(&encoded).Encode(policy); encodeErr != nil {
		t.Fatal(encodeErr)
	}
	testName := strings.Split(t.Name(), "/")[0]
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^" + testName + "$", "--", "--wu23-landlock-child", base64.RawStdEncoding.EncodeToString(encoded.Bytes())}, argv...)...)
	cmd.Env = ScrubGatewayEnv()
	return cmd
}

func wu23LandlockChild() bool {
	i := slices.Index(os.Args, "--wu23-landlock-child")
	if i < 0 {
		return false
	}
	encoded, decodeErr := base64.RawStdEncoding.DecodeString(os.Args[i+1])
	if decodeErr != nil {
		fmt.Fprintln(os.Stderr, "invalid probe policy encoding")
		os.Exit(90)
	}
	var policy SandboxPolicy
	if policyErr := gob.NewDecoder(bytes.NewReader(encoded)).Decode(&policy); policyErr != nil {
		fmt.Fprintln(os.Stderr, "invalid probe policy")
		os.Exit(90)
	}
	backend, ok := NewLinuxBackend()
	if !ok {
		fmt.Fprintln(os.Stderr, "Landlock unavailable in child — NOT a skip")
		os.Exit(90)
	}
	// Activate the real per-thread/per-child seam without confining the outer
	// test process. savedMode is the boot fact, not a substituted syscall.
	backend.savedMode = ModeEnforce
	runtime.LockOSThread()
	if err := backend.RestrictCurrentThreadWithPolicy(&policy); err != nil {
		fmt.Fprintf(os.Stderr, "real Landlock restrict failed: %v\n", err)
		os.Exit(90)
	}
	argv := os.Args[i+2:]
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = ScrubGatewayEnv()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "confined exec failed: %v\n", err)
		os.Exit(90)
	}
	os.Exit(0)
	return true
}

func TestWU23_LandlockRuntimeAllowances(t *testing.T) {
	if wu23LandlockChild() {
		return
	}
	wu23RuntimeAllowances(t)
}

func TestWU23_LandlockInventory(t *testing.T) {
	if wu23LandlockChild() {
		return
	}
	wu23KernelInventory(t)
}

func TestWU23_LandlockAgentHome(t *testing.T) {
	if wu23LandlockChild() {
		return
	}
	wu23KernelAgentHome(t)
}

func TestWU23_LandlockPostSpawnDenied(t *testing.T) {
	if wu23LandlockChild() {
		return
	}
	wu23PostSpawn(t, true)
}

func TestWU23_LandlockGodFloor(t *testing.T) {
	if wu23LandlockChild() {
		return
	}
	wu23KernelFloor(t)
}
