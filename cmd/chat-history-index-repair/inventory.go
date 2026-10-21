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
type inventoryEntry struct { // not-wire-format
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	Mtime  string `json:"mtime_unix_ns"`
	Mode   uint32 `json:"mode"`
}

func validInventoryPath(path string) bool {
	if path == "" || !utf8.ValidString(path) || strings.ContainsAny(path, "\\:\x00") || strings.HasPrefix(path, "/") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, " .") != part {
			return false
		}
	}
	return true
}
func derivedInventoryPath(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) >= 3 && parts[0] == "sessions" && parts[1] != "" && parts[2] == session.ChatHistoryIndexDirectory
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
		seen[strings.ToLower(rel)] = true
		if d.Type()&os.ModeSymlink != 0 {
			return refusal("unsafe_kind")
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
		writeTuple(h, e.Path, e.Kind, strconv.FormatInt(e.Size, 10), e.SHA256, mtime)
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
func inventoryCounts(entries []inventoryEntry) (files, directories int) {
	for _, e := range entries {
		if e.Kind == "file" {
			files++
		} else {
			directories++
		}
	}
	return
}
