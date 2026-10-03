package agent

// RED — w5-integration claim 1 (wiring site 4): the agent email tools'
// clients borrow sessions from THE process-wide shared session manager —
// never a private pool built at registration.
//
// Oracle (derived from the spec before reading the implementation;
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w5 spec US-1.1/B-1/MC-1: "all three hold the same pool, budget and
//     cache instances (one process-wide set, keyed by the data dir)".
//   - w5 spec §2.2 wiring site 4: "The client facade is constructed with the
//     injected pool/cache handles… never a second `New*` call with fresh
//     state".
//   - w5 spec §2.2 rule 4: no pool construction below the facade.
//
// Mutation this test must kill (check-integration-report.md §2, M16): the
// tool registration path constructs a PRIVATE manager and injects that.
//
// Observable: the shared manager's establishment-time credentials resolver
// is consulted when a registered tool dials (the pool resolves a pair's
// login lazily at establishment, through the resolver installed on THE
// shared manager). A tool client pointed at a private pool never consults
// it — the recorded resolver call is the pointer-identity observable that
// needs no exported getter on the client.

import (
	"context"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestRegisterEmailTools_ResolveThroughTheSharedSessionManager(t *testing.T) {
	// The setters write process globals; save and restore so neighbouring
	// tests in this package see the state they expect.
	prevSessions := sharedMailSessions.Load()
	prevResolver := sharedMailGenerationResolver.Load()
	prevBudget := sharedMailBudget.Load()
	t.Cleanup(func() {
		sharedMailSessions.Store(prevSessions)
		sharedMailGenerationResolver.Store(prevResolver)
		sharedMailBudget.Store(prevBudget)
	})

	var (
		mu       sync.Mutex
		resolved []string
	)
	sessions := email.NewMailSessions(email.SessionsConfig{
		// Establishment-time resolver: the pool consults it when a client
		// borrows a session THROUGH this manager. No IMAP server exists in
		// this test — the dial fails after the resolver runs, which is
		// exactly the observable we need (consulted = the shared manager
		// owns this client's sessions).
		Credentials: func(pairKey string) (string, string, error) {
			mu.Lock()
			resolved = append(resolved, pairKey)
			mu.Unlock()
			return "mailbox@test.local", "pw", nil
		},
	})
	SetSharedMailSessions(sessions)
	SetMailGenerationResolver(func(agentID, workspaceID string) (string, error) {
		return "gen-" + agentID + "-" + workspaceID, nil
	})

	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {
			"ws_shared": {
				Enabled: true, PasswordRef: "MIA_SHARED_POOL_PW", WorkspaceID: "ws_shared",
				IMAPHost: "127.0.0.1", IMAPPort: 1, // nothing listens; the dial fails after establishment starts
				SMTPHost: "127.0.0.1", SMTPPort: 1,
				Username: "mailbox@test.local",
			},
		},
	}}
	t.Setenv("MIA_SHARED_POOL_PW", "pw")

	ag := newEmailTestAgent()
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, true)

	tool, ok := ag.Tools.Get("read_inbox")
	require.True(t, ok, "read_inbox must be registered")

	ctx := tools.WithWorkspaceID(context.Background(), "ws_shared")
	res := tool.Execute(ctx, map[string]any{})
	if !res.IsError {
		t.Fatalf("read_inbox against an unreachable server must fail; got %+v", res)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(resolved) == 0 {
		t.Fatalf("MC-1/§2.2 site 4: the registered tool's client never resolved credentials through THE shared session manager — it is running on a private pool (the shared manager must own every tool client's sessions)")
	}
	if resolved[0] != "mia/ws_shared" {
		t.Fatalf("MC-1: the shared manager was consulted for pair %q, want the tool's own pair identity mia/ws_shared", resolved[0])
	}
}
