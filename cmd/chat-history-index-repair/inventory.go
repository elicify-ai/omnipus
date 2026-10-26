package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// inventoryEntry is private operator evidence. It is never read by the SPA.
//
// Kinds: "file" (hashed), "directory", "symlink" (target string recorded, never
// followed or opened) and "special" (socket, named pipe, device; never opened).
// Links and special files are only accepted outside sessions/.
type inventoryEntry struct { // not-wire-format
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256,omitempty"`
	LinkTarget  string `json:"link_target,omitempty"`
	SpecialType string `json:"special_type,omitempty"`
	Mtime       string `json:"mtime_unix_ns"`
	Mode        uint32 `json:"mode"`
}

// validInventoryPath is the platform-independent path rule: '/'-separated
// UTF-8 text (the manifest stores JSON text losslessly), no NUL, not absolute,
// and no empty, "." or ".." part. Every other character is accepted here; what
// the running OS cannot name is checked by nativeInventoryPath.
func validInventoryPath(path string) bool {
	if path == "" || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "/") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// nativeInventoryPath reports whether every part is a name the running OS can
// hold (nativeInventoryName). It fails, with its own refusal class, only for a
// manifest written on another OS, such as a macOS name with ':' read on Windows.
func nativeInventoryPath(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if !nativeInventoryName(part) {
			return false
		}
	}
	return true
}
func derivedInventoryPath(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) >= 3 && parts[0] == "sessions" && parts[1] != "" && parts[2] == session.ChatHistoryIndexDirectory
}

// strictInventoryPath reports whether path is the sessions/ subtree the tool
// reads canonically and writes into. Case-folded so a case-insensitive file
// system cannot route the product's sessions directory around the check.
func strictInventoryPath(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return strings.EqualFold(first, "sessions")
}

// specialFileType names a non-regular, non-directory, non-link file type.
func specialFileType(mode os.FileMode) string {
	switch {
	case mode&os.ModeNamedPipe != 0:
		return "named_pipe"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeDevice != 0 && mode&os.ModeCharDevice != 0:
		return "char_device"
	case mode&os.ModeDevice != 0:
		return "block_device"
	default:
		return "irregular"
	}
}

// linkOrSpecialEntry records a symbolic link or special file outside
// sessions/ from its own lstat metadata. A link is never followed and a
// special file is never opened (an open of a named pipe can block forever).
func linkOrSpecialEntry(path, rel string, d os.DirEntry) (inventoryEntry, error) {
	info, err := d.Info()
	if err != nil {
		return inventoryEntry{}, integrity("io_error", err)
	}
	e := inventoryEntry{Path: rel, Mode: uint32(info.Mode().Perm()), Mtime: strconv.FormatInt(info.ModTime().UnixNano(), 10)}
	switch mode := info.Mode(); {
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return e, integrity("io_error", err)
		}
		// The manifest stores the target as a JSON string; a target that
		// cannot round-trip losslessly is refused like a non-UTF-8 name.
		if target == "" || !utf8.ValidString(target) || strings.ContainsRune(target, 0) {
			return e, refusal("unsafe_path")
		}
		after, err := os.Lstat(path)
		if err != nil {
			return e, integrity("io_error", err)
		}
		if !os.SameFile(info, after) || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) {
			return e, refusal("source_changed")
		}
		e.Kind, e.LinkTarget = "symlink", target
	case mode.IsDir() || mode.IsRegular():
		// Directory entry type and lstat disagree: the entry changed under us.
		return e, refusal("source_changed")
	default:
		e.Kind, e.SpecialType = "special", specialFileType(mode)
	}
	return e, nil
}
func inventoryTree(ctx context.Context, root string, canonicalOnly bool) ([]inventoryEntry, error) {
	entries := make([]inventoryEntry, 0)
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return integrity("io_error", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return integrity("interrupted", err)
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !validInventoryPath(rel) || seen[strings.ToLower(rel)] {
			return refusal("unsafe_path")
		}
		if !nativeInventoryPath(rel) {
			return refusal("non_native_name")
		}
		seen[strings.ToLower(rel)] = true
		if !d.IsDir() && !d.Type().IsRegular() {
			// Links and special files are refused inside sessions/ (read
			// canonically and written by apply) and recorded elsewhere.
			if strictInventoryPath(rel) {
				return refusal("unsafe_kind")
			}
			e, err := linkOrSpecialEntry(path, rel, d)
			if err != nil {
				return err
			}
			entries = append(entries, e)
			return nil
		}
		if canonicalOnly && derivedInventoryPath(rel) {
			if !d.IsDir() {
				return refusal("unsafe_kind")
			}
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return integrity("io_error", err)
		}
		e := inventoryEntry{Path: rel, Mode: uint32(info.Mode().Perm()), Mtime: strconv.FormatInt(info.ModTime().UnixNano(), 10)}
		if info.IsDir() {
			e.Kind = "directory"
		} else if info.Mode().IsRegular() {
			e.Kind, e.Size = "file", info.Size()
			f, err := os.Open(path)
			if err != nil {
				return integrity("io_error", err)
			}
			opened, err := f.Stat()
			if err != nil {
				return errors.Join(err, f.Close())
			}
			if !os.SameFile(info, opened) {
				f.Close()
				return refusal("source_changed")
			}
			h := sha256.New()
			n, readErr := io.Copy(h, f)
			after, statErr := f.Stat()
			if err = errors.Join(readErr, statErr, f.Close()); err != nil {
				return integrity("io_error", err)
			}
			if n != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Mode() != info.Mode() {
				return refusal("source_changed")
			}
			e.SHA256 = hex.EncodeToString(h.Sum(nil))
		} else {
			return refusal("unsafe_kind")
		}
		entries = append(entries, e)
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, err
}
func canonicalFingerprint(entries []inventoryEntry) string {
	h := sha256.New()
	for _, e := range entries {
		if derivedInventoryPath(e.Path) {
			continue
		}
		mtime := e.Mtime
		if e.Kind == "directory" {
			mtime = ""
		}
		values := []string{e.Path, e.Kind, strconv.FormatInt(e.Size, 10), e.SHA256, mtime}
		// Kind precedes these fields, so the length-prefixed stream stays
		// unambiguous; file/directory tuples are unchanged from version 1.
		switch e.Kind {
		case "symlink":
			values = append(values, e.LinkTarget)
		case "special":
			values = append(values, e.SpecialType)
		}
		writeTuple(h, values...)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func inventoriesEqual(a, b []inventoryEntry, canonicalOnly bool) bool {
	filter := func(in []inventoryEntry) []inventoryEntry {
		out := make([]inventoryEntry, 0, len(in))
		for _, e := range in {
			if canonicalOnly && derivedInventoryPath(e.Path) {
				continue
			}
			if canonicalOnly && e.Kind == "directory" {
				e.Mtime = ""
			}
			out = append(out, e)
		}
		return out
	}
	a, b = filter(a), filter(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func inventoryCounts(entries []inventoryEntry) (files, directories, symlinks, special int) {
	for _, e := range entries {
		switch e.Kind {
		case "file":
			files++
		case "directory":
			directories++
		case "symlink":
			symlinks++
		default:
			special++
		}
	}
	return files, directories, symlinks, special
}
