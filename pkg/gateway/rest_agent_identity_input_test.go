package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent oracles: ARCH-DECISIONS §1.4 accepts any letter-case of a listed
// hex, but never a colour outside the ten. The 2026-10-09 founder A ruling keeps
// create-only omission/null/empty as defaults, for all three create variants.
func TestAgentIdentity_PaletteCaseMatchesStrictValidation(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, operation := range []string{"Main", "Subagent", "subagent_3p", "PUT"} {
			for _, tc := range []struct {
				input, canonical string
			}{
				{"#fb923c", "#FB923C"},
				{"#22d3Ee", "#22D3EE"},
				{"#123456", ""}, // Well-formed hex, not in the published palette.
			} {
				t.Run(fmt.Sprintf("strict=%t/%s/%s", strict, operation, tc.input), func(t *testing.T) {
					api := buildExecutorTestAPI(t)
					api.agentLoop.GetConfig().Gateway.ValidateInbound = strict
					namesBefore := agentNameSet(t, api)
					storedBefore := savedAgent(t, api, "test-agent")
					stateBefore, err := agentstore.New(api.homePath).ReadState("test-agent")
					require.NoError(t, err)
					var code int
					var response map[string]any
					if operation == "PUT" {
						rec := putAgent(t, api, "test-agent", fmt.Sprintf(`{"color":%q}`, tc.input))
						code, response = rec.Code, decodeObject(t, rec.Body.Bytes())
					} else {
						rec := postAgent(t, api, identityCreateInput(operation, fmt.Sprintf(`"color":%q`, tc.input)))
						code, response = rec.Code, decodeObject(t, rec.Body.Bytes())
					}
					t.Logf("COLOUR_CASE strict=%t operation=%s input=%s status=%d color=%v", strict, operation, tc.input, code, response["color"])
					if tc.canonical == "" {
						assert.Equal(t, http.StatusBadRequest, code)
						assert.Equal(t, identityInputError("color", operation, strict), response, "preserve the existing specific rejection envelope")
						assert.Equal(t, namesBefore, agentNameSet(t, api), "invalid colour must not create an agent")
						assert.Equal(t, storedBefore, savedAgent(t, api, "test-agent"))
						stateAfter, err := agentstore.New(api.homePath).ReadState("test-agent")
						require.NoError(t, err)
						assert.Equal(t, stateBefore, stateAfter, "invalid colour must not update state or revision")
						return
					}
					wantStatus := http.StatusCreated
					if operation == "PUT" {
						wantStatus = http.StatusOK
					}
					require.Equal(t, wantStatus, code, "response: %#v", response)
					assert.Equal(t, tc.canonical, response["color"], "response uses the canonical palette spelling")
					id, ok := response["id"].(string)
					require.True(t, ok)
					assert.Equal(t, tc.canonical, savedAgent(t, api, id)["color"], "actual stored colour must be canonical too")
				})
			}
		}
	}
}

func TestAgentIdentity_CreateNullAndEmptyUseDefaults(t *testing.T) {
	cases := []struct {
		name, fields string
	}{
		{"omitted", ""},
		{"null figure", `"figure":null`},
		{"null role", `"role":null`},
		{"null color", `"color":null`},
		{"empty figure", `"figure":""`},
		{"empty role", `"role":""`},
		{"empty color", `"color":""`},
		{"all null", `"figure":null,"role":null,"color":null`},
		{"all empty", `"figure":"","role":"","color":""`},
	}
	for _, strict := range []bool{false, true} {
		for _, variant := range []string{"Main", "Subagent", "subagent_3p"} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("strict=%t/%s/%s", strict, variant, tc.name), func(t *testing.T) {
					api := buildExecutorTestAPI(t)
					api.agentLoop.GetConfig().Gateway.ValidateInbound = strict
					rec := postAgent(t, api, identityCreateInput(variant, tc.fields))
					t.Logf("CREATE_DEFAULT_CASE strict=%t variant=%s input=%s status=%d", strict, variant, tc.name, rec.Code)
					require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
					response := decodeObject(t, rec.Body.Bytes())
					id, ok := response["id"].(string)
					require.True(t, ok)
					stored := savedAgent(t, api, id)
					for field, expected := range map[string]string{"figure": "Omnipus", "role": "general", "color": "#9CA3AF"} {
						assert.Equal(t, expected, response[field], "create response default for %s", field)
						assert.Equal(t, expected, stored[field], "persisted create default for %s", field)
					}
				})
			}
		}
	}
}

