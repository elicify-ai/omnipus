package skills

// Start-up recovery of interrupted changes (§9.3) and the daily retention
// sweep (§9.1).

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// legacyBackupSuffix marks the pre-T9 publisher's replace backup (F17).
const legacyBackupSuffix = ".replace-backup"

// RecoverInterrupted runs §9.3 under the skills root's mutation lock:
// skills/.swap/<slug> leftovers, legacy <slug>.replace-backup folders,
// capture-blob temporaries and staging-area scratch. It never deletes a
// manifest and never deletes the only copy of a skill. Every failure is
// collected and returned; the remaining items are still processed.
func RecoverInterrupted(skillsRoot, historyRoot string) error {
	root := filepath.Clean(skillsRoot)
	return WithMutationLock(root, func() error {
		errs := make([]error, 0, 4)
		errs = append(errs, recoverSwaps(root, historyRoot)...)
		errs = append(errs, recoverLegacyBackups(root)...)
		errs = append(errs, removeCaptureTemporaries(historyRoot)...)
		errs = append(errs, removeStagingScratch(root)...)
		return errors.Join(errs...)
	})
}

// recoverSwaps applies the .swap table: live absent and the newest manifest
// not delete_skill (or no history) → rename back; anything else → remove.
func recoverSwaps(root, historyRoot string) []error {
	swapRoot := filepath.Join(root, ".swap")
	entries, err := os.ReadDir(swapRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []error{fmt.Errorf("list %s: %w", swapRoot, err)}
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		swap := filepath.Join(swapRoot, name)
		_, live, err := resolveLiveFolder(root, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		putBack := !live
		if putBack {
			newest, err := historyStore{dir: filepath.Join(historyRoot, SkillKey(name))}.newest()
			if err != nil {
				errs = append(errs, fmt.Errorf("recover %s: %w", swap, err))
				continue
			}
			putBack = newest == nil || newest.Kind != manifestDeleteSkill
		}
		if putBack {
			if err := os.Rename(swap, filepath.Join(root, name)); err != nil {
				errs = append(errs, fmt.Errorf("put back %s: %w", swap, err))
				continue
			}
			slog.Info("skills: interrupted change recovered; the previous folder was put back", "skill", name)
			continue
		}
		if err := os.RemoveAll(swap); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", swap, err))
			continue
		}
		slog.Info("skills: interrupted change leftover removed", "path", swap)
	}
	return errs
}

// recoverLegacyBackups applies the legacy table: live absent → rename back;
// live present → delete with one INFO line. No capture into history (F-28).
func recoverLegacyBackups(root string) []error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []error{fmt.Errorf("list skills folder: %w", err)}
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasSuffix(name, legacyBackupSuffix) {
			continue
		}
		slug := strings.TrimSuffix(name, legacyBackupSuffix)
		if !ValidSlug(slug) {
			continue
		}
		backup := filepath.Join(root, name)
		_, live, err := resolveLiveFolder(root, slug)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !live {
			if err := os.Rename(backup, filepath.Join(root, slug)); err != nil {
				errs = append(errs, fmt.Errorf("put back %s: %w", backup, err))
			}
			continue
		}
		if err := os.RemoveAll(backup); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", backup, err))
			continue
		}
		slog.Info("skills: removed legacy replace backup; the live skill is unchanged", "path", backup)
	}
	return errs
}

// removeCaptureTemporaries removes system/skill-history/<key>/blobs/.incoming-*.
func removeCaptureTemporaries(historyRoot string) []error {
	matches, err := filepath.Glob(filepath.Join(historyRoot, "*", "blobs", ".incoming-*"))
	if err != nil {
		return []error{err}
	}
	var errs []error
	for _, m := range matches {
		if err := os.RemoveAll(m); err != nil {
			errs = append(errs, fmt.Errorf("remove capture temporary %s: %w", m, err))
		}
	}
	return errs
}

// removeStagingScratch removes every skills/.staging/ entry: staged folders
// that were never published and built-in capture scratch.
func removeStagingScratch(root string) []error {
	staging := filepath.Join(root, ".staging")
	entries, err := os.ReadDir(staging)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []error{fmt.Errorf("list %s: %w", staging, err)}
	}
	var errs []error
	for _, e := range entries {
		p := filepath.Join(staging, e.Name())
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, fmt.Errorf("remove staging leftover %s: %w", p, err))
		}
	}
	return errs
}

// RetentionSweep applies §9.1 to every skill key under historyRoot, each
// under the skills root's mutation lock (released between skills). disabled
// keeps everything. Capture temporaries are never touched.
func RetentionSweep(historyRoot string, windowDays int, disabled bool, now time.Time) error {
	if disabled {
		return nil
	}
	if windowDays <= 0 {
		windowDays = 90
	}
	cutoff := now.UTC().AddDate(0, 0, -windowDays)
	skillsRoot := filepath.Join(filepath.Dir(filepath.Dir(filepath.Clean(historyRoot))), "skills")
	keys, err := os.ReadDir(historyRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("list skill history: %w", err)
	}
	var errs []error
	for _, k := range keys {
		if !k.IsDir() {
			continue
		}
		h := historyStore{dir: filepath.Join(historyRoot, k.Name())}
		if err := WithMutationLock(skillsRoot, func() error { return sweepKey(h, cutoff) }); err != nil {
			errs = append(errs, fmt.Errorf("retention for %s: %w", k.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// sweepKey applies rules 1–3 to one skill key's history.
func sweepKey(h historyStore, cutoff time.Time) error {
	ids, err := h.ids()
	if err != nil || len(ids) == 0 {
		return err
	}
	manifests := make([]*versionManifest, len(ids))
	for i, id := range ids {
		m, loadErr := h.load(id)
		if loadErr != nil {
			return loadErr
		}
		manifests[i] = m
	}
	newest := manifests[len(manifests)-1]

	// Rule 2: a delete_skill head keeps everything until it is itself older
	// than the window; then the whole history directory goes.
	if newest.Kind == manifestDeleteSkill {
		created, parseErr := time.Parse(manifestTimeFormat, newest.CreatedAt)
		if parseErr != nil {
			return fmt.Errorf("version %s: unreadable created_at %q: %w", newest.ID, newest.CreatedAt, parseErr)
		}
		if created.Before(cutoff) {
			return os.RemoveAll(h.dir)
		}
		return nil
	}

	// Rule 1: a non-newest manifest goes once its successor is older than the
	// window. A manifest whose successor's clock is unreadable is kept.
	keep := []*versionManifest{newest}
	for i := 0; i < len(manifests)-1; i++ {
		succ, parseErr := time.Parse(manifestTimeFormat, manifests[i+1].CreatedAt)
		if parseErr != nil || !succ.Before(cutoff) {
			keep = append(keep, manifests[i])
			continue
		}
		if err = os.Remove(filepath.Join(h.versionsDir(), manifests[i].ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	// Rule 3: remove finalized blobs no retained manifest references.
	referenced := map[string]bool{}
	for _, m := range keep {
		for _, f := range m.Files {
			referenced[f.SHA256] = true
		}
	}
	blobs, err := os.ReadDir(h.blobsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, b := range blobs {
		name := b.Name()
		if b.IsDir() || strings.HasPrefix(name, ".") || referenced[name] {
			continue
		}
		if err := os.Remove(filepath.Join(h.blobsDir(), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
