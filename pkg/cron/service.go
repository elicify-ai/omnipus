package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// SessionMode constants for a scheduled job (FR-004).
const (
	SessionModeIsolated = "isolated"
	SessionModeContinue = "continue"
	SessionModeMain     = "main"

	// defaultMaxConcurrentRuns is the fallback parallel-lane capacity when none
	// is supplied (schedules.max_concurrent_runs default 8, FR-007).
	defaultMaxConcurrentRuns = 8

	// runHistoryCap is the number of inline run records retained per job
	// (Ambiguity #5: keep last 20; full history via linked sessions).
	runHistoryCap = 20
)

// ScheduledRunner is the Wave-2 integration seam (set via SetRunner). The lane
// calls RunScheduled for each due job that has an owner; the (string,error)
// outcome is recorded into CronJobState. The agent side is implemented in a
// later wave — this package only defines and calls the interface.
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
	// (FR-012). Used by alerting/auto-pause heuristics in later waves.
	ConsecutiveFailures int `json:"consecutiveFailures,omitempty"`
	// Running is the overlap guard (FR-008): set true around a run, false after.
	Running bool `json:"running,omitempty"`
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
	SessionMode string `json:"sessionMode,omitempty"`
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

type JobHandler func(job *CronJob) (string, error)

type CronService struct {
	storePath string
	store     *CronStore
	onJob     JobHandler
	mu        sync.RWMutex
	running   bool
	stopChan  chan struct{}
	wakeChan  chan struct{}
	gronx     *gronx.Gronx

	// clock is the injected time source (W-5). Defaults to realClock.
	clock Clock

	// runner is the Wave-2 agent seam (W-3/runner seam); nil until set.
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
}

func NewCronService(storePath string, onJob JobHandler) *CronService {
	cs := &CronService{
		storePath:         storePath,
		onJob:             onJob,
		gronx:             gronx.New(),
		wakeChan:          make(chan struct{}),
		clock:             realClock{},
		maxConcurrentRuns: defaultMaxConcurrentRuns,
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

// SetRunner sets the Wave-2 agent seam invoked by the parallel lane for each
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

func (cs *CronService) Start() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.running {
		return nil
	}

	if err := cs.loadStore(); err != nil {
		return fmt.Errorf("failed to load store: %w", err)
	}
	cs.migrateOwnersUnsafe()

	cs.recomputeNextRuns()
	if err := cs.saveStoreUnsafe(); err != nil {
		return fmt.Errorf("failed to save store: %w", err)
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
	go cs.runLoop(cs.stopChan)

	return nil
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
	cs.mu.Unlock()

	// Cancel in-flight runs, then drain the lane outside the lock so running
	// goroutines can acquire it to record their outcome.
	if cancel != nil {
		cancel()
	}
	cs.laneWG.Wait()
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
	var callbackJob *CronJob
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID {
			// Overlap guard (FR-008): skip if the previous run is still going.
			if job.State.Running {
				cs.mu.Unlock()
				log.Printf("[cron] ⤳ job '%s' (id: %s) skipped — previous run still in progress", job.Name, jobID)
				return
			}
			// Owner-missing skip (W-8): a job with no resolvable owner must not
			// fire (no default-agent fallback). Only enforced once a runner is
			// wired (Wave-2); the legacy onJob path is owner-agnostic.
			if cs.runner != nil && job.AgentID == "" {
				cs.mu.Unlock()
				log.Printf("[cron] ⚠ job '%s' (id: %s) skipped — no owning agent", job.Name, jobID)
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

	var (
		sessionID string
		err       error
	)
	cs.mu.RLock()
	runner := cs.runner
	onJob := cs.onJob
	cs.mu.RUnlock()

	switch {
	case runner != nil:
		// Wave-2 owner-aware fire path.
		sessionID, err = runner.RunScheduled(runCtx, callbackJob)
	case onJob != nil:
		// Legacy fire path (string,error). Kept so the gateway adapter keeps
		// working until Wave-2 swaps in a runner.
		sessionID, err = onJob(callbackJob)
	}
	if cancel != nil {
		cancel()
	}

	execDuration := cs.now().UnixMilli() - startTime

	// Now acquire lock to update state
	cs.mu.Lock()
	defer cs.mu.Unlock()

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

	status := "ok"
	errText := ""
	if err != nil {
		status = "error"
		errText = err.Error()
		job.State.LastStatus = "error"
		job.State.LastError = errText
		job.State.ConsecutiveFailures++
		log.Printf("[cron] ✗ job '%s' failed after %dms: %v", job.Name, execDuration, err)
	} else {
		job.State.LastStatus = "ok"
		job.State.LastError = ""
		job.State.ConsecutiveFailures = 0
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

	// Compute next run time
	var nextRunStr string
	if job.Schedule.Kind == "at" {
		if job.DeleteAfterRun {
			cs.removeJobUnsafe(job.ID)
			nextRunStr = "(deleted)"
		} else {
			job.Enabled = false
			job.State.NextRunAtMS = nil
			nextRunStr = "(disabled)"
		}
	} else {
		nextRun := cs.computeNextRun(&job.Schedule, cs.clockNowUnsafeMS())
		job.State.NextRunAtMS = nextRun
		if nextRun != nil {
			nextRunStr = time.UnixMilli(*nextRun).Format("2006-01-02 15:04:05")
		} else {
			nextRunStr = "(none)"
		}
	}

	if err == nil {
		log.Printf("[cron] ✓ job '%s' completed in %dms, next run: %s", job.Name, execDuration, nextRunStr)
	}

	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to save store: %v", err)
	}
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

func (cs *CronService) SetOnJob(handler JobHandler) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.onJob = handler
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
