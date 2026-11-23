// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// memory_retro_recall.go — how far back recall_memory reads past reflections
// (Settings redesign #1519, FR-050, founder answer FQ-1 A: "recall reaches as
// far back as memory is kept").
//
// The window follows storage.retention, the same setting the retro sweep
// deletes by:
//
//   - memory_retros_days N (default 180) → reflections up to N days old;
//   - memory_retros_forever or the legacy disabled switch → no age limit.
//
// Bounded read: whatever the window, one search reads at most the newest
// retroRecallMaxDayDirs day folders of the retros directory (one folder per
// calendar day that has reflections, so 3,650 folders is ten years of daily
// reflections). Older folders are skipped with a warning naming how many
// were skipped, never silently.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memrooms"
)

// retroRecallMaxDayDirs is the bounded read: the most day folders one
// recall search reads, newest first.
const retroRecallMaxDayDirs = 3650

// retroRecallLegacyDays is the window of a store whose window was never set
// (a MemoryStore built outside the agent wiring): the 365 days recall read
// before FR-050.
const retroRecallLegacyDays = 365

// retroRecallWindow is how far back recall reads reflections. noAgeLimit
// wins over days.
type retroRecallWindow struct {
	days       int
	noAgeLimit bool
}

// retroRecallWindowFromRetention maps the retention setting to the recall
// window: recall reaches exactly as far back as the sweep keeps reflections.
func retroRecallWindowFromRetention(ret config.OmnipusRetentionConfig) retroRecallWindow {
	if ret.KeepsMemoryForever() {
		return retroRecallWindow{noAgeLimit: true}
	}
	return retroRecallWindow{days: ret.RetentionMemoryRetrosDays()}
}

// SetRetroRecallWindow sets how far back recall reads reflections, from the
// retention setting (FR-050). Called when the agent instance is built; a
// live source set with SetRetroRecallSource takes precedence.
func (ms *MemoryStore) SetRetroRecallWindow(ret config.OmnipusRetentionConfig) {
	w := retroRecallWindowFromRetention(ret)
	ms.recallMu.Lock()
	defer ms.recallMu.Unlock()
	ms.recallWindow = &w
}

// SetRetroRecallSource makes recall read the retention setting from source
// on every search, so a retention change reaches an agent that is already
// built without a rebuild or restart (B2, CR-08). source reports false when
// it has no running config; the window set at build time is then used.
func (ms *MemoryStore) SetRetroRecallSource(source func() (config.OmnipusRetentionConfig, bool)) {
	ms.recallMu.Lock()
	defer ms.recallMu.Unlock()
	ms.recallSource = source
}

// retroRecallWindowOrLegacy returns the window from the live source, else
// the set window, else the pre-FR-050 365-day window for a store whose
// window was never set.
func (ms *MemoryStore) retroRecallWindowOrLegacy() retroRecallWindow {
	ms.recallMu.Lock()
	source := ms.recallSource
	set := ms.recallWindow
	ms.recallMu.Unlock()
	if source != nil {
		if ret, ok := source(); ok {
			return retroRecallWindowFromRetention(ret)
		}
	}
	if set == nil {
		return retroRecallWindow{days: retroRecallLegacyDays}
	}
	return *set
}

// ReadRetrosForRecall returns the private room's reflections inside the
// recall window, newest first, reading at most retroRecallMaxDayDirs day
// folders.
//
// B4 (SFH-12): a day folder or reflection file that cannot be read does not
// end the search and is not hidden either. The readable reflections are
// returned together with a *memrooms.PartialReadError naming every failed
// read, so the caller keeps the hits and warns that the results are
// incomplete. A folder or file removed between listing and reading is an
// expected omission (the memrooms.ScanMemoriesWithErrors rule), not a
// failure. A missing retros folder is no reflections, not an error.
func (ms *MemoryStore) ReadRetrosForRecall() ([]Retro, error) {
	w := ms.retroRecallWindowOrLegacy()
	retrosBase := filepath.Join(ms.privateRoom.Root, "retros")
	var cutoff time.Time // zero: no age limit
	if !w.noAgeLimit {
		days := w.days
		if days < 1 {
			days = 1
		}
		cutoff = ms.now().AddDate(0, 0, -days)
	}

	entries, err := os.ReadDir(retrosBase)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cutoffDay := time.Time{}
	if !cutoff.IsZero() {
		cutoffDay = time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, time.UTC)
	}
	type dayDir struct {
		day  time.Time
		name string
	}
	var days []dayDir
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		day, perr := time.Parse("2006-01-02", e.Name())
		if perr != nil {
			continue // not a day folder
		}
		if !cutoffDay.IsZero() && day.Before(cutoffDay) {
			continue
		}
		days = append(days, dayDir{day: day, name: e.Name()})
	}
	sort.Slice(days, func(i, j int) bool { return days[i].day.After(days[j].day) })
	if len(days) > retroRecallMaxDayDirs {
		logger.WarnCF("agent.memory", "ReadRetrosForRecall: reading only the newest day folders",
			map[string]any{"read": retroRecallMaxDayDirs, "skipped": len(days) - retroRecallMaxDayDirs})
		days = days[:retroRecallMaxDayDirs]
	}

	var retros []Retro
	var readFailures []error
	for _, d := range days {
		dir := filepath.Join(retrosBase, d.name)
		files, rerr := os.ReadDir(dir)
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				continue // removed after listing: expected omission
			}
			logger.WarnCF("agent.memory", "ReadRetrosForRecall: cannot read day dir",
				map[string]any{"dir": dir, "error": rerr.Error()})
			readFailures = append(readFailures, fmt.Errorf("reflections of %s: %w", d.name, rerr))
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), "_retro.md") {
				continue
			}
			path := filepath.Join(dir, f.Name())
			data, ferr := os.ReadFile(path)
			if ferr != nil {
				if errors.Is(ferr, os.ErrNotExist) {
					continue // removed after listing: expected omission
				}
				logger.WarnCF("agent.memory", "ReadRetrosForRecall: skipping unreadable retro file",
					map[string]any{"path": path, "error": ferr.Error()})
				readFailures = append(readFailures,
					fmt.Errorf("reflection file %s/%s: %w", d.name, f.Name(), ferr))
				continue
			}
			for _, r := range parseRetroFile(string(data)) {
				if r.Timestamp.IsZero() {
					continue
				}
				if cutoff.IsZero() || r.Timestamp.After(cutoff) {
					retros = append(retros, r)
				}
			}
		}
	}
	sort.SliceStable(retros, func(i, j int) bool {
		return retros[i].Timestamp.After(retros[j].Timestamp)
	})
	if len(readFailures) > 0 {
		return retros, &memrooms.PartialReadError{Err: errors.Join(readFailures...)}
	}
	return retros, nil
}
