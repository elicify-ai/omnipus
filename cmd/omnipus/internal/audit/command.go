// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package audit provides the `omnipus audit` subcommand. Today it only
// exposes `omnipus audit verify` (v0.2 #155) — chain integrity check for
// the tamper-evident audit log.
//
// The verify subcommand walks every audit file under
// $OMNIPUS_HOME/system/ (audit.jsonl + audit-*.jsonl), threads the HMAC
// chain across rotation boundaries, and reports the first broken link or
// "intact" on success.
//
// Exit codes:
//
//	0 — chain intact OR no audit files present (nothing to verify)
//	1 — chain broken (tampering detected)
//	2 — operational error (no master key, dir unreadable, etc.)
//
// The non-zero exit codes are stable for shell scripts and monitoring.
// Use `omnipus audit verify --json` for machine-readable output.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	auditpkg "github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// NewAuditCommand returns the `omnipus audit` command tree.
func NewAuditCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect and verify the security audit log",
		Long: "Inspect and verify the security audit log written to $OMNIPUS_HOME/system/audit.jsonl.\n\n" +
			"v0.2 #155 introduced an HMAC chain so truncation or surgical-rewrite of the\n" +
			"audit log is detectable: each entry carries an `hmac` field computed over\n" +
			"the previous entry's HMAC plus the entry's canonical content. The chain key\n" +
			"is derived from the master key via HKDF-SHA256 and held only in process\n" +
			"memory. `omnipus audit verify` re-derives the key, walks the log files in\n" +
			"chronological order, and reports the first broken link.",
	}
	cmd.AddCommand(newVerifyCommand())
	return cmd
}

// newVerifyCommand returns `omnipus audit verify`.
func newVerifyCommand() *cobra.Command {
	var (
		dirFlag    string
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Walk the audit log and verify the HMAC chain",
		Long: "Walk every audit file under $OMNIPUS_HOME/system/ (or the directory passed via\n" +
			"--dir), recompute each entry's HMAC, and report the first broken link.\n\n" +
			"Requires the master key to be available via the same channels as the\n" +
			"gateway: OMNIPUS_MASTER_KEY env var, OMNIPUS_KEY_FILE env var,\n" +
			"$OMNIPUS_HOME/master.key, or interactive passphrase prompt.\n\n" +
			"Exit codes: 0 = intact, 1 = tamper detected, 2 = operational error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFlag
			if dir == "" {
				dir = filepath.Join(internal.GetOmnipusHome(), "system")
			}
			return runVerify(cmd.Context(), dir, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&dirFlag, "dir", "",
		"Audit directory (defaults to $OMNIPUS_HOME/system)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false,
		"Emit machine-readable JSON instead of human text")
	return cmd
}

// runVerify is the verify subcommand entry point. Returns:
//
//   - nil on intact chain.
//   - a *cobra.ErrUsage-shaped error on operational failure (printed by cobra).
//   - a sentinel error wrapped with code 1 on tamper detection.
//
// Cobra uses RunE's error to produce a non-zero exit; we wrap so the
// shell sees the documented exit codes (0/1/2) regardless of cobra's
// default mapping.
func runVerify(ctx context.Context, dir string, jsonOutput bool) error {
	// Sanity check: directory must exist.
	if _, err := os.Stat(dir); err != nil {
		return exitErr(2, fmt.Errorf("audit verify: directory not accessible: %w", err))
	}

	// Open and unlock the credential store the same way `omnipus credentials`
	// does. We need the master key to derive the audit chain key — it is
	// NOT stored on disk by design.
	credPath := filepath.Join(internal.GetOmnipusHome(), "credentials.json")
	store := credentials.NewStore(credPath)
	if err := credentials.Unlock(store); err != nil {
		return exitErr(2, fmt.Errorf("audit verify: credential store: %w", err))
	}
	if store.IsLocked() {
		return exitErr(2, fmt.Errorf(
			"audit verify: credential store is locked — provide a master key via %s, %s, or interactive passphrase",
			credentials.EnvMasterKey, credentials.EnvKeyFile,
		))
	}

	chainKey, err := store.DeriveSubkey(auditpkg.AuditChainKeyInfo)
	if err != nil {
		return exitErr(2, fmt.Errorf("audit verify: derive chain key: %w", err))
	}

	res, err := auditpkg.VerifyDir(ctx, dir, chainKey)
	if err != nil {
		return exitErr(2, fmt.Errorf("audit verify: walk: %w", err))
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	} else {
		fmt.Println(res.String())
	}

	if !res.Valid {
		return exitErr(1, fmt.Errorf("audit chain broken at %s line %d: %s",
			res.FailedFile, res.BrokenAt, res.Reason))
	}
	return nil
}

// exitErr prints err to stderr and triggers an os.Exit with the
// specified code. Used by runVerify to honor the documented 0/1/2
// contract.
func exitErr(code int, err error) error {
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
	}
	os.Exit(code)
	return err // unreachable
}
