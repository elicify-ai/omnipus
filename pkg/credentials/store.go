// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package credentials implements the Omnipus encrypted credential store
// per BRD SEC-23/23a–e and Wave 1 user stories US-3, US-4, US-11, US-12.
//
// Storage format (credentials.json):
//
//	{
//	  "version": 2,
//	  "salt": "<base64>",
//	  "credentials": {
//	    "ANTHROPIC_API_KEY": {
//	      "nonce": "<base64>",
//	      "ciphertext": "<base64>"
//	    }
//	  }
//	}
//
// Key derivation: Argon2id(time=3, memory=64MB, parallelism=4, keyLen=32).
// Encryption: AES-256-GCM, with the credential name bound as additional
// authenticated data (AAD). Every envelope is sealed and opened with
//
//	"omnipus-credential-v1:" + name
//
// as its AAD, so a ciphertext moved to a different name fails authentication
// instead of decrypting under the name it was moved to. The version tag lives
// in the AAD rather than in the file: it domain-separates this sealing context
// from any other use of the master key, and it keeps the AAD non-empty even
// for an empty name — crypto/cipher cannot tell a nil AAD from a zero-length
// one, so binding the bare name would leave an empty-named entry unbound.
// Ciphertexts sealed under one tag cannot be opened under another, which is
// what makes a future AAD change a deliberate, self-enforcing break rather
// than something a stale read path could paper over.
//
// Format versions and the one-time upgrade: a store written before the name
// binding is at "version": 1 and its entries carry a nil AAD. Unlocking such a
// store migrates it once, before any other read, to "version": 2 — every entry
// opened the legacy way and re-sealed under its own name with the same key, in
// one atomic write (store_migrate.go). After that the file is at version 2 and
// loadFileInternal refuses every other version, so the legacy read is
// unreachable for that store. There is deliberately no per-read "open with the
// name, else try no AAD" fallback — it would restore the vulnerability for
// every entry, forever. See store_migrate.go for what the migration refuses
// and for the residual risk it cannot remove.
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const (
	// storeVersion is the name-bound format: every entry sealed with
	// aadFor(name). The pre-binding format (legacyStoreVersion) is read only
	// by the one-time migration in store_migrate.go.
	storeVersion = 2
	saltLen      = 32
	nonceLen     = 12
	keyLen       = 32

	// Argon2id parameters per SEC-23b.
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4

	// aadDomainTag is the domain-separation label prefixed to every entry
	// name before it is used as AES-GCM additional authenticated data. It is
	// not a secret — it is a fixed, public tag that scopes this sealing
	// context so it can never collide with any other use of the master key.
	// Bump it whenever the AAD construction changes: ciphertexts sealed under
	// one tag cannot be opened under another, so the bump is what makes an
	// accidental back-compat read impossible rather than merely discouraged.
	aadDomainTag = "omnipus-credential-v1:"
)

// ErrStoreLocked is returned when the credential store is not unlocked.
var ErrStoreLocked = errors.New(
	"credentials: store is locked — provide master key via OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, or interactive passphrase",
)

// ErrWrongKey is returned when AES-GCM authentication fails (wrong key).
// A *EntryAuthError unwraps to it, so a caller that only knows this sentinel
// still classifies every tag failure the way it always did.
var ErrWrongKey = errors.New("credentials: decryption failed — wrong master key?")

// EntryAuthError reports that one stored entry failed AES-GCM authentication.
//
// The cause is not cryptographically separable: a failed tag means the key,
// the AAD (here, the entry name) or the ciphertext bytes did not match what
// was sealed, and the tag carries one bit for all three. The message therefore
// names the entry and every cause it could be instead of asserting a wrong
// passphrase, which would be a guess. What does separate them is scope — a
// wrong master key fails EVERY entry, while a swapped or edited entry fails
// only the name it was moved to — so a caller that can observe more than one
// entry holds the evidence this error cannot carry.
type EntryAuthError struct{ Name string }

