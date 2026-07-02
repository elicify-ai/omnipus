//go:build !cgo

// POST /api/v1/system/cli-validate — stateless, create-time validation that an
// external-executor CLI actually runs at a caller-supplied path (ADR-030 §11,
// spec FR-006/FR-012/FR-013/FR-014/FR-015/FR-017/FR-018).
//
// SECURITY POSTURE (why this file is owned by the security role):
// this endpoint SPAWNS a caller-supplied path (`<cli> --version`). Unlike the
// read-only cli-detect probe, it is therefore hardened as a privileged
// diagnostic (F-01, CRITICAL):
//
//   - Auth: withAuth at CREATE-PARITY — the SAME gating as createAgent (plain
//     withAuth, NOT admin, NOT RequireNotBypass). Anyone who can add the
//     subagent can validate it; wired in registerAdditionalEndpoints.
//   - Rate limit: a DEDICATED per-IP limiter (cliValidateLimiter), tighter than
//     the read-only endpoints, applied via withRateLimit at registration.
//   - Concurrency: a per-caller in-flight cap (cliValidateInflight) so a single
//     principal cannot fan out unbounded concurrent spawns (self-DoS). It is
//     per-caller, not a global slot — a global cap would let one caller starve
//     everyone else.
//   - Pre-spawn guard: the target MUST be a regular, executable file, else it is
//     classified missing-binary WITHOUT a spawn. An empty path short-circuits to
//     missing-binary (never a silent $PATH fallback — FR-014). Per the operator
//     decision there is NO basename/identity check — a runnable binary that
//     returns a version passes even if mis-pointed (F-03).
//   - Audit: exactly one audit event {cli, resolved_path, reason} per call
//     (EventCliValidate), including the no-spawn classifications.
//   - Response hygiene: `detail` is a fixed classified message per reason — NEVER
//     raw stderr from the spawned process (FR-017). The classified-message set is
//     closed, so process output cannot be smuggled through the response.

package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// cliValidateInflight caps concurrent in-flight validations PER CALLER (not a
// single global slot). Keyed by authenticated username when present, else client
// IP. A caller already at the cap is rejected fast with 429 rather than queued —
// no silent wait, no unbounded fan-out of caller-supplied spawns.
var cliValidateInflight = newInflightLimiter(2)

// inflightLimiter is a per-key bounded counter used to cap concurrent in-flight
// operations for one caller.
type inflightLimiter struct {
	mu    sync.Mutex
	inUse map[string]int
	limit int
}

func newInflightLimiter(limitPerCaller int) *inflightLimiter {
	return &inflightLimiter{inUse: make(map[string]int), limit: limitPerCaller}
}

// acquire reserves one in-flight slot for key, returning false when the caller
// is already at the cap. A successful acquire MUST be paired with a release.
func (l *inflightLimiter) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inUse[key] >= l.limit {
		return false
	}
	l.inUse[key]++
	return true
}

// release returns one slot for key, deleting the map entry at zero so the map
// does not grow unbounded across distinct callers.
func (l *inflightLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inUse[key] <= 1 {
		delete(l.inUse, key)
		return
	}
	l.inUse[key]--
}

// cliValidateCallerKey identifies the caller for the per-caller in-flight cap.
// The authenticated principal is preferred so the cap is per-user; it falls back
// to client IP for the dev-bypass / env-token paths that carry no user context.
func cliValidateCallerKey(r *http.Request) string {
	if u, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && u != nil && u.Username != "" {
		return "user:" + u.Username
	}
	return "ip:" + clientIP(r)
}

// HandleSystemCliValidate handles POST /api/v1/system/cli-validate.
//
// It decodes {cli, cli_path}, enforces the per-caller in-flight cap, classifies
// the target (delegating the actual spawn to runner.TestConnectionWithPath,
// reused as-is), audits the call, and returns the classified CliValidateResponse.
func (a *restAPI) HandleSystemCliValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req gen.CliValidateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "CliValidateRequest", &req, validateEnabled) {
		return
	}

	// Per-caller in-flight cap (FR-013). Acquire BEFORE any spawn; a caller
	// already at the cap is rejected fast (no queue, no silent wait).
	key := cliValidateCallerKey(r)
	if !cliValidateInflight.acquire(key) {
		w.Header().Set("Retry-After", "1")
		jsonErr(w, http.StatusTooManyRequests, "too many concurrent validations in flight; retry shortly")
		return
	}
	defer cliValidateInflight.release(key)

	// Trim leading/trailing whitespace on the path (D-3.5) before classifying.
	resp := a.validateCLI(r.Context(), string(req.Cli), strings.TrimSpace(req.CliPath))

	// Audit exactly one event per call (FR-013), including the no-spawn
	// classifications (unknown-cli / empty / non-regular). Best-effort: an audit
	// write failure must NEVER fail the request (never crash on audit).
	a.auditCliValidate(string(req.Cli), resp)

	jsonOK(w, resp)
}

