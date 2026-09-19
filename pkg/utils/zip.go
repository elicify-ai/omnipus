package utils

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

const maxZipEntryBytes int64 = 5 * 1024 * 1024

// ZipExtractionLimits bounds the resources consumed while extracting an archive.
type ZipExtractionLimits struct {
	MaxEntries       int
	MaxExpandedBytes int64
}

// ExtractZipFile extracts a ZIP archive from disk to targetDir.
// It reads entries one at a time from disk, keeping memory usage minimal.
//
// Security: rejects path traversal attempts and symlinks.
func ExtractZipFile(zipPath string, targetDir string) error {
	return ExtractZipFileWithLimits(zipPath, targetDir, ZipExtractionLimits{
		MaxEntries:       10_000,
		MaxExpandedBytes: 250 * 1024 * 1024,
	})
}

// ExtractZipFileWithLimits extracts an archive while bounding both entry count
// and cumulative uncompressed bytes. Each individual file retains the existing
// 5 MiB ceiling.
func ExtractZipFileWithLimits(zipPath string, targetDir string, limits ZipExtractionLimits) error {
	if limits.MaxEntries <= 0 || limits.MaxExpandedBytes <= 0 {
		return errors.New("invalid ZIP extraction limits")
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("invalid ZIP: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > limits.MaxEntries {
		return fmt.Errorf("ZIP has too many entries: %d exceeds %d", len(reader.File), limits.MaxEntries)
	}
	var declaredTotal uint64
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > uint64(maxZipEntryBytes) {
			return fmt.Errorf("zip entry %q is too large (%d bytes)", f.Name, f.UncompressedSize64)
		}
		if f.UncompressedSize64 > uint64(limits.MaxExpandedBytes)-declaredTotal {
			return fmt.Errorf("ZIP expanded size exceeds %d bytes", limits.MaxExpandedBytes)
		}
		declaredTotal += f.UncompressedSize64
	}

	logger.DebugCF("zip", "Extracting ZIP", map[string]any{
		"zip_path":   zipPath,
		"target_dir": targetDir,
		"entries":    len(reader.File),
	})

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create target dir: %w", err)
	}

	var expandedBytes int64
	for _, f := range reader.File {
		// Path traversal protection.
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return fmt.Errorf("zip entry has unsafe path: %q", f.Name)
		}

		destPath := filepath.Join(targetDir, cleanName)

		// Double-check the resolved path is within target directory (defense-in-depth).
		targetDirClean := filepath.Clean(targetDir)
		if !strings.HasPrefix(filepath.Clean(destPath), targetDirClean+string(filepath.Separator)) &&
			filepath.Clean(destPath) != targetDirClean {
			return fmt.Errorf("zip entry escapes target dir: %q", f.Name)
		}

		mode := f.FileInfo().Mode()

		// Reject any symlink.
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("zip contains symlink %q; symlinks are not allowed", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}

		// Ensure parent directory exists.
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}

		written, err := extractSingleFile(f, destPath, limits.MaxExpandedBytes-expandedBytes)
		if err != nil {
			return err
		}
		expandedBytes += written
	}

	return nil
}

// extractSingleFile extracts one zip.File entry to destPath, with a size check.
func extractSingleFile(f *zip.File, destPath string, remainingExpandedBytes int64) (int64, error) {
	// Check the uncompressed size from the header, if available.
	if f.UncompressedSize64 > uint64(maxZipEntryBytes) {
		return 0, fmt.Errorf("zip entry %q is too large (%d bytes)", f.Name, f.UncompressedSize64)
	}

	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("failed to open zip entry %q: %w", f.Name, err)
	}
	defer rc.Close()

	outFile, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("failed to create file %q: %w", destPath, err)
	}
	// We don't return the close error via return, since it's not a named error return.
	// Instead, we log to stderr and remove the partially written file as defensive cleanup.
	defer func() {
		if cerr := outFile.Close(); cerr != nil {
			_ = os.Remove(destPath)
			logger.ErrorCF("zip", "Failed to close file", map[string]any{
				"dest_path": destPath,
				"error":     cerr.Error(),
			})
		}
	}()

	// Streamed size check: prevent overruns and malicious/corrupt headers.
	written, err := copyZipEntry(outFile, rc, f.Name, remainingExpandedBytes)
	if err != nil {
		_ = os.Remove(destPath)
		return 0, err
	}

	return written, nil
}

func copyZipEntry(dst io.Writer, src io.Reader, name string, remainingExpandedBytes int64) (int64, error) {
	readLimit := min(maxZipEntryBytes, remainingExpandedBytes) + 1
	written, err := io.CopyN(dst, src, readLimit)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("failed to extract %q: %w", name, err)
	}
	if written > remainingExpandedBytes {
		return 0, fmt.Errorf("ZIP expanded size exceeds configured limit")
	}
	if written > maxZipEntryBytes {
		return 0, fmt.Errorf("zip entry %q exceeds max size (%d bytes)", name, written)
	}
	return written, nil
}
