package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// initializeOrdinaryRootRecord is the canonical missing-parent shape used by
// the launcher. Admission uses the same constructor before an ordinary first
// turn can delegate; an idle launch without an admission keeps no execution ID.
func initializeOrdinaryRootRecord(rec *session.LifecycleRecord, meta *session.UnifiedMeta) {
	rec.Generation = 1
	rec.State = session.LifecycleRunning
	rec.OwnerScopeKind = session.OwnerScopeHuman
	rec.WorkspaceID = meta.WorkspaceID
	// The immutable owner (DEL-11); never the retired active_agent_id.
	rec.AgentID = meta.AgentID
	rec.Origin = &session.Origin{Kind: session.OriginKind(meta.Type)}
	// FR-050: the mode is written once, here. A root stores no provenance;
	// a lineage-classified record created here has none to copy and is
	// unresolved (fail closed).
	rec.ProvenanceMode = session.ClassifyProvenanceMode(session.ProvenanceModeEvidence{
		Origin: rec.Origin.Kind, ParentSessionID: meta.ParentSessionID, TaskID: meta.TaskID,
	})
}

// scheduledRecordProvenance is the stored provenance a scheduled session's new
// record takes (I-2, FR-050 producer "the schedule record at creation"): the
// fired schedule's provenance, which the scheduled admission carries as the
// turn's input origin. Any other admission has none to give (nil).
func scheduledRecordProvenance(opts processOptions, revival *steer.Principal) []string {
	if revival == nil || *revival != scheduledRevivalPrincipal {
		return nil
	}
	return provenanceOrigins(opts.InputOrigin.provenance)
}

// The caller holds entryMu. The existing parent-only publication primitive
// creates a missing record under its owning lock and never rewrites an existing
// record. Metadata identifies the real root; a missing helper edge is not minted.
func (al *AgentLoop) ensureOrdinaryRootRecord(lifecycle *session.LifecycleStore, sessionID string, sessions *session.UnifiedStore) (*session.LifecycleRecord, error) {
	return al.ensureRootRecordStoring(lifecycle, sessionID, sessions, nil)
}

// ensureRootRecordStoring is ensureOrdinaryRootRecord for an admission that
// knows a new scheduled session's provenance: that session's new lineage
// record stores scheduled (the schedule's provenance,
// scheduledRecordProvenance); every other new record stores none. An existing
// record is never rewritten.
func (al *AgentLoop) ensureRootRecordStoring(lifecycle *session.LifecycleStore, sessionID string, sessions *session.UnifiedStore, scheduled []string) (*session.LifecycleRecord, error) {
	if sessions == nil {
		return nil, fmt.Errorf("ordinary admission: session store not found for %q", sessionID)
	}
	meta, err := sessions.GetMeta(sessionID)
	if err != nil {
		return nil, fmt.Errorf("ordinary admission: read session metadata: %w", err)
	}
	if meta.ParentSessionID != "" || meta.Type == session.SessionTypeDelegate {
		return nil, fmt.Errorf("ordinary admission: saved helper lifecycle is missing for %q", sessionID)
	}
	var record *session.LifecycleRecord
	err = lifecycle.PublishChildUnderParentLock(sessionID, func(rec *session.LifecycleRecord, existed bool) (childRec *session.LifecycleRecord, err error) {
		if !existed {
			initializeOrdinaryRootRecord(rec, meta)
			if rec.ProvenanceMode == session.ProvenanceModeLineage && rec.Origin.Kind == session.OriginKindScheduled {
				rec.Provenance = scheduled
			}
		}
		record = rec
		// This is a parent-only publication: the API's optional child is unset.
		return childRec, err
	})
	if err != nil {
		return nil, fmt.Errorf("ordinary admission: establish root record: %w", err)
	}
	return record, nil
}
