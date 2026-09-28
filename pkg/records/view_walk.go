// Omnipus — library-views-anywhere-spec §4 step 1, in-package walker.
//
// pkg/records.LoadViews is the lone public shim that has to discover
// `.view` files without depending on pkg/knowledge (MAJ-008's import
// cycle: pkg/records ← pkg/knowledge, but pkg/knowledge → pkg/records).
// The canonical walker is pkg/knowledge.DiscoverViewFiles; this in-package
// walker is a smaller, view-only mirror that honours the SAME rules:
//   - the four control-plane names (.omnipus-vault, .obsidian, .git, .trash)
//     are skipped at any depth;
//   - nested-vault directories (a child carrying its own .obsidian /
//     .omnipus-vault marker) stop the walk (D-WALK's round-2 rule,
//     R2-MIN-012);
//   - symlinks are NEVER followed (FR-044);
//   - file names are matched case-insensitively against ".view";
//   - the file is read with no-follow semantics and a 256 KiB cap
//     (D-SYMLINK-READ + D-SIZECAP, same as the canonical walker).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func walkViewFiles(vaultRoot string) ([]ViewFileBytes, []string, error) {
	if vaultRoot == "" {
		return nil, nil, errors.New("records.walkViewFiles: empty vault root")
	}
	abs, err := filepath.Abs(vaultRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("records.walkViewFiles: resolve %q: %w", vaultRoot, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, nil, fmt.Errorf("records.walkViewFiles: stat %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("records.walkViewFiles: %q is not a directory", abs)
	}

	stopDirs := map[string]struct{}{}
	var files []string
	var skipped []string

	stack := []string{abs}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rel, _ := filepath.Rel(abs, cur)
		if rel == "" {
			rel = "."
		}
		if rel != "." {
			if _, stop := stopDirs[filepath.ToSlash(rel)]; stop {
				continue
			}
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			skipped = append(skipped, filepath.ToSlash(rel))
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			full := filepath.Join(cur, name)
			if e.IsDir() {
				switch name {
				case ".omnipus-vault", ".obsidian", ".git", ".trash":
					continue
				}
				if carriesVaultMarker(full) {
					childRel := filepath.ToSlash(filepath.Join(rel, name))
					if rel == "." {
						childRel = name
					}
					stopDirs[childRel] = struct{}{}
					continue
				}
				stack = append(stack, full)
				continue
			}
			if !e.Type().IsRegular() {
				continue
			}
			if !strings.EqualFold(filepath.Ext(name), ".view") {
				continue
			}
			if name == ".view" {
				continue
			}
			files = append(files, full)
		}
	}
	sort.Strings(files)

	cap := int64(MaxViewFileBytes)
	out := make([]ViewFileBytes, 0, len(files))
	for _, absPath := range files {
		vf := readViewFileNoFollow(absPath, cap)
		out = append(out, vf)
	}
	sort.Strings(skipped)
	return out, skipped, nil
}

func carriesVaultMarker(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		switch e.Name() {
		case ".obsidian", ".omnipus-vault":
			return true
		}
	}
	return false
}

func readViewFileNoFollow(abs string, cap int64) ViewFileBytes {
	walkInfo, err := os.Lstat(abs)
	if err != nil {
		return ViewFileBytes{
			Path:      abs,
			Rejection: string(RejectViewUnreadable),
			Err:       fmt.Errorf("lstat view file: %w", err),
		}
	}
	if !walkInfo.Mode().IsRegular() {
		return ViewFileBytes{
			Path:      abs,
			Rejection: string(RejectViewUnreadable),
			Err:       fmt.Errorf("view file is not a regular file"),
		}
	}

	f, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return ViewFileBytes{
			Path:      abs,
			Rejection: string(RejectViewUnreadable),
			Err:       fmt.Errorf("open view file: %w", err),
		}
	}
	defer f.Close()

	fdInfo, ferr := f.Stat()
	if ferr != nil {
		return ViewFileBytes{
			Path:      abs,
			Rejection: string(RejectViewUnreadable),
			Err:       fmt.Errorf("stat opened view file: %w", ferr),
		}
	}
	if !sameFileStat(walkInfo, fdInfo) {
		return ViewFileBytes{
			Path:      abs,
			Rejection: string(RejectViewUnreadable),
			Err:       errors.New("view file was swapped for a different file between walk and read"),
		}
	}

	buf := &bytes.Buffer{}
	_, err = io.CopyN(buf, f, cap+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return ViewFileBytes{
			Path:      abs,
			Rejection: string(RejectViewUnreadable),
			Err:       fmt.Errorf("read view file: %w", err),
		}
	}
	data := buf.Bytes()
	rejection := ""
	if int64(len(data)) > cap {
		data = data[:cap]
		rejection = string(RejectViewTooLarge)
	}
	return ViewFileBytes{
		Path:      abs,
		Bytes:     data,
		Rejection: rejection,
	}
}

func sameFileStat(a, b fs.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	if sa, ok := a.Sys().(*syscall.Stat_t); ok {
		if sb, ok := b.Sys().(*syscall.Stat_t); ok {
			if sa.Dev != 0 && sb.Dev != 0 && sa.Ino != 0 && sb.Ino != 0 {
				return sa.Dev == sb.Dev && sa.Ino == sb.Ino
			}
		}
	}
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}