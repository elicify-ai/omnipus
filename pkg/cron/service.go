package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/adhocore/gronx"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// Clock is the time source for the cron service. It is injected so tests can
// fire jobs deterministically (W-5 / M-10 clock seam) without any wall-clock
// sleeps. Production uses realClock (time.Now); the runLoop timer remains real
// time — tests bypass the loop and drive RunDueJobs directly.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// SessionMode is the session-selection mode for a scheduled job (FR-004).
type SessionMode string

// SessionMode values (FR-004).
const (
	SessionModeIsolated SessionMode = "isolated"
	SessionModeContinue SessionMode = "continue"
	SessionModeMain     SessionMode = "main"
)

// Valid reports whether m is one of the known session modes.
func (m SessionMode) Valid() bool {
	switch m {
	case SessionModeIsolated, SessionModeContinue, SessionModeMain:
		return true
	default:
		return false
	}
}

const (
	// defaultMaxConcurrentRuns is the fallback parallel-lane capacity when none
	// is supplied (schedules.max_concurrent_runs default 8, FR-007).
	defaultMaxConcurrentRuns = 8

	// runHistoryCap is the number of inline run records retained per job
	// (Ambiguity #5: keep last 20; full history via linked sessions).
	runHistoryCap = 20

	// defaultStopDrainTimeout bounds how long Stop() blocks on in-flight lane
	// runs before it gives up waiting and proceeds (the lane ctx is already
	// canceled, so the runs are tearing down). Prevents an unbounded shutdown.
	defaultStopDrainTimeout = 30 * time.Second
)

// defaultRetryBackoffMs is the transient-error retry backoff schedule (FR-010):
// the next fire after the Nth transient failure is offset by backoff[N] ms. A
// run that exhausts len(backoff) attempts records a terminal failure and resumes
// normal cadence. RetryAttempt resets to 0 after any success.
var defaultRetryBackoffMs = []int64{60000, 120000, 300000}

// TransientError wraps a provider/agent error the runner classified as transient
// (rate-limit / overload / network / 5xx / provider-unavailable). The cron lane
// inspects it via IsTransient to drive FR-010 retry/backoff scheduling.
type TransientError struct{ Err error }

func (e *TransientError) Error() string {
	if e.Err == nil {
		return "transient error"
	}
	return e.Err.Error()
}

func (e *TransientError) Unwrap() error { return e.Err }

// IsTransient reports whether err (or anything it wraps) is a TransientError.
// The runner wraps classified provider errors in *TransientError; the lane uses
// this to decide whether to schedule a backoff retry (FR-010).
func IsTransient(err error) bool {
	var te *TransientError
	return errors.As(err, &te)
}

// ScheduledRunner is the agent integration seam (set via SetRunner). The lane
// calls RunScheduled for each due job that has an owner; the (string,error)
// outcome is recorded into CronJobState. The production implementation is the
// gateway's scheduledRunner (pkg/gateway/schedules.go); it is the sole fire
// path — when no runner is set the job records a no-op.
type ScheduledRunner interface {
	RunScheduled(ctx context.Context, job *CronJob) (string, error)
}

type CronSchedule struct {
	Kind    string `json:"kind"`
	AtMS    *int64 `json:"atMs,omitempty"`
	EveryMS *int64 `json:"everyMs,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

type CronPayload struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Command string `json:"command,omitempty"`
	Deliver bool   `json:"deliver"`
	Channel string `json:"channel,omitempty"`
	To      string `json:"to,omitempty"`
}

// CronRunRecord is one inline run-history entry (Revision 2 run-history shape).
type CronRunRecord struct {
	RanAtMs    int64  `json:"ranAtMs"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

type CronJobState struct {
	NextRunAtMS *int64 `json:"nextRunAtMs,omitempty"`
	LastRunAtMS *int64 `json:"lastRunAtMs,omitempty"`
	LastStatus  string `json:"lastStatus,omitempty"`
	LastError   string `json:"lastError,omitempty"`

	// ConsecutiveFailures counts back-to-back error runs; reset to 0 on success
	// (FR-012). Surfaced by the runner's failure alerting (Ambiguity #1: no
	// auto-pause — failures keep alerting, coalesced per schedule).
	ConsecutiveFailures int `json:"consecutiveFailures,omitempty"`
	// Running is the overlap guard (FR-008): set true around a run, false after.
	Running bool `json:"running,omitempty"`
	// RetryAttempt counts consecutive transient-error retries currently scheduled
	// on backoff (FR-010). Incremented when a transient failure schedules a
	// backoff retry; reset to 0 after any success or after the backoff schedule
	// is exhausted. Distinct from ConsecutiveFailures, which counts all failures.
	RetryAttempt int `json:"retryAttempt,omitempty"`
	// History holds the last runHistoryCap run records inline.
	History []CronRunRecord `json:"history,omitempty"`
}

type CronJob struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Enabled        bool         `json:"enabled"`
	Schedule       CronSchedule `json:"schedule"`
	Payload        CronPayload  `json:"payload"`
	State          CronJobState `json:"state"`
	CreatedAtMS    int64        `json:"createdAtMs"`
	UpdatedAtMS    int64        `json:"updatedAtMs"`
	DeleteAfterRun bool         `json:"deleteAfterRun"`

	// AgentID is the owning agent that a fired job wakes (FR-001). Optional /
	// default-zero for back-compat; empty owners are migrated on load (W-8).
	AgentID string `json:"agentId,omitempty"`
	// SessionMode is one of isolated|continue|main (FR-004); default isolated.
	SessionMode SessionMode `json:"sessionMode,omitempty"`
	// TimeoutSeconds is the per-schedule run deadline override (FR-003); 0 = use default.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// CreatedBy is the user who created the schedule (W-7 notification ownership).
	CreatedBy string `json:"createdBy,omitempty"`
	// SessionID is the stable per-schedule session id for continue mode (W-2).
	SessionID string `json:"sessionId,omitempty"`
}

