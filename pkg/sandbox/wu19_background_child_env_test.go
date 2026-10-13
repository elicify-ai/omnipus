package sandbox_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/tests/wu19envprobe"
)

// FR-239 / CHECK F02: exercise the dev-server spawn, not foreground Run.
// The executable is a process-edge fixture; spawning/merging/hardening are real.
func TestWU19_BackgroundChildEnvironment(t *testing.T) {
	if i := slices.Index(os.Args, "--wu19-background-snapshot"); i >= 0 {
		if err := wu19envprobe.Save(os.Args[i+1]); err != nil {
			os.Exit(90)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("USER", "wu19-fixture-user")
	t.Setenv("OMNIPUS_HOME", filepath.Join(root, "data"))
	t.Setenv("OMNIPUS_CHILD_WU19_CONTROL", "allowed-control")
	for _, key := range append(wu19envprobe.DeniedKeys(), "OMNIPUS_MASTER_KEY", "OMNIPUS_KEY_FILE", "OMNIPUS_BEARER_TOKEN") {
		t.Setenv(key, "wu19-innocuous-fixture")
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(key, "wu19-parent-proxy-fixture")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []int32{0, 18191} { // zero=no pin; positive port overrides caller's pin.
		t.Run(fmt.Sprintf("port_%d", port), func(t *testing.T) {
			work := t.TempDir()
			path := filepath.Join(work, "child.json")
			lim := sandbox.Limits{WorkspaceDir: work, EgressProxyAddr: "127.0.0.1:19891"}
			caller := []string{"WU19_SITE_OPTION=background-option", "HOST=caller-host", "PORT=caller-port"}
			cmd, err := sandbox.SpawnBackgroundChild([]string{exe, "-test.run=^TestWU19_BackgroundChildEnvironment$", "--", "--wu19-background-snapshot", path}, work, caller, port, lim)
			if err != nil {
				t.Fatalf("real background child failed to start: %v", err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("real background child failed: %v", err)
			}
			s, err := wu19envprobe.Read(path)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{
				"HOME": root, "PATH": os.Getenv("PATH"), "USER": "wu19-fixture-user",
				"OMNIPUS_CHILD_WU19_CONTROL": "allowed-control", "WU19_SITE_OPTION": "background-option",
				"HOST": "127.0.0.1", "PORT": "caller-port", "npm_config_cache": work + "/.npm-cache",
				"HTTP_PROXY": "http://127.0.0.1:19891", "HTTPS_PROXY": "http://127.0.0.1:19891",
				"http_proxy": "http://127.0.0.1:19891", "https_proxy": "http://127.0.0.1:19891",
				"NO_PROXY": "127.0.0.1,localhost,::1", "no_proxy": "127.0.0.1,localhost,::1",
			}
			if port > 0 {
				want["PORT"] = strconv.Itoa(int(port))
			}
			t.Run("positive_control", func(t *testing.T) {
				if s.PID != cmd.Process.Pid {
					t.Fatal("receipt did not come from the actual background child")
				}
				for key, value := range want {
					if s.Values[key] != value {
						t.Errorf("background control %s got %q want %q", key, s.Values[key], value)
					}
				}
			})
			for _, key := range s.Keys {
				if wu19envprobe.GenericAllowedKey(key) || key == "HOST" || key == "PORT" || key == "npm_config_cache" || key == "WU19_SITE_OPTION" {
					continue
				}
				// os/exec.Cmd.Env documents this mandatory Windows insertion.
				if runtime.GOOS == "windows" && key == "SYSTEMROOT" {
					continue
				}
				t.Errorf("FR-239: unexpected gateway/runner-only key %s reached background child; want absent", key)
			}
		})
	}
}
