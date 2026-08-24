package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adhocore/gronx"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// scheduledExecutor is the subset of *agent.AgentLoop the runner needs. It is an
// interface so tests can inject a fake without a full agent loop (#264 W-1).
type scheduledExecutor interface {
	ProcessScheduled(ctx context.Context, ownerAgentID, sessionID, content, channel, chatID string) (string, error)
	GetSessionStore() *session.UnifiedStore
	GetConfig() *config.Config
	EmitNotification(p agent.NotificationPayload)
}

// agentChecker reports whether an agent id is available to run a schedule:
// registered in the runtime registry. Kept narrow so the runner can be tested
// with a stub.
type agentChecker interface {
	IsRegistered(agentID string) bool
}

// scheduledCanceller is the subset of *agent.AgentLoop used to force-abort a
// scheduled run that overran its deadline (FR-003/FR-006). Defined as an
// interface so the runner can be tested without a full agent loop.
type scheduledCanceller interface {
	RequestCancel(
		ctx context.Context,
		scope agent.CancelScope,
		canceller agent.CancelCanceller,
		hooks agent.CancelHooks,
	) (agent.CancelOutcome, error)
}

// scheduledRunner implements cron.ScheduledRunner (#264). It wakes a fired
// schedule's OWNING agent in the session mode the schedule chose, under a
// per-run deadline, and on failure raises a notification + a channel alert to
// the owning agent's default channel. It NEVER falls back to the default agent.
type scheduledRunner struct {
	exec      scheduledExecutor
	checker   agentChecker
	canceller scheduledCanceller
	msgBus    *bus.MessageBus
	notifs    *notifications.Store
	getConfig func() *config.Config

	// procTrack records a child PID spawned during a run under the run's session
	// id (FR-011). It is installed on the ProcessScheduled context so the exec/
	// shell tools report spawned children; procCleanup later terminates them.
	// nil means "no tracking wired".
	procTrack func(sessionID string, pid int)

	// procCleanup best-effort terminates child/browser processes tracked for a
	// finished scheduled run's session (FR-011). nil means "no cleanup wired".
	// Set via setProcessCleanup; the gateway wires it to the proc registry.
	procCleanup func(sessionID string)
}

// newScheduledRunner builds the runner. getConfig is read at run time so the
// runner always sees the live (possibly hot-reloaded) config. If exec also
// implements scheduledCanceller (the real *agent.AgentLoop does), it is used to
// force-abort a run that overran its deadline (FR-003).
func newScheduledRunner(
	exec scheduledExecutor,
	checker agentChecker,
	msgBus *bus.MessageBus,
	notifs *notifications.Store,
	getConfig func() *config.Config,
) *scheduledRunner {
	r := &scheduledRunner{
		exec:      exec,
		checker:   checker,
		msgBus:    msgBus,
		notifs:    notifs,
		getConfig: getConfig,
	}
	if c, ok := exec.(scheduledCanceller); ok {
		r.canceller = c
	}
	return r
}

// RunScheduled is the owner-aware fire path (FR-001/FR-003/FR-004/FR-013/FR-014).
func (r *scheduledRunner) RunScheduled(ctx context.Context, job *cron.CronJob) (string, error) {
	owner := job.AgentID

	// Owner pinning: a missing owner is a failure, never a default fallback
	// (FR-001). The checker reports availability = registered in the runtime
	// registry. A missing owner is rejected here with no fallback. The lane
	// records the (string,error) failure, which drives the alert path below.
	if owner == "" || r.checker == nil || !r.checker.IsRegistered(owner) {
		err := fmt.Errorf("owner unavailable: agent %q is not registered", owner)
		r.onFailure(job, "", err)
		return "", err
	}

	// Workers are not chat targets and have no heartbeat: they execute one
	// delegated task at a time and are invoked ONLY via delegation, never on a
	// schedule/heartbeat cadence. A scheduled/heartbeat run whose owner is a
	// worker is rejected with no fallback so worker autonomy can never fire.
	if cfg := r.getConfig(); cfg != nil {
		if oc := findAgentConfig(cfg, owner); oc != nil && oc.IsWorker() {
			err := fmt.Errorf(
				"owner unavailable: agent %q is a worker (workers have no heartbeat and run only via delegation)",
				owner,
			)
			r.onFailure(job, "", err)
			return "", err
		}
	}

	// The deliver=true path -- publish straight to a channel with NO agent turn
	// -- was removed with the rest of the retired Schedules UI plumbing
	// (ADR-065 spec FR-8). Nothing in the product ever set it; it was reachable
	// only by calling the API directly, and it was an egress path no ownership
	// rule could govern because no agent was involved in it at all.
	//
	// Scheduled work is tasks and heartbeats. Both wake the owning agent.

	// Wake the owning agent in the chosen session mode.
	sessionID, err := r.pickSession(job, owner)
	if err != nil {
		r.onFailure(job, "", err)
		return "", err
	}

	// Per-run deadline (FR-003): per-schedule override else global default.
	timeout := job.TimeoutSeconds
	if timeout <= 0 {
		if cfg := r.getConfig(); cfg != nil {
			timeout = cfg.Schedules.RunTimeoutSeconds
		}
		if timeout <= 0 {
			timeout = config.DefaultSchedulesRunTimeoutSeconds
		}
	}
	ctx2, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// FR-011: install a per-run child-process tracker on the run context so the
	// exec/shell tools (which call tools.TrackProcess after spawning a child)
	// record their PIDs under THIS run's session id. cleanupRunProcesses below
	// then terminates exactly those PIDs on completion (success/error/timeout).
	if r.procTrack != nil {
		sid := sessionID
		ctx2 = tools.WithProcessTracker(ctx2, func(pid int) { r.procTrack(sid, pid) })
	}

	// O-3 / F-13 / issue #342: inject the schedule identity into the run
	// context so the agent loop's auto-deny path can include the job id and
	// name when emitting audit entries for ask-gated tools that are
	// auto-denied during this headless run.
	//
	// The schedule identity (job_id, job_name) appears in two places per
	// auto-deny event:
	//   1. The tool.policy.deny.attempted entry — via emitPolicyDenyAudit's
	//      extra Details map (schedule_job_id / schedule_job_name fields).
	//   2. The structured INFO log line in emitScheduledAutoDenyAudit.
	//
	// Note: the companion tool.policy.ask.denied entry (emitted via
	// EmitToolPolicyAskDenied) does NOT carry the schedule identity fields —
	// EmitToolPolicyAskDenied has a fixed field set without them.
	ctx2 = agent.WithScheduledJobContext(ctx2, job.ID, job.Name)

	// Run the owning agent's turn. If the deadline fires while the turn is
	// still going, force-abort it via RequestCancel(CancelScope{SessionID}) and
	// allow a short cleanup window, then return a deadline error so the lane
	// records a "timeout" run (FR-003/FR-006). A watcher goroutine triggers the
	// cancel exactly once on ctx2.Done() while ProcessScheduled is in flight.
	runDone := make(chan struct{})
	go r.watchDeadline(ctx2, runDone, sessionID, owner)

	reply, runErr := r.exec.ProcessScheduled(ctx2, owner, sessionID, job.Payload.Message, "", "")
	close(runDone)
	// M6: ProcessScheduled runs the turn with SendResponse:false,
	// so the agent's final text reply is NOT auto-published to any channel. If the
	// agent wants to message a user, it does so via the message tool during its
	// turn. We intentionally discard the returned reply here.
	_ = reply

	// FR-011: best-effort clean up any child/browser processes the run spawned.
	r.cleanupRunProcesses(job, sessionID)

	if runErr != nil {
		// Always log the raw error before any wrapping so the real cause lands in
		// gateway.log regardless of what the channel-alert path surfaces.
		logger.ErrorCF("gateway", "scheduled run failed",
			map[string]any{
				"schedule_id": job.ID,
				"session_id":  sessionID,
				"owner":       owner,
				"error":       runErr.Error(),
			})
		// Surface the deadline as a context.DeadlineExceeded error so the cron
		// lane classifies the run record Status as "timeout" (errors.Is). The
		// underlying agent error is wrapped so it stays inspectable.
		if ctx2.Err() == context.DeadlineExceeded {
			runErr = fmt.Errorf("scheduled run timed out after %ds: %w", timeout, context.DeadlineExceeded)
		} else {
			// Classify transient provider errors so the lane schedules a backoff
			// retry (FR-010). Wrapping in *cron.TransientError lets cron.IsTransient
			// detect it without coupling cron to provider error types.
			if isTransientRunError(runErr) {
				runErr = &cron.TransientError{Err: runErr}
			}
		}
		r.onFailure(job, sessionID, runErr)
		return sessionID, runErr
	}
	return sessionID, nil
}

