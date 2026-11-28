// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED test — Skills & Tools redesign (#1963, epic #1519), C-22 row 2, S-44.
// PUT /api/v1/performance refuses a body that names the retired switch
// tools_on_demand — in any letter case, alone or beside a valid setting — with
// the answer PUT /api/v1/config gives for the retired tool-loading keys, and
// stores nothing.
//
// Oracles:
//   - skills-tools-redesign-spec.md C-22 row 2 (FR-039, ST-Q4 B, BDD S-44):
//     `tools_on_demand` leaves PerformanceSettings and PerformanceSettingsUpdate;
//     "A request still carrying `tools_on_demand` gets the schema's 400
//     (`additionalProperties: false`). Saving the other Limits values keeps
//     working." So the status is 400.
//   - C-22 row 3, pinned for PUT /config by N83
//     (retired_tool_loading_put_config_red_test.go): the refusal text,
//     rtlRefusalText, character for character.
//   - Team-lead's ruling of 2026-11-28 (a teammate's ruling, not the founder's):
//     PUT /performance must refuse a body that names tools_on_demand,
//     case-insensitive, even when another valid setting is in the same body,
//     with "the same status, code and message PUT /config gives for the retired
//     tool-loading keys". The spec fixes the status (400) and N83 fixes the
//     text. The spec names no code, and N83 accepts any 4xx, so the status and
//     the code are also compared with the ones PUT /config's refusal carries,
//     read in the same test (today that is 400 and no code, so PUT /performance's
//     must be 400 with no code either).
//
// By name only, as N83 reads C-22 row 3 for PUT /config: a body that names the
// key is refused whatever its value (null included) and however the name is
// cased or spelled. encoding/json matches a member name to a field
// case-insensitively and after unescaping it, so TOOLS_ON_DEMAND and a name
// spelled with a JSON escape for the underscore are the same name to the
// decoder that used to read this switch.
//
// Mode: the rows run with inbound validation as the unit-test harness has it,
// off (each row checks that first and fails loudly if it ever changes). With it
// on, the schema's own 400 answers before the handler runs; that validator's
// text is not the spec's, so it is not pinned here.
//
// Expected RED at the backend's round 18 (2f632361c): the generated update type
// no longer has the field, so the decoder drops it. With the switch beside a
// valid setting the handler answers 200 and stores the valid setting; with the
// switch alone it answers 400 only because no recognised setting is left ("at
// least one performance setting is required"), which is not the refusal's text.
//
// Not covered: what the upgrade does with a stored tools.manifest.compressed
// (the founder's rule: no upgrade tests until the Chief Architect rules); the
// validation-on text; whether a refusal leaves the re-auth token unspent.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// todAnswer is the part of an error answer that the ruling compares between the
// two endpoints: its status, its machine-readable code (nil when the answer
// carries none) and its message.
type todAnswer struct {
	status int
	code   any
	msg    string
}

// todReadAnswer decodes an error answer; who names the endpoint in failure
// messages.
func todReadAnswer(t *testing.T, w *httptest.ResponseRecorder, who string) todAnswer {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body),
		"%s: the answer must be a JSON object, got: %s", who, rtlShort(w.Body.String()))
	msg, _ := body["error"].(string)
	return todAnswer{status: w.Code, code: body["code"], msg: msg}
}

// todPut sends body to PUT /api/v1/performance through the real handler with a
// re-auth consent token, the way rest_performance_test.go does.
func todPut(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	return w
}

// todPutConfigAnswer is PUT /api/v1/config's answer to a body that names a
// retired tool-loading key: the answer the ruling names as the oracle. It is
// read on its own API instance, after N83's own anchors (a client error that
// carries C-22 row 3's text), so the comparison below rests on the spec and
// not only on what the endpoint happens to say.
func todPutConfigAnswer(t *testing.T) todAnswer {
	t.Helper()
	w := rtlPut(newTestRestAPIWithHome(t), `{"tools":{"manifest":{"compressed":false}}}`)
	require.True(t, w.Code >= http.StatusBadRequest && w.Code < http.StatusInternalServerError,
		"instrument: PUT /config must refuse a retired key with a client error (N83's rows) before it can serve as the oracle, got %d: %s",
		w.Code, rtlShort(w.Body.String()))
	answer := todReadAnswer(t, w, "PUT /config")
	require.Contains(t, answer.msg, rtlRefusalText,
		"instrument: PUT /config's refusal must carry C-22 row 3's text (N83's rows) before it can serve as the oracle")
	return answer
}

