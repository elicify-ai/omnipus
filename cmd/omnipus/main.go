// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	auditcmd "github.com/dapicom-ai/omnipus/cmd/omnipus/internal/audit"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/clitoken"
	credcmd "github.com/dapicom-ai/omnipus/cmd/omnipus/internal/credentials"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/doctor"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/gateway"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/onboard"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/run"
	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/version"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// rosterLine describes a single chat-target agent for the no-args listing.
type rosterLine struct {
	ID   string
	Name string
}

// buildRoster returns the chat-target agents from the given config, excluding
// workers. The ordering mirrors the order in cfg.Agents.List (FR-008/US-6).
func buildRoster(cfg *config.Config) []rosterLine {
	lines := make([]rosterLine, 0, len(cfg.Agents.List))
	for _, a := range cfg.Agents.List {
		if a.IsChatTarget() {
			lines = append(lines, rosterLine{ID: a.ID, Name: a.Name})
		}
	}
	return lines
}

// printRosterAndUsage writes the agent roster and a usage block to w.
// Called when omnipus is invoked with no arguments (FR-008/US-6).
func printRosterAndUsage(w *os.File, roster []rosterLine) {
	fmt.Fprintln(w, "Agents:")
	for _, r := range roster {
		fmt.Fprintf(w, "  %-20s %s\n", r.ID, r.Name)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, `  omnipus <agent> "<prompt>"     run a one-shot task`)
	fmt.Fprintln(w, `  omnipus <agent> --model <slug> "<prompt>"`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  omnipus onboard      first-time setup — configure a provider and admin account")
	fmt.Fprintln(w, "  omnipus start        start Omnipus (SPA + API on port 5000)")
	fmt.Fprintln(w, "  omnipus credentials  manage secrets (set/list/delete/rotate)")
	fmt.Fprintln(w, "  omnipus audit        view the audit log")
	fmt.Fprintln(w, "  omnipus doctor       diagnose configuration issues")
	fmt.Fprintln(w, "  omnipus version      print build version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "omnipus --help" for full documentation.`)
}

// removedVerbs is the set of CLI verbs removed in the CLI redesign. When the
// user types one of these, RunE prints a helpful message instead of the
// confusing "unknown agent" error that would otherwise appear because the root
// uses cobra.ArbitraryArgs (US-11/AC-1).
var removedVerbs = map[string]bool{
	"agent":   true,
	"auth":    true,
	"status":  true,
	"cron":    true,
	"migrate": true,
	"model":   true,
	"skills":  true,
}

// NewOmnipusCommand builds the root cobra command with the minimized CLI tree.
//
// Subcommands (resolved first by cobra): onboard, start, credentials, audit, doctor, version.
// Root RunE handles the positional <agent> [<prompt>] execute path (FR-001/002/008).
// Removed verbs (agent, auth, status, cron, migrate, model, skills) are no longer registered;
// typing one prints a helpful message (US-11/AC-1) rather than "unknown agent".
func NewOmnipusCommand() *cobra.Command {
	short := fmt.Sprintf("%s omnipus - Personal AI Assistant v%s\n\n", internal.Logo, config.GetVersion())

	// Per-run flags — declared here, attached to root.
	var flagModel string
	var flagYes bool
	var flagTimeout time.Duration
	var flagURL string

	cmd := &cobra.Command{
		Use:   "omnipus",
		Short: short,
		Long: fmt.Sprintf(`%s omnipus — Personal AI Assistant v%s

Run a one-shot task against a named agent:

  omnipus <agent> "<prompt>"
  omnipus <agent> --model <slug> "<prompt>"

With no arguments, lists available agents and usage.

Commands:
  onboard      First-time setup — configure a provider and admin account.
  start        Start Omnipus (SPA + API on port 5000). Alias: gateway, g.
  credentials  Manage secrets: set, list, delete, rotate.
  audit        View the audit log.
  doctor       Diagnose configuration issues.
  version      Print build version.

Examples:
  omnipus jim "summarize the last 10 commits"
  omnipus mia "what can you do?"
  omnipus jim --model openrouter/glm-5.2 "explain Go generics"
  omnipus onboard
  omnipus start

Reserved names (shadowed by subcommands): onboard, start, credentials, audit, doctor, version.
If an agent shares a name with a subcommand, use the agent's ID directly via the API or rename it.
`, internal.Logo, config.GetVersion()),
		Example: `  omnipus jim "summarize the last 10 commits"
  omnipus mia "what can you do?"
  omnipus onboard
  omnipus start`,

		// DisableFlagParsing is not set; TraverseChildren allows flags on the root
		// to be declared so --model/--yes/--timeout/--url are available on the run path.
		//
		// Cobra subcommand resolution: cobra checks args[0] against registered
		// subcommand names (and aliases) first. When args[0] matches, the subcommand
		// takes over. Only when args[0] does NOT match a subcommand does RunE fire.
		// We set Args to ArbitraryArgs so cobra doesn't error on unknown args[0]
		// before RunE gets a chance to interpret them as the agent name.
		Args: cobra.ArbitraryArgs,

		// SilenceUsage suppresses cobra's auto-printed usage on RunE errors so
		// our own formatted messages are the only thing printed.
		SilenceUsage: true,

		RunE: func(cmd *cobra.Command, args []string) error {
			home := internal.GetOmnipusHome()
			configPath := internal.GetConfigPath()

			// FR-007: --url guard (remote unsupported in P0).
			if flagURL != "" {
				fmt.Fprintln(os.Stderr, "error: remote gateways are not supported yet")
				os.Exit(1)
			}

			// 0 args → print roster + usage (FR-008/US-6).
			if len(args) == 0 {
				cfg, loadErr := config.LoadConfig(configPath)
				if loadErr != nil {
					fmt.Fprintln(os.Stderr, "error: failed to load config:", loadErr)
					os.Exit(1)
				}
				roster := buildRoster(cfg)
				if len(roster) == 0 && len(cfg.Agents.List) == 0 {
					// Pre-onboard: no agents seeded yet.
					fmt.Fprintln(os.Stderr, "No agents configured. Run `omnipus onboard` first.")
					os.Exit(1)
				}
				printRosterAndUsage(os.Stdout, roster)
				return nil
			}

			agentID := args[0]

			// US-11/AC-1: if args[0] is a removed verb, print a helpful message
			// before attempting the agent-lookup (which would print the confusing
			// "unknown agent" error because the root uses ArbitraryArgs).
			if removedVerbs[agentID] {
				fmt.Fprintf(os.Stderr,
					"%q was removed in the CLI redesign — run 'omnipus --help' for the current commands\n",
					agentID,
				)
				os.Exit(1)
			}

			// Exactly 1 arg (agent, no prompt) → usage error.
			if len(args) == 1 {
				fmt.Fprintf(os.Stderr, "error: provide a prompt: omnipus %s \"<prompt>\"\n", agentID)
				os.Exit(1)
			}

			// Join remaining args as the prompt (allows: omnipus jim word1 word2).
			prompt := strings.Join(args[1:], " ")

			// Empty prompt guard (edge case: omnipus jim "").
			if strings.TrimSpace(prompt) == "" {
				fmt.Fprintf(os.Stderr, "error: provide a prompt: omnipus %s \"<prompt>\"\n", agentID)
				os.Exit(1)
			}

			// Load config to validate the agent.
			cfg, loadErr := config.LoadConfig(configPath)
			if loadErr != nil {
				fmt.Fprintln(os.Stderr, "error: failed to load config:", loadErr)
				os.Exit(1)
			}

			// FR-002: validate agent is a chat-target.
			var agentFound bool
			for _, a := range cfg.Agents.List {
				if a.ID == agentID {
					agentFound = true
					if !a.IsChatTarget() {
						fmt.Fprintf(os.Stderr, "error: %q is a worker agent and cannot be run directly\n", agentID)
						os.Exit(1)
					}
					break
				}
			}
			if !agentFound {
				fmt.Fprintf(os.Stderr, "error: unknown agent %q — run `omnipus` to list available agents\n", agentID)
				os.Exit(1)
			}

			// FR-006: load the CLI token.
			token, tokenErr := clitoken.LoadCLIToken(home)
			if tokenErr != nil {
				if errors.Is(tokenErr, clitoken.ErrNoCLIToken) {
					fmt.Fprintln(os.Stderr, "error: no CLI key found — run `omnipus start` to create one")
					os.Exit(1)
				}
				fmt.Fprintln(os.Stderr, "error: failed to load CLI key:", tokenErr)
				os.Exit(1)
			}

			// Build the local gateway address from config.
			addr := fmt.Sprintf("localhost:%d", cfg.Gateway.Port)

			// Dispatch to the run client.
			runErr := run.Run(cmd.Context(), run.Options{
				Agent:   agentID,
				Prompt:  prompt,
				Model:   flagModel,
				Yes:     flagYes,
				Timeout: flagTimeout,
				URL:     flagURL,
				Addr:    addr,
				Token:   token,
				Stdout:  os.Stdout,
				Stderr:  os.Stderr,
			})
			if runErr == nil {
				return nil
			}

			// Map sentinel errors to user-facing messages + non-zero exit.
			// The four sentinel values carry complete user-facing messages;
			// print them verbatim. Only the default (wrapped) case prepends
			// the "error:" prefix so the label isn't duplicated.
			switch {
			case errors.Is(runErr, run.ErrRemoteUnsupported):
				fmt.Fprintln(os.Stderr, run.ErrRemoteUnsupported.Error())
			case errors.Is(runErr, run.ErrGatewayDown):
				fmt.Fprintln(os.Stderr, run.ErrGatewayDown.Error())
			case errors.Is(runErr, run.ErrKeyInvalid):
				fmt.Fprintln(os.Stderr, run.ErrKeyInvalid.Error())
			case errors.Is(runErr, run.ErrTimeout):
				fmt.Fprintln(os.Stderr, run.ErrTimeout.Error())
			default:
				fmt.Fprintln(os.Stderr, "error:", runErr)
			}
			os.Exit(1)
			return nil // unreachable; satisfies RunE signature
		},
	}

	// Per-run flags.
	cmd.Flags().StringVar(&flagModel, "model", "", "Override the model for this run (e.g. openrouter/glm-5.2)")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Auto-approve tool-approval requests (default: deny-and-continue)")
	cmd.Flags().DurationVar(&flagTimeout, "timeout", 300*time.Second, "Maximum time to wait for the agent to finish")
	cmd.Flags().StringVar(&flagURL, "url", "", "Remote gateway URL (not supported yet; reserved for P1)")

	// Subcommands — all kept verbs. Subcommands resolve before RunE, so these
	// names shadow same-named agents (documented in Long + US-11/OBS-2).
	cmd.AddCommand(
		onboard.NewOnboardCommand(),
		auditcmd.NewAuditCommand(),
		credcmd.NewCredentialsCommand(),
		doctor.NewDoctorCommand(),
		gateway.NewGatewayCommand(),
		version.NewVersionCommand(),
	)

	return cmd
}

func main() {
	cmd := NewOmnipusCommand()
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}
