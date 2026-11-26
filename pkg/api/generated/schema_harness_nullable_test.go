// Tests of the schema harness's handling of the OpenAPI 3.0.3 keyword `nullable`.
//
// Skills & Tools redesign (#1963, epic #1519), Part H, architect ruling 2 of backend hand-off 8
// (rulings-handoff-8.md, in the team's working logs, not in the repo). The contract is OpenAPI 3.0.3
// (contracts/openapi.yaml, line 1) and many of its schemas say a field may be null with
// `nullable: true`. The harness compiles them with jsonschema/v6, which does not know that
// keyword, so before this change a `null` could never pass a field the contract allows to be null;
// the AgentToolsResponse fixture had to avoid the very rows of an installed skill
// (project_workspace null, configured_policy null) that C-4 describes. The ruling: fix the
// checker (yamlLoader.Load, schema_harness_test.go::openAPINullableToJSONSchema), never the
// schemas.
//
// The oracle is the OpenAPI 3.0.3 rule for `type`, the architect's ruling for `enum` and the
// contract's own field descriptions, never the harness: `nullable: true` adds "null" to the
// allowed type only when `type` is in the same schema object (OpenAPI 3.0.3), and the ruling adds
// the null to an `enum` of that object ("When the field also has an `enum`, add `null` to the
// enum.").
//   - AgentSkillEntry.yaml: project_workspace "null for an installed skill"; configured_policy
//     "null when it has none and follows the global value".
//   - TaskRun.yaml: occurrence_ms "Null for an ad-hoc/once/manual run"; ended_at "Null while
//     in_progress".
//
// What each test is for:
//   - TestSchemaHarness_NullableKeyword drives the real compiler over a temp schema pair and pins
//     the rule in both directions: a nullable field accepts null and still enforces its type,
//     enum, items and linked-file constraints; a field that is not nullable still rejects null
//     (the positive control the ruling asks for).
//   - TestContract_AgentToolsResponse_InstalledSkillNullFields and ..._InstalledSkillStillStrict
//     are the same two directions on the real AgentToolsResponse contract, with the installed-skill
//     fixture the ruling asks for next to the project-skill row of the Populated fixture.
//   - TestContract_TaskRun_InProgressNullFields shows the fix is not special to one schema: the
//     other fixture that documented the gap (FixtureTaskRun_Edge) is exercised with the nulls it
//     had to avoid.
//
// Mutation probes belong to independent CHECK. A probe that makes openAPINullableToJSONSchema a
// no-op must kill the three "accepts null" tests; a probe that makes it accept null everywhere
// must kill the "positive control" and "StillStrict" tests.

package generated

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingLocations lists the instance location of every leaf validation failure in err ("/" is
// the document root). A test uses it to require that a schema rejected a document for the reason
// the test names and not because of some other violation in the same document.
func failingLocations(err error) []string {
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return nil
	}
	var locations []string
	var walk func(*jsonschema.ValidationError)
	walk = func(failure *jsonschema.ValidationError) {
		if len(failure.Causes) == 0 {
			locations = append(locations, "/"+strings.Join(failure.InstanceLocation, "/"))
			return
		}
		for _, cause := range failure.Causes {
			walk(cause)
		}
	}
	walk(validation)
	return locations
}

// assertRejectedOnlyAt requires err to be a validation failure whose every leaf is at want.
func assertRejectedOnlyAt(t *testing.T, err error, want string) {
	t.Helper()
	require.Error(t, err, "the document must be rejected at %s", want)
	locations := failingLocations(err)
	require.NotEmpty(t, locations, "the rejection names its instance locations: %v", err)
	for _, location := range locations {
		assert.Equal(t, want, location, "rejected for the intended reason only; all locations: %v", locations)
	}
}

// nullableLinkedSchema is the file the main schema links to: its root is nullable, so a null for
// the linked property passes only if the loader also translates files reached through $ref.
const nullableLinkedSchema = `type: object
nullable: true
required: [id]
additionalProperties: false
properties:
  id:
    type: string
    minLength: 1
`

const nullableMainSchema = `type: object
additionalProperties: false
required: [plain]
properties:
  plain:
    type: string
  maybe_text:
    type: string
    nullable: true
  maybe_level:
    type: string
    nullable: true
    enum: [allow, ask, deny]
  never_null:
    type: string
    nullable: false
  maybe_list:
    type: array
    nullable: true
    items:
      type: string
  list_of_maybe:
    type: array
    items:
      type: string
      nullable: true
  linked:
    $ref: "./linked.yaml"
`