// validateCLI performs the classification without any HTTP concern so it is
// directly unit-testable. Order matters and is fail-closed: an unknown cli and
// an empty / non-regular / non-executable target are rejected WITHOUT a spawn;
// only a supported cli pointing at a real regular executable file reaches
// runner.TestConnectionWithPath.
func (a *restAPI) validateCLI(ctx context.Context, cli, cliPath string) gen.CliValidateResponse {
	// 1. Unknown cli — no spawn (FR-012).
	if !isSupportedCLI(cli) {
		return buildCliValidateResponse(gen.CliValidateResponseReasonUnknownCli, nil, nil)
	}
	// 2. Empty path — short-circuit to missing-binary; NEVER a silent $PATH
	//    fallback (FR-014).
	if cliPath == "" {
		return buildCliValidateResponse(gen.CliValidateResponseReasonMissingBinary, nil, nil)
	}
	// 3. Pre-spawn target constraint (FR-013): must be a regular, executable
	//    file. No basename / identity check (operator decision — F-03).
	if !isRegularExecutableFile(cliPath) {
		return buildCliValidateResponse(gen.CliValidateResponseReasonMissingBinary, nil, nil)
	}
	// 4. Delegate to the shared connection test AS-IS (no identity extension).
	res := runner.TestConnectionWithPath(ctx, cli, cliPath)
	reason := mapValidateReason(res.Reason)

	var resolved, version *string
	switch reason {
	case gen.CliValidateResponseReasonOk, gen.CliValidateResponseReasonUnauthenticated:
		resolved = strPtr(absResolvePath(cliPath))
		if res.CLIVersion != "" {
			version = strPtr(res.CLIVersion)
		}
	case gen.CliValidateResponseReasonHandshakeFailed:
		// A target was found and spawned but did not report a version — surface
		// the resolved path (no version).
		resolved = strPtr(absResolvePath(cliPath))
	}
	return buildCliValidateResponse(reason, resolved, version)
}

// isSupportedCLI reports whether cli is one of the recognized external CLIs.
// Reuses runner.SupportedCLIs (the authoritative set) — no parallel list.
func isSupportedCLI(cli string) bool {
	for _, s := range runner.SupportedCLIs() {
		if s == cli {
			return true
		}
	}
	return false
}

// isRegularExecutableFile reports whether p is a regular file the OS would
// execute. Symlinks are followed (os.Stat). On Windows the execute bit is not
// meaningful, so a regular file suffices; elsewhere at least one execute bit must
// be set. A directory, socket, device, or missing path returns false — the
// caller then classifies missing-binary WITHOUT a spawn.
func isRegularExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	if !fi.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode().Perm()&0o111 != 0
}

// absResolvePath returns the absolute, best-effort symlink-resolved form of p
// for the resolved_path response field. EvalSymlinks failure falls back to Abs
// of the raw path so a real target is never dropped.
func absResolvePath(p string) string {
	resolved := p
	if ev, err := filepath.EvalSymlinks(p); err == nil && ev != "" {
		resolved = ev
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		return abs
	}
	return resolved
}

// mapValidateReason maps runner.FailureReason to the wire enum, mapping
// runner.ReasonOK (the empty string) to "ok" (FR-018). An unrecognized reason
// fails CLOSED to handshake-failed (which blocks Create) rather than silently
// reporting "ok".
func mapValidateReason(r runner.FailureReason) gen.CliValidateResponseReason {
	switch r {
	case runner.ReasonOK:
		return gen.CliValidateResponseReasonOk
	case runner.ReasonMissingBinary:
		return gen.CliValidateResponseReasonMissingBinary
	case runner.ReasonHandshakeFailed:
		return gen.CliValidateResponseReasonHandshakeFailed
	case runner.ReasonUnauthenticated:
		return gen.CliValidateResponseReasonUnauthenticated
	case runner.ReasonUnknownCLI:
		return gen.CliValidateResponseReasonUnknownCli
	default:
		return gen.CliValidateResponseReasonHandshakeFailed
	}
}

// reasonReportsOK returns the `ok` convenience boolean: true for a binary that
// runs and reports a version (ok OR unauthenticated). Clients MUST gate on
// `reason`, not this field (G2-07) — unauthenticated is ok=true but non-"ok".
func reasonReportsOK(reason gen.CliValidateResponseReason) bool {
	return reason == gen.CliValidateResponseReasonOk ||
		reason == gen.CliValidateResponseReasonUnauthenticated
}

// classifiedDetail returns the FIXED, allowlisted human message for a reason. It
// is NEVER raw stderr from the spawned process (FR-017) — the mapping is a closed
// set, so an attacker cannot smuggle process output through the detail field.
func classifiedDetail(reason gen.CliValidateResponseReason) string {
	switch reason {
	case gen.CliValidateResponseReasonOk:
		return "OK"
	case gen.CliValidateResponseReasonUnauthenticated:
		return "installed; not logged in"
	case gen.CliValidateResponseReasonMissingBinary:
		return "not found"
	case gen.CliValidateResponseReasonHandshakeFailed:
		return "did not run or returned no version"
	case gen.CliValidateResponseReasonUnknownCli:
		return "unsupported cli"
	default:
		return "validation failed"
	}
}

// buildCliValidateResponse assembles the wire response with the fixed detail and
// the reason-derived ok boolean.
func buildCliValidateResponse(reason gen.CliValidateResponseReason, resolved, version *string) gen.CliValidateResponse {
	return gen.CliValidateResponse{
		Ok:           reasonReportsOK(reason),
		Reason:       reason,
		ResolvedPath: resolved,
		Version:      version,
		Detail:       strPtr(classifiedDetail(reason)),
	}
}

// auditCliValidate emits exactly one audit event per validate call with the
// {cli, resolved_path, reason} triple (FR-013). Best-effort: a nil auditor or a
// write error is logged and swallowed — audit failure must never fail the
// diagnostic (never crash on audit write).
func (a *restAPI) auditCliValidate(cli string, resp gen.CliValidateResponse) {
	if a.auditor == nil {
		return
	}
	resolved := ""
	if resp.ResolvedPath != nil {
		resolved = *resp.ResolvedPath
	}
	decision := audit.DecisionDeny
	if reasonReportsOK(resp.Reason) {
		decision = audit.DecisionAllow
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    audit.EventCliValidate,
		Decision: decision,
		Details: map[string]any{
			"cli":           cli,
			"resolved_path": resolved,
			"reason":        string(resp.Reason),
		},
	}); err != nil {
		slog.Warn("audit write failed", "event", audit.EventCliValidate, "error", err)
	}
}
