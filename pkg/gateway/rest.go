//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
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
	"os/exec"
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

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	whatsappnative "github.com/dapicom-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/notifications"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	providers_pkg "github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/security"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/skills"
	"github.com/dapicom-ai/omnipus/pkg/task"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// Version is set at build time via -ldflags "-X github.com/dapicom-ai/omnipus/pkg/gateway.Version=x.y.z".
// Dev builds default to a semver-compatible string so the /version endpoint still
// passes the contract schema used by the SPA.
var Version = "0.0.0-dev"

// errConflict is the sentinel returned from a safeUpdateConfigJSON mutate
// closure when an optimistic-concurrency check fails inside the configMu
// lock (closing the TOCTOU race that existed when the check ran against the
// in-memory cached config outside the lock). Callers test with errors.Is
// and map it to HTTP 409.
var errConflict = errors.New("optimistic concurrency conflict")

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
	credStore     *credentials.Store  // shared unlocked credential store (injected at boot)
	mediaStore    media.MediaStore    // shared media store for serving media files
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
	// Lazy-initialized admin-only handler wrappers. Built once on first use so
	// each incoming PUT request doesn't allocate a fresh middleware chain.
	adminUpdateConfigOnce    sync.Once
	adminUpdateConfigHandler http.Handler
	adminPutPoliciesOnce     sync.Once
	adminPutPoliciesHandler  http.Handler

	// devServers is the gateway-wide Tier 3 dev-server registry. Shared with
	// the web_serve tool (dev mode) and workspace.shell_bg tool via the agent
	// instance. HandlePreview / HandleDevProxy read this to validate tokens
	// and resolve the upstream loopback port. Nil when Tier 3 is not
	// supported on the current platform (non-Linux).
	devServers *sandbox.DevServerRegistry

	// servedSubdirs is the gateway-wide static-preview registration map.
	// Shared with the web_serve tool (static mode) via the agent instance.
	// HandlePreview / HandleServeWorkspace read this to validate tokens and
	// resolve the served directory. Nil when web_serve is not configured.
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

	// allowGodMode is set when the gateway was started with --allow-god-mode.
	// When false, the agent update handler rejects sandbox_profile=off with 403.
	// Mirrors the same field on AgentLoop for runtime tool coercion.
	// Latch (2) — REST enforcement.
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
		if result.Role != "" {
			ctx = context.WithValue(ctx, RoleContextKey{}, result.Role)
		}
		handler(w, r.WithContext(ctx))
	}
}

// withAuth wraps a handler with preflight, bearer auth, CORS header boilerplate,
// and a 1 MB request body size limit to prevent unbounded memory allocation.
func (a *restAPI) withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return a.withAuthAndBodyLimit(handler, 1<<20) // 1 MB
}

