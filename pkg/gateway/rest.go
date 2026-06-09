//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"crypto/rand"
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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dapicom-ai/omnipus/pkg/agent"
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
	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/notifications"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	providers_pkg "github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/security"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/skills"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// Version is set at build time via -ldflags "-X github.com/dapicom-ai/omnipus/pkg/gateway.Version=x.y.z".
var Version = "dev"

// restAPI holds shared dependencies for all REST endpoint handlers.
// Handlers are registered as method-dispatching http.HandlerFuncs in gateway.go.
// Note: do NOT cache *config.Config here — use a.agentLoop.GetConfig() for
// the current config, since config can hot-reload.
type restAPI struct {
	agentLoop     *agent.AgentLoop
	allowedOrigin string
	onboardingMgr *onboarding.Manager  // manages first-launch + doctor state
	homePath      string               // ~/.omnipus — root of the data directory
	configMu      sync.Mutex           // guards safeUpdateConfigJSON (read-modify-write cycle)
	taskStore     *taskstore.TaskStore // task persistence
	taskExecutor  *agent.TaskExecutor  // task execution engine
	credStore     *credentials.Store   // shared unlocked credential store (injected at boot)
	mediaStore    media.MediaStore     // shared media store for serving media files
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

	// allowGodMode is set when the gateway was started with --allow-god-mode.
	// When false, the agent update handler rejects sandbox_profile=off with 403.
	// Mirrors the same field on AgentLoop for runtime tool coercion.
	// Latch (2) — REST enforcement.
	allowGodMode bool

	// cronService backs the /api/v1/schedules CRUD + run-now + pause endpoints
	// (#264). Schedules are a contract-first projection over cron.CronJob.
	// Nil in test setups that do not exercise schedules.
	cronService *cron.CronService

	// notifStore backs /api/v1/notifications (#264). Per-user, file-based.
	// Nil in test setups that do not exercise notifications.
	notifStore *notifications.Store

	// auditor is the shared audit logger for mutation events on projects and
	// board tasks. Sourced from agentLoop.AuditLogger() at construction time;
	// may be nil when audit logging is disabled (best-effort — nil-safe callers).
	auditor *audit.Logger

	// taskStripedMu is a 64-shard mutex pool keyed by FNV hash of the task ID.
	// Held for the full read→mutate→write cycle of a board task to make RMW
	// atomic (prevents a race between two concurrent /start calls or between
	// /start and the completion callback). The pool is fixed-size so memory
	// usage is O(1) regardless of task count, matching the pattern in
	// pkg/memory/jsonl.go::JSONLStore.
	taskStripedMu [64]sync.Mutex
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
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Csrf-Token")
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
	if m.ProjectID != "" {
		s.ProjectId = &m.ProjectID
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
			cfg := a.agentLoop.GetConfig()
			for _, ag := range cfg.Agents.List {
				if ag.IsActive() {
					agentID = ag.ID
					break
				}
			}
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
	sm := steeringModeOrDefault(cfg.Agents.Defaults.SteeringMode)
	return gen.Agent{
		TimeoutSeconds:    cfg.Agents.Defaults.TimeoutSeconds,
		MaxToolIterations: cfg.Agents.Defaults.MaxToolIterations,
		SteeringMode:      sm,
		ToolFeedback:      cfg.Agents.Defaults.ToolFeedback.Enabled,
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
		ag.Type = gen.AgentType(ac.ResolveType(coreagent.IsCoreAgent))
		ag.Locked = ac.Locked
		ag.Model = &model
		ag.Status = gen.AgentStatus(computeAgentStatus(ac.ID, activeIDs, soul, ac.Locked))
		ag.Soul = soul
		ag.Default = boolPtr(ac.Default)
		if len(ac.Skills) > 0 {
			skills := make([]string, len(ac.Skills))
			copy(skills, ac.Skills)
			ag.Skills = &skills
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
			ag.Type = gen.AgentType(ac.ResolveType(coreagent.IsCoreAgent))
			ag.Locked = ac.Locked
			ag.Model = &model
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
	color := ""
	if req.Color != nil {
		color = *req.Color
	}
	icon := ""
	if req.Icon != nil {
		icon = *req.Icon
	}
	ac := config.AgentConfig{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: description,
		Color:       color,
		Icon:        icon,
		Type:        config.AgentTypeCustom,
	}
	if req.Model != nil && *req.Model != "" {
		ac.Model = &config.AgentModelConfig{Primary: *req.Model}
	}
	if req.Skills != nil && len(*req.Skills) > 0 {
		ac.Skills = make([]string, len(*req.Skills))
		copy(ac.Skills, *req.Skills)
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
			newAgent["model"] = map[string]any{"primary": ac.Model.Primary}
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
		agents["list"] = append(list, newAgent)
		return nil
	}); err != nil {
		slog.Error("rest: save config for new agent", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
		return
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
	ag.Type = gen.AgentTypeCustom
	ag.Model = &model
	ag.Status = gen.AgentStatusDraft
	if len(ac.Skills) > 0 {
		skills := make([]string, len(ac.Skills))
		copy(skills, ac.Skills)
		ag.Skills = &skills
	}
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
	if found.Locked {
		jsonErr(w, http.StatusForbidden, "cannot delete a locked (core) agent")
		return
	}
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
	// awaitReload sleeps 100ms after triggering so the in-memory config is
	// updated before the 204 response is sent back to the caller (prevents a
	// race where an immediate GET /sessions/:id still sees agent_removed=false).
	if err := a.awaitReload(); err != nil {
		slog.Error("rest: deleteAgent: reload failed", "agent_id", id, "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
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
	// Enforce god-mode latches (1) and (2) at the REST write gate.
	// Reject sandbox_profile=off unless both sandbox.GodModeAvailable (build
	// tag) and a.allowGodMode (--allow-god-mode boot flag) are true.
	if req.SandboxProfile != nil && config.SandboxProfile(*req.SandboxProfile) == config.SandboxProfileOff {
		if !sandbox.GodModeAvailable {
			jsonErr(w, http.StatusForbidden,
				"sandbox_profile=off is not available in this build")
			return
		}
		if !a.allowGodMode {
			jsonErr(w, http.StatusForbidden,
				"sandbox_profile=off requires --allow-god-mode at gateway boot")
			return
		}
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
				if req.TimeoutSeconds != nil {
					agentMap["timeout_seconds"] = *req.TimeoutSeconds
				}
				if req.MaxToolIterations != nil {
					agentMap["max_tool_iterations"] = *req.MaxToolIterations
				}
				if req.SteeringMode != nil {
					agentMap["steering_mode"] = *req.SteeringMode
				}
				if req.ToolFeedback != nil {
					tfMap, _ := agentMap["tool_feedback"].(map[string]any)
					if tfMap == nil {
						tfMap = map[string]any{}
						agentMap["tool_feedback"] = tfMap
					}
					tfMap["enabled"] = *req.ToolFeedback
				}
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
				// Skills: replace the agent's skill list when the caller sends the field.
				// An explicit empty array removes all skills. Nil (absent) leaves unchanged.
				if req.Skills != nil {
					if len(*req.Skills) > 0 {
						agentMap["skills"] = *req.Skills
					} else {
						delete(agentMap, "skills")
					}
				}
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
		// Heartbeat fields are top-level in config.json.
		if req.HeartbeatEnabled != nil || req.HeartbeatInterval != nil {
			hbMap, _ := m["heartbeat"].(map[string]any)
			if hbMap == nil {
				hbMap = map[string]any{}
				m["heartbeat"] = hbMap
			}
			if req.HeartbeatEnabled != nil {
				hbMap["enabled"] = *req.HeartbeatEnabled
			}
			if req.HeartbeatInterval != nil {
				hbMap["interval"] = *req.HeartbeatInterval
			}
		}
		return nil
	}); err != nil {
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
	needsReload := req.Soul != nil || req.Heartbeat != nil || req.Instructions != nil
	var reloadWarning string
	if needsReload {
		if err := a.agentLoop.TriggerReload(); err != nil {
			slog.Error("config reload after agent update failed", "error", err)
			reloadWarning = fmt.Sprintf("config reload failed: %v", err)
		}
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
	ag.Type = gen.AgentType(foundAgent.ResolveType(coreagent.IsCoreAgent))
	ag.Locked = foundAgent.Locked
	ag.Model = &model
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
	// Populate Default and Skills from the live config after the write (handles
	// both the req.Default=true case and the leave-unchanged case).
	if liveCfg := a.agentLoop.GetConfig(); liveCfg != nil {
		for _, ac := range liveCfg.Agents.List {
			if ac.ID == agentID {
				ag.Default = boolPtr(ac.Default)
				if len(ac.Skills) > 0 {
					skills := make([]string, len(ac.Skills))
					copy(skills, ac.Skills)
					ag.Skills = &skills
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
		sm := steeringModeOrDefault(*req.SteeringMode)
		ag.SteeringMode = sm
	}
	if req.ToolFeedback != nil {
		ag.ToolFeedback = *req.ToolFeedback
	}
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
	// Refresh the in-memory config AND rewire sensitive-data scrubbing.
	// Propagate the error so callers fail the HTTP request rather than silently
	// serving stale in-memory state (prevents A1 regression on REST-initiated writes).
	if refreshErr := a.refreshConfigAndRewireServices(a.configPath()); refreshErr != nil {
		return fmt.Errorf("config written but in-memory refresh failed: %w", refreshErr)
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
	case r.Method == http.MethodPost && sub == "search":
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
	info := a.agentLoop.GetStartupInfo()
	// GetStartupInfo returns aggregate metadata (total, available, names) — not per-skill entries.
	skillsInfo, ok := info["skills"].(map[string]any)
	if !ok {
		jsonOK(w, []gen.Skill{})
		return
	}
	names, _ := skillsInfo["names"].([]string)
	if len(names) == 0 {
		jsonOK(w, []gen.Skill{})
		return
	}
	result := make([]gen.Skill, 0, len(names))
	for _, name := range names {
		result = append(result, gen.Skill{
			Id:      name,
			Name:    name,
			Version: "0.0.0",
			Status:  gen.SkillStatusActive,
		})
	}
	jsonOK(w, result)
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

func (a *restAPI) searchSkills(w http.ResponseWriter, _ *http.Request) {
	jsonErr(w, http.StatusNotImplemented, "ClawHub search not yet implemented")
}

func (a *restAPI) installSkill(w http.ResponseWriter, r *http.Request) {
	var req gen.SkillInstallRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SkillInstallRequest", &req, validateEnabled) {
		return
	}
	if req.Name == "" {
		jsonErr(w, http.StatusBadRequest, "name is required")
		return
	}
	jsonErr(w, http.StatusNotImplemented, "skill installation not yet available")
}

func (a *restAPI) deleteSkill(w http.ResponseWriter, name string) {
	if err := validateEntityID(name); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid skill name")
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
	cm.RegisterHTTPHandler("/api/v1/status", a.withAuth(a.HandleStatus))
	cm.RegisterHTTPHandler("/api/v1/tasks", a.withAuth(a.HandleTasks))
	cm.RegisterHTTPHandler("/api/v1/tasks/", a.withAuth(a.HandleTasks))
	cm.RegisterHTTPHandler("/api/v1/projects", a.withAuth(withRateLimit(configLimiter, a.HandleProjects)))
	cm.RegisterHTTPHandler("/api/v1/projects/", a.withAuth(withRateLimit(configLimiter, a.HandleProjects)))
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

	// GTD board task endpoints (Wave 2b, Level 1 Project & Task Mgmt).
	// Full CRUD — GET list/item, POST create, PUT update, DELETE remove.
	// Traces to: contracts/components/schemas/BoardTask.yaml, BoardTaskListResponse.yaml.
	cm.RegisterHTTPHandler("/api/v1/board/tasks", a.withAuth(withRateLimit(configLimiter, a.HandleBoardTasks)))
	cm.RegisterHTTPHandler("/api/v1/board/tasks/", a.withAuth(withRateLimit(configLimiter, a.HandleBoardTasks)))

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

// HandleTasks handles GET/POST /api/v1/tasks, GET/PUT /api/v1/tasks/{id},
// GET /api/v1/tasks/{id}/subtasks, and POST /api/v1/tasks/{id}/start.
func (a *restAPI) HandleTasks(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/tasks")
	rest = strings.TrimPrefix(rest, "/")

	// /api/v1/tasks/{id}/subtasks
	if strings.HasSuffix(rest, "/subtasks") {
		taskID := strings.TrimSuffix(rest, "/subtasks")
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.listSubtasks(w, taskID)
		return
	}

	// /api/v1/tasks/{id}/start
	if strings.HasSuffix(rest, "/start") {
		taskID := strings.TrimSuffix(rest, "/start")
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.startTask(w, r, taskID)
		return
	}

	taskID := rest
	switch r.Method {
	case http.MethodGet:
		if taskID == "" {
			a.listTasks(w, r)
		} else {
			a.getTask(w, taskID)
		}
	case http.MethodPost:
		if taskID != "" {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.createTask(w, r)
	case http.MethodPut:
		if taskID == "" {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.updateTask(w, r, taskID)
	case http.MethodDelete:
		if taskID == "" {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.deleteTask(w, taskID)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) listTasks(w http.ResponseWriter, r *http.Request) {
	filter := taskstore.TaskFilter{
		Status:  r.URL.Query().Get("status"),
		AgentID: r.URL.Query().Get("agent_id"),
	}
	tasks, err := a.taskStore.List(filter)
	if err != nil {
		slog.Error("rest: list tasks", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not list tasks: %v", err))
		return
	}
	jsonOK(w, tasks)
}

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

func (a *restAPI) getTask(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	t, err := a.taskStore.Get(id)
	if err != nil {
		if errors.Is(err, taskstore.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("task %q not found", id))
			return
		}
		slog.Error("rest: get task", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read task: %v", err))
		return
	}
	jsonOK(w, t)
}

func (a *restAPI) createTask(w http.ResponseWriter, r *http.Request) {
	var req gen.TaskCreateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "TaskCreateRequest", &req, validateEnabled) {
		return
	}
	// Backward compat: accept name→title, description→prompt
	title := req.Title
	prompt := ""
	if req.Prompt != nil {
		prompt = *req.Prompt
	}
	if title == "" && req.Name != nil && *req.Name != "" {
		title = *req.Name
	}
	if prompt == "" && req.Description != nil && *req.Description != "" {
		prompt = *req.Description
	}
	if title == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "title is required")
		return
	}
	agentID := ""
	if req.AgentId != nil {
		agentID = *req.AgentId
	}
	priority := 3
	if req.Priority != nil && *req.Priority != 0 {
		priority = *req.Priority
	}
	parentTaskID := ""
	if req.ParentTaskId != nil {
		parentTaskID = *req.ParentTaskId
	}
	triggerType := "manual"
	if req.TriggerType != nil && *req.TriggerType != "" {
		triggerType = string(*req.TriggerType)
	}
	t := &taskstore.TaskEntity{
		Title:        title,
		Prompt:       prompt,
		AgentID:      agentID,
		Priority:     priority,
		ParentTaskID: parentTaskID,
		TriggerType:  triggerType,
		CreatedBy:    "user",
		Status:       "queued",
	}
	if err := a.taskStore.Create(t); err != nil {
		slog.Error("rest: create task", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save task: %v", err))
		return
	}
	jsonCreated(w, t)
}

func (a *restAPI) updateTask(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	var req gen.TaskUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "TaskUpdateRequest", &req, validateEnabled) {
		return
	}
	// Backward compat mappings
	if req.Title == nil && req.Name != nil {
		req.Title = req.Name
	}
	if req.Result == nil && req.Description != nil {
		req.Result = req.Description
	}
	var patchStatus *string
	if req.Status != nil {
		s := string(*req.Status)
		patchStatus = &s
	}
	patch := taskstore.TaskPatch{
		Status:      patchStatus,
		Result:      req.Result,
		Artifacts:   req.Artifacts,
		Title:       req.Title,
		AgentID:     req.AgentId,
		Priority:    req.Priority,
		StartedAt:   req.StartedAt,
		CompletedAt: req.CompletedAt,
	}
	t, err := a.taskStore.Update(id, patch)
	if err != nil {
		if errors.Is(err, taskstore.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("task %q not found", id))
			return
		}
		slog.Warn("rest: update task", "id", id, "error", err)
		jsonErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("could not update task: %v", err))
		return
	}
	jsonOK(w, t)
}

func (a *restAPI) listSubtasks(w http.ResponseWriter, parentID string) {
	if err := validateEntityID(parentID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	tasks, err := a.taskStore.List(taskstore.TaskFilter{ParentTaskID: parentID})
	if err != nil {
		slog.Error("rest: list subtasks", "parent_id", parentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not list subtasks: %v", err))
		return
	}
	jsonOK(w, tasks)
}

func (a *restAPI) startTask(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	t, err := a.taskStore.Get(id)
	if err != nil {
		if errors.Is(err, taskstore.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("task %q not found", id))
			return
		}
		slog.Error("rest: start task: get", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read task: %v", err))
		return
	}
	if t.Status != "queued" {
		jsonErr(
			w,
			http.StatusUnprocessableEntity,
			fmt.Sprintf("task is %s, only queued tasks can be started", t.Status),
		)
		return
	}
	if a.taskExecutor == nil {
		jsonErr(w, http.StatusServiceUnavailable, "task executor not available")
		return
	}
	go func() {
		if err := a.taskExecutor.ExecuteTask(context.Background(), id); err != nil {
			slog.Error("rest: start task: execute", "id", id, "error", err)
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(gen.TaskAcceptedResponse{
		TaskId: id,
		Status: gen.Accepted,
	}); err != nil {
		slog.Warn("rest: start task: encode response failed", "error", err)
	}
}

func (a *restAPI) deleteTask(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task id")
		return
	}
	if err := a.taskStore.Delete(id); err != nil {
		if errors.Is(err, taskstore.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not delete task: %v", err))
		return
	}
	jsonOK(w, map[string]string{"deleted": id})
}

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

	// Collect task_created events from tasks directory.
	recentTasks, taskErr := a.taskStore.List(taskstore.TaskFilter{})
	if taskErr != nil {
		slog.Warn("rest: activity: list tasks", "error", taskErr)
	}
	for _, t := range recentTasks {
		if t.CreatedAt.After(cutoff) {
			taskAgentID := t.AgentID
			title := t.Title
			ev := gen.ActivityEvent{
				Id:        "task-c-" + t.ID,
				Type:      "task_created",
				Timestamp: t.CreatedAt,
				Summary:   &title,
			}
			if taskAgentID != "" {
				ev.AgentId = &taskAgentID
			}
			events = append(events, ev)
		}
		if t.CompletedAt != nil && t.CompletedAt.After(cutoff) {
			taskAgentID := t.AgentID
			title := t.Title
			ev := gen.ActivityEvent{
				Id:        "task-u-" + t.ID,
				Type:      "task_updated",
				Timestamp: *t.CompletedAt,
				Summary:   &title,
			}
			if taskAgentID != "" {
				ev.AgentId = &taskAgentID
			}
			events = append(events, ev)
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
	ch := cfg.Channels
	channels := []gen.ChannelEntry{
		{Id: "webchat", Name: "Web Chat", Transport: "websocket", Enabled: true, Description: "Built-in browser chat"},
		{
			Id:          "telegram",
			Name:        "Telegram",
			Transport:   "webhook",
			Enabled:     ch.Telegram.Enabled,
			Description: "Telegram Bot API",
		},
		{
			Id:          "discord",
			Name:        "Discord",
			Transport:   "websocket",
			Enabled:     ch.Discord.Enabled,
			Description: "Discord Gateway",
		},
		{
			Id:          "slack",
			Name:        "Slack",
			Transport:   "websocket",
			Enabled:     ch.Slack.Enabled,
			Description: "Slack Socket Mode",
		},
		{
			Id:              "whatsapp",
			Name:            "WhatsApp",
			Transport:       "native",
			Enabled:         ch.WhatsApp.Enabled,
			Description:     "WhatsApp (native, whatsmeow)",
			NativeAvailable: boolPtr(whatsappnative.NativeAvailable),
		},
		{
			Id:          "feishu",
			Name:        "Feishu / Lark",
			Transport:   "webhook",
			Enabled:     ch.Feishu.Enabled,
			Description: "Feishu (Lark) Bot",
		},
		{
			Id:          "dingtalk",
			Name:        "DingTalk",
			Transport:   "webhook",
			Enabled:     ch.DingTalk.Enabled,
			Description: "DingTalk Bot",
		},
		{
			Id:          "wecom",
			Name:        "WeCom",
			Transport:   "webhook",
			Enabled:     ch.WeCom.Enabled,
			Description: "WeCom (WeChat Work) Bot",
		},
		{
			Id:          "weixin",
			Name:        "Weixin",
			Transport:   "webhook",
			Enabled:     ch.Weixin.Enabled,
			Description: "Weixin (WeChat) Official Account",
		},
		{Id: "line", Name: "LINE", Transport: "webhook", Enabled: ch.LINE.Enabled, Description: "LINE Messaging API"},
		{Id: "qq", Name: "QQ", Transport: "websocket", Enabled: ch.QQ.Enabled, Description: "QQ via napcat"},
		{Id: "irc", Name: "IRC", Transport: "tcp", Enabled: ch.IRC.Enabled, Description: "Internet Relay Chat"},
		{Id: "matrix", Name: "Matrix", Transport: "http", Enabled: ch.Matrix.Enabled, Description: "Matrix protocol"},
		{
			Id:          "google-chat",
			Name:        "Google Chat",
			Transport:   "webhook",
			Enabled:     ch.GoogleChat.Enabled,
			Description: "Google Chat (webhook or service account)",
		},
	}

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
		found := false
		for _, ac := range cfg.Agents.List {
			if ac.ID == agentID {
				found = true
				break
			}
		}
		if !found {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
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
	// Reload path is crash-safe and name-correct as of #313. awaitReload treats an
	// unwired reload pipeline (the unit-test environment) as a no-op and only returns an
	// error on a genuine reload failure, which we surface rather than reporting a false
	// success (the flag persisted but the channel did not start).
	if a.agentLoop != nil {
		if err := a.awaitReload(); err != nil {
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

// channelSensitiveFields maps channel IDs to their secret/credential field names.
// These are redacted in GET responses (replaced with "[configured]" if set).
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
}

// channelRequiredFields maps channel IDs to fields that must be non-empty for the channel to work.
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
	ch := cfg.Channels
	count := 0
	for _, enabled := range []bool{
		ch.Telegram.Enabled,
		ch.Discord.Enabled,
		ch.Slack.Enabled,
		ch.WhatsApp.Enabled,
		ch.Feishu.Enabled,
		ch.DingTalk.Enabled,
		ch.WeCom.Enabled,
		ch.Weixin.Enabled,
		ch.LINE.Enabled,
		ch.QQ.Enabled,
		ch.IRC.Enabled,
		ch.Matrix.Enabled,
		ch.GoogleChat.Enabled,
	} {
		if enabled {
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
