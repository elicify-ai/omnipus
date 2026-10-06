// Issues #276 and #640: POST /reload without a valid admin bearer is 401 and
// does not fire a reload. POST /reload with the same bearer other mutating
// routes accept fires the reload and returns 200. The success body is the one
// #640 records for a triggered reload: {"status":"reload triggered"}.
// GET /health stays open — #640's suggested fix keeps liveness probes
// unauthenticated.

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/health"
)

// reloadAdminBearer is the admin bearer #276 requires. It is not read off a
// response; the hash installed on the account is bcrypt of this exact string.
const reloadAdminBearer = "gwsec-276-admin-bearer"

func newReloadAuthHarness(t *testing.T) (*restAPI, http.Handler, *atomic.Int32) {
	t.Helper()
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	hash, err := bcrypt.GenerateFromPassword([]byte(reloadAdminBearer), bcrypt.MinCost)
	require.NoError(t, err)
	// Installed before the route is mounted, so a fix that snapshots accounts
	// at registration still sees this bearer.
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{{
		Username:  "owner",
		TokenHash: config.BcryptHash(hash),
	}}

	fired := &atomic.Int32{}
	hs := health.NewServer("127.0.0.1", 0)
	hs.SetReloadFunc(func() error {
		fired.Add(1)
		return nil
	})

	cm, err := channels.NewManager(api.agentLoop.GetConfig(), credentials.SecretBundle{}, bus.NewMessageBus(), nil)
	require.NoError(t, err)
	cm.SetupHTTPServer("127.0.0.1:0", hs)
	var mux http.Handler
	require.NoError(t, cm.WrapHTTPHandler(func(inner http.Handler) http.Handler {
		mux = inner
		return inner
	}))
	require.NotNil(t, mux, "production mount did not hand over the mux")
	return api, buildProductionMiddlewareChain(api, mux), fired
}

func callReload(t *testing.T, api *restAPI, h http.Handler, method, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/reload", nil)
	req.RemoteAddr = uniqueTestSourceIP()
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestReload_Unauthenticated_Is401_AndDoesNotFire(t *testing.T) {
	api, h, fired := newReloadAuthHarness(t)

	rec := callReload(t, api, h, http.MethodPost, "")

	require.Equal(t, http.StatusUnauthorized, rec.Code,
		"issue #276: POST /reload without a token is 401; body=%s", rec.Body.String())
	assert.Equal(t, int32(0), fired.Load(),
		"issue #276: a rejected reload must not call reloadFunc")
}

func TestReload_WrongBearer_Is401_AndDoesNotFire(t *testing.T) {
	api, h, fired := newReloadAuthHarness(t)

	rec := callReload(t, api, h, http.MethodPost, "Bearer not-the-admin-token")

	require.Equal(t, http.StatusUnauthorized, rec.Code,
		"issue #276: POST /reload with the wrong token is 401; body=%s", rec.Body.String())
	assert.Equal(t, int32(0), fired.Load(),
		"issue #276: a rejected reload must not call reloadFunc")
}

func TestReload_ValidBearer_FiresAndReturns200(t *testing.T) {
	api, h, fired := newReloadAuthHarness(t)

	rec := callReload(t, api, h, http.MethodPost, "Bearer "+reloadAdminBearer)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "reload triggered",
		"issue #640: a valid admin bearer returns the triggered-reload status")
	assert.Equal(t, int32(1), fired.Load(),
		"issue #276: a valid admin bearer fires the reload exactly once")
}

func TestReload_HealthStaysOpenWithoutAuth(t *testing.T) {
	api, h, fired := newReloadAuthHarness(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = uniqueTestSourceIP()
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))

	require.Equal(t, http.StatusOK, rec.Code,
		"issue #640: /health stays open; body=%s", rec.Body.String())
	assert.Equal(t, int32(0), fired.Load(), "a liveness probe must not reload")
}
