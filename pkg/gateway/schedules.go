//go:build !cgo

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adhocore/gronx"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/notifications"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// scheduledExecutor is the subset of *agent.AgentLoop the runner needs. It is an
// interface so tests can inject a fake without a full agent loop (#264 W-1).
type scheduledExecutor interface {
	ProcessScheduled(ctx context.Context, ownerAgentID, sessionID, content, channel, chatID string) (string, error)
	GetSessionStore() *session.UnifiedStore
	GetConfig() *config.Config
	EmitNotification(p agent.NotificationPayload)
}

// agentChecker reports whether an agent id is registered and enabled. The agent
// registry satisfies this. Kept narrow so the runner can be tested with a stub.
type agentChecker interface {
	IsRegistered(agentID string) bool
}

// scheduledRunner implements cron.ScheduledRunner (#264). It wakes a fired
// schedule's OWNING agent in the session mode the schedule chose, under a
// per-run deadline, and on failure raises a notification + a channel alert to
// the owning agent's default channel. It NEVER falls back to the default agent.
type scheduledRunner struct {
	exec      scheduledExecutor
	checker   agentChecker
	msgBus    *bus.MessageBus
	notifs    *notifications.Store
	getConfig func() *config.Config
}

// newScheduledRunner builds the runner. getConfig is read at run time so the
// runner always sees the live (possibly hot-reloaded) config.
func newScheduledRunner(
	exec scheduledExecutor,
	checker agentChecker,
	msgBus *bus.MessageBus,
	notifs *notifications.Store,
	getConfig func() *config.Config,
) *scheduledRunner {
	return &scheduledRunner{
		exec:      exec,
		checker:   checker,
		msgBus:    msgBus,
		notifs:    notifs,
		getConfig: getConfig,
	}
}

// RunScheduled is the owner-aware fire path (FR-001/FR-003/FR-004/FR-013/FR-014).
func (r *scheduledRunner) RunScheduled(ctx context.Context, job *cron.CronJob) (string, error) {
	owner := job.AgentID

	// Owner pinning: a missing/disabled owner is a failure, never a default
	// fallback (FR-001). The lane records the (string,error) failure, which
	// drives the alert path below.
	if owner == "" || r.checker == nil || !r.checker.IsRegistered(owner) {
		err := fmt.Errorf("owner unavailable: agent %q is not registered or enabled", owner)
		r.onFailure(job, "", err)
		return "", err
	}

	channel := job.Payload.Channel
	chatID := job.Payload.To

	// deliver=true: send the message straight to the channel (no agent turn).
	if job.Payload.Deliver {
		if channel == "" {
			err := fmt.Errorf("deliver=true schedule has no channel configured")
			r.onFailure(job, "", err)
			return "", err
		}
		pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: job.Payload.Message,
		}); err != nil {
			r.onFailure(job, "", err)
			return "", fmt.Errorf("deliver failed: %w", err)
		}
		return "", nil
	}

	// deliver=false: wake the owning agent in the chosen session mode.
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

	reply, runErr := r.exec.ProcessScheduled(ctx2, owner, sessionID, job.Payload.Message, channel, chatID)
	_ = reply // the reply is published to the channel by the agent loop itself.
	if runErr != nil {
		r.onFailure(job, sessionID, runErr)
		return sessionID, runErr
	}
	return sessionID, nil
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
			return fresh.ID, nil
		}
		return meta.ID, nil

	case cron.SessionModeMain:
		id := "sched-main-" + owner
		meta, err := store.GetOrCreateScheduledSession(id, owner)
		if err != nil {
			return "", fmt.Errorf("main session create: %w", err)
		}
		return meta.ID, nil

	default: // isolated
		fresh, err := store.NewScheduledSession(owner)
		if err != nil {
			return "", fmt.Errorf("isolated session create: %w", err)
		}
		return fresh.ID, nil
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
			SessionID:  sessionID,
			AgentID:    job.AgentID,
		}
		var stored notifications.Notification
		var err error
		if r.notifs != nil {
			stored, err = r.notifs.Create(n)
			if err != nil {
				logger.WarnCF("gateway", "failed to persist schedule-failure notification",
					map[string]any{"schedule_id": job.ID, "recipient": recipient, "error": err.Error()})
				continue
			}
		} else {
			stored = n
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
// schedule's CreatedBy user and the owning agent's OwnerUsername. If neither is
// resolvable, returns the admin-broadcast sentinel as the sole recipient so the
// notification still reaches admins.
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
	if cfg != nil {
		if owner := findAgentConfig(cfg, job.AgentID); owner != nil {
			add(owner.OwnerUsername)
		}
	}

	if len(out) == 0 {
		// Neither resolvable → notify all admins. Persist per-admin so each gets
		// their own read state; if there are no users at all, fall back to the
		// broadcast sentinel for the live push only.
		if cfg != nil {
			for i := range cfg.Gateway.Users {
				if cfg.Gateway.Users[i].Role == config.UserRoleAdmin {
					add(cfg.Gateway.Users[i].Username)
				}
			}
		}
	}
	if len(out) == 0 {
		out = append(out, agent.NotificationAdminBroadcast)
	}
	return out
}

