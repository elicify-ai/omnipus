// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements the timed allowance store of Skills & Tools C-26: a
// person's answer "Allow for 1 hour / 3 hours / 1 day" on an approval card.
// An allowance belongs to the AGENT named on the card, not to the chat: it
// covers every session of that agent (its chats in every workspace, its
// channel conversations, its task, plan and scheduled runs, and the helpers
// it starts for itself) and no other agent. It lives in memory only and ends
// at its end time, at a gateway restart, when the agent is deleted
// (ClearAgent) or when the permission it answers changes (EndFor).
//
// Subjects: an ordinary tool card covers the whole tool, any arguments
// (founder Q4 B); the bash exact scope keeps the argument fingerprint; the
// narrow shell cards cover one command family, one path with its access, the
// named network addresses, or the usual web ports; a skill card covers that
// skill's permission part.
//
// Lengths are measured on a monotonic clock from the moment the allowance is
// recorded; the reported end is wall-clock. A second answer for the same
// subject never shortens a running allowance: the later end wins.
//
// The per-(session, agent) egress-proxy token stays per session: a shell
// command's proxy token is seeded from the agent's allowed addresses.

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
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// bashToolName is the tool name ExecTool.Name() returns (pkg/tools/shell.go),
// duplicated as a literal because pkg/tools imports pkg/security. Only bash
// keeps an argument fingerprint (its exact scope), and only bash grants emit
// shell.grant_recorded.
const bashToolName = "bash"

// grantKey scopes an egress-proxy token to one (session, agent) pair.
type grantKey struct {
	sessionID string
	agentID   string
}

// Allowance lengths offered on cards (C-26).
const (
	AllowFor1Hour  = time.Hour
	AllowFor3Hours = 3 * time.Hour
	AllowFor1Day   = 24 * time.Hour
)

// ParseAllowDuration maps the wire ApprovalAllowDuration (1h, 3h, 1d).
func ParseAllowDuration(s string) (time.Duration, bool) {
	switch s {
	case "1h":
		return AllowFor1Hour, true
	case "3h":
		return AllowFor3Hours, true
	case "1d":
		return AllowFor1Day, true
	}
	return 0, false
}

// noEnd marks an entry recorded through a legacy (duration-less) Record*
// call: it lasts until the store is discarded (a restart).
const noEnd = time.Duration(-1)

// agentAllowances is everything one agent may do without a card.
type agentAllowances struct {
	// tools: tool name -> end (whole tool, any arguments).
	tools map[string]time.Duration
	// exact: tool + NUL + argument fingerprint -> end (bash exact scope and
	// the exact-action tools).
	exact    map[string]time.Duration
	prefixes []timedPrefix
	paths    []timedPath
	// networkAny: the usual web and lookup ports to any address.
	networkAny time.Duration
	hasNetwork bool
	hosts      map[string]time.Duration
	// skills: skill key -> end (the permission part only).
	skills map[string]time.Duration
}

type timedPrefix struct {
	tool  string
	grant ShellPrefixGrant
	end   time.Duration
}

type timedPath struct {
	grant fspolicy.PathGrant
	end   time.Duration
}

// EndedAllowance describes one allowance EndFor or ClearAgent removed, for
// the approval.allowance_ended audit entry.
type EndedAllowance struct {
	AgentID string
	Kind    string // tool, bash_exact, shell_family, path, network, skill
	Subject string
}

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

// ApprovalGrantStore is the thread-safe, agent-keyed, in-memory store of
// timed allowances. The zero value is not usable — construct with
// NewApprovalGrantStore. Every method is nil-receiver-safe and resolves to
// the fail-safe outcome (no allowance, i.e. "ask").
type ApprovalGrantStore struct {
	mu     sync.Mutex
	agents map[string]*agentAllowances
	// networkTokens is the per-(session, agent) egress-proxy run token.
	networkTokens map[grantKey]string

	wall func() time.Time
	mono func() time.Duration

	inheritSourceMiss atomic.Int64
	inheritInvalidKey atomic.Int64

	auditLogger *audit.Logger
}

// NewApprovalGrantStore returns an empty store on the process clock.
func NewApprovalGrantStore() *ApprovalGrantStore {
	start := time.Now()
	return &ApprovalGrantStore{
		agents:        make(map[string]*agentAllowances),
		networkTokens: make(map[grantKey]string),
		wall:          time.Now,
		mono:          func() time.Duration { return time.Since(start) },
	}
}

