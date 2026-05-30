// Package audit implements structured security audit logging for Omnipus.
//
// It provides SEC-15 (structured audit logging), SEC-16 (log redaction),
// and SEC-17 (explainable policy decisions) from the Omnipus BRD.
//
// Audit events are written as JSONL to ~/.omnipus/system/audit.jsonl.
// The logger rotates files daily or at the configured max size, retains for
// a configurable number of days, and recovers from corruption on startup.
package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Decision is the typed alias for audit decisions. The constants below are
// untyped string constants so they remain assignable to both `Decision`-typed
// values (e.g. predicate functions, future field migrations) and to the
// existing `string`-typed Entry.Decision field without requiring an explicit
// conversion at every callsite.
//
// Predicate function: IsValidDecision(Decision) bool.
type Decision string

// EventName is the typed alias for audit event identifiers. Same untyped-const
// strategy as Decision — values are assignable to both `EventName`-typed
// parameters and `string` fields without a conversion.
//
// Predicate function: IsValidEventName(EventName) bool.
type EventName string

// Event types for audit logging. Values are EventName-compatible (untyped
// strings — see EventName type docs).
const (
	EventToolCall          = "tool_call"
	EventExec              = "exec"
	EventFileOp            = "file_op"
	EventLLMCall           = "llm_call"
	EventPolicyEval        = "policy_eval"
	EventRateLimit         = "rate_limit"
	EventSSRF              = "ssrf"
	EventStartup           = "startup"
	EventShutdown          = "shutdown"
	EventBootAbort         = "boot.abort"
	EventProcessKillFailed = "process_kill_failed"
)

// Decision values for audit entries. Values are Decision-compatible
// (untyped strings — see Decision type docs).
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionError = "error"
)

// IsValidDecision reports whether d is one of the recognized Decision values.
// Returns false for unknown values (typos, deprecated values) so the audit
// pipeline can warn-once and continue rather than reject the entry.
func IsValidDecision(d Decision) bool {
	switch d {
	case DecisionAllow, DecisionDeny, DecisionError:
		return true
	}
	return false
}

// IsValidEventName reports whether e is one of the recognized EventName
// values. The set is intentionally narrow (only events emitted from the
// audit, agent, gateway, sandbox, and tools packages today). Unknown values
// trigger a warn-once log so a typo or new event introduction is loud
// without rejecting the audit row — losing audit data is worse than logging
// a weird event name.
func IsValidEventName(e EventName) bool {
	switch e {
	case EventToolCall,
		EventExec,
		EventFileOp,
		EventLLMCall,
		EventPolicyEval,
		EventRateLimit,
		EventSSRF,
		EventStartup,
		EventShutdown,
		EventBootAbort,
		EventProcessKillFailed,
		// Tool Registry redesign event names from events.go. These are
		// emitted from the agent loop and the policy package.
		EventToolPolicyDenyAttempted,
		EventToolPolicyAskRequested,
		EventToolPolicyAskGranted,
		EventToolPolicyAskDenied,
		EventToolCollisionMCPRejected,
		EventAgentConfigCorrupt,
		EventAgentConfigInvalidPolicyValue,
		EventAgentConfigUnknownToolInPolicy,
		EventToolAssemblyDuplicateName,
		EventMCPServerRenamed,
		EventGatewayStartupGuardDisabled,
		EventGatewayConfigInvalidValue,
		EventTurnAbortedSyntheticLoop,
		EventApproverFallback,
		// Cancel-flow events (FR-10, FR-11, FR-15, FR-17-21, FR-25a).
		EventTurnCancelAttempt,
		EventTurnCancelled,
		EventTurnCancelStuck,
		EventCancelAbusePattern,
		// security_change.go.
		EventSecuritySettingChange,
		// Misc event names emitted by other packages with stable wire
		// contracts — keep the predicate aligned with them so they don't
		// trip the unknown-event warn-once.
		"egress_denied",
		"egress_upstream_error",
		"path.network_denied",
		"sandbox.thread_restrict_failed",
		// pkg/tools/memory.go: long-term memory write events.
		// "memory.remember" and "memory.retrospective" are the success-path
		// events; "memory.rate_limited" is emitted by the v0.2 #155 item 6
		// gate when a write is rejected (see RememberTool.logRateLimited
		// and RetrospectiveTool.logRateLimited).
		"memory.remember",
		"memory.retrospective",
		"memory.rate_limited":
		return true
	}
	return false
}

