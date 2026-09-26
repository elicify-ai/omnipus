// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/plan"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	// onboardingStateUnknown records that system/state.json existed at boot
	// but could NOT be read or parsed, so this instance's onboarding status
	// is genuinely unknown rather than "fresh install" (M3). It is sampled in
	// gateway.go immediately BEFORE onboarding.NewManager, because the
	// manager renames a corrupt state file aside and resets to the fresh
	// install zero value — after that the ambiguity is unobservable. The
	// FR-050 pre-auth window treats unknown as CLOSED; see
	// preAuthOnboardingWindowOpen (rest_auth.go). False in test
	// constructions, which is correct: they have no corrupt state file.
	onboardingStateUnknown bool
	// copilotProbe bounds the cost of the GitHub Copilot sign-in probe (C2):
	// one concurrent vendor-CLI exec at a time, and one premium request per
	// cache TTL rather than one per HTTP call. Zero value is ready; see
	// copilotProbeGuard (rest_signin_copilot.go).
	copilotProbe copilotProbeGuard
	homePath     string              // ~/.omnipus — root of the data directory
	configMu     sync.Mutex          // guards safeUpdateConfigJSON (read-modify-write cycle)
	taskStore    *task.Store         // unified task persistence
	taskExecutor *agent.TaskExecutor // task execution engine
	// liveTaskActivity (founder decision 2026-09-14) is the read seam
	// Task.last_activity_at is stamped from: the live progress stamp of a
	// running task's turn (advancing on streamed reasoning as well as
	// tool-call deltas). Normally the shared TaskExecutor (wired at boot via
	// its SetLiveTaskActivitySource); overridable per-test. Nil is valid and
	// common in test constructions — the stamp is then simply absent.
	liveTaskActivity LiveTaskActivityReader
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
	// bedrockControlPlaneBaseOverride is issue #800's own follow-up (live
	// AWS ListInferenceProfiles refresh, rest_providers.go): a test seam
	// for the Bedrock control-plane base URL (httptest.Server), empty in
	// every production path — production always derives
	// bedrock.ControlPlaneEndpoint(region).
	bedrockControlPlaneBaseOverride string
	// bedrockRuntimeBaseOverride is a TEST-ONLY seam (orchestrator review
	// round 2, D1), empty in every production path: when set, the onboarding
	// probe (HandleOnboardingProbeProvider) and the PUT-triggered save-time
	// key check (providerPutValidateKey) build the Bedrock RUNTIME (Converse)
	// base as this value + "/" + the resolved region, instead of
	// bedrock.RegionalEndpoint(region) — production always derives the real
	// bedrock-runtime.<region>.amazonaws.com host. An httptest.Server's own
	// host cannot BE a per-region AWS DNS name, so this lets a test's fake
	// server observe which region a probe actually resolved to via the
	// request PATH (r.URL.Path's leading segment), the same way
	// bedrockControlPlaneBaseOverride above tests the control-plane call.
	bedrockRuntimeBaseOverride string
	// entitlements is the ADR-067 FR-021 "Check with my account" cache:
	// one annotated model list per (provider, credential ref NAME) for the
	// life of the process, evicted on provider DELETE, on a key-changing
	// PUT and on a catalog refresh. Its zero value is ready to use — see
	// rest_providers_entitlement.go.
	entitlements entitlementCache

	// signInMu guards signInSessions, the in-process store of open
	// device-code sign-in sessions (ADR-068 FR-008/FR-044, T068-14) — AND
	// every field of every *deviceCodeSession the map holds. No session
	// pointer ever escapes the lock: rest_sign_in.go's accessors return
	// value copies and mutate only inside the critical section. Read the
	// CONCURRENCY CONTRACT block above putDeviceSession there before
	// touching either field; handing the live map out is what made
	// GET .../sign-in/status a pre-auth process-killing fatal. Lazily
	// initialized on first put — a bare restAPI{} test literal that never
	// exercises the sign-in routes need not know this field exists.
	// Single-gateway-process
	// in-memory state is sufficient: a device-code session's own ceiling is
	// 15 minutes (FR-044) and Omnipus is a single Go binary, not a
	// horizontally-scaled fleet.
	signInMu       sync.Mutex
	signInSessions map[string]*deviceCodeSession
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

	// libraryChangeBroadcast is the D-107 cross-tab listing-invalidation hook:
	// the Library REST write handlers call emitLibraryChange
	// (library_change_broadcast.go) after a mutation lands, which fans a
	// library_changed WS frame out through the chat WS handler so every OTHER
	// connected tab drops its stale folder listing. A func-in-pointer rather
	// than a *WSHandler field for the same reason previewTokens is one: the
	// write handlers live in files that never see the WS route registrar, and
	// a nil value (unwired tests, partial boots) must degrade to a no-op —
	// wired in gateway.go right after this struct is built, once wsHandler
	// exists.
	libraryChangeBroadcast atomic.Pointer[func(gen.LibraryChangedFrame)]

	// previewTokens is the live ADR-067 preview-token store (rest_library_preview.go),
	// published by newLibraryPreviewRoutes at registration time.
	//
	// It lives on the restAPI, and not in a package variable, because FR-003d's
	// three revocation events are handled in three OTHER files —
	// HandleLogout (rest_auth.go), handleWorkspaceMountDelete
	// (rest_workspace_mounts.go) and the Library delete/rename/move handlers
	// (rest_library.go) — none of which ever sees the route registrar. Without a
	// reachable handle every one of them silently degrades to "expiry is the only
	// revocation", which FR-003d names as the omission that turns a preview token
	// into a standing unauthenticated read grant.
	//
	// Nil until the preview routes are registered; every reader goes through
	// previewTokenStore(), which is nil-safe.
	previewTokens atomic.Pointer[PreviewTokenStore]

	// mailPreviewTokens is the Mail HTML-preview token store (rest_mail_preview.go,
	// MC-43), published by newMailPreviewRoutes at registration time and read by
	// logout revocation. Nil until registered; readers go through
	// mailPreviewTokenStoreOf(), which is nil-safe.
	mailPreviewTokens atomic.Pointer[mailPreviewTokenStore]

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
	//
	// These fields are upstream's own local-mode state (ADR-0010 WP2): harmless
	// in platform mode, where requireReAuth short-circuits to true before ever
	// touching the store (see requireReAuth's doc comment, rest_integrations_auth.go).
	reauthOnce sync.Once
	reauth     *reauthStore

	// corsAllowHeaders' one-time composition (auth_mode.go): the mode never
	// changes after boot, so the header string is computed on first use.
	corsAllowHeadersOnce  sync.Once
	corsAllowHeadersValue string

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
	w.Header().Set("Access-Control-Allow-Headers", a.corsAllowHeaders())
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

