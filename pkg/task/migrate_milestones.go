// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// migrate_milestones.go implements the one-way Milestone→Tag migration
// (ADR-049 D1, spec "Milestone REMOVAL + MIGRATION", FR-026..FR-031). It runs
// once at task-store load (boot), guarded by a completion sentinel file, and
// is idempotent + crash-safe: milestone files are never deleted individually,
// only the whole directory, and only AFTER the sentinel is durably written —
// so Phase 1 always recomputes an identical deterministic mapping from every
// surviving file on any re-run.
package task

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// milestoneMigrationSentinelFile is the completion marker (SD-A6),
// $OMNIPUS_HOME/.milestones_migrated.
const milestoneMigrationSentinelFile = ".milestones_migrated"

// milestoneTagPrefix is prepended to every migrated milestone name.
const milestoneTagPrefix = "milestone:"

// maxMilestoneSuffixHeadroom reserves room for a worst-case "-999"
// disambiguation suffix so the final "milestone:<name>[-N]" tag never
// exceeds maxTagRunes regardless of collision depth (FR-027).
const maxMilestoneSuffixHeadroom = 4 // len("-999")

// legacyMilestone mirrors the on-disk shape of a pre-removal
// ~/.omnipus/milestones/<id>.json file (the now-deleted pkg/gateway
// `milestone` struct). Read-only, migration-local — not a live schema.
type legacyMilestone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	DueDate string `json:"due_date,omitempty"`
}

// milestoneTagAssignment is the Phase-1 computed mapping entry for one
// migrated milestone.
type milestoneTagAssignment struct {
	Tag     string
	DueDate string
	Name    string
}

