//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// boardTask mirrors the on-disk format of ~/.omnipus/tasks/{id}.json.
// not-wire-format
type boardTask struct { // not-wire-format
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	ProjectID   string `json:"project_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// boardTasksDir returns the absolute path of ~/.omnipus/tasks/.
func (a *restAPI) boardTasksDir() string {
	return filepath.Join(a.homePath, "tasks")
}

// readBoardTask reads a single task from disk by ID.
// Returns os.ErrNotExist-wrapped error when the file is absent.
func (a *restAPI) readBoardTask(id string) (boardTask, error) {
	path := filepath.Join(a.boardTasksDir(), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return boardTask{}, err
	}
	var t boardTask
	if err := json.Unmarshal(data, &t); err != nil {
		return boardTask{}, err
	}
	return t, nil
}

// listBoardTasks reads all task JSON files from the tasks directory.
// Malformed or unreadable files are skipped with a Warn log.
func (a *restAPI) listBoardTasks() ([]boardTask, error) {
	dir := a.boardTasksDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []boardTask
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 6 || name[len(name)-5:] != ".json" {
			continue
		}
		id := name[:len(name)-5]
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			slog.Warn("rest: board task: skipping unreadable file", "file", name, "error", err)
			continue
		}
		var t boardTask
		if err := json.Unmarshal(data, &t); err != nil {
			slog.Warn("rest: board task: skipping corrupt file", "file", name, "error", err)
			continue
		}
		if t.ID == "" {
			t.ID = id
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// toWireBoardTask converts the internal boardTask to the generated wire type.
func toWireBoardTask(t boardTask) gen.BoardTask {
	var desc *string
	if t.Description != "" {
		d := t.Description
		desc = &d
	}
	var projectID *string
	if t.ProjectID != "" {
		p := t.ProjectID
		projectID = &p
	}
	var agentID *string
	if t.AgentID != "" {
		ag := t.AgentID
		agentID = &ag
	}

	createdAt, _ := time.Parse(time.RFC3339, t.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, t.UpdatedAt)

	return gen.BoardTask{
		Id:          t.ID,
		Name:        t.Name,
		Description: desc,
		Status:      gen.BoardTaskStatus(t.Status),
		ProjectId:   projectID,
		AgentId:     agentID,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
}

// handleBoardTaskList handles GET /api/v1/board/tasks with optional filters.
func (a *restAPI) handleBoardTaskList(w http.ResponseWriter, r *http.Request) {
	projectFilter := r.URL.Query().Get("project_id")
	statusFilter := r.URL.Query().Get("status")

	// Parse limit (default 200, max 1000 per spec).
	limit := 200
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if v, err := strconv.Atoi(lStr); err == nil && v > 0 {
			if v > 1000 {
				v = 1000
			}
			limit = v
		}
	}
	// Parse offset (default 0).
	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if v, err := strconv.Atoi(oStr); err == nil && v >= 0 {
			offset = v
		}
	}

	all, err := a.listBoardTasks()
	if err != nil {
		slog.Error("rest: board task: list failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list board tasks")
		return
	}

	// Apply project_id and status filters.
	filtered := make([]boardTask, 0, len(all))
	for _, t := range all {
		if projectFilter != "" && t.ProjectID != projectFilter {
			continue
		}
		if statusFilter != "" && t.Status != statusFilter {
			continue
		}
		filtered = append(filtered, t)
	}

	total := len(filtered)

	// Apply pagination.
	if offset >= len(filtered) {
		filtered = filtered[:0]
	} else {
		filtered = filtered[offset:]
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
	}

	// Build the response. BoardTaskListResponse.Items is an inline anonymous struct
	// in the generated code, so we use a local shape that is JSON-identical.
	// not-wire-format
	type listItem struct { // not-wire-format
		AgentId     *string                              `json:"agent_id,omitempty"`
		CreatedAt   time.Time                            `json:"created_at"`
		Description *string                              `json:"description,omitempty"`
		Id          string                               `json:"id"`
		Name        string                               `json:"name"`
		ProjectId   *string                              `json:"project_id,omitempty"`
		Status      gen.BoardTaskListResponseItemsStatus `json:"status"`
		UpdatedAt   time.Time                            `json:"updated_at"`
	}
	// not-wire-format
	type boardTaskListResp struct { // not-wire-format
		Items []listItem `json:"items"`
		Total int        `json:"total"`
	}

	items := make([]listItem, 0, len(filtered))
	for _, t := range filtered {
		wt := toWireBoardTask(t)
		items = append(items, listItem{
			AgentId:     wt.AgentId,
			CreatedAt:   wt.CreatedAt,
			Description: wt.Description,
			Id:          wt.Id,
			Name:        wt.Name,
			ProjectId:   wt.ProjectId,
			Status:      gen.BoardTaskListResponseItemsStatus(wt.Status),
			UpdatedAt:   wt.UpdatedAt,
		})
	}

	jsonOK(w, boardTaskListResp{Items: items, Total: total})
}

// handleBoardTaskGet handles GET /api/v1/board/tasks/{id}.
func (a *restAPI) handleBoardTaskGet(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	t, err := a.readBoardTask(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "board task not found")
			return
		}
		slog.Error("rest: board task: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read board task")
		return
	}
	jsonOK(w, toWireBoardTask(t))
}

// HandleBoardTasks dispatches GET /api/v1/board/tasks and GET /api/v1/board/tasks/{id}.
// Write operations (POST/PUT/DELETE) are intentionally not supported here —
// GTD board tasks are mutated exclusively through agent tools (system.task.*).
func (a *restAPI) HandleBoardTasks(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/board/tasks")

	if rest != "" {
		id := strings.TrimPrefix(rest, "/")
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleBoardTaskGet(w, r, id)
		return
	}

	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.handleBoardTaskList(w, r)
}

// HandleTokenStats handles GET /api/v1/stats/tokens.
// It returns per-agent token usage aggregated from SessionMeta.Stats across
// all sessions. The optional ?period=month query param restricts aggregation
// to the current calendar month (UTC). Absent or unrecognised period values
// default to "month" per the OpenAPI spec.
func (a *restAPI) HandleTokenStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)

	// agentAccum accumulates per-agent token counts.
	// not-wire-format
	type agentAccum struct { // not-wire-format
		name      string
		tokensIn  int
		tokensOut int
	}
	byAgent := make(map[string]*agentAccum)

	// ListAllSessions merges the shared store (new sessions) with all per-agent
	// legacy stores, deduplicates, and returns a slice of *UnifiedMeta.
	allSessions, errs := a.agentLoop.ListAllSessions()
	for _, e := range errs {
		slog.Warn("rest: token stats: partial session list error", "error", e)
	}

	registry := a.agentLoop.GetRegistry()

	for _, sm := range allSessions {
		// Apply month filter: keep sessions whose UpdatedAt falls within [periodStart, periodEnd).
		if sm.UpdatedAt.Before(periodStart) || !sm.UpdatedAt.Before(periodEnd) {
			continue
		}

		// PostLoad backfills AgentIDs from legacy AgentID for sessions that pre-date
		// the multi-agent model.
		sm.PostLoad()

		for _, agentID := range sm.AgentIDs {
			if agentID == "" {
				continue
			}
			acc, ok := byAgent[agentID]
			if !ok {
				name, _ := registry.GetAgentName(agentID)
				if name == "" {
					name = agentID
				}
				acc = &agentAccum{name: name}
				byAgent[agentID] = acc
			}
			acc.tokensIn += sm.Stats.TokensIn
			acc.tokensOut += sm.Stats.TokensOut
		}
	}

	// Build the wire response using local structs whose JSON tags match the
	// generated gen.TokenUsageSummary shape exactly. The generated type uses
	// an inline anonymous struct for the Agents element, which cannot be
	// constructed directly from outside the package.
	// not-wire-format
	type agentEntry struct { // not-wire-format
		AgentId     string `json:"agent_id"`
		AgentName   string `json:"agent_name"`
		TokensIn    int    `json:"tokens_in"`
		TokensOut   int    `json:"tokens_out"`
		TokensTotal int    `json:"tokens_total"`
	}
	// not-wire-format
	type tokenUsageResp struct { // not-wire-format
		Agents      []agentEntry `json:"agents"`
		PeriodEnd   time.Time    `json:"period_end"`
		PeriodStart time.Time    `json:"period_start"`
	}

	entries := make([]agentEntry, 0, len(byAgent))
	for agentID, acc := range byAgent {
		entries = append(entries, agentEntry{
			AgentId:     agentID,
			AgentName:   acc.name,
			TokensIn:    acc.tokensIn,
			TokensOut:   acc.tokensOut,
			TokensTotal: acc.tokensIn + acc.tokensOut,
		})
	}

	// Insertion sort for a stable, deterministic response order by agent_id.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].AgentId < entries[j-1].AgentId; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}

	jsonOK(w, tokenUsageResp{
		Agents:      entries,
		PeriodEnd:   periodEnd,
		PeriodStart: periodStart,
	})
}
