// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

const secretSubmitBodyLimit = 65536

// HandleSecretRequests is K5's destination-bound consent exception to ADR-022.
// Authentication still runs at the route boundary; no raw credential mutation
// is exposed here and the existing credential routes retain requireReAuth.
func (a *restAPI) HandleSecretRequests(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, "/api/v1/secret-requests/")
	parts := strings.Split(remainder, "/")
	if remainder == r.URL.Path || len(parts) > 2 || parts[0] == "" {
		jsonErr(w, http.StatusNotFound, "secret request not found")
		return
	}
	id := parts[0]
	submit := len(parts) == 2 && parts[1] == "submit"
	if (submit && r.Method != http.MethodPost) || (!submit && (len(parts) != 1 || r.Method != http.MethodGet)) {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var raw []byte
	if submit {
		var err error
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, secretSubmitBodyLimit))
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			jsonErr(w, http.StatusRequestEntityTooLarge, "secret submit body is too large")
			return
		}
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "could not read secret submit")
			return
		}
	}
	reg := a.agentLoop.SecretRequests()
	if reg == nil {
		jsonErr(w, http.StatusServiceUnavailable, "secret request service is unavailable")
		return
	}
	owner := ""
	if user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && user != nil {
		owner = user.Username
	}
	rec, err := reg.Get(id, owner)
	if err != nil {
		writeSecretRequestFailure(w, err)
		return
	}
	if !submit {
		jsonOK(w, secretrequest.State(rec))
		return
	}
	// Same lock order as Settings and the approved shared-resource tools:
	// gateway config lock -> live config snapshot -> card/store operation.
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if rec.Card.State != "pending" {
		writeJSON(w, http.StatusConflict, secretrequest.State(rec))
		return
	}
	input, err := decodeSecretSubmit(raw)
	if err != nil {
		reg.AuditRefusal(id, owner, "invalid_payload")
		jsonErr(w, http.StatusBadRequest, "submit exactly the requested values, or cancel; no extra or null fields")
		return
	}
	rec, err = reg.Submit(id, owner, input)
	if err != nil {
		writeSecretRequestFailure(w, err)
		return
	}
	jsonOK(w, secretrequest.State(rec))
}

func decodeSecretSubmit(raw []byte) (gen.SecretSubmitRequest, error) {
	var input gen.SecretSubmitRequest
	var fields map[string]json.RawMessage
	if !utf8.Valid(raw) || json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) != 1 {
		return input, errors.New("invalid secret submit")
	}
	for key, value := range fields {
		if (key != "values" && key != "cancel") || string(value) == "null" {
			return input, errors.New("invalid secret submit")
		}
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, errors.New("invalid secret submit")
	}
	if input.Cancel != nil && !bool(*input.Cancel) {
		return input, errors.New("cancel must be true")
	}
	if input.Values == nil && input.Cancel == nil {
		return input, errors.New("invalid secret submit")
	}
	return input, nil
}

func writeSecretRequestFailure(w http.ResponseWriter, err error) {
	var refusal *secretrequest.RequestError
	if errors.As(err, &refusal) {
		if refusal.State != nil {
			writeJSON(w, refusal.Status, refusal.State)
		} else {
			jsonErr(w, refusal.Status, refusal.Message)
		}
		return
	}
	jsonErr(w, http.StatusInternalServerError, "secret request could not be completed")
}
