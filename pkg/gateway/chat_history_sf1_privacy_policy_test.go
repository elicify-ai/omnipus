package gateway

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type sf1IdentityFixture struct {
	sessionID, agentID, workspaceID string
	agentIDs                        []string
}

// Security F2 policy: ONLY these four contracted identity slots are exempt from
// the identity-fragment bans. Assert exact fixture values/types first; replace
// only those exact nodes in an independently parsed copy. No title, summary,
// task_id, parent_session_id, recursive key-name or blanket text exemption.
// Physical paths/raw causes are NEVER masked: check the original whole response
// separately, including decoded strings/keys so JSON escapes cannot hide them.
func sf1MaskedIdentityCopy(t *testing.T, body []byte, expected sf1IdentityFixture) []byte {
	t.Helper()
	original := append([]byte(nil), body...)
	var checked map[string]any
	require.NoError(t, json.Unmarshal(body, &checked))
	metadata, metadataOK := checked["session"].(map[string]any)
	require.True(t, metadataOK)
	for key, want := range map[string]string{"id": expected.sessionID, "agent_id": expected.agentID} {
		value, valueOK := metadata[key].(string)
		require.True(t, valueOK, "identity slot %s must be a string, not an unvalidated exemption", key)
		require.Equal(t, want, value, "exact fixture-derived identity %s", key)
	}
	if expected.workspaceID == "" {
		require.NotContains(t, metadata, "workspace_id")
	} else {
		value, valueOK := metadata["workspace_id"].(string)
		require.True(t, valueOK)
		require.Equal(t, expected.workspaceID, value)
	}
	if len(expected.agentIDs) == 0 {
		require.NotContains(t, metadata, "agent_ids")
	} else {
		values, valuesOK := metadata["agent_ids"].([]any)
		require.True(t, valuesOK)
		require.Len(t, values, len(expected.agentIDs), "extra participants cannot hide behind an identity exemption")
		for index, want := range expected.agentIDs {
			value, valueOK := values[index].(string)
			require.True(t, valueOK, "every participant is a checked string")
			require.Equal(t, want, value, "complete fixture participant list, not one included element")
		}
	}
	var independent map[string]any
	require.NoError(t, json.Unmarshal(original, &independent))
	copyMetadata, copyOK := independent["session"].(map[string]any)
	require.True(t, copyOK)
	copyMetadata["id"] = "AUTHORIZED_SESSION_ID"
	copyMetadata["agent_id"] = "AUTHORIZED_AGENT_ID"
	if expected.workspaceID != "" {
		copyMetadata["workspace_id"] = "AUTHORIZED_WORKSPACE_ID"
	}
	if len(expected.agentIDs) != 0 {
		neutral := make([]string, len(expected.agentIDs))
		for index := range neutral {
			neutral[index] = "AUTHORIZED_PARTICIPANT_ID"
		}
		copyMetadata["agent_ids"] = neutral
	}
	masked, marshalErr := json.Marshal(independent)
	require.NoError(t, marshalErr)
	require.Equal(t, original, body, "original document bytes are untouched by assertion masking")
	return masked
}

func sf1JSONTexts(t *testing.T, body []byte) []string {
	t.Helper()
	var document any
	require.NoError(t, json.Unmarshal(body, &document), "privacy oracle examines decoded JSON, not escape-vulnerable text alone")
	texts := []string{string(body)}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, item := range typed {
				texts = append(texts, key)
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case string:
			texts = append(texts, typed)
		}
	}
	visit(document)
	return texts
}

func sf1PrivacyFragmentsFound(t *testing.T, body []byte, fragments []string) []string {
	t.Helper()
	found := make([]string, 0)
	texts := sf1JSONTexts(t, body)
	for _, fragment := range fragments {
		if fragment == "" {
			continue
		}
		for _, text := range texts {
			if strings.Contains(text, fragment) {
				found = append(found, fragment)
				break
			}
		}
	}
	return found
}

func sf1AssertAuthorizedIdentityPolicy(t *testing.T, env *u1Env, body string, expected sf1IdentityFixture) {
	t.Helper()
	// The physical/raw-cause oracle always receives original, unmasked bytes.
	sf1AssertNoPhysicalStorageDetail(t, env, body)
	masked := sf1MaskedIdentityCopy(t, []byte(body), expected)
	for _, text := range sf1JSONTexts(t, masked) {
		sf1AssertNoInternalDetail(t, env, text, expected.workspaceID, expected.agentID, "transcript")
	}
}

func TestSF1_IdentityMaskDoesNotExemptUncontractedLocations(t *testing.T) {
	identity := sf1IdentityFixture{sessionID: "main-session-workspace-f2-mia", agentID: "mia", workspaceID: "workspace-f2", agentIDs: []string{"mia", "jim"}}
	clean := []byte(`{"session":{"id":"main-session-workspace-f2-mia","agent_id":"mia","workspace_id":"workspace-f2","agent_ids":["mia","jim"],"title":"Safe controlled title"},"history_state":{"status":"unavailable","message":"Fixed sanitized message."}}`)
	fragments := []string{"main-session-", identity.sessionID, identity.agentID, identity.workspaceID, "transcript"}
	masked := sf1MaskedIdentityCopy(t, clean, identity)
	require.Empty(t, sf1PrivacyFragmentsFound(t, masked, fragments), "positive instrument: exactly checked identity slots are the sole exception")
	var duplicate map[string]any
	require.NoError(t, json.Unmarshal(clean, &duplicate))
	metadata, metadataOK := duplicate["session"].(map[string]any)
	require.True(t, metadataOK)
	metadata["title"] = identity.sessionID // Not one of the four permitted nodes.
	leaking, marshalErr := json.Marshal(duplicate)
	require.NoError(t, marshalErr)
	maskedLeak := sf1MaskedIdentityCopy(t, leaking, identity)
	found := sf1PrivacyFragmentsFound(t, maskedLeak, fragments)
	require.Contains(t, found, "main-session-", "negative instrument: an allowed identity copied into title must still fail legacy privacy")
	require.Contains(t, found, identity.workspaceID)
	require.Contains(t, found, identity.agentID)
	// Escaped paths/raw causes must be detected after decoding too.
	encodedSlash := string(rune(0x5c)) + "u002f" // JSON escape backslash (ASCII0x5c), not a literal path slash.
	escaped := []byte(strings.ReplaceAll(`{"error":"/Users/private/meta.json: ENOTDIR"}`, "/", encodedSlash))
	require.NotContains(t, string(escaped), "/Users/", "negative instrument must actually hide the raw-text fragment")
	require.Contains(t, sf1PrivacyFragmentsFound(t, escaped, []string{"/Users/", "meta.json", "ENOTDIR"}), "/Users/", "decoded original strings still reveal the escaped path")
	require.NotEqual(t, string(clean), string(masked), "masking changes only the independent copy")
}

func sf1PhysicalFragments(t *testing.T, env *u1Env) []string {
	t.Helper()
	home, homeErr := os.UserHomeDir()
	require.NoError(t, homeErr)
	return []string{env.home, env.store(t).BaseDir(), os.TempDir(), home, "/Users/", "/var/", "/tmp", "~", "meta.json", ".jsonl", "OMNIPUS_HOME", "ENOTDIR", "not a directory", "permission denied"}
}