func (e *EntryAuthError) Error() string {
	return fmt.Sprintf(
		"credentials: entry %q failed authentication — the stored value was not sealed under this name: it was edited in place, moved here from another entry, or the whole store was written under a different master key (re-enter this credential)",
		e.Name,
	)
}

// Unwrap classifies any entry authentication failure as a decryption failure.
// errors.Is(err, ErrWrongKey) therefore stays true, and every caller that
// treats ErrWrongKey as store-wide-and-fatal — gateway boot's
// reportInjectionErrors, rest.go's describeCredentialResolutionError — keeps
// that behaviour without having to know this type exists.
func (e *EntryAuthError) Unwrap() error { return ErrWrongKey }

// EntryEncodingError identifies malformed encoding in one stored entry, not an
// unreadable store document. Its cause contains only the encoding fault, never
// the nonce, ciphertext or plaintext. Gateway callers scope it only after the
// store health check finds another decryptable entry.
type EntryEncodingError struct {
	Name string
	Err  error
}

func (e *EntryEncodingError) Error() string {
	return fmt.Sprintf("credentials: entry %q is malformed: %v", e.Name, e.Err)
}

func (e *EntryEncodingError) Unwrap() error { return e.Err }

// NotFoundError is returned when a credential name is not in the store.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("credentials: %q not found in credential store", e.Name)
}

// storeFile is the on-disk JSON structure.
type storeFile struct {
	Version      int                    `json:"version"`
	Salt         string                 `json:"salt"`
	Credentials  map[string]encEntry    `json:"credentials"`
	CardSecrets  map[string]CardSecret  `json:"card_secrets,omitempty"`
	CardReceipts map[string]CardReceipt `json:"card_receipts,omitempty"`
	// CardHighWater records, per reference family (CardValue.BaseName), the
	// highest version ever issued. Deleting a version never lowers it, so a
	// deleted reference name is never issued again: a consumer still naming
	// it stays unresolved until a new approved selection (gate r1 M1).
	CardHighWater map[string]int `json:"card_high_water,omitempty"`
	// PrivateSelected maps one agent-private variable (privateKey) to the
	// reference of its selected version. Only the selected version is
	// injected into that pair's children (FR-076, FR-086).
	PrivateSelected map[string]string `json:"private_selected,omitempty"`
	// PrivateOptions holds per-variable options (FR-241), keyed like
	// PrivateSelected.
	PrivateOptions map[string]PrivateOptions `json:"private_options,omitempty"`
}

type encEntry struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// Store is the Omnipus encrypted credential store. It is safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	path string
	key  []byte // nil when locked
}

// NewStore returns a locked Store backed by path.
// Call Unlock (or UnlockWithKey) before reading or writing credentials.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path returns the on-disk path of the credentials.json file.
func (s *Store) Path() string {
	return s.path
}

// Exists reports whether the credentials.json file currently exists on disk.
// Used by the auto-generate path in Unlock to determine whether this is a
// fresh install (no existing encrypted data) and thus safe to mint a new key.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.path)
	return err == nil
}

// IsLocked reports whether the store is currently locked.
func (s *Store) IsLocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.key == nil
}

// Close overwrites the store's key material and locks the store. Every owner of
// a Store calls it once it is done with it — the gateway at shutdown, each CLI
// command on completion — so that the key does not outlive the work that needed
// it.
//
// # The guarantee, stated honestly
//
// The overwrite is best-effort, not cryptographic. Close zeroes the one array
// this Store owns exclusively — every unlock path allocates it and copies into
// it, so a caller's slice is never affected — and then drops the reference.
// Copies it cannot reach stay readable: subkeys handed out by DeriveSubkey,
// plaintext credential values returned by Get, the passphrase string itself
// (Go strings are immutable and cannot be overwritten at all), and anything the
// runtime copied. See wipe for why Go cannot offer more than this.
//
// Close is idempotent, and it is safe on a store that was never unlocked. The
// store is left locked rather than unusable: Unlock (or UnlockWithKey) can
// unlock it again, and every operation called in between returns ErrStoreLocked
// rather than attempting a decrypt with a zeroed key.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	wipe(s.key)
	s.key = nil
}

