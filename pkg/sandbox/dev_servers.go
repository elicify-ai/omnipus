// Package sandbox — dev-server registration and lifecycle for web_serve dev
// mode (Tier 3).
//
// Lifecycle:
// - Registration created when web_serve dev mode successfully spawns a
// hardened child that bound the requested port.
// - Idle deadline: 30 min from last activity (touched by the preview
// reverse proxy on each request — /preview/, /serve/, and /dev/ all
// route through Lookup).
// - Hard cap: 4 h from registration time, regardless of activity. The
// janitor enforces both.
// - Per-agent cap: 1 active dev server. Per-gateway cap from
// cfg.Sandbox.MaxConcurrentDevServers (operator-configurable).
//
// Threat model: dev servers are Tier 3 (Linux-only), trusted-prompt feature.
// The token in the URL is the gating credential — see /preview/<agent>/<token>/
// reverse proxy in pkg/gateway/rest_preview.go.
//
// Package placement: the spec calls for pkg/agent/dev_servers.go but that
// would create an import cycle — pkg/tools (which contains web_serve) is
// already imported by pkg/agent. The registry lives in pkg/sandbox so both
// pkg/agent (via the gateway wiring) and pkg/tools can import it without a cycle.

package sandbox

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"
)

// DevServerRegistration captures the per-instance state of a running Tier 3
// dev server. Stored in DevServerRegistry; surfaced (without the token) to
// operators via /api/v1/security/dev-servers in a future iteration.
type DevServerRegistration struct {
	// AgentID is the owning agent (Default uniqueness: one per agent).
	AgentID string
	// Token is the URL path component used to authenticate dev-proxy
	// requests in addition to cookie/bearer auth. 32 bytes of randomness
	// base64-RawURL encoded (43 chars).
	Token string
	// Port is the loopback port the child bound to (in
	// cfg.Sandbox.DevServerPortRange).
	Port int32
	// PID is the PID of the dev-server child process (for SIGTERM on
	// expiry / agent deletion).
	PID int
	// CreatedAt is when this registration was first inserted (drives the
	// 4-h hard cap).
	CreatedAt time.Time
	// LastActivity is the most recent preview reverse-proxy hit on any of
	// the three URL prefixes (/preview/, /serve/, /dev/) — drives the
	// 30-min idle cap.
	LastActivity time.Time
	// Command is the user-supplied command for diagnostics; not used in
	// auth decisions.
	Command string
}

// IdleTimeout is the duration of inactivity that expires a registration.
const IdleTimeout = 30 * time.Minute

// HardTimeout is the absolute lifetime of a registration regardless of
// activity. Spec v4.
const HardTimeout = 4 * time.Hour

// JanitorInterval is how often the janitor sweeps for expired registrations.
// 30 s matches the web_serve static-mode janitor cadence so expired entries
// clear within one sweep window of their deadline.
const JanitorInterval = 30 * time.Second

// devServerTokenBytes is the entropy of the URL-path token (256 bits before
// base64). Matches the pattern used by SessionCookieToken / web_serve static mode.
const devServerTokenBytes = 32

// ErrPerAgentCap is returned by Register when the agent already has an
// active dev-server registration. Tier 3 is per-agent capped to 1.
var ErrPerAgentCap = errors.New("dev_servers: agent already has an active dev server")

// GatewayCapError is returned by Register when the gateway-wide concurrency
// cap (cfg.Sandbox.MaxConcurrentDevServers) is reached. The error message
// uses the wording specified in spec v4.
type GatewayCapError struct {
	Current int
	Max     int
	// EarliestExpiry is the LastActivity+IdleTimeout (or CreatedAt+
	// HardTimeout, whichever is sooner) of the registration most likely
	// to free up first. Surfaced in the error message for operator
	// debugging and matches the spec wording.
	EarliestExpiry time.Time
}

func (e GatewayCapError) Error() string {
	return fmt.Sprintf("too many concurrent dev servers (%d/%d); previous registration expires at %s",
		e.Current, e.Max, e.EarliestExpiry.UTC().Format(time.RFC3339))
}

// DevServerRegistry tracks active Tier 3 dev servers. A single registry is
// owned by the gateway; tools and the reverse-proxy handler share it via
// pointer.
//
// Thread-safety: every public method takes mu internally. Callers must NOT
// hold any other lock when calling these methods (no specific ordering is
// required because mu has no nested-lock relationships).
type DevServerRegistry struct {
	mu      sync.Mutex
	entries map[string]*DevServerRegistration // keyed by token

	// stop and stopped form the janitor's lifecycle handshake. Close
	// stop to request shutdown; <-stopped reports the goroutine has
	// exited. closeOnce guards stop's close — Close may be invoked
	// multiple times but stop must close exactly once.
	stop      chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once

	// onEvict is called (outside mu) after each token is removed from the
	// registry (idle/TTL expiry, manual Unregister, Close). Wired at boot
	// by the gateway to purge the firstServedTokens audit set (F-9 fix).
	// May be nil.
	onEvict func(token string)
}

