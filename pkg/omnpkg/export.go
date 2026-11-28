package omnpkg

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Selection names what to export (S1 accepts exactly one agent, FR-010).
type Selection struct {
	AgentIDs []string
}

// ExportSource is the store edge the gateway fills (ADR D1): the engine never
// reads the data folder around the stores. An error the source returns as a
// *PackageError (for example agent_not_found) is passed through unchanged.
type ExportSource interface {
	// AgentRecord returns the stored record JSON for id.
	AgentRecord(id string) ([]byte, error)
	// AgentHomeDir returns the agent's home folder (holds SOUL.md / AGENT.md).
	AgentHomeDir(id string) (string, error)
	// IsCoreAgent reports built-in / system / shipped-subagent agents (D4).
	IsCoreAgent(id string) bool
}

// exportRemovedFields are the top-level record fields Field mapping removes at
// export (home path, timestamps, lock flag, skill grants).
var exportRemovedFields = []string{"workspace", "created_at", "updated_at", "locked", "skill_grants", "programs"}

// exportRemovedSubagentFields are the retired/unused subagents fields (ADR A3).
var exportRemovedSubagentFields = []string{"allow_agents", "model"}

// executorRuntimeFields are the host-specific, possibly secret executor
// values that never travel: removed at export, ignored at import (Field
// mapping rows subagents.executor.*; spec D6 amended).
var executorRuntimeFields = []string{"cli_path", "cli_args", "env_overrides"}

// NeedsSecretKindExecutorEnv is the needs_secrets kind of an external
// worker's removed environment override (FR-014).
const NeedsSecretKindExecutorEnv = "executor_env"

// NeedsSecretKindProgram is the needs_secrets kind of a connected program
// switched on for the exported agent (connected programs FR-022). Name is
// the program entry id. Neither the token nor the switch travels: the
// shared login is entered on the target under Settings -> Programs.
const NeedsSecretKindProgram = "program"

