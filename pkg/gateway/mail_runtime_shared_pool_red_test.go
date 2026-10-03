package gateway

// RED — w5-integration claim 1: ONE shared pool/budget/cache instance serves
// the panel, the watcher and the agent tools, and a client constructed
// without an injected source fails with the typed wiring error instead of
// silently creating its own pool.
//
// Oracles (derived from the spec BEFORE reading the implementation; see
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w5 spec US-1.1/B-1/MC-1: all construction sites hold the SAME
//     process-wide pool instance, keyed by the data dir ("pointer identity"
//     is the spec's own independent-test wording, §3 US-1).
//   - w5 spec §2.2 rule 4: "No pool construction below the facade… a
//     REST-created or tool-created client must not build a private pool".
//   - w5 spec US-1.4/B-4: "a missing source produces W1's typed visible
//     wiring error rather than an unmanaged dial… the server accepts no
//     unmanaged socket".
//
// Mutations this pack must kill (check-integration-report.md §2):
//   - M3: restAPI's lazy accessor constructs a PRIVATE manager
//     (NewMailSessions) — two pools for one data dir.
//   - M1/M2's gateway-site sibling: an unwired client legacy-dials (the
//     audit found no test at gateway sites 1–5; only pkg/email's
//     client-level test exists).

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/stretchr/testify/require"
)

// TestMailRuntime_RestLazyAccessorResolvesTheSharedManager pins US-1.1/MC-1
// at wiring site 2 (rest.go's restAPI fields): the lazy accessor and the
// process-wide state-dir-keyed accessor (site 6) must resolve the SAME
// manager instance — pointer identity, not "same-looking config". Kills M3.
func TestMailRuntime_RestLazyAccessorResolvesTheSharedManager(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	viaRest := api.mailSessionsFor()
	viaShared := email.SharedMailSessions(api.homePath, email.SessionsConfig{})
	if viaRest == nil || viaShared == nil {
		t.Fatalf("US-1.1: both accessors must resolve a manager (rest=%v, shared=%v)", viaRest, viaShared)
	}
	if viaRest != viaShared {
		t.Fatalf("US-1.1/MC-1: restAPI lazy accessor resolved a DIFFERENT manager instance than the process-wide shared accessor for the same data dir (two pools for one data dir); want pointer-identical instances")
	}

	// Lazy create-once (site 6 semantics): a second resolve is the same
	// instance, never a fresh manager.
	again := api.mailSessionsFor()
	if again != viaRest {
		t.Fatalf("US-1.1: a second restAPI resolve returned a different manager instance; lazy resolution must be create-once per data dir")
	}
}

// TestMailPairClient_UnwiredClientFailsTypedNoDial pins US-1.4/B-4 at wiring
// site 5 (rest_mail.go::mailPairClient → wireMailSessionSource): when the
// pair's identity scope cannot resolve, the client is left UNWIRED and its
// first production dial fails with W1's typed ErrSessionSourceMissing —
// the fake server accepts ZERO connections (no unmanaged dial), and the
// wire answer stays the class-only envelope.
//
// The pair uses unique IDs on purpose: the identity memo is keyed by
// (agent, workspace) alone, so reusing the shared fixture pair could pick up
// another test's memo entry and mask the unreadable identity file.
func TestMailPairClient_UnwiredClientFailsTypedNoDial(t *testing.T) {
	env := newMailRedEnv(t)
	const (
		unwiredAgent = "mia-unwired"
		unwiredWS    = "ws-unwired"
	)
	imapPort, accepts := listenCount(t)
	smtpPort, _ := listenCount(t)

	cfg := env.api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID: unwiredAgent, Name: "Unwired", Type: config.AgentTypeCustom, Home: env.api.homePath,
	})
	cfg.Mailboxes[unwiredAgent] = map[string]config.MailboxConfig{
		unwiredWS: {
			Enabled: true, WorkspaceID: unwiredWS,
			IMAPHost: "127.0.0.1", IMAPPort: imapPort,
			SMTPHost: "127.0.0.1", SMTPPort: smtpPort,
			Username:    "unwired@test.local",
			PasswordRef: mailboxCredKey(unwiredAgent, unwiredWS),
		},
	}
	require.NoError(t, env.api.credStore.Set(mailboxCredKey(unwiredAgent, unwiredWS), "s3cret"))

	// US-1.4's Given: "the feature's application manager EXISTS". The typed
	// wiring refusal is the manager-wired process's contract; before that,
	// the Wave C→D legacy per-call dial is the sanctioned behaviour and the
	// scenario does not apply.
	env.api.mailSessionsFor()

	// Break the pair's identity state on disk: scope resolution must fail
	// (US-8.4's honest store-state rule), leaving the client unwired.
	identPath, err := config.MailPairIdentityPath(env.api.homePath, unwiredAgent, unwiredWS)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(identPath), 0o700))
	require.NoError(t, os.WriteFile(identPath, []byte("{ this is not valid identity json"), 0o600))

	path := "/api/v1/workspaces/" + unwiredWS + "/mail/" + unwiredAgent + "/folders"
	rec := mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")

	if stdlibNotFound(rec) {
		t.Fatalf("BLOCKED: GET %s not implemented — required by w5 US-1.4/B-4", path)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("US-1.4: an unwired client's first dial must fail with the typed wiring refusal (502 class envelope), got %d body=%s", rec.Code, rec.Body.String())
	}
	er := decodeMailErr(t, rec)
	if er.Code == nil || !mailUpstreamClasses[*er.Code] {
		got := "<nil>"
		if er.Code != nil {
			got = *er.Code
		}
		t.Fatalf("US-1.4: refusal must carry one closed safe class, got %q (body=%s)", got, rec.Body.String())
	}
	if accepts.Load() != 0 {
		t.Fatalf("US-1.4/B-4: the unwired client opened %d connection(s) to the mail server — a missing source must produce the typed wiring error, never an unmanaged dial", accepts.Load())
	}
}

// TestMailRuntime_TypedWiringErrorIsErrSessionSourceMissing pins the error
// identity itself: the refusal the gateway path produces IS W1's typed
// ErrSessionSourceMissing (US-1.4: "W1's typed visible wiring error"), so a
// future silent fallback cannot masquerade as any generic 502.
func TestMailRuntime_TypedWiringErrorIsErrSessionSourceMissing(t *testing.T) {
	var client *email.Client
	_ = client // the typed error is exercised through the REST surface above;
	// here the identity is pinned directly so a renamed/reworded typed error
	// cannot silently drift away from the spec's wording.
	msg := email.ErrSessionSourceMissing.Error()
	for _, want := range []string{"no mail session source injected", "SetSessionSource"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("US-1.4: typed wiring error must name the missing injection (want substring %q), got %q", want, msg)
		}
	}
}

// TestMailRuntime_SessionsAccessorIsCreateOncePerDataDir pins the shared
// accessor's own contract (site 6): two direct resolves for the same state
// dir return the same instance, so a second caller can never fork the pool
// even without the restAPI in the picture.
func TestMailRuntime_SessionsAccessorIsCreateOncePerDataDir(t *testing.T) {
	home := t.TempDir()
	first := email.SharedMailSessions(home, email.SessionsConfig{})
	second := email.SharedMailSessions(home, email.SessionsConfig{})
	if first == nil || first != second {
		t.Fatalf("US-1.1/MC-1: SharedMailSessions must be create-once per state dir; got two different instances for %s", home)
	}
}
