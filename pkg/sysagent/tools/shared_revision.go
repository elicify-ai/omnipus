// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Shared mutable setup resources (omnipus-system-agent §6.6, FR-085, FR-088).
//
// Providers, MCP servers and channel instances are install-wide: Settings and
// agent tools both write them. Every read tool returns a secret-free revision
// token per entry (revisionAbsent for an entry that does not exist), and every
// tool write carries the token it planned against in expected_revision. The
// write is refused when the live entry's token differs, so a change made by
// Settings or by another helper after the plan was confirmed is never
// overwritten.
//
// The one synchronization boundary is the gateway config write lock: a tool
// write takes it (Deps.LockConfigWrites) BEFORE entering the agent-loop lock
// (Deps.MutateConfig via WithConfig) — the same order every Settings write
// uses (restAPI.configMu → AgentLoop.SwapConfig), never the reverse.
//
// Mailboxes are the fourth shared resource in §6.6; their token and the
// list_mailboxes read tool belong to the several-mailboxes work unit (WU-18,
// landing 2) — landing 1 has no tool write to a mailbox.

// revisionAbsent is the token of an entry that does not exist (FR-088).
const revisionAbsent = "absent"

// expectedRevisionParam is the write tools' precondition argument.
const expectedRevisionParam = "expected_revision"

// sharedResourceKind names one of the guarded install-wide resources.
type sharedResourceKind string

const (
	sharedProvider  sharedResourceKind = "provider"
	sharedMCPServer sharedResourceKind = "mcp_server"
	sharedChannel   sharedResourceKind = "channel"
)

