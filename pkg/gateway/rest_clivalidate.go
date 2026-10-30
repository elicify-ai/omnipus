// POST /api/v1/system/cli-validate — stateless, create-time validation that an
// external-executor CLI actually runs at a caller-supplied path (ADR-030 §11,
// spec FR-006/FR-012/FR-013/FR-014/FR-015/FR-017/FR-018).
//
// SECURITY POSTURE (why this file is owned by the security role):
// this endpoint SPAWNS a caller-supplied path (`<cli> --version`). Unlike the
// read-only cli-detect probe, it is therefore hardened as a privileged
// diagnostic (F-01, CRITICAL):
//
//   - Auth: a signed-in person only (T11 spec §3.2, ADR-030 §11 as amended
//     2026-10-29): withAuth then middleware.RequirePersonSession, which
//     answers 403 before the body is read for CLI-token, development-bypass
//     and user-less requests. Wired in registerAdditionalEndpoints.
//   - Rate limit: a DEDICATED per-IP limiter (cliValidateLimiter), tighter than
//     the read-only endpoints, applied via withRateLimit at registration.
//   - Concurrency: a per-caller in-flight cap (cliValidateInflight) so a single
//     principal cannot fan out unbounded concurrent spawns (self-DoS). It is
//     per-caller, not a global slot — a global cap would let one caller starve
//     everyone else.
//   - Pre-spawn guard: the shared program-file check
//     (clitrust.CheckProgramPath against clitrust.AgentWritableRoots) must
//     accept the target, else it is classified path-not-allowed WITHOUT a
//     spawn; the checked resolved file is the one launched. An empty path
//     short-circuits to missing-binary (never a silent $PATH fallback —
//     FR-014). Per the operator decision there is NO basename/identity check —
//     a runnable binary that returns a version passes even if mis-pointed
//     (F-03).
//   - Audit: exactly one audit event {cli, resolved_path, reason} per call
//     (EventCliValidate), including the no-spawn classifications.
//   - Response hygiene: `detail` is a fixed classified message per reason — NEVER
//     raw stderr from the spawned process (FR-017). The one non-fixed detail is
//     path-not-allowed's, which is the program-file check's own message (a
//     path and the failed check, produced before anything runs), so process
//     output still cannot be smuggled through the response.

package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/clitrust"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// cliValidateInflight caps concurrent in-flight validations PER CALLER (not a
// single global slot). Keyed by authenticated username when present, else client
// IP. A caller already at the cap is rejected fast with 429 rather than queued —
// no silent wait, no unbounded fan-out of caller-supplied spawns.
var cliValidateInflight = newInflightLimiter(2)

// cliTestConnection performs the actual spawn-based connection test on the
// checked program file. It is a package var so tests can inject a
// deterministic / blocking stand-in. In production it is
// runner.TestConnectionCheckedFile, which runs exactly the checked path bytes
// (no trimming, no PATH lookup) — T11 code review R2-C1.
var cliTestConnection = runner.TestConnectionCheckedFile

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

	// Trim leading/trailing whitespace on the path (D-3.5) before classifying,
	// but keep the RAW requested value for the audit record (M-1 — forensics need
	// exactly what the caller asked for, including a no-spawn classification).
	rawPath := req.CliPath
	resp := a.validateCLI(r.Context(), string(req.Cli), strings.TrimSpace(rawPath))

	// Audit exactly one event per call (FR-013 / M-1), including the no-spawn
	// classifications (unknown-cli / empty / non-regular). The authenticated
	// caller is attributed and the raw requested path is recorded even when no
	// spawn occurred. Best-effort: an audit write failure must NEVER fail the
	// request (never crash on audit).
	a.auditCliValidate(r, string(req.Cli), rawPath, resp)

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
	//    fallback (FR-014). This MUST stay before the LookPath resolve below so an
	//    empty path never triggers a $PATH search for the default binary name.
	if cliPath == "" {
		return buildCliValidateResponse(gen.CliValidateResponseReasonMissingBinary, nil, nil)
	}
	// 3. Shared program-file check BEFORE any spawn (T11 spec §3.2, FR-5): an
	//    absolute path whose link chain and final file stay out of the
	//    agent-writable roots, are not writable by others, and end at a regular
	//    executable. A refusal is path-not-allowed and starts nothing. A bare
	//    name is refused (not absolute) rather than resolved on $PATH.
	roots, rootsErr := a.cliTrustForbiddenRoots(ctx)
	if rootsErr != nil {
		// Fail closed: without the complete agent-writable set the location
		// check cannot be trusted, so nothing is started.
		slog.Warn("cli-validate: agent-writable folders could not be listed; refusing the candidate",
			"error", rootsErr)
		return pathNotAllowedResponse(rootsErr)
	}
	finalFile, checkErr := clitrust.CheckProgramPath(cliPath, roots)
	if checkErr != nil {
		return pathNotAllowedResponse(checkErr)
	}
	// 4. Delegate to the shared connection test AS-IS (no identity extension),
	//    on exactly the checked resolved file — never a second resolution.
	res := cliTestConnection(ctx, cli, finalFile)
	reason := mapValidateReason(res.Reason)

	var resolved, version *string
	switch reason {
	case gen.CliValidateResponseReasonOk, gen.CliValidateResponseReasonUnauthenticated:
		resolved = strPtr(finalFile)
		if res.CLIVersion != "" {
			version = strPtr(res.CLIVersion)
		}
	case gen.CliValidateResponseReasonHandshakeFailed:
		// A target was found and spawned but did not report a version — surface
		// the resolved path (no version).
		resolved = strPtr(finalFile)
	case gen.CliValidateResponseReasonMissingBinary, gen.CliValidateResponseReasonUnknownCli,
		gen.CliValidateResponseReasonPathNotAllowed:
		// No binary was found/spawned for these reasons, so there is nothing
		// to resolve — resolved and version stay nil.
	}
	return buildCliValidateResponse(reason, resolved, version)
}