type CronStore struct {
	Version int       `json:"version"`
	Jobs    []CronJob `json:"jobs"`
}

type CronService struct {
	storePath string
	store     *CronStore
	mu        sync.RWMutex
	running   bool
	stopChan  chan struct{}
	wakeChan  chan struct{}
	gronx     *gronx.Gronx

	// clock is the injected time source (W-5). Defaults to realClock.
	clock Clock

	// runner is the agent fire path (W-3/runner seam); nil until SetRunner.
	runner ScheduledRunner

	// defaultAgentID backfills owner-less jobs on load (W-8 migration). Empty
	// means "no default" → owner-less jobs are skipped, not fired.
	defaultAgentID string

	// maxConcurrentRuns bounds the parallel autonomous lane (FR-007).
	maxConcurrentRuns int
	// laneSem is the semaphore for the lane; cap = maxConcurrentRuns.
	laneSem chan struct{}
	// laneWG tracks in-flight lane runs so Stop() can drain them (W-3).
	laneWG sync.WaitGroup
	// laneCtx/laneCancel cancel in-flight runs on shutdown (W-3).
	laneCtx    context.Context
	laneCancel context.CancelFunc

	// retryBackoffMs is the FR-010 transient-error backoff schedule (ms offsets
	// keyed by RetryAttempt). Defaults to defaultRetryBackoffMs.
	retryBackoffMs []int64
	// stopDrainTimeout bounds Stop()'s wait on in-flight runs (defaults to
	// defaultStopDrainTimeout). 0 means "wait unbounded".
	stopDrainTimeout time.Duration

	// onSkip, when set, is invoked synchronously whenever executeJobByID skips a
	// fire via the overlap guard (FR-008) or the owner-missing guard (W-8),
	// before the lock is released. It is a deterministic test seam so a test can
	// receive a skip signal instead of polling a wall-clock window for a
	// non-event. nil in production. reason is "overlap" or "owner-missing".
	onSkip func(jobID, reason string)
}

// SetOnSkip installs a deterministic skip-observer (tests only). The callback
// fires for every overlap/owner-missing skip in executeJobByID. nil clears it.
func (cs *CronService) SetOnSkip(fn func(jobID, reason string)) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.onSkip = fn
}

func NewCronService(storePath string) *CronService {
	cs := &CronService{
		storePath:         storePath,
		gronx:             gronx.New(),
		wakeChan:          make(chan struct{}),
		clock:             realClock{},
		maxConcurrentRuns: defaultMaxConcurrentRuns,
		retryBackoffMs:    append([]int64(nil), defaultRetryBackoffMs...),
		stopDrainTimeout:  defaultStopDrainTimeout,
	}
	cs.laneSem = make(chan struct{}, cs.maxConcurrentRuns)
	cs.laneCtx, cs.laneCancel = context.WithCancel(context.Background())
	// Initialize and load store on creation
	cs.loadStore()
	return cs
}

// SetClock overrides the time source (tests). Must be called before Start.
func (cs *CronService) SetClock(c Clock) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if c != nil {
		cs.clock = c
	}
}

// SetRunner installs the agent fire path invoked by the parallel lane for each
// owned due job (W-3 runner seam).
func (cs *CronService) SetRunner(r ScheduledRunner) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.runner = r
}

// SetDefaultAgentID supplies the default agent id used to backfill owner-less
// jobs on load (W-8 migration). If empty, owner-less jobs are skipped on fire.
func (cs *CronService) SetDefaultAgentID(id string) {
	cs.mu.Lock()
	cs.defaultAgentID = id
	cs.mu.Unlock()
	cs.migrateOwners()
}

// SetMaxConcurrentRuns configures the parallel-lane capacity (FR-007). Values
// <= 0 fall back to the default. Must be called before Start.
func (cs *CronService) SetMaxConcurrentRuns(n int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if n <= 0 {
		n = defaultMaxConcurrentRuns
	}
	cs.maxConcurrentRuns = n
	cs.laneSem = make(chan struct{}, n)
}

