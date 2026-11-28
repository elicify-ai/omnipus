// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package components implements the operator CLI for office-preview
// components (office-preview spec AD-4, §10):
//
//	omnipus pdf-renderer register <folder>   register an unpacked renderer release
//	omnipus pdf-renderer unregister
//	omnipus office register <folder>         register an official LibreOffice 26.8.x
//	omnipus office unregister
//
// Registration validates the folder, computes the fingerprints and writes the
// manifest into Omnipus's protected data directory ($OMNIPUS_HOME/system/
// components/<kind>/), marked operator-registered with the operator, time
// and path. It refuses a folder any agent can write (writer exclusion). The
// running gateway picks the change up on its next preview call — except on
// Linux under the confined file model, where it needs Omnipus stopped and
// started, and the command says so (office-preview spec §3.8 BR-2.7).
package components

import (
	"fmt"
	"os/user"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/pkg/app/internal"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// confinedRestartHint is printed after a successful registration on Linux
// under the confined file model (office-preview spec §3.8 BR-2.7): the
// running gateway's startup rules cover only components present when it
// started.
const confinedRestartHint = "Stop and start Omnipus (for example with your service manager) to use this component; the in-app restart is not enough in the confined sandbox, which lets the running gateway use only components installed before it started."

// NewPDFRendererCommand returns `omnipus pdf-renderer`.
func NewPDFRendererCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pdf-renderer",
		Short: "Register or unregister the PDF renderer used for document previews",
	}
	cmd.AddCommand(registerCommand(component.KindPDFRenderer,
		"register <folder>",
		"Register an unpacked Omnipus PDF renderer release folder",
		component.RegisterRenderer))
	cmd.AddCommand(unregisterCommand(component.KindPDFRenderer, "PDF renderer"))
	return cmd
}

// NewOfficeCommand returns `omnipus office`.
func NewOfficeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "office",
		Short: "Register or unregister LibreOffice for Office document previews",
	}
	cmd.AddCommand(registerCommand(component.KindOffice,
		"register <folder>",
		"Register an official LibreOffice "+component.SupportedOfficeSeries+"x install (the app or its install folder)",
		component.RegisterOffice))
	cmd.AddCommand(unregisterCommand(component.KindOffice, "Office preview"))
	return cmd
}

func registerCommand(kind component.Kind, use, short string, register func(component.RegisterOptions) (*component.Manifest, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home := internal.GetOmnipusHome()
			cfg, cfgErr := config.LoadConfig(internal.GetConfigPath())
			opts := component.RegisterOptions{
				Home:       home,
				Folder:     args[0],
				Operator:   operatorName(),
				WriteRoots: configuredWriteRoots(home, cfg, cfgErr),
			}
			m, err := register(opts)
			if err != nil {
				return fmt.Errorf("%s not registered: %w", kind, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registered %s %s from %s\n", kind, m.Version, m.Root)
			if needsRestartHint(cfg, cfgErr) {
				fmt.Fprintln(cmd.OutOrStdout(), confinedRestartHint)
			}
			return nil
		},
	}
}

func unregisterCommand(kind component.Kind, label string) *cobra.Command {
	return &cobra.Command{
		Use:   "unregister",
		Short: "Remove the registered " + label,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			removed, err := component.Unregister(internal.GetOmnipusHome(), kind)
			if err != nil {
				return fmt.Errorf("unregister %s: %w", kind, err)
			}
			if removed {
				fmt.Fprintf(cmd.OutOrStdout(), "Removed the registered %s.\n", label)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "No %s was registered.\n", label)
			}
			return nil
		},
	}
}

func operatorName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// configuredWriteRoots gathers the agent write authorities from the home
// folder and configuration for the writer-exclusion check. A configuration
// that cannot be read refuses registration (AD-4 fails closed). Approved
// grants live in the running gateway's memory: the gateway re-checks every
// component against them (and against configuration reloads) before every
// use, so a component registered here is still disabled there if a grant
// covers it.
func configuredWriteRoots(home string, cfg *config.Config, cfgErr error) []component.WriteRoot {
	if cfgErr != nil {
		return []component.WriteRoot{{All: true, Grant: "agent write settings (the configuration could not be read: " + cfgErr.Error() + ")"}}
	}
	return preview.ConfiguredWriteRoots(home, cfg, nil)
}

// needsRestartHint reports whether a successful registration prints
// confinedRestartHint: on Linux when the configured file model is confined,
// resolved as the gateway resolves it (an empty value is confined), and when
// the configuration cannot be read or names an unknown model — a hint that
// is not needed is harmless, a missing one leaves a component that silently
// fails (BR-2.7). macOS and Windows never print it.
func needsRestartHint(cfg *config.Config, cfgErr error) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if cfgErr != nil || cfg == nil {
		return true
	}
	model, err := sandbox.ParseFilesystemModel(cfg.Sandbox.FilesystemModel, sandbox.FilesystemModelConfined)
	return err != nil || model == sandbox.FilesystemModelConfined
}
