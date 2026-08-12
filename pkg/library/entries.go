// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// List lists the entries directly inside rel (already-cleaned; "" means the
// work-tree root). Hidden entries (name begins with ".") are omitted unless
// includeHidden is true — the sole, explicit definition of "hidden" for the
// Library (library-spec.md D-8), applied here and ONLY here for listing; a
// directory's own hidden-ness does not affect whether ITS children are
// listed (asking to list ".library" directly, with includeHidden=false,
// still returns children whose own names do not start with a dot).
func (r *Root) List(rel string, includeHidden bool) ([]gen.LibraryEntry, error) {
	if _, err := r.StatDir(rel); err != nil {
		return nil, err
	}
	name := rel
	if name == "" {
		name = "."
	}
	rt, sub := r.resolve(name)
	dirEntries, err := fs.ReadDir(rt.FS(), sub)
	if err != nil {
		return nil, translateErr(err)
	}

	out := make([]gen.LibraryEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		hidden := strings.HasPrefix(de.Name(), ".")
		if hidden && !includeHidden {
			continue
		}
		info, infoErr := de.Info()
		if infoErr != nil {
			// A raced concurrent delete or a broken symlink target between
			// ReadDir and Info() — skip rather than failing the whole
			// listing over one entry.
			continue
		}
		childRel := de.Name()
		if rel != "" {
			childRel = rel + "/" + de.Name()
		}
		out = append(out, entryFromParts(childRel, de.Name(), info, hidden))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// EntryFromInfo builds the wire gen.LibraryEntry for rel from an
// already-stat'd FileInfo — shared by every operation that returns a single
// entry (write content, rename, move, copy) so the field-population logic
// (name split, is_hidden, is_text_editable hint, mime, timestamps) is never
// duplicated.
func EntryFromInfo(rel string, fi os.FileInfo) gen.LibraryEntry {
	name := path.Base(rel)
	hidden := strings.HasPrefix(name, ".")
	return entryFromParts(rel, name, fi, hidden)
}

func entryFromParts(rel, name string, fi os.FileInfo, hidden bool) gen.LibraryEntry {
	isDir := fi.IsDir()
	size := int64(0)
	if !isDir {
		size = fi.Size()
	}
	entry := gen.LibraryEntry{
		Path:       rel,
		Name:       name,
		IsDir:      isDir,
		IsHidden:   hidden,
		ModifiedAt: fi.ModTime().UTC(),
		Size:       size,
	}
	if !isDir {
		entry.IsTextEditable = isTextEditableExt(extOf(name))
		if m := mimeByExt(name); m != "" {
			mimeCopy := m
			entry.Mime = &mimeCopy
		}
	}
	return entry
}

func extOf(name string) string {
	return strings.ToLower(path.Ext(name))
}

// extMimeTypes is a small, deterministic, OS-independent extension→MIME
// table. Deliberately NOT backed by the stdlib "mime" package's
// TypeByExtension: that function additionally consults system MIME
// databases on Unix (e.g. /etc/mime.types), whose presence and contents
// vary between the dev pod and CI — a listing/content-sniff result must not
// depend on what happens to be installed on the host running Omnipus.
var extMimeTypes = map[string]string{
	".txt":      "text/plain",
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".csv":      "text/csv",
	".tsv":      "text/tab-separated-values",
	".json":     "application/json",
	".yaml":     "application/x-yaml",
	".yml":      "application/x-yaml",
	".xml":      "application/xml",
	".svg":      "image/svg+xml",
	".html":     "text/html",
	".htm":      "text/html",
	".toml":     "application/toml",
	".ini":      "text/plain",
	".cfg":      "text/plain",
	".conf":     "text/plain",
	".log":      "text/plain",
	".env":      "text/plain",
	".go":       "text/x-go",
	".py":       "text/x-python",
	".js":       "text/javascript",
	".ts":       "text/typescript",
	".tsx":      "text/tsx",
	".jsx":      "text/jsx",
	".rs":       "text/rust",
	".rb":       "text/x-ruby",
	".java":     "text/x-java",
	".c":        "text/x-c",
	".h":        "text/x-c",
	".cpp":      "text/x-c++",
	".cc":       "text/x-c++",
	".cxx":      "text/x-c++",
	".cs":       "text/x-csharp",
	".swift":    "text/x-swift",
	".kt":       "text/x-kotlin",
	".sh":       "text/x-shellscript",
	".bash":     "text/x-shellscript",
	".zsh":      "text/x-shellscript",
	".fish":     "text/x-shellscript",
	".ps1":      "text/x-powershell",
	".sql":      "application/sql",
	".graphql":  "application/graphql",
	".proto":    "text/x-protobuf",
	".tf":       "text/x-hcl",
	".hcl":      "text/x-hcl",
	".png":      "image/png",
	".jpg":      "image/jpeg",
	".jpeg":     "image/jpeg",
	".gif":      "image/gif",
	".webp":     "image/webp",
	".bmp":      "image/bmp",
	".ico":      "image/x-icon",
	".mp4":      "video/mp4",
	".webm":     "video/webm",
	".mov":      "video/quicktime",
	".mp3":      "audio/mpeg",
	".wav":      "audio/wav",
	".pdf":      "application/pdf",
	".zip":      "application/zip",
	".gz":       "application/gzip",
	".tar":      "application/x-tar",
	".docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".pptx":     "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".xlsx":     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

// textExtensions is the subset of extMimeTypes (plus a couple of source
// extensions with no distinct MIME entry, e.g. none currently) treated as
// CodeMirror-editable text (library-spec.md D-5 / section 4 scope table).
// Deliberately excludes binary formats that DO have an extMimeTypes entry
// (images, video, audio, archives, PDF, OOXML).
var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".tsv": true,
	".json": true, ".yaml": true, ".yml": true, ".xml": true, ".svg": true,
	".html": true, ".htm": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true,
	".log": true, ".env": true,
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".rs": true, ".rb": true, ".java": true, ".c": true, ".h": true, ".cpp": true,
	".cc": true, ".cxx": true, ".cs": true, ".swift": true, ".kt": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".ps1": true,
	".sql": true, ".graphql": true, ".proto": true, ".tf": true, ".hcl": true,
}

// extensionlessTextNames covers common well-known extension-less filenames
// so the directory-listing IsTextEditable hint is not universally false for
// them. Best-effort only (library-spec.md LibraryEntry.is_text_editable doc:
// "not a guarantee" — GET .../content's is_text/too_large are authoritative).
var extensionlessTextNames = map[string]bool{
	"Dockerfile": true, "Makefile": true, "Rakefile": true, "Gemfile": true,
	"Procfile": true, "LICENSE": true, "README": true, "CHANGELOG": true,
	"NOTICE": true, "AGENT.md": true,
}

// isTextEditableExt reports whether ext (as returned by extOf, i.e. always
// lowercased with the leading dot) identifies a format the SPA should offer
// for CodeMirror editing.
func isTextEditableExt(ext string) bool {
	return textExtensions[ext]
}

// mimeByExt returns a best-effort MIME type for name from the extension
// alone (no file content read) — used by List, which must stay cheap across
// a directory with many entries. Falls back to extensionlessTextNames for a
// handful of common no-extension filenames. Returns "" when nothing is
// known, per LibraryEntry.Mime's documented "absent... where sniffing was
// inconclusive."
func mimeByExt(name string) string {
	ext := extOf(name)
	if m, ok := extMimeTypes[ext]; ok {
		return m
	}
	if ext == "" && extensionlessTextNames[name] {
		return "text/plain"
	}
	return ""
}

// dirParent returns the parent directory of an already-cleaned rel path, in
// the same "" == work-tree-root convention every method in this package
// uses (never ".").
func dirParent(rel string) string {
	p := path.Dir(rel)
	if p == "." {
		return ""
	}
	return p
}

// requireParentDir confirms rel's parent directory already exists within
// root — the shared "parent directory must already exist" rule
// putLibraryContent, uploadLibraryFiles, and rename/move/copy destinations
// all share (library-spec.md; none of these operations auto-create
// intermediate directories beyond the work-tree root itself, which OpenRoot
// already guarantees). A missing parent is reported as
// *DestinationParentNotFoundError (not the bare ErrNotFound StatDir itself
// would return) so the REST layer can name the missing directory instead of
// a generic "not found" (UAT Issue 4) — Unwrap() still makes
// errors.Is(err, ErrNotFound) true for it.
func (r *Root) requireParentDir(rel string) error {
	parent := dirParent(rel)
	if _, err := r.StatDir(parent); err != nil {
		if errors.Is(err, ErrNotFound) {
			return &DestinationParentNotFoundError{Parent: parent}
		}
		return err
	}
	return nil
}