// UnlockWithKey sets the 32-byte AES-256 key directly.
// Used when the key was provisioned via OMNIPUS_MASTER_KEY or key-file.
//
// The key is COPIED, never aliased: the store owns the array Close overwrites,
// and the caller keeps ownership of the slice it passed in.
//
// A store still in the pre-upgrade format is migrated here, before the key is
// installed and before anything else can read it (store_migrate.go). If the
// migration is refused the error is returned, the file is unchanged, and the
// store stays locked.
func (s *Store) UnlockWithKey(key []byte) error {
	if len(key) != keyLen {
		return fmt.Errorf("credentials: key must be exactly %d bytes, got %d", keyLen, len(key))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installKeyLocked(key)
}

// installKeyLocked migrates a pre-upgrade store under key and, only if that
// succeeds, installs a private copy of key. Caller holds s.mu for writing.
func (s *Store) installKeyLocked(key []byte) error {
	k := make([]byte, keyLen)
	copy(k, key)
	if _, err := s.migrateLegacyLocked(k, k, nil); err != nil {
		wipe(k)
		return err
	}
	s.key = k
	return nil
}

// UnlockWithPassphrase derives the key using Argon2id and the salt stored in
// credentials.json (or a freshly generated salt if the file does not yet exist).
func (s *Store) UnlockWithPassphrase(passphrase string) error {
	if passphrase == "" {
		return fmt.Errorf("credentials: passphrase must not be empty")
	}

	salt, err := s.loadOrCreateSalt()
	if err != nil {
		return err
	}

	derived := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, keyLen)
	// The store copies the key rather than aliasing this buffer, so the Argon2id
	// output can be overwritten the moment it has been handed over. Argon2id's
	// own internal working memory (64 MB at these parameters) lives inside
	// golang.org/x/crypto and is not reachable from here — any copy surviving
	// there is one this wipe cannot touch.
	defer wipe(derived)

	// A pre-upgrade store is migrated under a FRESH salt, so the key the
	// passphrase derives afterwards no longer opens pre-upgrade entries (see
	// store_migrate.go, "Residual risk"). The second Argon2id run is paid only
	// when the lock-free probe sees a legacy file.
	// Probe errors are returned by installKeyLocked before it installs the key.
	if sf, present, probeErr := s.readStoreVersionFile(); probeErr != nil || !present || sf.Version != legacyStoreVersion {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.installKeyLocked(derived)
	}
	freshSalt := make([]byte, saltLen)
	if _, saltErr := io.ReadFull(rand.Reader, freshSalt); saltErr != nil {
		return fmt.Errorf("credentials: generate migration salt: %w", saltErr)
	}
	freshKey := argon2.IDKey([]byte(passphrase), freshSalt, argonTime, argonMemory, argonThreads, keyLen)
	defer wipe(freshKey)

	s.mu.Lock()
	defer s.mu.Unlock()
	migrated, err := s.migrateLegacyLocked(derived, freshKey, freshSalt)
	if err != nil {
		return err
	}
	if migrated {
		s.key = make([]byte, keyLen)
		copy(s.key, freshKey)
		return nil
	}
	// Another process migrated the store while this one waited for the lock,
	// under ITS fresh salt, so neither key derived here is the right one.
	// Derive again from the salt now on disk.
	current, err := s.loadOrCreateSalt()
	if err != nil {
		return err
	}
	rederived := argon2.IDKey([]byte(passphrase), current, argonTime, argonMemory, argonThreads, keyLen)
	defer wipe(rederived)
	return s.installKeyLocked(rederived)
}

