package systools_test

// T-26 / Agent-tool parity / FR-030: compare the actual writer's parameter
// schema with the committed generated REST request. Expected names, required
// fields, types and enum are from channels-spec-r8, not from new tool output.
// Metadata parity is not proof of execution authority, consent or persistence.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func t26RESTFieldNames() []string {
	typ := reflect.TypeOf(gen.ChannelOutboundUpdateRequest{})
	var names []string
	for i := range typ.NumField() {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			names = append(names, name)
		}
	}
	return names
}

func t26FindTool(t *testing.T, name string) tools.Tool {
	t.Helper()
	var matches []tools.Tool
	for _, tool := range systools.AllTools(nil) {
		if tool.Name() == name {
			matches = append(matches, tool)
		}
	}
	if len(matches) == 0 {
		t.Fatalf("BLOCKED: %s is not registered in AllTools — required by FR-030/FR-032/T-26", name)
	}
	require.Len(t, matches, 1, "FR-032: tool metadata must have one canonical registered entry")
	return matches[0]
}

func t26Parameters(t *testing.T, tool tools.Tool) (map[string]any, map[string]any) {
	t.Helper()
	// Normalize schema representation only (e.g. []string vs []any); no
	// replacement descriptor or test-side writable-field list is supplied.
	raw, err := json.Marshal(tool.Parameters())
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "instrument: real tool metadata must declare an object property map")
	return schema, properties
}

func TestT26_GeneratedRESTRequestHasOnlyPolicyTargetsAndRevision(t *testing.T) {
	require.ElementsMatch(t, []string{"policy", "targets", "expected_revision"}, t26RESTFieldNames(),
		"T-26/ChannelOutboundUpdateRequest: contract drift requires an explicit agent-parity decision")
}

func TestT26_OutboundWriterExactlyMatchesTheUserWritableSurface(t *testing.T) {
	tool := t26FindTool(t, "set_channel_outbound")
	schema, properties := t26Parameters(t, tool)
	require.Equal(t, "object", schema["type"])
	require.Equal(t, false, schema["additionalProperties"], "FR-030: undeclared writable fields must be refused")
	require.ElementsMatch(t, []string{"id", "policy", "targets", "expected_revision"}, schema["required"],
		"FR-030: routing id, both writable properties and concurrency token are all required")
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	require.ElementsMatch(t, []string{"id", "policy", "targets", "expected_revision"}, names,
		"FR-030/T-26: no missing or extra tool field beyond the explicit routing/token differences")
	var restWritable, toolWritable []string
	for _, name := range t26RESTFieldNames() {
		if name != "expected_revision" {
			restWritable = append(restWritable, name)
		}
	}
	for _, name := range names {
		if name != "id" && name != "expected_revision" {
			toolWritable = append(toolWritable, name)
		}
	}
	require.ElementsMatch(t, restWritable, toolWritable, "T-26: every REST writable property maps by the same name, with no extra writable field")
	for _, name := range []string{"id", "expected_revision", "policy"} {
		property, ok := properties[name].(map[string]any)
		require.True(t, ok, "FR-030: %s is a declared property", name)
		require.Equal(t, "string", property["type"], "FR-030: %s has string type", name)
	}
	// The loop above already fatally requires policy to be an object.
	policy, _ := properties["policy"].(map[string]any)
	require.ElementsMatch(t, []string{"reply_only", "agent_may_start"}, policy["enum"], "FR-030: exact posting-policy enum")
	targets, ok := properties["targets"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "array", targets["type"], "FR-030: the writer receives the whole target list")
	items, ok := targets["items"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", items["type"], "FR-030: targets are exact string ids")
}

func TestT26_ExistingConnectionWritersDoNotDeclareOutbound(t *testing.T) {
	// Positive controls: real pre-existing metadata discovery remains live;
	// dedicated-write parity must not silently add a second posting-list door.
	for _, name := range []string{"configure_channel", "connect_channel"} {
		t.Run(name, func(t *testing.T) {
			_, properties := t26Parameters(t, t26FindTool(t, name))
			require.NotContains(t, properties, "outbound", "FR-025: this is not an outbound writer")
			require.NotContains(t, properties, "outbound.policy")
			require.NotContains(t, properties, "outbound.targets")
		})
	}
}