// watchDeadline force-aborts the in-flight scheduled run when ctx2's deadline
// fires (FR-003/FR-006). It returns immediately if the run completes first
// (runDone closed). On deadline it issues RequestCancel against the run's
// session and allows a short cleanup window for the turn to tear down.
func (r *scheduledRunner) watchDeadline(ctx2 context.Context, runDone <-chan struct{}, sessionID, owner string) {
	select {
	case <-runDone:
		return
	case <-ctx2.Done():
		if ctx2.Err() != context.DeadlineExceeded || r.canceller == nil || sessionID == "" {
			return
		}
		logger.WarnCF("gateway", "scheduled run exceeded deadline — force-aborting",
			map[string]any{"session_id": sessionID, "owner": owner})
		// Force-abort the turn registered under this session id. Use a detached
		// context so the cancel itself is not aborted by the just-expired ctx2.
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		outcome, cancelErr := r.canceller.RequestCancel(cancelCtx,
			agent.CancelScope{SessionID: sessionID},
			agent.CancelCanceller{UserID: "scheduler", Channel: "cron"},
			agent.CancelHooks{
				// Cascade the scheduled-run force-abort to any detached
				// background bash/exec sessions this run's session started
				// (FR-B10/FR-B11). The returned counts flow back through
				// RequestCancel into the turn_canceled audit event's
				// background_sessions_killed/background_sessions_failed
				// fields.
				KillBackgroundSessions: func(sid string) (killed, failed int) {
					return tools.GetSharedSessionManager().KillAllForSession(sid)
				},
				// OnLatchExpired: this force-abort found no active turn and
				// (per outcome.Armed, logged just below) may have armed a
				// pre-registration cancel latch (pkg/agent/cancel_prearm.go)
				// in its place — deadline enforcement DEFERRED to the next
				// turn to register under this session, not skipped. If that
				// latch itself ages out (cancelPreArmTTL) with no turn ever
				// registering, deferred became NEVER: the scheduled run's
				// deadline was silently never enforced at all. There is no
				// browser watching a cron run (unlike handleCancel's WS
				// path), so an operator-visible log is this surface's only
				// possible signal — notifyLatchExpired already logs a
				// generic Warn unconditionally; this adds the cron-specific
				// context (which run, which owner) so it is findable against
				// "scheduled run exceeded deadline" above rather than an
				// unattributed line.
				OnLatchExpired: func(scope agent.CancelScope, canceller agent.CancelCanceller) {
					logger.WarnCF("gateway", "scheduled run deadline force-abort's pre-registration cancel latch expired unconsumed — the run was never actually force-aborted",
						map[string]any{
							"session_id":        scope.SessionID,
							"owner":             owner,
							"canceller_channel": canceller.Channel,
						})
				},
			})
		if cancelErr == nil {
			if !outcome.Fired {
				// A force-abort that targeted no active turn is worth observing: the
				// run may have already completed between the deadline check and the
				// cancel, or the turn was never registered under this session id.
				// outcome.Armed distinguishes the two (CancelOutcome.Armed doc
				// comment, pkg/agent/cancel.go): true means RequestCancel latched a
				// pre-registration cancel (pkg/agent/cancel_prearm.go) that fires
				// the instant a turn registers under this session, bounded by the
				// latch's TTL — deadline enforcement is DEFERRED, not skipped;
				// false means there is genuinely nothing left to enforce against.
				logger.DebugCF("gateway", "scheduled run force-abort hit no active turn",
					map[string]any{"session_id": sessionID, "owner": owner, "armed": outcome.Armed})
				if outcome.Armed {
					logger.InfoCF("gateway", "scheduled run force-abort armed a pre-registration cancel latch — deadline enforcement deferred to the next turn registration for this session, bounded by the latch TTL",
						map[string]any{"session_id": sessionID, "owner": owner})
				}
			}
			// Observability fix: even when the run's own turn is already
			// gone (!Fired — e.g. it dispatched a `bash
			// run_in_background=true` job and returned before the deadline
			// fired), the kill cascade above still runs unconditionally and
			// may have killed real background work. There is no live client
			// connection to notify here (unlike the WS handleCancel path),
			// so an INFO log is this surface's equivalent observability
			// signal — an operator scanning gateway.log for this run should
			// see that background work was cleaned up, not just silence.
			if outcome.BackgroundSessionsKilled > 0 || outcome.BackgroundSessionsFailed > 0 {
				logger.InfoCF("gateway", "scheduled run force-abort killed background session(s)",
					map[string]any{
						"session_id":                 sessionID,
						"owner":                      owner,
						"background_sessions_killed": outcome.BackgroundSessionsKilled,
						"background_sessions_failed": outcome.BackgroundSessionsFailed,
					})
			}
		}
		// Short cleanup window: let the turn observe the cancel and unwind before
		// ProcessScheduled returns.
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
		}
	}
}

