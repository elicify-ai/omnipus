//go:build linux || darwin

package cdppipe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// FR-239: launch.start is real; only Chromium is replaced at the process edge.
func TestWU19_BrowserChildEnvironment(t *testing.T) {
	if i := slices.Index(os.Args, "--wu19-browser"); i >= 0 {
		got := map[string]string{}
		for _, key := range wu19BrowserKeys() {
			if value, ok := os.LookupEnv(key); ok {
				got[key] = value
			}
		}
		b, err := json.Marshal(got)
		if err == nil {
			err = os.WriteFile(os.Args[i+1], b, 0o600)
		}
		if err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("OMNIPUS_HOME", filepath.Join(root, "data"))
	t.Setenv("OMNIPUS_CHILD_WU19_CONTROL", "allowed-control")
	for _, key := range wu19BrowserKeys()[4:] {
		t.Setenv(key, "wu19-innocuous-fixture")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, additions := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit_additions_%t", additions), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "child.json")
			l := &launch{execPath: exe, opts: PipeOptions{UserDataDir: t.TempDir(), ModifyCmd: func(cmd *exec.Cmd) {
				// Replace only argv, never the environment being tested.
				cmd.Args = []string{exe, "-test.run=^TestWU19_BrowserChildEnvironment$", "--", "--wu19-browser", path}
			}}}
			if additions {
				l.opts.Env = []string{"WU19_SITE_OPTION=browser-option"}
			}
			if err := l.start(context.Background()); err != nil {
				t.Fatalf("launch child: %v", err)
			}
			t.Cleanup(l.teardown)
			<-l.waitDone
			if l.waitErr != nil {
				t.Fatalf("positive control: snapshot child failed: %v", l.waitErr)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]string
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			t.Run("positive_control", func(t *testing.T) {
				if got["HOME"] != root || got["PATH"] != os.Getenv("PATH") || got["OMNIPUS_CHILD_WU19_CONTROL"] != "allowed-control" {
					t.Fatal("browser child lost exact allowlisted controls")
				}
				want := ""
				if additions {
					want = "browser-option"
				}
				if got["WU19_SITE_OPTION"] != want {
					t.Fatalf("explicit browser addition: got %q want %q", got["WU19_SITE_OPTION"], want)
				}
			})
			for _, key := range wu19BrowserKeys()[4:] {
				if _, exists := got[key]; exists {
					t.Errorf("FR-239: gateway-only key %s reached browser child; want absent", key)
				}
			}
		})
	}
}

func wu19BrowserKeys() []string {
	return []string{"HOME", "PATH", "OMNIPUS_CHILD_WU19_CONTROL", "WU19_SITE_OPTION", "OPENROUTER_API_KEY", "EMAIL_PASSWORD", "OMNIPUS_BEARER_TOKEN", "WU19_FUTURE_GATEWAY_VALUE"}
}
