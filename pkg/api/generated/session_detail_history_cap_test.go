package generated

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

// Extracted from contract_test.go so its grandfathered size only shrinks.
// D10's actual cap is100; the original100001 case remains a regression input.
func TestContract_SessionDetail_TooManyMessages(t *testing.T) {
	withoutCap := sessionDetailWithoutMessagesCap(t)
	for _, count := range []int{100, 101, 100001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			document := sessionDetailCapFixture(count)
			raw, err := json.Marshal(document)
			require.NoError(t, err)
			var normalized any
			require.NoError(t, json.Unmarshal(raw, &normalized))
			// This must fail on the former legacy-shaped fixture: removing only
			// the cap cannot excuse missing session/history_state/message fields.
			require.NoError(t, withoutCap.Validate(normalized), "instrument: fixture must be structurally valid when only messages.maxItems is removed")
			validationErr := validateAgainstComponentSchemaRawJSON(t, "SessionDetail", raw)
			if count == 100 {
				require.NoError(t, validationErr, "maximum100 is a real otherwise-valid positive control")
				return
			}
			sessionDetailRequireMessagesMaxItems(t, validationErr)
		})
	}
}

// not-wire-format: structural component-schema data, not a real producer or
// performance receipt. The former generic-error fixture was shown RED first.
func sessionDetailCapFixture(count int) map[string]any {
	messages := make([]any, count)
	positions := make([]string, count)
	for i := range messages {
		digest := sha256.Sum256([]byte(fmt.Sprintf("schema-only-occurrence-%d", i)))
		positions[i] = "hp1_" + base64.RawURLEncoding.EncodeToString(digest[:])
		messages[i] = map[string]any{
			"id": fmt.Sprintf("msg-%d", i), "role": "user", "agent_id": "jim",
			"timestamp": "2026-05-17T10:00:00Z", "history_position": positions[i],
			"content": "x", "type": "message", "status": "ok",
		}
	}
	// This test isolates JSON-Schema messages.maxItems. The separate structural
	// source-count cap stays100 even for invalid101/100001-message documents;
	// the no-cap control is not a claim of a semantically coherent real page.
	groups := min(count, 100)
	return map[string]any{
		"session": map[string]any{
			"id": "session-1", "type": "chat", "agent_id": "jim", "title": "Test session", "status": "active",
			"created_at": "2026-05-17T10:00:00Z", "updated_at": "2026-05-17T10:00:00Z", "channel": "webchat", "partitions": []string{},
			"stats": map[string]any{"cost": 0.0, "message_count": count, "tokens_in": 0, "tokens_out": 0, "tokens_total": 0, "tool_calls": 0},
		},
		"history_state": map[string]any{"status": "available"},
		"messages":      messages, "has_earlier": false, "has_later": false,
		"page_info": map[string]any{
			"mode": "latest", "limit": 100, "min_rows": 20, "row_count": count, "source_group_count": groups,
			"soft_byte_budget": 4194304, "projected_bytes": 0, "byte_budget_exceeded": false,
			"stop_reason": "end", "retention_generation": "schema-only-retained",
			"first_position": positions[0], "last_position": positions[len(positions)-1],
			"context": map[string]any{"spans": []any{}, "duplicate_ids": []any{}},
		},
	}
}

func sessionDetailWithoutMessagesCap(t *testing.T) *jsonschema.Schema {
	t.Helper()
	location := fileURL(filepath.Join(contractsDir(), "components", "schemas", "SessionDetail.yaml"))
	loaded, loadErr := (yamlLoader{}).Load(location)
	require.NoError(t, loadErr)
	// Independent in-memory resource only: no production file/shared compiler
	// mutation, and every referenced component still uses the actual contract.
	encoded, encodeErr := json.Marshal(loaded)
	require.NoError(t, encodeErr)
	var document map[string]any
	require.NoError(t, json.Unmarshal(encoded, &document))
	properties, propertiesOK := document["properties"].(map[string]any)
	require.True(t, propertiesOK)
	messages, messagesOK := properties["messages"].(map[string]any)
	require.True(t, messagesOK)
	delete(messages, "maxItems")
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(yamlLoader{})
	require.NoError(t, compiler.AddResource(location, document))
	schema, compileErr := compiler.Compile(location)
	require.NoError(t, compileErr)
	return schema
}

func sessionDetailRequireMessagesMaxItems(t *testing.T, err error) {
	t.Helper()
	var validation *jsonschema.ValidationError
	require.ErrorAs(t, err, &validation, "over-cap messages must fail the actual structural bound")
	leaves := 0
	var visit func(*jsonschema.ValidationError)
	visit = func(issue *jsonschema.ValidationError) {
		if len(issue.Causes) == 0 {
			leaves++
			require.Equal(t, []string{"messages"}, issue.InstanceLocation, "no unrelated missing-property/type error may satisfy the cap test")
			require.Equal(t, []string{"maxItems"}, issue.ErrorKind.KeywordPath())
		}
		for _, cause := range issue.Causes {
			visit(cause)
		}
	}
	visit(validation)
	require.Equal(t, 1, leaves, "only the messages cap may reject this otherwise-valid structural fixture")
}
