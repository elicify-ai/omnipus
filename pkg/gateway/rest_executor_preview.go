// rest_executor_preview.go — POST /api/v1/agents/executor-preview.
//
// Stateless, agent-agnostic preview of the REAL command Omnipus would spawn
// for a subagent_3p external-CLI worker with the given cli/model/cli_path/
// cli_args/max_tool_iterations settings. Unlike GET /api/v1/agents/
// executor-defaults (a hand-maintained description that must be kept
// byte-accurate to the drivers by hand — see listExecutorDefaults' doc
// comment), this endpoint calls the REAL buildArgs() logic each driver uses
// at actual dispatch time (pkg/agent/runner/driver_{claude,codex,opencode}.go,
// via the BuildClaudeArgs/BuildCodexArgs/BuildOpencodeArgs cross-package
// export in buildargs_crosspkg.go) so the previewed argv can never drift from
// what a real run would do. No subprocess is spawned and nothing is
// persisted.

package gateway

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// executorPreviewDroppedArg is the exact anonymous shape oapi-codegen
// generated for gen.ExecutorCommandPreviewResponse.DroppedArgs' item type —
// same field names, types, json tags, and ORDER (Go struct type identity
// considers field sequence). Declared once here so every append site below
// constructs an identical literal instead of repeating the inline shape.
type executorPreviewDroppedArg = struct { // not-wire-format: mirrors the generated gen.ExecutorCommandPreviewResponse.DroppedArgs item shape
	Flag   string `json:"flag"`
	Reason string `json:"reason"`
}

