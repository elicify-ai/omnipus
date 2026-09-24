// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements the session-scoped "Always Allow" tool-approval grant
// store — the fix for the tool-consent-boundary bug where a user's "Always
// Allow" decision on the exec/tool approval dialog was recorded on a
// per-WebSocket-CONNECTION map (wsApprovalHook.alwaysAllowed) instead of a
// per-SESSION store. Because the SPA reconnects on any drop (network blip,
// idle, gateway restart, refresh), every reconnect created a fresh hook with
// an empty map, silently discarding the grant and re-prompting the user.
//
// ApprovalGrantStore keys grants on (session_id, agent_id, tool_name, args
// fingerprint) rather than the connection, so the grant survives reconnects
// for the lifetime of the SESSION. A grant recorded for one argument object
// never auto-approves a later call of the same tool with different arguments
// ("Always Allow" on `request_mount` of folder A is "this folder, this
// session", not "any folder"). A DIFFERENT agent in the same session, or a
// DIFFERENT session calling the same tool, still requires a fresh prompt.

package security

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// bashToolName is the tool name ExecTool.Name() returns (pkg/tools/shell.go)
// — duplicated here as a literal rather than imported, since pkg/tools
// already imports pkg/security and the reverse would cycle. Used to gate
// EventShellGrantRecorded emission on the "exact" scope (Record) to bash
// calls only: ApprovalGrantStore is shared by every ask-policy tool, but
// FR-032(c)'s "exact" grant kind is specifically ADR-092's shell/bash
// taxonomy (it is enumerated alongside prefix/path-widening/network-
// widening, which are all bash-only concepts) — emitting shell.grant_recorded
// for, say, a write_file "Always Allow" would misattribute it to this ADR.
const bashToolName = "bash"

// grantKey scopes a set of always-allowed (tool → argument fingerprints) to
// one (session, agent) pair. Both fields participate in the key so a grant
// recorded for one agent never applies to a different agent running in the
// same session, and a grant recorded in one session never applies to another
// session.
type grantKey struct {
	sessionID string
	agentID   string
}

// ApprovalGrantStore is a thread-safe, session-scoped store of "Always
// Allow" tool-approval grants. Each (session, agent, tool) holds a SET of
// argument fingerprints: one Always Allow on `bash {command:ls}` does not
// approve `bash {command:rm -rf /}`; a later Always Allow on a different
// argv for the same tool is a second fingerprint (union).
//
// The zero value is not usable — construct with NewApprovalGrantStore. Every
// method is nil-receiver-safe: calling a method on a nil *ApprovalGrantStore
// never panics and always resolves to the fail-safe outcome (IsAllowed =>
// false, i.e. "ask"; Record/InheritFrom/ClearSession => no-op). This lets
// callers hold a possibly-unwired store (e.g. in a test fixture) without an
// extra nil check at every call site.
// ShellPrefixGrant is the ADR-092 D4 "prefix" scope Allow grant: `{binary,
// arg_prefix}`, ignoring cwd, token-boundary matched against a segment's
// argument words following the resolved binary — "npm run test" does not
// match "npm run testfoo" (FR-024). Binary is the RESOLVED absolute
// executable path (D3's resolve-and-verify, ADR-092 S24's look-alike
// defence: a grant recorded against the real `git` must never match a
// same-named look-alike earlier on a later, attacker-influenced PATH).
// RunInBackground is a separate match dimension (D4): a grant recorded for
// a foreground call does not cover the same command run_in_background, and
// vice versa.
type ShellPrefixGrant struct {
	Binary          string
	ArgPrefix       string
	RunInBackground bool
}

// prefixMatches reports whether g covers a call whose resolved binary is
// resolvedBinary, whose argument words are args, and whose run_in_background
// flag is runInBackground. Token-boundary: every word of g.ArgPrefix must
// appear, in order, as a whole word at the start of args — "run test" is a
// prefix of ["run","test","-v"] but not of ["run","testfoo"]. An empty
// ArgPrefix matches any arguments (a bare-binary grant).
func (g ShellPrefixGrant) prefixMatches(resolvedBinary string, args []string, runInBackground bool) bool {
	if g.Binary != resolvedBinary || g.RunInBackground != runInBackground {
		return false
	}
	prefix := strings.Fields(g.ArgPrefix)
	if len(prefix) == 0 {
		return true
	}
	if len(args) < len(prefix) {
		return false
	}
	for i, w := range prefix {
		if args[i] != w {
			return false
		}
	}
	return true
}

