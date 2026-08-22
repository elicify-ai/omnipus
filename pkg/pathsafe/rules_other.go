// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package pathsafe

// activeRules is the name-shape rule set for every non-Windows build:
// Linux, macOS, and the BSDs. No Windows-shape rule applies, because no
// file this binary creates will ever be opened by the Win32 API on a path
// that could only exist here — a mount stores an immutable absolute host
// path, so a workspace copied to another OS is broken by the path, not by
// the filenames.
//
// Selection is by GOOS — this file's build constraint — and never by a
// custom build tag; see rules_windows.go for why that distinction is a
// safety property rather than a style preference.
var activeRules = POSIXRules
