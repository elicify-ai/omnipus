// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

// One-time migration of a pre-name-binding store to the name-bound format.
//
// # What it does
//
// A store written before the AAD name binding (issue #85b) is at format
// version 1 and its entries are sealed with a nil AES-GCM AAD. Those entries
// cannot be opened by decrypt, which requires aadFor(name). On unlock — and
// before anything else reads the store — migrateLegacyLocked opens every entry
// the legacy way, re-seals each one under aadFor(name) with the SAME key, and
// writes the whole store back at version 2 in one atomic write. From then on
// the file is at version 2 and loadFileInternal refuses any other version, so
// the legacy read path is unreachable for that store.
//
// # What it deliberately refuses
//
//   - Any entry that fails the legacy read: nothing is migrated, the file is
//     left byte-identical, the unlock fails with a *MigrationError naming
//     every failing entry. That error unwraps to one *EntryAuthError per entry
//     (and so to ErrWrongKey), which keeps the gateway's existing "store-wide,
//     fatal" classification: boot STOPS, it does not run degraded.
//   - A version-1 file on an install that already migrated (the record file
//     credentials.json.migrated exists): ErrLegacyStoreAfterMigration. This
//     is the replay defence — see "Residual risk".
//   - Any version other than 1 or 2: ErrUnsupportedStoreVersion.
//
// # Residual risk (stated, not hidden)
//
// A nil-AAD ciphertext carries no name, so a swap made to the legacy file
// BEFORE its one migration is indistinguishable from the genuine layout and is
// re-sealed under the name it was moved to. Cryptography cannot detect it; the
// migration makes that window exactly one unlock wide. The migration record
// stops the obvious extension — restoring an old, swapped legacy copy after
// the install migrated — but an attacker who can write the data directory can
// delete the record too, so the record is defence in depth, not a guarantee.
// An attacker who can write the data directory already has a stronger attack
// (whole-file rollback of a name-bound store), which the binding never claimed
// to stop.

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const (
	// legacyStoreVersion is the pre-name-binding format: entries sealed with a
	// nil AAD. Only migrateLegacyLocked may read it.
	legacyStoreVersion = 1

	// migrationRecordSuffix names the file, next to credentials.json, that
	// records a completed migration. fspolicy withholds every
	// credentials.json.<suffix> name from agents, so it inherits the store's
	// sandbox protection.
	migrationRecordSuffix = ".migrated"
)

// ErrLegacyStoreFormat is returned by every read or write when the file on
// disk is at the pre-name-binding version. Only unlock migrates it; no other
// path reads nil-AAD entries or stamps the file with the new version.
var ErrLegacyStoreFormat = errors.New(
	"credentials: credentials.json is in the pre-upgrade format and has not been migrated — unlock the store (restart the gateway) to migrate it",
)

// ErrLegacyStoreAfterMigration is returned when a pre-upgrade file appears on
// an install whose store has already been migrated.
var ErrLegacyStoreAfterMigration = errors.New(
	"credentials: credentials.json is in the pre-upgrade format but this install already migrated it once — refusing to migrate again",
)

// ErrUnsupportedStoreVersion is returned for a format version this binary
// does not know (a newer binary's file, or a hand-edited one).
var ErrUnsupportedStoreVersion = errors.New("credentials: unsupported credentials.json format version")

// MigrationError reports a refused migration: at least one entry could not be
// opened even the legacy way, so no entry was migrated and the file is
// unchanged. Failed lists every such entry (sorted); Total is the entry count.
type MigrationError struct {
	Failed []string
	Total  int
}

