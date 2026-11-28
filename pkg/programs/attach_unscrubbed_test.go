package programs

import (
	"bytes"
	"errors"
	"os/exec"
	"testing"
)

// Ruling D10 (#1960 S1, FR-239): Attach never reads the gateway environment.
// A command with no scrubbed environment (nil Env) that would receive program
// variables is refused with ErrUnscrubbedCommand and left unchanged.
func TestAttach_UnscrubbedCommandFailsClosed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("true")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := Attach(cmd, ResolvedPrograms{Env: []string{"GH_TOKEN=attach-unscrubbed-synthetic"}, Tokens: []string{"attach-unscrubbed-synthetic"}})
	if !errors.Is(err, ErrUnscrubbedCommand) {
		t.Fatalf("Attach on a nil-Env command with program variables: got %v, want ErrUnscrubbedCommand", err)
	}
	if cmd.Env != nil {
		t.Fatalf("Attach must leave a refused command's Env nil, got %d entries", len(cmd.Env))
	}
	if cmd.Stdout != &stdout || cmd.Stderr != &stderr {
		t.Fatal("Attach must not change a refused command's writers")
	}
}