// setProcessCleanup wires the best-effort per-run child-process cleanup hook
// (FR-011). Idempotent; a nil fn clears the hook.
func (r *scheduledRunner) setProcessCleanup(fn func(sessionID string)) {
	r.procCleanup = fn
}

// setProcessTracker wires the per-run child-process tracking hook (FR-011). It
// is installed on the ProcessScheduled context so exec/shell tool spawns are
// recorded under the run's session id and later cleaned up. Idempotent; a nil
// fn clears the hook.
func (r *scheduledRunner) setProcessTracker(fn func(sessionID string, pid int)) {
	r.procTrack = fn
}

// scheduledProcRegistry is a per-session child-process registry for scheduled
// runs (FR-011). It is wired into the LEGACY (sandbox=off) exec/shell spawn
// paths: the runner installs a tools.ProcessTracker on the run context keyed to
// the run's session id, and ExecTool.runSync / ExecTool.runBackground call
// tools.TrackProcess(ctx, pid) after spawning a child, which lands here via
// Track. On run completion (success, error, or timeout) the runner calls
// Cleanup(sessionID), which terminates and forgets every tracked PID for that
// session.
//
// Residual / NOT covered:
//   - Sandbox-ON exec (ExecTool.runSyncHardened → sandbox.Run) is NOT tracked
//     here: sandbox.Run owns its child's full lifecycle and force-kills it
//     (SIGTERM→SIGKILL) on context cancel/timeout via the run's own ctx2, so a
//     hardened child cannot outlive the run regardless of this registry. Adding
//     a PID hook through sandbox.Run would require threading a callback through
//     the hardened-exec API for no additional safety on the timeout path.
//   - Browser/chromedp processes are not tracked: chromedp manages its own
//     allocator lifecycle tied to the run context. This is the known residual.
//
// Safe for concurrent use.
type scheduledProcRegistry struct {
	mu   sync.Mutex
	pids map[string][]int
	// kill terminates a pid; overridable in tests. Defaults to killProcess.
	kill func(pid int) error
}

func newScheduledProcRegistry() *scheduledProcRegistry {
	return &scheduledProcRegistry{pids: map[string][]int{}, kill: killProcess}
}

// Track records a spawned child PID under sessionID for later cleanup.
func (p *scheduledProcRegistry) Track(sessionID string, pid int) {
	if sessionID == "" || pid <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pids[sessionID] = append(p.pids[sessionID], pid)
}

// Cleanup best-effort terminates and forgets all tracked PIDs for sessionID.
// Errors are logged, never returned — cleanup must not fail the run (FR-011).
func (p *scheduledProcRegistry) Cleanup(sessionID string) {
	p.mu.Lock()
	pids := p.pids[sessionID]
	delete(p.pids, sessionID)
	kill := p.kill
	p.mu.Unlock()
	for _, pid := range pids {
		if err := kill(pid); err != nil {
			logger.WarnCF("gateway", "scheduled run: failed to terminate tracked child process",
				map[string]any{"session_id": sessionID, "pid": pid, "error": err.Error()})
		}
	}
}

// killProcess best-effort terminates a process by pid.
func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

// cleanupRunProcesses best-effort terminates any child/browser processes the
// run spawned for its session (FR-011). It NEVER fails the run — errors inside
// the hook are the hook's responsibility to log. A no-op when no hook is wired
// or no session id is known (deliver=true runs have no session).
func (r *scheduledRunner) cleanupRunProcesses(job *cron.CronJob, sessionID string) {
	if r.procCleanup == nil || sessionID == "" {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			logger.WarnCF("gateway", "scheduled run process cleanup panicked",
				map[string]any{"schedule_id": job.ID, "session_id": sessionID, "panic": fmt.Sprintf("%v", rec)})
		}
	}()
	r.procCleanup(sessionID)
}

// isTransientRunError classifies an agent/provider error as transient (FR-010):
// rate-limit, overload, network, 5xx, or provider-unavailable. Matching is by
// substring on the error text since provider errors surface as plain strings.
// A context.Canceled/DeadlineExceeded is NOT transient (handled separately).
func isTransientRunError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	transientMarkers := []string{
		"rate limit", "rate-limit", "ratelimit", "429",
		"overloaded", "overload", "503", "502", "504", "500",
		"server error", "internal server error", "bad gateway",
		"service unavailable", "temporarily unavailable", "provider unavailable",
		"timeout", "timed out", "connection reset", "connection refused",
		"network", "eof", "no route to host", "i/o timeout", "try again",
	}
	for _, m := range transientMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// stampScheduledSessionOwner sets the user-owner on a scheduled session from
