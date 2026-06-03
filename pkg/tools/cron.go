package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/constants"
	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

// CronTool provides scheduling capabilities for the agent
type CronTool struct {
	BaseTool
	cronService  *cron.CronService
	allowCommand bool
	execEnabled  bool
}

// NewCronTool creates a new CronTool. The owner-aware autonomous fire path lives
// in the gateway's scheduledRunner (wired via CronService.SetRunner); the tool
// itself only creates/lists/edits schedules, so it carries no executor or exec
// tool of its own. The allow_command / exec.enabled flags still gate whether a
// command-bearing schedule may be created.
func NewCronTool(
	cronService *cron.CronService, config *config.Config,
) (*CronTool, error) {
	allowCommand := false
	execEnabled := false
	if config != nil {
		allowCommand = config.Tools.Cron.AllowCommand
		execEnabled = config.Tools.Exec.Enabled
	}

	return &CronTool{
		cronService:  cronService,
		allowCommand: allowCommand,
		execEnabled:  execEnabled,
	}, nil
}

// Name returns the tool name
func (t *CronTool) Name() string {
	return "cron"
}

// Description returns the tool description
func (t *CronTool) Description() string {
	return "Schedule reminders, tasks, or system commands. IMPORTANT: When user asks to be reminded or scheduled, you MUST call this tool. Use 'at_seconds' for one-time reminders (e.g., 'remind me in 10 minutes' → at_seconds=600). Use 'every_seconds' ONLY for recurring tasks (e.g., 'every 2 hours' → every_seconds=7200). Use 'cron_expr' for complex recurring schedules. Use 'command' to execute shell commands directly."
}

// Scope returns the tool's privilege level.
func (t *CronTool) Scope() ToolScope { return ScopeCore }

// Parameters returns the tool parameters schema
func (t *CronTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "list", "remove", "enable", "disable"},
				"description": "Action to perform. Use 'add' when user wants to schedule a reminder or task.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "The reminder/task message to display when triggered. If 'command' is used, this describes what the command does.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Optional: Shell command to execute directly (e.g., 'df -h'). If set, the agent will run this command and report output instead of just showing the message. 'deliver' will be forced to false for commands.",
			},
			"command_confirm": map[string]any{
				"type":        "boolean",
				"description": "Optional explicit confirmation flag for scheduling a shell command. Command execution must also be enabled via tools.cron.allow_command.",
			},
			"at_seconds": map[string]any{
				"type":        "integer",
				"description": "One-time reminder: seconds from now when to trigger (e.g., 600 for 10 minutes later). Use this for one-time reminders like 'remind me in 10 minutes'.",
			},
			"every_seconds": map[string]any{
				"type":        "integer",
				"description": "Recurring interval in seconds (e.g., 3600 for every hour). Use this ONLY for recurring tasks like 'every 2 hours' or 'daily reminder'.",
			},
			"cron_expr": map[string]any{
				"type":        "string",
				"description": "Cron expression for complex recurring schedules (e.g., '0 9 * * *' for daily at 9am). Use this for complex recurring schedules.",
			},
			"job_id": map[string]any{
				"type":        "string",
				"description": "Job ID (for remove/enable/disable)",
			},
			"deliver": map[string]any{
				"type":        "boolean",
				"description": "If true, send message directly to channel. If false, let agent process message (for complex tasks). Default: false",
			},
			"session_mode": map[string]any{
				"type": "string",
				"enum": []string{"isolated", "continue", "main"},
				"description": "Session mode for the scheduled run. 'isolated' (default): a fresh session each run. " +
					"'continue': a persistent session that builds on history across runs. 'main': inject into the owning agent's main session.",
			},
			"owner": map[string]any{
				"type":        "string",
				"description": "Optional agent id that owns and runs this schedule. Defaults to the calling agent.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Optional per-schedule run deadline in seconds. 0 (default) uses the global schedules timeout.",
			},
		},
		"required": []string{"action"},
	}
}