// TestSchemaHarness_NullableKeyword pins the OpenAPI 3.0.3 `nullable` rule on a temp schema pair
// loaded through the real compiler and yamlLoader.
// Traces to: OpenAPI 3.0.3 Schema Object, `nullable`; architect ruling 2 of hand-off 8.
func TestSchemaHarness_NullableKeyword(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "linked.yaml"), []byte(nullableLinkedSchema), 0o644))
	require.NoError(t, os.WriteFile(mainPath, []byte(nullableMainSchema), 0o644))

	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(jsonschema.SchemeURLLoader{"file": yamlLoader{}})
	schema, err := compiler.Compile(fileURL(mainPath))
	require.NoError(t, err, "the temp schema pair compiles through yamlLoader")

	cases := []struct {
		name string
		doc  string
		// rejectedAt is empty when the document must validate, else the instance location
		// at which (and only at which) it must be rejected.
		rejectedAt string
	}{
		{name: "baseline: only the required field", doc: `{"plain":"x"}`},
		{name: "nullable string accepts a string", doc: `{"plain":"x","maybe_text":"hello"}`},
		{name: "nullable string accepts null", doc: `{"plain":"x","maybe_text":null}`},
		{name: "nullable string still enforces its type", doc: `{"plain":"x","maybe_text":5}`, rejectedAt: "/maybe_text"},
		{name: "nullable enum accepts a member", doc: `{"plain":"x","maybe_level":"ask"}`},
		{name: "nullable enum accepts null", doc: `{"plain":"x","maybe_level":null}`},
		{name: "nullable enum still rejects a non-member", doc: `{"plain":"x","maybe_level":"maybe"}`, rejectedAt: "/maybe_level"},
		{name: "nullable array accepts null", doc: `{"plain":"x","maybe_list":null}`},
		{name: "nullable array still enforces its items", doc: `{"plain":"x","maybe_list":[1]}`, rejectedAt: "/maybe_list/0"},
		{name: "a nullable items schema accepts null inside the array", doc: `{"plain":"x","list_of_maybe":["a",null]}`},
		{name: "a nullable items schema still enforces its type", doc: `{"plain":"x","list_of_maybe":["a",7]}`, rejectedAt: "/list_of_maybe/1"},
		{name: "linked file: an object passes", doc: `{"plain":"x","linked":{"id":"a"}}`},
		{name: "linked file: its nullable root accepts null", doc: `{"plain":"x","linked":null}`},
		{name: "linked file: its required member is still enforced", doc: `{"plain":"x","linked":{}}`, rejectedAt: "/linked"},
		{name: "linked file: its minLength is still enforced", doc: `{"plain":"x","linked":{"id":""}}`, rejectedAt: "/linked/id"},
		{name: "POSITIVE CONTROL: a field without nullable rejects null", doc: `{"plain":null}`, rejectedAt: "/plain"},
		{name: "POSITIVE CONTROL: nullable false rejects null", doc: `{"plain":"x","never_null":null}`, rejectedAt: "/never_null"},
		{name: "POSITIVE CONTROL: a required field is still required", doc: `{}`, rejectedAt: "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc any
			require.NoError(t, json.Unmarshal([]byte(tc.doc), &doc))
			err := schema.Validate(doc)
			if tc.rejectedAt == "" {
				assert.NoError(t, err, "%s must validate", tc.doc)
				return
			}
			assertRejectedOnlyAt(t, err, tc.rejectedAt)
		})
	}
}

// installedSkillEntry is the row of an installed skill granted to an agent with no own value
// (Skills & Tools spec C-4): not a project skill and no override, so both nullable members are
// nil. The contract's descriptions are the oracle (see the file header).
func installedSkillEntry() AgentSkillEntry {
	return AgentSkillEntry{
		Key:              "deep-research",
		Name:             "deep-research",
		DisplayName:      "Deep Research",
		Granted:          true,
		ProjectWorkspace: nil,
		ConfiguredPolicy: nil,
		GlobalPolicy:     ToolPolicyAllow,
		EffectivePolicy:  ToolPolicyAllow,
		Unverified:       false,
	}
}

// agentToolsWithInstalledSkill is the Populated response (whose project-skill row, where the
// fixture has one, keeps every member set) with the installed-skill row added last.
func agentToolsWithInstalledSkill() AgentToolsResponse {
	response := FixtureAgentToolsResponse_Populated()
	response.Skills = append(response.Skills, installedSkillEntry())
	return response
}

// mutatedInstalledSkillDoc marshals agentToolsWithInstalledSkill, lets mutate edit the installed
// skill's row (the last of `skills`) and returns the document with the row's JSON pointer.
func mutatedInstalledSkillDoc(t *testing.T, mutate func(row map[string]any)) (raw []byte, rowPointer string) {
	t.Helper()
	encoded, err := json.Marshal(agentToolsWithInstalledSkill())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(encoded, &doc))
	skills, ok := doc["skills"].([]any)
	require.True(t, ok && len(skills) > 0, "fixture instrument: the response lists skills")
	last := len(skills) - 1
	row, ok := skills[last].(map[string]any)
	require.True(t, ok, "fixture instrument: a skills row is an object")
	require.Equal(t, "deep-research", row["key"], "fixture instrument: the last row is the installed skill")
	mutate(row)
	raw, err = json.Marshal(doc)
	require.NoError(t, err)
	return raw, fmt.Sprintf("/skills/%d", last)
}

