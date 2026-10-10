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

// CutoverSavedChatsAtBoot is the EXPLICIT boot entry for the one-time CONV
// saved-chat cutover (architect Q10). It runs over a single session store base
// directory and MUST be invoked by the boot flow BEFORE any session store
// (shared or per-agent) is constructed — before caches warm, before
// list/attach, before retention or any task/scheduler/agent dispatch — so no
// runtime path ever observes the pre-cutover shape.
//
// It is exported precisely so the agent boot flow can run the cutover ahead of
// registry/session-store construction; the store constructor calls it too
// (convergeSavedChats) as a boot-constructor backstop. Both paths share this
// one implementation, so the conversion is idempotent across the two calls:
// a completed cutover performs no write on the second pass.
//
// Idempotent and retry-safe: a completed conversion is recognized and not
// rewritten; an interrupted one is finished without appending a second copy.
// Any failure is a visible cutover error naming the affected saved chat.
func CutoverSavedChatsAtBoot(baseDir string) error {
	return CutoverSavedChatsAtBootWithAgentStores(baseDir, nil)
}

// CutoverSavedChatsAtBootWithAgentStores is the cutover that also takes in the
// retired per-agent stores (DEL-10, spec CONV Inputs/Destination): every
// per-agent session directory moves into the shared store under the same id,
// and every per-agent `.context` model archive is converted into the chat it
// belongs to in the shared store. A same-id conflict refuses the cutover
// visibly and leaves the source in place; a completed transfer is not repeated.
func CutoverSavedChatsAtBootWithAgentStores(baseDir string, agentSessionDirs []string) error {
	sources := convAgentSources(baseDir, agentSessionDirs)
	for _, dir := range sources {
		if err := convMoveAgentSessionDirs(baseDir, dir); err != nil {
			return err
		}
	}
	// Faithful model-content conversion FIRST (unified_conv_archive.go): the
	// legacy .context model archives become the addressed archive the runtime now
	// reads. Then the metadata/recovery passes.
	// The chat partitions convert first: the model conversion appends into the
	// same files, and its projection identities name converted chat records.
	if err := convConvergeTranscripts(baseDir); err != nil {
		return err
	}
	if err := convConvertLegacyModelArchives(baseDir); err != nil {
		return err
	}
	for _, dir := range sources {
		if err := convConvertLegacyModelArchivesFrom(baseDir, filepath.Join(dir, convContextDir), true); err != nil {
			return err
		}
	}
	if err := convConvergeFlatJSONLSources(baseDir); err != nil {
		return err
	}
	return convNormalizeSavedChatDirs(baseDir)
}

// convergeSavedChats runs CONV over this store's base directory; it is the
// boot-constructor backstop for the explicit CutoverSavedChatsAtBoot entry
// above (both share one implementation).
func (us *UnifiedStore) convergeSavedChats() error {
	return CutoverSavedChatsAtBoot(us.baseDir)
}

// convConvergeFlatJSONLSources handles the legacy flat "<id>.jsonl" sources
// that UnifiedStore.migrateLegacy used to wrap into a session directory (DEL-09
// deletes that method; CONV is its replacement). Only a file directly in the
// base dir is a source — session directories and the ".context" backend are
// skipped.
func convConvergeFlatJSONLSources(baseDir string) error {
	entries, err := os.ReadDir(baseDir)
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
		if err := convConvergeFlatJSONLSource(baseDir, id); err != nil {
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
func convConvergeFlatJSONLSource(baseDir, id string) error {
	src := filepath.Join(baseDir, id+".jsonl")
	sessionDir := filepath.Join(baseDir, id)
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
func convNormalizeSavedChatDirs(baseDir string) error {
	entries, err := os.ReadDir(baseDir)
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
		sessionDir := filepath.Join(baseDir, e.Name())
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
// The retired handover "active_agent_id" is deliberately NOT a trigger: its
// presence alone does not make a chat pre-cutover. It is dropped only as PART
// of converting a genuinely pre-cutover chat, where keeping "agent_id" as the
// immutable owner is the requirement.
func convNeedsNormalization(f u5IdentityFile) bool {
	return f.Type == ""
}

// convWriteIdentityFile is the CONV-ONLY current-format publisher (architect
// Q12): it atomically writes a session directory's meta.json from its parsed
// identity, in the SAME identity-file shape u5WriteIdentityLocked uses — so a
// converted saved chat is byte-for-byte a current-format session and a
// completed conversion round-trips unchanged. It reuses the lock/atomic-write
// primitives (fileutil.WithFlock + the writeFileAtomicFn seam) that the DEL-09
// generic helper writeUnifiedMetaDirect used, but publishes ONLY the
// current-format identity group a conversion needs — it is not a general meta
// writer. CONV runs before the store has escaped its constructor, so no
// in-process shard lock is required; the OS sidecar flock still guards a
// concurrent cross-process writer.
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
