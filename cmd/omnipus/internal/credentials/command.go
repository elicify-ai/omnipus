// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package credentials provides the `omnipus credentials` subcommand.
package credentials

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/cmd/omnipus/internal"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// NewCredentialsCommand returns the `omnipus credentials` command with subcommands.
func NewCredentialsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "credentials",
		Aliases: []string{"creds"},
		Short:   "Manage encrypted credentials",
	}
	cmd.AddCommand(
		newSetCommand(),
		newListCommand(),
		newDeleteCommand(),
		newRotateCommand(),
	)
	return cmd
}

// openStore opens and unlocks the credential store for the current home directory.
func openStore() (*credentials.Store, error) {
	home := internal.GetOmnipusHome()
	storePath := home + "/credentials.json"

	store := credentials.NewStore(storePath)
	if err := credentials.Unlock(store); err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	if store.IsLocked() {
		return nil, fmt.Errorf(
			"credential store is locked — provide a master key via %s, %s, or interactive passphrase",
			credentials.EnvMasterKey,
			credentials.EnvKeyFile,
		)
	}
	return store, nil
}

// newSetCommand returns `omnipus credentials set NAME VALUE`.
func newSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set NAME VALUE",
		Short: "Encrypt and store a credential",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			value := args[1]
			if name == "" {
				return fmt.Errorf("credential name must not be empty")
			}

			store, err := openStore()
			if err != nil {
				return err
			}
			if err := store.Set(name, value); err != nil {
				return fmt.Errorf("failed to store credential: %w", err)
			}
			fmt.Printf("Credential %q stored.\n", name)
			return nil
		},
	}
}

// newListCommand returns `omnipus credentials list`.
func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List credential names (values are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			names, err := store.List()
			if err != nil {
				return fmt.Errorf("failed to list credentials: %w", err)
			}
			if len(names) == 0 {
				fmt.Println("No credentials stored.")
				return nil
			}
			for _, name := range names {
				fmt.Println(name)
			}
			return nil
		},
	}
}

// newDeleteCommand returns `omnipus credentials delete NAME`.
func newDeleteCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Remove a credential from the store",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])

			if !force {
				fmt.Printf("Delete credential %q? This cannot be undone. [y/N]: ", name)
				var confirm string
				fmt.Fscan(os.Stdin, &confirm)
				if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			store, err := openStore()
			if err != nil {
				return err
			}
			if err := store.Delete(name); err != nil {
				return fmt.Errorf("failed to delete credential: %w", err)
			}
			fmt.Printf("Credential %q deleted.\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")
	return cmd
}

// newRotateCommand returns `omnipus credentials rotate`.
func newRotateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate",
		Short: "Re-encrypt all credentials with a new passphrase",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}

			fmt.Println("Enter current passphrase to verify existing credentials, then a new passphrase.")
			newPass, err := credentials.PromptNewPassphrase()
			if err != nil {
				return fmt.Errorf("credentials rotate: %w", err)
			}

			if err := store.RotateWithPassphrase(newPass); err != nil {
				return fmt.Errorf("rotation failed: %w", err)
			}
			fmt.Println("All credentials re-encrypted with new passphrase.")
			return nil
		},
	}
}