// SetRetryBackoff configures the FR-010 transient-error backoff schedule (ms
// offsets keyed by RetryAttempt). A nil/empty slice falls back to the default.
// Must be called before Start.
func (cs *CronService) SetRetryBackoff(backoffMs []int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(backoffMs) == 0 {
		cs.retryBackoffMs = append([]int64(nil), defaultRetryBackoffMs...)
		return
	}
	cs.retryBackoffMs = append([]int64(nil), backoffMs...)
}

// SetStopDrainTimeout bounds how long Stop() waits on in-flight runs before it
// proceeds (the lane ctx is already canceled). A value <= 0 means "wait
// unbounded". Must be called before Stop.
func (cs *CronService) SetStopDrainTimeout(d time.Duration) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.stopDrainTimeout = d
}

func (cs *CronService) now() time.Time {
	cs.mu.RLock()
	c := cs.clock
	cs.mu.RUnlock()
	if c == nil {
		return time.Now()
	}
	return c.Now()
}

// clockNowUnsafeMS returns the injected clock's time in ms. Caller must hold the
// lock (it reads cs.clock without re-locking, for use inside locked sections).
func (cs *CronService) clockNowUnsafeMS() int64 {
	if cs.clock == nil {
		return time.Now().UnixMilli()
	}
	return cs.clock.Now().UnixMilli()
}

// initRunStateUnsafe loads the store, migrates owners, recomputes next-run
// times, and initializes the stop/wake channels + lane context. The caller must
// hold cs.mu. It returns started=true only on a stopped→running transition (so
// Start knows to launch the runLoop exactly once).
func (cs *CronService) initRunStateUnsafe() (started bool, err error) {
	if cs.running {
		return false, nil
	}

	if err := cs.loadStore(); err != nil {
		return false, fmt.Errorf("failed to load store: %w", err)
	}
	cs.migrateOwnersUnsafe()

	cs.recomputeNextRuns()
	if err := cs.saveStoreUnsafe(); err != nil {
		return false, fmt.Errorf("failed to save store: %w", err)
	}

	cs.stopChan = make(chan struct{})
	if cs.wakeChan == nil {
		cs.wakeChan = make(chan struct{})
	}
	// Fresh lane context for this run window (a prior Stop canceled the old one).
	if cs.laneCtx == nil || cs.laneCtx.Err() != nil {
		cs.laneCtx, cs.laneCancel = context.WithCancel(context.Background())
	}
	cs.running = true
	return true, nil
}

func (cs *CronService) Start() error {
	cs.mu.Lock()
	started, err := cs.initRunStateUnsafe()
	stop := cs.stopChan
	cs.mu.Unlock()
	if err != nil {
		return err
	}
	if started {
		go cs.runLoop(stop)
	}
	return nil
}

// startNoLoop enables RunDueJobs/dispatch (running=true + lane initialized)
// WITHOUT launching the real-time runLoop timer. Test-only: deterministic tests
// drive RunDueJobs(now) directly with the injected fake Clock, whereas runLoop
// uses real time.Now() and would otherwise busy-spin and race the fake-clock
// dispatch (the spec intends tests to bypass the loop, W-5).
func (cs *CronService) startNoLoop() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	_, err := cs.initRunStateUnsafe()
	return err
}

// Stop cancels the parallel lane context and blocks (bounded) on in-flight runs
// (W-3 blocking drain). The runLoop is signaled via stopChan; lane goroutines
// observe the canceled context and are then waited on.
func (cs *CronService) Stop() {
	cs.mu.Lock()
	if !cs.running {
		cs.mu.Unlock()
		return
	}
	cs.running = false
	if cs.stopChan != nil {
		close(cs.stopChan)
		cs.stopChan = nil
	}
	cancel := cs.laneCancel
	drainTimeout := cs.stopDrainTimeout
	cs.mu.Unlock()

	// Cancel in-flight runs, then drain the lane outside the lock so running
	// goroutines can acquire it to record their outcome.
	if cancel != nil {
		cancel()
	}

	// Bounded drain: wait for in-flight lane runs to finish, but give up after
	// stopDrainTimeout so a stuck/uncancellable run cannot hang shutdown forever.
	// The lane ctx is already canceled above, so proceeding is safe — any
	// still-running goroutine is tearing down.
	if drainTimeout <= 0 {
		cs.laneWG.Wait()
		return
	}
	done := make(chan struct{})
	go func() {
		cs.laneWG.Wait()
		close(done)
	}()
	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		log.Printf(
			"[cron] Stop(): lane drain timed out after %s — proceeding with in-flight runs still tearing down",
			drainTimeout,
		)
	}
}

func (cs *CronService) runLoop(stopChan chan struct{}) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		// every loop, recalculate the next wake time
		cs.mu.RLock()
		nextWake := cs.getNextWakeMS()
		cs.mu.RUnlock()

		var delay time.Duration
		now := time.Now().UnixMilli()

		if nextWake == nil {
			// no jobs, sleep for a long time (or until a new job is added)
			delay = time.Hour
		} else {
			diff := *nextWake - now
			if diff <= 0 {
				delay = 0
			} else {
				delay = time.Duration(diff) * time.Millisecond
			}
		}

		timer.Reset(delay)

		select {
		case <-stopChan:
			return
		case <-cs.wakeChan: // wake on new job or update
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case <-timer.C:
			cs.checkJobs()
		}
	}
}