type ApprovalGrantStore struct {
	mu     sync.Mutex
	grants map[grantKey]map[string]map[string]struct{}

	// prefixGrants holds the ADR-092 D4 "prefix" scope grants, keyed the same
	// way exact grants are (session, agent), then by tool name — a session may
	// hold several prefix grants for the same tool (e.g. `npm run test` and
	// `npm run build`, recorded on two separate Allow clicks).
	prefixGrants map[grantKey]map[string][]ShellPrefixGrant

	// pathGrants holds the ADR-092 D7 filesystem-widening grants (FR-016/
	// FR-036): bash-scoped, single-path, single-access-class widenings
	// approved through the Auto pre-flight escalation. Keyed by (session,
	// agent) only — not by tool — because ResolveTurnFSPolicy's grant-overlay
	// parameter (FR-036) is bash's own per-turn FSPolicy input, not a
	// per-call fingerprint.
	pathGrants map[grantKey][]fspolicy.PathGrant

	// networkGrants holds the ADR-092 D8 network-widening grant (FR-044): a
	// session either holds it or does not — there is no finer scope (D8 is
	// port-level, not domain-level, and applies uniformly to the session's
	// bash child once granted).
	networkGrants map[grantKey]struct{}

	// networkHosts holds the D-13 fix's (2026-09-24 security review)
	// per-session set of HOST names a D8 network escalation's "Always
	// Allow" approved — session-scoped, "for this chat session only,
	// never written to the global config" (founder decision B, point 2).
	// This is layered ON TOP of networkGrants (the existing port-level
	// kernel widening): networkGrants alone used to leave the SEPARATE
	// egress proxy still 403'ing every request ("host not in allow-list"),
	// since the proxy's own static cfg.Sandbox.EgressAllowList never
	// changes at runtime. networkHosts is what
	// sandbox.EgressProxy.GrantRunHosts actually receives (via
	// networkTokens' token, below).
	networkHosts map[grantKey]map[string]struct{}

	// networkTokens holds one opaque, unguessable credential per (session,
	// agent) pair once it has been minted (SessionEgressToken) — reused for
	// every subsequent D8 "Always Allow" in that session rather than
	// re-minted, so a session's egress-proxy grant (sandbox.EgressProxy.
	// runHosts, keyed by this SAME token) accumulates hosts under one
	// stable key instead of orphaning earlier grants under a discarded one.
	networkTokens map[grantKey]string

	// inheritSourceMiss counts InheritFrom calls whose four key components
	// were all non-empty but whose SOURCE key held no grants, so nothing was
	// copied (ADR-057 FR-079). Read via InheritSourceMissCount.
	//
	// This counter — not the log level — is the FR-079 tripwire. A "no grants
	// under the source key" outcome is ROUTINE (most agents never record an
	// "Always Allow"), so logging it at Warn on every delegation would train
	// operators to ignore Warns; the log record is therefore Debug and the
	// counter is always on and assertable. Its purpose is to make ADR-057
	// grill finding C-1 impossible to reproduce silently: a re-key that makes
	// the source lookup miss when it should have hit shows up here as a
	// climbing count with zero inherited grants, instead of as a delegation
	// that hangs for 300 s with no signal anywhere.
	inheritSourceMiss atomic.Int64

	// inheritInvalidKey counts InheritFrom calls rejected before any lookup
	// because at least one of the four key components was empty (ADR-057
	// FR-079, dataset row 6). Read via InheritInvalidKeyCount.
	//
	// Unlike a source miss this is never routine: at spawn time every one of
	// the four components is a resolved id, so an empty one means the CALLER
	// is broken and the child will silently fall through to a fresh approval
	// prompt. It is therefore logged at Warn as well as counted.
	inheritInvalidKey atomic.Int64

	// auditLogger is the ADR-092 FR-032(c)/FR-046 grant-recorded audit sink.
	// Nil by default (pure store, no audit — matches the zero-value contract
	// every other field on this struct follows). Wired via SetAuditLogger.
	// Guarded by mu like every other field.
	auditLogger *audit.Logger
}

// SetAuditLogger wires this store's FR-032(c) grant-recorded audit emitter
// (EventShellGrantRecorded). Nil-safe on the receiver; passing a nil logger
// is also valid (explicitly disables audit for this store, matching
// audit.EmitEntry's own nil-logger no-op contract). Idempotent — callers may
// call it more than once (pkg/tools/shell_permission_mode.go's
// enforceShellPermissionMode calls it once per bash invocation) without
// side effects beyond replacing the stored pointer.
func (s *ApprovalGrantStore) SetAuditLogger(logger *audit.Logger) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.auditLogger = logger
	s.mu.Unlock()
}