// job.CreatedBy. Only stamps when the session has no owner yet (new or
// explicitly unowned). Errors are non-fatal and logged as warnings — a missing
// owner on a scheduled session degrades gracefully to the shared/unowned
// behavior rather than aborting the run (SEC-2/#406).
func (r *scheduledRunner) stampScheduledSessionOwner(store *session.UnifiedStore, sessionID, userOwner string) {
	if userOwner == "" {
		return
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		logger.WarnCF("gateway", "scheduled run: could not read session meta for owner stamp",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return
	}
	// Only stamp on sessions that have no owner yet to avoid clobbering an
	// existing owner on continue/main sessions that already carry one.
	if meta.Owner != "" {
		return
	}
	ownerCopy := userOwner
	if setErr := store.SetMeta(sessionID, session.MetaPatch{Owner: &ownerCopy}); setErr != nil {
		logger.WarnCF("gateway", "scheduled run: could not stamp session owner",
			map[string]any{"session_id": sessionID, "owner": userOwner, "error": setErr.Error()})
	}
}

// pickSession resolves the session id for the run per session mode (W-2). The
// session is created/looked up BEFORE the run so ProcessScheduled can register
// the cancellable turn under it. On a pruned continue-session, it falls back to
// a fresh isolated session (Edge: continue pruned).
func (r *scheduledRunner) pickSession(job *cron.CronJob, owner string) (string, error) {
	store := r.exec.GetSessionStore()
	if store == nil {
		return "", fmt.Errorf("session store unavailable")
	}

	mode := job.SessionMode
	if mode == "" {
		mode = cron.SessionModeIsolated
	}

	// userOwner is the authenticated user who created the schedule; used to
	// stamp the session owner so sysagent tools inherit the correct owner
	// during the run (SEC-2/#406, Rule-2). Distinct from owner (agentID).
	userOwner := job.CreatedBy

	// workspaceID is the schedule's resolved workspace (see
	// resolveScheduleWorkspaceID's doc for the two sources and why a plain
	// CronJob.AgentID lookup was rejected). Resolved once per fire and
	// stamped onto whichever session this run picks below, so
	// ProcessScheduled's existing transcriptStore.GetMeta(sessionID).WorkspaceID
	// read (loop.go, the FIX-1 comment there) resolves to a real value
	// instead of always "" — this is the write side that closes that gap.
	workspaceID := resolveScheduleWorkspaceID(r.getConfig(), *job)

	switch mode {
	case cron.SessionModeContinue:
		id := job.SessionID
		if id == "" {
			// Generate + persist a stable id on the job before the run so the
			// same session is reused across runs (W-2).
			fresh, err := store.NewScheduledSession(owner)
			if err != nil {
				return "", fmt.Errorf("continue session create: %w", err)
			}
			job.SessionID = fresh.ID
			r.stampScheduledSessionOwner(store, fresh.ID, userOwner)
			r.stampScheduledSessionWorkspace(store, fresh.ID, workspaceID)
			return fresh.ID, nil
		}
		meta, err := store.GetOrCreateScheduledSession(id, owner)
		if err != nil {
			// Continue session was deleted/pruned: fall back to a fresh one.
			logger.WarnCF("gateway", "continue session unavailable, creating fresh scheduled session",
				map[string]any{"schedule_id": job.ID, "session_id": id, "error": err.Error()})
			fresh, ferr := store.NewScheduledSession(owner)
			if ferr != nil {
				return "", fmt.Errorf("continue fallback create: %w", ferr)
			}
			r.stampScheduledSessionOwner(store, fresh.ID, userOwner)
			r.stampScheduledSessionWorkspace(store, fresh.ID, workspaceID)
			return fresh.ID, nil
		}
		r.stampScheduledSessionOwner(store, meta.ID, userOwner)
		r.stampScheduledSessionWorkspace(store, meta.ID, workspaceID)
		return meta.ID, nil

	case cron.SessionModeMain:
		id := "sched-main-" + owner
		meta, err := store.GetOrCreateScheduledSession(id, owner)
		if err != nil {
			return "", fmt.Errorf("main session create: %w", err)
		}
		r.stampScheduledSessionOwner(store, meta.ID, userOwner)
		r.stampScheduledSessionWorkspace(store, meta.ID, workspaceID)
		return meta.ID, nil

	default: // isolated
		fresh, err := store.NewScheduledSession(owner)
		if err != nil {
			return "", fmt.Errorf("isolated session create: %w", err)
		}
		r.stampScheduledSessionOwner(store, fresh.ID, userOwner)
		r.stampScheduledSessionWorkspace(store, fresh.ID, workspaceID)
		return fresh.ID, nil
	}
}

// resolveScheduleWorkspaceID resolves the workspace a fired schedule's agent
// turn should be scoped to, so pickSession can stamp it onto the session
// meta BEFORE ProcessScheduled runs. loop.go's ProcessScheduled already
// reads transcriptStore.GetMeta(sessionID).WorkspaceID (see its FIX-1
// comment) — that read was always "" because nothing upstream ever wrote
// it; this is the write side that makes it meaningful, for both
// operator-created schedules and workspace-scoped heartbeats (ADR-027).
//
// Two sources, tried in order:
//
//  1. Heartbeat jobs are workspace-scoped BY CONSTRUCTION: the reconciler
//     (heartbeat_schedule.go) names each job
//     "heartbeat:<workspaceID>:<agentID>" and that (workspace, agent) pairing
//     never changes for the job's lifetime (a workspace/interval/message
//     change re-stamps the same job; enabling elsewhere creates a distinct
//     job with a distinct name). workspaceIDFromHeartbeatJobName parses it
//     back out.
//
//  2. An operator-created schedule's payload.Channel, when set, names a
//     channel INSTANCE — the same key space processMessage resolves a
//     session's workspace from (cfg.Channels[instanceID].WorkspaceID; see
//     loop.go's resolveOrCreateChannelSession / resolveWorkspaceIDForContinuation).
//     A channel instance is bound to at most one workspace (ADR-029), so
//     this is a direct config lookup — no ResolveRoute/peer-binding pass is
//     needed or even possible here: a fired schedule carries no
//     peer/guild/team match context, only a channel + chat id.
//
// Rejected as a general-purpose source: CronJob.AgentID (the owning agent)
// alone. An agent can sit on multiple workspaces with different rosters —
// that is the explicit rationale ADR-037 gives for removing the global
// delegation graph in favor of workspace-scoped trust — so "the owner's
// workspace" is ambiguous for a plain schedule. It is NOT ambiguous for a
// heartbeat job specifically, because a heartbeat job already IS one
// (workspace, agent) pair by construction; case 1 above relies on that,
// not on AgentID being globally unique to one workspace.
//
// Rejected as a schema change: adding an explicit WorkspaceID field to
// CronJob/CronPayload (option (c) in the investigation). Schedules are a
// REST entity (Constraint #8), so a new field would need a contracts/
// change, codegen, AND a migration story for every job already on disk.
// Both sources above are derivable from data the job already carries, so no
// wire change is needed.
//
// A schedule with neither a heartbeat name nor a channel (e.g.
// deliver=true's own send-path, which never reaches pickSession, or an
// operator schedule that fires no channel context at all) has no
// resolvable workspace. "" is returned deliberately in that case — never
// fabricated — and stampScheduledSessionWorkspace treats "" as "nothing to
// stamp", leaving the session's existing (possibly still-empty) tag alone.
func resolveScheduleWorkspaceID(_ *config.Config, job cron.CronJob) string {
	// A workspace-scoped heartbeat encodes its workspace in the job name, and
	// that is now the only signal available: the schedule itself no longer
	// carries a channel to look one up from (ADR-065 spec FR-8 removed that
	// plumbing along with the rest of the retired Schedules UI).
	//
	// Everything else resolves to "" — deliberately, never fabricated.
	// stampScheduledSessionWorkspace treats "" as "nothing to stamp", leaving
	// the session's existing tag alone rather than guessing one.
	return workspaceIDFromHeartbeatJobName(job)
}

// workspaceIDFromHeartbeatJobName extracts the workspace id encoded in a
// workspace-scoped heartbeat job's deterministic name
// ("heartbeat:<workspaceID>:<agentID>", built by heartbeatJobName in
// heartbeat_schedule.go — same package, reused here rather than
// re-implemented). job.AgentID is already known and trusted (it is exactly
// the string the reconciler appended when it built the name), so trimming
// that exact ":"+AgentID suffix from the name yields exactly the workspace
// id, regardless of what characters the workspace id itself contains —
// robust even if a workspace id were ever allowed to include a colon,
// unlike a positional split on ":". Returns "" for a non-heartbeat job or a
// name that does not match the expected shape (e.g. hand-edited store data
// or a stale AgentID after a heartbeat reassignment mid-flight).
func workspaceIDFromHeartbeatJobName(job cron.CronJob) string {
	if job.Payload.Kind != heartbeatJobKind {
		// Not a heartbeat job at all — this is the overwhelmingly common
		// case for every plain user schedule, so it is deliberately NOT
		// logged (would be pure noise on every non-heartbeat fire).
		return ""
	}
	// From here on the job claims to BE a heartbeat job, so every remaining
	// "" return is a name/AgentID mismatch on a job that should be
	// internally consistent by construction (heartbeatJobName, the
	// reconciler's sole writer of this shape) — a config-integrity signal
	// worth an operator's attention regardless of how it arose (FIX 2):
	// hand-edited store data, a stale AgentID surviving a heartbeat
	// reassignment mid-flight (ReconcileHeartbeatSchedules never mutates an
	// existing job's AgentID in place — see its "Update in place" comment —
	// so this path is reconcile-safe on its own), or a caller reassigning
	// owner_agent_id through PUT /schedules/{id} before the FIX 2 guard on
	// that endpoint existed.
	if job.AgentID == "" {
		logger.WarnCF("gateway", "heartbeat-kind job has no owning agent id; cannot resolve workspace from its name",
			map[string]any{"job_id": job.ID, "job_name": job.Name})
		return ""
	}
	rest := strings.TrimPrefix(job.Name, heartbeatJobNamePrefix)
	if rest == job.Name {
		logger.WarnCF(
			"gateway",
			"heartbeat-kind job name is missing the expected \"heartbeat:\" prefix; workspace cannot be resolved",
			map[string]any{"job_id": job.ID, "job_name": job.Name, "agent_id": job.AgentID},
		)
		return "" // name doesn't carry the expected "heartbeat:" prefix at all
	}
	suffix := ":" + job.AgentID
	if !strings.HasSuffix(rest, suffix) {
		logger.WarnCF(
			"gateway",
			"heartbeat-kind job name does not match its own agent id; workspace cannot be resolved",
			map[string]any{"job_id": job.ID, "job_name": job.Name, "agent_id": job.AgentID},
		)
		return ""
	}
	return strings.TrimSuffix(rest, suffix)
}

// stampScheduledSessionWorkspace sets WorkspaceID on a scheduled session from
// the resolved schedule workspace (resolveScheduleWorkspaceID). Mirrors
// stampScheduledSessionOwner's posture in every respect: only stamps when
// the session has no workspace yet (a later change to the schedule's channel
// binding does not retroactively move an already-anchored continue/main
// session — same non-clobber policy the owner stamp uses; for isolated mode
// every session is fresh so this always fires), a resolved-empty
// workspaceID is a legitimate "no context" outcome and is never forced onto
// an existing meta, and errors are non-fatal and logged as warnings
// (SEC-2/#406 posture) — a missing workspace tag degrades gracefully to the
// private/global room for media resolution rather than aborting the run.
func (r *scheduledRunner) stampScheduledSessionWorkspace(store *session.UnifiedStore, sessionID, workspaceID string) {
	if workspaceID == "" {
		return
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		logger.WarnCF("gateway", "scheduled run: could not read session meta for workspace stamp",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return
	}
	if meta.WorkspaceID != "" {
		return
	}
	wsCopy := workspaceID
	if setErr := store.SetMeta(sessionID, session.MetaPatch{WorkspaceID: &wsCopy}); setErr != nil {
		logger.WarnCF("gateway", "scheduled run: could not stamp session workspace",
			map[string]any{"session_id": sessionID, "workspace_id": workspaceID, "error": setErr.Error()})
	}
}

// onFailure raises the FR-013 side-effects for a failed run: (a) a coalesced
// notification for the schedule's creator AND the owning agent's owner (dedup;
// all admins if neither resolvable), (b) a channel alert to the owning agent's
// default channel (skipped if none resolves), and (c) a live WS push of the
// notification (via EmitNotification). On success this is never called.
func (r *scheduledRunner) onFailure(job *cron.CronJob, sessionID string, runErr error) {
	cfg := r.getConfig()

	recipients := r.resolveRecipients(job, cfg)
	body := runErr.Error()
	title := fmt.Sprintf("Schedule %q failed", scheduleDisplayName(job))

	for _, recipient := range recipients {
		n := notifications.Notification{
			Recipient:  recipient,
			Type:       notifications.TypeScheduleFailed,
			Title:      title,
			Body:       body,
			Severity:   notifications.SeverityError,
			ScheduleID: job.ID,
			// One live item per schedule, updated — the behaviour Create used
			// to give this path implicitly by keying on ScheduleID. It is
			// stated explicitly now that the key is its own field.
			CoalesceKey: "schedule:" + job.ID,
			SessionID:   sessionID,
			AgentID:     job.AgentID,
		}
		stored := n
		// M4: the admin-broadcast sentinel is NOT a real username — persisting it
		// writes _admin_.json which no ListForUser(username) ever reads. Only
		// resolved usernames are persisted; the sentinel is a live-push-only
		// broadcast (used when there are no users at all to enumerate).
		if r.notifs != nil && recipient != agent.NotificationAdminBroadcast {
			persisted, err := r.notifs.Create(n)
			if err != nil {
				// H3: persistence failed, but the recipient must STILL see the alert
				// live. Dropping the push too would lose the failure alert entirely —
				// operator-visible data loss, so log at Error (not Warn) and fall
				// through to push the in-memory notification. The bell will be stale
				// after a restart, but the live WS push still fires now.
				logger.ErrorCF("gateway", "failed to persist schedule-failure notification; pushing live anyway",
					map[string]any{"schedule_id": job.ID, "recipient": recipient, "error": err.Error()})
			} else {
				stored = persisted
			}
		}
		// Live push to the recipient's connections (filtered by userID in the WS
		// forwarder). The admin-broadcast recipient maps to the sentinel.
		r.pushNotification(stored, recipient)
	}

	// Channel alert to the owning agent's default channel (FR-013b). Skip if the
	// agent has no resolvable channel — the notification still went out.
	r.publishChannelAlert(job, cfg, body)
}

// pushNotification emits a live WS notification frame for the recipient. The
// special all-admins recipient is translated to the broadcast sentinel.
func (r *scheduledRunner) pushNotification(n notifications.Notification, recipient string) {
	if r.exec == nil {
		return
	}
	r.exec.EmitNotification(agent.NotificationPayload{
		Recipient:        recipient,
		ID:               n.ID,
		NotificationType: n.Type,
		Title:            n.Title,
		Body:             n.Body,
		Severity:         n.Severity,
		Read:             n.Read,
		CreatedAtMs:      n.CreatedAtMs,
		ScheduleID:       n.ScheduleID,
		SessionID:        n.SessionID,
		AgentID:          n.AgentID,
	})
}

// resolveRecipients returns the deduped set of usernames to notify (W-7): the
// schedule's CreatedBy user. Single-user product: ownership/role are no longer
// access-control concepts, so when CreatedBy is not resolvable (e.g. a
// pre-existing job with no creator recorded), it falls back to the sole
// account. Only if there are no users at all does it return the
// admin-broadcast sentinel, which is a live-push-only target (never
// persisted; see onFailure) — WS delivery is unconditional now, so it
// correctly reaches whoever is connected.
func (r *scheduledRunner) resolveRecipients(job *cron.CronJob, cfg *config.Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		out = append(out, u)
	}

	add(job.CreatedBy)
	if len(out) == 0 && cfg != nil && len(cfg.Gateway.Users) > 0 {
		add(cfg.Gateway.Users[0].Username) // the sole account
	}
	if len(out) == 0 {
		// No persistable recipient (zero users at all). The failure can only be
		// live-broadcast to any connected WS — if none is open, the alert
		// would otherwise vanish. Log at WARN so the failure is still recorded in
		// gateway.log for after-the-fact diagnosis (finding M4).
		logger.WarnCF("gateway", "schedule-failure notification has no persistable recipient; live-broadcast only",
			map[string]any{"schedule_id": job.ID, "owner": job.AgentID})
		out = append(out, agent.NotificationAdminBroadcast)
	}
	return out
}

// publishChannelAlert sends an alert to the owning agent's default channel
// (FR-013b). The default channel is resolved from the most-specific
// channel-wildcard binding for the owner; if the schedule itself carries a
// channel, that wins (it is the run's outbound context).
func (r *scheduledRunner) publishChannelAlert(job *cron.CronJob, cfg *config.Config, body string) {
	// The schedule no longer carries its own channel, so the owner's default
	// channel is the only target. That is the correct one for a failure alert:
	// it reaches the agent that owns the schedule.
	var channel, chatID string
	if cfg != nil {
		channel, chatID = resolveAgentDefaultChannel(cfg, job.AgentID)
	}
	if channel == "" {
		logger.WarnCF("gateway", "schedule failure: no default channel to alert",
			map[string]any{"schedule_id": job.ID, "owner": job.AgentID})
		return
	}
	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	alert := fmt.Sprintf("⚠ Scheduled task %q failed: %s", scheduleDisplayName(job), body)
	if err := r.msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: alert,
	}); err != nil {
		logger.WarnCF("gateway", "schedule failure alert publish failed",
			map[string]any{"schedule_id": job.ID, "channel": channel, "error": err.Error()})
	}
}