func (cs *CronService) checkJobs() {
	cs.RunDueJobs(cs.now())
}

// RunDueJobs collects every job due at `now`, clears their next-run to avoid
// duplicate execution, then dispatches each onto the semaphore-bounded parallel
// lane (FR-007). It is exported so tests can fire deterministically with zero
// wall-clock sleeps (W-5): pass an explicit `now` and the injected clock drives
// all state math. Dispatch is asynchronous; callers that need to wait for the
// runs to finish (tests) use WaitForLane.
func (cs *CronService) RunDueJobs(now time.Time) {
	cs.mu.Lock()

	if !cs.running {
		cs.mu.Unlock()
		return
	}

	nowMS := now.UnixMilli()
	var dueJobIDs []string

	// Collect jobs that are due (we need to copy them to execute outside lock)
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.Enabled && job.State.NextRunAtMS != nil && *job.State.NextRunAtMS <= nowMS {
			dueJobIDs = append(dueJobIDs, job.ID)
		}
	}

	// Reset next run for due jobs before unlocking to avoid duplicate execution.
	dueMap := make(map[string]bool, len(dueJobIDs))
	for _, jobID := range dueJobIDs {
		dueMap[jobID] = true
	}
	for i := range cs.store.Jobs {
		if dueMap[cs.store.Jobs[i].ID] {
			cs.store.Jobs[i].State.NextRunAtMS = nil
		}
	}

	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to save store: %v", err)
	}

	cs.mu.Unlock()

	// Dispatch each due job onto the parallel lane.
	for _, jobID := range dueJobIDs {
		cs.dispatch(jobID)
	}
}

// WaitForLane blocks until all in-flight lane runs complete. Tests use this to
// observe deterministic completion without sleeping.
func (cs *CronService) WaitForLane() {
	cs.laneWG.Wait()
}

// dispatch enqueues a due job onto the bounded lane. Excess runs queue on the
// semaphore (the worker goroutine blocks on acquisition). The overlap guard
// (FR-008) is checked inside the goroutine once a slot is held so the queue
// position is preserved for non-overlapping jobs.
func (cs *CronService) dispatch(jobID string) {
	cs.mu.RLock()
	laneCtx := cs.laneCtx
	sem := cs.laneSem
	cs.mu.RUnlock()
	if laneCtx == nil {
		laneCtx = context.Background()
	}

	cs.laneWG.Add(1)
	go func() {
		defer cs.laneWG.Done()
		// Outer recover (defense-in-depth): executeJobByID has its own recover
		// that converts a run panic into a recorded error, but a panic in the
		// slot-acquisition/bookkeeping around it must never crash the gateway.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cron] dispatch goroutine recovered from panic for job %s: %v", jobID, r)
			}
		}()

		// Acquire a lane slot (queues when the cap is reached, FR-007).
		select {
		case sem <- struct{}{}:
		case <-laneCtx.Done():
			return
		}
		defer func() { <-sem }()

		cs.executeJobByID(laneCtx, jobID)
	}()
}

