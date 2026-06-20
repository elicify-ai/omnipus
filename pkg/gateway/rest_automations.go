//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"strings"
	"time"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
)

// rest_automations.go — GET /api/v1/automations (FR-12.3 automations reframe).
//
// W3-AC reframes the schedules surface as "Automations" (Trigger → Action). The
// underlying storage is unchanged (cron.CronJob); this endpoint is a computed
// display projection that pairs each schedule with human-readable trigger and
// action strings.
//
// NOTE (future-use): the SPA's AutomationsScreen currently computes trigger/action
// summaries client-side via triggerSummary (SchedulesList). This server-side
// projection is retained as a future replacement for the client-side logic
// (centralising cron humanisation + agent-name resolution). It is read-only,
// exercised by tests, and does not affect the persisted schedule contract.
// Writes still go through /api/v1/schedules.
//
// The response reuses the generated gen.Schedule for the raw fields and adds
// the two computed display strings alongside it; because the display strings
// are a derived presentational projection (not a persisted wire format), the
// wrapper is marked not-wire-format rather than added to the contract — the
// contract remains the single source of truth for the persisted schedule
// shape, and this projection can be regenerated from it at any time.

// automationItem is one row of the automations view: the raw schedule plus
// display-friendly trigger/action strings. The raw schedule reuses the
// contract-generated gen.Schedule (round-tripped from toSchedule); the display
// strings are computed here.
type automationItem struct { // not-wire-format: computed display projection over the persisted gen.Schedule — trigger/action_display are derived presentational strings, not a new storage format; the contract owns the schedule shape.
	Schedule       gen.Schedule `json:"schedule"`
	TriggerDisplay string       `json:"trigger_display"`
	ActionDisplay  string       `json:"action_display"`
	AgentName      string       `json:"agent_name,omitempty"`
	NextRunDisplay string       `json:"next_run_display,omitempty"`
}

// automationsResponse is the body of GET /api/v1/automations.
type automationsResponse struct { // not-wire-format: computed automations view wrapping the contract-generated gen.Schedule list with display strings; not a persisted format, derived from the contract-owned schedule shape.
	Automations []automationItem `json:"automations"`
}

// HandleAutomations handles GET /api/v1/automations — the trigger→action
// projection over schedules for the Automations UI (W3-AC). Read-only.
func (a *restAPI) HandleAutomations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.cronSvc() == nil {
		jsonErr(w, http.StatusServiceUnavailable, "schedules service unavailable")
		return
	}
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "invalid token")
		return
	}

	jobs := a.cronSvc().ListJobs(true)
	items := make([]automationItem, 0, len(jobs))
	for _, job := range jobs {
		// B3: a non-admin only sees schedules whose owning agent they may
		// access; admins see all (same filter as handleListSchedules).
		if code, _ := a.authorizeScheduleAccess(user, job); code != 0 {
			continue
		}
		items = append(items, a.buildAutomationItem(job))
	}
	jsonOK(w, automationsResponse{Automations: items})
}

// buildAutomationItem computes the display projection for one cron job.
func (a *restAPI) buildAutomationItem(job cron.CronJob) automationItem {
	sched := toSchedule(job)
	item := automationItem{Schedule: sched}

	// Agent display name (falls back to the id).
	if reg := a.agentLoop.GetRegistry(); reg != nil {
		if name, ok := reg.GetAgentName(job.AgentID); ok && name != "" {
			item.AgentName = name
		}
	}
	if item.AgentName == "" {
		item.AgentName = job.AgentID
	}

	item.TriggerDisplay = triggerDisplay(job.Schedule)
	item.ActionDisplay = a.actionDisplay(job, item.AgentName)
	item.NextRunDisplay = nextRunDisplay(job.State.NextRunAtMS)
	return item
}

// triggerDisplay humanises a cron.CronSchedule into a display string.
func triggerDisplay(s cron.CronSchedule) string {
	switch s.Kind {
	case "at":
		if s.AtMS == nil {
			return "Once (time not set)"
		}
		return "Once at " + formatTime(*s.AtMS)
	case "every":
		if s.EveryMS == nil || *s.EveryMS <= 0 {
			return "Every (interval not set)"
		}
		return "Every " + humanizeDuration(*s.EveryMS)
	case "cron":
		return humanizeCron(s.Expr, s.TZ)
	default:
		return "Unknown trigger"
	}
}

