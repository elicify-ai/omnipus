// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-8 — steer.RecordClassifier's real implementation.
// Owner: WP-A (landing order §3: "pkg/session/lifecycle*.go ... ensureWarm
// → I-9" is WP-A's row; the classifier that reads that store is this
// file). Consumed by WP-B (AudienceResolver, on top of this) and WP-D (boot
// recovery).
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// maxChainWalk bounds chainValid's ancestor walk. Cycle detection (the
// visited set) is the real safety net — this is a defensive backstop
// against a pathologically long, non-cycling chain rather than the
// mechanism that makes the walk terminate.
const maxChainWalk = 4096

// SteerRecordClassifier implements steer.RecordClassifier (I-8): it reads
// the lifecycle store AND the session's own metadata
// (UnifiedMeta.Type, UnifiedMeta.ParentSessionID) and requires them to
// agree (R02) — a lost record, or a lost edge on a present record, must
// never turn a child into a root.
type SteerRecordClassifier struct {
	Lifecycle *session.LifecycleStore
	Sessions  *session.UnifiedStore
}

var _ steer.RecordClassifier = (*SteerRecordClassifier)(nil)

// NewSteerRecordClassifier builds a classifier over the given stores.
func NewSteerRecordClassifier(lifecycle *session.LifecycleStore, sessions *session.UnifiedStore) *SteerRecordClassifier {
	return &SteerRecordClassifier{Lifecycle: lifecycle, Sessions: sessions}
}

// Classify implements steer.RecordClassifier, resolving sessionID against
// landing order I-8's eight-row table.
func (c *SteerRecordClassifier) Classify(_ context.Context, sessionID string) (steer.Class, error) {
	rec, hasRecord, err := c.loadRecord(sessionID)
	if err != nil {
		return steer.ClassUnreadable, fmt.Errorf("steer: classify %q: lifecycle load: %w", sessionID, err)
	}

	meta, hasMeta, err := c.loadMeta(sessionID)
	if err != nil {
		// Not one of I-8's eight rows (a genuinely unreadable session
		// meta, distinct from "no meta yet") — fail closed rather than
		// guess whether the session is parented.
		return steer.ClassUnreadable, fmt.Errorf("steer: classify %q: meta read: %w", sessionID, err)
	}
	metaParented := hasMeta && (meta.ParentSessionID != "" || meta.Type == session.SessionTypeDelegate)

	if !hasRecord {
		if metaParented {
			// Row 4: no record, but meta names a parent or a delegate type.
			return steer.ClassDamagedChild, nil
		}
		// Row 1: no record; meta agrees this is a root.
		return steer.ClassOrdinaryRoot, nil
	}

	if rec.SteeredBy == nil {
		if rec.Origin == nil {
			if hasMeta && meta.Type == session.SessionTypeDelegate {
				// Row 6: a delegate-typed record with no Origin at all —
				// written before ADR-091, never resumed.
				return steer.ClassLegacyDelegate, nil
			}
			// No Origin, not a delegate type: a genuine pre-ADR-091 record
			// (e.g. a chat/task session created before this ADR).
			//
			// Lead's CP-0 review item 2: I-8 has no explicit row for this
			// shape (a non-delegate legacy record whose metadata happens
			// to carry a ParentSessionID for reasons unrelated to
			// steering). Decision: treat it as ordinary_root, NEVER
			// damaged_child — row 6's legacy_delegate leniency is
			// textually scoped to "Type == delegate", but nothing in I-8
			// says a non-delegate legacy record with a parent-shaped
			// metadata field is unrunnable; refusing it at boot would be
			// inventing a new refusal the spec never asked for, on data
			// this ADR does not claim to interpret. See
			// TestSteerRecordClassifier_LegacyNonDelegateRecordWithParent_IsOrdinaryRoot.
			//
			// Effect on existing installs: none observed today — verified
			// against every current production writer of
			// UnifiedMeta.ParentSessionID (`grep -rn 'ParentSessionID:\|
			// \.ParentSessionID = ' pkg/ --include='*.go' | grep -v
			// _test.go`): the ONLY writer is subturn.go's
			// createChildSession, which only ever creates
			// session.SessionTypeDelegate sessions. So on every existing
			// install, a record reaching this branch (non-delegate,
			// Origin nil, meta names a parent) does not occur via any
			// current write path; this decision only matters for a future
			// writer or a hand-edited meta.json, and treating it as a
			// root rather than refusing it is the least-surprising
			// default for data ADR-091 was never told to distrust.
			return steer.ClassOrdinaryRoot, nil
		}
		if metaParented {
			// Row 5: Origin present (ADR-091 code wrote this record), but
			// SteeredBy is gone while meta still names a parent — the edge
			// was lost. Never ordinary_root.
			return steer.ClassDamagedChild, nil
		}
		// Row 2: SteeredBy nil, Origin present, meta agrees this is a root.
		return steer.ClassOrdinaryRoot, nil
	}

	// SteeredBy present: valid only if its own fields are non-empty, the
	// session's metadata agrees (R02), AND the ancestor chain re-verifies
	// what I-1 checked at launch — no cycle, every ancestor resolves, and
	// the walk ends at the record's own claimed RootSessionID (lead's CP-0
	// review: row 8's "cycle, unknown ancestor, or wrong root" needs a
	// real walk, not just the local-field/meta check this classifier did
	// at CP-0).
	valid := rec.SteeredBy.SteeringSessionID != "" &&
		rec.SteeredBy.RootSessionID != "" &&
		hasMeta &&
		meta.ParentSessionID == rec.SteeredBy.SteeringSessionID &&
		c.chainValid(sessionID, rec.SteeredBy)
	if valid {
		// Row 3: steered.
		return steer.ClassSteered, nil
	}
	// Row 8: SteeredBy present but invalid (local fields, meta
	// disagreement, a cycle, an unknown ancestor, or a wrong root).
	return steer.ClassInvalidEdge, nil
}

