// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package ui

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/elicify-ai/omnipus/pkg/daemon"
)

// omnipusHome returns the Omnipus home directory (~/.omnipus).
func omnipusHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".omnipus"
	}
	return home + "/.omnipus"
}

// getGatewayStatus returns the current gateway status by delegating to
// pkg/daemon. When daemon.Status returns an error (e.g. corrupt or locked PID
// file) the status is flagged with the error so callers can surface it rather
// than silently treating a broken PID file as "not running" — which would
// trigger a duplicate start and orphan the existing process.
func getGatewayStatus() gatewayStatus {
	running, pid, err := daemon.Status(omnipusHome())
	if err != nil {
		slog.Warn("daemon status error; treating as unknown — will not auto-start",
			"error", err)
		return gatewayStatus{running: false, statusErr: err}
	}
	return gatewayStatus{running: running, pid: pid}
}

type gatewayStatus struct {
	running   bool
	pid       int
	statusErr error // non-nil when daemon.Status itself failed
}

// startGateway spawns the Omnipus gateway via pkg/daemon. It resolves the
// `omnipus` binary from PATH (the launcher is a separate process from the
// main gateway binary) and delegates the detached spawn + PID file write to
// daemon.SpawnExe. Returns an error if the gateway status cannot be determined,
// if the gateway is already running, if `omnipus` cannot be found, or if the
// spawn fails.
func startGateway() error {
	status := getGatewayStatus()
	if status.statusErr != nil {
		// A status error means we cannot safely determine whether a gateway is
		// already running. Refuse to spawn to avoid orphaning a live process.
		return fmt.Errorf("cannot determine gateway status (PID file may be corrupt): %w",
			status.statusErr)
	}
	if status.running {
		return fmt.Errorf("gateway is already running (PID: %d)", status.pid)
	}

	// Resolve the omnipus binary from PATH; the launcher is a separate
	// binary so os.Executable() would point at the launcher, not omnipus.
	omnipusExe, err := exec.LookPath("omnipus")
	if err != nil {
		return fmt.Errorf("cannot find omnipus binary in PATH: %w", err)
	}

	home := omnipusHome()
	if _, err := daemon.SpawnExe(home, omnipusExe, nil); err != nil {
		return fmt.Errorf("failed to start gateway: %w", err)
	}
	return nil
}

// stopGateway stops the Omnipus gateway via pkg/daemon. It sends SIGTERM then
// SIGKILL (Unix) or taskkill (Windows) and removes the PID file. Returns an
// error if no gateway is running or if the termination fails.
func stopGateway() error {
	stopped, err := daemon.Stop(omnipusHome())
	if err != nil {
		return fmt.Errorf("failed to stop gateway: %w", err)
	}
	if !stopped {
		return fmt.Errorf("gateway is not running")
	}
	return nil
}

func (a *App) newGatewayPage() tview.Primitive {
	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	flex.SetBorder(true).
		SetTitle(" [#00f0ff::b] GATEWAY MANAGEMENT ").
		SetTitleColor(tcell.NewHexColor(0x00f0ff)).
		SetBorderColor(tcell.NewHexColor(0x00f0ff))
	flex.SetBackgroundColor(tcell.NewHexColor(0x050510))

	statusTV := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("Checking status...")
	statusTV.SetBackgroundColor(tcell.NewHexColor(0x050510))

	var updateStatus func()

	buttons := tview.NewList()
	buttons.SetBackgroundColor(tcell.NewHexColor(0x050510))
	buttons.SetMainTextColor(tcell.ColorWhite)
	buttons.SetSelectedBackgroundColor(tcell.NewHexColor(0xff00ff))
	buttons.SetSelectedTextColor(tcell.ColorBlack)

	buttons.AddItem(" [lime]START[white]   ", "", 0, func() {
		if !getGatewayStatus().running {
			err := startGateway()
			if err != nil {
				a.showError(err.Error())
			}
			updateStatus()
		}
	})
	buttons.AddItem(" [red]STOP[white]    ", "", 0, func() {
		if getGatewayStatus().running {
			err := stopGateway()
			if err != nil {
				a.showError(err.Error())
			}
			updateStatus()
		}
	})

	buttonFlex := tview.NewFlex().SetDirection(tview.FlexColumn)
	buttonFlex.
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(buttons, 20, 1, true).
		AddItem(tview.NewBox(), 0, 1, false)

	flex.
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(statusTV, 3, 1, false).
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(buttonFlex, 4, 1, true).
		AddItem(tview.NewBox(), 0, 1, false)

	updateStatus = func() {
		status := getGatewayStatus()
		switch {
		case status.statusErr != nil:
			// daemon.Status itself failed (corrupt/locked PID file). Show the
			// error in the status line so the operator can act, and disable
			// the START button to prevent a duplicate-spawn.
			statusTV.SetText(fmt.Sprintf(
				"[#ffaa00::b]STATUS ERROR[-]\n\n%s", status.statusErr))
			buttons.SetItemText(0, " [gray]START[white]   ", "")
			buttons.SetItemText(1, " [gray]STOP[white]    ", "")
		case status.running:
			statusTV.SetText(fmt.Sprintf("[#39ff14::b]GATEWAY RUNNING[-]\n\nPID: %d", status.pid))
			buttons.SetItemText(0, " [gray]START[white]   ", "")
			buttons.SetItemText(1, " [red]STOP[white]    ", "")
		default:
			statusTV.SetText("[#ff2a2a::b]GATEWAY STOPPED[-]\n\nPID: N/A")
			buttons.SetItemText(0, " [lime]START[white]   ", "")
			buttons.SetItemText(1, " [gray]STOP[white]    ", "")
		}
	}

	updateStatus()

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.tapp.QueueUpdateDraw(updateStatus)
			case <-done:
				return
			}
		}
	}()

	originalInputCapture := flex.GetInputCapture()
	flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			close(done)
			return a.goBack()
		}
		if originalInputCapture != nil {
			return originalInputCapture(event)
		}
		return event
	})

	a.pageRefreshFns["gateway"] = updateStatus

	return a.buildShell("gateway", flex, " [#39ff14]Enter:[-] select  [#ff2a2a]ESC:[-] back ")
}
