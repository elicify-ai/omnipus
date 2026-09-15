// REST create-by-type tests for the discriminated-union AgentCreateRequest
// (W1: AgentCreateRequestMain / AgentCreateRequestSubagent /
// AgentCreateRequestSubagent3p). Proves:
//  1. Omitted type → 400 (the historical omit-type→Main default is retired;
//     type is now a required, single-value discriminator on every variant).
//  2. type="Subagent" → persisted as "worker" (no default, not a chat
//     target), and is unlocked so the operator can edit it. Response echoes
//     "Subagent".
//  3. type="core" / "system" / anything else → 400.
//  4. updateAgent does NOT change Type (a custom stays custom).
//  5. type="subagent_3p" with executor.kind=external-cli persists directly
//     (Subagent has no executor property at all any more — there is no
//     "reclassify a Subagent create into subagent_3p" path; the caller must
//     choose the variant up front).
//  6. Main create with an `executor` key: the field does not exist on
//     AgentCreateRequestMain — it is unknown-field-ignored (ValidateInbound
//     off) rather than coerced-with-a-warning (the old runtime coercion
//     check is gone; see TestCreateAgent_ValidateInbound_MainWithExecutorRejected
//     in rest_inbound_validate_test.go for the ValidateInbound-on 400 case).
//  7. Worker create with a delegation_policy field at all → 400 (ADR-037:
//     delegation_policy is retired from the wire entirely; delegation is
//     configured exclusively via the per-workspace Team tab now).
//
// The test scaffolding is the same as rest_agent_executor_test.go:
// buildExecutorTestAPI() gives a fresh temp config.json + restAPI handle so
// the write gate (safeUpdateConfigJSON) has a real file to mutate.

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// findTypeTestAgentInStore returns the persisted config.AgentConfig with the
// given name (case-sensitive) from the entity store (entities/agents/<id>.json),
// or nil when not found.
//
// ADR-054 + the config.AgentsConfig.List = `json:"-"` follow-up: agents are
// per-entity records now, and agents.list can NEVER be marshaled into
// config.json by any code path — createAgent/updateAgent persist exclusively
// via agentstore.Store. Tests must therefore assert on "the persisted type"
// by reading the entity store directly, not config.json (which was the
// historical, now-obsolete, fixture assertion this helper replaces).
func findTypeTestAgentInStore(t *testing.T, homePath, name string) *config.AgentConfig {
	t.Helper()
	agents, _, err := agentstore.New(homePath).List()
	require.NoError(t, err)
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}