// publishChannelAlert sends an alert to the owning agent's default channel
// (FR-013b). The default channel is resolved from the most-specific
// channel-wildcard binding for the owner; if the schedule itself carries a
// channel, that wins (it is the run's outbound context).
func (r *scheduledRunner) publishChannelAlert(job *cron.CronJob, cfg *config.Config, body string) {
	channel := job.Payload.Channel
	chatID := job.Payload.To
	if channel == "" && cfg != nil {
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
		Deliver:        job.Payload.Deliver,
		Channel:        strPtr(job.Payload.Channel),
		ChatId:         strPtr(job.Payload.To),
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

// HandleSchedules dispatches /api/v1/schedules and /api/v1/schedules/{id}[/run|/pause].
func (a *restAPI) HandleSchedules(w http.ResponseWriter, r *http.Request) {
	if a.cronService == nil {
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
			a.handleListSchedules(w, r)
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
				a.handleUpdateSchedule(w, r, user, id)
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

func (a *restAPI) handleListSchedules(w http.ResponseWriter, _ *http.Request) {
	jobs := a.cronService.ListJobs(true)
	// Build a slice of the generated Schedule type, then round-trip the whole
	// list into gen.ScheduleList. The ScheduleList element is structurally
	// identical to gen.Schedule, so the JSON marshal/unmarshal maps cleanly
	// without any hand-written wire-format struct (hard-constraint #8).
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
	job, ok := a.cronService.GetJob(id)
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

	// Owner authz (W-6): the caller must be permitted to use the chosen owner.
	if code, msg := a.authorizeScheduleOwner(user, req.OwnerAgentId); code != 0 {
		jsonErr(w, code, msg)
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

	deliver := derefBool(req.Deliver, false)
	job, err := a.cronService.AddJob(req.Name, schedule, req.Message, deliver,
		derefStr(req.Channel), derefStr(req.ChatId))
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to create schedule")
		return
	}

	// Fill the #264 fields that AddJob doesn't take, then persist.
	job.AgentID = req.OwnerAgentId
	job.CreatedBy = user.Username
	job.TimeoutSeconds = timeout
	job.SessionMode = cron.SessionModeIsolated
	if req.SessionMode != nil {
		job.SessionMode = string(*req.SessionMode)
	}
	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}
	if err := a.cronService.UpdateJob(job); err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to persist schedule")
		return
	}
	jsonCreated(w, toSchedule(*job))
}

func (a *restAPI) handleUpdateSchedule(w http.ResponseWriter, r *http.Request, user *config.UserConfig, id string) {
	job, ok := a.cronService.GetJob(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	var req gen.ScheduleUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Re-authorize when the owner changes (PUT re-authorizes owner if changed).
	if req.OwnerAgentId != nil && *req.OwnerAgentId != job.AgentID {
		if code, msg := a.authorizeScheduleOwner(user, *req.OwnerAgentId); code != 0 {
			jsonErr(w, code, msg)
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
	if req.Deliver != nil {
		job.Payload.Deliver = *req.Deliver
	}
	if req.Channel != nil {
		job.Payload.Channel = *req.Channel
	}
	if req.ChatId != nil {
		job.Payload.To = *req.ChatId
	}
	if req.SessionMode != nil {
		job.SessionMode = string(*req.SessionMode)
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

	if err := a.cronService.UpdateJob(&job); err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to update schedule")
		return
	}
	jsonOK(w, toSchedule(job))
}

func (a *restAPI) handleDeleteSchedule(w http.ResponseWriter, id string) {
	if !a.cronService.RemoveJob(id) {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *restAPI) handleRunSchedule(w http.ResponseWriter, id string) {
	if _, ok := a.cronService.GetJob(id); !ok {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	status, sessionID, runErr := a.cronService.RunNow(id)
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
	job, ok := a.cronService.GetJob(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	updated := a.cronService.EnableJob(id, !job.Enabled)
	if updated == nil {
		jsonErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	jsonOK(w, toSchedule(*updated))
}

// authorizeScheduleOwner validates that user may own a schedule for ownerAgentID
// (W-6). Returns (0,"") on success or an (httpStatus, message) on failure.
func (a *restAPI) authorizeScheduleOwner(user *config.UserConfig, ownerAgentID string) (int, string) {
	cfg := a.agentLoop.GetConfig()
	owner := findAgentConfig(cfg, ownerAgentID)
	if owner == nil {
		return http.StatusBadRequest, "owner_agent_id is not a known agent"
	}
	if err := config.AuthorizeAgentAccess(user, owner); err != nil {
		return http.StatusForbidden, "not permitted to schedule for this agent"
	}
	return 0, ""
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

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
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
