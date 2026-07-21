// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package fileutil provides file manipulation utilities.
package fileutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// WriteFileAtomic atomically writes data to a file using a temp file + rename pattern.
//
// This guarantees that the target file is either:
// - Completely written with the new data
// - Unchanged (if any step fails before rename)
//
// The function:
// 1. Creates a temp file in the same directory (original untouched)
// 2. Writes data to temp file
// 3. Syncs data to disk (critical for SD cards/flash storage)
// 4. Sets file permissions
// 5. Syncs directory metadata (ensures rename is durable)
// 6. Atomically renames temp file to target path
//
// Safety guarantees:
// - Original file is NEVER modified until successful rename
// - Temp file is always cleaned up on error
// - Data is flushed to physical storage before rename
// - Directory entry is synced to prevent orphaned inodes
//
// Parameters:
//   - path: Target file path
//   - data: Data to write
//   - perm: File permission mode (e.g., 0o600 for secure, 0o644 for readable)
//
// Returns:
//   - Error if any step fails, nil on success
//
// Example:
//
//	// Secure config file (owner read/write only)
//	err := utils.WriteFileAtomic("config.json", data, 0o600)
//
//	// Public readable file
//	err := utils.WriteFileAtomic("public.txt", data, 0o644)
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create temp file in the same directory (ensures atomic rename works).
	// Using a hidden prefix (".tmp-") and os.CreateTemp's crypto-random suffix.
	//
	// History: previous versions used ".tmp-<pid>-<nano>" which collided on
	// macOS arm64 — `time.Now().UnixNano()` resolution is bounded by the
	// system clock backing, and WithFlock locks the inode rather than the
	// path, so os.Rename swaps the inode and lets two writers run
	// WriteFileAtomic concurrently. CreateTemp's retry loop with random
	// suffix is collision-proof regardless of clock resolution.
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}

	tmpPath := tmpFile.Name()
	cleanup := true

	defer func() {
		if cleanup {
			tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	// Write data to temp file
	// Note: Original file is untouched at this point
	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// CRITICAL: Force sync to storage medium before any other operations.
	// This ensures data is physically written to disk, not just cached.
	// Essential for SD cards, eMMC, and other flash storage on edge devices.
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	// Set file permissions before closing
	if err := tmpFile.Chmod(perm); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Close file before rename (required on Windows)
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename: temp file becomes the target
	// On POSIX: rename() is atomic
	// On Windows: Rename() is atomic for files
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Sync directory to ensure rename is durable.
	// This prevents the renamed file from disappearing after a crash.
	if dirFile, err := os.Open(dir); err == nil {
		if syncErr := dirFile.Sync(); syncErr != nil {
			logger.SlogWarn("fileutil: dir sync after write failed", "dir", dir, "error", syncErr)
		}
		dirFile.Close()
	}

	// Success: skip cleanup (file was renamed, no temp to remove)
	cleanup = false
	return nil
}

func CopyFile(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("fileutil: read source file %q: %w", src, err)
	}
	return WriteFileAtomic(dst, data, perm)
}

// AppendJSONL appends a single JSON-encoded record followed by a newline to a
// JSONL file. The file is opened with O_APPEND|O_CREATE.
//
// Atomicity note: on Linux, O_APPEND causes each write(2) to atomically seek to
// end-of-file before writing, so short concurrent writes from different goroutines
// will not interleave. However, this guarantee applies only to writes within the
// PIPE_BUF limit (~4 KB on most systems) and only when all writers open the same
// underlying file description. For production usage, callers should use a
// single-writer goroutine or explicit locking when strict ordering is required.
//
// The directory is created if it does not exist.
func AppendJSONL(path string, record any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fileutil: create dir for jsonl: %w", err)
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("fileutil: marshal jsonl record: %w", err)
	}
	// Append record + newline in one write to stay atomic on Linux.
	// Use explicit allocation to avoid mutating the backing array of data.
	//
	// Cap the per-record size at 1 GiB so the make() below cannot integer-
	// overflow on a 32-bit build (CodeQL go/allocation-size-overflow) and
	// so a runaway encoder cannot exhaust memory. JSONL records that hit
	// this cap indicate a serious upstream bug; fail loudly.
	const maxJsonlRecord = 1 << 30
	if len(data) > maxJsonlRecord {
		return fmt.Errorf("fileutil: jsonl record (%d bytes) exceeds %d cap", len(data), maxJsonlRecord)
	}
	line := make([]byte, len(data)+1)
	copy(line, data)
	line[len(data)] = '\n'

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("fileutil: open jsonl file: %w", err)
	}

	if _, err := f.Write(line); err != nil {
		f.Close()
		return fmt.Errorf("fileutil: append jsonl record: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("fileutil: sync jsonl file: %w", err)
	}
	return f.Close()
}