// agentModelParamsInput is a request-shape-agnostic normalization of the
// wire model_params object.
// gen.AgentCreateRequestMain, gen.AgentCreateRequestSubagent, and
// gen.AgentUpdateRequest each generate their own anonymous ModelParams
// struct (none $refs AgentModelParams.yaml — oapi-codegen inlines
// model_params separately per schema), but all three are structurally
// identical (same field names/types/tags/order), so one non-generic helper
// below handles every call site.
//
// top_p (T2): removed from the wire entirely — no provider adapter in this
// codebase implements nucleus sampling and there was no agents.defaults
// equivalent to fall back to, so it never had anywhere to go once
// commit 2b057e15 (Q1) started actually persisting model_params instead of
// silently dropping the whole object. See rest.go's raw-body top_p sniff on
// updateAgent (mirrors the sandbox_profile/delegation_policy precedent) and
// AgentCreateRequest{Main,Subagent}'s unconditional DisallowUnknownFields
// decode for how a client still sending top_p is rejected now.
type agentModelParamsInput struct {
	Temperature *float64
	MaxTokens   *int
}

// restAPIRegisterAdditionalEndpoints carries the shared state of registerAdditionalEndpoints across its stages.
type restAPIRegisterAdditionalEndpoints struct {
	a  *restAPI
	cm httpHandlerRegistrar
}

