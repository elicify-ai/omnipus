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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/vaultimport"
)

// NewRecordsCommand returns the `omnipus records` command with subcommands.
func NewRecordsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "records",
		Short: "Manage the vault records control plane (ADR-068)",
	}
	cmd.AddCommand(newImportObsidianCommand())
	cmd.AddCommand(newStampIDsCommand())
	return cmd
}

// resolveVaultLockDir returns the note-lock directory under $OMNIPUS_HOME for
// this vault, so these commands exclude a RUNNING GATEWAY writing the same
// notes rather than only excluding themselves.
//
// A failure is returned, never swallowed into "no locking": both commands
// below write into an operator's own notes, and doing that with no
// cross-process exclusion because the lock directory could not be resolved is
// exactly the silent downgrade that loses somebody's edit.
func resolveVaultLockDir(vaultRoot string) (string, error) {
	lockDir, err := knowledge.LockDirFor(config.OmnipusHomeDir(), vaultRoot)
	if err != nil {
		return "", fmt.Errorf("resolve the write lock for %q: %w", vaultRoot, err)
	}
	return lockDir, nil
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
			lockDir, err := resolveVaultLockDir(vault)
			if err != nil {
				return err
			}
			report, err := vaultimport.RunWithOptions(vault, vaultimport.Options{
				Write:   !dryRun,
				LockDir: lockDir,
			})
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

// newStampIDsCommand returns `omnipus records stamp-ids` — the repair path for
// a vault that was imported before the importer minted identifiers.
func newStampIDsCommand() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "stamp-ids --vault PATH",
		Short: "Add a missing record identifier to every record note in an already-imported vault",
		Long: `stamp-ids gives an ` + "`id:`" + ` to every note in an already-imported vault
that is a record of a declared type and does not have one yet.

Why it exists: inline record editing needs a row to carry BOTH a version
token and an identifier. The token is computed from the note's bytes and is
always there; the identifier is a frontmatter key something has to write.
Vaults imported before the importer minted identifiers have none at all, so
every row arrives with no id, no editor appears, and nothing on screen
explains it. This is the one-shot repair for that state — new imports mint
identifiers themselves, and so does creating a record inside Omnipus.

What it will NOT do:

  - It never touches a note that already has an ` + "`id:`" + ` — so running it
    twice changes nothing the second time.
  - It never stamps a note whose record type it cannot determine. A note with
    no ` + "`type:`" + ` is an ordinary note; a note whose ` + "`type:`" + ` this vault has no
    schema for is REPORTED, not guessed at. An identifier written under a
    guessed type is a wrong answer that looks authoritative.
  - It never re-infers schemas or re-translates .base files. The only change
    it can make to a note is adding one line.

The write is a SPLICE, not a rewrite: the identifier is inserted into the
existing frontmatter and every other byte of the file — comments, key order,
blank lines, quoting style — is preserved exactly. The vault is simultaneously
a human's working notes, and a writer that re-serialises YAML degrades them a
little on every touch.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vault, err := cmd.Flags().GetString("vault")
			if err != nil {
				return err
			}
			if vault == "" {
				return fmt.Errorf("--vault is required")
			}
			lockDir, err := resolveVaultLockDir(vault)
			if err != nil {
				return err
			}
			report, err := vaultimport.StampVault(vault, vaultimport.Options{
				Write:   !dryRun,
				LockDir: lockDir,
			})
			if err != nil {
				return fmt.Errorf("stamping identifiers failed: %w", err)
			}
			report.Render(os.Stdout, 0)
			return nil
		},
	}
	cmd.Flags().String("vault", "", "Path to the already-imported vault to repair (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report what would be stamped without writing anything")
	return cmd
}
