//go:build !unix

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import "os"

// linkCount cannot be answered on this platform: os.FileInfo.Sys() returns no
// link count outside unix (on Windows it is a *syscall.Win32FileAttributeData,
// which carries attributes and timestamps only).
//
// The consequence is stated plainly rather than hidden behind a zero value:
// on non-unix platforms the hard-link scan does not run, so a hard link whose
// target lies inside a secret DIRECTORY is not detected by the app layer there.
// See aliasesSecretDirectory's "Residual" section for why the alternative —
// scanning unconditionally, for every path check, because the gate cannot be
// evaluated — was rejected.
func linkCount(_ os.FileInfo) (uint64, bool) { return 0, false }
