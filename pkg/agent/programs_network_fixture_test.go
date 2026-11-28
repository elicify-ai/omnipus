package agent

// Spec rev8 US-3 / FR-015–018. Everything from the trusted web input through
// actual child execution and shared-proxy grants is real. Only the external
// executable and the person's approval decision are process-edge fixtures.
import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

type programsNetworkApprover struct {
	mu        sync.Mutex
	requests  []PolicyApprovalReq
	holdFirst bool
	entered   chan PolicyApprovalReq
	decision  chan bool
}

func (a *programsNetworkApprover) RequestApproval(ctx context.Context, req PolicyApprovalReq) (bool, string, bool) {
	a.mu.Lock()
	a.requests = append(a.requests, req)
	first := len(a.requests) == 1
	a.mu.Unlock()
	if a.holdFirst && first {
		select {
		case a.entered <- req:
		case <-ctx.Done():
			return false, "cancel", false
		}
		select {
		case approved := <-a.decision:
			return approved, "", false
		case <-ctx.Done():
			return false, "cancel", false
		}
	}
	return true, "", false // A person's allow-once decision; no session grant.
}

func (a *programsNetworkApprover) snapshot() []PolicyApprovalReq {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]PolicyApprovalReq(nil), a.requests...)
}

type programsNetworkFixture struct {
	f              *wu5Fixture
	sid, work      string
	child, release string
	proxy          *sandbox.EgressProxy
	audit          chan *audit.Entry
	approver       *programsNetworkApprover
}

func newProgramsNetworkFixture(t *testing.T, god, auto bool, deny bool) *programsNetworkFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("BLOCKED: native Windows network executable fixture is not implemented; POSIX process-edge scripts do not certify Windows S21/S22/S23/S24/S28/S89")
	}
	if god && !sandbox.GodModeAvailable {
		t.Fatal("BLOCKED: this build has no God Mode support — required by S89's actual God Mode branch")
	}
	previousAvailability := godModeAvailable.Load()
	SetGodModeAvailable(god)
	t.Cleanup(func() { setGodModeAvailable(previousAvailability) })
	f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies["bash"] = "ask"
		cfg.Sandbox.AutoApprove = auto
		cfg.Sandbox.GodMode = god
		if deny {
			cfg.Sandbox.CommandRules = []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: "gh"}}
		}
	})
	x := &programsNetworkFixture{f: f, sid: f.mainSession("sales", "dev"), audit: make(chan *audit.Entry, 64)}
	x.work = filepath.Join(f.home, "workspaces", "sales", "work")
	require.NoError(t, os.MkdirAll(x.work, 0700))
	x.child, x.release = filepath.Join(x.work, "network-child-proxy"), filepath.Join(x.work, "network-release")
	x.approver = &programsNetworkApprover{entered: make(chan PolicyApprovalReq, 1), decision: make(chan bool, 1)}
	f.al.SetToolApprover(x.approver)
	var err error
	x.proxy, err = sandbox.NewEgressProxy(nil, func(entry *audit.Entry) { x.audit <- entry })
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, x.proxy.Close()) })
	f.al.WireTier13Deps(Tier13Deps{EgressProxy: x.proxy})
	require.Equal(t, god, GodModeActive(f.al.GetConfig()), "Given: God Mode needs both the config switch and the real boot/build availability gate")
	require.Equal(t, god, shellGodModeForAgentNoRewire(t, f.al, "dev"), "Given: the actual loop-wired child tool has the requested mode")
	wantMode := tools.ShellModeAsk
	if auto {
		wantMode = tools.ShellModeAuto
	}
	if god {
		wantMode = tools.ShellModeGod
	}
	require.Equal(t, wantMode, f.al.shellGate.ResolveShellMode(context.Background(), "dev", x.sid))
	// This child validates its own actual token before publishing its proxy URL.
	// It does not create or register a proxy grant. The already-completed real
	// account check remains the fixture's connected-state precondition.
	bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
	body := fmt.Sprintf(`#!/bin/sh
case "$1:$2" in api:user|s3:ls) ;; *) exit 81;; esac
if [ "${S1_EXPECT_NO_TOKEN:-}" = yes ]; then
 [ -z "${GH_TOKEN:-}" ] || exit 82
else
 [ "$GH_TOKEN" = tok_valid_12345 ] || exit 83
fi
printf '%%s' "${HTTPS_PROXY:-}" > %q
while [ ! -f %q ]; do sleep 0.02; done
printf 'octocat\n'
`, x.child, x.release)
	for _, name := range []string{"gh", "aws"} {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = https://example.org ] || exit 84\nprintf 'S22_CURL_RAN'\n"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(x.work, "deploy.sh"), []byte("#!/bin/sh\ngh api user --jq .login\n"), 0700))
	t.Cleanup(func() { require.NoError(t, os.WriteFile(x.release, []byte("release"), 0600)) })
	// Test the observation itself: a canceled request never dials publicly, but
	// the real proxy still either denies the host or reaches its upstream-error
	// branch. The audit event proves which branch produced the HTTP status.
	x.probe(t, "observer-instrument", "api.github.com", false)
	x.proxy.GrantRunHosts("observer-instrument", []string{"api.github.com"})
	x.probe(t, "observer-instrument", "api.github.com", true)
	x.proxy.RevokeRunToken("observer-instrument")
	x.probe(t, "observer-instrument", "api.github.com", false)
	return x
}