// emitGrantRecorded writes the FR-032(c) shell.grant_recorded event for a
// NEWLY recorded grant (callers must not call this for a no-op/duplicate
// record — see each Record* method's own "already granted" short-circuit).
// Reads s.auditLogger under s.mu then emits OUTSIDE the lock, so a slow
// audit write never blocks a concurrent grant lookup/record.
func (s *ApprovalGrantStore) emitGrantRecorded(scope audit.ShellGrantScope, resolvedBinary, agentID, sessionID, tool string, extra map[string]any) {
	s.mu.Lock()
	logger := s.auditLogger
	s.mu.Unlock()
	if logger == nil {
		return
	}
	audit.EmitShellGrantRecorded(context.Background(), logger, scope, resolvedBinary, agentID, sessionID, tool, extra)
}

// NewApprovalGrantStore creates an empty grant store.
func NewApprovalGrantStore() *ApprovalGrantStore {
	return &ApprovalGrantStore{
		grants:        make(map[grantKey]map[string]map[string]struct{}),
		prefixGrants:  make(map[grantKey]map[string][]ShellPrefixGrant),
		pathGrants:    make(map[grantKey][]fspolicy.PathGrant),
		networkGrants: make(map[grantKey]struct{}),
		networkHosts:  make(map[grantKey]map[string]struct{}),
		networkTokens: make(map[grantKey]string),
	}
}

// fingerprintArgs returns the canonical JSON of args. encoding/json sorts
// object keys at every nesting level, so key order does not matter. A nil
// map and an empty map both encode as "{}" and therefore match. A Marshal
// failure returns ("", false) so the caller can fail closed.
func fingerprintArgs(args map[string]any) (string, bool) {
	if args == nil {
		args = map[string]any{}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// IsAllowed reports whether (sessionID, agentID) has previously been granted
// "Always Allow" for tool with this exact argument object.
//
// Fail-safe (consent boundary — SEC audit): a nil store, or an empty
// sessionID / agentID / tool, ALWAYS returns false. This is deliberate and
// load-bearing: an empty string must never be treated as a valid scoping key,
// or two unrelated callers that both happen to have an empty session_id (or
// empty agent_id) would silently share the same grant. An args map that
// cannot be marshaled also returns false. An empty (or nil) args map is NOT
// fail-closed — it is a real grant for a no-arg tool, fingerprinted as "{}".
// Callers MUST keep prompting ("ask") whenever this returns false.
func (s *ApprovalGrantStore) IsAllowed(sessionID, agentID, tool string, args map[string]any) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" {
		return false
	}
	fp, ok := fingerprintArgs(args)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tools, ok := s.grants[grantKey{sessionID: sessionID, agentID: agentID}]
	if !ok {
		return false
	}
	fps, ok := tools[tool]
	if !ok {
		return false
	}
	_, granted := fps[fp]
	return granted
}

// Record grants "Always Allow" for tool with this exact argument object,
// scoped to (sessionID, agentID). A later Record of the same tool with a
// different argv adds a second fingerprint; it does not replace the first.
//
// Returns true when the grant was actually recorded, false when this call was
// a no-op — a nil store, an empty sessionID / agentID / tool, or an args map
// that cannot be marshaled. Recording under an empty-string key would risk
// exactly the cross-caller collision IsAllowed's fail-safe check exists to
// prevent. Callers that report success back to a human (e.g. the "always"
// tool-approval action) MUST check this return value rather than assuming the
// grant took effect — see rest_tool_registry.go's HandleToolApprovals, which
// logs a Warn instead of Info when Record no-ops so the operator knows the
// tool will keep prompting on the next matching call.
func (s *ApprovalGrantStore) Record(sessionID, agentID, tool string, args map[string]any) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" {
		return false
	}
	fp, ok := fingerprintArgs(args)
	if !ok {
		return false
	}
	s.mu.Lock()
	key := grantKey{sessionID: sessionID, agentID: agentID}
	tools, ok := s.grants[key]
	if !ok {
		tools = make(map[string]map[string]struct{})
		s.grants[key] = tools
	}
	fps, ok := tools[tool]
	if !ok {
		fps = make(map[string]struct{})
		tools[tool] = fps
	}
	_, dup := fps[fp]
	fps[fp] = struct{}{}
	s.mu.Unlock()

	// FR-032(c): only the "bash" tool's exact grants are ADR-092's own
	// taxonomy (see bashToolName's doc comment) — and only NEW fingerprints,
	// never a re-Record of one already on file.
	if !dup && tool == bashToolName {
		s.emitGrantRecorded(audit.ShellGrantScopeExact, "", agentID, sessionID, tool, map[string]any{
			"args_fingerprint": fp,
		})
	}
	return true
}