func scheduleDisplayName(job *cron.CronJob) string {
	if job.Name != "" {
		return job.Name
	}
	return job.ID
}

// findAgentConfig returns the AgentConfig with the given id, or nil.
func findAgentConfig(cfg *config.Config, agentID string) *config.AgentConfig {
	if cfg == nil || agentID == "" {
		return nil
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			return &cfg.Agents.List[i]
		}
	}
	return nil
}

// resolveAgentDefaultChannel finds the channel an agent is bound to, returning
// (channel, chatID). It scans bindings for one whose AgentID == agentID and
// returns its channel; chatID is left empty (channel-level binding). Returns
// ("","") when the agent has no binding.
func resolveAgentDefaultChannel(cfg *config.Config, agentID string) (string, string) {
	if cfg == nil || agentID == "" {
		return "", ""
	}
	for _, b := range cfg.Bindings {
		if b.AgentID == agentID && b.Match.Channel != "" && b.Match.Channel != "*" {
			return b.Match.Channel, ""
		}
	}
	return "", ""
}

// ---------------------------------------------------------------------------
// REST handlers (#264, FR-015). Contract-first: every wire byte uses gen.* types.
// ---------------------------------------------------------------------------

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func i64Ptr(v int64) *int64 { return &v }

func intPtr(v int) *int { return &v }