// Execute runs the tool with the given arguments
func (t *CronTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, ok := args["action"].(string)
	if !ok {
		return ErrorResult("action is required")
	}

	switch action {
	case "add":
		return t.addJob(ctx, args)
	case "list":
		return t.listJobs()
	case "remove":
		return t.removeJob(args)
	case "enable":
		return t.enableJob(args, true)
	case "disable":
		return t.enableJob(args, false)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

func (t *CronTool) addJob(ctx context.Context, args map[string]any) *ToolResult {
	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)

	if channel == "" || chatID == "" {
		return ErrorResult("no session context (channel/chat_id not set). Use this tool in an active conversation.")
	}

	message, ok := args["message"].(string)
	if !ok || message == "" {
		return ErrorResult("message is required for add")
	}

	var schedule cron.CronSchedule

	// Check for at_seconds (one-time), every_seconds (recurring), or cron_expr
	atSeconds, hasAt := args["at_seconds"].(float64)
	everySeconds, hasEvery := args["every_seconds"].(float64)
	cronExpr, hasCron := args["cron_expr"].(string)

	// Fix: type assertions return true for zero values, need additional validity checks
	// This prevents LLMs that fill unused optional parameters with defaults (0) from triggering wrong type
	hasAt = hasAt && atSeconds > 0
	hasEvery = hasEvery && everySeconds > 0
	hasCron = hasCron && cronExpr != ""

	// Priority: at_seconds > every_seconds > cron_expr
	if hasAt {
		atMS := time.Now().UnixMilli() + int64(atSeconds)*1000
		schedule = cron.CronSchedule{
			Kind: "at",
			AtMS: &atMS,
		}
	} else if hasEvery {
		everyMS := int64(everySeconds) * 1000
		schedule = cron.CronSchedule{
			Kind:    "every",
			EveryMS: &everyMS,
		}
	} else if hasCron {
		schedule = cron.CronSchedule{
			Kind: "cron",
			Expr: cronExpr,
		}
	} else {
		return ErrorResult("one of at_seconds, every_seconds, or cron_expr is required")
	}

	// Read deliver parameter, default to false so scheduled tasks execute through the agent
	deliver := false
	if d, ok := args["deliver"].(bool); ok {
		deliver = d
	}

	// GHSA-pv8c-p6jf-3fpp: command scheduling requires internal channel. When
	// allow_command is disabled, explicit confirmation is required as an override.
	// Non-command reminders remain open to all channels.
	command, _ := args["command"].(string)
	commandConfirm, _ := args["command_confirm"].(bool)
	if command != "" {
		if !t.execEnabled {
			return ErrorResult("command execution is disabled")
		}
		if !constants.IsInternalChannel(channel) {
			return ErrorResult("scheduling command execution is restricted to internal channels")
		}
		if !t.allowCommand && !commandConfirm {
			return ErrorResult("command_confirm=true is required when allow_command is disabled")
		}
		deliver = false
	}

	// Truncate message for job name (max 30 chars)
	messagePreview := utils.Truncate(message, 30)

	// Owner defaults to the calling agent (FR-002).
	owner, _ := args["owner"].(string)
	if owner == "" {
		owner = ToolAgentID(ctx)
	}

	// Session mode defaults to isolated (FR-004).
	sessionModeStr, _ := args["session_mode"].(string)
	sessionMode := cron.SessionMode(sessionModeStr)
	switch {
	case sessionModeStr == "":
		sessionMode = cron.SessionModeIsolated
	case sessionMode.Valid():
		// valid
	default:
		return ErrorResult(fmt.Sprintf("invalid session_mode %q (want isolated|continue|main)", sessionModeStr))
	}

	// Optional per-schedule timeout (FR-003).
	timeoutSeconds := 0
	if ts, ok := args["timeout_seconds"].(float64); ok && ts > 0 {
		timeoutSeconds = int(ts)
	}

	// Single atomic write (AddJobFull) so the job never persists with an empty
	// owner/mode between an AddJob and a follow-up UpdateJob. SessionMode is
	// already normalized above (isolated default + Valid() check); Enabled is
	// true by default (nil → true).
	job, err := t.cronService.AddJobFull(cron.JobSpec{
		Name:           messagePreview,
		Schedule:       schedule,
		Message:        message,
		Command:        command,
		Deliver:        deliver,
		Channel:        channel,
		To:             chatID,
		AgentID:        owner,
		SessionMode:    sessionMode,
		TimeoutSeconds: timeoutSeconds,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("Error adding job: %v", err))
	}

	return SilentResult(fmt.Sprintf("Cron job added: %s (id: %s)", job.Name, job.ID))
}

func (t *CronTool) listJobs() *ToolResult {
	jobs := t.cronService.ListJobs(false)

	if len(jobs) == 0 {
		return SilentResult("No scheduled jobs")
	}

	var result strings.Builder
	result.WriteString("Scheduled jobs:\n")
	for _, j := range jobs {
		var scheduleInfo string
		if j.Schedule.Kind == "every" && j.Schedule.EveryMS != nil {
			scheduleInfo = fmt.Sprintf("every %ds", *j.Schedule.EveryMS/1000)
		} else if j.Schedule.Kind == "cron" {
			scheduleInfo = j.Schedule.Expr
		} else if j.Schedule.Kind == "at" {
			scheduleInfo = "one-time"
		} else {
			scheduleInfo = "unknown"
		}
		result.WriteString(fmt.Sprintf("- %s (id: %s, %s)\n", j.Name, j.ID, scheduleInfo))
	}

	return SilentResult(result.String())
}

func (t *CronTool) removeJob(args map[string]any) *ToolResult {
	jobID, ok := args["job_id"].(string)
	if !ok || jobID == "" {
		return ErrorResult("job_id is required for remove")
	}

	if t.cronService.RemoveJob(jobID) {
		return SilentResult(fmt.Sprintf("Cron job removed: %s", jobID))
	}
	return ErrorResult(fmt.Sprintf("Job %s not found", jobID))
}

func (t *CronTool) enableJob(args map[string]any, enable bool) *ToolResult {
	jobID, ok := args["job_id"].(string)
	if !ok || jobID == "" {
		return ErrorResult("job_id is required for enable/disable")
	}

	job := t.cronService.EnableJob(jobID, enable)
	if job == nil {
		return ErrorResult(fmt.Sprintf("Job %s not found", jobID))
	}

	status := "enabled"
	if !enable {
		status = "disabled"
	}
	return SilentResult(fmt.Sprintf("Cron job '%s' %s", job.Name, status))
}
