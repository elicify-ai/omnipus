package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: approved T11 §4 / FR-8 / U-5 and the Agent/AgentFieldDescriptor
// contract: per-agent env and extra flags are editable, while Settings-owned
// program/preset fields are not. Exercise real POST and GET responses, not the
// metadata producer or a mocked descriptor. No separately developed fix read.
func TestT11FR8_ExternalWorkerAdvertisesEditableRuntimeFields(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			body := fmt.Sprintf(`{"name":"Runtime editability worker","type":"subagent_3p","description":"T11 U-5","executor":{"cli":%q}}`, cli)
			created := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			api.HandleAgents(created, request)
			require.Equal(t, http.StatusCreated, created.Code, "SETUP: create real external worker: %s", created.Body.String())
			worker := decodeAgentResp(t, created.Body.Bytes())
			require.Equal(t, gen.AgentTypeSubagent3p, worker.Type)
			require.NotEmpty(t, worker.Id)

			read := httptest.NewRecorder()
			api.HandleAgents(read, httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+worker.Id, nil))
			require.Equal(t, http.StatusOK, read.Code, "SETUP: read real worker for Runtime: %s", read.Body.String())
			for _, surface := range []struct {
				name  string
				agent gen.Agent
			}{
				{"create_echo", worker},
				{"runtime_get", decodeAgentResp(t, read.Body.Bytes())},
			} {
				t.Run(surface.name, func(t *testing.T) {
					require.NotNil(t, surface.agent.EditableFields, "FR-8: the real response must carry editable_fields")
					fields := make(map[string]gen.AgentFieldDescriptor)
					var editable []string
					for _, descriptor := range *surface.agent.EditableFields {
						_, duplicate := fields[descriptor.Name]
						assert.False(t, duplicate, "duplicate descriptor must not hide conflicting editability: %s", descriptor.Name)
						fields[descriptor.Name] = descriptor
						if descriptor.Editable {
							editable = append(editable, descriptor.Name)
						}
					}
					for _, name := range []string{"executor.env_overrides", "executor.env_overrides_remove", "executor.cli_args"} {
						descriptor, present := fields[name]
						assert.True(t, present, "FR-8 / U-5: editable_fields must include %s", name)
						assert.True(t, descriptor.Editable, "FR-8 / U-5: %s must remain editable for an external worker", name)
					}
					for _, name := range []string{"executor.kind", "executor.cli", "executor.effective_cli_path"} {
						descriptor, present := fields[name]
						assert.True(t, present, "Agent contract: protected descriptor %s is reported", name)
						assert.False(t, descriptor.Editable, "FR-8: %s is not per-agent editable", name)
					}
					// Read-only fields must never be advertised writable. The
					// contract does not require a descriptor for preset parameters.
					for _, name := range []string{"executor.effective_cli_path", "executor.effective_preset_args", "executor.cli_path", "executor.preset_args"} {
						assert.NotContains(t, editable, name, "FR-8: person-owned program and parameters stay read-only")
					}
				})
			}
		})
	}
}
