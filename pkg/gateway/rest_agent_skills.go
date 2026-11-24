// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools C-4: the agent's own skill permissions on
// GET/PUT /agents/{id}/tools. skills omitted = overrides unchanged;
// skills.policies {} = clear every override; otherwise the complete set,
// with C-23 keys. The response lists every skill the agent can use (its
// granted installed skills and the project skills of its mounts).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// requestedSkillPolicies returns the skills member of the request (the
// top-level one, or config.skills on a GET-shaped round trip) and whether
// one was sent.
func (rau *restAPIUpdateAgentTools) requestedSkillPolicies() (map[string]gen.ToolPolicy, bool) {
	switch {
	case rau.req.Skills != nil:
		return rau.req.Skills.Policies, true
	case rau.req.Config != nil && rau.req.Config.Skills != nil:
		return rau.req.Config.Skills.Policies, true
	}
	return nil, false
}

// normalizeSkillPolicies checks the requested skill overrides
// (checkAgentSkillPolicies). It writes the refusal and reports whether it
// did.
func (rau *restAPIUpdateAgentTools) normalizeSkillPolicies() bool {
	in, sent := rau.requestedSkillPolicies()
	if !sent {
		return false
	}
	rau.skillsSpecified = true
	checked, ok := checkAgentSkillPolicies(rau.w, rau.a.homePath, rau.agentID, rau.cfg.Sandbox.SkillPolicies, in)
	rau.skillPolicies = checked
	return !ok
}

// checkAgentSkillPolicies checks a complete set of per-agent skill
// overrides: no null, values allow/ask/deny, keys by the per-agent C-23
// rule, and the Omnipus rule (no per-agent Ask). On a refusal it writes
// the answer and returns ok false.
func checkAgentSkillPolicies(w http.ResponseWriter, home, agentID string, global map[string]string,
	in map[string]gen.ToolPolicy,
) (map[string]config.ToolPolicy, bool) {
	if in == nil {
		jsonErr(w, http.StatusBadRequest, "`skills.policies` cannot be empty.")
		return nil, false
	}
	raw := make(map[string]string, len(in))
	for k, v := range in {
		if v == "" { // null decodes to ""
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("`%s` cannot be empty.", k))
			return nil, false
		}
		if !isPolicyValue(string(v)) {
			jsonErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("invalid policy %q for skill %q", v, k))
			return nil, false
		}
		raw[k] = string(v)
	}
	checked, err := skills.CheckSkillPolicyMap(tools.AgentSkillKeyScope(home, agentID, global), raw)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	out := make(map[string]config.ToolPolicy, len(checked))
	for k, v := range checked {
		out[k] = config.ToolPolicy(v)
	}
	if _, bad := coreagent.OmnipusPerAgentAskViolation(agentID, out); bad {
		// S-22: the Omnipus text starts the message.
		jsonErr(w, http.StatusUnprocessableEntity, coreagent.OmnipusPerAgentAskRefusal)
		return nil, false
	}
	return out, true
}

// applySkillPolicies stores the checked overrides on the agent record:
// an empty set clears them all.
func (rau *restAPIUpdateAgentTools) applySkillPolicies(tc *config.AgentToolsCfg) {
	if !rau.skillsSpecified {
		return
	}
	if len(rau.skillPolicies) == 0 {
		tc.Skills = nil
		return
	}
	tc.Skills = &config.AgentSkillsCfg{Policies: rau.skillPolicies}
}

// agentSkillEntries is AgentToolsResponse.skills for one agent. Values are
// read from config.json, as the turn reads them, so the response shows
// what runs.
func (a *restAPI) agentSkillEntries(ac *config.AgentConfig) ([]gen.AgentSkillEntry, error) {
	out := []gen.AgentSkillEntry{}
	if ac == nil {
		return out, nil
	}
	settings, err := config.ReadPersistedSkillSettings(a.homePath)
	if err != nil {
		return nil, err
	}
	var agentSkills *config.AgentSkillsCfg
	if ac.Tools != nil {
		agentSkills = ac.Tools.Skills
	}
	skillsDir := filepath.Join(a.homePath, "skills")
	names := map[string]string{}
	for _, s := range skills.NewSkillsLoader("", skillsDir, "").ListSkills() {
		names[config.NormalizeSkillKey(s.ID)] = s.Name
	}
	for _, row := range tools.AgentSkillPermissions(a.homePath, settings, ac.ID, ac.Skills, agentSkills) {
		entry := gen.AgentSkillEntry{
			Key:             row.Skill,
			Name:            row.Skill,
			DisplayName:     row.Skill,
			EffectivePolicy: gen.ToolPolicy(row.Effective),
			GlobalPolicy:    gen.ToolPolicy(globalSkillPolicy(settings, row.Skill)),
		}
		if row.Override != nil {
			v := gen.AgentSkillEntryConfiguredPolicy(*row.Override)
			entry.ConfiguredPolicy = &v
		}
		if ws, slug, ok := config.ParseProjectSkillKey(row.Skill); ok {
			entry.Name, entry.DisplayName = slug, slug
			entry.ProjectWorkspace = &ws
			entry.Unverified = true
		} else {
			entry.Granted = true
			if n := names[row.Skill]; n != "" {
				entry.DisplayName = n
			}
			verified, _, verr := skills.SkillVerified(skillsDir, row.Skill)
			entry.Unverified = verr != nil || !verified
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// globalSkillPolicy is the stored global permission of a skill key; an
// installed skill with no stored value is ask (C-2, ST-Q3 B).
func globalSkillPolicy(settings config.PersistedSkillSettings, key string) string {
	if v := settings.Policies[key]; v != "" {
		return v
	}
	return string(config.ToolPolicyAsk)
}

// bufferRequestBody reads the body (1 MB cap) and puts it back for the
// decoder, so the raw members can be checked for null afterwards.
func bufferRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "The request body could not be read.")
		return nil, false
	}
	raw = dropSkillRowsEcho(raw)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, true
}

// dropSkillRowsEcho removes a top-level skills ARRAY: that is the GET
// response's read-only rows (AgentSkillEntry[]), echoed back by a client
// that writes the GET body as is (D-86). Only the skills OBJECT
// ({policies}) is a write. Any other body is returned unchanged.
func dropSkillRowsEcho(raw []byte) []byte {
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return raw
	}
	v, ok := doc["skills"]
	if !ok || !bytes.HasPrefix(bytes.TrimSpace(v), []byte("[")) {
		return raw
	}
	delete(doc, "skills")
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

// refuseNullSkills refuses skills: null (top level or under config); the
// generated pointer cannot tell null from absent (C-4). It reports whether
// it wrote the refusal.
func refuseNullSkills(w http.ResponseWriter, body []byte) bool {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return false // the decoder above already ruled on the body
	}
	if v, ok := doc["skills"]; ok && isJSONNull(v) {
		jsonErr(w, http.StatusBadRequest, "`skills` cannot be empty.")
		return true
	}
	var cfg map[string]json.RawMessage
	if v, ok := doc["config"]; ok && json.Unmarshal(v, &cfg) == nil {
		if sk, ok := cfg["skills"]; ok && isJSONNull(sk) {
			jsonErr(w, http.StatusBadRequest, "`config.skills` cannot be empty.")
			return true
		}
	}
	return false
}
