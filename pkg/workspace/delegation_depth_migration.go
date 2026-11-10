// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// # One-time delegation-depth default migration (founder decision 2026-11-08, Q8 B)
//
// The shipped delegation-depth default was raised from 3 to 10 (founder ruling
// 2026-10-27). Under the OLD default, freshly seeded self-edges materialized
// min(3, ceiling) as an EXPLICIT edge depth in the delegation store, so existing
// workspaces carry stored explicit 3s that today's fresh seeds no longer produce
// (they materialize min(10, ceiling), coreagent.FreshSelfDelegationMaxDepth).
// The founder decided (2026-11-08, Q8 B) that workspaces carrying such a stored
// 3 are migrated ONCE to the new default 10, with one log line — replacing the
// rounds 1–3 keep-3 behavior for this item only.
//
// # The 3 the migration cannot see through
//
// The store records only the number. There is no provenance field and no
// timestamp, so a stored 3 written by the old seeder and a stored 3 an operator
// chose on purpose are byte-identical — no store-format change can retroactively
// classify existing records. The founder accepted rewriting both, once. A 3
// saved AFTER the migration rewrote a record is a deliberate choice by
// construction and is never rewritten — including when the migration was
// interrupted or could not record its progress (see "Ledger" below).
//
// # Rule
//
//   - Exactly `depth == legacyDelegationDepthDefault` (3) becomes
//     `delegationDepthDefaultRaisedTo` (10). Every other stored value — 0, 1, 2,
//     4…10 — and an inherit edge (depth absent) is left byte-untouched.
//   - Once per install; a completed migration writes nothing and logs nothing.
//   - A record that cannot be trusted when the migration is planned (unsafe
//     id, unreadable file, malformed JSON, workspace_id mismatch) or whose
//     rewritten edges fail ValidateShape is SKIPPED permanently, never
//     rewritten — the same posture as loadDelegationStore. records_skipped in
//     the summary line is the visible trace.
//   - A VALID record whose rewrite did not land (write denied, read-only
//     store) is not a skip: it stays queued, the run reports an error, and the
//     next boot retries only that record.
//
// # Ledger (round 5, CHECK findings R1/R2/R4)
//
// The marker file is a write-ahead ledger: {"state":"in_progress",
// "pending":[ids],"in_flight":id} or {"state":"done"}. A record is rewritten
// only after the ledger durably names it in_flight; the next ledger write
// retires it. On resume, a leftover in_flight record that holds no 3 is done;
// one that holds a 3 is ambiguous (crash before its write, or an operator 3
// saved after it) and is left unchanged with a warning — fail closed, because
// widening a limit an operator deliberately tightened is the worse outcome.
// If the very first ledger write fails, nothing has been rewritten. The
// round-4 marker (a stamp without "state") means done. An unparseable ledger
// is an error and nothing is rewritten. The whole run holds an exclusive
// cross-process file lock beside the ledger, so two processes sharing one
// install cannot both run it (a second one waits, then sees done).
// Design: depth10-migration-design.md, round 5.
//
// # Why the ledger lives inside the store directory
//
// entities/ is in fspolicy.SecretEntriesAlways: kernel-denied to sandboxed
// agents on both layers (see delegationstore.go's leading comment). A ledger
// anywhere agent-writable could be deleted by a child, re-arming the migration
// to later destroy a deliberate user-chosen 3. The scan enumerates `*.json`
// files only, so the dotfile ledger and its lock are naturally excluded.

const (
	// legacyDelegationDepthDefault is the retired shipped default whose
	// materialized value this migration rewrites (founder decision
	// 2026-11-08, Q8 B: migrate stored 3s once).
	legacyDelegationDepthDefault = 3

	// delegationDepthDefaultRaisedTo is the shipped default since the founder
	// ruling of 2026-10-27. Kept as a literal (mirroring the established
	// per-package pins — e.g. pkg/gateway's delegationDepthCeilingFallback and
	// its TestDelegationDepthCeilingFallbackPin_IsTen) so a future default move
	// fails loudly here instead of silently rebasing the migration target.
	delegationDepthDefaultRaisedTo = 10

	// delegationDepthMigrationMarker is the migration's ledger file (see
	// "Ledger" above); its sidecar lock serializes processes.
	delegationDepthMigrationMarker = ".depth-default-10-migration"

	depthLedgerVersion    = 2
	depthLedgerInProgress = "in_progress"
	depthLedgerDone       = "done"
)

