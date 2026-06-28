//go:build goolm && stdjson

// Package stop implements the `omnipus stop` subcommand, which terminates a
// running Omnipus gateway that was started via `omnipus start` (daemon mode).
package stop

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/daemon"
)

// NewStopCommand returns the cobra command for `omnipus stop`.
func NewStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running Omnipus gateway",
		Long: `Stop the Omnipus gateway that was started with 'omnipus start'.

Reads the PID file from the Omnipus home directory, verifies the process is an
Omnipus gateway, sends SIGTERM (and SIGKILL after the grace period if needed),
and removes the PID file.

If no gateway is running, this command exits 0 with a message.`,
		Example: `  omnipus stop`,
		// SilenceUsage suppresses cobra's usage block on RunE errors.
		SilenceUsage: true,

		RunE: func(cmd *cobra.Command, args []string) error {
			home := internal.GetOmnipusHome()

			stopped, err := daemon.Stop(home)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			if stopped {
				fmt.Fprintln(os.Stdout, "Omnipus gateway stopped.")
			} else {
				fmt.Fprintln(os.Stdout, "No Omnipus gateway is running.")
			}
			return nil
		},
	}
}
