package gateway

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/cmd/omnipus/internal"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/clitoken"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/netinfo"
	"github.com/elicify-ai/omnipus/pkg/gateway"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

func NewGatewayCommand() *cobra.Command {
	var debug bool
	var noTruncate bool
	var sandboxMode string
	var allowGodMode bool
	var newCLIToken bool
	// allowEmptyDeprecated accepts the legacy --allow-empty flag silently so
	// existing callers (CI workflows, ops scripts, eval-runner, etc.) keep
	// working unchanged. The behavior is now unconditional: the gateway
	// boots into limited mode when no provider is configured regardless of
	// the flag. Hidden from --help so new users don't discover it.
	var allowEmptyDeprecated bool

	cmd := &cobra.Command{
		Use: "start",
		// Aliases: "gateway" is preserved because every existing script,
		// workflow, doc, and runbook references it (CLAUDE.md, evals/,
		// tests/e2e/README.md, .github/workflows/*.yml, eval-runner, etc.).
		// "g" is the original short alias. Both are accepted unchanged so the
		// rename is zero-break, but only "start" is shown in --help.
		Aliases: []string{"gateway", "g"},
		Short:   "Start Omnipus (serves the SPA + API on port 5000)",
		Long: "Start Omnipus.\n\n" +
			"Serves the SPA, the REST + WebSocket API, and sandboxed agent\n" +
			"preview iframes on a SINGLE port (5000 by default). ADR-044 unified\n" +
			"/preview/ onto this listener; there is no second preview port.\n\n" +
			"On a fresh install the SPA at http://localhost:5000 runs the\n" +
			"onboarding wizard; afterwards `start` boots straight into the\n" +
			"chat UI with the configured provider.\n\n" +
			"Exit codes:\n" +
			"  0   clean shutdown\n" +
			"  1   generic boot failure (credential/config/provider error)\n" +
			"  2   usage error (invalid flag value)\n" +
			"  78  sandbox apply/install failure on a capable kernel (EX_CONFIG)\n",
		Args: cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if noTruncate && !debug {
				return fmt.Errorf("the --no-truncate option can only be used in conjunction with --debug (-d)")
			}
			if noTruncate {
				utils.SetDisableTruncation(true)
				logger.Info("String truncation is globally disabled via 'no-truncate' flag")
			}
			// Validate --sandbox up front so typos (--sandbox=of) exit
			// with code 2 (usage error) before any boot logic runs.
			// FR-J-006 second sentence.
			if sandboxMode != "" {
				if _, err := sandbox.ParseMode(sandboxMode); err != nil {
					// Cobra maps PreRunE errors to exit 1 by default.
					// We want exit 2 (usage error) for bad flag values,
					// which cobra reserves for argument parse errors via
					// its SilenceErrors/SilenceUsage + explicit os.Exit.
					fmt.Fprintln(os.Stderr, "Error:", err)
					os.Exit(2)
				}
			}
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			// Validate --allow-god-mode against the build tag before boot.
			// GodModeAvailable is false when compiled with -tags=nogodmode.
			if allowGodMode && !sandbox.GodModeAvailable {
				fmt.Fprintln(os.Stderr,
					"Error: god mode unavailable in this build (compiled with nogodmode); "+
						"remove --allow-god-mode and restart")
				os.Exit(2)
			}

			home := internal.GetOmnipusHome()

			// FR-018: ensure the cli principal and its token file exist
			// (create-if-absent). When --new-cli-token is set, always
			// regenerate so the old token stops authenticating.
			if newCLIToken {
				if err := clitoken.ResetCLIToken(home); err != nil {
					// Non-fatal: the token file is informational for this
					// invocation. Log and continue so the gateway still boots.
					fmt.Fprintf(os.Stderr, "Warning: could not rotate cli token: %v\n", err)
				}
			} else {
				if _, err := clitoken.EnsureCLIToken(home); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not ensure cli token: %v\n", err)
				}
			}

			// FR-011: print bind-aware access URL block before booting so the
			// operator sees where to connect while the gateway starts up.
			printStartURLBlock(home)

			// AllowEmptyStartup is unconditionally true: if no provider is
			// configured the gateway boots into limited mode so the operator
			// can finish onboarding via the SPA wizard (or `omnipus onboard`).
			// The previous --allow-empty flag was a foot-gun: a fresh install
			// failed to start with a misleading "no providers configured" error
			// and the operator had to read the help text to discover the flag.
			runErr := gateway.RunWithOptions(gateway.RunOptions{
				Debug:             debug,
				HomePath:          home,
				ConfigPath:        internal.GetConfigPath(),
				AllowEmptyStartup: true,
				SandboxMode:       sandboxMode,
				AllowGodMode:      allowGodMode,
			})
			if runErr == nil {
				return nil
			}
			// FR-J-004: sandbox apply/install failure on a capable kernel
			// must exit 78 (EX_CONFIG) rather than the generic 1 used by
			// the top-level main.go. Distinguish via a sentinel error.
			var sbErr *gateway.SandboxBootError
			if errors.As(runErr, &sbErr) {
				fmt.Fprintln(os.Stderr, "Error:", runErr)
				os.Exit(gateway.ExitSandboxConfig)
			}
			return runErr
		},
	}

	cmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
	cmd.Flags().BoolVarP(&noTruncate, "no-truncate", "T", false, "Disable string truncation in debug logs")
	// Legacy --allow-empty / -E: silently accepted, hidden from help. The
	// gateway now always boots into limited mode when no provider is
	// configured, which is what --allow-empty did. Existing scripts keep
	// working unchanged. The variable is intentionally unread.
	cmd.Flags().BoolVarP(
		&allowEmptyDeprecated,
		"allow-empty",
		"E",
		false,
		"Deprecated; gateway always boots into limited mode on a fresh install",
	)
	_ = cmd.Flags().MarkHidden("allow-empty")
	cmd.Flags().StringVar(
		&sandboxMode,
		"sandbox",
		"",
		"Sandbox mode: enforce (default on Linux 5.13+), permissive (audit-only), off (disabled). "+
			"Overrides the gateway.sandbox.mode config value.",
	)
	cmd.Flags().BoolVar(&allowGodMode, "allow-god-mode", false,
		"Authorize the global god-mode runtime toggle (sandbox.god_mode), which "+
			"disables the kernel sandbox for every agent when switched on via "+
			"POST /api/v1/gateway/god-mode. Without this flag the toggle has no "+
			"effect even if enabled. Disabled in builds with the nogodmode tag.")
	// FR-018: --new-cli-token always regenerates the cli bearer token.
	// Without this flag, the token is created-if-absent (idempotent).
	cmd.Flags().BoolVar(&newCLIToken, "new-cli-token", false,
		"Rotate the CLI bearer token (old token stops authenticating immediately)")

	return cmd
}

// printStartURLBlock reads config.json for the gateway bind settings and
// prints the bind-aware access URL block to stderr, framed as a dashboard
// open hint. Errors are non-fatal — the URL block is purely informational.
func printStartURLBlock(home string) {
	configPath := home + "/config.json"
	urls := netinfo.AccessURLsFromConfig(configPath)
	fmt.Fprintln(os.Stderr, "Open the dashboard / log in as admin:")
	fmt.Fprint(os.Stderr, netinfo.Render(urls))
}