// SetApprovalGrantStoreClock replaces the store's wall and monotonic clocks.
// Lengths are measured on mono; reported ends use wall. Production never
// calls it; it is the clock input of the store.
func SetApprovalGrantStoreClock(s *ApprovalGrantStore, wall func() time.Time, mono func() time.Duration) {
	if s == nil || wall == nil || mono == nil {
		return
	}
	s.mu.Lock()
	s.wall, s.mono = wall, mono
	s.mu.Unlock()
}

// SetAuditLogger wires the audit logger used for shell.grant_recorded.
func (s *ApprovalGrantStore) SetAuditLogger(logger *audit.Logger) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.auditLogger = logger
	s.mu.Unlock()
}

func (s *ApprovalGrantStore) emitGrantRecorded(scope audit.ShellGrantScope, resolvedBinary, agentID, sessionID, tool string, extra map[string]any) {
	s.mu.Lock()
	logger := s.auditLogger
	s.mu.Unlock()
	if logger == nil {
		return
	}
	audit.EmitShellGrantRecorded(context.Background(), logger, scope, resolvedBinary, agentID, sessionID, tool, extra)
}

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

// live reports whether an entry with end time end is still running. Caller
// holds s.mu.
func (s *ApprovalGrantStore) live(end time.Duration) bool {
	return end == noEnd || s.mono() < end
}

// extend returns the later of the current end and now+d (noEnd for a legacy
// record). Caller holds s.mu.
func (s *ApprovalGrantStore) extend(cur time.Duration, had bool, d time.Duration) time.Duration {
	if d == noEnd || (had && cur == noEnd) {
		return noEnd
	}
	next := s.mono() + d
	if had && s.live(cur) && cur > next {
		return cur
	}
	return next
}

// wallEnd reports an end in wall-clock time. Caller holds s.mu.
func (s *ApprovalGrantStore) wallEnd(end time.Duration) time.Time {
	if end == noEnd {
		return time.Time{}
	}
	return s.wall().Add(end - s.mono())
}

// allowancesFor returns the agent's entry, creating it. Caller holds s.mu.
func (s *ApprovalGrantStore) allowancesFor(agentID string) *agentAllowances {
	a := s.agents[agentID]
	if a == nil {
		a = &agentAllowances{
			tools: map[string]time.Duration{}, exact: map[string]time.Duration{},
			hosts: map[string]time.Duration{}, skills: map[string]time.Duration{},
		}
		s.agents[agentID] = a
	}
	return a
}

// exactActionTools are the five tools whose answer covers one exact action
// only (C-26 point 9, founder Q5 A): they are never covered whole, never get
// a timed allowance, and a legacy record keeps their argument fingerprint.
var exactActionTools = []string{"browser_evaluate", "environment_setup", "request_mount", "send_email", "set_config"}

// ExactActionToolNames returns a copy of the exact-action tool list.
func ExactActionToolNames() []string {
	return append([]string(nil), exactActionTools...)
}

// IsExactActionTool reports whether tool is one of the exact-action tools.
func IsExactActionTool(tool string) bool {
	for _, t := range exactActionTools {
		if t == tool {
			return true
		}
	}
	return false
}

// toolKey returns the map and key of a tool allowance subject: the whole
// tool, or for bash and the exact-action tools the exact arguments.
func toolKey(a *agentAllowances, tool string, args map[string]any) (map[string]time.Duration, string, string, bool) {
	if tool != bashToolName && !IsExactActionTool(tool) {
		return a.tools, tool, "", true
	}
	fp, ok := fingerprintArgs(args)
	if !ok {
		return nil, "", "", false
	}
	return a.exact, tool + "\x00" + fp, fp, true
}

// recordTool stores a tool allowance and returns its end, the fingerprint
// and whether a new entry was added.
func (s *ApprovalGrantStore) recordTool(agentID, tool string, args map[string]any, d time.Duration) (time.Time, string, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.allowancesFor(agentID)
	m, key, fp, ok := toolKey(a, tool, args)
	if !ok {
		return time.Time{}, "", false, false
	}
	cur, had := m[key]
	m[key] = s.extend(cur, had, d)
	return s.wallEnd(m[key]), fp, !had, true
}

// IsAllowed reports whether agentID holds a running allowance covering a
// call of tool with args, in any of its sessions (the session is not part of
// the key; an empty one fails closed). Ordinary tools are covered whole;
// bash and the exact-action tools need the exact arguments.
func (s *ApprovalGrantStore) IsAllowed(sessionID, agentID, tool string, args map[string]any) bool {
	if s == nil || sessionID == "" {
		return false
	}
	return s.IsAllowedForAgent(agentID, tool, args)
}