// DeriveSubkey derives a 32-byte subkey from the unlocked master key using
// HKDF-SHA256 with the supplied info string as the domain-separation tag.
//
// The master key itself is NEVER returned — every subsystem that needs key
// material (e.g. the audit-chain HMAC from #155) must request a derived
// subkey with its own info string. This way:
//
//   - An attacker with read access to a derived subkey cannot reverse it back
//     to the master key (HKDF is one-way).
//   - Two subsystems with different info strings get cryptographically
//     independent keys, so a compromise of one subkey does not affect the
//     other.
//   - The master key never crosses a package boundary, limiting the surface
//     where a future bug could leak it (logs, panics, error wrapping).
//
// info MUST be a stable, namespaced string (e.g. "omnipus-audit-chain-v1").
// Bumping the version suffix when a subsystem rotates its key derivation is
// the supported migration path.
//
// Returns ErrStoreLocked if the store has not been unlocked.
func (s *Store) DeriveSubkey(info string) ([]byte, error) {
	if info == "" {
		return nil, fmt.Errorf("credentials: DeriveSubkey requires non-empty info string")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return nil, ErrStoreLocked
	}

	// HKDF-Expand on the master key with no salt and the caller's info tag.
	// We deliberately use the master key as the IKM (input keying material)
	// rather than running HKDF-Extract first: the master key is already a
	// uniform 256-bit AES key with high entropy, so the Extract step would
	// add no security and only obscure the derivation.
	r := hkdf.New(sha256.New, s.key, nil, []byte(info))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("credentials: hkdf expand %q: %w", info, err)
	}
	return out, nil
}

// Set encrypts value and stores it under name in credentials.json.
// Implements US-3 AC1, US-12 AC1.
func (s *Store) Set(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrStoreLocked
	}

	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}

	entry, err := encrypt(s.key, name, []byte(value))
	if err != nil {
		return fmt.Errorf("credentials: encrypt %q: %w", name, err)
	}
	sf.Credentials[name] = entry

	return s.saveFileNoLock(sf)
}

// Get decrypts and returns the credential named name.
// Returns NotFoundError if the name is not present, and a *EntryAuthError
// naming the entry when the stored bytes do not authenticate under this name
// — edited, moved here from another entry, or sealed under a different master
// key. That error unwraps to ErrWrongKey, so an errors.Is check against the
// sentinel still holds.
// Implements US-3 AC2, US-3 AC3.
func (s *Store) Get(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return "", ErrStoreLocked
	}

	sf, err := s.loadFileInternal()
	if err != nil {
		return "", err
	}

	entry, ok := sf.Credentials[name]
	if !ok {
		return "", &NotFoundError{Name: name}
	}

	plain, err := decrypt(s.key, name, entry)
	if err != nil {
		return "", err
	}
	return plain, nil
}

// List returns all credential names, sorted alphabetically. Values are never returned.
// Implements US-12 AC2.
func (s *Store) List() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sf, err := s.loadFileInternal()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(sf.Credentials))
	for k := range sf.Credentials {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// DamagedEntriesError reports unreadable entries while at least one other entry
// decrypts under the master key: the failure is real and visible, but scoped to
// the named entries — the store itself is readable. HealthCheck reports encoding
// damage; SensitiveValues also includes authentication failures in its partial
// scan. A locked, unreadable or wholly undecryptable store never returns this type.
type DamagedEntriesError struct {
	Names []string
	Errs  []error
}

func (e *DamagedEntriesError) Error() string {
	return fmt.Sprintf("credentials: %d damaged store entr%s cannot be read (%s); the other entries are readable — re-enter the named credentials: %v",
		len(e.Names), pluralY(len(e.Names)), strings.Join(e.Names, ", "), errors.Join(e.Errs...))
}

// Unwrap exposes each entry's own error.
func (e *DamagedEntriesError) Unwrap() []error { return e.Errs }

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// HealthCheck checks the store independently of configured credential refs.
// A locked, unreadable or undecodable store is unhealthy. A nonempty store must
// have at least one decryptable entry (referenced or not); otherwise its entry
// errors are returned, preserving ErrWrongKey and any decoding causes. With a
// readable witness, entries whose encoding is damaged (malformed Base64, wrong
// nonce length) are reported as a *DamagedEntriesError naming them — a
// visible, scoped failure; entries that fail authentication are still left to
// their consumers. An absent file or an empty current-format store is healthy
// and is not written.
func (s *Store) HealthCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return ErrStoreLocked
	}

	// Read once while holding the key stable. Do not use Exists: a failed stat
	// can mean an inaccessible path, not an absent file. loadFileInternal only
	// treats os.ErrNotExist as an empty store and preserves other read errors.
	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}
	if len(sf.Credentials) == 0 {
		return nil
	}

	names := make([]string, 0, len(sf.Credentials))
	for name := range sf.Credentials {
		names = append(names, name)
	}
	sort.Strings(names)
	entryErrs := make([]error, 0, len(names))
	damaged := &DamagedEntriesError{}
	witness := false
	for _, name := range names {
		_, decryptErr := decrypt(s.key, name, sf.Credentials[name])
		if decryptErr == nil {
			witness = true
			continue
		}
		entryErrs = append(entryErrs, decryptErr)
		var authErr *EntryAuthError
		if !errors.As(decryptErr, &authErr) {
			damaged.Names = append(damaged.Names, name)
			damaged.Errs = append(damaged.Errs, decryptErr)
		}
	}
	if !witness {
		return fmt.Errorf("credentials: no store entry could be decrypted (wrong master key or corrupted entries): %w",
			errors.Join(entryErrs...))
	}
	if len(damaged.Names) > 0 {
		return damaged
	}
	return nil
}

