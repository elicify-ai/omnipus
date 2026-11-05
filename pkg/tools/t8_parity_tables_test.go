package tools_test

// Oracle: tool-parity r17 §7.1 and scenarios 13.1-13.6. This binds the
// recursive instrument of t8_parity_guard_test.go to the real rule tables
// (task.MappingRows, plan.MappingRows, agentmutation.MappingRows and the
// ExcludedRows of each package), the real generated REST request types and the
// real tool schemas. The tables are the production claim; the leaves come from
// the generated types and the tool Parameters(), never from the tables.
import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/plan"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// t8Mapping and t8Exclusion unify the three packages' row types.
type t8Mapping struct{ rest, tool, toolPath, ops string }
type t8Exclusion struct{ side, path, ops, reason string }

type t8Family struct {
	name       string
	mappings   []t8Mapping
	exclusions []t8Exclusion
	surfaces   []t8Surface
}

// t8Surface is one write operation: the REST request types it is sent as and
// the tool that does the same write.
type t8Surface struct {
	op, toolName string
	rest         []reflect.Type
	tool         tools.Tool
}

func t8Families() []t8Family {
	var task8 t8Family
	task8.name = "task"
	for _, m := range task.MappingRows() {
		task8.mappings = append(task8.mappings, t8Mapping{m.RESTPath, m.Tool, m.ToolPath, m.Ops})
	}
	for _, e := range task.ExcludedRows() {
		task8.exclusions = append(task8.exclusions, t8Exclusion{e.Side, e.Path, e.Ops, e.Reason})
	}
	task8.surfaces = []t8Surface{
		{"create", "create_task", []reflect.Type{reflect.TypeFor[generated.TaskCreateRequest]()}, tools.NewTaskCreateTool(nil)},
		{"update", "update_task", []reflect.Type{reflect.TypeFor[generated.TaskUpdateRequest]()}, tools.NewTaskUpdateTool(nil)},
	}
	var plan8 t8Family
	plan8.name = "plan"
	for _, m := range plan.MappingRows() {
		plan8.mappings = append(plan8.mappings, t8Mapping{m.RESTPath, m.Tool, m.ToolPath, m.Ops})
	}
	for _, e := range plan.ExcludedRows() {
		plan8.exclusions = append(plan8.exclusions, t8Exclusion{e.Side, e.Path, e.Ops, e.Reason})
	}
	plan8.surfaces = []t8Surface{
		{"create", "create_plan", []reflect.Type{reflect.TypeFor[generated.PlanCreateRequest]()}, tools.NewPlanCreateTool(nil)},
		{"update", "update_plan", []reflect.Type{reflect.TypeFor[generated.PlanUpdateRequest]()}, tools.NewPlanUpdateTool(nil)},
	}
	var agent8 t8Family
	agent8.name = "agent"
	for _, m := range agentmutation.MappingRows() {
		agent8.mappings = append(agent8.mappings, t8Mapping{m.RESTPath, m.Tool, m.ToolPath, m.Ops})
	}
	for _, e := range agentmutation.ExcludedRows() {
		agent8.exclusions = append(agent8.exclusions, t8Exclusion{e.Side, e.Path, e.Ops, e.Reason})
	}
	deps := &systools.Deps{}
	agent8.surfaces = []t8Surface{
		{"create", "create_agent", []reflect.Type{reflect.TypeFor[generated.AgentCreateRequestMain](), reflect.TypeFor[generated.AgentCreateRequestSubagent](), reflect.TypeFor[generated.AgentCreateRequestSubagent3p]()}, systools.NewAgentCreateTool(deps)},
		{"update", "update_agent", []reflect.Type{reflect.TypeFor[generated.AgentUpdateRequest]()}, systools.NewAgentUpdateTool(deps)},
	}
	return []t8Family{task8, plan8, agent8}
}

func t8OpApplies(rowOps, op string) bool { return rowOps == op || rowOps == "both" }

