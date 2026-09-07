// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// CopilotCliProvider implements LLMProvider by driving the official GitHub
// Copilot CLI as a subprocess (ADR-068 §2.1, spec S68 "GitHub Copilot
// subprocess provider"). The provider id is `github-copilot`; the catalog row
// carries protocol `cli` and `cli_kind: copilot` (X-14).
//
// No Go SDK module is added for this provider (Constraint #1): the vendor CLI
// is the whole integration, it holds the login, and it bills the operator's own
// Copilot subscription. Tool definitions are NOT forwarded — the CLI runs its
// own tools, exactly as `codex-cli` does.
//
// # Verified CLI contract (@github/copilot 1.0.80, checked 2026-08-24)
//
// The task flagged these flags as UNVERIFIED. They were confirmed by running
// the published 1.0.80 binary (`copilot --help`, and a real invocation of the
// exact argv below, which reaches the CLI's authentication check — argument
// parsing happens first, so an unknown flag would have failed differently):
//
//   - `-p, --prompt <text>`   execute a prompt in non-interactive mode and exit.
//   - `-s, --silent`          output only the agent response (no stats) —
//     "useful for scripting with -p".
//   - `--allow-all-tools`     "required for non-interactive mode".
//   - `--no-ask-user`         disable the ask_user tool so the run never blocks
//     waiting for a human.
//   - `--no-color`            no ANSI escapes in the captured output.
//   - `--log-level none`      keep the CLI's own logging out of the stream.
//   - `-C <directory>`        change working directory before doing anything.
//   - `--model <model>`       pin the model (e.g. `claude-sonnet-4.6`).
//   - `copilot login`         the operator's own sign-in command (FR-008).
//   - `COPILOT_HOME`          the CLI's config home (default ~/.copilot).
//
// The CLI also offers `--output-format json` (JSONL, one object per line), the
// shape of which cannot be observed without a live Copilot subscription. Rather
// than parse an unverified event schema this provider uses the documented
// silent-text contract, whose shape is exactly "the agent's response".
type CopilotCliProvider struct {
	command   string
	workspace string
}

// CopilotCLICommand is the default binary name of the GitHub Copilot CLI, as
// installed by `npm i -g @github/copilot`, Homebrew `copilot-cli` and WinGet
// `GitHub.Copilot`.
const CopilotCLICommand = "copilot"

// CopilotHomeEnvVar overrides the Copilot CLI's configuration home (its login
// state lives there). Default: ~/.copilot.
const CopilotHomeEnvVar = "COPILOT_HOME"

// CopilotDefaultModel is the model identifier reported when a row names no
// model. The CLI picks its own default (Claude Sonnet) in that case.
const CopilotDefaultModel = "github-copilot"

// ErrCopilotCLINotFound is returned when the `copilot` binary is not on PATH.
// The gateway maps it to Provider.status `disconnected` with the operator hint
// (ADR-068 FR-009).
var ErrCopilotCLINotFound = errors.New("copilot cli not found")

// NewCopilotCliProvider creates a Copilot CLI provider that runs the `copilot`
// binary found on PATH, with the given workspace as the child's working
// directory.
func NewCopilotCliProvider(workspace string) *CopilotCliProvider {
	return NewCopilotCliProviderWithCommand("", workspace)
}

// NewCopilotCliProviderWithCommand creates a Copilot CLI provider that runs an
// explicit binary name/path. This is a plain Go parameter, not a catalog row
// field — catalog.Provider carries no `cli_path`, and both callers
// (CreateProviderFromConfig and the onboarding probe) pass "" unconditionally
// today, so there is currently no way for an operator to point this at a
// `copilot` binary installed off PATH; a row reports `cli_missing` instead.
// An empty command falls back to the default `copilot` on PATH.
func NewCopilotCliProviderWithCommand(command, workspace string) *CopilotCliProvider {
	if command == "" {
		command = CopilotCLICommand
	}
	slog.Warn("Copilot CLI runs its own tools without per-tool approval (--allow-all-tools)",
		"provider", "github-copilot", "command", command)
	return &CopilotCliProvider{
		command:   command,
		workspace: workspace,
	}
}

// GetDefaultModel returns the default model identifier.
func (p *CopilotCliProvider) GetDefaultModel() string {
	return CopilotDefaultModel
}

// CopilotCLIAvailable reports whether the given command (empty means the
// default `copilot`) resolves to an executable on this machine.
func CopilotCLIAvailable(command string) bool {
	if command == "" {
		command = CopilotCLICommand
	}
	_, err := exec.LookPath(command)
	return err == nil
}

// Chat implements LLMProvider.Chat by executing the Copilot CLI once in
// non-interactive prompt mode.
func (p *CopilotCliProvider) Chat(
	ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any,
) (*LLMResponse, error) {
	if p.command == "" {
		return nil, fmt.Errorf("copilot command not configured")
	}
	if !CopilotCLIAvailable(p.command) {
		return nil, fmt.Errorf("%w: %s is not on this machine", ErrCopilotCLINotFound, p.command)
	}

	prompt := p.buildPrompt(messages, tools)

	args := []string{
		"-p", prompt,
		"-s",
		"--allow-all-tools",
		"--no-ask-user",
		"--no-color",
		"--log-level", "none",
	}
	if p.workspace != "" {
		args = append(args, "-C", p.workspace)
	}
	if model != "" && model != CopilotDefaultModel {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, p.command, args...)
	if p.workspace != "" {
		cmd.Dir = p.workspace
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
			return nil, fmt.Errorf("copilot cli error: %s", stderrStr)
		}
		return nil, fmt.Errorf("copilot cli error: %w", err)
	}

	return p.parseOutput(stdout.String())
}

// buildPrompt flattens the conversation into the single prompt string the CLI
// accepts. System messages become leading instructions (the CLI has no
// system-prompt flag), and tool definitions — when a caller passes any — are
// rendered with the shared CLI tools prompt so the model can still answer in
// the JSON tool-call form the loop understands.
func (p *CopilotCliProvider) buildPrompt(messages []Message, tools []ToolDefinition) string {
	var systemParts []string
	var conversationParts []string

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemParts = append(systemParts, msg.Content)
		case "user":
			conversationParts = append(conversationParts, msg.Content)
		case "assistant":
			conversationParts = append(conversationParts, "Assistant: "+msg.Content)
		case "tool":
			conversationParts = append(conversationParts,
				fmt.Sprintf("[Tool Result for %s]: %s", msg.ToolCallID, msg.Content))
		}
	}

	var sb strings.Builder

	if len(systemParts) > 0 {
		sb.WriteString("## System Instructions\n\n")
		sb.WriteString(strings.Join(systemParts, "\n\n"))
		sb.WriteString("\n\n## Task\n\n")
	}

	if len(tools) > 0 {
		sb.WriteString(buildCLIToolsPrompt(tools))
		sb.WriteString("\n\n")
	}

	if len(conversationParts) == 1 && len(systemParts) == 0 && len(tools) == 0 {
		return conversationParts[0]
	}

	sb.WriteString(strings.Join(conversationParts, "\n"))
	return sb.String()
}

// parseOutput turns the CLI's suppressed-mode stdout (plain response text)
// into an LLMResponse. The CLI reports no token usage in this mode, so Usage
// is nil — the loop treats that as "unknown", never as zero.
func (p *CopilotCliProvider) parseOutput(output string) (*LLMResponse, error) {
	content := strings.TrimSpace(output)

	toolCalls := extractToolCallsFromText(content)

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		content = stripToolCallsFromText(content)
	}

	return &LLMResponse{
		Content:      strings.TrimSpace(content),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        nil,
	}, nil
}