// Delete removes the credential named name from the store atomically.
// Implements US-12 AC3.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrStoreLocked
	}

	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}

	if _, ok := sf.Credentials[name]; !ok {
		return &NotFoundError{Name: name}
	}
	delete(sf.Credentials, name)
	// A card-stored version also carries a recipient metadata row (and, for
	// a private variable, possibly its selection). Remove them in the same
	// publication so no valueless "ghost" stays listed (architect WU-6 ruling).
	if secret, card := sf.CardSecrets[name]; card {
		delete(sf.CardSecrets, name)
		if secret.Recipient.Kind == KindAgentPrivate {
			key := privateKey(secret.Recipient)
			if sf.PrivateSelected[key] == name {
				delete(sf.PrivateSelected, key)
			}
		}
	}

	return s.saveFileNoLock(sf)
}

// Rotate re-encrypts all credentials with newKey.
// The old key must already be loaded (store must be unlocked).
// A fresh random salt is generated and persisted alongside the new ciphertext.
//
// The store takes a COPY of newKey and never overwrites the caller's slice:
// Close wipes the store's own array only, so what the caller does with the
// buffer it passed in is the caller's to decide.
// Implements US-12 AC4.
func (s *Store) Rotate(newKey []byte) error {
	if len(newKey) != keyLen {
		return fmt.Errorf("credentials: new key must be exactly %d bytes", keyLen)
	}
	newSalt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, newSalt); err != nil {
		return fmt.Errorf("credentials: generate salt for rotation: %w", err)
	}
	return s.rotateFull(newKey, newSalt)
}

// RotateWithPassphrase derives a new key from newPassphrase, generates a salt,
// persists the SAME salt that was used for key derivation, and re-encrypts all
// credentials. The salt and key are always kept in sync.
func (s *Store) RotateWithPassphrase(newPassphrase string) error {
	if newPassphrase == "" {
		return fmt.Errorf("credentials: passphrase must not be empty")
	}
	// Generate salt ONCE — used for both derivation and persistence.
	newSalt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, newSalt); err != nil {
		return fmt.Errorf("credentials: generate salt: %w", err)
	}
	newKey := argon2.IDKey([]byte(newPassphrase), newSalt, argonTime, argonMemory, argonThreads, keyLen)
	// rotateFull copies newKey into the store before it returns, so this
	// derivation buffer can be overwritten on the way out.
	defer wipe(newKey)
	return s.rotateFull(newKey, newSalt)
}

