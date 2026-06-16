// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !linux

package config

// readMemTotalBytes returns a conservative 4 GB constant on non-Linux platforms.
// The auto-detect heuristic in autoDetectMaxParallel will still be bounded by
// NumCPU, so the CPU-based floor (max 16) prevents runaway fan-out.
func readMemTotalBytes() uint64 {
	return 4 * 1024 * 1024 * 1024 // 4 GB
}
