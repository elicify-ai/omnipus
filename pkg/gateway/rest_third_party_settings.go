// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// GET/PUT /api/v1/settings/third-party-agents — Settings -> Third-party
// agents (tool-parity spec r17 §3.2–§3.4, §3.10). Both routes sit behind the
// shared person-only check (middleware.RequirePersonSession). PUT replaces
// the whole section; every row is validated before anything is written; no
// revision and no 409.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/clidetect"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
)

const (
	thirdPartySettingsSchema = "ThirdPartyAgentSettings"
	codeCLIPathInvalid       = "cli_path_invalid"
	codeCLIPresetInvalid     = "cli_preset_invalid"
)

// sessionCookieAuthContextKey marks a request authenticated by the browser
// session cookie (AuthResult.ViaSessionCookie), set by withAuthAndBodyLimit.
type sessionCookieAuthContextKey struct{}

// requireSessionCookieAuth refuses, before the body is read, every request
// that did not authenticate with the person's session cookie: a bearer
// token — agent, tool, CLI or the person's own API token — gets 403, the
// same refusal as RequirePersonSession. r17 §3.3 / row 9.23 ("agent or tool
// token → 403"). Fail closed: an absent marker is a refusal.
func requireSessionCookieAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if via, _ := r.Context().Value(sessionCookieAuthContextKey{}).(bool); !via {
			logsafeWarn("gateway.person_session_required", "reason", "not_session_cookie")
			code := middleware.PersonSessionRequiredCode
			writeJSON(w, http.StatusForbidden, gen.ErrorResponse{
				Error: "this action requires a signed-in person",
				Code:  &code,
			})
			return
		}
		next(w, r)
	}
}

// HandleThirdPartyAgentSettings dispatches GET and PUT.
func (a *restAPI) HandleThirdPartyAgentSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.thirdPartySettingsView())
	case http.MethodPut:
		a.putThirdPartyAgentSettings(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// thirdPartyAgentWritable lists the agent-writable locations the path check
// refuses beyond $OMNIPUS_HOME: the gateway's own home folder (every
// workspace root lives under it) and the sandbox's read+write grants
// (sandbox.allowed_paths). r17 §3.5.
func thirdPartyAgentWritable(homePath string, cfg *config.Config) []string {
	return thirdpartycli.AgentWritableRoots(homePath, cfg)
}

// putThirdPartyAgentSettings validates every row, then writes the whole
// section in one config write and audits each changed path and preset.
func (a *restAPI) putThirdPartyAgentSettings(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if !validateThirdPartySettingsSchema(w, raw) {
		return
	}
	var body gen.ThirdPartyAgentSettings
	if err := json.Unmarshal(raw, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rows := thirdPartyRowsFromBody(body)
	cfg := configFromContext(r.Context())
	if cfg == nil {
		cfg = a.agentLoop.GetConfig()
	}
	writable := thirdPartyAgentWritable(a.homePath, cfg)
	for _, cli := range thirdpartycli.CLIs {
		if checkErr := validateThirdPartyRow(cli, rows[cli], writable); checkErr != nil {
			writeThirdPartyRefusal(w, checkErr)
			return
		}
	}
	var previous map[string]config.ThirdPartyCLIConfig
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		previous = readThirdPartyRows(m)
		writeThirdPartyRows(m, rows)
		return nil
	}); err != nil {
		logsafeError("rest: PUT /settings/third-party-agents: failed", "error", err)
		if writeRequestRefusal(w, err) {
			return
		}
		jsonErr(w, http.StatusInternalServerError, "failed to update third-party agent settings")
		return
	}
	a.auditThirdPartyChanges(r, previous, rows)
	writeJSON(w, http.StatusOK, a.thirdPartySettingsView())
}

