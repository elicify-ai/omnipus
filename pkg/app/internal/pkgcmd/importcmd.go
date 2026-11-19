package pkgcmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

// maxUploadBytes is the upload route's body limit (FR-045: 64 MiB); a larger
// file is refused before it is read into memory.
const maxUploadBytes = 64 << 20

// importFlags are the parsed `omnipus import` flags.
type importFlags struct {
	workspace string
	yes       bool
	mode      string
}

// NewImportCommand returns `omnipus import` (FR-051):
// import <file> [--workspace <id|name>] [--yes] [--mode new].
func NewImportCommand(opts Options) *cobra.Command {
	var fl importFlags
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import a .omnpkg package as a new agent (preview, confirm, apply)",
		Args:  exactlyOneFile,
		RunE:  func(c *cobra.Command, args []string) error { return runImport(c, opts, args[0], fl) },
	}
	cmd.Flags().StringVar(&fl.workspace, "workspace", "", "Target workspace ID or unique name (default: the default workspace)")
	cmd.Flags().BoolVar(&fl.yes, "yes", false, "Skip the confirmation prompt")
	cmd.Flags().StringVar(&fl.mode, "mode", "new", "Import mode (only \"new\" is available)")
	setUsageRules(cmd)
	return cmd
}

func runImport(cmd *cobra.Command, opts Options, path string, fl importFlags) error {
	data, err := readUpload(cmd, path)
	if err != nil {
		return err
	}
	client, err := newGatewayClient(opts)
	if err != nil {
		return failErr(cmd, err)
	}
	var ws *gen.Workspace
	if fl.workspace != "" {
		if ws, err = resolveWorkspace(cmd, client, fl.workspace); err != nil {
			return err
		}
	}

	stdout := cmd.OutOrStdout()
	fmt.Fprintf(stdout, "Uploading %s…\n", path)
	var preview gen.PackageImportPreview
	resp, err := client.send(cmd.Context(), http.MethodPost, "/api/v1/packages/import", "application/octet-stream", data)
	if err == nil {
		err = decodeAnswer(resp, http.StatusCreated, &preview)
	}
	if err != nil {
		return failErr(cmd, err)
	}
	printPreview(cmd, &preview)

	if !fl.yes && !confirm(cmd, len(preview.WillAdd), fl.mode) {
		fmt.Fprintln(stdout, "Import cancelled.")
		return &exitError{code: exitFailed, msg: "import cancelled"}
	}

	applyReq := gen.PackageImportApplyRequest{Mode: gen.PackageImportApplyRequestMode(fl.mode)}
	if ws != nil {
		applyReq.WorkspaceId = &ws.Id
	}
	body, err := json.Marshal(applyReq)
	if err != nil {
		return failErr(cmd, err)
	}
	applyPath := "/api/v1/packages/import/" + url.PathEscape(preview.StagingId) + "/apply"
	resp, err = client.send(cmd.Context(), http.MethodPost, applyPath, "application/json", body)
	if err != nil {
		if errors.Is(err, errUnreachable) {
			return failErr(cmd, err)
		}
		return failLine(cmd, fmt.Sprintf("The import may or may not have completed (staging %s). "+
			"Check the agent list for %q before importing again. "+
			"If the gateway stopped, it finishes or rolls back the import when it starts.",
			preview.StagingId, previewName(&preview)))
	}
	var result gen.PackageImportResult
	if err = decodeAnswer(resp, http.StatusOK, &result); err != nil {
		return applyFailure(cmd, err, preview.StagingId)
	}
	printResult(cmd, client, ws, data, &result)
	return nil
}

// readUpload reads the package file; a missing, unreadable or folder input
// is a usage error (FR-053), an over-limit file is refused.
func readUpload(cmd *cobra.Command, path string) ([]byte, error) {
	f, err := openPackageFile(cmd, path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		return nil, fail(cmd, exitUsage, "cannot read %s: %v", path, err)
	}
	if len(data) > maxUploadBytes {
		return nil, fail(cmd, exitFailed, "%s is larger than the 64 MiB upload limit (package_too_large)", path)
	}
	return data, nil
}