// MigrateMilestonesToTags runs the one-time Milestone→Tag migration
// (ADR-049 D1, FR-026..031). home is $OMNIPUS_HOME. Safe to call on every
// boot: once the sentinel exists it returns immediately (after
// opportunistically removing any leftover milestones dir from a prior crash
// that landed between the sentinel write and the dir removal).
//
// Any per-file I/O error is logged at Warn and that file is skipped — a
// single corrupt milestone or task file does not abort the whole migration.
func MigrateMilestonesToTags(home string) error {
	sentinelPath := filepath.Join(home, milestoneMigrationSentinelFile)
	if _, err := os.Stat(sentinelPath); err == nil {
		milestonesDir := filepath.Join(home, "milestones")
		if rmErr := os.RemoveAll(milestonesDir); rmErr != nil {
			slog.Warn("task: milestone migration: could not remove leftover milestones dir",
				"dir", milestonesDir, "error", rmErr)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("task: milestone migration: stat sentinel: %w", err)
	}

	milestonesDir := filepath.Join(home, "milestones")
	entries, err := os.ReadDir(milestonesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return writeMigrationSentinel(sentinelPath)
		}
		return fmt.Errorf("task: milestone migration: read milestones dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		files = append(files, e.Name())
	}
	if len(files) == 0 {
		return writeMigrationSentinel(sentinelPath)
	}
	// Ascending by milestone ID (the filename minus .json) for deterministic
	// suffix disambiguation (ADR D1 r2/r3).
	sort.Strings(files)

	var milestones []legacyMilestone
	for _, fname := range files {
		data, rerr := os.ReadFile(filepath.Join(milestonesDir, fname))
		if rerr != nil {
			slog.Warn("task: milestone migration: skip unreadable milestone file", "file", fname, "error", rerr)
			continue
		}
		var m legacyMilestone
		if jerr := json.Unmarshal(data, &m); jerr != nil {
			slog.Warn("task: milestone migration: skip corrupt milestone file", "file", fname, "error", jerr)
			continue
		}
		if m.ID == "" {
			m.ID = strings.TrimSuffix(fname, ".json")
		}
		milestones = append(milestones, m)
	}

	// PHASE 1 — build the milestoneID -> {tag, dueDate, name} mapping from
	// every surviving milestone file, deterministically.
	mapping := make(map[string]milestoneTagAssignment, len(milestones))
	assigned := make(map[string]string, len(milestones)) // tag -> milestoneID
	for _, m := range milestones {
		tag := assignMilestoneTag(m.Name, m.ID, assigned)
		assigned[tag] = m.ID
		mapping[m.ID] = milestoneTagAssignment{Tag: tag, DueDate: m.DueDate, Name: m.Name}
	}

	// PHASE 2 — apply to member tasks. CRITICAL: milestone_id is read from
	// raw JSON (map[string]json.RawMessage), NOT the Task struct — FR-032
	// removes Task.MilestoneID in this same epic, and json.Unmarshal silently
	// drops keys with no matching struct field.
	tasksDir := filepath.Join(home, "tasks")
	emptyMilestones := make(map[string]bool, len(mapping))
	for id := range mapping {
		emptyMilestones[id] = true
	}
	taskEntries, terr := os.ReadDir(tasksDir)
	if terr != nil && !os.IsNotExist(terr) {
		return fmt.Errorf("task: milestone migration: read tasks dir: %w", terr)
	}
	var migrationFailures int
	for _, te := range taskEntries {
		if te.IsDir() || !strings.HasSuffix(te.Name(), ".json") {
			continue
		}
		taskPath := filepath.Join(tasksDir, te.Name())
		if err := migrateOneTaskFile(taskPath, mapping, emptyMilestones); err != nil {
			migrationFailures++
			slog.Warn("task: milestone migration: could not migrate task file", "file", te.Name(), "error", err)
		}
	}

	for id := range emptyMilestones {
		entry := mapping[id]
		slog.Info("task: milestone migration: empty milestone preserved as log entry",
			"milestone_id", id, "name", entry.Name, "due_date", entry.DueDate)
	}

	// PHASE 3 — finalize: sentinel LAST (before dir removal), only after
	// EVERY task write above succeeded (review r1 silent-failure HIGH 2). A
	// partial-failure run must NOT write the sentinel or remove the
	// milestones dir — doing so unconditionally (the pre-fix behavior) meant
	// any per-file Warn logged above was PERMANENT data loss: the sentinel's
	// existence short-circuits every future call at this function's own top,
	// and the milestone source files driving Phase 1's mapping would already
	// be gone. Leaving both undone lets the NEXT boot retry deterministically
	// (Phase 1 recomputes an identical mapping from the still-intact
	// milestone files on any re-run, per this file's own package doc
	// comment) rather than losing the failed file(s)' milestone_id/tag
	// mapping forever.
	if migrationFailures > 0 {
		return fmt.Errorf(
			"task: milestone migration: %d task file(s) failed to migrate — sentinel NOT written, "+
				"milestones dir NOT removed, will retry on next boot",
			migrationFailures,
		)
	}
	if err := writeMigrationSentinel(sentinelPath); err != nil {
		return err
	}
	if err := os.RemoveAll(milestonesDir); err != nil {
		slog.Warn("task: milestone migration: could not remove milestones dir after migration",
			"dir", milestonesDir, "error", err)
	}
	slog.Info("task: milestone migration: completed", "milestones_migrated", len(mapping))
	return nil
}

// writeMigrationSentinel atomically writes the completion marker.
func writeMigrationSentinel(path string) error {
	data := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("task: milestone migration: write sentinel: %w", err)
	}
	return nil
}

// assignMilestoneTag computes the final "milestone:<name>[-N]" tag for one
// milestone (FR-027/028), normalizing (lowercase+trim) then truncating with
// headroom reserved for the prefix and a worst-case "-999" suffix so the
// final tag never exceeds maxTagRunes. Collisions are disambiguated with
// "-2", "-3", … keyed by ascending milestone-ID processing order, with
// uniqueness RE-CHECKED after each suffix attempt (ADR D1 r3) so a triple
// collision resolves to <tag>, <tag>-2, <tag>-3 rather than colliding again
// at -2. assigned is the tag->milestoneID ledger built so far by the caller.
func assignMilestoneTag(name, milestoneID string, assigned map[string]string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	maxBaseRunes := maxTagRunes - len(milestoneTagPrefix) - maxMilestoneSuffixHeadroom
	if baseRunes := []rune(base); len(baseRunes) > maxBaseRunes {
		base = string(baseRunes[:maxBaseRunes])
	}
	tag := milestoneTagPrefix + base
	for n := 1; ; {
		owner, exists := assigned[tag]
		if !exists || owner == milestoneID {
			return tag
		}
		n++
		tag = fmt.Sprintf("%s%s-%d", milestoneTagPrefix, base, n)
	}
}