// RecordPrefixGrant grants ADR-092 D4 "prefix" scope for tool, scoped to
// (sessionID, agentID). A later RecordPrefixGrant of the same tool with a
// different {binary, arg_prefix, run_in_background} adds a second entry; it
// does not replace the first. Returns false (no-op) for a nil store, an
// empty sessionID/agentID/tool, or an empty grant.Binary — the same
// never-key-on-empty-string discipline Record enforces.
func (s *ApprovalGrantStore) RecordPrefixGrant(sessionID, agentID, tool string, grant ShellPrefixGrant) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" || grant.Binary == "" {
		return false
	}
	s.mu.Lock()
	key := grantKey{sessionID: sessionID, agentID: agentID}
	if s.prefixGrants == nil {
		s.prefixGrants = make(map[grantKey]map[string][]ShellPrefixGrant)
	}
	tools, ok := s.prefixGrants[key]
	if !ok {
		tools = make(map[string][]ShellPrefixGrant)
		s.prefixGrants[key] = tools
	}
	for _, existing := range tools[tool] {
		if existing == grant {
			s.mu.Unlock()
			return true // already granted, not a duplicate entry
		}
	}
	tools[tool] = append(tools[tool], grant)
	s.mu.Unlock()

	s.emitGrantRecorded(audit.ShellGrantScopePrefix, grant.Binary, agentID, sessionID, tool, map[string]any{
		"arg_prefix":        grant.ArgPrefix,
		"run_in_background": grant.RunInBackground,
	})
	return true
}

// IsPrefixAllowed reports whether (sessionID, agentID) holds a D4 prefix
// grant for tool covering a call whose resolved binary is resolvedBinary,
// whose argument words are args, and whose run_in_background flag is
// runInBackground. Fail-safe: a nil store or an empty sessionID/agentID/
// tool/resolvedBinary always returns false — callers MUST keep prompting
// ("ask") whenever this returns false.
func (s *ApprovalGrantStore) IsPrefixAllowed(sessionID, agentID, tool, resolvedBinary string, args []string, runInBackground bool) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" || resolvedBinary == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tools, ok := s.prefixGrants[grantKey{sessionID: sessionID, agentID: agentID}]
	if !ok {
		return false
	}
	for _, g := range tools[tool] {
		if g.prefixMatches(resolvedBinary, args, runInBackground) {
			return true
		}
	}
	return false
}

// RecordPathGrant adds one ADR-092 D7 filesystem-widening grant (FR-016) to
// (sessionID, agentID)'s session-scoped set. A later RecordPathGrant for the
// same Path unions its Access bits into the existing entry rather than
// appending a duplicate — a session that first widens {P, read} and later
// {P, write} ends up with one entry covering both, which is what
// ResolveTurnFSPolicy's grant-overlay parameter (FR-036) expects to render
// as a single PathRule. Returns false (no-op) for a nil store, an empty
// sessionID/agentID, an empty grant.Path, or a zero grant.Access.
func (s *ApprovalGrantStore) RecordPathGrant(sessionID, agentID string, grant fspolicy.PathGrant) bool {
	if s == nil || sessionID == "" || agentID == "" || grant.Path == "" || grant.Access == 0 {
		return false
	}
	s.mu.Lock()
	key := grantKey{sessionID: sessionID, agentID: agentID}
	if s.pathGrants == nil {
		s.pathGrants = make(map[grantKey][]fspolicy.PathGrant)
	}
	existing := s.pathGrants[key]
	merged := false
	for i := range existing {
		if existing[i].Path == grant.Path {
			existing[i].Access |= grant.Access
			merged = true
			break
		}
	}
	if !merged {
		s.pathGrants[key] = append(existing, grant)
	}
	s.mu.Unlock()

	// bashToolName: RecordPathGrant is exclusively the ADR-092 D7 filesystem-
	// widening path, always bash-scoped (see the field's own doc comment —
	// "bash's own per-turn FSPolicy input"). Emitted on every call, merged
	// or new: even a widened-access-bits merge into an existing path entry
	// is a fresh grant decision an operator needs to see (e.g. a session
	// that first widened {P, read} just widened the SAME path to {P, write}).
	s.emitGrantRecorded(audit.ShellGrantScopePathWidening, "", agentID, sessionID, bashToolName, map[string]any{
		"path":   grant.Path,
		"access": pathAccessLabel(grant.Access),
	})
	return true
}

