// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ReadSecretRequestState reads session-owned, value-free interaction metadata.
// The caller owns its internal schema; this is never a SPA-read file. Keeping
// it inside the session directory makes deletion and retention cascade normally.
func (us *UnifiedStore) ReadSecretRequestState(sessionID string) ([]byte, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if _, err := us.readMetaLocked(sessionID); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(us.baseDir, sessionID, "secret_requests.json"))
	if errors.Is(err, os.ErrNotExist) {
		return []byte("[]"), nil
	}
	return raw, err
}

// WriteSecretRequestState never creates a deleted session. It holds the same
// session shard as deletion while publishing its complete bounded state.
func (us *UnifiedStore) WriteSecretRequestState(sessionID string, raw []byte) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if !json.Valid(raw) {
		return fmt.Errorf("invalid secret request metadata")
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if _, err := us.readMetaLocked(sessionID); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(us.baseDir, sessionID, "secret_requests.json"), raw, 0o600)
}
