package mailattachment

// The mail-derived marker + per-file scripts-allowance store (founder Q5=A;
// w4 spec §5.4). A saved mail attachment keeps its ORIGINAL bytes and is
// marked mail-derived; the marker is written ONLY on explicit Save — never
// by Open. The per-file scripts checkbox switches exactly one saved file's
// preview to the ordinary script-permitting profile.
//
// MECHANISM (the ADR deliberately leaves it open; grill-2 F-8 is the hard
// rule): one JSON index per workspace, keyed by the file's workspace-relative
// PATH — the allowance is keyed to the saved FILE, never to the content
// bytes. A content-addressed key would merge byte-identical twins and is
// out of bounds by construction here. Written atomically
// (fileutil.WriteFileAtomic) under the operator's data directory, not inside
// the workspace work tree — a marker must never appear as a Library entry.
//
// PROVENANCE (the traced-and-proved obligation): the store exposes
// Move/Copy/Delete so the Library operations that move a file can move its
// marker with it; the caller wires them at the operation handlers. A file
// whose marker cannot be READ fails safe to the stricter profile at serve
// time — never a silent scripts-on upgrade (§5.4).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// markerEntry is one file's record. ScriptsAllowed is the per-file checkbox
// state (nil = never answered = scripts off, the default posture).
type markerEntry struct {
	MailDerived    bool  `json:"mail_derived"`
	ScriptsAllowed *bool `json:"scripts_allowed,omitempty"`
	SavedAtUnixMS  int64 `json:"saved_at_unix_ms,omitempty"`
}

// MarkerStore is the durable per-workspace marker index.
type MarkerStore struct {
	mu   sync.Mutex
	path string // the index file's absolute path
}

// NewMarkerStore builds the store over one index file path (the caller
// derives it from the workspace's data directory; the service never guesses
// it).
func NewMarkerStore(path string) *MarkerStore {
	return &MarkerStore{path: path}
}

// markerIndex is the on-disk shape. Path-keyed; sorted on write for stable
// diffs.
type markerIndex struct {
	Entries map[string]markerEntry `json:"entries"`
}

func (s *MarkerStore) load() (*markerIndex, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &markerIndex{Entries: map[string]markerEntry{}}, nil
		}
		return nil, err
	}
	var idx markerIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		// A corrupt index is an UNREADABLE marker for every file: the serve
		// rule fails safe to scripts-off. The store reports the corruption;
		// it never guesses entries back.
		return nil, fmt.Errorf("mailattachment: marker index corrupt: %w", err)
	}
	if idx.Entries == nil {
		idx.Entries = map[string]markerEntry{}
	}
	return &idx, nil
}

func (s *MarkerStore) save(idx *markerIndex) error {
	paths := make([]string, 0, len(idx.Entries))
	for p := range idx.Entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	ordered := make(map[string]markerEntry, len(paths))
	for _, p := range paths {
		ordered[p] = idx.Entries[p]
	}
	idx.Entries = ordered
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(s.path, data, 0o600)
}

// Mark records a file as mail-derived, scripts off by default. Written only
// on explicit Save.
func (s *MarkerStore) Mark(rel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.load()
	if err != nil {
		return err
	}
	idx.Entries[rel] = markerEntry{MailDerived: true, SavedAtUnixMS: time.Now().UnixMilli()}
	return s.save(idx)
}

// Status is the three-valued marker answer the preview-profile resolver
// needs: Known(mail-derived) / Known(ordinary) / Unknown (unreadable —
// fail safe to the stricter profile).
type Status int

const (
	StatusOrdinary Status = iota
	StatusMailDerived
	StatusUnknown
)

// Status reports one file's marker state. A file with no entry is ordinary
// (a store that reads cleanly proves absence); a store read FAILURE is
// Unknown for every file — the fail-safe direction.
func (s *MarkerStore) Status(rel string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.load()
	if err != nil {
		return StatusUnknown, err
	}
	if e, ok := idx.Entries[rel]; ok && e.MailDerived {
		return StatusMailDerived, nil
	}
	return StatusOrdinary, nil
}

// ScriptsAllowed reports the per-file checkbox state: (allowed, mailDerived,
// err). Scripts default OFF for a mail-derived file; an ordinary file has no
// mail posture at all.
func (s *MarkerStore) ScriptsAllowed(rel string) (allowed, mailDerived bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.load()
	if err != nil {
		return false, false, err
	}
	e, ok := idx.Entries[rel]
	if !ok || !e.MailDerived {
		return false, false, nil
	}
	if e.ScriptsAllowed != nil {
		return *e.ScriptsAllowed, true, nil
	}
	return false, true, nil
}

// AllowScripts sets the per-file checkbox state for exactly this file. Only
// a mail-derived file carries the allowance; marking an ordinary file is a
// no-op (there is nothing to allow — its ordinary profile already permits
// scripts).
func (s *MarkerStore) AllowScripts(rel string, allow bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.load()
	if err != nil {
		return err
	}
	e, ok := idx.Entries[rel]
	if !ok || !e.MailDerived {
		return ErrNotMailDerived
	}
	e.ScriptsAllowed = &allow
	idx.Entries[rel] = e
	return s.save(idx)
}

// Move re-keys a file's marker after a rename/move of the file (provenance:
// the marker must survive Root.Rename / MoveInto).
func (s *MarkerStore) Move(fromRel, toRel string) error {
	return s.rekey(fromRel, toRel, false)
}

// Copy duplicates a file's marker to the copy's path (provenance: the marker
// must survive CopyInto — the copy is equally mail-derived; its allowance is
// the copy's own: the checkbox state travels as the default off, per §5.4's
// "two saves of the same attachment are two files with independent
// allowances" — a copy starts un-answered, scripts off).
func (s *MarkerStore) Copy(fromRel, toRel string) error {
	return s.rekey(fromRel, toRel, true)
}

func (s *MarkerStore) rekey(fromRel, toRel string, keepBoth bool) error {
	if fromRel == toRel {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.load()
	if err != nil {
		return err
	}
	e, ok := idx.Entries[fromRel]
	if !ok {
		return nil // nothing to move; the target file simply has no marker
	}
	copied := e
	if copied.ScriptsAllowed != nil {
		allow := false
		copied.ScriptsAllowed = &allow // a copy/new location starts scripts-off
	}
	copied.SavedAtUnixMS = e.SavedAtUnixMS
	idx.Entries[toRel] = copied
	if !keepBoth {
		delete(idx.Entries, fromRel)
	}
	return s.save(idx)
}

// Delete drops a file's marker (the delete path; a missing entry is
// orphan-tolerant, mirroring the Library's own delete tolerance).
func (s *MarkerStore) Delete(rel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := idx.Entries[rel]; !ok {
		return nil
	}
	delete(idx.Entries, rel)
	return s.save(idx)
}