func TestAgentIdentity_CreateNonemptyInvalidIsZeroWrite(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, variant := range []string{"Main", "Subagent", "subagent_3p"} {
			for _, tc := range []struct{ field, value string }{
				{"figure", "octopus"}, {"figure", "omnipus"}, {"figure", " "},
				{"role", "Developer"}, {"role", "General"}, {"role", " "},
				{"color", "#123456"}, {"color", "#d4af37"}, {"color", " "},
			} {
				t.Run(fmt.Sprintf("strict=%t/%s/%s", strict, variant, tc.field), func(t *testing.T) {
					api := buildExecutorTestAPI(t)
					api.agentLoop.GetConfig().Gateway.ValidateInbound = strict
					before := agentNameSet(t, api)
					rec := postAgent(t, api, identityCreateInput(variant, fmt.Sprintf(`%q:%q`, tc.field, tc.value)))
					assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
					assert.Equal(t, identityInputError(tc.field, variant, strict), decodeObject(t, rec.Body.Bytes()), "preserve the existing specific rejection envelope")
					assert.Equal(t, before, agentNameSet(t, api), "an invalid nonempty identity is not a default request")
				})
			}
		}
	}
}

// Characterization oracle for published error copy only. The semantic 400 and
// no-write assertions above derive independently from the founder's ruling and
// ARCH-DECISIONS. Strict messages enumerate the closed contract sets rather
// than naming the field; keep that existing envelope, not a new message format.
func identityInputError(field, operation string, strict bool) map[string]any {
	message := map[string]string{
		"figure": "figure must be Robot, Man, Woman, Omnipus, or Monogram",
		"role":   "role must be one of the curated role slugs",
		"color":  "color must be one of the ten identity colours",
	}[field]
	if strict {
		schema := map[string]string{
			"Main": "AgentCreateRequestMain", "Subagent": "AgentCreateRequestSubagent",
			"subagent_3p": "AgentCreateRequestSubagent3p", "PUT": "AgentUpdateRequest",
		}[operation]
		allowed := map[string][]string{
			"figure": {"Robot", "Man", "Woman", "Omnipus", "Monogram"},
			"role":   {"writer", "designer", "image", "video", "audio", "social", "developer", "data", "analyst", "itops", "automation", "security", "quality", "science", "orchestrator", "project", "product", "sales", "marketing", "finance", "legal", "support", "documents", "researcher", "people", "tutor", "knowledge", "translator", "general", "personal", "office"},
			"color":  {"#3B82F6", "#38BDF8", "#22D3EE", "#818CF8", "#A78BFA", "#C084FC", "#E879F9", "#F472B6", "#FB923C", "#9CA3AF"},
		}[field]
		message = "request body does not match schema " + schema + ": value must be one of '" + strings.Join(allowed, "', '") + "'"
	}
	return map[string]any{"error": message}
}

func identityCreateInput(variant, fields string) string {
	body := fmt.Sprintf(`{"name":"Input Boundary","type":%q,"description":"focused worker","soul":"editable-soul"`, variant)
	if variant == "subagent_3p" {
		// Match the existing executor fixture; creating it never launches a CLI.
		body += `,"executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"}`
	}
	if fields != "" {
		body += "," + fields
	}
	return body + "}"
}
