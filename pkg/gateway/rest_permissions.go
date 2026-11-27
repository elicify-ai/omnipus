// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// GET/PATCH /api/v1/security/permissions (Skills & Tools C-2): the global
// tool and skill permissions, the tool and skill levels and the skill trust
// in one read model, changed key by key. A PATCH validates the whole request
// first, then under the config write lock reads the stored config, changes
// only the named keys and writes atomically; one audit entry per changed
// key; a changed permission ends the running timed allowances of that tool
// or skill for every agent (C-26 point 7); running agents are reloaded.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/skillperm"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	permNothingToChange = "Nothing to change."
	permNotSaved        = "Not saved: the settings file could not be written."
	permTrustValues     = "Use allow, ask or deny."
)

// permissionMembers maps each PATCH member to its sandbox field.
var permissionMembers = []struct{ member, field string }{
	{"policies", "tool_policies"},
	{"skill_policies", "skill_policies"},
	{"visibility", "tool_visibility"},
	{"skill_visibility", "skill_visibility"},
}

// HandlePermissionSettings serves GET and PATCH /api/v1/security/permissions.
func (a *restAPI) HandlePermissionSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		state, err := a.readPermissionSettings()
		if err != nil {
			slog.Error("rest: read permission settings", "error", err)
			jsonErr(w, http.StatusInternalServerError, "The settings could not be read.")
			return
		}
		jsonOK(w, state)
	case http.MethodPatch:
		a.patchPermissionSettings(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// permissionChange is one key a PATCH changes.
type permissionChange struct {
	field, key string // sandbox field and key ("" key for skill_trust)
	old, new   string
}

func (c permissionChange) resource() string {
	if c.key == "" {
		return "sandbox." + c.field
	}
	return "sandbox." + c.field + "." + c.key
}

func (a *restAPI) patchPermissionSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	// No step-up re-auth (spec lines 60 and 135): the same sign-in the
	// retired tool-policy writer used; C-16 autosave sends no token.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	req, msg := decodePermissionPatch(r)
	if msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	home := a.homePath
	stored, err := readSandboxSection(a.configPath())
	if err != nil {
		slog.Error("rest: read config for permission settings", "error", err)
		jsonErr(w, http.StatusInternalServerError, "The settings could not be read.")
		return
	}
	if msg := validatePermissionPatch(home, stored, req); msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}

	a.configMu.Lock()
	stored, err = readSandboxSection(a.configPath())
	if err != nil {
		a.configMu.Unlock()
		slog.Error("rest: read config for permission settings", "error", err)
		jsonErr(w, http.StatusInternalServerError, "The settings could not be read.")
		return
	}
	changes := permissionChanges(stored, req)
	if len(changes) > 0 {
		err = a.updateConfigJSONLocked(func(m map[string]any) error {
			applyPermissionChanges(m, changes)
			return nil
		})
	}
	a.configMu.Unlock()
	if err != nil {
		slog.Error("rest: write permission settings", "error", err)
		jsonErr(w, http.StatusInternalServerError, permNotSaved)
		return
	}

	a.afterPermissionChanges(r, changes)
	if err = a.triggerReloadAndWait(); err != nil {
		slog.Error("rest: reload after permission settings change failed", "error", err)
		jsonErr(w, http.StatusServiceUnavailable,
			"The settings were saved, but could not be applied to running agents. "+
				"Retry applying the change, or restart Omnipus.")
		return
	}
	state, err := a.readPermissionSettings()
	if err != nil {
		slog.Error("rest: read permission settings after change", "error", err)
		jsonErr(w, http.StatusInternalServerError, "The settings were saved but could not be read back.")
		return
	}
	jsonOK(w, state)
}

// afterPermissionChanges writes one audit entry per changed key and ends
// the running timed allowances a changed permission answers (C-26 point 7).
func (a *restAPI) afterPermissionChanges(r *http.Request, changes []permissionChange) {
	auditLogger := a.packageAuditLogger() // the gateway's, else the loop's
	for _, c := range changes {
		if auditLogger != nil {
			if err := audit.EmitSecuritySettingChangeChecked(r.Context(), auditLogger, c.resource(), c.old, c.new); err != nil {
				slog.Error("rest: audit permission settings change", "resource", c.resource(), "error", err)
			}
		}
		key := ""
		switch c.field {
		case "tool_policies":
			key = c.key
		case "skill_policies":
			key = security.AllowanceSubjectSkillPrefix + c.key
		}
		if key == "" || a.agentLoop == nil {
			continue
		}
		if err := a.agentLoop.EndAllowancesFor(r.Context(), auditLogger, agent.PermissionChange{
			Key: key, Old: c.old, New: c.new,
		}); err != nil {
			slog.Error("rest: audit ended allowances", "key", key, "error", err)
		}
	}
}

// permissionPatch is a decoded PATCH body with null checks done.
type permissionPatch struct {
	maps  map[string]map[string]string // member → key → value
	trust *string
}

