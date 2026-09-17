package health

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

type Server struct {
	server     *http.Server
	mu         sync.RWMutex
	ready      bool
	checks     map[string]Check
	startTime  time.Time
	reloadFunc func() error
	degradedFn func() (bool, string) // optional; returns (isDegraded, reason)
	// sandboxInfoFn, when non-nil, returns the structured sandbox state
	// the /health handler embeds in the response under the "sandbox" key.
	// Sprint-J FR-J-008 / FR-J-016 require the status endpoint to report
	// {applied, mode, backend} after Apply has completed. See
	// gateway.registerSandboxHealthCheck for the wiring.
	sandboxInfoFn func() map[string]any

	// auditLoggerAvailableFn, when non-nil, reports whether the audit
	// logger is wired and operational. Returns true when audit is
	// available (a real *audit.Logger is reachable), false when audit is
	// disabled by config or has been emitting skip events recently. Used
	// by /health to populate audit_logger and audit_degraded.
	//
	// B1.2(f): wired by gateway boot to a closure over agentLoop.AuditLogger().
	auditLoggerAvailableFn func() bool

	// auditLoggerConfiguredFn, when non-nil, reports whether the operator
	// asked for audit logging at all (cfg.Sandbox.AuditLog). When false,
	// the audit logger is *deliberately* off and /health must not flag
	// audit_degraded=true just because no logger exists. When true and the
	// logger is unavailable, that IS degraded (operator wanted audit, it's
	// broken).
	auditLoggerConfiguredFn func() bool

	// catalogStateFn, when non-nil, reports the ADR-067 provider-catalog
	// state (FR-037): (degraded, reason). It is surfaced as the "catalog"
	// object on /health and, like audit_degraded, does NOT flip the HTTP
	// status — a stale registry snapshot degrades the model picker's
	// accuracy, it does not stop the gateway serving. See
	// gateway.catalogHealthState for the closure.
	catalogStateFn func() (bool, string)
}

type Check struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type StatusResponse struct {
	Status string           `json:"status"`
	Uptime string           `json:"uptime"`
	Checks map[string]Check `json:"checks,omitempty"`
	Pid    int              `json:"pid"`
}

func NewServer(host string, port int) *Server {
	mux := http.NewServeMux()
	s := &Server{
		ready:     false,
		checks:    make(map[string]Check),
		startTime: time.Now(),
	}

	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/ready", s.readyHandler)
	mux.HandleFunc("/reload", s.reloadHandler)

	addr := fmt.Sprintf("%s:%d", host, port)
	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	return s
}

func (s *Server) Start() error {
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return s.server.ListenAndServe()
}

func (s *Server) StartContext(ctx context.Context) error {
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return s.server.Shutdown(context.Background())
	}
}

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
	return s.server.Shutdown(ctx)
}

func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	s.ready = ready
	s.mu.Unlock()
}

