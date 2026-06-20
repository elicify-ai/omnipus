// runner_egress.go — external-runner CLI egress allowlist (Spec-4 FR-5.3).
//
// Per ADR-019 FR-5.3 (operator decision): NO new confiner primitive — external
// CLIs (claude-code / codex / opencode) self-sandbox (their own Landlock/seccomp
// or permission model). Omnipus's boundary on the runner is NETWORK EGRESS
// CONTROL: a loopback egress proxy with SSRF internal-CIDR blocking is injected
// into the child environment via HTTP_PROXY/HTTPS_PROXY so the CLI's HTTP/HTTPS
// traffic is forced through it.
//
// This file wires the proxy into the dispatch path. The proxy itself (with its
// SSRF filter) lives in pkg/sandbox/egress_proxy.go (NewRunnerEgressProxy).

package agent

import (
	"log/slog"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// runnerEgressProxyAddr lazily starts (on first external-CLI dispatch) and
// caches a dedicated egress proxy for external-runner children. Returns the
// loopback address the child should target via HTTP_PROXY/HTTPS_PROXY.
//
// Graceful degradation: if the proxy cannot start, returns "" and logs a warning.
// The dispatch path treats an empty address as "no proxy injection" and proceeds
// — the run is not aborted (FR-5.3 egress control is defense-in-depth on top of
// the CLI's own sandbox + the git-worktree FS boundary, not a hard gate).
//
// The proxy is cached for the AgentLoop's lifetime (one loopback listener). It
// is intentionally NOT closed on dispatch completion; closing would force a
// re-bind on the next dispatch and race in-flight requests from concurrent
// runs. The listener cost is negligible (one goroutine).
func (al *AgentLoop) runnerEgressProxyAddr() string {
	al.runnerEgressProxyOnce.Do(func() {
		// No host allow-list: external CLIs need broad egress to reach their
		// model providers. SSRF internal-CIDR blocking (NewRunnerEgressProxy
		// turns it on unconditionally) is the security-relevant gate.
		p, err := sandbox.NewRunnerEgressProxy(nil, al.runnerEgressAuditFn())
		if err != nil {
			slog.Warn("external-cli: runner egress proxy start failed; dispatch will run without HTTP_PROXY injection",
				"error", err)
			al.runnerEgressProxy = nil
			// Audit the degradation so operators have a record that egress
			// control is inactive for subsequent external-CLI dispatches.
			if al.auditLogger != nil {
				_ = al.auditLogger.Log(&audit.Entry{
					Event:    audit.EventSSRF,
					Decision: audit.DecisionError,
					Details: map[string]any{
						"reason": "egress proxy start failed",
						"impact": "external CLI dispatch runs without HTTP_PROXY injection (SSRF internal-CIDR blocking inactive)",
						"error":  err.Error(),
					},
				})
			}
			return
		}
		al.runnerEgressProxy = p
		slog.Info("external-cli: runner egress proxy started",
			"addr", p.Addr(), "ssrf", p.SSRFEnabled())
	})
	if al.runnerEgressProxy == nil {
		return ""
	}
	return al.runnerEgressProxy.Addr()
}

// runnerEgressAuditFn returns an EgressAuditFunc that routes proxy denials /
// SSRF blocks / upstream errors into the agent loop's audit logger when one is
// wired, or nil (so the proxy falls back to slog) when no audit logger exists.
func (al *AgentLoop) runnerEgressAuditFn() sandbox.EgressAuditFunc {
	if al.auditLogger == nil {
		return nil
	}
	return func(entry *audit.Entry) {
		if entry == nil {
			return
		}
		if err := al.auditLogger.Log(entry); err != nil {
			slog.Warn("external-cli: runner egress audit log failed",
				"event", entry.Event, "error", err)
		}
	}
}

// injectRunnerEgressProxy appends HTTP_PROXY / HTTPS_PROXY (both upper- and
// lower-case, matching the hardened-exec convention so npm/node/curl/Go all
// honor them) pointing at the runner egress proxy, plus a NO_PROXY that keeps
// loopback traffic direct. An empty proxyAddr is a no-op (graceful degradation
// — the proxy could not start). Any pre-existing proxy vars in env are stripped
// first so the injected values are authoritative (prevents a leaked operator
// proxy from shadowing the filtered one).
func injectRunnerEgressProxy(env []string, proxyAddr string) []string {
	if proxyAddr == "" {
		return env
	}
	proxyURL := "http://" + proxyAddr
	// Strip any pre-existing proxy vars so the injected values win.
	out := make([]string, 0, len(env)+6)
	for _, kv := range env {
		if isProxyEnvKey(envKey(kv)) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out,
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
		// NO_PROXY for loopback so the CLI's own loopback connections
		// (e.g. dev-server bind probes) don't bounce off the proxy.
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	)
	return out
}

// envKey extracts the key portion of a "KEY=VALUE" env entry. Returns "" for
// malformed entries (no '=' or '=' at index 0).
func envKey(kv string) string {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			if i == 0 {
				return ""
			}
			return kv[:i]
		}
	}
	return ""
}

// isProxyEnvKey reports whether name is one of the standard HTTP proxy env vars
// that injectRunnerEgressProxy replaces. Both cases are covered since tools
// honor either form.
func isProxyEnvKey(name string) bool {
	switch name {
	case "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy",
		"NO_PROXY", "no_proxy":
		return true
	}
	return false
}
