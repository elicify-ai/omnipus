package skills

// Built-ins: synced at start (§8.2, replaces the old boot-time seeding) and retired
// (§8.4). Both run under the skills root's mutation lock and write no audit
// lines directly: they return the facts in SyncReport so the gateway replays
// them once the audit logger exists (§8.5).

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// BuiltinSyncCase names §8.2's case for one current built-in.
type BuiltinSyncCase string

const (
	BuiltinInstalled BuiltinSyncCase = "installed"
	BuiltinUnchanged BuiltinSyncCase = "unchanged"
	BuiltinUpdated   BuiltinSyncCase = "updated"
)

// BuiltinOutcome is one built-in's start-up result (skill.builtin_updated
// source facts).
type BuiltinOutcome struct {
	Slug    string
	Case    BuiltinSyncCase
	OldRev  string
	NewRev  string
	Stamped bool // a missing stamp was written (equality shortcut, SD-1)
}

// RetiredOutcome is one retired name's start-up result (skill.builtin_retired
// / skill.builtin_retired_kept source facts).
type RetiredOutcome struct {
	Slug          string
	FolderDeleted bool
	Kept          bool
	// AgentsChanged lists the agents whose grant lists changed. GrantEditor's
	// single RemoveGrant method (§8.2) reports no holders, so this stays empty.
	AgentsChanged []string
}

// SyncReport carries the start-up facts the gateway replays as audit lines.
type SyncReport struct {
	Updated   []BuiltinOutcome
	Installed []string
	Retired   []RetiredOutcome
}

// lastSyncFailed remembers, per skills root, whether the most recent
// SyncBuiltins left a current built-in unhandled — §8.4's ordering guard.
var (
	lastSyncMu     sync.Mutex
	lastSyncFailed = map[string]bool{}
)

func setSyncFailed(root string, failed bool) {
	lastSyncMu.Lock()
	defer lastSyncMu.Unlock()
	lastSyncFailed[root] = failed
}

func syncFailed(root string) bool {
	lastSyncMu.Lock()
	defer lastSyncMu.Unlock()
	return lastSyncFailed[root]
}

// SyncBuiltins makes every current built-in folder equal to its shipped
// package (§8.2): absent → installed; equal → untouched (stamps written when
// missing); different → the shipped package is staged and swapped in through
// skills/.swap/<slug>, the old folder discarded. grants is not used by the
// sync itself (retirement uses it); it is accepted for the §8.2 signature.
func SyncBuiltins(root string, grants GrantEditor) (SyncReport, error) {
	_ = grants
	root = filepath.Clean(root)
	var report SyncReport
	err := WithMutationLock(root, func() error {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fmt.Errorf("sync built-in skills: %w", err)
		}
		var errs []error
		if err := removeStaleSeeds(root); err != nil {
			errs = append(errs, err)
		}
		builtinFailed := false
		for _, name := range DefaultSkillNames() {
			out, err := syncOneBuiltin(root, name)
			if err != nil {
				builtinFailed = true
				errs = append(errs, fmt.Errorf("built-in skill %s: %w", name, err))
				continue
			}
			switch out.Case {
			case BuiltinInstalled:
				report.Installed = append(report.Installed, name)
			case BuiltinUpdated:
				report.Updated = append(report.Updated, out)
			}
		}
		setSyncFailed(root, builtinFailed)
		return errors.Join(errs...)
	})
	return report, err
}

// removeStaleSeeds removes skills/.seed-* leftovers of an interrupted seed (F4).
func removeStaleSeeds(root string) error {
	matches, err := filepath.Glob(filepath.Join(root, ".seed-*"))
	if err != nil {
		return err
	}
	var errs []error
	for _, m := range matches {
		if err := os.RemoveAll(m); err != nil {
			errs = append(errs, fmt.Errorf("remove stale %s: %w", m, err))
		}
	}
	return errors.Join(errs...)
}

func syncOneBuiltin(root, name string) (BuiltinOutcome, error) {
	out := BuiltinOutcome{Slug: name}
	shipped, err := shippedRevision(name)
	if err != nil {
		return out, err
	}
	out.NewRev = shipped
	onDisk, present, err := resolveLiveFolder(root, name)
	if err != nil {
		return out, err
	}
	live := filepath.Join(root, onDisk)

	if !present {
		staged, cleanup, stageErr := stageShipped(root, name)
		defer cleanup()
		if stageErr != nil {
			return out, stageErr
		}
		if err = writeGeneration(staged, newGeneration()); err != nil {
			return out, err
		}
		if err = os.Rename(staged, live); err != nil {
			return out, fmt.Errorf("install: %w", err)
		}
		out.Case = BuiltinInstalled
		bumpGeneration()
		return out, nil
	}

	current, err := ContentRevision(live)
	if err != nil {
		return out, err
	}
	out.OldRev = current
	if current == shipped {
		out.Case = BuiltinUnchanged
		if !isStampedBuiltin(live) {
			if err = os.WriteFile(filepath.Join(live, builtinMarkerFile), []byte{}, 0o644); err != nil {
				return out, fmt.Errorf("write built-in stamp: %w", err)
			}
			out.Stamped = true
		}
		gen, readErr := ReadGeneration(live)
		if readErr != nil {
			return out, readErr
		}
		if gen == "" {
			if err = writeGeneration(live, newGeneration()); err != nil {
				return out, err
			}
			out.Stamped = true
		}
		return out, nil
	}

	gen, err := ReadGeneration(live)
	if err != nil {
		return out, err
	}
	if gen == "" {
		gen = newGeneration()
	}
	staged, cleanup, err := stageShipped(root, name)
	defer cleanup()
	if err != nil {
		return out, err
	}
	if err := writeGeneration(staged, gen); err != nil {
		return out, err
	}
	if err := swapBuiltin(root, onDisk, staged); err != nil {
		return out, err
	}
	out.Case = BuiltinUpdated
	bumpGeneration()
	return out, nil
}

