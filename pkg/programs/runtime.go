// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// Run-time notices (spec "Messages"; value-free, FR-012, FR-024, FR-027).
const (
	MsgWithheldWebChat = "Connected programs were not used for this command: programs are off for the web chat. " +
		"A person can switch them on in Settings → Programs."
	MsgWithheldUnresolved = "Connected programs were not used for this command: where this work came from could not be confirmed."
	MsgStoreUnavailable   = "Connected programs were not loaded for this command: the credential store is unavailable."
	msgWithheldConnection = "Connected programs were not used for this command: programs are off for work that came from %s. " +
		"A person can switch them on for this channel on the Connectors page."
	msgVariableConflict = "%s was not set for this command: two sources set it. A person can fix this in Settings → Programs."
	msgGitHostConflict  = "The connected git login for %s was not used: two programs supply it. A person can fix this in Settings → Programs."
	msgOldGit           = "Connected git logins were not used: git %s is too old. Update git to 2.31 or later."
	// MsgInheritedGitConfig is the God Mode notice when inherited GIT_CONFIG_*
	// entries are replaced by the managed git logins for one command.
	MsgInheritedGitConfig = "Git settings from the environment (GIT_CONFIG_*) were replaced for this command by the connected git logins."
)

// TokenSource is the credential store program tokens are read from. Tokens
// are read only through credentials.ReadProgramCredential, which requires a
// valid program-owned binding and generation (FR-048, I-3 at the reader), so
// an ordinary credential carrying a program reference's name is never used.
type TokenSource = *credentials.Store

// TurnRequest is what one command's program resolution needs from the turn.
type TurnRequest struct {
	// AgentID is the acting agent; its programs list decides the entries.
	AgentID string
	// Provenance is the turn's provenance (I-2); eligibility is decided from
	// it against the current settings (FR-010).
	Provenance bus.Provenance
	// PrivateVariables are the names of the agent-private variables the same
	// child receives. A program variable that is also one of them is set by
	// neither (FR-024, S-30).
	PrivateVariables []string
}

// gitLogin is one managed git login: the host, the username git sends and the
// variable the helper reads the token from (the entry's first variable).
type gitLogin struct {
	Host     string
	Username string
	Variable string
}

// TurnPrograms is the resolved program set of one command: what Attach adds,
// what the network grants cover and the notices the result carries.
type TurnPrograms struct {
	// Entries are the eligible switched-on entries with a saved credential.
	Entries []ProgramEntry
	// Env are the token variables (name=value), conflicts removed.
	Env []string
	// Tokens are every current and retired program token value to mask.
	Tokens []string
	// Notices are the value-free notices for the result.
	Notices []string

	gitLogins []gitLogin
	// withheld are the variables two sources set (FR-024): neither value
	// may reach the child, including the agent's private value.
	withheld []string
}

// Withholds reports whether name is a variable two sources set for this
// command (FR-024). The launch sites drop the agent's private value of such a
// variable, so neither source reaches the child.
func (p *TurnPrograms) Withholds(name string) bool {
	return p != nil && slices.Contains(p.withheld, name)
}

// HasGitLogins reports whether any entry supplies a managed git login.
func (p *TurnPrograms) HasGitLogins() bool { return p != nil && len(p.gitLogins) > 0 }

// Attachment is the ResolvedPrograms for Attach: the variables plus, when
// gitEnv is given, the managed git configuration.
func (p *TurnPrograms) Attachment(gitEnv []string) ResolvedPrograms {
	if p == nil {
		return ResolvedPrograms{}
	}
	env := append(append([]string(nil), p.Env...), gitEnv...)
	return ResolvedPrograms{Env: env, Tokens: append([]string(nil), p.Tokens...)}
}

// MaskOnly is the ResolvedPrograms of a child that receives no program
// variables but whose output is still masked (the git version probe).
func (p *TurnPrograms) MaskOnly() ResolvedPrograms {
	if p == nil {
		return ResolvedPrograms{}
	}
	return ResolvedPrograms{Tokens: append([]string(nil), p.Tokens...)}
}

