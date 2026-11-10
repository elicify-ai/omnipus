//go:build darwin

package sandbox

import (
	"os"
	"os/exec"
	"testing"
)

func wu23Platform() string { return "Seatbelt" }

func wu23KernelCommand(t *testing.T, policy SandboxPolicy, argv []string) *exec.Cmd {
	t.Helper()
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Fatalf("BLOCKED: real Seatbelt gate unavailable (never a skip): %v", err)
	}
	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Fatal("BLOCKED: sandbox-exec exists but Seatbelt backend is unavailable — D8 requires a non-skip receipt")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = ScrubGatewayEnv()
	if err := backend.ApplyToCmd(cmd, policy); err != nil {
		t.Fatalf("real Seatbelt child profile rejected: %v", err)
	}
	return cmd
}

func TestWU23_SeatbeltRuntimeAllowances(t *testing.T) { wu23RuntimeAllowances(t) }
func TestWU23_SeatbeltInventory(t *testing.T)         { wu23KernelInventory(t) }
func TestWU23_SeatbeltAgentHome(t *testing.T)         { wu23KernelAgentHome(t) }
func TestWU23_SeatbeltGodFloor(t *testing.T)          { wu23KernelFloor(t) }
func TestWU23_SeatbeltPostSpawnResidual(t *testing.T) { wu23PostSpawn(t, false) }