// pathAccessLabel renders a fspolicy.PathGrantAccess* bitmask as an
// operator-readable word for the FR-032(c) grant-recorded audit event.
// Package-local duplicate of shell_permission_mode.go's accessLabel
// (pkg/tools) — pkg/tools already imports pkg/security, so the reverse
// import would cycle; same "duplicated as an independent type" precedent
// that file's own package comment documents for ShellMode.
func pathAccessLabel(access uint64) string {
	switch {
	case access&fspolicy.PathGrantAccessWrite != 0 && access&fspolicy.PathGrantAccessRead != 0:
		return "read+write"
	case access&fspolicy.PathGrantAccessWrite != 0:
		return "write"
	default:
		return "read"
	}
}

// PathGrantsFor returns a defensive copy of every ADR-092 D7 filesystem
// widening recorded for (sessionID, agentID) this session — the value
// ResolveTurnFSPolicy's grant-overlay parameter (FR-036) is populated from
// at every bash pre-flight/exec call site. Nil-safe; returns nil for a nil
// store or an empty sessionID/agentID (never a stored grant leaks under an
// empty key, matching every other method's fail-safe discipline).
func (s *ApprovalGrantStore) PathGrantsFor(sessionID, agentID string) []fspolicy.PathGrant {
	if s == nil || sessionID == "" || agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.pathGrants[grantKey{sessionID: sessionID, agentID: agentID}]
	if len(existing) == 0 {
		return nil
	}
	out := make([]fspolicy.PathGrant, len(existing))
	copy(out, existing)
	return out
}

// RecordNetworkGrant grants the ADR-092 D8 network-widening scope (FR-044)
// for (sessionID, agentID) — the whole session's bash child, port-level
// (DefaultConnectPorts), not domain-level. Idempotent: recording it twice
// leaves the same single grant. Returns false (no-op) for a nil store or an
// empty sessionID/agentID.
func (s *ApprovalGrantStore) RecordNetworkGrant(sessionID, agentID string) bool {
	if s == nil || sessionID == "" || agentID == "" {
		return false
	}
	s.mu.Lock()
	if s.networkGrants == nil {
		s.networkGrants = make(map[grantKey]struct{})
	}
	key := grantKey{sessionID: sessionID, agentID: agentID}
	_, dup := s.networkGrants[key]
	s.networkGrants[key] = struct{}{}
	s.mu.Unlock()

	// bashToolName: RecordNetworkGrant is exclusively the ADR-092 D8
	// network-widening grant, always bash-scoped (see the field's own doc
	// comment). Idempotent state mutation — only emit on the FIRST record,
	// not on a redundant re-grant of an already-held session grant.
	if !dup {
		s.emitGrantRecorded(audit.ShellGrantScopeNetworkWidening, "", agentID, sessionID, bashToolName, nil)
	}
	return true
}

// HasNetworkGrant reports whether (sessionID, agentID) already holds the
// ADR-092 D8 network-widening grant this session. Fail-safe: a nil store or
// an empty sessionID/agentID always returns false.
func (s *ApprovalGrantStore) HasNetworkGrant(sessionID, agentID string) bool {
	if s == nil || sessionID == "" || agentID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.networkGrants[grantKey{sessionID: sessionID, agentID: agentID}]
	return ok
}

