package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/plan"
)

// I-2 / S-111 / S-144 (FR-050 classification row 4): the owner and supervision
// sessions the plan engine mints have no lifecycle record; a turn in them
// carries the OWNING PLAN's stored provenance, both as the session's lineage
// and as the turn's instruction origin. A plan with no stored provenance (one
// written before the field existed) stays unresolved (fail closed), and a
// later join into the plan is seen on the next turn.
func TestPlanSessionProvenance_OwnerAndSupervisionCarryThePlans(t *testing.T) {
	telegram := bus.Origin("connector:telegram")
	for _, row := range []struct {
		name       string
		provenance []string
		want       bus.Provenance
	}{
		{"telegram_created_plan_runs_as_connector_telegram", []string{string(telegram)}, bus.NewProvenance(telegram)},
		{"person_created_plan_runs_as_independent", []string{string(bus.OriginIndependent)}, bus.NewProvenance(bus.OriginIndependent)},
		{"legacy_plan_without_provenance_is_unresolved", nil, bus.UnresolvedProvenance()},
	} {
		t.Run(row.name, func(t *testing.T) {
			h := newPlanWakeHarness(t)
			withLifecycleStore(t, h)
			p := runningPlanWithDoD("p-"+row.name, "telegram", "chat-1")
			p.Provenance = row.provenance
			require.NoError(t, h.plans.Create(p))
			ownerID, err := h.pe.mintPlanSession(testPlanOwnerAgentID, "Plan: owner")
			require.NoError(t, err)
			supID, err := h.pe.mintPlanSession(testPlanOwnerAgentID, "Plan supervision")
			require.NoError(t, err)
			_, err = h.plans.Update(p.ID, plan.Patch{OwnerSessionID: &ownerID, SupervisionSessionID: &supID})
			require.NoError(t, err)
			_, loadErr := h.al.GetSessionLifecycleStore().Load(ownerID)
			require.Error(t, loadErr, "instrument: the minted owner session has no lifecycle record")

			for _, sid := range []string{ownerID, supID} {
				stored, lineage := h.al.sessionLineageProvenance(sid, "")
				require.True(t, lineage, "a plan-owned session is lineage (FR-050 row 4)")
				require.Equal(t, row.want, stored, "session lineage is the owning plan's provenance")
				require.Equal(t, row.want, h.al.sessionInstructionOrigin(sid).provenance,
					"the plan turn's instruction origin is the owning plan's provenance")
			}

			// A session no plan owns keeps the old answer: no record, unresolved.
			other, err := h.pe.mintPlanSession(testPlanOwnerAgentID, "not a plan session")
			require.NoError(t, err)
			stored, _ := h.al.sessionLineageProvenance(other, "")
			require.True(t, stored.IsUnresolved(), "an unowned record-less session stays unresolved")
		})
	}

	t.Run("a_later_agent_join_into_the_plan_is_seen", func(t *testing.T) {
		h := newPlanWakeHarness(t)
		withLifecycleStore(t, h)
		p := runningPlanWithDoD("p-join", "telegram", "chat-1")
		p.Provenance = []string{string(telegram)}
		require.NoError(t, h.plans.Create(p))
		ownerID, err := h.pe.mintPlanSession(testPlanOwnerAgentID, "Plan: owner")
		require.NoError(t, err)
		web := []string{string(bus.OriginWeb)}
		_, err = h.plans.Update(p.ID, plan.Patch{OwnerSessionID: &ownerID, Provenance: &web})
		require.NoError(t, err)
		stored, lineage := h.al.sessionLineageProvenance(ownerID, "")
		require.True(t, lineage)
		require.Equal(t, bus.NewProvenance(telegram, bus.OriginWeb), stored)
	})
}
