// Issues #276/#640 gate handoff (pr-test-analyzer F2) — pins the two
// untested decision paths of pkg/channels/manager.go::reloadBearerAuthorizer:
//
//   - the CLI-token branch: the machine-only Gateway.CLIToken bearer authorizes
//     /reload (with no request-context config snapshot, i.e. via the
//     construction-config fallback)
//   - the context-first config read: the request-context config snapshot
//     (ctxkey.ConfigContextKey, the one configSnapshotMiddleware injects in
//     production) is authoritative when present — a credential minted on it
//     authorizes even when the construction-time config knows nothing, and a
//     stale construction-time credential does NOT authorize past a newer
//     snapshot that lacks it
//
// The harness mounts the real channels-manager mux (the same production
// mount reload_auth_test.go uses) and injects the chosen snapshot with the
// same mechanism the production middleware uses.
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

const gwsecCLIBearer = "gwsec-640-cli-machine-token"

// newCLITokenReloadHarness mounts the real channels-manager HTTP mux with the
// reload hook wired, built over constructCfg (the Manager's construction-time
// config). It returns the bare mux and the reload-fired counter.
func newCLITokenReloadHarness(t *testing.T, constructCfg *config.Config) (http.Handler, *atomic.Int32) {
	t.Helper()
	fired := &atomic.Int32{}
	hs := health.NewServer("127.0.0.1", 0)
	hs.SetReloadFunc(func() error {
		fired.Add(1)
		return nil
	})
	cm, err := channels.NewManager(constructCfg, credentials.SecretBundle{}, bus.NewMessageBus(), nil)
	require.NoError(t, err)
	cm.SetupHTTPServer("127.0.0.1:0", hs)
	var mux http.Handler
	require.NoError(t, cm.WrapHTTPHandler(func(inner http.Handler) http.Handler {
		mux = inner
		return inner
	}))
	require.NotNil(t, mux, "production mount did not hand over the mux")
	return mux, fired
}

// reloadWithSnapshot posts /reload through the mux. When snapshotCfg is nil
// the request carries NO config snapshot (exercising the construction-config
// fallback); otherwise the snapshot is injected exactly the way
// configSnapshotMiddleware does in production.
func reloadWithSnapshot(t *testing.T, mux http.Handler, snapshotCfg *config.Config, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/reload", nil)
	req.RemoteAddr = uniqueTestSourceIP()
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if snapshotCfg != nil {
		ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, snapshotCfg)
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestReload_CLITokenBearer_FiresAndReturns200 pins the CLI-token branch via
// the construction-config fallback path.
func TestReload_CLITokenBearer_FiresAndReturns200(t *testing.T) {
	constructCfg := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0}}
	hash, err := bcrypt.GenerateFromPassword([]byte(gwsecCLIBearer), bcrypt.MinCost)
	require.NoError(t, err)
	constructCfg.Gateway.CLIToken = &config.TokenEntry{Hash: config.BcryptHash(hash)}
	// No request-context snapshot: the authorizer must fall back to the
	// construction-time config, where the CLI token lives.
	mux, fired := newCLITokenReloadHarness(t, constructCfg)

	rec := reloadWithSnapshot(t, mux, nil, "Bearer "+gwsecCLIBearer)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "reload triggered")
	assert.Equal(t, int32(1), fired.Load(), "the CLI token must fire the reload exactly once")
}

// TestReload_WrongCLIBearer_Is401_AndDoesNotFire is the negative twin: a
// non-matching bearer is rejected even when a CLI token exists.
func TestReload_WrongCLIBearer_Is401_AndDoesNotFire(t *testing.T) {
	constructCfg := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0}}
	hash, err := bcrypt.GenerateFromPassword([]byte(gwsecCLIBearer), bcrypt.MinCost)
	require.NoError(t, err)
	constructCfg.Gateway.CLIToken = &config.TokenEntry{Hash: config.BcryptHash(hash)}
	mux, fired := newCLITokenReloadHarness(t, constructCfg)

	rec := reloadWithSnapshot(t, mux, nil, "Bearer not-"+gwsecCLIBearer)

	require.Equal(t, http.StatusUnauthorized, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, int32(0), fired.Load(), "a rejected reload must not fire")
}

// TestReload_ContextConfig_IsAuthoritative pins the context-first read: the
// snapshot's freshly minted user bearer authorizes even though the
// construction-time config knows no credentials at all.
func TestReload_ContextConfig_IsAuthoritative(t *testing.T) {
	// Construction-time config: NO users, NO CLI token.
	constructCfg := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0}}
	mux, fired := newCLITokenReloadHarness(t, constructCfg)

	const freshBearer = "gwsec-640-context-minted-bearer"
	snapHash, err := bcrypt.GenerateFromPassword([]byte(freshBearer), bcrypt.MinCost)
	require.NoError(t, err)
	snapshotCfg := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0}}
	snapshotCfg.Gateway.Users = []config.UserConfig{{Username: "owner", TokenHash: config.BcryptHash(snapHash)}}

	rec := reloadWithSnapshot(t, mux, snapshotCfg, "Bearer "+freshBearer)

	require.Equal(t, http.StatusOK, rec.Code,
		"the context snapshot's freshly minted bearer must authorize /reload; body=%s", rec.Body.String())
	assert.Equal(t, int32(1), fired.Load())
}

// TestReload_StaleConstructionCredential_DoesNotAuthorizePastNewerSnapshot
// pins the security-relevant direction of the context-first read: a bearer
// that only the stale construction-time config knows does NOT authorize once
// a newer snapshot is present that lacks it.
func TestReload_StaleConstructionCredential_DoesNotAuthorizePastNewerSnapshot(t *testing.T) {
	const staleBearer = "gwsec-640-stale-bearer"
	constructCfg := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0}}
	staleHash, err := bcrypt.GenerateFromPassword([]byte(staleBearer), bcrypt.MinCost)
	require.NoError(t, err)
	constructCfg.Gateway.Users = []config.UserConfig{{Username: "owner", TokenHash: config.BcryptHash(staleHash)}}
	mux, fired := newCLITokenReloadHarness(t, constructCfg)

	// Newer snapshot: the user account exists but the stale token was revoked
	// (no tokens at all).
	snapshotCfg := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0}}
	snapshotCfg.Gateway.Users = []config.UserConfig{{Username: "owner"}}

	rec := reloadWithSnapshot(t, mux, snapshotCfg, "Bearer "+staleBearer)

	require.Equal(t, http.StatusUnauthorized, rec.Code,
		"a stale construction-time bearer must not authorize past a newer snapshot; body=%s", rec.Body.String())
	assert.Equal(t, int32(0), fired.Load())
}
