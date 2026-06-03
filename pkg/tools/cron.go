package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/constants"
	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

// JobExecutor is the interface for executing cron jobs through the agent
type JobExecutor interface {
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
}

// CronTool provides scheduling capabilities for the agent
type CronTool struct {
	BaseTool
	cronService  *cron.CronService
	executor     JobExecutor
	msgBus       *bus.MessageBus
	execTool     *ExecTool
	allowCommand bool
	execEnabled  bool
}

// NewCronTool creates a new CronTool
// execTimeout: 0 means no timeout, >0 sets the timeout duration
func NewCronTool(
	cronService *cron.CronService, executor JobExecutor, msgBus *bus.MessageBus, workspace string, restrict bool,
	execTimeout time.Duration, config *config.Config,
) (*CronTool, error) {
	allowCommand := false
	execEnabled := false
	if config != nil {
		allowCommand = config.Tools.Cron.AllowCommand
		execEnabled = config.Tools.Exec.Enabled
	}

	var execTool *ExecTool
	if execEnabled {
		var err error
		execTool, err = NewExecToolWithConfig(workspace, restrict, config)
		if err != nil {
			return nil, fmt.Errorf("unable to configure exec tool: %w", err)
		}
	}

	if execTool != nil {
		execTool.SetTimeout(execTimeout)
	}
	return &CronTool{
		cronService:  cronService,
		executor:     executor,
		msgBus:       msgBus,
		execTool:     execTool,
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

	job, err := t.cronService.AddJob(
		messagePreview,
		schedule,
		message,
		deliver,
		channel,
		chatID,
	)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Error adding job: %v", err))
	}

	// Persist owner/mode/timeout + the command payload (if any). Always update
	// when any of these were set so the new fields land on the stored job.
	needsUpdate := command != "" || owner != "" || sessionMode != cron.SessionModeIsolated || timeoutSeconds > 0
	if needsUpdate {
		if command != "" {
			job.Payload.Command = command
		}
		job.AgentID = owner
		job.SessionMode = sessionMode
		job.TimeoutSeconds = timeoutSeconds
		// H7: check error and remove job on failure.
		if err := t.cronService.UpdateJob(job); err != nil {
			t.cronService.RemoveJob(job.ID)
			return ErrorResult(fmt.Sprintf("Error saving cron job: %v", err))
		}
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

// ExecuteJob executes a cron job through the agent. It returns the linked
// session id (when one exists) and an error for any genuine run failure so the
// cron service records the run as an error rather than a stringified success
// (W-4 / M-2). The deliver and command semantics are unchanged.
func (t *CronTool) ExecuteJob(ctx context.Context, job *cron.CronJob) (string, error) {
	// Get channel/chatID from job payload
	channel := job.Payload.Channel
	chatID := job.Payload.To

	// Default values if not set
	if channel == "" {
		channel = "cli"
	}
	if chatID == "" {
		chatID = "direct"
	}

	// Execute command if present
	if job.Payload.Command != "" {
		if !t.execEnabled || t.execTool == nil {
			output := "Error executing scheduled command: command execution is disabled"
			pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer pubCancel()
			t.msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Content: output,
			})
			return "", fmt.Errorf("command execution is disabled")
		}

		args := map[string]any{
			"action":    "run",
			"command":   job.Payload.Command,
			"__channel": channel,
			"__chat_id": chatID,
		}

		result := t.execTool.Execute(ctx, args)
		var output string
		var runErr error
		if result.IsError {
			output = fmt.Sprintf("Error executing scheduled command: %s", result.ForLLM)
			runErr = fmt.Errorf("scheduled command failed: %s", result.ForLLM)
		} else {
			output = fmt.Sprintf("Scheduled command '%s' executed:\n%s", job.Payload.Command, result.ForLLM)
		}

		pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pubCancel()
		t.msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: output,
		})
		return "", runErr
	}

	// If deliver=true, send message directly without agent processing
	if job.Payload.Deliver {
		pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pubCancel()
		t.msgBus.PublishOutbound(pubCtx, bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: job.Payload.Message,
		})
		return "", nil
	}

	// For deliver=false, process through agent (for complex tasks)
	if t.executor == nil {
		return "", fmt.Errorf("no executor configured for scheduled agent run")
	}
	sessionKey := fmt.Sprintf("cron-%s", job.ID)

	// Call agent with job's message
	response, err := t.executor.ProcessDirectWithChannel(
		ctx,
		job.Payload.Message,
		sessionKey,
		channel,
		chatID,
	)
	if err != nil {
		return sessionKey, fmt.Errorf("scheduled agent run failed: %w", err)
	}

	// Response is automatically sent via MessageBus by AgentLoop
	_ = response // Will be sent by AgentLoop
	return sessionKey, nil
}
