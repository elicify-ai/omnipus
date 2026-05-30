//go:build !cgo

package gateway

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/gateway"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

func NewGatewayCommand() *cobra.Command {
	var debug bool
	var noTruncate bool
	var sandboxMode string
	var allowGodMode bool
	// allowEmptyDeprecated accepts the legacy --allow-empty flag silently so
	// existing callers (CI workflows, ops scripts, eval-runner, etc.) keep
	// working unchanged. The behavior is now unconditional: the gateway
	// boots into limited mode when no provider is configured regardless of
	// the flag. Hidden from --help so new users don't discover it.
	var allowEmptyDeprecated bool

	cmd := &cobra.Command{
		Use:     "gateway",
		Aliases: []string{"g"},
		Short:   "Start omnipus gateway",
		Long: "Start omnipus gateway.\n\n" +
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
			// AllowEmptyStartup is unconditionally true: if no provider is
			// configured the gateway boots into limited mode so the operator
			// can finish onboarding via the SPA wizard (or `omnipus onboard`).
			// The previous --allow-empty flag was a foot-gun: a fresh install
			// failed to start with a misleading "no providers configured" error
			// and the operator had to read the help text to discover the flag.
			runErr := gateway.RunWithOptions(gateway.RunOptions{
				Debug:             debug,
				HomePath:          internal.GetOmnipusHome(),
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
		"Allow agents to set sandbox_profile=off (disables the kernel sandbox). "+
			"Without this flag, off is silently coerced to workspace with a stderr WARN. "+
			"Disabled in builds with the nogodmode tag.")

	return cmd
}
