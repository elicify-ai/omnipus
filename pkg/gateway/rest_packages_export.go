// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_packages_export.go: POST /api/v1/packages/export (epic #1094 S1a T-5;
// spec revision 7 FR-010, FR-011, FR-013, FR-015, FR-016, FR-017).

package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

// handlePackageExport is POST /api/v1/packages/export (FR-010). The package is
// built in memory, audited, and only then sent: an export that cannot be
// audited sends no bytes (FR-016).
func (a *restAPI) handlePackageExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.PackageExportRequest
	if tooLarge, err := decodePackageJSON(r, &req); err != nil {
		if tooLarge {
			jsonErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		jsonErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(req.AgentIds) != 1 {
		writePackageError(w, packageErr(omnpkg.CodeExportSelectionInvalid, "reason", "exactly one agent must be selected"), nil)
		return
	}
	id := req.AgentIds[0]
	src := &packageExportSource{a: a}
	var buf bytes.Buffer
	if err := omnpkg.Export(r.Context(), src, omnpkg.Selection{AgentIDs: req.AgentIds}, &buf); err != nil {
		writePackageErr(w, "export", err)
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	digest := hex.EncodeToString(sum[:])
	if err := a.packageAudit("export", a.packageActor(r), id, map[string]string{"package_sha256": digest}); err != nil {
		slog.Error("packages: export refused because the audit write failed", "agent_id", id, "error", err)
		writePackageError(w, packageErr(packageCodeAuditFailed), nil)
		return
	}
	applyPackageDownloadHeaders(w, packageFileName(src.name))
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Debug("packages: write export body failed", "agent_id", id, "error", err)
	}
}

// packageFileName is a safe download name for an agent (FR-010). The rule
// lives in omnpkg.DownloadFileName so the export and the B1 review's
// file_name can never disagree.
func packageFileName(name string) string {
	return omnpkg.DownloadFileName(name)
}

// packageExportSource is the engine's store edge for export: the stored
// record from the agent store and the agent's home folder. It never opens
// config.json, credentials or keys (FR-013).
type packageExportSource struct {
	a    *restAPI
	name string // the exported agent's name, for the download file name
}

func (s *packageExportSource) AgentRecord(id string) ([]byte, error) {
	if err := agentstore.ValidateAgentID(id); err != nil {
		return nil, packageErr(packageCodeAgentNotFound, "agent_id", id)
	}
	agent, err := agentstore.New(s.a.homePath).Get(id)
	if errors.Is(err, entity.ErrNotFound) {
		return nil, packageErr(packageCodeAgentNotFound, "agent_id", id)
	}
	if err != nil {
		return nil, err
	}
	s.name = agent.Name
	return json.Marshal(agent)
}

// AgentHomeDir is the agent's folder under the data directory. The ID is
// checked the same way AgentRecord checks it (which runs first), and must be
// a single local path element, so it can never name a folder outside
// agents/.
func (s *packageExportSource) AgentHomeDir(id string) (string, error) {
	if err := agentstore.ValidateAgentID(id); err != nil {
		return "", packageErr(packageCodeAgentNotFound, "agent_id", id)
	}
	if !filepath.IsLocal(id) {
		return "", packageErr(packageCodeAgentNotFound, "agent_id", id)
	}
	return filepath.Join(s.a.homePath, "agents", id), nil
}

// IsCoreAgent reports built-in, system and shipped agents (Dataset D4): a
// shipped core ID, or a live roster entry of type core or system. Built-in
// agents are never exported (chief ruling 4).
func (s *packageExportSource) IsCoreAgent(id string) bool {
	cid := coreagent.CoreAgentID(id)
	if coreagent.IsCoreAgent(id) || coreagent.IsSubagentTierID(cid) || coreagent.IsSystemAgentID(cid) {
		return true
	}
	for _, ac := range s.a.agentLoop.GetConfig().Agents.List {
		if ac.ID == id {
			return ac.Locked || ac.Type == config.AgentTypeCore || ac.Type == config.AgentTypeSystem
		}
	}
	return false
}
