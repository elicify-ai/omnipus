// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/elicify-ai/omnipus/pkg/programs"
)

// normalizeAgentPrograms removes duplicate ids, keeping first-seen order.
func normalizeAgentPrograms(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// refuseProgramsWrite gates an agent update that carries `programs`
// (connected-programs FR-007, FR-028, FR-042): only a signed-in person's
// browser session may change it (403 before any change), and the next list
// must not conflict with the agent's private variables or put two git logins
// for one host on the agent (409 with the spec message). It reports whether
// the request was answered.
func (uf *restAPIUpdateAgentFlow) refuseProgramsWrite() bool {
	admitted := false
	personOnly(func(http.ResponseWriter, *http.Request) { admitted = true })(uf.w, uf.r)
	if !admitted {
		return true
	}
	next := normalizeAgentPrograms(*uf.ru.req.Programs)
	current := uf.cfg.Agents.List[uf.foundIdx].Programs
	var privateVars []string
	if store, err := uf.ru.a.programStore(); err == nil {
		inventory, ierr := store.CardInventory()
		if ierr != nil {
			slog.Error("rest: agent programs: read private variables", "agent", uf.ru.id, "error", ierr)
			jsonErr(uf.w, http.StatusInternalServerError, "the credential store could not be read")
			return true
		}
		privateVars = programs.SelectedPrivateVariables(inventory, uf.ru.id)
	} else if len(next) > 0 {
		jsonErr(uf.w, http.StatusServiceUnavailable, "credential store is locked — set OMNIPUS_MASTER_KEY or OMNIPUS_KEY_FILE")
		return true
	}
	if err := programs.SwitchConflict(uf.cfg, uf.ru.id, current, next, privateVars); err != nil {
		var verr *programs.ValidationError
		if errors.As(err, &verr) {
			status := http.StatusBadRequest
			if verr.Conflict {
				status = http.StatusConflict
			}
			jsonErr(uf.w, status, verr.Message)
			return true
		}
		slog.Error("rest: agent programs: conflict check", "agent", uf.ru.id, "error", err)
		jsonErr(uf.w, http.StatusInternalServerError, "internal server error")
		return true
	}
	return false
}
