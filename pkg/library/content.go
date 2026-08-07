// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// MaxContentBytes bounds both the inline-editable size GET .../content will
// return content for (too_large=true beyond this) and the maximum body
// PUT .../content accepts — the two are the same threshold precisely so a
// file GET reports as editable is always one PUT can accept back
// (LibraryContentRequest.content's documented maxLength).
const MaxContentBytes = 10 * 1024 * 1024 // 10 MB

// sniffPrefixBytes bounds how much of a file is read to sniff is_text/mime
// for GET .../content — independent of, and much smaller than,
// MaxContentBytes.
const sniffPrefixBytes = 8192

// ContentResult is GET .../content's resolved payload, before the REST
// handler wraps it into the wire gen.LibraryContentResponse.
type ContentResult struct {
	Content  string
	IsText   bool
	Mime     string
	Size     int64
	TooLarge bool
}

// sniffText decides whether prefix (a bounded read of a file's start) should
// be treated as text, and returns a best-effort MIME type. A NUL byte
// anywhere in prefix is the primary binary signal (the same heuristic
// git/grep -I use) and is checked BEFORE any extension override, so a file
// with a text-like extension but genuinely binary content (e.g. a
// mislabeled .txt) is still correctly reported as non-text. Absent a NUL
// byte, a recognized text extension (including image/svg+xml — XML markup a
// model/editor can read as source) forces is_text=true regardless of what
// http.DetectContentType guesses. Otherwise falls back to
// http.DetectContentType's own "text/" prefix check.
func sniffText(prefix []byte, name string) (isText bool, mimeType string) {
	detected := http.DetectContentType(prefix)
	if bytes.IndexByte(prefix, 0) != -1 {
		return false, detected
	}
	ext := extOf(name)
	if textExtensions[ext] {
		if m, ok := extMimeTypes[ext]; ok {
			return true, m
		}
		return true, detected
	}
	return len(detected) >= 5 && detected[:5] == "text/", detected
}

// ReadContent reads rel (already-cleaned, non-root) for the Library
// editor/viewer. Binary content (per sniffText) is reported with
// Content == "" and IsText == false — never returned as a mangled string. A
// text file over MaxContentBytes is reported TooLarge with Content omitted;
// the size is still returned so the SPA can show it and offer the download
// fallback.
func (r *Root) ReadContent(rel string) (ContentResult, error) {
	fi, err := r.StatFile(rel)
	if err != nil {
		return ContentResult{}, err
	}
	size := fi.Size()

	f, err := r.root.Open(rel)
	if err != nil {
		return ContentResult{}, translateErr(err)
	}
	defer f.Close()

	prefix := make([]byte, sniffPrefixBytes)
	n, readErr := io.ReadFull(f, prefix)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return ContentResult{}, fmt.Errorf("library: sniff %q: %w", rel, readErr)
	}
	prefix = prefix[:n]
	isText, mimeType := sniffText(prefix, rel)

	result := ContentResult{IsText: isText, Mime: mimeType, Size: size}
	if !isText {
		return result, nil
	}
	if size > MaxContentBytes {
		result.TooLarge = true
		return result, nil
	}

	if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return ContentResult{}, fmt.Errorf("library: seek %q: %w", rel, seekErr)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxContentBytes+1))
	if err != nil {
		return ContentResult{}, fmt.Errorf("library: read %q: %w", rel, err)
	}
	switch {
	case int64(len(data)) > MaxContentBytes:
		// The file grew past the cap between Stat and this full read —
		// degrade to too_large rather than serving a torn read.
		result.TooLarge = true
	case !utf8.Valid(data):
		// Sniffed as text by the prefix heuristic but the full content is
		// not valid UTF-8 — do not lie to the caller with a mangled
		// string; report as non-text so the SPA falls back to download.
		result.IsText = false
	default:
		result.Content = string(data)
	}
	return result, nil
}

