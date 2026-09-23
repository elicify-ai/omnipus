// sessionmode.go: ADR-092 D1 shell permission modes — mode types, the
// presentation-layer derivation of the global/per-agent modes from EXISTING
// config state (no new fields at those two levels, FR-001), and the
// session-scoped per-chat modifier store (FR-004) — the ONE genuinely new
// piece of state this ADR introduces.
//
// Design note (why this is not a fourth ApprovalGrantStore-shaped thing):
// ApprovalGrantStore (pkg/security/approvalgrants.go) keys grants on
// (sessionID, agentID, tool, argsFingerprint) because a grant is scoped to
// one tool call shape for one agent in one session. A shell MODE modifier is
// coarser — ADR-092's "composer quick switch" sets one mode for the whole
// chat, not per agent within it — so SessionModeStore keys on sessionID
// alone (FR-004: "new, separate, session-keyed state"). The per-agent
// dimension of the three-level merge is supplied separately, by
// AgentShellModeOverride reading the agent's own (pre-existing) bash
// tool-policy override — never by a second key on this store.
package agent

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ShellMode is one of the three ADR-092 D1 named shell-permission modes.
// Global and per-agent mode are a PRESENTATION over existing state (FR-001):
// they are computed by GlobalShellMode / AgentShellModeOverride below, never
// stored as a ShellMode on disk. The per-chat modifier (SessionModeStore) is
// the one place a ShellMode value is genuinely held as state.
type ShellMode string

const (
	// ShellModeAsk is the tightest mode: every shell command shows the
	// approval dialog. Presentation of the existing bash tool-policy value
	// "ask" (or any value stricter than "allow" that has no named-mode
	// equivalent, e.g. an explicit per-agent "deny" — ADR-092 D1: "the UI
	// shows such an agent as its nearest named mode (Ask)").
	ShellModeAsk ShellMode = "ask"
	// ShellModeAuto is the middle mode: commands run while a kernel sandbox
	// confines them; anything needing more asks (D7/D8, built by lane L3).
	// Presentation of bash tool-policy "allow" with GodMode=false.
	ShellModeAuto ShellMode = "auto"
	// ShellModeGod is the loosest mode: no approvals, no kernel sandbox, no
	// network egress filter. Global-only (D1) — never a valid per-agent or
	// per-chat value; AgentShellModeOverride and SessionModeStore never
	// produce it. Presentation of bash tool-policy "allow" with
	// GodMode=true.
	ShellModeGod ShellMode = "god"
)

// modeRank orders ShellMode by permissiveness: lower is tighter (stricter).
// This is the single ordering both ResolveEffectiveShellMode's tighten-only
// merge (loop_policy.go) and any future write-time validator compare
// against. An unrecognized value fails closed to rank 0 (as tight as Ask) —
// never treated as looser than a named mode, so a corrupt or unknown stored
// value can never accidentally widen the merge.
func modeRank(m ShellMode) int {
	switch m {
	case ShellModeAsk:
		return 0
	case ShellModeAuto:
		return 1
	case ShellModeGod:
		return 2
	default:
		return 0
	}
}

// tighterShellMode returns whichever of a, b has the lower (stricter) rank.
// A tie returns a. This is the sole primitive the tighten-only merge is
// built from — every "loosening refused" guarantee in this file and in
// loop_policy.go's ResolveEffectiveShellMode reduces to this one comparison
// never being able to pick the looser value.
func tighterShellMode(a, b ShellMode) ShellMode {
	if modeRank(b) < modeRank(a) {
		return b
	}
	return a
}

