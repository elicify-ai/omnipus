package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// FR-239: exercise the real hook handshake and inspect the child's environment,
// not a proposed scrub helper. Values are harmless fixtures, never real secrets.
func TestWU19_ProcessHookChildEnvironment(t *testing.T) {
	if i := slices.Index(os.Args, "--wu19-hook"); i >= 0 {
		wu19HookChild(os.Args[i+1])
		return
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USER", "wu19-fixture-user") // TestMain re-isolates HOME in a child.
	t.Setenv("OMNIPUS_HOME", filepath.Join(root, "data"))
	t.Setenv("OMNIPUS_CHILD_WU19_CONTROL", "allowed-control")
	denied := []string{"OPENROUTER_API_KEY", "OMNIPUS_MASTER_KEY", "OMNIPUS_BEARER_TOKEN", "EMAIL_PASSWORD", "WU19_FUTURE_GATEWAY_VALUE"}
	for _, key := range denied {
		t.Setenv(key, "wu19-innocuous-fixture")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, additions := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit_additions_%t", additions), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "child.json")
			opts := ProcessHookOptions{Command: []string{exe, "-test.run=^TestWU19_ProcessHookChildEnvironment$", "--", "--wu19-hook", path}}
			if additions {
				opts.Env = []string{"WU19_SITE_OPTION=hook-option"}
			}
			hook, err := NewProcessHook(context.Background(), "wu19", opts)
			if err != nil {
				t.Fatalf("positive control: hook handshake failed: %v", err)
			}
			defer func() {
				if closeErr := hook.Close(); closeErr != nil {
					t.Errorf("close hook child: %v", closeErr)
				}
			}()
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("child snapshot missing after handshake: %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			t.Run("positive_control", func(t *testing.T) {
				if got["USER"] != "wu19-fixture-user" || got["OMNIPUS_CHILD_WU19_CONTROL"] != "allowed-control" || got["PATH"] != os.Getenv("PATH") {
					t.Fatal("child did not retain exact USER, PATH and allowlisted control")
				}
				want := ""
				if additions {
					want = "hook-option"
				}
				if got["WU19_SITE_OPTION"] != want {
					t.Fatalf("explicit hook addition: got %q want %q", got["WU19_SITE_OPTION"], want)
				}
			})
			for _, key := range denied {
				if _, exists := got[key]; exists {
					t.Errorf("FR-239: gateway-only key %s reached hook child; want absent", key)
				}
			}
		})
	}
}

func wu19HookChild(path string) {
	keys := []string{"HOME", "USER", "PATH", "OMNIPUS_CHILD_WU19_CONTROL", "WU19_SITE_OPTION", "OPENROUTER_API_KEY", "OMNIPUS_MASTER_KEY", "OMNIPUS_BEARER_TOKEN", "EMAIL_PASSWORD", "WU19_FUTURE_GATEWAY_VALUE"}
	snapshot := make(map[string]string)
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			snapshot[key] = value
		}
	}
	b, err := json.Marshal(snapshot)
	if err == nil {
		err = os.WriteFile(path, b, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture snapshot failed")
		os.Exit(1)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID uint64 `json:"id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		if req.ID != 0 {
			if _, err := fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{}}\n", req.ID); err != nil {
				os.Exit(3)
			}
		}
	}
	if scanner.Err() != nil {
		os.Exit(4)
	}
	os.Exit(0)
}