// registerAdditionalEndpoints registers handlers for endpoints the frontend calls.
// Each returns a valid JSON response matching the shape the frontend expects,
// preventing "Unexpected token '<'" errors from the SPA catch-all.
func (a *restAPI) registerAdditionalEndpoints(cm httpHandlerRegistrar) {
	rae := &restAPIRegisterAdditionalEndpoints{a: a, cm: cm}

	rae.registerCoreRoutes()

	rae.registerSettingsAndAccountRoutes()

	rae.registerAncillaryRoutes()
}

// registerCoreRoutes registers state, system, task, workspace, library, provider, tool, channel, agent, mailbox, token, and activity routes.
func (rae *restAPIRegisterAdditionalEndpoints) registerCoreRoutes() {
	rae.cm.RegisterHTTPHandler("/api/v1/state", rae.a.withOptionalAuth(rae.a.HandleState))
	rae.cm.RegisterHTTPHandler("/api/v1/system/cli-detect", rae.a.withAuth(rae.a.HandleSystemCliDetect))
	// withAuth, never withOptionalAuth: this lists the operator's own disk.
	rae.cm.RegisterHTTPHandler("/api/v1/system/folders", rae.a.withAuth(rae.a.HandleSystemFolders))
	// POST /api/v1/system/cli-validate — spawns a caller-supplied path
	// (<cli> --version), so it is hardened as a privileged diagnostic (ADR-030
	// §11 F-01). CREATE-PARITY auth: plain withAuth, exactly like createAgent /
	// HandleAgents below — NOT admin, NOT RequireNotBypass (anyone who can add
	// the subagent can validate it). A DEDICATED rate limiter (cliValidateLimiter,
	// distinct from validateLimiter) throttles the spawn endpoint; the handler
	// additionally enforces a per-caller in-flight cap and audits each call.
	rae.cm.RegisterHTTPHandler(
		"/api/v1/system/cli-validate",
		rae.a.withAuth(withRateLimit(cliValidateLimiter, rae.a.HandleSystemCliValidate)),
	)
	rae.cm.RegisterHTTPHandler("/api/v1/status", rae.a.withAuth(rae.a.HandleStatus))
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
	rae.cm.RegisterHTTPHandler(
		"/api/v1/tasks/occurrences",
		rae.a.withAuth(withRateLimit(taskReadLimiter, rae.a.HandleTaskOccurrences)),
	)
	rae.cm.RegisterHTTPHandler("/api/v1/tasks", rae.a.withAuth(rae.a.HandleTasks))
	rae.cm.RegisterHTTPHandler("/api/v1/tasks/", rae.a.withAuth(rae.a.HandleTasks))
	// Plans REST surface (ADR-049 D1, Wave 2-C1). GET/POST /workspaces/{id}/plans
	// is dispatched from HandleWorkspaces (rest_workspaces.go); individual plan
	// GET/PUT/DELETE and /approve /stop live here.
	rae.cm.RegisterHTTPHandler("/api/v1/plans", rae.a.withAuth(rae.a.HandlePlans))
	rae.cm.RegisterHTTPHandler("/api/v1/plans/", rae.a.withAuth(rae.a.HandlePlans))
	rae.cm.RegisterHTTPHandler("/api/v1/workspaces", rae.a.withAuth(withRateLimit(configLimiter, rae.a.HandleWorkspaces)))
	rae.cm.RegisterHTTPHandler("/api/v1/workspaces/", rae.a.withAuth(withRateLimit(configLimiter, rae.a.HandleWorkspaces)))
	// Library file explorer (rest_library.go). withUploadAuth, not plain
	// withAuth: /library/{id}/upload streams multipart straight through this
	// dispatcher, and withAuth's body limit would truncate it. Every JSON
	// route behind this dispatcher is still independently capped at 1MB by
	// decodeAndValidate, so relaxing the outer limit does not widen the
	// attack surface for the non-upload operations.
	rae.cm.RegisterHTTPHandler("/api/v1/library", rae.a.withUploadAuth(withRateLimit(configLimiter, rae.a.HandleLibrary)))
	// ADR-067 stage 2: the subtree entry point is HandleLibraryTree, which peels
	// off /library/{id}/knowledge* (rest_knowledge.go) and hands everything else
	// to HandleLibrary unchanged. It is a shim rather than four more
	// registrations because the workspace id sits in the MIDDLE of those
	// patterns and this mux has no path wildcards — see HandleLibraryTree's doc.
	rae.cm.RegisterHTTPHandler("/api/v1/library/", rae.a.withUploadAuth(withRateLimit(configLimiter, rae.a.HandleLibraryTree)))
	// ADR-067 stage 1 (FR-003f): the preview-token mint endpoint and the bare
	// /library-preview/ serving prefix. Registered from rest_library_preview.go
	// so the two halves share one token store. The mint path is an EXACT
	// pattern, so it outranks the "/api/v1/library/" subtree above.
	rae.a.registerLibraryPreviewRoutes(rae.cm)
	// Mail HTML preview (email-mail-view-spec 2.3a, MC-10): the session-auth
	// mint endpoint and the token-only /mail-preview/ serve prefix, sharing
	// one token store published on the restAPI for logout revocation.
	rae.a.registerMailPreviewRoutes(rae.cm)
	// GET/PUT /api/v1/providers/default-model (ADR-068 FR-018/FR-042,
	// T068-11): its OWN route with the high-blast-radius adminWrap chain
	// (withAuth → RequireNotBypass — 401 unauthenticated, 503 under
	// dev-mode bypass), registered ahead of the /providers/ prefix
	// dispatcher. "default-model" is a reserved path segment, never a
	// provider id (MAJ-002); the dynamic mux matches this exact path before
	// the subtree prefix below, so a PUT here can never reach the
	// /providers/{id} upsert branch.
	rae.cm.RegisterHTTPHandler("/api/v1/providers/default-model", rae.a.adminWrap(rae.a.HandleDefaultModel))
	// GET /api/v1/providers/catalog (ADR-067 FR-017, T067-10). Its own
	// exact path, registered ahead of the /providers/ subtree dispatcher:
	// "catalog" is a reserved path segment and is never a provider id, and
	// the exact match always beats the prefix.
	//
	// withOptionalAuth, not withAuth: the onboarding wizard's provider
	// picker (src/routes/onboarding.tsx) calls this route to render its
	// list BEFORE any admin account exists to authenticate as — the same
	// FR-050 shape as GET /providers (see the C1 comment on that branch
	// below). The handler gates itself with requireAuthOutsideOnboarding,
	// the shared fail-closed FR-050 gate, rather than leaving the route
	// wide open. Unlike /providers there is no reduction to make for an
	// anonymous caller inside the window: the catalog is public vendor
	// metadata (provider/model ids, tiers, context windows) with no
	// operator secret or account-specific field anywhere in it, so the
	// full document is served either way.
	rae.cm.RegisterHTTPHandler("/api/v1/providers/catalog", rae.a.withOptionalAuth(rae.a.HandleProvidersCatalog))
	rae.cm.RegisterHTTPHandler("/api/v1/providers", rae.a.withOptionalAuth(rae.a.HandleProviders))
	rae.cm.RegisterHTTPHandler("/api/v1/providers/", rae.a.withOptionalAuth(rae.a.HandleProviders))
	rae.cm.RegisterHTTPHandler("/api/v1/mcp-servers", rae.a.withAuth(rae.a.HandleMCPServers))
	rae.cm.RegisterHTTPHandler("/api/v1/mcp-servers/", rae.a.withAuth(rae.a.HandleMCPServers))
	rae.cm.RegisterHTTPHandler("/api/v1/storage/stats", rae.a.withAuth(rae.a.HandleStorageStats))
	rae.cm.RegisterHTTPHandler("/api/v1/tools", rae.a.withAuth(rae.a.HandleToolsRegistry))
	rae.cm.RegisterHTTPHandler("/api/v1/tools/builtin", rae.a.withAuth(rae.a.HandleBuiltinToolsDeprecated))
	rae.cm.RegisterHTTPHandler("/api/v1/tools/mcp", rae.a.withAuth(rae.a.HandleMCPTools))
	rae.cm.RegisterHTTPHandler("/api/v1/tool-approvals/", rae.a.withAuth(rae.a.HandleToolApprovals))
	rae.cm.RegisterHTTPHandler("/api/v1/channels", rae.a.withAuth(rae.a.HandleChannels))
	rae.cm.RegisterHTTPHandler("/api/v1/channels/", rae.a.withAuth(rae.a.HandleChannels))
	rae.cm.RegisterHTTPHandler("/api/v1/agents/", rae.a.withAuth(rae.a.HandleAgents))
	// M11: list all configured mailboxes (never 404s; empty list = none) so the
	// SPA doesn't have to probe every agent's /agents/{id}/mailbox endpoint.
	rae.cm.RegisterHTTPHandler("/api/v1/mailboxes", rae.a.withAuth(rae.a.listMailboxes))
	rae.cm.RegisterHTTPHandler("/api/v1/config/gateway/rotate-token", rae.a.withAuth(rae.a.rotateGatewayToken))
	rae.cm.RegisterHTTPHandler("/api/v1/activity", rae.a.withAuth(rae.a.HandleActivity))
}