// revisionHash returns a short, stable token for v: the first 16 hex digits
// of the SHA-256 of its JSON encoding. v must hold only non-secret data.
func revisionHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Only plain strings, bools and slices of them reach here; a marshal
		// failure is a programming error. Fold it into the token so the write
		// is refused (no expected token can match) instead of guarded by "".
		b = []byte("unencodable:" + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// providerRevisionRow is the non-secret projection of one configured row of a
// provider: the effective endpoint, protocol and credential reference name.
type providerRevisionRow struct {
	Protocol   string `json:"protocol"`
	Endpoint   string `json:"endpoint"`
	Region     string `json:"region,omitempty"`
	Reference  string `json:"reference,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"`
	Custom     bool   `json:"custom,omitempty"`
	Unresolved string `json:"unresolved,omitempty"`
}

// providerRows returns every configured row of provider id, in config order.
func providerRows(cfg *config.Config, id string) []*config.ModelConfig {
	if cfg == nil {
		return nil
	}
	var out []*config.ModelConfig
	for _, p := range cfg.Providers {
		if p != nil && providerNameOf(p) == id {
			out = append(out, p)
		}
	}
	return out
}

// effectiveProviderEndpoint resolves a row the way runtime does. The row's
// Provider field may be empty (a "provider/model" reference), so the resolved
// name is set on a copy first.
func effectiveProviderEndpoint(p *config.ModelConfig) (providers.EffectiveEndpoint, error) {
	if p == nil {
		return providers.EffectiveEndpoint{}, fmt.Errorf("no configured entry")
	}
	row := *p
	row.Provider = providerNameOf(p)
	return providers.ResolveEndpoint(&row)
}

// providerRevision is the FR-088 token of provider id: a hash over the id,
// for each configured row its effective endpoint, protocol, region, credential
// reference name, auth method and custom flag, and the same projection of the
// representative row — the one list_providers describes and configure_provider
// writes to — so reordering rows that changes which row that is changes the
// token (type-design T2). revisionAbsent when no row names the provider.
// Never includes a credential value.
func providerRevision(cfg *config.Config, id string) string {
	rows := providerRows(cfg, id)
	if len(rows) == 0 {
		return revisionAbsent
	}
	proj := make([]providerRevisionRow, 0, len(rows))
	for _, p := range rows {
		proj = append(proj, providerProjection(p))
	}
	sort.SliceStable(proj, func(i, j int) bool {
		a, b := revisionHash(proj[i]), revisionHash(proj[j])
		return a < b
	})
	// Duplicate rows (one per model) carry the same projection; keep one of
	// each so adding a model row with the same endpoint is not a provider change.
	uniq := proj[:0]
	for i, r := range proj {
		if i > 0 && r == proj[i-1] {
			continue
		}
		uniq = append(uniq, r)
	}
	return revisionHash(struct {
		ID             string                `json:"id"`
		Rows           []providerRevisionRow `json:"rows"`
		Representative providerRevisionRow   `json:"representative"`
	}{ID: id, Rows: uniq, Representative: providerProjection(representativeProviderRow(cfg, id))})
}

// providerProjection is one provider row's secret-free revision projection.
func providerProjection(p *config.ModelConfig) providerRevisionRow {
	r := providerRevisionRow{
		Region:     strings.TrimSpace(p.Region),
		Reference:  strings.TrimSpace(p.APIKeyRef),
		AuthMethod: strings.TrimSpace(p.AuthMethod),
		Custom:     p.Custom,
	}
	if ep, err := effectiveProviderEndpoint(p); err == nil {
		r.Protocol, r.Endpoint = string(ep.Protocol), ep.API
	} else {
		// An unresolvable row still has a meaning to protect: its typed
		// fields. Hash those plus a marker so the token changes when they do.
		r.Protocol, r.Endpoint = strings.TrimSpace(p.Protocol), strings.TrimSpace(p.APIBase)
		r.Unresolved = "unresolved"
	}
	return r
}

// mcpEffectiveTransport mirrors the MCP manager's inference: an explicit type,
// else stdio when a command is set, else sse when a URL is set.
func mcpEffectiveTransport(srv config.MCPServerConfig) string {
	switch {
	case srv.Type != "":
		return srv.Type
	case srv.Command != "":
		return "stdio"
	case srv.URL != "":
		return "sse"
	}
	return ""
}

// mcpServerRevision is the FR-088 token of MCP server name: a hash over the
// name, transport, command and arguments or URL, enabled state and credential
// reference names (plus the names — never the values — of literal env vars
// and headers, which may hold secrets). revisionAbsent when not configured.
func mcpServerRevision(cfg *config.Config, name string) string {
	if cfg == nil {
		return revisionAbsent
	}
	srv, ok := cfg.Tools.MCP.Servers[name]
	if !ok {
		return revisionAbsent
	}
	refs := make([]string, 0, len(srv.EnvRefs))
	for _, k := range sortedStringKeys(srv.EnvRefs) {
		refs = append(refs, k+"="+srv.EnvRefs[k])
	}
	var deferred string
	if srv.Deferred != nil {
		deferred = fmt.Sprint(*srv.Deferred)
	}
	return revisionHash(struct {
		Name       string   `json:"name"`
		Transport  string   `json:"transport"`
		Command    string   `json:"command,omitempty"`
		Args       []string `json:"args,omitempty"`
		URL        string   `json:"url,omitempty"`
		Enabled    bool     `json:"enabled"`
		References []string `json:"references,omitempty"`
		EnvNames   []string `json:"env_names,omitempty"`
		Headers    []string `json:"header_names,omitempty"`
		EnvFile    string   `json:"env_file,omitempty"`
		Deferred   string   `json:"deferred,omitempty"`
	}{
		Name: name, Transport: mcpEffectiveTransport(srv), Command: srv.Command, Args: srv.Args,
		URL: srv.URL, Enabled: srv.Enabled, References: refs, EnvNames: sortedStringKeys(srv.Env),
		Headers: sortedStringKeys(srv.Headers), EnvFile: srv.EnvFile, Deferred: deferred,
	})
}

// channelRevision is the FR-088 token of channel instance id: a hash over the
// instance id and every non-secret field of its configuration — platform type,
// enabled state, binding (workspace and main agent), allow list, group and
// mention settings and every credential reference name. The two fields that
// can hold a secret value inline (webhook_url, service_account_json) are
// excluded; their *_ref names are included. revisionAbsent when not configured.
func channelRevision(cfg *config.Config, id string) string {
	if cfg == nil {
		return revisionAbsent
	}
	ch, ok := cfg.Channels[id]
	if !ok {
		return revisionAbsent
	}
	ch.WebhookURL = config.SecureString{}
	ch.ServiceAccountJSON = config.SecureString{}
	return revisionHash(struct {
		ID     string                       `json:"id"`
		Config config.ChannelInstanceConfig `json:"config"`
	}{ID: id, Config: ch})
}

// sharedRevision returns the live token of one shared resource.
func sharedRevision(cfg *config.Config, kind sharedResourceKind, id string) string {
	switch kind {
	case sharedProvider:
		return providerRevision(cfg, id)
	case sharedMCPServer:
		return mcpServerRevision(cfg, id)
	case sharedChannel:
		return channelRevision(cfg, id)
	}
	return ""
}

// revisionConflictError is returned by withSharedResourceWrite when the live
// token differs from the caller's expected token. Nothing was written.
type revisionConflictError struct {
	Kind     sharedResourceKind
	ID       string
	Expected string
	Current  string
}

func (e *revisionConflictError) Error() string {
	label := map[sharedResourceKind]string{
		sharedProvider: "provider", sharedMCPServer: "MCP server", sharedChannel: "channel",
	}[e.Kind]
	return fmt.Sprintf("%s %q changed since it was read: expected revision %q, current revision %q",
		label, e.ID, e.Expected, e.Current)
}

// withSharedResourceWrite runs write while holding the gateway config write
// lock, after checking that the live token of (kind, id) equals expected. The
// config lock is taken BEFORE write enters the agent-loop lock through
// WithConfig (FR-085 lock order). A token mismatch returns
// *revisionConflictError and write never runs. Credential writes that belong
// to the change must happen inside write, so a refused write stores nothing.
//
// Deps.LockConfigWrites is wired by the gateway to restAPI.configMu; a nil
// field (unit tests building Deps by hand) adds no outer lock.
func (d *Deps) withSharedResourceWrite(kind sharedResourceKind, id, expected string, write func() error) error {
	unlock := func() {}
	if d.LockConfigWrites != nil {
		unlock = d.LockConfigWrites()
	}
	defer unlock()
	current := sharedRevision(d.GetCfg(), kind, id)
	if current != expected {
		return &revisionConflictError{Kind: kind, ID: id, Expected: expected, Current: current}
	}
	return write()
}

// expectedRevisionArg reads the required expected_revision argument. A missing
// or blank value is refused before any write: every shared-resource write must
// carry the token from the confirmed plan (FR-085).
func expectedRevisionArg(args map[string]any, readTool string) (string, *tools.ToolResult) {
	v, _ := args[expectedRevisionParam].(string)
	v = strings.TrimSpace(v)
	if v == "" {
		return "", tools.ErrorResult(errorJSON("INVALID_INPUT",
			expectedRevisionParam+" is required",
			fmt.Sprintf("Read the entry's revision with %s first (use %q for an entry that does not exist yet)",
				readTool, revisionAbsent)))
	}
	return v, nil
}

// revisionConflictResult renders a refused shared-resource write with the
// agent/skill convention's code, CONFLICT (chief ruling 2026-10-14). It names the
// current token so the caller can re-read, re-plan and confirm again.
func revisionConflictResult(err error, readTool string) (*tools.ToolResult, bool) {
	var rc *revisionConflictError
	if !errors.As(err, &rc) {
		return nil, false
	}
	b, _ := json.MarshalIndent(map[string]any{
		"success": false,
		"error": map[string]any{
			"code":              "CONFLICT",
			"message":           rc.Error() + "; nothing was saved",
			"current_revision":  rc.Current,
			"expected_revision": rc.Expected,
			"suggestion": fmt.Sprintf("Read the current state with %s, revise the plan and confirm it again "+
				"before retrying", readTool),
		},
	}, "", "  ")
	return tools.ErrorResult(string(b)), true
}
