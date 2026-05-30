// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package credentials implements the Omnipus encrypted credential store
// per BRD SEC-23/23a–e and Wave 1 user stories US-3, US-4, US-11, US-12.
//
// Storage format (credentials.json):
//
//	{
//	  "version": 1,
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
// Encryption: AES-256-GCM.
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
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

const (
	storeVersion = 1
	saltLen      = 32
	nonceLen     = 12
	keyLen       = 32

	// Argon2id parameters per SEC-23b.
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
)

// ErrStoreLocked is returned when the credential store is not unlocked.
var ErrStoreLocked = errors.New(
	"credentials: store is locked — provide master key via OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, or interactive passphrase",
)

// ErrWrongKey is returned when AES-GCM authentication fails (wrong key).
var ErrWrongKey = errors.New("credentials: decryption failed — wrong master key?")

// NotFoundError is returned when a credential name is not in the store.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("credentials: %q not found in credential store", e.Name)
}

// storeFile is the on-disk JSON structure.
type storeFile struct {
	Version     int                 `json:"version"`
	Salt        string              `json:"salt"`
	Credentials map[string]encEntry `json:"credentials"`
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

// UnlockWithKey sets the 32-byte AES-256 key directly.
// Used when the key was provisioned via OMNIPUS_MASTER_KEY or key-file.
func (s *Store) UnlockWithKey(key []byte) error {
	if len(key) != keyLen {
		return fmt.Errorf("credentials: key must be exactly %d bytes, got %d", keyLen, len(key))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = make([]byte, keyLen)
	copy(s.key, key)
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

	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, keyLen)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = key
	return nil
}

// DeriveSubkey derives a 32-byte subkey from the unlocked master key using
// HKDF-SHA256 with the supplied info string as the domain-separation tag.
//
// The master key itself is NEVER returned — every subsystem that needs key
// material (e.g. the audit-chain HMAC for v0.2 #155) must request a derived
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

	entry, err := encrypt(s.key, []byte(value))
	if err != nil {
		return fmt.Errorf("credentials: encrypt %q: %w", name, err)
	}
	sf.Credentials[name] = entry

	return s.saveFileNoLock(sf)
}

// Get decrypts and returns the credential named name.
// Returns NotFoundError if the name is not present, ErrWrongKey on auth failure.
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

	plain, err := decrypt(s.key, entry)
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

	return s.saveFileNoLock(sf)
}

// Rotate re-encrypts all credentials with newKey.
// The old key must already be loaded (store must be unlocked).
// A fresh random salt is generated and persisted alongside the new ciphertext.
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

	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}

	// Decrypt all with old key, re-encrypt with new key.
	newCredentials := make(map[string]encEntry, len(sf.Credentials))
	for name, entry := range sf.Credentials {
		plain, err := decrypt(s.key, entry)
		if err != nil {
			return fmt.Errorf("credentials: rotate decrypt %q: %w", name, err)
		}
		newEntry, err := encrypt(newKey, []byte(plain))
		if err != nil {
			return fmt.Errorf("credentials: rotate encrypt %q: %w", name, err)
		}
		newCredentials[name] = newEntry
	}

	sf.Salt = base64.StdEncoding.EncodeToString(newSalt)
	sf.Credentials = newCredentials
	if err := s.saveFileNoLock(sf); err != nil {
		return err
	}

	s.key = newKey
	slog.Info("credentials: rotation complete")
	return nil
}

// loadFileInternal reads the store file. Caller must hold s.mu (read or write).
// If the file does not exist, returns an empty storeFile with a fresh salt.
func (s *Store) loadFileInternal() (*storeFile, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
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
	return fileutil.WithFlock(s.path, func() error {
		return fileutil.WriteFileAtomic(s.path, data, 0o600)
	})
}

// loadOrCreateSalt reads the salt from the existing credentials.json or
// generates a fresh one if the file does not yet exist.
func (s *Store) loadOrCreateSalt() ([]byte, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
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

// encrypt seals plaintext with AES-256-GCM using key.
func encrypt(key, plaintext []byte) (encEntry, error) {
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
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return encEntry{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// decrypt opens an AES-256-GCM ciphertext. Returns ErrWrongKey on auth failure.
func decrypt(key []byte, entry encEntry) (string, error) {
	nonce, err := base64.StdEncoding.DecodeString(entry.Nonce)
	if err != nil {
		return "", fmt.Errorf("credentials: decode nonce: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(entry.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("credentials: decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("credentials: cipher init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("credentials: gcm init: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", ErrWrongKey
	}
	return string(plain), nil
}
