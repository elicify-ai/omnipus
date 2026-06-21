//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_gateway_restart.go — O4-backend: UI-triggerable graceful self-restart.
//
// POST /api/v1/gateway/restart drains in-flight work then re-execs the process
// (or exits cleanly for a supervisor) so the SPA can apply restart-gated
// settings without a manual process bounce. The endpoint is high blast radius
// and is wired with the admin chain (withAuth → RequireAdmin → RequireNotBypass)
// at registration; dev_mode_bypass therefore returns 503.
//
// The actual re-exec is behind the restAPI.restarter indirection so the handler
// is unit-testable without killing the test process. Production uses
// gracefulSelfRestart (syscall.Exec, preserving argv + env).
package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"syscall"
	"time"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// selfRestartDrainSeconds is how long the gateway waits after acknowledging the
// restart request before re-execing, giving the HTTP response time to flush and
// in-flight requests a brief window to settle. Kept short so the SPA's down→up
// poll completes quickly.
const selfRestartDrainSeconds = 2

// HandleGatewayRestart handles POST /api/v1/gateway/restart (O4-backend).
//
// It acknowledges the request immediately (202 Accepted) with a
// GatewayRestartResponse the SPA polls on, then asynchronously drains for
// selfRestartDrainSeconds and triggers the restarter. The response is sent
// BEFORE the restart so the client always receives it.
//
// Admin + not-bypass are enforced by the adminWrap chain at registration; this
// handler only checks the HTTP method.
func (a *restAPI) HandleGatewayRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	restartID := fmt.Sprintf("restart-%d", time.Now().UnixNano())

	// Resolve the restart action. Tests inject a stub via a.restarter; production
	// falls back to the real re-exec. Resolve it BEFORE writing the response so a
	// nil/!nil decision is made on the request goroutine, then run it async.
	restart := a.restarter
	if restart == nil {
		restart = gracefulSelfRestart
	}

	msg := "Gateway is restarting; reconnecting shortly."
	resp := gen.GatewayRestartResponse{
		Status:       gen.Restarting,
		RestartId:    restartID,
		DrainSeconds: selfRestartDrainSeconds,
		Message:      &msg,
	}

	slog.Warn("gateway: self-restart requested via API",
		"restart_id", restartID, "drain_seconds", selfRestartDrainSeconds)

	// Acknowledge first so the SPA receives the response and can begin polling
	// /health for the down→up transition.
	jsonAccepted(w, resp)

	// Flush the response to the client before we start draining toward re-exec.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Drain + restart off the request goroutine so the handler returns and the
	// response is delivered. The brief drain lets in-flight requests settle.
	go func() {
		time.Sleep(selfRestartDrainSeconds * time.Second)
		slog.Warn("gateway: drain complete — restarting now", "restart_id", restartID)
		restart()
	}()
}

// gracefulSelfRestart re-execs the current process image, preserving the
// original argv and environment (the supervisor-friendly path is the same
// binary with the same flags). syscall.Exec replaces the process image
// in-place, so open listeners are closed by the kernel and the new image binds
// them fresh on boot — there is no orphaned half-shutdown process.
//
// On failure (e.g. the executable path can no longer be resolved) it logs and
// exits non-zero so a supervisor restarts it cleanly rather than leaving a
// wedged process.
func gracefulSelfRestart() {
	exe, err := os.Executable()
	if err != nil {
		slog.Error("gateway: self-restart could not resolve executable path; exiting for supervisor",
			"error", err)
		os.Exit(1)
	}
	argv := os.Args
	env := os.Environ()
	slog.Warn("gateway: re-execing process", "executable", exe, "argv", argv)
	if err := syscall.Exec(exe, argv, env); err != nil {
		// Exec only returns on failure. Exit non-zero so a supervisor restarts us.
		slog.Error("gateway: syscall.Exec failed; exiting for supervisor", "error", err)
		os.Exit(1)
	}
}