// decodePermissionPatch decodes the body, refusing nulls (naming the key),
// unknown members and a request that names no key.
func decodePermissionPatch(r *http.Request) (permissionPatch, string) {
	out := permissionPatch{maps: map[string]map[string]string{}}
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&raw); err != nil || raw == nil {
		return out, "The request body must be a JSON object."
	}
	known := map[string]bool{"skill_trust": true}
	for _, m := range permissionMembers {
		known[m.member] = true
	}
	for name, value := range raw {
		if !known[name] {
			return out, fmt.Sprintf("`%s` is not a setting of this page.", name)
		}
		if isJSONNull(value) {
			return out, fmt.Sprintf("`%s` cannot be empty.", name)
		}
	}
	if v, ok := raw["skill_trust"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return out, permTrustValues
		}
		out.trust = &s
	}
	named := out.trust != nil
	for _, m := range permissionMembers {
		v, ok := raw[m.member]
		if !ok {
			continue
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(v, &entries); err != nil {
			return out, fmt.Sprintf("`%s` must be an object.", m.member)
		}
		values := make(map[string]string, len(entries))
		for _, key := range sortedRawKeys(entries) {
			if isJSONNull(entries[key]) {
				return out, fmt.Sprintf("`%s` cannot be empty.", key)
			}
			var s string
			if err := json.Unmarshal(entries[key], &s); err != nil {
				return out, fmt.Sprintf("`%s` must be a text value.", key)
			}
			values[key] = s
		}
		if len(values) > 0 {
			named = true
		}
		out.maps[m.member] = values
	}
	if !named {
		return out, permNothingToChange
	}
	return out, ""
}

func isJSONNull(v json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(v), []byte("null")) }

func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// validatePermissionPatch checks every key and value and normalises the
// skill keys in place (C-23). It returns the first refusal ("" when valid).
func validatePermissionPatch(home string, stored sandboxSection, req permissionPatch) string {
	if req.trust != nil {
		switch *req.trust {
		case "allow", "ask", "deny":
		default:
			return permTrustValues
		}
	}
	known := buildKnownBuiltinToolNames()
	if msg := validateToolPolicyPatch(req.maps["policies"], known); msg != "" {
		return msg
	}
	if msg := validateToolVisibilityPatch(req.maps["visibility"], known); msg != "" {
		return msg
	}
	scope := tools.GlobalSkillKeyScope(home, stored.SkillPolicies)
	for _, member := range []string{"skill_policies", "skill_visibility"} {
		in := req.maps[member]
		if len(in) == 0 {
			continue
		}
		checked, err := skills.CheckSkillPolicyMap(scope, in)
		if err != nil {
			return err.Error()
		}
		for key, v := range checked {
			if member == "skill_policies" && !isPolicyValue(v) {
				return fmt.Sprintf("`%s`: use allow, ask or deny.", key)
			}
			if member == "skill_visibility" && !config.IsModelVisibility(v) {
				return fmt.Sprintf("`%s`: use hidden, name_only, name_and_description or full.", key)
			}
		}
		req.maps[member] = checked
	}
	return ""
}

func isPolicyValue(v string) bool { return v == "allow" || v == "ask" || v == "deny" }

func validateToolPolicyPatch(in map[string]string, known map[string]struct{}) string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := in[k]
		if !isPolicyValue(v) {
			return fmt.Sprintf("`%s`: use allow, ask or deny.", k)
		}
		if config.IsPermissionFixedTool(k) && v != "allow" {
			return fmt.Sprintf("`%s` has no permission of its own.", k)
		}
	}
	if name, bad := tools.AlwaysAskAllowViolation(in); bad {
		return tools.AlwaysAskAllowRefusal(name)
	}
	if defects := config.ValidateSubmittedToolPolicyMap(in, known); len(defects.Invalid) > 0 {
		return fmt.Sprintf("policies: unrecognized tool name(s): %s", strings.Join(defects.Invalid, ", "))
	}
	return ""
}

func validateToolVisibilityPatch(in map[string]string, known map[string]struct{}) string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := in[k]
		if !config.IsModelVisibility(v) {
			return fmt.Sprintf("`%s`: use hidden, name_only, name_and_description or full.", k)
		}
		if locked, ok := config.LockedToolVisibility(k); ok && v != locked {
			return fmt.Sprintf("`%s` is always loaded: agents need it to work, so its level cannot be changed.", k)
		}
	}
	if defects := config.ValidateSubmittedToolPolicyMap(in, known); len(defects.Invalid) > 0 {
		return fmt.Sprintf("visibility: unrecognized tool name(s): %s", strings.Join(defects.Invalid, ", "))
	}
	return ""
}

