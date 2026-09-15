// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// UAT 2026-09-13 D-85: agent-door tool_call audit entries carried only
// tool / decision / duration_ms / agent_id — no path, note id, property or
// collection — so an auditor could prove THAT a knowledge_edit was allowed
// but never reconstruct WHICH note changed or how. The web door's
// knowledge.note.write events carried all of that. SalientToolArgs closes
// the asymmetry without turning the audit log into a transcript: it copies
// only the argument keys that IDENTIFY what a call acted on (a path, an id,
// an operation, a property name), never the content it wrote.

// salientToolArgKeys is the closed allowlist of argument keys copied into a
// tool_call audit entry. A key is on this list because it answers "what did
// the call touch" — a location, an identity or an operation — and never
// because it carries the payload. A URL-shaped value under ANY of these keys
// is logged without its userinfo, query string and fragment (salientString). Free-text keys (content, text, body,
// value, message, command, query) are deliberately absent: bash already
// audits its command on its own exec event, and everything else is content.
var salientToolArgKeys = []string{
	"action", "op", "operation", "mode", "kind",
	"path", "paths", "file", "file_path", "target", "target_path", "from", "to", "destination",
	"source", "dest", "cwd", "host_path", "url",
	"collection", "collection_id", "base", "vault", "workspace_id", "workspace",
	"id", "note", "note_id", "record", "record_id", "record_type", "type",
	"property", "properties", "key", "field", "name", "label", "view", "view_id", "heading", "section",
	"agent_id", "target_agent_id", "session_id", "task_id", "skill", "tool", "names",
}

// salientToolArgMaxValueLen bounds every copied string so a path-shaped key
// that happens to carry a document cannot bloat the entry.
const salientToolArgMaxValueLen = 256

// salientToolArgMaxListLen bounds a list value (paths, names, properties).
const salientToolArgMaxListLen = 16

// SalientToolArgs returns the identifying subset of a tool call's arguments
// for its tool_call audit entry: allowlisted keys only (salientToolArgKeys),
// scalar values and short lists of scalars only, every string truncated to
// salientToolArgMaxValueLen, secret-looking keys redacted by the same rule
// ArgsPreview uses. Returns nil when nothing qualifies, so callers can omit
// the field rather than write an empty object.
//
// The tool name is accepted for future per-tool refinement and is not used
// to widen the allowlist today: one list, one rule, no per-tool surprises.
func SalientToolArgs(_ string, args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any)
	for _, k := range salientToolArgKeys {
		v, ok := args[k]
		if !ok || v == nil {
			continue
		}
		if secretKeyPattern.MatchString(k) {
			out[k] = "<redacted>"
			continue
		}
		if sv, ok := salientScalar(v); ok {
			out[k] = sv
			continue
		}
		if list, ok := v.([]any); ok {
			items := make([]any, 0, len(list))
			for _, item := range list {
				if sv, ok := salientScalar(item); ok {
					items = append(items, sv)
				}
				if len(items) == salientToolArgMaxListLen {
					break
				}
			}
			if len(items) > 0 {
				out[k] = items
			}
			continue
		}
		if strs, ok := v.([]string); ok {
			items := make([]any, 0, len(strs))
			for _, s := range strs {
				if sv, ok := salientString(s); ok {
					items = append(items, sv)
				}
				if len(items) == salientToolArgMaxListLen {
					break
				}
			}
			if len(items) > 0 {
				out[k] = items
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// salientScalar accepts strings, numbers and booleans; anything structured
// is refused so a nested object never reaches the audit log through a
// location-shaped key. A string that is a URL is reduced to its
// credential-free form first (salientString).
func salientScalar(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		return salientString(x)
	case bool, int, int32, int64, float32, float64:
		return x, true
	default:
		return nil, false
	}
}

// salientURLPattern recognises a string that starts like an absolute URL
// (scheme followed by "://"). A plain path such as "notes/a.md?x=1" does
// not match and is kept as written — the question mark in a filename is not
// a query string.
var salientURLPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://`)

// salientString prepares one string value for the audit entry. Codex review
// 2026-09-14 finding #10: the allowlist copies `url` (and any other
// location-shaped key that may hold a URL) into a DURABLE record, and a URL
// routinely carries its credential in the query string
// (?access_token=…, a signed S3 URL's X-Amz-Signature=…) or in the
// authority (user:password@host). The redactor only looks at KEY names, so
// none of that was caught. Every URL-shaped value is therefore reduced to
// scheme + host + path before it is logged: userinfo, query and fragment
// are dropped. That keeps what the call touched (which host, which
// resource) and loses only the part that could authenticate someone else.
// A URL that does not parse cannot be sanitised and is dropped outright
// (ok=false) rather than logged as-is.
//
// Claude review 2026-09-14 (cut-list): a bearer-token-shaped VALUE under an
// innocent allowlisted key (`key`, `id`, `name`, `target`) is redacted here
// too, by the same bearerTokenValuePattern ArgsPreview applies — the salient
// copy used to skip it and log the token at up to 256 bytes where the old
// surface redacted it and capped at 32. Redaction runs BEFORE the URL
// reduction and the length cap so neither can carry the token through.
func salientString(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if bearerTokenValuePattern.MatchString(s) {
		return "<redacted>", true
	}
	if salientURLPattern.MatchString(s) {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		s = u.String()
	}
	return truncateSalient(s), true
}

// truncateSalient caps s at salientToolArgMaxValueLen BYTES on a UTF-8 rune
// boundary by delegating to truncate (argshash.go) — the same rune-safe cut
// ArgsPreview has always used. A raw byte slice here split multi-byte runes
// in half and wrote mojibake into durable audit records (Claude review
// 2026-09-14 cut-list).
func truncateSalient(s string) string {
	return truncate(strings.TrimSpace(s), salientToolArgMaxValueLen)
}

// SalientToolArgsSummary renders the salient arguments as one stable,
// sorted "k=v" line for log or transcript use.
func SalientToolArgsSummary(tool string, args map[string]any) string {
	m := SalientToolArgs(tool, args)
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, " ")
}