// stageShipped exports the shipped package into skills/.staging/ and returns
// the staged folder (carrying the built-in stamp) and its cleanup.
func stageShipped(root, name string) (string, func(), error) {
	stagingRoot := filepath.Join(root, ".staging")
	noop := func() {}
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", noop, err
	}
	parent, err := os.MkdirTemp(stagingRoot, name+"-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() {
		if err := os.RemoveAll(parent); err != nil {
			slog.Warn("skills: built-in staging not removed; it is removed at the next start", "dir", parent, "error", err)
		}
	}
	staged := filepath.Join(parent, name)
	export := ExportEmbeddedPackage
	if ShippedExportHook != nil {
		export = ShippedExportHook
	}
	if err := export(name, staged); err != nil {
		return "", cleanup, fmt.Errorf("export shipped package: %w", err)
	}
	if !isStampedBuiltin(staged) {
		if err := os.WriteFile(filepath.Join(staged, builtinMarkerFile), []byte{}, 0o644); err != nil {
			return "", cleanup, fmt.Errorf("write built-in stamp: %w", err)
		}
	}
	return staged, cleanup, nil
}

// swapBuiltin replaces skills/<onDisk> with staged through skills/.swap/.
// A failed publication puts the old folder back; if that fails too, it waits
// in .swap for §9.3 (live absent → put back at the next start).
func swapBuiltin(root, onDisk, staged string) error {
	swapDir := filepath.Join(root, ".swap")
	if err := os.MkdirAll(swapDir, 0o755); err != nil {
		return err
	}
	swap := filepath.Join(swapDir, onDisk)
	live := filepath.Join(root, onDisk)
	if fileExists(swap) {
		if err := os.RemoveAll(swap); err != nil {
			return fmt.Errorf("remove stale %s: %w", swap, err)
		}
	}
	if err := forwardRename(live, swap); err != nil {
		return fmt.Errorf("move old folder aside: %w", err)
	}
	if err := os.Rename(staged, live); err != nil {
		if putErr := putBackRename(swap, live); putErr != nil {
			return fmt.Errorf("publish shipped package: %w; put back also failed (%w) — the old folder is in %s and is put back at the next start", err, putErr, swap)
		}
		return fmt.Errorf("publish shipped package: %w", err)
	}
	if err := os.RemoveAll(swap); err != nil {
		slog.Warn("skills: old built-in folder not removed; it is removed at the next start", "path", swap, "error", err)
	}
	return nil
}

// RetireBuiltins processes §8.4's retired names, but only when the last
// SyncBuiltins of this root handled every current built-in (otherwise it is
// skipped and logged). A failed grant write is logged; removal is idempotent
// and is retried at the next start.
func RetireBuiltins(root string, grants GrantEditor, report *SyncReport) error {
	root = filepath.Clean(root)
	if syncFailed(root) {
		slog.Warn("skills: built-in retirement skipped this start because a current built-in was not handled")
		return nil
	}
	return WithMutationLock(root, func() error {
		var errs []error
		for _, name := range RetiredBuiltinNames() {
			onDisk, present, err := resolveLiveFolder(root, name)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			dir := filepath.Join(root, onDisk)
			switch {
			case present && isStampedBuiltin(dir):
				if err := os.RemoveAll(dir); err != nil {
					errs = append(errs, fmt.Errorf("retire %s: %w", name, err))
					continue
				}
				bumpGeneration()
				removeRetiredGrant(grants, name)
				if report != nil {
					report.Retired = append(report.Retired, RetiredOutcome{Slug: name, FolderDeleted: true})
				}
			case present:
				slog.Info("skills: retired built-in name kept; the folder was not placed by Omnipus", "skill", onDisk)
				if report != nil {
					report.Retired = append(report.Retired, RetiredOutcome{Slug: onDisk, Kept: true})
				}
			default:
				removeRetiredGrant(grants, name)
				slog.Info("skills: retired built-in name removed from agent grants", "skill", name)
			}
		}
		return errors.Join(errs...)
	})
}

func removeRetiredGrant(grants GrantEditor, name string) {
	if grants == nil {
		return
	}
	if err := grants.RemoveGrant(name); err != nil {
		slog.Warn("skills: retired built-in grant not removed; retried at the next start", "skill", name, "error", err)
	}
}