// GlobalShellMode derives the operator's global default mode from the
// EXISTING sandbox config state — no new field (ADR-092 D1/FR-001): the
// "bash" entry in cfg.Sandbox.ToolPolicies (config.ToolPolicy's "allow"/
// "ask"/"deny" values) and the EXISTING cfg.Sandbox.GodMode flag.
//
//	bash policy != "allow"        -> ShellModeAsk (covers "ask", "deny", and
//	                                  an absent/empty entry — all fail closed
//	                                  to the tightest named mode)
//	bash policy == "allow", !GodMode -> ShellModeAuto
//	bash policy == "allow", GodMode  -> ShellModeGod
//
// A nil cfg has no ceiling to read and resolves to ShellModeAsk, the safe
// default (matches every other fail-closed resolver in this package).
func GlobalShellMode(cfg *config.Config) ShellMode {
	if cfg == nil {
		return ShellModeAsk
	}
	if config.ToolPolicy(cfg.Sandbox.ToolPolicies["bash"]) != config.ToolPolicyAllow {
		return ShellModeAsk
	}
	if cfg.Sandbox.GodMode {
		return ShellModeGod
	}
	return ShellModeAuto
}

// AgentShellModeOverride derives agentID's per-agent presentation mode from
// its EXISTING per-agent bash tool-policy override (cfg.Agents.List[i].
// Tools.Builtin.Policies["bash"]) — no new field (FR-001). Returns
// (mode, true) when the agent has an explicit override; (ShellModeAsk aka
// the zero value, false) when the agent carries no override at all and
// therefore rides the global ceiling untouched — callers MUST check the
// bool, not treat the zero ShellMode as "Ask, explicitly set", since an
// absent override must not tighten anything at resolution time.
//
// God Mode is global-only (D1): an agent-level "allow" override presents as
// ShellModeAuto, never ShellModeGod, regardless of the global GodMode flag
// — an agent cannot grant itself God Mode by carrying its own "allow" entry.
//
// A per-agent value with no named-mode equivalent (e.g. ADR-090's Jim,
// bash: "deny") presents as its nearest named mode, ShellModeAsk — it is
// still at least as tight as Ask, so folding it into the tighten-only merge
// as Ask never loosens anything relative to the agent's real (stricter)
// policy; the full deny is still enforced separately by
// resolveEffectivePolicyWith's own deny>ask>allow merge, unaffected by this
// mode presentation.
func AgentShellModeOverride(cfg *config.Config, agentID string) (mode ShellMode, hasOverride bool) {
	if cfg == nil || agentID == "" {
		return "", false
	}
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if a.ID != agentID {
			continue
		}
		if a.Tools == nil {
			return "", false
		}
		p, ok := a.Tools.Builtin.Policies["bash"]
		if !ok {
			return "", false
		}
		if p == config.ToolPolicyAllow {
			return ShellModeAuto, true
		}
		return ShellModeAsk, true
	}
	return "", false
}

// SessionModeStore is a thread-safe, session-scoped store of the ADR-092 D1
// per-chat mode modifier (FR-004) — the "composer quick switch." It holds at
// most one ShellMode per session id. Structurally mirrors
// security.ApprovalGrantStore (session-keyed, nil-receiver-safe, dies with
// the session) but is deliberately NOT a fourth tool-policy layer: it is
// consulted only by ResolveEffectiveShellMode (loop_policy.go) AFTER the
// ADR-077 global×agent policy merge has already resolved the "bash" ceiling
// — there is no chat_id key on cfg.Sandbox.ToolPolicies or any agent's
// Tools.Builtin.Policies (Hard Constraint #6).
//
// The zero value is not usable — construct with NewSessionModeStore. Every
// method is nil-receiver-safe: a nil *SessionModeStore never panics and
// always resolves to the fail-safe outcome (Get => not found, i.e. "no
// modifier, inherit the layer above"; Set/InheritFrom/ClearSession =>
// no-op).
type SessionModeStore struct {
	mu        sync.Mutex
	modifiers map[string]ShellMode // sessionID -> per-chat modifier
}

// NewSessionModeStore creates an empty session-mode store.
func NewSessionModeStore() *SessionModeStore {
	return &SessionModeStore{
		modifiers: make(map[string]ShellMode),
	}
}

