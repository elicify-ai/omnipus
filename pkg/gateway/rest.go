// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/channels"
	whatsappnative "github.com/elicify-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/elicify-ai/omnipus/pkg/clidetect"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/mcp"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/plan"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	wkspace "github.com/elicify-ai/omnipus/pkg/workspace"
)

// Version is set at build time via -ldflags "-X github.com/elicify-ai/omnipus/pkg/gateway.Version=x.y.z".
// Dev builds default to a semver-compatible string so the /version endpoint still
// passes the contract schema used by the SPA.
var Version = "0.0.0-dev"

// errConflict is the sentinel returned from a safeUpdateConfigJSON mutate
// closure when an optimistic-concurrency check fails inside the configMu
// lock (closing the TOCTOU race that existed when the check ran against the
// in-memory cached config outside the lock). Callers test with errors.Is
// and map it to HTTP 409.
var errConflict = errors.New("optimistic concurrency conflict")

// errAgentVanishedDuringUpdate is the sentinel a persist closure returns when
// its fresh, ID-based lookup against the on-disk agents list (read inside
// a.configMu, at the top of updateConfigJSONLocked) fails to find the target
// agent — e.g. a concurrent DELETE /agents/{id} raced this PUT in the window
// between the handler's fast-path existence check (run before the lock, on a
// config snapshot that can go stale) and the locked critical section actually
// running. Silently returning nil in that case would be a phantom-200: the
// caller's PUT reports success for an update that touched nothing because the
// target no longer exists. Callers test with errors.Is and map it to HTTP 404.
var errAgentVanishedDuringUpdate = errors.New("agent no longer exists")

// restAPI holds shared dependencies for all REST endpoint handlers.
// Handlers are registered as method-dispatching http.HandlerFuncs in gateway.go.
// Note: do NOT cache *config.Config here — use a.agentLoop.GetConfig() for
// the current config, since config can hot-reload.
type restAPI struct {
	agentLoop     *agent.AgentLoop
	allowedOrigin string
	onboardingMgr *onboarding.Manager // manages first-launch + doctor state
	homePath      string              // ~/.omnipus — root of the data directory
	configMu      sync.Mutex          // guards safeUpdateConfigJSON (read-modify-write cycle)
	taskStore     *task.Store         // unified task persistence
	taskExecutor  *agent.TaskExecutor // task execution engine
	// planStore is the Plan entity persistence (ADR-049 D1, pkg/plan), shared
	// with the pkg/agent PlanEngine (both hold the SAME *plan.Store instance,
	// constructed once at boot — setupAndStartServices). Nil in test setups
	// that do not exercise the Plans REST surface; handlers in rest_plans.go
	// and the plan_id FK check in rest_tasks.go fail closed (503/400) rather
	// than silently skipping validation when nil (mirrors
	// errTaskAgentLoopUnavailable's fail-closed convention).
	planStore  *plan.Store
	credStore  *credentials.Store // shared unlocked credential store (injected at boot)
	mediaStore media.MediaStore   // shared media store for serving media files
	// providerCatalog is the ADR-067 registry-fed provider catalog. It is the
	// only authority on whether a configured provider id is KNOWN: a
	// configured row whose id the served document does not contain is
	// reported as status=unknown-provider with the generic text
	// `unknown provider "<id>"` (ADR-068 FR-043 / ADR-067 FR-016). Nil, or
	// non-nil with no document loaded (the E7 "boots with no catalog" state),
	// classifies nothing — rows keep their credential-derived status. Wired
	// at boot by T067-10; until then only tests set it.
	providerCatalog *catalog.Catalog
	// ssrfChecker enforces SEC-24 SSRF protection on outbound HTTP requests made
	// by REST handlers (skills installer). Nil when SSRF protection is disabled
	// in config (sandbox.ssrf.enabled = false). Shared with the agent loop's
	// singleton so allow_internal is honored consistently across all surfaces.
	ssrfChecker *security.SSRFChecker
	// sandboxResult captures the Sprint-J apply outcome (mode, backend,
	// applied state). Immutable after boot — FR-J-015 forbids hot-reload
	// of sandbox config. HandleSandboxStatus reads this to enrich the
	// response with mode and disabled_by.
	sandboxResult *SandboxApplyResult
	// appliedConfig is a deep copy of the config that was active when the
	// gateway process started. It is set once during boot (setupAndStartServices)
	// and never mutated afterward. HandlePendingRestart compares this snapshot
	// against the current on-disk config to compute the set of restart-required
	// changes — keys that differ between persisted and applied represent changes
	// that will only take effect after a restart.
	appliedConfig *config.Config

	// devServers is the gateway-wide Tier 3 dev-server registry. Shared with
	// the web_serve tool (dev mode) and workspace.shell_bg tool via the agent
	// instance. HandlePreview reads this to validate tokens and resolve the
	// upstream loopback port. Nil when Tier 3 is not supported on the current
	// platform (non-Linux).
	devServers *sandbox.DevServerRegistry

	// servedSubdirs is the gateway-wide static-preview registration map.
	// Shared with the web_serve tool (static mode) via the agent instance.
	// HandlePreview reads this to validate tokens and resolve the served
	// directory. Nil when web_serve is not configured.
	servedSubdirs *agent.ServedSubdirs

	// approvalReg is the in-process tool-approval registry (FR-016, FR-070).
	// Injected at boot by the gateway; nil in test setups that do not exercise approvals.
	approvalReg *approvalRegistryV2

	// builtinRegistry is the central registry for builtin tools (M16, FR-001).
	// Populated at boot via BuiltinRegistry.RegisterBuiltin for all sysagent tools.
	// GET /api/v1/tools consults this as the authoritative supply-side source.
	// Nil in test setups that do not populate it; HandleToolsRegistry falls back
	// to the per-agent tool set when nil.
	builtinRegistry *tools.BuiltinRegistry

	// mcpRegistry is the central registry for MCP server tools (M16, FR-001).
	// Populated at runtime as MCP servers connect.
	// GET /api/v1/tools includes MCP entries from this registry.
	// Nil in test setups that do not wire MCP.
	mcpRegistry *tools.MCPRegistry

	// skillRegistry is the ClawHub marketplace registry backing GET
	// /api/v1/skills/search. Built at boot from cfg.Tools.Skills.Registries.ClawHub
	// with the SSRF-safe HTTP client (SEC-24). Nil in test setups (and when no
	// registry is configured); the search handler returns 502 when it is nil so
	// the SPA can surface "registry unavailable" rather than a hard 500.
	skillRegistry skills.SkillRegistry

	// allowGodMode is set when the gateway was started with --allow-god-mode
	// (or the config-persisted sandbox.god_mode_allowed grant). Combined with
	// sandbox.GodModeAvailable to compute god-mode AVAILABILITY for the
	// Settings UI (see (*restAPI).godModeAvailable in rest_god_mode.go).
	// Mirrors the same field on AgentLoop. Latch (2) — REST enforcement.
	allowGodMode bool

	// cronService backs the /api/v1/schedules CRUD + run-now + pause endpoints
	// (#264). Schedules are a contract-first projection over cron.CronJob.
	// Stored as an atomic pointer so restartServices can update it from a reload
	// goroutine without racing against concurrent HTTP schedule handler reads.
	// Nil/zero in test setups that do not exercise schedules.
	cronService atomic.Pointer[cron.CronService]

	// notifStore backs /api/v1/notifications (#264). Per-user, file-based.
	// Nil in test setups that do not exercise notifications.
	notifStore *notifications.Store

	// auditor is the shared audit logger for mutation events on workspaces and
	// board tasks. Sourced from agentLoop.AuditLogger() at construction time;
	// may be nil when audit logging is disabled (best-effort — nil-safe callers).
	auditor *audit.Logger

	// taskLock is the process-wide per-task-ID striped mutex shared by the REST
	// task handlers and the sysagent system.task.* tools. Both paths use
	// task.TaskFileLock (the package-level singleton), which is the same pointer
	// stored here. Holding the lock for the full read→mutate→write cycle prevents
	// a race between two concurrent mutations of the same task.
	taskLock *task.StripedLock

	// selfWriteReg is the registry of config.json content hashes written by
	// the app itself. safeUpdateConfigJSON registers each write here so the
	// config file watcher can suppress spurious full-service reloads on
	// app-initiated changes (logins, settings writes, channel config, etc.).
	// Nil in test setups that do not wire the config watcher.
	selfWriteReg *configSelfWriteRegistry

	// reauth is the in-memory store of short-lived password re-auth consent
	// tokens (Spec-6 FR-12.2). Minted by HandleReAuth, consumed (single-use) by
	// requireReAuth before a sensitive settings change. Distinct from
	// RequireNotBypass (a 503 dev-mode guard). Lazily initialized via
	// reauthStoreOrInit so test setups that construct restAPI literals without
	// this field still function.
	reauthOnce sync.Once
	reauth     *reauthStore

	// restarter performs the graceful self-restart triggered by
	// POST /api/v1/gateway/restart (O4-backend). It is an indirection so the
	// handler can be unit-tested without re-execing the test process — tests
	// inject a stub. Nil means "use the production re-exec path"
	// (gracefulSelfRestart), resolved lazily in HandleGatewayRestart.
	restarter func()

	// testForceFastUpsertErr is a test-only seam (same pattern as restarter
	// above): when non-nil, fastAgentUpsert (issue #571) reports this error
	// as if AgentLoop.UpsertAgentFast itself had failed, instead of calling
	// it, and proceeds straight to the full-reload fallback. Exercising a
	// genuine internal UpsertAgentFast failure (provider/registry state, a
	// lost optimistic-concurrency race) deterministically from a black-box
	// REST test is impractical; this lets a test simulate "the fast path
	// failed" so the fallback-and-warning behavior can still be verified end
	// to end (see TestCreateAgent_ReloadFailure_ReturnsWarning). Always nil
	// in production.
	testForceFastUpsertErr error
}

// ssrfChk returns the SSRF checker as a providers_pkg.URLChecker interface.
// When a.ssrfChecker is nil (SSRF globally disabled), it returns an explicit
// providers_pkg.NoopChecker — NOT a nil interface and NOT a non-nil interface
// wrapping a nil concrete pointer (the typed-nil trap, which would panic inside
// providers_pkg.ValidateKey / FetchModels on the nil-receiver method call). The
// NoopChecker makes "no SSRF guard" an explicit, type-safe value.
func (a *restAPI) ssrfChk() providers_pkg.URLChecker {
	if a.ssrfChecker == nil {
		return providers_pkg.NoopChecker{}
	}
	return a.ssrfChecker
}

// reauthStoreOrInit returns the lazily-initialized re-auth token store, creating
// it on first use. Safe for concurrent callers.
func (a *restAPI) reauthStoreOrInit() *reauthStore {
	a.reauthOnce.Do(func() {
		if a.reauth == nil {
			a.reauth = newReAuthStore()
		}
	})
	return a.reauth
}

// --- CORS / JSON helpers ---

func (a *restAPI) setCORSHeaders(w http.ResponseWriter, r ...*http.Request) {
	origin := a.allowedOrigin
	// isExplicitlyAllowed tracks whether the request origin matched the
	// configured allowedOrigin exactly. Localhost/loopback fallback does NOT
	// count — credentials are only sent for origins the operator explicitly
	// configured, preventing overly broad cookie sharing.
	isExplicitlyAllowed := false

	// Allow same-origin requests: if the request Origin matches the Host header,
	// reflect it so the SPA works when accessed via public IP.
	// Only reflect origins that are same-origin or localhost — never arbitrary origins.
	if len(r) > 0 && r[0] != nil {
		reqOrigin := r[0].Header.Get("Origin")
		if reqOrigin != "" && isAllowedOrigin(reqOrigin, r[0].Host, a.allowedOrigin) {
			origin = reqOrigin
			// Mark explicit only when the request origin matches the operator-configured
			// allowedOrigin directly (not merely localhost/same-host fallback).
			if a.allowedOrigin != "" && reqOrigin == a.allowedOrigin {
				isExplicitlyAllowed = true
			}
		}
	}
	// Never fall back to "*" — if no origin is configured and the request origin
	// is not localhost/same-origin, omit the header (browser will block the request).
	if origin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Csrf-Token, X-Reauth-Token")
	// Access-Control-Allow-Credentials must only be sent when the origin is
	// explicitly configured — never when falling back to wildcard or localhost
	// reflection. Per CORS spec, "true" + wildcard is illegal; restricting to
	// explicit origins is both correct and secure.
	if isExplicitlyAllowed {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
}

// isAllowedOrigin checks whether a request origin should be reflected in CORS headers.
// Allows: the configured origin, same-origin (host match), and localhost/127.0.0.1.
func isAllowedOrigin(reqOrigin, host, configuredOrigin string) bool {
	if configuredOrigin != "" && reqOrigin == configuredOrigin {
		return true
	}
	parsed, err := url.Parse(reqOrigin)
	if err != nil {
		return false
	}
	hostname := parsed.Hostname()
	originPort := parsed.Port()
	// Same-origin: request Origin hostname AND port must match the Host header.
	if host != "" {
		hostOnly := host
		hostPort := ""
		if h, p, err := net.SplitHostPort(host); err == nil {
			hostOnly = h
			hostPort = p
		}
		if hostname == hostOnly && originPort == hostPort {
			return true
		}
	}
	// Allow localhost and loopback for development.
	return hostname == "localhost" || hostname == "127.0.0.1"
}

func (a *restAPI) handlePreflight(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		a.setCORSHeaders(w, r)
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// withAuthAndBodyLimit wraps a handler with preflight, bearer auth, CORS headers,
// and the given request body size limit. This is the shared implementation used
// by withAuth (1 MB) and withUploadAuth (1 GB).
func (a *restAPI) withAuthAndBodyLimit(handler http.HandlerFunc, bodyLimit int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.handlePreflight(w, r) {
			return
		}
		// Prefer config snapshot from configSnapshotMiddleware (race-free during
		// hot-reload). Fall back to GetConfig() if middleware was not applied.
		cfg := configFromContext(r.Context())
		if cfg == nil {
			slog.Warn("configFromContext returned nil — configSnapshotMiddleware may not be applied")
			cfg = a.agentLoop.GetConfig()
		}
		result := checkBearerAuth(r.Context(), w, r, cfg)
		if !result.Authenticated {
			return
		}
		a.setCORSHeaders(w, r)
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		ctx := r.Context()
		if result.User != nil {
			ctx = context.WithValue(ctx, UserContextKey{}, result.User)
		}
		if result.ViaCLIToken {
			ctx = context.WithValue(ctx, CLITokenContextKey{}, true)
		}
		handler(w, r.WithContext(ctx))
	}
}

// withAuth wraps a handler with preflight, bearer auth, CORS header boilerplate,
// and a 1 MB request body size limit to prevent unbounded memory allocation.
func (a *restAPI) withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return a.withAuthAndBodyLimit(handler, 1<<20) // 1 MB
}

// adminWrap composes the canonical high-blast-radius middleware chain around h:
//
//	withAuth → RequireNotBypass → h
//
// Named "admin" for historical continuity with the pre-single-user RBAC model;
// under the single-account model it gates high-blast-radius endpoints (config,
// security settings, credentials, etc.) behind authentication plus the
// dev-mode-bypass guard, rather than an admin-vs-user role check. Exposed as a
// method so Sprint K admin-endpoint registrations outside
// registerAdditionalEndpoints can reuse the same chain verbatim, and so
// future refactors (e.g. adding a new gating middleware) update one site.
func (a *restAPI) adminWrap(h http.HandlerFunc) http.HandlerFunc {
	return a.withAuth(
		middleware.RequireNotBypass(h),
	)
}

// requireAdminAuthz composes ONLY the authorization layer (RequireNotBypass →
// h), WITHOUT re-running withAuth. Use this to gate a specific verb inside a
// handler that is ALREADY registered under withAuth (e.g. the shared
// /api/v1/channels dispatcher): withAuth has already verified the Bearer token
// and written the config snapshot into the request context, so wrapping the
// whole adminWrap chain again would double-authenticate (and, on a
// context-only test call, spuriously 401 on the re-auth). RequireNotBypass
// reads the config snapshot from context, which is present post-withAuth. It
// mirrors the authorization half of adminWrap so the two stay in lockstep.
func (a *restAPI) requireAdminAuthz(h http.HandlerFunc) http.HandlerFunc {
	return middleware.RequireNotBypass(h)
}

// writeJSON marshals body and writes it with the given status code. The
// Content-Type is set BEFORE WriteHeader so it is honored regardless of status
// — once WriteHeader is called the header map is flushed and later Set calls are
// silently ignored, which is how 201 responses were leaking out as text/plain
// (#96). All JSON-returning handlers must route through this (or jsonOK /
// jsonCreated, which wrap it) rather than calling w.WriteHeader themselves.
func writeJSON(w http.ResponseWriter, code int, body any) {
	buf, err := json.Marshal(body)
	if err != nil {
		slog.Error("rest: json encode failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(buf); err != nil {
		slog.Debug("rest: write response body failed", "error", err)
		return
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		slog.Debug("rest: write newline failed", "error", err)
	}
}

// jsonOK writes body as a 200 OK JSON response.
func jsonOK(w http.ResponseWriter, body any) { writeJSON(w, http.StatusOK, body) }

// setAgentModelProvider echoes the agent's explicit primary-model provider (O3
// two-field model) onto the wire response. A nil model or an empty provider
// leaves ag.Provider unset (absent on the wire), signaling default-provider
// resolution. Used by every agent response builder so create/list/get/update all
// round-trip the provider field consistently.
func setAgentModelProvider(ag *gen.Agent, model *config.AgentModelConfig) {
	if model == nil || model.Provider == "" {
		return
	}
	p := model.Provider
	ag.Provider = &p
}

// jsonCreated writes body as a 201 Created JSON response. Use this instead of
// `w.WriteHeader(http.StatusCreated); jsonOK(w, ...)` — that ordering drops the
// Content-Type and the response is served as text/plain (#96).
func jsonCreated(w http.ResponseWriter, body any) { writeJSON(w, http.StatusCreated, body) }

// jsonAccepted writes body as a 202 Accepted JSON response. Use for async
// operations that have been accepted and dispatched but not yet completed.
func jsonAccepted(w http.ResponseWriter, body any) { writeJSON(w, http.StatusAccepted, body) }

// jsonErr writes an ErrorResponse with the given status. Like writeJSON it sets
// Content-Type before WriteHeader so error bodies are never served as text/plain
// (#96). It does not call writeJSON to avoid recursion on a marshal failure.
func jsonErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(gen.ErrorResponse{Error: msg}); err != nil {
		slog.Debug("rest: write error response failed", "error", err)
	}
}

// boolPtr returns a pointer to b. Used wherever an API response field requires
// *bool but the source value is a plain bool.
func boolPtr(b bool) *bool { return &b }

// jsonSessionDetail writes a response that conforms to the gen.SessionDetail wire
// shape: { session, messages, agent_removed? }.
// The domain types (session.UnifiedMeta, session.TranscriptEntry) serialize via
// their own json tags to JSON layouts that match SessionDetail.yaml / Session.yaml /
// Message.yaml — we exploit that to avoid a field-by-field copy into gen.SessionDetail.
// The anonymous struct is not a named wire-format type and therefore does not trigger
// the check-no-handwritten-wire-types lint rule.
// jsonSessionDetail serializes a session detail response. The internal
// session.UnifiedMeta is converted to gen.Session via unifiedMetaToGenSession
// so that required-but-empty array fields (e.g. partitions) marshal as []
// rather than null, honoring the Session.yaml contract (zod schema validates
// type:array, rejecting null and dropping the whole list).
//
// Messages stay as []session.TranscriptEntry: the Message.yaml schema only
// requires {id, timestamp, agent_id}, and every other field uses omitempty
// in Go — so nil slices/maps are omitted, not emitted as null.
func jsonSessionDetail(
	w http.ResponseWriter,
	meta *session.UnifiedMeta,
	messages []session.TranscriptEntry,
	agentRemoved bool,
) {
	genSession := unifiedMetaToGenSession(meta)
	if messages == nil {
		messages = []session.TranscriptEntry{}
	}
	if agentRemoved {
		jsonOK(w, struct {
			Session      gen.Session               `json:"session"`
			Messages     []session.TranscriptEntry `json:"messages"`
			AgentRemoved bool                      `json:"agent_removed,omitempty"`
		}{Session: genSession, Messages: messages, AgentRemoved: agentRemoved})
		return
	}
	jsonOK(w, struct {
		Session  gen.Session               `json:"session"`
		Messages []session.TranscriptEntry `json:"messages"`
	}{Session: genSession, Messages: messages})
}

// --- Sessions ---

// HandleSessions routes /api/v1/sessions requests: GET (list/detail/messages/tool-results), POST (create), PUT (rename), DELETE (delete).
func (a *restAPI) HandleSessions(w http.ResponseWriter, r *http.Request) {
	// Extract optional session ID and sub-path from the URL.
	// Supports: /api/v1/sessions, /api/v1/sessions/{id}, /api/v1/sessions/{id}/messages,
	//           /api/v1/sessions/{id}/tool-results/{ref}
	path := strings.TrimSuffix(r.URL.Path, "/")
	remainder := strings.TrimPrefix(path, "/api/v1/sessions")
	remainder = strings.TrimPrefix(remainder, "/")

	var sessionID, subPath string
	if remainder != "" {
		parts := strings.SplitN(remainder, "/", 2)
		sessionID = parts[0]
		if len(parts) > 1 {
			subPath = parts[1]
		}
	}

	if sessionID != "" {
		if err := validateEntityID(sessionID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid session ID")
			return
		}
	}

	// Dispatch tool-results sub-resource before the generic method switch so the
	// full path (including ref) is available to HandleToolResults via r.URL.Path.
	if strings.HasPrefix(subPath, "tool-results/") || subPath == "tool-results" {
		a.HandleToolResults(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if sessionID == "" {
			a.listSessions(w, r)
		} else if subPath == "messages" {
			a.getSessionMessages(w, r, sessionID)
		} else {
			a.getSession(w, r, sessionID)
		}
	case http.MethodPost:
		if sessionID == "" {
			a.createSessionHTTP(w, r)
		} else {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case http.MethodPut:
		if sessionID == "" {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		} else {
			a.renameSession(w, r, sessionID)
		}
	case http.MethodDelete:
		if sessionID == "" {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		} else {
			a.deleteSession(w, r, sessionID)
		}
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// sanitizePartialError extracts only the agent ID from a ListAllSessions partial
// error and returns an opaque failure token. ListAllSessions wraps errors as
// "agent=<id>: <underlying>". We keep only the agent prefix so that filesystem
// paths, syscall messages, and permission strings are never leaked to REST clients.
// The full error is always logged server-side before calling this function.
func sanitizePartialError(pe error) string {
	msg := pe.Error()
	if idx := strings.Index(msg, ": "); idx > 0 {
		return msg[:idx] + ": session_list_failed"
	}
	return "session_list_failed"
}

// modelEntry aliases the oapi-codegen-inlined Session.Stats.by_model element so
// it can be referenced by name (Go cannot name the anonymous inline struct).
type modelEntry = struct { // not-wire-format: alias of the codegen-inlined Session.Stats.by_model element; canonical wire schema is contracts/components/schemas/ModelTokens.yaml, not a new type
	CacheRead  *int `json:"cache_read,omitempty"`
	CacheWrite *int `json:"cache_write,omitempty"`
	In         *int `json:"in,omitempty"`
	Out        *int `json:"out,omitempty"`
	Total      int  `json:"total"`
}

// intPtrIfPositive returns &n when n > 0, else nil (for omitempty wire fields).
func intPtrIfPositive(n int) *int {
	if n > 0 {
		return &n
	}
	return nil
}

// unifiedMetaToGenSession converts a session.UnifiedMeta to the generated gen.Session wire type.
// The two types have matching JSON field names; this explicit conversion satisfies the Go type checker.
func unifiedMetaToGenSession(m *session.UnifiedMeta) gen.Session {
	// Build the optional per-model breakdown for the inline Stats struct.
	var byModel *map[string]modelEntry
	if len(m.Stats.ByModel) > 0 {
		mm := make(map[string]modelEntry, len(m.Stats.ByModel))
		for model, mt := range m.Stats.ByModel {
			mm[model] = modelEntry{
				In:         intPtrIfPositive(mt.In),
				Out:        intPtrIfPositive(mt.Out),
				CacheRead:  intPtrIfPositive(mt.CacheRead),
				CacheWrite: intPtrIfPositive(mt.CacheWrite),
				Total:      mt.Total,
			}
		}
		byModel = &mm
	}

	s := gen.Session{
		Id:        m.ID,
		AgentId:   m.AgentID,
		Channel:   m.Channel,
		CreatedAt: m.CreatedAt,
		// UpdatedAt was previously omitted, so every session serialized Go's
		// zero time ("0001-01-01T00:00:00Z") — breaking session sort order and
		// the relative-time display. The meta carries a real UpdatedAt (stamped
		// on every message append; ListSessions sorts by it), so map it through.
		UpdatedAt: m.UpdatedAt,
		Title:     m.Title,
		Status:    gen.SessionStatus(m.Status),
		Partitions: func() []string {
			if m.Partitions == nil {
				return []string{}
			}
			return m.Partitions
		}(),
		Stats: struct {
			ByModel *map[string]struct {
				CacheRead  *int `json:"cache_read,omitempty"`
				CacheWrite *int `json:"cache_write,omitempty"`
				In         *int `json:"in,omitempty"`
				Out        *int `json:"out,omitempty"`
				Total      int  `json:"total"`
			} `json:"by_model,omitempty"`
			Cost             float64 `json:"cost"`
			MessageCount     int     `json:"message_count"`
			TokensCacheRead  *int    `json:"tokens_cache_read,omitempty"`
			TokensCacheWrite *int    `json:"tokens_cache_write,omitempty"`
			TokensIn         int     `json:"tokens_in"`
			TokensOut        int     `json:"tokens_out"`
			TokensTotal      int     `json:"tokens_total"`
			ToolCalls        int     `json:"tool_calls"`
		}{
			ByModel:          byModel,
			Cost:             m.Stats.Cost,
			MessageCount:     m.Stats.MessageCount,
			TokensCacheRead:  intPtrIfPositive(m.Stats.TokensCacheRead),
			TokensCacheWrite: intPtrIfPositive(m.Stats.TokensCacheWrite),
			TokensIn:         m.Stats.TokensIn,
			TokensOut:        m.Stats.TokensOut,
			TokensTotal:      m.Stats.TokensTotal,
			ToolCalls:        m.Stats.ToolCalls,
		},
	}
	if m.Model != "" {
		s.Model = &m.Model
	}
	if m.Provider != "" {
		s.Provider = &m.Provider
	}
	if m.WorkspaceID != "" {
		s.WorkspaceId = &m.WorkspaceID
	}
	if m.TaskID != "" {
		s.TaskId = &m.TaskID
	}
	if m.LastCompactionSummary != "" {
		s.LastCompactionSummary = &m.LastCompactionSummary
	}
	if m.ActiveAgentID != "" {
		s.ActiveAgentId = &m.ActiveAgentID
	}
	// ADR-057 FR-008/FR-091: present only on a subordinate (delegated child)
	// session; absent (never empty-string) on a root. A session whose
	// ParentSessionID names an id that no longer resolves is still surfaced
	// as a root by listSessions (FR-091, BDD-106) — that resolution happens
	// at the listing layer, not here; this mapping is a pure field copy.
	if m.ParentSessionID != "" {
		s.ParentSessionId = &m.ParentSessionID
	}
	if len(m.AgentIDs) > 0 {
		ids := make([]string, len(m.AgentIDs))
		copy(ids, m.AgentIDs)
		s.AgentIds = &ids
	}
	if len(m.CompactionSummaries) > 0 {
		cs := make(map[string]string, len(m.CompactionSummaries))
		for k, v := range m.CompactionSummaries {
			cs[k] = v
		}
		s.CompactionSummaries = &cs
	}
	sessionType := gen.SessionType(m.Type)
	s.Type = &sessionType
	return s
}

// computeSessionProtected derives the computed `protected` flag for a session
// (FR-021/028). A session is protected when:
//   - its type is "heartbeat", AND
//   - the workspace it belongs to still has member_configs[agentID].heartbeat.enabled=true
//     with session_id == this session's ID
//
// For any other session type, returns nil (absent on the wire — field is omitted).
// Disk reads are bounded: we only load the one workspace identified by session.WorkspaceID.
func computeSessionProtected(homePath string, m *session.UnifiedMeta) *bool {
	if m == nil || m.Type != session.SessionTypeHeartbeat || m.WorkspaceID == "" {
		return nil
	}
	ws, err := readWorkspaceFile(homePath, m.WorkspaceID)
	if err != nil {
		// MEDIUM-2: distinguish workspace-not-found (deleted) from I/O / corruption.
		// - Workspace deleted: correct, the session is no longer protected.
		// - Any other error (corrupt JSON, I/O): fail CLOSED so a transient read
		//   error never silently unprotects an active heartbeat session.
		if errors.Is(err, errWorkspaceNotFound) {
			slog.Debug("computeSessionProtected: workspace not found (deleted)",
				"workspace_id", m.WorkspaceID, "session_id", m.ID)
			f := false
			return &f
		}
		slog.Warn("computeSessionProtected: workspace load error (fail closed)",
			"workspace_id", m.WorkspaceID, "session_id", m.ID, "error", err)
		t := true
		return &t
	}
	// FIX-4b: require the agent still be on the workspace's CoreTeam. A stale
	// member_config entry for an off-team agent must not keep its session protected.
	inCoreTeam := false
	for _, id := range ws.CoreTeam {
		if id == m.AgentID {
			inCoreTeam = true
			break
		}
	}
	if !inCoreTeam {
		slog.Debug("computeSessionProtected: agent not in CoreTeam (stale entry)",
			"workspace_id", m.WorkspaceID, "agent_id", m.AgentID, "session_id", m.ID)
		f := false
		return &f
	}
	mc, hasMC := ws.MemberConfigs[m.AgentID]
	if !hasMC || mc.Heartbeat == nil {
		f := false
		return &f
	}
	// Protected only when the heartbeat is enabled AND the stored session_id
	// matches this session (not a replaced/rotated session).
	protected := mc.Heartbeat.Enabled && mc.Heartbeat.SessionID == m.ID
	return &protected
}

// u18DefaultSessionPageLimit is the page size GET /api/v1/sessions uses when
// the caller omits `limit` (ADR-057 FR-092). The response body scales with
// this number, not with total session count (FR-092(b) — boundary cost is
// O(page)), regardless of how many sessions the merged store set holds.
const u18DefaultSessionPageLimit = 50

// listSessions handles GET /api/v1/sessions (ADR-057 US-19/FR-091/FR-092/
// FR-097/FR-098/FR-104, W16c). Replaces the pre-pagination "load everything,
// filter, return" handler with the REST layer of the four-layer pagination
// stack (store U6 -> loop U9 -> REST here -> client U12): it accepts paging
// parameters, the parent_session_id / flat hierarchy switches, and returns
// the single named gen.SessionPage envelope FR-091 decided on (replacing the
// retired two-variant oneOf, gen.ListSessions200JSONResponseBody1 — see
// ADR-034/grill2 M2-10; U10 owns the contract, this handler is the consumer).
//
// Division of labor: AgentLoop.ListAllSessions (U9) applies hierarchy
// (roots-only / direct-children / flat) and FR-098's cross-store ordering +
// cursor BEFORE pagination, over the full merged set, so a page boundary can
// never split a parent from its child-count context. This handler's own job
// is the REST-layer concerns FR-092/FR-104 explicitly leave here: parsing
// and validating limit/offset, the flat+parent_session_id 400, narrowing by
// agent_id/type/include_verifier (orthogonal filters that only ever shrink a
// page, never grow it past limit), resolving each row's child_count from
// whichever store's in-memory parent index owns it (FR-097, O(1) per row),
// and building the SessionPage response.
func (a *restAPI) listSessions(w http.ResponseWriter, r *http.Request) {
	agentFilter := r.URL.Query().Get("agent_id")
	typeFilter := r.URL.Query().Get("type")
	// ADR-052 FR-036/US-13 Acceptance 6: verifier-role sessions are excluded
	// by default, REGARDLESS of the type filter — ?type=verifier alone does
	// NOT surface them; the operator must also pass include_verifier=true.
	// strconv.ParseBool accepts "1"/"t"/"T"/"TRUE"/"true"/"True" (and their
	// false counterparts); any absent/unparseable value defaults to false
	// per the contract (ListSessionsParams.IncludeVerifier default: false).
	includeVerifier, _ := strconv.ParseBool(r.URL.Query().Get("include_verifier"))

	parentSessionID := r.URL.Query().Get("parent_session_id")
	flat, _ := strconv.ParseBool(r.URL.Query().Get("flat"))
	// FR-104: flat=true and parent_session_id are mutually exclusive — a 400,
	// not a silent "flat wins" or "parent_session_id wins".
	if flat && parentSessionID != "" {
		jsonErr(w, http.StatusBadRequest, "flat and parent_session_id are mutually exclusive")
		return
	}

	limit := u18DefaultSessionPageLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			jsonErr(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			jsonErr(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		offset = n
	}

	page, partialErrs := a.agentLoop.ListAllSessions(limit, offset, parentSessionID, flat)
	for _, pe := range partialErrs {
		slog.Warn("rest: list sessions: partial error", "error", pe)
	}

	// Apply the orthogonal agent_id/type/include_verifier filters over this
	// page's rows. These narrow AFTER hierarchy+pagination (ListAllSessions'
	// own doc comment: "the 400 for supplying both is a REST-layer concern,
	// not this method's" applies equally to these filters) — they can only
	// shrink a page below `limit`, never grow it past `limit`, so FR-092(b)'s
	// "response body scales with limit, not total session count" still holds.
	filtered := make([]*session.UnifiedMeta, 0, len(page.Sessions))
	for _, m := range page.Sessions {
		if agentFilter != "" && m.AgentID != agentFilter {
			continue
		}
		if typeFilter != "" && string(m.Type) != typeFilter {
			continue
		}
		if m.Type == session.SessionTypeVerifier && !includeVerifier {
			continue
		}
		filtered = append(filtered, m)
	}

	// Always route through unifiedMetaToGenSession so that required array/map
	// fields (Partitions in particular) marshal as [] not null — Zod on the SPA
	// rejects null where the contract says type:array and drops the whole list.
	genSessions := make([]gen.Session, 0, len(filtered))
	for _, m := range filtered {
		s := unifiedMetaToGenSession(m)
		// FR-021/028: compute the `protected` flag for heartbeat sessions.
		// Non-heartbeat sessions get nil (field omitted from the wire response).
		s.Protected = computeSessionProtected(a.homePath, m)
		// FR-091/FR-097: child_count is resolved from whichever store's
		// in-memory parent index owns this session — O(1) per row, no disk
		// read, regardless of listing mode (roots-only, parent_session_id, or
		// flat). A session this handler cannot resolve a store for (should
		// not happen — it just came from ListAllSessions) is left at zero
		// rather than surfacing a spurious count.
		if store := a.resolveSessionStore(m.ID); store != nil {
			cc := store.ChildCount(m.ID)
			s.ChildCount = &cc
		}
		genSessions = append(genSessions, s)
	}

	resp := gen.SessionPage{Sessions: genSessions}
	if page.NextOffset >= 0 {
		nc := strconv.Itoa(page.NextOffset)
		resp.NextCursor = &nc
	}
	if len(partialErrs) > 0 {
		// FR-098(c): a store that errored mid-merge still yields a valid page
		// plus next_cursor — partial_errors composes with paging rather than
		// halting it.
		sanitized := make([]string, len(partialErrs))
		for i, pe := range partialErrs {
			sanitized[i] = sanitizePartialError(pe)
		}
		resp.PartialErrors = &sanitized
	}
	jsonOK(w, resp)
}

func (a *restAPI) getSession(w http.ResponseWriter, _ *http.Request, id string) {
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}
	meta, err := store.GetMeta(id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("session not found: %v", err))
		return
	}
	messages, err := store.ReadTranscript(id)
	if err != nil {
		slog.Error("rest: could not read transcript", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read transcript: %v", err))
		return
	}
	// ADR-057 D1/W11 (FR-034/FR-038): a delegated child owns its own real
	// session (FR-005), so its narration lives in the CHILD's OWN
	// transcript.jsonl and is simply never present here — the old REST-side
	// visibility filter helper is deleted, not reapplied (FR-035). This
	// boundary now returns id's full transcript unfiltered, same as every
	// other read boundary (FR-035/FR-037/FR-038, BDD-37).
	// Detect ghost sessions: if the session references an agent that no longer
	// exists in the current config, surface agent_removed=true so the frontend
	// can show the read-only "Agent removed" banner (#103).
	agentRemoved := false
	if meta.AgentID != "" {
		cfg := a.agentLoop.GetConfig()
		found := false
		for _, ac := range cfg.Agents.List {
			if ac.ID == meta.AgentID {
				found = true
				break
			}
		}
		agentRemoved = !found
	}
	// Build response matching gen.SessionDetail wire shape:
	// { session, messages, agent_removed? }
	// The domain types (session.UnifiedMeta, session.TranscriptEntry) serialize to
	// the same JSON layout defined in SessionDetail.yaml and Session.yaml/Message.yaml.
	// Using jsonSessionDetail avoids an import cycle while staying lint-compliant.
	jsonSessionDetail(w, meta, messages, agentRemoved)
}

func (a *restAPI) getSessionMessages(w http.ResponseWriter, _ *http.Request, id string) {
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}
	messages, err := store.ReadTranscript(id)
	if err != nil {
		slog.Error("rest: could not read transcript", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read transcript: %v", err))
		return
	}
	// ADR-057 D1/W11 (FR-034/FR-038): see getSession's identical note above —
	// the old delegate-narration visibility filter is deleted outright, not
	// reapplied; a child's own entries never land in another session's
	// transcript to begin with under the post-D1 design.
	// Coerce nil → empty slice so JSON marshals as [] not null. The SPA's
	// fetchSessionMessages validates via z.array(WireMessageSchema), which
	// rejects null — a fresh session with no transcript would surface as
	// "Could not load messages." in the UI.
	if messages == nil {
		messages = []session.TranscriptEntry{}
	}
	// review r2 RV2: every entry passes through as the raw TranscriptEntry
	// (unchanged shape) EXCEPT EntryTypeJudgeVerdict, whose Content is a raw
	// json.Marshal(task.JudgeVerdict) string with no typed field to carry it
	// on the wire. Before this fix, cold-load rendered an empty/broken verdict
	// card because Message.verdict was never populated. Parse Content and
	// attach it as "verdict" (same shape handleTaskVerdicts/toWireJudgeVerdict
	// produces, rest_tasks.go) so cold-load and live/replay can never disagree.
	out := make([]any, 0, len(messages))
	for _, entry := range messages {
		out = append(out, withWireJudgeVerdict(id, entry))
	}
	jsonOK(w, out)
}

// withWireJudgeVerdict returns entry unchanged for every entry type except
// EntryTypeJudgeVerdict, for which it returns a JSON-object representation of
// entry (all its own fields, unchanged) plus an added "verdict" field parsed
// from entry.Content and converted via toWireJudgeVerdict — the wire
// Message.verdict shape (review r2 RV2). On any parse/marshal failure it logs
// and falls back to the raw entry (verdict simply absent) rather than
// dropping the entry or failing the whole response.
func withWireJudgeVerdict(sessionID string, entry session.TranscriptEntry) any {
	if entry.Type != session.EntryTypeJudgeVerdict || entry.Content == "" {
		return entry
	}
	var verdict task.JudgeVerdict
	if uerr := json.Unmarshal([]byte(entry.Content), &verdict); uerr != nil {
		slog.Warn("rest: could not parse judge_verdict transcript entry — cold-load will omit verdict",
			"session_id", sessionID, "entry_id", entry.ID, "error", uerr)
		return entry
	}
	raw, merr := json.Marshal(entry)
	if merr != nil {
		slog.Error("rest: could not marshal judge_verdict transcript entry",
			"session_id", sessionID, "entry_id", entry.ID, "error", merr)
		return entry
	}
	var m map[string]any
	if uerr := json.Unmarshal(raw, &m); uerr != nil {
		slog.Error("rest: could not decode judge_verdict transcript entry to map",
			"session_id", sessionID, "entry_id", entry.ID, "error", uerr)
		return entry
	}
	m["verdict"] = toWireJudgeVerdict(verdict)
	return m
}

// renameSession handles PUT /api/v1/sessions/{id}.
// Accepts {"title": "new name"} and returns the updated session meta.
func (a *restAPI) renameSession(w http.ResponseWriter, r *http.Request, id string) {
	var req gen.SessionRenameRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SessionRenameRequest", &req, validateEnabled) {
		return
	}
	// A bare `== ""` check is not enough: "   " and invisible/zero-width runes
	// (ZWSP, ZWNJ, word joiner, BOM, soft hyphen, U+2800 …) both pass it and
	// produce a session that renders blank and is unfindable in the sidebar.
	// Same class of hole UAT found on plan/task titles — task.HasVisibleContent
	// is the shared predicate, so this stays fixed with them rather than
	// drifting into a second, weaker rule.
	req.Title = strings.TrimSpace(req.Title)
	if !task.HasVisibleContent(req.Title) {
		jsonErr(w, http.StatusBadRequest, "title is required")
		return
	}
	// Length is checked AFTER trimming so trailing padding can't push an
	// otherwise-valid title over the limit.
	if len(req.Title) > 256 {
		jsonErr(w, http.StatusBadRequest, "title too long (max 256 characters)")
		return
	}
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err := store.SetMeta(id, session.MetaPatch{Title: &req.Title}); err != nil {
		slog.Error("rest: rename session", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not rename session: %v", err))
		return
	}
	meta, err := store.GetMeta(id)
	if err != nil {
		slog.Error("rest: rename session: get meta after update", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read updated session: %v", err))
		return
	}
	jsonOK(w, unifiedMetaToGenSession(meta))
}

// deleteSession handles DELETE /api/v1/sessions/{id}.
// Removes all session data and returns {"success": true}.
func (a *restAPI) deleteSession(w http.ResponseWriter, _ *http.Request, id string) {
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}

	// FR-014 / US-7: reject deletion of an active heartbeat session with 409.
	// Load the meta to check the session type before attempting the delete so
	// we don't make a half-deletion attempt and then fail. The workspace load
	// is bounded by the session's WorkspaceID (no full scan).
	meta, metaErr := store.GetMeta(id)
	if metaErr != nil {
		// MEDIUM-1: fail CLOSED on meta-read error — a session whose metadata
		// cannot be read must not be silently deleted past the heartbeat guard.
		slog.Error("rest: delete session: could not read session meta",
			"session_id", id, "error", metaErr)
		jsonErr(w, http.StatusInternalServerError, "could not verify session protection")
		return
	}
	if meta != nil && meta.Type == session.SessionTypeHeartbeat {
		if isProtected := computeSessionProtected(a.homePath, meta); isProtected != nil && *isProtected {
			// C-1 (FR-014): audit the blocked delete before returning 409.
			if a.auditor != nil {
				if err := a.auditor.Log(&audit.Entry{
					Event:    "session.delete.blocked",
					Decision: audit.DecisionDeny,
					AgentID:  meta.AgentID,
					Details: map[string]any{
						"session_id":   id,
						"workspace_id": meta.WorkspaceID,
						"agent_id":     meta.AgentID,
						"reason":       "heartbeat enabled",
					},
				}); err != nil {
					slog.Warn("audit write failed", "event", "session.delete.blocked",
						"session_id", id, "error", err)
				}
			}
			jsonErr(w, http.StatusConflict,
				"cannot delete a protected heartbeat session while its heartbeat is enabled; "+
					"disable the heartbeat in the workspace settings first")
			return
		}
	}

	// ADR-057 W18b (FR-071/BDD-78): resolve id's full descendant set BEFORE
	// deleting anything, over the DURABLE lifecycle store — every delegation,
	// live or not, has a LifecycleRecord (User Story 4), so this walk is
	// authoritative independent of turn liveness and survives a restart.
	// Reuses U11's already-tested u11CollectDescendantSessionIDs (same
	// package, pkg/gateway/websocket.go), which walks U13's ParentDurableKey
	// index (pkg/session/lifecycle.go) exactly as the cancel/approval-cascade
	// paths do — this handler does not reimplement the walk. A nil lifecycle
	// store (no delegation store wired — most webchat-only installs never
	// mint one) degrades to zero descendants, matching that helper's
	// documented nil-store behavior.
	descendantIDs := u11CollectDescendantSessionIDs(a.agentLoop.GetSessionLifecycleStore(), id)

	if err := store.DeleteSession(id); err != nil {
		slog.Error("rest: delete session", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not delete session: %v", err))
		return
	}

	// ADR-057 W18b (FR-071): id's OWN <home>/uploads/<id>/ was already
	// removed by store.DeleteSession above (pre-existing ADR-017 cascade,
	// unified.go's DeleteSession). Under D1 each delegated descendant now
	// owns its OWN session — and therefore its OWN uploads/<descendantID>/
	// directory (FR-010) — which that per-id cascade cannot reach. Sweep
	// every descendant's upload tree here so deleting a parent chat does not
	// leave every delegated child's uploaded media permanently orphaned on
	// disk (US-16: "a silent disk leak, not a correctness break" — hence
	// best-effort, logged, non-fatal, matching the media-store release below).
	if len(descendantIDs) > 0 {
		if err := media.RemoveSessionUploadsTree(descendantIDs); err != nil {
			slog.Warn("rest: delete session: cascade-delete descendant uploads failed",
				"session_id", id, "descendant_count", len(descendantIDs), "error", err)
		}
	}

	// Release in-memory media store refs for any tool-generated inline media
	// (screenshots, charts, etc.) that were stored with CleanupPolicyForgetOnly
	// under the session-scoped scope media.SessionInlineScopePrefix+"<id>".
	// The underlying files are already on disk under uploads/<id>/ and have been
	// cascade-deleted by DeleteSession above; we only need to drop the in-memory
	// ref so the store index does not accumulate stale entries.
	mediaStore := a.agentLoop.GetMediaStore()
	if mediaStore == nil {
		mediaStore = a.mediaStore
	}
	if mediaStore != nil {
		if err := mediaStore.ReleaseAll(media.SessionInlineScopePrefix + id); err != nil {
			// Non-fatal: session data is already removed. Log and continue.
			slog.Warn("rest: delete session: media store release failed",
				"session_id", id, "error", err)
		}
	}

	jsonOK(w, map[string]bool{"success": true})
}

// firstChatTargetAgentID returns the ID of the first chat-target agent in the
// config list, or "" when no such agent is configured. Used as a last-resort
// fallback after GetDefaultAgent() — mirrors resolveDefaultAgentID in
// pkg/routing/route.go. Workers are NOT chat targets, so they are skipped here:
// a last-resort fallback must never land on a worker, which is invoked only via
// delegation.
func firstChatTargetAgentID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	for _, ag := range cfg.Agents.List {
		if ag.IsChatTarget() {
			return ag.ID
		}
	}
	return ""
}

// isWorkerAgentID reports whether agentID resolves to a worker agent in the config.
// Returns false for an unknown agent ID (existence is validated separately by the
// caller). Used by gateway session-binding chokepoints to reject a worker as a chat
// target — a worker is a delegation-only labor tier, never a live chat persona.
func isWorkerAgentID(cfg *config.Config, agentID string) bool {
	if cfg == nil || agentID == "" {
		return false
	}
	ac := findAgentConfig(cfg, agentID)
	return ac != nil && ac.IsWorker()
}

func (a *restAPI) createSessionHTTP(w http.ResponseWriter, r *http.Request) {
	var req gen.SessionCreateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SessionCreateRequest", &req, validateEnabled) {
		return
	}

	agentID := ""
	if req.AgentId != nil {
		agentID = *req.AgentId
	}
	if agentID == "" {
		if reg := a.agentLoop.GetRegistry(); reg != nil {
			if def := reg.GetDefaultAgent(); def != nil {
				agentID = def.ID
			}
		}
		if agentID == "" {
			// Fall back to the first chat-target agent (mirrors handleBoardTaskStart /
			// resolveDefaultAgentID in pkg/routing/route.go).
			agentID = firstChatTargetAgentID(a.agentLoop.GetConfig())
		}
	}
	if err := validateEntityID(agentID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid agent_id")
		return
	}
	// Validate the agent exists before creating the session.
	if agentStore := a.agentLoop.GetAgentStore(agentID); agentStore == nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("agent %q not found", agentID))
		return
	}
	// A worker is a delegation-only labor tier — never a chat target. A session
	// backs a live chat, so an explicit worker agent_id must be rejected (mirrors
	// setChannelRouting's worker 400). Both no-agent fallbacks above already skip
	// workers, so this only ever rejects an explicitly-supplied worker.
	if isWorkerAgentID(a.agentLoop.GetConfig(), agentID) {
		jsonErr(w, http.StatusBadRequest, "workers are not chat targets and cannot back a session")
		return
	}

	// Use the shared session store for new sessions (joined session model).
	// Fall back to the per-agent store if the shared store is unavailable.
	store := a.agentLoop.GetSessionStore()
	if store == nil {
		store = a.agentLoop.GetAgentStore(agentID)
		if store == nil {
			jsonErr(w, http.StatusInternalServerError, "session store unavailable")
			return
		}
	}

	var sessionType session.UnifiedSessionType
	reqType := ""
	if req.Type != nil {
		reqType = string(*req.Type)
	}
	switch reqType {
	case string(session.SessionTypeTask):
		sessionType = session.SessionTypeTask
	case string(session.SessionTypeChannel):
		sessionType = session.SessionTypeChannel
	default:
		sessionType = session.SessionTypeChat
	}

	meta, err := store.NewSession(sessionType, "webchat", agentID)
	if err != nil {
		slog.Error("rest: create session", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not create session: %v", err))
		return
	}
	jsonCreated(w, unifiedMetaToGenSession(meta))
}

// --- Agents ---

// HandleAgents handles /api/v1/agents (list + create), /api/v1/agents/{id} (detail),
// and /api/v1/agents/{id}/sessions (sessions for agent).
func (a *restAPI) HandleAgents(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	remainder := strings.TrimPrefix(path, "/api/v1/agents")
	remainder = strings.TrimPrefix(remainder, "/")

	// Split remainder into agentID and optional sub-path.
	var agentID, subPath string
	if remainder != "" {
		parts := strings.SplitN(remainder, "/", 2)
		agentID = parts[0]
		if len(parts) > 1 {
			subPath = parts[1]
		}
	}

	// GET /api/v1/agents/executor-defaults — static reference data (agent-system-
	// fixes-2 ghost-text bug fix). This reservation is structurally different
	// from the "sessions"/"runner"/"tools"/"mailboxes" sub-path guards below:
	// those reserve a VERB-SUFFIX position that is only checked AFTER agentID
	// has already been split off and validated (so they can never collide with
	// a real agent ID, only with a same-named sub-resource segment). This guard
	// instead claims the agentID SLOT ITSELF — "executor-defaults" is matched
	// as if it were the {id} value before any agent lookup happens, so it is a
	// static path segment carved out of the agent-ID namespace, not a
	// sub-resource reservation. createAgent/updateAgent do not reject this
	// literal ID, so if an agent were ever created with it, that agent would
	// become permanently unreachable via GET /api/v1/agents/{id} (shadowed by
	// this branch). Practical risk is low — agent IDs are always
	// uuid.New().String(), never operator-chosen — but this is a narrower,
	// more fragile precedent than the sub-path guards below and should not be
	// copied casually for a future static route under /agents/.
	if agentID == "executor-defaults" && subPath == "" {
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.listExecutorDefaults(w)
		return
	}

	// POST /api/v1/agents/executor-preview — stateless real-command preview
	// (rest_executor_preview.go). Same agentID-SLOT carve-out pattern as
	// executor-defaults immediately above (see that block's comment for why
	// this is structurally different from the sessions/runner/tools/mailboxes
	// sub-path guards below). Body-driven and agent-agnostic — mirrors POST
	// /system/cli-validate — so it works both from the create wizard, where no
	// agent id exists yet, and from an existing agent's edit form.
	if agentID == "executor-preview" && subPath == "" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.postAgentsExecutorPreview(w, r)
		return
	}

	// POST /api/v1/agents/executor-smoke-test — actually RUN a bounded, real
	// test prompt through an external-CLI worker's real dispatch path
	// (rest_executor_smoketest.go). Same agentID-SLOT carve-out pattern as
	// executor-preview/executor-defaults immediately above. Unlike those two
	// (stateless computation only, no spawn), this endpoint DOES spend real
	// model tokens and DOES run a real, authenticated subprocess — it
	// enforces its own dedicated rate limit (smokeTestLimiter) and per-caller
	// in-flight cap (smokeTestInflight) inline, since it shares this route's
	// registration-time auth wrapping (api.withAuth(api.HandleAgents), same
	// create-parity as executor-preview) rather than getting its own
	// dedicated top-level route like /system/cli-validate does.
	if agentID == "executor-smoke-test" && subPath == "" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.postAgentsExecutorSmokeTest(w, r)
		return
	}

	// Validate agentID before any filesystem operations (path traversal guard, C1).
	if agentID != "" {
		if err := validateEntityID(agentID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid agent ID")
			return
		}
	}

	// GET /api/v1/agents/{id}/sessions
	if r.Method == http.MethodGet && agentID != "" && subPath == "sessions" {
		a.listAgentSessions(w, agentID)
		return
	}

	// POST /api/v1/agents/{id}/runner/test — external-CLI runner connection test (Spec-4 FR-4.2)
	if agentID != "" && subPath == "runner/test" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.testAgentRunner(w, r, agentID)
		return
	}

	// GET/PUT /api/v1/agents/{id}/tools — per-agent tool registry view (FR-028, FR-086)
	if agentID != "" && subPath == "tools" {
		switch r.Method {
		case http.MethodGet:
			a.HandleAgentToolsRegistry(w, r, agentID)
		case http.MethodPut:
			a.updateAgentTools(w, r, agentID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// GET/PUT/DELETE /api/v1/agents/{id}/mailboxes/{workspaceId} — one
	// (agent, workspace) email mailbox account (M11, pair-addressed
	// 2026-07-03: the same agent may hold a different mailbox in each
	// workspace it belongs to). Match both the bare "mailboxes" prefix (so a
	// missing workspace segment gets a proper 400 instead of silently
	// falling through to the generic agent-CRUD switch below) and
	// "mailboxes/<workspaceId>".
	if agentID != "" && (subPath == "mailboxes" || strings.HasPrefix(subPath, "mailboxes/")) {
		mbParts := strings.SplitN(subPath, "/", 2)
		if len(mbParts) != 2 || mbParts[1] == "" || strings.Contains(mbParts[1], "/") {
			jsonErr(w, http.StatusBadRequest,
				"mailboxes path requires exactly one workspace ID segment: /agents/{id}/mailboxes/{workspaceId}")
			return
		}
		workspaceID := mbParts[1]
		if err := validateEntityID(workspaceID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
			return
		}
		switch r.Method {
		case http.MethodGet:
			a.getAgentMailbox(w, agentID, workspaceID)
		case http.MethodPut:
			a.setAgentMailbox(w, r, agentID, workspaceID)
		case http.MethodDelete:
			a.deleteAgentMailbox(w, agentID, workspaceID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		if agentID == "" {
			a.listAgents(w)
		} else {
			a.getAgent(w, agentID)
		}
	case http.MethodPost:
		if agentID == "" {
			a.createAgent(w, r)
		} else {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case http.MethodPut:
		if agentID != "" {
			a.updateAgent(w, r, agentID)
		} else {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case http.MethodDelete:
		if agentID != "" {
			a.deleteAgent(w, agentID)
		} else {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// testAgentRunner handles POST /api/v1/agents/{id}/runner/test (Spec-4 FR-4.2).
// It validates the agent's configured external-CLI runner WITHOUT running real work:
// binary present + version handshake + authenticated. Returns distinct reasons for
// missing-binary vs unauthenticated. When the agent's executor is not external-cli
// (native / remote-a2a / unset), there is no runner to test → reason "not-external-cli".
func (a *restAPI) testAgentRunner(w http.ResponseWriter, r *http.Request, agentID string) {
	cfg := a.agentLoop.GetConfig()

	var found bool
	var executor *config.ExecutorConfig
	for _, ac := range cfg.Agents.List {
		if ac.ID == agentID {
			found = true
			if ac.Subagents != nil {
				executor = ac.Subagents.Executor
			}
			break
		}
	}
	if !found {
		jsonErr(w, http.StatusNotFound, "agent not found")
		return
	}

	// The agent must be configured for external-cli to have a runner to test.
	if executor == nil || executor.EffectiveKind() != config.ExecutorKindExternalCLI {
		jsonOK(w, gen.RunnerTestResponse{
			Ok:      false,
			Reason:  gen.NotExternalCli,
			Message: "agent executor is not external-cli; no external runner to test",
		})
		return
	}
	cli := executor.CLI
	if cli == "" {
		jsonOK(w, gen.RunnerTestResponse{
			Ok:      false,
			Reason:  gen.UnknownCli,
			Message: "agent executor.cli is empty; set claude-code, codex, or opencode",
			Cli:     strPtr(""),
		})
		return
	}

	// Validate the agent's CONFIGURED binary (executor.cli_path), not just the
	// default $PATH binary — otherwise a custom cli_path that does not exist
	// would false-green. Empty cli_path falls back to the default name.
	res := runner.TestConnectionWithPath(r.Context(), cli, executor.CLIPath)
	resp := gen.RunnerTestResponse{
		Ok:      res.OK,
		Reason:  gen.RunnerTestResponseReason(res.Reason),
		Message: res.Message,
		Cli:     strPtr(cli),
	}
	if res.CLIVersion != "" {
		resp.CliVersion = strPtr(res.CLIVersion)
	}
	jsonOK(w, resp)
}

// listExecutorDefaults handles GET /api/v1/agents/executor-defaults (Agent
// System ghost-text bug fix). Returns static, byte-accurate reference data —
// for each supported subagent_3p external CLI, the ORDERED list of arguments
// pkg/agent/runner/driver_{claude,codex,opencode}.go's buildArgs() actually
// applies BEFORE any operator-supplied executor.cli_args, plus a note on how
// the prompt itself reaches the CLI. Not agent-scoped: this is pure reference
// documentation sourced directly from the three drivers. There is no runtime
// introspection of buildArgs — a change to any driver's own flags MUST be
// mirrored here by hand, or this endpoint drifts from reality.
func (a *restAPI) listExecutorDefaults(w http.ResponseWriter) {
	jsonOK(w, []gen.ExecutorDefaults{
		{
			Cli: gen.ExternalCliToolClaudeCode,
			AutoAppliedFlags: []string{
				"-p",
				"--output-format stream-json",
				"--verbose",
				"--no-chrome",
				"--model <configured model> (only when a model is configured)",
				"--dangerously-skip-permissions",
				"--max-turns <configured max turns> (only when a turn cap is configured)",
			},
			Notes: "The prompt is delivered via stdin, with no positional prompt argument at all — never via a --prompt flag. --resume/--session-id are never passed; every run starts a fresh claude session. --dangerously-skip-permissions is passed unconditionally (operator decision, issue #488, reversing the original FR-5.3/US-5 stance of using --permission-mode acceptEdits instead) — this matches codex/opencode, which already ran permission-bypassed; see the tracked issue for the sandbox-boundary follow-up this reversal implies for claude specifically. Operator cli_args are appended after this list; a redundant --dangerously-skip-permissions or an attempt to change --output-format away from stream-json is dropped with a WARN (see argsafety.go) — the latter because the driver's own NDJSON stream parser requires stream-json output.",
		},
		{
			Cli: gen.ExternalCliToolCodex,
			AutoAppliedFlags: []string{
				"--ask-for-approval never",
				"exec",
				"--json",
				"--sandbox workspace-write",
				"--skip-git-repo-check",
				"--color never",
				"-m <configured model> (only when a model is configured)",
				"-C <agent working directory> (only when a working directory is set — always populated for a real dispatched run)",
			},
			Notes: "--ask-for-approval is a GLOBAL codex flag and must precede the exec subcommand (codex errors if it follows exec); --sandbox is an exec-subcommand flag and is placed after exec instead. The prompt is delivered via stdin — a trailing \"-\" argument — never via a --prompt flag. Operator cli_args are appended after this list; --dangerously-bypass-approvals-and-sandbox, --sandbox danger-full-access, any --ask-for-approval override, and any --json override (bare or \"=false\"-shaped) are dropped with a WARN (see argsafety.go) — the last one because the driver's own NDJSON stream parser requires --json output.",
		},
		{
			Cli: gen.ExternalCliToolOpencode,
			AutoAppliedFlags: []string{
				"run",
				"--format json",
				"--model <configured model> (only when the configured model is shaped like \"provider/model\", e.g. \"anthropic/claude-3-5-sonnet\"; a bare model name is omitted so the CLI falls back to its own default)",
				"--dangerously-skip-permissions",
				"--",
			},
			Notes: "opencode's `run` command has no --prompt flag; the prompt is delivered as the POSITIONAL argument placed LAST, after the literal \"--\" end-of-options separator, so opencode's yargs-based argument parser never mistakes prompt text beginning with \"--\" for a flag. It is never sent via stdin (stdin is always an empty reader for opencode runs). --dangerously-skip-permissions is opencode's only non-interactive auto-approve posture in this CLI version (no middle-ground \"auto-accept edits\" flag exists) — Omnipus's own consent routing for opencode remains best-effort/post-hoc regardless of this flag. Operator cli_args are appended before the trailing \"--\"; a redundant --dangerously-skip-permissions or an attempt to change --format away from json is dropped with a WARN (see argsafety.go) — the latter because the driver's own NDJSON stream parser requires --format json output.",
		},
	})
}

// listAgentSessions returns the union of an agent's sessions from both
// session stores, deduplicated by session ID. Ordinary chat sessions moved
// to the shared store (AgentLoop.GetSessionStore — "the shared store for new
// sessions") some time ago; AgentLoop.GetAgentStore's own doc marks it "kept
// for legacy per-agent session access". This endpoint used to read
// GetAgentStore exclusively, so it silently omitted every session minted
// after that move.
//
// This does not call AgentLoop.ListAllSessions: that helper merges the
// shared store with EVERY registered agent's legacy store to build a
// cross-agent list, which would mean opening and reading every OTHER
// agent's session directory off disk just to filter the result back down to
// this one agent — needless I/O for a single-agent-scoped endpoint. Instead
// this inlines the same shared-primary/per-agent-secondary merge idiom
// ListAllSessions and createSessionHTTP already use, scoped to just the two
// stores that can hold this agent's sessions.
func (a *restAPI) listAgentSessions(w http.ResponseWriter, agentID string) {
	// agentID is already validated by HandleAgents before reaching here.
	seen := make(map[string]bool)
	var metas []*session.UnifiedMeta
	var errs []error

	if shared := a.agentLoop.GetSessionStore(); shared != nil {
		sharedMetas, err := shared.ListSessions()
		if err != nil {
			// Logged and collected. This used to be treated as non-fatal
			// whenever the OTHER store still produced data, on the theory
			// that a partial list beats an empty one — but the shared store
			// is the PRIMARY home for sessions minted after the migration
			// described in this function's doc comment, so "legacy store
			// still has data" typically means "most of this agent's real
			// sessions are the ones now missing". A 200 built from whatever
			// the healthy store returned would look complete to the caller
			// (the SPA has no way to tell "all sessions" from "some
			// sessions") while silently omitting the majority — reintroducing
			// one level up the exact bug this function was written to fix.
			// See the escalation check after both scans: ANY store error now
			// aborts with 500 rather than risk a caller trusting an
			// incomplete list as complete.
			slog.Warn("rest: list agent sessions: shared store", "agent_id", agentID, "error", err)
			errs = append(errs, fmt.Errorf("shared: %w", err))
		}
		for _, m := range sharedMetas {
			// The shared store holds sessions for every agent; membership is
			// AgentIDs (PostLoad-backfilled from the legacy single AgentID
			// field on every read, so this is never empty for a real session).
			if slices.Contains(m.AgentIDs, agentID) {
				metas = append(metas, m)
				seen[m.ID] = true
			}
		}
	}

	if legacy := a.agentLoop.GetAgentStore(agentID); legacy != nil {
		legacyMetas, err := legacy.ListSessions()
		if err != nil {
			slog.Warn("rest: list agent sessions: legacy store", "agent_id", agentID, "error", err)
			errs = append(errs, fmt.Errorf("legacy: %w", err))
		}
		for _, m := range legacyMetas {
			// A session can exist in both stores (a pre-fix duplicate-mint bug
			// produced exactly that) — the shared-store copy wins.
			if !seen[m.ID] {
				metas = append(metas, m)
				seen[m.ID] = true
			}
		}
	}

	// ANY store read failure escalates to a 500, even when the OTHER store
	// produced data. The wire shape for this endpoint is a bare
	// `type: array, items: Session` (contracts/openapi.yaml) — unlike e.g.
	// ChannelEntry's per-entry Degraded/DegradedReason fields (rest.go's
	// applyDegradedOverlay), a JSON array has no sibling slot to carry a
	// "this list is incomplete" signal, and changing the response to a
	// wrapped object would be a breaking wire-shape change for every
	// existing caller. Given that choice, silently returning whatever the
	// healthy store has — indistinguishable on the wire from "this really is
	// the complete list" — is worse than an honest 500: a partial 200 here
	// would repeat, one layer up, the exact bug this function was written to
	// fix (see the doc comment above). A future wire-shape change to carry an
	// explicit partial/degraded flag is a legitimate alternative but requires
	// a coordinated SPA update, not a decision to make unilaterally here.
	if len(errs) > 0 {
		slog.Error("rest: list agent sessions: store read failed", "agent_id", agentID, "errors", errs)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not list sessions: %v", errors.Join(errs...)))
		return
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})

	// Route every session through unifiedMetaToGenSession so required arrays
	// (Partitions) marshal as [] not null — Zod requires type:array on the SPA.
	genSessions := make([]gen.Session, 0, len(metas))
	for _, m := range metas {
		genSessions = append(genSessions, unifiedMetaToGenSession(m))
	}
	jsonOK(w, genSessions)
}

// resolveSessionStore finds which agent's UnifiedStore owns the given sessionID.
// Delegates to the shared AgentLoop method.
func (a *restAPI) resolveSessionStore(sessionID string) *session.UnifiedStore {
	return a.agentLoop.ResolveSessionStore(sessionID)
}

// Skill, SessionDetail, GatewayStatus, and Provider response types are defined
// in contracts/components/schemas/ and generated into pkg/api/generated/.
// Use gen.Skill, gen.SessionDetail, gen.GatewayStatus, and gen.Provider directly.

// strVal extracts a string value from a JSON-decoded map, returning "" if missing or wrong type.
func strVal(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// isSeedTemplateRow reports whether a providers[] entry is a fresh-install
// TEMPLATE rather than something the operator configured (ADR-067 FR-029).
//
// The test used to be `Provider == ""`, because seed templates carried no
// provider identity at all. ADR-067 FR-011 made the provider id mandatory —
// a row IS the pair (provider, model) — so identity no longer distinguishes
// them. What does is that a template names a provider and supplies NOTHING
// with which to reach it: no credential, no endpoint, no model list, no PUT
// stamp and no auth method. The moment any of those is present, an operator
// has touched the row and it is a configuration.
func isSeedTemplateRow(m *config.ModelConfig) bool {
	if m == nil {
		return true
	}
	return strings.TrimSpace(m.Provider) == "" ||
		(m.APIKeyRef == "" &&
			m.APIBase == "" &&
			m.AuthMethod == "" &&
			m.UpdatedAt == nil &&
			len(m.Models) == 0)
}

// Agent response type is defined in contracts/components/schemas/Agent.yaml
// and generated into pkg/api/generated/. Use gen.Agent directly.

// agentWorkspacePath returns the expanded workspace directory for the named agent.
// Per FUNC-11 (BRD), each custom agent gets its own isolated workspace directory.
// If the agent has an explicit workspace set, that is used (with ~ expansion).
// Otherwise, a per-agent directory is derived: ~/.omnipus/agents/{agentID}/.
// The system agent uses the default workspace from config.
//
// Returns (path, error). Callers must handle the error; a non-nil error means the
// workspace could not be created and the returned path may be unusable.
func agentWorkspacePath(cfg interface {
	AgentHomeBasePath() string
}, agentID, agentWorkspace, omnipusHome string,
) (string, error) {
	if agentWorkspace != "" {
		// AgentConfig.Home may contain "~"; expand it the same way config does.
		if len(agentWorkspace) > 0 && agentWorkspace[0] == '~' {
			home, err := os.UserHomeDir()
			if err != nil {
				slog.Error("rest: agentWorkspacePath: UserHomeDir failed", "error", err)
				return agentWorkspace, fmt.Errorf("UserHomeDir: %w", err)
			}
			if len(agentWorkspace) > 1 && (agentWorkspace[1] == '/' || agentWorkspace[1] == filepath.Separator) {
				return home + agentWorkspace[1:], nil
			}
			return home, nil
		}
		return agentWorkspace, nil
	}
	// Per-agent isolated workspace (FUNC-11). Use OMNIPUS_HOME/agents/{id}
	// to match where system.agent.create writes SOUL.md.
	if agentID != "" {
		base := omnipusHome
		if base == "" {
			// Fallback to ~/.omnipus if homePath not provided.
			home, err := os.UserHomeDir()
			if err != nil {
				slog.Error("rest: agentWorkspacePath: UserHomeDir failed", "error", err)
				return cfg.AgentHomeBasePath(), fmt.Errorf("UserHomeDir: %w", err)
			}
			base = filepath.Join(home, ".omnipus")
		}
		agentDir := filepath.Join(base, "agents", agentID)
		cleaned := filepath.Clean(agentDir)
		safePrefix := filepath.Clean(base)
		if !strings.HasPrefix(cleaned, safePrefix) {
			return "", fmt.Errorf("agent workspace path escapes omnipus home: %s", cleaned)
		}
		if err := os.MkdirAll(cleaned, 0o755); err != nil {
			slog.Error("rest: agentWorkspacePath: MkdirAll failed", "path", cleaned, "error", err)
			return cleaned, fmt.Errorf("MkdirAll %s: %w", cleaned, err)
		}
		return cleaned, nil
	}
	return cfg.AgentHomeBasePath(), nil
}

// readSoulMD returns the contents of SOUL.md for the given workspace.
// Used by listAgents to determine draft status without reading all three agent files.
func readSoulMD(workspace string) string {
	data, err := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("rest: readSoulMD: cannot read SOUL.md", "workspace", workspace, "error", err)
		}
		return ""
	}
	return string(data)
}

// readAgentFiles returns the contents of SOUL.md and HEARTBEAT.md from the
// given workspace directory. Missing files return an empty string without
// logging an error — their absence is expected for newly created agents.
// Permission and other I/O errors (not IsNotExist) are logged at Warn level (M11).
func readAgentFiles(workspace string) (soul, heartbeat string) {
	if data, err := os.ReadFile(filepath.Join(workspace, "SOUL.md")); err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("rest: readAgentFiles: cannot read SOUL.md", "workspace", workspace, "error", err)
		}
	} else {
		soul = string(data)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "HEARTBEAT.md")); err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("rest: readAgentFiles: cannot read HEARTBEAT.md", "workspace", workspace, "error", err)
		}
	} else {
		heartbeat = string(data)
	}
	return soul, heartbeat
}

// activeAgentIDSet returns a set of agent IDs that currently have an active turn.
func (a *restAPI) activeAgentIDSet() map[string]bool {
	ids := a.agentLoop.GetActiveAgentIDs()
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// computeAgentStatus determines the agent status based on whether it is active,
// has a non-empty SOUL.md, or is a locked core agent.
func computeAgentStatus(agentID string, activeIDs map[string]bool, soul string, locked bool) string {
	if activeIDs[agentID] {
		return "active"
	}
	// Core agents (locked) have compiled prompts — always idle (never draft).
	if locked {
		return "idle"
	}
	if strings.TrimSpace(soul) == "" {
		return "draft"
	}
	return "idle"
}

// applyAgentOverrides copies per-agent execution overrides onto a
// defaults-seeded wire Agent. MaxToolIterations: per-agent value wins when
// set (>0); 0 means "inherit" and leaves the effective default in place.
func applyAgentOverrides(ag *gen.Agent, ac *config.AgentConfig) {
	if ac.MaxToolIterations > 0 {
		ag.MaxToolIterations = ac.MaxToolIterations
	}
	// memory_enabled (ADR-052 FR-039): every response path (list/get/create/
	// update) funnels through this function, so populating it here once
	// covers all of them. Previously never set here — the wire field is
	// `omitempty` on the generated Go struct, so an unset pointer meant the
	// key was silently dropped from every JSON response and the SPA always
	// rendered memory as on, even for the seeded-memory-off Judge. Always
	// resolve via MemoryEnabledEffective() (nil → true) rather than echoing
	// the raw possibly-nil ac.MemoryEnabled, so the wire always carries the
	// agent's actual effective value.
	memEnabled := ac.MemoryEnabledEffective()
	ag.MemoryEnabled = &memEnabled
	// shell_policy: echo the persisted per-agent override. Previously this was
	// persisted (updateAgent) or should have been persisted (createAgent, fixed
	// alongside this) but never surfaced on any response path (list/get/create/
	// update all built gen.Agent without ever touching this field) — a GET
	// could never confirm what was saved.
	if ac.ShellPolicy != nil {
		// The literal below mirrors the inlined anonymous-struct shape
		// oapi-codegen generated for gen.Agent.ShellPolicy — field
		// names/types/tags (and order) must match for the assignment to
		// gen.Agent.ShellPolicy below to type-check.
		sp := struct { // not-wire-format: generated gen.Agent.ShellPolicy inline shape, only populates the generated field
			CustomDenyPatterns *[]string `json:"custom_deny_patterns,omitempty"`
			EnableDenyPatterns *bool     `json:"enable_deny_patterns,omitempty"`
		}{
			EnableDenyPatterns: boolPtr(ac.ShellPolicy.EnableDenyPatterns),
		}
		if len(ac.ShellPolicy.CustomDenyPatterns) > 0 {
			cdp := make([]string, len(ac.ShellPolicy.CustomDenyPatterns))
			copy(cdp, ac.ShellPolicy.CustomDenyPatterns)
			sp.CustomDenyPatterns = &cdp
		}
		ag.ShellPolicy = &sp
	}
	// fallback_models: P-F2 — this was persisted correctly (createAgent/updateAgent
	// both write ac.FallbackModels to config.json) but never echoed back on ANY
	// response path (list/get/update all built gen.Agent without ever touching
	// this field), so a GET could never confirm what was saved and a reopened
	// agent's UI always rendered the field empty even though it was safely on
	// disk — mirrors the ShellPolicy fix above. config.FallbackModel.Provider is
	// a bare string (empty when unset); gen.FallbackModel.Provider is a pointer,
	// so translate unconditionally (mirrors getMemorySettings' identical
	// config->wire FallbackModel translation for recap_fallback_models).
	if len(ac.FallbackModels) > 0 {
		fm := make([]gen.FallbackModel, len(ac.FallbackModels))
		for i, m := range ac.FallbackModels {
			fm[i] = gen.FallbackModel{Model: m.Model, Provider: &m.Provider}
		}
		ag.FallbackModels = &fm
	}
}

// buildAgentDefaults populates the execution-related fields from config defaults.
func buildAgentDefaults(cfg *config.Config) gen.Agent {
	// Effective per-turn tool-round cap: mirror the runtime resolution
	// (pkg/agent/instance.go) — defaults value when set, else 200 — so the
	// wire never reports a meaningless 0 (a zeroed default was the visible
	// half of the 2026-07-03 P0: the UI showed and re-persisted 0s).
	maxIter := cfg.Agents.Defaults.MaxToolIterations
	if maxIter <= 0 {
		maxIter = 200
	}
	return gen.Agent{
		TimeoutSeconds:    cfg.Agents.Defaults.TimeoutSeconds,
		MaxToolIterations: maxIter,
		// Required string fields — initialized to empty (overwritten per-agent).
		Soul: "",
	}
}

// readChannelConfigRaw reads config.json from disk and returns the raw map for
// the given channel. Both getChannelConfig and testChannel use this to avoid
// reading stale in-memory config after async reloads.
func (a *restAPI) readChannelConfigRaw(channelID string) (map[string]any, error) {
	a.configMu.Lock()
	raw, err := os.ReadFile(a.configPath())
	a.configMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	channels, _ := m["channels"].(map[string]any)
	if channels == nil {
		return map[string]any{}, nil
	}
	chCfg, _ := channels[channelID].(map[string]any)
	if chCfg == nil {
		return map[string]any{}, nil
	}
	return chCfg, nil
}

func (a *restAPI) listAgents(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()
	agents := make([]gen.Agent, 0, len(cfg.Agents.List))
	activeIDs := a.activeAgentIDSet()

	defaults := buildAgentDefaults(cfg)
	defaultModel := cfg.Agents.Defaults.DefaultModel.Model
	for _, ac := range cfg.Agents.List {
		model := defaultModel
		if ac.Model != nil && ac.Model.Primary != "" {
			model = ac.Model.Primary
		}
		workspace, wsErr := agentWorkspacePath(cfg, ac.ID, ac.Home, a.homePath)
		if wsErr != nil {
			slog.Warn("rest: listAgents: could not resolve workspace", "agent_id", ac.ID, "error", wsErr)
		}
		// M2: listAgents only needs SOUL.md to determine draft status — avoid reading
		// HEARTBEAT.md and AGENT.md unnecessarily in the list endpoint.
		// Core agents have compiled prompts — do not expose them via SOUL.md.
		// ADR-052 FR-038: System Agents (the Judge) are the carve-out — their soul
		// IS their (operator-editable) verifier rubric, not a compiled prompt, so
		// it must render like any custom agent's soul despite Locked==true.
		var soul string
		if !ac.Locked || ac.IsSystem() {
			soul = readSoulMD(workspace)
		}
		ag := defaults
		ag.Id = ac.ID
		ag.Name = ac.Name
		if ac.Description != "" {
			ag.Description = &ac.Description
		}
		if ac.Color != "" {
			ag.Color = &ac.Color
		}
		if ac.Icon != "" {
			ag.Icon = &ac.Icon
		}
		ag.Type = coreagent.ToWireType(ac)
		ag.Locked = ac.Locked
		applyAgentOverrides(&ag, &ac)
		ag.Model = &model
		setAgentModelProvider(&ag, ac.Model)
		ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, activeIDs, soul, ac.Locked))
		ag.Soul = soul
		// The wire `default` is DERIVED from the settings singleton
		// (cfg.Agents.Defaults.DefaultAgentID), never read from the per-entity
		// ac.Default bool — see updateAgent's singleton-write block for why:
		// nothing (routing, registry.GetDefaultAgent) has consulted the
		// per-entity flag since ADR-054 D6.4, so echoing it back here would
		// silently disagree with which agent actually receives inbound
		// messages with no more-specific routing rule.
		ag.Default = boolPtr(ac.ID == cfg.Agents.Defaults.DefaultAgentID)
		// ADR-068 FR-014 (T068-08): needs_model is derived, never stored.
		ag.NeedsModel = agentNeedsModel(cfg, &ac)
		// ADR-067 FR-016/FR-031 (T067-09): degraded_reason is derived too,
		// from the SAME predicate the agent runtime's pre-turn gate uses.
		// Both flags may be true; `needs_provider` wins in copy (the SPA's
		// concern) and they stay separate fields on the wire.
		ag.DegradedReason = agentDegradedReason(a.providerCatalog, cfg, &ac)
		if len(ac.Skills) > 0 {
			skills := make([]string, len(ac.Skills))
			copy(skills, ac.Skills)
			ag.Skills = &skills
		}
		setAgentExecutorResponse(&ag, ac.Subagents)
		if ac.UpdatedAt != nil {
			ag.UpdatedAt = ac.UpdatedAt
		}
		agents = append(agents, ag)
	}

	jsonOK(w, agents)
}

func (a *restAPI) getAgent(w http.ResponseWriter, id string) {
	cfg := a.agentLoop.GetConfig()
	defaults := buildAgentDefaults(cfg)
	activeIDs := a.activeAgentIDSet()

	for _, ac := range cfg.Agents.List {
		if ac.ID == id {
			model := cfg.Agents.Defaults.DefaultModel.Model
			if ac.Model != nil && ac.Model.Primary != "" {
				model = ac.Model.Primary
			}
			workspace, wsErr := agentWorkspacePath(cfg, ac.ID, ac.Home, a.homePath)
			if wsErr != nil {
				slog.Warn("rest: getAgent: could not resolve workspace", "agent_id", ac.ID, "error", wsErr)
			}
			soul, _ := readAgentFiles(workspace)
			// Core agents have compiled prompts — do not expose them.
			// ADR-052 FR-038: System Agents (the Judge) are exempted — their soul
			// is their operator-editable verifier rubric, not a compiled prompt.
			if ac.Locked && !ac.IsSystem() {
				soul = ""
			}
			ag := defaults
			ag.Id = ac.ID
			ag.Name = ac.Name
			if ac.Description != "" {
				ag.Description = &ac.Description
			}
			if ac.Color != "" {
				ag.Color = &ac.Color
			}
			if ac.Icon != "" {
				ag.Icon = &ac.Icon
			}
			ag.Type = coreagent.ToWireType(ac)
			ag.Locked = ac.Locked
			applyAgentOverrides(&ag, &ac)
			ag.Model = &model
			setAgentModelProvider(&ag, ac.Model)
			ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, activeIDs, soul, ac.Locked))
			ag.Soul = soul
			// Derived from the settings singleton — see listAgents' comment on
			// the same line shape for the full rationale.
			ag.Default = boolPtr(ac.ID == cfg.Agents.Defaults.DefaultAgentID)
			// ADR-068 FR-014 (T068-08): needs_model is derived, never stored.
			ag.NeedsModel = agentNeedsModel(cfg, &ac)
			// ADR-067 FR-016/FR-031 (T067-09) — see listAgents.
			ag.DegradedReason = agentDegradedReason(a.providerCatalog, cfg, &ac)
			if len(ac.Skills) > 0 {
				skills := make([]string, len(ac.Skills))
				copy(skills, ac.Skills)
				ag.Skills = &skills
			}
			setAgentExecutorResponse(&ag, ac.Subagents)
			if ac.UpdatedAt != nil {
				ag.UpdatedAt = ac.UpdatedAt
			}
			jsonOK(w, ag)
			return
		}
	}

	jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", id))
}

// agentCreateToolsCfgInput is a request-shape-agnostic normalization of the
// wire ToolsCfg object. gen.AgentCreateRequestMain and gen.AgentCreateRequestSubagent
// each carry an anonymous ToolsCfg struct (oapi-codegen inlines it per-variant,
// distinct named enum types per variant) — createAgent normalizes whichever
// variant was sent into this shape so the ac.Tools construction below is
// written once. gen.AgentCreateRequestSubagent3p has no tools_cfg property at
// all (the external runner manages its own tools), so this stays nil for that
// variant.
type agentCreateToolsCfgInput struct {
	BuiltinPolicies map[string]string
	MCPServers      []agentCreateMCPServerInput
}

// agentCreateMCPServerInput is one entry of agentCreateToolsCfgInput.MCPServers.
type agentCreateMCPServerInput struct {
	ID    string
	Tools []string
}

// agentCreateShellPolicyInput is a request-shape-agnostic normalization of
// the wire ShellPolicy object, mirroring agentCreateToolsCfgInput above.
// gen.AgentCreateRequestMain and gen.AgentCreateRequestSubagent each carry an
// anonymous ShellPolicy struct (oapi-codegen inlines it per-variant);
// gen.AgentCreateRequestSubagent3p has no shell_policy property at all (the
// external runner manages its own isolation), so this stays nil for that
// variant.
type agentCreateShellPolicyInput struct {
	EnableDenyPatterns *bool
	CustomDenyPatterns []string
}

// decodeAgentCreateVariant strictly decodes raw into out (a pointer to one of
// the three generated AgentCreateRequest{Main,Subagent,Subagent3p} structs)
// using a json.Decoder with DisallowUnknownFields, so a field that variant
// does not carry — at any nesting depth (e.g. a "system.*" key inside a
// tools_cfg.builtin.policies map is fine; an unrelated top-level or nested
// property is not) — is rejected with 400 rather than silently dropped. This
// runs UNCONDITIONALLY (independent of cfg.Gateway.ValidateInbound, which
// defaults to false) — it is the mechanism that makes "a field sent on the
// wrong variant is a schema violation, never silently persisted" actually
// true regardless of config.
//
// On success it returns true and leaves w untouched. On failure it writes the
// 400 response itself and returns false so the caller can `return` directly.
func decodeAgentCreateVariant(w http.ResponseWriter, raw []byte, wireType, variantName string, out any) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf(
				"field not allowed on agent type %q: %v — see the %s schema", wireType, err, variantName,
			))
		} else {
			jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}
	return true
}

// wireStringMap converts a generated map whose values are a string-based enum
// (e.g. map[string]AgentCreateRequestMainToolsCfgBuiltinPolicies) into a plain
// map[string]string, preserving nil-vs-non-nil: a nil input returns nil
// (field absent); a non-nil (possibly empty) input returns a newly allocated
// map with every value stringified. Builtin.Policies is a required,
// non-pointer map on the wire (there is no default_policy fallback any more —
// CLAUDE.md hard constraint 6), so this takes the map directly rather than a
// pointer-to-map.
func wireStringMap[V ~string](in map[string]V) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = string(v)
	}
	return out
}

// agentToolPolicyMapFromWire converts a generated per-tool policy map (whose
// values are a request-specific string enum, e.g.
// AgentUpdateRequestToolsCfgBuiltinPolicies) into config.ToolPolicy values.
// Used to build a candidate config.AgentBuiltinToolsCfg for
// config.ValidateToolPolicyCoverage before persisting a write (CLAUDE.md hard
// constraint 6) — never mutates anything, purely a type conversion.
func agentToolPolicyMapFromWire[V ~string](in map[string]V) map[string]config.ToolPolicy {
	if in == nil {
		return nil
	}
	out := make(map[string]config.ToolPolicy, len(in))
	for k, v := range in {
		out[k] = config.ToolPolicy(v)
	}
	return out
}

// validateCandidateToolPolicyCoverage clones cfg, applies mutate to the
// clone, then checks tool-policy coverage (config.ValidateToolPolicyCoverage,
// CLAUDE.md hard constraint 6). Returns gaps (nil = covered) or an error if
// cloning failed. Shared by all 4 REST write paths that must reject an
// incomplete tool-policy map before persisting: createAgent, updateAgent,
// updateAgentTools (this file) and putToolPolicies (rest_tool_policies.go).
//
// Callers must hold a.configMu across both this validation call and the
// subsequent persist (via updateConfigJSONLocked) so the two steps form one
// atomic critical section — otherwise a second concurrent write could slip
// in between validation and persist and reintroduce the exact coverage gap
// this check exists to prevent (see updateConfigJSONLocked's doc comment).
func (a *restAPI) validateCandidateToolPolicyCoverage(
	cfg *config.Config,
	mutate func(*config.Config),
) ([]config.CoverageGap, error) {
	candidateCfg, err := cfg.Clone()
	if err != nil {
		return nil, err
	}
	mutate(candidateCfg)
	return config.ValidateToolPolicyCoverage(candidateCfg, buildKnownBuiltinToolNames()), nil
}

// withToolPolicyCoverageGuard runs the shared "fetch-fresh, validate,
// persist" critical section for every write path that must reject an
// incomplete tool-policy map before persisting (CLAUDE.md hard constraint 6).
// It always fetches the CURRENT live config fresh, INSIDE a.configMu — never
// a caller-supplied snapshot — because fetching before the lock (as
// updateAgent/updateAgentTools used to) reopens the exact TOCTOU window the
// lock exists to close (see updateConfigJSONLocked's doc comment): a
// concurrent write between the pre-lock fetch and the lock acquisition could
// swap the live config, so validation would silently run against stale data.
//
// mutate receives a clone of that freshly-fetched config and must locate any
// agent it touches by ID (never a pre-lock-computed slice index — a
// concurrently-changed agent list can shift or shrink that index between
// fetch and lock). gapErrMsg renders the 400 body when coverage is
// incomplete. persist is handed to updateConfigJSONLocked as-is: it must
// perform its own fresh, ID-based lookup against the on-disk JSON map (never
// trust a pre-lock index there either) and return errAgentVanishedDuringUpdate
// if the target it expects to find is gone (mapped to 404 below), or
// errConflict for an optimistic-concurrency mismatch (mapped to 409) — both
// sentinels are safe to check unconditionally since only the call sites that
// actually use them will ever produce them.
//
// mutate may be nil: some writes (e.g. updateAgent fields that have nothing
// to do with tools_cfg — default flag, model, skills, …) must NOT run the
// coverage check at all, because config.ValidateToolPolicyCoverage checks
// EVERY agent in the whole roster, not just the one this write touches. A
// pre-existing agent with an incomplete tools map (a config seeded before
// full coverage enumeration, or a bare test fixture) would then turn an
// entirely unrelated field update into a spurious 400 — a real regression
// caught by rest_routing_test.go's default-flag fixtures, which carry no
// Tools field at all. Skipping the check when mutate is nil restores the
// original "only validate when this write actually changes tool policy"
// behavior while still closing the TOCTOU for call sites that DO validate.
//
// Returns false once it has already written the HTTP response (error/reject
// case); the caller just returns in that case.
func (a *restAPI) withToolPolicyCoverageGuard(
	w http.ResponseWriter,
	mutate func(*config.Config),
	gapErrMsg func(gaps []config.CoverageGap) string,
	persist func(map[string]any) error,
	persistErrLogMsg string,
) bool {
	a.configMu.Lock()
	defer a.configMu.Unlock()

	if mutate != nil {
		cfg := a.agentLoop.GetConfig()
		gaps, err := a.validateCandidateToolPolicyCoverage(cfg, mutate)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("tool policy coverage check: %v", err))
			return false
		}
		if len(gaps) > 0 {
			jsonErr(w, http.StatusBadRequest, gapErrMsg(gaps))
			return false
		}
	}
	if err := a.updateConfigJSONLocked(persist); err != nil {
		if errors.Is(err, errConflict) {
			writeJSON(w, http.StatusConflict, gen.ErrorResponse{
				Error: "conflict",
				Code:  strPtr("conflict"),
			})
			return false
		}
		if errors.Is(err, errAgentVanishedDuringUpdate) {
			jsonErr(w, http.StatusNotFound, err.Error())
			return false
		}
		slog.Error(persistErrLogMsg, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return false
	}
	return true
}

// fastAgentUpsert is createAgent/updateAgent's ADR-054-completing fast path
// (issue #571) for publishing a single agent create/update into the live
// AgentRegistry, instead of a full config reload that restarts channels,
// cron, schedulers, and the plan engine (up to ~60s under load).
//
// By the time either handler calls this, updateConfigJSONLocked has ALREADY
// run (via withToolPolicyCoverageGuard) and its refreshConfigAndRewireServices
// call has ALREADY re-read config.json and repopulated cfg.Agents.List from
// the entity store — so a.agentLoop.GetConfig() here already contains
// agentID's just-persisted entity record, and (for updateAgent's
// default-agent-ID-flip case) the just-written agents.defaults.default_agent_id
// singleton too. This function only has to swap the ONE affected
// AgentInstance into the AgentRegistry; see AgentLoop.UpsertAgentFast for the
// resolver/default-override rebuild (TRAP 1) and the atomic, lost-update-safe
// publish (TRAP 2).
//
// Returns "" on success, or a non-empty warning string mirroring
// createAgent/updateAgent's existing "warning" response field. Any failure —
// the agent unexpectedly missing from the just-refreshed config, or a wiring
// error inside UpsertAgentFast — falls back to the slow, already-hardened
// full reload (triggerReloadAndWait) rather than leaving a half-wired agent
// live: the exact risk AgentRegistry.UpsertAgent's own doc comment warns a
// bare caller into.
//
// Defers to an ALREADY in-flight full reload rather than racing it:
// AgentLoop.UpsertAgentFast's own doc documents a known, narrow residual —
// a full ReloadProviderAndConfig that started with an OLDER config snapshot
// (predating this agent's entity write) can complete AFTER this function's
// fast-path publish and silently overwrite it, since that reload's snapshot
// never saw the new agent. This is exactly the class of race
// reload_coalescing_test.go's coalescing fix exists to close (a create
// landing mid-reload must never be lost). Checking IsReloadPending() first
// and, when true, going straight to the coalescing-aware
// triggerReloadAndWait (which waits out the in-flight reload AND any
// coalesced follow-up that re-reads config fresh) keeps that guarantee
// intact — it only costs the full reload's latency in the narrower case
// where one is already running for some other reason, not on every
// create/update.
func (a *restAPI) fastAgentUpsert(agentID string) string {
	if a.agentLoop.IsReloadPending() {
		return a.fallbackFullReload()
	}
	err := a.testForceFastUpsertErr
	if err == nil {
		cfg := a.agentLoop.GetConfig()
		_, err = a.agentLoop.UpsertAgentFast(cfg, agentID)
	}
	if err != nil {
		slog.Error("rest: fast agent upsert failed; falling back to full reload",
			"agent_id", agentID, "error", err)
		return a.fallbackFullReload()
	}
	return ""
}

// fallbackFullReload runs the slow, well-tested full config reload
// (triggerReloadAndWait) and renders its error, if any, as a warning string
// in the same shape createAgent/updateAgent already surface on their
// "warning" response field. Used when fastAgentUpsert cannot complete the
// narrow single-agent path.
func (a *restAPI) fallbackFullReload() string {
	if err := a.triggerReloadAndWait(); err != nil {
		return fmt.Sprintf("config reload failed: %v", err)
	}
	return ""
}

// joinCoverageGapMessages renders a []config.CoverageGap as a single
// semicolon-joined human-readable string for 400 error bodies — the smallest
// adaptation for existing strings.Join call sites now that
// config.ValidateToolPolicyCoverage returns a structured []CoverageGap
// instead of []string (see CoverageGap.String's doc comment).
func joinCoverageGapMessages(gaps []config.CoverageGap) string {
	msgs := make([]string, len(gaps))
	for i, g := range gaps {
		msgs[i] = g.String()
	}
	return strings.Join(msgs, "; ")
}

// agentCreateShellPolicyFromWire converts either variant's shell_policy wire
// object into the common agentCreateShellPolicyInput. gen.AgentCreateRequestMain
// and gen.AgentCreateRequestSubagent each generate their own anonymous
// ShellPolicy struct, but — unlike ToolsCfg below — neither carries a
// per-variant enum type, so the two anonymous types are structurally
// identical and one non-generic helper handles both call sites.
func agentCreateShellPolicyFromWire(wp *struct {
	CustomDenyPatterns *[]string `json:"custom_deny_patterns,omitempty"`
	EnableDenyPatterns *bool     `json:"enable_deny_patterns,omitempty"`
},
) *agentCreateShellPolicyInput {
	if wp == nil {
		return nil
	}
	out := &agentCreateShellPolicyInput{EnableDenyPatterns: wp.EnableDenyPatterns}
	if wp.CustomDenyPatterns != nil {
		out.CustomDenyPatterns = *wp.CustomDenyPatterns
	}
	return out
}

// agentCreateToolsCfgFromWire converts either variant's tools_cfg wire object
// into the common agentCreateToolsCfgInput, or nil when tc is nil. Generic
// over P — the per-variant enum type oapi-codegen emits for Builtin.Policies'
// map values (AgentCreateRequestMainToolsCfgBuiltinPolicies vs
// AgentCreateRequestSubagentToolsCfgBuiltinPolicies, both underlying type
// string) — since that's the only reason ToolsCfg isn't structurally
// identical across variants the way ShellPolicy is; every other field
// (including the Mcp.Servers element shape, which carries no enum) is
// identical, so the whole tools_cfg object is accepted directly (Go's
// generic type inference resolves P from tc's concrete argument type) rather
// than pre-extracting each sub-field per call site.
//
// There is no default_policy field on the wire (CLAUDE.md hard constraint
// 6): Builtin.Policies is a required, fully-enumerated per-tool map.
// createAgent enforces completeness via config.ValidateToolPolicyCoverage
// against a candidate config snapshot before persisting — this helper only
// normalizes the wire shape.
func agentCreateToolsCfgFromWire[P ~string](tc *struct {
	Builtin *struct {
		Policies map[string]P `json:"policies"`
	} `json:"builtin,omitempty"`
	Mcp *struct {
		Servers *[]struct {
			Id    string    `json:"id"`
			Tools *[]string `json:"tools,omitempty"`
		} `json:"servers,omitempty"`
	} `json:"mcp,omitempty"`
},
) *agentCreateToolsCfgInput {
	if tc == nil {
		return nil
	}
	out := &agentCreateToolsCfgInput{}
	if tc.Builtin != nil {
		out.BuiltinPolicies = wireStringMap(tc.Builtin.Policies)
	}
	if tc.Mcp != nil && tc.Mcp.Servers != nil {
		for _, s := range *tc.Mcp.Servers {
			var t []string
			if s.Tools != nil {
				t = *s.Tools
			}
			out.MCPServers = append(out.MCPServers, agentCreateMCPServerInput{ID: s.Id, Tools: t})
		}
	}
	return out
}

// createAgent handles POST /api/v1/agents.
//
// The wire contract is a discriminated union: AgentCreateRequestMain /
// AgentCreateRequestSubagent / AgentCreateRequestSubagent3p, each a distinct
// schema (contracts/components/schemas/AgentCreateRequest{Main,Subagent,Subagent3p}.yaml,
// additionalProperties: false) selected by the request's `type` field. This
// handler mirrors the WsFrame dispatch pattern (websocket.go's peek+switch on
// frame.Type): buffer the body, peek `type` from the raw JSON, resolve the
// matching variant schema name, optionally validate the FULL body against
// that JSON Schema (when cfg.Gateway.ValidateInbound is enabled — richer
// errors, off by default), then strictly decode into the NAMED variant
// struct via decodeAgentCreateVariant (a field the chosen variant does not
// carry is rejected 400 unconditionally, independent of ValidateInbound) —
// never the generated union wrapper's As*() accessors — and normalize into a
// small set of variant-agnostic locals so the remainder of the handler
// (validation, persistence, response building) is written once.
//
// type is REQUIRED on every variant: a missing or unrecognized type value is
// a 400.
func (a *restAPI) createAgent(w http.ResponseWriter, r *http.Request) {
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(w, http.StatusBadRequest, "request body is required")
		return
	}

	var typePeek struct {
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(raw, &typePeek); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	const typeErrMsg = "type is required and must be one of Main, Subagent, subagent_3p"
	if typePeek.Type == nil {
		jsonErr(w, http.StatusBadRequest, typeErrMsg)
		return
	}
	// ADR-049 D3: System Agents (the Judge category) are seed-only. The ONLY
	// creation path is coreagent.SeedConfig — never the REST create path nor the
	// create_agent tool. Reject with a precise message before the generic
	// variant switch so a client sending {"type":"system"} gets a clear 400.
	if *typePeek.Type == "system" {
		jsonErr(w, http.StatusBadRequest, "system agents are not creatable")
		return
	}
	var variantName string
	switch *typePeek.Type {
	case "Main":
		variantName = "AgentCreateRequestMain"
	case "Subagent":
		variantName = "AgentCreateRequestSubagent"
	case "subagent_3p":
		variantName = "AgentCreateRequestSubagent3p"
	default:
		// Covers "core" / "system" (seeded-only classifications — the only way
		// to obtain one is via SeedConfig, never the REST create path) and any
		// other unrecognized value.
		jsonErr(w, http.StatusBadRequest, typeErrMsg)
		return
	}
	// Boundary translation between wire (Main/Subagent/subagent_3p) and
	// persisted config (custom/worker) — single source of truth in
	// coreagent.ResolveType.
	createType := coreagent.ResolveType(gen.AgentType(*typePeek.Type))

	if validateEnabled {
		if errMsg, serverErr := validateBodyAgainstSchema(variantName, raw); errMsg != "" {
			if serverErr {
				jsonErr(w, http.StatusInternalServerError, "inbound schema unavailable")
			} else {
				jsonErr(w, http.StatusBadRequest,
					fmt.Sprintf("request body does not match schema %s: %s", variantName, errMsg))
			}
			return
		}
	}

	// Normalize the chosen variant into variant-agnostic locals. Each branch
	// strictly decodes raw into the NAMED generated struct via
	// decodeAgentCreateVariant (unknown fields — including fields that ARE
	// valid on a sibling variant, e.g. Subagent's tools_cfg on a
	// subagent_3p body — are rejected 400, independent of ValidateInbound)
	// and copies out only the fields that variant actually carries.
	// AgentCreateRequestSubagent has no Executor field at all;
	// AgentCreateRequestSubagent3p has no ToolsCfg/Skills/FallbackModels/
	// ShellPolicy/Voice/MaxToolIterations
	// fields — a subagent_3p create supplying any of those is rejected at
	// decode time, both because the Go type has no matching field and
	// because the strict decoder refuses to silently drop it.
	var (
		name           string
		description    *string
		model          *string
		provider       *string
		color          *string
		icon           *string
		soul           string
		skills         *[]string
		fallbackModels *[]gen.FallbackModel
		shellPolicyIn  *agentCreateShellPolicyInput
		toolsCfgIn     *agentCreateToolsCfgInput
		executorIn     *executorRequestInput
	)

	switch *typePeek.Type {
	case "Main":
		var vreq gen.AgentCreateRequestMain
		if !decodeAgentCreateVariant(w, raw, *typePeek.Type, variantName, &vreq) {
			return
		}
		name = vreq.Name
		description = vreq.Description
		model = vreq.Model
		provider = vreq.Provider
		color = vreq.Color
		icon = vreq.Icon
		soul = vreq.Soul
		skills = vreq.Skills
		fallbackModels = vreq.FallbackModels
		shellPolicyIn = agentCreateShellPolicyFromWire(vreq.ShellPolicy)
		toolsCfgIn = agentCreateToolsCfgFromWire(vreq.ToolsCfg)
	case "Subagent":
		var vreq gen.AgentCreateRequestSubagent
		if !decodeAgentCreateVariant(w, raw, *typePeek.Type, variantName, &vreq) {
			return
		}
		name = vreq.Name
		description = vreq.Description
		model = vreq.Model
		provider = vreq.Provider
		color = vreq.Color
		icon = vreq.Icon
		soul = vreq.Soul
		skills = vreq.Skills
		fallbackModels = vreq.FallbackModels
		shellPolicyIn = agentCreateShellPolicyFromWire(vreq.ShellPolicy)
		toolsCfgIn = agentCreateToolsCfgFromWire(vreq.ToolsCfg)
	case "subagent_3p":
		var vreq gen.AgentCreateRequestSubagent3p
		if !decodeAgentCreateVariant(w, raw, *typePeek.Type, variantName, &vreq) {
			return
		}
		name = vreq.Name
		description = vreq.Description
		model = vreq.Model
		provider = vreq.Provider
		color = vreq.Color
		icon = vreq.Icon
		soul = vreq.Soul
		executorIn = &executorRequestInput{
			Cli:          executorCliStr(vreq.Executor.Cli),
			CliPath:      vreq.Executor.CliPath,
			EnvOverrides: vreq.Executor.EnvOverrides,
			CliArgs:      vreq.Executor.CliArgs,
		}
	}

	// Trim before the empty check so a whitespace-only name ("   ") is rejected
	// rather than silently accepted (UAT fix). Persist the trimmed value.
	name = strings.TrimSpace(name)
	if name == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	// subagent_3p executor.cli_path: required (spec §9.2), whitespace-only
	// rejected. The schema requires the `executor` object itself to be
	// present for this variant, but its nested cli/cli_path properties are
	// optional strings — JSON Schema has no "non-whitespace" constraint, so
	// this stays a runtime check even with ValidateInbound enabled.
	// executor.kind is intentionally NOT checked here: it is always
	// server-derived to external-cli for subagent_3p below, regardless of
	// what (if anything) the client sent — kind "is exposed in responses but
	// is NOT a writable field on create/update — clients cannot choose kind
	// directly" (contracts/components/schemas/ExecutorConfig.yaml).
	if createType == config.AgentTypeWorker && *typePeek.Type == string(gen.AgentTypeSubagent3p) {
		if executorIn == nil || executorIn.CliPath == nil || strings.TrimSpace(*executorIn.CliPath) == "" {
			jsonErr(w, http.StatusBadRequest, "executor.cli_path is required for subagent_3p agents")
			return
		}
	}
	// Referential validation: reject unknown skill IDs before doing any work.
	if skills != nil && len(*skills) > 0 {
		if errMsg := a.validateSkillIDs(*skills); errMsg != "" {
			jsonErr(w, http.StatusBadRequest, errMsg)
			return
		}
	}
	descTrimmed := ""
	if description != nil {
		descTrimmed = strings.TrimSpace(*description)
	}
	// W2 spec §4.3 / §9.2 F-02 (mirror of PUT path): Subagent and subagent_3p
	// require a non-empty description after trim. A worker without a
	// description cannot be routed to by the orchestrator.
	if createType == config.AgentTypeWorker && descTrimmed == "" {
		jsonErr(w, http.StatusBadRequest, "description is required for worker agents (Subagent, subagent_3p)")
		return
	}
	// O12.1 — voice is Main-only (form matrix row 13): no runtime check is
	// needed here any more. AgentCreateRequestSubagent / …Subagent3p
	// structurally have no `voice` property (additionalProperties: false) —
	// a worker create can no longer carry one at all. Heartbeat is
	// workspace-scoped (ADR-027) and is no longer set at create time.
	//
	// W2 spec §3.1 row 16 / §9.2 F-13 (mirror of PUT path): fallback_models
	// is capped at 2 entries. Server-enforced so direct REST callers (not the
	// SPA) cannot smuggle more entries past the schema validator. subagent_3p
	// has no fallback_models property (fallbackModels stays nil for that variant).
	if fallbackModels != nil && len(*fallbackModels) > 2 {
		jsonErr(w, http.StatusBadRequest, "fallback_models exceeds maxItems: 2")
		return
	}
	// W2 spec §4.7 / §9.2 row 8: whitespace-only soul is rejected (the wire
	// schema enforces minLength:1; whitespace-only is the natural
	// soft-bypass). Backend trims before validation.
	if soul == "" || strings.TrimSpace(soul) == "" {
		jsonErr(w, http.StatusBadRequest, "soul is required (whitespace-only is rejected as minLength violation)")
		return
	}
	colorVal := ""
	if color != nil {
		colorVal = *color
	}
	iconVal := ""
	if icon != nil {
		iconVal = *icon
	}
	// color hex regex (spec §4.4).
	if colorVal != "" {
		if matched, _ := regexp.MatchString(`^#[0-9A-Fa-f]{6}$`, colorVal); !matched {
			jsonErr(w, http.StatusBadRequest, "color must be a valid hex code (e.g. #D4AF37)")
			return
		}
	}
	// icon maxLength:50 (spec §4.4).
	if len(iconVal) > 50 {
		jsonErr(w, http.StatusBadRequest, "icon exceeds maxLength: 50")
		return
	}
	// ac is the in-memory config record. Locked is left at its zero value
	// (false) for BOTH custom and worker creates — the API is the operator's
	// surface for editing; only SeedConfig-seeded agents are locked. A newly
	// created worker is therefore editable (unlike the seeded default
	// general-purpose worker, which is locked by coreagent.SeedConfig).
	ac := config.AgentConfig{
		ID:          uuid.New().String(),
		Name:        name,
		Description: descTrimmed,
		Color:       colorVal,
		Icon:        iconVal,
		Type:        createType,
	}
	if model != nil && *model != "" {
		ac.Model = &config.AgentModelConfig{Primary: *model}
		// O3 two-field model: persist the explicit primary provider when supplied.
		if provider != nil && strings.TrimSpace(*provider) != "" {
			ac.Model.Provider = strings.TrimSpace(*provider)
		}
	}
	// shell_policy: mapped onto AgentConfig so it is actually persisted.
	// subagent_3p has no shell_policy property on the wire (shellPolicyIn
	// stays nil for that variant — the CLI manages its own isolation), so it
	// stays unset there, matching updateAgent's rejection of this field on a
	// subagent_3p PUT.
	if shellPolicyIn != nil {
		sp := &config.AgentShellPolicy{}
		if shellPolicyIn.EnableDenyPatterns != nil {
			sp.EnableDenyPatterns = *shellPolicyIn.EnableDenyPatterns
		}
		if len(shellPolicyIn.CustomDenyPatterns) > 0 {
			sp.CustomDenyPatterns = make([]string, len(shellPolicyIn.CustomDenyPatterns))
			copy(sp.CustomDenyPatterns, shellPolicyIn.CustomDenyPatterns)
		}
		ac.ShellPolicy = sp
	}
	// Heartbeat is workspace-scoped (ADR-027); no per-agent heartbeat at create.
	if skills != nil && len(*skills) > 0 {
		ac.Skills = make([]string, len(*skills))
		copy(ac.Skills, *skills)
	}
	// Sub-agent executor. Mapped into AgentConfig.Subagents.Executor so it is
	// actually persisted. A native Subagent (no Executor property on the wire
	// at all) always gets a native runtime here. A subagent_3p always gets
	// kind=external-cli — the wire schema requires `executor` to be present
	// for that variant and, per the field matrix, kind is server-derived
	// (never client-writable) rather than read from the request.
	if createType == config.AgentTypeWorker {
		if *typePeek.Type == string(gen.AgentTypeSubagent3p) {
			execCfg, errMsg := executorConfigFromRequest(string(config.ExecutorKindExternalCLI), executorIn.Cli)
			if errMsg != "" {
				jsonErr(w, http.StatusBadRequest, errMsg)
				return
			}
			if executorIn.CliPath != nil {
				execCfg.CLIPath = *executorIn.CliPath
			}
			if executorIn.EnvOverrides != nil {
				execCfg.EnvOverrides = *executorIn.EnvOverrides
			}
			if executorIn.CliArgs != nil {
				execCfg.CLIArgs = *executorIn.CliArgs
			}
			ac.Subagents = &config.SubagentsConfig{Executor: execCfg}
		} else {
			ac.Subagents = &config.SubagentsConfig{
				Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative},
			}
		}
	}
	// ADR-037: delegation_policy is retired from the wire entirely — the
	// per-workspace delegation graph (Team tab) is the sole delegation
	// mechanism. There is nothing left to map/validate/persist here.
	// Seed the privilege rail (FR-008/FR-022): custom agents always get every
	// static builtin tool explicitly denied except a narrow, conservative
	// read-only allow-list (coreagent.NewCustomAgentToolsCfg —
	// denyAllThenOverride), unless the caller explicitly overrides individual
	// entries. There is no wildcard involved anywhere in this model (the old
	// "system.*" glob matched zero real tool names and was retired). subagent_3p
	// has no tools_cfg property at all (toolsCfgIn stays nil), so it always
	// falls through to the seeded baseCfg — matching "the runner has its own
	// tools" (field matrix).
	baseCfg := coreagent.NewCustomAgentToolsCfg()
	if toolsCfgIn != nil {
		builtin := config.AgentBuiltinToolsCfg{
			// Inherit the fully-enumerated deny-by-default seed (every static
			// tool present with an explicit "deny" or "allow" entry, no
			// wildcard) so the merged map stays complete even when the
			// caller's policies map is sparse.
			Policies: make(map[string]config.ToolPolicy, len(baseCfg.Builtin.Policies)),
		}
		for k, v := range baseCfg.Builtin.Policies {
			builtin.Policies[k] = v
		}
		// Merge caller-supplied policies; the caller's per-tool entry (exact
		// name, never a wildcard) overrides the corresponding seed entry.
		for k, v := range toolsCfgIn.BuiltinPolicies {
			builtin.Policies[k] = config.ToolPolicy(v)
		}
		ac.Tools = &config.AgentToolsCfg{Builtin: builtin}
		if len(toolsCfgIn.MCPServers) > 0 {
			servers := make([]config.AgentMCPServerBinding, 0, len(toolsCfgIn.MCPServers))
			for _, s := range toolsCfgIn.MCPServers {
				servers = append(servers, config.AgentMCPServerBinding{ID: s.ID, Tools: s.Tools})
			}
			ac.Tools.MCP = config.AgentMCPToolsCfg{Servers: servers}
		}
	} else {
		// No caller-supplied tools config: use the full base config.
		ac.Tools = baseCfg
	}
	// CLAUDE.md hard constraint 6 / config.ValidateToolPolicyCoverage: reject
	// the create if the new agent's tool-policy map — together with the
	// global sandbox.tool_policies — would leave any static builtin tool
	// without an explicit policy entry. Validated against a candidate config
	// snapshot (the live config plus this new agent appended); nothing here
	// persists or mutates the live in-memory config, so a rejected request
	// leaves no partial state behind.
	//
	// The validate step and the persist step below run inside ONE
	// a.configMu-locked critical section (closing a TOCTOU race two
	// concurrent creates could otherwise open — see updateConfigJSONLocked's
	// doc comment), via withToolPolicyCoverageGuard: it returns false once it
	// has already written the HTTP response (error case), so the caller just
	// returns.
	if ok := a.withToolPolicyCoverageGuard(
		w,
		func(c *config.Config) {
			c.Agents.List = append(c.Agents.List, ac)
		},
		func(gaps []config.CoverageGap) string {
			return fmt.Sprintf(
				"tool policy coverage incomplete for new agent (%d gap(s)): %s",
				len(gaps), joinCoverageGapMessages(gaps),
			)
		},
		// ADR-054 D2/§11 checklist item 1: agents are per-entity records under
		// entities/agents/<id>.json, not config.json's agents.list — persist
		// via the agent store instead of splicing the raw config map. `m` is
		// deliberately left untouched (config.json no longer carries the
		// roster); this closure still runs inside updateConfigJSONLocked's
		// a.configMu-guarded critical section, so it composes with the
		// tool-policy-coverage validation above exactly as before.
		// agentstore.Store.Create (via entity.Store.Create) already performs
		// write-then-verify (D6 corollary: it reads the just-written file back
		// and confirms it parses with the expected ID) and stamps CreatedAt —
		// a nil error here means ac is durably confirmed on disk before this
		// handler ever reports success to the caller.
		func(m map[string]any) error {
			if err := agentstore.New(a.homePath).Create(ac.ID, &ac); err != nil {
				return fmt.Errorf("create agent entity record: %w", err)
			}
			return nil
		},
		"rest: save agent entity record for new agent",
	); !ok {
		return
	}
	// Persist the create-time soul to SOUL.md. createAgent previously
	// write-dropped req.Soul: the contract accepted it, the FE sent it, but
	// nothing ever landed on disk — and a "draft" agent created without a
	// soul stayed in the draft state forever on the soul-empty path. Write
	// it here, mirroring the workspace-resolution + WriteFileAtomic pattern
	// used by updateAgent. Trimming is fine; this overwrites any prior
	// SOUL.md (create is the only write gate at this point, so a re-run is
	// idempotent against itself). soul is required on every variant (schema
	// minLength:1, enforced again above), so this is always non-empty in
	// practice — the guard stays as defense-in-depth.
	var createSoulContent string
	if soul != "" {
		createSoulContent = strings.TrimSpace(soul)
		workspace, wsErr := agentWorkspacePath(a.agentLoop.GetConfig(), ac.ID, ac.Home, a.homePath)
		if wsErr != nil {
			slog.Error("rest: agentWorkspacePath for create", "agent_id", ac.ID, "error", wsErr)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not resolve workspace: %v", wsErr))
			return
		}
		soulPath := filepath.Join(workspace, "SOUL.md")
		if err := fileutil.WriteFileAtomic(soulPath, []byte(createSoulContent), 0o600); err != nil {
			slog.Error("rest: write SOUL.md for new agent", "agent_id", ac.ID, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not write SOUL.md: %v", err))
			return
		}
	}
	// Capture the default model name BEFORE the fast upsert to avoid a race
	// between it (which may swap the live config) and the read below.
	defaultModelName := a.agentLoop.GetConfig().Agents.Defaults.DefaultModel.Model

	// Persistence succeeded. Publish the new agent into the live AgentRegistry
	// BEFORE we answer 201 — via the ADR-054 fast path (issue #571), not a
	// full config reload: creating one agent must not restart channels, cron,
	// the plan engine, or rebuild every OTHER agent's instance. fastAgentUpsert
	// re-registers just this one agent (registry.UpsertAgent + a fresh
	// resolver/default-agent-override, see AgentLoop.UpsertAgentFast) so
	// GetAgent/ResolveRoute/GetDefaultAgent all observe it the instant this
	// handler returns — closing the exact "201 followed by POST /tasks 400 on
	// the agent we just created" split a bare cfg.Agents.List append used to
	// leave open. It falls back to the slow, already-hardened full reload on
	// any wiring error, so a failure degrades to "slow but correct" instead of
	// a half-wired agent.
	//
	// The "warning" field signals a partial success — frontend must check this field.
	createReloadWarning := a.fastAgentUpsert(ac.ID)
	// Build the response from local variables only (do NOT read from live config — race).
	respModel := defaultModelName
	if ac.Model != nil && ac.Model.Primary != "" {
		respModel = ac.Model.Primary
	}
	// Capture execution config AFTER reload (TriggerReload may have swapped the live config).
	cfgAfterCreate := a.agentLoop.GetConfig()
	ag := buildAgentDefaults(cfgAfterCreate)
	ag.Id = ac.ID
	ag.Name = ac.Name
	if ac.Description != "" {
		ag.Description = &ac.Description
	}
	if ac.Color != "" {
		ag.Color = &ac.Color
	}
	if ac.Icon != "" {
		ag.Icon = &ac.Icon
	}
	// Type reflects the chosen classification (custom or worker). For
	// "custom" this matches the pre-existing hardcoded behavior. For
	// "worker" it surfaces the create-time choice so the response — and
	// subsequent GET / list reads via coreagent.ResolveType — round-trips the
	// agent kind the caller actually created (Main/Subagent/subagent_3p on the wire).
	ag.Type = coreagent.ToWireType(ac)
	ag.Locked = ac.Locked
	applyAgentOverrides(&ag, &ac)
	ag.Model = &respModel
	setAgentModelProvider(&ag, ac.Model)
	// Echo the just-persisted soul so the FE round-trip works. Status is
	// derived from the soul too: a non-empty soul moves the agent out of "draft".
	ag.Soul = createSoulContent
	ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, nil, createSoulContent, ac.Locked))
	if len(ac.Skills) > 0 {
		skillsResp := make([]string, len(ac.Skills))
		copy(skillsResp, ac.Skills)
		ag.Skills = &skillsResp
	}
	setAgentExecutorResponse(&ag, ac.Subagents)
	if createReloadWarning != "" {
		ag.Warning = &createReloadWarning
	}
	jsonCreated(w, ag)
}

// deleteAgent handles DELETE /api/v1/agents/{id}.
// Removes the agent from config.json and reloads the live config.
// Core (locked) agents cannot be deleted (403).
func (a *restAPI) deleteAgent(w http.ResponseWriter, id string) {
	cfg := a.agentLoop.GetConfig()
	var found *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			found = &cfg.Agents.List[i]
			break
		}
	}
	if found == nil {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", id))
		return
	}
	// ADR-049 D3: System Agents (the Judge) are non-deletable. Checked BEFORE the
	// locked 403 because a System Agent is also locked — the spec requires the
	// system-specific 400 ("not deletable"), not the generic locked 403.
	if found.IsSystem() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "system agents are not deletable",
			"code":  "system_agent_undeletable",
		})
		return
	}
	if found.Locked {
		// Surface the contract's "agent_locked" error code so the SPA can
		// distinguish the locked-agent 403 from generic forbidden. JSON shape
		// mirrors the ErrorResponse schema: { error, code }.
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "cannot delete a locked (core) agent",
			"code":  "agent_locked",
		})
		return
	}
	// ADR-049 D4/FR-065: an agent owning >=1 active (State=running) Plan
	// cannot be deleted outright — the plan engine has no owner left to wake
	// at its next decision point, which would silently stall the loop. The
	// operator must stop/reassign the plan(s) first (or disable the agent,
	// which pauses them instead — see the workspace member-heartbeat
	// enable/disable path in rest_workspaces.go, this codebase's only
	// per-agent enable/disable toggle). Checked before the destructive config
	// write below. Nil-safe: a pre-boot/degraded engine (not yet wired) is a
	// legitimate skip (Wave 2-C1's plan feature simply isn't available in
	// this process), not a fail-open on real data. When the engine IS wired,
	// HasActivePlansOwnedBy fails CLOSED on a plan-store read error (fix-wave
	// finding 1) — this handler mirrors that by refusing the delete (503)
	// rather than treating "could not verify" as "no active plans".
	if pe := agent.GetPlanEngine(a.agentLoop); pe != nil {
		hasActive, err := pe.HasActivePlansOwnedBy(id)
		if err != nil {
			slog.Error("delete agent: could not verify active plan ownership", "agent_id", id, "error", err)
			jsonErr(w, http.StatusServiceUnavailable,
				"could not verify plan ownership; try again")
			return
		}
		if hasActive {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "agent owns active plans; stop or reassign them before deleting this agent",
				"code":  "agent_owns_active_plans",
			})
			return
		}
	}
	// Snapshot the audit fields BEFORE we mutate config.json — `found` still
	// points into the in-memory config and the safeUpdateConfigJSON callback
	// runs before the reload returns.
	deletedName := found.Name
	deletedType := string(found.Type)
	// ADR-054 D2/D6 rule 5/§11 checklist item 2: remove the agent's entity
	// record (entities/agents/<id>.json) FIRST — via the agent store, not by
	// splicing config.json's agents.list — before any best-effort directory
	// cleanup below. Dangling referrers (bindings, mailboxes, workspace
	// core_team) are surfaced for repair per D6 rule 2, never silently
	// pruned here.
	if err := agentstore.New(a.homePath).Delete(id); err != nil {
		slog.Error("rest: deleteAgent: delete agent entity record failed", "agent_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to delete agent")
		return
	}
	// Tell the roster regression guard this shrink was INTENTIONAL before the
	// reload below re-reads the store. Deleting the last agent legitimately
	// empties the roster, which is otherwise indistinguishable from the store
	// failing — see forgetRosterBaseline's doc comment. Without this the
	// post-delete reload is rejected and the in-memory roster keeps serving
	// the agent we just deleted from disk.
	forgetRosterBaseline(a.homePath)
	// Reload the live config so the deleted agent is no longer in memory.
	// triggerReloadAndWait polls until reload completes (or 5s deadline) so the in-memory config is
	// updated before the 204 response is sent back to the caller (prevents a
	// race where an immediate GET /sessions/:id still sees agent_removed=false).
	if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Error("rest: deleteAgent: reload failed", "agent_id", id, "error", err)
	} else if !confirmed {
		slog.Warn("rest: deleteAgent: reload did not confirm within the poll window; "+
			"deleted agent may still be resolvable in the runtime registry", "agent_id", id)
	}
	// Audit the destructive action. Emitted after the write succeeds; a
	// failed audit write is logged (not silently discarded) so audit-log
	// gaps stay visible. The auditor is nil only in unit-test fixtures
	// where audit isn't wired; the pre-existing workspace handlers use the
	// same nil-guard pattern.
	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    "agent.delete",
			Decision: audit.DecisionAllow,
			AgentID:  id,
			Details: map[string]any{
				"agent_id":   id,
				"agent_type": deletedType,
				"agent_name": deletedName,
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", "agent.delete", "agent_id", id, "error", err)
		}
	}
	// O6 — drop any heartbeat schedule the deleted agent owned (the reconciler
	// removes heartbeat jobs whose agent is no longer in config).
	a.reconcileHeartbeatSchedules()
	w.WriteHeader(http.StatusNoContent)
}

// cliDetectAll is the detection function used by HandleSystemCliDetect. It is a
// package-level var (rather than a direct clidetect.DetectAll call) so unit
// tests can swap in a deterministic map without depending on the host layout.
// In production this is always clidetect.DetectAll.
var cliDetectAll = clidetect.DetectAll

// HandleSystemCliDetect handles GET /api/v1/system/cli-detect.
//
// Reports, per external-CLI runner (claude-code / codex / opencode), whether the
// binary is installed and — when it is — its absolute resolved path and how it
// was located ("path" via the gateway $PATH, "well-known" via a curated per-OS
// install-dir scan). The roster screen greys-out CLIs the host cannot run, and
// the create wizard / edit form prefill the executor cli_path field from `path`.
//
// Pure-Go filesystem probe — detection NEVER spawns a subprocess (FR-004) and is
// unaudited by design: no caller-supplied path is executed here (auditing is
// reserved for cli-validate, which does spawn). withAuth only.
func (a *restAPI) HandleSystemCliDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	all := cliDetectAll()

	var resp gen.CliDetect

	c := all["claude-code"]
	resp.Claude.Installed = c.Installed
	if c.Installed {
		resp.Claude.Path = strPtr(c.Path)
		if wire := cliDetectSourceWire(c.Source); wire != "" {
			src := gen.CliDetectClaudeSource(wire)
			resp.Claude.Source = &src
		}
	}

	cx := all["codex"]
	resp.Codex.Installed = cx.Installed
	if cx.Installed {
		resp.Codex.Path = strPtr(cx.Path)
		if wire := cliDetectSourceWire(cx.Source); wire != "" {
			src := gen.CliDetectCodexSource(wire)
			resp.Codex.Source = &src
		}
	}

	oc := all["opencode"]
	resp.Opencode.Installed = oc.Installed
	if oc.Installed {
		resp.Opencode.Path = strPtr(oc.Path)
		if wire := cliDetectSourceWire(oc.Source); wire != "" {
			src := gen.CliDetectOpencodeSource(wire)
			resp.Opencode.Source = &src
		}
	}

	jsonOK(w, resp)
}

// cliDetectSourceWire validates a clidetect.Source against the known set and
// returns its wire string, or "" for an unexpected value. The detector only ever
// emits the two known sources, so this is defense-in-depth: a future/unknown
// value normalizes to an omitted Source rather than being reflected onto the
// wire enum unvalidated.
func cliDetectSourceWire(s clidetect.Source) string {
	switch s {
	case clidetect.SourcePath, clidetect.SourceWellKnown:
		return string(s)
	default:
		return ""
	}
}

// isExternalSubagent reports whether the persisted agent is a subagent_3p
// (an External-CLI worker). Subagent (native worker) returns false; Main /
// core / system return false; only subagent_3p returns true.
func isExternalSubagent(ac config.AgentConfig) bool {
	if ac.Type != config.AgentTypeWorker {
		return false
	}
	if ac.Subagents == nil || ac.Subagents.Executor == nil {
		return false
	}
	return ac.Subagents.Executor.EffectiveKind() == config.ExecutorKindExternalCLI
}

// firstForbiddenSubagent3pField moved to agent_field_rules.go (W2a) — it now
// only handles the AgentUpdateRequest shape (create-time forbidden fields are
// enforced structurally by the discriminated-union AgentCreateRequestSubagent3p
// type, which has no matching properties at all).

func (a *restAPI) updateAgent(w http.ResponseWriter, r *http.Request, id string) {
	cfg := a.agentLoop.GetConfig()
	var foundIdx int = -1
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			foundIdx = i
			break
		}
	}
	if foundIdx < 0 {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", id))
		return
	}

	// ADR-035 / ADR-037: sandbox_profile and delegation_policy are retired from
	// the wire entirely. decodeAgentCreateVariant already rejects retired
	// fields on the create path via unconditional DisallowUnknownFields, but
	// decodeAndValidate's fast path below is non-strict by default
	// (validate_inbound defaults false) and gen.AgentUpdateRequest no longer
	// has either field at all — without this explicit raw-body sniff a client
	// still sending {"sandbox_profile":...} or {"delegation_policy":...} would
	// have the field silently dropped by Go's default JSON decode, and the PUT
	// would report 200 with no change applied instead of the loud 400 this
	// codebase's own create-path convention expects (ADR-035 §7 established
	// this raw-body-sniff pattern for exactly this failure mode; ADR-037
	// follows the same precedent rather than accepting the silent drop). Read
	// +restore r.Body so the normal decode below is unaffected.
	rawBody, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if readErr != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(rawBody))
	if bytes.Contains(rawBody, []byte(`"sandbox_profile"`)) {
		jsonErr(w, http.StatusBadRequest,
			`sandbox_profile is retired — use the global god-mode switch (POST /api/v1/gateway/god-mode)`)
		return
	}
	if bytes.Contains(rawBody, []byte(`"delegation_policy"`)) {
		jsonErr(
			w,
			http.StatusBadRequest,
			`delegation_policy is retired — delegation is now configured exclusively via the workspace Team tab (PUT /api/v1/workspaces/{id}/delegation)`,
		)
		return
	}

	var req gen.AgentUpdateRequest
	validateEnabled := cfg.Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "AgentUpdateRequest", &req, validateEnabled) {
		return
	}
	// Timestamp applied to the persisted agent on every successful save.
	now := time.Now().UTC()
	// Validate any custom deny patterns in shell_policy — each must be a valid Go regexp.
	if req.ShellPolicy != nil && req.ShellPolicy.CustomDenyPatterns != nil {
		for _, pat := range *req.ShellPolicy.CustomDenyPatterns {
			if _, compileErr := regexp.Compile(pat); compileErr != nil {
				jsonErr(w, http.StatusBadRequest,
					fmt.Sprintf("shell_policy.custom_deny_patterns: invalid regexp %q: %v", pat, compileErr))
				return
			}
		}
	}
	// Locked core agents: reject identity and prompt mutations.
	// Allowed: model selection, heartbeat schedule (enabled/interval), tools (via updateAgentTools).
	foundAgent := cfg.Agents.List[foundIdx]
	// ADR-049 D3 / ADR-055 — System Agent guards.
	//
	// NO seeded System Agent can be disabled. Both of today's members hold a
	// grant nothing else holds, so switching one off silently breaks the loop
	// that depends on it: disabling the Judge stalls every goal/plan loop via
	// the D7 judge-unavailability pause, and disabling the PlanSupervisor —
	// the SOLE holder of the plan-correction grant — leaves a wedged plan with
	// no actor able to correct it. The condition is therefore the whole
	// System-Agent category, not an id equality test: it was `== IDJudge` and
	// the PlanSupervisor slipped straight through it.
	//
	// AgentUpdateRequest carries no enabled/disabled field, so a client can
	// only smuggle one as an unknown field; sniff the raw body (mirrors the
	// sandbox_profile/delegation_policy raw-body-sniff precedent above) and
	// reject a disable attempt with a loud 400 rather than a silent drop.
	//
	// NOT the plan kill switch: containment is plan-scoped (stopping a plan
	// stops its supervision). This only stops a locked System Agent being
	// switched off through the agent API.
	//
	// Both predicates, deliberately: IsSystemAgentID is seeded-ROSTER
	// membership, so a seeded System Agent stays protected even if its
	// persisted type was tampered with in config.json (seedSystemAgents
	// repairs the type at the next boot, but a PUT can land before that);
	// IsSystem is the persisted-TYPE predicate every sibling System-Agent
	// guard in this file already uses (the not-deletable 400 above, the
	// soul-editable carve-out below), so the two categories cannot drift apart
	// into "deletable: no, disable-able: yes" for the same agent.
	if coreagent.IsSystemAgentID(coreagent.CoreAgentID(foundAgent.ID)) || foundAgent.IsSystem() {
		var statePeek struct { // not-wire-format: decode-only local peek at raw body fields to reject a disable attempt, never serialized to any response
			Enabled  *bool `json:"enabled"`
			Disabled *bool `json:"disabled"`
		}
		if peekErr := json.Unmarshal(rawBody, &statePeek); peekErr != nil {
			// Unreachable by construction — decodeAndValidate above already
			// parsed this exact body as JSON. Logged rather than discarded so
			// a future reordering that makes it reachable cannot silently
			// disarm this guard.
			slog.Warn("gateway: could not peek enabled/disabled on System Agent update; disable guard not evaluated",
				"agent_id", foundAgent.ID, "error", peekErr)
		}
		if (statePeek.Enabled != nil && !*statePeek.Enabled) ||
			(statePeek.Disabled != nil && *statePeek.Disabled) {
			name := strings.TrimSpace(foundAgent.Name)
			if name == "" {
				name = foundAgent.ID
			}
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("the %s System Agent cannot be disabled", name))
			return
		}
	}
	// Worker agents can never be the routing default — they are not chat targets
	// (invoked only via delegation). Reject an attempt to star a worker before
	// any work is done so the single-default invariant and routing stay coherent.
	if req.Default != nil && *req.Default && foundAgent.IsWorker() {
		jsonErr(
			w,
			http.StatusBadRequest,
			"a worker agent cannot be set as the default agent (workers are not chat targets)",
		)
		return
	}
	// Worker agents have no heartbeat — they execute one delegated task at a
	// Heartbeat is workspace-scoped (ADR-027); per-agent heartbeat writes on the
	// agent PUT path are silently ignored. Workers still cannot carry heartbeat.
	// Per-agent voice is a chat-persona attribute (TTS persona) — workers are
	// not chat personas. Reject setting a non-empty voice on a worker at the
	// write gate so a worker never carries a TTS persona. An explicit null
	// (clearing) is fine, and so is omitting the field. Runs BEFORE the
	// locked-agent identity check so a locked worker is also blocked.
	if foundAgent.IsWorker() && req.Voice != nil && strings.TrimSpace(*req.Voice) != "" {
		jsonErr(w, http.StatusBadRequest, "a worker cannot have a per-agent voice (workers are not chat personas)")
		return
	}

	// W2 spec §4.19.1 / §9.2: subagent_3p agents (External CLI workers) reject
	// PUTs on any of the 7 forbidden fields. These properties are CLI-owned and
	// cannot be tuned at runtime — they are fixed at the create call when the
	// executor was wired. A silent-drop would be a foot-gun.
	if isExternalSubagent(foundAgent) {
		if field, forbidden := firstForbiddenSubagent3pField(&req); forbidden {
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("subagent_3p agents do not support %s; this is fixed at create time.", field))
			return
		}
	}

	// W2 spec §4.3 / §9.2: worker types (Subagent, subagent_3p) MUST have a
	// non-empty description (after trim). A blank/whitespace PUT is rejected
	// 400 rather than silently stripping the field — the routing layer
	// depends on the description to pick a worker for delegation.
	if foundAgent.IsWorker() && req.Description != nil && strings.TrimSpace(*req.Description) == "" {
		jsonErr(w, http.StatusBadRequest, "description is required for worker agents (Subagent, subagent_3p)")
		return
	}

	// W2 spec §3.1 row 16 / §9.2 row 11+15: fallback_models is capped at 2
	// entries (maxItems: 2). Server-enforced on every PUT/CREATE so a direct
	// REST caller (not the SPA) cannot smuggle a 3rd entry past the schema.
	if req.FallbackModels != nil && len(*req.FallbackModels) > 2 {
		jsonErr(w, http.StatusBadRequest, "fallback_models exceeds maxItems: 2")
		return
	}

	if foundAgent.Locked {
		// Protected: name, description, soul (prompt content),
		// color, icon, and skills are identity/capability fields — reject on locked agents.
		// Skills are included here (B-2 defense-in-depth): core agents have compiled-in capability
		// sets; allowing runtime skill assignment would silently override that invariant.
		//
		// ADR-052 FR-038 (soul/rubric unification, R3-1 CLOSED): AgentConfig.Rubric
		// was deleted — a System Agent's (e.g. the Judge) verification standards ARE
		// its soul, and the ADR is explicit that "the Judge's soul is editable while
		// the agent stays otherwise locked (core agents keep their souls locked)".
		// So req.Soul is exempted from the reject-set for System Agents ONLY —
		// every other identity field (name/description/color/icon/skills) stays
		// locked even for a System Agent, and core agents (Mia/Jim/Ava/Ray) keep
		// the full reject-set including soul: their souls are product identity,
		// not a verifier rubric.
		soulLocked := req.Soul != nil && !foundAgent.IsSystem()
		if req.Name != nil || req.Description != nil ||
			soulLocked ||
			req.Color != nil || req.Icon != nil || req.Skills != nil {
			jsonErr(w, http.StatusForbidden, "cannot modify locked agent identity or prompt")
			return
		}
	}
	// Referential validation: reject unknown skill IDs before doing any work.
	if req.Skills != nil && len(*req.Skills) > 0 {
		if errMsg := a.validateSkillIDs(*req.Skills); errMsg != "" {
			jsonErr(w, http.StatusBadRequest, errMsg)
			return
		}
	}
	// Validate the executor (kind/cli) before any work so a bad request 400s
	// rather than persisting an invalid combination.
	var updatedExecutor *config.ExecutorConfig
	if req.Executor != nil {
		// CLI-lock rule (spec §4.16 / F-10): once an agent is created with a
		// non-empty executor.cli, the cli is IMMUTABLE. Subsequent PUTs may
		// only mutate cli_path (allows binary upgrades without re-creating the
		// agent), env_overrides, and cli_args. cli_path IS mutable on PUT;
		// env_overrides and cli_args are too. cli itself is fixed at create.
		if foundAgent.Subagents != nil && foundAgent.Subagents.Executor != nil &&
			foundAgent.Subagents.Executor.CLI != "" {
			reqCLI := executorCliStr(req.Executor.Cli)
			if reqCLI != "" && reqCLI != foundAgent.Subagents.Executor.CLI {
				jsonErr(w, http.StatusBadRequest,
					"executor.cli is locked after create; create a new agent to switch CLIs.")
				return
			}
		}
		// env_overrides OMNIPUS_-prefix guard (spec §4.18 / F-04 STRIDE).
		// A user-submitted env_overrides key starting with OMNIPUS_ would
		// override gateway-managed secrets (master key, audit chain, etc.)
		// for the spawned CLI process — a defense-in-depth gap.
		if req.Executor.EnvOverrides != nil {
			for k := range *req.Executor.EnvOverrides {
				if strings.HasPrefix(strings.ToUpper(k), "OMNIPUS_") {
					jsonErr(w, http.StatusBadRequest,
						fmt.Sprintf("env_overrides key %q is reserved: OMNIPUS_* is gateway-internal", k))
					return
				}
			}
		}
		execCfg, errMsg := executorConfigFromRequest(
			executorKindStr(req.Executor.Kind),
			executorCliStr(req.Executor.Cli),
		)
		if errMsg != "" {
			jsonErr(w, http.StatusBadRequest, errMsg)
			return
		}
		// Merge the request into the existing executor so mutable CLI-owned
		// fields (cli_path, env_overrides, cli_args) and any supplied kind/cli
		// survive the update. If there is no existing executor, fall back to
		// the validated config from the request.
		var merged *config.ExecutorConfig
		if foundAgent.Subagents != nil && foundAgent.Subagents.Executor != nil {
			cp := *foundAgent.Subagents.Executor
			merged = &cp
		} else {
			merged = execCfg
		}
		if merged == nil {
			merged = &config.ExecutorConfig{Kind: config.ExecutorKindNative}
		}
		if execCfg != nil {
			if execCfg.Kind != "" {
				merged.Kind = execCfg.Kind
			}
			if execCfg.CLI != "" {
				merged.CLI = execCfg.CLI
			}
		}
		if req.Executor.CliPath != nil {
			merged.CLIPath = *req.Executor.CliPath
		}
		if req.Executor.EnvOverrides != nil {
			merged.EnvOverrides = *req.Executor.EnvOverrides
		}
		if req.Executor.CliArgs != nil {
			merged.CLIArgs = *req.Executor.CliArgs
		}
		// Native-only executor on a non-worker (worker property-model
		// correction): only a sub-agent worker may declare a non-native
		// executor. A base or custom agent updated to kind="external-cli"
		// or "remote-a2a" is rejected at the update gate. A native (or
		// absent) executor on a non-worker stays allowed.
		if !foundAgent.IsWorker() && merged.EffectiveKind() != config.ExecutorKindNative {
			jsonErr(w, http.StatusBadRequest,
				"only sub-agent workers can use an external executor; base agents run native")
			return
		}
		updatedExecutor = merged
	}
	// ADR-037: delegation_policy is retired from the wire entirely — the
	// per-workspace delegation graph (Team tab) is the sole delegation
	// mechanism. There is nothing left to merge/validate/persist here. See
	// the raw-body sniff below (before decode) for the loud-400 rejection of
	// a client still sending this retired field.
	// Persist to config.json BEFORE mutating the live config.
	// Capture the new values to apply after persistence succeeds.
	newName := foundAgent.Name
	newModel := ""
	if foundAgent.Model != nil {
		newModel = foundAgent.Model.Primary
	}
	if req.Name != nil {
		// Trim before the empty check so a whitespace-only name is rejected
		// rather than silently accepted (UAT fix). Persist the trimmed value.
		trimmedName := strings.TrimSpace(*req.Name)
		if trimmedName == "" {
			jsonErr(w, http.StatusUnprocessableEntity, "name must not be empty")
			return
		}
		newName = trimmedName
		req.Name = &trimmedName
	}
	if req.Model != nil {
		newModel = *req.Model
	}
	// CLAUDE.md hard constraint 6 / config.ValidateToolPolicyCoverage: when
	// the caller sends tools_cfg, the whole per-tool policies map is a full
	// replacement of what gets persisted (see the tools_cfg branch in the
	// updateConfigJSONLocked closure below) — so re-validate coverage for THIS
	// agent's prospective policies (the request's map when tools_cfg.builtin
	// is sent, else the agent's existing stored policies when tools_cfg is
	// omitted or only touches mcp) against the current global
	// sandbox.tool_policies, before persisting. Reject 400 with the full gap
	// list on any hole rather than silently writing an incomplete map.
	// Validated against a candidate config snapshot (a shallow copy of the
	// live config with this one agent's Tools spliced in) — nothing here
	// mutates the live in-memory config or disk, so a rejected request
	// leaves no partial state behind.
	//
	// The validate step and the persist step below run inside ONE
	// a.configMu-locked critical section (closing a TOCTOU race two
	// concurrent updates could otherwise open — see updateConfigJSONLocked's
	// doc comment), via withToolPolicyCoverageGuard: unlike the fast-path
	// checks above (which read the top-of-function cfg/foundIdx snapshot —
	// fine for those, since none of them persist anything), the guard always
	// fetches the config FRESH, inside a.configMu, right before validating —
	// so a concurrent write (e.g. another request's TriggerReload, or this
	// same agent being deleted) cannot slip in between fetch and lock and go
	// unobserved by the coverage check or the persist step. Returns false
	// once it has already written the HTTP response (error case), so the
	// caller just returns.
	// Coverage validation only runs when this request actually touches
	// tools_cfg: config.ValidateToolPolicyCoverage checks EVERY agent in the
	// roster, not just this one, so running it unconditionally would turn an
	// entirely unrelated field update (default flag, model, skills, …) into
	// a spurious 400 whenever ANY other agent's tools map is incomplete
	// (e.g. a bare/legacy fixture, or a pre-migration config). toolsCoverageMutate
	// stays nil (skipping the check — see withToolPolicyCoverageGuard's doc
	// comment) unless the caller actually sent tools_cfg.
	var toolsCoverageMutate func(*config.Config)
	// defaultAgentIDChanged is set INSIDE the persist closure below, iff this
	// request actually flips cfg.Agents.Defaults.DefaultAgentID (the settings
	// singleton registry.GetDefaultAgent/routing.resolveDefaultAgentID
	// consult). It gates needsReload further down: AgentProfile.tsx's autosave
	// sends `default: <current value>` on EVERY save (not only the deliberate
	// ★ toggle), so gating on mere req.Default != nil would force a full
	// reload — dropping the WebSocket — on every unrelated profile edit. A
	// full reload is genuinely required here (not merely convenient) because
	// both registry.GetDefaultAgent's cached defaultAgentOverride field and
	// AgentRegistry's nested *routing.RouteResolver each capture their own
	// config.Config snapshot at last full-registry-rebuild time — a bare
	// SwapConfig (what every OTHER config-only field on this handler relies
	// on) never reaches either, so without a rebuild the two ladders would
	// keep disagreeing exactly as this bug fix set out to close.
	var defaultAgentIDChanged bool
	if req.ToolsCfg != nil {
		toolsCoverageMutate = func(c *config.Config) {
			// Search by ID against the FRESHLY-fetched clone — never the
			// pre-lock foundIdx, which can point at the wrong slot (or be out
			// of bounds) if the agent list changed shape between the top-of-
			// function fetch and this locked section (see
			// errAgentVanishedDuringUpdate's doc comment for the sharper,
			// persist-side consequence of the same staleness).
			for i := range c.Agents.List {
				if c.Agents.List[i].ID != id {
					continue
				}
				var candidatePolicies map[string]config.ToolPolicy
				if req.ToolsCfg.Builtin != nil {
					candidatePolicies = agentToolPolicyMapFromWire(req.ToolsCfg.Builtin.Policies)
				} else if c.Agents.List[i].Tools != nil {
					candidatePolicies = c.Agents.List[i].Tools.Builtin.Policies
				}
				candidateAgent := c.Agents.List[i]
				candidateAgent.Tools = &config.AgentToolsCfg{
					Builtin: config.AgentBuiltinToolsCfg{Policies: candidatePolicies},
				}
				c.Agents.List[i] = candidateAgent
				break
			}
		}
	}
	if ok := a.withToolPolicyCoverageGuard(
		w,
		toolsCoverageMutate,
		func(gaps []config.CoverageGap) string {
			return fmt.Sprintf(
				"tool policy coverage incomplete for agent %q (%d gap(s)): %s",
				id, len(gaps), joinCoverageGapMessages(gaps),
			)
		},
		// ADR-054 D2/§11 checklist item 3: agents are per-entity records under
		// entities/agents/<id>.json, not config.json's agents.list — persist
		// via the agent store instead of splicing the raw config map. `m` is
		// deliberately left untouched. entity.Store.Update (via
		// agentstore.Store.Update) performs the read-modify-write under its
		// own striped-mutex + sidecar-flock (ADR-054 D3), nested inside this
		// closure's a.configMu hold — same lock-ordering rule as the
		// tool-policy-coverage validation above (workspace/agent locks are
		// never held across this call).
		func(m map[string]any) error {
			store := agentstore.New(a.homePath)
			var conflictErr error
			_, updateErr := store.Update(id, func(agentRec *config.AgentConfig) error {
				// Optimistic concurrency check (runs INSIDE both a.configMu AND
				// the entity's own sidecar lock, so two concurrent PUTs cannot
				// both pass the version check and then both write). If the
				// caller sent an updated_at value, it must match the persisted
				// value exactly; otherwise another edit raced and we abort the
				// mutate (nothing is written). The caller maps errConflict to
				// HTTP 409.
				if req.UpdatedAt != nil && agentRec.UpdatedAt != nil && !req.UpdatedAt.Equal(*agentRec.UpdatedAt) {
					conflictErr = errConflict
					return errConflict
				}
				if req.Name != nil {
					agentRec.Name = newName
				}
				if req.Description != nil {
					agentRec.Description = strings.TrimSpace(*req.Description)
				}
				if req.Model != nil {
					if agentRec.Model == nil {
						agentRec.Model = &config.AgentModelConfig{}
					}
					agentRec.Model.Primary = newModel
				}
				// O3 two-field model: persist (or clear) the explicit primary
				// provider. A non-empty value pins the provider; an explicit empty
				// string clears it (fall back to default-provider resolution).
				if req.Provider != nil {
					if agentRec.Model == nil {
						agentRec.Model = &config.AgentModelConfig{}
					}
					agentRec.Model.Provider = strings.TrimSpace(*req.Provider)
				}
				// NOTE (discovered during ADR-054 conversion, pre-existing gap,
				// out of scope here): req.TimeoutSeconds, req.ModelParams, and
				// req.RateLimits have NO corresponding config.AgentConfig field
				// — config.AgentConfig has no TimeoutSeconds/ModelParams/
				// RateLimits at all (only agents.defaults.timeout_seconds, a
				// global setting). The pre-conversion code wrote them to raw
				// map keys with no Go struct field to read them back into, so
				// they never survived a struct-based config reload even
				// before this change — this conversion does not persist them
				// either, matching (not worsening) that pre-existing behavior.
				if req.MaxToolIterations != nil {
					agentRec.MaxToolIterations = *req.MaxToolIterations
				}
				// tool_feedback was removed from the wire in W1 (it's now per-channel
				// runtime behavior driven by pkg/agent/loop.go: webchat skips). The
				// global config-level agents.defaults.tool_feedback stays.
				if req.ShellPolicy != nil {
					// Load the existing shell_policy (if any) so a partial PATCH
					// (e.g. only custom_deny_patterns) does not clobber fields the
					// caller did not send.
					existing := agentRec.ShellPolicy
					if existing == nil {
						existing = &config.AgentShellPolicy{}
					}
					// Only overwrite enable_deny_patterns when the caller explicitly
					// sent it (non-nil pointer).
					if req.ShellPolicy.EnableDenyPatterns != nil {
						existing.EnableDenyPatterns = *req.ShellPolicy.EnableDenyPatterns
					}
					// An explicitly-sent array overwrites, INCLUDING the empty array —
					// that is how the SPA clears all deny patterns. Only a nil (field
					// absent from the request) leaves the persisted list untouched.
					if req.ShellPolicy.CustomDenyPatterns != nil {
						existing.CustomDenyPatterns = *req.ShellPolicy.CustomDenyPatterns
					}
					agentRec.ShellPolicy = existing
				}
				if req.Color != nil {
					agentRec.Color = *req.Color
				}
				if req.Icon != nil {
					agentRec.Icon = *req.Icon
				}
				// memory_enabled (ADR-052 FR-039): "Allowed on all agents" per
				// AgentUpdateRequest.yaml — including locked/system agents (the
				// Judge), which is why this is not gated behind the
				// foundAgent.Locked identity-mutation check above (that block
				// only forbids name/description/soul/color/icon/skills).
				if req.MemoryEnabled != nil {
					agentRec.MemoryEnabled = req.MemoryEnabled
				}
				if req.FallbackModels != nil {
					fbs := make(config.FallbackModelSlice, 0, len(*req.FallbackModels))
					for _, fm := range *req.FallbackModels {
						provider := ""
						if fm.Provider != nil {
							provider = *fm.Provider
						}
						fbs = append(fbs, config.FallbackModel{Model: fm.Model, Provider: provider})
					}
					agentRec.FallbackModels = fbs
				}
				// Default flag: ADR-054 D6.4 moved default-agent RESOLUTION
				// entirely to the settings singleton
				// (cfg.Agents.Defaults.DefaultAgentID — see registry.go's
				// GetDefaultAgent and route.go's resolveDefaultAgentID). This
				// per-entity bool is retained only for backward display
				// compatibility (see config.go's ADR-054 D6.4 note) — it is
				// NOT read by any resolution logic, and the wire `default`
				// field is derived from the singleton at every response site
				// (listAgents/getAgent above, updateAgent's response below),
				// never from this field. The actual singleton write happens
				// further down in THIS SAME a.configMu-locked closure (see the
				// "agents.defaults.default_agent_id" write after the entity
				// write below succeeds), so both land atomically or not at
				// all — that replaces the old racy N-write fan-out that used
				// to clear Default on every OTHER agent's entity record (see
				// git history: independently-locked per-entity writes with no
				// shared lock could each set Default=true, which is exactly
				// the composition ADR-054 D6.4 retired RepairMultipleDefaults
				// over).
				if req.Default != nil {
					agentRec.Default = *req.Default
				}
				if req.ToolsCfg != nil {
					newTools := &config.AgentToolsCfg{}
					if req.ToolsCfg.Builtin != nil {
						newTools.Builtin = config.AgentBuiltinToolsCfg{
							Policies: agentToolPolicyMapFromWire(req.ToolsCfg.Builtin.Policies),
						}
					} else if agentRec.Tools != nil {
						// req.ToolsCfg is present (e.g. it only touches mcp) but
						// omitted builtin — the coverage-validation block above
						// assumed the agent's EXISTING persisted builtin.policies
						// survives untouched in this case.
						newTools.Builtin = agentRec.Tools.Builtin
					}
					if req.ToolsCfg.Mcp != nil && req.ToolsCfg.Mcp.Servers != nil {
						servers := make([]config.AgentMCPServerBinding, 0, len(*req.ToolsCfg.Mcp.Servers))
						for _, s := range *req.ToolsCfg.Mcp.Servers {
							binding := config.AgentMCPServerBinding{ID: s.Id}
							if s.Tools != nil {
								binding.Tools = *s.Tools
							}
							servers = append(servers, binding)
						}
						newTools.MCP = config.AgentMCPToolsCfg{Servers: servers}
					} else if agentRec.Tools != nil {
						// Symmetric preservation for mcp: req.ToolsCfg present but
						// omitted mcp (e.g. a builtin-only policy update) must not
						// silently drop the agent's existing MCP server bindings.
						newTools.MCP = agentRec.Tools.MCP
					}
					agentRec.Tools = newTools
				}
				// Executor: write the sub-agent executor under Subagents.Executor
				// when the caller sends it. kind="native" with no cli clears any
				// prior external-cli config (updatedExecutor == nil → clear).
				if req.Executor != nil {
					if updatedExecutor == nil {
						if agentRec.Subagents != nil {
							agentRec.Subagents.Executor = nil
						}
					} else {
						if agentRec.Subagents == nil {
							agentRec.Subagents = &config.SubagentsConfig{}
						}
						agentRec.Subagents.Executor = updatedExecutor
					}
				}
				// Skills: replace the agent's skill list when the caller sends the field.
				// An explicit empty array removes all skills. Nil (absent) leaves unchanged.
				if req.Skills != nil {
					if len(*req.Skills) > 0 {
						agentRec.Skills = *req.Skills
					} else {
						agentRec.Skills = nil
					}
				}
				// ADR-037: delegation_policy is retired — no longer written here.
				// Heartbeat is workspace-scoped (ADR-027); per-agent heartbeat fields
				// are ignored on PUT. Workspace handler manages member_configs.
				// Optimistic concurrency timestamp: refresh on every successful save.
				// Sub-second precision (time.Time, not truncated) — the frontend uses
				// this field as an ordinal "is this newer" comparator
				// (lastIncorporatedUpdatedAtRef in AgentProfile.tsx); whole-second
				// precision let two distinct autosave writes within the same
				// wall-clock second collide on an identical truncated timestamp,
				// defeating the ordinal comparison (reopening the P-F2
				// fallback_models data-loss class this fix wave closed).
				agentRec.UpdatedAt = &now
				return nil
			})
			if updateErr != nil {
				if conflictErr != nil {
					return errConflict
				}
				if errors.Is(updateErr, entity.ErrNotFound) {
					// The fast-path existence check at the top of updateAgent found
					// this agent, but by the time this locked store update ran it
					// was gone — e.g. a concurrent DELETE /agents/{id} raced this
					// PUT. Mapped to 404 by withToolPolicyCoverageGuard (see
					// errAgentVanishedDuringUpdate's doc comment).
					return fmt.Errorf("%w: agent %q not found", errAgentVanishedDuringUpdate, id)
				}
				return fmt.Errorf("update agent entity record: %w", updateErr)
			}
			// Single-default invariant, for real this time: the settings
			// singleton (agents.defaults.default_agent_id) is the ONLY thing
			// registry.GetDefaultAgent and routing.resolveDefaultAgentID
			// consult, and the ONLY thing the wire `default` field is derived
			// from (listAgents/getAgent above, this handler's response
			// below) — so it is also the only thing that needs writing here.
			// There is no more N-write fan-out across every OTHER agent's
			// entity record: a per-entity bool never had to be reconciled
			// once "the one default" became a single string behind the
			// existing config-write lock (this closure already holds
			// a.configMu via withToolPolicyCoverageGuard), and that old loop
			// was itself racy (each Update below was its own
			// independently-locked write, so two concurrent PUTs to two
			// different agents could each "win" with no shared lock to
			// serialize them — precisely the failure mode ADR-054 D6.4
			// retired RepairMultipleDefaults over).
			//
			// true  → point the singleton at this agent (worker guard
			//         already rejected this request above if foundAgent is a
			//         worker, so `id` is always a valid chat-target here).
			// false → clear the singleton ONLY if it currently names this
			//         agent, so un-starring the actual default reverts to
			//         the registry's own fallback ladder (main sentinel,
			//         then first non-worker) instead of leaving the
			//         singleton pointed at an agent that just un-defaulted
			//         itself. Un-starring an agent that the singleton
			//         doesn't currently name is a no-op — matches the old
			//         per-entity semantics ("clear this agent only, leave
			//         others unchanged").
			if req.Default != nil {
				defaultsMap := ensureMap(m, "agents", "defaults")
				cur, _ := defaultsMap["default_agent_id"].(string)
				if *req.Default {
					if cur != id {
						defaultsMap["default_agent_id"] = id
						defaultAgentIDChanged = true
					}
				} else if cur == id {
					defaultsMap["default_agent_id"] = ""
					defaultAgentIDChanged = true
				}
			}
			// O6: heartbeat is now fully per-agent (written inside the agent-found
			// block above). The legacy global heartbeat block was removed; there is
			// no cfg.Heartbeat mirror to maintain.
			return nil
		},
		"rest: save agent entity record for agent update",
	); !ok {
		return
	}
	// Write SOUL.md, HEARTBEAT.md, and AGENT.md BEFORE triggering reload,
	// so the new AgentInstance reads the updated files.
	// Capture agentWorkspace into a local to avoid TOCTOU on cfg.Agents.List (M1).
	capturedWorkspace := cfg.Agents.List[foundIdx].Home
	workspace, wsErr := agentWorkspacePath(cfg, id, capturedWorkspace, a.homePath)
	if wsErr != nil {
		slog.Error("rest: agentWorkspacePath for update", "agent_id", id, "error", wsErr)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not resolve workspace: %v", wsErr))
		return
	}
	if req.Soul != nil {
		soulPath := filepath.Join(workspace, "SOUL.md")
		if err := fileutil.WriteFileAtomic(soulPath, []byte(*req.Soul), 0o600); err != nil {
			slog.Error("rest: write SOUL.md for agent", "agent_id", id, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not write SOUL.md: %v", err))
			return
		}
	}
	// Only trigger a full reload when structural changes require it (SOUL.md,
	// agent creation/deletion). Model, rate limit, timeout, and steering mode changes are
	// config-only and do NOT need a reload — avoiding the WebSocket drop and context loss
	// that a full reload causes mid-conversation.
	//
	// ADR-037: delegation_policy is retired, so it no longer appears in this
	// condition. Delegation edits now go exclusively through the per-workspace
	// delegation graph (PUT /api/v1/workspaces/{id}/delegation), which
	// buildDelegationDenyChecker reads fresh from disk on every delegation
	// call (workspace.ReadDelegation) rather than from a closure baked at
	// agent-instance construction time — so a graph edit already takes effect
	// on the NEXT turn with no reload required at all, unlike the old
	// per-agent policy this replaced.
	//
	// defaultAgentIDChanged (set inside the persist closure above, iff this
	// request actually flipped agents.defaults.default_agent_id) also forces
	// a reload — see that variable's doc comment. This is deliberately NOT
	// keyed off req.Default != nil (which AgentProfile.tsx's autosave sends
	// on every save, unrelated edits included); only a real transition of the
	// singleton earns the WebSocket-dropping cost of a full rebuild, because
	// nothing short of one re-syncs registry.GetDefaultAgent's cached
	// override AND the registry's nested RouteResolver's own config
	// snapshot — the two ladders this bug fix makes agree.
	//
	// fastAgentUpsert (issue #571), not a full config reload: a default-agent
	// flip is only real once the registry's cached override AND its nested
	// RouteResolver snapshot are rebuilt (ADR-037 — a control that looks like
	// it worked and changed no routing is the anti-pattern this project
	// bans), and a soul change is only real once a fresh AgentInstance/
	// ContextBuilder picks up the new SOUL.md — but neither requires
	// restarting channels/cron/schedulers/the plan engine or rebuilding
	// every OTHER agent's instance, only this one agent's. See
	// AgentLoop.UpsertAgentFast for how the resolver/override/wiring parity
	// is achieved without that cost; it falls back to the slow, hardened
	// full reload on any wiring error.
	needsReload := req.Soul != nil || defaultAgentIDChanged
	var reloadWarning string
	if needsReload {
		reloadWarning = a.fastAgentUpsert(id)
	}

	// #73: a model-only change is intentionally config-only (no reload above, so
	// the WebSocket and conversation context survive). But persisting to config +
	// SwapConfig does NOT touch the already-constructed agent instance — its
	// cached provider/model would keep serving the OLD model until a restart.
	// Apply the change in place so it takes effect on the next turn while the
	// live session context is preserved. Skip when needsReload fired, since
	// TriggerReload already rebuilt the instance with the new model.
	if req.Model != nil && newModel != "" && !needsReload {
		if _, err := a.agentLoop.ApplyAgentModel(id, newModel); err != nil {
			// Error, not Warn: a live-apply failure means the running agent keeps
			// serving the OLD model despite a 200 response. The cause (bad model
			// config, provider init / API-key failure, no candidates) typically
			// recurs on the next reload too, so this is not reliably "applies
			// later" — surface it loudly and in the response so it is not silent.
			slog.Error("updateAgent: live model apply failed; running agent still on previous model",
				"agent_id", id, "model", newModel, "error", err)
			if reloadWarning == "" {
				reloadWarning = fmt.Sprintf(
					"model saved to config but could not be applied to the running agent (still serving the previous model): %v",
					err,
				)
			}
		}
	}
	// Re-read the files so the response reflects what was just persisted.
	soul, _ := readAgentFiles(workspace)
	// Build the response from defaults, then override with request values.
	agentID := cfg.Agents.List[foundIdx].ID
	model := cfg.Agents.Defaults.DefaultModel.Model
	if newModel != "" {
		model = newModel
	}
	activeIDs := a.activeAgentIDSet()
	ag := buildAgentDefaults(cfg)
	ag.Id = agentID
	ag.Name = newName
	// Description: use the just-updated value when provided, else fall back
	// to what's on disk (which will be the previously-persisted value because
	// TriggerReload has refreshed cfg.Agents.List).
	if req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		if desc != "" {
			ag.Description = &desc
		}
	} else {
		// Re-read from the current config after reload.
		if cur := a.agentLoop.GetConfig(); cur != nil {
			for _, ac := range cur.Agents.List {
				if ac.ID == agentID && ac.Description != "" {
					ag.Description = &ac.Description
					break
				}
			}
		}
	}
	ag.Locked = foundAgent.Locked
	ag.Model = &model
	// O3 two-field model: echo the explicit primary provider. When the request
	// touched provider, the request value is authoritative (a non-empty string
	// sets it; an empty string clears it → absent on the wire). Otherwise reflect
	// the persisted provider from the reloaded config.
	switch {
	case req.Provider != nil:
		if p := strings.TrimSpace(*req.Provider); p != "" {
			ag.Provider = &p
		}
	default:
		if cur := a.agentLoop.GetConfig(); cur != nil {
			for i := range cur.Agents.List {
				if cur.Agents.List[i].ID == agentID {
					setAgentModelProvider(&ag, cur.Agents.List[i].Model)
					break
				}
			}
		}
	}
	ag.Status = gen.AgentStatus(computeAgentStatus(agentID, activeIDs, soul, foundAgent.Locked))
	// Hide compiled prompts for locked (core) agents.
	// ADR-052 FR-038: System Agents (the Judge) are exempted — the PUT response
	// must echo back what was just persisted to SOUL.md, or a client's next
	// edit (built on a blank round-trip) would clobber the just-saved content.
	if foundAgent.Locked && !foundAgent.IsSystem() {
		soul = ""
	}
	ag.Soul = soul
	if reloadWarning != "" {
		ag.Warning = &reloadWarning
	}
	// Populate Default, Skills, and Executor from the live config after the write
	// (handles both the req.Default=true case and the leave-unchanged case, and
	// ensures a GET→edit→PUT round-trip echoes the persisted executor).
	if liveCfg := a.agentLoop.GetConfig(); liveCfg != nil {
		for _, ac := range liveCfg.Agents.List {
			if ac.ID == agentID {
				ag.Type = coreagent.ToWireType(ac)
				// Derived from the settings singleton — see listAgents' comment
				// for the full rationale. liveCfg is fetched fresh above, and
				// (when defaultAgentIDChanged fired) TriggerReload has already
				// rebuilt it from the just-written config.json, so this reflects
				// the singleton this exact request just persisted.
				ag.Default = boolPtr(ac.ID == liveCfg.Agents.Defaults.DefaultAgentID)
				// ADR-068 FR-014 (T068-08): needs_model is derived, never stored.
				ag.NeedsModel = agentNeedsModel(liveCfg, &ac)
				// ADR-067 FR-016/FR-031 (T067-09): the PUT response carries
				// the degrade the request just cleared (or created) — a
				// repair is visible in the very response that made it, with
				// no restart beyond the reload this handler already triggers
				// (US-6.AC3).
				ag.DegradedReason = agentDegradedReason(a.providerCatalog, liveCfg, &ac)
				if len(ac.Skills) > 0 {
					skills := make([]string, len(ac.Skills))
					copy(skills, ac.Skills)
					ag.Skills = &skills
				}
				setAgentExecutorResponse(&ag, ac.Subagents)
				if ac.UpdatedAt != nil {
					ag.UpdatedAt = ac.UpdatedAt
				}
				// P-F2: echo shell_policy/fallback_models on the PUT response too —
				// this loop previously never called applyAgentOverrides at all, so
				// updateAgent's own response (unlike list/get) never reflected either
				// field even though both persist correctly above. Runs before the
				// request-value overrides below so an explicit req.MaxToolIterations
				// (also touched by applyAgentOverrides) still wins.
				applyAgentOverrides(&ag, &ac)
				break
			}
		}
	}
	// Override defaults with request values when provided.
	if req.TimeoutSeconds != nil {
		ag.TimeoutSeconds = *req.TimeoutSeconds
	}
	if req.MaxToolIterations != nil {
		ag.MaxToolIterations = *req.MaxToolIterations
	}
	jsonOK(w, ag)
}

// --- Config ---

// HandleConfig handles GET /api/v1/config and PUT /api/v1/config.
// Both verbs require only authentication (registered under withAuth in
// gateway.go): mutating gateway config can change ports, dev_mode_bypass, and
// provider settings, but under the single-account model the authenticated
// caller IS the sole account, so no further role gate applies.
func (a *restAPI) HandleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getConfig(w)
	case http.MethodPut:
		a.updateConfig(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) getConfig(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()

	// Marshal to JSON then unmarshal to a generic map so we can redact credential fields.
	raw, err := json.Marshal(cfg)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not serialize config")
		return
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not process config")
		return
	}

	// Redact any top-level field names that look like credentials.
	redactSensitiveFields(m)

	jsonOK(w, m)
}

// redactSensitiveFields recursively redacts map values whose keys contain
// sensitive keywords. Credential data must live in credentials.json, not config.json,
// but this is a defense-in-depth measure per BRD SEC-23.
func redactSensitiveFields(m map[string]any) {
	sensitive := []string{"key", "token", "secret", "password", "credential", "api_key"}
	for k, v := range m {
		kl := strings.ToLower(k)
		for _, s := range sensitive {
			if strings.Contains(kl, s) {
				if str, ok := v.(string); ok && str != "" {
					m[k] = "[redacted]"
				}
				break
			}
		}
		if sub, ok := v.(map[string]any); ok {
			redactSensitiveFields(sub)
		}
		if arr, ok := v.([]any); ok {
			for _, elem := range arr {
				if subMap, ok := elem.(map[string]any); ok {
					redactSensitiveFields(subMap)
				}
			}
		}
	}
}

// configPath returns the path to config.json under the home directory.
func (a *restAPI) configPath() string {
	return filepath.Join(a.homePath, "config.json")
}

// resolveCredentialRef resolves a credential reference from the shared credential store.
// Returns an error if the store is locked or the ref is not found, so callers can
// surface a meaningful error instead of silently returning "".
func (a *restAPI) resolveCredentialRef(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return "", fmt.Errorf("credential store locked: %w", err)
		}
	}
	value, err := credentials.ResolveRef(store, ref)
	if err != nil {
		return "", fmt.Errorf("credential store: %w", err)
	}
	return value, nil
}

// describeCredentialResolutionError classifies a resolveCredentialRef failure into
// an operator-facing message with the CORRECT remediation advice. fmt.Errorf's %w
// already preserves errors.As-compatibility through the wrap in resolveCredentialRef
// above — the bug this helper fixes is that callers never called errors.As and instead
// treated every non-nil error identically.
//
// Two semantically distinct causes exist:
//   - *credentials.NotFoundError: the ref itself is not in the store (stale/deleted
//     credential, a hand-edited config with a typo'd ref, …). Unlocking the vault
//     changes nothing here — the correct advice is to re-enter the API key.
//   - anything else (locked store, decrypt/auth failure, …): the vault genuinely
//     could not be read. This is transient — unlock and retry IS correct advice.
func describeCredentialResolutionError(err error) string {
	var notFound *credentials.NotFoundError
	if errors.As(err, &notFound) {
		return "the configured credential reference no longer exists — re-enter the API key."
	}
	return "API key is configured but the credential vault could not be read " +
		"(store locked or undecryptable) — unlock and retry."
}

// storeCredential stores an API key in the encrypted credentials store and
// returns the credential reference name. Returns an error if the store is locked
// or unavailable — never falls back to plaintext (SEC-23).
func (a *restAPI) storeCredential(refName, apiKey string) (string, error) {
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return "", fmt.Errorf(
				"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets: %w",
				err,
			)
		}
	}
	if err := store.Set(refName, apiKey); err != nil {
		return "", fmt.Errorf("failed to store API key in credentials store: %w", err)
	}
	return refName, nil
}

// credentialStoreReady reports whether the encrypted credential store can accept a
// write (unlocked / master key available), WITHOUT writing anything. It mirrors the
// readiness path of storeCredential so the PUT handler can return 503 BEFORE running a
// live key-validation probe — there is no point making a billable upstream call to
// validate a key we cannot persist (SEC-23: no plaintext fallback).
func (a *restAPI) credentialStoreReady() error {
	if a.credStore != nil {
		return nil // an already-unlocked store was injected
	}
	store := credentials.NewStore(a.credentialsStorePath())
	if err := credentials.Unlock(store); err != nil {
		return fmt.Errorf(
			"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets: %w",
			err,
		)
	}
	return nil
}

// channelCredKey is the credential-store key for a channel's secret field. The
// format is opaque to readers (channel constructors resolve secrets via the
// config <field>_ref, never by reconstructing this key); it exists so the
// producer side has a single definition.
func channelCredKey(channelID, field string) string {
	return "channel_" + channelID + "_" + field
}

// removeStoredCredential removes refName from the credential store. A missing
// entry is not an error — clearing a never-set secret is legitimate. (Distinct
// from the deleteCredential REST handler, which writes an HTTP response.)
func (a *restAPI) removeStoredCredential(refName string) error {
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return fmt.Errorf("credential store locked: %w", err)
		}
	}
	if err := store.Delete(refName); err != nil {
		var nf *credentials.NotFoundError
		if errors.As(err, &nf) {
			return nil
		}
		return err
	}
	return nil
}

// credentialRefResolves reports whether refName names a non-empty secret in the
// credential store. Used by testChannel (#289). The returned error is non-nil
// only for store-access faults (locked / wrong master key / I/O) — distinct from
// a genuinely absent secret, which returns (false, nil). The caller MUST surface
// a store fault separately rather than reporting the field as "missing", or
// "Test" would tell the user to re-enter a secret that is already correct.
func (a *restAPI) credentialRefResolves(refName string) (bool, error) {
	refName = strings.TrimSpace(refName)
	if refName == "" {
		return false, nil // genuinely not configured
	}
	store := a.credStore
	if store == nil {
		store = credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(store); err != nil {
			return false, fmt.Errorf("credential store locked: %w", err)
		}
	}
	v, err := store.Get(refName)
	if err != nil {
		var nf *credentials.NotFoundError
		if errors.As(err, &nf) {
			return false, nil // truly absent
		}
		return false, err // locked / wrong key / I/O — surface to the caller
	}
	return strings.TrimSpace(v) != "", nil
}

// safeUpdateConfigJSON reads config.json, applies a mutation function on the raw JSON map,
// and writes it back atomically. This preserves SecureStrings (API keys) that would be
// destroyed by config.SaveConfig's JSON round-trip through the Go struct.
//
// After a successful atomic write it calls refreshConfigAndRewireServices so the
// configSnapshotMiddleware picks up the new config immediately AND sensitive-data
// scrubbing is re-armed with the new credentials (A1+A2 fix). If the in-memory
// refresh fails the error is returned to the caller so the HTTP handler can surface
// a 500 rather than silently serving stale state.
func (a *restAPI) safeUpdateConfigJSON(mutate func(m map[string]any) error) error {
	// configMu serializes concurrent REST config writes (read-modify-write cycles).
	// Sysagent mutations go through MutateConfig (al.mu) with SaveConfigLocked,
	// which does not acquire configMu — so there is no lock ordering conflict.
	a.configMu.Lock()
	defer a.configMu.Unlock()
	return a.updateConfigJSONLocked(mutate)
}

// updateConfigJSONLocked is safeUpdateConfigJSON's body with the configMu
// acquisition factored out. Callers that must validate a candidate config
// snapshot and persist it as ONE atomic critical section — closing a TOCTOU
// window where two concurrent writes could each validate against a stale
// snapshot and both pass individually while the COMBINED persisted result has
// a gap neither observed alone (config.ValidateToolPolicyCoverage, CLAUDE.md
// hard constraint 6) — take a.configMu.Lock() themselves around the whole
// read-validate-persist sequence and call this method directly instead of
// safeUpdateConfigJSON, which would deadlock re-acquiring the same
// non-reentrant sync.Mutex. See createAgent, updateAgent, updateAgentTools
// (this file) and putToolPolicies (rest_tool_policies.go) for the pattern.
func (a *restAPI) updateConfigJSONLocked(mutate func(m map[string]any) error) error {
	raw, err := os.ReadFile(a.configPath())
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return fmt.Errorf("parse config: %w", unmarshalErr)
	}
	if mutateErr := mutate(m); mutateErr != nil {
		return mutateErr
	}
	// Ensure "version" is always stamped before writing back, mirroring
	// config.SaveConfig's own version-stamping for the struct-based save path.
	// Without this, a config.json that reached this raw-map read-modify-write
	// cycle without a "version" key (e.g. hand-edited, restored from an old
	// backup, or — as this exact bug once did — a stale test fixture) would
	// permanently fail every subsequent reload with "unsupported config
	// version: 0", since there is no more v0 migration fallback to bail it out.
	if v, ok := m["version"].(float64); !ok || int(v) < config.CurrentVersion {
		m["version"] = config.CurrentVersion
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	if writeErr := fileutil.WriteFileAtomic(a.configPath(), out, 0o600); writeErr != nil {
		return writeErr
	}
	// Register the content hash of what we just wrote so the config file
	// watcher knows this is an app-initiated write and does not trigger a
	// full service reload (channels disconnect/reconnect, cron lanes canceled).
	// We register `out` first so even a fast poller tick before the refresh sees
	// a known hash.
	if a.selfWriteReg != nil {
		a.selfWriteReg.register(sha256.Sum256(out))
	}
	// Refresh the in-memory config AND rewire sensitive-data scrubbing.
	// Propagate the error so callers fail the HTTP request rather than silently
	// serving stale in-memory state (prevents A1 regression on REST-initiated writes).
	if refreshErr := a.refreshConfigAndRewireServices(a.configPath()); refreshErr != nil {
		return fmt.Errorf("config written but in-memory refresh failed: %w", refreshErr)
	}
	// refreshConfigAndRewireServices loads the config, and config.LoadConfig may
	// normalize + re-save the file (config.go SaveConfig-on-load), producing
	// different bytes than `out`. Register the FINAL on-disk content hash too, so
	// the poller recognizes the post-refresh file as an app write and suppresses
	// the reload. Best-effort: a read failure just means the poller may reload once.
	if a.selfWriteReg != nil {
		if finalBytes, readErr := os.ReadFile(a.configPath()); readErr == nil {
			a.selfWriteReg.register(sha256.Sum256(finalBytes))
		} else {
			slog.Warn("rest: safeUpdateConfigJSON: could not read final config hash",
				"path", a.configPath(), "error", readErr)
		}
	}
	// FR-061 single chokepoint: every config mutation invalidates all cached
	// system-prompt preambles so the next agent turn rebuilds from the updated
	// config. Doing this inside safeUpdateConfigJSON removes the need for
	// individual call sites to remember the invalidation step.
	if a.agentLoop != nil {
		if reg := a.agentLoop.ContextBuilderRegistry(); reg != nil {
			reg.InvalidateAllContextBuilders()
		}
	}
	return nil
}

// ensureMap walks m through the given keys, creating intermediate map[string]any
// nodes as needed, and returns the deepest map. Panics only on a non-map value
// at a pre-existing key (a legitimate config.json corruption that should abort
// the request handler). Pure function — callers are already inside
// safeUpdateConfigJSON's mutex, so no locking here.
func ensureMap(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		existing, ok := cur[k]
		if !ok {
			next := map[string]any{}
			cur[k] = next
			cur = next
			continue
		}
		// Already exists — must be a map. Panic surfaces as 500 to the caller,
		// which is correct: a non-map node where a map is expected means
		// config.json on disk is structurally broken.
		nested, ok := existing.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("ensureMap: expected map at key %q, got %T", k, existing))
		}
		cur = nested
	}
	return cur
}

// refreshConfigAndRewireServices loads a fresh config from disk, re-resolves the
// credential bundle, registers all resolved plaintexts with the sensitive-data
// replacer, and atomically swaps the in-memory config on the agent loop.
//
// This is the single authoritative refresh path — both safeUpdateConfigJSON and
// any future REST-initiated config write must call this method rather than
// calling a bare SwapConfig (which skips credential resolution and
// RegisterSensitiveValues, causing an A1-class scrubber regression).
//
// When a.credStore is nil (e.g. tests that don't wire a store), the function
// falls back to config.LoadConfig (no migration, no credential resolution) and
// skips RegisterSensitiveValues — there are no credentials to re-arm in that case.
//
// Credential-resolution escalation: mirrors bootCredentials/executeReload
// (gateway.go) — a resolution failure on a ref that buildEnabledRefMap
// considers actually enabled/in-use (an enabled channel, or an in-use voice /
// web-search / skill-marketplace credential) is escalated to an error instead
// of a bare Warn, whether the failure is a NotFoundError (ref genuinely
// missing) or something worse (wrong master key, corrupted store entry —
// the ref IS configured but unreadable). Unlike executeReload, this call site
// cannot "reject and roll back" a config write: by the time this method runs,
// updateConfigJSONLocked has ALREADY durably written the new config.json to
// disk (fileutil.WriteFileAtomic runs before this call). Rejection here
// therefore means: do NOT swap the broken config into the live in-memory
// pointer below (the gateway keeps serving the previous good config), and
// return the error so the caller's HTTP handler surfaces a 500 instead of
// silently reporting success — the operator then knows the save did not fully
// take effect and must fix the credential before it applies.
//
// Called while a.configMu is held.
// populateAgentsListFromStore is the ADR-054 D3/§5 "read through an
// in-memory cache" bridge for the config-reload path — see
// populateAgentsListFromEntityStoreStrict's doc comment (gateway.go) for the
// full rationale. The 2026-07-26 privilege-escalation chain documented there
// ran through the "main" sentinel, which no longer exists; the remaining half
// still stands on its own — a silently-empty roster makes the tool-policy
// coverage gate pass vacuously, over zero agents.
//
// SECURITY FIX (RELEASE BLOCKER, F3 follow-up): this used to call the LENIENT
// populateAgentsListFromEntityStore (log-and-continue on failure, silently
// leaving cfg.Agents.List whatever it already was — which, on a genuine
// entity-store failure, is often already empty this early in a fresh
// *config.Config's life). refreshConfigAndRewireServices is the single
// authoritative refresh path for EVERY REST-initiated config write — agent
// create/update/delete, channel configure, tool-policy write, mailbox grant,
// god-mode toggle, all of it — making this the highest-traffic call site for
// the bug F3 closed in gateway.go's boot/manual-reload/file-watcher paths.
// Now calls the STRICT variant directly (same package, same function — no
// export needed) and returns its error so refreshConfigAndRewireServices can
// reject the candidate config exactly like it already does for a credential-
// resolution failure below: never call SwapConfig, propagate the error so the
// caller's updateConfigJSONLocked fails the write and the HTTP handler
// surfaces a 500 instead of silently serving a config whose roster may have
// come back empty. restAPI has no reference to gateway.go's *services (and
// therefore no markReloadDegraded hook to call) — the synchronous REST-write
// path's equivalent signal is failing THIS request with a real error instead
// of a fake 200, which is the same "reject, don't swap" semantic gateway.go's
// async reload loop expresses via markReloadDegraded + a degraded /health.
func (a *restAPI) populateAgentsListFromStore(cfg *config.Config) error {
	return populateAgentsListFromEntityStoreStrict(cfg, a.homePath)
}

func (a *restAPI) refreshConfigAndRewireServices(configPath string) error {
	if a.credStore == nil {
		// No credential store wired — use the plain loader (no v0 migration, no
		// credential resolution). Safe because without a store there are no
		// secrets to re-arm in the replacer.
		newCfg, err := config.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("load config (no store): %w", err)
		}
		if rosterErr := a.populateAgentsListFromStore(newCfg); rosterErr != nil {
			slog.Error("refreshConfigAndRewireServices: rejecting in-memory refresh — "+
				"agent roster population failed (no credential store variant)", "error", rosterErr)
			return fmt.Errorf("agent roster population failed: %w", rosterErr)
		}
		a.agentLoop.SwapConfig(newCfg)
		// Hot-apply the log level: gateway.log_level is a hot-reload key (not
		// restart-gated), so a Settings save must take effect immediately
		// rather than waiting for a manual restart.
		logger.SetLevelFromString(newCfg.Gateway.LogLevel)
		return nil
	}
	newCfg, err := config.LoadConfigWithStore(configPath, a.credStore)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if rosterErr := a.populateAgentsListFromStore(newCfg); rosterErr != nil {
		slog.Error("refreshConfigAndRewireServices: rejecting in-memory refresh — "+
			"agent roster population failed", "error", rosterErr)
		return fmt.Errorf("agent roster population failed: %w", rosterErr)
	}
	// Build a ref→in-use map so a resolution failure on something actually
	// enabled/in-use (fatal — surfaced as a failed request) can be
	// distinguished from one on a disabled channel or unused feature
	// (Warn + continue), matching bootCredentials/executeReload.
	enabledRefs := buildEnabledRefMap(newCfg)
	bundle, bundleErrs := credentials.ResolveBundle(newCfg, a.credStore)
	for _, e := range bundleErrs {
		var notFound *credentials.NotFoundError
		if errors.As(e, &notFound) {
			if enabledRefs[notFound.Name] {
				refreshErr := fmt.Errorf(
					"enabled credential %q not found in store: %w", notFound.Name, e,
				)
				slog.Error("refreshConfigAndRewireServices: rejecting in-memory refresh — "+
					"enabled credential not found", "ref", notFound.Name, "error", e)
				return refreshErr
			}
			slog.Info("refreshConfigAndRewireServices: credential not found (not currently enabled/in use)",
				"ref", notFound.Name)
			continue
		}
		// Any error other than "not found" on an enabled/in-use ref is worse
		// than a simple missing ref (the credential exists but can't be
		// read) — escalate exactly like the NotFoundError-on-enabled case
		// above instead of a log-only Warn.
		if ref, ok := enabledRefFromBundleError(e, enabledRefs); ok {
			refreshErr := fmt.Errorf(
				"enabled credential %q failed to resolve (not simply missing — check "+
					"OMNIPUS_MASTER_KEY / credentials.json integrity): %w", ref, e,
			)
			slog.Error("refreshConfigAndRewireServices: rejecting in-memory refresh — "+
				"enabled credential failed to resolve", "ref", ref, "error", e)
			return refreshErr
		}
		// Non-fatal: a disabled channel / unused feature missing its cred is
		// acceptable here.
		slog.Warn("refreshConfigAndRewireServices: bundle resolution error", "error", e)
	}
	// Replace (not append) the entire sensitive-values set so rotated secrets
	// are evicted and the scrubber reflects exactly the current config state.
	values := make([]string, 0, len(bundle))
	for _, v := range bundle {
		if v != "" {
			values = append(values, v)
		}
	}
	newCfg.RegisterSensitiveValues(values)
	// Atomically swap the config pointer so all subsequent requests see the
	// new config with scrubbing fully re-armed.
	a.agentLoop.SwapConfig(newCfg)
	// Hot-apply the log level: gateway.log_level is a hot-reload key (not
	// restart-gated), so a Settings save must take effect immediately
	// rather than waiting for a manual restart.
	logger.SetLevelFromString(newCfg.Gateway.LogLevel)
	return nil
}

func (a *restAPI) updateConfig(w http.ResponseWriter, r *http.Request) {
	// Read the raw body once so we can decode it into two shapes: a RawMessage
	// map for the existing deep-merge persistence path, and a fully-typed
	// map[string]any for the blockedPaths walker which needs to
	// recurse into nested objects.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var updates map[string]json.RawMessage
	if decodeErr := json.Unmarshal(rawBody, &updates); decodeErr != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Block credential fields and providers (credentials must use /providers endpoint)
	for k := range updates {
		kl := strings.ToLower(k)
		if kl == "providers" || strings.Contains(kl, "api_key") || strings.Contains(kl, "secret") ||
			strings.Contains(kl, "password") {
			jsonErr(w, http.StatusForbidden, fmt.Sprintf("credential field %q cannot be set via config endpoint", k))
			return
		}
	}

	// Block security-sensitive paths at ANY nesting depth. The walker handles
	// both nested bodies ({"gateway":{"users":[...]}}) and dot-path literal
	// keys ({"gateway.users":[...]}). Rejected requests persist NOTHING — we
	// return before safeUpdateConfigJSON is ever called, so benign sibling
	// keys in the same body are not written either.
	var typedBody map[string]any
	if decodeErr := json.Unmarshal(rawBody, &typedBody); decodeErr != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if path, blocked := matchBlockedPath(typedBody, blockedPaths); blocked {
		jsonErr(
			w,
			http.StatusForbidden,
			fmt.Sprintf("%s is a blocked path — use the dedicated endpoint", path),
		)
		return
	}

	// Use safeUpdateConfigJSON to hold configMu during the read-modify-write cycle.
	// Deep merge nested objects so partial updates don't wipe sibling keys
	// (e.g., updating gateway.port must not delete gateway.users).
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		for k, v := range updates {
			var parsed any
			if err := json.Unmarshal(v, &parsed); err != nil {
				return fmt.Errorf("invalid value for %q: %w", k, err)
			}
			// Deep merge maps; replace scalars/arrays.
			if existingMap, ok := m[k].(map[string]any); ok {
				if newMap, ok := parsed.(map[string]any); ok {
					for nk, nv := range newMap {
						existingMap[nk] = nv
					}
					continue // merged into existing map
				}
			}
			m[k] = parsed
		}
		return nil
	}); err != nil {
		slog.Error("rest: save config", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	a.getConfig(w)
}

// --- Skills ---

// HandleSkills handles GET /api/v1/skills and POST sub-paths (search, install).
func (a *restAPI) HandleSkills(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/skills")
	sub = strings.TrimPrefix(sub, "/")

	switch {
	case r.Method == http.MethodGet && sub == "":
		a.listSkills(w)
	case r.Method == http.MethodGet && sub == "marketplace":
		a.skillMarketplaceStatus(w)
	case r.Method == http.MethodGet && sub == "search":
		a.searchSkills(w, r)
	case r.Method == http.MethodPost && sub == "install":
		a.installSkill(w, r)
	case r.Method == http.MethodDelete && sub != "":
		a.deleteSkill(w, sub)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) listSkills(w http.ResponseWriter) {
	// Per-skill metadata (name, source, description, author, version) sourced from
	// the default agent's skills loader — the single source of truth for installed
	// skills (same loader that feeds GetStartupInfo's aggregate summary).
	detailed := a.agentLoop.ListSkillsDetailed()
	if len(detailed) == 0 {
		jsonOK(w, []gen.Skill{})
		return
	}
	result := make([]gen.Skill, 0, len(detailed))
	for _, s := range detailed {
		// The skill's stable identifier is its slug (ID = directory name). The
		// display Name is the human-readable label (e.g. "Daily Briefing"). They
		// are kept separate so renaming the display label never changes the ID
		// used by DELETE, activation, or built-in detection.
		id := s.ID
		if id == "" {
			id = s.Name // defensive: older loaders may not populate ID
		}
		name := s.Name
		if name == "" {
			name = id
		}
		// A skill is "built-in" (system) when it ships embedded in the binary —
		// identified by its slug (ID), not the display name. DefaultSkillNames()
		// returns slugs. The embedded defaults are seeded into the global skills
		// dir on first boot, so the loader reports their source as "global"; we
		// override that here so they surface as system skills.
		isBuiltin := s.Source == "builtin" || isSystemSkill(id)

		skill := gen.Skill{
			Id:       id,
			Name:     name,
			Status:   gen.SkillStatusActive,
			Verified: isBuiltin, // built-in skills are Omnipus-team-verified.
		}

		// Version: SKILL.md frontmatter when present, else a neutral default.
		if s.Version != "" {
			skill.Version = s.Version
		} else {
			skill.Version = "0.0.0"
		}

		// Description (optional wire field): omit when empty.
		if s.Description != "" {
			desc := s.Description
			skill.Description = &desc
		}

		// Author (optional): built-ins are authored by Omnipus; otherwise use the
		// frontmatter author if present, omitting the field when absent.
		switch {
		case isBuiltin:
			author := "Omnipus"
			skill.Author = &author
		case s.Author != "":
			author := s.Author
			skill.Author = &author
		}

		// Source (optional): map the loader source onto the wire enum. Embedded
		// defaults report "builtin" regardless of the dir they were seeded into.
		effectiveSource := s.Source
		if isBuiltin {
			effectiveSource = "builtin"
		}
		if src, ok := skillSourceToWire(effectiveSource); ok {
			skill.Source = &src
		}

		// ArgumentHint (optional): surface the SKILL.md frontmatter argument-hint
		// in the wire response so the composer palette and ghost-text can use it
		// (FR-006/FR-014/R3). Omit when the skill declares no hint.
		if s.ArgumentHint != "" {
			hint := s.ArgumentHint
			skill.ArgumentHint = &hint
		}

		result = append(result, skill)
	}
	jsonOK(w, result)
}

// skillSourceToWire maps a skills-loader source string onto the generated
// SkillSource wire enum. Returns ok=false for unrecognized values so the caller
// can leave the optional field unset rather than emit an invalid enum.
func skillSourceToWire(source string) (gen.SkillSource, bool) {
	switch source {
	case "builtin":
		return gen.SkillSourceBuiltin, true
	case "global":
		return gen.SkillSourceGlobal, true
	case "workspace":
		return gen.SkillSourceWorkspace, true
	default:
		return "", false
	}
}

// skillSource returns the loader source ("builtin"/"global"/"workspace") for the
// skill addressed by id (the slug = directory name), or "" when the skill is not
// found. Used to enforce the built-in deletion guard (built-ins cannot be
// removed). The DELETE route param is the skill's Id (slug), so matching is keyed
// on the slug — never the human-readable display name.
func (a *restAPI) skillSource(id string) string {
	// Embedded defaults are system skills regardless of which dir they were
	// seeded into (the loader reports them as "global"). DefaultSkillNames()
	// returns slugs, so this check is keyed on the slug.
	if isSystemSkill(id) {
		return "builtin"
	}
	for _, s := range a.agentLoop.ListSkillsDetailed() {
		skillID := s.ID
		if skillID == "" {
			skillID = s.Name
		}
		if skillID == id {
			return s.Source
		}
	}
	return ""
}

// systemSkillNames is the set of embedded default skill names (the "built-in"
// system skills shipped inside the binary). They are seeded into the global
// skills dir on first boot, so they must be identified by NAME — not by the
// loader's directory-derived source — to be surfaced as built-in and protected
// from deletion.
var systemSkillNames = func() map[string]struct{} {
	names := skills.DefaultSkillNames()
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}()

// isSystemSkill reports whether name is one of the embedded default skills.
func isSystemSkill(name string) bool {
	_, ok := systemSkillNames[name]
	return ok
}

// installedSkillIDs returns the set of skill IDs currently known to the agent
// loop (same source as GET /api/v1/skills). An empty map is returned when no
// skills are installed, which lets the validation below produce a proper 400
// ("unknown skill id") rather than silently accepting any string.
func (a *restAPI) installedSkillIDs() map[string]struct{} {
	// Read the skills directories directly. This used to go through
	// GetStartupInfo, which sourced them from the DEFAULT AGENT's context
	// builder — so when no default agent existed the set came back empty, and
	// validateSkillIDs is documented to skip validation entirely on an empty
	// set. Unknown skill ids were then accepted, through three layers of
	// indirection, with nothing logged. Skills are install-wide; no agent is
	// needed to enumerate them.
	workspace := a.homePath
	if cfg := a.agentLoop.GetConfig(); cfg != nil && strings.TrimSpace(cfg.Agents.Defaults.Home) != "" {
		workspace = cfg.Agents.Defaults.Home
	}
	names := agent.InstalledSkillIDs(workspace)
	result := make(map[string]struct{}, len(names))
	for _, n := range names {
		result[n] = struct{}{}
	}
	return result
}

// validateSkillIDs returns an error string (for a 400 response) if any of the
// supplied skill IDs are not present in the installed-skills registry.
// Returns "" when all IDs are valid or when no skills are installed at all
// (to avoid false rejections in environments where the skills directory hasn't
// been populated yet — the agent loop's runtime filter is the final gate).
func (a *restAPI) validateSkillIDs(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	installed := a.installedSkillIDs()
	// Skip validation when the installed set is empty: the skills directory may
	// not exist yet (fresh install, test environment). Accept any id and let the
	// agent loop's skill filter gate unknown ids at runtime.
	if len(installed) == 0 {
		return ""
	}
	for _, id := range ids {
		if _, ok := installed[id]; !ok {
			return fmt.Sprintf("unknown skill id: %q", id)
		}
	}
	return ""
}

// marketplaceEnabled reports whether at least one skill marketplace registry is
// enabled, read live from the current config. When false the search and
// slug-install endpoints refuse with 409 and the SPA hides its skill-browse UI.
// A marketplace is available when any entry in the unified Marketplaces list
// (FR-10.1) is enabled. ClawHub is enabled by default, so the default behavior
// is "on".
func (a *restAPI) marketplaceEnabled() bool {
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		return false
	}
	for _, m := range cfg.Tools.Skills.Marketplaces {
		if m.Enabled {
			return true
		}
	}
	return false
}

// skillMarketplaceStatus handles GET /api/v1/skills/marketplace. It reports
// whether any skill marketplace registry is enabled so the SPA can gate its
// skill-browse UI (search / install-by-slug) on marketplace availability.
func (a *restAPI) skillMarketplaceStatus(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()

	status := gen.SkillMarketplaceStatus{}
	if cfg != nil {
		for _, m := range cfg.Tools.Skills.Marketplaces {
			name := m.Name
			if name == "" {
				name = m.Type
			}
			if m.Enabled {
				status.Enabled = true
			}
			status.Registries = append(status.Registries, struct {
				Enabled bool   `json:"enabled"`
				Name    string `json:"name"`
			}{Enabled: m.Enabled, Name: name})
		}
	}

	jsonOK(w, status)
}

// searchSkillsDefaultLimit / searchSkillsMaxLimit bound the number of marketplace
// results returned by GET /api/v1/skills/search.
const (
	searchSkillsDefaultLimit = 20
	searchSkillsMaxLimit     = 50
)

// searchSkills handles GET /api/v1/skills/search?q=<query>&limit=<n>. It queries
// the configured skill marketplace (ClawHub) and maps each registry hit onto the
// SkillSearchResult wire type. An empty query is a 400; a registry/transport
// failure is a 502 (not a 500) so the SPA can surface "registry unavailable".
func (a *restAPI) searchSkills(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		jsonErr(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	limit := searchSkillsDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			jsonErr(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if limit > searchSkillsMaxLimit {
		limit = searchSkillsMaxLimit
	}

	if !a.marketplaceEnabled() {
		jsonErr(w, http.StatusConflict, "no skill marketplace is enabled")
		return
	}

	if a.skillRegistry == nil {
		slog.Warn("rest: skill search requested but no registry configured")
		jsonErr(w, http.StatusBadGateway, "skill registry unavailable")
		return
	}

	results, err := a.skillRegistry.Search(r.Context(), q, limit)
	if err != nil {
		slog.Warn("rest: skill search failed", "query", q, "error", err)
		jsonErr(w, http.StatusBadGateway, "skill registry unavailable")
		return
	}

	out := make([]gen.SkillSearchResult, 0, len(results))
	for _, res := range results {
		item := gen.SkillSearchResult{Slug: res.Slug}
		if res.DisplayName != "" {
			dn := res.DisplayName
			item.DisplayName = &dn
		}
		if res.Summary != "" {
			sm := res.Summary
			item.Summary = &sm
		}
		if res.Version != "" {
			v := res.Version
			item.Version = &v
		}
		if res.Score != 0 {
			score := res.Score
			item.Score = &score
		}
		if res.RegistryName != "" {
			rn := res.RegistryName
			item.RegistryName = &rn
		}
		if res.OwnerHandle != "" {
			oh := res.OwnerHandle
			item.OwnerHandle = &oh
		}
		out = append(out, item)
	}
	jsonOK(w, out)
}

// installSkill handles POST /api/v1/skills/install. It installs a skill from the
// ClawHub marketplace by its slug, optionally pinning a version. The slug is
// path-validated; the install is SSRF-safe (the registry's HTTP client honors
// the SSRF policy). On success it returns the freshly installed skill as it now
// appears in the local inventory.
func (a *restAPI) installSkill(w http.ResponseWriter, r *http.Request) {
	var req gen.SkillInstallRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SkillInstallRequest", &req, validateEnabled) {
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		jsonErr(w, http.StatusBadRequest, "slug is required")
		return
	}
	// Path-traversal / identity guard: the slug becomes a directory name.
	if err := validateEntityID(slug); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid skill slug")
		return
	}

	if !a.marketplaceEnabled() {
		jsonErr(w, http.StatusConflict, "no skill marketplace is enabled")
		return
	}

	if a.skillRegistry == nil {
		slog.Warn("rest: skill install requested but no registry configured", "slug", slug)
		jsonErr(w, http.StatusBadGateway, "skill registry unavailable")
		return
	}

	// Reject re-installing over an existing skill (built-in or user) so an install
	// never silently clobbers a local skill.
	if a.skillSource(slug) != "" {
		jsonErr(w, http.StatusConflict, fmt.Sprintf("skill %q is already installed", slug))
		return
	}

	version := ""
	if req.Version != nil {
		version = strings.TrimSpace(*req.Version)
	}

	targetDir := filepath.Join(a.homePath, "skills", slug)
	result, err := a.skillRegistry.DownloadAndInstall(r.Context(), slug, version, targetDir)
	if err != nil {
		// Clean up any partial extraction so a failed install leaves no debris.
		if rmErr := os.RemoveAll(targetDir); rmErr != nil {
			slog.Warn("rest: cleanup after failed skill install", "slug", slug, "error", rmErr)
		}
		slog.Warn("rest: skill install failed", "slug", slug, "version", version, "error", err)
		jsonErr(w, http.StatusBadGateway, fmt.Sprintf("could not install skill %q: %v", slug, err))
		return
	}

	// Surface a moderation block as a hard failure: do not leave a malware-flagged
	// skill installed.
	if result != nil && result.IsMalwareBlocked {
		if rmErr := os.RemoveAll(targetDir); rmErr != nil {
			slog.Warn("rest: cleanup after blocked skill install", "slug", slug, "error", rmErr)
		}
		jsonErr(w, http.StatusForbidden, fmt.Sprintf("skill %q is blocked by registry moderation", slug))
		return
	}

	installedVersion := "0.0.0"
	if result != nil && result.Version != "" && result.Version != "latest" {
		installedVersion = result.Version
	}

	skill := gen.Skill{
		Id:       slug,
		Name:     slug,
		Version:  installedVersion,
		Status:   gen.SkillStatusActive,
		Verified: result != nil && result.Verified,
	}
	if result != nil && result.Summary != "" {
		summary := result.Summary
		skill.Description = &summary
	}
	if src, ok := skillSourceToWire("global"); ok {
		skill.Source = &src
	}
	jsonOK(w, skill)
}

func (a *restAPI) deleteSkill(w http.ResponseWriter, name string) {
	if err := validateEntityID(name); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid skill name")
		return
	}
	// Built-in (pre-installed/system) skills ship inside the binary's skills dir
	// and must never be removable through the API — the frontend disables the
	// button, but the backend is the enforcing gate. Reject with 403.
	if a.skillSource(name) == "builtin" {
		jsonErr(w, http.StatusForbidden, "built-in skills cannot be removed")
		return
	}
	// Inject the SSRF checker (SEC-24) so that any outbound HTTP calls made by
	// the installer (e.g. future hash verification against a registry) are
	// protected. a.ssrfChecker is nil when SSRF is disabled; the constructor
	// accepts nil and falls back to a plain HTTP client in that case.
	installer, err := skills.NewSkillInstallerWithSSRF(a.homePath, "", "", a.ssrfChecker)
	if err != nil {
		slog.Error("rest: create skill installer for delete", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not initialize skill installer")
		return
	}
	if err := installer.Uninstall(name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("skill %q not found", name))
			return
		}
		slog.Error("rest: delete skill", "name", name, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not remove skill: %v", err))
		return
	}
	jsonOK(w, map[string]string{"status": "removed", "name": name})
}

// --- Doctor / Diagnostics ---

// HandleDoctor handles GET/POST /api/v1/doctor.
func (a *restAPI) HandleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.agentLoop.GetConfig()

	// Run real diagnostic checks and compute a score.
	issues := a.runDiagnosticChecks(cfg)
	score := 100
	for _, iss := range issues {
		sev, _ := iss["severity"].(string)
		switch sev {
		case "high":
			score -= 20
		case "medium":
			score -= 10
		case "low":
			score -= 5
		}
	}
	if score < 0 {
		score = 0
	}

	// Persist the doctor run result.
	if a.onboardingMgr != nil {
		if err := a.onboardingMgr.RecordDoctorRun(score); err != nil {
			slog.Warn("rest: could not persist doctor run", "error", err)
		}
	}

	result := map[string]any{
		"score":      score,
		"issues":     issues,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	}

	if r.Method == http.MethodGet {
		info := a.agentLoop.GetStartupInfo()
		checks := map[string]any{
			"gateway": map[string]any{
				"status":  "ok",
				"address": fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port),
			},
			"agent_loop": map[string]any{
				"status": "ok",
				"info":   info,
			},
			"session_store": func() map[string]any {
				for _, id := range a.agentLoop.GetRegistry().ListAgentIDs() {
					if store := a.agentLoop.GetAgentStore(id); store != nil {
						return map[string]any{"status": "ok", "available": true}
					}
				}
				return map[string]any{"status": "degraded", "available": false}
			}(),
			"go_runtime": map[string]any{
				"version":    runtime.Version(),
				"goroutines": runtime.NumGoroutine(),
				"os":         runtime.GOOS,
				"arch":       runtime.GOARCH,
			},
		}
		result["status"] = "ok"
		result["checks"] = checks
	}

	jsonOK(w, result)
}

// runDiagnosticChecks performs real diagnostic checks and returns issues found.
// Returns a non-nil empty slice when there are no issues so the JSON shape is
// always `"issues": []` rather than `"issues": null` — frontend consumers
// (DiagnosticsSection.tsx) call .filter() directly on the field.
func (a *restAPI) runDiagnosticChecks(cfg *config.Config) []map[string]any {
	issues := []map[string]any{}

	// Check if a default model is configured.
	if len(cfg.Providers) == 0 {
		issues = append(issues, map[string]any{
			"id":             "no-models",
			"severity":       "high",
			"title":          "No LLM models configured",
			"description":    "No models are configured in model_list. The agent cannot generate responses without at least one model.",
			"recommendation": "Go to Settings → Providers and add an API key.",
			"action_link":    "/settings?tab=providers",
			"action_label":   "Configure providers",
		})
	}

	// Session store is always available via the unified store on each agent.

	// Check if any agents are configured.
	if len(cfg.Agents.List) == 0 {
		issues = append(issues, map[string]any{
			"id":             "no-custom-agents",
			"severity":       "low",
			"title":          "No custom agents configured",
			"description":    "Only the built-in agents are available. Custom agents can be defined to personalise your assistant.",
			"recommendation": "Go to Settings → Agents and create a custom agent.",
			"action_link":    "/settings?tab=agents",
			"action_label":   "Manage agents",
		})
	}

	// Check sandbox configuration. ResolvedMode is the source of truth.
	// Both "enforce" and "permissive" represent an active sandbox; only
	// "off" (or an empty config that resolves to off) should warn.
	if cfg.Sandbox.ResolvedMode() == string(config.SandboxModeOff) {
		issues = append(issues, map[string]any{
			"id":             "sandbox-disabled",
			"severity":       "medium",
			"title":          "Sandbox is disabled",
			"description":    "Filesystem and process sandboxing is not enabled. Agent tool executions run without confinement.",
			"recommendation": "Go to Settings → Security → Advanced to enable sandbox mode.",
			"action_link":    "/settings?tab=security",
			"action_label":   "Open security settings",
		})
	}

	// D6: God mode armed. Triggered on the raw persisted intent
	// (cfg.Sandbox.GodMode), NOT on cfg.Sandbox.GodModeAllowed alone —
	// GodModeAllowed being true (S3: authorized in the past, currently
	// disabled) is a genuinely inert state and must not false-positive here.
	// cfg.Sandbox.GodMode true covers both S1 (armed via the UI, pending
	// restart — the config write already happened even though this boot
	// hasn't activated it) and S2 (live-active): either way an operator has
	// committed to disabling the kernel sandbox, egress restrictions, and
	// the shell guard, which is strictly worse than the sandbox-disabled
	// check above (that one only concerns the sandbox; god mode disables
	// three controls simultaneously), hence "high" not "medium".
	if cfg.Sandbox.GodMode {
		issues = append(issues, map[string]any{
			"id":             "god-mode-armed",
			"severity":       "high",
			"title":          "God-mode is armed",
			"description":    "God-mode is enabled or pending activation. It bypasses every permission prompt and disables the kernel sandbox, outbound-network restrictions, and the shell guard for every agent.",
			"recommendation": "Go to Settings → Security → Danger zone and turn god-mode off, unless this is intentional.",
			"action_link":    "/settings?tab=security",
			"action_label":   "Open security settings",
		})
	}

	return issues
}

// HandleUserContext handles GET and PUT /api/v1/user-context.
// It reads and writes USER.md in the default workspace directory, which holds
// workspace-level context about the user (their background, preferences, etc.).
func (a *restAPI) HandleUserContext(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getUserContext(w)
	case http.MethodPut:
		a.putUserContext(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) getUserContext(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()
	userMDPath := filepath.Join(cfg.AgentHomeBasePath(), "USER.md")
	content := ""
	if data, err := os.ReadFile(userMDPath); err != nil {
		if !os.IsNotExist(err) {
			// Distinguish missing file (normal, return empty) from unreadable file (error).
			slog.Error("rest: read USER.md", "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read USER.md: %v", err))
			return
		}
	} else {
		content = string(data)
	}
	jsonOK(w, gen.UserContextResponse{Content: content})
}

func (a *restAPI) putUserContext(w http.ResponseWriter, r *http.Request) {
	var req gen.UserContextRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "UserContextRequest", &req, validateEnabled) {
		return
	}
	cfg := a.agentLoop.GetConfig()
	userMDPath := filepath.Join(cfg.AgentHomeBasePath(), "USER.md")
	if err := fileutil.WriteFileAtomic(userMDPath, []byte(req.Content), 0o600); err != nil {
		slog.Error("rest: write USER.md", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not write USER.md: %v", err))
		return
	}
	jsonOK(w, gen.UserContextResponse{Content: req.Content})
}

// registerAdditionalEndpoints registers handlers for endpoints the frontend calls.
// Each returns a valid JSON response matching the shape the frontend expects,
// preventing "Unexpected token '<'" errors from the SPA catch-all.
func (a *restAPI) registerAdditionalEndpoints(cm httpHandlerRegistrar) {
	cm.RegisterHTTPHandler("/api/v1/state", a.withOptionalAuth(a.HandleState))
	cm.RegisterHTTPHandler("/api/v1/system/cli-detect", a.withAuth(a.HandleSystemCliDetect))
	// withAuth, never withOptionalAuth: this lists the operator's own disk.
	cm.RegisterHTTPHandler("/api/v1/system/folders", a.withAuth(a.HandleSystemFolders))
	// POST /api/v1/system/cli-validate — spawns a caller-supplied path
	// (<cli> --version), so it is hardened as a privileged diagnostic (ADR-030
	// §11 F-01). CREATE-PARITY auth: plain withAuth, exactly like createAgent /
	// HandleAgents below — NOT admin, NOT RequireNotBypass (anyone who can add
	// the subagent can validate it). A DEDICATED rate limiter (cliValidateLimiter,
	// distinct from validateLimiter) throttles the spawn endpoint; the handler
	// additionally enforces a per-caller in-flight cap and audits each call.
	cm.RegisterHTTPHandler(
		"/api/v1/system/cli-validate",
		a.withAuth(withRateLimit(cliValidateLimiter, a.HandleSystemCliValidate)),
	)
	cm.RegisterHTTPHandler("/api/v1/status", a.withAuth(a.HandleStatus))
	// GET /api/v1/tasks/occurrences — Calendar Recurrence Redesign occurrence
	// expansion endpoint (FR-008, contracts/openapi.yaml operationId
	// listTaskOccurrences). Registered as an EXACT pattern, independent of
	// registration order relative to the "/api/v1/tasks/" prefix route
	// below: dynamicServeMux.ServeHTTP (pkg/channels/dynamic_mux.go) always
	// checks its handlers map for an exact path match FIRST, falling back
	// to the longest trailing-slash PREFIX match only when no exact match
	// exists — so this exact "occurrences" registration always wins over
	// HandleTasks' ID-parsing branch (which would otherwise 404 it as
	// task-not-found, per the spec's "Routing note"). Wrapped in the
	// DEDICATED taskReadLimiter (240/min, rest_auth.go) — NOT configLimiter
	// and NOT plain withAuth like the task CRUD routes immediately below
	// (which carry no limiter).
	cm.RegisterHTTPHandler(
		"/api/v1/tasks/occurrences",
		a.withAuth(withRateLimit(taskReadLimiter, a.HandleTaskOccurrences)),
	)
	cm.RegisterHTTPHandler("/api/v1/tasks", a.withAuth(a.HandleTasks))
	cm.RegisterHTTPHandler("/api/v1/tasks/", a.withAuth(a.HandleTasks))
	// Plans REST surface (ADR-049 D1, Wave 2-C1). GET/POST /workspaces/{id}/plans
	// is dispatched from HandleWorkspaces (rest_workspaces.go); individual plan
	// GET/PUT/DELETE and /approve /stop live here.
	cm.RegisterHTTPHandler("/api/v1/plans", a.withAuth(a.HandlePlans))
	cm.RegisterHTTPHandler("/api/v1/plans/", a.withAuth(a.HandlePlans))
	cm.RegisterHTTPHandler("/api/v1/workspaces", a.withAuth(withRateLimit(configLimiter, a.HandleWorkspaces)))
	cm.RegisterHTTPHandler("/api/v1/workspaces/", a.withAuth(withRateLimit(configLimiter, a.HandleWorkspaces)))
	// Library file explorer (rest_library.go). withUploadAuth, not plain
	// withAuth: /library/{id}/upload streams multipart straight through this
	// dispatcher, and withAuth's body limit would truncate it. Every JSON
	// route behind this dispatcher is still independently capped at 1MB by
	// decodeAndValidate, so relaxing the outer limit does not widen the
	// attack surface for the non-upload operations.
	cm.RegisterHTTPHandler("/api/v1/library", a.withUploadAuth(withRateLimit(configLimiter, a.HandleLibrary)))
	cm.RegisterHTTPHandler("/api/v1/library/", a.withUploadAuth(withRateLimit(configLimiter, a.HandleLibrary)))
	// GET/PUT /api/v1/providers/default-model (ADR-068 FR-018/FR-042,
	// T068-11): its OWN route with the high-blast-radius adminWrap chain
	// (withAuth → RequireNotBypass — 401 unauthenticated, 503 under
	// dev-mode bypass), registered ahead of the /providers/ prefix
	// dispatcher. "default-model" is a reserved path segment, never a
	// provider id (MAJ-002); the dynamic mux matches this exact path before
	// the subtree prefix below, so a PUT here can never reach the
	// /providers/{id} upsert branch.
	cm.RegisterHTTPHandler("/api/v1/providers/default-model", a.adminWrap(a.HandleDefaultModel))
	// GET /api/v1/providers/catalog (ADR-067 FR-017, T067-10). Its own
	// exact path under withAuth, registered ahead of the /providers/
	// subtree dispatcher: "catalog" is a reserved path segment and is
	// never a provider id, and the exact match always beats the prefix.
	// Plain withAuth rather than adminWrap — this is a READ of the same
	// public registry document the binary ships embedded, so a dev-mode
	// bypass 503 would only break local development for no gain.
	cm.RegisterHTTPHandler("/api/v1/providers/catalog", a.withAuth(a.HandleProvidersCatalog))
	cm.RegisterHTTPHandler("/api/v1/providers", a.withOptionalAuth(a.HandleProviders))
	cm.RegisterHTTPHandler("/api/v1/providers/", a.withOptionalAuth(a.HandleProviders))
	cm.RegisterHTTPHandler("/api/v1/mcp-servers", a.withAuth(a.HandleMCPServers))
	cm.RegisterHTTPHandler("/api/v1/mcp-servers/", a.withAuth(a.HandleMCPServers))
	cm.RegisterHTTPHandler("/api/v1/storage/stats", a.withAuth(a.HandleStorageStats))
	cm.RegisterHTTPHandler("/api/v1/tools", a.withAuth(a.HandleToolsRegistry))
	cm.RegisterHTTPHandler("/api/v1/tools/builtin", a.withAuth(a.HandleBuiltinToolsDeprecated))
	cm.RegisterHTTPHandler("/api/v1/tools/mcp", a.withAuth(a.HandleMCPTools))
	cm.RegisterHTTPHandler("/api/v1/tool-approvals/", a.withAuth(a.HandleToolApprovals))
	cm.RegisterHTTPHandler("/api/v1/channels", a.withAuth(a.HandleChannels))
	cm.RegisterHTTPHandler("/api/v1/channels/", a.withAuth(a.HandleChannels))
	cm.RegisterHTTPHandler("/api/v1/agents/", a.withAuth(a.HandleAgents))
	// M11: list all configured mailboxes (never 404s; empty list = none) so the
	// SPA doesn't have to probe every agent's /agents/{id}/mailbox endpoint.
	cm.RegisterHTTPHandler("/api/v1/mailboxes", a.withAuth(a.listMailboxes))
	cm.RegisterHTTPHandler("/api/v1/config/gateway/rotate-token", a.withAuth(a.rotateGatewayToken))
	cm.RegisterHTTPHandler("/api/v1/activity", a.withAuth(a.HandleActivity))

	// Schedules CRUD + run-now + pause (#264).
	cm.RegisterHTTPHandler("/api/v1/schedules", a.withAuth(a.HandleSchedules))
	cm.RegisterHTTPHandler("/api/v1/schedules/", a.withAuth(a.HandleSchedules))
	// Header notification center (#264).
	cm.RegisterHTTPHandler("/api/v1/notifications", a.withAuth(a.HandleNotifications))
	cm.RegisterHTTPHandler("/api/v1/notifications/", a.withAuth(a.HandleNotifications))

	// Memory settings endpoint (FR-019 / US-6, ADR-027): readable/writable by any
	// authenticated user (A2/G-02 — not admin-only because recap and retention
	// settings are non-sensitive operational knobs without blast-radius risk).
	cm.RegisterHTTPHandler("/api/v1/settings/memory", a.withAuth(a.HandleMemorySettings))

	// Token-budget settings endpoint (ADR-053 D12/R§8.3, FE-6 / US-13): readable
	// by any authenticated user, same posture as /settings/memory. GET returns
	// the live spend accounting; PUT persists the restart-gated ceiling (the live
	// spend lever is Stop/cancel, not a live token cut — R§8.3e/FR-177).
	cm.RegisterHTTPHandler("/api/v1/settings/token-budget", a.withAuth(a.HandleTokenBudgetSettings))

	// Settings endpoints (Wave 4).
	// GET /api/v1/audit-log — the audit log contains every privileged action,
	// tool-use trace, and LLM request; gated behind authentication only under
	// the single-account model.
	// Chain: withAuth (verifies token) → handler.
	cm.RegisterHTTPHandler("/api/v1/audit-log", a.withAuth(a.HandleAuditLog))
	cm.RegisterHTTPHandler("/api/v1/security/exec-allowlist", a.withAuth(a.HandleExecAllowlist))
	// Wave 3 security endpoints (SEC-25, SEC-28).
	cm.RegisterHTTPHandler("/api/v1/security/exec-proxy-status", a.withAuth(a.HandleExecProxyStatus))
	// High-blast-radius security endpoints.
	// Chain: withAuth → RequireNotBypass → handler.
	// CSRF is enforced by the global WrapHTTPHandler layer (no per-handler wiring needed).
	cm.RegisterHTTPHandler("/api/v1/config/pending-restart", a.adminWrap(a.HandlePendingRestart))
	// O4-backend: UI-triggerable graceful self-restart. High blast radius —
	// RequireNotBypass (dev_mode_bypass → 503) via adminWrap.
	cm.RegisterHTTPHandler("/api/v1/gateway/restart", a.adminWrap(a.HandleGatewayRestart))
	// O14 god-mode toggle. High blast radius — RequireNotBypass via adminWrap,
	// and the POST additionally requires a password re-auth consent token
	// (enforced inside the handler via requireReAuth).
	cm.RegisterHTTPHandler("/api/v1/gateway/god-mode", a.adminWrap(a.HandleGodMode))
	cm.RegisterHTTPHandler("/api/v1/security/audit-log", a.adminWrap(a.HandleSandboxAuditLog))
	cm.RegisterHTTPHandler("/api/v1/security/skill-trust", a.adminWrap(a.HandleSkillTrust))
	cm.RegisterHTTPHandler("/api/v1/security/prompt-guard", a.adminWrap(a.HandlePromptGuard))
	// /api/v1/security/rate-limits handles GET (read state — the response carries
	// the live daily-cost meter and current cap config, sensitive observability)
	// and PUT (write — gated by RequireNotBypass, since dev_mode_bypass would
	// otherwise let an anonymous caller change global rate-limit caps). Wrapped
	// with adminWrap to bring it in line with the other high-blast-radius
	// security endpoints below and to satisfy item 7 of v0.2-#155 (admin-route
	// bypass coverage).
	cm.RegisterHTTPHandler("/api/v1/security/rate-limits", a.adminWrap(a.HandleRateLimits))
	cm.RegisterHTTPHandler("/api/v1/security/sandbox-config", a.adminWrap(a.HandleSandboxConfig))
	cm.RegisterHTTPHandler("/api/v1/security/session-scope", a.adminWrap(a.HandleSessionScope))
	cm.RegisterHTTPHandler("/api/v1/security/retention", a.adminWrap(a.HandleRetention))
	cm.RegisterHTTPHandler("/api/v1/security/retention/sweep", a.adminWrap(a.HandleRetentionSweep))
	cm.RegisterHTTPHandler("/api/v1/performance", a.adminWrap(a.HandlePerformance))
	// Wave 5 security endpoints (SEC-01/02/03).
	cm.RegisterHTTPHandler("/api/v1/security/sandbox-status", a.withAuth(a.HandleSandboxStatus))
	// /api/v1/security/sandbox-config is registered above with adminWrap — do NOT
	// re-register here; Go ServeMux takes the last registration, and a lighter
	// wrapper here would silently drop the dev_mode_bypass gate.
	// GET/PUT /api/v1/security/tool-policies — readable and writable by the
	// single authenticated account (PUT authorization enforced inside
	// HandleToolPolicies).
	cm.RegisterHTTPHandler("/api/v1/security/tool-policies", a.withAuth(a.HandleToolPolicies))
	// GET /api/v1/credentials — even though plaintext is not returned, the
	// credential ref names reveal what integrations exist, so this is gated
	// behind authentication.
	// Chain: withAuth (verifies token) → handler.
	cm.RegisterHTTPHandler("/api/v1/credentials", a.withAuth(a.HandleCredentials))
	cm.RegisterHTTPHandler("/api/v1/credentials/", a.withAuth(a.HandleCredentials))
	// Option A keeps workspace and media IDs as separately validated path segments;
	// the legacy global media route remains below for backward compatibility.
	cm.RegisterHTTPHandler("/api/v1/media/workspace/", a.withOptionalAuth(a.HandleMediaByRef))
	cm.RegisterHTTPHandler("/api/v1/media/", a.withOptionalAuth(a.HandleMedia))
	cm.RegisterHTTPHandler("/api/v1/backup", a.withAuth(a.HandleCreateBackup))
	cm.RegisterHTTPHandler("/api/v1/backups", a.withAuth(a.HandleListBackups))
	cm.RegisterHTTPHandler("/api/v1/restore", a.withAuth(a.HandleRestore))
	// Exact match takes precedence over the /sessions/ prefix handler for this specific path.
	cm.RegisterHTTPHandler("/api/v1/sessions/all", a.withAuth(a.HandleClearSessions))
	cm.RegisterHTTPHandler("/api/v1/about", a.withAuth(a.HandleAbout))
	cm.RegisterHTTPHandler("/api/v1/user-context", a.withAuth(a.HandleUserContext))
	cm.RegisterHTTPHandler(
		"/api/v1/onboarding/complete",
		a.withOptionalAuth(withRateLimit(onboardingCompleteLimiter, a.HandleCompleteOnboarding)),
	)
	cm.RegisterHTTPHandler(
		"/api/v1/onboarding/probe-provider",
		a.withOptionalAuth(withRateLimit(onboardingCompleteLimiter, a.HandleOnboardingProbeProvider)),
	)
	cm.RegisterHTTPHandler("/api/v1/auth/login", a.withOptionalAuth(a.HandleLogin))
	cm.RegisterHTTPHandler("/api/v1/auth/validate", a.withAuth(withRateLimit(validateLimiter, a.HandleValidateToken)))
	cm.RegisterHTTPHandler("/api/v1/auth/logout", a.withAuth(a.HandleLogout))
	cm.RegisterHTTPHandler("/api/v1/auth/change-password", a.withAuth(a.HandleChangePassword))
	// Password re-auth consent primitive (Spec-6 FR-12.2). Distinct from
	// RequireNotBypass (a 503 dev-mode guard) — this re-verifies the user's one
	// password before a sensitive settings change.
	cm.RegisterHTTPHandler("/api/v1/auth/reauth", a.withAuth(withRateLimit(reauthLimiter, a.HandleReAuth)))

	// Integrations provider-picker — search + voice-input providers (Spec-6
	// FR-12.1). GET lists; PUT (gated by the re-auth consent token) configures.
	cm.RegisterHTTPHandler("/api/v1/integrations/providers", a.withAuth(a.HandleIntegrationProviders))
	cm.RegisterHTTPHandler("/api/v1/integrations/providers/", a.withAuth(a.HandleIntegrationProviders))
	// Automations — trigger→action display projection over schedules (W3-AC
	// UI reframe). Read-only; writes go through /api/v1/schedules.
	cm.RegisterHTTPHandler("/api/v1/automations", a.withAuth(a.HandleAutomations))
	// Composer mic — voice transcription (Spec-6 FR-12.1).
	cm.RegisterHTTPHandler("/api/v1/voice/transcribe", a.withAuth(a.HandleTranscribe))
	// Voice provider descriptor (agent-form spec §4.10.1). Drives the dropdown /
	// free-text / disabled widget in the agent edit slide-over.
	cm.RegisterHTTPHandler("/api/v1/voice/provider", a.withAuth(a.HandleVoiceProvider))

	// File upload endpoints (Milestone 3).
	cm.RegisterHTTPHandler("/api/v1/upload", a.withUploadAuth(a.HandleUpload))
	cm.RegisterHTTPHandler("/api/v1/uploads/", a.withOptionalAuth(a.HandleServeUpload))

	// Prometheus-compatible metrics endpoint (FR-039).
	// Unauthenticated for Prometheus scrape compatibility; does not expose secrets.
	cm.RegisterHTTPHandler("/metrics", http.HandlerFunc(a.HandleMetrics))

	// Version endpoint — unauthenticated; returns build SHA for frontend version-drift detection (#110).
	cm.RegisterHTTPHandler("/api/v1/version", http.HandlerFunc(a.HandleVersion))

	// GET /api/v1/devices — returns pending pairing requests and paired devices.
	// Admin-only. Device pairing infrastructure is not yet implemented; the handler
	// returns valid empty arrays so the SPA DevicesSection renders its empty state
	// rather than 404-ing. Traces to: contracts/components/schemas/DevicesResponse.yaml.
	cm.RegisterHTTPHandler("/api/v1/devices", a.adminWrap(a.HandleDevices))

	// The legacy GTD /board/tasks endpoints were folded into the unified
	// /api/v1/tasks surface in Sprint 2 (one store, one wire schema). See
	// HandleTasks above (registered earlier) for the unified CRUD + subtasks +
	// todos + dependencies routes.

	// Token usage stats endpoint (Wave 2b).
	// Traces to: contracts/components/schemas/TokenUsageSummary.yaml.
	cm.RegisterHTTPHandler("/api/v1/stats/tokens", a.withAuth(withRateLimit(configLimiter, a.HandleTokenStats)))

	// Best-effort DOM inspect for the live interactive browser panel
	// (ADR-039 D-B3) — resolves the element at a point so the SPA can attach
	// its text/HTML when a user annotates a spot. Sibling to, but registered
	// independently of, /api/v1/browser/ws (gateway.go).
	cm.RegisterHTTPHandler("/api/v1/browser/inspect", a.withAuth(a.HandleBrowserInspect))
}

// registerPreviewEndpoints registers /preview/ on the MAIN mux (ADR-044,
// FR-001/FR-002/FR-003). There is no separate preview listener/mux anymore —
// /preview/ shares gateway.port with the SPA and /api/v1/*.
//
// Auth model: token-only (FR-023). Registered bare — no withAuth/session
// wrapping — the URL path token is the credential. (There is also no live
// Origin-check middleware in this handler chain to opt out of:
// middleware.RequireMatchingOriginOnStateChanging exists in origin.go as a
// tested reference helper — FR-023a documents the /preview/ exemption it
// would need — but it is not wired into gateway.go for ANY route, so this is
// not something /preview/ specifically forgoes.) It DOES inherit the global
// configSnapshotMiddleware (and the CSRF middleware, which exempts the
// /preview/ prefix — see middleware/csrf.go's defaultExemptPrefixes) because
// those are wrapped around the whole main mux in gateway.go, not per-route.
// HandlePreview itself checks cfg.IsPreviewEnabled() live on every request
// and 404s when disabled (FR-006) — toggling it never requires a restart.
//
// /preview/ is the unified route for the web_serve tool. The legacy /serve/
// and /dev/ back-compat handlers (for registrations produced before /preview/
// landed, 2026-05-04) were retired from actually SERVING content once the
// safety window for any such registration closed — registrations are
// short-lived (max 24h), so nothing minted before the migration could still
// be valid. "Retired" here means routing to HandlePreview was removed; it
// does NOT mean the prefixes were dropped from the mux entirely — see below.
//
// Both legacy prefixes remain registered here, but ONLY to a dedicated
// 404 responder (handleLegacyPreviewRetired) — not to HandlePreview. Without
// this, an unmatched /serve/... or /dev/... request falls through to the
// "/" SPA catch-all (embed.go's newSPAHandler), which answers any unknown
// path with a 200 + index.html. The SPA's own link validator
// (src/lib/preview-url.ts's PREVIEW_PATH_REGEX) still recognizes /serve/ and
// /dev/ paths for historical transcript replay, and its warmup probe
// (IframePreview.tsx) does a same-origin HEAD fetch expecting a genuine
// non-2xx/3xx status to detect a dead legacy link — a 200-index.html
// response instead makes it falsely report the (nonexistent) dev server as
// "ready". Registering these two prefixes on the dynamicServeMux
// (pkg/channels/dynamic_mux.go) outranks the "/" catch-all for any path
// under them, since it dispatches by longest matching subtree prefix.
func (a *restAPI) registerPreviewEndpoints(cm httpHandlerRegistrar) {
	cm.RegisterHTTPHandler(middleware.PreviewPathPrefix, http.HandlerFunc(a.HandlePreview))
	cm.RegisterHTTPHandler(legacyServePathPrefix, http.HandlerFunc(handleLegacyPreviewRetired))
	cm.RegisterHTTPHandler(legacyDevPathPrefix, http.HandlerFunc(handleLegacyPreviewRetired))
}

// legacyServePathPrefix and legacyDevPathPrefix are the retired back-compat
// preview prefixes (pre-ADR-044). See registerPreviewEndpoints' doc comment.
const (
	legacyServePathPrefix = "/serve/"
	legacyDevPathPrefix   = "/dev/"
)

// handleLegacyPreviewRetired answers a GET/HEAD/OPTIONS request under the
// retired /serve/ or /dev/ preview prefixes with a genuine 404. It exists
// solely to keep these paths off the "/" SPA catch-all — no legacy token
// minted before the ADR-044 migration can still be valid (registrations are
// short-lived, max 24h), so there is nothing to look up or proxy here. A
// state-changing method (POST/PUT/PATCH/DELETE) never actually reaches this
// handler: /serve/ and /dev/ are deliberately NOT in the CSRF
// exempt-prefixes set (middleware/csrf.go's defaultExemptPrefixes, which
// lists only middleware.PreviewPathPrefix), so the CSRF middleware rejects
// those methods with 403 first. Either outcome — this handler's 404 or the
// CSRF middleware's 403 — keeps the retired prefixes off the 200 SPA shell,
// which is the invariant that matters.
func handleLegacyPreviewRetired(w http.ResponseWriter, _ *http.Request) {
	jsonErr(w, http.StatusNotFound, "this preview path prefix has been retired; use /preview/")
}

// rotateGatewayToken generates a new random bearer token, persists it to config, and returns it.
// POST /api/v1/config/gateway/rotate-token
func (a *restAPI) rotateGatewayToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		slog.Error("rest: generate gateway token", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not generate token")
		return
	}
	// Token format: "omnipus_" + 64-char lowercase hex (32 random bytes).
	// This matches the BearerToken schema pattern '^omnipus_[a-f0-9]{64}$'.
	newToken := "omnipus_" + hex.EncodeToString(tokenBytes)
	// Persist to config.json BEFORE updating the live config.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, _ := m["gateway"].(map[string]any)
		if gw == nil {
			gw = map[string]any{}
			m["gateway"] = gw
		}
		gw["token"] = newToken
		return nil
	}); err != nil {
		slog.Error("rest: save config for token rotation", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// Persistence succeeded. Reload so the in-memory config picks up the new token.
	// If reload fails, the new token is on disk but not yet active — return 500 so the
	// caller knows the token is not yet in effect and can retry.
	//
	// triggerReloadAndWait (not the bare TriggerReload): the caller's very next
	// request authenticates with the token in this response body, so returning
	// before the reload lands hands out a token that 401s. A request that
	// arrives mid-reload used to be dropped entirely, making that permanent
	// until some unrelated reload happened to run.
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Error("config reload after token rotation failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("token saved but reload failed: %v", err))
		return
	}
	jsonOK(w, gen.RotateTokenResponse{Token: newToken})
}

// httpHandlerRegistrar is the subset of channels.Manager used for route
// registration on the main mux. registerPreviewEndpoints (ADR-044) uses this
// same interface — /preview/ no longer has its own registrar/mux.
type httpHandlerRegistrar interface {
	RegisterHTTPHandler(pattern string, handler http.Handler)
}

// --- App State ---

// HandleState handles GET/PATCH /api/v1/state (onboarding state).
func (a *restAPI) HandleState(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		complete := true
		var lastRun *time.Time
		var lastScore *int
		if a.onboardingMgr != nil {
			complete = a.onboardingMgr.IsComplete()
			lastRun = a.onboardingMgr.LastDoctorRun()
			lastScore = a.onboardingMgr.LastDoctorScore()
		}
		resp := map[string]any{
			"onboarding_complete": complete,
		}
		if lastRun != nil {
			resp["last_doctor_run"] = lastRun.Format(time.RFC3339)
		}
		if lastScore != nil {
			resp["last_doctor_score"] = *lastScore
		}
		jsonOK(w, resp)
	case http.MethodPatch:
		var body gen.AppStatePatchRequest
		validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
		if !decodeAndValidate(w, r, "AppStatePatchRequest", &body, validateEnabled) {
			return
		}
		if body.OnboardingComplete == nil || !*body.OnboardingComplete {
			jsonErr(w, http.StatusBadRequest, "onboarding_complete must be true")
			return
		}
		if a.onboardingMgr != nil {
			if err := a.onboardingMgr.CompleteOnboarding(); err != nil {
				slog.Error("rest: could not persist onboarding completion", "error", err)
				jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save onboarding state: %v", err))
				return
			}
		}
		jsonOK(w, gen.AppState{
			OnboardingComplete: true,
		})
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// --- Gateway Status ---

// HandleStatus handles GET /api/v1/status (polled by StatusBar every 15s).
func (a *restAPI) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.agentLoop.GetConfig()
	v := Version
	jsonOK(w, gen.GatewayStatus{
		Online:       true,
		AgentCount:   len(cfg.Agents.List) + 1,      // +1 for system agent
		ChannelCount: countEnabledChannels(cfg) + 1, // +1 for webchat (always available)
		DailyCost:    0,
		Version:      &v,
	})
}

// HandleVersion handles GET /api/v1/version — unauthenticated build-info endpoint
// used by the frontend to detect version drift and prompt "New version available" (#110).
// Both fields are contract-constrained: `version` matches ^\d+\.\d+\.\d+(?:[-+].*)?$
// (semver) and `build_sha` matches ^([0-9a-f]{7,40}|dev)$ (see VersionResponse.yaml).
// build_sha is "dev" when built outside a version-controlled tree, else a 7-40 char
// lowercase hex SHA pulled from debug.ReadBuildInfo() vcs.revision.
func (a *restAPI) HandleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	buildSha := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				buildSha = s.Value
				break
			}
		}
	}
	jsonOK(w, gen.VersionResponse{
		Version:  Version,
		BuildSha: buildSha,
	})
}

// --- Devices ---

// HandleDevices handles GET /api/v1/devices. Returns pending pairing requests
// and already-paired devices. Device pairing infrastructure is not yet fully
// implemented (the device-side request entry point and persistence are
// missing); this handler returns valid empty arrays so the SPA renders its
// empty state. Dark-launched behind Sandbox.Experimental.DevicePairingEnabled
// — returns 404 when disabled (default).
// Traces to: contracts/openapi.yaml#/paths/~1devices/get (operationId: listDevices).
// Traces to: contracts/components/schemas/DevicesResponse.yaml.
func (a *restAPI) HandleDevices(w http.ResponseWriter, r *http.Request) {
	if !a.agentLoop.GetConfig().Sandbox.Experimental.DevicePairingEnabled {
		jsonErr(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	resp := gen.DevicesResponse{
		Pending: []struct {
			CreatedAt   time.Time "json:\"created_at\""
			DeviceId    string    "json:\"device_id\""
			DeviceName  string    "json:\"device_name\""
			ExpiresAt   time.Time "json:\"expires_at\""
			Fingerprint string    "json:\"fingerprint\""
			PairingCode string    "json:\"pairing_code\""
		}{},
		Paired: []struct {
			DeviceId    string                          "json:\"device_id\""
			DeviceName  string                          "json:\"device_name\""
			Fingerprint string                          "json:\"fingerprint\""
			LastSeenAt  time.Time                       "json:\"last_seen_at\""
			PairedAt    time.Time                       "json:\"paired_at\""
			Status      gen.DevicesResponsePairedStatus "json:\"status\""
		}{},
	}
	jsonOK(w, resp)
}

// --- Tasks ---

// validateEntityID rejects IDs that contain path separators, "..", or null bytes
// to prevent path traversal attacks.
func validateEntityID(id string) error {
	if id == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("invalid id")
	}
	return nil
}

// The unified /api/v1/tasks handlers (HandleTasks and friends) live in
// rest_tasks.go.

// --- Activity ---

// ActivityEvent type is defined in contracts/components/schemas/ActivityEvent.yaml
// and generated into pkg/api/generated/. Use gen.ActivityEvent directly.

// HandleActivity handles GET /api/v1/activity.
// Returns up to 50 activity events from the last 24 hours, sorted reverse-chronological.
func (a *restAPI) HandleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	var events []gen.ActivityEvent
	var sessionWarning string

	// Build agent name lookup
	cfg := a.agentLoop.GetConfig()
	agentNames := map[string]string{}
	for _, ac := range cfg.Agents.List {
		agentNames[ac.ID] = ac.Name
	}

	// Collect session_start events from all agent stores (last 24h).
	{
		// ADR-057 FR-092/FR-098 (U9's new paginated signature): this feed
		// scans every session across every store, hierarchy notwithstanding
		// (it existed before FR-091's roots/children split), so flat=true
		// preserves the pre-pagination "all agent stores" semantics exactly.
		// limit=0 means "no limit" (FR-098(b)) — the 24h cutoff below is
		// itself the real bound on how much of the result actually surfaces.
		page, partialErrs := a.agentLoop.ListAllSessions(0, 0, "", true)
		metas := page.Sessions
		if len(partialErrs) > 0 {
			agentIDs := make([]string, 0, len(partialErrs))
			for _, pe := range partialErrs {
				sanitized := sanitizePartialError(pe)
				// Extract the "agent=<id>" prefix for the summary message.
				agentLabel := sanitized
				if idx := strings.Index(sanitized, ":"); idx > 0 {
					agentLabel = sanitized[:idx]
				}
				agentIDs = append(agentIDs, agentLabel)
				slog.Warn("rest: activity: session listing failed", "error", pe)
			}
			sessionWarning = fmt.Sprintf("could not load session history for %d agents: %s (see gateway logs)",
				len(agentIDs), strings.Join(agentIDs, ", "))
		}
		{
			for _, m := range metas {
				if m.CreatedAt.After(cutoff) {
					summary := m.Title
					if summary == "" {
						summary = "New session"
					}
					agentID := m.AgentID
					agentName := agentNames[m.AgentID]
					ev := gen.ActivityEvent{
						Id:        "session-" + m.ID,
						Type:      "session_start",
						Timestamp: m.CreatedAt,
						Summary:   &summary,
					}
					if agentID != "" {
						ev.AgentId = &agentID
					}
					if agentName != "" {
						ev.AgentName = &agentName
					}
					events = append(events, ev)
				}
			}
		}
	}

	// Collect task_created / task_updated events from the unified task store.
	recentTasks, taskErr := a.taskStore.List(task.Filter{})
	if taskErr != nil {
		slog.Warn("rest: activity: list tasks", "error", taskErr)
	}
	for _, t := range recentTasks {
		if createdAt, perr := time.Parse(time.RFC3339, t.CreatedAt); perr == nil && createdAt.After(cutoff) {
			taskAgentID := t.AgentID
			title := t.Title
			ev := gen.ActivityEvent{
				Id:        "task-c-" + t.ID,
				Type:      "task_created",
				Timestamp: createdAt,
				Summary:   &title,
			}
			if taskAgentID != "" {
				ev.AgentId = &taskAgentID
			}
			events = append(events, ev)
		}
		if t.CompletedAt != "" {
			if completedAt, perr := time.Parse(time.RFC3339, t.CompletedAt); perr == nil && completedAt.After(cutoff) {
				taskAgentID := t.AgentID
				title := t.Title
				ev := gen.ActivityEvent{
					Id:        "task-u-" + t.ID,
					Type:      "task_updated",
					Timestamp: completedAt,
					Summary:   &title,
				}
				if taskAgentID != "" {
					ev.AgentId = &taskAgentID
				}
				events = append(events, ev)
			}
		}
	}

	// Sort reverse-chronological.
	slices.SortFunc(events, func(a, b gen.ActivityEvent) int {
		return b.Timestamp.Compare(a.Timestamp)
	})

	// Limit to 50 entries.
	if len(events) > 50 {
		events = events[:50]
	}

	// FINAL-REVIEW HIGH — a partial-failure session-listing warning was computed
	// above (sessionWarning) but previously only slog'd and then discarded: the
	// handler always returned the bare events array, so a caller had no way to
	// know the results were incomplete. gen.ActivityEventsResponse carries an
	// explicit Warning field for exactly this case (contracts/components/schemas/
	// ActivityEventsResponse.yaml) — return it instead of the bare array so the
	// caller can surface the partial-failure state.
	resp := gen.ActivityEventsResponse{
		Events: make([]struct {
			AgentId   *string                              `json:"agent_id,omitempty"`
			AgentName *string                              `json:"agent_name,omitempty"`
			Id        string                               `json:"id"`
			Summary   *string                              `json:"summary,omitempty"`
			Timestamp time.Time                            `json:"timestamp"`
			Type      gen.ActivityEventsResponseEventsType `json:"type"`
		}, len(events)),
	}
	for i, ev := range events {
		resp.Events[i].AgentId = ev.AgentId
		resp.Events[i].AgentName = ev.AgentName
		resp.Events[i].Id = ev.Id
		resp.Events[i].Summary = ev.Summary
		resp.Events[i].Timestamp = ev.Timestamp
		resp.Events[i].Type = gen.ActivityEventsResponseEventsType(ev.Type)
	}
	if sessionWarning != "" {
		slog.Warn("rest: activity: partial results due to session listing errors", "warning", sessionWarning)
		resp.Warning = &sessionWarning
	}
	jsonOK(w, resp)
}

// --- Providers ---

// providerSignInNotImplementedMsg is the 501 body for the ADR-068 sign-in
// routes until T068-16 implements them.
const providerSignInNotImplementedMsg = "provider sign-in not implemented — T068-16"

// HandleProviders handles GET/PUT/POST /api/v1/providers and sub-paths.
func (a *restAPI) HandleProviders(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/providers")
	sub = strings.TrimPrefix(sub, "/")

	switch {
	case r.Method == http.MethodGet && sub == "":
		// Return the CONFIGURED providers only (ADR-068 FR-011a, resolution
		// #16), enriched with upstream available models for OpenAI-compatible
		// providers. The seeded cfg.Providers templates (pkg/config/defaults.go
		// — model + api_base, no `provider` identity, no credential ref) are
		// NOT rows: a row the operator never created is not theirs to manage,
		// and the SPA used to have to filter the ~10 permanent "disconnected"
		// template rows out of every list. A row is configured when it
		// carries a `provider` identity — PUT /providers/{id} and onboarding
		// completion stamp it on every row they write (template or new), and
		// ADR-067 makes it the only provider identity.
		cfg := a.agentLoop.GetConfig()
		// providerUserModels holds the operator-supplied catalog slugs for
		// providers that have no live /models endpoint (UAT model-catalog fix).
		providerUserModels := make(map[string][]string)
		providerAPIKeys := make(map[string]string)
		// providerCredErrors holds an operator-facing message, keyed by provider
		// name, for the case where a.resolveCredentialRef failed for a reason
		// WORSE than "ref not found" (locked/undecryptable vault) — see
		// describeCredentialResolutionError. Without this, that case and "no key
		// configured at all" were both reported as plain status=disconnected,
		// indistinguishable to the operator viewing Settings/Providers.
		providerCredErrors := make(map[string]string)
		// providerUpdatedAt / providerAuthMethod carry the ADR-068 row fields
		// (T068-08): the latest PUT stamp across the provider's rows (MAJ-015,
		// the picker's Recent ordering key) and the row's auth method
		// (api_key unless a sign_in row exists — T068-14 wires the sign-in
		// status/account_label on top of this).
		providerUpdatedAt := make(map[string]*time.Time)
		providerAuthMethod := make(map[string]string)
		// providerFirstRow is the representative config row for each
		// provider — the first non-template one, which carries the
		// custom/protocol/api_base identity ADR-067 FR-020 and FR-039 read
		// to decide where this row's model list comes from.
		providerFirstRow := make(map[string]*config.ModelConfig)
		providerOrder := make([]string, 0)
		seen := make(map[string]struct{})
		for _, m := range cfg.Providers {
			if isSeedTemplateRow(m) {
				continue // never configured — not a row (FR-029)
			}
			providerName := m.Provider
			if _, exists := seen[providerName]; !exists {
				seen[providerName] = struct{}{}
				providerOrder = append(providerOrder, providerName)
				providerFirstRow[providerName] = m
			}
			if m.UpdatedAt != nil {
				if cur := providerUpdatedAt[providerName]; cur == nil || m.UpdatedAt.After(*cur) {
					providerUpdatedAt[providerName] = m.UpdatedAt
				}
			}
			if providerAuthMethod[providerName] == "" && m.AuthMethod != "" {
				providerAuthMethod[providerName] = m.AuthMethod
			}
			if len(m.Models) > 0 {
				providerUserModels[providerName] = append(providerUserModels[providerName], m.Models...)
			}
			// Resolve API key for upstream model fetching.
			// APIKeyRef is resolved via process environment (set by InjectFromConfig).
			if _, hasKey := providerAPIKeys[providerName]; !hasKey {
				resolved := m.APIKey()
				if resolved == "" && m.APIKeyRef != "" {
					if v, err := a.resolveCredentialRef(m.APIKeyRef); err != nil {
						slog.Warn("rest: could not resolve provider credential", "ref", m.APIKeyRef, "error", err)
						var notFound *credentials.NotFoundError
						if !errors.As(err, &notFound) {
							providerCredErrors[providerName] = describeCredentialResolutionError(err)
						}
					} else {
						resolved = v
					}
				}
				if resolved != "" {
					providerAPIKeys[providerName] = resolved
				}
			}
		}
		providers := make([]gen.Provider, 0, len(providerOrder))
		for _, name := range providerOrder {
			// ADR-067 FR-020 (T067-10): the model list's source is decided
			// by the row's locality, not by whether a vendor base URL is
			// hardcoded anywhere. A `locality = cloud` row lists the
			// CATALOG's models with no outbound call at all (US-9.AC1 —
			// the list is instant and works offline); only a
			// `locality = local` row is listed live, because nothing but
			// that machine knows what has been pulled onto it (US-9.AC3).
			src := a.resolveProviderRow(name, providerFirstRow[name])
			models, modelFetchWarning := a.providerModelList(
				r.Context(), name, src, providerAPIKeys[name], providerUserModels[name])
			// "Has a live /models endpoint" means "the gateway fills this
			// list, so the SPA must not present it as an editable slug
			// list". A custom endpoint never qualifies — its catalogue IS
			// the operator's slugs. APIBaseFor reads the process catalog,
			// which the gateway installs from the very document
			// resolveProviderRow read (providers.SetCatalog at boot), so
			// the two agree on every real installation.
			hasEndpoint := !src.custom && providers_pkg.APIBaseFor(name) != ""
			// FR-104: report Connected only when the provider's API key resolves to
			// a non-empty credential. providerAPIKeys is populated above for every
			// provider that has either a resolvable api_key_ref or an inline api_key;
			// absence from the map means no key was found.
			status := gen.ProviderStatusDisconnected
			if _, hasKey := providerAPIKeys[name]; hasKey {
				status = gen.ProviderStatusConnected
			}
			// ADR-068 FR-043 / ADR-067 FR-016: a configured row whose id the
			// served catalog does not contain is unknown-provider, with the
			// generic text parameterised by the operator's own id (the id is
			// user data, not a trace — CRIT-003) and an empty model list
			// (S67 Q4). Classified only when a catalog document is actually
			// loaded, and never for a custom row — an operator-named
			// endpoint is not in the catalog BY DESIGN (FR-035, X-13); an
			// absent catalog (E7) never turns every row unknown.
			var unknownProviderMsg string
			if src.unknownProvider() {
				status = gen.ProviderStatusUnknownProvider
				unknownProviderMsg = fmt.Sprintf("unknown provider %q", name)
				models = []string{}
			}
			// ADR-068 FR-009 (T068-15): a `github-copilot` row is backed by
			// the vendor CLI, not an API key, so the key-derived status above
			// says nothing useful about it. When the CLI is absent from this
			// machine the row stays `disconnected` and carries the operator
			// hint. Whether the operator is SIGNED IN is not computed here —
			// that check runs the CLI and costs a premium request, so it is
			// the explicit Check sign-in action only.
			copilotHint := copilotRowHint(name)
			hasEndpointCopy := hasEndpoint
			// ADR-068 T068-08: the row's auth method comes from the config row
			// (closed set api_key | sign_in — Validate rejects anything else);
			// api_key when unset. account_label stays absent until T068-14's
			// sign-in status computation lands (zero value per FR-024).
			authMethod := gen.ProviderAuthMethodApiKey
			if providerAuthMethod[name] == config.AuthMethodSignIn {
				authMethod = gen.ProviderAuthMethodSignIn
			}
			p := gen.Provider{
				Id:                name,
				Name:              name,
				Status:            status,
				Models:            models,
				HasModelsEndpoint: &hasEndpointCopy,
				// ADR-068 FR-012 (T068-08): dependents/backs_default are
				// advisory here; T068-09's DELETE recomputes them under
				// configMu and its response is authoritative (MAJ-018).
				AuthMethod:   authMethod,
				Dependents:   computeProviderDependents(cfg, name),
				BacksDefault: providerBacksDefault(cfg, name),
				UpdatedAt:    providerUpdatedAt[name],
				// ADR-067 identity fields (T067-10): the wire row now
				// carries what the catalog says about it, so the SPA never
				// has to re-derive protocol, locality or grouping.
				Protocol: providerWireProtocol(src.protocol),
				Locality: providerWireLocality(src.locality),
			}
			if src.custom {
				customCopy := true
				p.Custom = &customCopy
			}
			if src.known {
				if company := src.row.Company; company != "" {
					companyCopy := company
					p.Company = &companyCopy
				}
				if display := src.row.Name; display != "" {
					displayCopy := display
					p.DisplayName = &displayCopy
				}
				p.CliKind = providerWireCLIKind(src.row.CLIKind)
			}
			if modelFetchWarning != "" {
				p.Warning = &modelFetchWarning
			}
			switch {
			case unknownProviderMsg != "":
				// unknown-provider wins over the credential-derived states: a
				// key for a provider that does not exist is not "connected".
				p.Error = &unknownProviderMsg
			case status != gen.ProviderStatusConnected:
				// A credential-resolution failure worse than "not configured"
				// (locked/undecryptable vault) is reported as status=error with
				// the classified remediation message, instead of a plain
				// "disconnected" indistinguishable from a provider whose key was
				// never entered (Task 3 fix).
				if credErrMsg, ok := providerCredErrors[name]; ok {
					p.Status = gen.ProviderStatusError
					p.Error = &credErrMsg
				} else if copilotHint != "" {
					p.Error = &copilotHint
				}
			}
			providers = append(providers, p)
		}
		// No configured provider → `[]`, never a synthetic "default" filler
		// row (a fresh install has nothing to manage yet; the onboarding wizard
		// creates the first row).
		jsonOK(w, providers)

	case r.Method == http.MethodDelete && isReservedProviderPathSegment(sub):
		// DELETE on a reserved path segment ("catalog", "model-capabilities";
		// "default-model" normally dispatches to its own route first) — the
		// reserved literals are never provider ids, so there is nothing to
		// delete: 404, per the MAJ-002 scenario rows.
		jsonErr(w, http.StatusNotFound, "provider not found")

	case r.Method == http.MethodDelete && sub != "" && !strings.Contains(sub, "/"):
		// DELETE /api/v1/providers/{id} (T068-09, ADR-068 FR-010/FR-011,
		// rest_providers_delete.go). The shared dispatcher is registered
		// withOptionalAuth, so the verb carries its own authorization gate
		// inline (FR-042/MAJ-007): requireAdminAuthz (RequireNotBypass →
		// 503 under dev-mode bypass) here, and the unconditional 401 for an
		// unauthenticated caller inside deleteProvider — no pre-onboarding
		// exception.
		providerID := sub
		a.requireAdminAuthz(func(w http.ResponseWriter, r *http.Request) {
			a.deleteProvider(w, r, providerID)
		})(w, r)

	case r.Method == http.MethodPut && sub != "" && !strings.HasSuffix(sub, "/test"):
		// PUT /api/v1/providers/{id} — update or insert a provider entry.
		// Reserved path segments are never provider ids (MAJ-002): reject
		// BEFORE auth-gating or decoding so no request shape can upsert a
		// provider named "catalog" / "default-model" / "model-capabilities".
		if isReservedProviderPathSegment(sub) {
			jsonErrField(w, http.StatusBadRequest,
				fmt.Sprintf("unknown provider %q", sub), "id")
			return
		}
		// Allow unauthenticated access during onboarding so the wizard can
		// configure the provider before the admin user exists.
		onboardingDone := a.onboardingMgr != nil && a.onboardingMgr.IsComplete()
		if onboardingDone && r.Context().Value(UserContextKey{}) == nil {
			jsonErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		// Re-auth gate (Spec-6 FR-12.2 / FR-6.6): a model/provider API-key mutation
		// is a sensitive HTTP-layer settings change and requires the single-use
		// re-auth consent token — the same gate the Integrations PUT enforces.
		// Skipped only during onboarding (no authenticated user yet), where the
		// provider is configured before any password exists. When a user IS in
		// context (post-onboarding edits), the token is mandatory.
		if reauthUser, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && reauthUser != nil {
			if !a.requireReAuth(w, r, reauthUser.Username) {
				return
			}
		}
		providerID := sub
		var req gen.ProviderUpdateRequest
		validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
		if !decodeAndValidate(w, r, "ProviderUpdateRequest", &req, validateEnabled) {
			return
		}
		// Bounds enforcement (M-slug): cap the model list inline so the limits
		// hold even when schema validation is skipped (validate_inbound=false is
		// the default). Mirrors the inline name/description caps in
		// rest_workspaces.go. dedupeNonEmpty applies no maxItems / length cap.
		if req.Models != nil {
			const maxModels = 500
			const maxSlugLen = 256
			if len(*req.Models) > maxModels {
				jsonErr(w, http.StatusBadRequest, fmt.Sprintf("models exceeds %d entries", maxModels))
				return
			}
			for _, slug := range *req.Models {
				if len(slug) > maxSlugLen {
					jsonErr(w, http.StatusBadRequest, fmt.Sprintf("model slug exceeds %d characters", maxSlugLen))
					return
				}
			}
		}
		// ADR-067 FR-019 / FR-035 (T067-10): catalog admission. The id the
		// operator typed is either a catalog row (accepted unless the
		// catalog itself marks it unsupported, with the catalog's own
		// reason), or an operator-named CUSTOM endpoint — accepted only
		// when it carries both halves of what it takes to reach one, an
		// api_base and one of the two protocols a base URL fully
		// describes. Anything else is an unknown provider, and saying so
		// here is the difference between an obvious 400 and a row that
		// looks saved and never resolves a model.
		reqAPIBase := derefStr(req.ApiBase)
		reqProtocol := derefStr((*string)(req.Protocol))
		isCustomRow, admitErr := providerAdmission(a.providerCatalog, providerID, reqAPIBase, reqProtocol)
		if admitErr != nil {
			field := "id"
			if errors.Is(admitErr, providers_pkg.ErrUnknownProvider) && reqAPIBase != "" {
				// The id is unknown AND a base was supplied: what is
				// missing is the protocol, so point the SPA at that field.
				field = "protocol"
			}
			jsonErrField(w, http.StatusBadRequest, admitErr.Error(), field)
			return
		}

		// Check if the provider already exists.
		cfg := a.agentLoop.GetConfig()
		found := false
		for _, m := range cfg.Providers {
			if m.IsVirtual() {
				continue
			}
			if strings.TrimSpace(m.Provider) == providerID {
				found = true
				break
			}
		}
		if !found {
			// New provider — api_key is required.
			if req.ApiKey == nil || *req.ApiKey == "" {
				jsonErr(w, http.StatusUnprocessableEntity, "api_key is required")
				return
			}
			if req.Model == nil || *req.Model == "" {
				defaultModel := "default"
				req.Model = &defaultModel
			}
		}
		// R-D fixed order (spec FR-011 / R-D):
		// Step 2 — key-changed check (R-C): validate ONLY when api_key is present and non-empty.
		// Re-sending the same key value DOES re-probe (accepted cost). A PUT omitting
		// api_key (model/label-only edit) skips the probe entirely — no billable upstream call.
		// Step 3 — resolve persisted api_base + SSRF check (NOT a request field; ProviderUpdateRequest
		// has no api_base field). Step 4 — ValidateKey. Step 5 — InvalidKey → 422, persist nothing.
		// Note: the re-auth consent token (step 1) is consumed BEFORE reaching here (see requireReAuth
		// above). A 422 at step 5 therefore burns the single-use token — the SPA must re-auth on retry
		// of a corrected key (R-D/M4, accepted trade-off: validating before consent would probe without step-up).
		var putValidationResult providers_pkg.ValidationResult
		keyChanged := req.ApiKey != nil && *req.ApiKey != ""
		if keyChanged {
			// Store-readiness FIRST (SEC-23): if the credential store is locked we cannot
			// persist the key, so return 503 BEFORE the SSRF check + the billable
			// validation probe. (Otherwise a locked store would be reported as an invalid
			// key — the validation probe would run and 422 before we discovered we can't
			// store anything.)
			if err := a.credentialStoreReady(); err != nil {
				slog.Error("rest: credential store unavailable for provider update",
					"provider", providerID, "error", err)
				jsonErr(w, http.StatusServiceUnavailable,
					"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets")
				return
			}
			// Resolve the base URL to probe: the api_base this very
			// request supplies wins (a custom row has no other source),
			// then the persisted one, then the catalog's.
			persistedAPIBase := reqAPIBase
			if persistedAPIBase == "" {
				for _, m := range cfg.Providers {
					if m.IsVirtual() {
						continue
					}
					if strings.TrimSpace(m.Provider) == providerID {
						persistedAPIBase = m.APIBase
						break
					}
				}
			}
			if persistedAPIBase == "" {
				persistedAPIBase = providers_pkg.APIBaseFor(providerID)
			}
			// SSRF-check the persisted api_base before any outbound probe.
			if persistedAPIBase != "" && a.ssrfChecker != nil {
				if err := a.ssrfChecker.CheckURL(r.Context(), persistedAPIBase); err != nil {
					slog.Warn("rest: PUT provider: SSRF blocked persisted api_base",
						"provider", providerID, "api_base", persistedAPIBase, "error", err)
					jsonErr(w, http.StatusUnprocessableEntity, "provider endpoint not allowed (SSRF guard)")
					return
				}
			}
			// Run the centralized key validator. SEC-16: RawDetail is server-debug only.
			putValidationResult = providers_pkg.ValidateKey(r.Context(), providers_pkg.ValidateInput{
				ProviderID:   providerID,
				ProviderName: providers_pkg.DisplayName(providerID),
				BaseURL:      persistedAPIBase,
				APIKey:       *req.ApiKey,
			}, a.ssrfChk())
			slog.Debug("rest: PUT provider: key validation result",
				"provider", providerID, "outcome", putValidationResult.Outcome,
				"detail", putValidationResult.RawDetail)
			if putValidationResult.Blocks() {
				// InvalidKey — reject the save. The key is NOT stored. SEC-16: message is curated.
				jsonErr(w, http.StatusUnprocessableEntity, putValidationResult.Message)
				return
			}
		}

		// Store API key in the encrypted credentials store (AES-256-GCM) and
		// reference it via api_key_ref in config.json. Refuses the operation if
		// the credential store is locked (SEC-23: no plaintext fallback).
		var credRefName string
		if keyChanged {
			ref, err := a.storeCredential(providerID+"_API_KEY", *req.ApiKey)
			if err != nil {
				slog.Error(
					"rest: credential store unavailable for provider update",
					"provider",
					providerID,
					"error",
					err,
				)
				jsonErr(
					w,
					http.StatusServiceUnavailable,
					"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets",
				)
				return
			}
			credRefName = ref
		}
		// Normalise the user-supplied catalog slugs (UAT model-catalog fix) into
		// a deduplicated []any for JSON persistence. nil req.Models leaves the
		// stored list unchanged; a non-nil (incl. empty) list replaces it.
		var userModelsJSON []any
		if req.Models != nil {
			for _, slug := range dedupeNonEmpty(*req.Models) {
				userModelsJSON = append(userModelsJSON, slug)
			}
			if userModelsJSON == nil {
				userModelsJSON = []any{} // explicit clear
			}
		}
		// ADR-068 MAJ-015: every PUT stamps the row's updated_at — the
		// picker's Recent ordering key (Provider.updated_at).
		putStamp := time.Now().UTC()
		putStampStr := putStamp.Format(time.RFC3339)
		if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
			providerList, _ := m["providers"].([]any)
			updated := false
			for _, entry := range providerList {
				model, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				if strings.TrimSpace(strVal(model, "provider")) == providerID {
					model["updated_at"] = putStampStr
					if req.ApiKey != nil && *req.ApiKey != "" {
						model["api_key_ref"] = credRefName
						delete(model, "api_key")
						delete(model, "api_keys")
					}
					if req.Model != nil && *req.Model != "" {
						model["model"] = *req.Model
					}
					if req.Models != nil {
						if len(userModelsJSON) > 0 {
							model["models"] = userModelsJSON
						} else {
							delete(model, "models")
						}
					}
					model["provider"] = providerID
					applyProviderIdentity(model, reqAPIBase, reqProtocol, isCustomRow)
					updated = true
					break
				}
			}
			if !updated {
				// Provider not found — add a new entry.
				modelVal := ""
				if req.Model != nil {
					modelVal = *req.Model
				}
				newEntry := map[string]any{
					"provider":    providerID,
					"model":       modelVal,
					"api_key_ref": credRefName,
					"updated_at":  putStampStr,
				}
				if len(userModelsJSON) > 0 {
					newEntry["models"] = userModelsJSON
				}
				applyProviderIdentity(newEntry, reqAPIBase, reqProtocol, isCustomRow)
				m["providers"] = append(providerList, newEntry)
			}
			return nil
		}); err != nil {
			slog.Error("rest: save config for provider update", "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
			return
		}
		// Trigger reload AND WAIT for it (triggerReloadAndWaitOutcome, not a bare
		// TriggerReload — mirrors createAgent/updateAgent/deleteAgent/
		// updateAgentTools): a bare TriggerReload only enqueues the reload and
		// returns before the registry actually swaps. Per updateAgent's
		// model-apply doc comment a few hundred lines up, persisting +
		// SwapConfig alone does NOT touch an already-constructed agent
		// instance's cached provider/model client — only the async
		// TriggerReload → executeReload → ReloadProviderAndConfig →
		// NewAgentRegistry rebuild does. Without waiting, a client that fixes a
		// revoked/invalid API key here and immediately sends a chat message
		// could still be served by the stale cached client (and the old,
		// possibly-compromised key) for as long as that goroutine takes to
		// run. triggerReloadAndWaitOutcome absorbs ErrReloadNotConfigured (unit tests
		// / minimal embeddings without the full reload pipeline wired)
		// internally as a no-op, so a non-nil error here is always a genuine
		// reload failure — preserving the existing 500 semantics below (the
		// key IS persisted; only the live application failed). The confirmed
		// bool additionally distinguishes a genuine timeout (agents may still
		// be served by the stale cached provider client) from a hard failure.
		if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
			slog.Error("config reload after provider update failed", "error", err)
			jsonErr(
				w,
				http.StatusInternalServerError,
				fmt.Sprintf("provider updated but config reload failed: %v", err),
			)
			return
		} else if !confirmed {
			slog.Warn("rest: reload after provider update did not confirm within the poll window; "+
				"agents may still be served by the stale cached provider client", "provider_id", providerID)
		}
		// The saved row is a catalog row unless admission classified it as
		// an operator-named custom endpoint (FR-035); a catalog row's list
		// is filled by the gateway, a custom row's is the operator's own.
		hasEndpoint := !isCustomRow && providers_pkg.IsCatalogProvider(providerID)
		respModels := []string{}
		if req.Models != nil {
			respModels = dedupeNonEmpty(*req.Models)
			if respModels == nil {
				respModels = []string{}
			}
		}
		providerResp := gen.Provider{
			Id:                providerID,
			Name:              providerID,
			Status:            gen.ProviderStatusConnected,
			Models:            respModels,
			HasModelsEndpoint: &hasEndpoint,
			AuthMethod:        gen.ProviderAuthMethodApiKey,
			Dependents:        []gen.ProviderDependent{},
			BacksDefault:      providerBacksDefault(a.agentLoop.GetConfig(), providerID),
			UpdatedAt:         &putStamp,
		}
		if isCustomRow {
			customCopy := true
			providerResp.Custom = &customCopy
		}
		if p := providerWireProtocol(catalog.Protocol(reqProtocol)); p != nil {
			providerResp.Protocol = p
		}
		// R-D step 7 / FR-011: attach validation for warning outcomes (NoCredit/Unreachable/Restricted).
		// Valid outcome and key-absent PUTs carry no validation field.
		if keyChanged && putValidationResult.Outcome != providers_pkg.OutcomeValid {
			outcomeStr := gen.ProviderValidationOutcome(putValidationResult.Outcome)
			// Guard: only assign the validation object when the cast is a known wire value.
			// An off-contract Outcome (e.g. future 6th value, wrong case) must not silently
			// produce an invalid enum value on the wire.
			if !outcomeStr.Valid() {
				slog.Warn("rest: PUT provider: unrecognized validation outcome; omitting validation field",
					"provider", providerID, "outcome", putValidationResult.Outcome)
			} else {
				msg := putValidationResult.Message
				providerResp.Validation = &struct {
					Message *string                       `json:"message,omitempty"`
					Outcome gen.ProviderValidationOutcome `json:"outcome"`
				}{
					Outcome: outcomeStr,
					Message: &msg,
				}
			}
			// R-F / FR-017: audit the warning-proceed. Best-effort — log write failures.
			// Only persisting flows are audited (not the informational probe/Test), per O2.
			if a.auditor != nil {
				if err := a.auditor.Log(&audit.Entry{
					Event:    "provider_key_validated",
					Decision: audit.DecisionAllow,
					Details: map[string]any{
						"provider": providerID,
						"outcome":  string(putValidationResult.Outcome),
						"action":   "proceeded",
					},
				}); err != nil {
					slog.Warn("audit write failed", "event", "provider_key_validated", "error", err)
				}
			}
		}
		jsonOK(w, providerResp)

	case r.Method == http.MethodPost && strings.HasSuffix(sub, "/sign-in"),
		r.Method == http.MethodGet && strings.HasSuffix(sub, "/sign-in/status"):
		// POST /api/v1/providers/{id}/sign-in and GET …/sign-in/status
		// (ADR-068 FR-008/FR-009, contract landed by T068-06). The vendor
		// sign-in flow itself is T068-16; until it lands these are honest
		// stubs behind the contract's adminWrap posture (401 unauthenticated,
		// 503 under dev-mode bypass) that answer 501 with a message naming
		// the owning task, never a silent 200 or a blank 404.
		if r.Context().Value(UserContextKey{}) == nil {
			jsonErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		a.requireAdminAuthz(func(w http.ResponseWriter, r *http.Request) {
			// ADR-068 FR-008/FR-009 for `github-copilot` (T068-15). T068-14
			// generalises these two routes to every sign_in row (its own
			// rest_sign_in.go) and absorbs this branch; until then the Copilot
			// half is real and every other id keeps the honest stub.
			switch {
			case r.Method == http.MethodPost && sub == copilotProviderID+"/sign-in":
				a.handleCopilotSignInStart(w, r)
			case r.Method == http.MethodGet && sub == copilotProviderID+"/sign-in/status":
				a.handleCopilotSignInStatus(w, r)
			default:
				jsonErr(w, http.StatusNotImplemented, providerSignInNotImplementedMsg)
			}
		})(w, r)

	case r.Method == http.MethodPost && strings.HasSuffix(sub, "/test"):
		// POST /api/v1/providers/{id}/test — verify the provider has a valid API key.
		// Allow unauthenticated access during onboarding (same reason as PUT above).
		onboardingDone := a.onboardingMgr != nil && a.onboardingMgr.IsComplete()
		if onboardingDone && r.Context().Value(UserContextKey{}) == nil {
			jsonErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		// Read from disk directly to avoid stale in-memory config after async reload.
		providerID := strings.TrimSuffix(sub, "/test")
		cfgData, err := os.ReadFile(a.configPath())
		if err != nil {
			errMsg := "could not read config"
			jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
			return
		}
		var cfgRaw map[string]any
		if err := json.Unmarshal(cfgData, &cfgRaw); err != nil {
			errMsg := "could not parse config"
			jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
			return
		}
		providerList, _ := cfgRaw["providers"].([]any)
		found := false
		var resolvedAPIKey string
		var firstModel string
		var configuredAPIBase string
		// credResolveErr captures a resolveCredentialRef failure when the provider
		// entry references an api_key_ref that the credential vault could NOT
		// resolve. This is distinct from "no key configured" and must surface a
		// different, actionable message that depends on WHY resolution failed —
		// see describeCredentialResolutionError (fix #5, hardened to distinguish
		// "ref not found" from "vault locked/undecryptable").
		var credResolveErr error
		for _, entry := range providerList {
			modelMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(strVal(modelMap, "provider")) == providerID {
				found = true
				// Capture the provider entry's configured api_base (config.go
				// `json:"api_base"`). Preferred over the vendor default so a
				// regional host / self-hosted gateway is probed at the SAME base
				// the runtime factory will actually use (fix #3).
				configuredAPIBase = strings.TrimSpace(strVal(modelMap, "api_base"))
				// Check if API key is set: either via api_keys array or api_key_ref
				// pointing to the encrypted credentials store.
				apiKeys, _ := modelMap["api_keys"].([]any)
				apiKeyRef, _ := modelMap["api_key_ref"].(string)
				if len(apiKeys) > 0 {
					if k, _ := apiKeys[0].(string); k != "" {
						resolvedAPIKey = k
					}
				}
				if resolvedAPIKey == "" && apiKeyRef != "" {
					if v, err := a.resolveCredentialRef(apiKeyRef); err != nil {
						// A ref is present but could not be resolved — do NOT fall
						// through to "no API key configured" (misleading). The exact
						// remediation depends on WHY it failed; see
						// describeCredentialResolutionError below.
						slog.Warn("rest: provider test: credential store error", "ref", apiKeyRef, "error", err)
						credResolveErr = err
					} else {
						resolvedAPIKey = v
					}
				}
				if resolvedAPIKey == "" {
					if credResolveErr != nil {
						errMsg := describeCredentialResolutionError(credResolveErr)
						jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
						return
					}
					errMsg := "no API key configured for this provider"
					jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
					return
				}
				// Presence gate only — firstModel is passed to pickProbeModel inside
				// ValidateKey, which selects the actual probe model from the catalog.
				// A non-empty firstModel here satisfies the "has a model configured"
				// check below; the probe model may differ.
				if configuredModels, _ := modelMap["models"].([]any); len(configuredModels) > 0 {
					firstModel, _ = configuredModels[0].(string)
				}
				if firstModel == "" {
					firstModel = strVal(modelMap, "model")
				}
				break
			}
		}
		if !found {
			errMsg := fmt.Sprintf("provider %q not configured", providerID)
			jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
			return
		}
		// Resolve the base URL to probe: prefer the entry's configured api_base
		// (regional host / self-hosted gateway), fall back to the vendor default
		// (fix #3).
		baseURL := configuredAPIBase
		if baseURL == "" {
			baseURL = providers_pkg.APIBaseFor(providerID)
		}
		if baseURL == "" {
			// Neither a configured api_base nor a known vendor default — the probe
			// genuinely cannot run. Report success WITH a note rather than a silent
			// pass, so the caller knows the key was not actually verified (fix #3).
			slog.Warn("rest: provider test: no api_base or vendor default; key NOT verified",
				"provider", providerID)
			note := "API key not verified: no endpoint is configured for this provider " +
				"(set api_base) and it has no known default."
			jsonOK(w, gen.OperationResult{Success: true, Error: &note})
			return
		}
		// SEC-24: block a probe against an internal/loopback/metadata address before
		// any outbound call (fix #1). The configured api_base is caller-influenced
		// (set via PUT /providers), so it must be SSRF-checked just like the
		// onboarding endpoint override.
		if a.ssrfChecker != nil {
			if err := a.ssrfChecker.CheckURL(r.Context(), baseURL); err != nil {
				slog.Warn("rest: provider test: SSRF blocked api_base", "provider", providerID, "error", err)
				errMsg := "endpoint not allowed"
				jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
				return
			}
		}
		if firstModel == "" {
			// No model to probe with — the auth call cannot run. Make the skip
			// observable instead of silently returning success (fix #4).
			slog.Warn("rest: provider test: provider has no model to probe; API key not verified",
				"provider", providerID)
			jsonOK(w, gen.OperationResult{Success: true})
			return
		}
		// Auth-validation step: use the centralized providers.ValidateKey to probe
		// the key with a minimal chat-completion call. This is an informational test
		// — it does NOT persist anything. The classified outcome is returned in the
		// validation field so the SPA can surface the right icon/message per outcome.
		// SEC-16: result.RawDetail is server-debug-only; never sent to the client.
		result := providers_pkg.ValidateKey(r.Context(), providers_pkg.ValidateInput{
			ProviderID:   providerID,
			ProviderName: providers_pkg.DisplayName(providerID),
			BaseURL:      baseURL,
			APIKey:       resolvedAPIKey,
		}, a.ssrfChk())
		slog.Debug("rest: provider test: classification complete",
			"provider", providerID, "outcome", result.Outcome, "detail", result.RawDetail)
		success := !result.Blocks()
		resp := gen.OperationResult{Success: success}
		if result.Outcome != providers_pkg.OutcomeValid {
			outcomeStr := gen.OperationResultValidationOutcome(result.Outcome)
			// Guard: only assign the validation object when the cast is a known wire value.
			if !outcomeStr.Valid() {
				slog.Warn("rest: provider test: unrecognized validation outcome; omitting validation field",
					"provider", providerID, "outcome", result.Outcome)
			} else {
				msg := result.Message
				resp.Validation = &struct {
					Message *string                              `json:"message,omitempty"`
					Outcome gen.OperationResultValidationOutcome `json:"outcome"`
				}{
					Outcome: outcomeStr,
					Message: &msg,
				}
			}
		}
		if result.Blocks() {
			resp.Error = &result.Message
		}
		jsonOK(w, resp)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// dedupeNonEmpty trims, drops empties, and de-duplicates a slice of strings,
// preserving first-seen order. Returns nil when the result is empty so callers
// can distinguish "no entries" from "an explicit empty list" at the call site.
func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// --- MCP Servers ---

// HandleMCPServers handles GET/POST /api/v1/mcp-servers and DELETE /api/v1/mcp-servers/{id}.
// GET returns McpServer[] shaped from cfg.Tools.MCP.Servers (contracts/components/schemas/McpServer.yaml).
// POST accepts McpServerCreate (contracts/components/schemas/McpServerCreate.yaml) and
// returns the newly created McpServer entry.
func (a *restAPI) HandleMCPServers(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/mcp-servers")
	sub = strings.TrimPrefix(sub, "/")

	// Determine if path is /{id}/tools.
	parts := strings.SplitN(sub, "/", 2)
	serverID := parts[0]
	subSuffix := ""
	if len(parts) == 2 {
		subSuffix = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && sub == "":
		a.listMCPServers(w, r)

	case r.Method == http.MethodPost && sub == "":
		a.addMCPServer(w, r)

	case r.Method == http.MethodDelete && sub != "" && subSuffix == "":
		a.deleteMCPServer(w, r, serverID)

	case r.Method == http.MethodGet && serverID != "" && subSuffix == "tools":
		a.listMCPServerTools(w, serverID)

	case r.Method == http.MethodPost && serverID != "" && subSuffix == "test":
		a.testMCPServer(w, r, serverID)

	case r.Method == http.MethodPatch && serverID != "" && subSuffix == "":
		a.patchMCPServer(w, r, serverID)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// errMCPNotFound is the sentinel a config-mutating closure returns when the
// requested MCP server id is absent, letting callers map it to 404 via errors.Is
// while leaving real config I/O / parse failures to surface as 500.
var errMCPNotFound = errors.New("mcp server not found")

// mcpLiveStatus maps AgentLoop.MCPServerStatus's live status string
// ("connected"|"error"|"disconnected") to the generated McpServerStatus enum,
// alongside the live tool_count (entries this server has in the central MCP
// registry; 0 when unset or not connected). addMCPServer, patchMCPServer, and
// listMCPServers all derive status/tool_count through this single path so the
// three endpoints never disagree on what "connected" means.
func (a *restAPI) mcpLiveStatus(name string) (gen.McpServerStatus, int) {
	status, toolCount, _ := a.agentLoop.MCPServerStatus(name)
	switch status {
	case "connected":
		return gen.McpServerStatusConnected, toolCount
	case "error":
		return gen.McpServerStatusError, toolCount
	default:
		return gen.McpServerStatusDisconnected, toolCount
	}
}

// listMCPServers reads configured MCP servers from config and returns them as
// McpServer[] (contracts/components/schemas/McpServer.yaml). G6: status comes from
// mcpLiveStatus (AgentLoop.MCPServerStatus), the SAME live-reconciliation state
// addMCPServer/patchMCPServer/deleteMCPServer read back after a config write — a
// server actually connected via the live manager reports "connected", a server
// that failed to connect reports "error", and anything else (disabled,
// reconciliation never ran) reports "disconnected". tool_count is sourced from
// a.mcpRegistry directly (matching GET /mcp-servers/{id}/tools) rather than
// mcpLiveStatus's own tool_count — see the inline comment below. tools (sorted
// tool names) is populated from the same a.mcpRegistry pass, and omitted when
// the server has no registered tools.
func (a *restAPI) listMCPServers(w http.ResponseWriter, _ *http.Request) {
	// MCPServersSnapshot (not GetConfig().Tools.MCP.Servers) — ranging the live
	// map directly races the sysagent config-mutation path, which mutates it
	// in place while holding the agent loop's write lock.
	servers := a.agentLoop.MCPServersSnapshot()
	result := make([]gen.McpServer, 0, len(servers))
	for name, srv := range servers {
		transport := gen.McpServerTransportStdio
		switch srv.Type {
		case "sse":
			transport = gen.McpServerTransportSse
		case "http":
			transport = gen.McpServerTransportHttp
		}
		enabled := srv.Enabled

		// Status comes from the same live-reconciliation state add/patch/delete
		// read back (mcpLiveStatus), but tool_count is sourced from a.mcpRegistry
		// directly — as before this fix — rather than mcpLiveStatus's own
		// tool_count (which reads the AgentLoop's central registry). In production
		// these are the SAME registry instance (gateway.go wires both to
		// centralMCPReg), but keeping this path independent matches the existing
		// GET /mcp-servers/{id}/tools contract, which also reads a.mcpRegistry.
		status, _ := a.mcpLiveStatus(name)
		toolCount := 0
		var toolNames []string
		if a.mcpRegistry != nil {
			for _, e := range a.mcpRegistry.Describe() {
				if e.ServerID == name {
					toolCount++
					toolNames = append(toolNames, e.Name)
				}
			}
		}

		entry := gen.McpServer{
			Id:        name,
			Name:      name,
			Transport: transport,
			Status:    status,
			ToolCount: toolCount,
			Enabled:   &enabled,
		}
		if len(toolNames) > 0 {
			sort.Strings(toolNames)
			entry.Tools = &toolNames
		}
		// Non-secret config fields for edit pre-fill (#437).
		if srv.Command != "" {
			c := srv.Command
			entry.Command = &c
		}
		if srv.URL != "" {
			u := srv.URL
			entry.Url = &u
		}
		if len(srv.Args) > 0 {
			a := append([]string(nil), srv.Args...)
			entry.Args = &a
		}
		if srv.EnvFile != "" {
			ef := srv.EnvFile
			entry.EnvFile = &ef
		}
		// env/headers: return KEYS ONLY — values may be secrets (Authorization, API keys).
		if len(srv.Env) > 0 {
			keys := make([]string, 0, len(srv.Env))
			for k := range srv.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			entry.EnvKeys = &keys
		}
		if len(srv.Headers) > 0 {
			names := make([]string, 0, len(srv.Headers))
			for k := range srv.Headers {
				names = append(names, k)
			}
			sort.Strings(names)
			entry.HeaderNames = &names
		}
		result = append(result, entry)
	}
	// Sort for deterministic response order.
	sort.Slice(result, func(i, j int) bool { return result[i].Id < result[j].Id })
	jsonOK(w, result)
}

// listMCPServerTools handles GET /api/v1/mcp-servers/{id}/tools.
// Returns the tool names registered by the given MCP server via McpServerToolsResponse.
// If the server has no registered tools (e.g. not yet connected), returns an empty list.
// 404 if serverID is not present in the config.
func (a *restAPI) listMCPServerTools(w http.ResponseWriter, serverID string) {
	// MCPServersSnapshot (not GetConfig().Tools.MCP.Servers) — indexing the
	// live map directly races the sysagent config-mutation path, which
	// mutates it in place while holding the agent loop's write lock.
	servers := a.agentLoop.MCPServersSnapshot()
	if _, exists := servers[serverID]; !exists {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("mcp server %q not found", serverID))
		return
	}
	toolNames := make([]string, 0)
	if a.mcpRegistry != nil {
		for _, entry := range a.mcpRegistry.Describe() {
			if entry.ServerID == serverID {
				toolNames = append(toolNames, entry.Name)
			}
		}
	}
	sort.Strings(toolNames)
	jsonOK(w, gen.McpServerToolsResponse{Tools: toolNames})
}

// addMCPServer handles POST /api/v1/mcp-servers.
// Accepts McpServerCreate (contracts/components/schemas/McpServerCreate.yaml).
// Transport must be one of: stdio, sse, http (enforced by enum validation).
// After the config write, live-reconciles the MCP manager (AgentLoop.ReconcileMCP)
// so the server actually connects and its tools register before the response is
// built — status/tool_count reflect the real outcome, not a hardcoded placeholder.
// Returns the new McpServer entry shaped per contracts/components/schemas/McpServer.yaml.
func (a *restAPI) addMCPServer(w http.ResponseWriter, r *http.Request) {
	var req gen.McpServerCreate
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "McpServerCreate", &req, validateEnabled) {
		return
	}
	if req.Name == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if err := validateEntityID(req.Name); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid server name")
		return
	}
	transport := string(req.Transport)
	if transport == "" {
		transport = "stdio"
	}
	// Validate transport against the contract enum (stdio | sse | http).
	// The "websocket" value is not supported — reject it early rather than storing
	// a config entry that the MCP manager will refuse at connection time.
	switch transport {
	case "stdio", "sse", "http":
		// valid
	default:
		jsonErr(
			w,
			http.StatusBadRequest,
			fmt.Sprintf("invalid transport %q: must be one of stdio, sse, http", transport),
		)
		return
	}
	// Per-transport field validation: stdio requires command; sse/http require url.
	// The MCP manager (pkg/mcp/manager.go ConnectServer) hard-fails on missing
	// cfg.URL for sse/http and missing cfg.Command for stdio — catch it here so the
	// error surfaces as a 422 rather than a silent connection failure.
	switch transport {
	case "stdio":
		if req.Command == nil || *req.Command == "" {
			jsonErr(w, http.StatusUnprocessableEntity, "command is required for stdio transport")
			return
		}
	case "sse", "http":
		if req.Url == nil || *req.Url == "" {
			jsonErr(w, http.StatusUnprocessableEntity, "url is required for sse/http transport")
			return
		}
		// Mirror SPA isValidUrlScheme: https always accepted; http only for
		// loopback (localhost, 127.x.x.x, ::1). Any other http:// URL is
		// rejected so the SPA validation cannot be bypassed via direct API call.
		if !mcpURLSchemeValid(*req.Url) {
			jsonErr(w, http.StatusUnprocessableEntity,
				"url must use https, or http for loopback addresses only (localhost, 127.x.x.x, ::1)")
			return
		}
	}
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		tools, _ := m["tools"].(map[string]any)
		if tools == nil {
			tools = map[string]any{}
			m["tools"] = tools
		}
		mcp, _ := tools["mcp"].(map[string]any)
		if mcp == nil {
			mcp = map[string]any{}
			tools["mcp"] = mcp
		}
		servers, _ := mcp["servers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			mcp["servers"] = servers
		}
		if _, exists := servers[req.Name]; exists {
			return fmt.Errorf("mcp server %q already exists", req.Name)
		}
		// Adding a server is explicit operator intent to use MCP — flip the global
		// kill-switch on in the same write so ReconcileMCP's desired set isn't
		// forced empty by a still-false tools.mcp.enabled (default on fresh
		// installs). PATCH/delete deliberately do NOT touch this flag: turning
		// MCP off globally is a separate, explicit action.
		mcp["enabled"] = true
		entry := map[string]any{
			"enabled": true,
			"type":    transport,
		}
		// Write the correct config field for each transport so the MCP manager
		// (pkg/mcp/manager.go ConnectServer) can connect: stdio uses cfg.Command,
		// sse/http use cfg.URL.
		switch transport {
		case "stdio":
			entry["command"] = *req.Command
		case "sse", "http":
			entry["url"] = *req.Url
		}
		if req.Args != nil && len(*req.Args) > 0 {
			entry["args"] = *req.Args
		}
		if req.Env != nil && len(*req.Env) > 0 {
			entry["env"] = *req.Env
		}
		if req.EnvFile != nil && *req.EnvFile != "" {
			entry["env_file"] = *req.EnvFile
		}
		if req.Headers != nil && len(*req.Headers) > 0 {
			entry["headers"] = *req.Headers
		}
		servers[req.Name] = entry
		return nil
	}); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		slog.Error("rest: add mcp server", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// Config write succeeded — reconcile the live manager so the server actually
	// connects instead of sitting "disconnected" until a full gateway
	// restart. Best-effort: a reconcile failure/timeout does not undo the config
	// write or fail the request — the operator can retry via PATCH/reload, and the
	// response status below honestly reflects whatever the live state ended up as.
	rctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	if err := a.agentLoop.ReconcileMCP(rctx); err != nil {
		slog.Warn("rest: add mcp server: live reconcile failed", "server", req.Name, "error", err)
	}
	cancel()
	// Map the transport string to the generated enum value for the response.
	var respTransport gen.McpServerTransport
	switch transport {
	case "sse":
		respTransport = gen.McpServerTransportSse
	case "http":
		respTransport = gen.McpServerTransportHttp
	default:
		respTransport = gen.McpServerTransportStdio
	}
	status, toolCount := a.mcpLiveStatus(req.Name)
	resp := gen.McpServer{
		Id:        req.Name,
		Name:      req.Name,
		Transport: respTransport,
		Status:    status,
		ToolCount: toolCount,
	}
	jsonCreated(w, resp)
}

// mcpURLSchemeValid reports whether rawURL is acceptable for an sse/http MCP
// server endpoint. It mirrors the SPA's isValidUrlScheme function
// (src/components/skills/McpServerModal.tsx) so the contract described in
// McpServerCreate.yaml is enforced server-side and cannot be bypassed via
// direct API calls.
//
// Rules:
//   - https:// is always accepted.
//   - http:// is accepted only for loopback hosts: "localhost", any 127.x.x.x
//     address, or "::1" / "[::1]".
//   - Any other scheme (http:// to a public host, ws://, ftp://, etc.) is rejected.
func mcpURLSchemeValid(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return true
	case "http":
		host := strings.ToLower(parsed.Hostname())
		return host == "localhost" ||
			strings.HasPrefix(host, "127.") ||
			host == "::1" ||
			host == "[::1]"
	default:
		return false
	}
}

// deleteMCPServer handles DELETE /api/v1/mcp-servers/{id}. Removes the server
// from config, then live-reconciles the MCP manager (AgentLoop.ReconcileMCP) so
// a connected server is actually disconnected and its tools evicted from the
// central/per-agent registries, rather than lingering live until restart.
func (a *restAPI) deleteMCPServer(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	found := false
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		tools, _ := m["tools"].(map[string]any)
		if tools == nil {
			return nil
		}
		mcp, _ := tools["mcp"].(map[string]any)
		if mcp == nil {
			return nil
		}
		servers, _ := mcp["servers"].(map[string]any)
		if servers == nil {
			return nil
		}
		if _, exists := servers[id]; exists {
			delete(servers, id)
			found = true
		}
		return nil
	}); err != nil {
		slog.Error("rest: delete mcp server", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	if !found {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("mcp server %q not found", id))
		return
	}
	// Config write succeeded — reconcile the live manager so the removed server is
	// actually disconnected (DisconnectServer) and its tools evicted from the
	// central/per-agent registries, rather than lingering connected until restart.
	rctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	if err := a.agentLoop.ReconcileMCP(rctx); err != nil {
		slog.Warn("rest: delete mcp server: live reconcile failed", "server", id, "error", err)
	}
	cancel()
	jsonOK(w, map[string]string{"status": "removed", "id": id})
}

// testMCPServer handles POST /api/v1/mcp-servers/{id}/test (G7).
// Opens a temporary MCP manager, attempts to connect to the configured server,
// and reports the result, then closes the temporary manager. The test
// connection itself changes no persistent state beyond the heal described
// below. Returns McpServerTestResponse: success=true on live connection,
// success=false (HTTP 200) when the server is unreachable or misconfigured
// (including a relative env_file the gateway cannot resolve).
//
// Heal on success: a successful test proves the server is reachable, so if it
// is enabled in config, the global tools.mcp.enabled kill-switch is ALSO on,
// and it is not yet connected in the live manager (e.g. it failed to connect
// at boot, or was added before the kill-switch was flipped on), this triggers
// a real AgentLoop.ReconcileMCP pass to bring the live state in line with what
// the test just proved works — a manual "Test" click on a stuck server
// doubles as an unstick, instead of leaving the operator to separately toggle
// enabled off/on to force a reconnect. When the global flag is off, no
// reconcile can bring the server live (ReconcileMCP's desired set is forced
// empty), so the success message says so instead of silently doing nothing.
// This is the ONLY state change the endpoint makes, and only follows from
// success; a failed test never triggers reconciliation.
func (a *restAPI) testMCPServer(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	// MCPServersSnapshot (not GetConfig().Tools.MCP.Servers) — ranging/indexing
	// the live map directly races the sysagent config-mutation path, which
	// mutates it in place while holding the agent loop's write lock.
	servers := a.agentLoop.MCPServersSnapshot()
	srv, exists := servers[id]
	if !exists {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("mcp server %q not found", id))
		return
	}

	// Resolve a relative env_file against the same workspace path production
	// reconciliation uses, so the throwaway test connection sees the same
	// environment a real connect would — otherwise "Test" could report success
	// for a server that fails to actually connect once reconciled (or vice
	// versa). A resolution error is a test failure, not a 500: it is exactly
	// the misconfiguration the test button exists to surface.
	resolvedSrv, err := mcp.ResolveServerEnvFile(srv, a.agentLoop.MCPWorkspacePath())
	if err != nil {
		jsonOK(w, gen.McpServerTestResponse{
			Success: false,
			Message: fmt.Sprintf("env_file: %s", err.Error()),
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpMgr := mcp.NewManager()
	defer func() {
		if err := tmpMgr.Close(); err != nil {
			slog.Warn("rest: test mcp server: close temp manager", "id", id, "error", err)
		}
	}()

	if err := tmpMgr.ConnectServer(ctx, id, resolvedSrv); err != nil {
		resp := gen.McpServerTestResponse{
			Success: false,
			Message: fmt.Sprintf("connection failed: %s", err.Error()),
		}
		jsonOK(w, resp)
		return
	}

	conn, ok := tmpMgr.GetServer(id)
	if !ok {
		resp := gen.McpServerTestResponse{
			Success: false,
			Message: "connected but server entry missing",
		}
		jsonOK(w, resp)
		return
	}

	toolCount := len(conn.Tools)
	toolNames := make([]string, toolCount)
	for i, t := range conn.Tools {
		toolNames[i] = t.Name
	}
	sort.Strings(toolNames)

	msg := fmt.Sprintf("connected successfully; %d tool(s) available", toolCount)

	// Heal: the throwaway connection just proved the server is reachable. If
	// config still has it enabled, the global kill-switch is on, and the LIVE
	// manager doesn't have it up (status != "connected"), run a real reconcile
	// so this success is not wasted — the operator's next GET reflects a
	// genuinely connected server instead of "disconnected"/"error" despite the
	// test that just passed. When the global flag is off, ReconcileMCP's
	// desired set would be empty regardless of this server's own Enabled bit,
	// so a reconcile here would be a silent no-op — skip it and say so instead.
	if srv.Enabled {
		if !a.agentLoop.GetConfig().Tools.MCP.Enabled {
			msg += " (MCP is globally disabled — enable tools.mcp.enabled to connect)"
		} else if status, _, _ := a.agentLoop.MCPServerStatus(id); status != "connected" {
			rctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			if err := a.agentLoop.ReconcileMCP(rctx); err != nil {
				slog.Warn("rest: test mcp server: heal reconcile failed", "id", id, "error", err)
			}
			cancel()
		}
	}

	resp := gen.McpServerTestResponse{
		Success:   true,
		Message:   msg,
		ToolCount: &toolCount,
		Tools:     &toolNames,
	}
	jsonOK(w, resp)
}

// patchMCPServer handles PATCH /api/v1/mcp-servers/{id} (G8).
// Merges the provided (non-nil) fields from McpServerUpdate into the stored config
// entry; omitted fields are preserved. Validates transport-specific constraints
// when URL is changed. An explicit {enabled: true} also flips the global
// tools.mcp.enabled kill-switch on in the same write (mirrors addMCPServer;
// {enabled: false} and any other field never touch the global flag). After the
// config write, live-reconciles the MCP manager (AgentLoop.ReconcileMCP) so the
// live connection is reconnected with the new config (or disconnected, if the
// patch disabled it) before the response is built.
func (a *restAPI) patchMCPServer(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	var req gen.McpServerUpdate
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "McpServerUpdate", &req, validateEnabled) {
		return
	}

	// Validate new URL if provided.
	if req.Url != nil && *req.Url != "" {
		if !mcpURLSchemeValid(*req.Url) {
			jsonErr(w, http.StatusUnprocessableEntity,
				"url must use https, or http for loopback addresses only (localhost, 127.x.x.x, ::1)")
			return
		}
	}

	var updatedEntry config.MCPServerConfig
	mcpPatchValidationMsg := ""

	// errMCPNotFound is returned ONLY by the closure when the server id is absent,
	// so the dispatch below can distinguish a genuine 404 from a config I/O/parse
	// failure (e.g. unreadable or corrupt config.json) that aborts safeUpdateConfigJSON
	// before the closure runs — those must surface as 500, not a misleading 404.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		tools, _ := m["tools"].(map[string]any)
		if tools == nil {
			return errMCPNotFound
		}
		mcpSection, _ := tools["mcp"].(map[string]any)
		if mcpSection == nil {
			return errMCPNotFound
		}
		servers, _ := mcpSection["servers"].(map[string]any)
		if servers == nil {
			return errMCPNotFound
		}
		existing, ok := servers[id]
		if !ok {
			return errMCPNotFound
		}

		// Round-trip the existing entry through JSON to get a typed MCPServerConfig.
		raw, err := json.Marshal(existing)
		if err != nil {
			return fmt.Errorf("marshal existing entry: %w", err)
		}
		var current config.MCPServerConfig
		if uerr := json.Unmarshal(raw, &current); uerr != nil {
			return fmt.Errorf("unmarshal existing entry: %w", uerr)
		}

		// Merge only the fields that the caller provided (non-nil pointer).
		if req.Enabled != nil {
			current.Enabled = *req.Enabled
		}
		if req.Command != nil {
			current.Command = *req.Command
		}
		if req.Url != nil {
			current.URL = *req.Url
		}
		if req.Args != nil {
			current.Args = *req.Args
		}
		if req.Env != nil {
			current.Env = *req.Env
		}
		if req.EnvFile != nil {
			current.EnvFile = *req.EnvFile
		}
		if req.Headers != nil {
			current.Headers = *req.Headers
		}

		// Transport-consistency on the MERGED result (transport itself is immutable
		// via PATCH): stdio uses command (no url); sse/http use url (no command).
		if current.Type == "stdio" && strings.TrimSpace(current.URL) != "" {
			mcpPatchValidationMsg = "stdio servers must not set a url"
			return fmt.Errorf("validation: %s", mcpPatchValidationMsg)
		}
		if (current.Type == "sse" || current.Type == "http") && strings.TrimSpace(current.Command) != "" {
			mcpPatchValidationMsg = "sse/http servers must not set a command"
			return fmt.Errorf("validation: %s", mcpPatchValidationMsg)
		}

		// Rebuild the map entry from the updated struct so the JSON shape is
		// consistent with what addMCPServer writes.
		updated, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("marshal updated entry: %w", err)
		}
		var updatedMap map[string]any
		if err := json.Unmarshal(updated, &updatedMap); err != nil {
			return fmt.Errorf("unmarshal updated entry: %w", err)
		}
		servers[id] = updatedMap
		updatedEntry = current

		// An explicit PATCH {enabled: true} is operator intent to (re)connect
		// this server, exactly like addMCPServer's own auto-enable — flip the
		// global kill-switch on in the same write so ReconcileMCP's desired set
		// isn't forced empty by a still-false tools.mcp.enabled (the
		// upgraded-install trap: an operator re-enables a server that was
		// disabled before the global flag existed, Test succeeds, but nothing
		// ever connects because the flag itself was never on). Any other PATCH
		// — including {enabled: false} — leaves the global flag untouched.
		if req.Enabled != nil && *req.Enabled {
			mcpSection["enabled"] = true
		}
		return nil
	}); err != nil {
		if mcpPatchValidationMsg != "" {
			jsonErr(w, http.StatusUnprocessableEntity, mcpPatchValidationMsg)
			return
		}
		if errors.Is(err, errMCPNotFound) {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("mcp server %q not found", id))
			return
		}
		slog.Error("rest: patch mcp server", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// Config write succeeded — reconcile the live manager so an edited server
	// (changed command/url/args/env/headers, or toggled enabled) is reconnected
	// with the new config, or disconnected if the patch disabled it, instead of
	// the live connection silently drifting from what config.json now says.
	rctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	if err := a.agentLoop.ReconcileMCP(rctx); err != nil {
		slog.Warn("rest: patch mcp server: live reconcile failed", "server", id, "error", err)
	}
	cancel()

	transport := gen.McpServerTransportStdio
	switch updatedEntry.Type {
	case "sse":
		transport = gen.McpServerTransportSse
	case "http":
		transport = gen.McpServerTransportHttp
	}
	enabled := updatedEntry.Enabled
	status, toolCount := a.mcpLiveStatus(id)
	resp := gen.McpServer{
		Id:        id,
		Name:      id,
		Transport: transport,
		Status:    status,
		ToolCount: toolCount,
		Enabled:   &enabled,
	}
	jsonOK(w, resp)
}

// --- Tools ---

// HandleTools handles GET /api/v1/tools — returns the list of tools available to the agent.
func (a *restAPI) HandleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	registry := a.agentLoop.GetRegistry()
	agent := registry.GetDefaultAgent()
	if agent == nil {
		jsonOK(w, []map[string]any{})
		return
	}
	allTools := agent.Tools.GetAll()
	tools := make([]map[string]any, 0, len(allTools))
	for _, t := range allTools {
		name := t.Name()
		category := "general"
		if idx := strings.Index(name, "."); idx > 0 {
			category = name[:idx]
		}
		tools = append(tools, map[string]any{
			"name":        name,
			"category":    category,
			"description": t.Description(),
		})
	}
	jsonOK(w, tools)
}

// --- Tool Visibility (Issue #41) ---

// HandleMCPTools handles GET /api/v1/tools/mcp — returns all configured MCP
// servers with their status and tool lists for the agent tool picker UI.
func (a *restAPI) HandleMCPTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()
	servers := make([]map[string]any, 0, len(cfg.Tools.MCP.Servers))
	for name, srv := range cfg.Tools.MCP.Servers {
		entry := map[string]any{
			"id":      name,
			"name":    name,
			"enabled": srv.Enabled,
			"command": srv.Command,
		}
		if len(srv.Args) > 0 {
			entry["args"] = srv.Args
		}
		servers = append(servers, entry)
	}
	jsonOK(w, servers)
}

// updateAgentTools handles PUT /api/v1/agents/{id}/tools — replaces the
// agent's tool visibility config.
func (a *restAPI) updateAgentTools(w http.ResponseWriter, r *http.Request, agentID string) {
	cfg := a.agentLoop.GetConfig()
	found := false
	var foundAgent config.AgentConfig
	for _, ac := range cfg.Agents.List {
		if ac.ID == agentID {
			found = true
			foundAgent = ac
			break
		}
	}
	if !found {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}
	// Locked (core/system) agents cannot have their tool policy overwritten via the API.
	// Use coreagent.IsCoreAgent or check the Locked flag.
	if foundAgent.Locked {
		jsonErr(w, http.StatusForbidden, fmt.Sprintf("agent %q is locked and cannot be modified", agentID))
		return
	}
	// subagent_3p (External CLI) agents have no tools_cfg on the wire at all
	// (W1 discriminated union — AgentCreateRequestSubagent3p has no tools_cfg
	// property) — the runner manages its own tool loop. This endpoint is a
	// separate write path from updateAgent's firstForbiddenSubagent3pField
	// guard, so it needs its own check: closes the tools_cfg-for-3p leak
	// endpoint (a caller could otherwise bypass the PUT /agents/{id} guard by
	// hitting PUT /agents/{id}/tools directly).
	if isExternalSubagent(foundAgent) {
		jsonErr(w, http.StatusBadRequest, "external subagents run their own tools — tools_cfg is not configurable")
		return
	}

	// The step-up re-auth gate (requireReAuth) was INTENTIONALLY REMOVED here for
	// the per-agent tool-grant PUT per UAT feedback (operator found re-typing the
	// password to change a tool permission to be unnecessary friction). This is a
	// deliberate, scoped loosening — do NOT restore it. Authorization is still
	// enforced: this handler runs behind withAuth, so the caller must hold a valid
	// session; only the password re-prompt is removed. The same gate remains in
	// force on Integrations/Providers/Sandbox/Credentials/Performance PUTs.
	if user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req gen.AgentToolsUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "AgentToolsUpdateRequest", &req, validateEnabled) {
		return
	}

	// Extract builtin fields. There is no default_policy field on the wire
	// any more (CLAUDE.md hard constraint 6).
	var builtinPolicies map[string]string
	if req.Builtin != nil && req.Builtin.Policies != nil {
		builtinPolicies = make(map[string]string, len(req.Builtin.Policies))
		for k, v := range req.Builtin.Policies {
			builtinPolicies[k] = string(v)
		}
	}
	// Legacy mode/visible compatibility (one release): only mode="explicit"
	// with `visible` has any effect (populates builtinPolicies from visible
	// names as "allow"), and ONLY when the caller did NOT also send a real
	// `policies` map — policies always wins outright when present, matching
	// the documented wire contract ("mode/visible are... ignored when
	// policies is present"). Before this fix, the guard checked a now-inert
	// `builtinDefaultPolicy` bookkeeping variable (always "" at this point —
	// nothing set it earlier) instead of req.Builtin.Policies == nil, so a
	// caller sending BOTH a real policies map AND mode="explicit"+visible had
	// their real per-tool values silently discarded and replaced by the
	// deny-all-except-visible legacy conversion a few lines below — found
	// live, two independent reviewers, 2026-07-06. mode="inherit" alone is a
	// no-op now that there is no default-policy fallback to inherit from
	// (CLAUDE.md hard constraint 6) — an incomplete map is rejected by the
	// coverage check below regardless.
	if req.Builtin != nil && req.Builtin.Policies == nil && req.Builtin.Mode != nil &&
		string(*req.Builtin.Mode) == "explicit" && req.Builtin.Visible != nil {
		builtinPolicies = make(map[string]string, len(*req.Builtin.Visible))
		for _, name := range *req.Builtin.Visible {
			builtinPolicies[name] = "allow"
		}
	}

	// Validate per-tool policy values sent in the request.
	validPolicies := map[string]bool{"allow": true, "ask": true, "deny": true}
	for name, p := range builtinPolicies {
		if !validPolicies[p] {
			jsonErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("invalid policy %q for tool %q", p, name))
			return
		}
	}

	// Validate MCP server IDs reference configured servers. Computed before
	// the locked validate+persist IIFE below (it does not feed
	// config.ValidateToolPolicyCoverage, only the MCP section of the persist
	// payload), then captured by that IIFE's persist closure.
	var mcpServers []struct {
		ID    string
		Tools []string
	}
	if req.Mcp != nil && req.Mcp.Servers != nil {
		configuredServers := cfg.Tools.MCP.Servers
		for _, s := range *req.Mcp.Servers {
			if s.Id == "" {
				jsonErr(w, http.StatusUnprocessableEntity, "mcp.servers[].id must not be empty")
				return
			}
			if _, exists := configuredServers[s.Id]; !exists {
				jsonErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("MCP server %q is not configured", s.Id))
				return
			}
			toolList := []string{}
			if s.Tools != nil {
				toolList = *s.Tools
			}
			mcpServers = append(mcpServers, struct {
				ID    string
				Tools []string
			}{ID: s.Id, Tools: toolList})
		}
	}

	// CLAUDE.md hard constraint 6 / config.ValidateToolPolicyCoverage: this
	// endpoint fully replaces the agent's builtin tools config on persist
	// (see the updateConfigJSONLocked closure below), so builtinPolicies IS
	// the prospective complete per-tool map for this agent — reject 400 with
	// the full gap list if it (together with the current global
	// sandbox.tool_policies) would leave any static builtin tool without an
	// explicit policy entry, before persisting anything.
	//
	// The validate step and the persist step run inside ONE a.configMu-locked
	// critical section (closing a TOCTOU race two concurrent tool-policy
	// writes could otherwise open — see updateConfigJSONLocked's doc
	// comment), via withToolPolicyCoverageGuard: it always fetches the config
	// FRESH, inside a.configMu, discarding the `cfg` fetched at the top of
	// this function (used only for the fast-path 404/locked/MCP-server-id
	// checks above, none of which persist anything) — so a concurrent write
	// racing this one cannot slip in between fetch and lock unobserved by
	// either the coverage check or the persist step. Returns false once it
	// has already written the HTTP response (error case), so the caller just
	// returns.
	if ok := a.withToolPolicyCoverageGuard(
		w,
		func(c *config.Config) {
			candidatePolicies := make(map[string]config.ToolPolicy, len(builtinPolicies))
			for k, v := range builtinPolicies {
				candidatePolicies[k] = config.ToolPolicy(v)
			}
			// Base the candidate on the FRESHLY-fetched agent copy (from c,
			// searched by ID), never the pre-lock foundAgent snapshot — only
			// Tools is overridden below, so any other field a concurrent
			// write changed in the meantime stays correctly reflected.
			for i := range c.Agents.List {
				if c.Agents.List[i].ID != agentID {
					continue
				}
				candidateAgent := c.Agents.List[i]
				candidateAgent.Tools = &config.AgentToolsCfg{
					Builtin: config.AgentBuiltinToolsCfg{Policies: candidatePolicies},
				}
				c.Agents.List[i] = candidateAgent
				break
			}
		},
		func(gaps []config.CoverageGap) string {
			return fmt.Sprintf(
				"tool policy coverage incomplete for agent %q (%d gap(s)): %s",
				agentID, len(gaps), joinCoverageGapMessages(gaps),
			)
		},
		// ADR-054 D2/§11 checklist item 4 ("tools/policies"): agents are
		// per-entity records under entities/agents/<id>.json, not config.json's
		// agents.list — persist via the agent store instead of splicing the
		// raw config map. `m` is deliberately left untouched.
		func(m map[string]any) error {
			_, updateErr := agentstore.New(a.homePath).Update(agentID, func(agentRec *config.AgentConfig) error {
				builtinCfg := config.AgentBuiltinToolsCfg{}
				if len(builtinPolicies) > 0 {
					builtinCfg.Policies = agentToolPolicyMapFromWire(builtinPolicies)
				}
				newTools := &config.AgentToolsCfg{Builtin: builtinCfg}
				if len(mcpServers) > 0 {
					servers := make([]config.AgentMCPServerBinding, 0, len(mcpServers))
					for _, s := range mcpServers {
						servers = append(servers, config.AgentMCPServerBinding{ID: s.ID, Tools: s.Tools})
					}
					newTools.MCP = config.AgentMCPToolsCfg{Servers: servers}
				} else if agentRec.Tools != nil {
					// Symmetric preservation for mcp, mirroring updateAgent's
					// identical branch above: a request that omits mcp (every
					// builtin-policy update from the Agents UI does) must not
					// silently drop the agent's existing MCP server bindings.
					//
					// This is not hypothetical here. The SPA's
					// ToolsAndPermissions editor builds its payload by
					// spreading the agent's existing tools cfg, but NO gateway
					// read path populates tools_cfg, so `existing.mcp` is
					// always undefined and the payload never carries mcp. The
					// write is triggered by useAutoSave, so a single
					// allow/ask/deny toggle wiped the bindings with no Save
					// click and no way to restore them from the UI.
					newTools.MCP = agentRec.Tools.MCP
				}
				agentRec.Tools = newTools
				return nil
			})
			if updateErr != nil {
				if errors.Is(updateErr, entity.ErrNotFound) {
					return fmt.Errorf("agent %q not found in agent store", agentID)
				}
				return fmt.Errorf("update agent tools entity record: %w", updateErr)
			}
			return nil
		},
		fmt.Sprintf("rest: update agent tools entity record (agent_id=%s)", agentID),
	); !ok {
		return
	}

	// Trigger a reload AND WAIT for it (triggerReloadAndWaitOutcome, not a bare
	// TriggerReload — mirrors createAgent/updateAgent/deleteAgent) so the
	// agent's atomic toolPolicy pointer (pkg/agent/instance.go:290 —
	// populated by ReloadProviderAndConfig) is actually swapped to the new
	// policy before this handler responds. A bare TriggerReload only
	// enqueues the reload and returns before the registry swap happens —
	// without waiting, the next turn's resolveToolPolicyAtExec /
	// FilterToolsByPolicy could still see the previous snapshot for as long
	// as that swap takes to land, and (e.g.) an exec call freshly bumped to
	// "ask" would run as "allow" because LoadToolPolicy returns the stale
	// pointer. triggerReloadAndWaitOutcome already treats
	// ErrReloadNotConfigured (unit tests without the full gateway reload
	// pipeline wired) as a no-op, so a non-nil error here is always a genuine
	// reload failure. This is a fail-open authorization path — returning 200
	// while the rebuild is still queued means a tool freshly bumped to
	// "deny"/"ask" keeps executing as "allow" for the duration — so the
	// confirmed bool below also surfaces an unconfirmed (timed-out) reload as
	// a warning rather than silently claiming success.
	if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Error("agent tools update: reload failed — in-memory policy not updated",
			"agent_id", agentID, "error", err)
		if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
			if auditErr := audit.EmitSecuritySettingChange(
				r.Context(), auditLogger, "agent.tools_policy",
				map[string]any{"agent_id": agentID, "saved": true},
				map[string]any{"agent_id": agentID, "reload_error": err.Error()},
			); auditErr != nil {
				slog.Error("rest: audit emit agent tools reload failure", "error", auditErr)
			}
		}
		jsonErr(w, http.StatusServiceUnavailable,
			"config saved but in-memory reload failed; restart the gateway or retry")
		return
	} else if !confirmed {
		slog.Warn("rest: agent tools update: reload did not confirm within the poll window; "+
			"in-memory tool policy may not yet reflect the new config", "agent_id", agentID)
	}
	// Use HandleAgentToolsRegistry so the PUT response emits `tools` (not
	// `effective_tools`) — both paths must share the same wire shape to match
	// the AgentToolsResponse spec and the SPA Zod schema (_agentToolsSchema).
	a.HandleAgentToolsRegistry(w, r, agentID)
}

// --- Channels ---

// HandleChannels handles GET /api/v1/channels, GET /api/v1/channels/{id},
// PUT /api/v1/channels/{id}/enable|disable|configure, POST /api/v1/channels/{id}/test,
// and GET/PUT /api/v1/channels/{id}/routing.
func (a *restAPI) HandleChannels(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/channels")
	sub = strings.TrimPrefix(sub, "/")

	if sub != "" {
		parts := strings.SplitN(sub, "/", 2)
		channelID := parts[0]
		// Accept both bare type keys ("whatsapp") and per-instance keys
		// ("whatsapp.eu"): extract the base type and validate against known types.
		// Per-instance existence is validated per-endpoint (ADR-029 Gate 0).
		baseChannelType, _ := config.ParseInstanceKey(channelID)
		if !validChannelIDs[baseChannelType] {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("channel %q not found", channelID))
			return
		}
		// FR-024 / S-1+S-2: enforce the full ADR-029 instance-key grammar at the
		// write boundary BEFORE any credential or config write reaches disk.
		// ParseInstanceKey only extracts the base type; it does not validate the
		// slug. An attacker with a valid session could otherwise send
		// PUT /channels/whatsapp.BAD/configure with an uppercase or overlong slug,
		// causing safeUpdateConfigJSON to persist the malformed key — which makes
		// LoadConfig abort on next boot (boot-brick) and leaves an orphaned
		// credential under the attacker-chosen key.
		// Return 400 "malformed channel id" (distinct from the 404 "not found"
		// above) so callers can distinguish unknown-type from bad-grammar.
		if err := config.ValidateInstanceKey(channelID); err != nil {
			jsonErr(w, http.StatusBadRequest, "malformed channel id")
			return
		}

		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				a.getChannelConfig(w, channelID)
			case http.MethodDelete:
				// FINAL-REVIEW MEDIUM: DELETE is a high-blast-radius destructive verb
				// (os.RemoveAll on the instance state dir + credential deletion), so it
				// is gated by the dev-mode-bypass guard. The /channels route is already
				// registered under withAuth, so apply only the authorization layer
				// (RequireNotBypass) here — re-running withAuth would double-authenticate.
				a.requireAdminAuthz(func(w http.ResponseWriter, r *http.Request) {
					a.deleteChannelInstance(w, channelID)
				})(w, r)
			default:
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		action := parts[1]
		switch action {
		case "enable":
			if r.Method != http.MethodPut {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.setChannelEnabled(w, channelID, true)
		case "disable":
			if r.Method != http.MethodPut {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.setChannelEnabled(w, channelID, false)
		case "configure":
			if r.Method != http.MethodPut {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.configureChannel(w, r, channelID)
		case "test":
			if r.Method != http.MethodPost {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.testChannel(w, channelID)
		case "routing":
			switch r.Method {
			case http.MethodGet:
				a.getChannelRouting(w, channelID)
			case http.MethodPut:
				a.setChannelRouting(w, r, channelID)
			default:
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		default:
			jsonErr(w, http.StatusNotFound, "unknown channel action")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		// fall through to list logic below
	case http.MethodPost:
		// FINAL-REVIEW MEDIUM: POST creates a channel instance (a destructive,
		// config-mutating action). The /channels route is already registered
		// under withAuth, so apply only the authorization layer (RequireNotBypass),
		// mirroring the other high-blast-radius routes without re-running withAuth.
		a.requireAdminAuthz(a.createChannelInstance)(w, r)
		return
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()

	// FINAL-REVIEW MAJOR: emit ONE ChannelEntry per configured instance, not one
	// per base type. The previous design collapsed all instances of a type into a
	// single row (byType last-writer-wins), so a second instance (whatsapp.eu +
	// whatsapp.us) was invisible and unmanageable — defeating US-6/US-11.
	//
	// Shape now:
	//   1. webchat (built-in, always first, always enabled, no instance entry).
	//   2. One entry per key in cfg.Channels — id == instance_id == the map key,
	//      with base-type metadata (name/transport/description) looked up from
	//      channelBaseTypes, plus per-instance enabled + identity.
	//   3. One "available but unconfigured" entry per base type that has NO
	//      configured instance (so the operator can still discover + add it).
	channels := []gen.ChannelEntry{
		{Id: "webchat", Name: "Web Chat", Transport: "websocket", Enabled: true, Description: "Built-in browser chat"},
	}

	// Track which base types already have at least one configured instance so we
	// can append the static "unconfigured" rows for the rest.
	configuredTypes := make(map[string]bool, len(cfg.Channels))

	// Deterministic ordering: configured instances sorted by their map key so the
	// list is stable across requests (Go map iteration is randomized).
	instanceKeys := make([]string, 0, len(cfg.Channels))
	for key := range cfg.Channels {
		instanceKeys = append(instanceKeys, key)
	}
	sort.Strings(instanceKeys)

	for _, key := range instanceKeys {
		inst := cfg.Channels[key]
		baseType := strings.ToLower(strings.TrimSpace(inst.Type))
		if baseType == "" {
			baseType, _ = config.ParseInstanceKey(key)
		}
		meta, known := channelBaseTypes[baseType]
		if !known {
			// An unknown/legacy type persisted in config still gets a row so the
			// operator can see and delete it; fall back to sensible defaults.
			meta = channelBaseTypeMeta{name: baseType, transport: "webhook", description: baseType}
		}
		configuredTypes[baseType] = true

		instanceID := key
		entry := gen.ChannelEntry{
			Id:          key,
			InstanceId:  &instanceID,
			Name:        meta.name,
			Transport:   gen.ChannelEntryTransport(meta.transport),
			Enabled:     inst.Enabled,
			Description: meta.description,
		}
		if baseType == "whatsapp" {
			entry.NativeAvailable = boolPtr(whatsappnative.NativeAvailable)
		}
		if ident := identityForInstance(inst.Identity); ident != nil {
			entry.Identity = ident
		}
		channels = append(channels, entry)
	}

	// Append the static "available but unconfigured" rows in the canonical base-type
	// order for every type with no configured instance.
	for _, baseType := range channelBaseTypeOrder {
		if configuredTypes[baseType] {
			continue
		}
		meta := channelBaseTypes[baseType]
		entry := gen.ChannelEntry{
			Id:          baseType,
			Name:        meta.name,
			Transport:   gen.ChannelEntryTransport(meta.transport),
			Enabled:     false,
			Description: meta.description,
		}
		if baseType == "whatsapp" {
			entry.NativeAvailable = boolPtr(whatsappnative.NativeAvailable)
		}
		channels = append(channels, entry)
	}

	// Overlay degraded state from the runtime channel manager. Channels that
	// failed to construct at startup are marked degraded=true with the init
	// error as the reason. whatsapp_native failures map to the "whatsapp" entry
	// because both transports share one list entry.
	if mgr := a.agentLoop.GetChannelManager(); mgr != nil {
		failed := mgr.FailedChannels()
		applyDegradedOverlay(channels, failed)
		// Warn for any failed channel whose (normalised) base type has no matching
		// entry in the channels list — these are dead channels that would otherwise
		// be silently invisible to operators. Entry ids may now be per-instance
		// keys ("whatsapp.eu"), so match on the base type extracted from each id.
		if len(failed) > 0 {
			entryBaseTypes := make(map[string]struct{}, len(channels))
			for _, e := range channels {
				bt, _ := config.ParseInstanceKey(e.Id)
				entryBaseTypes[bt] = struct{}{}
			}
			for _, f := range failed {
				id := f.Name
				if id == "whatsapp_native" {
					id = "whatsapp"
				}
				if _, matched := entryBaseTypes[id]; !matched {
					slog.Warn(
						"channels: failed channel has no matching entry in channels list",
						"registry_id", f.Name,
						"channel", f.Channel,
						"error", f.Err.Error(),
					)
				}
			}
		}
	}

	jsonOK(w, channels)
}

// applyDegradedOverlay marks entries in channelList as degraded when the
// supplied failed list contains a matching registry id.  It is a pure function
// extracted from HandleChannels so that it can be unit-tested without a full
// REST stack.
//
// Normalisation rule: "whatsapp_native" maps to the "whatsapp" base type because
// both the bridge and native transports share the WhatsApp channel type.
//
// Matching is by BASE TYPE (config.ParseInstanceKey of the entry id) so that a
// per-instance entry ("whatsapp.eu") is correctly marked when its base type
// ("whatsapp") failed to construct, and every instance of a failing type is
// flagged. A base-type entry ("telegram") parses to itself, so the existing
// base-type overlay tests keep passing unchanged.
func applyDegradedOverlay(channelList []gen.ChannelEntry, failed []channels.ChannelInitError) {
	if len(failed) == 0 {
		return
	}
	// Build a map of normalised registry-id (base type) → error reason.
	degradedMap := make(map[string]string, len(failed))
	for _, f := range failed {
		id := f.Name
		if id == "whatsapp_native" {
			id = "whatsapp"
		}
		degradedMap[id] = f.Err.Error()
	}
	for i := range channelList {
		baseType, _ := config.ParseInstanceKey(channelList[i].Id)
		if reason, ok := degradedMap[baseType]; ok {
			r := reason
			channelList[i].Degraded = boolPtr(true)
			channelList[i].DegradedReason = &r
		}
	}
}

// channelBaseTypeMeta is the static presentation metadata for a base channel
// type: the human-readable name, transport label, and description surfaced in
// each ChannelEntry. It is type-level (shared by every instance of the type).
type channelBaseTypeMeta struct {
	name        string
	transport   string
	description string
}

// channelBaseTypes maps base channel type → its presentation metadata. This is
// the single source of truth for the name/transport/description of every
// conversational channel row (both configured instances and the static
// "available but unconfigured" rows). Email is intentionally absent — it is a
// TOOL surface (per-agent mailbox), not a conversational channel.
var channelBaseTypes = map[string]channelBaseTypeMeta{
	"telegram":    {name: "Telegram", transport: "webhook", description: "Telegram Bot API"},
	"discord":     {name: "Discord", transport: "websocket", description: "Discord Gateway"},
	"slack":       {name: "Slack", transport: "websocket", description: "Slack Socket Mode"},
	"whatsapp":    {name: "WhatsApp", transport: "native", description: "WhatsApp (native, whatsmeow)"},
	"feishu":      {name: "Feishu / Lark", transport: "webhook", description: "Feishu (Lark) Bot"},
	"dingtalk":    {name: "DingTalk", transport: "webhook", description: "DingTalk Bot"},
	"wecom":       {name: "WeCom", transport: "webhook", description: "WeCom (WeChat Work) Bot"},
	"weixin":      {name: "Weixin", transport: "webhook", description: "Weixin (WeChat) Official Account"},
	"line":        {name: "LINE", transport: "webhook", description: "LINE Messaging API"},
	"qq":          {name: "QQ", transport: "websocket", description: "QQ via napcat"},
	"irc":         {name: "IRC", transport: "tcp", description: "Internet Relay Chat"},
	"matrix":      {name: "Matrix", transport: "http", description: "Matrix protocol"},
	"google-chat": {name: "Google Chat", transport: "webhook", description: "Google Chat (webhook or service account)"},
}

// channelBaseTypeOrder is the canonical display order for the static
// "available but unconfigured" rows (Go map iteration is randomized, so a fixed
// slice keeps the list stable across requests).
var channelBaseTypeOrder = []string{
	"telegram", "discord", "slack", "whatsapp", "feishu", "dingtalk",
	"wecom", "weixin", "line", "qq", "irc", "matrix", "google-chat",
}

// identityForInstance converts a persisted config.ChannelIdentity into the
// generated ChannelEntry.Identity anonymous-struct pointer, or nil when the
// instance has no identity or a malformed one (a bad persisted identity is
// dropped rather than emitting a schema-invalid entry). Extracted so the
// per-instance list construction stays readable.
func identityForInstance(identity *config.ChannelIdentity) *struct {
	Id   *string                      `json:"id,omitempty"`
	Kind gen.ChannelEntryIdentityKind `json:"kind"`
} {
	if identity == nil {
		return nil
	}
	var entryKind gen.ChannelEntryIdentityKind
	switch strings.ToLower(strings.TrimSpace(identity.Kind)) {
	case "agent":
		entryKind = gen.ChannelEntryIdentityKindAgent
	case "user":
		entryKind = gen.ChannelEntryIdentityKindUser
	default:
		return nil
	}
	ident := &struct { // not-wire-format: pointer to the generated gen.ChannelEntry.Identity anonymous field type — not a parallel wire type
		Id   *string                      `json:"id,omitempty"`
		Kind gen.ChannelEntryIdentityKind `json:"kind"`
	}{
		Kind: entryKind,
	}
	if id := strings.TrimSpace(identity.ID); id != "" {
		idCopy := id
		ident.Id = &idCopy
	}
	return ident
}

// validChannelIDs is the set of base channel types that can be toggled via the
// API. "webchat" is always enabled and intentionally excluded.
//
// Keyed by plain string literals (base channel type) because ChannelId is now a
// validated pattern (^[a-z0-9-]+(\.[a-z0-9-]+)?$) rather than a closed enum.
// Per-instance IDs like "whatsapp.eu" are accepted by extracting the base type
// via config.ParseInstanceKey before the lookup (ADR-029 Gate 0).
var validChannelIDs = map[string]bool{
	"telegram": true, "discord": true, "slack": true, "whatsapp": true,
	"feishu": true, "dingtalk": true, "wecom": true, "weixin": true,
	"line": true, "qq": true, "irc": true,
	"matrix": true, "google-chat": true,
	// "email" is deliberately absent — email is NOT a channel (M11:
	// config.knownChannelTypes excludes it too). The SPA uses
	// /agents/{id}/mailbox exclusively; /channels/email/* is unknown and 404s.
}

// channelWildcardIdx returns the index of the channel-wildcard AgentBinding
// for the given channelID in the bindings slice, or -1 if not found.
// A channel-wildcard binding has Match.Channel equal to channelID (compared
// case-insensitively), Match.AccountID=="*", and no Peer/Guild/Team qualifiers.
func channelWildcardIdx(bindings []config.AgentBinding, channelID string) int {
	for i, b := range bindings {
		if strings.ToLower(b.Match.Channel) != channelID {
			continue
		}
		if b.Match.AccountID != "*" {
			continue
		}
		if b.Match.Peer != nil || b.Match.GuildID != "" || b.Match.TeamID != "" {
			continue
		}
		return i
	}
	return -1
}

// isChannelWildcardRaw reports whether a raw binding match-map represents the
// channel-wildcard entry for channelID. A match is:
//   - match["channel"] equal to channelID (compared case-insensitively)
//   - match["account_id"] == "*"
//   - no "peer", "guild_id", or "team_id" keys present
func isChannelWildcardRaw(matchMap map[string]any, channelID string) bool {
	ch, _ := matchMap["channel"].(string)
	acc, _ := matchMap["account_id"].(string)
	_, hasPeer := matchMap["peer"]
	_, hasGuild := matchMap["guild_id"]
	_, hasTeam := matchMap["team_id"]
	return strings.ToLower(ch) == channelID && acc == "*" && !hasPeer && !hasGuild && !hasTeam
}

// getChannelRouting handles GET /api/v1/channels/{id}/routing.
// ADR-029 FR-029 / MAJ-004: for a BOUND instance (has WorkspaceID + agent Identity)
// returns {workspace_id, default_agent_id} read from cfg.Channels[id]; for an
// UNBOUND instance returns the legacy wildcard binding. The two representations are
// mutually exclusive per instance. For routing endpoints, a well-formed instance id
// that is not in cfg.Channels returns 404 "unknown instance" (FR-024/US-11 AC-2).
func (a *restAPI) getChannelRouting(w http.ResponseWriter, channelID string) {
	cfg := a.agentLoop.GetConfig()

	// Per-instance existence check for routing endpoints (FR-024).
	// A namespaced id like "whatsapp.eu" passes the base-type gate above but may
	// not have an entry in cfg.Channels if it was never configured.
	_, hasSlug := func() (string, bool) {
		_, slug := config.ParseInstanceKey(channelID)
		return slug, slug != ""
	}()
	if hasSlug {
		// Namespaced key: require entry in cfg.Channels.
		if _, exists := cfg.Channels[channelID]; !exists {
			jsonErr(w, http.StatusNotFound, "unknown instance")
			return
		}
	}

	// Check for bound representation first (FR-029). Use the single-source-of-truth
	// predicate so this GET path cannot diverge from the config layer's definition
	// of "bound" (e.g. the inline check previously omitted the TrimSpace on
	// WorkspaceID that IsWorkspaceBound applies).
	if inst, ok := cfg.Channels[channelID]; ok && inst.IsWorkspaceBound() {
		wsID := inst.WorkspaceID
		agentID := inst.Identity.ID
		resp := gen.ChannelRouting{
			WorkspaceId:    &wsID,
			DefaultAgentId: &agentID,
		}
		jsonOK(w, resp)
		return
	}

	// Unbound: fall back to legacy wildcard binding.
	idx := channelWildcardIdx(cfg.Bindings, channelID)
	var resp gen.ChannelRouting
	if idx >= 0 {
		id := cfg.Bindings[idx].AgentID
		resp.DefaultAgentId = &id
	}
	jsonOK(w, resp)
}

// setChannelRouting handles PUT /api/v1/channels/{id}/routing.
// ADR-029 FR-029 / MAJ-004 REWRITE:
//   - When workspace_id is present (bound flow): writes cfg.Channels[id].WorkspaceID +
//     cfg.Channels[id].Identity and REMOVES any stale channel-wildcard binding for id.
//     Rejection set: empty default_agent_id → 422; agent ∉ workspace.CoreTeam → 422;
//     worker agent → 422 (MIN-002, standardized from 400); unknown/archived workspace → 404.
//   - When workspace_id is absent (unbound/legacy flow): keeps existing wildcard-binding
//     upsert behavior unchanged (FR-005/FR-008 for the worker check still apply).
//   - Emits a routing-change audit event in both flows (FR-030 / STRIDE repudiation).
func (a *restAPI) setChannelRouting(w http.ResponseWriter, r *http.Request, channelID string) {
	var req gen.SetChannelRoutingJSONRequestBody
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ChannelRouting", &req, validateEnabled) {
		return
	}

	// Per-instance existence check for routing endpoints (FR-024).
	cfg := a.agentLoop.GetConfig()
	_, hasSlug := func() (string, bool) {
		_, slug := config.ParseInstanceKey(channelID)
		return slug, slug != ""
	}()
	if hasSlug {
		if _, exists := cfg.Channels[channelID]; !exists {
			jsonErr(w, http.StatusNotFound, "unknown instance")
			return
		}
	}

	// Determine flow: bound (workspace_id present) or legacy (unbound).
	workspaceID := ""
	if req.WorkspaceId != nil {
		workspaceID = strings.TrimSpace(*req.WorkspaceId)
	}

	if workspaceID != "" {
		// S-5: validate workspace_id grammar before building any filesystem path.
		// A workspace_id from the request body is only TrimSpace'd; without a
		// positive-allowlist check, a value such as "../../../etc/passwd" would
		// reach filepath.Join(home, "workspaces", id+".json") and probe outside
		// the data root. validateEntityID is the same traversal guard used by
		// handleWorkspaceGet and all other per-entity FS handlers in this file.
		if err := validateEntityID(workspaceID); err != nil {
			jsonErr(w, http.StatusBadRequest, "malformed workspace_id")
			return
		}

		// ── Bound flow ──────────────────────────────────────────────────────────
		// FR-005: empty default_agent_id → 422.
		agentID := ""
		if req.DefaultAgentId != nil {
			agentID = strings.TrimSpace(*req.DefaultAgentId)
		}
		if agentID == "" {
			jsonErr(w, http.StatusUnprocessableEntity, "default_agent_id is required for a bound instance")
			return
		}

		// FR-007: unknown or archived workspace → 404.
		ws, err := readWorkspaceFile(a.homePath, workspaceID)
		if err != nil {
			if errors.Is(err, errWorkspaceNotFound) {
				jsonErr(w, http.StatusNotFound, "workspace not found")
				return
			}
			slog.Error("rest: setChannelRouting: read workspace", "workspace_id", workspaceID, "error", err)
			jsonErr(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if ws.Status == string(gen.WorkspaceStatusArchived) {
			jsonErr(w, http.StatusNotFound, "workspace not found")
			return
		}

		// FR-008: worker agent → 422 (MIN-002: was 400, standardized to 422).
		var foundAgent *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == agentID {
				ac := cfg.Agents.List[i]
				foundAgent = &ac
				break
			}
		}
		if foundAgent == nil {
			jsonErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("agent %q not found", agentID))
			return
		}
		// ADR-049 D3: System Agents are not valid routing-binding targets (400).
		// Checked before the worker 422 because a System Agent is also a
		// non-chat-target — the spec requires the system-specific 400.
		if foundAgent.IsSystem() {
			jsonErr(w, http.StatusBadRequest,
				"system agents are not chat targets and cannot be a channel's default agent")
			return
		}
		if foundAgent.IsWorker() {
			jsonErr(
				w,
				http.StatusUnprocessableEntity,
				"workers are not chat targets and cannot be a channel's default agent",
			)
			return
		}

		// FR-006: agent must be in the workspace's CoreTeam.
		inTeam := false
		for _, memberID := range ws.CoreTeam {
			if memberID == agentID {
				inTeam = true
				break
			}
		}
		if !inTeam {
			jsonErr(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("agent %q is not a member of workspace %q", agentID, workspaceID))
			return
		}

		// All validations passed — persist the bound representation.
		if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
			// Set cfg.Channels[id].workspace_id and identity.
			channels, _ := m["channels"].(map[string]any)
			if channels == nil {
				channels = map[string]any{}
				m["channels"] = channels
			}
			ch, _ := channels[channelID].(map[string]any)
			if ch == nil {
				ch = map[string]any{}
			}
			ch["workspace_id"] = workspaceID
			ch["identity"] = map[string]any{
				"kind": "agent",
				"id":   agentID,
			}
			// Persist the type field if not already set (normalizeChannelMap backfills, but
			// be explicit so the on-disk entry is self-describing).
			if _, ok := ch["type"]; !ok {
				baseType, _ := config.ParseInstanceKey(channelID)
				ch["type"] = baseType
			}
			channels[channelID] = ch
			m["channels"] = channels

			// FR-029: remove any stale channel-wildcard binding for this instance.
			rawBindings, _ := m["bindings"].([]any)
			filtered := make([]any, 0, len(rawBindings))
			for _, entry := range rawBindings {
				bMap, ok := entry.(map[string]any)
				if !ok {
					filtered = append(filtered, entry)
					continue
				}
				matchMap, _ := bMap["match"].(map[string]any)
				if matchMap == nil {
					filtered = append(filtered, entry)
					continue
				}
				if isChannelWildcardRaw(matchMap, channelID) {
					continue // drop the stale wildcard
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				delete(m, "bindings")
			} else {
				m["bindings"] = filtered
			}
			return nil
		}); err != nil {
			slog.Error("rest: save config for channel routing (bound)", "channel_id", channelID, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
			return
		}

		// FR-030: emit routing-change audit event (STRIDE repudiation).
		if a.auditor != nil {
			if err := a.auditor.Log(&audit.Entry{
				Event:    audit.EventChannelRoutingChanged,
				Decision: audit.DecisionAllow,
				Details: map[string]any{
					"channel_id":   channelID,
					"workspace_id": workspaceID,
					"agent_id":     agentID,
					"flow":         "bound",
				},
			}); err != nil {
				slog.Warn("audit write failed", "event", audit.EventChannelRoutingChanged,
					"channel_id", channelID, "flow", "bound", "error", err)
			}
		}

		// Re-stamp workspace_id onto every session that already existed for
		// this channel BEFORE the bind — resolveOrCreateChannelSession
		// (pkg/agent/loop.go) only stamps workspace_id at session CREATION
		// time and explicitly does not patch already-existing sessions.
		// Without this, an existing conversation keeps routing delegation
		// trust, memory rooms, and task-board placement against the stale
		// (or empty, silently-default-substituted) workspace forever, even
		// though new messages on the SAME conversation resolve the new
		// agent via routing. See restampChannelSessionsWorkspace's doc
		// comment for the full rationale.
		if store := a.agentLoop.GetSessionStore(); store != nil {
			if n, rsErr := restampChannelSessionsWorkspace(store, channelID, workspaceID); rsErr != nil {
				slog.Warn("rest: setChannelRouting: restamp existing sessions",
					"instance_id", channelID, "workspace_id", workspaceID, "error", rsErr)
			} else if n > 0 {
				slog.Info("rest: setChannelRouting: restamped existing sessions to bound workspace",
					"instance_id", channelID, "workspace_id", workspaceID, "count", n)
			}
		}

		// Return the bound representation.
		wsIDCopy := workspaceID
		agentIDCopy := agentID
		jsonOK(w, gen.ChannelRouting{
			WorkspaceId:    &wsIDCopy,
			DefaultAgentId: &agentIDCopy,
		})
		return
	}

	// ── Unbound / legacy flow ────────────────────────────────────────────────
	// Validate the agent ID when non-empty (FR-005/FR-008 still apply).
	if req.DefaultAgentId != nil && *req.DefaultAgentId != "" {
		agentID := *req.DefaultAgentId
		var found *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == agentID {
				ac := cfg.Agents.List[i]
				found = &ac
				break
			}
		}
		if found == nil {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
			return
		}
		// ADR-049 D3: System Agents are not valid routing-binding targets (400).
		if found.IsSystem() {
			jsonErr(w, http.StatusBadRequest,
				"system agents are not chat targets and cannot be a channel's default agent")
			return
		}
		// MIN-002: standardize to 422 (was 400).
		if found.IsWorker() {
			jsonErr(
				w,
				http.StatusUnprocessableEntity,
				"workers are not chat targets and cannot be a channel's default agent",
			)
			return
		}
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		// FR-029 (bound→unbound): clear workspace_id and identity from the
		// channel entry so that a previously-bound instance is fully unbound.
		// Without this, cfg.Channels[id].WorkspaceID persists across the PUT
		// and RepairStaleChannelWildcardBindings drops the new wildcard on
		// next config load — leaving the bound representation intact and
		// silently discarding the caller's unbind intent.
		if channels, _ := m["channels"].(map[string]any); channels != nil {
			if ch, _ := channels[channelID].(map[string]any); ch != nil {
				delete(ch, "workspace_id")
				delete(ch, "identity")
				channels[channelID] = ch
				m["channels"] = channels
			}
		}

		// Read the bindings array from the raw JSON map.
		rawBindings, _ := m["bindings"].([]any)

		if req.DefaultAgentId == nil || *req.DefaultAgentId == "" {
			// Remove the channel-wildcard binding for this channel if it exists.
			filtered := make([]any, 0, len(rawBindings))
			for _, entry := range rawBindings {
				bMap, ok := entry.(map[string]any)
				if !ok {
					filtered = append(filtered, entry)
					continue
				}
				matchMap, _ := bMap["match"].(map[string]any)
				if matchMap == nil {
					filtered = append(filtered, entry)
					continue
				}
				if isChannelWildcardRaw(matchMap, channelID) {
					continue
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				delete(m, "bindings")
			} else {
				m["bindings"] = filtered
			}
			return nil
		}

		// Upsert: replace or append the channel-wildcard binding.
		newBinding := map[string]any{
			"agent_id": *req.DefaultAgentId,
			"match": map[string]any{
				"channel":    channelID,
				"account_id": "*",
			},
		}
		replaced := false
		for i, entry := range rawBindings {
			bMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			matchMap, _ := bMap["match"].(map[string]any)
			if matchMap == nil {
				continue
			}
			if isChannelWildcardRaw(matchMap, channelID) {
				rawBindings[i] = newBinding
				replaced = true
				break
			}
		}
		if !replaced {
			rawBindings = append(rawBindings, newBinding)
		}
		m["bindings"] = rawBindings
		return nil
	}); err != nil {
		slog.Error("rest: save config for channel routing", "channel_id", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// FR-030: emit routing-change audit event.
	agentIDForAudit := ""
	if req.DefaultAgentId != nil {
		agentIDForAudit = *req.DefaultAgentId
	}
	if a.auditor != nil {
		if err := a.auditor.Log(&audit.Entry{
			Event:    audit.EventChannelRoutingChanged,
			Decision: audit.DecisionAllow,
			Details: map[string]any{
				"channel_id": channelID,
				"agent_id":   agentIDForAudit,
				"flow":       "unbound",
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", audit.EventChannelRoutingChanged,
				"channel_id", channelID, "flow", "unbound", "error", err)
		}
	}

	// Symmetric with the bound flow above: unbinding a previously-bound
	// channel clears cfg.Channels[id].WorkspaceID, so every existing
	// session that was stamped from that binding must be cleared too —
	// otherwise it stays pinned to a workspace the channel is no longer
	// routed through, forever. A channel that was never bound has no
	// sessions carrying a channel-derived workspace stamp, so this is a
	// harmless no-op for it (restampChannelSessionsWorkspace skips writes
	// where WorkspaceID already matches the target).
	if store := a.agentLoop.GetSessionStore(); store != nil {
		if n, rsErr := restampChannelSessionsWorkspace(store, channelID, ""); rsErr != nil {
			slog.Warn("rest: setChannelRouting: clear existing sessions on unbind",
				"instance_id", channelID, "error", rsErr)
		} else if n > 0 {
			slog.Info("rest: setChannelRouting: cleared stale workspace on existing sessions after unbind",
				"instance_id", channelID, "count", n)
		}
	}

	// Return the resulting routing state.
	liveCfg := a.agentLoop.GetConfig()
	idx := channelWildcardIdx(liveCfg.Bindings, channelID)
	var resp gen.ChannelRouting
	if idx >= 0 {
		id := liveCfg.Bindings[idx].AgentID
		resp.DefaultAgentId = &id
	}
	jsonOK(w, resp)
}

// restampChannelSessionsWorkspace re-stamps WorkspaceID on every existing
// session belonging to the given channel base type (e.g. "whatsapp" for
// both the bare "whatsapp" instance key and namespaced ones like
// "whatsapp.eu"). SessionMeta.Channel persists only the bare channel TYPE,
// never the instance slug (see pkg/agent/loop.go::resolveOrCreateChannelSession's
// NewChannelSession(channel, chatID, ...) call, which is handed msg.Channel —
// the bare type — not the instance ID) — so this is the finest granularity
// the session data model supports today. On an install with two instances
// of the same channel type (e.g. "whatsapp.eu" and "whatsapp.us"), binding
// one instance's workspace will also restamp the other instance's sessions,
// because they are indistinguishable at the session-meta level; that is a
// pre-existing limitation of SessionMeta (no InstanceID field), not
// something introduced here, and fixing it would require a session schema
// change outside this handler's scope.
//
// Closes the gap where setChannelRouting updated cfg.Channels[id].WorkspaceID
// but left every session created BEFORE the change pinned to its stale
// workspace_id forever: resolveOrCreateChannelSession only stamps
// workspace_id at session CREATION time and its own doc comment concedes
// "Already-existing sessions are NOT patched." A stale or empty workspace_id
// is not a safe no-op — resolveEffectiveWorkspaceID (pkg/agent/loop.go)
// silently substitutes the operator's default workspace, so a stale stamp
// routes delegation trust (ADR-037), memory rooms, and task-board placement
// against the WRONG workspace's graph.
//
// newWorkspaceID may be "" to clear the stamp (the unbind case). Every
// matching session's WorkspaceID is unconditionally overwritten with
// newWorkspaceID (skipping sessions that already match, to avoid a
// no-op SetMeta write) — this must be able to MOVE a session off a stale
// workspace onto a new one on re-bind, so it deliberately does not use the
// non-clobber-once-set policy pkg/gateway/schedules.go's
// stampScheduledSessionWorkspace uses for a different (single-anchor)
// purpose.
//
// Only sessions whose Channel field equals baseType are touched — webchat
// and other-channel sessions are never affected. A per-session SetMeta
// failure is logged and does not abort the rest of the batch; the returned
// count is the number of sessions successfully updated, and the returned
// error (if any) is the first SetMeta failure encountered, for the caller's
// own WARN log.
// restampChannelSessionsWorkspace moves the sessions of ONE channel instance
// onto a new workspace.
//
// It matches on the INSTANCE key, never the bare channel type. An install can
// hold many instances of one platform — a hundred WhatsApp numbers, each bound
// to its own (workspace, agent) pair under ADR-029 — and every one of their
// sessions records Channel=="whatsapp". Matching on type would relabel all
// hundred when an operator re-binds one of them: the very bug this restamp
// exists to fix, inflicted on the other ninety-nine.
//
// A session with NO instance key is left alone. Those predate the field, so
// "unknown" is the honest reading; guessing from the type is exactly the
// conflation above. Their workspace still resolves correctly at turn time via
// processMessage's channel-instance fallback whenever it is empty.
func restampChannelSessionsWorkspace(store *session.UnifiedStore, instanceID, newWorkspaceID string) (int, error) {
	if store == nil || instanceID == "" {
		return 0, nil
	}
	sessions, err := store.ListSessionsFiltered(func(m *session.UnifiedMeta) bool {
		return m != nil && m.InstanceID != "" && m.InstanceID == instanceID
	})
	if err != nil {
		return 0, fmt.Errorf("list sessions for channel instance %q: %w", instanceID, err)
	}
	updated := 0
	var firstErr error
	for _, m := range sessions {
		if m.WorkspaceID == newWorkspaceID {
			continue // already correct — skip the redundant write
		}
		wsCopy := newWorkspaceID
		if setErr := store.SetMeta(m.ID, session.MetaPatch{WorkspaceID: &wsCopy}); setErr != nil {
			slog.Warn("rest: restampChannelSessionsWorkspace: SetMeta failed",
				"session_id", m.ID, "instance_id", instanceID, "workspace_id", newWorkspaceID, "error", setErr)
			if firstErr == nil {
				firstErr = fmt.Errorf("session %q: %w", m.ID, setErr)
			}
			continue
		}
		updated++
	}
	return updated, firstErr
}

// createChannelInstance handles POST /api/v1/channels (ADR-029 FR-024/FR-025,
// US-6/US-10). Creates a new channel instance with key "<type>.<slug>".
// The instance starts disabled (Enabled=false) and its config entry is written
// via safeUpdateConfigJSON. Rejects on: unknown type (400), malformed slug (400),
// duplicate key (409).
func (a *restAPI) createChannelInstance(w http.ResponseWriter, r *http.Request) {
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.CreateChannelInstanceJSONRequestBody
	if !decodeAndValidate(w, r, "ChannelCreateRequest", &req, validateEnabled) {
		return
	}

	chType := strings.ToLower(strings.TrimSpace(req.Type))
	slug := strings.TrimSpace(req.Slug)

	// Validate the base channel type.
	if !validChannelIDs[chType] {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("unknown channel type %q", chType))
		return
	}

	// Build the instance key and validate its full grammar (FR-017).
	instanceKey := chType + "." + slug
	if err := config.ValidateInstanceKey(instanceKey); err != nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("malformed slug: %v", err))
		return
	}

	// Reject if the instance key already exists (409).
	cfg := a.agentLoop.GetConfig()
	if _, exists := cfg.Channels[instanceKey]; exists {
		jsonErr(w, http.StatusConflict, fmt.Sprintf("channel instance %q already exists", instanceKey))
		return
	}

	// Persist the new entry (disabled by default).
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			channels = map[string]any{}
			m["channels"] = channels
		}
		// Double-check for races — another concurrent request may have inserted it.
		if _, exists := channels[instanceKey]; exists {
			return fmt.Errorf("channel instance %q already exists", instanceKey)
		}
		channels[instanceKey] = map[string]any{
			"type":    chType,
			"enabled": false,
		}
		m["channels"] = channels
		return nil
	}); err != nil {
		// surfaced as 409 when the race guard fires, 500 otherwise
		if strings.Contains(err.Error(), "already exists") {
			jsonErr(w, http.StatusConflict, fmt.Sprintf("channel instance %q already exists", instanceKey))
			return
		}
		slog.Error("rest: create channel instance: save config", "instance", instanceKey, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	slog.Info("rest: created channel instance", "id", instanceKey, "type", chType)
	writeJSON(w, http.StatusCreated, gen.ChannelCreateResponse{
		Id:      instanceKey,
		Type:    chType,
		Enabled: false,
	})
}

// deleteChannelInstance handles DELETE /api/v1/channels/{id} (ADR-029 FR-025,
// US-10/US-11). Removes the channel instance's config entry, credential refs,
// any stale channel-wildcard binding for the id, and the per-instance state
// directory (e.g. WhatsApp SQLite store).
//
// Teardown order (config removal first so a partial failure never leaves a
// config pointing at a missing store):
//  1. Collect credential ref names from the live config BEFORE writing.
//  2. Remove config entry + stale wildcard binding via safeUpdateConfigJSON.
//  3. Delete credential store entries (best-effort; orphaned blobs not fatal).
//  4. Remove per-instance state directory (best-effort; stale dir not fatal).
func (a *restAPI) deleteChannelInstance(w http.ResponseWriter, channelID string) {
	// Grammar already validated by HandleChannels before we reach here.
	// Existence check: require the instance to be in cfg.Channels.
	cfg := a.agentLoop.GetConfig()
	inst, exists := cfg.Channels[channelID]
	if !exists {
		jsonErr(w, http.StatusNotFound, "unknown instance")
		return
	}

	// Snapshot credential ref names to delete AFTER the config write.
	chBaseType, _ := config.ParseInstanceKey(channelID)
	var credRefs []string
	for _, field := range channelSensitiveFields[chBaseType] {
		credRefs = append(credRefs, channelCredKey(channelID, field))
	}

	// Snapshot the WhatsApp store path (if applicable) before the config write.
	// We derive the default path the same way init.go does: AgentHomeBasePath()/whatsapp/<instanceID>.
	// If SessionStorePath is explicitly set, use that; otherwise use the derived default.
	var stateDir string
	if chBaseType == "whatsapp" {
		storePath := inst.SessionStorePath
		if storePath == "" {
			storePath = filepath.Join(cfg.AgentHomeBasePath(), "whatsapp", channelID)
		}
		stateDir = storePath
	}

	// Step 2: remove config entry and stale wildcard binding atomically.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels != nil {
			delete(channels, channelID)
			if len(channels) == 0 {
				delete(m, "channels")
			} else {
				m["channels"] = channels
			}
		}

		// Remove any stale channel-wildcard binding for this instance (FR-029).
		rawBindings, _ := m["bindings"].([]any)
		if len(rawBindings) > 0 {
			filtered := make([]any, 0, len(rawBindings))
			for _, entry := range rawBindings {
				bMap, ok := entry.(map[string]any)
				if !ok {
					filtered = append(filtered, entry)
					continue
				}
				matchMap, _ := bMap["match"].(map[string]any)
				if matchMap == nil {
					filtered = append(filtered, entry)
					continue
				}
				if isChannelWildcardRaw(matchMap, channelID) {
					continue // drop stale wildcard
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				delete(m, "bindings")
			} else {
				m["bindings"] = filtered
			}
		}
		return nil
	}); err != nil {
		slog.Error("rest: delete channel instance: save config", "id", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Step 2b (FINAL-REVIEW Medium-High — leaked running channel): reload the
	// manager NOW, after the config entry is removed but BEFORE the credential and
	// state-dir teardown. ChannelManager.Reload stops any channel dropped from the
	// config, so a deleted-while-ENABLED instance's goroutine/connection is torn
	// down instead of continuing to process inbound with credentials we are about
	// to delete. Doing it before the cred/state teardown means Stop() still has the
	// live credentials and store files it may need to close cleanly. In the
	// unit-test environment TriggerReload is a no-op (ErrReloadNotConfigured), so
	// this is safe to call unconditionally.
	if a.agentLoop != nil {
		if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
			// A genuine reload failure means the (now config-less) channel may still
			// be running. Log it but continue the teardown — we must still remove the
			// credentials/state the operator asked to delete, and the config entry is
			// already gone so a subsequent reload will converge.
			slog.Error("rest: delete channel instance: reload after config removal failed",
				"id", channelID, "error", err)
		} else if !confirmed {
			slog.Warn("rest: delete channel instance: reload did not confirm within the poll window; "+
				"the now config-less channel instance may still be running", "id", channelID)
		}
	}

	// Step 3: delete credential store entries. An orphaned encrypted secret left
	// behind against the operator's explicit "delete this instance" intent is a
	// security-relevant outcome (the credential remains recoverable at rest), so a
	// real store fault is escalated to Error (with a correlatable errorId) and
	// surfaced in the audit event via cleanup_failed=true. removeStoredCredential
	// returns nil for not-found keys, so only a genuine I/O/store fault trips this.
	cleanupFailed := false
	for _, refName := range credRefs {
		if err := a.removeStoredCredential(refName); err != nil {
			cleanupFailed = true
			errorID := uuid.NewString()
			slog.Error("rest: delete channel instance: remove credential failed — orphaned encrypted secret",
				"id", channelID, "ref", refName, "error", err, "errorId", errorID)
		}
	}

	// Step 4: remove per-instance state directory (best-effort).
	if stateDir != "" {
		if err := os.RemoveAll(stateDir); err != nil {
			slog.Warn("rest: delete channel instance: remove state dir",
				"id", channelID, "dir", stateDir, "error", err)
		}
	}

	// FINAL-REVIEW MEDIUM: emit an audit event for the destructive delete (ADR-029
	// FR-025 / STRIDE repudiation). A channel-instance delete revokes stored
	// credentials and removes state; it MUST leave an audit trail even on the happy
	// path. cleanup_failed=true flags an orphaned encrypted blob for the operator.
	if a.auditor != nil {
		decision := audit.DecisionAllow
		if cleanupFailed {
			decision = audit.DecisionError
		}
		if err := a.auditor.Log(&audit.Entry{
			Event:    audit.EventChannelInstanceDeleted,
			Decision: decision,
			Details: map[string]any{
				"channel_id":     channelID,
				"type":           chBaseType,
				"cleanup_failed": cleanupFailed,
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", audit.EventChannelInstanceDeleted,
				"channel_id", channelID, "error", err)
		}
	}

	slog.Info("rest: deleted channel instance", "id", channelID, "type", chBaseType, "cleanup_failed", cleanupFailed)
	w.WriteHeader(http.StatusNoContent)
}

func (a *restAPI) setChannelEnabled(w http.ResponseWriter, channelID string, enabled bool) {
	baseType, _ := config.ParseInstanceKey(channelID)
	if !validChannelIDs[baseType] {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("channel %q not found", channelID))
		return
	}
	if enabled {
		// Stage 1 of the channel-Test redesign: enabling a channel with an
		// incomplete config must be rejected, not silently persisted — an
		// enabled-but-incomplete channel would fail to construct on the next
		// reload/boot. Disabling never validates (turning a channel off is
		// always safe). readChannelConfigRaw returns {} for a channel with no
		// persisted section yet.
		stored, err := a.readChannelConfigRaw(channelID)
		if err != nil {
			slog.Error("rest: read config for channel enable validation", "channel", channelID, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read config: %v", err))
			return
		}
		if msg := validateChannelConfigComplete(baseType, stored, nil, nil); msg != "" {
			jsonErr(w, http.StatusBadRequest, msg)
			return
		}
	}
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			channels = map[string]any{}
			m["channels"] = channels
		}
		ch, _ := channels[channelID].(map[string]any)
		if ch == nil {
			ch = map[string]any{}
			channels[channelID] = ch
		}
		// Persist the type discriminator. For bare type keys the base type equals
		// the key; for namespaced instance keys ("whatsapp.eu") the type is the
		// pre-dot segment (ADR-029 Gate 0 / FR-017).
		ch["type"] = baseType
		ch["enabled"] = enabled
		return nil
	}); err != nil {
		slog.Error("rest: set channel enabled", "channel", channelID, "enabled", enabled, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// #358: persisting channels.<id>.enabled via safeUpdateConfigJSON only swaps the
	// in-memory config pointer (refreshConfigAndRewireServices → SwapConfig); it does
	// NOT start or stop the channel. Reload so ChannelManager.Reload applies the diff —
	// starting a newly-enabled channel (e.g. whatsapp_native, which then emits its
	// pairing QR over the whatsapp_pairing WS frame) or stopping a disabled one. The
	// Reload path is crash-safe and name-correct as of #313. triggerReloadAndWait treats an
	// unwired reload pipeline (the unit-test environment) as a no-op and only returns an
	// error on a genuine reload failure, which we surface rather than reporting a false
	// success (the flag persisted but the channel did not start).
	if a.agentLoop != nil {
		if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
			verb := "start"
			if !enabled {
				verb = "stop"
			}
			slog.Error("rest: channel reload after enable toggle failed",
				"channel", channelID, "enabled", enabled, "error", err)
			jsonErr(w, http.StatusInternalServerError,
				fmt.Sprintf("channel %s saved but failed to %s: %v", channelID, verb, err))
			return
		} else if !confirmed {
			verb := "start"
			if !enabled {
				verb = "stop"
			}
			slog.Warn("rest: channel reload after enable toggle did not confirm within the poll window; "+
				"channel may not yet have finished attempting to "+verb,
				"channel", channelID, "enabled", enabled)
		}
	}
	jsonOK(w, gen.ChannelEnabledResponse{Id: channelID, Enabled: enabled})
}

// channelSensitiveFields maps channel TYPE to their secret/credential field names.
// These are redacted in GET responses (replaced with "[configured]" if set).
// Keyed by base channel TYPE (not instance ID) because field sensitivity is
// type-level knowledge shared across all instances of that type
// (SEC-23 type-vs-instance boundary). Lookups use the base type extracted from
// the instance key via config.ParseInstanceKey (ADR-029 Gate 0).
var channelSensitiveFields = map[string][]string{
	"telegram":    {"token"},
	"discord":     {"token"},
	"slack":       {"bot_token", "app_token"},
	"feishu":      {"app_secret", "encrypt_key", "verification_token"},
	"matrix":      {"access_token", "crypto_passphrase"},
	"line":        {"channel_secret", "channel_access_token"},
	"dingtalk":    {"client_secret"},
	"qq":          {"app_secret"},
	"wecom":       {"secret"},
	"irc":         {"password", "nickserv_password", "sasl_password"},
	"weixin":      {"token"},
	"whatsapp":    {},
	"google-chat": {"webhook_url", "service_account_json"},
	// "email" is deliberately absent — see validChannelIDs.
}

// channelFilesystemPathFields is the set of ChannelInstanceConfig JSON keys that
// hold a filesystem path. They must never be settable through PUT
// /channels/{id}/configure: deleteChannelInstance does os.RemoveAll on the
// derived (or persisted) SessionStorePath, so an attacker-persisted path turns a
// later delete into arbitrary-directory deletion. These are deployment/operator
// concerns, not UI config — configureChannel strips them so the derived default
// is always used. Sourced from a grep of config.ChannelInstanceConfig for
// path-typed string fields (session_store_path, crypto_database_path,
// service_account_file). If a new path-typed field is added to that struct, add
// it here too.
var channelFilesystemPathFields = []string{
	"session_store_path",   // WhatsApp
	"crypto_database_path", // Matrix
	"service_account_file", // Google Chat
}

// channelRequiredFields maps channel base TYPE to fields that must be non-empty
// for the channel to work. Keyed by base TYPE for the same reason as
// channelSensitiveFields — required fields are type-level knowledge, not
// instance-specific. Lookups use config.ParseInstanceKey (ADR-029 Gate 0).
var channelRequiredFields = map[string][]string{
	"telegram":    {"token"},
	"discord":     {"token"},
	"slack":       {"bot_token", "app_token"},
	"feishu":      {"app_id", "app_secret"},
	"matrix":      {"homeserver", "user_id", "access_token"},
	"line":        {"channel_secret", "channel_access_token"},
	"dingtalk":    {"client_id", "client_secret"},
	"qq":          {"app_id", "app_secret"},
	"wecom":       {"bot_id", "secret"},
	"irc":         {"server", "nick"},
	"weixin":      {"token"},
	"whatsapp":    {},
	"google-chat": {},
	// "email" is deliberately absent — see validChannelIDs.
}

// redactChannelConfig returns a copy of cfg with sensitive fields replaced by a
// "[configured]" marker (never the real secret). Post-#289 the secret lives in
// the credential store and config holds only <field>_ref, so the marker is
// driven by the presence of the ref — this preserves the UI's
// secret-already-set indicator (the inline field is no longer present).
func redactChannelConfig(channelID string, cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}
	baseType, _ := config.ParseInstanceKey(channelID)
	for _, field := range channelSensitiveFields[baseType] {
		// A set <field>_ref means a credential is stored → mark configured.
		if ref, _ := out[field+"_ref"].(string); strings.TrimSpace(ref) != "" {
			out[field] = "[configured]"
			continue
		}
		// Legacy/pre-scrub inline value (should be transient): mask, never echo.
		if v, ok := out[field]; ok {
			if s, _ := v.(string); s != "" {
				out[field] = "[configured]"
			} else {
				out[field] = ""
			}
		}
	}
	return out
}

// getChannelConfig handles GET /api/v1/channels/{id}.
// Returns the channel's config with credential fields redacted.
func (a *restAPI) getChannelConfig(w http.ResponseWriter, channelID string) {
	chCfg, err := a.readChannelConfigRaw(channelID)
	if err != nil {
		slog.Error("rest: read config for channel get", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read config: %v", err))
		return
	}
	jsonOK(w, redactChannelConfig(channelID, chCfg))
}

// validateChannelConfigComplete reports whether the channel config that would
// result from persisting updates — plus prospectiveRefs, the <field>_ref
// values this request's sensitive fields WOULD resolve to once committed — on
// top of existing (the channel's currently-persisted raw config) satisfies
// channelRequiredFields[baseType]. Returns "" when complete, otherwise a
// "missing required fields: ..." message in the same format testChannel uses
// below (the SPA's mapErrorsToHumanLabels parses it).
//
// Stage 1 of the channel-Test redesign: the backend must prevent persisting
// an incomplete channel config, so callers run this BEFORE any
// credential-store write or config write — a rejection must be a true no-op.
// It deliberately checks <field>_ref PRESENCE, not credentialRefResolves — a
// save must not depend on an unlocked credential store; that liveness concern
// belongs to Test/activation (testChannel), not to "did the operator fill in
// the form".
func validateChannelConfigComplete(
	baseType string,
	existing, updates map[string]any,
	prospectiveRefs map[string]string,
) string {
	merged := make(map[string]any, len(existing)+len(updates)+len(prospectiveRefs))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range updates {
		merged[k] = v
	}
	for field, ref := range prospectiveRefs {
		merged[field+"_ref"] = ref
	}
	nonEmpty := func(key string) bool {
		s, _ := merged[key].(string)
		return strings.TrimSpace(s) != ""
	}

	// Google Chat's required config is either/or (mirrors testChannel's
	// special-case below): webhook mode needs webhook_url, bot mode needs
	// service_account_json or service_account_file. channelRequiredFields is a
	// flat AND-list and cannot express that.
	if baseType == "google-chat" {
		if nonEmpty("webhook_url_ref") || nonEmpty("service_account_json_ref") || nonEmpty("service_account_file") {
			return ""
		}
		return "missing required fields: configure webhook_url (webhook mode) or service_account_json / service_account_file (bot mode)"
	}

	sensitive := make(map[string]bool, len(channelSensitiveFields[baseType]))
	for _, f := range channelSensitiveFields[baseType] {
		sensitive[f] = true
	}
	var missing []string
	for _, field := range channelRequiredFields[baseType] {
		if sensitive[field] {
			// A secret required field is satisfied by its <field>_ref resolving
			// to a non-empty value — the secret never lives in config.json.
			if !nonEmpty(field + "_ref") {
				missing = append(missing, field)
			}
			continue
		}
		if !nonEmpty(field) {
			missing = append(missing, field)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("missing required fields: %s", strings.Join(missing, ", "))
}

// configureChannel handles PUT /api/v1/channels/{id}/configure.
// Merges the request body fields into the channel's config section (does not overwrite absent fields).
// Returns the updated channel config with credential fields redacted.
func (a *restAPI) configureChannel(w http.ResponseWriter, r *http.Request, channelID string) {
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var updates map[string]any
	if !decodeAndValidate(w, r, "ChannelConfigureRequest", &updates, validateEnabled) {
		return
	}
	// Remove reserved fields that must not be set here.
	delete(updates, "enabled")
	// instance_id is a URL/addressing hint in the request body; the {id} path
	// segment is the authoritative instance key in v0.1 (cap-1/type). Never
	// persist it as a config field (it is not part of ChannelInstanceConfig).
	delete(updates, "instance_id")

	// SECURITY (FINAL-REVIEW HIGH — membership-bypass): the request body schema is
	// additionalProperties:true and this handler blind-merges every remaining field
	// into the persisted ChannelInstanceConfig below. Two classes of field must
	// NEVER be settable through /configure or the merge becomes an authorization
	// and filesystem-integrity hole:
	//
	//   1. Workspace binding (workspace_id + identity). Persisting these here binds
	//      the instance to an arbitrary agent WITHOUT the FR-006 CoreTeam-membership
	//      check that lives only in setChannelRouting. An attacker could set
	//      workspace_id + identity{kind:agent,id:<non-member>} to route a
	//      workspace's inbound traffic at an agent that is not on its team
	//      (IsWorkspaceBound() would then be true and ResolveRoute would honor it).
	//      Binding MUST go through PUT /channels/{id}/routing, which validates the
	//      workspace, the agent, worker-ness, and CoreTeam membership. Reject
	//      (not silently strip) so a client using the wrong endpoint gets told.
	//
	//   2. Filesystem-path fields (session_store_path, crypto_database_path,
	//      service_account_file). deleteChannelInstance calls os.RemoveAll on the
	//      instance's SessionStorePath; a caller who persists an attacker-chosen
	//      path here turns a later delete into arbitrary-directory deletion. These
	//      paths are operator/deployment concerns, not UI-settable config — strip
	//      them so the derived-default path is always used.
	if _, present := updates["workspace_id"]; present {
		jsonErr(w, http.StatusBadRequest,
			"workspace_id cannot be set via configure; use PUT /api/v1/channels/{id}/routing")
		return
	}
	if _, present := updates["identity"]; present {
		jsonErr(w, http.StatusBadRequest,
			"identity cannot be set via configure; use PUT /api/v1/channels/{id}/routing")
		return
	}
	// Filesystem-path fields (grep of config.ChannelInstanceConfig for path-typed
	// fields: session_store_path [WhatsApp], crypto_database_path [Matrix],
	// service_account_file [Google Chat]). Stripped, not rejected — they are not
	// part of the UI configure surface, so a stray value is silently ignored
	// rather than failing an otherwise-valid save.
	for _, pathField := range channelFilesystemPathFields {
		delete(updates, pathField)
	}

	// SEC-23 / #289: route secret fields into the encrypted credential store and
	// persist only their <field>_ref in config.json. Every channel constructor
	// reads its secret via the *_ref (e.g. token_ref); an inline plaintext secret
	// is both a plaintext-at-rest violation AND unreadable by the constructor —
	// so a UI-configured token-based channel would never start.
	// Sensitive-field lookup uses the base type (ADR-029 Gate 0): per-instance
	// keys like "whatsapp.eu" use the same type-level field set as "whatsapp".
	chBaseType, _ := config.ParseInstanceKey(channelID)

	// Phase A — classify (zero I/O). Every present sensitive field is either a
	// "clear" (empty/whitespace value) or a "store" (non-empty value), recorded
	// as an action plus the prospective <field>_ref it would produce. Nothing
	// is written to the credential store or config.json here, so a rejection in
	// Phase B below leaves the save a true no-op.
	type sensitiveAction struct {
		field   string
		refName string // channelCredKey(channelID, field)
		secret  string // raw value to store; unused when clear
		clear   bool
	}
	var actions []sensitiveAction
	prospectiveRefs := make(map[string]string, len(channelSensitiveFields[chBaseType]))
	for _, field := range channelSensitiveFields[chBaseType] {
		raw, present := updates[field]
		if !present {
			continue
		}
		secret, isStr := raw.(string)
		if !isStr && raw != nil {
			// A non-string, non-null secret (e.g. {"token": 123}) would collapse to
			// "" and be misread as a clear — reject it instead of silently revoking.
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("%s must be a string", field))
			return
		}
		refName := channelCredKey(channelID, field)
		if strings.TrimSpace(secret) == "" {
			actions = append(actions, sensitiveAction{field: field, refName: refName, clear: true})
			prospectiveRefs[field] = ""
			continue
		}
		actions = append(actions, sensitiveAction{field: field, refName: refName, secret: secret})
		prospectiveRefs[field] = refName
	}

	// Phase B — validate BEFORE any I/O. The backend must prevent persisting an
	// incomplete channel config (Stage 1 of the channel-Test redesign): a save
	// that would leave the channel unable to construct is rejected here, before
	// any credential is stored or config.json is touched. existing is the
	// channel's currently-persisted raw config ({} for a not-yet-configured
	// instance); prospectiveRefs overlays what this request's sensitive fields
	// would resolve to if committed.
	existing, err := a.readChannelConfigRaw(channelID)
	if err != nil {
		slog.Error("rest: read config for channel configure validation", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read config: %v", err))
		return
	}
	if msg := validateChannelConfigComplete(chBaseType, existing, updates, prospectiveRefs); msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}

	// Phase C — execute. Validation passed: perform the credential-store writes
	// and the config-file merge (unchanged from the pre-restructure behavior).
	var clearedRefs []string // credentials to delete AFTER the config write commits
	for _, act := range actions {
		delete(updates, act.field) // never persist the inline plaintext
		refField := act.field + "_ref"
		if act.clear {
			// Clearing: drop the ref now, but delete the stored credential only
			// AFTER the config write commits (below). Deleting first would strand
			// the channel pointing at a missing credential if the config write fails.
			updates[refField] = ""
			clearedRefs = append(clearedRefs, act.refName)
			continue
		}
		if _, err := a.storeCredential(act.refName, act.secret); err != nil {
			slog.Error("rest: store channel credential", "channel", channelID, "field", act.field, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not store %s credential: %v", act.field, err))
			return
		}
		updates[refField] = act.refName
	}

	var updatedCh map[string]any
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		channels, _ := m["channels"].(map[string]any)
		if channels == nil {
			channels = map[string]any{}
			m["channels"] = channels
		}
		ch, _ := channels[channelID].(map[string]any)
		if ch == nil {
			ch = map[string]any{}
		}
		for k, v := range updates {
			ch[k] = v
		}
		// Persist the type discriminator: for a bare type key ("whatsapp") the
		// base type equals the key; for a namespaced instance key ("whatsapp.eu")
		// the type is the pre-dot segment (ADR-029 Gate 0 / FR-017).
		ch["type"] = chBaseType
		// Invariant (#289/SEC-23): a known secret field is never stored inline in
		// config.json — it lives only in the credential store via its <field>_ref.
		// This also scrubs any stale plaintext left by the pre-#289 blind merge.
		for _, field := range channelSensitiveFields[chBaseType] {
			delete(ch, field)
		}
		channels[channelID] = ch
		updatedCh = ch
		return nil
	}); err != nil {
		slog.Error("rest: configure channel", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Config (with cleared refs) is durable now — delete the now-unreferenced
	// credentials. A failure here only leaves an orphaned encrypted blob (the ref
	// is already gone), so log rather than fail the already-committed request.
	credentialCleanupFailed := false
	for _, refName := range clearedRefs {
		if err := a.removeStoredCredential(refName); err != nil {
			credentialCleanupFailed = true
			slog.Error("rest: delete cleared channel credential", "channel", channelID, "ref", refName, "error", err)
		}
	}

	// FINAL-REVIEW HIGH — configureChannel had no audit trail for a mutating,
	// credential-touching config write (unlike its sibling deleteChannelInstance,
	// which emits EventChannelInstanceDeleted). A configure call can create,
	// rotate, or clear stored encrypted secrets and change arbitrary instance
	// fields, so it MUST leave an audit trail even on the happy path.
	// updates' keys (never values) are logged — secrets have already been
	// stripped and replaced with *_ref entries by Phase C above.
	//
	// Cross-cutting interaction (intentional, fail-closed tradeoff — see the
	// matching note in pkg/gateway/gateway.go's executeReload, next to
	// "Cross-cutting interaction"): the safeUpdateConfigJSON write above
	// triggers an async config reload via the file-watcher (HotReload
	// defaults to true, pkg/config/defaults.go). executeReload's credential
	// check runs against ALL enabled channels, not just this one, so this
	// DecisionAllow audit entry (recorded because THIS handler's own write
	// succeeded) can be followed moments later by executeReload rejecting
	// the reload — and rolling back this channel's newly-saved config in
	// memory — because a DIFFERENT, unrelated enabled channel's credential
	// ref fails to resolve. There is no correlation ID between this audit
	// entry and that later rejection. This is desired behavior (fail closed
	// rather than silently run a channel with a broken credential), not a
	// bug: an operator who wants to know whether their save actually "stuck"
	// checks GET /health's reloadDegraded-driven 503 (surfaces
	// runningServices.reloadError) alongside the gateway logs, not this
	// audit event alone.
	if a.auditor != nil {
		decision := audit.DecisionAllow
		if credentialCleanupFailed {
			decision = audit.DecisionError
		}
		touchedFields := make([]string, 0, len(updates))
		for k := range updates {
			touchedFields = append(touchedFields, k)
		}
		sort.Strings(touchedFields)
		if err := a.auditor.Log(&audit.Entry{
			Event:    audit.EventChannelInstanceConfigured,
			Decision: decision,
			Details: map[string]any{
				"channel_id":     channelID,
				"type":           chBaseType,
				"fields":         touchedFields,
				"cleanup_failed": credentialCleanupFailed,
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", audit.EventChannelInstanceConfigured, "error", err)
		}
	}

	jsonOK(w, redactChannelConfig(channelID, updatedCh))
}

// testChannel handles POST /api/v1/channels/{id}/test.
// For v1.0: verifies required credential fields are configured without starting the channel.
func (a *restAPI) testChannel(w http.ResponseWriter, channelID string) {
	chCfg, err := a.readChannelConfigRaw(channelID)
	if err != nil {
		slog.Error("rest: read config for channel test", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read config: %v", err))
		return
	}

	// Use the base channel type for type-level field lookups (ADR-029 Gate 0).
	testBaseType, _ := config.ParseInstanceKey(channelID)

	// Google Chat's required config is either/or (see NewGoogleChatChannel):
	// webhook mode needs webhook_url, bot mode needs service_account_json or
	// service_account_file. channelRequiredFields is a flat AND-list and
	// cannot express that — leaving it {} let "Test" report success on a
	// completely blank instance. Give it its own branch instead of the
	// generic required-fields loop below.
	if testBaseType == "google-chat" {
		webhookRef, _ := chCfg["webhook_url_ref"].(string)
		hasWebhook, err := a.credentialRefResolves(webhookRef)
		if err != nil {
			slog.Error(
				"rest: channel test credential check",
				"channel",
				channelID,
				"field",
				"webhook_url",
				"error",
				err,
			)
			jsonOK(w, gen.ChannelTestResponse{
				Success: false,
				Message: "credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry",
			})
			return
		}
		saJSONRef, _ := chCfg["service_account_json_ref"].(string)
		hasSAJSON, err := a.credentialRefResolves(saJSONRef)
		if err != nil {
			slog.Error(
				"rest: channel test credential check",
				"channel",
				channelID,
				"field",
				"service_account_json",
				"error",
				err,
			)
			jsonOK(w, gen.ChannelTestResponse{
				Success: false,
				Message: "credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry",
			})
			return
		}
		saFile, _ := chCfg["service_account_file"].(string)
		hasSAFile := strings.TrimSpace(saFile) != ""

		if !hasWebhook && !hasSAJSON && !hasSAFile {
			jsonOK(w, gen.ChannelTestResponse{
				Success: false,
				Message: "missing required fields: configure webhook_url (webhook mode) or service_account_json / service_account_file (bot mode)",
			})
			return
		}
		jsonOK(w, gen.ChannelTestResponse{
			Success: true,
			Message: fmt.Sprintf("channel %q is configured", channelID),
		})
		return
	}

	required := channelRequiredFields[testBaseType]
	sensitive := make(map[string]bool, len(channelSensitiveFields[testBaseType]))
	for _, f := range channelSensitiveFields[testBaseType] {
		sensitive[f] = true
	}
	var missing []string
	for _, field := range required {
		if sensitive[field] {
			// #289: a secret required field is satisfied by its <field>_ref
			// resolving in the credential store, NOT by an inline value — the
			// secret never lives in config.json. Checking the inline field here
			// (the old behavior) made "Test" report success for channels that
			// could never activate.
			ref, _ := chCfg[field+"_ref"].(string)
			ok, err := a.credentialRefResolves(ref)
			if err != nil {
				// Store fault (locked / wrong key / I/O) — distinct from a missing
				// secret. Reporting it as "missing" would tell the user to re-enter
				// a credential that is already correct, so surface it as its own state.
				slog.Error("rest: channel test credential check", "channel", channelID, "field", field, "error", err)
				jsonOK(w, gen.ChannelTestResponse{
					Success: false,
					Message: "credential store unavailable — unlock it (set OMNIPUS_MASTER_KEY) and retry",
				})
				return
			}
			if !ok {
				missing = append(missing, field)
			}
			continue
		}
		if v, vOk := chCfg[field].(string); !vOk || v == "" {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		jsonOK(w, gen.ChannelTestResponse{
			Success: false,
			Message: fmt.Sprintf("missing required fields: %s", strings.Join(missing, ", ")),
		})
		return
	}
	jsonOK(w, gen.ChannelTestResponse{
		Success: true,
		Message: fmt.Sprintf("channel %q is configured", channelID),
	})
}

// countEnabledChannels returns the number of non-webchat channels currently enabled in cfg.
func countEnabledChannels(cfg *config.Config) int {
	count := 0
	for _, inst := range cfg.Channels {
		if inst.Enabled {
			count++
		}
	}
	return count
}

// --- Storage Stats ---

// HandleStorageStats handles GET /api/v1/storage/stats.
func (a *restAPI) HandleStorageStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var sessionCount int
	var workspaceSize int64
	var warnings []string
	// ADR-057 FR-092/FR-098: this only ever needed a COUNT, never the rows —
	// page.Total is the full merged sequence length computed before slicing
	// (session.SessionListPage's doc comment), so limit=1 avoids copying the
	// whole session set into this handler just to discard it. flat=true
	// preserves the pre-pagination "every session, every store" semantics.
	if page, partialErrs := a.agentLoop.ListAllSessions(1, 0, "", true); len(partialErrs) > 0 {
		for _, pe := range partialErrs {
			slog.Warn("rest: storage stats: list sessions partial error", "error", pe)
			warnings = append(warnings, sanitizePartialError(pe))
		}
		sessionCount = page.Total
	} else {
		sessionCount = page.Total
	}
	// Walk the home directory for workspace size.
	homeDir := a.homePath
	if err := filepath.Walk(homeDir, func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			if !os.IsNotExist(walkErr) {
				slog.Warn("rest: storage stats: walk error", "error", walkErr)
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		workspaceSize += info.Size()
		return nil
	}); err != nil {
		slog.Warn("rest: storage stats: walk failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("workspace size unavailable: %v", err))
	}

	resp := map[string]any{
		"workspace_size_bytes": workspaceSize,
		"session_count":        sessionCount,
		"memory_entry_count":   0,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	jsonOK(w, resp)
}

// --- File upload ---

const (
	// maxUploadFileSize is the per-file limit enforced via io.LimitReader.
	maxUploadFileSize int64 = 100 << 20 // 100 MB
)

// withUploadAuth is like withAuth but applies a 1 GB total body limit instead of
// the default 1 MB limit so that multi-file uploads can proceed. The per-file
// limit (100 MB) is enforced separately via io.LimitReader inside HandleUpload.
func (a *restAPI) withUploadAuth(handler http.HandlerFunc) http.HandlerFunc {
	return a.withAuthAndBodyLimit(handler, maxUploadFileSize*10)
}

// UploadedFile is the named type gen.UploadedFile, generated from the UploadedFile
// component schema in contracts/openapi.yaml (components/schemas/UploadedFile).
// oapi-codegen v2 generates it as a named struct that is referenced as the element
// type within gen.UploadFilesResponse.Files. Use gen.UploadedFile directly for
// struct literals.

// HandleUpload handles POST /api/v1/upload — streams multipart file uploads to disk.
// Files are stored at ~/.omnipus/uploads/{session_id}/{sanitized_filename}.
// Max file size per part: 100 MB. Data is streamed directly to disk; the full
// file is never buffered in memory.
//
// ADR-051 Rev 4 (FR-001): when the request carries a workspace_id (query param
// or form field before the file parts), files are routed to the workspace's
// persistent media library (workspaces/<ws>/media/) via library.Upload instead
// of the legacy session-scoped uploads dir. When no workspace_id is present,
// the legacy path is used unchanged (backward compat).
func (a *restAPI) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// session_id may come from either a query parameter or a form field that
	// appears before any file parts. We prefer the query param for simplicity.
	sessionID := r.URL.Query().Get("session_id")
	// workspace_id (ADR-051 Rev 4 FR-001) follows the same pattern: query
	// param first, form field as fallback before the file parts.
	workspaceID := r.URL.Query().Get("workspace_id")

	// Parse the multipart stream without buffering file content in memory.
	reader, err := r.MultipartReader()
	if err != nil {
		slog.Warn("rest: upload: multipart reader failed", "error", err)
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart request: %v", err))
		return
	}

	var resp gen.UploadFilesResponse

	// workspaceLib is resolved lazily on the first file part when workspace_id
	// is set. A nil workspaceLib means workspace routing is unavailable, so the
	// handler falls back to the legacy session-scoped path (graceful
	// degradation — the file still uploads, just not to the library).
	var workspaceLib *library.Library

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Warn("rest: upload: read part failed", "error", err)
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("multipart read error: %v", err))
			return
		}

		formName := part.FormName()
		fileName := part.FileName()

		// Non-file field — check for session_id / workspace_id overrides.
		if fileName == "" {
			if formName == "session_id" && sessionID == "" {
				buf, readErr := io.ReadAll(io.LimitReader(part, 256))
				part.Close()
				if readErr != nil {
					slog.Warn("rest: upload: read session_id field", "error", readErr)
					jsonErr(w, http.StatusBadRequest, "could not read session_id field")
					return
				}
				sessionID = strings.TrimSpace(string(buf))
			} else if formName == "workspace_id" && workspaceID == "" {
				buf, readErr := io.ReadAll(io.LimitReader(part, 256))
				part.Close()
				if readErr != nil {
					slog.Warn("rest: upload: read workspace_id field", "error", readErr)
					jsonErr(w, http.StatusBadRequest, "could not read workspace_id field")
					return
				}
				workspaceID = strings.TrimSpace(string(buf))
			} else {
				// Discard unrecognized non-file fields.
				if _, discardErr := io.Copy(io.Discard, part); discardErr != nil {
					slog.Warn("rest: upload: discard field failed", "field", formName, "error", discardErr)
				}
				part.Close()
			}
			continue
		}

		// --- Workspace media library path (ADR-051 Rev 4, FR-001) ---
		//
		// When a workspace_id is present and the workspace library can be
		// resolved, stream the file directly into the workspace's persistent
		// media library (workspaces/<ws>/media/) via library.Upload instead
		// of the legacy session-scoped uploads dir. The library handles
		// filename normalization, MIME sniffing, sha256, the per-file size
		// limit (100 MB), and an atomic write+manifest commit — so this path
		// does not need to replicate any of that.
		if workspaceID != "" {
			if err := validateEntityID(workspaceID); err != nil {
				part.Close()
				jsonErr(w, http.StatusBadRequest, "invalid workspace_id")
				return
			}
			if workspaceLib == nil && a.agentLoop != nil {
				workspaceLib = a.agentLoop.GetWorkspaceLibrary(workspaceID)
				if workspaceLib == nil {
					// GetWorkspaceLibrary (pkg/agent/media_present.go) collapses
					// two very different conditions into the same nil signal:
					// "workspace library genuinely not configured" and "library
					// exists but failed to load" (corrupt manifest, permission
					// error, disk I/O). Silently falling back to the legacy
					// session-scoped path here would mask a real failure behind
					// a plausible 201 — the file would never appear in
					// GET /workspaces/{id}/media, get no refcount/orphan-GC/
					// cascade-delete coverage, and never resolve via
					// media://workspace/... . Re-derive the real cause the same
					// way rest_workspace_media.go's openLibraryForWorkspace
					// does: call library.New directly. It NEVER returns
					// (nil, nil) — every call either succeeds with a valid
					// *Library or fails with a concrete error (verified by
					// reading library.New's full body) — so this
					// deterministically separates the two cases without
					// needing to change GetWorkspaceLibrary's signature (out
					// of scope here: pkg/agent/).
					lib, libErr := library.New(a.homePath, workspaceID)
					if libErr != nil {
						part.Close()
						// Re-review FIX 1: was a bare slog.Error, invisible on a
						// backgrounded gateway (slog.SetDefault is never called
						// anywhere in this repo, so log/slog.Default() never
						// reaches $OMNIPUS_HOME/logs/gateway.log). Route through
						// pkg/logger instead.
						logger.ErrorCF("rest", "upload: workspace library load failed",
							map[string]any{"workspace_id": workspaceID, "error": libErr})
						jsonErr(w, http.StatusInternalServerError,
							fmt.Sprintf("workspace media library unavailable: %v", libErr))
						return
					}
					workspaceLib = lib
				}
			}
			if workspaceLib != nil {
				ref, projection, uploadErr := workspaceLib.Upload(fileName, gen.UserUpload, part)
				part.Close()
				if uploadErr != nil {
					slog.Error("rest: upload: workspace library store failed",
						"workspace_id", workspaceID, "filename", fileName, "error", uploadErr)
					// Remove previously uploaded workspace files in this batch.
					a.cleanupWorkspaceUploads(&resp, workspaceLib)
					switch {
					case errors.Is(uploadErr, library.ErrFileTooLarge):
						jsonErr(w, http.StatusRequestEntityTooLarge,
							fmt.Sprintf("file %q exceeds 100 MB limit", fileName))
					case errors.Is(uploadErr, library.ErrInvalidFilename):
						jsonErr(w, http.StatusBadRequest,
							fmt.Sprintf("invalid filename: %q", fileName))
					default:
						jsonErr(w, http.StatusInternalServerError,
							fmt.Sprintf("workspace media store failed: %v", uploadErr))
					}
					return
				}

				var size int64
				if projection.Size != nil {
					size = *projection.Size
				}
				mimeStr := ""
				if projection.Mime != nil {
					mimeStr = *projection.Mime
				}
				// Relative path for the response — informational; the SPA
				// serves workspace media via the media://workspace/ ref, not
				// the /api/v1/uploads/{session_id}/{filename} URL.
				_, mediaID, _ := media.ParseWorkspaceRef(ref)
				relativePath := filepath.Join("workspaces", workspaceID, "media", mediaID)

				// D-1 (library-spec, 2026-07-29 UAT): the media-library blob
				// above lives in workspaces/<id>/media/ — a SIBLING of work/,
				// structurally unreachable by every agent file tool (they
				// open an os.Root at work/ and cannot escape it by
				// construction, ADR-046). Dual-write the SAME bytes as a
				// real, named file inside workspaces/<id>/work/.library/ so
				// library_read/read_file can actually find it — this is the
				// single change that makes the agent-visibility requirement
				// satisfiable. The media-library entry above remains the
				// metadata index (mime/size/sha256/refcount/source); this is
				// purely an additional copy, never a replacement. A failure
				// here means the upload as a whole does NOT satisfy "the
				// agent can read this file", so it is treated as a hard
				// upload failure — the just-created library entry is rolled
				// back rather than left as a half-satisfied promise.
				workRelPath, stageErr := a.stageWorkspaceUploadCopy(workspaceID, mediaID, fileName, workspaceLib)
				if stageErr != nil {
					logger.ErrorCF("rest", "upload: could not stage workspace library copy for agent access",
						map[string]any{
							"workspace_id": workspaceID, "media_id": mediaID,
							"filename": fileName, "error": stageErr.Error(),
						})
					if _, delErr := workspaceLib.Delete(mediaID); delErr != nil {
						slog.Warn("rest: upload: rollback of media-library entry failed after stage failure",
							"media_id", mediaID, "error", delErr)
					}
					a.cleanupWorkspaceUploads(&resp, workspaceLib)
					jsonErr(w, http.StatusInternalServerError,
						fmt.Sprintf("could not stage uploaded file for agent access: %v", stageErr))
					return
				}
				agent.RecordUploadWorkPath(ref, workRelPath)

				var refPtr *string
				if ref != "" {
					refCopy := ref
					refPtr = &refCopy
				}
				resp.Files = append(resp.Files, gen.UploadedFile{
					ContentType: mimeStr,
					Name:        projection.Filename,
					Path:        relativePath,
					Ref:         refPtr,
					Size:        size,
				})

				slog.Info(
					"rest: upload: file stored in workspace library",
					"workspace_id", workspaceID,
					"filename", projection.Filename,
					"size", size,
					"content_type", mimeStr,
					"media_ref", ref,
					"work_path", workRelPath,
				)
				continue
			}
			// workspace_id set but library unavailable → fall through to the
			// legacy session-scoped path (graceful degradation).
			slog.Warn("rest: upload: workspace library unavailable, falling back to session-scoped path",
				"workspace_id", workspaceID)
		}

		// --- Legacy session-scoped path ---

		// Validate session_id before the first file write.
		if sessionID == "" {
			part.Close()
			jsonErr(w, http.StatusBadRequest, "session_id is required (query param or form field before files)")
			return
		}
		if err := validateEntityID(sessionID); err != nil {
			part.Close()
			jsonErr(w, http.StatusBadRequest, "invalid session_id")
			return
		}

		// Sanitize the filename: strip directory components, reject empty result.
		sanitized := filepath.Base(filepath.Clean("/" + fileName))
		if sanitized == "" || sanitized == "." || sanitized == "/" {
			part.Close()
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid filename: %q", fileName))
			return
		}
		// Additional safety: reject null bytes.
		if strings.ContainsRune(sanitized, 0) {
			part.Close()
			jsonErr(w, http.StatusBadRequest, "filename contains null byte")
			return
		}

		uploadDir := filepath.Join(a.homePath, "uploads", sessionID)
		if mkErr := os.MkdirAll(uploadDir, 0o700); mkErr != nil {
			part.Close()
			slog.Error("rest: upload: mkdir failed", "dir", uploadDir, "error", mkErr)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not create upload directory: %v", mkErr))
			return
		}

		// Use O_CREATE|O_EXCL to atomically create the destination file.
		// If another concurrent upload or replay already created a file with
		// the same name (TOCTOU-safe: Stat+Create would race), retry once
		// with a nanosecond suffix to guarantee uniqueness.
		destPath := filepath.Join(uploadDir, sanitized)
		f, createErr := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil && os.IsExist(createErr) {
			ext := filepath.Ext(sanitized)
			base := strings.TrimSuffix(sanitized, ext)
			sanitized = fmt.Sprintf("%s_%d%s", base, time.Now().UnixNano(), ext)
			destPath = filepath.Join(uploadDir, sanitized)
			f, createErr = os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		}

		// Read Content-Type before closing the part.
		contentType := part.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		// cleanupUploaded removes all previously uploaded files on error.
		cleanupUploaded := func() {
			for _, prev := range resp.Files {
				os.Remove(filepath.Join(a.homePath, prev.Path))
			}
		}

		if createErr != nil {
			part.Close()
			slog.Error("rest: upload: create file failed", "path", destPath, "error", createErr)
			cleanupUploaded()
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not create file: %v", createErr))
			return
		}

		// Enforce per-file size limit. If the limit is exceeded, io.Copy returns
		// an error because LimitReader returns 0 bytes after the limit and the
		// copy stops, but to make the violation explicit we detect it below.
		limitedPart := io.LimitReader(part, maxUploadFileSize+1)
		written, copyErr := io.Copy(f, limitedPart)
		f.Close()
		part.Close()

		if copyErr != nil {
			slog.Error("rest: upload: copy failed", "path", destPath, "error", copyErr)
			if rmErr := os.Remove(destPath); rmErr != nil && !os.IsNotExist(rmErr) {
				slog.Warn("rest: upload: remove partial file failed", "path", destPath, "error", rmErr)
			}
			cleanupUploaded()
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("file write failed: %v", copyErr))
			return
		}

		if written > maxUploadFileSize {
			if rmErr := os.Remove(destPath); rmErr != nil && !os.IsNotExist(rmErr) {
				slog.Warn("rest: upload: remove oversized file failed", "path", destPath, "error", rmErr)
			}
			cleanupUploaded()
			jsonErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file %q exceeds 100 MB limit", sanitized))
			return
		}

		// Relative path for the response — callers use this to construct the
		// /api/v1/uploads/{session_id}/{filename} URL.
		relativePath := filepath.Join("uploads", sessionID, sanitized)

		// #254: register the uploaded file in the media store so it gets a
		// media:// ref. The SPA echoes this ref back in the message frame's
		// "media" array; the agent loop then threads it into the LLM content
		// array as a multimodal content block so the agent can see the file.
		// CleanupPolicyForgetOnly: the uploads dir is operator-visible data —
		// the media store must never auto-delete the file. Registration failure
		// is non-fatal: the file is still downloadable via path, the agent just
		// won't see it inline.
		// #254 stale media store fix: always fetch the current store via the
		// agent loop so uploads survive a restartServices store swap. Fall back
		// to a.mediaStore only when the agent loop is not yet wired (e.g. tests
		// that construct a restAPI with a direct mediaStore but no agentLoop).
		var ref string
		store := a.agentLoop.GetMediaStore()
		if store == nil {
			store = a.mediaStore
		}
		if store != nil {
			var storeErr error
			ref, storeErr = store.Store(destPath, media.MediaMeta{
				Filename:      sanitized,
				ContentType:   contentType,
				Source:        "upload:webchat",
				CleanupPolicy: media.CleanupPolicyForgetOnly,
			}, "upload:"+sessionID)
			if storeErr != nil {
				slog.Warn("rest: upload: media store registration failed",
					"path", destPath, "error", storeErr)
				ref = ""
			}
		}

		slog.Info(
			"rest: upload: file stored",
			"session_id", sessionID,
			"filename", sanitized,
			"size", written,
			"content_type", contentType,
			"media_ref", ref,
		)

		var refPtr *string
		if ref != "" {
			refCopy := ref
			refPtr = &refCopy
		}
		resp.Files = append(resp.Files, gen.UploadedFile{
			ContentType: contentType,
			Name:        sanitized,
			Path:        relativePath,
			Ref:         refPtr,
			Size:        written,
		})
	}

	if len(resp.Files) == 0 {
		jsonErr(w, http.StatusBadRequest, "no files found in upload")
		return
	}

	jsonCreated(w, resp)
}

// cleanupWorkspaceUploads removes previously uploaded workspace library files
// from this batch when a later file in the same request fails. The failing
// file's own cleanup is handled transactionally by library.Upload; this only
// removes files that already succeeded. Best-effort — errors are logged.
// stageWorkspaceUploadCopy performs the D-1 (library-spec) dual-write: it
// reads back the just-uploaded file's bytes from the workspace media
// library (lib.Read, which sha256-verifies on read) and writes them a
// SECOND time into the SAME workspace's work/.library/ directory — a real,
// named file inside the os.Root every agent file tool is confined to
// (ADR-046), unlike workspaces/<id>/media/ which is a sibling those tools
// cannot reach by construction. De-duplicates a filename collision with a
// human-readable " (N)" numeric suffix, mirroring the legacy session-scoped
// upload path's own collision handling further down in HandleUpload.
//
// Returns the workspace-relative announced path (".library/<name>",
// agent.LibraryDirName()-prefixed) on success. On any failure, the
// caller MUST treat the whole upload as failed (see HandleUpload's use)
// rather than leaving a media-library entry the agent still cannot read —
// stageWorkspaceUploadCopy itself removes any partially-written destination
// file before returning an error, so the caller does not need to.
func (a *restAPI) stageWorkspaceUploadCopy(
	workspaceID, mediaID, filename string,
	lib *library.Library,
) (string, error) {
	sanitized, err := agent.SanitizeUploadFilename(filename)
	if err != nil {
		return "", fmt.Errorf("sanitize filename: %w", err)
	}
	workDir, err := wkspace.SafeWorkDir(a.homePath, workspaceID)
	if err != nil {
		return "", fmt.Errorf("resolve workspace work dir: %w", err)
	}
	libraryDir := filepath.Join(workDir, agent.LibraryDirName())
	if mkErr := os.MkdirAll(libraryDir, 0o700); mkErr != nil {
		return "", fmt.Errorf("create workspace library dir: %w", mkErr)
	}

	// Read back the FULL, sha256-verified bytes rather than tee-ing the
	// original multipart reader — the multipart part is already fully
	// consumed by lib.Upload above by the time this is called, and
	// re-reading through the library's own integrity-checked Read keeps this
	// function's contract simple (one clear source of truth for "what did we
	// actually store") at the cost of one extra full read, bounded by the
	// same 100 MB library.MaxFileSize every upload is already capped at.
	data, _, err := lib.Read(mediaID)
	if err != nil {
		return "", fmt.Errorf("read back uploaded bytes: %w", err)
	}

	ext := filepath.Ext(sanitized)
	base := strings.TrimSuffix(sanitized, ext)
	const maxDedupAttempts = 1000

	destName := sanitized
	var destFile *os.File
	var destPath string
	for attempt := 1; ; attempt++ {
		destPath = filepath.Join(libraryDir, destName)
		f, openErr := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			destFile = f
			break
		}
		if !os.IsExist(openErr) {
			return "", fmt.Errorf("create workspace library file: %w", openErr)
		}
		if attempt > maxDedupAttempts {
			return "", fmt.Errorf("too many filename collisions for %q in workspace library", sanitized)
		}
		destName = fmt.Sprintf("%s (%d)%s", base, attempt, ext)
	}

	// keepFile flips to true only once the write AND close both succeed —
	// any earlier return leaves it false, so the deferred cleanup below
	// removes the just-created (possibly empty or partially-written) file
	// rather than stranding it.
	keepFile := false
	defer func() {
		if !keepFile {
			if rmErr := os.Remove(destPath); rmErr != nil && !os.IsNotExist(rmErr) {
				logger.WarnCF("rest", "upload: cleanup partial workspace library file failed",
					map[string]any{"path": destPath, "error": rmErr.Error()})
			}
		}
	}()

	if _, writeErr := destFile.Write(data); writeErr != nil {
		destFile.Close()
		return "", fmt.Errorf("write workspace library file: %w", writeErr)
	}
	if closeErr := destFile.Close(); closeErr != nil {
		return "", fmt.Errorf("close workspace library file: %w", closeErr)
	}
	keepFile = true

	return agent.LibraryDirName() + "/" + destName, nil
}

func (a *restAPI) cleanupWorkspaceUploads(resp *gen.UploadFilesResponse, lib *library.Library) {
	if lib == nil {
		return
	}
	for _, prev := range resp.Files {
		if prev.Ref == nil || !strings.HasPrefix(*prev.Ref, "media://workspace/") {
			continue
		}
		_, mediaID, ok := media.ParseWorkspaceRef(*prev.Ref)
		if !ok {
			continue
		}
		if _, err := lib.Delete(mediaID); err != nil {
			slog.Warn("rest: upload: cleanup workspace file failed",
				"media_id", mediaID, "error", err)
		}
	}
}

// HandleServeUpload serves uploaded files for display in chat.
// GET /api/v1/uploads/{session_id}/{filename}
// Authentication is optional — browsers must be able to load image URLs directly.
func (a *restAPI) HandleServeUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract session_id and filename from the URL path.
	// Pattern: /api/v1/uploads/{session_id}/{filename}
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/v1/uploads/")
	trimmed = strings.TrimPrefix(trimmed, "/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		jsonErr(w, http.StatusBadRequest, "path must be /api/v1/uploads/{session_id}/{filename}")
		return
	}

	sessionID := parts[0]
	filename := parts[1]

	if err := validateEntityID(sessionID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid session_id")
		return
	}

	// Sanitize filename — reject anything with path separators or "..".
	if strings.ContainsAny(filename, "/\\") || strings.Contains(filename, "..") || strings.ContainsRune(filename, 0) {
		jsonErr(w, http.StatusBadRequest, "invalid filename")
		return
	}

	filePath := filepath.Join(a.homePath, "uploads", sessionID, filename)

	// Defense-in-depth: resolve symlinks and confirm the real path is still inside
	// the uploads directory. EvalSymlinks also returns an error if the file does
	// not exist, which naturally produces the 404 case below.
	uploadsRoot, _ := filepath.EvalSymlinks(filepath.Join(a.homePath, "uploads"))
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "file not found")
		return
	}
	if !strings.HasPrefix(resolved, uploadsRoot+string(filepath.Separator)) {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, resolved)
}

// --- Media ---

// HandleMedia serves a legacy global media file by its ref ID extracted from
// the URL path (e.g. /api/v1/media/abc123 resolves "media://abc123").
func (a *restAPI) HandleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.setCORSHeaders(w, r)

	refID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/media/"), "/")
	if refID == "" || strings.ContainsAny(refID, "/\\") || strings.Contains(refID, "..") {
		jsonErr(w, http.StatusBadRequest, "invalid media ref")
		return
	}

	a.serveMedia(w, r, "media://"+refID, media.ResolveOpts{}, refID)
}

// HandleMediaByRef serves workspace-library media through the split path shape
// /api/v1/media/workspace/{workspace}/{id}; the split keeps each path segment
// independently validated while preserving the opaque media ref for resolution.
func (a *restAPI) HandleMediaByRef(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.setCORSHeaders(w, r)

	const prefix = "/api/v1/media/workspace/"
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) != 2 || validateEntityID(parts[0]) != nil || validateEntityID(parts[1]) != nil {
		jsonErr(w, http.StatusBadRequest, "invalid media ref")
		return
	}

	workspaceID, mediaID := parts[0], parts[1]
	ref := media.WorkspaceRefPrefix + workspaceID + "/" + mediaID
	a.serveMedia(w, r, ref, media.WithCallerWorkspace(workspaceID), ref)
}

func (a *restAPI) serveMedia(
	w http.ResponseWriter,
	r *http.Request,
	ref string,
	opts media.ResolveOpts,
	logRef string,
) {
	store := a.agentLoop.GetMediaStore()
	if store == nil {
		store = a.mediaStore
	}
	if store == nil {
		jsonErr(w, http.StatusServiceUnavailable, "media store not available")
		return
	}

	localPath, meta, err := store.ResolveWithMetaOpts(ref, opts)
	if err != nil {
		if errors.Is(err, media.ErrCrossWorkspaceRef) || errors.Is(err, library.ErrWorkspaceMismatch) {
			slog.Warn("rest: media ref not found", "ref", logRef, "error", err.Error())
			jsonErr(w, http.StatusForbidden, "media access denied")
			return
		}
		if errors.Is(err, library.ErrEntryStranded) {
			// See rest_workspace_media.go's handleWorkspaceMediaGet/Delete for
			// the identical branch and its full rationale: ErrEntryStranded
			// means the manifest still claims the entry is present while its
			// bytes are actually quarantined at an internal path — a
			// server-side data-integrity failure, not a routine absent ref.
			// Folding it into the catch-all below would report it as 404
			// ("this ref never existed"), which is a lie; 500 with an
			// attributable message keeps this path coherent with the
			// workspace-media handlers' mapping for the same sentinel.
			//
			// library.ErrIntegrityCheckFailed (sha256 mismatch) deliberately
			// gets no analogous branch here: it is only ever returned by
			// Library.Read/ResolveWithWorkspace (the bytes-returning,
			// integrity-checked path), never by ResolvePathWithCaller (the
			// path-only resolver this handler's workspace-ref route reaches
			// through FileMediaStore.resolveWorkspaceRef) or by the legacy
			// registry lookup the non-workspace route uses — so it cannot
			// reach this catch in practice. Should the resolution path ever
			// change to route through the integrity-checked reader, this
			// error deserves the same non-404 treatment as ErrEntryStranded.
			slog.Error("rest: media: entry stranded (manifest/disk diverged)", "ref", logRef, "error", err.Error())
			jsonErr(w, http.StatusInternalServerError, "media entry is in an inconsistent state")
			return
		}
		if errors.Is(err, media.ErrNotFound) || errors.Is(err, library.ErrNotFound) {
			// Both sentinels mean the same thing at two different layers:
			// media.ErrNotFound is FileMediaStore's own "no provider/resolver
			// wired, or the ref is absent from the legacy global registry"
			// (see FileMediaStore.resolveWorkspaceRef/resolveLegacyWithMeta);
			// library.ErrNotFound is the owning workspace library reporting
			// its manifest has no such id (Library.ResolvePathWithCaller).
			// Either way this is a genuine, routine absent ref — 404 is
			// correct and matches the workspace-media handlers' own mapping
			// for the same library.ErrNotFound sentinel
			// (rest_workspace_media.go's handleWorkspaceMediaGet/Delete).
			slog.Warn("rest: media ref not found", "ref", logRef, "error", err.Error())
			jsonErr(w, http.StatusNotFound, "media not found")
			return
		}
		// Anything else is a genuine resolution FAILURE, not a routine
		// absent ref — most notably FileMediaStore.resolveWorkspaceRef's
		// "workspace library %q unavailable: %w" when a wired provider
		// itself errors (disk/library-open failure). Collapsing that into
		// the same 404 the block above returns would report "this media
		// never existed" for what is actually "the server could not check".
		// media.ErrNotFound is deliberately NEVER used to wrap that error —
		// see its doc comment — so this catch-all is unreachable for a
		// routine absent ref and only fires on a real backend fault.
		slog.Error("rest: media: resolve failed", "ref", logRef, "error", err.Error())
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	if meta.ContentType != "" {
		h.Set("Content-Type", meta.ContentType)
	}
	if meta.Filename != "" {
		h.Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", meta.Filename))
	}
	http.ServeFile(w, r, localPath)
}
