//go:build linux

package tools

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// D7: actual tool front doors, all three spawn sites, God Mode A. The executable
// opens dynamically constructed paths so a command-text refusal cannot conceal
// a missing kernel floor. Each child proves outside writes and open networking.
func TestWU23_GodModeNativeSpawns_Landlock(t *testing.T) {
	if slices.Contains(os.Args, "--wu23-os-probe") {
		wu23GodOSProbe()
		return
	}
	if !slices.Contains(os.Args, "--wu23-god-inner") {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe, "-test.run=^TestWU23_GodModeNativeSpawns_Landlock$", "-test.v", "--", "--wu23-god-inner")
		cmd.Env = sandbox.ScrubGatewayEnv()
		out, err := cmd.CombinedOutput()
		t.Logf("isolated tool-spawn receipt:\n%s", out)
		if err != nil {
			t.Fatalf("God Mode native spawn matrix is red: %v", err)
		}
		return
	}
	f := fixture.New(t)
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: f.Home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	t.Setenv("OMNIPUS_CHILD_WU23_DATA", f.Home)
	t.Setenv("OMNIPUS_CHILD_WU23_WORK", f.Work)
	t.Setenv("OMNIPUS_CHILD_WU23_OUTSIDE", f.Outside)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("OMNIPUS_CHILD_WU23_ADDRESS", ln.Addr().String())
	backend, available := sandbox.NewLinuxBackend()
	if !available {
		t.Fatal("BLOCKED: Landlock mechanism unavailable — no skip allowed for D8")
	}
	boot := sandbox.DefaultPolicyForModel(sandbox.FilesystemModelOpen, f.Home, nil, nil, nil, nil)
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	boot.ConnectPortRules = append(boot.ConnectPortRules, sandbox.NetPortRule{Port: port})
	// Gateway setup remains broad; the tested floor must be applied at each
	// child spawn. All irreversible Linux state is confined to this re-exec.
	runtime.LockOSThread()
	if err := backend.Apply(boot); err != nil {
		t.Fatalf("Landlock boot instrument failed: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := wu23Quote(exe) + " -test.run=^TestWU23_GodModeNativeSpawns_Landlock$ -- --wu23-os-probe"
	ctx := wu23Context(f)
	for _, site := range []string{"foreground", "background", "environment_setup"} {
		t.Run(site, func(t *testing.T) {
			done := make(chan *ToolResult, 1)
			var result *ToolResult
			if site == "environment_setup" {
				prefix := filepath.Join(f.Work, "setup-prefix")
				target := &lifecycleTarget{prefix: prefix, cache: filepath.Join(prefix, "cache"), tmp: filepath.Join(prefix, "tmp")}
				for _, dir := range []string{target.prefix, target.cache, target.tmp} {
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{Home: f.Home, AgentWorkDir: f.AgentHome, GodMode: true, Store: lifecycleStore{target: target}})
				start := tool.ExecuteAsync(ctx, map[string]any{"command": command, "purpose": "protected path probe", "scope": "workspace", "timeout_seconds": float64(20)}, func(_ context.Context, r *ToolResult) { done <- r })
				if start.IsError {
					t.Fatalf("setup child did not start: %s", start.ForLLM)
				}
			} else {
				tool, err := NewExecToolWithDeps(f.AgentHome, false, nil, ExecToolDeps{GodMode: true})
				if err != nil {
					t.Fatal(err)
				}
				result = tool.ExecuteAsync(ctx, map[string]any{"command": command, "run_in_background": site == "background", "timeout_seconds": float64(20)}, func(_ context.Context, r *ToolResult) { done <- r })
				if site == "background" && result.IsError {
					t.Fatalf("background child did not start: %s", result.ForLLM)
				}
			}
			if site != "foreground" {
				select {
				case result = <-done:
				case <-time.After(30 * time.Second):
					t.Fatal("child completion not observed; not a kernel denial receipt")
				}
			}
			t.Run("positive_control", func(t *testing.T) {
				if !strings.Contains(result.ForLLM, "GOD_CONTROL=workspace,outside,network") {
					t.Fatalf("D7 A: God child lost free writes/network: %s", result.ForLLM)
				}
			})
			for _, rel := range []string{"sessions", "credentials"} {
				if !strings.Contains(result.ForLLM, "DENIED="+rel) || strings.Contains(result.ForLLM, "LEAKED="+rel) {
					t.Errorf("D7: God %s child reached %s; actual output=%s", site, rel, result.ForLLM)
				}
			}
		})
	}
}

func wu23GodOSProbe() {
	home := os.Getenv("OMNIPUS_CHILD_WU23_DATA")
	work := os.Getenv("OMNIPUS_CHILD_WU23_WORK")
	outside := os.Getenv("OMNIPUS_CHILD_WU23_OUTSIDE")
	b, err := os.ReadFile(filepath.Join(work, "allowed.txt"))
	if err != nil || string(b) != "WU23_ALLOWED_CONTROL\n" {
		fmt.Fprintln(os.Stderr, "workspace control failed")
		os.Exit(90)
	}
	if err := os.WriteFile(filepath.Join(outside, "god-free.txt"), []byte("free"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "God outside write control failed")
		os.Exit(91)
	}
	conn, err := net.DialTimeout("tcp", os.Getenv("OMNIPUS_CHILD_WU23_ADDRESS"), 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "God arbitrary-port network control failed")
		os.Exit(92)
	}
	if err := conn.Close(); err != nil {
		os.Exit(93)
	}
	fmt.Println("GOD_CONTROL=workspace,outside,network")
	for name, rel := range map[string]string{"sessions": "sessions/private.txt", "credentials": "credentials.json"} {
		if _, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel))); err == nil {
			fmt.Println("LEAKED=" + name)
		} else if os.IsPermission(err) || os.IsNotExist(err) {
			fmt.Println("DENIED=" + name)
		} else {
			fmt.Fprintln(os.Stderr, "unexpected protected-file I/O error")
			os.Exit(94)
		}
	}
	os.Exit(0)
}