func (x *programsNetworkFixture) probe(t *testing.T, token, host string, allowed bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // An allowed host must be observable without any public connection.
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/s1-grant-observer", nil).WithContext(ctx)
	if token != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(token+":")))
	}
	response := httptest.NewRecorder()
	x.proxy.ServeHTTP(response, req)
	wantStatus, wantEvent := http.StatusForbidden, "egress_denied"
	if allowed {
		wantStatus, wantEvent = http.StatusBadGateway, "egress_upstream_error"
	}
	require.Equal(t, wantStatus, response.Code, "actual proxy grant for %s; allowed means authorized before the canceled upstream boundary, not a delivered network request", host)
	select {
	case entry := <-x.audit:
		require.Equal(t, wantEvent, entry.Event, "instrument: distinguish authorization from upstream failure")
		require.Equal(t, host, entry.Details["host"])
	default:
		t.Fatal("actual proxy emitted no policy/upstream audit event; observer cannot establish the grant")
	}
}

func (x *programsNetworkFixture) start(t *testing.T, command string) func() string {
	t.Helper()
	x.f.provider.setScript(x.sid, []providers.LLMResponse{wu5BashCall("network-call", command), wu5Stop("network command completed")})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	input := programsWebInput()
	input.SessionID, input.ChatID, input.Content = x.sid, x.sid, "run the controlled program command"
	go func() {
		_, _, err := x.f.al.processMessage(ctx, input)
		done <- err
	}()
	var once sync.Once
	join := func() string {
		once.Do(func() {
			require.NoError(t, os.WriteFile(x.release, []byte("release"), 0600))
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(12 * time.Second):
				t.Fatal("actual network-fixture turn did not return after child release")
			}
		})
		return x.f.provider.resultForCall(x.sid, "network-call")
	}
	t.Cleanup(func() {
		_ = os.WriteFile(x.release, []byte("release"), 0600)
		cancel()
		join()
	})
	return join
}

func (x *programsNetworkFixture) childToken(t *testing.T, wantToken bool) string {
	t.Helper()
	var body []byte
	require.Eventually(t, func() bool {
		var err error
		body, err = os.ReadFile(x.child)
		return err == nil && len(body) > 0
	}, 6*time.Second, 10*time.Millisecond, "actual child must verify its environment and record its own proxy URL")
	u, err := url.Parse(string(body))
	require.NoError(t, err)
	require.Equal(t, "http", u.Scheme)
	require.Equal(t, x.proxy.Addr(), u.Host, "the real wired proxy, not an ambient host")
	token := ""
	if u.User != nil {
		token = u.User.Username()
	}
	if wantToken {
		require.NotEmpty(t, token, "actual child's proxy must carry its own run grant")
	} else {
		require.Empty(t, token, "unmatched program must not receive an automatically granted proxy token")
	}
	return token
}
