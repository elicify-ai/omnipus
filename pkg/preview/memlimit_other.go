// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !linux

package preview

// HostMemoryLimitUnenforced: the data-limit check is Linux only (macOS
// memory is best effort and disclosed in the docs; Windows uses a job).
func HostMemoryLimitUnenforced() (reason, note string) { return "", "" }
