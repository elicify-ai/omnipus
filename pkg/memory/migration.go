package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// jsonSession mirrors pkg/session.Session for migration purposes.
type jsonSession struct {
	Key      string              `json:"key"`
	Messages []providers.Message `json:"messages"`
	Created  time.Time           `json:"created"`
	Updated  time.Time           `json:"updated"`
}

// MigrateFromJSON reads legacy sessions/*.json files from sessionsDir,
// writes them into the store, and renames each migrated file to
// .json.migrated as a backup. Returns the number of sessions migrated.
//
// Files that fail to parse are logged and skipped. Already-migrated
// files (.json.migrated) are ignored, making the function idempotent.
//
// Only StoreWriter (SetHistory) is required — migration never
// reads back through the interface, so the narrower type is sufficient
// (interface segregation: this is the one clear, safe narrowing call site
// for the Store split, since every other production caller round-trips
// reads and writes and is left on the composed Store type).
func MigrateFromJSON(
	ctx context.Context, sessionsDir string, store StoreWriter,
) (int, error) {
	entries, err := os.ReadDir(sessionsDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("memory: read sessions dir: %w", err)
	}

	migrated := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// Skip JSONL metadata files. They are part of the new storage format,
		// not legacy session snapshots, and re-importing them would overwrite
		// the paired .jsonl history with an empty message list.
		if strings.HasSuffix(name, ".meta.json") {
			continue
		}
		// Skip already-migrated files.
		if strings.HasSuffix(name, ".migrated") {
			continue
		}

		srcPath := filepath.Join(sessionsDir, name)

		data, readErr := os.ReadFile(srcPath)
		if readErr != nil {
			logger.WarnCF("memory", "migrate: skipping file", map[string]any{"file": name, "error": readErr.Error()})
			continue
		}

		var sess jsonSession
		if parseErr := json.Unmarshal(data, &sess); parseErr != nil {
			logger.WarnCF(
				"memory",
				"migrate: skipping unparseable file",
				map[string]any{"file": name, "error": parseErr.Error()},
			)
			continue
		}

		// Use the key from the JSON content, not the filename.
		// Filenames are sanitized (":" → "_") but keys are not.
		key := sess.Key
		if key == "" {
			key = strings.TrimSuffix(name, ".json")
		}

		// Use SetHistory (atomic first fill) instead of per-message
		// AddFullMessage. This makes migration idempotent: the JSONL is
		// written with one atomic rename, so if the process crashes after
		// writing but before the .migrated rename below, the retry finds
		// a complete, non-empty archive. SetHistory refuses a non-empty
		// archive (ADR-066 FR-047 — it never rewrites or resets Skip), and
		// that refusal is exactly the "already imported" signal: skip the
		// write, finish the rename, never duplicate.
		if setErr := store.SetHistory(ctx, key, sess.Messages); setErr != nil {
			if !errors.Is(setErr, ErrArchiveNotEmpty) {
				return migrated, fmt.Errorf(
					"memory: migrate %s: set history: %w",
					name, setErr,
				)
			}
			logger.InfoCF("memory", "migrate: archive already populated, finishing rename only",
				map[string]any{"file": name, "key": key})
		}

		// Rename to .migrated as backup (not delete).
		renameErr := os.Rename(srcPath, srcPath+".migrated")
		if renameErr != nil {
			logger.WarnCF(
				"memory",
				"migrate: failed to rename migrated file",
				map[string]any{"file": name, "error": renameErr.Error()},
			)
		}

		migrated++
	}

	return migrated, nil
}
