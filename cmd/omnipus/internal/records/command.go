// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package records provides the `omnipus records` subcommand — currently
// just `import-obsidian`, the FR-100 one-shot Obsidian-vault importer.
//
// FR-100 (spec docs/internal/specs/vault-records-spec-2026-08-25.md,
// revision 3): this MUST be an operator/CLI one-shot, never an agent tool.
// FR-103: it MUST NOT appear in the static tool catalog and MUST NOT hold a
// tool-policy entry. This command is that one-shot's only caller.
package records

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/pkg/vaultimport"
)

// NewRecordsCommand returns the `omnipus records` command with subcommands.
func NewRecordsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "records",
		Short: "Manage the vault records control plane (ADR-068)",
	}
	cmd.AddCommand(newImportObsidianCommand())
	return cmd
}

// newImportObsidianCommand returns `omnipus records import-obsidian`.
func newImportObsidianCommand() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "import-obsidian --vault PATH",
		Short: "One-shot import: infer record-type schemas from frontmatter and translate .base files into saved views (FR-100)",
		Long: `import-obsidian bootstraps a vault's .omnipus-vault/ records control plane
from what is already on disk in an Obsidian vault:

  - HALF 1: infers .omnipus-vault/records/<type>.yaml schemas from the
    frontmatter already written on every note (the vault's own 'type:'
    property is the record-type discriminator).
  - HALF 2: translates every .base file's views into
    .omnipus-vault/views/<name>.yaml saved views.

It is a one-shot, operator-run command (FR-100, FR-103) — it is never
registered as an agent tool, and .base files are never read again after this
command exits (FR-102).

Every property this command cannot classify without guessing, and every
.base filter expression it cannot translate, is named in the report rather
than silently dropped or approximated.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vault, err := cmd.Flags().GetString("vault")
			if err != nil {
				return err
			}
			if vault == "" {
				return fmt.Errorf("--vault is required")
			}
			report, err := vaultimport.Run(vault, !dryRun)
			if err != nil {
				return fmt.Errorf("import failed: %w", err)
			}
			report.Render(os.Stdout)
			return nil
		},
	}
	cmd.Flags().String("vault", "", "Path to the Obsidian vault to import (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report what would be written without writing anything")
	return cmd
}