// registerSettingsAndAccountRoutes registers schedules, notifications, settings, security, account, integration, automation, and voice routes.
func (rae *restAPIRegisterAdditionalEndpoints) registerSettingsAndAccountRoutes() {
	// Schedules CRUD + run-now + pause (#264).
	rae.cm.RegisterHTTPHandler("/api/v1/schedules", rae.a.withAuth(rae.a.HandleSchedules))
	rae.cm.RegisterHTTPHandler("/api/v1/schedules/", rae.a.withAuth(rae.a.HandleSchedules))
	// Header notification center (#264).
	rae.cm.RegisterHTTPHandler("/api/v1/notifications", rae.a.withAuth(rae.a.HandleNotifications))
	rae.cm.RegisterHTTPHandler("/api/v1/notifications/", rae.a.withAuth(rae.a.HandleNotifications))

	// Memory settings endpoint (FR-019 / US-6, ADR-027): readable/writable by any
	// authenticated user (A2/G-02 — not admin-only because recap and retention
	// settings are non-sensitive operational knobs without blast-radius risk).
	rae.cm.RegisterHTTPHandler("/api/v1/settings/memory", rae.a.withAuth(rae.a.HandleMemorySettings))

	// Context-budget settings endpoint (ADR-066 D9, FR-036 / US-11): the D4
	// per-surface caps, the D6 absolute trigger, the D10 ingest bound, the D2
	// global default window and the per-(provider, model) overrides. Same
	// posture as /settings/memory (withAuth, not RequireNotBypass) per the
	// contract. This is the operator's only escape from the
	// context_window_unknown turn refusal, which names it by hand.
	rae.cm.RegisterHTTPHandler("/api/v1/settings/context", rae.a.withAuth(rae.a.HandleContextSettings))

	// Settings endpoints (Wave 4).
	// GET /api/v1/audit-log — the audit log contains every privileged action,
	// tool-use trace, and LLM request; gated behind authentication only under
	// the single-account model.
	// Chain: withAuth (verifies token) → handler.
	rae.cm.RegisterHTTPHandler("/api/v1/audit-log", rae.a.withAuth(rae.a.HandleAuditLog))
	// Wave 3 security endpoints (SEC-25, SEC-28).
	rae.cm.RegisterHTTPHandler("/api/v1/security/exec-proxy-status", rae.a.withAuth(rae.a.HandleExecProxyStatus))
	// High-blast-radius security endpoints.
	// Chain: withAuth → RequireNotBypass → handler.
	// CSRF is enforced by the global WrapHTTPHandler layer (no per-handler wiring needed).
	rae.cm.RegisterHTTPHandler("/api/v1/config/pending-restart", rae.a.adminWrap(rae.a.HandlePendingRestart))
	// O4-backend: UI-triggerable graceful self-restart. High blast radius —
	// RequireNotBypass (dev_mode_bypass → 503) via adminWrap.
	rae.cm.RegisterHTTPHandler("/api/v1/gateway/restart", rae.a.adminWrap(rae.a.HandleGatewayRestart))
	// O14 god-mode toggle. High blast radius — RequireNotBypass via adminWrap.
	// The SPA confirms the flip with the operator before POSTing (ADR-0008
	// ruling 6).
	rae.cm.RegisterHTTPHandler("/api/v1/gateway/god-mode", rae.a.adminWrap(rae.a.HandleGodMode))
	rae.cm.RegisterHTTPHandler("/api/v1/security/audit-log", rae.a.adminWrap(rae.a.HandleSandboxAuditLog))
	rae.cm.RegisterHTTPHandler("/api/v1/security/skill-trust", rae.a.adminWrap(rae.a.HandleSkillTrust))
	rae.cm.RegisterHTTPHandler("/api/v1/security/prompt-guard", rae.a.adminWrap(rae.a.HandlePromptGuard))
	// /api/v1/security/rate-limits handles GET (read state — the response carries
	// the live daily-cost meter and current cap config, sensitive observability)
	// and PUT (write — gated by RequireNotBypass, since dev_mode_bypass would
	// otherwise let an anonymous caller change global rate-limit caps). Wrapped
	// with adminWrap to bring it in line with the other high-blast-radius
	// security endpoints below and to satisfy item 7 of #155 (admin-route
	// bypass coverage).
	rae.cm.RegisterHTTPHandler("/api/v1/security/rate-limits", rae.a.adminWrap(rae.a.HandleRateLimits))
	rae.cm.RegisterHTTPHandler("/api/v1/security/sandbox-config", rae.a.adminWrap(rae.a.HandleSandboxConfig))
	rae.cm.RegisterHTTPHandler("/api/v1/security/session-scope", rae.a.adminWrap(rae.a.HandleSessionScope))
	rae.cm.RegisterHTTPHandler("/api/v1/security/retention", rae.a.adminWrap(rae.a.HandleRetention))
	rae.cm.RegisterHTTPHandler("/api/v1/security/retention/sweep", rae.a.adminWrap(rae.a.HandleRetentionSweep))
	rae.cm.RegisterHTTPHandler("/api/v1/performance", rae.a.adminWrap(rae.a.HandlePerformance))
	// Wave 5 security endpoints (SEC-01/02/03).
	rae.cm.RegisterHTTPHandler("/api/v1/security/sandbox-status", rae.a.withAuth(rae.a.HandleSandboxStatus))
	// /api/v1/security/sandbox-config is registered above with adminWrap — do NOT
	// re-register here; Go ServeMux takes the last registration, and a lighter
	// wrapper here would silently drop the dev_mode_bypass gate.
	// GET/PUT /api/v1/security/tool-policies — readable and writable by the
	// single authenticated account (PUT authorization enforced inside
	// HandleToolPolicies).
	rae.cm.RegisterHTTPHandler("/api/v1/security/tool-policies", rae.a.withAuth(rae.a.HandleToolPolicies))
	// GET /api/v1/credentials — even though plaintext is not returned, the
	// credential ref names reveal what integrations exist, so this is gated
	// behind authentication.
	// Chain: withAuth (verifies token) → handler.
	rae.cm.RegisterHTTPHandler("/api/v1/credentials", rae.a.withAuth(rae.a.HandleCredentials))
	rae.cm.RegisterHTTPHandler("/api/v1/credentials/", rae.a.withAuth(rae.a.HandleCredentials))
	// WP4: local backup — off in platform mode
	if config.EditionAuthMode() == config.AuthModeLocal {
		rae.cm.RegisterHTTPHandler("/api/v1/backup", rae.a.withAuth(rae.a.HandleCreateBackup))
		rae.cm.RegisterHTTPHandler("/api/v1/backups", rae.a.withAuth(rae.a.HandleListBackups))
		rae.cm.RegisterHTTPHandler("/api/v1/restore", rae.a.withAuth(rae.a.HandleRestore))
	}
	// Option A keeps workspace and media IDs as separately validated path segments;
	// the legacy global media route remains below for backward compatibility.
	rae.cm.RegisterHTTPHandler("/api/v1/media/workspace/", rae.a.withOptionalAuth(rae.a.HandleMediaByRef))
	rae.cm.RegisterHTTPHandler("/api/v1/media/", rae.a.withOptionalAuth(rae.a.HandleMedia))
	// Exact match takes precedence over the /sessions/ prefix handler for this specific path.
	rae.cm.RegisterHTTPHandler("/api/v1/sessions/all", rae.a.withAuth(rae.a.HandleClearSessions))
	rae.cm.RegisterHTTPHandler("/api/v1/about", rae.a.withAuth(rae.a.HandleAbout))
	rae.cm.RegisterHTTPHandler("/api/v1/user-context", rae.a.withAuth(rae.a.HandleUserContext))
	// Auth mode composition seam (ADR-0010 WP2, auth_mode.go): the mode
	// derived from the stamped edition — never a config key — picks which
	// sign-in routes exist at all and how each is wrapped. Local mode
	// registers upstream's own /api/v1/auth/login, /auth/change-password and
	// /auth/reauth; platform mode registers the omnipus.ai
	// start/session/claim/callback routes. An unknown edition (the empty
	// mode) registers none of them, so sign-in answers 404 rather than
	// guessing which mode a misbuilt binary meant to be.
	mode := activeAuthMode(rae.a)
	for _, rt := range mode.authRoutes {
		rae.cm.RegisterHTTPHandler(rt.path, rae.a.wrapRoute(rt))
	}
	// Onboarding's auth posture is the other half of the FR-050 property this
	// seam carries: local mode runs onboarding BEFORE any session exists
	// (withOptionalAuth, upstream's own posture — the pre-auth window this
	// route used to carry when the local account it protects still exists);
	// platform mode runs it AFTER sign-in (withAuth — ADR-0008 rulings 1 and
	// 2, onboarding-and-profile-spec §0.4 FR-OB-060: the caller already holds
	// the session the platform issued, so an unauthenticated POST is a 401
	// rather than something the handler has to decide). Gated on mode.known()
	// for the same reason the loop above registers nothing for an unknown
	// edition — the zero-value onboardingWrap (authWrapBare) would otherwise
	// register onboarding with NO auth check at all.
	if mode.known() {
		rae.cm.RegisterHTTPHandler(
			"/api/v1/onboarding/complete",
			rae.a.wrapRoute(authRoute{
				handler: rae.a.HandleCompleteOnboarding,
				wrap:    mode.onboardingWrap,
				limiter: onboardingCompleteLimiter,
			}),
		)
		// The provider probe runs INSIDE the wizard, so it shares the
		// wizard's own auth posture. In platform mode it is additionally
		// authenticated because it carries a caller-supplied API key to an
		// upstream host, and an anonymous caller has no business doing that;
		// its remaining gate (onboardingSetupWindowGate) refuses only after
		// onboarding is finished.
		rae.cm.RegisterHTTPHandler(
			"/api/v1/onboarding/probe-provider",
			rae.a.wrapRoute(authRoute{
				handler: rae.a.HandleOnboardingProbeProvider,
				wrap:    mode.onboardingWrap,
				limiter: onboardingCompleteLimiter,
			}),
		)
	}
	// Common to both modes: validating a token and logging out never depend
	// on how the caller signed in.
	rae.cm.RegisterHTTPHandler("/api/v1/auth/validate", rae.a.withAuth(withRateLimit(validateLimiter, rae.a.HandleValidateToken)))
	rae.cm.RegisterHTTPHandler("/api/v1/auth/logout", rae.a.withAuth(rae.a.HandleLogout))

	// Integrations provider-picker — search + voice-input providers (Spec-6
	// FR-12.1). GET lists; PUT configures.
	rae.cm.RegisterHTTPHandler("/api/v1/integrations/providers", rae.a.withAuth(rae.a.HandleIntegrationProviders))
	rae.cm.RegisterHTTPHandler("/api/v1/integrations/providers/", rae.a.withAuth(rae.a.HandleIntegrationProviders))
	// Automations — trigger→action display projection over schedules (W3-AC
	// UI reframe). Read-only; writes go through /api/v1/schedules.
	rae.cm.RegisterHTTPHandler("/api/v1/automations", rae.a.withAuth(rae.a.HandleAutomations))
	// Composer mic — voice transcription (Spec-6 FR-12.1).
	rae.cm.RegisterHTTPHandler("/api/v1/voice/transcribe", rae.a.withAuth(rae.a.HandleTranscribe))
	// Voice provider descriptor (agent-form spec §4.10.1). Drives the dropdown /
	// free-text / disabled widget in the agent edit slide-over.
	rae.cm.RegisterHTTPHandler("/api/v1/voice/provider", rae.a.withAuth(rae.a.HandleVoiceProvider))
}