// Get returns sessionID's recorded per-chat modifier, or ("", false) when
// none has been set — the "inherit the global×agent ceiling untouched" case.
// Nil-safe; an empty sessionID always misses.
func (s *SessionModeStore) Get(sessionID string) (ShellMode, bool) {
	if s == nil || sessionID == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.modifiers[sessionID]
	return m, ok
}

// Set records sessionID's per-chat modifier, replacing any prior value for
// this session. Returns false (no-op) for a nil store or an empty
// sessionID/mode — the same "never key on an empty string" discipline
// ApprovalGrantStore's Record enforces, so two unrelated callers with an
// empty session id can never collide.
//
// Set does NOT itself enforce tighten-only — it has no config access to
// derive the ceiling it must not loosen below. Tighten-only is enforced two
// ways, by design (ADR-092 D1's "server-side, enforced" language plus its
// own resolution-time backstop): the WRITE-TIME caller (lane L5's REST/WS
// handler, FR-003) re-reads the global default and rejects a loosening
// write with 4xx BEFORE calling Set at all; and even if a looser value were
// somehow recorded anyway, ResolveEffectiveShellMode's tighten-only merge
// (loop_policy.go) can never let it widen the effective mode past what the
// global×agent layers already resolved to — Set persists whatever it is
// given, but resolution never honors a value looser than its ceiling.
func (s *SessionModeStore) Set(sessionID string, mode ShellMode) bool {
	if s == nil || sessionID == "" || mode == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modifiers[sessionID] = mode
	return true
}

// InheritFrom copies the parent's per-chat modifier from srcSessionID into
// the delegate's own dstSessionID — copy-at-spawn semantics, mirroring
// security.ApprovalGrantStore.InheritFrom (ADR-057): a snapshot taken at the
// moment of the call, not a live/shared reference, and a later Set on the
// source is not retroactively visible to an already-spawned delegate.
//
// This copies ONLY the session-modifier half of FR-005's "tightest of
// (parent modifier, delegate's own agent override)" merge. The other half —
// the delegate's own per-agent override — is NOT folded in here: it is
// supplied separately, by resolving AgentShellModeOverride(cfg,
// delegateAgentID) against the delegate's OWN agent id at resolution time
// (ResolveEffectiveShellMode, loop_policy.go). Folding it in here would
// require this store to carry config access it does not have, and would let
// a later change to the delegate's per-agent policy go stale in a copied
// value instead of being read live on every resolution. The net effect at
// resolution time is identical to "tightest of (parent modifier, delegate's
// own override)" either way, since tighterShellMode is applied at every
// merge step regardless of which layer a value came from.
//
// If dstSessionID already holds a modifier (e.g. a second delegation hop, or
// the delegate's own chat previously received a quick-switch write before
// this call), the result is the TIGHTER of the existing value and the
// copied one — a union that can only tighten, never a blind overwrite that
// could lose a stricter value dstSessionID already had on file.
//
// No-op on a nil store, a nil-store-equivalent call, or an empty
// srcSessionID/dstSessionID. A source with no recorded modifier and a
// destination with no existing value leaves the destination unset (nothing
// to inherit) — consistent with ApprovalGrantStore.InheritFrom's "no grants
// under the source key" no-op branch.
func (s *SessionModeStore) InheritFrom(srcSessionID, dstSessionID string) {
	if s == nil || srcSessionID == "" || dstSessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.modifiers[srcSessionID]
	if !ok {
		return
	}
	if dst, ok := s.modifiers[dstSessionID]; ok {
		s.modifiers[dstSessionID] = tighterShellMode(dst, src)
		return
	}
	s.modifiers[dstSessionID] = src
}

// ClearSession removes sessionID's recorded modifier — the session modifier
// "ends with the chat" (ADR-092 D4's grant-lifetime language applies
// identically here: FR-006, "Modifier clears on restart with the session").
// No-op on a nil store or an empty sessionID.
func (s *SessionModeStore) ClearSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.modifiers, sessionID)
}
