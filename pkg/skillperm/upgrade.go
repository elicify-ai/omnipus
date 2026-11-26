// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package skillperm runs the one-time Skills & Tools permission upgrade
// (spec C-19, with C-3, C-6/D7 and C-18 folded into its config step): the old
// blanket Skill tool values become per-skill values, legacy trust words
// become allow / ask / deny, the old manifest switches become stored levels,
// and uncovered MCP services get mcp_<S>_* = ask.
//
// No single write covers config.json and the per-agent records, so the
// upgrade follows the write-ahead pattern of
// pkg/workspace/delegation_depth_migration.go: an exclusive lock, a snapshot
// written once and never rewritten (the only source of old values), a ledger
// naming pending and in-flight agents, then a done marker. Every step is
// replay-safe: a value a person changed in between is left as it is.
package skillperm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// MigrationName is the sandbox.migrations key of this upgrade.
const MigrationName = "skill_permissions"

const (
	lockName         = ".skill-permissions-upgrade.lock"
	snapshotName     = ".skill-permissions-upgrade-snapshot.json"
	snapshotDoneName = ".skill-permissions-upgrade-snapshot.done.json"
	ledgerName       = ".skill-permissions-upgrade-ledger.json"
	logComponent     = "skillperm"
)

// Stop points named to Options.Boundary, in order.
const (
	PointLockBeforeSnapshot  = "lock-before-snapshot"
	PointSnapshot            = "snapshot"
	PointLedger              = "ledger"
	PointConfigWritten       = "config-written"
	PointInFlightBeforeWrite = "in-flight-before-write"
	PointRecordWritten       = "record-written"
	PointBeforeDone          = "before-done"
	PointMarker              = "marker"
)

// Options carries the observation hooks of the upgrade. Both are optional;
// production startup passes none.
type Options struct {
	// Boundary is called after each durable step (agent is set for the two
	// per-agent points).
	Boundary func(point, agent string)
	// BeforeAgentWrite is called before an agent's record is written; an
	// error leaves that agent pending, as a failed write does.
	BeforeAgentWrite func(agent string) error
}

// Snapshot is the durable record of every old value the plan needs.
type Snapshot struct {
	OldGlobalSkill   string                              `json:"old_global_skill"`
	OldTrust         string                              `json:"old_trust"`
	Compressed       bool                                `json:"compressed"`
	PreviewAllLazy   bool                                `json:"preview_all_lazy"`
	DiscoveryEnabled bool                                `json:"discovery_enabled"`
	Servers          []config.VisibilityConversionServer `json:"servers"`
	GlobalMCP        map[string]string                   `json:"global_mcp"`
	GodMode          bool                                `json:"god_mode"`
	Agents           []config.SkillUpgradeAgent          `json:"agents"`
	SkippedAgents    []string                            `json:"skipped_agents,omitempty"`
	InstalledSkills  []string                            `json:"installed_skills"`
	ProjectSkills    []string                            `json:"project_skills"`
	StaticTools      []string                            `json:"static_tools"`
}

// Ledger is the upgrade's progress record. Failures names each pending
// agent whose record write failed (reason ReasonWriteFailed); a successful
// write removes its entry. Skipped lists the agent records the snapshot
// skipped as unreadable; it stays after done (ruling spec-gaps-1).
type Ledger struct {
	State    string            `json:"state"`
	Pending  []string          `json:"pending"`
	InFlight *string           `json:"in_flight"`
	Failures map[string]string `json:"failures"`
	Skipped  []string          `json:"skipped"`
}

// The reasons an agent is listed as not upgraded (C-19 "The notice").
const (
	ReasonWriteFailed = "write_failed"
	ReasonUnreadable  = "unreadable"
)

const (
	stateInProgress = "in_progress"
	stateDone       = "done"
)

// Run performs (or completes) the upgrade for home. It must run before the
// agent loop starts any turn. It returns an error when an agent record could
// not be written; that agent stays pending and the next start retries it.
func Run(home string, opts Options) error {
	if _, err := os.Stat(filepath.Join(home, "config.json")); errors.Is(err, os.ErrNotExist) {
		// A fresh install has no old values to translate; its config is
		// written with the new shape from the start.
		return nil
	}
	entities := filepath.Join(home, "entities")
	if done, err := finishIfDone(home, entities); err != nil || done {
		return err
	}
	if err := os.MkdirAll(entities, 0o700); err != nil {
		return fmt.Errorf("skillperm: create entities dir: %w", err)
	}
	return fileutil.WithFlock(filepath.Join(entities, lockName), func() error {
		if done, err := finishIfDone(home, entities); err != nil || done {
			return err
		}
		return runLocked(home, entities, opts)
	})
}

func (o Options) at(point, agent string) {
	if o.Boundary != nil {
		o.Boundary(point, agent)
	}
}