// rotateFull holds the lock, re-encrypts all credentials with newKey, and
// persists newSalt. The caller is responsible for ensuring newKey was derived
// from newSalt so they remain consistent.
func (s *Store) rotateFull(newKey, newSalt []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrStoreLocked
	}
	// Hold the replaced key explicitly so it is still reachable, and still
	// wipeable, after s.key has been repointed at the new one.
	oldKey := s.key

	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}

	// Decrypt all with old key, re-encrypt with new key.
	newCredentials := make(map[string]encEntry, len(sf.Credentials))
	for name, entry := range sf.Credentials {
		plain, err := decrypt(oldKey, name, entry)
		if err != nil {
			// The name is already in the error decrypt returns.
			return fmt.Errorf("credentials: rotate: %w", err)
		}
		newEntry, err := encrypt(newKey, name, []byte(plain))
		if err != nil {
			return fmt.Errorf("credentials: rotate encrypt %q: %w", name, err)
		}
		newCredentials[name] = newEntry
	}

	sf.Salt = base64.StdEncoding.EncodeToString(newSalt)
	sf.Credentials = newCredentials
	// A failed save leaves the rotation without effect, so the old key stays
	// live and is deliberately NOT wiped on this path.
	if err := s.saveFileNoLock(sf); err != nil {
		return err
	}

	s.key = make([]byte, keyLen)
	copy(s.key, newKey)

	// From here the replaced key is unreachable from the store, and nothing
	// else holds a reference that would ever clear it. Overwrite it now.
	wipe(oldKey)

	slog.Info("credentials: rotation complete")
	return nil
}

// loadFileInternal reads the store file. Caller must hold s.mu (read or write).
// If the file does not exist, returns an empty storeFile with a fresh salt.
//
// Only the current format version is returned. A pre-upgrade file yields
// ErrLegacyStoreFormat — only unlock migrates it, so no read path ever opens a
// nil-AAD entry and no write path stamps a legacy file with the new version.
func (s *Store) loadFileInternal() (*storeFile, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		// Create empty store with fresh salt on first use.
		salt := make([]byte, saltLen)
		if _, saltErr := io.ReadFull(rand.Reader, salt); saltErr != nil {
			return nil, fmt.Errorf("credentials: generate initial salt: %w", saltErr)
		}
		return &storeFile{
			Version:     storeVersion,
			Salt:        base64.StdEncoding.EncodeToString(salt),
			Credentials: make(map[string]encEntry),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credentials: read store file: %w", err)
	}

	var sf storeFile
	if unmarshalErr := json.Unmarshal(data, &sf); unmarshalErr != nil {
		// Corrupted file: log and refuse to overwrite.
		slog.Error("credentials: store file is corrupted — fix or delete it manually",
			"path", s.path, "error", unmarshalErr)
		return nil, fmt.Errorf("credentials: store file corrupted (manual fix required): %w", unmarshalErr)
	}
	switch sf.Version {
	case storeVersion:
	case legacyStoreVersion:
		return nil, ErrLegacyStoreFormat
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedStoreVersion, sf.Version)
	}
	if sf.Credentials == nil {
		sf.Credentials = make(map[string]encEntry)
	}
	return &sf, nil
}

// saveFileNoLock writes sf atomically with an advisory OS-level flock;
// caller holds s.mu write lock (single-writer goroutine serialization).
// The flock is defense-in-depth for multi-process scenarios.
func (s *Store) saveFileNoLock(sf *storeFile) error {
	sf.Version = storeVersion
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("credentials: marshal store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("credentials: create store dir: %w", err)
	}
	// Lock the sidecar credentials.json.lock, never credentials.json itself:
	// locking the store file created it EMPTY on the first write, which
	// loadFileInternal then reports as corrupted (see fileutil.SidecarLockPath).
	// The sidecar is created 0600 like the store, and the sandbox already
	// withholds it from agents — fspolicy's secret set covers every
	// credentials.json.<suffix> name.
	return fileutil.WithFlock(fileutil.SidecarLockPath(s.path), func() error {
		return writeFileAtomicFn(s.path, data, 0o600)
	})
}

// writeFileAtomicFn is fileutil.WriteFileAtomic, held in a package variable so
// a test can pause a store write inside its lock (store_lock_test.go).
// Production code never reassigns it.
var writeFileAtomicFn = fileutil.WriteFileAtomic

