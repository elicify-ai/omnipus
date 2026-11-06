package tools

// Oracle: r17 §7.3 PL-02–04 and D-1. These cases exercise create_plan
// and its real store, not the missing update_plan tool or a schema mirror.
import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatePlan_D1_FieldBoundaries(t *testing.T) {
	for _, bound := range []struct {
		field string
		max   int
	}{{"title", 200}, {"objective", 2000}, {"description", 2000}} {
		lengths := []int{bound.max - 1, bound.max, bound.max + 1}
		if bound.field == "title" {
			lengths = append([]int{0, 1, 2}, lengths...)
		}
		for _, n := range lengths {
			t.Run(fmt.Sprintf("%s_%d_characters", bound.field, n), func(t *testing.T) {
				ps, _ := newPlanAndTaskStores(t)
				tool := NewPlanCreateTool(ps)
				tool.SetOwnerValidator(allowOwner)
				value := strings.Repeat("p", n)
				args := map[string]any{"title": "T8 bounded plan", "owner_agent_id": "agent-a", "dod": validDoDArg(), "rationale": "verify the plan field bound", bound.field: value}
				result := tool.Execute(parityCtx("agent-a"), args)
				if n > bound.max || bound.field == "title" && n == 0 {
					require.True(t, result.IsError, "D-1 invalid %s: %s", bound.field, result.ForLLM)
					assert.Contains(t, result.ForLLM, bound.field)
					rows, err := ps.List(plan.Filter{})
					require.NoError(t, err)
					assert.Empty(t, rows, "a refused create stores no plan")
					return
				}
				require.False(t, result.IsError, "D-1 valid %s: %s", bound.field, result.ForLLM)
				var reply map[string]any
				require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &reply))
				id, ok := reply["plan_id"].(string)
				require.True(t, ok)
				fresh, err := ps.Get(id)
				require.NoError(t, err)
				switch bound.field {
				case "title":
					assert.Equal(t, value, fresh.Title)
				case "objective":
					assert.Equal(t, value, fresh.Objective)
				case "description":
					assert.Equal(t, value, fresh.Description)
				}
			})
		}
	}
}