// DelegationDepthMigrationResult reports what the one-time migration did on
// this call. Ran is false (and the counts zero) when the migration had already
// completed: nothing is re-examined. Ran is true when this call planned or
// resumed the migration, whether or not it completed.
type DelegationDepthMigrationResult struct {
	Ran               bool
	WorkspacesScanned int
	WorkspacesChanged int
	EdgesMigrated     int
	RecordsSkipped    int
}

// depthMigrationLedger is the on-disk ledger. The round-4 marker stamp
// (legacy_depth/raised_to/migrated_at, no state) decodes with State "" and is
// treated as done.
type depthMigrationLedger struct {
	Version     int      `json:"version"`
	State       string   `json:"state"`
	Pending     []string `json:"pending,omitempty"`
	InFlight    string   `json:"in_flight,omitempty"`
	LegacyDepth int      `json:"legacy_depth"`
	RaisedTo    int      `json:"raised_to"`
	UpdatedAt   string   `json:"updated_at"`
}

// depthMigrationMu serializes in-process calls; the ledger's sidecar file lock
// serializes processes. Both are held for the whole run.
var depthMigrationMu sync.Mutex

// MigrateDelegationDepthDefaultOnce runs the one-time migration of stored
// delegation edge depth 3 (the retired shipped default) to 10 across every
// record in the delegation store directory, exactly once per install.
//
// The gateway boot calls this before any scheduler, plan engine, REST surface,
// channel or agent loop can read or write a store. Best-effort at the call
// site: a returned error is logged there and boot continues; the ledger makes
// the next boot resume exactly the unfinished part, never re-migrate a record
// it already rewrote.
//
// Exactly one summary log line is emitted per install, by the call that
// completes the migration; every other call is silent on success.
func MigrateDelegationDepthDefaultOnce(home string) (DelegationDepthMigrationResult, error) {
	depthMigrationMu.Lock()
	defer depthMigrationMu.Unlock()

	var res DelegationDepthMigrationResult
	dir := DelegationStoreDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, fmt.Errorf("workspace: delegation depth migration: mkdir store dir: %w", err)
	}
	marker := filepath.Join(dir, delegationDepthMigrationMarker)
	err := fileutil.WithFlock(fileutil.SidecarLockPath(marker), func() error {
		var runErr error
		res, runErr = migrateDelegationDepthLocked(home, marker)
		return runErr
	})
	if err != nil {
		return res, fmt.Errorf("workspace: delegation depth migration: %w", err)
	}
	return res, nil
}

// migrateDelegationDepthLocked runs under both locks.
func migrateDelegationDepthLocked(home, marker string) (DelegationDepthMigrationResult, error) {
	var res DelegationDepthMigrationResult
	led, found, err := readDepthMigrationLedger(marker)
	if err != nil {
		return res, err
	}
	if found && led.State != depthLedgerInProgress {
		return res, nil // completed on an earlier boot: never re-examine
	}
	res.Ran = true

	if !found {
		pending, planErr := planDelegationDepthMigration(home, &res)
		if planErr != nil {
			return res, planErr
		}
		led = depthMigrationLedger{State: depthLedgerInProgress, Pending: pending}
		if len(pending) == 0 {
			led.State = depthLedgerDone
		}
		if wErr := writeDepthMigrationLedger(marker, led); wErr != nil {
			return res, fmt.Errorf("record migration plan (no record was rewritten; the next boot plans again): %w", wErr)
		}
		if led.State == depthLedgerDone {
			logDepthMigrationSummary(res)
			return res, nil
		}
	} else if led.InFlight != "" {
		resolveInFlightDepthRecord(home, led.InFlight, &res)
	}

	queue := append([]string(nil), led.Pending...)
	sort.Strings(queue)
	var retry []string
	var failures []error
	for i, id := range queue {
		if found { // a planning run already counted this record in its scan
			res.WorkspacesScanned++
		}
		step := led
		step.Pending = append(append([]string(nil), retry...), queue[i+1:]...)
		step.InFlight = id
		if wErr := writeDepthMigrationLedger(marker, step); wErr != nil {
			failures = append(failures, fmt.Errorf("record progress before migrating %q (it keeps its stored 3 and stays pending): %w", id, wErr))
			return res, errors.Join(failures...)
		}
		outcome, migrated, mErr := migrateDelegationStoreDepth(home, id)
		switch outcome {
		case depthRecordMigrated:
			res.WorkspacesChanged++
			res.EdgesMigrated += migrated
		case depthRecordUnchanged:
		case depthRecordSkipped:
			res.RecordsSkipped++
			logger.WarnCF("workspace", "delegation depth migration: record skipped permanently, left untouched", map[string]any{
				"workspace_id": id, "error": mErr.Error(),
			})
		case depthRecordNotLanded:
			retry = append(retry, id)
			failures = append(failures, fmt.Errorf("migrate %q (unchanged, retried at next boot): %w", id, mErr))
		case depthRecordUnresolved:
			failures = append(failures, fmt.Errorf("migrate %q: write failed and its outcome cannot be confirmed; it is left in flight and will not be rewritten again: %w", id, mErr))
			return res, errors.Join(failures...)
		}
	}

	final := led
	final.Pending = retry
	final.InFlight = ""
	if len(retry) == 0 {
		final.State = depthLedgerDone
	}
	if wErr := writeDepthMigrationLedger(marker, final); wErr != nil {
		failures = append(failures, fmt.Errorf("record migration progress: %w", wErr))
		return res, errors.Join(failures...)
	}
	if len(retry) > 0 {
		return res, fmt.Errorf("%d record(s) not migrated, retried at next boot: %w", len(retry), errors.Join(failures...))
	}
	logDepthMigrationSummary(res)
	return res, nil
}