func (s t8Surface) restLeaves() []string {
	seen := map[string]bool{}
	var out []string
	for _, typ := range s.rest {
		for _, leaf := range t8JSONLeaves(typ, "") {
			if !seen[leaf] {
				seen[leaf] = true
				out = append(out, leaf)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s t8Surface) toolLeaves() []string {
	leaves := t8SchemaLeaves(s.tool.Parameters(), "")
	sort.Strings(leaves)
	return leaves
}

// rows converts a family's table rows for this surface into the instrument's
// row shape. A mapping applies to the surface only through its own tool.
func (f t8Family) rows(s t8Surface) (mapped, excluded []t8ParityRow) {
	for _, m := range f.mappings {
		if m.tool != s.toolName {
			continue
		}
		mapped = append(mapped,
			t8ParityRow{side: "REST", path: m.rest, operation: m.ops},
			t8ParityRow{side: "tool", path: m.toolPath, operation: m.ops})
	}
	for _, e := range f.exclusions {
		excluded = append(excluded, t8ParityRow{side: e.side, path: e.path, operation: e.ops, reason: e.reason})
	}
	return mapped, excluded
}

// TestParity_RecursivePaths_MappingAndExclusions: every leaf of every REST
// create/update request and of every tool schema is mapped or excluded with a
// reason, and no row names a leaf that does not exist (13.1-13.4 against the
// real tables).
func TestParity_RecursivePaths_MappingAndExclusions(t *testing.T) {
	for _, family := range t8Families() {
		for _, surface := range family.surfaces {
			t.Run(family.name+"/"+surface.toolName, func(t *testing.T) {
				restLeaves, toolLeaves := surface.restLeaves(), surface.toolLeaves()
				require.NotEmpty(t, restLeaves, "instrument: saw the generated REST leaves")
				require.NotEmpty(t, toolLeaves, "instrument: saw the tool schema leaves")
				mapped, excluded := family.rows(surface)

				assert.Empty(t, t8Unmapped("REST", surface.op, restLeaves, mapped, excluded), "every REST leaf is mapped or excluded")
				assert.Empty(t, t8Unmapped("tool", surface.op, toolLeaves, mapped, excluded), "every tool leaf is mapped or excluded")

				// Stale rows: a mapping of this tool names a real leaf on both
				// sides, so a deleted field cannot leave a row behind.
				for _, m := range family.mappings {
					if m.tool != surface.toolName {
						continue
					}
					assert.Contains(t, restLeaves, m.rest, "mapping row names a REST leaf that does not exist: %+v", m)
					assert.Contains(t, toolLeaves, m.toolPath, "mapping row names a tool leaf that does not exist: %+v", m)
				}
			})
		}

		t.Run(family.name+"/excluded rows name real leaves and carry reasons", func(t *testing.T) {
			require.NotEmpty(t, family.exclusions, "instrument: the family has excluded rows")
			for _, e := range family.exclusions {
				assert.NotEmpty(t, strings.TrimSpace(e.reason), "13.5 excluded row has a reason: %+v", e)
				found := false
				for _, surface := range family.surfaces {
					if !t8OpApplies(e.ops, surface.op) {
						continue
					}
					leaves := surface.restLeaves()
					if e.side == "tool" {
						leaves = surface.toolLeaves()
					}
					if slices.Contains(leaves, e.path) {
						found = true
					}
				}
				assert.True(t, found, "excluded row names a leaf no applicable surface carries: %+v", e)
			}
		})
	}
}

// TestParity_TablesRefuseAnUnmappedLeaf is the mutation control for the
// binding above: with one real row removed or its reason blanked the same
// instrument fails, naming the leaf. Without it the binding could pass
// vacuously.
func TestParity_TablesRefuseAnUnmappedLeaf(t *testing.T) {
	for _, family := range t8Families() {
		surface := family.surfaces[0]
		mapped, excluded := family.rows(surface)
		require.Empty(t, t8Unmapped("REST", surface.op, surface.restLeaves(), mapped, excluded), "baseline is clean")

		t.Run(family.name+" removing a mapping row is caught", func(t *testing.T) {
			victim := ""
			for _, m := range family.mappings {
				if m.tool == surface.toolName {
					victim = m.rest
					break
				}
			}
			require.NotEmpty(t, victim)
			var kept []t8ParityRow
			for _, row := range mapped {
				if row.side == "REST" && row.path == victim {
					continue
				}
				kept = append(kept, row)
			}
			got := t8Unmapped("REST", surface.op, surface.restLeaves(), kept, excluded)
			assert.Contains(t, got, "unmapped REST "+surface.op+" "+victim)
		})
		t.Run(family.name+" blanking an exclusion reason is caught", func(t *testing.T) {
			require.NotEmpty(t, excluded)
			blank := slices.Clone(excluded)
			victim := -1
			for i, row := range blank {
				if row.side == "REST" && t8OpApplies(row.operation, surface.op) && slices.Contains(surface.restLeaves(), row.path) {
					victim = i
					break
				}
			}
			require.GreaterOrEqual(t, victim, 0, "instrument: a REST exclusion applies to the first surface")
			blank[victim].reason = " "
			got := t8Unmapped("REST", surface.op, surface.restLeaves(), mapped, blank)
			assert.Contains(t, got, "REST "+surface.op+" "+blank[victim].path+": exclusion requires a reason")
		})
	}
}

// TestParity_ExcludedRowsCarryReasons is scenario 13.5: each excluded
// property in the real tables has a non-empty reason, including rate_limits,
// todos and surface.
func TestParity_ExcludedRowsCarryReasons(t *testing.T) {
	reasons := map[string]string{}
	for _, rule := range task.FieldRules() {
		if rule.Group == task.GroupExcluded {
			assert.NotEmpty(t, strings.TrimSpace(rule.Reason), "task row %s property %s", rule.Row, rule.Property)
			reasons[rule.Property] = rule.Reason
		}
	}
	for _, rule := range agentmutation.FieldRules() {
		if rule.Reason != "" || rule.Property == "rate_limits" {
			reasons[rule.Property] = rule.Reason
		}
	}
	for _, name := range []string{"rate_limits", "todos", "surface"} {
		reason, present := reasons[name]
		require.True(t, present, "13.5: the table lists %s", name)
		assert.NotEmpty(t, strings.TrimSpace(reason), "13.5: %s has a reason", name)
	}
}

// TestRuleTable_TaskFrozenSetShared is scenario 13.6: the freeze set is one
// table read, {prompt, criteria, dod}, and update_task refuses exactly that set
// on a running task (the shared table read; REST PATCH enforcement is not asserted here).
func TestRuleTable_TaskFrozenSetShared(t *testing.T) {
	frozen := task.FrozenWhileRunningProperties()
	assert.ElementsMatch(t, []string{"prompt", "criteria", "dod"}, frozen, "13.6 the shared freeze set")

	// Every frozen property is a real property on both surfaces.
	family := t8Families()[0]
	for _, property := range frozen {
		assert.True(t, t8LeafUnder(family.surfaces[0].restLeaves(), property), "REST create carries %s", property)
		assert.True(t, t8LeafUnder(family.surfaces[1].restLeaves(), property), "REST update carries %s", property)
		assert.True(t, t8LeafUnder(family.surfaces[1].toolLeaves(), property), "update_task carries %s", property)
	}
	// The creator-only group contains the frozen set (definition properties).
	assert.Subset(t, task.CreatorOnlyProperties(), frozen)
}

// t8LeafUnder reports whether a leaf is the property or sits below it
// (criteria[].text is under criteria).
func t8LeafUnder(leaves []string, property string) bool {
	for _, leaf := range leaves {
		if leaf == property || strings.HasPrefix(leaf, property+".") || strings.HasPrefix(leaf, property+"[]") || strings.HasPrefix(leaf, property+"{}") {
			return true
		}
	}
	return false
}
