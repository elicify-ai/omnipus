//go:build !linux && !darwin

// Fallback platform backend for filesystem watching (watch.go): every OS
// this project supports except Linux and macOS, foremost Windows.
//
// This is stated, not discovered (design §9's own framing, and the backend-
// lead task spec's explicit requirement): startPlatformWatch here always
// returns a *WatchUnavailableError before touching the filesystem at all, so
// Watcher.Start fails synchronously and Watcher.Unavailable() is closed
// immediately — a caller finds out it must rely entirely on its own periodic
// sweep the moment it tries to start watching, never partway through, and
// never by noticing search results are stale.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "runtime"

func startPlatformWatch(_ string, _ chan<- fsEvent, _ chan<- struct{}, _ <-chan struct{}) (<-chan error, error) {
	return nil, &WatchUnavailableError{
		Reason: "filesystem watching is not implemented on " + runtime.GOOS,
	}
}
