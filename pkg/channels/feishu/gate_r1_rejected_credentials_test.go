//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package feishu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// fakeFeishuOpenAPI serves the tenant-token and bot-info endpoints. "reject"
// answers the token request with Feishu's documented code 10015 ("wrong app
// secret"); "ok" issues a token and a bot open_id.
func fakeFeishuOpenAPI(t *testing.T, mode string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" && mode == "reject":
			_, _ = w.Write([]byte(`{"code":10015,"msg":"wrong app secret"}`))
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"t-gate","expire":7200}`))
		case r.URL.Path == "/open-apis/bot/v3/info":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","bot":{"open_id":"ou_gate"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func newGateFeishuChannel(t *testing.T, baseURL string) *FeishuChannel {
	t.Helper()
	ch, err := NewFeishuChannel(config.FeishuConfig{AppID: "cli_gate", AppSecretRef: "FEISHU_GATE"},
		credentials.SecretBundle{"FEISHU_GATE": "gate-secret"}, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewFeishuChannel: %v", err)
	}
	ch.client = lark.NewClient("cli_gate", "gate-secret", lark.WithTokenCache(ch.tokenCache), lark.WithOpenBaseUrl(baseURL))
	return ch
}

// TestFeishuStart_RejectedAppSecretFailsTheStart is Train 2 gate r1
// (BDD-8.3 channel survey): Start logged a failed bot-info call ("@mention
// detection may not work") and ran with credentials Feishu had rejected. A
// documented credential rejection from the token endpoint must fail Start.
func TestFeishuStart_RejectedAppSecretFailsTheStart(t *testing.T) {
	ch := newGateFeishuChannel(t, fakeFeishuOpenAPI(t, "reject"))
	if err := ch.Start(context.Background()); err == nil {
		_ = ch.Stop(context.Background())
		t.Fatal("a rejected app secret must fail Start")
	}
	if ch.IsRunning() {
		t.Fatal("a rejected channel must not be running")
	}
}

// TestFeishuCredentialCheck_OnlyRejectionsFailTheCheck pins the other side of
// the rule: accepted credentials and an unreachable Feishu are not rejections
// (Start continues; the websocket keeps retrying as before). The check is
// called directly so the test never opens the real Feishu websocket.
func TestFeishuCredentialCheck_OnlyRejectionsFailTheCheck(t *testing.T) {
	if err := newGateFeishuChannel(t, fakeFeishuOpenAPI(t, "reject")).checkCredentials(context.Background()); err == nil {
		t.Fatal("a documented rejection (10015) must fail the check")
	}
	if err := newGateFeishuChannel(t, fakeFeishuOpenAPI(t, "ok")).checkCredentials(context.Background()); err != nil {
		t.Fatalf("accepted credentials must pass, got %v", err)
	}
	if err := newGateFeishuChannel(t, "http://127.0.0.1:1").checkCredentials(context.Background()); err != nil {
		t.Fatalf("an unreachable Feishu is not a rejection, got %v", err)
	}
}
