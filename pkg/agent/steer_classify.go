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
			// No Origin, not a delegate type: a genuine pre-ADR-091 root
			// record (e.g. a chat/task session created before this ADR).
			// Meta agreement still governs.
			if metaParented {
				return steer.ClassDamagedChild, nil
			}
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

	// SteeredBy present: valid only if its own fields are non-empty AND the
	// session's metadata agrees (R02). Full ancestor-chain / cycle
	// verification is the launcher's job at write time (I-1's invariants);
	// Classify checks the local invariants a read-only classification can
	// verify without walking the store.
	valid := rec.SteeredBy.SteeringSessionID != "" &&
		rec.SteeredBy.RootSessionID != "" &&
		hasMeta &&
		meta.ParentSessionID == rec.SteeredBy.SteeringSessionID
	if valid {
		// Row 3: steered.
		return steer.ClassSteered, nil
	}
	// Row 8: SteeredBy present but invalid or meta disagrees.
	return steer.ClassInvalidEdge, nil
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