// SessionEgressToken returns the opaque, unguessable D-13 credential for
// (sessionID, agentID) — minting a fresh one (32 random hex bytes,
// crypto/rand) the first time it is needed for this key, and returning the
// SAME value on every later call for the same key so a session's egress
// grants accumulate under one stable sandbox.EgressProxy.runHosts entry
// rather than a fresh, orphaned one per call. Returns "" for a nil store or
// an empty sessionID/agentID (fail-closed: no token means the caller mints
// nothing and the egress proxy grants nothing for that call).
func (s *ApprovalGrantStore) SessionEgressToken(sessionID, agentID string) string {
	if s == nil || sessionID == "" || agentID == "" {
		return ""
	}
	key := grantKey{sessionID: sessionID, agentID: agentID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok, ok := s.networkTokens[key]; ok {
		return tok
	}
	tok := newEgressToken()
	if s.networkTokens == nil {
		s.networkTokens = make(map[grantKey]string)
	}
	s.networkTokens[key] = tok
	return tok
}

// newEgressToken mints a fresh 32-hex-character (16-byte) random token.
// crypto/rand failure is treated the same as every other fail-closed path
// in this file: an empty result, which SessionEgressToken's callers (and
// EgressProxy.GrantRunHosts) treat as "grant nothing" rather than a panic
// or a predictable fallback value.
func newEgressToken() string {
	buf := make([]byte, 16)
	if _, err := cryptorand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// RecordNetworkHosts is the D-13 fix's "Always Allow" write path: adds
// hosts to the SESSION-scoped set already recorded for (sessionID,
// agentID) — union, not replace, so approving a second, different host
// later in the same session does not drop the first. Returns false (no-op)
// for a nil store, an empty sessionID/agentID, or an empty hosts slice.
func (s *ApprovalGrantStore) RecordNetworkHosts(sessionID, agentID string, hosts []string) bool {
	if s == nil || sessionID == "" || agentID == "" || len(hosts) == 0 {
		return false
	}
	key := grantKey{sessionID: sessionID, agentID: agentID}
	s.mu.Lock()
	if s.networkHosts == nil {
		s.networkHosts = make(map[grantKey]map[string]struct{})
	}
	set := s.networkHosts[key]
	if set == nil {
		set = make(map[string]struct{}, len(hosts))
		s.networkHosts[key] = set
	}
	added := false
	for _, h := range hosts {
		if h == "" {
			continue
		}
		if _, dup := set[h]; !dup {
			set[h] = struct{}{}
			added = true
		}
	}
	s.mu.Unlock()

	if added {
		s.emitGrantRecorded(audit.ShellGrantScopeNetworkWidening, "", agentID, sessionID, bashToolName,
			map[string]any{"hosts": hosts})
	}
	return true
}

// NetworkHostsFor returns the session-scoped set of hosts (D-13's "Always
// Allow") already recorded for (sessionID, agentID), sorted for a
// deterministic result. Fail-safe: a nil store or an empty sessionID/
// agentID always returns nil.
func (s *ApprovalGrantStore) NetworkHostsFor(sessionID, agentID string) []string {
	if s == nil || sessionID == "" || agentID == "" {
		return nil
	}
	key := grantKey{sessionID: sessionID, agentID: agentID}
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.networkHosts[key]
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// NetworkTokensForSession returns every D-13 egress token minted for
// sessionID, across every agent that used it — called by AgentLoop.
// CloseSession BEFORE ClearSession wipes this store's own copy, so the
// caller can also revoke each token on the (separate-package)
// sandbox.EgressProxy it lives on. Fail-safe: a nil store or an empty
// sessionID always returns nil.
func (s *ApprovalGrantStore) NetworkTokensForSession(sessionID string) []string {
	if s == nil || sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for key, tok := range s.networkTokens {
		if key.sessionID == sessionID && tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// InheritFrom copies the grant set currently recorded under the SOURCE key
// {srcSessionID, srcAgentID} into the DESTINATION key {dstSessionID,
// dstAgentID} — a union, not a replace, so any grant the destination already
// holds in its own right is preserved, and a copy, not a move, so the source
// still resolves afterwards. Every tool → every argument fingerprint is
// copied.
//
// This is copy-at-spawn semantics: it snapshots the source's grants at the
// moment of the call. Grants recorded on the source AFTER a child has already
// been spawned are NOT retroactively visible to that already-running child —
// this matches "copy-at-spawn" as specified for delegation inheritance
// (spawn / run_subagent), not a live/shared reference.
//
// # Why the operation takes TWO keys (ADR-057 FR-031, grill finding C-1)
//
// The predecessor of this method, Inherit(sessionID, parentAgentID,
// childAgentID), used ONE session id for both the source lookup and the
// destination write. That is correct only while parent and child share a
// session id. ADR-057 gives every delegated child its OWN store-backed
// session, so the parent's grants live under the parent's session id while
// the child reads under its own — and "re-key Inherit's first argument to the
// child" (the obvious one-line fix) makes the SOURCE lookup miss, returns
// having done nothing, and reports success. The child then blocks on a fresh
// approval prompt nobody is watching until the 300 s approval timeout fires.
//
// Source and destination are therefore separate parameters and MUST be passed
// separately: at spawn, the SOURCE is the parent's session id + the parent's
// agent id, and the DESTINATION is the child's OWN session id + the child's
// agent id. Self-delegation is the same-agent, different-session case
// (srcAgentID == dstAgentID, srcSessionID != dstSessionID) and is handled by
// the same union.
//
// Both no-op branches are counted and logged rather than returning silently
// (FR-079) — see inheritSourceMiss / inheritInvalidKey for why each has the
// log level it has:
//
//   - a nil store: no state exists to count into, so it returns immediately;
//   - any empty key component: counted in inheritInvalidKey, logged at Warn
//     (a caller defect — every component is a resolved id at spawn);
//   - a source key holding no grants: counted in inheritSourceMiss, logged at
//     Debug (routine — most agents never record an "Always Allow").
//
// An identity call (source key == destination key) copies nothing because the
// destination already holds exactly the source's set; it is neither an error
// nor a miss and is not counted.
func (s *ApprovalGrantStore) InheritFrom(srcSessionID, srcAgentID, dstSessionID, dstAgentID string) {
	if s == nil {
		return
	}
	if srcSessionID == "" || srcAgentID == "" || dstSessionID == "" || dstAgentID == "" {
		total := s.inheritInvalidKey.Add(1)
		slog.Warn("approvalgrants: InheritFrom skipped — empty key component, no grants inherited",
			"src_session_id", srcSessionID,
			"src_agent_id", srcAgentID,
			"dst_session_id", dstSessionID,
			"dst_agent_id", dstAgentID,
			"invalid_key_total", total)
		return
	}

	srcKey := grantKey{sessionID: srcSessionID, agentID: srcAgentID}
	dstKey := grantKey{sessionID: dstSessionID, agentID: dstAgentID}

	s.mu.Lock()
	srcSet := s.grants[srcKey]
	_, srcHasNetwork := s.networkGrants[srcKey]
	// ADR-092: the source-miss short-circuit must consider all FOUR grant
	// kinds, not only the exact-fingerprint map — a session holding nothing
	// but a D7 path widening (no exact "Always Allow" ever recorded) must
	// still inherit it on delegation, not be treated as an empty source and
	// skipped before the D7/D8/D4 copy blocks below ever run.
	if len(srcSet) == 0 && len(s.prefixGrants[srcKey]) == 0 && len(s.pathGrants[srcKey]) == 0 && !srcHasNetwork {
		s.mu.Unlock()
		total := s.inheritSourceMiss.Add(1)
		slog.Debug("approvalgrants: InheritFrom found no grants to inherit under the source key",
			"src_session_id", srcSessionID,
			"src_agent_id", srcAgentID,
			"dst_session_id", dstSessionID,
			"dst_agent_id", dstAgentID,
			"source_miss_total", total)
		return
	}
	// Identity: the destination set IS the source set. Ranging a map while
	// writing the keys it already contains is safe, but short-circuiting says
	// so explicitly instead of relying on that subtlety.
	if srcKey != dstKey {
		dstSet := s.grants[dstKey]
		if dstSet == nil {
			dstSet = make(map[string]map[string]struct{}, len(srcSet))
			s.grants[dstKey] = dstSet
		}
		for tool, srcFPs := range srcSet {
			dstFPs := dstSet[tool]
			if dstFPs == nil {
				dstFPs = make(map[string]struct{}, len(srcFPs))
				dstSet[tool] = dstFPs
			}
			for fp := range srcFPs {
				dstFPs[fp] = struct{}{}
			}
		}
	}
	// ADR-092: the three new grant kinds inherit on delegation exactly like
	// the exact-fingerprint grant above — "a delegate inherits it via the
	// same InheritFrom mechanism as command grants" (D7), "same session/
	// delegate/clear-on-close lifetime as PathGrants" (D8's own text about
	// the network grant, and D4 about prefix grants). Same union-not-replace,
	// copy-not-move semantics; same identity short-circuit.
	if srcKey != dstKey {
		if srcPrefix := s.prefixGrants[srcKey]; len(srcPrefix) > 0 {
			dstPrefix := s.prefixGrants[dstKey]
			if dstPrefix == nil {
				dstPrefix = make(map[string][]ShellPrefixGrant, len(srcPrefix))
				s.prefixGrants[dstKey] = dstPrefix
			}
			for tool, grants := range srcPrefix {
				existing := dstPrefix[tool]
				for _, g := range grants {
					dup := false
					for _, e := range existing {
						if e == g {
							dup = true
							break
						}
					}
					if !dup {
						existing = append(existing, g)
					}
				}
				dstPrefix[tool] = existing
			}
		}
		if srcPaths := s.pathGrants[srcKey]; len(srcPaths) > 0 {
			dstPaths := s.pathGrants[dstKey]
			for _, g := range srcPaths {
				merged := false
				for i := range dstPaths {
					if dstPaths[i].Path == g.Path {
						dstPaths[i].Access |= g.Access
						merged = true
						break
					}
				}
				if !merged {
					dstPaths = append(dstPaths, g)
				}
			}
			s.pathGrants[dstKey] = dstPaths
		}
		if _, ok := s.networkGrants[srcKey]; ok {
			if s.networkGrants == nil {
				s.networkGrants = make(map[grantKey]struct{})
			}
			s.networkGrants[dstKey] = struct{}{}
		}
		// D-13 fix: networkHosts/networkTokens are DELIBERATELY NOT
		// inherited here, unlike every other grant kind above. Both this
		// store's HasNetworkGrant/PathGrantsFor-style "already granted"
		// signal AND the sandbox.EgressProxy's own runHosts map (keyed by
		// TOKEN, not session id) would have to agree for a delegate to
		// actually pass traffic — copying only the host SET into the
		// store without also pushing it to the proxy under the delegate's
		// own token would leave enforceNetworkPreflight reporting
		// "already granted" (skipping its own escalation, which is the
		// ONLY call site that pushes to the proxy) while the proxy itself
		// still 403s the delegate's first request. A delegate therefore
		// re-asks and re-grants its own hosts on its own first need,
		// exactly like a brand-new session — a clean fail-closed gap
		// rather than a "shows granted, silently 403s anyway" one.
	}
	s.mu.Unlock()
}

// InheritSourceMissCount returns the number of InheritFrom calls that resolved
// a well-formed source key holding no grants (ADR-057 FR-079). Nil-safe.
//
// This is real store state, not instrumentation: a test asserts on it directly
// to prove the empty-source branch is observable rather than silent.
func (s *ApprovalGrantStore) InheritSourceMissCount() int64 {
	if s == nil {
		return 0
	}
	return s.inheritSourceMiss.Load()
}

// InheritInvalidKeyCount returns the number of InheritFrom calls rejected
// because at least one of the four key components was empty (ADR-057 FR-079,
// dataset row 6). Nil-safe.
func (s *ApprovalGrantStore) InheritInvalidKeyCount() int64 {
	if s == nil {
		return 0
	}
	return s.inheritInvalidKey.Load()
}

// ClearSession removes every grant recorded for sessionID, across all
// agents. Called when a session ends (AgentLoop.CloseSession) so the store
// does not grow without bound and a finished session's grants can never leak
// into an unrelated future session.
//
// No-op on a nil store or an empty sessionID.
func (s *ApprovalGrantStore) ClearSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.grants {
		if key.sessionID == sessionID {
			delete(s.grants, key)
		}
	}
	// ADR-092: the three new grant kinds die with the session exactly like
	// the exact-fingerprint grant above (D4's "session grants ... end with
	// the chat"; D7/D8 explicitly cite the same clear-on-close lifetime).
	for key := range s.prefixGrants {
		if key.sessionID == sessionID {
			delete(s.prefixGrants, key)
		}
	}
	for key := range s.pathGrants {
		if key.sessionID == sessionID {
			delete(s.pathGrants, key)
		}
	}
	for key := range s.networkGrants {
		if key.sessionID == sessionID {
			delete(s.networkGrants, key)
		}
	}
	// D-13 fix: the two new maps die with the session the same way — this
	// only clears THIS store's own bookkeeping; the caller (AgentLoop.
	// CloseSession) is responsible for calling NetworkTokensForSession
	// BEFORE ClearSession and revoking each token on the sandbox.
	// EgressProxy separately, since this package cannot reach that one
	// (pkg/sandbox already imports pkg/security; the reverse would cycle).
	for key := range s.networkHosts {
		if key.sessionID == sessionID {
			delete(s.networkHosts, key)
		}
	}
	for key := range s.networkTokens {
		if key.sessionID == sessionID {
			delete(s.networkTokens, key)
		}
	}
}
