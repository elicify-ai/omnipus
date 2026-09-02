// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"log/slog"
	"sync/atomic"
)

// defaultLeaseWaitSec is the wait before a browser tool call declares
// contention and refuses. Two seconds: long enough to absorb an in-flight call
// that is about to finish, short enough that a user watching a chat notices
// nothing.
const defaultLeaseWaitSec = 2

// defaultPageTimeoutSec mirrors pkg/tools/browser's own page-timeout default.
// It is the denominator the clamp uses when tools.browser.page_timeout is
// UNSET, which is the common case — clamping against a configured 0 would make
// the ceiling 0 and silently reduce every lease wait to nothing.
const defaultPageTimeoutSec = 30

// ClampLeaseWait returns the lease wait to actually use, given the configured
// lease_wait and page_timeout (both in seconds, both 0 when unset), and warns
// LOUDLY when it lowers an operator's configured value.
//
// THE CEILING IS HALF THE PAGE TIMEOUT, and the reasoning is worth stating
// because the number looks arbitrary. Waiting for the lease consumes the same
// budget the page operation itself needs. A lease wait at or above the page
// timeout means a call can spend its entire budget queueing and then time out
// having done nothing — the operator sees a page-timeout error and goes looking
// at the page, which is fine, when the real cause was contention. Half leaves
// the operation at least as long as it spent waiting.
//
// THE WARN IS PART OF THE REQUIREMENT, not a nicety. A silent clamp is a
// setting the operator believes took effect and did not — the ADR-037
// anti-pattern this project bans. It names BOTH keys and BOTH values, because
// "lease_wait was lowered" without the page_timeout that lowered it sends
// someone to change lease_wait again and watch it be lowered again.
func ClampLeaseWait(configuredLeaseWaitSec, configuredPageTimeoutSec int) int {
	leaseWait := configuredLeaseWaitSec
	if leaseWait <= 0 {
		leaseWait = defaultLeaseWaitSec
	}

	pageTimeout := configuredPageTimeoutSec
	if pageTimeout <= 0 {
		// Unset means the browser package's own default is in force, so that
		// is what the ceiling must be computed against. Using the configured 0
		// would make the ceiling 0 and reduce every lease wait to nothing —
		// a clamp that fires on every default install is not a clamp, it is a
		// second default nobody asked for.
		pageTimeout = defaultPageTimeoutSec
	}

	ceiling := pageTimeout / 2
	if ceiling < 1 {
		// A page_timeout of 1s. Keep at least a whole second of lease wait
		// rather than zero: zero would turn every concurrent call into an
		// instant contention refusal.
		ceiling = 1
	}

	if leaseWait <= ceiling {
		return leaseWait
	}

	if shouldLogLeaseWaitClampWarn() {
		slog.Warn("tools.browser.lease_wait is configured above half of tools.browser.page_timeout and has been lowered — waiting longer than this for the single-driver lease lets a call spend its whole budget queueing and then fail with a page-timeout error that points at the page rather than at contention",
			"configured_lease_wait_sec", configuredLeaseWaitSec,
			"page_timeout_sec", pageTimeout,
			"applied_lease_wait_sec", ceiling,
			"remedy", "raise tools.browser.page_timeout, or lower tools.browser.lease_wait to at most half of it")
	}
	return ceiling
}

// leaseWaitClampWarned makes the clamp WARN fire once per process.
//
// Once, not once-per-call: the clamp is re-applied on every config reload, and
// a reload can be triggered by any Settings save, so an unthrottled warning
// would accumulate a line per save for a condition that has not changed. It is
// re-armed on reload (see ResetLeaseWaitClampWarnForReload) so an operator who
// EDITS the value and saves does see the warning again for their new value —
// which is the moment they are actually looking.
var leaseWaitClampWarned atomic.Bool

func shouldLogLeaseWaitClampWarn() bool {
	return !leaseWaitClampWarned.Swap(true)
}

// ResetLeaseWaitClampWarnForReload re-arms the clamp warning. Called by the
// config-reload path so a re-clamp after an operator edits the value warns
// again — the alternative is an operator changing lease_wait, saving, seeing
// nothing in the log, and concluding it took effect.
func ResetLeaseWaitClampWarnForReload() {
	leaseWaitClampWarned.Store(false)
}

// EffectiveLeaseWaitSec is the clamp applied to a live config. It is what both
// the load path and the reload path call, so the two cannot drift.
func (c BrowserToolConfig) EffectiveLeaseWaitSec() int {
	return ClampLeaseWait(c.LeaseWaitSec, c.PageTimeoutSec)
}