// adminWrap composes the canonical admin middleware chain around h:
//
//	withAuth → RequireAdmin → RequireNotBypass → h
//
// Exposed as a method so Sprint K admin-endpoint registrations outside
// registerAdditionalEndpoints can reuse the same chain verbatim, and so
// future refactors (e.g. adding a new admin middleware) update one site.
func (a *restAPI) adminWrap(h http.HandlerFunc) http.HandlerFunc {
	return a.withAuth(
		middleware.RequireAdmin(
			middleware.RequireNotBypass(h),
		).ServeHTTP,
	)
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

// setAgentHeartbeat echoes the agent's per-agent heartbeat schedule fields (O6)
// onto the wire response. Heartbeat is per-agent for Main agents; a worker never
// has one (its stored flag, if any, is ignored). When an agent has not set its
// own heartbeat (HeartbeatEnabled nil and interval 0), the migrated/global
// default is surfaced as the effective value so the form shows a sensible state.
func setAgentHeartbeat(ag *gen.Agent, ac config.AgentConfig, cfg *config.Config) {
	if ac.IsWorker() {
		// Subagents never have a heartbeat — report disabled regardless of stored bits.
		ag.HeartbeatEnabled = false
		ag.HeartbeatInterval = 0
		return
	}
	if ac.HeartbeatEnabled != nil {
		ag.HeartbeatEnabled = *ac.HeartbeatEnabled
	} else {
		ag.HeartbeatEnabled = cfg.Heartbeat.Enabled
	}
	if ac.HeartbeatInterval > 0 {
		ag.HeartbeatInterval = ac.HeartbeatInterval
	} else {
		ag.HeartbeatInterval = cfg.Heartbeat.Interval
	}
}

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

// unifiedMetaToGenSession converts a session.UnifiedMeta to the generated gen.Session wire type.
// The two types have matching JSON field names; this explicit conversion satisfies the Go type checker.
func unifiedMetaToGenSession(m *session.UnifiedMeta) gen.Session {
	s := gen.Session{
		Id:        m.ID,
		AgentId:   m.AgentID,
		Channel:   m.Channel,
		CreatedAt: m.CreatedAt,
		Title:     m.Title,
		Status:    gen.SessionStatus(m.Status),
		Partitions: func() []string {
			if m.Partitions == nil {
				return []string{}
			}
			return m.Partitions
		}(),
		Stats: struct {
			Cost         float64 `json:"cost"`
			MessageCount int     `json:"message_count"`
			TokensIn     int     `json:"tokens_in"`
			TokensOut    int     `json:"tokens_out"`
			TokensTotal  int     `json:"tokens_total"`
			ToolCalls    int     `json:"tool_calls"`
		}{
			Cost:         m.Stats.Cost,
			MessageCount: m.Stats.MessageCount,
			TokensIn:     m.Stats.TokensIn,
			TokensOut:    m.Stats.TokensOut,
			TokensTotal:  m.Stats.TokensTotal,
			ToolCalls:    m.Stats.ToolCalls,
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

func (a *restAPI) listSessions(w http.ResponseWriter, r *http.Request) {
	agentFilter := r.URL.Query().Get("agent_id")
	typeFilter := r.URL.Query().Get("type")

	metas, partialErrs := a.agentLoop.ListAllSessions()
	for _, pe := range partialErrs {
		slog.Warn("rest: list sessions: partial error", "error", pe)
	}

	// Apply filters.
	filtered := make([]*session.UnifiedMeta, 0, len(metas))
	for _, m := range metas {
		if agentFilter != "" && m.AgentID != agentFilter {
			continue
		}
		if typeFilter != "" && string(m.Type) != typeFilter {
			continue
		}
		filtered = append(filtered, m)
	}

	// Always route through unifiedMetaToGenSession so that required array/map
	// fields (Partitions in particular) marshal as [] not null — Zod on the SPA
	// rejects null where the contract says type:array and drops the whole list.
	genSessions := make([]gen.Session, 0, len(filtered))
	for _, m := range filtered {
		genSessions = append(genSessions, unifiedMetaToGenSession(m))
	}
	if len(partialErrs) == 0 {
		jsonOK(w, genSessions)
		return
	}
	sanitized := make([]string, len(partialErrs))
	for i, pe := range partialErrs {
		sanitized[i] = sanitizePartialError(pe)
	}
	jsonOK(w, gen.ListSessions200JSONResponseBody1{
		Sessions:      genSessions,
		PartialErrors: sanitized,
	})
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
	// Coerce nil → empty slice so JSON marshals as [] not null. The SPA's
	// fetchSessionMessages validates via z.array(WireMessageSchema), which
	// rejects null — a fresh session with no transcript would surface as
	// "Could not load messages." in the UI.
	if messages == nil {
		messages = []session.TranscriptEntry{}
	}
	jsonOK(w, messages)
}

// renameSession handles PUT /api/v1/sessions/{id}.
// Accepts {"title": "new name"} and returns the updated session meta.
func (a *restAPI) renameSession(w http.ResponseWriter, r *http.Request, id string) {
	var req gen.SessionRenameRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SessionRenameRequest", &req, validateEnabled) {
		return
	}
	if req.Title == "" {
		jsonErr(w, http.StatusBadRequest, "title is required")
		return
	}
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
	if err := store.DeleteSession(id); err != nil {
		slog.Error("rest: delete session", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not delete session: %v", err))
		return
	}
	jsonOK(w, map[string]bool{"success": true})
}

// firstEnabledAgentID returns the ID of the first active/enabled chat-target agent
// in the config list, or "" when no such agent is configured. Used as a last-resort
// fallback after GetDefaultAgent() — mirrors resolveDefaultAgentID in
// pkg/routing/route.go. Workers are active but are NOT chat targets, so they are
// skipped here: a last-resort fallback must never land on a worker, which is invoked
// only via delegation.
func firstEnabledAgentID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	for _, ag := range cfg.Agents.List {
		if ag.IsActive() && ag.IsChatTarget() {
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
			// Fall back to the first enabled agent (mirrors handleBoardTaskStart /
			// resolveDefaultAgentID in pkg/routing/route.go).
			agentID = firstEnabledAgentID(a.agentLoop.GetConfig())
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

	// GET/PUT/DELETE /api/v1/agents/{id}/mailbox — per-agent email mailbox account (M11)
	if agentID != "" && subPath == "mailbox" {
		switch r.Method {
		case http.MethodGet:
			a.getAgentMailbox(w, agentID)
		case http.MethodPut:
			a.setAgentMailbox(w, r, agentID)
		case http.MethodDelete:
			a.deleteAgentMailbox(w, agentID)
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
	case http.MethodPatch:
		if agentID != "" {
			a.patchAgentOwnership(w, r, agentID)
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
			Reason:  gen.RunnerTestResponseReasonNotExternalCli,
			Message: "agent executor is not external-cli; no external runner to test",
		})
		return
	}
	cli := executor.CLI
	if cli == "" {
		jsonOK(w, gen.RunnerTestResponse{
			Ok:      false,
			Reason:  gen.RunnerTestResponseReasonUnknownCli,
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

func (a *restAPI) listAgentSessions(w http.ResponseWriter, agentID string) {
	// agentID is already validated by HandleAgents before reaching here.
	store := a.agentLoop.GetAgentStore(agentID)
	if store == nil {
		jsonOK(w, []gen.Session{})
		return
	}
	metas, err := store.ListSessions()
	if err != nil {
		slog.Error("rest: list agent sessions", "agent_id", agentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not list sessions: %v", err))
		return
	}
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

// inferProviderName returns the provider name from an explicit Provider field,
// or infers it from the Model field's "provider/model" format. Falls back to "default".
func inferProviderName(provider, model string) string {
	if provider != "" {
		return provider
	}
	if parts := strings.SplitN(model, "/", 2); len(parts) == 2 {
		return parts[0]
	}
	return "default"
}

// fetchUpstreamModels fetches the list of available models from an OpenAI-compatible
// provider's /models endpoint. Returns model IDs sorted alphabetically, or nil on error.
// Used to populate the model dropdown with all models the provider supports, not just
// the ones explicitly configured in config.json.
func fetchUpstreamModels(baseURL, apiKey string) ([]string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", strings.TrimSuffix(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream models: status %d", resp.StatusCode)
	}

	// Validate Content-Type before attempting JSON parse (M12).
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "application/json") {
		return nil, fmt.Errorf("upstream models: unexpected Content-Type %q", ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB limit
	if err != nil {
		return nil, err
	}

	var result struct { // not-wire-format: decodes upstream provider /models API response, never emitted to SPA
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	return models, nil
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
	WorkspacePath() string
}, agentID, agentWorkspace, omnipusHome string,
) (string, error) {
	if agentWorkspace != "" {
		// AgentConfig.Workspace may contain "~"; expand it the same way config does.
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
				return cfg.WorkspacePath(), fmt.Errorf("UserHomeDir: %w", err)
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
	return cfg.WorkspacePath(), nil
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

// readAgentFiles returns the contents of SOUL.md, HEARTBEAT.md, and the body
// of AGENT.md (everything after the closing frontmatter delimiter) from the
// given workspace directory. Missing files return an empty string without
// logging an error — their absence is expected for newly created agents.
// Permission and other I/O errors (not IsNotExist) are logged at Warn level (M11).
func readAgentFiles(workspace string) (soul, heartbeat, instructions string) {
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
	if data, err := os.ReadFile(filepath.Join(workspace, "AGENT.md")); err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("rest: readAgentFiles: cannot read AGENT.md", "workspace", workspace, "error", err)
		}
	} else {
		fm, body := splitAgentMDFrontmatter(string(data))
		if fm == "" && strings.HasPrefix(strings.TrimSpace(string(data)), "---") {
			// M17: AGENT.md starts with --- but has no closing delimiter.
			slog.Debug("rest: AGENT.md has opening --- delimiter but no closing ---", "workspace", workspace)
		}
		instructions = body
	}
	return soul, heartbeat, instructions
}

// splitAgentMDFrontmatter splits an AGENT.md file into its YAML frontmatter
// and markdown body. The frontmatter is the raw YAML text between the opening
// and closing "---" delimiters. When no valid frontmatter block is found the
// entire content is returned as the body.
func splitAgentMDFrontmatter(content string) (frontmatter, body string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", content
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return "", content
	}
	frontmatter = strings.Join(lines[1:end], "\n")
	body = strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
	return frontmatter, body
}

// steeringModeOrDefault returns the steering mode string, defaulting to "one-at-a-time"
// when the configured value is empty.
func steeringModeOrDefault(mode string) string {
	if mode == "" {
		return "one-at-a-time"
	}
	return mode
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

// buildAgentDefaults populates the execution-related fields from config defaults.
func buildAgentDefaults(cfg *config.Config) gen.Agent {
	sm := gen.AgentSteeringMode(steeringModeOrDefault(cfg.Agents.Defaults.SteeringMode))
	return gen.Agent{
		TimeoutSeconds:    cfg.Agents.Defaults.TimeoutSeconds,
		MaxToolIterations: cfg.Agents.Defaults.MaxToolIterations,
		SteeringMode:      sm,
		HeartbeatEnabled:  cfg.Heartbeat.Enabled,
		HeartbeatInterval: cfg.Heartbeat.Interval,
		// Required string fields — initialized to empty (overwritten per-agent).
		Soul:         "",
		Heartbeat:    "",
		Instructions: "",
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
	defaultModel := cfg.Agents.Defaults.ModelName
	for _, ac := range cfg.Agents.List {
		model := defaultModel
		if ac.Model != nil && ac.Model.Primary != "" {
			model = ac.Model.Primary
		}
		workspace, wsErr := agentWorkspacePath(cfg, ac.ID, ac.Workspace, a.homePath)
		if wsErr != nil {
			slog.Warn("rest: listAgents: could not resolve workspace", "agent_id", ac.ID, "error", wsErr)
		}
		// M2: listAgents only needs SOUL.md to determine draft status — avoid reading
		// HEARTBEAT.md and AGENT.md unnecessarily in the list endpoint.
		// Core agents have compiled prompts — do not expose them via SOUL.md.
		var soul string
		if !ac.Locked {
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
		ag.Model = &model
		setAgentModelProvider(&ag, ac.Model)
		setAgentHeartbeat(&ag, ac, cfg)
		ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, activeIDs, soul, ac.Locked))
		ag.Soul = soul
		ag.Default = boolPtr(ac.Default)
		if len(ac.Skills) > 0 {
			skills := make([]string, len(ac.Skills))
			copy(skills, ac.Skills)
			ag.Skills = &skills
		}
		setAgentExecutorResponse(&ag, ac.Subagents)
		setAgentDelegationPolicyResponse(&ag, ac.DelegationPolicy)
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
			model := cfg.Agents.Defaults.ModelName
			if ac.Model != nil && ac.Model.Primary != "" {
				model = ac.Model.Primary
			}
			workspace, wsErr := agentWorkspacePath(cfg, ac.ID, ac.Workspace, a.homePath)
			if wsErr != nil {
				slog.Warn("rest: getAgent: could not resolve workspace", "agent_id", ac.ID, "error", wsErr)
			}
			soul, heartbeat, instructions := readAgentFiles(workspace)
			// Core agents have compiled prompts — do not expose them.
			if ac.Locked {
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
			ag.Model = &model
			setAgentModelProvider(&ag, ac.Model)
			setAgentHeartbeat(&ag, ac, cfg)
			ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, activeIDs, soul, ac.Locked))
			ag.Soul = soul
			ag.Heartbeat = heartbeat
			ag.Instructions = instructions
			ag.Default = boolPtr(ac.Default)
			if len(ac.Skills) > 0 {
				skills := make([]string, len(ac.Skills))
				copy(skills, ac.Skills)
				ag.Skills = &skills
			}
			setAgentExecutorResponse(&ag, ac.Subagents)
			setAgentDelegationPolicyResponse(&ag, ac.DelegationPolicy)
			if ac.UpdatedAt != nil {
				ag.UpdatedAt = ac.UpdatedAt
			}
			jsonOK(w, ag)
			return
		}
	}

	jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", id))
}

func (a *restAPI) createAgent(w http.ResponseWriter, r *http.Request) {
	var req gen.AgentCreateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "AgentCreateRequest", &req, validateEnabled) {
		return
	}
	if req.Name == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	// O13: per-agent sandbox_profile=off is retired. The generated validator
	// already rejects "off" (removed from the enum); this is defense-in-depth
	// for the non-validated path (ValidateInbound disabled).
	if req.SandboxProfile != nil && config.SandboxProfile(*req.SandboxProfile) == config.SandboxProfileOff {
		jsonErr(
			w,
			http.StatusBadRequest,
			`sandbox_profile=off is retired — use the global god-mode switch (POST /api/v1/gateway/god-mode); per-agent profiles are "workspace", "workspace+net", or "host"`,
		)
		return
	}
	// Resolve the requested agent type. The wire enum (W1) is
	// [Main, Subagent, subagent_3p]; legacy "custom"/"worker" still round-trip
	// via the boundary translation in coreagent.ResolveType so existing payloads
	// don't break. "core" and "system" are seeded-only classifications —
	// sending them (or any other value) is a 400: the only way to obtain a
	// core/system agent is via SeedConfig, never via the REST create path.
	createType := config.AgentTypeCustom
	if req.Type != nil {
		switch *req.Type {
		case gen.AgentCreateRequestTypeMain:
			createType = config.AgentTypeCustom
		case gen.AgentCreateRequestTypeSubagent:
			createType = config.AgentTypeWorker
		case gen.AgentCreateRequestTypeSubagent3p:
			createType = config.AgentTypeWorker
		default:
			jsonErr(w, http.StatusBadRequest,
				"type must be one of Main, Subagent, subagent_3p; \"core\" and \"system\" are seeded-only")
			return
		}
	}
	// W2 spec: subagent_3p agents run on an external CLI, so the create
	// request MUST carry an executor with kind=external-cli. A subagent_3p
	// without an external-cli executor has no runnable target — reject at the
	// gate rather than silently defaulting to native (which would make it a
	// mislabelled Subagent). This runs before the worker-without-executor
	// native-defaulting below so that defaulting only applies to plain
	// Subagent, never to subagent_3p.
	if req.Type != nil && *req.Type == gen.AgentCreateRequestTypeSubagent3p {
		if req.Executor == nil || req.Executor.Kind == nil ||
			*req.Executor.Kind != gen.AgentCreateRequestExecutorKindExternalCli {
			jsonErr(w, http.StatusBadRequest, "subagent_3p requires executor.kind=external-cli")
			return
		}
		// Spec §9.2: cli_path is required for subagent_3p on create.
		if req.Executor.CliPath == nil || strings.TrimSpace(*req.Executor.CliPath) == "" {
			jsonErr(w, http.StatusBadRequest, "executor.cli_path is required for subagent_3p agents")
			return
		}
	}
	// Derive / coerce executor for non-worker and worker-without-executor cases.
	// Main (and legacy custom) agents always run native on the Omnipus engine; an
	// external executor in the request is coerced to native with a warning.
	// Workers without an executor derive kind=native so dispatch has a concrete
	// runtime target.
	effectiveExecutor := req.Executor
	var createExecutorWarning string
	if createType == config.AgentTypeWorker && req.Executor == nil {
		// Subagent with no executor: default to native runtime. The actual
		// config record is created further down; we record the intent and
		// materialize the native executor there.
	}
	if createType != config.AgentTypeWorker && req.Executor != nil {
		kind := executorKindStr(req.Executor.Kind)
		if kind == string(config.ExecutorKindExternalCLI) || kind == string(config.ExecutorKindRemoteA2A) {
			createExecutorWarning = "executor.kind was coerced to native because Main agents run on the Omnipus engine"
			effectiveExecutor = nil
		}
	}
	// Referential validation: reject unknown skill IDs before doing any work.
	if req.Skills != nil && len(*req.Skills) > 0 {
		if errMsg := a.validateSkillIDs(*req.Skills); errMsg != "" {
			jsonErr(w, http.StatusBadRequest, errMsg)
			return
		}
	}
	description := ""
	if req.Description != nil {
		description = strings.TrimSpace(*req.Description)
	}
	// W2 spec §4.3 / §9.2 F-02 (mirror of PUT path): Subagent and subagent_3p
	// require a non-empty description after trim. A worker without a
	// description cannot be routed to by the orchestrator.
	if (createType == config.AgentTypeWorker) && description == "" {
		jsonErr(w, http.StatusBadRequest, "description is required for worker agents (Subagent, subagent_3p)")
		return
	}
	// O6 / O12.1 — heartbeat + voice are Main-only (form matrix rows 10–13): a
	// Subagent / subagent_3p create that tries to set them is rejected. Subagents
	// run only via delegation (no schedule) and are not chat personas (no voice).
	if createType == config.AgentTypeWorker {
		if (req.HeartbeatEnabled != nil && *req.HeartbeatEnabled) ||
			(req.HeartbeatInterval != nil && *req.HeartbeatInterval > 0) ||
			(req.Heartbeat != nil && strings.TrimSpace(*req.Heartbeat) != "") {
			jsonErr(w, http.StatusBadRequest, "a worker cannot have heartbeat (Subagents run only via delegation)")
			return
		}
		if req.Voice != nil && strings.TrimSpace(*req.Voice) != "" {
			jsonErr(w, http.StatusBadRequest, "a worker cannot have a per-agent voice (workers are not chat personas)")
			return
		}
	}
	// W2 spec §3.1 row 16 / §9.2 F-13 (mirror of PUT path): fallback_models
	// is capped at 2 entries. Server-enforced so direct REST callers (not the
	// SPA) cannot smuggle more entries past the schema validator.
	if req.FallbackModels != nil && len(*req.FallbackModels) > 2 {
		jsonErr(w, http.StatusBadRequest, "fallback_models exceeds maxItems: 2")
		return
	}
	// W2 spec §4.7 / §9.2 row 8: whitespace-only soul is rejected (the wire
	// schema enforces minLength:1; whitespace-only is the natural
	// soft-bypass). Backend trims before validation.
	if req.Soul == "" || strings.TrimSpace(req.Soul) == "" {
		jsonErr(w, http.StatusBadRequest, "soul is required (whitespace-only is rejected as minLength violation)")
		return
	}
	color := ""
	if req.Color != nil {
		color = *req.Color
	}
	icon := ""
	if req.Icon != nil {
		icon = *req.Icon
	}
	// color hex regex (spec §4.4).
	if color != "" {
		if matched, _ := regexp.MatchString(`^#[0-9A-Fa-f]{6}$`, color); !matched {
			jsonErr(w, http.StatusBadRequest, "color must be a valid hex code (e.g. #D4AF37)")
			return
		}
	}
	// icon maxLength:50 (spec §4.4).
	if len(icon) > 50 {
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
		Name:        req.Name,
		Description: description,
		Color:       color,
		Icon:        icon,
		Type:        createType,
	}
	if req.Model != nil && *req.Model != "" {
		ac.Model = &config.AgentModelConfig{Primary: *req.Model}
		// O3 two-field model: persist the explicit primary provider when supplied.
		if req.Provider != nil && strings.TrimSpace(*req.Provider) != "" {
			ac.Model.Provider = strings.TrimSpace(*req.Provider)
		}
	}
	// O6 — per-agent heartbeat (Main only; worker create already rejected above).
	if createType != config.AgentTypeWorker {
		if req.HeartbeatEnabled != nil {
			hb := *req.HeartbeatEnabled
			ac.HeartbeatEnabled = &hb
		}
		if req.HeartbeatInterval != nil {
			ac.HeartbeatInterval = *req.HeartbeatInterval
		}
	}
	if req.Skills != nil && len(*req.Skills) > 0 {
		ac.Skills = make([]string, len(*req.Skills))
		copy(ac.Skills, *req.Skills)
	}
	// Sub-agent executor (kind/cli). Mapped into AgentConfig.Subagents.Executor so
	// it is actually persisted. Workers created without an executor get a native
	// runtime derived here; Main agents have any external executor request
	// coerced to native earlier with a warning.
	if createType == config.AgentTypeWorker && req.Executor == nil {
		ac.Subagents = &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative},
		}
	}
	if effectiveExecutor != nil {
		execCfg, errMsg := executorConfigFromRequest(
			executorKindStr(effectiveExecutor.Kind),
			executorCliStr(effectiveExecutor.Cli),
		)
		if errMsg != "" {
			jsonErr(w, http.StatusBadRequest, errMsg)
			return
		}
		if execCfg != nil {
			// executorConfigFromRequest only maps kind+cli. Copy the
			// remaining executor fields (cli_path, env_overrides, cli_args)
			// from the request so they persist on create. The PUT handler
			// does this via executorConfigUpdate; create needs it here.
			if effectiveExecutor.CliPath != nil {
				execCfg.CLIPath = *effectiveExecutor.CliPath
			}
			if effectiveExecutor.EnvOverrides != nil {
				execCfg.EnvOverrides = *effectiveExecutor.EnvOverrides
			}
			if effectiveExecutor.CliArgs != nil {
				execCfg.CLIArgs = *effectiveExecutor.CliArgs
			}
			ac.Subagents = &config.SubagentsConfig{Executor: execCfg}
		}
	}
	// subagent_3p forbidden-field guard. Any worker created/pointed at an
	// external-cli executor cannot receive the seven CLI-owned fields.
	if createType == config.AgentTypeWorker && isExternalSubagent(ac) {
		if field, forbidden := firstForbiddenSubagent3pField(&req); forbidden {
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("subagent_3p agents do not support %s; this is fixed at create time.", field))
			return
		}
	}
	// Delegation policy (to/modes/depth enforced; accept_from/budget inert). Mapped
	// into AgentConfig.DelegationPolicy so it is actually persisted (previously the
	// contract field was write-dropped). Validate targets/modes/depth before any work
	// so a bad request 400s rather than persisting an invalid policy. There is no
	// existing stored policy on create, so inert fields come solely from the request.
	if dpIn := delegationInputFromCreateRequest(&req); dpIn != nil {
		cfg := a.agentLoop.GetConfig()
		// selfID = the new agent's id (known at create time) so a self-ref A→A is
		// rejected. A Subagent/worker created with a non-empty to[] is now allowed
		// (bounded by depth, not the worker tier). No existing stored policy on
		// create → grandfathering is a no-op.
		dp, errMsg := buildDelegationPolicy(dpIn, nil, rosterIDSet(cfg), delegationDepthCeiling(cfg),
			ac.ID, peerDelegationGraph(cfg, ac.ID))
		if errMsg != "" {
			jsonErr(w, http.StatusBadRequest, errMsg)
			return
		}
		ac.DelegationPolicy = dp
	}
	// Seed the privilege rail (FR-008/FR-022): custom agents always get
	// system.*: deny unless the caller explicitly overrides it.
	baseCfg := coreagent.NewCustomAgentToolsCfg()
	if req.ToolsCfg != nil {
		builtin := config.AgentBuiltinToolsCfg{
			// Start with the base default_policy=allow.
			DefaultPolicy: baseCfg.Builtin.DefaultPolicy,
			// Inherit the system.*: deny seed.
			Policies: make(map[string]config.ToolPolicy, len(baseCfg.Builtin.Policies)),
		}
		for k, v := range baseCfg.Builtin.Policies {
			builtin.Policies[k] = v
		}
		if req.ToolsCfg.Builtin != nil && req.ToolsCfg.Builtin.DefaultPolicy != nil &&
			*req.ToolsCfg.Builtin.DefaultPolicy != "" {
			builtin.DefaultPolicy = config.ToolPolicy(*req.ToolsCfg.Builtin.DefaultPolicy)
		}
		// Merge caller-supplied policies; caller's system.* entry overrides seed.
		if req.ToolsCfg.Builtin != nil && req.ToolsCfg.Builtin.Policies != nil {
			for k, v := range *req.ToolsCfg.Builtin.Policies {
				builtin.Policies[k] = config.ToolPolicy(v)
			}
		}
		ac.Tools = &config.AgentToolsCfg{Builtin: builtin}
		if req.ToolsCfg.Mcp != nil && req.ToolsCfg.Mcp.Servers != nil && len(*req.ToolsCfg.Mcp.Servers) > 0 {
			servers := make([]config.AgentMCPServerBinding, 0, len(*req.ToolsCfg.Mcp.Servers))
			for _, s := range *req.ToolsCfg.Mcp.Servers {
				var tools []string
				if s.Tools != nil {
					tools = *s.Tools
				}
				servers = append(servers, config.AgentMCPServerBinding{ID: s.Id, Tools: tools})
			}
			ac.Tools.MCP = config.AgentMCPToolsCfg{Servers: servers}
		}
	} else {
		// No caller-supplied tools config: use the full base config.
		ac.Tools = baseCfg
	}
	// Persist the new agent to config.json BEFORE mutating the live config.
	// If persistence fails, the in-memory config stays consistent with disk.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		agents, _ := m["agents"].(map[string]any)
		if agents == nil {
			agents = map[string]any{}
			m["agents"] = agents
		}
		list, _ := agents["list"].([]any)
		newAgent := map[string]any{
			"id":   ac.ID,
			"name": ac.Name,
			"type": string(ac.Type),
		}
		if ac.Description != "" {
			newAgent["description"] = ac.Description
		}
		if ac.Color != "" {
			newAgent["color"] = ac.Color
		}
		if ac.Icon != "" {
			newAgent["icon"] = ac.Icon
		}
		if ac.Model != nil {
			modelMap := map[string]any{"primary": ac.Model.Primary}
			// O3 two-field model: persist the explicit primary provider so it
			// round-trips through a config reload and drives resolution.
			if ac.Model.Provider != "" {
				modelMap["provider"] = ac.Model.Provider
			}
			newAgent["model"] = modelMap
		}
		// O6 — per-agent heartbeat persistence (Main only).
		if ac.HeartbeatEnabled != nil {
			newAgent["heartbeat_enabled"] = *ac.HeartbeatEnabled
		}
		if ac.HeartbeatInterval > 0 {
			newAgent["heartbeat_interval"] = ac.HeartbeatInterval
		}
		if ac.Tools != nil {
			builtinMap := map[string]any{
				"default_policy": string(ac.Tools.Builtin.DefaultPolicy),
			}
			if len(ac.Tools.Builtin.Policies) > 0 {
				policies := make(map[string]string, len(ac.Tools.Builtin.Policies))
				for k, v := range ac.Tools.Builtin.Policies {
					policies[k] = string(v)
				}
				builtinMap["policies"] = policies
			}
			toolsCfg := map[string]any{"builtin": builtinMap}
			if len(ac.Tools.MCP.Servers) > 0 {
				servers := make([]map[string]any, 0, len(ac.Tools.MCP.Servers))
				for _, s := range ac.Tools.MCP.Servers {
					srv := map[string]any{"id": s.ID}
					if len(s.Tools) > 0 {
						srv["tools"] = s.Tools
					}
					servers = append(servers, srv)
				}
				toolsCfg["mcp"] = map[string]any{"servers": servers}
			}
			newAgent["tools"] = toolsCfg
		}
		if len(ac.Skills) > 0 {
			newAgent["skills"] = ac.Skills
		}
		if ac.Subagents != nil && ac.Subagents.Executor != nil {
			newAgent["subagents"] = map[string]any{
				"executor": executorConfigToMap(ac.Subagents.Executor),
			}
		}
		if ac.DelegationPolicy != nil {
			newAgent["delegation_policy"] = delegationPolicyToMap(ac.DelegationPolicy)
		}
		agents["list"] = append(list, newAgent)
		return nil
	}); err != nil {
		slog.Error("rest: save config for new agent", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// Persist the create-time soul to SOUL.md. createAgent previously
	// write-dropped req.Soul: the contract accepted it, the FE sent it, but
	// nothing ever landed on disk — and a "draft" agent created without a
	// soul stayed in the draft state forever on the soul-empty path. Write
	// it here, mirroring the workspace-resolution + WriteFileAtomic pattern
	// used by updateAgent. An empty (but non-nil) req.Soul is a legitimate
	// "soul optional" create (workers may be created with no soul and edited
	// later), so the field-nonzero check is intentional and matches the
	// locked concept. Trimming is fine; this overwrites any prior SOUL.md
	// (create is the only write gate at this point, so a re-run is
	// idempotent against itself).
	var createSoulContent string
	if req.Soul != "" {
		createSoulContent = strings.TrimSpace(req.Soul)
		workspace, wsErr := agentWorkspacePath(a.agentLoop.GetConfig(), ac.ID, ac.Workspace, a.homePath)
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
	// Capture the default model name BEFORE triggering a reload to avoid a race
	// between TriggerReload (which may swap the live config) and the read below.
	defaultModelName := a.agentLoop.GetConfig().Agents.Defaults.ModelName

	// Persistence succeeded. Trigger reload so the in-memory config picks up the new agent.
	// The "warning" field signals a partial success — frontend must check this field.
	var createReloadWarning string
	if err := a.agentLoop.TriggerReload(); err != nil {
		slog.Error("config reload after agent create failed", "error", err)
		createReloadWarning = fmt.Sprintf("config reload failed: %v", err)
	}
	// Build the response from local variables only (do NOT read from live config — race).
	model := defaultModelName
	if ac.Model != nil && ac.Model.Primary != "" {
		model = ac.Model.Primary
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
	ag.Model = &model
	setAgentModelProvider(&ag, ac.Model)
	setAgentHeartbeat(&ag, ac, cfgAfterCreate)
	// Echo the just-persisted soul so the FE round-trip works (req.Soul was
	// write-dropped before this fix; a created agent would come back with
	// soul="" regardless of what the caller sent). Status is derived from
	// the soul too: a non-empty soul moves the agent out of "draft".
	ag.Soul = createSoulContent
	ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, nil, createSoulContent, ac.Locked))
	if len(ac.Skills) > 0 {
		skills := make([]string, len(ac.Skills))
		copy(skills, ac.Skills)
		ag.Skills = &skills
	}
	setAgentExecutorResponse(&ag, ac.Subagents)
	setAgentDelegationPolicyResponse(&ag, ac.DelegationPolicy)
	warnings := []string{}
	if createExecutorWarning != "" {
		warnings = append(warnings, createExecutorWarning)
	}
	if createReloadWarning != "" {
		warnings = append(warnings, createReloadWarning)
	}
	if len(warnings) > 0 {
		w := strings.Join(warnings, "; ")
		ag.Warning = &w
	}
	// O6 — register the new agent's heartbeat schedule when it was created with
	// heartbeat enabled (Main only; workers were rejected earlier).
	if ac.HeartbeatEnabled != nil && *ac.HeartbeatEnabled {
		a.reconcileHeartbeatSchedules()
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
	// Snapshot the audit fields BEFORE we mutate config.json — `found` still
	// points into the in-memory config and the safeUpdateConfigJSON callback
	// runs before the reload returns.
	deletedName := found.Name
	deletedType := string(found.Type)
	// Remove the agent from config.json.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		agents, _ := m["agents"].(map[string]any)
		if agents == nil {
			return nil
		}
		list, _ := agents["list"].([]any)
		filtered := make([]any, 0, len(list))
		for _, item := range list {
			entry, _ := item.(map[string]any)
			if entry == nil {
				continue
			}
			if entryID, _ := entry["id"].(string); entryID == id {
				continue // skip the deleted agent
			}
			filtered = append(filtered, item)
		}
		agents["list"] = filtered
		return nil
	}); err != nil {
		slog.Error("rest: deleteAgent: save config failed", "agent_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to save config")
		return
	}
	// Reload the live config so the deleted agent is no longer in memory.
	// triggerReloadAndWait polls until reload completes (or 5s deadline) so the in-memory config is
	// updated before the 204 response is sent back to the caller (prevents a
	// race where an immediate GET /sessions/:id still sees agent_removed=false).
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Error("rest: deleteAgent: reload failed", "agent_id", id, "error", err)
	}
	// Audit the destructive action. Best-effort (matches rest_workspaces
	// conventions: emit after the write succeeds, ignore logger errors). The
	// auditor is nil only in unit-test fixtures where audit isn't wired; the
	// pre-existing workspace handlers use the same nil-guard pattern.
	if a.auditor != nil {
		_ = a.auditor.Log(&audit.Entry{
			Event:    "agent.delete",
			Decision: audit.DecisionAllow,
			AgentID:  id,
			Details: map[string]any{
				"agent_id":   id,
				"agent_type": deletedType,
				"agent_name": deletedName,
			},
		})
	}
	// O6 — drop any heartbeat schedule the deleted agent owned (the reconciler
	// removes heartbeat jobs whose agent is no longer in config).
	a.reconcileHeartbeatSchedules()
	w.WriteHeader(http.StatusNoContent)
}

// cliProbeLookPath is the probe function used by HandleSystemCliDetect. It is
// a package-level var (rather than a direct call to exec.LookPath) so unit
// tests can swap in a deterministic implementation without OS-level mocking.
// In production this is always exec.LookPath.
var cliProbeLookPath = exec.LookPath

// HandleSystemCliDetect handles GET /api/v1/system/cli-detect.
//
// Reports whether each of the three external-CLI runner binaries is on PATH
// for the gateway process. The roster screen uses this to grey-out CLIs the
// host cannot run so operators don't have to discover a missing binary inside
// the wizard.
//
// Pure Go probe (no shell-out). Idempotent and unaudited — this is a
// read-only diagnostic. LookPath returns (path, err); any non-nil error is
// treated as "not on PATH".
func (a *restAPI) HandleSystemCliDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	probe := func(binary string) bool {
		_, err := cliProbeLookPath(binary)
		return err == nil
	}
	resp := gen.CliDetect{
		HasClaude:   probe("claude"),
		HasCodex:    probe("codex"),
		HasOpencode: probe("opencode"),
	}
	jsonOK(w, resp)
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

// firstForbiddenSubagent3pField returns the first forbidden field supplied on
// a create/update request, or ("", false). Both gen.AgentCreateRequest and
// gen.AgentUpdateRequest are handled by a type switch.
func firstForbiddenSubagent3pField(req any) (string, bool) {
	switch r := req.(type) {
	case *gen.AgentCreateRequest:
		if r.ToolsCfg != nil {
			return "tools_cfg", true
		}
		if r.Skills != nil && len(*r.Skills) > 0 {
			return "skills", true
		}
		if r.FallbackModels != nil && len(*r.FallbackModels) > 0 {
			return "fallback_models", true
		}
		if r.ModelParams != nil {
			return "model_params", true
		}
		if r.SandboxProfile != nil {
			return "sandbox_profile", true
		}
		if r.ShellPolicy != nil {
			return "shell_policy", true
		}
		if r.DelegationPolicy != nil {
			return "delegation_policy", true
		}
	case *gen.AgentUpdateRequest:
		// worker-PUT-400 fix: only the genuinely CLI-owned fields are rejected on
		// a subagent_3p PUT. Fields that ARE valid for a worker — model,
		// timeout_seconds, max_tool_iterations, color, icon, description, and
		// delegation_policy — must be accepted (a delegation_policy with a
		// non-empty to[] is now allowed for a Subagent and bounded by
		// buildDelegationPolicy's depth cap). The external CLI manages its own
		// isolation, tools,
		// and skills, so tools_cfg / sandbox_profile / shell_policy /
		// fallback_models / model_params / skills stay forbidden (O13).
		if r.ToolsCfg != nil {
			return "tools_cfg", true
		}
		if r.Skills != nil {
			return "skills", true
		}
		if r.FallbackModels != nil {
			return "fallback_models", true
		}
		if r.ModelParams != nil {
			return "model_params", true
		}
		if r.SandboxProfile != nil {
			return "sandbox_profile", true
		}
		if r.ShellPolicy != nil {
			return "shell_policy", true
		}
	}
	return "", false
}

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
	var req gen.AgentUpdateRequest
	validateEnabled := cfg.Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "AgentUpdateRequest", &req, validateEnabled) {
		return
	}
	// Timestamp applied to the persisted agent on every successful save.
	now := time.Now().UTC()
	// O13: per-agent sandbox_profile=off is retired. "No sandbox" is reachable
	// only via the global god-mode switch (POST /api/v1/gateway/god-mode). The
	// generated validator already rejects "off" (removed from the enum); this is
	// defense-in-depth for any non-validated path (ValidateInbound disabled).
	if req.SandboxProfile != nil && config.SandboxProfile(*req.SandboxProfile) == config.SandboxProfileOff {
		jsonErr(
			w,
			http.StatusBadRequest,
			`sandbox_profile=off is retired — use the global god-mode switch (POST /api/v1/gateway/god-mode); per-agent profiles are "workspace", "workspace+net", or "host"`,
		)
		return
	}
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
	// time and never run on a schedule. Reject enabling/setting heartbeat on
	// a worker at the write gate so a worker never gets a HEARTBEAT.md prompt
	// or a positive enable flag. Setting heartbeat to disabled / interval
	// unchanged is fine (the gate below only rejects the "turning it on" path).
	// This guard runs BEFORE the locked-agent identity check so a locked
	// worker (the seed default) is also blocked: a worker must never have
	// heartbeat regardless of its locked status.
	if foundAgent.IsWorker() && (req.HeartbeatEnabled != nil || req.HeartbeatInterval != nil || req.Heartbeat != nil) {
		// Allow idempotent "off" writes (e.g., setting enabled=false or
		// clearing the interval) so an operator's defensive reset is not
		// blocked; reject any write that would actually enable heartbeat on
		// a worker. A worker is also free to receive an EMPTY HEARTBEAT.md
		// (clearing) but never a non-empty one.
		if (req.HeartbeatEnabled != nil && *req.HeartbeatEnabled) ||
			(req.HeartbeatInterval != nil && *req.HeartbeatInterval > 0) ||
			(req.Heartbeat != nil && strings.TrimSpace(*req.Heartbeat) != "") {
			jsonErr(
				w,
				http.StatusBadRequest,
				"a worker cannot have heartbeat enabled (workers run only via delegation)",
			)
			return
		}
	}
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

	// W2 spec §9.2 row 14: PUT worker with steering_mode=queue-and-process
	// returns 200 but server forces steering_mode="one-at-a-time". Workers
	// never queue (they run only via delegation; no concurrent inbound).
	// Implementation: if the caller sent a non-nil steering_mode and the agent
	// is a worker, ignore the caller's value and force the stored value to
	// "one-at-a-time" via the persist block below.

	// W2 spec §3.1 row 16 / §9.2 row 11+15: fallback_models is capped at 2
	// entries (maxItems: 2). Server-enforced on every PUT/CREATE so a direct
	// REST caller (not the SPA) cannot smuggle a 3rd entry past the schema.
	if req.FallbackModels != nil && len(*req.FallbackModels) > 2 {
		jsonErr(w, http.StatusBadRequest, "fallback_models exceeds maxItems: 2")
		return
	}

	if foundAgent.Locked {
		// Protected: name, description, soul (prompt content), heartbeat (HEARTBEAT.md content),
		// instructions, color, icon, and skills are identity/capability fields — reject on locked agents.
		// Skills are included here (B-2 defense-in-depth): core agents have compiled-in capability
		// sets; allowing runtime skill assignment would silently override that invariant.
		if req.Name != nil || req.Description != nil ||
			req.Soul != nil || req.Heartbeat != nil || req.Instructions != nil ||
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
	// Delegation policy (to/modes/depth enforced; accept_from/budget inert).
	// MERGE semantics: when req.DelegationPolicy is non-nil, set to/modes/depth from
	// the request but PRESERVE any existing AcceptFrom/Budget already on the stored
	// policy (the editor does not send those inert fields). When req.DelegationPolicy
	// is nil, leave the stored policy untouched (do not wipe seeded delegation).
	// Validate before any persistence so a bad target/mode/depth 400s.
	var updatedDelegationPolicy *config.DelegationPolicy
	delegationPolicyTouched := false
	if dpIn := delegationInputFromUpdateRequest(&req); dpIn != nil {
		delegationPolicyTouched = true
		dp, errMsg := buildDelegationPolicy(
			dpIn,
			foundAgent.DelegationPolicy, // existing stored policy (may be nil)
			rosterIDSet(cfg),
			delegationDepthCeiling(cfg),
			foundAgent.ID,                           // selfID: reject A→A self-delegation
			peerDelegationGraph(cfg, foundAgent.ID), // reject multi-hop cycle (A→B→A)
		)
		if errMsg != "" {
			jsonErr(w, http.StatusBadRequest, errMsg)
			return
		}
		updatedDelegationPolicy = dp
	}
	// Persist to config.json BEFORE mutating the live config.
	// Capture the new values to apply after persistence succeeds.
	newName := foundAgent.Name
	newModel := ""
	if foundAgent.Model != nil {
		newModel = foundAgent.Model.Primary
	}
	if req.Name != nil {
		newName = *req.Name
	}
	if req.Model != nil {
		newModel = *req.Model
	}
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		agents, _ := m["agents"].(map[string]any)
		if agents == nil {
			return fmt.Errorf("agents section not found in config")
		}
		// Per-agent fields: name, model, timeout_seconds, max_tool_iterations,
		// steering_mode, tool_feedback — stored under agents.list[*].
		list, _ := agents["list"].([]any)
		for _, entry := range list {
			agentMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if agentMap["id"] == id {
				// Optimistic concurrency check (runs INSIDE configMu so two
				// concurrent PUTs cannot both pass the version check and then
				// both write — closing the TOCTOU race that existed when this
				// check ran against the in-memory cached config outside the
				// lock). If the caller sent an updated_at value, it must match
				// the persisted value exactly; otherwise another edit raced and
				// we abort the mutate (nothing is written). The caller maps
				// errConflict to HTTP 409.
				if req.UpdatedAt != nil {
					persistedStr, _ := agentMap["updated_at"].(string)
					if persistedStr != "" {
						persistedAt, parseErr := time.Parse(time.RFC3339, persistedStr)
						if parseErr == nil && !req.UpdatedAt.Equal(persistedAt) {
							return errConflict
						}
					}
				}
				if req.Name != nil {
					agentMap["name"] = newName
				}
				if req.Description != nil {
					trimmed := strings.TrimSpace(*req.Description)
					if trimmed == "" {
						delete(agentMap, "description")
					} else {
						agentMap["description"] = trimmed
					}
				}
				if req.Model != nil {
					modelMap, _ := agentMap["model"].(map[string]any)
					if modelMap == nil {
						modelMap = map[string]any{}
						agentMap["model"] = modelMap
					}
					modelMap["primary"] = newModel
				}
				// O3 two-field model: persist (or clear) the explicit primary
				// provider. A non-empty value pins the provider; an explicit empty
				// string clears it (fall back to default-provider resolution).
				if req.Provider != nil {
					provider := strings.TrimSpace(*req.Provider)
					modelMap, _ := agentMap["model"].(map[string]any)
					if modelMap == nil {
						modelMap = map[string]any{}
						agentMap["model"] = modelMap
					}
					if provider == "" {
						delete(modelMap, "provider")
					} else {
						modelMap["provider"] = provider
					}
				}
				if req.TimeoutSeconds != nil {
					agentMap["timeout_seconds"] = *req.TimeoutSeconds
				}
				if req.MaxToolIterations != nil {
					agentMap["max_tool_iterations"] = *req.MaxToolIterations
				}
				if req.SteeringMode != nil {
					// W2 spec §4.24 / §9.2 row 14: workers are forced to
					// "one-at-a-time" server-side regardless of the caller's
					// value. Workers run only via delegation (no concurrent
					// inbound), so queueing is meaningless. This is a silent
					// override, not a 400 — the spec calls for 200 with the
					// stored value forced to one-at-a-time.
					sm := string(*req.SteeringMode)
					if foundAgent.IsWorker() {
						sm = "one-at-a-time"
					}
					agentMap["steering_mode"] = sm
				}
				// tool_feedback was removed from the wire in W1 (it's now per-channel
				// runtime behavior driven by pkg/agent/loop.go: webchat skips). The
				// global config-level agents.defaults.tool_feedback stays.
				if req.SandboxProfile != nil {
					if *req.SandboxProfile == "" {
						delete(agentMap, "sandbox_profile")
					} else {
						agentMap["sandbox_profile"] = string(*req.SandboxProfile)
					}
				}
				if req.ShellPolicy != nil {
					// Load existing shell_policy from the persisted map (if any) so
					// that a partial PATCH (e.g. only custom_deny_patterns) does not
					// clobber fields the caller did not send.
					existing, _ := agentMap["shell_policy"].(map[string]any)
					if existing == nil {
						existing = map[string]any{}
					}
					// Only overwrite enable_deny_patterns when the caller explicitly
					// sent it (non-nil pointer). Writing nil would persist JSON null
					// and reset the flag to false on next decode.
					if req.ShellPolicy.EnableDenyPatterns != nil {
						existing["enable_deny_patterns"] = *req.ShellPolicy.EnableDenyPatterns
					}
					if req.ShellPolicy.CustomDenyPatterns != nil && len(*req.ShellPolicy.CustomDenyPatterns) > 0 {
						existing["custom_deny_patterns"] = *req.ShellPolicy.CustomDenyPatterns
					}
					agentMap["shell_policy"] = existing
				}
				if req.Color != nil {
					agentMap["color"] = *req.Color
				}
				if req.Icon != nil {
					agentMap["icon"] = *req.Icon
				}
				if req.FallbackModels != nil {
					agentMap["fallback_models"] = *req.FallbackModels
				}
				if req.ModelParams != nil {
					mpMap := map[string]any{}
					if req.ModelParams.Temperature != nil {
						mpMap["temperature"] = *req.ModelParams.Temperature
					}
					if req.ModelParams.MaxTokens != nil {
						mpMap["max_tokens"] = *req.ModelParams.MaxTokens
					}
					if req.ModelParams.TopP != nil {
						mpMap["top_p"] = *req.ModelParams.TopP
					}
					agentMap["model_params"] = mpMap
				}
				if req.RateLimits != nil {
					rlMap := map[string]any{}
					if req.RateLimits.UseGlobalDefaults != nil {
						rlMap["use_global_defaults"] = *req.RateLimits.UseGlobalDefaults
					}
					if req.RateLimits.MaxLlmCallsPerHour != nil {
						rlMap["max_llm_calls_per_hour"] = *req.RateLimits.MaxLlmCallsPerHour
					}
					if req.RateLimits.MaxToolCallsPerMinute != nil {
						rlMap["max_tool_calls_per_minute"] = *req.RateLimits.MaxToolCallsPerMinute
					}
					if req.RateLimits.MaxCostPerDay != nil {
						rlMap["max_cost_per_day"] = *req.RateLimits.MaxCostPerDay
					}
					agentMap["rate_limits"] = rlMap
				}
				// Default flag: single-default invariant.
				// If req.Default is set, handle two sub-cases:
				//   true  → mark this agent as default; clear Default on all others.
				//   false → clear Default on this agent only; leave others unchanged.
				// If req.Default is nil (absent), leave all Default flags unchanged.
				if req.Default != nil {
					agentMap["default"] = *req.Default
				}
				if req.ToolsCfg != nil {
					tcMap := map[string]any{}
					if req.ToolsCfg.Builtin != nil {
						builtinMap := map[string]any{}
						if req.ToolsCfg.Builtin.DefaultPolicy != nil {
							builtinMap["default_policy"] = string(*req.ToolsCfg.Builtin.DefaultPolicy)
						}
						if req.ToolsCfg.Builtin.Policies != nil {
							builtinMap["policies"] = *req.ToolsCfg.Builtin.Policies
						}
						tcMap["builtin"] = builtinMap
					}
					if req.ToolsCfg.Mcp != nil && req.ToolsCfg.Mcp.Servers != nil {
						servers := make([]map[string]any, 0, len(*req.ToolsCfg.Mcp.Servers))
						for _, s := range *req.ToolsCfg.Mcp.Servers {
							srv := map[string]any{"id": s.Id}
							if s.Tools != nil {
								srv["tools"] = *s.Tools
							}
							servers = append(servers, srv)
						}
						mcpMap := map[string]any{"servers": servers}
						tcMap["mcp"] = mcpMap
					}
					agentMap["tools_cfg"] = tcMap
				}
				// Executor: write the sub-agent executor under subagents.executor when
				// the caller sends it. kind="native" with no cli clears any prior
				// external-cli config (executorConfigFromRequest returns nil → delete).
				if req.Executor != nil {
					subMap, _ := agentMap["subagents"].(map[string]any)
					if updatedExecutor == nil {
						if subMap != nil {
							delete(subMap, "executor")
							if len(subMap) == 0 {
								delete(agentMap, "subagents")
							}
						}
					} else {
						if subMap == nil {
							subMap = map[string]any{}
							agentMap["subagents"] = subMap
						}
						subMap["executor"] = executorConfigToMap(updatedExecutor)
					}
				}
				// Skills: replace the agent's skill list when the caller sends the field.
				// An explicit empty array removes all skills. Nil (absent) leaves unchanged.
				if req.Skills != nil {
					if len(*req.Skills) > 0 {
						agentMap["skills"] = *req.Skills
					} else {
						delete(agentMap, "skills")
					}
				}
				// Delegation policy: only write when the caller sent the field.
				// When omitted (delegationPolicyTouched=false), the stored policy is
				// left untouched so seeded delegation is not wiped. The merged value
				// already preserves inert accept_from/budget from the stored policy.
				if delegationPolicyTouched {
					if updatedDelegationPolicy != nil {
						agentMap["delegation_policy"] = delegationPolicyToMap(updatedDelegationPolicy)
					} else {
						delete(agentMap, "delegation_policy")
					}
				}
				// O6 — heartbeat IS per-agent: persist heartbeat_enabled /
				// heartbeat_interval onto THIS agent's entry, never the global
				// heartbeat block (the old global write bled onto every agent).
				// Worker rejection happened earlier (a worker PUT carrying these
				// fields 400s before reaching persistence).
				if req.HeartbeatEnabled != nil {
					agentMap["heartbeat_enabled"] = *req.HeartbeatEnabled
				}
				if req.HeartbeatInterval != nil {
					agentMap["heartbeat_interval"] = *req.HeartbeatInterval
				}
				// Optimistic concurrency timestamp: refresh on every successful save.
				agentMap["updated_at"] = now.Format(time.RFC3339)
				break
			}
		}
		// Single-default invariant: when setting default=true on this agent,
		// clear default on every OTHER agent in the list.
		if req.Default != nil && *req.Default {
			for _, entry := range list {
				agentMap, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				if agentMap["id"] == id {
					continue // already set above
				}
				// Clear default on every other agent. Delete the key so config
				// stays minimal (omitempty in Go struct); false and missing are
				// equivalent to the router but missing is cleaner JSON.
				delete(agentMap, "default")
			}
		}
		// O6: heartbeat is now per-agent (written inside the agent-found block
		// above), no longer a global block. The global cfg.Heartbeat mirror is
		// left untouched for legacy round-trip.
		return nil
	}); err != nil {
		if errors.Is(err, errConflict) {
			writeJSON(w, http.StatusConflict, gen.ErrorResponse{
				Error: "conflict",
				Code:  strPtr("conflict"),
			})
			return
		}
		slog.Error("rest: save config for agent update", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}
	// Write SOUL.md, HEARTBEAT.md, and AGENT.md BEFORE triggering reload,
	// so the new AgentInstance reads the updated files.
	// Capture agentWorkspace into a local to avoid TOCTOU on cfg.Agents.List (M1).
	capturedWorkspace := cfg.Agents.List[foundIdx].Workspace
	capturedName := cfg.Agents.List[foundIdx].Name
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
	if req.Heartbeat != nil {
		heartbeatPath := filepath.Join(workspace, "HEARTBEAT.md")
		if err := fileutil.WriteFileAtomic(heartbeatPath, []byte(*req.Heartbeat), 0o600); err != nil {
			slog.Error("rest: write HEARTBEAT.md for agent", "agent_id", id, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not write HEARTBEAT.md: %v", err))
			return
		}
	}
	if req.Instructions != nil {
		agentMDPath := filepath.Join(workspace, "AGENT.md")
		// Read existing AGENT.md to preserve frontmatter if it exists.
		existingFrontmatter := ""
		if data, err := os.ReadFile(agentMDPath); err == nil {
			existingFrontmatter, _ = splitAgentMDFrontmatter(string(data))
		} else if !os.IsNotExist(err) {
			slog.Warn(
				"rest: could not read existing AGENT.md for frontmatter preservation",
				"agent_id",
				id,
				"error",
				err,
			)
		}
		if existingFrontmatter == "" {
			existingFrontmatter = "name: " + capturedName
		}
		agentMDContent := "---\n" + existingFrontmatter + "\n---\n"
		if *req.Instructions != "" {
			agentMDContent += "\n" + *req.Instructions
		}
		if err := fileutil.WriteFileAtomic(agentMDPath, []byte(agentMDContent), 0o600); err != nil {
			slog.Error("rest: write AGENT.md for agent", "agent_id", id, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not write AGENT.md: %v", err))
			return
		}
	}
	// Only trigger a full reload when structural changes require it (SOUL.md, HEARTBEAT.md,
	// agent creation/deletion). Model, rate limit, timeout, and steering mode changes are
	// config-only and do NOT need a reload — avoiding the WebSocket drop and context loss
	// that a full reload causes mid-conversation.
	//
	// A delegation_policy change MUST reload: each agent instance's spawn/subagent/task
	// delegation deny-checkers are bound to the config captured at agent-instance
	// construction (registerSharedTools → buildDelegationDenyChecker, etc.). Persisting
	// the new policy + SwapConfig alone does NOT rebuild those closures, so the running
	// agent would keep enforcing the OLD allowlist — a tightening edit (revoke a target /
	// drop a mode) would silently not apply until a restart. TriggerReload →
	// executeReload → ReloadProviderAndConfig → registerSharedTools reconstructs every
	// agent's deny-checkers from the swapped config, applying the new policy live.
	needsReload := req.Soul != nil || req.Heartbeat != nil || req.Instructions != nil ||
		req.DelegationPolicy != nil
	var reloadWarning string
	if needsReload {
		if err := a.agentLoop.TriggerReload(); err != nil {
			slog.Error("config reload after agent update failed", "error", err)
			reloadWarning = fmt.Sprintf("config reload failed: %v", err)
		}
	}

	// O6 — re-reconcile heartbeat schedules when the heartbeat changed (enabled,
	// interval, or HEARTBEAT.md content). Runs after any reload so the reconciler
	// reads the fresh per-agent config + updated HEARTBEAT.md.
	if req.HeartbeatEnabled != nil || req.HeartbeatInterval != nil || req.Heartbeat != nil {
		a.reconcileHeartbeatSchedules()
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
	soul, heartbeat, instructions := readAgentFiles(workspace)
	// Build the response from defaults, then override with request values.
	agentID := cfg.Agents.List[foundIdx].ID
	model := cfg.Agents.Defaults.ModelName
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
	// O6 — per-agent heartbeat: echo the effective schedule fields. A worker
	// never has a heartbeat. The request value (when present) is authoritative;
	// otherwise reflect the reloaded per-agent config, falling back to the global
	// default for an agent that has never set its own.
	if foundAgent.IsWorker() {
		ag.HeartbeatEnabled = false
		ag.HeartbeatInterval = 0
	} else {
		var reloaded *config.AgentConfig
		if cur := a.agentLoop.GetConfig(); cur != nil {
			for i := range cur.Agents.List {
				if cur.Agents.List[i].ID == agentID {
					reloaded = &cur.Agents.List[i]
					break
				}
			}
		}
		if req.HeartbeatEnabled != nil {
			ag.HeartbeatEnabled = *req.HeartbeatEnabled
		} else if reloaded != nil && reloaded.HeartbeatEnabled != nil {
			ag.HeartbeatEnabled = *reloaded.HeartbeatEnabled
		}
		if req.HeartbeatInterval != nil {
			ag.HeartbeatInterval = *req.HeartbeatInterval
		} else if reloaded != nil && reloaded.HeartbeatInterval > 0 {
			ag.HeartbeatInterval = reloaded.HeartbeatInterval
		}
	}
	ag.Status = gen.AgentStatus(computeAgentStatus(agentID, activeIDs, soul, foundAgent.Locked))
	// Hide compiled prompts for locked (core) agents.
	if foundAgent.Locked {
		soul = ""
	}
	ag.Soul = soul
	ag.Heartbeat = heartbeat
	ag.Instructions = instructions
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
				ag.Default = boolPtr(ac.Default)
				if len(ac.Skills) > 0 {
					skills := make([]string, len(ac.Skills))
					copy(skills, ac.Skills)
					ag.Skills = &skills
				}
				setAgentExecutorResponse(&ag, ac.Subagents)
				setAgentDelegationPolicyResponse(&ag, ac.DelegationPolicy)
				if ac.UpdatedAt != nil {
					ag.UpdatedAt = ac.UpdatedAt
				}
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
	if req.SteeringMode != nil {
		sm := gen.AgentSteeringMode(steeringModeOrDefault(string(*req.SteeringMode)))
		ag.SteeringMode = sm
	}
	// tool_feedback removed from the wire in W1 (per-channel runtime behavior now).
	if req.HeartbeatEnabled != nil {
		ag.HeartbeatEnabled = *req.HeartbeatEnabled
	}
	if req.HeartbeatInterval != nil {
		ag.HeartbeatInterval = *req.HeartbeatInterval
	}
	jsonOK(w, ag)
}

// --- Config ---

// HandleConfig handles GET /api/v1/config and PUT /api/v1/config.
// PUT is restricted to admin-role users (Issue #98): mutating gateway config
// can change ports, dev_mode_bypass, and provider settings — a critical
// privilege that must not be available to user-role accounts.
func (a *restAPI) HandleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getConfig(w)
	case http.MethodPut:
		// Enforce admin-only for config mutations. withAuth has already run and
		// written the role into the context; RequireAdmin reads it from there.
		// The wrapper is built once (sync.Once) so each PUT doesn't allocate a
		// new middleware chain.
		a.adminUpdateConfigOnce.Do(func() {
			a.adminUpdateConfigHandler = middleware.RequireAdmin(
				http.HandlerFunc(a.updateConfig),
			)
		})
		a.adminUpdateConfigHandler.ServeHTTP(w, r)
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
// Called while a.configMu is held.
func (a *restAPI) refreshConfigAndRewireServices(configPath string) error {
	if a.credStore == nil {
		// No credential store wired — use the plain loader (no v0 migration, no
		// credential resolution). Safe because without a store there are no
		// secrets to re-arm in the replacer.
		newCfg, err := config.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("load config (no store): %w", err)
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
	bundle, bundleErrs := credentials.ResolveBundle(newCfg, a.credStore)
	for _, e := range bundleErrs {
		// Non-fatal: a disabled channel missing its cred is acceptable here.
		// Enabled-channel fatality is enforced at boot; REST-initiated reloads
		// are best-effort so we log and continue.
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
	info := a.agentLoop.GetStartupInfo()
	skillsInfo, ok := info["skills"].(map[string]any)
	if !ok {
		return map[string]struct{}{}
	}
	names, _ := skillsInfo["names"].([]string)
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
	userMDPath := filepath.Join(cfg.WorkspacePath(), "USER.md")
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
	userMDPath := filepath.Join(cfg.WorkspacePath(), "USER.md")
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
	cm.RegisterHTTPHandler("/api/v1/status", a.withAuth(a.HandleStatus))
	cm.RegisterHTTPHandler("/api/v1/tasks", a.withAuth(a.HandleTasks))
	cm.RegisterHTTPHandler("/api/v1/tasks/", a.withAuth(a.HandleTasks))
	cm.RegisterHTTPHandler("/api/v1/workspaces", a.withAuth(withRateLimit(configLimiter, a.HandleWorkspaces)))
	cm.RegisterHTTPHandler("/api/v1/workspaces/", a.withAuth(withRateLimit(configLimiter, a.HandleWorkspaces)))
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
	cm.RegisterHTTPHandler("/api/v1/config/gateway/rotate-token", a.withAuth(a.rotateGatewayToken))
	cm.RegisterHTTPHandler("/api/v1/activity", a.withAuth(a.HandleActivity))

	// Schedules CRUD + run-now + pause (#264).
	cm.RegisterHTTPHandler("/api/v1/schedules", a.withAuth(a.HandleSchedules))
	cm.RegisterHTTPHandler("/api/v1/schedules/", a.withAuth(a.HandleSchedules))
	// Header notification center (#264).
	cm.RegisterHTTPHandler("/api/v1/notifications", a.withAuth(a.HandleNotifications))
	cm.RegisterHTTPHandler("/api/v1/notifications/", a.withAuth(a.HandleNotifications))

	// Settings endpoints (Wave 4).
	// GET /api/v1/audit-log — admin-only: audit log contains every admin
	// action, tool-use trace, and LLM request. A user-role leak here exposes
	// the full activity history to non-privileged accounts (Issue #98).
	// Chain: withAuth (verifies token, writes role into ctx) → RequireAdmin (checks role).
	cm.RegisterHTTPHandler("/api/v1/audit-log",
		a.withAuth(middleware.RequireAdmin(http.HandlerFunc(a.HandleAuditLog)).ServeHTTP))
	cm.RegisterHTTPHandler("/api/v1/security/exec-allowlist", a.withAuth(a.HandleExecAllowlist))
	// Wave 3 security endpoints (SEC-25, SEC-28).
	cm.RegisterHTTPHandler("/api/v1/security/exec-proxy-status", a.withAuth(a.HandleExecProxyStatus))
	// Admin-only security endpoints.
	// Chain: withAuth → RequireAdmin → RequireNotBypass → handler.
	// CSRF is enforced by the global WrapHTTPHandler layer (no per-handler wiring needed).
	cm.RegisterHTTPHandler("/api/v1/config/pending-restart", a.adminWrap(a.HandlePendingRestart))
	// O4-backend: UI-triggerable graceful self-restart. High blast radius —
	// admin-only + RequireNotBypass (dev_mode_bypass → 503) via adminWrap.
	cm.RegisterHTTPHandler("/api/v1/gateway/restart", a.adminWrap(a.HandleGatewayRestart))
	// O14 god-mode toggle. High blast radius — admin-only + RequireNotBypass via
	// adminWrap, and the POST additionally requires a password re-auth consent
	// token (enforced inside the handler via requireReAuth).
	cm.RegisterHTTPHandler("/api/v1/gateway/god-mode", a.adminWrap(a.HandleGodMode))
	cm.RegisterHTTPHandler("/api/v1/security/audit-log", a.adminWrap(a.HandleSandboxAuditLog))
	cm.RegisterHTTPHandler("/api/v1/security/skill-trust", a.adminWrap(a.HandleSkillTrust))
	cm.RegisterHTTPHandler("/api/v1/security/prompt-guard", a.adminWrap(a.HandlePromptGuard))
	// /api/v1/security/rate-limits handles GET (read state, admin-only because
	// the response carries the live daily-cost meter and current cap config —
	// admin-sensitive observability) and PUT (write — must be admin and gated
	// by RequireNotBypass, since dev_mode_bypass would otherwise let an
	// anonymous caller change global rate-limit caps). Wrapped with adminWrap
	// to bring it in line with the other admin security endpoints below and
	// to satisfy item 7 of v0.2-#155 (admin-route bypass coverage). The inner
	// handler keeps its own role check as a defense-in-depth belt-and-braces.
	cm.RegisterHTTPHandler("/api/v1/security/rate-limits", a.adminWrap(a.HandleRateLimits))
	cm.RegisterHTTPHandler("/api/v1/security/sandbox-config", a.adminWrap(a.HandleSandboxConfig))
	cm.RegisterHTTPHandler("/api/v1/security/session-scope", a.adminWrap(a.HandleSessionScope))
	cm.RegisterHTTPHandler("/api/v1/security/retention", a.adminWrap(a.HandleRetention))
	cm.RegisterHTTPHandler("/api/v1/security/retention/sweep", a.adminWrap(a.HandleRetentionSweep))
	cm.RegisterHTTPHandler("/api/v1/performance", a.adminWrap(a.HandlePerformance))
	cm.RegisterHTTPHandler("/api/v1/users", a.adminWrap(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			a.HandleUsersList(w, r)
		case http.MethodPost:
			a.HandleUserCreate(w, r)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}))
	cm.RegisterHTTPHandler("/api/v1/users/", a.adminWrap(a.handleUsersWithParam))
	// Wave 5 security endpoints (SEC-01/02/03).
	cm.RegisterHTTPHandler("/api/v1/security/sandbox-status", a.withAuth(a.HandleSandboxStatus))
	// /api/v1/security/sandbox-config is registered above with adminWrap — do NOT
	// re-register here; Go ServeMux takes the last registration, and a lighter
	// wrapper here would silently drop the dev_mode_bypass gate.
	// GET /api/v1/security/tool-policies — read available to all authenticated
	// users; PUT is admin-only (enforced inside HandleToolPolicies, Issue #98).
	cm.RegisterHTTPHandler("/api/v1/security/tool-policies", a.withAuth(a.HandleToolPolicies))
	// GET /api/v1/credentials — admin-only: even though plaintext is not
	// returned, the credential ref names reveal what integrations exist
	// (Issue #98).
	// Chain: withAuth (verifies token, writes role into ctx) → RequireAdmin (checks role).
	cm.RegisterHTTPHandler("/api/v1/credentials",
		a.withAuth(middleware.RequireAdmin(http.HandlerFunc(a.HandleCredentials)).ServeHTTP))
	cm.RegisterHTTPHandler("/api/v1/credentials/",
		a.withAuth(middleware.RequireAdmin(http.HandlerFunc(a.HandleCredentials)).ServeHTTP))
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
	cm.RegisterHTTPHandler(
		"/api/v1/auth/register-admin",
		a.withOptionalAuth(withRateLimit(registerAdminLimiter, a.HandleRegisterAdmin)),
	)
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

	// GET /api/v1/me — returns the authenticated user's RBAC role (MeInfo).
	// Used by the SPA for feature gating (fetchMe in src/lib/api.ts:1250).
	// Traces to: contracts/components/schemas/MeInfo.yaml.
	cm.RegisterHTTPHandler("/api/v1/me", a.withAuth(a.HandleMe))

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
}

// registerPreviewEndpoints registers /preview/, /serve/, and /dev/ on the
// preview mux ONLY (FR-005, FR-006). These paths are not registered on the
// main mux.
//
// Auth model: token-only (FR-023). No RequireSessionCookieOrBearer, no
// RequireMatchingOriginOnStateChanging (FR-023a).
//
// /preview/ is the unified route for the web_serve tool.
// /serve/ and /dev/ are kept ONLY for replay of historical session transcripts
// that still link to the old URL shapes. The legacy serve_workspace (static
// mode) and run_in_workspace (dev mode) tools that produced those registrations
// are removed; no new registration can reach these back-compat handlers.
// Scheduled for deletion in v0.2 cleanup (target 2026-08-01).
func (a *restAPI) registerPreviewEndpoints(cm previewHandlerRegistrar) {
	cm.RegisterPreviewHandler("/preview/", http.HandlerFunc(a.HandlePreview))
	cm.RegisterPreviewHandler("/serve/", http.HandlerFunc(a.HandleServeWorkspace))
	cm.RegisterPreviewHandler("/dev/", http.HandlerFunc(a.HandleDevProxy))
}

// handleUsersWithParam dispatches /api/v1/users/{username}[/subpath] requests
// to the appropriate user-management handler based on HTTP method and path suffix.
//
// Routing table:
//
//	DELETE /api/v1/users/{username}          → HandleUserDelete
//	PUT    /api/v1/users/{username}/password → HandleUserResetPassword
//	PATCH  /api/v1/users/{username}/role     → HandleUserChangeRole
//
// Unrecognized method+path combinations return 405 or 404 respectively.
// Auth and bypass guards are applied by the enclosing adminWrap wrapper;
// this function only handles dispatch.
func (a *restAPI) handleUsersWithParam(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/password") && r.Method == http.MethodPut:
		a.HandleUserResetPassword(w, r)
	case strings.HasSuffix(path, "/role") && r.Method == http.MethodPatch:
		a.HandleUserChangeRole(w, r)
	case r.Method == http.MethodDelete:
		a.HandleUserDelete(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
	// Persistence succeeded. Trigger reload so the in-memory config picks up the new token.
	// If reload fails, the new token is on disk but not yet active — return 500 so the
	// caller knows the token is not yet in effect and can retry.
	if err := a.agentLoop.TriggerReload(); err != nil {
		slog.Error("config reload after token rotation failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("token saved but reload failed: %v", err))
		return
	}
	jsonOK(w, gen.RotateTokenResponse{Token: newToken})
}

// httpHandlerRegistrar is the subset of channels.Manager used for route registration.
type httpHandlerRegistrar interface {
	RegisterHTTPHandler(pattern string, handler http.Handler)
}

// previewHandlerRegistrar is the subset of channels.Manager used to register
// routes on the preview mux (FR-005). Separate from httpHandlerRegistrar so
// that existing test mocks implementing the main-mux interface do not need to
// be updated when preview routes are added.
type previewHandlerRegistrar interface {
	RegisterPreviewHandler(pattern string, handler http.Handler)
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

// --- Me ---

// HandleMe handles GET /api/v1/me. Returns the authenticated user's RBAC role.
// The role is written into the request context by withAuth; this handler reads
// it and returns a MeInfo-shaped response (contracts/components/schemas/MeInfo.yaml).
// Traces to: contracts/openapi.yaml#/paths/~1me/get (operationId: getMe).
func (a *restAPI) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	role := config.UserRoleAdmin // default: dev-mode bypass treats callers as admin
	if ctxRole, ok := r.Context().Value(RoleContextKey{}).(config.UserRole); ok && ctxRole != "" {
		role = ctxRole
	}
	resp := gen.MeInfo{
		Role: gen.MeInfoRole(role),
	}
	jsonOK(w, resp)
}

// --- Devices ---

// HandleDevices handles GET /api/v1/devices. Returns pending pairing requests
// and already-paired devices. Device pairing infrastructure is not yet implemented;
// this handler returns valid empty arrays so the SPA renders its empty state.
// Traces to: contracts/openapi.yaml#/paths/~1devices/get (operationId: listDevices).
// Traces to: contracts/components/schemas/DevicesResponse.yaml.
func (a *restAPI) HandleDevices(w http.ResponseWriter, r *http.Request) {
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
		metas, partialErrs := a.agentLoop.ListAllSessions()
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
	if events == nil {
		events = []gen.ActivityEvent{}
	}
	if sessionWarning != "" {
		slog.Warn("rest: activity: partial results due to session listing errors", "warning", sessionWarning)
	}
	jsonOK(w, events)
}

// --- Providers ---

// HandleProviders handles GET/PUT/POST /api/v1/providers and sub-paths.
func (a *restAPI) HandleProviders(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/providers")
	sub = strings.TrimPrefix(sub, "/")

	switch {
	case r.Method == http.MethodGet && sub == "":
		// Return provider list derived from config model_list, enriched with
		// upstream available models for OpenAI-compatible providers.
		cfg := a.agentLoop.GetConfig()
		providerModels := make(map[string][]string)
		providerAPIKeys := make(map[string]string)
		providerOrder := make([]string, 0)
		for _, m := range cfg.Providers {
			providerName := inferProviderName(m.Provider, m.Model)
			if _, exists := providerModels[providerName]; !exists {
				providerOrder = append(providerOrder, providerName)
			}
			providerModels[providerName] = append(providerModels[providerName], m.ModelName)
			// Resolve API key for upstream model fetching.
			// APIKeyRef is resolved via process environment (set by InjectFromConfig).
			if _, hasKey := providerAPIKeys[providerName]; !hasKey {
				resolved := m.APIKey()
				if resolved == "" && m.APIKeyRef != "" {
					if v, err := a.resolveCredentialRef(m.APIKeyRef); err != nil {
						slog.Warn("rest: could not resolve provider credential", "ref", m.APIKeyRef, "error", err)
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
			var models []string
			var modelFetchWarning string
			// Try to fetch the full model list from the provider's upstream API.
			if apiKey, ok := providerAPIKeys[name]; ok {
				if baseURL := providers_pkg.GetDefaultAPIBase(name); baseURL != "" {
					if upstream, err := fetchUpstreamModels(baseURL, apiKey); err != nil {
						slog.Warn("rest: failed to fetch upstream models", "provider", name, "error", err)
						modelFetchWarning = fmt.Sprintf("could not fetch upstream model list: %v", err)
					} else if len(upstream) > 0 {
						models = upstream
					}
				}
			}
			// Fall back to configured models if upstream fetch failed or returned
			// nothing. Provider.yaml requires models:array — nil marshals as null
			// which fails Zod validation on the SPA.
			if models == nil {
				if configured, ok := providerModels[name]; ok && configured != nil {
					models = configured
				} else {
					models = []string{}
				}
			}
			// FR-104: report Connected only when the provider's API key resolves to
			// a non-empty credential. providerAPIKeys is populated above for every
			// provider that has either a resolvable api_key_ref or an inline api_key;
			// absence from the map means no key was found.
			status := gen.ProviderStatusDisconnected
			if _, hasKey := providerAPIKeys[name]; hasKey {
				status = gen.ProviderStatusConnected
			}
			p := gen.Provider{
				Id:     name,
				Name:   name,
				Status: status,
				Models: models,
			}
			if modelFetchWarning != "" {
				p.Warning = &modelFetchWarning
			}
			providers = append(providers, p)
		}
		if len(providers) == 0 {
			providers = append(providers, gen.Provider{
				Id:     "default",
				Name:   "Default",
				Status: gen.ProviderStatusDisconnected,
				Models: []string{},
			})
		}
		jsonOK(w, providers)

	case r.Method == http.MethodPut && sub != "" && !strings.HasSuffix(sub, "/test"):
		// PUT /api/v1/providers/{id} — update or insert a provider entry.
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
		// Check if the provider already exists.
		cfg := a.agentLoop.GetConfig()
		found := false
		for _, m := range cfg.Providers {
			if m.IsVirtual() {
				continue
			}
			pName := inferProviderName(m.Provider, m.Model)
			if pName == providerID {
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
		// Store API key in the encrypted credentials store (AES-256-GCM) and
		// reference it via api_key_ref in config.json. Refuses the operation if
		// the credential store is locked (SEC-23: no plaintext fallback).
		var credRefName string
		if req.ApiKey != nil && *req.ApiKey != "" {
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
		if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
			providerList, _ := m["providers"].([]any)
			updated := false
			for _, entry := range providerList {
				model, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				pName := inferProviderName(strVal(model, "provider"), strVal(model, "model"))
				if pName == providerID {
					if req.ApiKey != nil && *req.ApiKey != "" {
						model["api_key_ref"] = credRefName
						delete(model, "api_key")
						delete(model, "api_keys")
					}
					if req.Model != nil && *req.Model != "" {
						model["model"] = *req.Model
					}
					model["provider"] = providerID
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
					"model_name":  providerID,
					"provider":    providerID,
					"model":       modelVal,
					"api_key_ref": credRefName,
				}
				m["providers"] = append(providerList, newEntry)
			}
			return nil
		}); err != nil {
			slog.Error("rest: save config for provider update", "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
			return
		}
		// Trigger reload so the in-memory config picks up the new API key.
		if err := a.agentLoop.TriggerReload(); err != nil {
			slog.Error("config reload after provider update failed", "error", err)
			jsonErr(
				w,
				http.StatusInternalServerError,
				fmt.Sprintf("provider updated but config reload failed: %v", err),
			)
			return
		}
		jsonOK(w, gen.Provider{
			Id:     providerID,
			Name:   providerID,
			Status: gen.ProviderStatusConnected,
			Models: []string{},
		})

	case r.Method == http.MethodPost && strings.HasSuffix(sub, "/test"):
		// POST /api/v1/providers/{id}/test — verify the provider has an API key configured.
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
		for _, entry := range providerList {
			modelMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			pName := inferProviderName(strVal(modelMap, "provider"), strVal(modelMap, "model"))
			if pName == providerID {
				found = true
				// Check if API key is set: either via api_keys array or api_key_ref
				// pointing to the encrypted credentials store.
				apiKeys, _ := modelMap["api_keys"].([]any)
				apiKeyRef, _ := modelMap["api_key_ref"].(string)
				hasPlaintextKey := len(apiKeys) > 0 && apiKeys[0] != ""
				hasCredRef := false
				if apiKeyRef != "" {
					if v, err := a.resolveCredentialRef(apiKeyRef); err != nil {
						slog.Warn("rest: provider test: credential store error", "ref", apiKeyRef, "error", err)
					} else {
						hasCredRef = v != ""
					}
				}
				if !hasPlaintextKey && !hasCredRef {
					errMsg := "no API key configured for this provider"
					jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
					return
				}
				break
			}
		}
		if !found {
			errMsg := fmt.Sprintf("provider %q not configured", providerID)
			jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
			return
		}
		jsonOK(w, gen.OperationResult{Success: true})

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
		a.deleteMCPServer(w, serverID)

	case r.Method == http.MethodGet && serverID != "" && subSuffix == "tools":
		a.listMCPServerTools(w, serverID)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// listMCPServers reads configured MCP servers from config and returns them as
// McpServer[] (contracts/components/schemas/McpServer.yaml).
// Status is always "disconnected" — live connection state is not tracked at
// the config layer (the MCP manager reconnects at agent loop startup, not per-request).
func (a *restAPI) listMCPServers(w http.ResponseWriter, _ *http.Request) {
	cfg := a.agentLoop.GetConfig()
	result := make([]gen.McpServer, 0, len(cfg.Tools.MCP.Servers))
	for name, srv := range cfg.Tools.MCP.Servers {
		transport := gen.McpServerTransportStdio
		switch srv.Type {
		case "sse":
			transport = gen.McpServerTransportSse
		case "http":
			transport = gen.McpServerTransportHttp
		}
		result = append(result, gen.McpServer{
			Id:        name,
			Name:      name,
			Transport: transport,
			Status:    gen.McpServerStatusDisconnected,
			ToolCount: 0,
		})
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
	cfg := a.agentLoop.GetConfig()
	if _, exists := cfg.Tools.MCP.Servers[serverID]; !exists {
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
	resp := gen.McpServer{
		Id:        req.Name,
		Name:      req.Name,
		Transport: respTransport,
		Status:    gen.McpServerStatusDisconnected,
		ToolCount: 0,
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

func (a *restAPI) deleteMCPServer(w http.ResponseWriter, id string) {
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
	jsonOK(w, map[string]string{"status": "removed", "id": id})
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

	// Re-auth gate (Spec-3 FR-3.3 / Spec-6 FR-12.2): changing which tools an
	// agent may call is a sensitive capability grant and requires the single-use
	// re-auth consent token — the same gate the Integrations PUT enforces.
	if user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && user != nil {
		if !a.requireReAuth(w, r, user.Username) {
			return
		}
	} else {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req gen.AgentToolsUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "AgentToolsUpdateRequest", &req, validateEnabled) {
		return
	}

	// Extract builtin fields with defaults.
	builtinDefaultPolicy := ""
	var builtinPolicies map[string]string
	if req.Builtin != nil {
		if req.Builtin.DefaultPolicy != nil {
			builtinDefaultPolicy = string(*req.Builtin.DefaultPolicy)
		}
		if req.Builtin.Policies != nil {
			builtinPolicies = make(map[string]string, len(*req.Builtin.Policies))
			for k, v := range *req.Builtin.Policies {
				builtinPolicies[k] = string(v)
			}
		}
	}

	// Convert legacy format to policy format if needed.
	if req.Builtin != nil && builtinDefaultPolicy == "" && req.Builtin.Mode != nil {
		switch string(*req.Builtin.Mode) {
		case "explicit":
			builtinDefaultPolicy = "deny"
			if req.Builtin.Visible != nil {
				builtinPolicies = make(map[string]string, len(*req.Builtin.Visible))
				for _, name := range *req.Builtin.Visible {
					builtinPolicies[name] = "allow"
				}
			}
		case "inherit":
			builtinDefaultPolicy = "allow"
		}
	}
	if builtinDefaultPolicy == "" {
		builtinDefaultPolicy = "allow"
	}

	// Validate policy values.
	validPolicies := map[string]bool{"allow": true, "ask": true, "deny": true}
	if !validPolicies[builtinDefaultPolicy] {
		jsonErr(w, http.StatusUnprocessableEntity, "builtin.default_policy must be 'allow', 'ask', or 'deny'")
		return
	}
	for name, p := range builtinPolicies {
		if !validPolicies[p] {
			jsonErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("invalid policy %q for tool %q", p, name))
			return
		}
	}

	// Validate MCP server IDs reference configured servers.
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

	// Persist to config.json.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		agents, _ := m["agents"].(map[string]any)
		if agents == nil {
			return fmt.Errorf("agents section not found in config")
		}
		list, _ := agents["list"].([]any)
		for i, raw := range list {
			agentMap, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if agentMap["id"] == agentID {
				builtinCfg := map[string]any{
					"default_policy": builtinDefaultPolicy,
				}
				if len(builtinPolicies) > 0 {
					builtinCfg["policies"] = builtinPolicies
				}
				toolsCfg := map[string]any{
					"builtin": builtinCfg,
				}
				if len(mcpServers) > 0 {
					servers := make([]map[string]any, 0, len(mcpServers))
					for _, s := range mcpServers {
						srv := map[string]any{"id": s.ID}
						if len(s.Tools) > 0 {
							srv["tools"] = s.Tools
						}
						servers = append(servers, srv)
					}
					toolsCfg["mcp"] = map[string]any{"servers": servers}
				}
				agentMap["tools"] = toolsCfg
				list[i] = agentMap
				return nil
			}
		}
		return fmt.Errorf("agent %q not found in config list", agentID)
	}); err != nil {
		slog.Error("rest: update agent tools config", "agent_id", agentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Trigger a reload so the agent's atomic toolPolicy pointer
	// (pkg/agent/instance.go:290 — populated by ReloadProviderAndConfig)
	// is swapped to the new policy. Without this the next turn's
	// resolveToolPolicyAtExec / FilterToolsByPolicy still sees the previous
	// snapshot, and (e.g.) an exec call freshly bumped to "ask" runs as
	// "allow" because LoadToolPolicy returns the stale pointer. The earlier
	// "no reload needed" claim was wrong — config-on-disk and the in-memory
	// pointer are decoupled. Reload is cheap and idempotent.
	if err := a.agentLoop.TriggerReload(); err != nil {
		// ErrReloadNotConfigured is normal in unit tests where the full gateway
		// reload pipeline is not wired — treat it as a no-op and continue.
		if !errors.Is(err, agent.ErrReloadNotConfigured) {
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
		}
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
		if !validChannelIDs[gen.ChannelId(channelID)] {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("channel %q not found", channelID))
			return
		}

		if len(parts) == 1 {
			// GET /api/v1/channels/{id}
			if r.Method == http.MethodGet {
				a.getChannelConfig(w, channelID)
				return
			}
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
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

	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()
	// channelEnabledByType returns true when any instance of the given channel type
	// is enabled in the map. In v0.1 cap-1 guarantees at most one instance per type.
	channelEnabledByType := func(channelType string) bool {
		for _, inst := range cfg.Channels {
			if inst.Type == channelType && inst.Enabled {
				return true
			}
		}
		return false
	}
	channels := []gen.ChannelEntry{
		{Id: "webchat", Name: "Web Chat", Transport: "websocket", Enabled: true, Description: "Built-in browser chat"},
		{
			Id:          "telegram",
			Name:        "Telegram",
			Transport:   "webhook",
			Enabled:     channelEnabledByType("telegram"),
			Description: "Telegram Bot API",
		},
		{
			Id:          "discord",
			Name:        "Discord",
			Transport:   "websocket",
			Enabled:     channelEnabledByType("discord"),
			Description: "Discord Gateway",
		},
		{
			Id:          "slack",
			Name:        "Slack",
			Transport:   "websocket",
			Enabled:     channelEnabledByType("slack"),
			Description: "Slack Socket Mode",
		},
		{
			Id:              "whatsapp",
			Name:            "WhatsApp",
			Transport:       "native",
			Enabled:         channelEnabledByType("whatsapp"),
			Description:     "WhatsApp (native, whatsmeow)",
			NativeAvailable: boolPtr(whatsappnative.NativeAvailable),
		},
		{
			Id:          "feishu",
			Name:        "Feishu / Lark",
			Transport:   "webhook",
			Enabled:     channelEnabledByType("feishu"),
			Description: "Feishu (Lark) Bot",
		},
		{
			Id:          "dingtalk",
			Name:        "DingTalk",
			Transport:   "webhook",
			Enabled:     channelEnabledByType("dingtalk"),
			Description: "DingTalk Bot",
		},
		{
			Id:          "wecom",
			Name:        "WeCom",
			Transport:   "webhook",
			Enabled:     channelEnabledByType("wecom"),
			Description: "WeCom (WeChat Work) Bot",
		},
		{
			Id:          "weixin",
			Name:        "Weixin",
			Transport:   "webhook",
			Enabled:     channelEnabledByType("weixin"),
			Description: "Weixin (WeChat) Official Account",
		},
		{
			Id:          "line",
			Name:        "LINE",
			Transport:   "webhook",
			Enabled:     channelEnabledByType("line"),
			Description: "LINE Messaging API",
		},
		{
			Id:          "qq",
			Name:        "QQ",
			Transport:   "websocket",
			Enabled:     channelEnabledByType("qq"),
			Description: "QQ via napcat",
		},
		{
			Id:          "irc",
			Name:        "IRC",
			Transport:   "tcp",
			Enabled:     channelEnabledByType("irc"),
			Description: "Internet Relay Chat",
		},
		{
			Id:          "matrix",
			Name:        "Matrix",
			Transport:   "http",
			Enabled:     channelEnabledByType("matrix"),
			Description: "Matrix protocol",
		},
		{
			Id:          "google-chat",
			Name:        "Google Chat",
			Transport:   "webhook",
			Enabled:     channelEnabledByType("google-chat"),
			Description: "Google Chat (webhook or service account)",
		},
		// M11: email is NOT listed here — it is a TOOL surface (per-agent mailbox,
		// GET/PUT/DELETE /api/v1/agents/{id}/mailbox), not a conversational channel.
	}

	// Overlay per-instance surface (Spec-2 FR-2.5): instance_id and identity.
	// For each list entry that maps to a configured channel instance, expose the
	// map key (instance_id) and the persisted routing identity so the SPA can
	// address per-instance configure/enable/routing endpoints and render the
	// identity override without re-deriving it. In v0.1 cap-1 the instance key
	// equals the channel type, so the entry id matches the map key by type; we
	// look up the instance by matching Type to keep this correct if a future map
	// key diverges from the type. webchat is built-in and has no instance entry.
	applyInstanceOverlay(channels, cfg.Channels)

	// Overlay degraded state from the runtime channel manager. Channels that
	// failed to construct at startup are marked degraded=true with the init
	// error as the reason. whatsapp_native failures map to the "whatsapp" entry
	// because both transports share one list entry.
	if mgr := a.agentLoop.GetChannelManager(); mgr != nil {
		failed := mgr.FailedChannels()
		applyDegradedOverlay(channels, failed)
		// Warn for any failed channel whose (normalised) id has no matching entry
		// in the channels list — these are dead channels that would otherwise be
		// silently invisible to operators.
		if len(failed) > 0 {
			entryIDs := make(map[string]struct{}, len(channels))
			for _, e := range channels {
				entryIDs[string(e.Id)] = struct{}{}
			}
			for _, f := range failed {
				id := f.Name
				if id == "whatsapp_native" {
					id = "whatsapp"
				}
				if _, matched := entryIDs[id]; !matched {
					slog.Warn("channels: failed channel has no matching entry in channels list",
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
// Normalisation rule: "whatsapp_native" maps to the list entry "whatsapp"
// because both the bridge and native transports share a single ChannelEntry.
func applyDegradedOverlay(channelList []gen.ChannelEntry, failed []channels.ChannelInitError) {
	if len(failed) == 0 {
		return
	}
	// Build a map of normalised registry-id → error reason.
	degradedMap := make(map[string]string, len(failed))
	for _, f := range failed {
		id := f.Name
		if id == "whatsapp_native" {
			id = "whatsapp"
		}
		degradedMap[id] = f.Err.Error()
	}
	for i := range channelList {
		if reason, ok := degradedMap[string(channelList[i].Id)]; ok {
			r := reason
			channelList[i].Degraded = boolPtr(true)
			channelList[i].DegradedReason = &r
		}
	}
}

// applyInstanceOverlay populates instance_id and identity on each list entry
// that maps to a configured channel instance (Spec-2 FR-2.5). It is a pure
// function extracted from HandleChannels so it can be unit-tested without a full
// REST stack.
//
// Matching rule: an entry maps to an instance when the instance's Type equals
// the entry id (the v0.1 cap-1 contract: instance key == channel type). The
// instance_id surfaced is the actual config map key, so the SPA addresses the
// right per-instance endpoint even if a future key diverges from the type.
// Entries with no configured instance (e.g. the built-in webchat, or a channel
// type the operator has never touched) are left untouched.
func applyInstanceOverlay(channelList []gen.ChannelEntry, instances map[string]config.ChannelInstanceConfig) {
	if len(instances) == 0 {
		return
	}
	// Index instances by type → (key, identity) for an O(1) lookup per entry.
	type instMeta struct {
		key      string
		identity *config.ChannelIdentity
	}
	byType := make(map[string]instMeta, len(instances))
	for key, inst := range instances {
		t := strings.ToLower(strings.TrimSpace(inst.Type))
		if t == "" {
			t = strings.ToLower(strings.TrimSpace(key))
		}
		byType[t] = instMeta{key: key, identity: inst.Identity}
	}
	for i := range channelList {
		meta, ok := byType[strings.ToLower(string(channelList[i].Id))]
		if !ok {
			continue
		}
		instanceID := meta.key
		channelList[i].InstanceId = &instanceID
		if meta.identity == nil {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(meta.identity.Kind))
		// Only surface a well-formed identity; a malformed persisted identity is
		// ignored rather than emitting a schema-invalid entry.
		var entryKind gen.ChannelEntryIdentityKind
		switch kind {
		case "agent":
			entryKind = gen.ChannelEntryIdentityKindAgent
		case "user":
			entryKind = gen.ChannelEntryIdentityKindUser
		default:
			continue
		}
		ident := struct { // not-wire-format: composite literal of the generated gen.ChannelEntry.Identity anonymous field type — not a parallel wire type
			Id   *string                      `json:"id,omitempty"`
			Kind gen.ChannelEntryIdentityKind `json:"kind"`
		}{
			Kind: entryKind,
		}
		if id := strings.TrimSpace(meta.identity.ID); id != "" {
			idCopy := id
			ident.Id = &idCopy
		}
		channelList[i].Identity = &ident
	}
}

// validChannelIDs is the set of channel IDs that can be toggled via the API.
// "webchat" is always enabled and intentionally excluded.
//
// drift-guard: keyed by the generated gen.ChannelId enum and populated with the
// named enum constants (not string literals), so removing or renaming a ChannelId
// value breaks this build until the list is brought back in sync with the contract.
var validChannelIDs = map[gen.ChannelId]bool{
	gen.Telegram: true, gen.Discord: true, gen.Slack: true, gen.Whatsapp: true,
	gen.Feishu: true, gen.Dingtalk: true, gen.Wecom: true, gen.Weixin: true,
	gen.Line: true, gen.Qq: true, gen.Irc: true,
	gen.Matrix: true, gen.GoogleChat: true,
	gen.Email: true,
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
// Returns the channel-wildcard AgentBinding for the given channel, or null agent ID
// if none exists.
func (a *restAPI) getChannelRouting(w http.ResponseWriter, channelID string) {
	cfg := a.agentLoop.GetConfig()
	idx := channelWildcardIdx(cfg.Bindings, channelID)
	var resp gen.ChannelRouting
	if idx >= 0 {
		id := cfg.Bindings[idx].AgentID
		resp.DefaultAgentId = &id
	}
	jsonOK(w, resp)
}

// setChannelRouting handles PUT /api/v1/channels/{id}/routing.
// Upserts (or removes) the channel-wildcard AgentBinding for the given channel.
func (a *restAPI) setChannelRouting(w http.ResponseWriter, r *http.Request, channelID string) {
	var req gen.SetChannelRoutingJSONRequestBody
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ChannelRouting", &req, validateEnabled) {
		return
	}

	cfg := a.agentLoop.GetConfig()

	// Validate the agent ID when non-empty.
	if req.DefaultAgentId != nil && *req.DefaultAgentId != "" {
		agentID := *req.DefaultAgentId
		var found *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == agentID {
				found = &cfg.Agents.List[i]
				break
			}
		}
		if found == nil {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
			return
		}
		// A worker is never a chat target (invoked only via delegation), so it
		// cannot serve as a channel's default agent. Reject before any work,
		// mirroring updateAgent's rejection of starring a worker as default (M1).
		if found.IsWorker() {
			jsonErr(w, http.StatusBadRequest, "workers are not chat targets and cannot be a channel's default agent")
			return
		}
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
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
					// This is the wildcard binding to remove — skip it.
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

func (a *restAPI) setChannelEnabled(w http.ResponseWriter, channelID string, enabled bool) {
	if !validChannelIDs[gen.ChannelId(channelID)] {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("channel %q not found", channelID))
		return
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
		// Persist the type discriminator (Spec-2 FR-2.5). In v0.1 cap-1 the map
		// key equals the channel type, so write it explicitly rather than relying
		// solely on load-time normalizeChannelMap inference — this keeps the
		// on-disk instance self-describing and makes cap-1 validation deterministic.
		ch["type"] = channelID
		ch["enabled"] = enabled
		return nil
	}); err != nil {
		// Cap-1 (FR-2.3): a config that now holds >1 instance of a type (e.g. a
		// pre-existing hand-edited duplicate surfaced by the load-time validator)
		// is a client-correctable constraint violation, not a server fault —
		// return a clean 422 "one-per-type" rather than an opaque 500.
		if errors.Is(err, config.ErrChannelsCap1Violated) {
			slog.Warn(
				"rest: set channel enabled rejected by cap-1",
				"channel",
				channelID,
				"enabled",
				enabled,
				"error",
				err,
			)
			jsonErr(w, http.StatusUnprocessableEntity, "one-per-type in v0.1.0: "+err.Error())
			return
		}
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
		if err := a.triggerReloadAndWait(); err != nil {
			verb := "start"
			if !enabled {
				verb = "stop"
			}
			slog.Error("rest: channel reload after enable toggle failed",
				"channel", channelID, "enabled", enabled, "error", err)
			jsonErr(w, http.StatusInternalServerError,
				fmt.Sprintf("channel %s saved but failed to %s: %v", channelID, verb, err))
			return
		}
	}
	jsonOK(w, gen.ChannelEnabledResponse{Id: gen.ChannelEnabledResponseId(channelID), Enabled: enabled})
}

// channelSensitiveFields maps channel TYPE to their secret/credential field names.
// These are redacted in GET responses (replaced with "[configured]" if set).
// Keyed by TYPE (not instance ID) because field sensitivity is type-level knowledge
// shared across all instances of that type (SEC-23 type-vs-instance boundary).
//
// drift-guard: keyed by the generated gen.ChannelId enum with named enum
// constants, so removing/renaming a ChannelId value breaks this build.
var channelSensitiveFields = map[gen.ChannelId][]string{
	gen.Telegram:   {"token"},
	gen.Discord:    {"token"},
	gen.Slack:      {"bot_token", "app_token"},
	gen.Feishu:     {"app_secret", "encrypt_key", "verification_token"},
	gen.Matrix:     {"access_token", "crypto_passphrase"},
	gen.Line:       {"channel_secret", "channel_access_token"},
	gen.Dingtalk:   {"client_secret"},
	gen.Qq:         {"app_secret"},
	gen.Wecom:      {"secret"},
	gen.Irc:        {"password", "nickserv_password", "sasl_password"},
	gen.Weixin:     {"token"},
	gen.Whatsapp:   {},
	gen.GoogleChat: {"webhook_url", "service_account_json"},
	// email: password is the only secret field (username is public config, not a secret).
	gen.Email: {"password"},
}

// channelRequiredFields maps channel TYPE to fields that must be non-empty for the
// channel to work. Keyed by TYPE for the same reason as channelSensitiveFields —
// required fields are type-level knowledge, not instance-specific.
//
// drift-guard: keyed by the generated gen.ChannelId enum with named enum
// constants, so removing/renaming a ChannelId value breaks this build.
var channelRequiredFields = map[gen.ChannelId][]string{
	gen.Telegram:   {"token"},
	gen.Discord:    {"token"},
	gen.Slack:      {"bot_token"},
	gen.Feishu:     {"app_id", "app_secret"},
	gen.Matrix:     {"homeserver", "user_id", "access_token"},
	gen.Line:       {"channel_secret", "channel_access_token"},
	gen.Dingtalk:   {"client_id", "client_secret"},
	gen.Qq:         {"app_id", "app_secret"},
	gen.Wecom:      {"bot_id", "secret"},
	gen.Irc:        {"server", "nick"},
	gen.Weixin:     {"token"},
	gen.Whatsapp:   {},
	gen.GoogleChat: {},
	// email: imap_host, smtp_host, username, and password (credential) are all required.
	gen.Email: {"imap_host", "smtp_host", "username", "password"},
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
	for _, field := range channelSensitiveFields[gen.ChannelId(channelID)] {
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

// validateChannelIdentity validates the per-instance routing identity supplied
// in a configure request body (Spec-2 US-5 / FR-2.9). It mirrors the
// ChannelIdentity contract: required "kind" in {agent,user}; "id" required and
// non-empty when kind=="agent" (the target agent), forbidden/ignored otherwise;
// no unknown fields. Returns "" when valid, or a human-readable reason.
func validateChannelIdentity(raw any) string {
	obj, ok := raw.(map[string]any)
	if !ok {
		return "must be an object with a \"kind\" field"
	}
	for k := range obj {
		switch k {
		case "kind", "id":
		default:
			return fmt.Sprintf("unknown field %q", k)
		}
	}
	kindRaw, present := obj["kind"]
	if !present {
		return "\"kind\" is required"
	}
	kind, isStr := kindRaw.(string)
	if !isStr {
		return "\"kind\" must be a string"
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "agent":
		idRaw, hasID := obj["id"]
		if !hasID {
			return "\"id\" is required when kind is \"agent\""
		}
		idStr, idIsStr := idRaw.(string)
		if !idIsStr {
			return "\"id\" must be a string"
		}
		if strings.TrimSpace(idStr) == "" {
			return "\"id\" must be non-empty when kind is \"agent\""
		}
	case "user":
		// id is optional and ignored for user-kind.
		if idRaw, hasID := obj["id"]; hasID {
			if _, idIsStr := idRaw.(string); !idIsStr {
				return "\"id\" must be a string"
			}
		}
	default:
		return fmt.Sprintf("\"kind\" must be \"agent\" or \"user\", got %q", kind)
	}
	return ""
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

	// Validate the optional per-instance identity (Spec-2 US-5 / FR-2.9) before
	// any write. A malformed identity must be rejected up front (422) rather than
	// silently dropped, otherwise identity-based routing would be a no-op without
	// the operator ever knowing. The field is persisted as-is and later wired
	// into ResolveRoute for inbound messages on this instance.
	if raw, present := updates["identity"]; present {
		if raw == nil {
			// Explicit null clears the identity override.
			delete(updates, "identity")
		} else if errMsg := validateChannelIdentity(raw); errMsg != "" {
			jsonErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("invalid identity: %s", errMsg))
			return
		}
	}

	// SEC-23 / #289: route secret fields into the encrypted credential store and
	// persist only their <field>_ref in config.json. Every channel constructor
	// reads its secret via the *_ref (e.g. token_ref); an inline plaintext secret
	// is both a plaintext-at-rest violation AND unreadable by the constructor —
	// so a UI-configured token-based channel would never start.
	var clearedRefs []string // credentials to delete AFTER the config write commits
	for _, field := range channelSensitiveFields[gen.ChannelId(channelID)] {
		raw, present := updates[field]
		if !present {
			continue
		}
		delete(updates, field) // never persist the inline plaintext
		refField := field + "_ref"
		refName := channelCredKey(channelID, field)
		secret, isStr := raw.(string)
		if !isStr && raw != nil {
			// A non-string, non-null secret (e.g. {"token": 123}) would collapse to
			// "" and be misread as a clear — reject it instead of silently revoking.
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("%s must be a string", field))
			return
		}
		if strings.TrimSpace(secret) == "" {
			// Clearing: drop the ref now, but delete the stored credential only
			// AFTER the config write commits (below). Deleting first would strand
			// the channel pointing at a missing credential if the config write fails.
			updates[refField] = ""
			clearedRefs = append(clearedRefs, refName)
			continue
		}
		if _, err := a.storeCredential(refName, secret); err != nil {
			slog.Error("rest: store channel credential", "channel", channelID, "field", field, "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not store %s credential: %v", field, err))
			return
		}
		updates[refField] = refName
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
		// Persist the type discriminator (Spec-2 FR-2.5): the map key equals the
		// channel type in v0.1 cap-1, so record it explicitly so the on-disk
		// instance is self-describing and cap-1 validation is deterministic.
		ch["type"] = channelID
		// Invariant (#289/SEC-23): a known secret field is never stored inline in
		// config.json — it lives only in the credential store via its <field>_ref.
		// This also scrubs any stale plaintext left by the pre-#289 blind merge.
		for _, field := range channelSensitiveFields[gen.ChannelId(channelID)] {
			delete(ch, field)
		}
		channels[channelID] = ch
		updatedCh = ch
		return nil
	}); err != nil {
		// Cap-1 (FR-2.3): surface a >1-instance-per-type violation as a clean 422
		// "one-per-type" rather than an opaque 500. Note the credential write above
		// has already committed; the config write is reverted (the refresh failed),
		// so a retry after the operator removes the duplicate succeeds.
		if errors.Is(err, config.ErrChannelsCap1Violated) {
			slog.Warn("rest: configure channel rejected by cap-1", "channel", channelID, "error", err)
			jsonErr(w, http.StatusUnprocessableEntity, "one-per-type in v0.1.0: "+err.Error())
			return
		}
		slog.Error("rest: configure channel", "channel", channelID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
	}

	// Config (with cleared refs) is durable now — delete the now-unreferenced
	// credentials. A failure here only leaves an orphaned encrypted blob (the ref
	// is already gone), so log rather than fail the already-committed request.
	for _, refName := range clearedRefs {
		if err := a.removeStoredCredential(refName); err != nil {
			slog.Error("rest: delete cleared channel credential", "channel", channelID, "ref", refName, "error", err)
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

	cid := gen.ChannelId(channelID)
	required := channelRequiredFields[cid]
	sensitive := make(map[string]bool, len(channelSensitiveFields[cid]))
	for _, f := range channelSensitiveFields[cid] {
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
	if metas, partialErrs := a.agentLoop.ListAllSessions(); len(partialErrs) > 0 {
		for _, pe := range partialErrs {
			slog.Warn("rest: storage stats: list sessions partial error", "error", pe)
			warnings = append(warnings, sanitizePartialError(pe))
		}
		sessionCount = len(metas)
	} else {
		sessionCount = len(metas)
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
func (a *restAPI) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// session_id may come from either a query parameter or a form field that
	// appears before any file parts. We prefer the query param for simplicity.
	sessionID := r.URL.Query().Get("session_id")

	// Parse the multipart stream without buffering file content in memory.
	reader, err := r.MultipartReader()
	if err != nil {
		slog.Warn("rest: upload: multipart reader failed", "error", err)
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart request: %v", err))
		return
	}

	var resp gen.UploadFilesResponse

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

		// Non-file field — check for session_id override (only if not already set).
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
			} else {
				// Discard unrecognized non-file fields.
				if _, discardErr := io.Copy(io.Discard, part); discardErr != nil {
					slog.Warn("rest: upload: discard field failed", "field", formName, "error", discardErr)
				}
				part.Close()
			}
			continue
		}

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

		slog.Info("rest: upload: file stored",
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

// HandleMedia serves a media file by its ref ID extracted from the URL path
// (e.g. /api/v1/media/abc123 resolves "media://abc123" via MediaStore).
func (a *restAPI) HandleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.setCORSHeaders(w, r)

	// Always read the current store via the agent loop. The store is replaced
	// on every restartServices, so a.mediaStore would go stale after the first
	// reload (screenshots stored in the new store are invisible to the old one).
	store := a.agentLoop.GetMediaStore()
	if store == nil {
		store = a.mediaStore
	}
	if store == nil {
		jsonErr(w, http.StatusServiceUnavailable, "media store not available")
		return
	}

	refID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/media/"), "/")
	if refID == "" || strings.ContainsAny(refID, "/\\") || strings.Contains(refID, "..") {
		jsonErr(w, http.StatusBadRequest, "invalid media ref")
		return
	}

	localPath, meta, err := store.ResolveWithMeta("media://" + refID)
	if err != nil {
		slog.Warn("rest: media ref not found", "ref", refID, "error", err.Error())
		jsonErr(w, http.StatusNotFound, "media not found")
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