// Export packs the selected agent into w (manifest first, FR-001/FR-002,
// Field mapping Export column, FR-018 bounded source reads). Nothing is
// written to w unless the whole package was built.
func Export(ctx context.Context, src ExportSource, sel Selection, w io.Writer) error {
	if len(sel.AgentIDs) != 1 {
		return newErr(CodeExportSelectionInvalid, "reason", "exactly one agent must be selected")
	}
	id := sel.AgentIDs[0]
	if src.IsCoreAgent(id) {
		return newErr(CodeCoreAgentNotExportable, "agent_id", id)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	agent, err := collectAgentFiles(src, id)
	if err != nil {
		return err
	}
	pkg, err := buildPackage(id, agent.name, agent.files, agent.needsSecrets(id), time.Now().UTC().Truncate(time.Second))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := w.Write(pkg); err != nil {
		return fmt.Errorf("omnpkg: write package: %w", err)
	}
	return nil
}

// packagedFile is one payload entry in package order.
type packagedFile struct {
	path string
	body []byte
}

// exportedAgent is one agent's captured package content.
type exportedAgent struct {
	files    []packagedFile
	name     string
	envNames []string // removed executor environment-override names (FR-014)
	programs []string // switched-on connected programs, declared not carried (FR-022)
	// stored is the record as the store returned it, before cleaning. Only
	// the export review reads it, to say which removed settings were present
	// (names only; B1 left_out rules). It never enters the package.
	stored []byte
	kind   string // the review's agent kind (main, worker, external_worker)
}

// needsSecrets lists what the target must supply, in package order: the
// removed executor environment-override names (FR-014), then the switched-on
// connected programs (#1960 FR-022). Export and the export review share it, so
// the review shows exactly the package's needs_secrets.
func (a *exportedAgent) needsSecrets(id string) []NeedsSecret {
	// Non-nil even when empty (the package writes needs_secrets as []);
	// append grows it for the programs.
	secrets := make([]NeedsSecret, 0, len(a.envNames))
	for _, n := range a.envNames {
		secrets = append(secrets, NeedsSecret{Name: n, UsedBy: id, Kind: NeedsSecretKindExecutorEnv})
	}
	for _, p := range a.programs {
		secrets = append(secrets, NeedsSecret{Name: p, UsedBy: id, Kind: NeedsSecretKindProgram})
	}
	return secrets
}

// collectAgentFiles reads and cleans the record and captures SOUL.md and
// AGENT.md, applying the export refusals (FR-010, FR-011, FR-018, FR-019).
// An external worker has no native prompt: its SOUL.md is never packaged
// (Field mapping row SOUL.md) and its runtime values are removed.
func collectAgentFiles(src ExportSource, id string) (*exportedAgent, error) {
	raw, err := src.AgentRecord(id)
	if err != nil {
		return nil, sourceErr("read agent record", err)
	}
	v, err := parseRecordView(raw)
	if err != nil {
		return nil, fmt.Errorf("omnpkg: agent %s: stored record is not valid JSON: %w", id, err)
	}
	ac := v.asAgentConfig()
	switch {
	case v.isCoreType():
		return nil, newErr(CodeCoreAgentNotExportable, "agent_id", id)
	case ac.IsWorker() && blank(v.Description):
		return nil, fieldErr(CodeElementNotCreatable, "description", "a worker needs a description")
	}
	external := ac.IsExternalCLIWorker()
	home, err := src.AgentHomeDir(id)
	if err != nil {
		return nil, sourceErr("resolve agent home", err)
	}
	var soul []byte
	if !external {
		var soulPresent bool
		soul, soulPresent, err = readSourceFile(home, AgentSoulName)
		if err != nil {
			return nil, err
		}
		if !soulPresent || blank(string(soul)) {
			return nil, newErr(CodeAgentPromptMissing, "agent_id", id)
		}
	}
	agentMD, mdPresent, err := readSourceFile(home, AgentInstructionsNm)
	if err != nil {
		return nil, err
	}
	record, envNames, err := cleanRecordForExport(raw)
	if err != nil {
		return nil, fmt.Errorf("omnpkg: agent %s: %w", id, err)
	}
	files := []packagedFile{{path: agentRecordPath(id), body: record}}
	if !external {
		files = append(files, packagedFile{path: agentSoulPath(id), body: soul})
	}
	if mdPresent {
		files = append(files, packagedFile{path: agentInstructionsPath(id), body: agentMD})
	}
	kind := PlanKindMain
	switch {
	case external:
		kind = PlanKindExternalWorker
	case ac.IsWorker():
		kind = PlanKindWorker
	}
	return &exportedAgent{files: files, name: v.Name, envNames: envNames, programs: exportedPrograms(v.Programs),
		stored: raw, kind: kind}, nil
}

// sourceErr passes a coded source error through and wraps any other.
func sourceErr(what string, err error) error {
	var pe *PackageError
	if errors.As(err, &pe) {
		return err
	}
	return fmt.Errorf("omnpkg: %s: %w", what, err)
}

// readSourceFile reads home/name without following links, as a regular file
// only, bounded to MaxAgentFileBytes (FR-018). An absent file reports
// present=false; every other problem refuses with agent_file_unreadable.
// name is one of the fixed file names (SOUL.md, AGENT.md): a name that is not
// a plain local path is refused before anything is opened.
func readSourceFile(home, name string) ([]byte, bool, error) {
	unreadable := func(check string) error {
		return newErr(CodeAgentFileUnreadable, "file", name, "check", check)
	}
	if !filepath.IsLocal(name) {
		return nil, false, unreadable(CheckUnreadable)
	}
	path := filepath.Join(home, name)
	before, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, unreadable(CheckUnreadable)
	}
	if before.Mode()&fs.ModeSymlink != 0 {
		return nil, false, unreadable(CheckLink)
	}
	if !before.Mode().IsRegular() {
		return nil, false, unreadable(CheckNotRegular)
	}
	// A file nobody may read (mode 000) is refused the same way whether or
	// not the gateway runs with privileges that would bypass the mode.
	if before.Mode().Perm()&0o444 == 0 {
		return nil, false, unreadable(CheckUnreadable)
	}
	if before.Size() > MaxAgentFileBytes {
		return nil, false, unreadable(CheckTooLarge)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, unreadable(CheckUnreadable)
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return nil, false, unreadable(CheckUnreadable)
	}
	// The path was swapped between the check and the open (for example for a
	// link): refuse rather than read what it points at now.
	if !os.SameFile(before, after) {
		return nil, false, unreadable(CheckLink)
	}
	body, err := io.ReadAll(io.LimitReader(f, MaxAgentFileBytes+1))
	if err != nil {
		return nil, false, unreadable(CheckUnreadable)
	}
	if len(body) > MaxAgentFileBytes {
		return nil, false, unreadable(CheckTooLarge)
	}
	return body, true, nil
}