// loadOrCreateSalt reads the salt from the existing credentials.json or
// generates a fresh one if the file does not yet exist.
func (s *Store) loadOrCreateSalt() ([]byte, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		salt := make([]byte, saltLen)
		if _, saltErr := io.ReadFull(rand.Reader, salt); saltErr != nil {
			return nil, fmt.Errorf("credentials: generate salt: %w", saltErr)
		}
		// Persist the salt so subsequent unlocks use the same KDF output.
		sf := &storeFile{
			Version:     storeVersion,
			Salt:        base64.StdEncoding.EncodeToString(salt),
			Credentials: make(map[string]encEntry),
		}
		raw, marshalErr := json.MarshalIndent(sf, "", "  ")
		if marshalErr != nil {
			return nil, fmt.Errorf("credentials: marshal initial store: %w", marshalErr)
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(s.path), 0o700); mkdirErr != nil {
			return nil, fmt.Errorf("credentials: create store dir: %w", mkdirErr)
		}
		if writeErr := fileutil.WriteFileAtomic(s.path, raw, 0o600); writeErr != nil {
			return nil, fmt.Errorf("credentials: persist salt: %w", writeErr)
		}
		return salt, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credentials: read store for salt: %w", err)
	}

	var sf storeFile
	if unmarshalErr := json.Unmarshal(data, &sf); unmarshalErr != nil {
		return nil, fmt.Errorf("credentials: parse store for salt: %w", unmarshalErr)
	}
	salt, decodeErr := base64.StdEncoding.DecodeString(sf.Salt)
	if decodeErr != nil {
		return nil, fmt.Errorf("credentials: decode salt: %w", decodeErr)
	}
	return salt, nil
}

// aadFor returns the additional authenticated data that binds a ciphertext to
// the name it is stored under. See the package doc for why the version tag is
// part of the AAD.
func aadFor(name string) []byte {
	return []byte(aadDomainTag + name)
}

// encrypt seals plaintext with AES-256-GCM using key, binding the envelope to
// name via the AAD so the ciphertext only opens under that same name.
func encrypt(key []byte, name string, plaintext []byte) (encEntry, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return encEntry{}, fmt.Errorf("credentials: cipher init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return encEntry{}, fmt.Errorf("credentials: gcm init: %w", err)
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return encEntry{}, fmt.Errorf("credentials: generate nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, aadFor(name))
	return encEntry{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// decrypt opens an AES-256-GCM ciphertext, requiring the AAD that binds it to
// name. A ciphertext sealed under a different name, edited in place, or sealed
// under a different master key fails authentication and yields a
// *EntryAuthError naming the entry. Malformed base64 or nonce length yields
// *EntryEncodingError so callers can distinguish entry damage from a file,
// lock or cipher-initialization failure without parsing an error message.
func decrypt(key []byte, name string, entry encEntry) (string, error) {
	nonce, err := base64.StdEncoding.DecodeString(entry.Nonce)
	if err != nil {
		return "", &EntryEncodingError{Name: name, Err: fmt.Errorf("nonce is not valid base64: %w", err)}
	}
	ct, err := base64.StdEncoding.DecodeString(entry.Ciphertext)
	if err != nil {
		return "", &EntryEncodingError{Name: name, Err: fmt.Errorf("ciphertext is not valid base64: %w", err)}
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("credentials: cipher init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("credentials: gcm init: %w", err)
	}
	// GCM.Open panics on a nonce of the wrong length; a damaged entry is a
	// clear, named error for that entry, never a crash (founder 2026-10-15).
	if len(nonce) != gcm.NonceSize() {
		return "", &EntryEncodingError{Name: name, Err: fmt.Errorf("nonce must be %d bytes, got %d", gcm.NonceSize(), len(nonce))}
	}
	plain, err := gcm.Open(nil, nonce, ct, aadFor(name))
	if err != nil {
		return "", &EntryAuthError{Name: name}
	}
	return string(plain), nil
}
