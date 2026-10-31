package gateway

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

const t10Bearer = "t10-red-test-fixture-bearer"

// The fallback and middleware are installed ONLY by production prepareListener.
// No copied /api/ handler is installed in this fixture. The embedded HTML is
// explicitly compile scaffolding, not browser/redirect acceptance.
func t10ProductionRouter(t *testing.T) (*restAPI, http.Handler) {
	t.Helper()
	st := t10BootStage(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(t10Bearer), bcrypt.MinCost)
	require.NoError(t, err)
	st.cfg.Gateway.Users = []config.UserConfig{{Username: "t10-user", TokenHash: config.BcryptHash(hash)}}
	api := &restAPI{agentLoop: st.agentLoop, homePath: st.homePath, allowedOrigin: "http://localhost:3000", notifStore: notifications.NewStore(filepath.Join(st.homePath, "notifications"))}
	cs, err := setupCronTool(st.agentLoop, st.msgBus, st.homePath, st.cfg, api.notifStore)
	require.NoError(t, err)
	api.cronService.Store(cs)
	cm, err := channels.NewManager(st.cfg, credentials.SecretBundle{}, st.msgBus, nil)
	require.NoError(t, err)
	hs := health.NewServer("127.0.0.1", 0)
	cm.SetupHTTPServer("127.0.0.1:0", hs)
	st.api = api
	st.runningServices = &services{homePath: st.homePath, ChannelManager: cm, HealthServer: hs, CronService: cs, notifStore: api.notifStore, restAPIRef: api}
	api.registerAdditionalEndpoints(cm)
	_, stop, err := st.prepareListener()
	require.NoError(t, err)
	require.False(t, stop)
	var chain http.Handler
	require.NoError(t, cm.WrapHTTPHandler(func(h http.Handler) http.Handler { chain = h; return h }))
	require.NotNil(t, chain)
	return api, chain
}
func t10Request(chain http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+t10Bearer)
	req.Header.Set("Content-Type", "application/json")
	value := strings.Repeat("a", 64)
	req.AddCookie(&http.Cookie{Name: middleware.CSRFCookieNameHTTP, Value: value})
	req.Header.Set(middleware.CSRFHeaderName, value)
	out := httptest.NewRecorder()
	chain.ServeHTTP(out, req)
	return out
}
func t10AssertGeneric404(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	var actual gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &actual), "not SPA HTML or plain text")
	assert.Equal(t, "endpoint not found", actual.Error)
	var object map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &object))
	assert.Equal(t, map[string]any{"error": "endpoint not found"}, object, "canonical envelope, not a schedules-specific 404")
}

// Traces to: schedules-t10-spec-r6.md lines 823–839, 867–871; TDD 14/15, D-5.1–7/9.
func TestSchedulesRoutes_Removed_AllGeneric404(t *testing.T) {
	_, chain := t10ProductionRouter(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/schedules"}, {"POST", "/api/v1/schedules"}, {"GET", "/api/v1/schedules/t10-missing"},
		{"PUT", "/api/v1/schedules/t10-missing"}, {"DELETE", "/api/v1/schedules/t10-missing"},
		{"POST", "/api/v1/schedules/t10-missing/run"}, {"POST", "/api/v1/schedules/t10-missing/pause"},
		{"GET", "/api/v1/automations"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) { t10AssertGeneric404(t, t10Request(chain, tc.method, tc.path, `{}`)) })
	}
}

// Traces to: schedules-t10-spec-r6.md lines 860–865; TDD 14/15, D-5.8.
// PASSING characterization: generic JSON fallback is already present on this base.
func TestUnknownAPI_JSON404_SPAControl(t *testing.T) {
	_, chain := t10ProductionRouter(t)
	for _, path := range []string{"/api/v1/t10-invented-route", "/api/v99/t10-not-an-entity"} {
		t10AssertGeneric404(t, t10Request(chain, "GET", path, ""))
	}
	w := t10Request(chain, "GET", "/t10-ordinary-spa-route", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, w.Body.String(), `<div id="root">`)
	// Instrument control: the production mutation CSRF middleware really is live.
	req := httptest.NewRequest("POST", "/api/v1/t10-invented-route", nil)
	bad := httptest.NewRecorder()
	chain.ServeHTTP(bad, req)
	assert.Equal(t, http.StatusForbidden, bad.Code)
}

// Traces to: schedules-t10-spec-r6.md lines 848–852; TDD 14, D-5.11.
func TestT10_TimerFailureStillReachesNotificationsREST(t *testing.T) {
	api, chain := t10ProductionRouter(t)
	checker := agentCheckerFunc(func(string) bool { return true })
	runner := newScheduledRunner(api.agentLoop, checker, nil, api.notifStore, api.agentLoop.GetConfig)
	for _, tc := range []struct{ id, diagnostic string }{{"heartbeat-a", "fixture diagnostic A"}, {"heartbeat-b", "fixture diagnostic B"}} {
		runner.onFailure(&cron.CronJob{ID: tc.id, Name: tc.id, AgentID: "mia", CreatedBy: "t10-user", Payload: cron.CronPayload{Kind: "heartbeat"}}, "session-"+tc.id, errors.New(tc.diagnostic))
	}
	w := t10Request(chain, "GET", "/api/v1/notifications", "")
	require.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Notifications []gen.Notification `json:"notifications"`
		Unread        int                `json:"unread_count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Notifications, 2)
	assert.Equal(t, 2, response.Unread)
	bodies := []string{}
	for _, n := range response.Notifications {
		require.NotNil(t, n.Body)
		bodies = append(bodies, *n.Body)
		assert.Equal(t, gen.NotificationSeverity("error"), n.Severity)
	}
	assert.ElementsMatch(t, []string{"fixture diagnostic A", "fixture diagnostic B"}, bodies)
}

// Traces to: schedules-t10-spec-r6.md lines 854–858, 867–871; TDD 16, D-5.12.
func TestT10_NoScheduleWireTypesOrConsumers(t *testing.T) {
	root := filepath.Clean("../..")
	for _, dir := range []string{"contracts/components/schemas", "pkg/api/generated", "src/lib/api/generated"} {
		t.Run(dir, func(t *testing.T) {
			seen := 0
			forbidden := []string{}
			err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.IsDir() {
					return nil
				}
				seen++
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				if strings.HasPrefix(d.Name(), "Schedule") && strings.HasSuffix(d.Name(), ".yaml") {
					forbidden = append(forbidden, path)
				}
				if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
					f, parseErr := parser.ParseFile(token.NewFileSet(), path, data, 0)
					if parseErr != nil {
						return parseErr
					}
					ast.Inspect(f, func(n ast.Node) bool {
						if typ, ok := n.(*ast.TypeSpec); ok && strings.HasPrefix(typ.Name.Name, "Schedule") {
							forbidden = append(forbidden, path+":"+typ.Name.Name)
						}
						return true
					})
				}
				if strings.HasSuffix(path, ".ts") && (strings.Contains(string(data), "export const Schedule") || strings.Contains(string(data), "export type Schedule") || strings.Contains(string(data), "Schedule:")) {
					forbidden = append(forbidden, path)
				}
				return nil
			})
			require.NoError(t, err)
			require.Positive(t, seen, "empty/missing tree cannot be green")
			assert.Empty(t, forbidden, "retired generated Schedule tree remains")
		})
	}
}
