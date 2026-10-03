// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const bootEpochFileName = "boot_epoch.json"

// BootEpochStore is the process-lifetime monotonic boot counter persisted
// at <dir>/boot_epoch.json. One store instance may mint once. A later boot
// is a new instance over the same directory; the number lives in the file.
//
// On Windows, fileutil.WithFlock does not lock across processes (founder
// decision D1: that limitation is accepted for this counter only). Two
// Omnipus processes started together can therefore mint the same number.
// The in-process mutex still serializes callers of one instance.
type BootEpochStore struct {
	dir    string
	mu     sync.Mutex
	minted uint64
}

// NewBootEpochStore returns a store for dir. It does not read or create the
// file; Current is 0 until Mint succeeds.
func NewBootEpochStore(dir string) *BootEpochStore {
	return &BootEpochStore{dir: dir}
}

// Current returns the epoch minted by this instance, or 0 when Mint has not
// succeeded.
func (s *BootEpochStore) Current() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.minted
}

// Mint reads the counter, adds one, and persists it. A missing file mints 1.
// A second call on this instance fails without touching the file. A corrupt
// file, a file missing boot_epoch, an unreadable file, or a stored value
// that cannot grow inside the int64 wire fails visibly and is left as it
// was — never rewritten as 1.
func (s *BootEpochStore) Mint() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.minted != 0 {
		return 0, fmt.Errorf("session: boot epoch already minted (%d); a second Mint on this instance is a wiring bug", s.minted)
	}
	if s.dir == "" {
		return 0, fmt.Errorf("session: boot epoch mint: data dir is empty")
	}

	path := filepath.Join(s.dir, bootEpochFileName)
	var next uint64
	err := fileutil.WithFlock(fileutil.SidecarLockPath(path), func() error {
		current, missing, readErr := readStoredBootEpoch(path)
		if readErr != nil {
			return readErr
		}
		if !missing && current >= uint64(math.MaxInt64) {
			return fmt.Errorf("session: boot_epoch.json overflow: stored boot_epoch %d; the next value would exceed int64", current)
		}
		if missing {
			next = 1
		} else {
			next = current + 1
		}
		payload, marshalErr := json.Marshal(bootEpochFile{BootEpoch: next})
		if marshalErr != nil {
			return fmt.Errorf("session: encode boot_epoch.json: %w", marshalErr)
		}
		if writeErr := fileutil.WriteFileAtomicSyncDir(path, payload, 0o600); writeErr != nil {
			return fmt.Errorf("session: write boot_epoch.json: %w", writeErr)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.minted = next
	return next, nil
}

type bootEpochFile struct {
	BootEpoch uint64 `json:"boot_epoch"`
}

// readStoredBootEpoch reads the one-key counter file.
// missing is true only when the file is absent, which is a fresh install.
func readStoredBootEpoch(path string) (value uint64, missing bool, err error) {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if errors.Is(readErr, fs.ErrNotExist) {
			return 0, true, nil
		}
		return 0, false, fmt.Errorf("session: read boot_epoch.json: %w", readErr)
	}
	var payload map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
		return 0, false, fmt.Errorf("session: boot_epoch.json is corrupt: %w", unmarshalErr)
	}
	raw, ok := payload["boot_epoch"]
	if !ok {
		return 0, false, fmt.Errorf("session: boot_epoch.json is missing boot_epoch")
	}
	var decoded uint64
	if decodeErr := json.Unmarshal(raw, &decoded); decodeErr != nil {
		return 0, false, fmt.Errorf("session: boot_epoch.json has an unreadable boot_epoch: %w", decodeErr)
	}
	if decoded == 0 {
		return 0, false, fmt.Errorf("session: boot_epoch.json boot_epoch must be >= 1")
	}
	return decoded, false, nil
}