// actionDisplay humanises what a fired schedule does. deliver=true sends the
// message straight to a channel; deliver=false runs the owning agent.
func (a *restAPI) actionDisplay(job cron.CronJob, agentName string) string {
	if job.Payload.Deliver {
		target := strings.TrimSpace(job.Payload.Channel)
		if target == "" {
			target = "channel"
		}
		return "Send to " + target
	}
	return "Run agent: " + agentName
}

// nextRunDisplay renders the next fire time as a relative + absolute string.
func nextRunDisplay(atMS *int64) string {
	if atMS == nil {
		return ""
	}
	return formatTime(*atMS)
}

// formatTime renders a unix-ms instant as a localised absolute string.
func formatTime(ms int64) string {
	t := time.UnixMilli(ms)
	return t.Format("Mon, 02 Jan 2006 15:04 MST")
}

// humanizeDuration renders a millisecond interval as a compact English phrase.
func humanizeDuration(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return roundDur(d, time.Second, "s")
	case d < time.Hour:
		return roundDur(d, time.Minute, "m")
	case d < 24*time.Hour:
		return roundDur(d, time.Hour, "h")
	default:
		return roundDur(d, 24*time.Hour, "d")
	}
}

// roundDur rounds d to the nearest unit and returns "<n><suffix>".
func roundDur(d, unit time.Duration, suffix string) string {
	n := int64((d + unit/2) / unit)
	if n < 1 {
		n = 1
	}
	// Pluralise whole units only for clarity (e.g. "5m", "1h").
	return itoaInt64(n) + suffix
}

// itoaInt64 avoids strconv to keep the helper dependency-free.
func itoaInt64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// humanizeCron produces a readable summary of a cron expression. It covers the
// common automation cases (interval, daily, weekly) and falls back to the raw
// expression for anything it cannot confidently phrase, so the user is never
// misled by an over-fitted translation.
func humanizeCron(expr, tz string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "On a schedule"
	}
	fields := strings.Fields(expr)
	// Standard 5-field cron: minute hour day month dayofweek. Some engines use
	// a 6-field form with a leading second field; handle both by reading the
	// trailing 5 fields when 6 are present.
	if len(fields) == 6 {
		fields = fields[1:]
	}
	if len(fields) != 5 {
		return "Cron: " + expr
	}
	minute, hour, dom, _, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	// "Every N minutes/hours" patterns.
	if hour == "*" && dom == "*" && dow == "*" {
		if minute == "*" {
			return "Every minute"
		}
		if strings.HasPrefix(minute, "*/") {
			return "Every " + minute[2:] + " minutes"
		}
	}
	if minute == "0" && dom == "*" && dow == "*" && strings.HasPrefix(hour, "*/") {
		return "Every " + hour[2:] + " hours"
	}

	// Weekly: a named day-of-week at a fixed time.
	if dayName, ok := dayOfWeekName(dow); ok && dom == "*" {
		t, ok := timeOfDay(minute, hour)
		if ok {
			return dayName + " at " + t
		}
		return dayName
	}

	// Daily: every day at a fixed time.
	if dow == "*" && dom == "*" && minute != "*" && hour != "*" {
		if t, ok := timeOfDay(minute, hour); ok {
			return "Daily at " + t
		}
	}

	// Monthly: a specific day-of-month at a fixed time.
	if dom != "*" && dow == "*" {
		if t, ok := timeOfDay(minute, hour); ok {
			return "Monthly on day " + dom + " at " + t
		}
		return "Monthly on day " + dom
	}

	out := "Cron: " + expr
	if tz != "" {
		out += " (" + tz + ")"
	}
	return out
}

// dayOfWeekName maps a cron day-of-week field to a human day name, returning ok
// only for the single-day cases this projection can phrase confidently.
func dayOfWeekName(dow string) (string, bool) {
	switch dow {
	case "0", "7":
		return "Sunday", true
	case "1":
		return "Monday", true
	case "2":
		return "Tuesday", true
	case "3":
		return "Wednesday", true
	case "4":
		return "Thursday", true
	case "5":
		return "Friday", true
	case "6":
		return "Saturday", true
	}
	return "", false
}

// timeOfDay renders a cron minute/hour pair as "HH:MM" (24h). ok is false when
// either field is a wildcard or step (this projection phrases only fixed times).
func timeOfDay(minute, hour string) (string, bool) {
	if minute == "*" || hour == "*" || strings.ContainsAny(minute, "*/-,") || strings.ContainsAny(hour, "*/-,") {
		return "", false
	}
	m := pad2(minute)
	h := pad2(hour)
	return h + ":" + m, true
}

// pad2 left-pads a numeric string to 2 characters with zeros.
func pad2(s string) string {
	if len(s) >= 2 {
		return s
	}
	return "0" + s
}