// validateThirdPartySettingsSchema applies the generated request schema
// (unknown CLI key, unknown field, missing field, path over 4,096
// characters). preset_args over 4,096 characters is left to the
// cli_preset_invalid too_long check so that refusal carries its r17 §3.4
// code; the schema's limit and that check are the same rule.
func validateThirdPartySettingsSchema(w http.ResponseWriter, raw []byte) bool {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if root, ok := doc.(map[string]any); ok {
		if clis, ok := root["clis"].(map[string]any); ok {
			for _, row := range clis {
				if fields, ok := row.(map[string]any); ok {
					if s, ok := fields["preset_args"].(string); ok && utf8.RuneCountInString(s) > thirdpartycli.MaxPresetChars {
						fields["preset_args"] = ""
					}
				}
			}
		}
	}
	checked, err := json.Marshal(doc)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if msg, serverErr := validateBodyAgainstSchema(thirdPartySettingsSchema, checked); msg != "" {
		if serverErr {
			jsonErr(w, http.StatusInternalServerError, "inbound schema unavailable")
			return false
		}
		jsonErr(w, http.StatusBadRequest, "request body does not match schema "+thirdPartySettingsSchema+": "+msg)
		return false
	}
	return true
}

// thirdPartyRowsFromBody maps the PUT body to all three rows; an absent CLI
// key is the empty row (whole-section replacement, r17 §3.4).
func thirdPartyRowsFromBody(body gen.ThirdPartyAgentSettings) map[string]config.ThirdPartyCLIConfig {
	rows := make(map[string]config.ThirdPartyCLIConfig, len(thirdpartycli.CLIs))
	for _, cli := range thirdpartycli.CLIs {
		rows[cli] = config.ThirdPartyCLIConfig{}
	}
	if c := body.Clis.ClaudeCode; c != nil {
		rows[thirdpartycli.CLIClaudeCode] = config.ThirdPartyCLIConfig{Path: c.Path, PresetArgs: c.PresetArgs}
	}
	if c := body.Clis.Codex; c != nil {
		rows[thirdpartycli.CLICodex] = config.ThirdPartyCLIConfig{Path: c.Path, PresetArgs: c.PresetArgs}
	}
	if c := body.Clis.Opencode; c != nil {
		rows[thirdpartycli.CLIOpenCode] = config.ThirdPartyCLIConfig{Path: c.Path, PresetArgs: c.PresetArgs}
	}
	return rows
}

// validateThirdPartyRow checks path before preset_args; "" is a valid path.
func validateThirdPartyRow(cli string, row config.ThirdPartyCLIConfig, writable []string) *thirdpartycli.CheckError {
	if row.Path != "" {
		if _, err := thirdpartycli.ValidateProgramPath(cli, row.Path, writable...); err != nil {
			return asCheckError(cli, err)
		}
	}
	if err := thirdpartycli.ValidatePreset(cli, row.PresetArgs); err != nil {
		return asCheckError(cli, err)
	}
	return nil
}

// asCheckError keeps the check error; any other error is reported as the
// path not being found, never as success.
func asCheckError(cli string, err error) *thirdpartycli.CheckError {
	var ce *thirdpartycli.CheckError
	if errors.As(err, &ce) {
		return ce
	}
	return &thirdpartycli.CheckError{CLI: cli, Check: thirdpartycli.CheckNotFound, Detail: err.Error()}
}

// writeThirdPartyRefusal writes the r17 §3.4 400 for a failed row.
func writeThirdPartyRefusal(w http.ResponseWriter, ce *thirdpartycli.CheckError) {
	code, field := codeCLIPathInvalid, "clis."+ce.CLI+".path"
	details := map[string]any{"cli": ce.CLI, "check": ce.Check}
	if ce.IsPresetCheck() {
		code, field = codeCLIPresetInvalid, "clis."+ce.CLI+".preset_args"
	} else {
		details["at"] = ce.At
	}
	writeJSON(w, http.StatusBadRequest, gen.ErrorResponse{
		Error:   ce.SaveRefusal(),
		Code:    &code,
		Field:   &field,
		Details: &details,
	})
}

// readThirdPartyRows reads the saved rows out of the raw config map.
func readThirdPartyRows(m map[string]any) map[string]config.ThirdPartyCLIConfig {
	out := make(map[string]config.ThirdPartyCLIConfig, len(thirdpartycli.CLIs))
	sandbox, _ := m["sandbox"].(map[string]any)
	section, _ := sandbox["third_party_agents"].(map[string]any)
	clis, _ := section["clis"].(map[string]any)
	for _, cli := range thirdpartycli.CLIs {
		row, _ := clis[cli].(map[string]any)
		path, _ := row["path"].(string)
		preset, _ := row["preset_args"].(string)
		out[cli] = config.ThirdPartyCLIConfig{Path: path, PresetArgs: preset}
	}
	return out
}