func (cs *CronService) executeJobByID(ctx context.Context, jobID string) {
	startTime := cs.now().UnixMilli()

	cs.mu.Lock()
	// Snapshot the runner under the lock we already hold (SetRunner is a one-time
	// boot wiring, but this keeps the read race-free and avoids a second RLock).
	runner := cs.runner
	var callbackJob *CronJob
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID {
			// Overlap guard (FR-008): skip if the previous run is still going.
			// A skipped recurring job must still recompute its next fire so it
			// is not lost (RunDueJobs cleared NextRunAtMS before dispatch).
			if job.State.Running {
				cs.rescheduleSkippedUnsafe(job)
				onSkip := cs.onSkip
				cs.mu.Unlock()
				log.Printf("[cron] ⤳ job '%s' (id: %s) skipped — previous run still in progress", job.Name, jobID)
				if onSkip != nil {
					onSkip(jobID, "overlap")
				}
				return
			}
			// Owner-missing skip (W-8): a job with no resolvable owner must not
			// fire (no default-agent fallback). Only enforced once a runner is
			// wired. Recompute the next fire on skip so a recurring job survives.
			if runner != nil && job.AgentID == "" {
				cs.rescheduleSkippedUnsafe(job)
				onSkip := cs.onSkip
				cs.mu.Unlock()
				log.Printf("[cron] ⚠ job '%s' (id: %s) skipped — no owning agent", job.Name, jobID)
				if onSkip != nil {
					onSkip(jobID, "owner-missing")
				}
				return
			}
			job.State.Running = true
			jobCopy := *job
			callbackJob = &jobCopy
			break
		}
	}
	if callbackJob != nil {
		if err := cs.saveStoreUnsafe(); err != nil {
			log.Printf("[cron] failed to save store: %v", err)
		}
	}
	cs.mu.Unlock()

	if callbackJob == nil {
		log.Printf("[cron] job %s not found, skipping", jobID)
		return
	}

	// Log job execution start
	log.Printf("[cron] ▶ executing job '%s' (id: %s, schedule: %s, channel: %s)",
		callbackJob.Name, jobID, callbackJob.Schedule.Kind, callbackJob.Payload.Channel)

	// Apply a per-run deadline (FR-003) derived from the schedule's timeout.
	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	var cancel context.CancelFunc
	if callbackJob.TimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, time.Duration(callbackJob.TimeoutSeconds)*time.Second)
	}

	// invokeRun calls the runner with a recover so a panic in a scheduled run
	// becomes a recorded error rather than crashing the gateway (BLOCKER 2). The
	// returned sessionID may be the stable id the runner chose (continue/main) so
	// it can be persisted back onto the real job.
	sessionID, err := invokeRun(runCtx, runner, callbackJob)
	if cancel != nil {
		cancel()
	}

	execDuration := cs.now().UnixMilli() - startTime

	// Now acquire lock to update state. The Running flag is cleared in a
	// deferred reset that ALWAYS runs, so even an early return (job vanished)
	// or a panic in the state-update code below leaves no stuck Running=true.
	cs.mu.Lock()
	defer cs.mu.Unlock()
	defer cs.clearRunningUnsafe(jobID)

	var job *CronJob
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			job = &cs.store.Jobs[i]
			break
		}
	}
	if job == nil {
		log.Printf("[cron] job %s disappeared before state update", jobID)
		return
	}

	job.State.Running = false
	job.State.LastRunAtMS = &startTime
	job.UpdatedAtMS = cs.clockNowUnsafeMS()

	// Persist the runner-chosen session id back onto the real job for stable
	// modes (continue/main) so the same session is reused next run (BLOCKER 5,
	// FR-004). The runner mints/looks up the id and returns it; without writing
	// it back here every continue run would mint a fresh session.
	if sessionID != "" && (job.SessionMode == SessionModeContinue || job.SessionMode == SessionModeMain) {
		job.SessionID = sessionID
	}

	// Classify the run outcome. A deadline-exceeded run is "timeout", not
	// "error", so the contract `timeout` enum is live (FR-003). A transient
	// provider error drives backoff-retry scheduling (FR-010).
	status := "ok"
	errText := ""
	transient := false
	switch {
	case err == nil:
		job.State.LastStatus = "ok"
		job.State.LastError = ""
		job.State.ConsecutiveFailures = 0
		job.State.RetryAttempt = 0
	case errors.Is(err, context.DeadlineExceeded):
		status = "timeout"
		errText = err.Error()
		job.State.LastStatus = "timeout"
		job.State.LastError = errText
		job.State.ConsecutiveFailures++
		log.Printf("[cron] ⏱ job '%s' timed out after %dms: %v", job.Name, execDuration, err)
	default:
		status = "error"
		errText = err.Error()
		transient = IsTransient(err)
		job.State.LastStatus = "error"
		job.State.LastError = errText
		job.State.ConsecutiveFailures++
		log.Printf("[cron] ✗ job '%s' failed after %dms: %v", job.Name, execDuration, err)
	}

	// Append to inline run history, capped at runHistoryCap.
	if sessionID == "" {
		sessionID = callbackJob.SessionID
	}
	rec := CronRunRecord{
		RanAtMs:    startTime,
		Status:     status,
		Error:      errText,
		SessionID:  sessionID,
		DurationMs: execDuration,
	}
	job.State.History = append(job.State.History, rec)
	if len(job.State.History) > runHistoryCap {
		job.State.History = job.State.History[len(job.State.History)-runHistoryCap:]
	}

	// Compute next run time.
	nextRunStr := cs.scheduleNextRunUnsafe(job, transient)

	if err == nil {
		log.Printf("[cron] ✓ job '%s' completed in %dms, next run: %s", job.Name, execDuration, nextRunStr)
	}

	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to save store: %v", err)
	}
}

// invokeRun calls the configured runner for the job, converting any panic into
// an error so a misbehaving scheduled run can never crash the gateway (BLOCKER
// 2). The (sessionID,error) outcome is recorded by the caller. When no runner is
// configured the job records a no-op (empty sessionID, nil error).
func invokeRun(
	ctx context.Context,
	runner ScheduledRunner,
	job *CronJob,
) (sessionID string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("scheduled run panic: %v", r)
			log.Printf("[cron] ✗ job '%s' (id: %s) panicked: %v", job.Name, job.ID, r)
		}
	}()
	if runner != nil {
		return runner.RunScheduled(ctx, job)
	}
	return "", nil
}

// clearRunningUnsafe forces Running=false for the job and persists, used as a
// deferred always-runs reset so a panic or early return cannot strand the
// overlap guard (BLOCKER 2). Caller must hold cs.mu.
func (cs *CronService) clearRunningUnsafe(jobID string) {
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			if cs.store.Jobs[i].State.Running {
				cs.store.Jobs[i].State.Running = false
				if err := cs.saveStoreUnsafe(); err != nil {
					log.Printf("[cron] failed to persist Running reset: %v", err)
				}
			}
			return
		}
	}
}

