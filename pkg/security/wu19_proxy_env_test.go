package security

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// FR-239: PrepareCmd's nil-environment fallback must not leak gateway values.
func TestWU19_ExecProxyChildEnvironment(t *testing.T) {
	if slices.Contains(os.Args, "--wu19-proxy") {
		got := map[string]string{}
		for _, key := range wu19ProxyKeys() {
			if value, ok := os.LookupEnv(key); ok {
				got[key] = value
			}
		}
		if err := json.NewEncoder(os.Stdout).Encode(got); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("OMNIPUS_HOME", filepath.Join(root, "data"))
	t.Setenv("OMNIPUS_CHILD_WU19_CONTROL", "allowed-control")
	for _, key := range wu19ProxyKeys()[7:] {
		t.Setenv(key, "wu19-innocuous-fixture")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := NewExecProxy(nil, nil) // no network request; only proxy env preparation.
	p.SetIdleTimeout(0)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	for _, preseeded := range []bool{false, true} {
		t.Run(fmt.Sprintf("preseeded_%t", preseeded), func(t *testing.T) {
			cmd := exec.Command(exe, "-test.run=^TestWU19_ExecProxyChildEnvironment$", "--", "--wu19-proxy")
			if preseeded {
				cmd.Env = []string{"HOME=" + root, "PATH=" + os.Getenv("PATH"), "OMNIPUS_CHILD_WU19_CONTROL=allowed-control"}
			}
			p.PrepareCmd(cmd)
			defer p.CmdDone()
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("positive control: proxy child failed: %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			t.Run("positive_control", func(t *testing.T) {
				if got["HOME"] != root || got["PATH"] != os.Getenv("PATH") || got["OMNIPUS_CHILD_WU19_CONTROL"] != "allowed-control" {
					t.Fatal("proxy child lost exact allowlisted controls")
				}
				for _, key := range wu19ProxyKeys()[3:7] {
					if got[key] != "http://"+p.Addr() {
						t.Fatalf("proxy addition %s is not the actual proxy address", key)
					}
				}
			})
			for _, key := range wu19ProxyKeys()[7:] {
				if _, exists := got[key]; exists {
					t.Errorf("FR-239: gateway-only key %s reached proxy child; want absent", key)
				}
			}
		})
	}
}

func wu19ProxyKeys() []string {
	return []string{"HOME", "PATH", "OMNIPUS_CHILD_WU19_CONTROL", "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "OPENROUTER_API_KEY", "EMAIL_PASSWORD", "OMNIPUS_BEARER_TOKEN", "WU19_FUTURE_GATEWAY_VALUE"}
}
