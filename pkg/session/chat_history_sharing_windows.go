// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package session

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isArchiveSharingBusy recognizes only a wrapped Windows sharing violation;
// generic access and permission failures are not treated as busy readers.
func isArchiveSharingBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
