package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func round2AllowFromAPI(t *testing.T, platform string) (*restAPI, string) {
	t.Helper()
	// The chief's F6/C cases exercise persistence, not network startup. The
	// existing synthetic token reference makes Discord's seed complete.
	seed := fmt.Sprintf(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{%q:{"type":%q,"enabled":false,"token_ref":"round2-existing-token","allow_from":["old-sender"]}}}`,
		platform, platform)
	api := newChannelTestAPI(t, seed)
	t.Setenv("OMNIPUS_HOME", api.homePath)
	return api, filepath.Join(api.homePath, "config.json")
}

func round2PutAllowFrom(api *restAPI, platform, value string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPut, "/api/v1/channels/"+platform+"/configure", strings.NewReader(`{"allow_from":`+value+`}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.HandleChannels(response, request)
	return response
}

// Chief ruling C, 2026-11-02 08:09Z: PUT null is an error, not an implicit
// widening of admission. The low-level handler must enforce this even when
// optional inbound schema validation is off (the existing fixture's mode).
func TestRound2_ConfigureNullAllowFromRejectsWithoutWriting(t *testing.T) {
	for _, platform := range []string{"discord", "whatsapp"} {
		t.Run(platform, func(t *testing.T) {
			api, path := round2AllowFromAPI(t, platform)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			response := round2PutAllowFrom(api, platform, "null")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, response.Code, 400, "chief C: Configure null must be a client error; body=%s", response.Body.String())
			assert.Less(t, response.Code, 500, "chief C: null is invalid input, not an internal failure")
			assert.Equal(t, string(before), string(after), "chief C: rejected null must leave config.json byte-for-byte unchanged")
			var body map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Contains(t, body, "error", "chief C: rejected null needs an explicit error response")
			var message string
			require.NoError(t, json.Unmarshal(body["error"], &message))
			assert.Contains(t, strings.ToLower(message), "allow_from", "clear error identifies the invalid field")
			assert.Regexp(t, `(?i)(null|array|list)`, message, "clear error explains null is not a valid allowed-ID list")
		})
	}
}

// Chief's F6 correction: empty text saves an empty list, possibly omitted by
// omitempty. Never require literal []; raw null, [""] and scalar "" are wrong.
func TestRound2_ConfigureEmptyAllowFromReloadsAllowAll(t *testing.T) {
	for _, platform := range []string{"discord", "whatsapp"} {
		t.Run(platform, func(t *testing.T) {
			api, path := round2AllowFromAPI(t, platform)
			before, err := config.LoadConfig(path)
			require.NoError(t, err)
			outsider := bus.SenderInfo{Platform: platform, PlatformID: "new-sender", CanonicalID: platform + ":new-sender"}
			control := channels.NewBaseChannel(platform, nil, bus.NewMessageBus(), before.Channels[platform].AllowFrom)
			require.False(t, control.IsAllowedSender(outsider), "instrument: the nonempty saved seed actually rejects this sender")

			response := round2PutAllowFrom(api, platform, `""`)
			require.Equal(t, http.StatusOK, response.Code, "empty text is valid; body=%s", response.Body.String())
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			var root map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(saved, &root))
			var instances map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(root["channels"], &instances))
			require.Contains(t, instances, platform, "instrument: the saved channel entry must not disappear")
			var entry map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(instances[platform], &entry))
			if raw, present := entry["allow_from"]; present {
				require.NotEqual(t, "null", strings.TrimSpace(string(raw)), "empty text must never persist null")
				var ids []string
				require.NoError(t, json.Unmarshal(raw, &ids), "a present saved allow_from must be a JSON array, never scalar text")
				assert.Equal(t, []string{}, ids, "empty text must never save the deny-all single empty ID")
			}
			loaded, err := config.LoadConfig(path)
			require.NoError(t, err)
			instance, exists := loaded.Channels[platform]
			require.True(t, exists)
			adapter := channels.NewBaseChannel(platform, instance, bus.NewMessageBus(), instance.AllowFrom)
			assert.True(t, adapter.IsAllowedSender(outsider), "empty-text Configure must admit an arbitrary new sender after reload")
			assert.True(t, adapter.IsAllowedSender(bus.SenderInfo{Platform: platform, PlatformID: "old-sender", CanonicalID: platform + ":old-sender"}),
				"allow all also retains admission for a previously listed sender")
		})
	}
}
