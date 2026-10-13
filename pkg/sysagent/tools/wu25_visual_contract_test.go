package systools_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R4: inspect the real provider-facing callable definitions, not source text or
// a private Parameters-only instance. Enums must be exactly the UI catalogs;
// descriptions may instead disclose those choices without pinning prose.
func TestWU25VisualContract_DisclosesExistingCatalogs(t *testing.T) {
	h := newWU25Harness(t)
	for _, name := range []string{"create_agent", "update_agent"} {
		fn := wu25Function(t, h.registry, name)
		props := wu25Properties(t, fn)
		for _, catalog := range wu25Catalogs(t) {
			t.Run(name+"/"+catalog.field, func(t *testing.T) {
				raw, present := props[catalog.field]
				if !present {
					t.Fatalf("BLOCKED: %s.%s callable input not implemented — required by WU-25/#1303 R1/R4", name, catalog.field)
				}
				property, ok := raw.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "string", property["type"], "visuals use the existing string catalogs")
				if rawEnum, hasEnum := property["enum"]; hasEnum {
					items, isArray := rawEnum.([]any)
					require.True(t, isArray)
					actual := make([]string, 0, len(items))
					for _, item := range items {
						value, isString := item.(string)
						require.True(t, isString)
						actual = append(actual, value)
					}
					want := slices.Clone(catalog.values)
					slices.Sort(actual)
					slices.Sort(want)
					assert.Equal(t, want, actual, "R4: no invented/extra choice and no missing UI choice")
				} else {
					description, ok := property["description"].(string)
					require.True(t, ok, "R4: without enums the field description must expose the whole catalog")
					tokens := wu25ChoiceTokens(description)
					for _, value := range catalog.values {
						assert.True(t, tokens[value], "R4: %s.%s hides valid choice %q from the model", name, catalog.field, value)
					}
				}
				if catalog.field == "role" {
					description, ok := property["description"].(string)
					require.True(t, ok, "#1303 requires discoverable role/icon semantics, not just slugs")
					assert.Regexp(t, regexp.MustCompile(`(?i)badge|icon`), description, "role selects the existing badge/icon artwork")
					assert.Regexp(t, regexp.MustCompile(`(?i)presentation|visual|avatar|display`), description, "role must be identified as presentation")
				}
			})
		}
	}
}

// R3: for a custom native agent every advertised editable field must actually
// be accepted by update_agent's callable schema, and vice versa. This also
// catches false permission-field advertisements after the FR-024 split.
func TestWU25EditableFields_MatchCallableUpdateContract(t *testing.T) {
	for _, runtimeType := range []string{"Main", "Subagent"} {
		t.Run(runtimeType, func(t *testing.T) {
			h := newWU25Harness(t)
			id := wu25SeedAgent(t, h, runtimeType)
			descriptors := wu25Descriptors(t, h.read(t, id))
			props := wu25Properties(t, wu25Function(t, h.registry, "update_agent"))
			var advertised, accepted []string
			for name, descriptor := range descriptors {
				if descriptor["editable"] == true {
					advertised = append(advertised, name)
				}
			}
			for name := range props {
				if name != "id" && name != "revision" {
					accepted = append(accepted, name)
				}
			}
			slices.Sort(advertised)
			slices.Sort(accepted)
			assert.Equal(t, accepted, advertised, "R3: get_agent must not advertise an unsupported update operation")
			for _, field := range []string{"figure", "role", "color"} {
				descriptor, present := descriptors[field]
				require.True(t, present, "R3: %s descriptor must exist", field)
				assert.Equal(t, true, descriptor["editable"], "custom %s must be editable", field)
				_, callable := props[field]
				assert.True(t, callable, "R3: editable %s must be callable, not just a label", field)
			}
		})
	}
}