// IsAllowedForAgent is IsAllowed without a session.
func (s *ApprovalGrantStore) IsAllowedForAgent(agentID, tool string, args map[string]any) bool {
	if s == nil || agentID == "" || tool == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.agents[agentID]
	if a == nil {
		return false
	}
	m, key, _, ok := toolKey(a, tool, args)
	if !ok {
		return false
	}
	end, had := m[key]
	return had && s.live(end)
}

// Record stores an allowance with no end (until restart). Kept for callers
// that predate timed answers; card answers use RecordFor.
func (s *ApprovalGrantStore) Record(sessionID, agentID, tool string, args map[string]any) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" {
		return false
	}
	_, fp, added, ok := s.recordTool(agentID, tool, args, noEnd)
	if ok && added && tool == bashToolName {
		s.emitGrantRecorded(audit.ShellGrantScopeExact, "", agentID, sessionID, tool, map[string]any{"args_fingerprint": fp})
	}
	return ok
}

// RecordFor stores a timed allowance for the agent and returns its
// wall-clock end (the later one when an allowance was already running). The
// exact-action tools never get a timed allowance.
func (s *ApprovalGrantStore) RecordFor(agentID, tool string, args map[string]any, d time.Duration) (time.Time, bool) {
	if s == nil || agentID == "" || tool == "" || d <= 0 || IsExactActionTool(tool) {
		return time.Time{}, false
	}
	end, fp, _, ok := s.recordTool(agentID, tool, args, d)
	if ok && tool == bashToolName {
		s.emitGrantRecorded(audit.ShellGrantScopeExact, "", agentID, "", tool, map[string]any{
			"args_fingerprint": fp, "duration": d.String(), "expires_at": end.UTC().Format(time.RFC3339),
		})
	}
	return end, ok
}

func (s *ApprovalGrantStore) recordPrefix(agentID, tool string, grant ShellPrefixGrant, d time.Duration) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.allowancesFor(agentID)
	for i := range a.prefixes {
		if a.prefixes[i].tool == tool && a.prefixes[i].grant == grant {
			a.prefixes[i].end = s.extend(a.prefixes[i].end, true, d)
			return s.wallEnd(a.prefixes[i].end), false
		}
	}
	a.prefixes = append(a.prefixes, timedPrefix{tool: tool, grant: grant, end: s.extend(0, false, d)})
	return s.wallEnd(a.prefixes[len(a.prefixes)-1].end), true
}

// RecordPrefixGrant stores a command-family allowance with no end.
func (s *ApprovalGrantStore) RecordPrefixGrant(sessionID, agentID, tool string, grant ShellPrefixGrant) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" || grant.Binary == "" {
		return false
	}
	if _, added := s.recordPrefix(agentID, tool, grant, noEnd); !added {
		return true
	}
	s.emitGrantRecorded(audit.ShellGrantScopePrefix, grant.Binary, agentID, sessionID, tool, map[string]any{
		"arg_prefix": grant.ArgPrefix, "run_in_background": grant.RunInBackground,
	})
	return true
}

// RecordPrefixGrantFor stores a timed command-family allowance.
func (s *ApprovalGrantStore) RecordPrefixGrantFor(agentID, tool string, grant ShellPrefixGrant, d time.Duration) (time.Time, bool) {
	if s == nil || agentID == "" || tool == "" || grant.Binary == "" || d <= 0 {
		return time.Time{}, false
	}
	end, _ := s.recordPrefix(agentID, tool, grant, d)
	s.emitGrantRecorded(audit.ShellGrantScopePrefix, grant.Binary, agentID, "", tool, map[string]any{
		"arg_prefix": grant.ArgPrefix, "run_in_background": grant.RunInBackground,
		"duration": d.String(), "expires_at": end.UTC().Format(time.RFC3339),
	})
	return end, true
}

// IsPrefixAllowed reports whether a running command-family allowance of the
// agent covers the call.
func (s *ApprovalGrantStore) IsPrefixAllowed(sessionID, agentID, tool, resolvedBinary string, args []string, runInBackground bool) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" || resolvedBinary == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.agents[agentID]
	if a == nil {
		return false
	}
	for _, p := range a.prefixes {
		if p.tool == tool && s.live(p.end) && p.grant.prefixMatches(resolvedBinary, args, runInBackground) {
			return true
		}
	}
	return false
}