// readDepthMigrationLedger returns (ledger, found, err). An unparseable
// ledger is an error: once-ness cannot be proven, so nothing may be rewritten.
func readDepthMigrationLedger(marker string) (depthMigrationLedger, bool, error) {
	var led depthMigrationLedger
	data, err := os.ReadFile(marker) // gosec rationale (out of gosec scope; kept as documentation): fixed name under the store dir
	if errors.Is(err, os.ErrNotExist) {
		return led, false, nil
	}
	if err != nil {
		return led, false, fmt.Errorf("read ledger %s (refusing to rewrite without it): %w", marker, err)
	}
	if err := json.Unmarshal(data, &led); err != nil {
		return led, false, fmt.Errorf("parse ledger %s (refusing to rewrite without it): %w", marker, err)
	}
	return led, true, nil
}

func writeDepthMigrationLedger(marker string, led depthMigrationLedger) error {
	led.Version = depthLedgerVersion
	led.LegacyDepth = legacyDelegationDepthDefault
	led.RaisedTo = delegationDepthDefaultRaisedTo
	led.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(led, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ledger: %w", err)
	}
	if err := writeFileAtomicFn(marker, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write ledger %s: %w", marker, err)
	}
	return nil
}

// planDelegationDepthMigration scans the store and returns the ids of valid
// records holding at least one stored 3. Untrusted records are counted as
// permanently skipped (documented posture) and never queued.
func planDelegationDepthMigration(home string, res *DelegationDepthMigrationResult) ([]string, error) {
	entries, err := os.ReadDir(DelegationStoreDir(home))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read store dir: %w", err)
	}
	var pending []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		res.WorkspacesScanned++
		id := strings.TrimSuffix(name, ".json")
		rec, ok := readTrustedDelegationRecord(home, id)
		if !ok {
			res.RecordsSkipped++
			continue
		}
		if countLegacyDepthEdges(rec.Delegation) > 0 {
			pending = append(pending, id)
		}
	}
	return pending, nil
}

// readTrustedDelegationRecord reads id's record; ok=false for an unsafe id or
// an unreadable, malformed or id-mismatched record.
func readTrustedDelegationRecord(home, id string) (delegationStoreRecord, bool) {
	var rec delegationStoreRecord
	if !safeID(id) {
		return rec, false
	}
	path, err := DelegationStorePath(home, id)
	if err != nil {
		return rec, false
	}
	data, err := os.ReadFile(path) // gosec rationale (out of gosec scope; kept as documentation): safeID-checked id under the store dir
	if err != nil {
		return rec, false
	}
	if json.Unmarshal(data, &rec) != nil || (rec.WorkspaceID != "" && rec.WorkspaceID != id) {
		return rec, false
	}
	return rec, true
}