// Entry is a single audit log record.
type Entry struct {
	Timestamp  time.Time      `json:"timestamp"`
	Event      string         `json:"event"`
	Decision   string         `json:"decision,omitempty"`
	AgentID    string         `json:"agent_id,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	Command    string         `json:"command,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
	PolicyRule string         `json:"policy_rule,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

// LoggerConfig configures the audit logger.
type LoggerConfig struct {
	Dir            string   // Directory for audit files
	MaxSizeBytes   int64    // File rotation threshold (default 50MB)
	RetentionDays  int      // Days to retain rotated files (default 90)
	RedactPatterns []string // Custom redaction patterns
	RedactEnabled  bool     // Enable redaction

	// AuditLogRequested signals that the operator explicitly enabled audit
	// logging (cfg.Sandbox.AuditLog == true). When true, NewLogger returns a
	// *LoggerConstructionError on openCurrentFile failure so the gateway can
	// fail closed (CRIT-2). When false, openCurrentFile failures keep the
	// legacy "log-and-continue" behavior: NewLogger returns a degraded logger
	// and a nil error so callers wired with audit-disabled don't get spurious
	// boot aborts on permission/disk hiccups.
	AuditLogRequested bool

	// HMACKey is the 32-byte tamper-evident chain key (v0.2 #155). Each
	// audit entry carries a `hmac` field computed over
	//   HMAC-SHA256(HMACKey, prev_hmac || canonical_json_without_hmac)
	// so truncation or surgical rewrite of the JSONL file is detectable
	// via Logger.Verify / audit.VerifyFile.
	//
	// In production the key is derived from the master key via
	// credentials.Store.DeriveSubkey(audit.AuditChainKeyInfo); the gateway
	// passes the derived bytes here. When this field is nil, NewLogger
	// falls back to the package-level processChainKey set via
	// audit.SetProcessChainKey; if neither is set, NewLogger uses an
	// insecure deterministic dev key with a sticky slog.Warn so tests pass
	// while production deployments are loud about the misconfiguration.
	HMACKey []byte
}

const defaultMaxSize = 50 * 1024 * 1024 // 50MB

// Logger writes audit entries as JSONL with rotation and retention.
type Logger struct {
	mu          sync.Mutex
	dir         string
	file        *os.File
	writer      *bufio.Writer
	currentSize int64
	currentDate string
	maxSize     int64
	retDays     int
	redactor    *Redactor
	degraded    bool

	// chainKey is the 32-byte HMAC-SHA256 key for the tamper-evident audit
	// chain (v0.2 #155). Held only in process memory — never written to disk.
	// See pkg/audit/hmac.go for the threat model.
	chainKey []byte

	// prevHMAC tracks the previous entry's HMAC so the next write can chain
	// off it. Initialized from the last line of audit.jsonl on open (or from
	// genesisSeed for a fresh file). Updated under mu after every successful
	// write. 32 bytes (raw, not hex-encoded).
	prevHMAC []byte

	// unknownDecisionWarn / unknownEventWarn fire at most once per process for
	// the "Decision/Event was an unrecognized value" path. Surfaces typos and
	// stale event names without spamming logs on every emit.
	unknownDecisionWarn sync.Once
	unknownEventWarn    sync.Once
}

// LoggerConstructionError wraps an audit.NewLogger failure so callers can
// distinguish "audit logger could not be built" from generic boot errors.
//
// B1.2(b) / CRIT-2: when cfg.Sandbox.AuditLog == true and audit construction
// fails (including openCurrentFile failure inside NewLogger), the gateway must
// fail closed (treat as a sandbox boot error). Wrapping the underlying error
// in this typed sentinel lets the gateway recognize the case without
// string-matching the message and without importing internal state from the
// agent package.
type LoggerConstructionError struct {
	Dir string
	Err error
}

// Error makes LoggerConstructionError satisfy the error interface.
func (e *LoggerConstructionError) Error() string {
	if e == nil {
		return "audit: logger construction failed"
	}
	if e.Err == nil {
		return fmt.Sprintf("audit: logger construction failed for dir %q", e.Dir)
	}
	return fmt.Sprintf(
		"audit log construction failed for dir %q: %v; "+
			"either disable `sandbox.audit_log` or fix the underlying problem (permissions, disk, redaction patterns)",
		e.Dir, e.Err,
	)
}

// Unwrap exposes the underlying error for errors.Is/As traversal.
func (e *LoggerConstructionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewLogger creates an audit logger. It recovers from corruption on startup
// and runs retention cleanup.
//
// CRIT-2: when cfg.AuditLogRequested == true and openCurrentFile fails, this
// function returns a *LoggerConstructionError so the gateway boot path
// (gateway.go around the agent.NewAgentLoop call) can fail closed. When
// cfg.AuditLogRequested == false, openCurrentFile failures fall back to the
// legacy "return a degraded logger, no error" behavior so audit-disabled
// deployments don't crash on transient permission issues.
func NewLogger(cfg LoggerConfig) (*Logger, error) {
	if cfg.MaxSizeBytes <= 0 {
		cfg.MaxSizeBytes = defaultMaxSize
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 90
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("audit: cannot create directory %s: %w", cfg.Dir, err)
	}

	var redactor *Redactor
	if cfg.RedactEnabled {
		var err error
		redactor, err = NewRedactor(cfg.RedactPatterns)
		if err != nil {
			return nil, fmt.Errorf("audit: invalid redaction pattern: %w", err)
		}
	}

	l := &Logger{
		dir:      cfg.Dir,
		maxSize:  cfg.MaxSizeBytes,
		retDays:  cfg.RetentionDays,
		redactor: redactor,
		chainKey: resolveChainKey(cfg.HMACKey),
		prevHMAC: GenesisSeed(),
	}

	l.recoverCorruption()

	if err := l.openCurrentFile(); err != nil {
		// CRIT-2: when the operator explicitly requested audit (AuditLog=true),
		// failure to open the current file is a fail-closed boot abort. The
		// gateway maps the wrapped LoggerConstructionError to a SandboxBootError
		// + EX_CONFIG (78) exit code. Without this branch the gateway would see
		// audit_logger=ok at startup while every subsequent write rejects in
		// degraded mode — silently breaking the SEC-15 audit-everything contract.
		if cfg.AuditLogRequested {
			return nil, &LoggerConstructionError{Dir: cfg.Dir, Err: err}
		}
		// Operator did not request audit — keep legacy degraded-mode behavior.
		slog.Error("Audit log file cannot be opened. Operating in degraded mode.",
			"error", err, "path", cfg.Dir)
		l.degraded = true
	}

	// Run retention cleanup on startup
	l.cleanupExpired()

	return l, nil
}

// Log writes an audit entry. Returns an error on write failure but never panics.
//
// B1.2(a): Logger.Log is nil-safe. If the receiver is nil (e.g. when audit
// logger construction failed during boot but the rest of the system continues
// — see B1.2(b) for the fail-closed path), Log returns nil without panicking.
// This guard exists because the audit logger is reached through deeply-nested
// call chains (egress proxy denials, per-thread restrict failures, web_serve
// audit fail-closed) where adding a defensive nil-check at every call site
// would be brittle. Keeping the guard on the receiver makes every caller safe.
func (l *Logger) Log(entry *Entry) error {
	if l == nil {
		return nil
	}
	if entry == nil {
		return nil
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	// Empty-Event reject (spec item 7): refuse to emit unlabelled audit rows.
	// IncSkipped + slog.Error so an operator inspecting /health and gateway
	// logs sees the gap; Log() still returns nil so the caller's tool-execution
	// path is not blocked (preserves the receiver-nil contract of best-effort,
	// never block on audit). The caller will treat nil-error as success — but
	// the entry is intentionally NOT written.
	if entry.Event == "" {
		IncSkipped("empty_event", entry.Decision)
		slog.Error("audit: refusing to emit entry with empty Event field",
			"decision", entry.Decision,
			"agent_id", entry.AgentID,
			"tool", entry.Tool,
			"session_id", entry.SessionID)
		return nil
	}

	// Validate Decision/Event against the known-good vocabulary. Unknown values
	// fire a sticky-once slog.Warn so a typo path (e.g. Decision: "allowed")
	// surfaces in the gateway logs without rejecting the row — losing audit
	// data is worse than emitting one with an unfamiliar Decision string.
	if entry.Decision != "" && !IsValidDecision(Decision(entry.Decision)) {
		l.unknownDecisionWarn.Do(func() {
			slog.Warn("audit: unknown Decision value (warn-once); please add to IsValidDecision or fix typo",
				"decision", entry.Decision,
				"event", entry.Event,
				"tool", entry.Tool)
		})
	}
	if !IsValidEventName(EventName(entry.Event)) {
		l.unknownEventWarn.Do(func() {
			slog.Warn("audit: unknown Event value (warn-once); please add to IsValidEventName or fix typo",
				"event", entry.Event,
				"decision", entry.Decision,
				"tool", entry.Tool)
		})
	}

	// Apply redaction (SEC-16)
	if l.redactor != nil {
		l.redactEntry(entry)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit: marshal failed: %w", err)
	}

	return l.writeLine(data, criticalEventNeedsSync(entry))
}

// criticalEventNeedsSync reports whether an entry must be fsync'd to disk
// before Log() returns (CRIT-5 / SEC-15 audit-everything contract).
//
// Sync criteria — security-relevant entries that must survive a crash:
//   - Decision == "deny": every denial must be durably recorded so an attacker
//     cannot count on a kernel buffer drop hiding a deny.
//   - Decision == "error": sandbox / per-thread restrict / upstream failures
//     are forensically important and must persist.
//   - Event == "boot.abort": boot-abort entries are emitted right before the
//     gateway exits; without fsync they may never reach disk.
//   - Event == "tool.policy.deny.attempted" / "tool.policy.ask.denied":
//     policy-deny entries are part of the tamper-evident security record.
//
// Bulk allow entries (Decision == "allow") deliberately do NOT fsync per
// write — they would 10–100× the per-write latency on rotational disks.
// They batch via the bufio.Writer's flush which is called after every Write
// in writeLine; on a clean shutdown Close() also flushes. The trade-off is
// explicit: a kernel-buffered allow row may be lost on hard kill, but the
// security signal (denials and errors) is durable.
func criticalEventNeedsSync(entry *Entry) bool {
	if entry == nil {
		return false
	}
	if entry.Decision == DecisionDeny || entry.Decision == DecisionError {
		return true
	}
	switch entry.Event {
	case EventBootAbort,
		EventToolPolicyDenyAttempted,
		EventToolPolicyAskDenied:
		return true
	}
	return false
}

// writeLine appends a single pre-marshaled JSON object as a JSONL record to the
// audit file, performing rotation and degraded-mode guarding identically to Log.
// It is reused by helpers that emit non-Entry-shaped records (e.g. security
// setting changes with flat top-level fields like actor/resource/old_value).
// The caller is responsible for any redaction before marshaling.
//
// fsyncRequired (CRIT-5): when true, the file is f.Sync()'d after the write so
// the entry is guaranteed durable before this function returns. See
// criticalEventNeedsSync for the gating policy.
//
// v0.2 #155: this function ALSO computes and embeds the HMAC chain link
// (`hmac` field) before writing. The computation is done while holding l.mu
// so the prevHMAC update is consistent with the on-disk byte sequence. If
// embedHMAC fails (malformed pre-marshaled JSON), the row is written WITHOUT
// the hmac field and prevHMAC is NOT advanced — Verify will then flag this
// row as a chain break. Failing closed on the chain (refuse to write) would
// be worse than dropping a chain link: losing audit data is a known
// non-goal of the chain feature.
func (l *Logger) writeLine(data []byte, fsyncRequired bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.degraded || l.file == nil {
		slog.Error("Audit log entry written in degraded mode", "entry", string(data))
		return fmt.Errorf("audit: operating in degraded mode")
	}

	today := time.Now().UTC().Format("2006-01-02")
	if today != l.currentDate || l.currentSize >= l.maxSize {
		if rotateErr := l.rotate(); rotateErr != nil {
			// CRIT-3: rotate() now propagates os.Rename errors and latches
			// degraded=true itself, but we double-latch here defensively in case
			// a future code path returns a different error class.
			slog.Error("Audit: rotation failed, entering degraded mode", "error", rotateErr)
			l.degraded = true
			return fmt.Errorf("audit: rotation failed: %w", rotateErr)
		}
	}

	// v0.2 #155: embed the HMAC chain link before writing. embedHMAC parses
	// the pre-marshaled `data`, removes any pre-existing `hmac` field
	// (defense against caller mistake), computes HMAC-SHA256 over
	//   prev_hmac || canonical_json_without_hmac
	// and re-marshals with the new `hmac` field. The output is what we
	// write to disk.
	out, mac, hmacErr := embedHMAC(data, l.prevHMAC, l.chainKey)
	if hmacErr != nil {
		slog.Error("audit: HMAC chain embed failed; writing entry without chain link",
			"error", hmacErr, "entry", string(data))
		// CRIT-1 (silent-failure-hunter): bump the skipped-counter so an
		// operator monitoring `audit.skipped` sees chain-link drops without
		// having to grep slog. Symmetric with the empty-event path which
		// already calls IncSkipped.
		IncSkipped("hmac_embed_failed", "unknown")
		// Fall back to the original data so the row is at least preserved.
		// Verify will flag the chain as broken at this entry.
		out = data
		mac = nil
	}

	line := append(out, '\n')
	n, err := l.writer.Write(line)
	if err != nil {
		l.degraded = true
		slog.Error("Audit log write failed, entering degraded mode", "error", err, "entry", string(data))
		return fmt.Errorf("audit: write failed: %w", err)
	}
	if err := l.writer.Flush(); err != nil {
		return fmt.Errorf("audit: flush failed: %w", err)
	}
	if fsyncRequired && l.file != nil {
		// CRIT-5: durable write for security-relevant entries. f.Sync() blocks
		// until the kernel reports the data hit stable storage. We accept the
		// per-call latency cost on the deny / error / boot.abort path because
		// losing those entries is a security regression. Allow entries are not
		// sync'd here — they batch via bufio + Flush above and rely on Close()
		// for shutdown durability.
		if err := l.file.Sync(); err != nil {
			// Sync failure is unusual (disk full, faulty hardware). Log and
			// continue — the row is in the bufio-flushed kernel buffer and
			// will reach disk if the kernel is still healthy. Do NOT latch
			// degraded for a single Sync error: the next Write attempt will
			// surface the underlying disk problem more reliably.
			slog.Error("audit: fsync of critical entry failed (entry already buffered, continuing)",
				"error", err, "entry", string(data))
			return fmt.Errorf("audit: fsync failed: %w", err)
		}
	}
	l.currentSize += int64(n)
	// Advance the chain only after the write succeeded. If embedHMAC failed
	// upstream, mac is nil — leaving prevHMAC unchanged means the NEXT entry
	// chains off the LAST GOOD entry, so a single embed failure does not
	// break the chain for every subsequent row.
	if mac != nil {
		l.prevHMAC = mac
	}
	return nil
}

// Close flushes and closes the audit log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer != nil {
		if err := l.writer.Flush(); err != nil {
			if l.file != nil {
				l.file.Close()
			}
			return fmt.Errorf("audit: flush on close failed: %w", err)
		}
	}
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// RunRetentionCleanup deletes rotated audit files older than the retention period.
func (l *Logger) RunRetentionCleanup() error {
	l.cleanupExpired()
	return nil
}

func (l *Logger) auditPath() string {
	return filepath.Join(l.dir, "audit.jsonl")
}

func (l *Logger) openCurrentFile() error {
	path := l.auditPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		// MED-1: latch degraded BEFORE returning so a transient stat failure
		// on the reload path doesn't leave the logger in a half-open state
		// (file pointer set, but currentSize uninitialised). The caller must
		// also treat the returned error as a setup failure, but the latch
		// belt-and-braces guards every subsequent write attempt.
		f.Close()
		l.degraded = true
		return fmt.Errorf("audit: stat %s: %w", path, err)
	}
	l.file = f
	l.writer = bufio.NewWriter(f)
	l.currentSize = info.Size()
	l.currentDate = time.Now().UTC().Format("2006-01-02")
	l.degraded = false

	// v0.2 #155: seed the HMAC chain from the last good entry of the
	// existing file. If the file is empty (fresh install or just rotated),
	// prevHMAC stays at the genesisSeed installed by NewLogger. If the last
	// line carries an `hmac` field, parse it and resume the chain so the
	// next write chains correctly across process restarts. If the last line
	// lacks an `hmac` field (legacy entry from before this fix), fall back
	// to genesisSeed for the next entry — Verify will mark the legacy
	// region as "pre-chain" rather than failing.
	if info.Size() > 0 {
		seed, ok := readChainSeedFromFile(path)
		if ok {
			l.prevHMAC = seed
		}
	}
	return nil
}

// rotate closes the current audit file, renames it with a date suffix, and
// re-opens audit.jsonl as a fresh empty file.
//
// CRIT-3: a Rename error is propagated to the caller and degraded=true is
// latched. The previous implementation called openCurrentFile on rename
// failure, which masked the rename error: if rename failed (cross-device,
// ENOSPC, EBUSY) but openCurrentFile succeeded, the next write appended to
// the OLD file because the file handle still pointed at the original inode.
// Latching degraded forces the next write to refuse and surface the failure.
//
// v0.2 #155: rotation preserves the HMAC chain across files. l.prevHMAC is
// the last entry of the about-to-rotate file, and openCurrentFile will see
// an empty audit.jsonl and skip the readChainSeedFromFile path — leaving
// l.prevHMAC pointed at the correct seed for the new file's first entry.
func (l *Logger) rotate() error {
	if l.writer != nil {
		l.writer.Flush()
	}
	if l.file != nil {
		l.file.Close()
	}

	// v0.2 #155: capture the chain seed BEFORE we rename. l.prevHMAC is
	// already correct (it tracks every successful write), but if we ever
	// add a code path that resets it on rotate, this comment is the
	// reminder that doing so breaks cross-file chain verification.
	chainSeed := l.prevHMAC

	src := l.auditPath()
	dst := filepath.Join(l.dir, fmt.Sprintf("audit-%s.jsonl", l.currentDate))
	if _, err := os.Stat(dst); err == nil {
		dst = filepath.Join(l.dir, fmt.Sprintf("audit-%s-%d.jsonl",
			l.currentDate, time.Now().UnixMilli()))
	}

	if err := os.Rename(src, dst); err != nil {
		// CRIT-3: latch degraded and propagate the rename error. Do NOT call
		// openCurrentFile here — opening a fresh handle while the rename
		// failed leaves us appending to the old inode (the original file is
		// still there because rename was a no-op). degraded=true forces every
		// subsequent write to reject so an operator notices the state.
		l.degraded = true
		l.file = nil
		l.writer = nil
		return fmt.Errorf("audit: rotate rename %s -> %s: %w", src, dst, err)
	}

	slog.Info("Audit log rotated", "to", dst)
	if err := l.openCurrentFile(); err != nil {
		return err
	}
	// openCurrentFile() called readChainSeedFromFile against the NEW (empty)
	// audit.jsonl, which returned ok=false because the file was empty — so
	// l.prevHMAC was reset to genesisSeed by the NewLogger initialisation.
	// We must restore the seed captured before rename so the first entry of
	// the new file chains to the last entry of the rotated-out file.
	if chainSeed != nil {
		l.prevHMAC = chainSeed
	}
	return nil
}

// recoverCorruption validates the last line of audit.jsonl on startup and
// truncates it if malformed. Called once from NewLogger BEFORE the file is
// opened for writing.
//
// CRIT-4 fixes:
//  1. Open with O_RDWR (not separate Open + Truncate via path) so the truncate
//     happens on the open handle and any error is propagated.
//  2. readLastLine expands its read window until a newline is found OR it
//     reaches the start of the file. The previous fixed 4 KiB window mistook
//     long-but-valid records as a corrupt trailing fragment, then truncated
//     healthy data.
//  3. Holds l.mu for the duration so future callers (reload path) can call
//     this safely without a TOCTOU between recovery and openCurrentFile.
func (l *Logger) recoverCorruption() {
	l.mu.Lock()
	defer l.mu.Unlock()

	path := l.auditPath()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		// File doesn't exist yet (fresh install) or unreadable — both fine.
		// openCurrentFile will create it. We only log unexpected errors.
		if !os.IsNotExist(err) {
			slog.Warn("audit: could not open file for corruption recovery", "path", path, "error", err)
		}
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		if err != nil {
			slog.Warn("audit: could not stat file for corruption recovery", "path", path, "error", err)
		}
		return
	}

	lastLine, lastLineStart, ok := readLastLine(f, info.Size())
	if !ok {
		// readLastLine couldn't find a newline OR a complete trailing record
		// after expanding to the start of the file. Two interpretations:
		//   (a) The whole file is one giant unterminated line — extremely
		//       unlikely in practice; treat as healthy and let the next write
		//       append a newline.
		//   (b) An I/O error during read — we logged it inside readLastLine.
		// Either way, do NOT truncate: discarding the entire file on
		// ambiguity is exactly the bug CRIT-4 fixes.
		return
	}
	if lastLine == "" {
		// File ends with an empty line — clean state, nothing to do.
		return
	}

	var js json.RawMessage
	if json.Unmarshal([]byte(lastLine), &js) == nil {
		return
	}

	slog.Warn("Audit log: truncating malformed last line", "path", path, "truncate_at", lastLineStart)
	if err := f.Truncate(lastLineStart); err != nil {
		// Truncate failure is rare (read-only mount, EPERM). Log and move on
		// — the malformed line stays, but the next write appends after it
		// and the file remains usable. Better than a startup hard-fail.
		slog.Error("audit: truncate of malformed last line failed",
			"path", path, "truncate_at", lastLineStart, "error", err)
	}
}

// readLastLine returns the last newline-terminated complete record in the
// file, its byte offset, and a third "ok" flag.
//
// CRIT-4: the previous implementation read a fixed 4 KiB window from the
// end of the file and split on newlines. If a single complete JSONL record
// was longer than 4 KiB, the leading fragment looked like "the last line",
// failed JSON unmarshal, and got truncated — discarding healthy data.
//
// The new algorithm grows the read window in 4 KiB doublings until either
// (a) a newline is found in the buffer (the record after the newline is
// the last line), or (b) the buffer covers the entire file (single line —
// no truncation possible without losing data, return ok=false to signal).
//
// ok=true: lastLine is a complete record (does not include the trailing
// newline) and lastLineStart is its byte offset in the file.
// ok=false: caller should NOT truncate (no certainty about line boundary).
func readLastLine(r io.ReadSeeker, size int64) (string, int64, bool) {
	const chunk = int64(4096)
	bufSize := chunk
	if bufSize > size {
		bufSize = size
	}

	for {
		offset := size - bufSize
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			slog.Warn("audit: seek failed during corruption recovery", "error", err, "offset", offset)
			return "", size, false
		}

		buf := make([]byte, bufSize)
		n, err := io.ReadFull(r, buf)
		if err != nil && err != io.ErrUnexpectedEOF {
			slog.Warn("audit: read failed during corruption recovery", "error", err)
			return "", size, false
		}
		buf = buf[:n]

		// Find the LAST newline in the buffer. Everything after it is the
		// final (potentially incomplete) record.
		if idx := lastIndexNewline(buf); idx >= 0 {
			afterNL := offset + int64(idx) + 1
			lastLine := strings.TrimRight(string(buf[idx+1:]), "\r\n")
			if lastLine == "" {
				// Trailing newline only — file is in a clean state.
				return "", afterNL, true
			}
			return lastLine, afterNL, true
		}

		// No newline in the current window. If we have already covered the
		// entire file, there is exactly one giant unterminated record. Don't
		// truncate; signal the caller to leave it alone.
		if offset == 0 {
			return "", 0, false
		}

		// Double the window size and try again. Cap at the file size.
		bufSize *= 2
		if bufSize > size {
			bufSize = size
		}
	}
}

// lastIndexNewline returns the index of the last '\n' in b, or -1.
func lastIndexNewline(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			return i
		}
	}
	return -1
}

func (l *Logger) cleanupExpired() {
	cutoff := time.Now().UTC().AddDate(0, 0, -l.retDays)
	pattern := filepath.Join(l.dir, "audit-*.jsonl")
	matches, _ := filepath.Glob(pattern)
	sort.Strings(matches)

	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil {
				slog.Error("Audit: failed to remove expired log", "path", path, "error", err)
			} else {
				slog.Info("Audit: removed expired log", "path", path)
			}
		}
	}
}

func (l *Logger) redactEntry(entry *Entry) {
	if l.redactor == nil {
		return
	}
	if entry.Parameters != nil {
		entry.Parameters = l.redactor.redactMap(entry.Parameters)
	}
	if entry.Details != nil {
		entry.Details = l.redactor.redactMap(entry.Details)
	}
	entry.Command = l.redactor.Redact(entry.Command)
}