func (s *ApprovalGrantStore) recordPath(agentID string, grant fspolicy.PathGrant, d time.Duration) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.allowancesFor(agentID)
	for i := range a.paths {
		if a.paths[i].grant.Path == grant.Path && a.paths[i].grant.Access == grant.Access {
			a.paths[i].end = s.extend(a.paths[i].end, true, d)
			return s.wallEnd(a.paths[i].end)
		}
	}
	a.paths = append(a.paths, timedPath{grant: grant, end: s.extend(0, false, d)})
	return s.wallEnd(a.paths[len(a.paths)-1].end)
}

// RecordPathGrant stores a path allowance (one path with its access) with no
// end.
func (s *ApprovalGrantStore) RecordPathGrant(sessionID, agentID string, grant fspolicy.PathGrant) bool {
	if s == nil || sessionID == "" || agentID == "" || grant.Path == "" || grant.Access == 0 {
		return false
	}
	s.recordPath(agentID, grant, noEnd)
	s.emitGrantRecorded(audit.ShellGrantScopePathWidening, "", agentID, sessionID, bashToolName, map[string]any{
		"path": grant.Path, "access": pathAccessLabel(grant.Access),
	})
	return true
}

// RecordPathGrantFor stores a timed path allowance.
func (s *ApprovalGrantStore) RecordPathGrantFor(agentID string, grant fspolicy.PathGrant, d time.Duration) (time.Time, bool) {
	if s == nil || agentID == "" || grant.Path == "" || grant.Access == 0 || d <= 0 {
		return time.Time{}, false
	}
	end := s.recordPath(agentID, grant, d)
	s.emitGrantRecorded(audit.ShellGrantScopePathWidening, "", agentID, "", bashToolName, map[string]any{
		"path": grant.Path, "access": pathAccessLabel(grant.Access),
		"duration": d.String(), "expires_at": end.UTC().Format(time.RFC3339),
	})
	return end, true
}

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

// PathGrantsFor returns the agent's running path allowances, access merged
// per path.
func (s *ApprovalGrantStore) PathGrantsFor(sessionID, agentID string) []fspolicy.PathGrant {
	if s == nil || sessionID == "" || agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.agents[agentID]
	if a == nil {
		return nil
	}
	var out []fspolicy.PathGrant
	for _, p := range a.paths {
		if !s.live(p.end) {
			continue
		}
		merged := false
		for i := range out {
			if out[i].Path == p.grant.Path {
				out[i].Access |= p.grant.Access
				merged = true
			}
		}
		if !merged {
			out = append(out, p.grant)
		}
	}
	return out
}

func (s *ApprovalGrantStore) recordNetwork(agentID string, hosts []string, d time.Duration) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.allowancesFor(agentID)
	added := false
	var end time.Duration
	named := false
	for _, h := range hosts {
		if h == "" {
			continue
		}
		named = true
		cur, had := a.hosts[h]
		a.hosts[h] = s.extend(cur, had, d)
		added = added || !had
		end = a.hosts[h]
	}
	if !named {
		a.networkAny = s.extend(a.networkAny, a.hasNetwork, d)
		added = added || !a.hasNetwork
		a.hasNetwork = true
		end = a.networkAny
	}
	return s.wallEnd(end), added
}

// RecordNetworkGrant stores an allowance for the usual web and lookup ports
// to any address, with no end.
func (s *ApprovalGrantStore) RecordNetworkGrant(sessionID, agentID string) bool {
	return s.RecordNetworkGrantWithHosts(sessionID, agentID, nil)
}

// RecordNetworkGrantWithHosts stores a network allowance with no end: the
// named addresses, or (none named) the usual ports to any address.
func (s *ApprovalGrantStore) RecordNetworkGrantWithHosts(sessionID, agentID string, hosts []string) bool {
	if s == nil || sessionID == "" || agentID == "" {
		return false
	}
	if _, added := s.recordNetwork(agentID, hosts, noEnd); added {
		var extra map[string]any
		if len(hosts) > 0 {
			extra = map[string]any{"hosts": hosts}
		}
		s.emitGrantRecorded(audit.ShellGrantScopeNetworkWidening, "", agentID, sessionID, bashToolName, extra)
	}
	return true
}

// RecordNetworkGrantFor stores a timed network allowance.
func (s *ApprovalGrantStore) RecordNetworkGrantFor(agentID string, hosts []string, d time.Duration) (time.Time, bool) {
	if s == nil || agentID == "" || d <= 0 {
		return time.Time{}, false
	}
	end, _ := s.recordNetwork(agentID, hosts, d)
	extra := map[string]any{"duration": d.String(), "expires_at": end.UTC().Format(time.RFC3339)}
	if len(hosts) > 0 {
		extra["hosts"] = hosts
	}
	s.emitGrantRecorded(audit.ShellGrantScopeNetworkWidening, "", agentID, "", bashToolName, extra)
	return end, true
}

