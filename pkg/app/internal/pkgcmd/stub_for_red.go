// stub_for_red.go — DELETE when GREEN lands.
//
// RED scaffold for epic #1094 slice S1a, task T-6 (the export/import CLI).
// This file exists only so the RED test files in this package compile: it
// declares the command API the tests need (flag surface, exit-code contract,
// dependency seams) and returns errors.New("not implemented") from every
// behaviour. It implements nothing.
//
// GREEN (backend-lead) deletes this file and implements command.go with the
// same exported surface:
//
//	NewExportCommand(Options) *cobra.Command   — omnipus export
//	NewImportCommand(Options) *cobra.Command   — omnipus import
//	NewPackageCommand(Options) *cobra.Command  — omnipus package inspect|verify
//
// The flags below are the spec's declared surface (export-import-s1-spec.md r7,
// FR-050/FR-051, Dataset D10); declaring them here is compiling the spec, not
// implementing it — every RunE still refuses with "not implemented", so the
// RED tests fail on assertion, never on a missing symbol.
//
// not-wire-format: no wire types are declared here; the fakes' JSON is decoded
// with encoding/json into maps in the tests, and GREEN must use the generated
// contract types (Hard Constraint #8, FR-060).

package pkgcmd

import (
	"errors"

	"github.com/spf13/cobra"
)

// ExitCoder is the exit-code contract of every pkgcmd command (FR-053):
// a RunE failure that carries the process exit code it maps to.
// 0 = success (warnings included), 1 = refused or failed, 2 = usage error.
// Production wiring maps ExitCode() to os.Exit; the RED tests read it from the
// error returned by cobra's Execute.
type ExitCoder interface {
	error
	ExitCode() int
}

// Options carries the dependency seams of the package commands.
//
// Zero value = production defaults: the gateway address comes from config, the
// bearer token from clitoken.LoadCLIToken(Home), and the auto-start step is the
// shared one reused from pkg/app/app.go::autoStartAndRetry (FR-054). The tests
// set the fields to point the commands at an httptest fake gateway.
type Options struct {
	// Home is the Omnipus data dir used only to load cli.token (FR-054/FR-055).
	// Empty = internal.GetOmnipusHome().
	Home string

	// GatewayURL is the gateway base URL, e.g. "http://127.0.0.1:5000".
	// Empty = "http://localhost:<config gateway port>".
	GatewayURL string

	// Token overrides the cli.token bearer (FR-054). Empty = LoadCLIToken(Home).
	Token string

	// AutoStart replaces the shared auto-start step (the seam the tests use;
	// nil = the shared pkg/app auto-start path, called once, then one retry).
	AutoStart func() error
}

// errNotImplemented is what every stub behaviour returns, so RED failures name
// the missing feature instead of an undefined symbol.
func errNotImplemented(what string) error {
	return errors.New("not implemented: " + what + " — RED stub (stub_for_red.go), required by #1094 S1a T-6")
}

// NewExportCommand returns `omnipus export` (FR-050):
// export --agent <id|name> -o <file> [--force].
func NewExportCommand(_ Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export --agent <id|name> -o <file>",
		Short: "Export one custom agent to a .omnpkg package file (#1094 S1)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errNotImplemented("omnipus export")
		},
	}
	cmd.Flags().String("agent", "", "Agent ID or name to export (required)")
	cmd.Flags().StringP("out", "o", "", "Output package file path (required)")
	cmd.Flags().Bool("force", false, "Overwrite an existing output file")
	return cmd
}

// NewImportCommand returns `omnipus import` (FR-051):
// import <file> [--workspace <id|name>] [--yes] [--mode new].
func NewImportCommand(_ Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import a .omnpkg package as a new agent (preview, confirm, apply) (#1094 S1)",
		Args:  cobra.ExactArgs(1), // the package file (D10 rows 8–9: absent/missing → usage error)
		RunE: func(*cobra.Command, []string) error {
			return errNotImplemented("omnipus import")
		},
	}
	cmd.Flags().String("workspace", "", "Target workspace ID or unique name (default: the default workspace)")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	cmd.Flags().String("mode", "new", "Import mode (S1 accepts only \"new\")")
	return cmd
}

// NewPackageCommand returns `omnipus package` with the offline subcommands
// `inspect` and `verify` (FR-052): neither needs a gateway nor writes anything.
// An unknown subcommand (D10 row 15) is a usage error (exit 2, FR-053).
func NewPackageCommand(_ Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "package",
		Short: "Inspect or verify a .omnpkg package file without a gateway (#1094 S1)",
		RunE: func(_ *cobra.Command, args []string) error {
			return errNotImplemented("omnipus package " + unknownSubcommandName(args))
		},
	}
	inspect := &cobra.Command{
		Use:   "inspect <file>",
		Short: "Show a package's manifest (format, elements, files, checksums, secrets) — reads only the manifest, verifies nothing",
		Args:  cobra.ExactArgs(1), // the package file (D10 row 10: a folder is a usage error)
		RunE: func(*cobra.Command, []string) error {
			return errNotImplemented("omnipus package inspect")
		},
	}
	verify := &cobra.Command{
		Use:   "verify <file>",
		Short: "Verify every file in a package against its manifest checksums",
		Args:  cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error {
			return errNotImplemented("omnipus package verify")
		},
	}
	cmd.AddCommand(inspect, verify)
	return cmd
}

// unknownSubcommandName names the first extra arg for the stub's not-implemented
// error, so a RED failure of D10 row 15 reads as a missing implementation.
func unknownSubcommandName(args []string) string {
	if len(args) == 0 {
		return "(no subcommand)"
	}
	return args[0]
}