// resolveWorkspace resolves --workspace by exact ID, then by unique name
// (letter case ignored) before anything is uploaded (FR-051).
func resolveWorkspace(cmd *cobra.Command, client *gatewayClient, ref string) (*gen.Workspace, error) {
	var list []gen.Workspace
	if err := client.getJSON(cmd.Context(), "/api/v1/workspaces", &list); err != nil {
		return nil, failErr(cmd, err)
	}
	for i := range list {
		if list[i].Id == ref {
			return &list[i], nil
		}
	}
	var matches []*gen.Workspace
	for i := range list {
		if strings.EqualFold(list[i].Name, ref) {
			matches = append(matches, &list[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, fail(cmd, exitFailed, "no workspace with ID or name %q (workspace_not_found)", ref)
	case 1:
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, w := range matches {
		ids[i] = w.Id
	}
	return nil, fail(cmd, exitFailed, "%d workspaces are named %q; use the ID instead: %s",
		len(matches), ref, strings.Join(ids, ", "))
}

// printPreview prints the provisional preview before the prompt (FR-021,
// FR-044: warnings such as unfinished_import_found are shown in full).
func printPreview(cmd *cobra.Command, p *gen.PackageImportPreview) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Preview (provisional; checked again when applied):")
	for _, w := range p.WillAdd {
		if w.Renamed {
			fmt.Fprintf(out, "  %s will be added: %q (renamed from %q, name already in use)\n", w.Type, w.ProposedName, w.SourceName)
		} else {
			fmt.Fprintf(out, "  %s will be added: %q\n", w.Type, w.ProposedName)
		}
	}
	for _, s := range p.SkippedElements {
		fmt.Fprintf(out, "  %s %s will be skipped\n", s.Type, s.SourceId)
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(out, "  warning %s: %s%s\n", w.Code, w.Message, itemsSuffix(w.Items))
	}
	for _, s := range p.NeedsSecrets {
		fmt.Fprintf(out, "  secret to enter again: %s (%s, used by %s)\n", s.Name, s.Kind, s.UsedBy)
	}
}

func itemsSuffix(items *[]string) string {
	if items == nil || len(*items) == 0 {
		return ""
	}
	return " [" + strings.Join(*items, ", ") + "]"
}

// maxAnswerBytes bounds the confirmation answer that is read; any longer
// line cannot be "y" or "yes" and cancels.
const maxAnswerBytes = 1024

// confirm asks "Import <n> agent(s) as <mode>? [y/N]". Only y or yes (any
// letter case) confirms; Enter, anything else, end of input, a cancelled
// command context and a standard input file that is not a real terminal
// (a pipe, a regular file, /dev/null, /dev/zero) cancel. An injected
// non-file reader is read as the typed answer.
func confirm(cmd *cobra.Command, n int, mode string) bool {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
		fmt.Fprintln(cmd.ErrOrStderr(), "No terminal to confirm on; pass --yes to import without the prompt.")
		return false
	}
	noun := "agents"
	if n == 1 {
		noun = "agent"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Import %d %s as %s? [y/N] ", n, noun, mode)

	// The read runs aside so a cancelled command context ends the prompt; a
	// blocked terminal read is abandoned and ends with the process.
	type answer struct {
		line string
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		line, err := bufio.NewReader(io.LimitReader(in, maxAnswerBytes)).ReadString('\n')
		got <- answer{line, err}
	}()
	select {
	case <-cmd.Context().Done():
		fmt.Fprintln(cmd.OutOrStdout())
		return false
	case a := <-got:
		if a.err != nil && a.line == "" {
			fmt.Fprintln(cmd.OutOrStdout())
			return false
		}
		reply := strings.ToLower(strings.TrimSpace(a.line))
		return reply == "y" || reply == "yes"
	}
}

func previewName(p *gen.PackageImportPreview) string {
	if len(p.WillAdd) == 0 {
		return ""
	}
	return p.WillAdd[0].ProposedName
}

// applyFailure prints the apply refusal: the rollback texts of the CLI rows
// for import_rolled_back and import_rollback_incomplete (FR-037), the
// error line otherwise.
func applyFailure(cmd *cobra.Command, err error, stagingID string) error {
	var ae *apiError
	if !errors.As(err, &ae) {
		return failErr(cmd, err)
	}
	switch ae.code() {
	case omnpkg.CodeImportRolledBack:
		reason := ae.body.Error
		if reason == "" {
			reason = ae.code()
		}
		return failLine(cmd, fmt.Sprintf("Import failed; nothing was imported: %s.", reason))
	case omnpkg.CodeImportRollbackIncomplete:
		staging := ae.detail("staging_id")
		if staging == "" {
			staging = stagingID
		}
		return failLine(cmd, fmt.Sprintf("Import failed and could not be fully undone. The agent was never visible. "+
			"Agent %s, staging %s. Restart the gateway to finish the clean-up; "+
			"see 'Recover from a failed import' in the CLI guide.", ae.detail("agent_id"), staging))
	}
	return failErr(cmd, err)
}

// printResult prints the outcome lines of a committed import: the result
// line (or the activation-pending guidance, FR-036), the delegation line for
// a worker (FR-036b) and any warnings. Exit 0 in every case.
func printResult(cmd *cobra.Command, client *gatewayClient, ws *gen.Workspace, pkg []byte, r *gen.PackageImportResult) {
	out := cmd.OutOrStdout()
	for _, c := range r.Created {
		if r.Activation == gen.PackageImportResultActivationPending {
			fmt.Fprintf(out, "Imported %q as %s, but the gateway did not confirm that it is live yet. "+
				"Restart the gateway, or wait and check the agent list, before chatting with it.\n", c.Name, c.NewId)
		} else {
			fmt.Fprintf(out, "Imported %q as %s into workspace %q\n", c.Name, c.NewId, workspaceName(cmd, client, ws, c.WorkspaceId))
		}
		if isWorker(cmd, pkg, c.SourceId) {
			fmt.Fprintf(out, "%q is a worker: it is reached by delegation. Draw a delegation line to it from a Main agent in the Team graph.\n", c.Name)
		}
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(out, "warning %s: %s%s\n", w.Code, w.Message, itemsSuffix(w.Items))
	}
	for _, s := range r.NeedsSecrets {
		fmt.Fprintf(out, "secret to enter again: %s (%s, used by %s)\n", s.Name, s.Kind, s.UsedBy)
	}
}

// workspaceName names the joined workspace: the resolved --workspace, else a
// lookup of the ID the gateway chose (the default workspace). A failed lookup
// is reported and the ID is shown instead.
func workspaceName(cmd *cobra.Command, client *gatewayClient, ws *gen.Workspace, id string) string {
	if ws != nil && ws.Id == id {
		return ws.Name
	}
	var list []gen.Workspace
	if err := client.getJSON(cmd.Context(), "/api/v1/workspaces", &list); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: could not look up the name of workspace %s: %v\n", id, err)
		return id
	}
	for _, w := range list {
		if w.Id == id {
			return w.Name
		}
	}
	return id
}

// isWorker reports whether the package's record for sourceID is a worker
// (not a chat target). The result's created[].type is the element type, so
// the agent type comes from the uploaded package itself. A package that
// cannot be read here (the gateway accepted it) is reported, not fatal: the
// import has already succeeded.
func isWorker(cmd *cobra.Command, pkg []byte, sourceID string) bool {
	_, payload, err := omnpkg.ReadPackage(bytes.NewReader(pkg))
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: could not read the agent type from the package: %s\n", describePackageError(err))
		return false
	}
	var rec struct { // not-wire-format: reads only the type of the package's own agent.json record
		Type config.AgentType `json:"type"`
	}
	if err = json.Unmarshal(payload[omnpkg.AgentsPrefix+sourceID+"/"+omnpkg.AgentRecordName], &rec); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: could not read the agent type from the package: %v\n", err)
		return false
	}
	return config.AgentConfig{Type: rec.Type}.IsWorker()
}