func (e *MigrationError) Error() string {
	quoted := make([]string, len(e.Failed))
	for i, n := range e.Failed {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	cause := "those entries were edited or moved on disk: remove them from credentials.json, restart, then enter them again"
	if len(e.Failed) == e.Total {
		cause = "every entry failed, which means the master key is not the one this store was written with: supply the original master key"
	}
	return fmt.Sprintf(
		"credentials: one-time upgrade of credentials.json to the name-bound format refused, file left unchanged — %d of %d entries failed authentication (%s); %s",
		len(e.Failed), e.Total, strings.Join(quoted, ", "), cause,
	)
}

// Unwrap exposes one *EntryAuthError per failed entry, so errors.As finds the
// named entry and errors.Is(err, ErrWrongKey) stays true — the classification
// every caller already treats as store-wide and fatal.
func (e *MigrationError) Unwrap() []error {
	errs := make([]error, len(e.Failed))
	for i, n := range e.Failed {
		errs[i] = &EntryAuthError{Name: n}
	}
	return errs
}

// migrationRecordPath is the path of the completed-migration record.
func (s *Store) migrationRecordPath() string {
	return s.path + migrationRecordSuffix
}

// readStoreVersionFile reads and parses the store file. present is false when
// the file does not exist.
func (s *Store) readStoreVersionFile() (sf *storeFile, present bool, err error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("credentials: read store file: %w", err)
	}
	var parsed storeFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, true, fmt.Errorf("credentials: store file corrupted (manual fix required): %w", err)
	}
	return &parsed, true, nil
}

// migrateLegacyLocked migrates a version-1 store to the current version under
// key. It is a no-op when the file is absent or already current. Caller holds
// s.mu for writing.
//
// The first read is lock-free, so the common case (a current store, or none
// yet) takes no lock and creates no file. Only a legacy file takes the sidecar
// flock that saveFileNoLock uses, and then the file is read and its version
// checked AGAIN under that lock before anything is written — two processes
// unlocking the same legacy file serialize, and the second sees version 2.
//
// An unreadable or unparseable file is NOT this function's to report: it
// returns nil and leaves the existing read path (loadFileInternal) to fail
// every operation with its established error. Safety never depends on this
// function running — loadFileInternal refuses every version but the current
// one, so a store this function skipped is refused, never read the legacy way.
func (s *Store) migrateLegacyLocked(key []byte) error {
	sf, present, err := s.readStoreVersionFile()
	if err != nil || !present {
		return nil //nolint:nilerr // reported by loadFileInternal on first use; see doc comment
	}
	switch sf.Version {
	case storeVersion:
		return nil
	case legacyStoreVersion:
	default:
		return fmt.Errorf("%w: %d", ErrUnsupportedStoreVersion, sf.Version)
	}
	return fileutil.WithFlock(fileutil.SidecarLockPath(s.path), func() error {
		sf, present, err := s.readStoreVersionFile()
		if err != nil || !present {
			return nil //nolint:nilerr // as above: loadFileInternal reports it on first use
		}
		switch sf.Version {
		case storeVersion:
			return nil // another process migrated it while we waited
		case legacyStoreVersion:
		default:
			return fmt.Errorf("%w: %d", ErrUnsupportedStoreVersion, sf.Version)
		}

		if _, statErr := os.Stat(s.migrationRecordPath()); statErr == nil {
			emitMigrationAudit(false, "legacy file after completed migration", len(sf.Credentials), 0)
			return fmt.Errorf(
				"%w: if you deliberately restored a pre-upgrade backup of credentials.json, delete %s and restart; otherwise treat the store as tampered with",
				ErrLegacyStoreAfterMigration, s.migrationRecordPath())
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("credentials: cannot check migration record %s: %w", s.migrationRecordPath(), statErr)
		}

		migrated, failed, err := resealLegacyEntries(key, sf.Credentials)
		if err != nil {
			return err
		}
		if len(failed) > 0 {
			emitMigrationAudit(false, "entries failed authentication", len(sf.Credentials), len(failed))
			return &MigrationError{Failed: failed, Total: len(sf.Credentials)}
		}

		sf.Version = storeVersion
		sf.Credentials = migrated
		out, err := json.MarshalIndent(sf, "", "  ")
		if err != nil {
			return fmt.Errorf("credentials: marshal migrated store: %w", err)
		}
		// One atomic write of the whole store: a crash leaves either the old
		// legacy file or the complete new one, never a mix.
		if err := writeFileAtomicFn(s.path, out, 0o600); err != nil {
			return fmt.Errorf("credentials: write migrated store (legacy file left in place): %w", err)
		}

		// Recorded only after the store write landed, so a crash in between
		// costs the replay defence for that one install, never the data.
		record := fmt.Sprintf(`{"from_version":%d,"to_version":%d,"entries":%d,"migrated_at":%q}`+"\n",
			legacyStoreVersion, storeVersion, len(migrated), time.Now().UTC().Format(time.RFC3339))
		if err := fileutil.WriteFileAtomic(s.migrationRecordPath(), []byte(record), 0o600); err != nil {
			slog.Warn("credentials: store migrated, but the migration record could not be written",
				"path", s.migrationRecordPath(), "error", err.Error(),
				"consequence", "a restored pre-upgrade copy of credentials.json would be migrated again instead of refused")
		}
		emitMigrationAudit(true, "", len(migrated), 0)
		return nil
	})
}

