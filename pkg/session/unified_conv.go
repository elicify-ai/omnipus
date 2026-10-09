// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core CONV — the ONE-TIME saved-chat cutover (spec section "CONV —
// One-time saved-chat cutover only"; FR-038's sole exception to the
// no-importer rule; DEL-09/10/11; BDD-12.6; T35; founder Q2=B narrowed).
//
// CONV is the ONLY legacy reader left in the runtime. It is the replacement for
// the deleted pkg/session/unified.go::migrateLegacy and
// pkg/memory/migration.go::MigrateFromJSON general importers (DEL-09): once a
// saved chat has been converted to current format, no runtime path ever reads
// the pre-cutover shape again. It is deliberately NOT a permanent importer, a
// second live store, a conversion ledger or an address map (spec CONV /
// Explicit exclusions) — it normalizes what it finds once and then gets out of
// the way.
//
// It runs at first cutover boot, from the session-store boot constructor,
// BEFORE loadMetaCacheLocked populates the cache — so the session cache/list/
// attach never observe the pre-cutover shape (spec CONV / Ordering / sole
// reader). A store already in current format is left byte-for-byte untouched
// (nothing is rewritten) and a fresh install with no saved chats performs zero
// writes (spec CONV / Publication / Completion).
//
// Publication follows the spec's ordering: verify/materialize the same-ID
// current content FIRST, then retire the legacy source — never remove the only
// readable copy first. A read/parse/write failure is a VISIBLE cutover error
// naming the affected saved chat (constructor error), never a silent exclusion
// (spec CONV / Failure).
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// convergeSavedChats runs CONV over this store's base directory. Idempotent and
// retry-safe: a completed conversion is recognized and not rewritten; an
// interrupted one is finished without appending a second copy. Any failure is a
// visible cutover error naming the affected saved chat.
func (us *UnifiedStore) convergeSavedChats() error {
	if err := us.convConvergeFlatJSONLSources(); err != nil {
		return err
	}
	return us.convNormalizeSavedChatDirs()
}

// convConvergeFlatJSONLSources handles the legacy flat "<id>.jsonl" sources
// that UnifiedStore.migrateLegacy used to wrap into a session directory (DEL-09
// deletes that method; CONV is its replacement). Only a file directly in the
// base dir is a source — session directories and the ".context" backend are
// skipped.
func (us *UnifiedStore) convConvergeFlatJSONLSources() error {
	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("conversion: read sessions dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if id == "" {
			continue
		}
		if err := us.convConvergeFlatJSONLSource(id); err != nil {
			return err
		}
	}
	return nil
}

// convConvergeFlatJSONLSource converts one flat "<id>.jsonl" source:
//
//   - destination "context.jsonl" already holds BYTE-IDENTICAL content → the
//     conversion is materialized and only the source needs retiring (an
//     interrupted boot the next one finishes, spec CONV / Publication);
//   - destination holds DIFFERENT content → the conversion conflicts and
//     refuses visibly, never overwriting (no last-wins);
//   - otherwise the content is materialized first, then the source retired.
func (us *UnifiedStore) convConvergeFlatJSONLSource(id string) error {
	src := filepath.Join(us.baseDir, id+".jsonl")
	sessionDir := filepath.Join(us.baseDir, id)
	dst := filepath.Join(sessionDir, "context.jsonl")

	srcData, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("conversion: saved chat %q: read legacy source: %w", id, err)
	}

	if dstData, dstErr := os.ReadFile(dst); dstErr == nil {
		if !bytes.Equal(srcData, dstData) {
			return fmt.Errorf(
				"conversion: saved chat %q: destination %q already holds different content; refusing to overwrite",
				id, dst)
		}
		return convRetireLegacySource(id, src)
	}

	if mkErr := os.MkdirAll(sessionDir, 0o700); mkErr != nil {
		return fmt.Errorf("conversion: saved chat %q: create session dir: %w", id, mkErr)
	}
	if wErr := fileutil.WriteFileAtomic(dst, srcData, 0o600); wErr != nil {
		return fmt.Errorf("conversion: saved chat %q: write context.jsonl: %w", id, wErr)
	}
	// A flat source carries no identity of its own beyond the file name, so give
	// it the current chat identity ONCE (as migrateLegacy did) to keep it
	// listable/continuable. A meta.json already present is left for the
	// normalization pass to reconcile.
	metaPath := filepath.Join(sessionDir, "meta.json")
	if _, statErr := os.Stat(metaPath); errors.Is(statErr, fs.ErrNotExist) {
		now := time.Now().UTC()
		identity := u5IdentityFile{
			ID:         id,
			Status:     StatusActive,
			CreatedAt:  now,
			UpdatedAt:  now,
			Partitions: []string{},
			Type:       SessionTypeChat,
		}
		if err := convWriteIdentityFile(sessionDir, identity); err != nil {
			return fmt.Errorf("conversion: saved chat %q: write meta.json: %w", id, err)
		}
	}
	return convRetireLegacySource(id, src)
}

