package gateway

// mail_runtime.go — the one shared Mail runtime (pool + budget + presence)
// and the pair-identity scope resolution (w5-integration wave; w5 spec US-1,
// MC-1–MC-3; landing-order register rows 9/11/12; w5 §2.2 wiring inventory).
//
// The budget was already shared (every site resolves email.SharedMailBudget,
// keyed by the state dir). This file extends the identical mechanism to the
// pooled session manager: ONE process-wide email.MailSessions per data
// root, constructed at the boot site (gateway.go::loadConfigAndProvider,
// before NewAgentLoop — the same ordering rule as SetSharedMailBudget) and
// injected into every client construction site:
//
//	#1 gateway.go::loadConfigAndProvider      agent.SetSharedMailSessions + generation resolver
//	#2 rest.go::restAPI (mailSessions field)  lazy accessor mirrors mailBudgetFor
//	#3 gateway_boot.go / gateway_reload.go    buildMailboxes wires the watcher's clients
//	#4 pkg/agent/email_tools.go               the tools' clients ride the setter + resolver
//	#5 rest_mail.go::mailPairClient           every panel request's client
//	#6 pkg/email (SharedMailSessions)         the state-dir-keyed accessor itself
//
// Once the manager exists, a production dial through a client without an
// injected source is W1's typed ErrSessionSourceMissing wiring error — never
// an uncounted fallback dial (FR-W1-2). That is why ALL client construction
// sites above are wired in the same change: the first SharedMailSessions
// call flips the process-wide "manager wired" flag for everyone.
//
// Credentials: the pool resolves a pair's IMAP login lazily, at
// establishment time, through the resolver installed here. Until the agent
// loop exists (NewAgentLoop runs AFTER the sessions are installed — tools
// constructed inside it already resolve their scope), the resolver reads the
// boot config snapshot; afterwards the LIVE config through the loop, so a
// mailbox save/reload is visible to the next establishment. The password is
// never logged and never leaves the resolver except into the pool's
// establish call (FR-W1-4).
//
// Instrument: W1's SessionsConfig.Instrument callback carries no context
// (PoolInstrumentSample has no pair identity), so a per-operation join of
// the pool sub-fields into the w6 §6.1 record is not soundly writable from
// here — two concurrent operations on one pair would race any context-free
// window marker. The sink is therefore left unwired in this wave and the
// R-3 request for a context-carrying Instrument variant is filed with W1
// (recorded in the wave report); the operation envelope
// (mail_instrument.go::emitMailOperation) emits everything the gateway owns
// truthfully and leaves the pool sub-fields to that seam.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// gatewayMailRuntime holds the process's Mail boot handles so the pool's
// lazy credential resolver can reach them regardless of construction order.
type gatewayMailRuntime struct {
	homePath string
	// bootCfg is the config snapshot loadConfigAndProvider loaded; it serves
	// scope/credential resolution only until the agent loop is published.
	bootCfg   atomic.Pointer[config.Config]
	agentLoop atomic.Pointer[agent.AgentLoop]
	credStore atomic.Pointer[credentials.Store]
}

// gatewayMail is the process's runtime holder — one writer
// (loadConfigAndProvider), many readers (REST lazy path, watcher set,
// agent-tool setters).
var gatewayMail atomic.Pointer[gatewayMailRuntime]

// initGatewayMailRuntime creates and publishes the process's Mail runtime.
// Called ONCE at boot before NewAgentLoop; a second call (double boot in a
// test harness) returns the existing holder unchanged.
func initGatewayMailRuntime(homePath string, cfg *config.Config, store *credentials.Store) *gatewayMailRuntime {
	if rt := gatewayMail.Load(); rt != nil {
		return rt
	}
	rt := newGatewayMailRuntime(homePath)
	rt.bootCfg.Store(cfg)
	if store != nil {
		rt.setCredentialStore(store)
	}
	if !gatewayMail.CompareAndSwap(nil, rt) {
		return gatewayMail.Load()
	}
	return rt
}

