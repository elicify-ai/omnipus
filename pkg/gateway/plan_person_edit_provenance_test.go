package gateway

// Round 13, CHECK finding F1 (/workspace/logs/1960/check-s1-delta3/check-report.md),
// the preserve half: a PERSON's edit of a plan through the REST handler must
// leave the plan's provenance exactly as it was. Oracle: spec I-2 ("A person's
// UI edit preserves the record's provenance unchanged (no join)") and
// ruling-check-delta.md S-111 writers ("Nil is correct only for a person's UI
// edit"). Wrong implementation killed: a fix that joins in the shared store
// path or the REST handler (for example joining {independent}, or the request's
// origin, into every update) would turn a Telegram-origin plan into
// {connector:telegram, independent}, widening what a record claims.
import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/plan"
)

func TestPlanPut_PersonEditPreservesStoredProvenance(t *testing.T) {
	for _, row := range []struct {
		name string
		join *[]string // origins joined in before the edit; nil keeps what REST create stamped
		want func(t *testing.T, stored []string)
	}{
		{"as_created_by_the_rest_create", nil, func(t *testing.T, stored []string) {}},
		{"joined_with_telegram", &[]string{"connector:telegram"}, func(t *testing.T, stored []string) {
			require.Contains(t, stored, "connector:telegram", "instrument: the record carries the Telegram origin")
		}},
		{"unresolved_record_older_than_the_field", &[]string{}, func(t *testing.T, stored []string) {
			require.Empty(t, stored, "instrument: the record is unresolved")
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			api := newTestRestAPIWithPlans(t)
			wsID := createTestWorkspace(t, api, "Plans WS")
			w := postPlan(t, api, wsID, `{"workspace_id":"`+wsID+`","title":"Edited by a person","owner_agent_id":"`+testPlansAgentID+`"}`)
			require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
			var created gen.Plan
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
			if row.join != nil {
				_, err := api.planStore.Update(created.Id, plan.Patch{Provenance: row.join})
				require.NoError(t, err)
			}
			before, err := api.planStore.Get(created.Id)
			require.NoError(t, err)
			row.want(t, before.Provenance)

			wPut := putPlan(t, api, created.Id, `{"title":"Renamed by a person","objective":"A changed objective written by a person"}`)
			require.Equal(t, http.StatusOK, wPut.Code, "body=%s", wPut.Body.String())

			after, err := api.planStore.Get(created.Id)
			require.NoError(t, err)
			require.Equal(t, "Renamed by a person", after.Title, "instrument: the person's instruction edit was applied")
			require.Equal(t, "A changed objective written by a person", after.Objective)
			assert.Equal(t, before.Provenance, after.Provenance, "I-2: a person's UI edit preserves provenance, no join")
		})
	}
}
