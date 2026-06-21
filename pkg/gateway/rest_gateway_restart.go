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
	// falls back to the real re-exec.
	restart := a.restarter
	if restart == nil {
		// Resolve and validate the executable path BEFORE writing the 202. The
		// documented run mode is a bare `./omnipus gateway &` with NO supervisor,
		// so an executable-resolution failure after the ack would be a permanent
		// down with the client already told "restarting" — a post-ack silent
		// death. Surfacing it here returns a 500 (restart not attempted) instead.
		// Only the actual syscall.Exec (which cannot return on success) runs after
		// the ack.
		exe, err := os.Executable()
		if err != nil {
			slog.Error("gateway: self-restart could not resolve executable path; restart not attempted",
				"restart_id", restartID, "error", err)
			jsonErr(w, http.StatusInternalServerError,
				"could not resolve executable path; gateway not restarted")
			return
		}
		restart = func() { reExecProcess(exe) }
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

// reExecProcess re-execs the current process image at the pre-resolved exe path,
// preserving the original argv and environment (the supervisor-friendly path is
// the same binary with the same flags). syscall.Exec replaces the process image
// in-place, so open listeners are closed by the kernel and the new image binds
// them fresh on boot — there is no orphaned half-shutdown process.
//
// The executable path is resolved and validated by the handler BEFORE the 202
// ack (see HandleGatewayRestart), so this function only runs once a valid path
// is in hand. syscall.Exec returns only on failure; in that case we log the
// cause clearly and exit non-zero so a supervisor (if any) restarts us. With no
// supervisor this is still strictly better than a pre-ack failure: the client
// was already told the gateway is restarting.
func reExecProcess(exe string) {
	argv := os.Args
	env := os.Environ()
	slog.Warn("gateway: re-execing process", "executable", exe, "argv", argv)
	if err := syscall.Exec(exe, argv, env); err != nil {
		// Exec only returns on failure. Surface the cause clearly; exit non-zero
		// so a supervisor restarts us.
		slog.Error("gateway: syscall.Exec failed; exiting for supervisor",
			"executable", exe, "error", err)
		os.Exit(1)
	}
}