// SitesFor returns the union of the network sites of the entries whose
// program words include any of words (exact, case-sensitive last path part;
// FR-015, FR-018), sorted and deduplicated. Nil when none matches.
func (p *TurnPrograms) SitesFor(words []string) []string {
	if p == nil {
		return nil
	}
	set := map[string]struct{}{}
	for _, e := range p.Entries {
		for _, w := range words {
			if slices.Contains(e.ProgramWords, ProgramWordName(w)) {
				for _, s := range e.NetworkSites {
					set[s] = struct{}{}
				}
				break
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// MatchesProgramWord reports whether word (last path part) belongs to an
// eligible switched-on entry.
func (p *TurnPrograms) MatchesProgramWord(word string) bool {
	if p == nil {
		return false
	}
	name := ProgramWordName(word)
	for _, e := range p.Entries {
		if slices.Contains(e.ProgramWords, name) {
			return true
		}
	}
	return false
}

// ProgramWordName is the exact, case-sensitive last path part of a program
// word (FR-018): `/usr/local/bin/gh` and `./bin/gh` are both `gh`.
func ProgramWordName(word string) string {
	if i := strings.LastIndexByte(word, '/'); i >= 0 {
		return word[i+1:]
	}
	return word
}

// retired keeps every program token value this process has seen, so a
// replaced or disconnected token stays masked (FR-020: current and retired).
var retired = struct {
	sync.Mutex
	values map[string]struct{}
}{values: map[string]struct{}{}}

// rememberToken records a token value for masking for the life of the process.
func rememberToken(v string) {
	if v == "" {
		return
	}
	retired.Lock()
	defer retired.Unlock()
	if _, known := retired.values[v]; known {
		return
	}
	retired.values[v] = struct{}{}
	out := make([]string, 0, len(retired.values))
	for t := range retired.values {
		out = append(out, t)
	}
	publishTokenReplacer(out)
}

// knownTokens returns every token value seen so far, sorted.
func knownTokens() []string {
	retired.Lock()
	defer retired.Unlock()
	out := make([]string, 0, len(retired.values))
	for v := range retired.values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// RememberToken registers a token value for masking (current or retired) for
// the life of the process. Publication calls it before the value can reach
// any child.
func RememberToken(v string) { rememberToken(v) }

// allowedOrigin answers FR-010's per-origin setting: web and independent by
// the web-chat setting (founder Q1 = A), a connector by its connection's
// setting; an unknown connection is not allowed.
func allowedOrigin(cfg *config.Config) func(bus.Origin) bool {
	return func(o bus.Origin) bool {
		if o == bus.OriginWeb || o == bus.OriginIndependent {
			return cfg.Programs.WebChatAllowed
		}
		id, ok := bus.ConnectorID(o)
		if !ok {
			return false
		}
		inst, ok := cfg.Channels[id]
		return ok && inst.ProgramsAllowed
	}
}

// withheldNotices names what withheld a provenance (FR-012).
func withheldNotices(p bus.Provenance, allowed func(bus.Origin) bool) []string {
	if p.IsUnresolved() {
		return []string{MsgWithheldUnresolved}
	}
	var out []string
	web := false
	for _, o := range p.WithheldOrigins(allowed) {
		if id, ok := bus.ConnectorID(o); ok {
			out = append(out, fmt.Sprintf(msgWithheldConnection, id))
			continue
		}
		if !web {
			web = true
			out = append(out, MsgWithheldWebChat)
		}
	}
	return out
}

// switchedOn returns the agent's switched-on entries in catalog order.
func switchedOn(cfg *config.Config, agentID string) ([]ProgramEntry, error) {
	var ids []string
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			ids = cfg.Agents.List[i].Programs
			break
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	entries, err := Entries(cfg)
	if err != nil {
		return nil, err
	}
	var on []ProgramEntry
	for _, e := range entries {
		if slices.Contains(ids, e.ID) {
			on = append(on, e)
		}
	}
	return on, nil
}

// Resolve decides one command's program set (FR-010, FR-011, FR-012,
// FR-024, FR-027). storeErr is the error of opening the credential store;
// a nil store with a nil error means no store exists yet (nothing saved).
// The result is order-independent: conflicts drop every party.
//
// The result is never nil. A non-nil error (catalog unreadable, store
// unavailable, a token unreadable) comes with a result that carries no
// variables and, for store failures, the FR-027 notice: the command still
// runs, and the caller logs the error.
func Resolve(cfg *config.Config, store TokenSource, storeErr error, req TurnRequest) (*TurnPrograms, error) {
	out := &TurnPrograms{Tokens: knownTokens()}
	if cfg == nil || req.AgentID == "" {
		return out, nil
	}
	on, err := switchedOn(cfg, req.AgentID)
	if err != nil {
		return out, err
	}
	if len(on) == 0 {
		return out, nil
	}
	allowed := allowedOrigin(cfg)
	if !req.Provenance.Eligible(allowed) {
		out.Notices = withheldNotices(req.Provenance, allowed)
		return out, nil
	}
	if storeErr != nil {
		// FR-027: the command runs without tokens; the caller logs the cause.
		out.Notices = []string{MsgStoreUnavailable}
		return out, fmt.Errorf("programs: credential store unavailable: %w", storeErr)
	}
	if store == nil {
		return out, nil
	}
	tokens := map[string]string{}
	for _, e := range on {
		v, _, gerr := credentials.ReadProgramCredential(store, TokenReference(e.ID))
		var nf *credentials.NotFoundError
		switch {
		case errors.As(gerr, &nf):
			continue // switched on but not connected: nothing to add
		case gerr != nil:
			out.Notices = []string{MsgStoreUnavailable}
			out.Entries, out.Env, out.gitLogins = nil, nil, nil
			return out, fmt.Errorf("programs: reading the token of %q: %w", e.ID, gerr)
		}
		rememberToken(v)
		tokens[e.ID] = v
		out.Entries = append(out.Entries, e)
	}
	out.Tokens = knownTokens()
	out.Env, out.Notices, out.withheld = variableEnv(out.Entries, tokens, req.PrivateVariables)
	logins, gitNotices := gitLogins(out.Entries)
	// A helper reads its token from the entry's first variable; when that
	// variable was not set (a run-time conflict) the helper is not added
	// either, so it can never answer with another source's value.
	for _, l := range logins {
		if envHas(out.Env, l.Variable) {
			out.gitLogins = append(out.gitLogins, l)
		}
	}
	out.Notices = append(out.Notices, gitNotices...)
	return out, nil
}

// variableEnv sets each token variable once. A variable two entries set, or
// that an agent-private variable also sets, is set by neither (FR-024, S-30).
func variableEnv(entries []ProgramEntry, tokens map[string]string, private []string) (env, notices, withheld []string) {
	owners := map[string][]string{}
	var order []string
	for _, e := range entries {
		for _, v := range e.TokenVariables {
			if _, seen := owners[v]; !seen {
				order = append(order, v)
			}
			owners[v] = append(owners[v], e.ID)
		}
	}
	sort.Strings(order)
	for _, v := range order {
		if len(owners[v]) > 1 || slices.Contains(private, v) {
			notices = append(notices, fmt.Sprintf(msgVariableConflict, v))
			withheld = append(withheld, v)
			continue
		}
		env = append(env, v+"="+tokens[owners[v][0]])
	}
	return env, notices, withheld
}

// gitLogins returns one managed login per git host; a host two entries
// supply gets none, with a notice (FR-024, S-86).
func gitLogins(entries []ProgramEntry) ([]gitLogin, []string) {
	byHost := map[string][]ProgramEntry{}
	var hosts []string
	for _, e := range entries {
		if e.Git == nil || e.Git.Host == "" || len(e.TokenVariables) == 0 {
			continue
		}
		if _, seen := byHost[e.Git.Host]; !seen {
			hosts = append(hosts, e.Git.Host)
		}
		byHost[e.Git.Host] = append(byHost[e.Git.Host], e)
	}
	sort.Strings(hosts)
	var logins []gitLogin
	var notices []string
	for _, h := range hosts {
		if len(byHost[h]) > 1 {
			notices = append(notices, fmt.Sprintf(msgGitHostConflict, h))
			continue
		}
		e := byHost[h][0]
		user := e.Git.Username
		if user == "" {
			user = DefaultGitUsername
		}
		logins = append(logins, gitLogin{Host: h, Username: user, Variable: e.TokenVariables[0]})
	}
	return logins, notices
}

// envHas reports whether env sets name.
func envHas(env []string, name string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return true
		}
	}
	return false
}
