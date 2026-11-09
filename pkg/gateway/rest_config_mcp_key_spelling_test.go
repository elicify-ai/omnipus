// SEC906 fix round (security review 45e960009, F1, F2 and F4): PUT
// /api/v1/config and the MCP header rules under tools.mcp.servers.<name>.
//
// Specification sources, never the implementation's output:
//   - Issue #638 / ARCH-906: a literal MCP header value is a credential and is
//     refused (403, nothing persisted) by the generic config write; only
//     POST/PATCH /api/v1/mcp-servers write header values (encrypted) and refs.
//   - config.json is decoded by encoding/json, which binds a JSON object key to
//     a struct field case-insensitively ("Headers", "HEADERS" and "headers" all
//     fill MCPServerConfig.Headers). A refusal that only knows the lowercase
//     spelling therefore does not cover what the loader actually reads.
//   - ARCH-906 change 2: a PUT that keeps a server keeps that server's stored
//     header refs.
//   - The GET redaction marker "[redacted]" is a read-modify-write round-trip
//     placeholder meaning "unchanged", never an operator-supplied header value.
//
// Derived from the reviewer's reproductions in
// /workspace/logs/team-a-restart-20261107/sec906-review-probes/gateway_proofs_test.go.

package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sec906CaseSecret = "sec906-fix-case-variant-bearer"

func readConfigBytes(t *testing.T, api *restAPI) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	return raw
}

func putConfigBody(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleConfig(w, r)
	return w
}

// F1: every spelling the config loader binds to tools.mcp.servers.<n>.headers
// is refused like the lowercase one, in the nested and in the dot-path shape.
func TestConfigPUT_MCPLiteralHeader_CaseVariantsRefused(t *testing.T) {
	const srv = `{"enabled":false,"type":"sse","url":"https://127.0.0.1:1/sse",`
	const hdr = `{"Authorization":"Bearer ` + sec906CaseSecret + `"}`
	cases := map[string]string{
		"control lowercase":       `{"tools":{"mcp":{"servers":{"s1":` + srv + `"headers":` + hdr + `}}}}}`,
		"Headers":                 `{"tools":{"mcp":{"servers":{"s1":` + srv + `"Headers":` + hdr + `}}}}}`,
		"HEADERS":                 `{"tools":{"mcp":{"servers":{"s1":` + srv + `"HEADERS":` + hdr + `}}}}}`,
		"SERVERS":                 `{"tools":{"mcp":{"SERVERS":{"s1":` + srv + `"headers":` + hdr + `}}}}}`,
		"Mcp":                     `{"tools":{"Mcp":{"servers":{"s1":` + srv + `"headers":` + hdr + `}}}}}`,
		"TOOLS":                   `{"TOOLS":{"mcp":{"servers":{"s1":` + srv + `"headers":` + hdr + `}}}}}`,
		"all upper":               `{"TOOLS":{"MCP":{"SERVERS":{"s1":` + srv + `"HEADERS":` + hdr + `}}}}}`,
		"dot-path HEADERS":        `{"tools.mcp.servers.s1.HEADERS.Authorization":"Bearer ` + sec906CaseSecret + `"}`,
		"dot-path all upper":      `{"TOOLS.MCP.SERVERS.s1.Headers.Authorization":"Bearer ` + sec906CaseSecret + `"}`,
		"partial dot-path Mcp":    `{"tools":{"Mcp.Servers.s1.Headers.Authorization":"Bearer ` + sec906CaseSecret + `"}}`,
		"partial dot-path nested": `{"Tools":{"mcp.servers":{"s1.HEADERS":{"Authorization":"Bearer ` + sec906CaseSecret + `"}}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			before := readConfigBytes(t, api)

			w := putConfigBody(t, api, body)

			assert.Equal(t, http.StatusForbidden, w.Code,
				"#638: a literal MCP header value is refused whatever JSON key spelling carries it; body=%s", w.Body.String())
			after := readConfigBytes(t, api)
			assert.Equal(t, string(before), string(after), "a refused write persists nothing")
			assert.NotContains(t, string(after), sec906CaseSecret, "the bearer must never reach config.json")
			if s, ok := api.agentLoop.GetConfig().Tools.MCP.Servers["s1"]; ok {
				assert.Empty(t, s.Headers["Authorization"], "the bearer must never become an active header")
			}
		})
	}
}

// F1 companion: two spellings of one field in the same object are ambiguous
// (which one the loader keeps depends on key order) and are refused before
// anything is saved.
func TestConfigPUT_MCPSection_AmbiguousKeySpellingsRefused(t *testing.T) {
	cases := map[string]string{
		"servers twice": `{"tools":{"mcp":{"servers":{},"SERVERS":{"s1":{"enabled":false}}}}}`,
		"tools twice":   `{"tools":{"mcp":{"enabled":false}},"Tools":{"mcp":{"enabled":true}}}`,
		"headers twice": `{"tools":{"mcp":{"servers":{"s1":{"enabled":false,"headers":{},"Headers":{"User-Agent":"[redacted]"}}}}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			before := readConfigBytes(t, api)

			w := putConfigBody(t, api, body)

			assert.Equal(t, http.StatusBadRequest, w.Code, "ambiguous key spellings are refused; body=%s", w.Body.String())
			assert.Equal(t, string(before), string(readConfigBytes(t, api)), "a refused write persists nothing")
		})
	}
}

