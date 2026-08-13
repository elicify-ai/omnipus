//go:build !unix

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import "os"

// deviceID cannot be answered on this platform; see the unix build's doc. The
// caller falls back to scanning, which is the safe direction.
func deviceID(os.FileInfo) (uint64, bool) { return 0, false }