// rescheduleSkippedUnsafe recomputes a recurring job's next fire after a skip
// (overlap or owner-missing) so the cleared NextRunAtMS from RunDueJobs is
// restored and the job fires next interval (BLOCKER 1). One-time ("at") jobs are
// left as-is — they do not recur. Caller must hold cs.mu.
func (cs *CronService) rescheduleSkippedUnsafe(job *CronJob) {
	if !job.Enabled || job.Schedule.Kind == "at" {
		return
	}
	job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, cs.clockNowUnsafeMS())
	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to persist skip-reschedule: %v", err)
	}
}

// scheduleNextRunUnsafe computes and assigns the job's next fire after a run,
// returning a human-readable description for logging. For a transient failure it
// schedules a backoff retry (FR-010); otherwise it resumes normal cadence.
// Caller must hold cs.mu.
func (cs *CronService) scheduleNextRunUnsafe(job *CronJob, transient bool) string {
	if job.Schedule.Kind == "at" {
		if job.DeleteAfterRun {
			cs.removeJobUnsafe(job.ID)
			return "(deleted)"
		}
		job.Enabled = false
		job.State.NextRunAtMS = nil
		return "(disabled)"
	}

	// FR-010: a transient provider error retries on the backoff schedule. The
	// Nth retry (RetryAttempt) fires at now + backoff[N]. After the schedule is
	// exhausted, fall through to normal cadence and reset RetryAttempt.
	if transient && len(cs.retryBackoffMs) > 0 && job.State.RetryAttempt < len(cs.retryBackoffMs) {
		offset := cs.retryBackoffMs[job.State.RetryAttempt]
		next := cs.clockNowUnsafeMS() + offset
		job.State.NextRunAtMS = &next
		job.State.RetryAttempt++
		log.Printf("[cron] ↻ job '%s' transient failure — retry %d scheduled in %dms",
			job.Name, job.State.RetryAttempt, offset)
		return time.UnixMilli(next).Format("2006-01-02 15:04:05") + " (retry)"
	}

	// Normal cadence (also the terminal-failure path after retries exhausted).
	job.State.RetryAttempt = 0
	nextRun := cs.computeNextRun(&job.Schedule, cs.clockNowUnsafeMS())
	job.State.NextRunAtMS = nextRun
	if nextRun != nil {
		return time.UnixMilli(*nextRun).Format("2006-01-02 15:04:05")
	}
	return "(none)"
}

func (cs *CronService) computeNextRun(schedule *CronSchedule, nowMS int64) *int64 {
	switch schedule.Kind {
	case "at":
		if schedule.AtMS != nil && *schedule.AtMS > nowMS {
			return schedule.AtMS
		}
		return nil
	case "every":
		if schedule.EveryMS == nil || *schedule.EveryMS <= 0 {
			return nil
		}
		next := nowMS + *schedule.EveryMS
		return &next
	case "cron":
		if schedule.Expr == "" {
			return nil
		}

		// Use gronx to calculate next run time
		now := time.UnixMilli(nowMS)
		nextTime, err := gronx.NextTickAfter(schedule.Expr, now, false)
		if err != nil {
			log.Printf("[cron] failed to compute next run for expr '%s': %v", schedule.Expr, err)
			return nil
		}

		nextMS := nextTime.UnixMilli()
		return &nextMS
	default:
		log.Printf("[cron] unknown schedule kind '%s'", schedule.Kind)
		return nil
	}
}

// wake up the loop to re-evaluate next wake time immediately (e.g. after add/update/remove jobs)
func (cs *CronService) notify() {
	select {
	case cs.wakeChan <- struct{}{}:
	default:
		// if the channel is full, it means the loop will wake up soon anyway, so we can skip sending
	}
}

func (cs *CronService) recomputeNextRuns() {
	now := cs.clockNowUnsafeMS()
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.Enabled {
			job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, now)
		}
	}
}

// migrateOwners backfills owner-less jobs with the default agent id, if one was
// supplied (W-8). Idempotent: only fills empty owners; persists once when it
// changes anything. Safe to call with the lock not held.
func (cs *CronService) migrateOwners() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.migrateOwnersUnsafe()
}

// migrateOwnersUnsafe is migrateOwners with the lock already held.
func (cs *CronService) migrateOwnersUnsafe() {
	if cs.defaultAgentID == "" || cs.store == nil {
		return
	}
	changed := false
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].AgentID == "" {
			cs.store.Jobs[i].AgentID = cs.defaultAgentID
			changed = true
		}
	}
	if changed {
		log.Printf("[cron] migration: backfilled owner-less jobs with default agent %q", cs.defaultAgentID)
		if err := cs.saveStoreUnsafe(); err != nil {
			log.Printf("[cron] failed to persist owner migration: %v", err)
		}
	}
}

func (cs *CronService) getNextWakeMS() *int64 {
	var nextWake *int64
	for _, job := range cs.store.Jobs {
		if job.Enabled && job.State.NextRunAtMS != nil {
			if nextWake == nil || *job.State.NextRunAtMS < *nextWake {
				nextWake = job.State.NextRunAtMS
			}
		}
	}
	return nextWake
}