// TestPerformancePut_RefusesToolsOnDemandByNameLikePutConfig — a PUT body that
// names tools_on_demand is refused with the 400, carries C-22 row 3's text and
// the code PUT /config's refusal carries, and stores nothing: not the switch,
// not the valid setting beside it.
// Traces to: C-22 row 2, FR-039, ST-Q4 B, BDD S-44, the 2026-11-28 ruling.
func TestPerformancePut_RefusesToolsOnDemandByNameLikePutConfig(t *testing.T) {
	want := todPutConfigAnswer(t)

	// The escaped name is built at run time (a backslash, then u005f) so that no
	// editing tool can turn the escape into a plain underscore in this source.
	escaped := "tools" + string(rune(0x5c)) + "u005fon_demand"

	for _, tc := range []struct{ name, body string }{
		{"the switch alone, turned off", `{"tools_on_demand":false}`},
		{"the switch last, beside a valid setting", `{"max_parallel_agents":6,"tools_on_demand":true}`},
		{"the switch first, beside a valid setting", `{"tools_on_demand":true,"max_parallel_agents":6}`},
		{"the name in mixed case, beside a valid setting", `{"max_parallel_agents":6,"Tools_On_Demand":false}`},
		{"the name in upper case, alone", `{"TOOLS_ON_DEMAND":false}`},
		{"the name spelled with a JSON escape for the underscore, beside a valid setting",
			`{"max_parallel_agents":6,"` + escaped + `":false}`},
		{"the name with a null value, beside a valid setting", `{"max_parallel_agents":6,"tools_on_demand":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			require.False(t, api.agentLoop.GetConfig().Gateway.ValidateInbound,
				"instrument: these rows run with inbound validation off, the mode in which the handler itself must refuse; with it on the schema's own 400 answers first")
			storedBefore := readConfigOnDisk(t, api)
			parallelBefore := api.agentLoop.GetConfig().Performance.MaxParallelAgents

			w := todPut(t, api, tc.body)
			require.Equal(t, http.StatusBadRequest, w.Code,
				"C-22 row 2: a body naming tools_on_demand must get the 400, got %d: %s", w.Code, rtlShort(w.Body.String()))
			got := todReadAnswer(t, w, "PUT /performance")
			require.Equal(t, want.status, got.status,
				"the 2026-11-28 ruling: the refusal carries the same status as PUT /config's, which is %d", want.status)
			require.Contains(t, got.msg, rtlRefusalText,
				"the 2026-11-28 ruling: the refusal carries the text PUT /config gives (C-22 row 3), got: %s", got.msg)
			require.Equal(t, want.code, got.code,
				"the 2026-11-28 ruling: the refusal carries the same code as PUT /config's, which is %v", want.code)

			require.Equal(t, storedBefore, readConfigOnDisk(t, api),
				"a refused request stores nothing: config.json must be unchanged, the valid setting beside the switch included")
			require.Equal(t, parallelBefore, api.agentLoop.GetConfig().Performance.MaxParallelAgents,
				"a refused request changes nothing in the live settings either")
		})
	}

	// Control (S-44: saving the other Limits values keeps working): a valid
	// setting alone is accepted and lands, so the refusals above are the
	// switch's and not a blanket refusal of Limits writes, and the harness can
	// see a stored write (the "unchanged" checks above could have failed).
	t.Run("control: a valid setting alone is accepted and lands", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		storedBefore := readConfigOnDisk(t, api)

		w := todPut(t, api, `{"max_parallel_agents":6}`)
		require.Equal(t, http.StatusOK, w.Code,
			"S-44: saving a Limits value keeps working, got %d: %s", w.Code, rtlShort(w.Body.String()))
		require.Equal(t, 6, api.agentLoop.GetConfig().Performance.MaxParallelAgents,
			"the accepted write must be live")
		require.NotEqual(t, storedBefore, readConfigOnDisk(t, api),
			"the accepted write must reach config.json")
	})
}
