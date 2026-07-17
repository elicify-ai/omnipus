// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// exec_resolver_test.go — ADR-044 sidecar-wiring T2 coverage for the
// PULSE_SERVER env guard in managedExecAllocatorOpts. Mirrors the existing
// DISPLAY guard behavior (headful-only): PULSE_SERVER must appear in the
// managed launch env if and only if the launch is VideoCapable AND a
// non-empty PulseServer was threaded through from the coordinator's audio
// sidecar (audioLaunchServer(), coordinator.go).

package browser

import "testing"

// envHasPulseServer reports whether env contains a PULSE_SERVER=<addr> entry
// equal to want.
func envHasPulseServer(env []string, want string) bool {
	for _, kv := range env {
		if kv == "PULSE_SERVER="+want {
			return true
		}
	}
	return false
}

// envHasAnyPulseServer reports whether env contains any PULSE_SERVER=...
// entry at all, regardless of value.
func envHasAnyPulseServer(env []string) bool {
	for _, kv := range env {
		if len(kv) >= len("PULSE_SERVER=") && kv[:len("PULSE_SERVER=")] == "PULSE_SERVER=" {
			return true
		}
	}
	return false
}

// TestManagedExecOpts_PulseServer_HeadfulOnlyGuard asserts the PULSE_SERVER
// env var is appended only on the headful (VideoCapable) path and only when
// PulseServer is non-empty — the same guard shape as the pre-existing DISPLAY
// var (exec_resolver.go). A stray PULSE_SERVER on the headless-shell path
// would make Chrome try to reach an audio sink it does not need; an empty
// PulseServer means the audio sidecar was never started or is unhealthy, so
// the launch must stay video-only rather than emit a broken env entry.
func TestManagedExecOpts_PulseServer_HeadfulOnlyGuard(t *testing.T) {
	cfg := BrowserConfig{ProfileDir: t.TempDir()}

	tests := []struct {
		name       string
		params     managedLaunchParams
		wantPulse  bool
		wantServer string
	}{
		{
			name:       "headful video-capable with healthy audio sidecar",
			params:     managedLaunchParams{VideoCapable: true, Display: ":99", PulseServer: "/run/omnipus/pulse.sock"},
			wantPulse:  true,
			wantServer: "/run/omnipus/pulse.sock",
		},
		{
			name:      "headful video-capable, no audio sidecar (empty PulseServer)",
			params:    managedLaunchParams{VideoCapable: true, Display: ":99", PulseServer: ""},
			wantPulse: false,
		},
		{
			name:      "headless-shell path never gets PULSE_SERVER even if set",
			params:    managedLaunchParams{VideoCapable: false, PulseServer: "/run/omnipus/pulse.sock"},
			wantPulse: false,
		},
		{
			name:      "headless-shell default (zero value)",
			params:    managedLaunchParams{},
			wantPulse: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmdline := managedExecAllocatorOpts(cfg, tc.params)
			got := envHasAnyPulseServer(cmdline.Env)
			if got != tc.wantPulse {
				t.Fatalf("PULSE_SERVER presence = %v, want %v (env=%v)", got, tc.wantPulse, cmdline.Env)
			}
			if tc.wantPulse && !envHasPulseServer(cmdline.Env, tc.wantServer) {
				t.Fatalf("expected PULSE_SERVER=%s in env, got %v", tc.wantServer, cmdline.Env)
			}
		})
	}
}