// toSchedule projects a cron.CronJob onto the generated Schedule wire type.
func toSchedule(job cron.CronJob) gen.Schedule {
	mode := job.SessionMode
	if mode == "" {
		mode = cron.SessionModeIsolated
	}
	s := gen.Schedule{
		Id:             job.ID,
		Name:           job.Name,
		Enabled:        job.Enabled,
		OwnerAgentId:   job.AgentID,
		CreatedBy:      strPtr(job.CreatedBy),
		Message:        job.Payload.Message,
		SessionMode:    gen.ScheduleSessionMode(mode),
		SessionId:      strPtr(job.SessionID),
		TimeoutSeconds: job.TimeoutSeconds,
		CreatedAtMs:    job.CreatedAtMS,
		UpdatedAtMs:    job.UpdatedAtMS,
	}

	// Trigger.
	s.Trigger.Kind = gen.ScheduleTriggerKind(job.Schedule.Kind)
	s.Trigger.CronExpr = strPtr(job.Schedule.Expr)
	s.Trigger.AtMs = job.Schedule.AtMS
	s.Trigger.EveryMs = job.Schedule.EveryMS

	// State.
	s.State.NextRunAtMs = job.State.NextRunAtMS
	s.State.LastRunAtMs = job.State.LastRunAtMS
	if job.State.LastStatus != "" {
		s.State.LastStatus = strPtr(job.State.LastStatus)
	}
	if job.State.LastError != "" {
		s.State.LastError = strPtr(job.State.LastError)
	}
	s.State.ConsecutiveFailures = intPtr(job.State.ConsecutiveFailures)
	running := job.State.Running
	s.State.Running = &running

	// Runs (newest first).
	if len(job.State.History) > 0 {
		runs := make([]struct {
			DurationMs *int64                 `json:"duration_ms,omitempty"`
			Error      *string                `json:"error,omitempty"`
			RanAtMs    int64                  `json:"ran_at_ms"`
			SessionId  *string                `json:"session_id,omitempty"`
			Status     gen.ScheduleRunsStatus `json:"status"`
		}, 0, len(job.State.History))
		for i := len(job.State.History) - 1; i >= 0; i-- {
			rec := job.State.History[i]
			runs = append(runs, struct {
				DurationMs *int64                 `json:"duration_ms,omitempty"`
				Error      *string                `json:"error,omitempty"`
				RanAtMs    int64                  `json:"ran_at_ms"`
				SessionId  *string                `json:"session_id,omitempty"`
				Status     gen.ScheduleRunsStatus `json:"status"`
			}{
				DurationMs: i64Ptr(rec.DurationMs),
				Error:      strPtr(rec.Error),
				RanAtMs:    rec.RanAtMs,
				SessionId:  strPtr(rec.SessionID),
				Status:     gen.ScheduleRunsStatus(rec.Status),
			})
		}
		s.Runs = &runs
	}
	return s
}