// writeThirdPartyRows replaces sandbox.third_party_agents with all three rows.
func writeThirdPartyRows(m map[string]any, rows map[string]config.ThirdPartyCLIConfig) {
	sandbox, _ := m["sandbox"].(map[string]any)
	if sandbox == nil {
		sandbox = make(map[string]any)
		m["sandbox"] = sandbox
	}
	clis := make(map[string]any, len(rows))
	for _, cli := range thirdpartycli.CLIs {
		clis[cli] = map[string]any{"path": rows[cli].Path, "preset_args": rows[cli].PresetArgs}
	}
	sandbox["third_party_agents"] = map[string]any{"clis": clis}
}

// auditThirdPartyChanges records each changed path and preset (r17 §3.10).
// A failed audit write is logged, never silent.
func (a *restAPI) auditThirdPartyChanges(r *http.Request, previous, saved map[string]config.ThirdPartyCLIConfig) {
	if a.auditor == nil {
		return
	}
	actor := ""
	if u, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && u != nil {
		actor = u.Username
	}
	for _, cli := range thirdpartycli.CLIs {
		before, after := previous[cli], saved[cli]
		if before.Path != after.Path {
			a.logThirdPartyAudit("settings.third_party_agents.path_changed", actor, cli, "path", before.Path, after.Path)
		}
		if before.PresetArgs != after.PresetArgs {
			a.logThirdPartyAudit("settings.third_party_agents.preset_changed", actor, cli, "preset_args", before.PresetArgs, after.PresetArgs)
		}
	}
}

func (a *restAPI) logThirdPartyAudit(event, actor, cli, field, oldValue, newValue string) {
	if err := a.auditor.Log(&audit.Entry{
		Event:    event,
		Decision: audit.DecisionAllow,
		User:     actor,
		Details: map[string]any{
			"cli":          cli,
			"actor":        actor,
			"old_" + field: oldValue,
			"new_" + field: newValue,
		},
	}); err != nil {
		logsafeWarn("audit write failed", "event", event, "cli", cli, "error", err)
	}
}

// thirdPartySettingsView builds the GET/PUT response: all three rows from
// the live config plus live detection (never stored), detection key
// "claude" already mapped to claude-code by clidetect.
func (a *restAPI) thirdPartySettingsView() gen.ThirdPartyAgentSettingsView {
	cfg := a.agentLoop.GetConfig()
	var sandbox *config.OmnipusSandboxConfig
	if cfg != nil {
		sandbox = &cfg.Sandbox
	}
	detected := cliDetectAll()
	var v gen.ThirdPartyAgentSettingsView
	cc, cx, oc := &v.Clis.ClaudeCode, &v.Clis.Codex, &v.Clis.Opencode
	row := sandbox.ThirdPartyCLI(thirdpartycli.CLIClaudeCode)
	cc.Path, cc.PresetArgs = row.Path, row.PresetArgs
	setDetected(&cc.Detected.Installed, &cc.Detected.Path, &cc.Detected.LinkPath, &cc.Detected.Source, detected[thirdpartycli.CLIClaudeCode])
	row = sandbox.ThirdPartyCLI(thirdpartycli.CLICodex)
	cx.Path, cx.PresetArgs = row.Path, row.PresetArgs
	setDetected(&cx.Detected.Installed, &cx.Detected.Path, &cx.Detected.LinkPath, &cx.Detected.Source, detected[thirdpartycli.CLICodex])
	row = sandbox.ThirdPartyCLI(thirdpartycli.CLIOpenCode)
	oc.Path, oc.PresetArgs = row.Path, row.PresetArgs
	setDetected(&oc.Detected.Installed, &oc.Detected.Path, &oc.Detected.LinkPath, &oc.Detected.Source, detected[thirdpartycli.CLIOpenCode])
	return v
}

// setDetected fills one row's generated detection carrier.
func setDetected[S ~string](installed *bool, path, link **string, source **S, res clidetect.Result) {
	*installed = res.Installed
	if !res.Installed {
		return
	}
	*path = strPtr(res.Path)
	if res.LinkPath != "" {
		*link = strPtr(res.LinkPath)
	}
	if wire := cliDetectSourceWire(res.Source); wire != "" {
		s := S(wire)
		*source = &s
	}
}