// chainValid walks the ancestor chain starting at sb's direct steering
// session, verifying I-1's launch-time invariants still hold at read time
// (landing order I-8 row 8: "cycle, unknown ancestor, or wrong root"):
//
//   - no cycle — including a chain that loops back to sessionID itself,
//     which is why sessionID seeds the visited set;
//   - every ancestor's own lifecycle record resolves (an ancestor with no
//     record, or one Classify cannot read, is an "unknown ancestor" —
//     never treated as a root by default);
//   - the walk terminates at a genuine root (a record with SteeredBy ==
//     nil) whose id equals sb.RootSessionID exactly — a different
//     terminus is the "wrong root" case.
//
// Bounded by maxChainWalk as a backstop; the visited set is what actually
// terminates a true cycle immediately, in O(1) extra steps past the first
// repeat.
func (c *SteerRecordClassifier) chainValid(sessionID string, sb *session.SteeredBy) bool {
	visited := map[string]bool{sessionID: true}
	cur := sb.SteeringSessionID
	for i := 0; i < maxChainWalk; i++ {
		if cur == "" || visited[cur] {
			// Empty next-hop is an invalid ancestor; a repeat is a cycle.
			return false
		}
		visited[cur] = true
		rec, err := c.Lifecycle.Load(cur)
		if err != nil {
			// Not found, or a genuine read failure: either way this
			// ancestor cannot be verified, so the edge is not trusted.
			return false
		}
		if rec.SteeredBy == nil {
			// cur is the walked root — valid only if it matches the
			// record's own claim.
			return cur == sb.RootSessionID
		}
		cur = rec.SteeredBy.SteeringSessionID
	}
	// Exhausted the walk bound without reaching a root: treat as invalid
	// rather than loop forever or silently accept an unverified claim.
	return false
}

// loadRecord loads sessionID's lifecycle record, distinguishing "no record
// yet" (hasRecord=false, err=nil) from a genuine read/parse failure
// (err!=nil, I-8 row "record unreadable").
func (c *SteerRecordClassifier) loadRecord(sessionID string) (rec *session.LifecycleRecord, hasRecord bool, err error) {
	rec, loadErr := c.Lifecycle.Load(sessionID)
	if loadErr != nil {
		if errors.Is(loadErr, session.ErrLifecycleNotFound) {
			return nil, false, nil
		}
		return nil, false, loadErr
	}
	return rec, true, nil
}

// loadMeta loads sessionID's UnifiedMeta, distinguishing "no session
// metadata" (hasMeta=false, err=nil) from a genuine read failure.
func (c *SteerRecordClassifier) loadMeta(sessionID string) (meta *session.UnifiedMeta, hasMeta bool, err error) {
	meta, metaErr := c.Sessions.GetMeta(sessionID)
	if metaErr != nil {
		if errors.Is(metaErr, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, metaErr
	}
	return meta, true, nil
}
