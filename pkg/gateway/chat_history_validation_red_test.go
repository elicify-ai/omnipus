package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func pagingRefusal(t *testing.T, w *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	require.Equal(t, status, w.Code, "D11 refusal status, not a newest-page fallback: %s", w.Body.String())
	var e generated.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e), "refusal is generated ErrorResponse")
	require.Equal(t, message, e.Error, "D11 exact fixed error")
}

// T11, D-V1/D-V1b/D-V1c/D-B4: RawQuery preserves duplicate/malformed
// pairs; no url.Values.Encode normalization hides the parser's inputs.
func TestGetSessionHistory_ValidationAndAccess(t *testing.T) {
	for _, validate := range []bool{false, true} {
		t.Run(fmt.Sprintf("validate_%t", validate), func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			api.agentLoop.GetConfig().Gateway.ValidateInbound = validate
			sid := createTestSession(t, api)
			pagingSeed(t, api.agentLoop.GetSessionStore(), sid, "a", 101)
			badLimit := []string{
				"limit=19", "limit=101", "limit=0", "limit=-1", "limit=1.5", "limit=%2B20", "limit=20e0",
				"limit=%2020", "limit=20%20", "limit=", "limit", "limit=null", "limit=NaN", "limit=Infinity",
				"limit=99999999999999999999999999999999999999999", "limit=20&limit=40", "limit=20&limit=20",
				"limit=%ZZ", "%6cimit=%ZZ", "limit=20&limit=%ZZ", "limit=%00", "limit=%FF",
			}
			for _, q := range badLimit {
				t.Run(q, func(t *testing.T) {
					pagingRefusal(t, pagingRequest(api, sid, q), 400, "limit must be an integer from 20 to 100")
				})
			}
			for _, q := range []string{"min_rows=0", "min_rows=-1", "min_rows=41", "min_rows=1.5", "min_rows=%2B20", "min_rows=20e0", "min_rows=", "min_rows=null", "min_rows=1&min_rows=2", "min_rows=%ZZ", "min_rows=%2020", "min_rows=999999999999999999999999999999"} {
				t.Run(q, func(t *testing.T) {
					pagingRefusal(t, pagingRequest(api, sid, q), 400, "min_rows must be an integer from 1 to limit")
				})
			}
			t.Run("limit_precedes_floor", func(t *testing.T) {
				pagingRefusal(t, pagingRequest(api, sid, "limit=19&min_rows=0"), 400, "limit must be an integer from 20 to 100")
			})
			validToken := "hp1_" + strings.Repeat("A", 43)
			for _, key := range []string{"before", "after", "window_start", "window_end"} {
				for _, value := range []string{"", "null", "hp2_" + strings.Repeat("A", 43), "hp1_" + strings.Repeat("A", 42), "hp1_" + strings.Repeat("A", 44), "hp1_" + strings.Repeat("!", 43), "%ZZ", "%00", "%20" + validToken} {
					q := key + "=" + value
					t.Run(q, func(t *testing.T) { pagingRefusal(t, pagingRequest(api, sid, q), 400, "invalid history position") })
				}
				q := key + "=" + validToken + "&" + key + "=" + validToken
				t.Run("duplicate_"+key, func(t *testing.T) { pagingRefusal(t, pagingRequest(api, sid, q), 400, "invalid history position") })
			}
			for _, q := range []string{"before=" + validToken + "&after=" + validToken, "window_start=" + validToken, "window_end=" + validToken, "window_start=" + validToken + "&window_end=" + validToken + "&after=" + validToken} {
				t.Run("range_"+q, func(t *testing.T) { pagingRefusal(t, pagingRequest(api, sid, q), 400, "invalid history page range") })
			}
			for _, tc := range []struct {
				query        string
				limit, floor int
			}{
				{"limit=20", 20, 20}, {"limit=21", 21, 20}, {"limit=99", 99, 20}, {"limit=100", 100, 20}, {"limit=020", 20, 20},
				{"limit=40&min_rows=1", 40, 1}, {"limit=40&min_rows=2", 40, 2}, {"limit=40&min_rows=19", 40, 19}, {"limit=40&min_rows=21", 40, 21},
				{"limit=40&min_rows=39", 40, 39}, {"limit=40&min_rows=40", 40, 40}, {"limit=100&min_rows=100", 100, 100},
			} {
				t.Run("valid_"+tc.query, func(t *testing.T) {
					p := pagingRead(t, api, sid, tc.query)
					require.Equal(t, tc.limit, p.PageInfo.Limit)
					require.Equal(t, tc.floor, p.PageInfo.MinRows)
					require.Equal(t, pagingWantIDs("a", 102-tc.limit, 101), pagingIDs(p), "valid decimal inputs preserve their exact requested newest range")
					pagingShape(t, p, true, false)
				})
			}
			for _, missing := range []string{"missing-child", "main-session-missing-workspace+mia"} {
				for _, q := range []string{"limit=19&before=%ZZ", "after=" + validToken, "window_start=" + validToken + "&window_end=" + validToken} {
					t.Run("access_"+missing+q, func(t *testing.T) { pagingRefusal(t, pagingRequest(api, missing, q), 404, "session not found") })
				}
			}
		})
	}
}