// cliTrustForbiddenRoots is the forbidden list for clitrust.CheckProgramPath:
// the Omnipus home, every workspace folder and mount root, and configured
// agent working folders. The request's config snapshot is preferred; the
// live config is the fallback. An error means the list is incomplete and the
// caller must refuse.
func (a *restAPI) cliTrustForbiddenRoots(ctx context.Context) ([]string, error) {
	cfg := configFromContext(ctx)
	if cfg == nil && a.agentLoop != nil {
		cfg = a.agentLoop.GetConfig()
	}
	home := a.homePath
	if home == "" {
		home = config.OmnipusHomeDir()
	}
	return clitrust.AgentWritableRoots(home, cfg)
}

// pathNotAllowedResponse is the path-not-allowed classification: not ok,
// nothing resolved or run, and the check's own message naming the failed
// check (spec §3.2 C-A2). The message comes from a filesystem check made
// before any spawn, never from process output.
func pathNotAllowedResponse(checkErr error) gen.CliValidateResponse {
	resp := buildCliValidateResponse(gen.CliValidateResponseReasonPathNotAllowed, nil, nil)
	resp.Detail = strPtr("not allowed: " + checkErr.Error())
	return resp
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

// requireSupportedCLI validates that cli is one of the recognized external
// CLIs (isSupportedCLI). On failure it writes the shared 400 response and
// returns false so the caller can `return` immediately; on success it writes
// nothing and returns true. Shared by POST /agents/executor-preview
// (rest_executor_preview.go) and POST /agents/executor-smoke-test
// (rest_executor_smoketest.go), which previously each duplicated the
// identical isSupportedCLI-then-jsonErr block.
func requireSupportedCLI(w http.ResponseWriter, cli string) bool {
	if isSupportedCLI(cli) {
		return true
	}
	jsonErr(w, http.StatusBadRequest,
		fmt.Sprintf("cli %q is not supported (valid: claude-code, codex, opencode)", cli))
	return false
}

// derefTrimStr returns the whitespace-trimmed value of an optional string
// field, or "" when the pointer is nil. Shared by
// POST /agents/executor-preview and POST /agents/executor-smoke-test for
// their identical model/cli_path optional-field extraction (both previously
// duplicated the same nil-check-then-TrimSpace block). Mirrors derefStr
// (schedules.go) but additionally trims, matching what both endpoints have
// always done for these two fields.
func derefTrimStr(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
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
	case gen.CliValidateResponseReasonPathNotAllowed:
		return "not allowed"
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

// cliValidateAuditUser returns the authenticated principal's username for audit
// attribution (M-1), from the same UserContextKey source cliValidateCallerKey
// reads. It returns "" for the dev-bypass / env-token paths that carry no user
// context — an empty User is the documented, never-guessed default (audit.Entry).
func cliValidateAuditUser(r *http.Request) string {
	if u, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && u != nil {
		return u.Username
	}
	return ""
}

// auditCliValidate emits exactly one audit event per validate call carrying the
// authenticated caller (Entry.User — M-1), the RAW requested path, the resolved
// path, and the reason (FR-013). requested_path is recorded even for no-spawn
// classifications (unknown-cli / empty / non-regular) where resolved_path is
// empty, so an audit reviewer can always see exactly what was asked for.
// Best-effort: a nil auditor or a write error is logged and swallowed — audit
// failure must never fail the diagnostic (never crash on audit write).
func (a *restAPI) auditCliValidate(r *http.Request, cli, requestedPath string, resp gen.CliValidateResponse) {
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
		User:     cliValidateAuditUser(r),
		Details: map[string]any{
			"cli":            cli,
			"requested_path": requestedPath,
			"resolved_path":  resolved,
			"reason":         string(resp.Reason),
		},
	}); err != nil {
		slog.Warn("audit write failed", "event", audit.EventCliValidate, "error", err)
	}
}