// NewDevServerRegistry constructs an empty registry and starts its janitor.
// Callers MUST invoke Close on shutdown to terminate the janitor goroutine.
func NewDevServerRegistry() *DevServerRegistry {
	r := &DevServerRegistry{
		entries: make(map[string]*DevServerRegistration),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go r.runJanitor()
	return r
}

// SetOnEvict installs a callback invoked (outside mu) after each token is
// removed from the registry. Gateway wires this to purgeFirstServedTokens
// so the audit firstServedTokens set stays in sync with the registry (F-9).
// Must be called before concurrent access (e.g. at boot, before StartAll).
func (r *DevServerRegistry) SetOnEvict(fn func(token string)) {
	r.mu.Lock()
	r.onEvict = fn
	r.mu.Unlock()
}

// Register inserts a new dev-server registration. agentID, port, and pid
// MUST already be validated by the caller (port-in-range, agent owned by
// user, etc.). maxConcurrent caps the gateway-wide active count. Returns
// the stored registration on success, or ErrPerAgentCap / GatewayCapError.
//
// On success, the registration's Token field is populated with a freshly
// generated 32-byte random token.
func (r *DevServerRegistry) Register(
	agentID string,
	port int32,
	pid int,
	command string,
	maxConcurrent int,
) (*DevServerRegistration, error) {
	if agentID == "" {
		return nil, errors.New("dev_servers: agentID is required")
	}
	if port <= 0 {
		return nil, errors.New("dev_servers: port must be positive")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Per-agent cap: 1 active.
	for _, e := range r.entries {
		if e.AgentID == agentID {
			return nil, ErrPerAgentCap
		}
	}
	// Per-gateway cap from config (default 2).
	if maxConcurrent > 0 && len(r.entries) >= maxConcurrent {
		earliest := r.earliestExpiryLocked()
		return nil, GatewayCapError{
			Current:        len(r.entries),
			Max:            maxConcurrent,
			EarliestExpiry: earliest,
		}
	}

	token, err := generateDevServerToken()
	if err != nil {
		return nil, fmt.Errorf("dev_servers: token generation: %w", err)
	}
	now := time.Now()
	reg := &DevServerRegistration{
		AgentID:      agentID,
		Token:        token,
		Port:         port,
		PID:          pid,
		CreatedAt:    now,
		LastActivity: now,
		Command:      command,
	}
	r.entries[token] = reg
	slog.Info("dev_servers: registered",
		"agent_id", agentID,
		"port", port,
		"pid", pid,
	)
	return reg, nil
}

// Lookup returns the registration matching token, or nil if the token is
// unknown or the entry has already expired.
//
// Expiry check: before touching LastActivity, Lookup tests both the idle
// deadline (time since LastActivity > IdleTimeout) and the hard deadline
// (time since CreatedAt > HardTimeout). If either has passed the entry is
// considered expired and nil is returned immediately — the LastActivity field
// is NOT updated and the janitor remains responsible for physically removing
// the entry. This prevents the proxy from resurrecting a stale entry by
// calling Lookup on it, which was the root cause of the "expired entries live
// forever" bug (B1.4-a).
func (r *DevServerRegistry) Lookup(token string) *DevServerRegistration {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg, ok := r.entries[token]
	if !ok {
		return nil
	}
	now := time.Now()
	if now.Sub(reg.CreatedAt) > HardTimeout || now.Sub(reg.LastActivity) > IdleTimeout {
		// Entry is past its deadline. Do not update LastActivity — that would
		// extend the lifetime and defeat the timeout. Leave removal to the
		// janitor's next sweep so we don't hold the write lock longer than
		// necessary here.
		return nil
	}
	reg.LastActivity = now
	// Return a copy so callers can read fields without holding mu.
	cp := *reg
	return &cp
}

// LookupByAgent returns the active registration for agentID, or nil if none.
// Used by web_serve dev mode to enforce the per-agent cap with a clear error
// before attempting Register.
func (r *DevServerRegistry) LookupByAgent(agentID string) *DevServerRegistration {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.AgentID == agentID {
			cp := *e
			return &cp
		}
	}
	return nil
}

// Unregister removes the registration matching token and SIGTERMs its
// child process. Returns true when an entry was removed.
//
// Used by:
// - The agent-deletion handler (/B6 — kill any active dev server
// when the agent is deleted).
// - The janitor (idle/hard-cap expiry).
// - Operators via a future admin endpoint.
func (r *DevServerRegistry) Unregister(token string) bool {
	r.mu.Lock()
	reg, ok := r.entries[token]
	if !ok {
		r.mu.Unlock()
		return false
	}
	delete(r.entries, token)
	cb := r.onEvict
	r.mu.Unlock()

	// SIGTERM outside the lock so the kernel call can't deadlock the
	// registry. Best-effort: if the process has already exited (PID
	// recycled or never existed), the error is logged but not surfaced.
	r.signalProcess(reg)
	slog.Info("dev_servers: unregistered",
		"agent_id", reg.AgentID,
		"port", reg.Port,
		"pid", reg.PID,
	)
	// F-9: notify audit set outside the lock.
	if cb != nil {
		cb(token)
	}
	return true
}

// UnregisterByAgent removes any registration owned by agentID and signals
// its child. Returns true when an entry was removed. Idempotent.
func (r *DevServerRegistry) UnregisterByAgent(agentID string) bool {
	r.mu.Lock()
	var found *DevServerRegistration
	var foundToken string
	for tok, e := range r.entries {
		if e.AgentID == agentID {
			found = e
			foundToken = tok
			break
		}
	}
	if found == nil {
		r.mu.Unlock()
		return false
	}
	delete(r.entries, foundToken)
	r.mu.Unlock()
	r.signalProcess(found)
	return true
}

// Count returns the number of active registrations.
func (r *DevServerRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// Close stops the janitor goroutine and unregisters every active server.
// Safe to call multiple times via sync.Once.
func (r *DevServerRegistry) Close() {
	r.closeOnce.Do(func() {
		// Snapshot active tokens under the lock, then signal the
		// janitor and run the per-token cleanup outside the lock so
		// signalProcess (called by Unregister) doesn't reacquire mu.
		r.mu.Lock()
		tokens := make([]string, 0, len(r.entries))
		for tok := range r.entries {
			tokens = append(tokens, tok)
		}
		r.mu.Unlock()

		// Tell the janitor to stop.
		close(r.stop)

		// H5: wait for the janitor goroutine to exit BEFORE the cleanup
		// loop. Without this, the janitor's sweepExpired → Unregister path
		// may run concurrently with the loop below and send SIGTERM twice
		// to the same PID (which may have been recycled by the OS).
		<-r.stopped

		for _, tok := range tokens {
			r.Unregister(tok)
		}
	})
}

// runJanitor sweeps expired registrations every JanitorInterval. Exits
// when stop is closed.
func (r *DevServerRegistry) runJanitor() {
	defer close(r.stopped)
	ticker := time.NewTicker(JanitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.sweepExpired()
		}
	}
}

// sweepExpired removes registrations that have hit either timer. It calls
// Unregister for each so the SIGTERM path runs uniformly.
func (r *DevServerRegistry) sweepExpired() {
	now := time.Now()
	expired := r.expiredTokens(now)
	for _, tok := range expired {
		r.Unregister(tok)
	}
}

// expiredTokens collects tokens whose registration has hit either timer,
// taking the lock once and returning a snapshot. Performed without
// signaling so signaling can happen outside the lock.
func (r *DevServerRegistry) expiredTokens(now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var expired []string
	for tok, e := range r.entries {
		if now.Sub(e.LastActivity) >= IdleTimeout || now.Sub(e.CreatedAt) >= HardTimeout {
			expired = append(expired, tok)
		}
	}
	return expired
}

// earliestExpiryLocked returns the soonest deadline (idle or hard) across
// all current registrations. Caller MUST hold r.mu. Returns the zero time
// when entries is empty.
func (r *DevServerRegistry) earliestExpiryLocked() time.Time {
	var earliest time.Time
	first := true
	for _, e := range r.entries {
		idleDeadline := e.LastActivity.Add(IdleTimeout)
		hardDeadline := e.CreatedAt.Add(HardTimeout)
		next := idleDeadline
		if hardDeadline.Before(next) {
			next = hardDeadline
		}
		if first || next.Before(earliest) {
			earliest = next
			first = false
		}
	}
	return earliest
}

// signalProcess sends SIGTERM to the registered PID. Best-effort: errors
// (process already exited, PID recycled, permission denied) are logged
// but not surfaced. Linux-only — Tier 3 is gated to Linux at the tool
// level so this code only runs on Linux in practice. The build is still
// cross-platform via the syscall.SIGTERM constant being defined on all
// targets we support.
func (r *DevServerRegistry) signalProcess(reg *DevServerRegistration) {
	if reg.PID <= 0 {
		return
	}
	proc, err := os.FindProcess(reg.PID)
	if err != nil {
		slog.Debug("dev_servers: FindProcess failed (may have exited)",
			"pid", reg.PID, "error", err)
		return
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// On unix, ESRCH / os.ErrProcessDone means the process is already
		// gone — that's fine and stays at Debug. Other errors (EPERM,
		// surprise EINVAL) are worth logging at Warn so operators can
		// investigate; the file-level comment already promises Warn here.
		// HIGH-4 (silent-failure-hunter): a chronic SIGTERM-permission
		// failure means dev servers leak past their expiry, which is a
		// resource issue operators must see.
		if errors.Is(err, os.ErrProcessDone) {
			slog.Debug("dev_servers: SIGTERM target already exited",
				"pid", reg.PID, "error", err)
		} else {
			slog.Warn("dev_servers: SIGTERM failed",
				"pid", reg.PID, "error", err)
		}
	}
}

// generateDevServerToken creates a fresh 32-byte cryptographically-random
// token, base64-RawURL encoded (43 chars).
func generateDevServerToken() (string, error) {
	buf := make([]byte, devServerTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("dev_servers: rand.Read: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