func countLegacyDepthEdges(edges []DelegationEdge) int {
	n := 0
	for _, e := range edges {
		if e.Depth != nil && *e.Depth == legacyDelegationDepthDefault {
			n++
		}
	}
	return n
}

// resolveInFlightDepthRecord settles the record a previous run named in
// flight without re-rewriting it: no stored 3 means its rewrite landed (or it
// was changed since); a stored 3 cannot be attributed and is left unchanged.
func resolveInFlightDepthRecord(home, id string, res *DelegationDepthMigrationResult) {
	res.WorkspacesScanned++
	path, err := DelegationStorePath(home, id)
	if err == nil {
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			return // record deleted since: nothing to settle
		}
	}
	rec, ok := readTrustedDelegationRecord(home, id)
	if ok && countLegacyDepthEdges(rec.Delegation) == 0 {
		return
	}
	res.RecordsSkipped++
	logger.WarnCF("workspace", "delegation depth migration: an interrupted record still holds depth 3 or cannot be read; left unchanged because it may be a deliberate 3 saved after its migration", map[string]any{
		"workspace_id": id,
	})
}

type depthRecordOutcome int

const (
	depthRecordUnchanged  depthRecordOutcome = iota // nothing to rewrite (no 3s, or deleted)
	depthRecordMigrated                             // rewrite landed
	depthRecordSkipped                              // untrusted or shape-invalid: permanent skip
	depthRecordNotLanded                            // valid record, rewrite did not land: retry
	depthRecordUnresolved                           // write failed and the outcome cannot be confirmed
)

// migrateDelegationStoreDepth rewrites one workspace's record: every stored
// explicit 3 becomes 10, everything else passes through. Holds LockID(id)
// across the read-modify-write (SaveDelegation's caller contract). A failed
// write is classified by re-reading the record: unchanged bytes mean the
// write did not land (retryable); anything else cannot be confirmed.
func migrateDelegationStoreDepth(home, id string) (depthRecordOutcome, int, error) {
	unlock := LockID(id)
	defer unlock()

	path, err := DelegationStorePath(home, id)
	if err != nil {
		return depthRecordSkipped, 0, err
	}
	data, err := os.ReadFile(path) // gosec rationale (out of gosec scope; kept as documentation): path is built from a safeID-checked id under the store dir
	if errors.Is(err, os.ErrNotExist) {
		return depthRecordUnchanged, 0, nil
	}
	if err != nil {
		return depthRecordNotLanded, 0, err
	}
	var rec delegationStoreRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return depthRecordSkipped, 0, err
	}
	if rec.WorkspaceID != "" && rec.WorkspaceID != id {
		return depthRecordSkipped, 0, fmt.Errorf("record's workspace_id %q disagrees with filename %q", rec.WorkspaceID, id)
	}

	migrated := 0
	for i := range rec.Delegation {
		if rec.Delegation[i].Depth == nil || *rec.Delegation[i].Depth != legacyDelegationDepthDefault {
			continue
		}
		ten := delegationDepthDefaultRaisedTo
		rec.Delegation[i].Depth = &ten
		migrated++
	}
	if migrated == 0 {
		return depthRecordUnchanged, 0, nil
	}
	for _, e := range rec.Delegation {
		if verr := e.ValidateShape(); verr != nil {
			return depthRecordSkipped, 0, verr
		}
	}
	if err := SaveDelegation(home, id, rec.Delegation); err != nil {
		after, rErr := os.ReadFile(path) // gosec rationale (out of gosec scope; kept as documentation): same path as above
		if rErr == nil && bytes.Equal(after, data) {
			return depthRecordNotLanded, 0, err
		}
		if rErr != nil {
			err = errors.Join(err, fmt.Errorf("re-read after failed write: %w", rErr))
		}
		return depthRecordUnresolved, 0, err
	}
	return depthRecordMigrated, migrated, nil
}

func logDepthMigrationSummary(res DelegationDepthMigrationResult) {
	logger.InfoCF("workspace", "one-time delegation depth default migration: stored edge depth 3 raised to the shipped default 10", map[string]any{
		"workspaces_scanned": res.WorkspacesScanned,
		"workspaces_changed": res.WorkspacesChanged,
		"edges_migrated":     res.EdgesMigrated,
		"records_skipped":    res.RecordsSkipped,
	})
}
