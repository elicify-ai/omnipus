package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/tests/wu19envprobe"
)

// Characterization test: founder dismissal 2026-10-14, accepted risk.
// God programs see the operator's provider/mailbox/unknown values. The current
// three-key gateway boot mask remains pinned; this is NOT protection acceptance.
func TestWU19_GodModeNativeChildEnvironmentCharacterization(t *testing.T) {
	wu19NativeChildEnvironments(t, true)
}

// FR-239: no God exception for any non-God foreground/background/setup child.
func TestWU19_NonGodNativeChildEnvironmentsUseAllowlist(t *testing.T) {
	wu19NativeChildEnvironments(t, false)
}

func wu19NativeChildEnvironments(t *testing.T, god bool) {
	if i := slices.Index(os.Args, "--wu19-tools-snapshot"); i >= 0 {
		if err := wu19envprobe.Save(os.Args[i+1]); err != nil {
			os.Exit(90)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	home, work := filepath.Join(root, "data"), filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("USER", "wu19-fixture-user")
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_CHILD_WU19_CONTROL", "allowed-control")
	boot := []string{"OMNIPUS_MASTER_KEY", "OMNIPUS_KEY_FILE", "OMNIPUS_BEARER_TOKEN"}
	for _, key := range append(wu19envprobe.DeniedKeys(), boot...) {
		t.Setenv(key, "wu19-innocuous-fixture")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithTranscriptSessionID(WithAgentID(t.Context(), "wu19-fixture-agent"), "wu19-fixture-session")
	testName := strings.Split(t.Name(), "/")[0]
	for _, site := range []string{"foreground", "background", "environment_setup"} {
		t.Run(site, func(t *testing.T) {
			path := filepath.Join(work, site+".json")
			command := wu19CommandQuote(exe) + " -test.run=^" + testName + "$ -- --wu19-tools-snapshot " + wu19CommandQuote(path)
			result := wu19RunNativeEnvProbe(t, ctx, home, work, command, site, god)
			if result.IsError {
				t.Fatalf("native %s child failed: %s", site, result.ForLLM)
			}
			s, err := wu19envprobe.Read(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("positive_control", func(t *testing.T) { wu19NativeEnvControls(t, s, root, work, site) })
			allowed := map[string]bool{"npm_config_cache": true}
			if site == "environment_setup" {
				for _, key := range []string{"OMNIPUS_ENV_PREFIX", "OMNIPUS_ENV_CACHE", "OMNIPUS_ENV_TMP", "PIP_CACHE_DIR"} {
					allowed[key] = true
				}
			}
			if runtime.GOOS != "windows" {
				for _, key := range []string{"PWD", "SHLVL", "_"} { // sh-generated values, not inherited gateway data.
					allowed[key] = true
				}
			}
			if god {
				for _, entry := range os.Environ() {
					key, _, _ := strings.Cut(entry, "=")
					allowed[key] = !slices.Contains(boot, key)
				}
				for _, key := range wu19envprobe.DeniedKeys() {
					if s.Values[key] != "wu19-innocuous-fixture" {
						t.Errorf("accepted-risk characterization: God %s did not retain current provider/mailbox/unknown fixture %s", site, key)
					}
				}
			}
			for _, key := range s.Keys {
				if wu19envprobe.GenericAllowedKey(key) || allowed[key] || (runtime.GOOS == "windows" && key == "SYSTEMROOT") {
					continue
				}
				t.Errorf("FR-239/characterization: God=%t %s child has unexpected key %s", god, site, key)
			}
		})
	}
}

func wu19RunNativeEnvProbe(t *testing.T, ctx context.Context, home, work, command, site string, god bool) *ToolResult {
	t.Helper()
	done := make(chan *ToolResult, 1)
	var result *ToolResult
	if site == "environment_setup" {
		prefix := filepath.Join(work, "setup-prefix")
		target := &lifecycleTarget{prefix: prefix, cache: filepath.Join(prefix, "cache"), tmp: filepath.Join(prefix, "tmp")}
		for _, dir := range []string{target.prefix, target.cache, target.tmp} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{Home: home, AgentWorkDir: work, GodMode: god, Store: lifecycleStore{target: target}})
		result = tool.ExecuteAsync(ctx, map[string]any{"command": command, "purpose": "child environment receipt", "scope": "workspace", "timeout_seconds": float64(20)}, func(_ context.Context, r *ToolResult) { done <- r })
	} else {
		tool, err := NewExecToolWithDeps(work, false, nil, ExecToolDeps{GodMode: god})
		if err != nil {
			t.Fatal(err)
		}
		result = tool.ExecuteAsync(ctx, map[string]any{"command": command, "run_in_background": site == "background", "timeout_seconds": float64(20)}, func(_ context.Context, r *ToolResult) { done <- r })
	}
	if result.IsError {
		t.Fatalf("native %s child failed to start: %s", site, result.ForLLM)
	}
	if site != "foreground" {
		select {
		case result = <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("native child completion callback missing; no environment verdict")
		}
	}
	return result
}

func wu19NativeEnvControls(t *testing.T, s wu19envprobe.Snapshot, root, work, site string) {
	t.Helper()
	want := map[string]string{"HOME": root, "PATH": os.Getenv("PATH"), "USER": "wu19-fixture-user", "OMNIPUS_CHILD_WU19_CONTROL": "allowed-control"}
	if site == "environment_setup" {
		prefix := filepath.Join(work, "setup-prefix")
		parts := []string{filepath.Join(prefix, "bin"), want["PATH"]}
		if runtime.GOOS == "windows" {
			parts = append([]string{filepath.Join(prefix, "Scripts")}, parts...)
		}
		want["PATH"] = strings.Join(parts, string(os.PathListSeparator))
		for key, value := range map[string]string{"OMNIPUS_ENV_PREFIX": prefix, "OMNIPUS_ENV_CACHE": filepath.Join(prefix, "cache"), "OMNIPUS_ENV_TMP": filepath.Join(prefix, "tmp"), "TMPDIR": filepath.Join(prefix, "tmp"), "npm_config_cache": filepath.Join(prefix, "cache"), "PIP_CACHE_DIR": filepath.Join(prefix, "cache")} {
			want[key] = value
		}
	}
	for key, value := range want {
		if s.Values[key] != value {
			t.Errorf("native %s control %s got %q want %q", site, key, s.Values[key], value)
		}
	}
}

func wu19CommandQuote(value string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return fmt.Sprintf("'%s'", strings.ReplaceAll(value, "'", "'\"'\"'"))
}