// permissionChanges lists the keys whose value differs from the stored one.
func permissionChanges(stored sandboxSection, req permissionPatch) []permissionChange {
	var out []permissionChange
	current := map[string]map[string]string{
		"tool_policies": stored.ToolPolicies, "skill_policies": stored.SkillPolicies,
		"tool_visibility": stored.ToolVisibility, "skill_visibility": stored.SkillVisibility,
	}
	for _, m := range permissionMembers {
		in := req.maps[m.member]
		keys := make([]string, 0, len(in))
		for k := range in {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if old := current[m.field][k]; old != in[k] {
				out = append(out, permissionChange{field: m.field, key: k, old: old, new: in[k]})
			}
		}
	}
	if req.trust != nil {
		old, _ := config.MigrateLegacySkillTrust(stored.SkillTrust)
		if old != *req.trust || stored.SkillTrust != *req.trust {
			out = append(out, permissionChange{field: "skill_trust", old: old, new: *req.trust})
		}
	}
	return out
}

// applyPermissionChanges writes the changed keys into a raw config map.
func applyPermissionChanges(m map[string]any, changes []permissionChange) {
	sandbox, _ := m["sandbox"].(map[string]any)
	if sandbox == nil {
		sandbox = map[string]any{}
		m["sandbox"] = sandbox
	}
	for _, c := range changes {
		if c.key == "" {
			sandbox[c.field] = c.new
			continue
		}
		target, _ := sandbox[c.field].(map[string]any)
		if target == nil {
			target = map[string]any{}
			sandbox[c.field] = target
		}
		target[c.key] = c.new
	}
}

// sandboxSection is the part of config.json this page reads.
type sandboxSection struct { // not-wire-format: the server reads its own config.json sandbox section here; GET and PATCH /security/permissions return the generated gen.PermissionSettings.
	ToolPolicies    map[string]string `json:"tool_policies"`
	SkillPolicies   map[string]string `json:"skill_policies"`
	ToolVisibility  map[string]string `json:"tool_visibility"`
	SkillVisibility map[string]string `json:"skill_visibility"`
	SkillTrust      string            `json:"skill_trust"`
}

// readSandboxSection reads the stored values from config.json.
func readSandboxSection(path string) (sandboxSection, error) {
	var doc struct {
		Sandbox sandboxSection `json:"sandbox"`
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return doc.Sandbox, nil
	}
	if err != nil {
		return doc.Sandbox, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc.Sandbox, fmt.Errorf("parse config: %w", err)
	}
	return doc.Sandbox, nil
}

// readPermissionSettings is the C-2 read model: the stored maps, with every
// installed skill shown at its default (ask, name_and_description) until a
// value is stored for it.
func (a *restAPI) readPermissionSettings() (gen.PermissionSettings, error) {
	stored, err := readSandboxSection(a.configPath())
	if err != nil {
		return gen.PermissionSettings{}, err
	}
	installed, err := skills.InstalledSkillKeys(filepath.Join(a.homePath, "skills"))
	if err != nil {
		return gen.PermissionSettings{}, fmt.Errorf("list installed skills: %w", err)
	}
	trust, _ := config.MigrateLegacySkillTrust(stored.SkillTrust)
	out := gen.PermissionSettings{
		Policies:        map[string]gen.ToolPolicy{},
		SkillPolicies:   map[string]gen.ToolPolicy{},
		Visibility:      map[string]gen.ModelVisibility{},
		SkillVisibility: map[string]gen.ModelVisibility{},
		SkillTrust:      gen.ToolPolicy(trust),
	}
	for k, v := range stored.ToolPolicies {
		out.Policies[k] = gen.ToolPolicy(v)
	}
	for k, v := range stored.ToolVisibility {
		out.Visibility[k] = gen.ModelVisibility(v)
	}
	for _, k := range installed {
		out.SkillPolicies[k] = gen.ToolPolicy(config.ToolPolicyAsk)
		out.SkillVisibility[k] = gen.ModelVisibility(config.VisibilityNameAndDescription)
	}
	for k, v := range stored.SkillPolicies {
		out.SkillPolicies[k] = gen.ToolPolicy(v)
	}
	for k, v := range stored.SkillVisibility {
		out.SkillVisibility[k] = gen.ModelVisibility(v)
	}
	// C-19 "The notice" (ruling spec-gaps-1): the agents the upgrade has not
	// finished for, read from the ledger on every request; absent when none.
	unfinished, err := skillperm.UnfinishedAgents(a.homePath)
	if err != nil {
		return gen.PermissionSettings{}, fmt.Errorf("read the skill permissions upgrade ledger: %w", err)
	}
	if len(unfinished) > 0 {
		list := make([]gen.SkillUpgradeUnfinishedAgent, 0, len(unfinished))
		for _, u := range unfinished {
			list = append(list, gen.SkillUpgradeUnfinishedAgent{
				AgentId: u.AgentID,
				Reason:  gen.SkillUpgradeUnfinishedAgentReason(u.Reason),
			})
		}
		out.UpgradeUnfinished = &list
	}
	return out, nil
}
