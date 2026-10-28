// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Main-session owner attribution (founder decision 2026-10-28).
//
// Owner on a main is the username of the person the main is attributed to.
// It is attribution only, never access control: nothing here changes who may
// read or write a session, and the main's computed id never changes.
//
// Two entry points:
//
//   - GetOrCreateMainSessionOwned stamps the owner in the SAME single identity
//     write that creates a main. An existing main is returned unchanged —
//     create-or-reuse never doubles as adoption.
//   - AdoptMainSessionOwner gives an ownerless main its first owner. The
//     "owner is empty" check and the write are one compare-and-set step under
//     the session shard AND the meta.json sidecar flock, re-reading meta.json
//     inside that lock so a second store (or process) over the same directory
//     cannot also win. The write changes ONLY the owner: meta.json's own
//     updated_at, stats.json and every other identity field are kept, so an
//     adoption is not session activity and never re-orders a session list.
package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// GetOrCreateMainSessionOwned is GetOrCreateMainSession with an owner for the
// create case: a main that does not exist yet is created with Owner = owner in
// its first identity write (an empty owner creates an ownerless main, for an
// install with no account). A main that already exists is returned exactly as
// stored — its owner, empty or not, is never set or overwritten here; use
// AdoptMainSessionOwner for that. The pair validation and refusals are those
// of GetOrCreateMainSession.
func (us *UnifiedStore) GetOrCreateMainSessionOwned(workspaceID, agentID, owner string) (*UnifiedMeta, error) {
	return us.getOrCreateMainSession(workspaceID, agentID, owner)
}

// AdoptMainSessionOwner persists owner as the first owner of an ownerless main.
//
// It returns true only when it wrote the owner. It returns false and writes
// nothing — without an error — when owner is empty, the session is not a main,
// or the main already has an owner (whoever it is, including owner itself).
// A missing or unreadable session is an error.
//
// The compare (type main, owner empty) and the write happen inside one hold of
// the session shard and the meta.json sidecar flock, against meta.json as read
// inside that hold — not against this store's cache, which another store over
// the same directory could have made stale. Only the owner changes: the
// identity file's own updated_at is preserved (adoption is not activity), and
// stats.json is not touched. The cache is updated only after the write
// succeeded, so a failed write never publishes an unpersisted owner.
//
// On Windows fileutil.WithFlock is a documented no-op, so the cross-store /
// cross-process half of the guarantee holds on Linux and macOS only; within
// one store the session shard still makes the compare-and-set atomic.
func (us *UnifiedStore) AdoptMainSessionOwner(sessionID, owner string) (bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	if owner == "" {
		return false, nil
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()

	sessionDir := filepath.Join(us.baseDir, sessionID)
	metaPath := filepath.Join(sessionDir, "meta.json")
	adopted := false
	err := fileutil.WithFlock(sessionFileLockPath(metaPath), func() error {
		identity, err := u5ReadIdentityFile(sessionDir)
		if err != nil {
			return err
		}
		if identity.Type != SessionTypeMain || identity.Owner != "" {
			return nil
		}
		identity.Owner = owner
		data, err := json.MarshalIndent(identity, "", "  ")
		if err != nil {
			return fmt.Errorf("unified_store: marshal meta.json: %w", err)
		}
		if err := writeFileAtomicFn(metaPath, data, 0o600); err != nil {
			return err
		}
		adopted = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("unified_store: adopt owner of main %q: %w", sessionID, err)
	}
	if !adopted {
		return false, nil
	}

	// Disk first, then ONLY the owner field of the live cache entry (FR-084:
	// never a wholesale replace). An uncached session is composed from disk on
	// its next read, which now carries the owner.
	us.cacheMu.Lock()
	if cached, ok := us.metaCache[sessionID]; ok {
		cached.Owner = owner
	}
	us.cacheMu.Unlock()
	return true, nil
}