// F2: a retained server keeps its stored header refs whatever spelling of the
// tools / mcp / servers keys the body uses, and config.json is left with the
// canonical keys only.
func TestConfigPUT_CaseVariantSections_PreserveHeaderRefs(t *testing.T) {
	const srv = `{"s1":{"enabled":false,"type":"sse","url":"https://127.0.0.1:1/sse"}}`
	cases := map[string]string{
		"control lowercase": `{"tools":{"mcp":{"enabled":false,"servers":` + srv + `}}}`,
		"Servers":           `{"tools":{"mcp":{"enabled":false,"Servers":` + srv + `}}}`,
		"SERVERS":           `{"tools":{"mcp":{"enabled":false,"SERVERS":` + srv + `}}}`,
		"MCP":               `{"tools":{"MCP":{"enabled":false,"servers":` + srv + `}}}`,
		"TOOLS":             `{"TOOLS":{"mcp":{"enabled":false,"servers":` + srv + `}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			api, _ := newTestRestAPIWithHomeAndCredStore(t)
			before := seedMCPServerWithHeaderRefs(t, api, "s1", map[string]string{
				"Authorization": "Bearer sec906-fix-preserve-secret",
			})
			wantRef, ok := before["Authorization"].(string)
			require.True(t, ok && wantRef != "", "seed must persist an Authorization ref; got %v", before)

			w := putConfigBody(t, api, body)

			require.Equal(t, http.StatusOK, w.Code, "a retained-server update without refs is accepted; body=%s", w.Body.String())
			live, present := api.agentLoop.GetConfig().Tools.MCP.Servers["s1"]
			require.True(t, present, "s1 is retained by the body")
			assert.Equal(t, wantRef, live.HeaderRefs["Authorization"],
				"ARCH-906: a PUT that keeps s1 must keep its stored header ref")
			disk := readPersistedMCPServer(t, api.homePath, "s1")
			require.NotNil(t, disk, "s1 must be saved under the canonical tools.mcp.servers path")
			assert.Equal(t, before, disk["header_refs"], "stored header_refs on disk unchanged")
			onDisk := readConfigOnDisk(t, api)
			for _, k := range []string{"TOOLS", "Tools"} {
				assert.NotContains(t, onDisk, k, "no non-canonical tools key may be saved")
			}
			toolsOnDisk, ok := onDisk["tools"].(map[string]any)
			require.True(t, ok, "tools must be saved as an object")
			for _, k := range []string{"MCP", "Mcp"} {
				assert.NotContains(t, toolsOnDisk, k, "no non-canonical mcp key may be saved")
			}
			mcp, ok := toolsOnDisk["mcp"].(map[string]any)
			require.True(t, ok, "tools.mcp must be saved as an object")
			for _, k := range []string{"Servers", "SERVERS"} {
				assert.NotContains(t, mcp, k, "no non-canonical servers key may be saved")
			}
		})
	}
}

// F4: the GET redaction marker never becomes an active header. With no stored
// value for that header it is dropped; with a stored value (a literal header
// from an older install) the marker means "unchanged" and the stored value is
// kept. The stored refs survive in both cases.
func TestConfigPUT_RedactionPlaceholder_NeverPersistedAsHeader(t *testing.T) {
	for _, spelling := range []string{"headers", "Headers"} {
		t.Run("no stored literal/"+spelling, func(t *testing.T) {
			api, _ := newTestRestAPIWithHomeAndCredStore(t)
			refs := seedMCPServerWithHeaderRefs(t, api, "s1", map[string]string{"Authorization": "Bearer sec906-fix-ref-secret"})
			body := `{"tools":{"mcp":{"servers":{"s1":{"enabled":false,"type":"sse","url":"https://127.0.0.1:1/sse","` +
				spelling + `":{"User-Agent":"[redacted]"}}}}}}`

			w := putConfigBody(t, api, body)

			require.Equal(t, http.StatusOK, w.Code, "a redacted placeholder is a supported round trip; body=%s", w.Body.String())
			disk := readPersistedMCPServer(t, api.homePath, "s1")
			require.NotNil(t, disk)
			assert.NotContains(t, disk, "headers", "the marker is not a header value and must not be saved; entry=%v", disk)
			assert.NotContains(t, disk, "Headers", "the marker is not a header value and must not be saved; entry=%v", disk)
			assert.Equal(t, refs, disk["header_refs"], "stored refs unchanged")
			live := api.agentLoop.GetConfig().Tools.MCP.Servers["s1"]
			assert.NotContains(t, live.Headers, "User-Agent", "the marker must never be an active header")
		})
	}

	t.Run("stored literal kept", func(t *testing.T) {
		api, _ := newTestRestAPIWithHomeAndCredStore(t)
		refs := seedMCPServerWithHeaderRefs(t, api, "s1", map[string]string{"Authorization": "Bearer sec906-fix-ref-secret"})
		// An older install's literal header, written straight to config.json.
		require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
			srv, ok := mcpServersSection(m)["s1"].(map[string]any)
			if !ok {
				return errors.New("seeded server s1 missing")
			}
			srv["headers"] = map[string]any{"User-Agent": "sec906-fix-agent/1"}
			return nil
		}))
		body := `{"tools":{"mcp":{"servers":{"s1":{"enabled":false,"type":"sse","url":"https://127.0.0.1:1/sse",` +
			`"headers":{"User-Agent":"[redacted]"}}}}}}`

		w := putConfigBody(t, api, body)

		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		disk := readPersistedMCPServer(t, api.homePath, "s1")
		require.NotNil(t, disk)
		assert.Equal(t, map[string]any{"User-Agent": "sec906-fix-agent/1"}, disk["headers"],
			"the marker means unchanged: the stored value stays, the marker is not saved")
		assert.Equal(t, refs, disk["header_refs"], "stored refs unchanged")
		live := api.agentLoop.GetConfig().Tools.MCP.Servers["s1"]
		assert.Equal(t, "sec906-fix-agent/1", live.Headers["User-Agent"], "the stored value stays active")
	})
}