// gatewayMailRuntimeFor returns the published runtime, building an
// unpublized holder only for a directly-constructed restAPI (the unit-test
// path that never ran loadConfigAndProvider).
func gatewayMailRuntimeFor(homePath string) *gatewayMailRuntime {
	if rt := gatewayMail.Load(); rt != nil {
		return rt
	}
	return initGatewayMailRuntime(homePath, nil, nil)
}

func newGatewayMailRuntime(homePath string) *gatewayMailRuntime {
	return &gatewayMailRuntime{homePath: homePath}
}

// setAgentLoop publishes the live agent loop (called as soon as
// NewAgentLoop returns — dials can only happen once the loop serves).
func (rt *gatewayMailRuntime) setAgentLoop(al *agent.AgentLoop) {
	rt.agentLoop.Store(al)
}

// setCredentialStore publishes the boot credential store.
func (rt *gatewayMailRuntime) setCredentialStore(s *credentials.Store) {
	rt.credStore.Store(s)
}

// liveConfig returns the loop's live config once wired, the boot snapshot
// before that.
func (rt *gatewayMailRuntime) liveConfig() *config.Config {
	if al := rt.agentLoop.Load(); al != nil {
		return al.GetConfig()
	}
	return rt.bootCfg.Load()
}

// errMailCredentialsResolver is the typed resolution failure: the resolver
// is not yet wired (boot still in progress) or the pair has no live mailbox
// row. It never names the account (US-7: opaque diagnostics only).
var errMailCredentialsResolver = errors.New("mail runtime: mailbox credentials not resolvable for this pair")

// splitMailPairKey splits the pool's pair key ("agentID/workspaceID") back
// into its components. The workspace ID is the LAST component (mirrors
// pkg/email/pool.go::workspaceFromPair — the same grammar, one reading).
func splitMailPairKey(pairKey string) (agentID, workspaceID string, ok bool) {
	idx := strings.LastIndex(pairKey, "/")
	if idx <= 0 || idx == len(pairKey)-1 {
		return "", "", false
	}
	return pairKey[:idx], pairKey[idx+1:], true
}

// resolveCredentials is the pool's establishment-time credential resolver.
func (rt *gatewayMailRuntime) resolveCredentials(pairKey string) (username, password string, err error) {
	agentID, workspaceID, ok := splitMailPairKey(pairKey)
	if !ok {
		return "", "", fmt.Errorf("%w: malformed pair key", errMailCredentialsResolver)
	}
	cfg := rt.liveConfig()
	if cfg == nil {
		return "", "", fmt.Errorf("%w: config not wired yet", errMailCredentialsResolver)
	}
	mb, ok := cfg.Mailboxes[agentID][workspaceID]
	if !ok || !mb.Enabled {
		return "", "", fmt.Errorf("%w: no enabled mailbox row", errMailCredentialsResolver)
	}
	if strings.TrimSpace(mb.PasswordRef) == "" {
		return "", "", fmt.Errorf("%w: no credential reference", errMailCredentialsResolver)
	}
	store := rt.credStore.Load()
	if store == nil {
		s := credentials.NewStore(filepath.Join(rt.homePath, "credentials.json"))
		if err := credentials.Unlock(s); err != nil {
			return "", "", fmt.Errorf("%w: credential store locked", errMailCredentialsResolver)
		}
		store = s
	}
	password, err = store.Get(mb.PasswordRef)
	if err != nil || strings.TrimSpace(password) == "" {
		return "", "", fmt.Errorf("%w: password did not resolve", errMailCredentialsResolver)
	}
	return mb.Username, password, nil
}

// mailScope resolves the pair's pool-identity components: the pair key and
// the non-secret generation (canonical identity + persisted epoch, one
// implementation in pkg/config). Both consumers — pool lease identity and
// budget flight identity — receive these as opaque values.
func (rt *gatewayMailRuntime) mailScope(agentID, workspaceID string, mb config.MailboxConfig) (pairKey, generation string, err error) {
	ident, err := config.LoadOrMintMailPairIdentity(rt.homePath, agentID, workspaceID)
	if err != nil {
		return "", "", err
	}
	return agentID + "/" + workspaceID, config.MailPairGeneration(mb, agentID, workspaceID, ident), nil
}

