package systools_test

// r15 C-1/T-S5 and §5.2: fields are derived from the contract's properties.
// Missing contract is a loud dependency failure, not an authorship deferral.
import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGetSkill_FieldsEnumMatchesSkillDetailContract(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	schemas := filepath.Join(root, "contracts", "components", "schemas")
	// Instrument sensitivity: this repository really has a readable schema root.
	raw, err := os.ReadFile(filepath.Join(schemas, "Skill.yaml"))
	require.NoError(t, err)
	var control map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &control))
	require.Contains(t, control, "properties")
	raw, err = os.ReadFile(filepath.Join(schemas, "SkillDetail.yaml"))
	if os.IsNotExist(err) {
		t.Fatal("BLOCKED: SkillDetail contract not implemented — required by r15 C-1/T-S5 and Hard Constraint #8")
	}
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	expected := []string{"all"}
	for key := range props {
		if key != "id" && key != "revision" && key != "absent_fields" && key != "truncated_fields" {
			expected = append(expected, key)
		}
	}
	assert.Len(t, expected, 15, "14 selectable properties plus all, from r15 §5.2")
	params := systools.NewGetSkillTool(nil).Parameters()
	properties, ok := params["properties"].(map[string]any)
	require.True(t, ok)
	fields, ok := properties["fields"].(map[string]any)
	require.True(t, ok)
	items, ok := fields["items"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, expected, items["enum"], "contract, runtime parameter enum, and documented field set may not drift")
}