func (cs *CronService) Load() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.loadStore()
}

func (cs *CronService) loadStore() error {
	cs.store = &CronStore{
		Version: 1,
		Jobs:    []CronJob{},
	}

	data, err := os.ReadFile(cs.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, cs.store)
}

func (cs *CronService) saveStoreUnsafe() error {
	data, err := json.MarshalIndent(cs.store, "", "  ")
	if err != nil {
		return err
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	return fileutil.WriteFileAtomic(cs.storePath, data, 0o600)
}

func (cs *CronService) AddJob(
	name string,
	schedule CronSchedule,
	message string,
	deliver bool,
	channel, to string,
) (*CronJob, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	now := cs.clockNowUnsafeMS()

	// One-time tasks (at) should be deleted after execution
	deleteAfterRun := (schedule.Kind == "at")

	job := CronJob{
		ID:       generateID(),
		Name:     name,
		Enabled:  true,
		Schedule: schedule,
		Payload: CronPayload{
			Kind:    "agent_turn",
			Message: message,
			Deliver: deliver,
			Channel: channel,
			To:      to,
		},
		State: CronJobState{
			NextRunAtMS: cs.computeNextRun(&schedule, now),
		},
		CreatedAtMS:    now,
		UpdatedAtMS:    now,
		DeleteAfterRun: deleteAfterRun,
	}

	cs.store.Jobs = append(cs.store.Jobs, job)
	if err := cs.saveStoreUnsafe(); err != nil {
		return nil, err
	}

	cs.notify()

	return &job, nil
}

// JobSpec is the full creation spec for AddJobFull. Every #264 field (owner,
// session mode, timeout, created-by, delivery) is set in one atomic write, so
// the REST handler avoids the two-write create window (AddJob + UpdateJob) that
// briefly persists a job with an empty owner.
type JobSpec struct {
	Name           string
	Schedule       CronSchedule
	Message        string
	Command        string
	Deliver        bool
	Channel        string
	To             string
	AgentID        string
	SessionMode    SessionMode
	TimeoutSeconds int
	CreatedBy      string
	Enabled        *bool // nil → default true
}

// AddJobFull persists a complete job (including owner/mode/timeout) in a single
// atomic write. It is the preferred constructor for the #264 REST create path;
// AddJob is kept for back-compat with the legacy cron tool. Returns a copy of
// the stored job.
func (cs *CronService) AddJobFull(spec JobSpec) (*CronJob, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	now := cs.clockNowUnsafeMS()

	mode := spec.SessionMode
	if mode == "" {
		mode = SessionModeIsolated
	} else if !mode.Valid() {
		// Self-reject an invalid non-empty mode rather than persisting a job
		// the runner cannot interpret (FR-004). Callers that pass "" get the
		// isolated default above.
		return nil, fmt.Errorf("invalid session mode %q", mode)
	}
	enabled := true
	if spec.Enabled != nil {
		enabled = *spec.Enabled
	}

	job := CronJob{
		ID:       generateID(),
		Name:     spec.Name,
		Enabled:  enabled,
		Schedule: spec.Schedule,
		Payload: CronPayload{
			Kind:    "agent_turn",
			Message: spec.Message,
			Command: spec.Command,
			Deliver: spec.Deliver,
			Channel: spec.Channel,
			To:      spec.To,
		},
		State: CronJobState{
			NextRunAtMS: cs.computeNextRun(&spec.Schedule, now),
		},
		CreatedAtMS:    now,
		UpdatedAtMS:    now,
		DeleteAfterRun: spec.Schedule.Kind == "at",
		AgentID:        spec.AgentID,
		SessionMode:    mode,
		TimeoutSeconds: spec.TimeoutSeconds,
		CreatedBy:      spec.CreatedBy,
	}
	if !enabled {
		job.State.NextRunAtMS = nil
	}

	cs.store.Jobs = append(cs.store.Jobs, job)
	if err := cs.saveStoreUnsafe(); err != nil {
		return nil, err
	}
	cs.notify()
	return &job, nil
}

// GetJob returns a copy of the job with the given id, or (zero,false) if no
// such job exists.
func (cs *CronService) GetJob(jobID string) (CronJob, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			return cs.store.Jobs[i], true
		}
	}
	return CronJob{}, false
}