// TestContract_AgentToolsResponse_InstalledSkillNullFields: an installed skill's row, with
// project_workspace and configured_policy both null, validates against AgentToolsResponse.
// Traces to: contracts/components/schemas/AgentSkillEntry.yaml (project_workspace and
// configured_policy are required and nullable); Skills & Tools spec C-4.
func TestContract_AgentToolsResponse_InstalledSkillNullFields(t *testing.T) {
	// Test the instrument: the fixture really sends the two nulls. A member that json drops
	// (omitempty) would turn this into a test of nothing.
	_, _ = mutatedInstalledSkillDoc(t, func(row map[string]any) {
		for _, field := range []string{"project_workspace", "configured_policy"} {
			value, present := row[field]
			require.True(t, present, "fixture instrument: %s is on the wire", field)
			require.Nil(t, value, "fixture instrument: %s is an explicit null", field)
		}
	})

	mustPassComponent(t, "AgentToolsResponse", agentToolsWithInstalledSkill())
}

// TestContract_AgentToolsResponse_InstalledSkillStillStrict is the positive control of ruling 2
// on the real contract: honouring `nullable` must not loosen anything else in the row.
func TestContract_AgentToolsResponse_InstalledSkillStillStrict(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(row map[string]any)
		// field is the member the row is rejected at; "" means the row itself.
		field string
	}{
		{"display_name null (a non-nullable string)", func(r map[string]any) { r["display_name"] = nil }, "display_name"},
		{"key null (a non-nullable string)", func(r map[string]any) { r["key"] = nil }, "key"},
		{"global_policy null (a non-nullable enum by $ref)", func(r map[string]any) { r["global_policy"] = nil }, "global_policy"},
		{"granted null (a non-nullable boolean)", func(r map[string]any) { r["granted"] = nil }, "granted"},
		{"configured_policy outside its enum (a nullable enum keeps its members)", func(r map[string]any) { r["configured_policy"] = "maybe" }, "configured_policy"},
		{"project_workspace of the wrong type (a nullable string keeps its type)", func(r map[string]any) { r["project_workspace"] = 5 }, "project_workspace"},
		{"configured_policy absent (required: null is allowed, absence is not)", func(r map[string]any) { delete(r, "configured_policy") }, ""},
		{"project_workspace absent (required: null is allowed, absence is not)", func(r map[string]any) { delete(r, "project_workspace") }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, rowPointer := mutatedInstalledSkillDoc(t, tc.mutate)
			want := rowPointer
			if tc.field != "" {
				want += "/" + tc.field
			}
			assertRejectedOnlyAt(t, validateAgainstComponentSchemaRawJSON(t, "AgentToolsResponse", raw), want)
		})
	}
}

// TestContract_TaskRun_InProgressNullFields: a run that is still in progress and was started by
// hand carries occurrence_ms null and ended_at null, and validates against TaskRun. These are the
// two required-and-nullable members FixtureTaskRun_Edge's doc comment says it had to avoid.
// Traces to: contracts/components/schemas/TaskRun.yaml (occurrence_ms "Null for an
// ad-hoc/once/manual run"; ended_at "Null while in_progress").
func TestContract_TaskRun_InProgressNullFields(t *testing.T) {
	run := TaskRun{
		RunId:        "01J8Z3K2R9G4S6M0N1P2Q3R4S5",
		TaskId:       "550e8400-e29b-41d4-a716-446655440000",
		OccurrenceMs: nil,
		Status:       TaskRunStatus("in_progress"),
		SessionId:    "session-uuid-1",
		Kind:         TaskRunKind("manual"),
		StartedAt:    time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
		EndedAt:      nil,
	}
	encoded, err := json.Marshal(run)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	for _, field := range []string{"occurrence_ms", "ended_at"} {
		value, present := wire[field]
		require.True(t, present, "fixture instrument: %s is on the wire", field)
		require.Nil(t, value, "fixture instrument: %s is an explicit null", field)
	}
	mustPassComponent(t, "TaskRun", run)

	// Positive controls on the same schema: the nullable members keep their types, and a
	// required member that is not nullable still rejects null.
	for field, value := range map[string]any{"occurrence_ms": "soon", "ended_at": 12, "session_id": nil} {
		doc := map[string]any{}
		require.NoError(t, json.Unmarshal(encoded, &doc))
		doc[field] = value
		raw, marshalErr := json.Marshal(doc)
		require.NoError(t, marshalErr)
		assertRejectedOnlyAt(t, validateAgainstComponentSchemaRawJSON(t, "TaskRun", raw), "/"+field)
	}
}