func (s *Server) RegisterCheck(name string, checkFn func() (bool, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	status, msg := checkFn()
	s.checks[name] = Check{
		Name:      name,
		Status:    statusString(status),
		Message:   msg,
		Timestamp: time.Now(),
	}
}

// SetReloadFunc sets the callback function for config reload.
func (s *Server) SetReloadFunc(fn func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadFunc = fn
}

// SetDegradedFunc sets a function that the /health handler calls to determine
// whether the service is in a degraded state (e.g., after a failed config
// reload). When the function returns (true, reason), /health responds with
// 503 and {"status":"degraded","reason":reason}.
func (s *Server) SetDegradedFunc(fn func() (bool, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.degradedFn = fn
}

// SetSandboxInfoFunc sets a function that returns the structured sandbox
// state the /health handler embeds under the "sandbox" key. Sprint-J
// FR-J-008 requires callers to verify {applied, mode, backend} via /health
// (and via the detailed /api/v1/security/sandbox-status endpoint).
//
// The function is called on every /health request; keep it cheap (return
// a pre-built map, not recomputed data). fn=nil clears the hook.
func (s *Server) SetSandboxInfoFunc(fn func() map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sandboxInfoFn = fn
}

// SetAuditLoggerAvailableFunc sets the closure /health calls to determine
// whether the audit logger is wired and operational. B1.2(f): gateway boot
// passes a closure over agentLoop.AuditLogger() != nil so the response
// reflects the live state without exposing the logger pointer to health.
func (s *Server) SetAuditLoggerAvailableFunc(fn func() bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLoggerAvailableFn = fn
}

// SetAuditLoggerConfiguredFunc sets the closure /health calls to determine
// whether the operator asked for audit at all (cfg.Sandbox.AuditLog).
// When this returns false, audit_degraded stays false even though the
// logger is unavailable — that's the deliberate-off case, not a degradation.
func (s *Server) SetAuditLoggerConfiguredFunc(fn func() bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLoggerConfiguredFn = fn
}

// SetCatalogStateFunc sets the closure /health calls for the ADR-067
// provider-catalog state (FR-037): it returns (degraded, reason) — true
// with the last refresh error whenever no document is loaded, the last
// refresh failed, the document arrived over the degraded transport, or the
// served document is stale (updated_at older than 14 days).
//
// The result is reported as the "catalog" object; it never changes the
// HTTP status, because a degraded catalog is an accuracy problem, not an
// availability one.
func (s *Server) SetCatalogStateFunc(fn func() (bool, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogStateFn = fn
}

func (s *Server) reloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed, use POST"})
		return
	}

	s.mu.Lock()
	reloadFunc := s.reloadFunc
	s.mu.Unlock()

	if reloadFunc == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "reload not configured"})
		return
	}

	if err := reloadFunc(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "reload triggered"})
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	degradedFn := s.degradedFn
	sandboxInfoFn := s.sandboxInfoFn
	auditAvailFn := s.auditLoggerAvailableFn
	auditConfiguredFn := s.auditLoggerConfiguredFn
	catalogStateFn := s.catalogStateFn
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	// Build sandbox info up front so it can be included in both the
	// degraded and healthy response bodies. This matters because Sprint-J
	// operators may want to query /health during a failed reload to
	// confirm the sandbox is still applied.
	var sandboxInfo map[string]any
	if sandboxInfoFn != nil {
		sandboxInfo = sandboxInfoFn()
	}

	// B1.2(f): surface audit-degraded mode. audit_logger reflects whether
	// the gateway constructed a real audit.Logger; audit_skipped is the
	// cumulative count of writes that fell through to slog because either
	// the logger was unavailable or AuditFailClosed=false on a write error.
	auditLoggerStatus := "unknown"
	if auditAvailFn != nil {
		if auditAvailFn() {
			auditLoggerStatus = "ok"
		} else {
			auditLoggerStatus = "unavailable"
		}
	}
	skipped := audit.SnapshotSkipped()
	// auditDegraded distinguishes three states:
	//   audit_logger=ok            → not degraded
	//   audit_logger=unavailable AND operator asked for audit (cfg.Sandbox.AuditLog=true) → degraded (broken)
	//   audit_logger=unavailable AND operator did NOT ask for audit → NOT degraded (deliberately off)
	//   skipped.Total > 0          → degraded (writes are being dropped)
	//   audit_logger=unknown (no configured-fn wired) → degraded (cannot confirm)
	// ADR-067 FR-037: the provider catalog's own state, reported as a
	// field beside the audit one. Absent when nothing wired the hook.
	var catalogInfo map[string]any
	if catalogStateFn != nil {
		catalogDegraded, catalogReason := catalogStateFn()
		catalogInfo = map[string]any{"degraded": catalogDegraded}
		if catalogReason != "" {
			catalogInfo["reason"] = catalogReason
		}
	}

	auditConfigured := auditConfiguredFn != nil && auditConfiguredFn()
	auditDegraded := skipped.Total > 0
	if auditLoggerStatus == "unknown" {
		auditDegraded = true
	} else if auditLoggerStatus == "unavailable" && auditConfigured {
		auditDegraded = true
	}

	if degradedFn != nil {
		if isDegraded, reason := degradedFn(); isDegraded {
			w.WriteHeader(http.StatusServiceUnavailable)
			resp := map[string]any{
				"status":         "degraded",
				"reason":         reason,
				"pid":            os.Getpid(),
				"audit_logger":   auditLoggerStatus,
				"audit_skipped":  skipped,
				"audit_degraded": auditDegraded,
			}
			if sandboxInfo != nil {
				resp["sandbox"] = sandboxInfo
			}
			if catalogInfo != nil {
				resp["catalog"] = catalogInfo
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	uptime := time.Since(s.startTime)
	// Use a generic map instead of StatusResponse so callers can include
	// the optional "sandbox" field without expanding the exported struct.
	// audit_degraded is surfaced as a field but does NOT flip /health to
	// 503 — operators read the field to decide. The HTTP status remains
	// driven by degradedFn (gateway-fatal conditions).
	resp := map[string]any{
		"status":         "ok",
		"uptime":         uptime.String(),
		"pid":            os.Getpid(),
		"audit_logger":   auditLoggerStatus,
		"audit_skipped":  skipped,
		"audit_degraded": auditDegraded,
	}
	if sandboxInfo != nil {
		resp["sandbox"] = sandboxInfo
	}
	if catalogInfo != nil {
		resp["catalog"] = catalogInfo
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	s.mu.RLock()
	ready := s.ready
	checks := make(map[string]Check)
	maps.Copy(checks, s.checks)
	s.mu.RUnlock()

	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(StatusResponse{
			Status: "not ready",
			Checks: checks,
		})
		return
	}

	for _, check := range checks {
		if check.Status == "fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(StatusResponse{
				Status: "not ready",
				Checks: checks,
			})
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	uptime := time.Since(s.startTime)
	json.NewEncoder(w).Encode(StatusResponse{
		Status: "ready",
		Uptime: uptime.String(),
		Checks: checks,
	})
}

// HandlerMux is the interface for registering HTTP handlers, used by
// RegisterOnMux so that callers can pass any mux implementation
// (e.g. *http.ServeMux or a custom dynamic mux).
type HandlerMux interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// RegisterOnMux registers /health, /ready and /reload handlers onto the given mux.
// This allows the health endpoints to be served by a shared HTTP server.
func (s *Server) RegisterOnMux(mux HandlerMux) {
	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/ready", s.readyHandler)
	mux.HandleFunc("/reload", s.reloadHandler)
}

func statusString(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}