// cronSvc safely loads the current cron service pointer.
// Returns nil when the schedules service has not been configured.
func (a *restAPI) cronSvc() *cron.CronService {
	return a.cronService.Load()
}

// HandleSchedules dispatches /api/v1/schedules and /api/v1/schedules/{id}[/run|/pause].
func (a *restAPI) HandleSchedules(w http.ResponseWriter, r *http.Request) {
	if a.cronSvc() == nil {
		jsonErr(w, http.StatusServiceUnavailable, "schedules service unavailable")
		return
	}
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "invalid token")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/schedules")
	rest = strings.Trim(rest, "/")

	switch {
	case rest == "": // collection
		switch r.Method {
		case http.MethodGet:
			a.handleListSchedules(w)
		case http.MethodPost:
			a.handleCreateSchedule(w, r, user)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	default:
		parts := strings.Split(rest, "/")
		id := parts[0]
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				a.handleGetSchedule(w, id)
			case http.MethodPut:
				a.handleUpdateSchedule(w, r, id)
			case http.MethodDelete:
				a.handleDeleteSchedule(w, id)
			default:
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}
		if len(parts) == 2 && r.Method == http.MethodPost {
			switch parts[1] {
			case "run":
				a.handleRunSchedule(w, id)
				return
			case "pause":
				a.handlePauseSchedule(w, id)
				return
			}
		}
		jsonErr(w, http.StatusNotFound, "endpoint not found")
	}
}

func (a *restAPI) handleListSchedules(w http.ResponseWriter) {
	jobs := a.cronSvc().ListJobs(true)
	// Build a slice of the generated Schedule type, then round-trip the whole
	// list into gen.ScheduleList. The ScheduleList element is structurally
	// identical to gen.Schedule, so the JSON marshal/unmarshal maps cleanly
	// without any hand-written wire-format struct (hard-constraint #8).
	//
	// Single-user product: every authenticated caller sees every schedule —
	// ownership is no longer an access-control concept.
	items := make([]gen.Schedule, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, toSchedule(job))
	}
	buf, err := json.Marshal(map[string][]gen.Schedule{"schedules": items})
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	var out gen.ScheduleList
	if err := json.Unmarshal(buf, &out); err != nil {
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	jsonOK(w, out)
}

func (a *restAPI) handleGetSchedule(w http.ResponseWriter, id string) {
	job, ok := a.cronSvc().GetJob(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	jsonOK(w, toSchedule(job))
}

func (a *restAPI) handleCreateSchedule(w http.ResponseWriter, r *http.Request, user *config.UserConfig) {
	var req gen.ScheduleCreate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Message) == "" || req.OwnerAgentId == "" {
		jsonErr(w, http.StatusBadRequest, "name, message, and owner_agent_id are required")
		return
	}

	// Write-time guard: a worker is not a chat target and never runs on a
	// schedule cadence — workers execute one delegated task at a time. Reject
	// creating a schedule whose owner is a worker at the write gate so the
	// call never lands a runnable job that the fire path would only reject
	// at execution time. Mirrors the in-flight guard in RunScheduled and the
	// default/chat guards elsewhere.
	if owner := findAgentConfig(a.agentLoop.GetConfig(), req.OwnerAgentId); owner != nil && owner.IsWorker() {
		jsonErr(w, http.StatusBadRequest,
			"a worker cannot own a schedule (workers are not chat targets and run only via delegation)")
		return
	}

	schedule := cron.CronSchedule{Kind: string(req.Trigger.Kind)}
	schedule.Expr = derefStr(req.Trigger.CronExpr)
	schedule.AtMS = req.Trigger.AtMs
	schedule.EveryMS = req.Trigger.EveryMs
	if msg, valErr := validateTrigger(schedule); valErr {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	timeout := derefInt(req.TimeoutSeconds)
	if timeout < 0 {
		jsonErr(w, http.StatusBadRequest, "timeout_seconds must be >= 0")
		return
	}

	// Resolve the session mode (M5b: reject an unknown value up front).
	sessionMode := cron.SessionModeIsolated
	if req.SessionMode != nil {
		sessionMode = cron.SessionMode(*req.SessionMode)
		if !sessionMode.Valid() {
			jsonErr(w, http.StatusBadRequest, "session_mode must be one of isolated|continue|main")
			return
		}
	}

	// M3: persist the COMPLETE job (owner, mode, timeout, created-by) in one
	// atomic write via AddJobFull. The previous AddJob+UpdateJob pair briefly
	// persisted an owner-less job that could fire and die between the two writes.
	job, err := a.cronSvc().AddJobFull(cron.JobSpec{
		Name:           req.Name,
		Schedule:       schedule,
		Message:        req.Message,
		AgentID:        req.OwnerAgentId,
		SessionMode:    sessionMode,
		TimeoutSeconds: timeout,
		CreatedBy:      user.Username,
		Enabled:        req.Enabled,
	})
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to create schedule")
		return
	}
	jsonCreated(w, toSchedule(*job))
}

