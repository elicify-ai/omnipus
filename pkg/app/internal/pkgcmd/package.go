package pkgcmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

// runInspect prints the manifest of path (FR-052): it reads only the
// bounded manifest, makes no integrity claim and writes nothing.
func runInspect(cmd *cobra.Command, path string) error {
	f, err := openPackageFile(cmd, path)
	if err != nil {
		return err
	}
	defer f.Close()
	m, err := omnpkg.Inspect(f)
	if err != nil {
		return fail(cmd, exitFailed, "%s: %s", filepath.Base(path), describePackageError(err))
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Package: %s\n", filepath.Base(path))
	fmt.Fprintf(out, "Format: %s version %d, scope %s\n", m.Format, m.FormatVersion, m.Scope)
	fmt.Fprintf(out, "Made by: Omnipus %s on %s\n", m.SourceBuild, m.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintln(out, "Elements:")
	for _, el := range m.Elements {
		fmt.Fprintf(out, "  %s %q (source ID %s, %d files)\n", el.Type, el.Name, el.SourceID, len(el.Files))
	}
	fmt.Fprintln(out, "Files:")
	names := make([]string, 0, len(m.Files))
	for name := range m.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fi := m.Files[name]
		fmt.Fprintf(out, "  %s  %d bytes  sha256 %s\n", name, fi.Size, fi.SHA256)
	}
	if len(m.NeedsSecrets) > 0 {
		fmt.Fprintln(out, "Secrets to re-enter after import:")
		for _, s := range m.NeedsSecrets {
			fmt.Fprintf(out, "  %s (%s, used by %s)\n", s.Name, s.Kind, s.UsedBy)
		}
	}
	fmt.Fprintln(out, "Checksums not verified; run `omnipus package verify` to check them.")
	return nil
}

// runVerify performs every format check on path (FR-052) and prints
// "OK: <n> files verified" or "FAILED: <file>: <reason>".
func runVerify(cmd *cobra.Command, path string) error {
	f, err := openPackageFile(cmd, path)
	if err != nil {
		return err
	}
	defer f.Close()
	report, err := omnpkg.Verify(f)
	if err != nil {
		var pe *omnpkg.PackageError
		if errors.As(err, &pe) && pe.Detail["file"] != "" {
			reason := pe.Detail["reason"]
			if reason == "" {
				reason = pe.Code
			}
			return failLine(cmd, fmt.Sprintf("FAILED: %s: %s (%s)", pe.Detail["file"], reason, pe.Code))
		}
		return failLine(cmd, "FAILED: "+describePackageError(err))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "OK: %d files verified\n", len(report.FilesVerified))
	return nil
}

// describePackageError turns an engine refusal into a plain sentence; the
// offline commands have no gateway message to show, only the engine's code.
func describePackageError(err error) string {
	var pe *omnpkg.PackageError
	if !errors.As(err, &pe) {
		return err.Error()
	}
	switch pe.Code {
	case omnpkg.CodePackageTooNew:
		return fmt.Sprintf("this package was made by a newer Omnipus (format version %s); update Omnipus first (%s)",
			pe.Detail["format_version"], pe.Code)
	case omnpkg.CodeElementSchemaTooNew:
		return fmt.Sprintf("element %s was made by a newer Omnipus (schema %s); update Omnipus first (%s)",
			pe.Detail["element"], pe.Detail["schema"], pe.Code)
	case omnpkg.CodePackageTooLarge:
		return fmt.Sprintf("the package is too large to read safely (limit: %s) (%s)", pe.Detail["limit"], pe.Code)
	}
	return pe.Error()
}