// migrateOneTaskFile migrates a single task file in place: if it carries a
// legacy milestone_id (read from raw JSON) that maps to a known migrated
// milestone, it appends that milestone's tag (dedup-guarded, idempotent),
// copies due_date onto Due only when Due is empty (SD-A12), and drops the
// milestone_id key. A task with no milestone_id key is left untouched
// (returns nil without writing). A milestone_id referencing an unknown/
// already-cleaned milestone has the stale key dropped.
func migrateOneTaskFile(path string, mapping map[string]milestoneTagAssignment, emptyMilestones map[string]bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	midRaw, ok := raw["milestone_id"]
	if !ok {
		return nil // never had a milestone_id — nothing to do, no rewrite.
	}
	var mid string
	if err := json.Unmarshal(midRaw, &mid); err != nil || mid == "" {
		delete(raw, "milestone_id")
		return rewriteRawTaskFile(path, raw)
	}
	entry, known := mapping[mid]
	if !known {
		// References a milestone that no longer exists (dangling) — drop the
		// stale key only.
		delete(raw, "milestone_id")
		return rewriteRawTaskFile(path, raw)
	}
	// Capture non-empty BEFORE any further processing (m2 fix, spec §
	// "capture mid BEFORE any clear") — this milestone has a member task.
	delete(emptyMilestones, mid)

	var tags []string
	if tagsRaw, ok := raw["tags"]; ok {
		if err := json.Unmarshal(tagsRaw, &tags); err != nil {
			return fmt.Errorf("parse existing tags: %w", err)
		}
	}
	hasTag := false
	for _, t := range tags {
		if t == entry.Tag {
			hasTag = true
			break
		}
	}
	if !hasTag {
		tags = append(tags, entry.Tag) // idempotent: only appended once ever.
	}
	// m5 fix (review r1): route the appended list through the SAME
	// normalizeTags the store's own Create/Update write path uses, instead of
	// writing the raw slice directly — this was bypassing the
	// ≤16-tags-per-task/≤64-rune-per-tag caps every other write path
	// enforces, letting the migration silently produce a task that violates
	// an invariant nothing else in the codebase is allowed to violate. A cap
	// violation now surfaces as a per-file migration error (logged Warn by
	// the caller, and — since this is a genuine ADR-049 D1 spec/data
	// conflict on a real task, not a transient I/O fault — correctly blocks
	// this run's sentinel finalization until an operator resolves it).
	normalizedTags, nErr := normalizeTags(tags)
	if nErr != nil {
		return fmt.Errorf("normalize tags after milestone-tag append: %w", nErr)
	}
	tagsJSON, mErr := json.Marshal(normalizedTags)
	if mErr != nil {
		return fmt.Errorf("marshal tags: %w", mErr)
	}
	raw["tags"] = tagsJSON

	// due: copy only when currently empty (SD-A12: YYYY-MM-DD -> RFC3339 UTC midnight).
	dueEmpty := true
	if dueRaw, ok := raw["due"]; ok {
		var due string
		if err := json.Unmarshal(dueRaw, &due); err == nil && due != "" {
			dueEmpty = false
		}
	}
	if dueEmpty && entry.DueDate != "" {
		dueJSON, mErr := json.Marshal(entry.DueDate + "T00:00:00Z")
		if mErr != nil {
			return fmt.Errorf("marshal due: %w", mErr)
		}
		raw["due"] = dueJSON
	}

	delete(raw, "milestone_id")
	return rewriteRawTaskFile(path, raw)
}

// rewriteRawTaskFile atomically writes back a raw (map[string]json.RawMessage)
// task document under the same striped-lock/flock discipline as the store's
// own write path.
func rewriteRawTaskFile(path string, raw map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}
