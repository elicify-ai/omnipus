// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// provenanceOrigins is the stored form of a provenance on durable records that
// cannot import pkg/bus (lifecycle record, request capture): its origin
// strings, nil when unresolved.
func provenanceOrigins(p bus.Provenance) []string {
	origins := p.Origins()
	if len(origins) == 0 {
		return nil
	}
	out := make([]string, len(origins))
	for i, o := range origins {
		out[i] = string(o)
	}
	return out
}

// storedProvenance reads a stored origin list back. Empty or malformed is
// unresolved (I-2: missing lineage never widens anything).
func storedProvenance(origins []string) bus.Provenance {
	if len(origins) == 0 {
		return bus.UnresolvedProvenance()
	}
	conv := make([]bus.Origin, len(origins))
	for i, o := range origins {
		conv[i] = bus.Origin(o)
	}
	return bus.NewProvenance(conv...)
}

// sessionLineageProvenance returns the stored provenance a turn of sessionID
// must join (I-2 "a turn's provenance"): lineage is true for a lineage
// session, whose stored provenance is joined into every turn; false for a
// root, which stores none. The mode comes from the record (written once at
// creation) or, for a record written before this feature, from the FR-050
// classification. Every read failure is lineage with unresolved provenance
// (fail closed). A store-less direct turn has nothing to classify and joins
// nothing: its consumed inputs alone decide.
func (al *AgentLoop) sessionLineageProvenance(sessionID string, declared session.OriginKind) (bus.Provenance, bool) {
	// A caller built without a loop (a PlanEngine with no AgentLoop) has no
	// store to read: nothing to join, and the result stays unresolved.
	if al == nil {
		return bus.UnresolvedProvenance(), false
	}
	sessions := al.GetSessionStore()
	lifecycle := al.GetSessionLifecycleStore()
	if sessionID == "" || sessions == nil || lifecycle == nil {
		return bus.UnresolvedProvenance(), false
	}
	unresolved := func(reason string, err error) (bus.Provenance, bool) {
		fields := map[string]any{"session_id": sessionID, "reason": reason}
		if err != nil {
			fields["error"] = err.Error()
		}
		logger.WarnCF("agent", "program provenance unresolved for this session; connected programs are withheld", fields)
		return bus.UnresolvedProvenance(), true
	}
	meta, err := sessions.GetMeta(sessionID)
	if err != nil || meta == nil {
		return unresolved("session metadata unreadable", err)
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil && !errors.Is(err, session.ErrLifecycleNotFound) {
		return unresolved("lifecycle record unreadable", err)
	}
	mode := session.ProvenanceMode("")
	if rec != nil {
		mode = rec.ProvenanceMode
	}
	if mode == "" {
		ev := session.ProvenanceModeEvidence{
			ParentSessionID: meta.ParentSessionID,
			TaskID:          meta.TaskID,
		}
		switch {
		case rec != nil && rec.Origin != nil:
			ev.Origin = rec.Origin.Kind
		case rec == nil && declared != "":
			// No record yet (a first turn before its root record is
			// published): the turn's declared origin kind is the evidence.
			ev.Origin = declared
		}
		if rec == nil {
			ev.PlanOwned = al.planEngineOwnsSession(sessionID)
		}
		mode = session.ClassifyProvenanceMode(ev)
	}
	if mode == session.ProvenanceModeRoot {
		return bus.UnresolvedProvenance(), false
	}
	if rec == nil {
		// FR-050 row 4: a session the plan engine owns (owner or supervision
		// session, minted without a lifecycle record) carries its plan's
		// stored provenance, read fresh so a later join is seen.
		if stored, owned := al.planSessionProvenance(sessionID); owned {
			if stored.IsUnresolved() {
				return unresolved("owning plan has no stored provenance", nil)
			}
			return stored, true
		}
		return unresolved("lineage session has no lifecycle record", nil)
	}
	stored := storedProvenance(rec.Provenance)
	if stored.IsUnresolved() {
		return unresolved("lineage session has no stored provenance", nil)
	}
	if reason, aerr := al.lineageAncestryUnresolved(rec); reason != "" {
		return unresolved(reason, aerr)
	}
	return stored, true
}

// maxProvenanceLineageHops is FR-041's ancestry bound: a session more than
// this many steering hops below its root has unresolved provenance.
const maxProvenanceLineageHops = 16

// lineageAncestryUnresolved walks a lineage session's steering ancestry
// (FR-041, S-79). The ancestry must be readable all the way to its root and
// at most maxProvenanceLineageHops long; otherwise it returns why (and the
// read error, if any), and the session's provenance is unresolved (fail
// closed). An ancestor with no lifecycle record is accepted only as the root:
// its session metadata must be readable and name no parent.
func (al *AgentLoop) lineageAncestryUnresolved(rec *session.LifecycleRecord) (string, error) {
	sessions := al.GetSessionStore()
	lifecycle := al.GetSessionLifecycleStore()
	seen := map[string]bool{rec.SessionID: true}
	cur := rec
	for hops := 0; ; hops++ {
		parentID := cur.SteeringSessionID()
		if parentID == "" {
			return "", nil
		}
		if hops+1 > maxProvenanceLineageHops {
			return "lineage is more than 16 hops deep", nil
		}
		if seen[parentID] {
			return "lineage ancestry is cyclic", nil
		}
		seen[parentID] = true
		parent, err := lifecycle.Load(parentID)
		if err == nil && parent != nil {
			cur = parent
			continue
		}
		if err != nil && !errors.Is(err, session.ErrLifecycleNotFound) {
			return "lineage ancestor's lifecycle record unreadable", err
		}
		meta, merr := sessions.GetMeta(parentID)
		if merr != nil || meta == nil {
			return "lineage ancestor unreadable", merr
		}
		if meta.ParentSessionID != "" {
			return "lineage ancestor has no lifecycle record", nil
		}
		return "", nil
	}
}

// wakeProvenance is the provenance of a server wake that adds no origin of
// its own into sessionID: the session's stored lineage for a lineage session,
// unresolved otherwise (I-2: a wake never widens anything).
func (al *AgentLoop) wakeProvenance(sessionID string) bus.Provenance {
	stored, lineage := al.sessionLineageProvenance(sessionID, "")
	if !lineage {
		return bus.UnresolvedProvenance()
	}
	return stored
}

// joinInstructionProvenance joins the calling agent turn's provenance into a
// lineage session's stored provenance when that turn changes the session's
// instructions (FR-041, I-2: "joined into a record ... as any agent change to
// its instructions"; S-110). The join only adds origins; an unresolved caller
// makes the record unresolved (fail closed). A root stores none and is left
// alone, and a call that does not come from an agent turn (no turn origin in
// ctx: a person's control, a server wake) joins nothing.
func (al *AgentLoop) joinInstructionProvenance(ctx context.Context, sessionID string) error {
	if tools.ToolTurnOrigin(ctx) == nil {
		return nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return session.ErrLifecycleNotFound
	}
	caller := tools.ToolTurnProvenance(ctx)
	err := lifecycle.Mutate(sessionID, func(rec *session.LifecycleRecord) error {
		if rec == nil {
			return session.ErrLifecycleNotFound
		}
		if rec.ProvenanceMode != session.ProvenanceModeLineage {
			return nil
		}
		rec.Provenance = provenanceOrigins(bus.Join(storedProvenance(rec.Provenance), caller))
		return nil
	})
	if err != nil {
		return fmt.Errorf("program provenance: join the instruction origin into %q: %w", sessionID, err)
	}
	return nil
}

// launchInputOrigin is the origin of a lineage session's own instruction
// input — its launch instruction, a queued or delegated steer, a revival's
// new instruction: the session's stored provenance, into which the
// instruction's writer joined its turn (FR-041, S-75, S-110). Any other
// session's such input stays unresolved.
func launchInputOrigin(rec *session.LifecycleRecord) inputOrigin {
	if rec == nil || rec.ProvenanceMode != session.ProvenanceModeLineage {
		return inputOrigin{}
	}
	return inputOrigin{provenance: storedProvenance(rec.Provenance)}
}

// sessionInstructionOrigin is launchInputOrigin for a session known by id: the
// stored lineage of a lineage session (a task run, S-77), otherwise
// unresolved. A session with no lifecycle record (a plan supervision turn, a
// board turn) has no stored instruction origin and stays unresolved.
func (al *AgentLoop) sessionInstructionOrigin(sessionID string) inputOrigin {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || sessionID == "" {
		return inputOrigin{}
	}
	rec, err := lifecycle.Load(sessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		// A plan owner or supervision turn's instruction is the plan's
		// (FR-050 row 4, handoff "plan created by a tool call").
		if stored, owned := al.planSessionProvenance(sessionID); owned {
			return inputOrigin{provenance: stored}
		}
	}
	if err != nil {
		return inputOrigin{}
	}
	return launchInputOrigin(rec)
}

// planSessionProvenance is the stored provenance of the plan that owns
// sessionID as its owner or supervision session (I-2, S-144): owned is false
// when no plan owns it. A plan written before Plan.Provenance existed has
// none and is unresolved (fail closed).
func (al *AgentLoop) planSessionProvenance(sessionID string) (bus.Provenance, bool) {
	p, owned := al.planOwningSession(sessionID)
	if !owned {
		return bus.UnresolvedProvenance(), false
	}
	return storedProvenance(p.Provenance), true
}

// goalWakeProvenance is the provenance of a goal keeper's wake into sessionID
// (I-2, FR-041, S-109): the goal's recorded origin (where its instructions
// came from), joined with the session's stored lineage for a lineage session.
// A goal with no recorded origin adds nothing (the session's lineage, or
// unresolved for a root); a goal record that cannot be read leaves a root's
// wake unresolved (fail closed).
func (al *AgentLoop) goalWakeProvenance(sessionID, goalID string) bus.Provenance {
	stored, lineage := al.sessionLineageProvenance(sessionID, "")
	var recorded []string
	if goalID != "" {
		if g, err := resolveGoalRecordStore().Get(goalID); err == nil && g != nil {
			recorded = g.Provenance
		}
	}
	switch {
	case lineage && len(recorded) == 0:
		return stored
	case lineage:
		return bus.Join(stored, storedProvenance(recorded))
	default:
		return storedProvenance(recorded)
	}
}
