package pkgcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

// freeTextNotice is printed on every export (Security and User Promises; L6).
const freeTextNotice = "SOUL.md and AGENT.md are copied as they are; check them for pasted secrets before sharing."

// NewExportCommand returns `omnipus export` (FR-050):
// export --agent <id|name> -o <file> [--force].
func NewExportCommand(opts Options) *cobra.Command {
	var agent, out string
	var force bool
	cmd := &cobra.Command{
		Use:   "export --agent <id|name> -o <file>",
		Short: "Export one custom agent to a .omnpkg package file",
		Args: func(c *cobra.Command, args []string) error {
			if len(args) != 0 {
				return fail(c, exitUsage, "unexpected argument %q (usage: %s)", args[0], c.UseLine())
			}
			return nil
		},
		RunE: func(c *cobra.Command, _ []string) error {
			if agent == "" {
				return fail(c, exitUsage, "missing --agent <id|name>")
			}
			if out == "" {
				return fail(c, exitUsage, "missing -o <file>")
			}
			return runExport(c, opts, agent, out, force)
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID or name to export (required)")
	cmd.Flags().StringVarP(&out, "out", "o", "", "Output package file path (required)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing output file")
	setUsageRules(cmd)
	return cmd
}

func runExport(cmd *cobra.Command, opts Options, agentRef, out string, force bool) error {
	if !force {
		if _, err := os.Lstat(out); err == nil {
			return fail(cmd, exitFailed, "%s already exists; use --force to replace it", out)
		}
	}
	client, err := newGatewayClient(opts)
	if err != nil {
		return failErr(cmd, err)
	}
	var roster []gen.Agent
	if err = client.getJSON(cmd.Context(), "/api/v1/agents", &roster); err != nil {
		return failErr(cmd, err)
	}
	target, err := resolveAgent(roster, agentRef)
	if err != nil {
		return fail(cmd, exitFailed, "%v", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Exporting %s…\n", target.Name)
	reqBody, err := json.Marshal(gen.PackageExportRequest{AgentIds: []string{target.Id}})
	if err != nil {
		return failErr(cmd, err)
	}
	resp, err := client.send(cmd.Context(), http.MethodPost, "/api/v1/packages/export", "application/json", reqBody)
	if err != nil {
		return failErr(cmd, err)
	}
	if resp.StatusCode != http.StatusOK {
		return failErr(cmd, decodeAnswer(resp, http.StatusOK, nil))
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fail(cmd, exitFailed, "reading the package from the gateway: %v", err)
	}
	m, err := omnpkg.ReadManifest(bytes.NewReader(data))
	if err != nil {
		return fail(cmd, exitFailed, "the gateway sent an unreadable package: %s", describePackageError(err))
	}
	if err = writeAtomic(out, data); err != nil {
		return fail(cmd, exitFailed, "writing %s: %v", out, err)
	}

	stdout := cmd.OutOrStdout()
	fmt.Fprintf(stdout, "Exported %q (%s, %d files, %s) to %s\n",
		target.Name, countAgents(m.Counts[omnpkg.ElementTypeAgent]), len(m.Files), humanSize(len(data)), out)
	fmt.Fprintln(stdout, freeTextNotice)
	return nil
}

// resolveAgent picks the export target: exact ID first, then the name with
// letter case ignored; unknown and ambiguous references are refused, the
// latter listing the matching IDs (FR-050).
func resolveAgent(roster []gen.Agent, ref string) (gen.Agent, error) {
	for _, a := range roster {
		if a.Id == ref {
			return a, nil
		}
	}
	var matches []gen.Agent
	for _, a := range roster {
		if strings.EqualFold(a.Name, ref) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 0:
		return gen.Agent{}, fmt.Errorf("no agent with ID or name %q (agent_not_found)", ref)
	case 1:
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, a := range matches {
		ids[i] = a.Id
	}
	return gen.Agent{}, fmt.Errorf("%d agents are named %q; export by ID instead: %s", len(matches), ref, strings.Join(ids, ", "))
}

// writeAtomic writes data to a temporary file beside path and renames it
// into place, so path is either untouched or complete (FR-017, FR-050).
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err == nil {
		err = os.Rename(tmpName, path)
	}
	if err != nil {
		if rmErr := os.Remove(tmpName); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("removing the temporary file %s: %w", tmpName, rmErr))
		}
	}
	return err
}

func countAgents(n int) string {
	if n == 1 {
		return "1 agent"
	}
	return fmt.Sprintf("%d agents", n)
}

// humanSize renders a byte count as B, KB or MB (1 KB = 1024 bytes).
func humanSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}
