// Package pkgcmd implements the owner commands for .omnpkg packages (epic
// #1094 slice S1a, task T-6): `omnipus export`, `omnipus import` and the
// offline `omnipus package inspect|verify`.
//
// export and import are thin clients of the gateway's package routes
// (/api/v1/packages/export, /api/v1/packages/import and
// /api/v1/packages/import/{staging_id}/apply) using the generated contract
// types; inspect and verify read only the package file through pkg/omnpkg.
// None of them reads or writes the data folder beyond cli.token and the
// gateway address (FR-055).
//
// Spec: docs/internal/specs/export-import-s1-spec.md (revision 7), FR-050 to
// FR-055, Dataset D10 and the CLI rows of "UI Screens and States".
package pkgcmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// ExitCoder is the exit-code contract of every pkgcmd command (FR-053):
// a RunE failure that carries the process exit code it maps to.
// 0 = success (warnings included), 1 = refused or failed, 2 = usage error.
// The command has already printed its message when it returns one, so the
// caller maps ExitCode() to the process exit status without printing again.
type ExitCoder interface {
	error
	ExitCode() int
}

// Options carries the dependency seams of the package commands. Zero value =
// production defaults, except AutoStart, which pkg/app supplies (this
// package cannot import pkg/app).
type Options struct {
	// Home is the Omnipus data dir used only to load cli.token (FR-054/FR-055).
	// Empty = internal.GetOmnipusHome().
	Home string

	// GatewayURL is the gateway base URL, e.g. "http://127.0.0.1:5000".
	// Empty = "http://" + LocalGatewayAddr().
	GatewayURL string

	// Token overrides the cli.token bearer (FR-054). Empty = LoadCLIToken(Home).
	Token string

	// AutoStart is the shared auto-start step (FR-054), called at most once
	// per command when the gateway refuses the connection, followed by one
	// retry. nil = no auto-start: an unreachable gateway is refused.
	AutoStart func() error
}

// Exit codes (FR-053).
const (
	exitFailed = 1
	exitUsage  = 2
)

// exitError is the ExitCoder every pkgcmd failure returns.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }

// fail prints "error: <msg>" to the command's error stream and returns the
// coded error.
func fail(cmd *cobra.Command, code int, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(cmd.ErrOrStderr(), "error: "+msg)
	return &exitError{code: code, msg: msg}
}

// failLine prints line as is to the error stream (the CLI rows that are not
// "error:" lines: FAILED, rollback and lost-connection texts).
func failLine(cmd *cobra.Command, line string) error {
	fmt.Fprintln(cmd.ErrOrStderr(), line)
	return &exitError{code: exitFailed, msg: line}
}

// failErr maps an error from the gateway client to exit 1 with its message.
func failErr(cmd *cobra.Command, err error) error {
	var ae *apiError
	if errors.As(err, &ae) {
		return fail(cmd, exitFailed, "%s", ae.Error())
	}
	return fail(cmd, exitFailed, "%v", err)
}

// setUsageRules makes cobra's own usage failures (unknown flag, wrong
// argument count) exit 2 with an "error:" line, and silences cobra's
// duplicate printing.
func setUsageRules(cmd *cobra.Command) {
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return fail(c, exitUsage, "%v (see `%s --help`)", err, c.CommandPath())
	})
}

// exactlyOneFile is the Args rule of the commands that take one package file.
func exactlyOneFile(c *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fail(c, exitUsage, "expected exactly one package file, got %d (usage: %s)", len(args), c.UseLine())
	}
	return nil
}

// openPackageFile opens a package file for reading; a missing, unreadable
// or folder input is a usage error (FR-053).
func openPackageFile(cmd *cobra.Command, path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fail(cmd, exitUsage, "cannot read %s: %v", path, err)
	}
	if info.IsDir() {
		return nil, fail(cmd, exitUsage, "%s is a folder, not a package file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fail(cmd, exitUsage, "cannot read %s: %v", path, err)
	}
	return f, nil
}

// NewPackageCommand returns `omnipus package` with the offline subcommands
// `inspect` and `verify` (FR-052): neither needs a gateway nor writes
// anything. A missing or unknown subcommand is a usage error (D10 row 15).
func NewPackageCommand(_ Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "package inspect|verify <file>",
		Short: "Inspect or verify a .omnpkg package file without a gateway",
		Args:  cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fail(c, exitUsage, "missing subcommand: use `omnipus package inspect <file>` or `omnipus package verify <file>`")
			}
			return fail(c, exitUsage, "unknown subcommand %q: use inspect or verify", args[0])
		},
	}
	inspect := &cobra.Command{
		Use:   "inspect <file>",
		Short: "Show a package's manifest (elements, files, checksums, secrets); verifies nothing",
		Args:  exactlyOneFile,
		RunE:  func(c *cobra.Command, args []string) error { return runInspect(c, args[0]) },
	}
	verify := &cobra.Command{
		Use:   "verify <file>",
		Short: "Verify every file in a package against its manifest checksums",
		Args:  exactlyOneFile,
		RunE:  func(c *cobra.Command, args []string) error { return runVerify(c, args[0]) },
	}
	for _, c := range []*cobra.Command{cmd, inspect, verify} {
		setUsageRules(c)
	}
	cmd.AddCommand(inspect, verify)
	return cmd
}