// mailGenerationForPair resolves just the generation for one pair from the
// live config — the resolver pkg/agent's email tools and the watcher's
// buildMailboxes call per pair. A resolution failure is returned, never
// swallowed: the caller skips the pair visibly (a client without its
// generation would pool under the endpoint-only identity and break pair
// isolation, MC-3).
func (rt *gatewayMailRuntime) mailGenerationForPair(agentID, workspaceID string) (string, error) {
	cfg := rt.liveConfig()
	if cfg == nil {
		return "", fmt.Errorf("%w: config not wired yet", errMailCredentialsResolver)
	}
	mb, ok := cfg.Mailboxes[agentID][workspaceID]
	if !ok || !mb.Enabled {
		return "", fmt.Errorf("%w: no enabled mailbox row", errMailCredentialsResolver)
	}
	_, generation, err := rt.mailScope(agentID, workspaceID, mb)
	return generation, err
}

// mailPairRef renders the instrument's opaque pair_ref: the pair ID and a
// generation fragment computed over an EMPTY MailboxConfig, so the ref is
// stable for the pair's lifetime (it identifies, it does not version).
// Never an address, host:port or folder name (w6 §6.1).
func (rt *gatewayMailRuntime) mailPairRef(agentID, workspaceID string) string {
	ident, err := config.LoadOrMintMailPairIdentity(rt.homePath, agentID, workspaceID)
	if err != nil {
		// Identity unreadable: the ref degrades to a fixed opaque placeholder
		// rather than leaking any pair attribute.
		return "pair-unresolved"
	}
	gen := config.MailPairGeneration(config.MailboxConfig{}, agentID, workspaceID, ident)
	return ident.PairID + "-" + gen[:12]
}

// gatewayMailSessionsFor resolves THE process-wide session manager for one
// data root, installing the credential resolver on first construction
// (first call wins — every later caller gets the same instance for that
// state dir). SessionsConfig.Instrument stays nil: see the file comment's
// Instrument paragraph for the seam hand-off.
func gatewayMailSessionsFor(homePath string) *email.MailSessions {
	return email.SharedMailSessions(homePath, email.SessionsConfig{
		Credentials: gatewayMailRuntimeFor(homePath).resolveCredentials,
	})
}

// wireMailSessionSource injects the shared pool source and the pair's
// identity scope into one freshly constructed client (w5 §2.2 sites 4/5).
// A scope-resolution failure injects NEITHER: the client's first dial then
// fails with W1's typed ErrSessionSourceMissing wiring error instead of
// silently pooling under the endpoint-only identity (which would break pair
// isolation, MC-3). The failure is logged without any pair attribute beyond
// the IDs the caller already named.
func wireMailSessionSource(homePath string, client *email.Client, agentID, workspaceID string, mb config.MailboxConfig) {
	rt := gatewayMailRuntimeFor(homePath)
	pairKey, generation, err := rt.mailScope(agentID, workspaceID, mb)
	if err != nil {
		logsafeWarn("rest: mail pair identity unavailable — client left unwired (typed wiring error on first dial)",
			"agent_id", agentID, "workspace_id", workspaceID)
		return
	}
	client.SetSessionSource(gatewayMailSessionsFor(homePath))
	client.SetSessionScope(pairKey, generation)
}

// restAPI accessors — the restAPI holds the sessions handle captured at
// boot; the lazy path keeps a directly-constructed restAPI (the unit-test
// literal) on the SAME process-wide instances, exactly like mailBudgetFor.

func (a *restAPI) mailRuntimeFor() *gatewayMailRuntime {
	return gatewayMailRuntimeFor(a.homePath)
}

// mailSessionsFor resolves the restAPI's handle on the shared pooled session
// manager (MC-1). Boot injects the process-wide instance at restAPI
// construction (gateway_boot.go); the lazy fallback resolves the same
// state-dir-keyed instance no matter how the restAPI was built.
func (a *restAPI) mailSessionsFor() *email.MailSessions {
	a.mailSessionsOnce.Do(func() {
		if a.mailSessions == nil {
			a.mailSessions = gatewayMailSessionsFor(a.homePath)
		}
	})
	return a.mailSessions
}
