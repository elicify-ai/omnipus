// Omnipus — knowledge base freshness manifest (ADR-067 stage 2, unit B2).
//
// The manifest is what makes indexing incremental (FR-033): it records what the
// index already knows about every file — size, modification time, content hash
// and, for a segmented note, how many index documents it produced — so that
// reopening a collection re-parses only what actually changed, and reopening an
// unchanged collection re-parses nothing at all (FR-039: persist across
// restarts without rebuilding).
//
// It lives beside the index under $OMNIPUS_HOME, never inside the operator's
// collection (FR-030), and is written 0600 (FR-032).
//
// The three freshness criteria, and why they are not equals:
//
//   - size and mtime are free — one Lstat per file, which is what MV-4's
//     "reconcile 100,000 unchanged files in under 2 seconds" budget can afford.
//     A file whose size AND mtime both match its record is unchanged and is not
//     read.
//   - the content hash costs a full read, so it is never used to *find* changed
//     files. It is used to *confirm* them: when size or mtime moved, the file is
//     read once (hashed in the same streaming pass that produces its index
//     documents) and, if the hash is unchanged after all, the re-parse is
//     discarded and only the stat facts are refreshed. That is what stops a
//     `touch`, a restore-from-backup, or a sync client rewriting identical bytes
//     from churning the index.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ManifestFileName is the manifest's filename inside the index directory.
const ManifestFileName = "manifest.json"

// manifestVersion is bumped when the recorded shape changes in a way that makes
// old records unusable. A manifest with an unrecognised version is discarded
// (treated as absent), which costs one rebuild and never produces wrong answers.
const manifestVersion = 1

// ManifestEntry is what the index knows about one file.
type ManifestEntry struct {
	// Path is the collection-relative, slash-separated path.
	Path string `json:"path"`
	// Kind is note or attachment.
	Kind ScanKind `json:"kind"`
	// Size is the size in bytes at the time of indexing.
	Size int64 `json:"size"`
	// ModTimeNanos is the modification time in Unix nanoseconds at the time of
	// indexing.
	ModTimeNanos int64 `json:"mtime_ns"`
	// Hash is the hex SHA-256 of the file's contents. It is EMPTY for an
	// attachment, and that emptiness is a requirement, not an omission: FR-039a
	// forbids opening an attachment for any reason, and a hash is a reason.
	Hash string `json:"hash,omitempty"`
	// Segments is how many index documents the file produced. A note over
	// IndexSegmentSize produces several (FR-034a); everything else produces
	// one. It is recorded because deleting a file from the index means deleting
	// every document it produced, and only the manifest remembers how many
	// there were.
	Segments int `json:"segments"`
}

// Manifest is the persisted record of an indexed collection.
type Manifest struct {
	// Version is manifestVersion.
	Version int `json:"version"`
	// Root is the collection root's resolved real path (FR-031). It is
	// recorded so a manifest found under the wrong index directory is
	// detectable rather than silently applied to the wrong collection.
	Root string `json:"root"`
	// Entries is keyed by collection-relative path.
	Entries map[string]ManifestEntry `json:"entries"`
}

// NewManifest returns an empty manifest for a collection root.
func NewManifest(root string) *Manifest {
	return &Manifest{
		Version: manifestVersion,
		Root:    root,
		Entries: make(map[string]ManifestEntry),
	}
}

// LoadManifest reads the manifest at path.
//
// A missing manifest is not an error — it is a first run, and returns an empty
// manifest for root. A manifest that is corrupt, of an unrecognised version, or
// recorded against a different collection root is also returned empty, together
// with a non-nil error describing why: the caller indexes from scratch (correct,
// merely slower) and the reason is on the record rather than silently swallowed.
func LoadManifest(path, root string) (*Manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is Omnipus-owned, under $OMNIPUS_HOME
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NewManifest(root), nil
		}
		return NewManifest(root), fmt.Errorf("knowledge: read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return NewManifest(root), fmt.Errorf("knowledge: parse manifest %s: %w", path, err)
	}
	if m.Version != manifestVersion {
		return NewManifest(root), fmt.Errorf(
			"knowledge: manifest %s has version %d, want %d; rebuilding", path, m.Version, manifestVersion)
	}
	if m.Root != root {
		return NewManifest(root), fmt.Errorf(
			"knowledge: manifest %s records root %q, opened for %q; rebuilding", path, m.Root, root)
	}
	if m.Entries == nil {
		m.Entries = make(map[string]ManifestEntry)
	}
	return &m, nil
}

// Save writes the manifest atomically at 0600, creating its parent 0700
// (FR-032). Both modes are asserted rather than left to the process umask.
func (m *Manifest) Save(path string) error {
	if m.Entries == nil {
		m.Entries = make(map[string]ManifestEntry)
	}
	m.Version = manifestVersion

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, indexDirMode); err != nil {
		return fmt.Errorf("knowledge: create manifest dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, indexDirMode); err != nil {
		return fmt.Errorf("knowledge: set mode on manifest dir %s: %w", dir, err)
	}

	// Marshal deterministically: encoding/json sorts map keys, so an unchanged
	// collection produces byte-identical manifests run to run.
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("knowledge: encode manifest: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, data, indexFileMode); err != nil {
		return fmt.Errorf("knowledge: write manifest %s: %w", path, err)
	}
	if err := os.Chmod(path, indexFileMode); err != nil {
		return fmt.Errorf("knowledge: set mode on manifest %s: %w", path, err)
	}
	return nil
}

// StatUnchanged reports whether the cheap freshness facts for e match what was
// recorded — the check that runs once per file on every reconcile, and the only
// one MV-4's budget can afford at 100,000 files.
//
// It is deliberately conservative in one direction only: it can say "changed"
// about a file whose bytes are identical (a touch), and the hash confirmation in
// Index.Sync then discards the needless re-parse. It must never say "unchanged"
// about a file whose bytes differ under a different size or mtime.
func (m *Manifest) StatUnchanged(e ScanEntry) bool {
	rec, ok := m.Entries[e.RelPath]
	if !ok {
		return false
	}
	return rec.Kind == e.Kind && rec.Size == e.Size && rec.ModTimeNanos == e.ModTimeNanos
}

// Get returns the recorded entry for a collection-relative path.
func (m *Manifest) Get(relPath string) (ManifestEntry, bool) {
	rec, ok := m.Entries[relPath]
	return rec, ok
}

// Put records an entry.
func (m *Manifest) Put(e ManifestEntry) {
	if m.Entries == nil {
		m.Entries = make(map[string]ManifestEntry)
	}
	m.Entries[e.Path] = e
}

// Remove drops an entry.
func (m *Manifest) Remove(relPath string) { delete(m.Entries, relPath) }

// Len returns the number of recorded files.
func (m *Manifest) Len() int { return len(m.Entries) }
