//go:build linux

// Omnipus - Ultra-lightweight personal AI gateway
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Gateway memory guard (founder decision 2026-10-30, "memory-guard fix yes,
// applies also with sandbox off"): the gateway's PR_SET_DUMPABLE=0
// self-hardening (sandbox.HardenGatewaySelf) stops same-user programs from
// reading /proc/<gateway>/environ and /proc/<gateway>/mem, which hold the
// master key and the login token. The property does not depend on the
// sandbox mode or on which backend was selected, so applySandbox must apply
// it on every boot path: sandbox mode off, and the application-level
// fallback backend (old kernel, Docker on Fly), not only on the Landlock path.

package gateway

import (
	"testing"

	"golang.org/x/sys/unix"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// resetDumpable makes the test process dumpable again before the case and
// after it, so each case observes only what applySandbox itself does.
func resetDumpable(t *testing.T) {
	t.Helper()
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 1, 0, 0, 0); err != nil {
		t.Fatalf("fixture: prctl(PR_SET_DUMPABLE, 1): %v", err)
	}
	t.Cleanup(func() {
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 1, 0, 0, 0); err != nil {
			t.Errorf("cleanup: prctl(PR_SET_DUMPABLE, 1): %v", err)
		}
	})
	if got := dumpable(t); got != 1 {
		t.Fatalf("fixture: process must start dumpable, PR_GET_DUMPABLE=%d", got)
	}
}

func dumpable(t *testing.T) int {
	t.Helper()
	v, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("prctl(PR_GET_DUMPABLE): %v", err)
	}
	return v
}

func TestApplySandbox_MemoryGuard_AppliedOnEveryBootPath(t *testing.T) {
	cases := []struct {
		name    string
		cfgMode string
		cliMode string
	}{
		{name: "sandbox_mode_off_in_config", cfgMode: "off"},
		{name: "sandbox_mode_off_by_cli_flag", cfgMode: "enforce", cliMode: "off"},
		// FallbackBackend with enforce: the old-kernel / Docker-on-Fly path
		// that degrades to application-level enforcement.
		{name: "fallback_backend_enforce", cfgMode: "enforce"},
		{name: "fallback_backend_permissive", cfgMode: "permissive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDumpable(t)

			cfg := &config.Config{}
			cfg.Sandbox.Mode = config.SandboxMode(tc.cfgMode)
			_, err := applySandbox(SandboxApplyOptions{
				CLIMode:  tc.cliMode,
				Cfg:      cfg,
				HomePath: t.TempDir(),
				Backend:  sandbox.NewFallbackBackend(),
				GetEnv:   func(string) string { return "" },
			})
			if err != nil {
				t.Fatalf("applySandbox: %v", err)
			}
			if got := dumpable(t); got != 0 {
				t.Fatalf("gateway memory guard not applied on this boot path: PR_GET_DUMPABLE=%d, want 0 "+
					"(same-user programs could read /proc/<gateway>/environ)", got)
			}
		})
	}
}