// RecordNetworkHosts adds named addresses with no end (the idempotent
// re-push of an approved host set).
func (s *ApprovalGrantStore) RecordNetworkHosts(sessionID, agentID string, hosts []string) bool {
	if s == nil || sessionID == "" || agentID == "" || len(hosts) == 0 {
		return false
	}
	if _, added := s.recordNetwork(agentID, hosts, noEnd); added {
		s.emitGrantRecorded(audit.ShellGrantScopeNetworkWidening, "", agentID, sessionID, bashToolName, map[string]any{"hosts": hosts})
	}
	return true
}

// HasNetworkGrant reports a running "usual ports to any address" allowance.
func (s *ApprovalGrantStore) HasNetworkGrant(sessionID, agentID string) bool {
	if s == nil || sessionID == "" || agentID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.agents[agentID]
	return a != nil && a.hasNetwork && s.live(a.networkAny)
}

// NetworkHostsFor returns the agent's running named-address allowances.
func (s *ApprovalGrantStore) NetworkHostsFor(sessionID, agentID string) []string {
	if s == nil || sessionID == "" || agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.agents[agentID]
	if a == nil {
		return nil
	}
	var out []string
	for h, end := range a.hosts {
		if s.live(end) {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// RecordSkillAllowance stores a timed allowance of a skill's permission part
// (C-24, C-26). It never counts as a trust approval.
func (s *ApprovalGrantStore) RecordSkillAllowance(agentID, skillKey string, d time.Duration) (time.Time, bool) {
	if s == nil || agentID == "" || skillKey == "" || d <= 0 {
		return time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.allowancesFor(agentID)
	cur, had := a.skills[skillKey]
	a.skills[skillKey] = s.extend(cur, had, d)
	return s.wallEnd(a.skills[skillKey]), true
}

// IsSkillAllowed reports a running skill allowance for the agent.
func (s *ApprovalGrantStore) IsSkillAllowed(agentID, skillKey string) bool {
	if s == nil || agentID == "" || skillKey == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.agents[agentID]
	if a == nil {
		return false
	}
	end, ok := a.skills[skillKey]
	return ok && s.live(end)
}

// SessionEgressToken returns (minting once) the (session, agent) egress
// token.
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
	s.networkTokens[key] = tok
	return tok
}

func newEgressToken() string {
	buf := make([]byte, 16)
	if _, err := cryptorand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// NetworkTokensForSession lists the session's egress tokens (for revocation
// at session end).
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

// NetworkTokensForAgent lists the agent's live egress tokens in every
// session, for revocation when a bash allowance ends (C-26).
func (s *ApprovalGrantStore) NetworkTokensForAgent(agentID string) []string {
	if s == nil || agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for key, tok := range s.networkTokens {
		if key.agentID == agentID && tok != "" {
			out = append(out, tok)
		}
	}
	sort.Strings(out)
	return out
}

// InheritFrom copies no allowances any more: a helper the agent starts for
// itself shares the agent's key, and another agent gets nothing (C-26). The
// call is kept so the spawn path's argument checks and counters stay.
func (s *ApprovalGrantStore) InheritFrom(srcSessionID, srcAgentID, dstSessionID, dstAgentID string) {
	if s == nil {
		return
	}
	if srcSessionID == "" || srcAgentID == "" || dstSessionID == "" || dstAgentID == "" {
		total := s.inheritInvalidKey.Add(1)
		slog.Warn("approvalgrants: InheritFrom skipped — empty key component",
			"src_session_id", srcSessionID, "src_agent_id", srcAgentID,
			"dst_session_id", dstSessionID, "dst_agent_id", dstAgentID,
			"invalid_key_total", total)
	}
}

// InheritSourceMissCount is kept for metrics callers; always 0 now that
// nothing is inherited.
func (s *ApprovalGrantStore) InheritSourceMissCount() int64 {
	if s == nil {
		return 0
	}
	return s.inheritSourceMiss.Load()
}

// InheritInvalidKeyCount counts InheritFrom calls with an empty key part.
func (s *ApprovalGrantStore) InheritInvalidKeyCount() int64 {
	if s == nil {
		return 0
	}
	return s.inheritInvalidKey.Load()
}

// ClearSession forgets the session's egress tokens. Allowances belong to the
// agent and are not cleared (C-26).
func (s *ApprovalGrantStore) ClearSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.networkTokens {
		if key.sessionID == sessionID {
			delete(s.networkTokens, key)
		}
	}
}

// ClearAgent drops every allowance and egress token of a deleted agent and
// returns the tokens so the caller can revoke them at the proxy.
func (s *ApprovalGrantStore) ClearAgent(agentID string) []string {
	if s == nil || agentID == "" {
		return nil
	}
	s.mu.Lock()
	delete(s.agents, agentID)
	var tokens []string
	for key, tok := range s.networkTokens {
		if key.agentID == agentID {
			tokens = append(tokens, tok)
			delete(s.networkTokens, key)
		}
	}
	s.mu.Unlock()
	sort.Strings(tokens)
	return tokens
}

// AllowanceSubjectSkillPrefix marks a skill key in EndFor's key argument.
const AllowanceSubjectSkillPrefix = "skill:"

// EndFor ends the running allowances that answer the permission of key for
// each agent in agentIDs (nil = every agent) and returns them for the audit
// (C-26 point 7). key is a tool name, an MCP server wildcard mcp_<S>_*
// (every tool of that server), or "skill:<skill key>". Ending a bash
// permission ends every bash allowance kind; the caller revokes the agent's
// egress tokens.
func (s *ApprovalGrantStore) EndFor(agentIDs []string, key string) []EndedAllowance {
	if s == nil || key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	targets := agentIDs
	if targets == nil {
		for id := range s.agents {
			targets = append(targets, id)
		}
		sort.Strings(targets)
	}
	var ended []EndedAllowance
	for _, id := range targets {
		a := s.agents[id]
		if a == nil {
			continue
		}
		ended = append(ended, s.endForAgent(id, a, key)...)
	}
	return ended
}

func (s *ApprovalGrantStore) endForAgent(id string, a *agentAllowances, key string) []EndedAllowance {
	var ended []EndedAllowance
	if skill, ok := strings.CutPrefix(key, AllowanceSubjectSkillPrefix); ok {
		if end, had := a.skills[skill]; had && s.live(end) {
			ended = append(ended, EndedAllowance{AgentID: id, Kind: "skill", Subject: skill})
		}
		delete(a.skills, skill)
		return ended
	}
	matches := func(tool string) bool { return tool == key }
	if prefix, ok := strings.CutSuffix(key, "_*"); ok && prefix != "" {
		matches = func(tool string) bool { return tool == prefix || strings.HasPrefix(tool, prefix+"_") }
	}
	for tool, end := range a.tools {
		if matches(tool) {
			if s.live(end) {
				ended = append(ended, EndedAllowance{AgentID: id, Kind: "tool", Subject: tool})
			}
			delete(a.tools, tool)
		}
	}
	for k, end := range a.exact {
		if tool, fp, _ := strings.Cut(k, "\x00"); tool != bashToolName && matches(tool) {
			if s.live(end) {
				ended = append(ended, EndedAllowance{AgentID: id, Kind: "tool", Subject: tool + " " + fp})
			}
			delete(a.exact, k)
		}
	}
	if key != bashToolName {
		return ended
	}
	for k, end := range a.exact {
		if tool, fp, _ := strings.Cut(k, "\x00"); tool == bashToolName {
			if s.live(end) {
				ended = append(ended, EndedAllowance{AgentID: id, Kind: "bash_exact", Subject: fp})
			}
			delete(a.exact, k)
		}
	}
	for _, p := range a.prefixes {
		if s.live(p.end) {
			ended = append(ended, EndedAllowance{AgentID: id, Kind: "shell_family", Subject: p.grant.Binary + " " + p.grant.ArgPrefix})
		}
	}
	for _, p := range a.paths {
		if s.live(p.end) {
			ended = append(ended, EndedAllowance{AgentID: id, Kind: "path", Subject: p.grant.Path})
		}
	}
	for h, end := range a.hosts {
		if s.live(end) {
			ended = append(ended, EndedAllowance{AgentID: id, Kind: "network", Subject: h})
		}
	}
	if a.hasNetwork && s.live(a.networkAny) {
		ended = append(ended, EndedAllowance{AgentID: id, Kind: "network", Subject: "*"})
	}
	a.prefixes, a.paths = nil, nil
	a.hosts = map[string]time.Duration{}
	a.hasNetwork, a.networkAny = false, 0
	return ended
}