// convRetireLegacySource removes a legacy source whose converted same-ID copy
// is already on disk. The converted copy is the readable one, so retiring the
// source never removes the only readable copy (spec CONV / Publication).
func convRetireLegacySource(id, src string) error {
	if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("conversion: saved chat %q: retire legacy source: %w", id, err)
	}
	return nil
}

// convNormalizeSavedChatDirs converts pre-cutover saved session DIRECTORY
// identities to current format: a missing "type" is written ONCE as "chat"
// (a valid existing non-heartbeat type is kept) and the retired
// "active_agent_id" is DROPPED by keeping "agent_id" as the immutable owner —
// never adopted as the owner (spec CONV / Identity; DEL-11). The directory's
// original ID/agent_id/workspace_id/title/timestamps/references are preserved
// byte-for-byte; no workspace is invented.
//
// A meta.json that is PRESENT but unreadable/unparseable is a corrupt saved
// chat: CONV refuses with a visible error and retains the source bytes (spec
// CONV / Failure). A directory with NO meta.json is not a saved chat and is
// left alone (nothing to strand), matching the store's existing lenient
// treatment of a non-session directory.
func (us *UnifiedStore) convNormalizeSavedChatDirs() error {
	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("conversion: read sessions dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".context" {
			continue
		}
		sessionDir := filepath.Join(us.baseDir, e.Name())
		identity, rErr := u5ReadIdentityFile(sessionDir)
		if rErr != nil {
			if errors.Is(rErr, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf(
				"conversion: saved chat %q: unreadable metadata; refusing cutover: %w",
				e.Name(), rErr)
		}
		if !convNeedsNormalization(identity) {
			continue // already current format — no write (idempotent)
		}
		if identity.Type == "" {
			identity.Type = SessionTypeChat
		}
		identity.ActiveAgentID = ""
		if wErr := convWriteIdentityFile(sessionDir, identity); wErr != nil {
			return fmt.Errorf(
				"conversion: saved chat %q: write normalized metadata: %w",
				e.Name(), wErr)
		}
	}
	return nil
}

// convNeedsNormalization reports whether a parsed saved-chat identity is a
// PRE-CUTOVER one. The marker is the MISSING now-required "type" (spec CONV /
// Identity: "Missing pre-cutover type is written once as chat"); a valid
// existing type means the chat is already current and must NOT be rewritten
// (spec CONV / Completion).
//
// The retired handover "active_agent_id" is deliberately NOT a trigger: it is
// still a LIVE field on current sessions — the handoff tool's SwitchAgent
// (unified_write.go) writes it and the runtime resolves the owner through it —
// so treating its presence as "pre-cutover" would strip a real handover on
// every boot. It is dropped only as PART of converting a genuinely pre-cutover
// chat, where keeping "agent_id" as the immutable owner is the requirement.
func convNeedsNormalization(f u5IdentityFile) bool {
	return f.Type == ""
}

// convWriteIdentityFile atomically writes a session directory's meta.json from
// its parsed identity, in the SAME identity-file shape u5WriteIdentityLocked
// uses — so a converted saved chat is byte-for-byte a current-format session
// and a completed conversion round-trips unchanged. CONV runs before the store
// has escaped its constructor, so no in-process shard lock is required; the OS
// sidecar flock still guards a concurrent cross-process writer.
func convWriteIdentityFile(sessionDir string, identity u5IdentityFile) error {
	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return fmt.Errorf("conversion: marshal meta.json: %w", err)
	}
	metaPath := filepath.Join(sessionDir, "meta.json")
	return fileutil.WithFlock(sessionFileLockPath(metaPath), func() error {
		return writeFileAtomicFn(metaPath, data, 0o600)
	})
}