// resealLegacyEntries opens every entry of a version-1 file and re-seals it
// under its own name. It returns the re-sealed map, or the sorted names of
// every entry that did not open. It never returns a partial success.
//
// An entry opens if it authenticates with a nil AAD (written by the released
// pre-binding binary) OR with aadFor(name) (written by a pre-release build that
// bound the name but did not bump the version). A name-bound ciphertext opens
// only under the name it was sealed for, so accepting it adds no swap surface.
func resealLegacyEntries(key []byte, entries map[string]encEntry) (map[string]encEntry, []string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("credentials: cipher init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("credentials: gcm init: %w", err)
	}

	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)

	var failed []string
	plains := make(map[string][]byte, len(entries))
	defer func() {
		for _, p := range plains {
			wipe(p)
		}
	}()
	for _, name := range names {
		plain, ok := openLegacyEntry(gcm, name, entries[name])
		if !ok {
			failed = append(failed, name)
			continue
		}
		plains[name] = plain
	}
	if len(failed) > 0 {
		return nil, failed, nil
	}

	out := make(map[string]encEntry, len(entries))
	for _, name := range names {
		sealed, err := encrypt(key, name, plains[name])
		if err != nil {
			return nil, nil, fmt.Errorf("credentials: migrate encrypt %q: %w", name, err)
		}
		out[name] = sealed
	}
	return out, nil, nil
}

// openLegacyEntry is the ONLY nil-AAD read in the package, and it is reachable
// only from resealLegacyEntries during the one migration of a version-1 file.
func openLegacyEntry(gcm cipher.AEAD, name string, e encEntry) ([]byte, bool) {
	nonce, err := base64.StdEncoding.DecodeString(e.Nonce)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, false
	}
	ct, err := base64.StdEncoding.DecodeString(e.Ciphertext)
	if err != nil {
		return nil, false
	}
	if plain, err := gcm.Open(nil, nonce, ct, nil); err == nil {
		return plain, true
	}
	if plain, err := gcm.Open(nil, nonce, ct, aadFor(name)); err == nil {
		return plain, true
	}
	return nil, false
}

// emitMigrationAudit records a migration outcome in the same structured-slog
// shape as credentials.master_key_load (the store is unlocked before the audit
// logger exists, so slog is the channel the audit hook reads). Counts only:
// no values and no entry names.
func emitMigrationAudit(success bool, detail string, entries, failedEntries int) {
	if success {
		event := "credentials.store_migrated"
		slog.Info(event,
			"event", event,
			"decision", "allow",
			"from_version", legacyStoreVersion,
			"to_version", storeVersion,
			"entries", entries,
		)
		return
	}
	event := "credentials.store_migration_refused"
	slog.Warn(event,
		"event", event,
		"decision", "deny",
		"from_version", legacyStoreVersion,
		"to_version", storeVersion,
		"entries", entries,
		"failed_entries", failedEntries,
		"detail", detail,
		"policy_rule", "credential_store_migration",
	)
}