func (a *restAPI) handleUpdateSchedule(w http.ResponseWriter, r *http.Request, id string) {
	job, ok := a.cronSvc().GetJob(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	var req gen.ScheduleUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Heartbeat-immutability guard: a heartbeat-kind job's (workspace, agent)
	// pair is fixed by construction — the reconciler names it
	// "heartbeat:<workspaceID>:<agentID>" (heartbeatJobName) and treats that
	// pairing as immutable for the job's lifetime; a reassignment there is
	// always a remove-and-recreate under a new name (ReconcileHeartbeatSchedules),
	// never an in-place AgentID mutation. This endpoint has no such
	// discipline: letting a caller reassign owner_agent_id here would desync
	// the job's name from its AgentID, which is exactly the drift
	// workspaceIDFromHeartbeatJobName treats as "workspace unresolvable" (it
	// refuses to guess and returns "", now also logged at WARN) — silently
	// losing the workspace stamp on the job's session at its very next fire.
	// Reject the reassignment outright instead of persisting an inconsistent
	// job; the workspace's own Team tab (member_configs) is the only place a
	// heartbeat's owning agent is meant to change, and that path removes and
	// recreates the job under the correct name via the reconciler.
	if req.OwnerAgentId != nil && *req.OwnerAgentId != job.AgentID && isHeartbeatJob(job) {
		jsonErr(
			w,
			http.StatusBadRequest,
			"owner_agent_id cannot be reassigned on a heartbeat-kind schedule (managed via the workspace's member_configs, not this endpoint)",
		)
		return
	}

	// Worker-tier guard: a worker is not a chat target and never runs on a
	// schedule cadence — block ownership reassignment to a worker the same
	// way the create gate does. The fire path also rejects worker owners at
	// run time; failing here keeps the API consistent and surfaces the
	// reason to the operator before any persistence.
	if req.OwnerAgentId != nil && *req.OwnerAgentId != job.AgentID {
		if newOwner := findAgentConfig(
			a.agentLoop.GetConfig(),
			*req.OwnerAgentId,
		); newOwner != nil &&
			newOwner.IsWorker() {
			jsonErr(w, http.StatusBadRequest,
				"a worker cannot own a schedule (workers are not chat targets and run only via delegation)")
			return
		}
		job.AgentID = *req.OwnerAgentId
	}
	if req.Name != nil {
		job.Name = *req.Name
	}
	if req.Message != nil {
		job.Payload.Message = *req.Message
	}
	if req.SessionMode != nil {
		// M5b: reject an unknown session mode before persisting it (a bad value
		// would silently degrade to isolated at run time).
		mode := cron.SessionMode(*req.SessionMode)
		if !mode.Valid() {
			jsonErr(w, http.StatusBadRequest, "session_mode must be one of isolated|continue|main")
			return
		}
		job.SessionMode = mode
	}
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds < 0 {
			jsonErr(w, http.StatusBadRequest, "timeout_seconds must be >= 0")
			return
		}
		job.TimeoutSeconds = *req.TimeoutSeconds
	}
	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}
	if req.Trigger != nil {
		schedule := cron.CronSchedule{Kind: string(req.Trigger.Kind)}
		schedule.Expr = derefStr(req.Trigger.CronExpr)
		schedule.AtMS = req.Trigger.AtMs
		schedule.EveryMS = req.Trigger.EveryMs
		if msg, valErr := validateTrigger(schedule); valErr {
			jsonErr(w, http.StatusBadRequest, msg)
			return
		}
		job.Schedule = schedule
	}

	if err := a.cronSvc().UpdateJob(&job); err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to update schedule")
		return
	}
	jsonOK(w, toSchedule(job))
}

func (a *restAPI) handleDeleteSchedule(w http.ResponseWriter, id string) {
	if _, ok := a.cronSvc().GetJob(id); !ok {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	if !a.cronSvc().RemoveJob(id) {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *restAPI) handleRunSchedule(w http.ResponseWriter, id string) {
	if _, ok := a.cronSvc().GetJob(id); !ok {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	status, sessionID, runErr := a.cronSvc().RunNow(id)
	if status == "" && runErr != nil {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	res := gen.ScheduleRunResult{
		ScheduleId: id,
		Status:     gen.ScheduleRunResultStatus(status),
	}
	if sessionID != "" {
		res.SessionId = &sessionID
	}
	if runErr != nil {
		msg := runErr.Error()
		res.Error = &msg
	}
	jsonOK(w, res)
}

func (a *restAPI) handlePauseSchedule(w http.ResponseWriter, id string) {
	job, ok := a.cronSvc().GetJob(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	updated := a.cronSvc().EnableJob(id, !job.Enabled)
	if updated == nil {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	jsonOK(w, toSchedule(*updated))
}

// validateTrigger checks a cron schedule's trigger fields. Returns (message,
// true) when invalid.
func validateTrigger(s cron.CronSchedule) (string, bool) {
	switch s.Kind {
	case "cron":
		if s.Expr == "" {
			return "cron trigger requires cron_expr", true
		}
		if !gronx.IsValid(s.Expr) {
			return "invalid cron expression", true
		}
	case "every":
		if s.EveryMS == nil || *s.EveryMS <= 0 {
			return "every trigger requires a positive every_ms", true
		}
	case "at":
		if s.AtMS == nil {
			return "at trigger requires at_ms", true
		}
	default:
		return "trigger.kind must be one of cron|every|at", true
	}
	return "", false
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// ---------------------------------------------------------------------------
// Notifications REST (#264).
// ---------------------------------------------------------------------------

// HandleNotifications dispatches /api/v1/notifications and
// /api/v1/notifications/{id}/read and /api/v1/notifications/read-all.
func (a *restAPI) HandleNotifications(w http.ResponseWriter, r *http.Request) {
	if a.notifStore == nil {
		jsonErr(w, http.StatusServiceUnavailable, "notifications unavailable")
		return
	}
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "invalid token")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/notifications")
	rest = strings.Trim(rest, "/")

	switch {
	case rest == "":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleListNotifications(w, user)
	case rest == "read-all":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := a.notifStore.MarkAllRead(user.Username); err != nil {
			jsonErr(w, http.StatusInternalServerError, "failed to mark all read")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		parts := strings.Split(rest, "/")
		if len(parts) == 2 && parts[1] == "read" && r.Method == http.MethodPost {
			if err := a.notifStore.MarkRead(user.Username, parts[0]); err != nil {
				jsonErr(w, http.StatusInternalServerError, "failed to mark read")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		jsonErr(w, http.StatusNotFound, "endpoint not found")
	}
}

// toNotification projects an internal notification onto the generated wire type.
func toNotification(n notifications.Notification) gen.Notification {
	g := gen.Notification{
		Id:          n.ID,
		Type:        gen.NotificationType(n.Type),
		Title:       n.Title,
		Body:        strPtr(n.Body),
		Severity:    gen.NotificationSeverity(n.Severity),
		Read:        n.Read,
		CreatedAtMs: n.CreatedAtMs,
		ScheduleId:  strPtr(n.ScheduleID),
		SessionId:   strPtr(n.SessionID),
		AgentId:     strPtr(n.AgentID),
	}
	if n.UpdatedAtMs != 0 {
		g.UpdatedAtMs = i64Ptr(n.UpdatedAtMs)
	}
	return g
}

func (a *restAPI) handleListNotifications(w http.ResponseWriter, user *config.UserConfig) {
	list, err := a.notifStore.ListForUser(user.Username)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to list notifications")
		return
	}
	unread := 0
	items := make([]gen.Notification, 0, len(list))
	for _, n := range list {
		if !n.Read {
			unread++
		}
		items = append(items, toNotification(n))
	}
	// Round-trip the generated Notification slice into NotificationList. The
	// NotificationList element is structurally identical to gen.Notification, so
	// this maps cleanly with no hand-written wire-format struct (#8).
	buf, mErr := json.Marshal(map[string]any{"notifications": items, "unread_count": unread})
	if mErr != nil {
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	var out gen.NotificationList
	if uErr := json.Unmarshal(buf, &out); uErr != nil {
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	jsonOK(w, out)
}
