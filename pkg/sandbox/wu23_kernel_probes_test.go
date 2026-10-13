//go:build linux || darwin

package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

func wu23KernelPolicy(t *testing.T, f fixture.Layout, work string) SandboxPolicy {
	t.Helper()
	p, err := fspolicy.EffectiveFSPolicy(context.Background(), f.AgentHome, work, true, f.Home, "self", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	return DeriveKernelPolicy(p, TurnPolicyInput{HomePath: f.Home, Model: FilesystemModelOpen})
}

// D8 L1/M1: real shell utilities, one operation per child, and an allowed
// read+write inside that SAME child. Each operation gets a fresh sentinel so
// an earlier bad mutation cannot make a later denial pass with ENOENT.
func wu23KernelInventory(t *testing.T) {
	f := fixture.New(t)
	policy := wu23KernelPolicy(t, f, f.Work)
	ops := []struct{ name, command string }{
		{"read", `cat "$1"`},
		{"list", `ls "$1"`},
		{"create", `touch "$1"`},
		{"write", `printf changed > "$1"`},
		{"rename", `mv "$1" "$3/moved.txt"`},
		{"unlink", `rm "$1"`},
	}
	for _, rel := range fixture.ProtectedRelative() {
		t.Run(rel, func(t *testing.T) {
			for _, op := range ops {
				t.Run(op.name, func(t *testing.T) {
					path := filepath.Join(f.Home, filepath.FromSlash(rel))
					fixture.Write(t, path, fixture.Sentinel)
					if op.name == "create" {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
					script := `cat "$2" >/dev/null || exit 90
printf control > "$3/control-written.txt" || exit 91
printf 'CONTROL=ok\n'
` + op.command + ` >"$3/probe-out.txt" 2>"$3/probe-err.txt"
rc=$?
printf 'OP_EXIT=%s\n' "$rc"
cat "$3/probe-err.txt"
`
					cmd := wu23KernelCommand(t, policy, []string{"/bin/sh", "-c", script, "wu23", path, filepath.Join(f.Work, "allowed.txt"), f.Work})
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("child failed before complete probe: %v output=%s", err, out)
					}
					t.Run("positive_control", func(t *testing.T) {
						if !strings.Contains(string(out), "CONTROL=ok\n") {
							t.Fatalf("no same-child positive read/write control: %s", out)
						}
						b, err := os.ReadFile(filepath.Join(f.Work, "control-written.txt"))
						if err != nil || string(b) != "control" {
							t.Fatalf("same-child write bytes=%q err=%v", b, err)
						}
					})
					if strings.Contains(string(out), "OP_EXIT=0\n") {
						t.Errorf("D8 %s: kernel allowed %s of protected %s", wu23Platform(), op.name, rel)
					} else if !strings.Contains(string(out), "Permission denied") && !strings.Contains(string(out), "Operation not permitted") && !strings.Contains(string(out), "No such file") {
						t.Errorf("unexpected denial (not EACCES/EPERM/ENOENT): %s", out)
					}
					b, readErr := os.ReadFile(path)
					if op.name == "create" {
						if !os.IsNotExist(readErr) {
							t.Errorf("protected absent target created: %s", rel)
						}
					} else if readErr != nil || string(b) != fixture.Sentinel {
						t.Errorf("protected %s changed or removed by child (err=%v)", rel, readErr)
					}
					fixture.Write(t, path, fixture.Sentinel)
				})
			}
		})
	}
}

func wu23KernelAgentHome(t *testing.T) {
	f := fixture.New(t)
	policy := wu23KernelPolicy(t, f, f.AgentHome)
	for _, rel := range []string{".omnipus/last-session.md", "memory/private.txt", "sessions/private.txt", "skills/private.txt", "SOUL.md"} {
		cmd := wu23KernelCommand(t, policy, []string{"/bin/sh", "-c", `cat "$2" >/dev/null || exit 90; printf 'CONTROL=ok\n'; cat "$1" >/dev/null`, "wu23", filepath.Join(f.AgentHome, filepath.FromSlash(rel)), filepath.Join(f.AgentHome, "allowed.txt")})
		out, err := cmd.CombinedOutput()
		if !strings.Contains(string(out), "CONTROL=ok\n") {
			t.Fatalf("agent-home control missing: %s err=%v", out, err)
		}
		if err == nil {
			t.Errorf("D4: real %s child read own internal %s", wu23Platform(), rel)
		}
	}
}

func wu23KernelFloor(t *testing.T) {
	f := fixture.New(t)
	authored, err := fspolicy.EffectiveFSPolicy(t.Context(), f.AgentHome, f.Work, false, f.Home, "self", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	in := TurnPolicyInput{HomePath: f.Home, Model: FilesystemModelOpen}
	floor := reflect.ValueOf(&in).Elem().FieldByName("Floor")
	if !floor.IsValid() || floor.Kind() != reflect.Bool || !floor.CanSet() {
		t.Fatal("BLOCKED: TurnPolicyInput.Floor not implemented — required by amendment §2 D7; real God spawn-site probes remain separate")
	}
	floor.SetBool(true)
	policy := DeriveKernelPolicy(authored, in)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	// An arbitrary ephemeral port is deliberately not added to ConnectPorts.
	script := fmt.Sprintf(`printf free > "$3/free.txt" || exit 90
cat "$2" >/dev/null || exit 91
/bin/bash -c 'exec 3<>/dev/tcp/127.0.0.1/%d' || exit 92
printf 'CONTROL=ok\n'
cat "$1" >/dev/null
`, port)
	cmd := wu23KernelCommand(t, policy, []string{"/bin/sh", "-c", script, "wu23", filepath.Join(f.Home, "sessions", "private.txt"), filepath.Join(f.Work, "allowed.txt"), f.Outside})
	out, childErr := cmd.CombinedOutput()
	if !strings.Contains(string(out), "CONTROL=ok\n") {
		t.Fatalf("D7: God floor not free outside protected home: %s err=%v", out, childErr)
	}
	if childErr == nil {
		t.Error("D7: floor child read sessions/private.txt")
	}
	ancestor := filepath.Dir(f.Home)
	cmd = wu23KernelCommand(t, policy, []string{"/bin/sh", "-c", `cat "$1" >/dev/null || exit 90; printf 'CONTROL=ok\n'; touch "$2"`, "wu23", filepath.Join(f.Work, "allowed.txt"), filepath.Join(ancestor, "ancestor-new.txt")})
	out, err = cmd.CombinedOutput()
	if !strings.Contains(string(out), "CONTROL=ok\n") {
		t.Fatalf("ancestor-limit control missing: %s err=%v", out, err)
	}
	if wu23Platform() == "Landlock" && err == nil {
		t.Error("D7/L3: Linux floor unexpectedly allowed a new direct ancestor entry; documented limitation not enforced")
	}
	if wu23Platform() == "Seatbelt" && err != nil {
		t.Errorf("D7/M3: macOS God floor must allow new ancestor entries: %v", err)
	}
	t.Logf("God ancestor creation succeeded=%t platform=%s", err == nil, wu23Platform())
}

func wu23QuotePath(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func wu23CheckExecuted(t *testing.T, cmd *exec.Cmd, want string) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("child got %q want %q err=%v", out, want, err)
	}
}

func TestWU23_KernelDerivationRequiredInputs(t *testing.T) {
	typ := reflect.TypeOf(TurnPolicyInput{})
	for _, name := range []string{"AllowRoots", "Floor"} {
		t.Run(name, func(t *testing.T) {
			if _, ok := typ.FieldByName(name); !ok {
				t.Fatalf("BLOCKED: TurnPolicyInput.%s not implemented — required by amendment §2 D3/D7 (allowances must precede denial)", name)
			}
		})
	}
	f := fixture.New(t)
	RegisterTurnPolicyBase(&TurnPolicyInput{HomePath: f.Home, Model: FilesystemModelOpen})
	t.Cleanup(func() { RegisterTurnPolicyBase(nil) })
	lim, err := ResolveLimits(true, f.Work, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if lim.KernelPolicy == nil {
		t.Error("D7: ResolveLimits(GodMode) returned no protected-path floor")
	}
	if lim.EgressProxyAddr != "" || lim.MemoryLimitBytes != 0 {
		t.Error("D7: God floor must not introduce egress filtering or resource caps")
	}
	t.Log(fmt.Sprintf("God limits: kernel floor present=%t", lim.KernelPolicy != nil))
}