// postAgentsExecutorPreview handles POST /api/v1/agents/executor-preview
// (operationId postAgentsExecutorPreview). Decodes and validates the request
// body, calls the real per-driver buildArgs() via the runner package's
// cross-package export, and returns the resulting argv/command_line/
// prompt_delivery/dropped_args/model_dropped_reason.
func (a *restAPI) postAgentsExecutorPreview(w http.ResponseWriter, r *http.Request) {
	var req gen.ExecutorCommandPreviewRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ExecutorCommandPreviewRequest", &req, validateEnabled) {
		return
	}

	cli := string(req.Cli)
	if !requireSupportedCLI(w, cli) {
		return
	}

	model := derefTrimStr(req.Model)
	cliPath := derefTrimStr(req.CliPath)
	cliArgsRaw := derefStr(req.CliArgs)
	// Mirror runExternalCLISubTurn's own fallback EXACTLY (external_dispatch.go:
	// "maxTurns := agent.MaxIterations; if maxTurns <= 0 { maxTurns =
	// agent.DefaultExternalMaxTurns }") — omitted OR an explicit zero/negative
	// value previews with the SAME default a real run would apply, referencing
	// the constant directly so the two can never drift apart. Without this,
	// leaving the field blank previously showed no --max-turns flag at all,
	// silently misrepresenting what a real dispatch actually does.
	maxTurns := agent.DefaultExternalMaxTurns
	if req.MaxToolIterations != nil && *req.MaxToolIterations > 0 {
		maxTurns = *req.MaxToolIterations
	}

	// Tokenise cli_args exactly the way the real dispatch site does
	// (pkg/agent/external_dispatch.go via runner.ParseCLIArgs), so the
	// preview sees the identical argv the runtime would build from the same
	// free-form string.
	cliArgs := runner.ParseCLIArgs(cliArgsRaw, "executor-preview")

	opts := runner.RunOptions{
		Model:    model,
		CLIPath:  cliPath,
		CLIArgs:  cliArgs,
		MaxTurns: maxTurns,
		// Constant placeholder: inert for claude/codex (their buildArgs never
		// reads opts.Input — the real prompt reaches them exclusively via
		// stdin in Run(), not buildArgs), and for opencode it correctly shows
		// the prompt's real position in argv (opencode's buildArgs appends
		// opts.Input as the trailing positional token after "--").
		Input: "<prompt>",
	}

	var argv []string
	switch cli {
	case "claude-code":
		argv = runner.BuildClaudeArgs(opts)
	case "codex":
		argv = runner.BuildCodexArgs(opts)
	case "opencode":
		argv = runner.BuildOpencodeArgs(opts)
	}
	// filterKey is the internal denylist key each driver's OWN buildArgs
	// passes to filterDangerousCLIArgs (see driver_{claude,codex,opencode}.go)
	// — "claude", not the wire ExternalCliTool value "claude-code". Shared
	// with executor-smoke-test via the ONE runner.FilterKey helper (see its
	// doc) instead of each endpoint re-deriving this mapping independently.
	filterKey := runner.FilterKey(cli)

	// Independently compute the same denylist decision each driver's
	// buildArgs already applied internally to opts.CLIArgs, but with reasons
	// attached — filterDangerousCLIArgsDetailed is a pure function of
	// (filterKey, cliArgs), so this reproduces exactly the "kept" set already
	// reflected in argv above without re-deriving the filtering logic here.
	_, droppedDetailed := runner.FilterDangerousCLIArgsDetailed(filterKey, cliArgs)
	droppedArgs := make([]executorPreviewDroppedArg, 0, len(droppedDetailed))
	for _, d := range droppedDetailed {
		droppedArgs = append(droppedArgs, executorPreviewDroppedArg{Flag: d.Flag, Reason: d.Reason})
	}

	binary := cliPath
	if binary == "" {
		binary = runner.DefaultCLIBinary(cli)
	}

	var promptDelivery gen.ExecutorCommandPreviewResponsePromptDelivery
	switch cli {
	case "claude-code", "codex":
		promptDelivery = gen.Stdin
	case "opencode":
		promptDelivery = gen.PositionalArgumentAfter
	}

	resp := gen.ExecutorCommandPreviewResponse{
		Binary:         binary,
		Argv:           argv,
		CommandLine:    shellQuoteCommandLine(append([]string{binary}, argv...)),
		PromptDelivery: promptDelivery,
		DroppedArgs:    droppedArgs,
	}

	// model_dropped_reason: only opencode conditionally omits --model based on
	// the configured value's shape (claude-code/codex accept any non-empty
	// model string as-is — see BuildClaudeArgs/BuildCodexArgs above, neither
	// of which has a shape guard). Independently re-derive the same
	// "not provider/model-shaped" condition BuildOpencodeArgs already acted on
	// when building argv, via the exported LooksLikeProviderModel helper.
	if cli == "opencode" && model != "" && !runner.LooksLikeProviderModel(model) {
		resp.ModelDroppedReason = strPtr(fmt.Sprintf(
			"model %q is not shaped like \"provider/model\" (opencode requires exactly one \"/\" separator with non-empty content on both sides); --model was omitted so the CLI falls back to its own configured default",
			model,
		))
	}

	jsonOK(w, resp)
}

// shellQuoteCommandLine joins argv tokens into a single, shell-quoted display
// string suitable for showing in the UI or copying into a terminal. It is
// deliberately a small, one-directional (argv → display string) helper — not
// a full POSIX shell quoter — sufficient for the flag values the three
// drivers actually produce (model names, paths, permission-mode strings).
func shellQuoteCommandLine(argv []string) string {
	quoted := make([]string, len(argv))
	for i, tok := range argv {
		quoted[i] = shellQuoteArg(tok)
	}
	return strings.Join(quoted, " ")
}

// shellQuoteArgSpecialChars are the characters that make a token unsafe (or
// merely misleading) to display unquoted in a copy-pasteable shell command
// line: whitespace, quotes, and common shell metacharacters.
const shellQuoteArgSpecialChars = " \t\n\"'\\$`|&;<>()*?[]#~!{}"

// shellQuoteArg returns tok unquoted when it contains no whitespace or
// shell-special characters, else wraps it in single quotes (POSIX-safe:
// single quotes suppress all expansion), escaping any embedded single quote
// by closing the quote, emitting an escaped literal quote, and reopening it
// (the standard shell idiom), so the result can be pasted directly into a
// shell.
func shellQuoteArg(tok string) string {
	if tok == "" {
		return "''"
	}
	if !strings.ContainsAny(tok, shellQuoteArgSpecialChars) {
		return tok
	}
	return "'" + strings.ReplaceAll(tok, "'", `'\''`) + "'"
}
