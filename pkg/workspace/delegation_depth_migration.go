// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
// classify existing records. The founder accepted rewriting both, once. After
// the migration has run (marker present), a NEWLY saved 3 is a deliberate choice
// by construction and is never rewritten.
//
// # Rule
//
//   - Exactly `depth == legacyDelegationDepthDefault` (3) becomes
//     `delegationDepthDefaultRaisedTo` (10). Every other stored value — 0, 1, 2,
//     4…10 — and an inherit edge (depth absent) is left byte-untouched.
//   - Once per install, guarded by the marker file below; a no-op run writes
//     nothing and logs nothing.
//   - A record that cannot be fully trusted (unsafe id, unreadable file,
//     malformed JSON, workspace_id mismatch) or whose rewritten edges fail
//     SaveDelegation's ValidateShape is SKIPPED, never rewritten — the same
//     posture as loadDelegationStore. Under the once rule a skipped record keeps
//     its 3s permanently; records_skipped in the summary line is the visible
//     trace.
//
// # Why the marker lives inside the store directory
//
// entities/ is in fspolicy.SecretEntriesAlways: kernel-denied to sandboxed
// agents on both layers (see delegationstore.go's leading comment). A marker
// anywhere agent-writable could be deleted by a child, re-arming the migration
// to later destroy a deliberate user-chosen 3. The scan enumerates `*.json`
// files only, so the dotfile marker is naturally excluded from it.

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

	// delegationDepthMigrationMarker marks the one-time migration as done.
	// Presence alone is the signal; the content is a stamp for human inspection.
	delegationDepthMigrationMarker = ".depth-default-10-migration"
)

// DelegationDepthMigrationResult reports what the one-time migration did on
// this call. Ran is false (and the counts zero) when the marker was already
// present: the migration ran on an earlier boot and nothing is re-examined.
type DelegationDepthMigrationResult struct {
	Ran               bool
	WorkspacesScanned int
	WorkspacesChanged int
	EdgesMigrated     int
	RecordsSkipped    int
}

// depthMigrationMu serializes concurrent MigrateDelegationDepthDefaultOnce
// calls (e.g. two gateway boots racing in a test). Production calls this once,
// single-threaded, before serving traffic; the mutex is defense-in-depth so a
// double call can never interleave the marker check with the store writes.
var depthMigrationMu sync.Mutex

// MigrateDelegationDepthDefaultOnce runs the one-time migration of stored
// delegation edge depth 3 (the retired shipped default) to 10 across every
// record in the delegation store directory, exactly once per install.
//
// The gateway boot calls this BEFORE ensureDefaultWorkspace — before any REST
// surface, channel or agent loop can read or write a store — so no operator
// write can interleave with the rewrite. Best-effort at the call site: a
// returned error is logged there and boot continues; the next boot re-runs the
// migration (idempotent for stored 3s) unless the marker was written.
//
// Exactly one log line is emitted, and only when the migration actually ran
// (marker was absent): a no-op call is silent by design ("migrated ONCE … with
// one log line").
func MigrateDelegationDepthDefaultOnce(home string) (DelegationDepthMigrationResult, error) {
	depthMigrationMu.Lock()
	defer depthMigrationMu.Unlock()

	var res DelegationDepthMigrationResult

	marker := filepath.Join(DelegationStoreDir(home), delegationDepthMigrationMarker)
	if _, err := os.Stat(marker); err == nil {
		return res, nil // already migrated on an earlier boot: never re-examine
	} else if !errors.Is(err, os.ErrNotExist) {
		// Cannot prove once-ness. Rewriting stored 3s without a readable marker
		// would re-run on every boot and eventually destroy a deliberate
		// user-chosen 3 — refuse instead and let the boot hook surface it.
		return res, fmt.Errorf("workspace: delegation depth migration: stat marker %s: %w", marker, err)
	}

	entries, err := os.ReadDir(DelegationStoreDir(home))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return res, fmt.Errorf("workspace: delegation depth migration: read store dir: %w", err)
	}
	res.Ran = true
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		res.WorkspacesScanned++
		id := strings.TrimSuffix(name, ".json")
		if !safeID(id) {
			res.RecordsSkipped++
			continue
		}
		changed, migrated, mErr := migrateDelegationStoreDepth(home, id)
		if mErr != nil {
			res.RecordsSkipped++
			continue
		}
		res.WorkspacesChanged += changed
		res.EdgesMigrated += migrated
	}

	// Marker written only AFTER every store write: a crash mid-migration leaves
	// the marker absent so the next boot finishes the job. Writing it first
	// would strand never-migrated 3s behind a "done" marker forever.
	if mkErr := os.MkdirAll(DelegationStoreDir(home), 0o700); mkErr != nil {
		return res, fmt.Errorf("workspace: delegation depth migration: mkdir store dir: %w", mkErr)
	}
	stamp := fmt.Sprintf(`{"legacy_depth": %d, "raised_to": %d, "migrated_at": %q}`+"\n",
		legacyDelegationDepthDefault, delegationDepthDefaultRaisedTo,
		time.Now().UTC().Format(time.RFC3339))
	if wErr := writeFileAtomicFn(marker, []byte(stamp), 0o600); wErr != nil {
		return res, fmt.Errorf("workspace: delegation depth migration: write marker %s: %w", marker, wErr)
	}

	logger.InfoCF("workspace", "one-time delegation depth default migration: stored edge depth 3 raised to the shipped default 10", map[string]any{
		"workspaces_scanned": res.WorkspacesScanned,
		"workspaces_changed": res.WorkspacesChanged,
		"edges_migrated":     res.EdgesMigrated,
		"records_skipped":    res.RecordsSkipped,
	})
	return res, nil
}

// migrateDelegationStoreDepth rewrites one workspace's delegation record in
// place: every stored explicit depth 3 becomes 10, everything else (including
// the record's other fields) is passed through byte-faithfully. Returns
// (workspacesChanged 0|1, edgesMigrated, error); a record with no 3s is not
// written at all (0, 0, nil). Holds LockID(id) across the read-modify-write —
// SaveDelegation's stated caller contract — and never partially applies a
// rewrite: SaveDelegation aborts the whole write when any edge fails
// ValidateShape, so the stored authorization cannot end up half-migrated.
func migrateDelegationStoreDepth(home, id string) (int, int, error) {
	unlock := LockID(id)
	defer unlock()

	path, err := DelegationStorePath(home, id)
	if err != nil {
		return 0, 0, err
	}
	data, err := os.ReadFile(path) // gosec rationale (out of gosec scope; kept as documentation): path is built from a safeID-checked id under the store dir
	if err != nil {
		return 0, 0, err
	}
	var rec delegationStoreRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return 0, 0, err
	}
	if rec.WorkspaceID != "" && rec.WorkspaceID != id {
		return 0, 0, fmt.Errorf("workspace: delegation depth migration: record's workspace_id %q disagrees with filename %q", rec.WorkspaceID, id)
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
		return 0, 0, nil
	}
	if err := SaveDelegation(home, id, rec.Delegation); err != nil {
		return 0, 0, err
	}
	return 1, migrated, nil
}
