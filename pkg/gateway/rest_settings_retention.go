// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Retention settings endpoint (Settings redesign #1519, C-3 / B3 + B6).
//
// GET /api/v1/settings/retention — RetentionSettings, as stored in config.json
// PUT /api/v1/settings/retention — RetentionSettingsUpdate, the ONLY write path
// for storage.retention.session_days, memory_retros_days and
// memory_retros_forever. Every other writer that would change the effective
// retention values is refused by effect (rest_config_effect_guards.go).
//
// Registered behind adminWrap (withAuth → RequireNotBypass); no password.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Retention ranges enforced on write (spec Machine-Verifiable Constraints).
const (
	retentionSessionDaysMin = 1
	retentionSessionDaysMax = 365
	retentionMemoryDaysMin  = 1
	retentionMemoryDaysMax  = 730
)

// HandleRetentionSettings dispatches GET and PUT /api/v1/settings/retention.
func (a *restAPI) HandleRetentionSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getRetentionSettings(w)
	case http.MethodPut:
		a.putRetentionSettings(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// storedRetention reads storage.retention from config.json on disk — the
// stored values (FR-041/FR-046), not the running copy, which can lag behind
// the file after a failed refresh.
func (a *restAPI) storedRetention() (config.OmnipusRetentionConfig, error) {
	raw, err := os.ReadFile(a.configPath())
	if err != nil {
		return config.OmnipusRetentionConfig{}, fmt.Errorf("read config: %w", err)
	}
	var doc struct {
		Storage config.OmnipusStorageConfig `json:"storage"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return config.OmnipusRetentionConfig{}, fmt.Errorf("parse config: %w", err)
	}
	return doc.Storage.Retention, nil
}

// retentionSettingsView maps the stored retention block to the read shape.
// Day counts are returned as stored, even outside the write ranges; 0 or a
// negative stored value is returned as its effective default (90 / 180).
func retentionSettingsView(ret config.OmnipusRetentionConfig) gen.RetentionSettings {
	return gen.RetentionSettings{
		SessionDays:           ret.RetentionSessionDays(),
		MemoryRetrosDays:      ret.RetentionMemoryRetrosDays(),
		MemoryRetrosForever:   ret.MemoryRetrosForever,
		KeepEverythingForever: ret.IsDisabled(),
	}
}

func (a *restAPI) getRetentionSettings(w http.ResponseWriter) {
	ret, err := a.storedRetention()
	if err != nil {
		jsonServerFailure(w, http.StatusInternalServerError, "could not read retention settings", err)
		return
	}
	jsonOK(w, retentionSettingsView(ret))
}

// parseRetentionUpdate checks a PUT body unconditionally — whatever
// gateway.validate_inbound is (FR-042, review MAJ-004) — and returns the
// update or the exact 400 message. Field names must match exactly: a
// differently cased key is an unknown field, never a silent alias.
func parseRetentionUpdate(raw []byte) (gen.RetentionSettingsUpdate, string) {
	var upd gen.RetentionSettingsUpdate
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return upd, "request body must be a JSON object"
	}
	if len(fields) == 0 {
		return upd, "at least one field required"
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := fields[name]
		switch name {
		case "session_days":
			n, ok := parseWholeNumberInRange(value, retentionSessionDaysMin, retentionSessionDaysMax)
			if !ok {
				return upd, fmt.Sprintf("session_days must be a whole number from %d to %d",
					retentionSessionDaysMin, retentionSessionDaysMax)
			}
			upd.SessionDays = &n
		case "memory_retros_days":
			n, ok := parseWholeNumberInRange(value, retentionMemoryDaysMin, retentionMemoryDaysMax)
			if !ok {
				return upd, fmt.Sprintf("memory_retros_days must be a whole number from %d to %d",
					retentionMemoryDaysMin, retentionMemoryDaysMax)
			}
			upd.MemoryRetrosDays = &n
		case "memory_retros_forever":
			b, ok := parseStrictBool(value)
			if !ok {
				return upd, "memory_retros_forever must be true or false"
			}
			upd.MemoryRetrosForever = &b
		case "keep_everything_forever":
			return upd, "keep_everything_forever is read-only: it mirrors storage.retention.disabled, " +
				"which is set only in the configuration file"
		default:
			return upd, fmt.Sprintf("unknown field %q: allowed fields are session_days, "+
				"memory_retros_days and memory_retros_forever", name)
		}
	}
	return upd, ""
}

// parseWholeNumberInRange accepts only a JSON number that is a whole number
// within [lo, hi]; strings, null, booleans and fractions are refused.
func parseWholeNumberInRange(raw json.RawMessage, lo, hi int) (int, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return 0, false
	}
	var n json.Number
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return 0, false
	}
	i, err := n.Int64()
	if err != nil || i < int64(lo) || i > int64(hi) {
		return 0, false
	}
	return int(i), true
}

// parseStrictBool accepts only the JSON literals true and false.
func parseStrictBool(raw json.RawMessage) (bool, bool) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func (a *restAPI) putRetentionSettings(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	upd, msg := parseRetentionUpdate(raw)
	if msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	// Schema validation, when switched on, is a second layer behind the
	// handler's own checks above — never the only one.
	if cfg := a.agentLoop.GetConfig(); cfg != nil && cfg.Gateway.ValidateInbound {
		var checked gen.RetentionSettingsUpdate
		if !decodeAndValidateBytes(w, raw, "RetentionSettingsUpdate", &checked, true, "") {
			return
		}
	}

	var before, after config.OmnipusRetentionConfig
	writeErr := a.safeUpdateConfigJSON(func(m map[string]any) error {
		retention, mapErr := retentionMapForWrite(m)
		if mapErr != nil {
			return mapErr
		}
		before = retentionFromMap(retention)
		if upd.SessionDays != nil {
			retention["session_days"] = *upd.SessionDays
		}
		if upd.MemoryRetrosDays != nil {
			retention["memory_retros_days"] = *upd.MemoryRetrosDays
		}
		if upd.MemoryRetrosForever != nil {
			retention["memory_retros_forever"] = *upd.MemoryRetrosForever
		}
		after = retentionFromMap(retention)
		return nil
	})
	if writeErr != nil {
		var refusal *requestRefusalError
		if errors.As(writeErr, &refusal) {
			jsonErr(w, http.StatusBadRequest, refusal.Error())
			return
		}
		jsonServerFailure(w, http.StatusInternalServerError, "could not save retention settings", writeErr)
		return
	}
	a.auditRetentionChange(r, before, after)

	stored, err := a.storedRetention()
	if err != nil {
		jsonServerFailure(w, http.StatusInternalServerError, "could not read retention settings", err)
		return
	}
	jsonOK(w, retentionSettingsView(stored))
}

// retentionMapForWrite returns m.storage.retention as a map, creating the
// missing levels. A level that exists but is not an object is a hand-edit
// the write must not silently overwrite.
func retentionMapForWrite(m map[string]any) (map[string]any, error) {
	storage, ok := m["storage"].(map[string]any)
	if !ok {
		if v, present := m["storage"]; present && v != nil {
			return nil, &requestRefusalError{msg: "storage in config.json is not an object; fix the configuration file first"}
		}
		storage = map[string]any{}
		m["storage"] = storage
	}
	retention, ok := storage["retention"].(map[string]any)
	if !ok {
		if v, present := storage["retention"]; present && v != nil {
			return nil, &requestRefusalError{msg: "storage.retention in config.json is not an object; fix the configuration file first"}
		}
		retention = map[string]any{}
		storage["retention"] = retention
	}
	return retention, nil
}

// retentionFromMap decodes a raw retention map the way config.json loads it;
// used only for the audit record, so a decode problem leaves the zero value.
func retentionFromMap(retention map[string]any) config.OmnipusRetentionConfig {
	var ret config.OmnipusRetentionConfig
	if raw, err := json.Marshal(retention); err == nil {
		if err := json.Unmarshal(raw, &ret); err != nil {
			slog.Warn("rest: retention settings: stored retention block does not decode", "error", err)
		}
	}
	return ret
}

// auditRetentionChange records the change: retention deletes data on a
// schedule, so every write is audited like the removed /security/retention
// route was.
func (a *restAPI) auditRetentionChange(r *http.Request, before, after config.OmnipusRetentionConfig) {
	if a.agentLoop == nil {
		return
	}
	auditLogger := a.agentLoop.AuditLogger()
	if auditLogger == nil {
		return
	}
	view := func(ret config.OmnipusRetentionConfig) map[string]any {
		return map[string]any{
			"session_days":          ret.SessionDays,
			"memory_retros_days":    ret.MemoryRetrosDays,
			"memory_retros_forever": ret.MemoryRetrosForever,
		}
	}
	if err := audit.EmitSecuritySettingChange(r.Context(), auditLogger, "storage.retention",
		view(before), view(after)); err != nil {
		slog.Error("rest: audit emit retention settings", "error", err)
	}
}
