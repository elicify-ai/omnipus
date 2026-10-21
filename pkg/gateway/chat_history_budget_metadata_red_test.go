package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

// D-B5: validate the actual embedded/generated contract with an otherwise-valid
// empty terminal response. Positive controls stop an unrelated schema error
// from masquerading as budget validation. This is not a handwritten validator.
func TestChatHistoryPage_AdvertisedBudgetSemanticBoundaries(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{"2097152", true}, {"4194304", true}, {"8388608", true}, {"0", false}, {"-1", false}, {"null", false}, {"1.5", false}} {
		t.Run(tc.value, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"session_id":"budget-contract","entries":[],"has_earlier":false,"has_later":false,"page_info":{"mode":"latest","limit":40,"min_rows":20,"row_count":0,"source_group_count":0,"soft_byte_budget":%s,"projected_bytes":0,"byte_budget_exceeded":false,"stop_reason":"end","retention_generation":"retained","context":{"spans":[],"duplicate_ids":[]}}}`, tc.value))
			msg, serverErr := validateBodyAgainstSchema("SessionHistoryResponse", body)
			require.False(t, serverErr, "real schema must compile; this is not an infrastructure refusal")
			if tc.valid {
				require.Empty(t, msg, "consistent positive advertised budgets are not a one-value enum")
			} else {
				require.NotEmpty(t, msg)
				// The existing user-facing formatter deliberately strips path
				// prefixes. Assert the stronger structured path/keyword instead
				// of inventing a contract for its presentation string.
				schema, err := compileInboundSchema("SessionHistoryResponse")
				require.NoError(t, err)
				var document any
				require.NoError(t, json.Unmarshal(body, &document))
				var validation *jsonschema.ValidationError
				require.ErrorAs(t, schema.Validate(document), &validation)
				keyword := "minimum"
				if tc.value == "null" || tc.value == "1.5" {
					keyword = "type"
				}
				leaves := 0
				var check func(*jsonschema.ValidationError)
				check = func(issue *jsonschema.ValidationError) {
					if len(issue.Causes) == 0 {
						leaves++
						require.Equal(t, []string{"page_info", "soft_byte_budget"}, issue.InstanceLocation)
						require.Equal(t, []string{keyword}, issue.ErrorKind.KeywordPath())
					}
					for _, cause := range issue.Causes {
						check(cause)
					}
				}
				check(validation)
				require.Equal(t, 1, leaves, "only the deliberately invalid budget field may fail")
			}
		})
	}
	t.Run("server_default", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		defer cleanup()
		sid := createTestSession(t, api)
		r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{}, historyPageProjectionDeps{ToolStore: newToolResultStore(api.homePath)})
		require.NoError(t, err, "real factory supplies its one weigher/defaults")
		require.NotNil(t, r)
		prepared, err := r.Read(context.Background(), api.agentLoop.GetSessionStore())
		require.NoError(t, err)
		require.EqualValues(t, 4194304, prepared.Response.PageInfo.SoftByteBudget)
		encoded, err := json.Marshal(prepared.Response)
		require.NoError(t, err)
		msg, serverErr := validateBodyAgainstSchema("SessionHistoryResponse", encoded)
		require.False(t, serverErr)
		require.Empty(t, msg)
	})
}