// WriteContent replaces (or creates) rel's content atomically (temp file +
// fsync + rename), mirroring pkg/tools/resolvepath.go's writeFileAtomicRoot.
// rel's parent directory must already exist (LibraryContentRequest's
// documented contract — this does not auto-create intermediate
// directories). Refuses to write onto an existing directory entry
// (ErrIsDir).
func (r *Root) WriteContent(rel string, content []byte) (os.FileInfo, error) {
	if err := r.requireParentDir(rel); err != nil {
		return nil, err
	}
	// Case-insensitive collision backstop (see caseInsensitiveMatch's doc)
	// is the SOLE existence check here, not a fallback reached only after
	// an exact-case os.Root.Stat "miss". FIX (real data-loss bug, not a
	// test-platform assumption): on a genuinely case-insensitive
	// filesystem (macOS's default APFS, Windows/NTFS-typical) an
	// exact-name r.root.Stat(rel) SUCCEEDS by case-folding onto a
	// DIFFERENTLY-CASED existing entry — indistinguishable from a true
	// exact match by return value alone. The previous version trusted that
	// success as "rel already exists under this exact name" and proceeded
	// straight to the in-place overwrite below, silently overwriting the
	// WRONG file's content on those hosts instead of ever reaching the
	// collision-rejection logic — a branch a case-folding Stat can never
	// take once ANY case variant already exists (os.IsNotExist(statErr)
	// was never true). Confirmed by the Cross-Platform CI matrix
	// (macos-latest, arm64)'s TestLibraryContent_CaseInsensitiveCollision_409
	// failure: the intended 409 never fired and the write silently landed
	// on "Report.txt" for a request naming "report.txt". A directory
	// listing name scan (caseInsensitiveMatch) already subsumes an
	// exact-case existence check — an exact match is trivially fold-equal —
	// so classifying the ACTUAL on-disk name it returns, rather than
	// trusting Stat's ambiguous success, is both sufficient and correct on
	// every host.
	match, found, ciErr := r.caseInsensitiveMatch(dirParent(rel), path.Base(rel))
	if ciErr != nil {
		return nil, ciErr
	}
	switch {
	case found && match == path.Base(rel):
		// The ACTUAL on-disk entry name (from the directory listing, not
		// the request) matches rel's exact casing — the normal
		// edit-and-save path (GET .../content returns entries by their
		// exact on-disk name, and a well-behaved caller echoes that same
		// name back to PUT), so proceed to the in-place overwrite below.
		existing, statErr := r.root.Stat(rel)
		if statErr != nil {
			return nil, translateErr(statErr)
		}
		if existing.IsDir() {
			return nil, ErrIsDir
		}
	case found:
		// A differently-cased sibling occupies this slot: this exact PUT
		// call would create a brand-new file on a case-sensitive host
		// (Linux) while silently overwriting the EXISTING file's content
		// on Windows/macOS instead — two different outcomes from the same
		// request depending on which OS happens to be running it. Reject
		// rather than guess which one the caller wanted; the caller can
		// resolve it explicitly (write to the existing entry's exact
		// name, or rename first).
		return nil, ErrAlreadyExists
	}
	// !found: no case-insensitive sibling at all — brand-new file, proceed.

	tmpRel := fmt.Sprintf("%s.tmp-%d-%d", rel, os.Getpid(), time.Now().UnixNano())
	f, err := r.root.OpenFile(tmpRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("library: create temp file: %w", err)
	}
	if _, writeErr := f.Write(content); writeErr != nil {
		f.Close()
		r.removeQuiet(tmpRel)
		return nil, fmt.Errorf("library: write temp file: %w", writeErr)
	}
	if syncErr := f.Sync(); syncErr != nil {
		f.Close()
		r.removeQuiet(tmpRel)
		return nil, fmt.Errorf("library: sync temp file: %w", syncErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		r.removeQuiet(tmpRel)
		return nil, fmt.Errorf("library: close temp file: %w", closeErr)
	}
	if renameErr := r.root.Rename(tmpRel, rel); renameErr != nil {
		r.removeQuiet(tmpRel)
		return nil, fmt.Errorf("library: rename temp file over target: %w", renameErr)
	}
	fi, err := r.root.Stat(rel)
	if err != nil {
		return nil, fmt.Errorf("library: stat written file: %w", err)
	}
	return fi, nil
}

// OpenFileForDownload opens rel for streaming (GET .../download). Returns
// ErrIsDir for a directory target, matching library-spec.md's "404 if path
// does not exist or names a directory" for this endpoint (the REST handler
// maps ErrIsDir the same as ErrNotFound here).
func (r *Root) OpenFileForDownload(rel string) (*os.File, os.FileInfo, error) {
	fi, err := r.StatFile(rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := r.root.Open(rel)
	if err != nil {
		return nil, nil, translateErr(err)
	}
	return f, fi, nil
}

// removeQuiet best-effort removes a temp file created by this package's own
// atomic-write helpers. A failure here is logged, never silently discarded
// — but it does not override the original error the caller is already
// returning: this only ever runs on an already-failing path, cleaning up
// dead weight (a zero-byte or partially-written temp file) rather than
// carrying the primary durability guarantee.
func (r *Root) removeQuiet(rel string) {
	if err := r.root.Remove(rel); err != nil && !os.IsNotExist(err) {
		logger.WarnCF("library", "failed to remove temp file after write error",
			map[string]any{"path": rel, "error": err.Error()})
	}
}