func TestGetSessionHistory_AuthWrapperWinsBeforeAnyPagingError(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "paging-auth-secret")
	for _, tc := range []struct{ header, message string }{
		{"", "unauthorized: missing Bearer token"}, {"Bearer wrong", "unauthorized: invalid Bearer token"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages?limit=19&before=%ZZ", nil)
		r.Header.Set("Authorization", tc.header)
		w := httptest.NewRecorder()
		api.withAuth(api.HandleSessions)(w, r)
		pagingRefusal(t, w, 401, tc.message)
	}
	// Positive control proves this wrapper can reach the same real handler.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages?limit=19", nil)
	r.Header.Set("Authorization", "Bearer paging-auth-secret")
	w := httptest.NewRecorder()
	api.withAuth(api.HandleSessions)(w, r)
	pagingRefusal(t, w, 400, "limit must be an integer from 20 to 100")
}

// Use a real authenticated socket/read loop, not direct schema validation or a
// fake attach handler. Ping is a deterministic serialized-dispatch fence. The
// read deadline only detects a broken instrument/deadlock; it is not the oracle.
func TestGetSessionHistory_WSValidationAndAccess(t *testing.T) {
	for _, validate := range []bool{false, true} {
		t.Run(fmt.Sprintf("validate_%t", validate), func(t *testing.T) {
			h, _, al := newTestWSHandler(t)
			t.Cleanup(h.Wait)
			al.GetConfig().Gateway.ValidateInbound = validate
			meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
			require.NoError(t, err)
			pagingSeed(t, al.GetSessionStore(), meta.ID, "short", 1)
			srv := httptest.NewServer(h)
			t.Cleanup(srv.Close)
			cases := []struct{ name, sid, fields, message string }{
				{"limit19", meta.ID, `,"history_limit":19`, "history_limit must be an integer from 20 to 100"},
				{"limit101", meta.ID, `,"history_limit":101`, "history_limit must be an integer from 20 to 100"},
				{"limitNull", meta.ID, `,"history_limit":null`, "history_limit must be an integer from 20 to 100"},
				{"limitFraction", meta.ID, `,"history_limit":20.5`, "history_limit must be an integer from 20 to 100"},
				{"limitString", meta.ID, `,"history_limit":"20"`, "history_limit must be an integer from 20 to 100"},
				{"limitOverflow", meta.ID, `,"history_limit":999999999999999999999999999999`, "history_limit must be an integer from 20 to 100"},
				{"floor0", meta.ID, `,"history_limit":20,"history_min_rows":0`, "history_min_rows must be an integer from 1 to history_limit"},
				{"floor21", meta.ID, `,"history_limit":20,"history_min_rows":21`, "history_min_rows must be an integer from 1 to history_limit"},
				{"floorNull", meta.ID, `,"history_min_rows":null`, "history_min_rows must be an integer from 1 to history_limit"},
				{"floorFraction", meta.ID, `,"history_min_rows":1.5`, "history_min_rows must be an integer from 1 to history_limit"},
				{"limitBeforeFloor", meta.ID, `,"history_limit":19,"history_min_rows":0`, "history_limit must be an integer from 20 to 100"},
				{"hiddenBeforeLimit", "main-session-missing-workspace+mia", `,"history_limit":19`, "session not found"},
				{"missingBeforeLimit", "missing-child", `,"history_limit":19`, "session not found"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					conn := dialTestWS(t, srv)
					defer func() { require.NoError(t, conn.Close()) }()
					sendWSAuthFrameDevMode(t, conn)
					raw := []byte(`{"type":"attach_session","session_id":"` + tc.sid + `"` + tc.fields + `}`)
					require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
					require.NoError(t, conn.WriteJSON(generated.PingFrame{Type: "ping"}))
					require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
					var errors []generated.ErrorFrame
					var types []string
					for {
						_, b, err := conn.ReadMessage()
						require.NoError(t, err, "serialized pong fence after rejected attach")
						var f generated.ErrorFrame
						require.NoError(t, json.Unmarshal(b, &f))
						if f.Type == "pong" {
							break
						}
						types = append(types, f.Type)
						if f.Type == "error" {
							errors = append(errors, f)
						}
					}
					require.NotContains(t, types, "session_snapshot", "bad params/access refuse before wipe")
					require.NotContains(t, types, "catch_up_complete", "rejection cannot complete snapshot or live_only")
					require.Len(t, errors, 1, "one exact session-scoped D11 refusal")
					require.Equal(t, tc.message, errors[0].Message)
				})
			}
		})
	}
}
