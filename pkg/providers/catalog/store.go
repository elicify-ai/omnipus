package catalog

// store.go — the persisted last-known-good and the boot read (T067-04,
// FR-010, A-4).
//
// A successfully refreshed document is persisted verbatim (the exact
// checksum-verified bytes) to $OMNIPUS_HOME/providers_catalog.json and
// read back at boot: a valid persisted document STRICTLY newer than the
// embedded snapshot wins (E6, served_from=pulled); otherwise the embedded
// snapshot serves. An invalid persisted file — wrong schema_version,
// unreadable envelope, FR-033 violation — is ignored with exactly one
// reason-keyed WARN naming providers_catalog.json (US-3.AC7). The legacy
// capabilities-era file is neither read nor deleted and produces zero log
// lines: no code path in this package names it (F-18, asserted by
// TestStore_NoLegacyFilenameInSource).

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// PersistedFileName is the last-known-good file under $OMNIPUS_HOME (A-4).
const PersistedFileName = "providers_catalog.json"

// Store persists the last-known-good document bytes across restarts. Read
// is called once at Boot; Write after every successfully applied refresh.
// The bytes are the raw published document — independently parseable,
// directly inspectable, no envelope.
type Store interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
}

// FileStore is the production Store: PersistedFileName inside the data
// dir, written atomically (temp file + rename) with 0600.
type FileStore struct {
	path string
}

// NewFileStore returns a FileStore rooted at dir ($OMNIPUS_HOME).
func NewFileStore(dir string) *FileStore {
	return &FileStore{path: filepath.Join(dir, PersistedFileName)}
}

// Path returns the absolute file path the store reads and writes.
func (s *FileStore) Path() string { return s.path }

// Read returns the persisted bytes; a missing file surfaces fs.ErrNotExist.
func (s *FileStore) Read(_ context.Context) ([]byte, error) {
	return os.ReadFile(s.path)
}

// Write atomically replaces the persisted file.
func (s *FileStore) Write(_ context.Context, data []byte) error {
	return fileutil.WriteFileAtomic(s.path, data, 0o600)
}

// ModTime reports when the persisted last-known-good was last written —
// the local fetch time, not the document's own updated_at (which is the
// assembly job's publish stamp and would make the FR-008 startup-pull skip
// fire almost never). The gateway reads it to decide whether the startup
// pull is worth making at all: a document written less than an hour ago is
// current enough that pulling again only spends GitHub's unauthenticated
// rate limit. A missing file surfaces fs.ErrNotExist, which the caller
// reads as "nothing persisted — pull".
func (s *FileStore) ModTime() (time.Time, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}

// Boot constructs the gateway's catalog (T067-04): it parses the embedded
// snapshot, reads the persisted last-known-good through store, and serves
// whichever is valid and newest — a strictly newer persisted document wins
// as served_from=pulled (E6); ties and everything else fall to the
// embedded snapshot. Boot never fails: a corrupt embedded snapshot with no
// usable persisted fallback yields a catalog with NO document (E7) — one
// ERROR is logged, every lookup misses, Served() reports nothing to serve
// (the handler answers 503), and Degraded() explains why.
//
// Boot performs no network I/O; the startup pull and the 24 h ticker are
// the gateway's (T067-07), via Refresh.
func Boot(ctx context.Context, embedded []byte, puller Puller, store Store, log Logger) *Catalog {
	c := New()
	c.puller = puller
	c.store = store
	c.log = log

	embDoc, embErr := ParseDocument(embedded)
	if embErr != nil {
		// E7: the committed snapshot itself is bad. Boot continues — the
		// persisted last-known-good may still serve.
		c.logError("catalog: embedded snapshot is invalid", "error", embErr)
	}

	var perDoc *Document
	if store != nil {
		data, err := store.Read(ctx)
		switch {
		case err == nil:
			doc, perr := ParseDocument(data)
			if perr != nil {
				reason := reasonInvalid
				if errors.Is(perr, ErrSchemaVersion) {
					reason = reasonSchemaVersion
				}
				c.logWarn("catalog: ignoring invalid persisted last-known-good",
					"file", PersistedFileName, "reason", reason, "error", perr)
			} else {
				perDoc = doc
			}
		case errors.Is(err, fs.ErrNotExist):
			// Fresh install — nothing persisted yet, nothing to say.
		default:
			c.logWarn("catalog: could not read persisted last-known-good",
				"file", PersistedFileName, "error", err)
		}
	}

	switch {
	case perDoc != nil && (embDoc == nil || perDoc.Version.Compare(embDoc.Version) > 0):
		// E6: the persisted document is strictly newer (or the snapshot is
		// unusable) — it was pulled from a release, so it serves as pulled.
		if err := c.applyDoc(perDoc, ServedPulled); err != nil {
			c.logError("catalog: could not serve persisted last-known-good", "error", err)
		}
	case embDoc != nil:
		if err := c.applyDoc(embDoc, ServedEmbedded); err != nil {
			c.logError("catalog: could not serve embedded snapshot", "error", err)
		}
	}
	return c
}