// RunNow fires a single job immediately through the same bounded lane the
// scheduler uses, respecting the overlap guard (FR-008) and the concurrency cap
// (FR-007), and blocks until that run completes. It returns:
//   - status "ok"      + session id when the run succeeded,
//   - status "error"   + the error  when the run failed (non-deadline),
//   - status "skipped" + nil        when the overlap/owner guard skipped it,
//   - status "timeout" + the error  when the run hit its deadline
//     (executeJobByID classifies a context.DeadlineExceeded run record as
//     "timeout"; RunNow surfaces that status verbatim).
//
// Unlike the async lane dispatch used by RunDueJobs, RunNow is synchronous so
// the REST run-now endpoint can return the outcome to the caller. It is the
// thin primitive the spec asks for ("add RunNow ONLY IF none exists"). The
// overlap/owner-missing checks live in executeJobByID, so RunNow reuses it and
// inspects the resulting CronJobState to classify the outcome.
func (cs *CronService) RunNow(jobID string) (status string, sessionID string, err error) {
	cs.mu.RLock()
	exists := false
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			exists = true
			break
		}
	}
	laneCtx := cs.laneCtx
	sem := cs.laneSem
	cs.mu.RUnlock()
	if !exists {
		return "", "", fmt.Errorf("job not found")
	}
	if laneCtx == nil {
		laneCtx = context.Background()
	}

	// Snapshot the run-count AND the most-recent record's identity so we can
	// detect whether executeJobByID actually ran (vs. skipped by the
	// overlap/owner guard before recording a record). Length alone is
	// insufficient: executeJobByID trims History to runHistoryCap, so a job
	// with a saturated history (len==runHistoryCap) keeps the same length
	// across a genuine run. executeJobByID stamps each record's RanAtMs with
	// the run's startTime, so a changed last-record RanAtMs is the reliable
	// "a new run was appended" signal.
	beforeLen, beforeRec := cs.runStateSnapshot(jobID)

	// Acquire a lane slot (queues when the cap is reached, FR-007).
	select {
	case sem <- struct{}{}:
	case <-laneCtx.Done():
		return "skipped", "", nil
	}
	cs.laneWG.Add(1)
	func() {
		defer cs.laneWG.Done()
		defer func() { <-sem }()
		cs.executeJobByID(laneCtx, jobID)
	}()

	afterLen, afterRec := cs.runStateSnapshot(jobID)
	ran := afterLen > beforeLen || afterRec.RanAtMs != beforeRec.RanAtMs
	if !ran {
		// No new run record appended → the overlap guard (or owner-missing
		// guard) skipped the run. Detected by the most-recent record's identity
		// (RanAtMs) rather than length, so a saturated history (trimmed back to
		// runHistoryCap) does not mask a genuine run as "skipped".
		return "skipped", "", nil
	}
	switch afterRec.Status {
	case "error", "timeout":
		errOut := afterRec.Error
		if errOut == "" {
			errOut = "run failed"
		}
		return afterRec.Status, afterRec.SessionID, fmt.Errorf("%s", errOut)
	default:
		return "ok", afterRec.SessionID, nil
	}
}

// runStateSnapshot returns the current history length and most-recent run record
// for a job (used by RunNow to detect whether a run was appended).
func (cs *CronService) runStateSnapshot(jobID string) (int, CronRunRecord) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			h := cs.store.Jobs[i].State.History
			if len(h) == 0 {
				return 0, CronRunRecord{}
			}
			return len(h), h[len(h)-1]
		}
	}
	return 0, CronRunRecord{}
}

func (cs *CronService) UpdateJob(job *CronJob) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			cs.store.Jobs[i] = *job
			cs.store.Jobs[i].UpdatedAtMS = cs.clockNowUnsafeMS()

			cs.notify()

			return cs.saveStoreUnsafe()
		}
	}
	return fmt.Errorf("job not found")
}

func (cs *CronService) RemoveJob(jobID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	return cs.removeJobUnsafe(jobID)
}

func (cs *CronService) removeJobUnsafe(jobID string) bool {
	before := len(cs.store.Jobs)
	var jobs []CronJob
	for _, job := range cs.store.Jobs {
		if job.ID != jobID {
			jobs = append(jobs, job)
		}
	}
	cs.store.Jobs = jobs
	removed := len(cs.store.Jobs) < before

	if removed {
		if err := cs.saveStoreUnsafe(); err != nil {
			log.Printf("[cron] failed to save store after remove: %v", err)
		}
	}

	cs.notify()

	return removed
}

func (cs *CronService) EnableJob(jobID string, enabled bool) *CronJob {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID {
			job.Enabled = enabled
			job.UpdatedAtMS = cs.clockNowUnsafeMS()

			if enabled {
				job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, cs.clockNowUnsafeMS())
			} else {
				job.State.NextRunAtMS = nil
			}

			if err := cs.saveStoreUnsafe(); err != nil {
				log.Printf("[cron] failed to save store after enable: %v", err)
			}

			cs.notify()

			return job
		}
	}

	return nil
}

func (cs *CronService) ListJobs(includeDisabled bool) []CronJob {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if includeDisabled {
		return cs.store.Jobs
	}

	var enabled []CronJob
	for _, job := range cs.store.Jobs {
		if job.Enabled {
			enabled = append(enabled, job)
		}
	}

	return enabled
}

func (cs *CronService) Status() map[string]any {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var enabledCount int
	for _, job := range cs.store.Jobs {
		if job.Enabled {
			enabledCount++
		}
	}

	return map[string]any{
		"enabled":      cs.running,
		"jobs":         len(cs.store.Jobs),
		"nextWakeAtMS": cs.getNextWakeMS(),
	}
}

func generateID() string {
	// Use crypto/rand for better uniqueness under concurrent access
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based if crypto/rand fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
