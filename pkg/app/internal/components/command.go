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
// running gateway picks the change up on its next preview call.
package components

import (
	"fmt"
	"os"
	"os/user"

	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/pkg/app/internal"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
)

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
			opts := component.RegisterOptions{
				Home:       home,
				Folder:     args[0],
				Operator:   operatorName(),
				WriteRoots: configuredWriteRoots(home),
			}
			m, err := register(opts)
			if err != nil {
				return fmt.Errorf("%s not registered: %w", kind, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registered %s %s from %s\n", kind, m.Version, m.Root)
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

// configuredWriteRoots gathers the agent write roots from configuration for
// the writer-exclusion check. A config that cannot be read still yields the
// home-derived roots, never an empty list.
func configuredWriteRoots(home string) []component.WriteRoot {
	cfg, err := config.LoadConfig(internal.GetConfigPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: config unreadable; checking only the Omnipus home folders:", err)
		cfg = nil
	}
	return preview.ConfiguredWriteRoots(home, cfg)
}