// registerAncillaryRoutes registers upload, metrics, version, devices, token statistics, and browser inspection routes.
func (rae *restAPIRegisterAdditionalEndpoints) registerAncillaryRoutes() {
	// File upload endpoints (Milestone 3).
	rae.cm.RegisterHTTPHandler("/api/v1/upload", rae.a.withUploadAuth(rae.a.HandleUpload))
	rae.cm.RegisterHTTPHandler("/api/v1/uploads/", rae.a.withOptionalAuth(rae.a.HandleServeUpload))

	// Prometheus-compatible metrics endpoint (FR-039).
	// Unauthenticated for Prometheus scrape compatibility; does not expose secrets.
	rae.cm.RegisterHTTPHandler("/metrics", http.HandlerFunc(rae.a.HandleMetrics))

	// Version endpoint — unauthenticated; returns build SHA for frontend version-drift detection (#110).
	rae.cm.RegisterHTTPHandler("/api/v1/version", http.HandlerFunc(rae.a.HandleVersion))

	// GET /api/v1/devices — returns pending pairing requests and paired devices.
	// Admin-only. Device pairing infrastructure is not yet implemented; the handler
	// returns valid empty arrays so the SPA DevicesSection renders its empty state
	// rather than 404-ing. Traces to: contracts/components/schemas/DevicesResponse.yaml.
	rae.cm.RegisterHTTPHandler("/api/v1/devices", rae.a.adminWrap(rae.a.HandleDevices))

	// The legacy GTD /board/tasks endpoints were folded into the unified
	// /api/v1/tasks surface in Sprint 2 (one store, one wire schema). See
	// HandleTasks above (registered earlier) for the unified CRUD + subtasks +
	// todos + dependencies routes.

	// Token usage stats endpoint (Wave 2b).
	// Traces to: contracts/components/schemas/TokenUsageSummary.yaml.
	rae.cm.RegisterHTTPHandler("/api/v1/stats/tokens", rae.a.withAuth(withRateLimit(configLimiter, rae.a.HandleTokenStats)))

	// Best-effort DOM inspect for the live interactive browser panel
	// (ADR-039 D-B3) — resolves the element at a point so the SPA can attach
	// its text/HTML when a user annotates a spot. Sibling to, but registered
	// independently of, /api/v1/browser/ws (gateway.go).
	rae.cm.RegisterHTTPHandler("/api/v1/browser/inspect", rae.a.withAuth(rae.a.HandleBrowserInspect))
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

// httpHandlerRegistrar is the subset of channels.Manager used for route
// registration on the main mux. registerPreviewEndpoints (ADR-044) uses this
// same interface — /preview/ no longer has its own registrar/mux.
type httpHandlerRegistrar interface {
	RegisterHTTPHandler(pattern string, handler http.Handler)
}
