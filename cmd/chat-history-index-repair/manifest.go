package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// manifestSchemaVersion 2 added the "symlink" and "special" entry kinds with
// their link_target / special_type fields. Only the current version is read.
const manifestSchemaVersion = 2

type backupManifest struct { // not-wire-format: private operator inventory
	SchemaVersion        int              `json:"schema_version"`
	SourceRoot           string           `json:"source_root"`
	BackupRoot           string           `json:"backup_root"`
	CreatedAt            string           `json:"created_at"`
	ToolCommit           string           `json:"tool_commit"`
	Entries              []inventoryEntry `json:"entries"`
	CanonicalFingerprint string           `json:"canonical_fingerprint"`
}

func produceManifest(ctx context.Context, a repairArgs, out io.Writer) error {
	source, err := inventoryTree(ctx, a.root, false)
	if err != nil {
		return err
	}
	copyEntries, err := inventoryTree(ctx, a.backup, false)
	if err != nil {
		return err
	}
	if !inventoriesEqual(source, copyEntries, false) {
		return refusal("backup_incomplete")
	}
	// Compare again after hashing the actual copy, so a moving source cannot
	// be certified from an older inventory observation.
	again, err := inventoryTree(ctx, a.root, false)
	if err != nil {
		return err
	}
	if !inventoriesEqual(source, again, false) {
		return refusal("source_changed")
	}
	commit, err := builtToolCommit()
	if err != nil {
		return err
	}
	m := backupManifest{SchemaVersion: manifestSchemaVersion, SourceRoot: a.root, BackupRoot: a.backup, CreatedAt: time.Now().UTC().Format(time.RFC3339), ToolCommit: commit, Entries: copyEntries, CanonicalFingerprint: canonicalFingerprint(source)}
	if err = writeRepairJSON(a.output, "backup-manifest.schema.json", m); err != nil {
		return err
	}
	files, dirs, links, special := inventoryCounts(copyEntries)
	fmt.Fprintf(out, "mode=manifest files=%d directories=%d symlinks=%d special=%d entries=%d verified=1\n", files, dirs, links, special, len(copyEntries))
	return nil
}
func loadBackupManifest(a repairArgs) (backupManifest, error) {
	var m backupManifest
	b, err := os.ReadFile(a.manifest)
	if errors.Is(err, os.ErrNotExist) {
		return m, refusal("backup_missing")
	}
	if err != nil {
		return m, integrity("io_error", err)
	}
	var version struct {
		Version int `json:"schema_version"`
	}
	if json.Unmarshal(b, &version) == nil && version.Version != manifestSchemaVersion {
		return m, refusal("unsupported_manifest_version")
	}
	if err = validateRepairJSON("backup-manifest.schema.json", b, &m); err != nil {
		return m, refusal("backup_incomplete")
	}
	if m.SourceRoot != a.root || m.BackupRoot != a.backup {
		return m, refusal("backup_wrong_root")
	}
	if !utcTimestampValid(m.CreatedAt) {
		return m, refusal("backup_incomplete")
	}
	previous := ""
	seen := map[string]string{}
	for _, e := range m.Entries {
		if !validInventoryPath(e.Path) || e.Path <= previous {
			return m, refusal("unsafe_path")
		}
		folded := strings.ToLower(e.Path)
		if _, exists := seen[folded]; exists {
			return m, refusal("unsafe_path")
		}
		parent := path.Dir(e.Path)
		if parent != "." && seen[strings.ToLower(parent)] != "directory" {
			return m, refusal("backup_incomplete")
		}
		seen[folded] = e.Kind
		previous = e.Path
		n, err := strconv.ParseInt(e.Mtime, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != e.Mtime {
			return m, refusal("backup_incomplete")
		}
	}
	if canonicalFingerprint(m.Entries) != m.CanonicalFingerprint {
		return m, refusal("backup_tampered")
	}
	return m, nil
}
func verifyBackupAndSource(ctx context.Context, a repairArgs) error {
	m, err := loadBackupManifest(a)
	if err != nil {
		return err
	}
	backup, err := inventoryTree(ctx, a.backup, false)
	if err != nil {
		return err
	}
	if !inventoriesEqual(m.Entries, backup, false) {
		return refusal("backup_tampered")
	}
	source, err := inventoryTree(ctx, a.root, false)
	if err != nil {
		return err
	}
	if !inventoriesEqual(m.Entries, source, true) || canonicalFingerprint(source) != m.CanonicalFingerprint {
		return refusal("source_changed")
	}
	return nil
}
