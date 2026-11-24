// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// personOnly admits a signed-in person's browser session only (FR-028).
func personOnly(h http.HandlerFunc) http.HandlerFunc {
	return middleware.RequirePersonSession(requireSessionCookieAuth(h))
}

// handleChannelPrograms serves GET/PUT /api/v1/channels/{id}/programs — the
// only writer of a connection's programs setting (connected-programs FR-009).
func (a *restAPI) handleChannelPrograms(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		personOnly(func(w http.ResponseWriter, _ *http.Request) { a.getChannelPrograms(w, id) })(w, r)
	case http.MethodPut:
		personOnly(func(w http.ResponseWriter, r *http.Request) { a.putChannelPrograms(w, r, id) })(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) getChannelPrograms(w http.ResponseWriter, id string) {
	live := a.agentLoop.GetConfig()
	inst, ok := live.Channels[id]
	if !ok {
		jsonErr(w, http.StatusNotFound, "unknown instance")
		return
	}
	jsonOK(w, gen.ChannelProgramsSetting{Allowed: inst.ProgramsAllowed, Revision: channelProgramsRevision(live, id)})
}

func (a *restAPI) putChannelPrograms(w http.ResponseWriter, r *http.Request, id string) {
	var req gen.ChannelProgramsSetting
	if !readProgramsBody(w, r, &req) {
		return
	}
	err := a.safeUpdateProgramsConfigJSON(func(m map[string]any) error {
		live := a.agentLoop.GetConfig()
		if _, ok := live.Channels[id]; !ok {
			return &programsWriteError{status: http.StatusNotFound, message: "unknown instance"}
		}
		if req.Revision != channelProgramsRevision(live, id) {
			return &programsWriteError{status: http.StatusConflict,
				message: "This connection changed since this page was loaded. Check the current state before saving again."}
		}
		channels, _ := m["channels"].(map[string]any)
		ch, _ := channels[id].(map[string]any)
		if ch == nil {
			return &programsWriteError{status: http.StatusNotFound, message: "unknown instance"}
		}
		if req.Allowed {
			ch["programs_allowed"] = true
		} else {
			delete(ch, "programs_allowed")
		}
		channels[id] = ch
		return nil
	})
	var werr *programsWriteError
	switch {
	case errors.As(err, &werr):
		jsonErr(w, werr.status, werr.message)
		return
	case err != nil:
		slog.Error("rest: save channel programs setting", "channel", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}
	slog.Info("programs.channel_setting_changed", "channel", id, "allowed", req.Allowed)
	a.auditProgram(auditProgramSettingChanged, audit.DecisionAllow, "", map[string]any{
		"setting": "channel_programs_allowed", "channel_id": id, "allowed": req.Allowed})
	a.getChannelPrograms(w, id)
}