// cleanRecordForExport removes the fields Field mapping's Export column
// removes and keeps every other stored value as it is. It also returns the
// names of the removed executor environment overrides, sorted (FR-014).
func cleanRecordForExport(raw []byte) ([]byte, []string, error) {
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, nil, fmt.Errorf("stored record is not a JSON object: %w", err)
	}
	for _, k := range exportRemovedFields {
		delete(rec, k)
	}
	var envNames []string
	if subRaw, ok := rec["subagents"]; ok && string(subRaw) != "null" {
		var sub map[string]json.RawMessage
		if err := json.Unmarshal(subRaw, &sub); err != nil {
			return nil, nil, fmt.Errorf("stored subagents is not a JSON object: %w", err)
		}
		for _, k := range exportRemovedSubagentFields {
			delete(sub, k)
		}
		if execRaw, ok := sub["executor"]; ok && string(execRaw) != "null" {
			cleaned, names, err := cleanExecutorForExport(execRaw)
			if err != nil {
				return nil, nil, err
			}
			sub["executor"], envNames = cleaned, names
		}
		cleaned, err := json.Marshal(sub)
		if err != nil {
			return nil, nil, fmt.Errorf("encode subagents: %w", err)
		}
		rec["subagents"] = cleaned
	}
	out, err := json.Marshal(rec)
	return out, envNames, err
}

// cleanExecutorForExport removes the executor's runtime values (program path,
// environment overrides, CLI arguments; Field mapping executor rows) and
// returns the override names, sorted. Values never leave this function.
func cleanExecutorForExport(raw json.RawMessage) (json.RawMessage, []string, error) {
	var exec map[string]json.RawMessage
	if err := json.Unmarshal(raw, &exec); err != nil {
		return nil, nil, fmt.Errorf("stored subagents.executor is not a JSON object: %w", err)
	}
	var names []string
	if envRaw, ok := exec["env_overrides"]; ok && string(envRaw) != "null" {
		var env map[string]json.RawMessage
		if err := json.Unmarshal(envRaw, &env); err != nil {
			return nil, nil, fmt.Errorf("stored subagents.executor.env_overrides is not a JSON object: %w", err)
		}
		for name := range env {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	for _, k := range executorRuntimeFields {
		delete(exec, k)
	}
	cleaned, err := json.Marshal(exec)
	if err != nil {
		return nil, nil, fmt.Errorf("encode subagents.executor: %w", err)
	}
	return cleaned, names, nil
}

// buildPackage writes manifest.json first, then the payload files in order,
// as a tar compressed with a zstd window of at most MaxDecoderWindow (FR-008).
// Hashes and sizes come from the same captured bytes that are packaged.
func buildPackage(id, name string, files []packagedFile, secrets []NeedsSecret, createdAt time.Time) ([]byte, error) {
	m := Manifest{
		Format:        FormatName,
		FormatVersion: FormatVersion,
		Scope:         ScopeCustom,
		Elements: []ManifestElement{{
			Type:     ElementTypeAgent,
			Schema:   AgentSchemaVersion,
			SourceID: id,
			Name:     name,
		}},
		Counts:       map[string]int{ElementTypeAgent: 1},
		Files:        make(map[string]FileInfo, len(files)),
		SourceBuild:  config.GetVersion(),
		CreatedAt:    createdAt,
		NeedsSecrets: secrets,
	}
	for _, f := range files {
		sum := sha256.Sum256(f.body)
		m.Files[f.path] = FileInfo{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(f.body))}
		m.Elements[0].Files = append(m.Elements[0].Files, f.path)
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("omnpkg: encode manifest: %w", err)
	}
	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out, zstd.WithWindowSize(MaxDecoderWindow), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("omnpkg: open zstd encoder: %w", err)
	}
	tw := tar.NewWriter(zw)
	entries := append([]packagedFile{{path: ManifestEntry, body: manifest}}, files...)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.path,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(e.body)),
			ModTime:  createdAt,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("omnpkg: write tar header %s: %w", e.path, err)
		}
		if _, err := tw.Write(e.body); err != nil {
			return nil, fmt.Errorf("omnpkg: write tar entry %s: %w", e.path, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("omnpkg: close tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("omnpkg: close zstd: %w", err)
	}
	return out.Bytes(), nil
}

// exportedPrograms returns the agent's switched-on program ids, in stored
// order without blanks or repeats, for needs_secrets (FR-022).
func exportedPrograms(ids []string) []string {
	var out []string
	for _, id := range ids {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