func runLocked(home, entities string, opts Options) error {
	opts.at(PointLockBeforeSnapshot, "")
	snap, err := loadOrTakeSnapshot(home, entities)
	if err != nil {
		return err
	}
	opts.at(PointSnapshot, "")
	led, ok, err := readLedger(entities)
	if err != nil {
		return err
	}
	if !ok {
		led = Ledger{State: stateInProgress, Pending: agentIDs(snap), Failures: map[string]string{}, Skipped: skippedIDs(snap)}
		if err := writeJSON(filepath.Join(entities, ledgerName), led); err != nil {
			return err
		}
	}
	if led.Failures == nil {
		led.Failures = map[string]string{} // a ledger from before ruling spec-gaps-1
	}
	if led.Skipped == nil {
		led.Skipped = skippedIDs(snap)
	}
	opts.at(PointLedger, "")
	if err := applyConfigPlan(home, snap); err != nil {
		return err
	}
	opts.at(PointConfigWritten, "")
	if err := applyAgentPlans(home, entities, snap, &led, opts); err != nil {
		return err
	}
	opts.at(PointBeforeDone, "")
	led = Ledger{State: stateDone, Pending: []string{}, Failures: map[string]string{}, Skipped: led.Skipped}
	if err := writeJSON(filepath.Join(entities, ledgerName), led); err != nil {
		return err
	}
	if err := writeMarker(home); err != nil {
		return err
	}
	opts.at(PointMarker, "")
	return retireSnapshot(entities)
}

// finishIfDone completes a done upgrade's tail (a missing config marker is
// rewritten from the ledger, a snapshot left by a stop is renamed) and
// reports whether the upgrade is done. A done home is never rewritten.
func finishIfDone(home, entities string) (bool, error) {
	led, ok, err := readLedger(entities)
	if err != nil || !ok || led.State != stateDone {
		return false, err
	}
	raw, err := readRawConfig(home)
	if err != nil {
		return true, err
	}
	if childMap(childMap(raw, "sandbox"), "migrations")[MigrationName] != stateDone {
		if err := writeMarker(home); err != nil {
			return true, err
		}
	}
	return true, retireSnapshot(entities)
}

func retireSnapshot(entities string) error {
	src := filepath.Join(entities, snapshotName)
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.Rename(src, filepath.Join(entities, snapshotDoneName)); err != nil {
		return fmt.Errorf("skillperm: retire snapshot: %w", err)
	}
	return nil
}

// skippedIDs is the sorted ids of the agent records the snapshot skipped.
func skippedIDs(snap Snapshot) []string {
	ids := slices.Clone(snap.SkippedAgents)
	if ids == nil {
		ids = []string{}
	}
	sort.Strings(ids)
	return ids
}

func agentIDs(snap Snapshot) []string {
	ids := make([]string, 0, len(snap.Agents))
	for _, a := range snap.Agents {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	return ids
}

func readLedger(entities string) (Ledger, bool, error) {
	var led Ledger
	raw, err := os.ReadFile(filepath.Join(entities, ledgerName))
	if errors.Is(err, os.ErrNotExist) {
		return led, false, nil
	}
	if err != nil {
		return led, false, fmt.Errorf("skillperm: read ledger: %w", err)
	}
	if err := json.Unmarshal(raw, &led); err != nil {
		return led, false, fmt.Errorf("skillperm: parse ledger: %w", err)
	}
	return led, true, nil
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("skillperm: encode %s: %w", filepath.Base(path), err)
	}
	if err := fileutil.WriteFileAtomic(path, raw, 0o600); err != nil {
		return fmt.Errorf("skillperm: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// applyAgentPlans runs step 4 for every pending agent: the ledger names it
// in flight, its record gains the planned overrides and loses its old Skill
// entry, then the ledger retires it. A failed agent stays pending and is
// reported; the others carry on.
func applyAgentPlans(home, entities string, snap Snapshot, led *Ledger, opts Options) error {
	byID := map[string]config.SkillUpgradeAgent{}
	for _, a := range snap.Agents {
		byID[a.ID] = a
	}
	var failed []error
	for _, id := range slices.Clone(led.Pending) {
		inFlight := id
		led.InFlight = &inFlight
		if err := writeJSON(filepath.Join(entities, ledgerName), led); err != nil {
			return err
		}
		opts.at(PointInFlightBeforeWrite, id)
		if err := writeAgentPlan(home, byID[id], snap, opts); err != nil {
			logger.ErrorCF(logComponent, "skill permissions upgrade: agent record not written; it stays pending and its old skill limits still apply",
				map[string]any{"agent": id, "error": err.Error()})
			failed = append(failed, fmt.Errorf("agent %s: %w", id, err))
			led.Failures[id] = ReasonWriteFailed
			led.InFlight = nil
			continue
		}
		opts.at(PointRecordWritten, id)
		led.Pending = slices.DeleteFunc(led.Pending, func(p string) bool { return p == id })
		delete(led.Failures, id)
		led.InFlight = nil
		if err := writeJSON(filepath.Join(entities, ledgerName), led); err != nil {
			return err
		}
	}
	if len(failed) > 0 {
		if err := writeJSON(filepath.Join(entities, ledgerName), led); err != nil {
			failed = append(failed, err)
		}
		return fmt.Errorf("skillperm: upgrade not finished: %w", errors.Join(failed...))
	}
	return nil
}
