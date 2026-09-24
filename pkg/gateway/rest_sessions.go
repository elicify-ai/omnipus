// rest_sessions.go: Session CRUD, messages, and the session wire shapes

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// jsonSessionDetail writes a response that conforms to the gen.SessionDetail wire
// shape: { session, messages, agent_removed? }.
// The domain types (session.UnifiedMeta, session.TranscriptEntry) serialize via
// their own json tags to JSON layouts that match SessionDetail.yaml / Session.yaml /
// Message.yaml — we exploit that to avoid a field-by-field copy into gen.SessionDetail.
// The anonymous struct is not a named wire-format type and therefore does not trigger
// the check-no-handwritten-wire-types lint rule.
// jsonSessionDetail serializes a session detail response. The internal
// session.UnifiedMeta is converted to gen.Session via unifiedMetaToGenSession
// so that required-but-empty array fields (e.g. partitions) marshal as []
// rather than null, honoring the Session.yaml contract (zod schema validates
// type:array, rejecting null and dropping the whole list).
//
// Messages stay as []session.TranscriptEntry: the Message.yaml schema only
// requires {id, timestamp, agent_id}, and every other field uses omitempty
// in Go — so nil slices/maps are omitted, not emitted as null.
func jsonSessionDetail(
	w http.ResponseWriter,
	meta *session.UnifiedMeta,
	messages []session.TranscriptEntry,
	agentRemoved bool,
) {
	genSession := unifiedMetaToGenSession(meta)
	if messages == nil {
		messages = []session.TranscriptEntry{}
	}
	if agentRemoved {
		jsonOK(w, struct {
			Session      gen.Session               `json:"session"`
			Messages     []session.TranscriptEntry `json:"messages"`
			AgentRemoved bool                      `json:"agent_removed,omitempty"`
		}{Session: genSession, Messages: messages, AgentRemoved: agentRemoved})
		return
	}
	jsonOK(w, struct {
		Session  gen.Session               `json:"session"`
		Messages []session.TranscriptEntry `json:"messages"`
	}{Session: genSession, Messages: messages})
}

// --- Sessions ---

// HandleSessions routes /api/v1/sessions requests: GET (list/detail/messages/tool-results), POST (create), PUT (rename), DELETE (delete).
func (a *restAPI) HandleSessions(w http.ResponseWriter, r *http.Request) {
	// Extract optional session ID and sub-path from the URL.
	// Supports: /api/v1/sessions, /api/v1/sessions/{id}, /api/v1/sessions/{id}/messages,
	//           /api/v1/sessions/{id}/tool-results/{ref}
	path := strings.TrimSuffix(r.URL.Path, "/")
	remainder := strings.TrimPrefix(path, "/api/v1/sessions")
	remainder = strings.TrimPrefix(remainder, "/")

	var sessionID, subPath string
	if remainder != "" {
		parts := strings.SplitN(remainder, "/", 2)
		sessionID = parts[0]
		if len(parts) > 1 {
			subPath = parts[1]
		}
	}

	if sessionID != "" {
		if err := validateEntityID(sessionID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid session ID")
			return
		}
	}

	// Dispatch tool-results sub-resource before the generic method switch so the
	// full path (including ref) is available to HandleToolResults via r.URL.Path.
	if strings.HasPrefix(subPath, "tool-results/") || subPath == "tool-results" {
		a.HandleToolResults(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if sessionID == "" {
			a.listSessions(w, r)
		} else if subPath == "messages" {
			a.getSessionMessages(w, r, sessionID)
		} else {
			a.getSession(w, r, sessionID)
		}
	case http.MethodPost:
		if sessionID == "" {
			a.createSessionHTTP(w, r)
		} else {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case http.MethodPut:
		if sessionID == "" {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		} else {
			a.renameSession(w, r, sessionID)
		}
	case http.MethodDelete:
		if sessionID == "" {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		} else {
			a.deleteSession(w, r, sessionID)
		}
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// sanitizePartialError extracts only the agent ID from a ListAllSessions partial
// error and returns an opaque failure token. ListAllSessions wraps errors as
// "agent=<id>: <underlying>". We keep only the agent prefix so that filesystem
// paths, syscall messages, and permission strings are never leaked to REST clients.
// The full error is always logged server-side before calling this function.
func sanitizePartialError(pe error) string {
	msg := pe.Error()
	if idx := strings.Index(msg, ": "); idx > 0 {
		return msg[:idx] + ": session_list_failed"
	}
	return "session_list_failed"
}

// intPtrIfPositive returns &n when n > 0, else nil (for omitempty wire fields).
func intPtrIfPositive(n int) *int {
	if n > 0 {
		return &n
	}
	return nil
}

// modelEntry aliases the oapi-codegen-inlined Session.Stats.by_model element so
// it can be referenced by name (Go cannot name the anonymous inline struct).
type modelEntry = struct { // not-wire-format: alias of the codegen-inlined Session.Stats.by_model element; canonical wire schema is contracts/components/schemas/ModelTokens.yaml, not a new type
	CacheRead  *int `json:"cache_read,omitempty"`
	CacheWrite *int `json:"cache_write,omitempty"`
	In         *int `json:"in,omitempty"`
	Out        *int `json:"out,omitempty"`
	Total      int  `json:"total"`
}

// unifiedMetaToGenSession converts a session.UnifiedMeta to the generated gen.Session wire type.
// The two types have matching JSON field names; this explicit conversion satisfies the Go type checker.
func unifiedMetaToGenSession(m *session.UnifiedMeta) gen.Session {
	// Build the optional per-model breakdown for the inline Stats struct.
	var byModel *map[string]modelEntry
	if len(m.Stats.ByModel) > 0 {
		mm := make(map[string]modelEntry, len(m.Stats.ByModel))
		for model, mt := range m.Stats.ByModel {
			mm[model] = modelEntry{
				In:         intPtrIfPositive(mt.In),
				Out:        intPtrIfPositive(mt.Out),
				CacheRead:  intPtrIfPositive(mt.CacheRead),
				CacheWrite: intPtrIfPositive(mt.CacheWrite),
				Total:      mt.Total,
			}
		}
		byModel = &mm
	}

	s := gen.Session{
		Id:        m.ID,
		AgentId:   m.AgentID,
		Channel:   m.Channel,
		CreatedAt: m.CreatedAt,
		// UpdatedAt was previously omitted, so every session serialized Go's
		// zero time ("0001-01-01T00:00:00Z") — breaking session sort order and
		// the relative-time display. The meta carries a real UpdatedAt (stamped
		// on every message append; ListSessions sorts by it), so map it through.
		UpdatedAt: m.UpdatedAt,
		Title:     m.Title,
		Status:    gen.SessionStatus(m.Status),
		Partitions: func() []string {
			if m.Partitions == nil {
				return []string{}
			}
			return m.Partitions
		}(),
		Stats: struct {
			ByModel *map[string]struct {
				CacheRead  *int `json:"cache_read,omitempty"`
				CacheWrite *int `json:"cache_write,omitempty"`
				In         *int `json:"in,omitempty"`
				Out        *int `json:"out,omitempty"`
				Total      int  `json:"total"`
			} `json:"by_model,omitempty"`
			Cost             float64 `json:"cost"`
			MessageCount     int     `json:"message_count"`
			TokensCacheRead  *int    `json:"tokens_cache_read,omitempty"`
			TokensCacheWrite *int    `json:"tokens_cache_write,omitempty"`
			TokensIn         int     `json:"tokens_in"`
			TokensOut        int     `json:"tokens_out"`
			TokensTotal      int     `json:"tokens_total"`
			ToolCalls        int     `json:"tool_calls"`
		}{
			ByModel:          byModel,
			Cost:             m.Stats.Cost,
			MessageCount:     m.Stats.MessageCount,
			TokensCacheRead:  intPtrIfPositive(m.Stats.TokensCacheRead),
			TokensCacheWrite: intPtrIfPositive(m.Stats.TokensCacheWrite),
			TokensIn:         m.Stats.TokensIn,
			TokensOut:        m.Stats.TokensOut,
			TokensTotal:      m.Stats.TokensTotal,
			ToolCalls:        m.Stats.ToolCalls,
		},
	}
	if m.Model != "" {
		s.Model = &m.Model
	}
	if m.Provider != "" {
		s.Provider = &m.Provider
	}
	if m.WorkspaceID != "" {
		s.WorkspaceId = &m.WorkspaceID
	}
	if m.TaskID != "" {
		s.TaskId = &m.TaskID
	}
	if m.LastCompactionSummary != "" {
		s.LastCompactionSummary = &m.LastCompactionSummary
	}
	if m.ActiveAgentID != "" {
		s.ActiveAgentId = &m.ActiveAgentID
	}
	// ADR-057 FR-008/FR-091: present only on a subordinate (delegated child)
	// session; absent (never empty-string) on a root. A session whose
	// ParentSessionID names an id that no longer resolves is still surfaced
	// as a root by listSessions (FR-091, BDD-106) — that resolution happens
	// at the listing layer, not here; this mapping is a pure field copy.
	if m.ParentSessionID != "" {
		s.ParentSessionId = &m.ParentSessionID
	}
	if len(m.AgentIDs) > 0 {
		ids := make([]string, len(m.AgentIDs))
		copy(ids, m.AgentIDs)
		s.AgentIds = &ids
	}
	if len(m.CompactionSummaries) > 0 {
		cs := make(map[string]string, len(m.CompactionSummaries))
		for k, v := range m.CompactionSummaries {
			cs[k] = v
		}
		s.CompactionSummaries = &cs
	}
	sessionType := gen.SessionType(m.Type)
	s.Type = &sessionType
	return s
}

// computeSessionProtected derives the computed `protected` flag for a session
// (FR-021/028). A session is protected when:
//   - its type is "heartbeat", AND
//   - the workspace it belongs to still has member_configs[agentID].heartbeat.enabled=true
//     with session_id == this session's ID
//
// For any other session type, returns nil (absent on the wire — field is omitted).
// Disk reads are bounded: we only load the one workspace identified by session.WorkspaceID.
func computeSessionProtected(homePath string, m *session.UnifiedMeta) *bool {
	if m == nil || m.Type != session.SessionTypeHeartbeat || m.WorkspaceID == "" {
		return nil
	}
	ws, err := readWorkspaceFile(homePath, m.WorkspaceID)
	if err != nil {
		// MEDIUM-2: distinguish workspace-not-found (deleted) from I/O / corruption.
		// - Workspace deleted: correct, the session is no longer protected.
		// - Any other error (corrupt JSON, I/O): fail CLOSED so a transient read
		//   error never silently unprotects an active heartbeat session.
		if errors.Is(err, errWorkspaceNotFound) {
			slog.Debug("computeSessionProtected: workspace not found (deleted)",
				"workspace_id", m.WorkspaceID, "session_id", m.ID)
			f := false
			return &f
		}
		slog.Warn("computeSessionProtected: workspace load error (fail closed)",
			"workspace_id", m.WorkspaceID, "session_id", m.ID, "error", err)
		t := true
		return &t
	}
	// FIX-4b: require the agent still be on the workspace's CoreTeam. A stale
	// member_config entry for an off-team agent must not keep its session protected.
	inCoreTeam := false
	for _, id := range ws.CoreTeam {
		if id == m.AgentID {
			inCoreTeam = true
			break
		}
	}
	if !inCoreTeam {
		slog.Debug("computeSessionProtected: agent not in CoreTeam (stale entry)",
			"workspace_id", m.WorkspaceID, "agent_id", m.AgentID, "session_id", m.ID)
		f := false
		return &f
	}
	mc, hasMC := ws.MemberConfigs[m.AgentID]
	if !hasMC || mc.Heartbeat == nil {
		f := false
		return &f
	}
	// Protected only when the heartbeat is enabled AND the stored session_id
	// matches this session (not a replaced/rotated session).
	protected := mc.Heartbeat.Enabled && mc.Heartbeat.SessionID == m.ID
	return &protected
}

// u18DefaultSessionPageLimit is the page size GET /api/v1/sessions uses when
// the caller omits `limit` (ADR-057 FR-092). The response body scales with
// this number, not with total session count (FR-092(b) — boundary cost is
// O(page)), regardless of how many sessions the merged store set holds.
const u18DefaultSessionPageLimit = 50

// listSessions handles GET /api/v1/sessions (ADR-057 US-19/FR-091/FR-092/
// FR-097/FR-098/FR-104, W16c). Replaces the pre-pagination "load everything,
// filter, return" handler with the REST layer of the four-layer pagination
// stack (store U6 -> loop U9 -> REST here -> client U12): it accepts paging
// parameters, the parent_session_id / flat hierarchy switches, and returns
// the single named gen.SessionPage envelope FR-091 decided on (replacing the
// retired two-variant oneOf, gen.ListSessions200JSONResponseBody1 — see
// ADR-034/grill2 M2-10; U10 owns the contract, this handler is the consumer).
//
// Division of labor: AgentLoop.ListAllSessions (U9) applies hierarchy
// (roots-only / direct-children / flat) and FR-098's cross-store ordering +
// cursor BEFORE pagination, over the full merged set, so a page boundary can
// never split a parent from its child-count context. This handler's own job
// is the REST-layer concerns FR-092/FR-104 explicitly leave here: parsing
// and validating limit/offset, the flat+parent_session_id 400, narrowing by
// agent_id/type/include_verifier (orthogonal filters that only ever shrink a
// page, never grow it past limit), resolving each row's child_count from
// whichever store's in-memory parent index owns it (FR-097, O(1) per row),
// and building the SessionPage response.
func (a *restAPI) listSessions(w http.ResponseWriter, r *http.Request) {
	agentFilter := r.URL.Query().Get("agent_id")
	typeFilter := r.URL.Query().Get("type")
	// ADR-052 FR-036/US-13 Acceptance 6: verifier-role sessions are excluded
	// by default, REGARDLESS of the type filter — ?type=verifier alone does
	// NOT surface them; the operator must also pass include_verifier=true.
	// strconv.ParseBool accepts "1"/"t"/"T"/"TRUE"/"true"/"True" (and their
	// false counterparts); any absent/unparseable value defaults to false
	// per the contract (ListSessionsParams.IncludeVerifier default: false).
	includeVerifier, _ := strconv.ParseBool(r.URL.Query().Get("include_verifier"))

	parentSessionID := r.URL.Query().Get("parent_session_id")
	flat, _ := strconv.ParseBool(r.URL.Query().Get("flat"))
	// FR-104: flat=true and parent_session_id are mutually exclusive — a 400,
	// not a silent "flat wins" or "parent_session_id wins".
	if flat && parentSessionID != "" {
		jsonErr(w, http.StatusBadRequest, "flat and parent_session_id are mutually exclusive")
		return
	}

	limit := u18DefaultSessionPageLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			jsonErr(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			jsonErr(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		offset = n
	}

	page, partialErrs := a.agentLoop.ListAllSessions(limit, offset, parentSessionID, flat)
	for _, pe := range partialErrs {
		slog.Warn("rest: list sessions: partial error", "error", pe)
	}

	// Apply the orthogonal agent_id/type/include_verifier filters over this
	// page's rows. These narrow AFTER hierarchy+pagination (ListAllSessions'
	// own doc comment: "the 400 for supplying both is a REST-layer concern,
	// not this method's" applies equally to these filters) — they can only
	// shrink a page below `limit`, never grow it past `limit`, so FR-092(b)'s
	// "response body scales with limit, not total session count" still holds.
	filtered := make([]*session.UnifiedMeta, 0, len(page.Sessions))
	for _, m := range page.Sessions {
		if agentFilter != "" && m.AgentID != agentFilter {
			continue
		}
		if typeFilter != "" && string(m.Type) != typeFilter {
			continue
		}
		if m.Type == session.SessionTypeVerifier && !includeVerifier {
			continue
		}
		filtered = append(filtered, m)
	}

	// Always route through unifiedMetaToGenSession so that required array/map
	// fields (Partitions in particular) marshal as [] not null — Zod on the SPA
	// rejects null where the contract says type:array and drops the whole list.
	genSessions := make([]gen.Session, 0, len(filtered))
	for _, m := range filtered {
		s := unifiedMetaToGenSession(m)
		// FR-021/028: compute the `protected` flag for heartbeat sessions.
		// Non-heartbeat sessions get nil (field omitted from the wire response).
		s.Protected = computeSessionProtected(a.homePath, m)
		// FR-091/FR-097: child_count is resolved from whichever store's
		// in-memory parent index owns this session — O(1) per row, no disk
		// read, regardless of listing mode (roots-only, parent_session_id, or
		// flat). A session this handler cannot resolve a store for (should
		// not happen — it just came from ListAllSessions) is left at zero
		// rather than surfacing a spurious count.
		if store := a.resolveSessionStore(m.ID); store != nil {
			cc := store.ChildCount(m.ID)
			s.ChildCount = &cc
		}
		genSessions = append(genSessions, s)
	}

	resp := gen.SessionPage{Sessions: genSessions}
	if page.NextOffset >= 0 {
		nc := strconv.Itoa(page.NextOffset)
		resp.NextCursor = &nc
	}
	if len(partialErrs) > 0 {
		// FR-098(c): a store that errored mid-merge still yields a valid page
		// plus next_cursor — partial_errors composes with paging rather than
		// halting it.
		sanitized := make([]string, len(partialErrs))
		for i, pe := range partialErrs {
			sanitized[i] = sanitizePartialError(pe)
		}
		resp.PartialErrors = &sanitized
	}
	jsonOK(w, resp)
}

func (a *restAPI) getSession(w http.ResponseWriter, _ *http.Request, id string) {
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}
	meta, err := store.GetMeta(id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("session not found: %v", err))
		return
	}
	messages, err := store.ReadTranscript(id)
	if err != nil {
		slog.Error("rest: could not read transcript", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read transcript: %v", err))
		return
	}
	// ADR-057 D1/W11 (FR-034/FR-038): a delegated child owns its own real
	// session (FR-005), so its narration lives in the CHILD's OWN
	// transcript.jsonl and is simply never present here — the old REST-side
	// visibility filter helper is deleted, not reapplied (FR-035). This
	// boundary now returns id's full transcript unfiltered, same as every
	// other read boundary (FR-035/FR-037/FR-038, BDD-37).
	// Detect ghost sessions: if the session references an agent that no longer
	// exists in the current config, surface agent_removed=true so the frontend
	// can show the read-only "Agent removed" banner (#103).
	agentRemoved := false
	if meta.AgentID != "" {
		cfg := a.agentLoop.GetConfig()
		found := false
		for _, ac := range cfg.Agents.List {
			if ac.ID == meta.AgentID {
				found = true
				break
			}
		}
		agentRemoved = !found
	}
	// Build response matching gen.SessionDetail wire shape:
	// { session, messages, agent_removed? }
	// The domain types (session.UnifiedMeta, session.TranscriptEntry) serialize to
	// the same JSON layout defined in SessionDetail.yaml and Session.yaml/Message.yaml.
	// Using jsonSessionDetail avoids an import cycle while staying lint-compliant.
	jsonSessionDetail(w, meta, messages, agentRemoved)
}

func (a *restAPI) getSessionMessages(w http.ResponseWriter, _ *http.Request, id string) {
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}
	messages, err := store.ReadTranscript(id)
	if err != nil {
		slog.Error("rest: could not read transcript", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read transcript: %v", err))
		return
	}
	// ADR-057 D1/W11 (FR-034/FR-038): see getSession's identical note above —
	// the old delegate-narration visibility filter is deleted outright, not
	// reapplied; a child's own entries never land in another session's
	// transcript to begin with under the post-D1 design.
	// Coerce nil → empty slice so JSON marshals as [] not null. The SPA's
	// fetchSessionMessages validates via z.array(WireMessageSchema), which
	// rejects null — a fresh session with no transcript would surface as
	// "Could not load messages." in the UI.
	if messages == nil {
		messages = []session.TranscriptEntry{}
	}
	// review r2 RV2: every entry passes through as the raw TranscriptEntry
	// (unchanged shape) EXCEPT EntryTypeJudgeVerdict, whose Content is a raw
	// json.Marshal(task.JudgeVerdict) string with no typed field to carry it
	// on the wire. Before this fix, cold-load rendered an empty/broken verdict
	// card because Message.verdict was never populated. Parse Content and
	// attach it as "verdict" (same shape handleTaskVerdicts/toWireJudgeVerdict
	// produces, rest_tasks.go) so cold-load and live/replay can never disagree.
	out := make([]any, 0, len(messages))
	for _, entry := range messages {
		out = append(out, withWireJudgeVerdict(id, entry))
	}
	jsonOK(w, out)
}

// withWireJudgeVerdict returns entry unchanged for every entry type except
// EntryTypeJudgeVerdict, for which it returns a JSON-object representation of
// entry (all its own fields, unchanged) plus an added "verdict" field parsed
// from entry.Content and converted via toWireJudgeVerdict — the wire
// Message.verdict shape (review r2 RV2). On any parse/marshal failure it logs
// and falls back to the raw entry (verdict simply absent) rather than
// dropping the entry or failing the whole response.
func withWireJudgeVerdict(sessionID string, entry session.TranscriptEntry) any {
	if entry.Type != session.EntryTypeJudgeVerdict || entry.Content == "" {
		return entry
	}
	var verdict task.JudgeVerdict
	if uerr := json.Unmarshal([]byte(entry.Content), &verdict); uerr != nil {
		slog.Warn("rest: could not parse judge_verdict transcript entry — cold-load will omit verdict",
			"session_id", sessionID, "entry_id", entry.ID, "error", uerr)
		return entry
	}
	raw, merr := json.Marshal(entry)
	if merr != nil {
		slog.Error("rest: could not marshal judge_verdict transcript entry",
			"session_id", sessionID, "entry_id", entry.ID, "error", merr)
		return entry
	}
	var m map[string]any
	if uerr := json.Unmarshal(raw, &m); uerr != nil {
		slog.Error("rest: could not decode judge_verdict transcript entry to map",
			"session_id", sessionID, "entry_id", entry.ID, "error", uerr)
		return entry
	}
	m["verdict"] = toWireJudgeVerdict(verdict)
	return m
}

// renameSession handles PUT /api/v1/sessions/{id}.
// Accepts {"title": "new name"} and returns the updated session meta.
func (a *restAPI) renameSession(w http.ResponseWriter, r *http.Request, id string) {
	var req gen.SessionRenameRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SessionRenameRequest", &req, validateEnabled) {
		return
	}
	// A bare `== ""` check is not enough: "   " and invisible/zero-width runes
	// (ZWSP, ZWNJ, word joiner, BOM, soft hyphen, U+2800 …) both pass it and
	// produce a session that renders blank and is unfindable in the sidebar.
	// Same class of hole UAT found on plan/task titles — task.HasVisibleContent
	// is the shared predicate, so this stays fixed with them rather than
	// drifting into a second, weaker rule.
	req.Title = strings.TrimSpace(req.Title)
	if !task.HasVisibleContent(req.Title) {
		jsonErr(w, http.StatusBadRequest, "title is required")
		return
	}
	// Length is checked AFTER trimming so trailing padding can't push an
	// otherwise-valid title over the limit.
	if len(req.Title) > 256 {
		jsonErr(w, http.StatusBadRequest, "title too long (max 256 characters)")
		return
	}
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err := store.SetMeta(id, session.MetaPatch{Title: &req.Title}); err != nil {
		slog.Error("rest: rename session", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not rename session: %v", err))
		return
	}
	meta, err := store.GetMeta(id)
	if err != nil {
		slog.Error("rest: rename session: get meta after update", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read updated session: %v", err))
		return
	}
	jsonOK(w, unifiedMetaToGenSession(meta))
}

// deleteSession handles DELETE /api/v1/sessions/{id}.
// Removes all session data and returns {"success": true}.
func (a *restAPI) deleteSession(w http.ResponseWriter, r *http.Request, id string) {
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}

	// FR-014 / US-7: reject deletion of an active heartbeat session with 409.
	// Load the meta to check the session type before attempting the delete so
	// we don't make a half-deletion attempt and then fail. The workspace load
	// is bounded by the session's WorkspaceID (no full scan).
	meta, metaErr := store.GetMeta(id)
	if metaErr != nil {
		// MEDIUM-1: fail CLOSED on meta-read error — a session whose metadata
		// cannot be read must not be silently deleted past the heartbeat guard.
		slog.Error("rest: delete session: could not read session meta",
			"session_id", id, "error", metaErr)
		jsonErr(w, http.StatusInternalServerError, "could not verify session protection")
		return
	}
	if meta != nil && meta.Type == session.SessionTypeHeartbeat {
		if isProtected := computeSessionProtected(a.homePath, meta); isProtected != nil && *isProtected {
			// C-1 (FR-014): audit the blocked delete before returning 409.
			if a.auditor != nil {
				if err := a.auditor.Log(&audit.Entry{
					Event:    "session.delete.blocked",
					Decision: audit.DecisionDeny,
					AgentID:  meta.AgentID,
					Details: map[string]any{
						"session_id":   id,
						"workspace_id": meta.WorkspaceID,
						"agent_id":     meta.AgentID,
						"reason":       "heartbeat enabled",
					},
				}); err != nil {
					slog.Warn("audit write failed", "event", "session.delete.blocked",
						"session_id", id, "error", err)
				}
			}
			jsonErr(w, http.StatusConflict,
				"cannot delete a protected heartbeat session while its heartbeat is enabled; "+
					"disable the heartbeat in the workspace settings first")
			return
		}
	}

	// Deleting a session is also a REST-originated Stop boundary: stamp and
	// cancel its durable steering subtree before any session data disappears.
	// A partial cascade aborts deletion instead of reporting success while an
	// unreadable descendant may still be running. The current OpenAPI delete
	// response has no cancel-report fields; until a future response
	// schema publishes one, the error body carries the same one-line partial summary used by
	// the WebSocket channel and no undocumented wire fields are emitted.
	report, cascaded := cancelSteeredSubtree(r.Context(), a.agentLoop, id, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   actorUsername(r),
	})
	// [Finding 3, ADR-091 fix lane 2] SkippedNewerGeneration also refuses
	// deletion: a descendant whose live turn had already advanced past the
	// generation this Stop stamped is STILL RUNNING, exactly the "may still
	// be running" case this guard exists to catch. That is deliberately a
	// WIDER condition than the `partial` flag on the cancel_stage frame,
	// which per WP-D FR-D-001 means "unreachable" alone: a newer generation
	// taking over is a CORRECT Stop outcome, but it is still a live turn, and
	// deleting its session data is what this guard refuses. Hence
	// cancelIncompleteSubtreeSummary, not cancelPartialSummary — the latter
	// is empty in the skipped-only case and would leave this 500 with no
	// reason in its body.
	if cascaded && (len(report.Unreachable) > 0 || len(report.SkippedNewerGeneration) > 0) {
		summary := cancelIncompleteSubtreeSummary(report)
		slog.Warn("rest: delete session: Stop cascade incomplete; deletion refused",
			"session_id", id, "summary", summary,
			"unreachable", report.Unreachable, "skipped_newer_generation", report.SkippedNewerGeneration)
		jsonErr(w, http.StatusInternalServerError, summary)
		return
	}

	// ADR-057 W18b (FR-071/BDD-78): resolve id's full descendant set BEFORE
	// deleting anything, over the DURABLE lifecycle store — every delegation,
	// live or not, has a LifecycleRecord (User Story 4), so this walk is
	// authoritative independent of turn liveness and survives a restart.
	// Reuses U11's already-tested u11CollectDescendantSessionIDs (same
	// package, pkg/gateway/websocket.go), which walks U13's SteeringSessionID
	// index (pkg/session/lifecycle.go) exactly as the cancel/approval-cascade
	// paths do — this handler does not reimplement the walk. A nil lifecycle
	// store (no delegation store wired — most webchat-only installs never
	// mint one) degrades to zero descendants, matching that helper's
	// documented nil-store behavior.
	descendantIDs := u11CollectDescendantSessionIDs(a.agentLoop.GetSessionLifecycleStore(), id)

	if err := store.DeleteSession(id); err != nil {
		slog.Error("rest: delete session", "session_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not delete session: %v", err))
		return
	}

	// ADR-057 W18b (FR-071): id's OWN <home>/uploads/<id>/ was already
	// removed by store.DeleteSession above (pre-existing ADR-017 cascade,
	// unified.go's DeleteSession). Under D1 each delegated descendant now
	// owns its OWN session — and therefore its OWN uploads/<descendantID>/
	// directory (FR-010) — which that per-id cascade cannot reach. Sweep
	// every descendant's upload tree here so deleting a parent chat does not
	// leave every delegated child's uploaded media permanently orphaned on
	// disk (US-16: "a silent disk leak, not a correctness break" — hence
	// best-effort, logged, non-fatal, matching the media-store release below).
	if len(descendantIDs) > 0 {
		if err := media.RemoveSessionUploadsTree(descendantIDs); err != nil {
			slog.Warn("rest: delete session: cascade-delete descendant uploads failed",
				"session_id", id, "descendant_count", len(descendantIDs), "error", err)
		}
	}

	// Release in-memory media store refs for any tool-generated inline media
	// (screenshots, charts, etc.) that were stored with CleanupPolicyForgetOnly
	// under the session-scoped scope media.SessionInlineScopePrefix+"<id>".
	// The underlying files are already on disk under uploads/<id>/ and have been
	// cascade-deleted by DeleteSession above; we only need to drop the in-memory
	// ref so the store index does not accumulate stale entries.
	mediaStore := a.agentLoop.GetMediaStore()
	if mediaStore == nil {
		mediaStore = a.mediaStore
	}
	if mediaStore != nil {
		if err := mediaStore.ReleaseAll(media.SessionInlineScopePrefix + id); err != nil {
			// Non-fatal: session data is already removed. Log and continue.
			slog.Warn("rest: delete session: media store release failed",
				"session_id", id, "error", err)
		}
	}

	jsonOK(w, map[string]bool{"success": true})
}

// firstChatTargetAgentID returns the ID of the first chat-target agent in the
// config list, or "" when no such agent is configured. Used as a last-resort
// fallback after GetDefaultAgent() — mirrors resolveDefaultAgentID in
// pkg/routing/route.go. Workers are NOT chat targets, so they are skipped here:
// a last-resort fallback must never land on a worker, which is invoked only via
// delegation.
func firstChatTargetAgentID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	for _, ag := range cfg.Agents.List {
		if ag.IsChatTarget() {
			return ag.ID
		}
	}
	return ""
}

// isWorkerAgentID reports whether agentID resolves to a worker agent in the config.
// Returns false for an unknown agent ID (existence is validated separately by the
// caller). Used by gateway session-binding chokepoints to reject a worker as a chat
// target — a worker is a delegation-only labor tier, never a live chat persona.
func isWorkerAgentID(cfg *config.Config, agentID string) bool {
	if cfg == nil || agentID == "" {
		return false
	}
	ac := findAgentConfig(cfg, agentID)
	return ac != nil && ac.IsWorker()
}

func (a *restAPI) createSessionHTTP(w http.ResponseWriter, r *http.Request) {
	var req gen.SessionCreateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SessionCreateRequest", &req, validateEnabled) {
		return
	}

	agentID := ""
	if req.AgentId != nil {
		agentID = *req.AgentId
	}
	if agentID == "" {
		if reg := a.agentLoop.GetRegistry(); reg != nil {
			if def := reg.GetDefaultAgent(); def != nil {
				agentID = def.ID
			}
		}
		if agentID == "" {
			// Fall back to the first chat-target agent (mirrors handleBoardTaskStart /
			// resolveDefaultAgentID in pkg/routing/route.go).
			agentID = firstChatTargetAgentID(a.agentLoop.GetConfig())
		}
	}
	if err := validateEntityID(agentID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid agent_id")
		return
	}
	// Validate the agent exists before creating the session.
	if agentStore := a.agentLoop.GetAgentStore(agentID); agentStore == nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("agent %q not found", agentID))
		return
	}
	// A worker is a delegation-only labor tier — never a chat target. A session
	// backs a live chat, so an explicit worker agent_id must be rejected (mirrors
	// setChannelRouting's worker 400). Both no-agent fallbacks above already skip
	// workers, so this only ever rejects an explicitly-supplied worker.
	if isWorkerAgentID(a.agentLoop.GetConfig(), agentID) {
		jsonErr(w, http.StatusBadRequest, "workers are not chat targets and cannot back a session")
		return
	}

	// Use the shared session store for new sessions (joined session model).
	// Fall back to the per-agent store if the shared store is unavailable.
	store := a.agentLoop.GetSessionStore()
	if store == nil {
		store = a.agentLoop.GetAgentStore(agentID)
		if store == nil {
			jsonErr(w, http.StatusInternalServerError, "session store unavailable")
			return
		}
	}

	var sessionType session.UnifiedSessionType
	reqType := ""
	if req.Type != nil {
		reqType = string(*req.Type)
	}
	switch reqType {
	case string(session.SessionTypeTask):
		sessionType = session.SessionTypeTask
	case string(session.SessionTypeChannel):
		sessionType = session.SessionTypeChannel
	default:
		sessionType = session.SessionTypeChat
	}

	// The chat's own workspace, when the caller is in one. Validated BEFORE
	// the session is minted so a bad id costs nothing on disk.
	//
	// U2: this field exists because the SPA's "Open browser" launcher creates
	// its session here, and the live browser panel resolves which workspace's
	// browser (and whose live logins) to show by reading the workspace off the
	// attaching chat session's own meta, server-side (ADR-075 FR-016/FR-017).
	// Before this, the launcher sent agent_id and nothing else, so the session
	// it handed the panel named no workspace at all; an agent on more than one
	// workspace's team was refused under FR-033 and advised to "open this panel
	// from a chat that belongs to the workspace you mean" — which is precisely
	// where the click had come from. The route named the workspace; the session
	// simply never carried it.
	//
	// Membership is deliberately NOT checked here. This is a preference, not a
	// grant: browser.ResolveBrowsingKeyForAgent honours it only when the agent
	// really is on that workspace's team and otherwise falls through to the
	// plain membership ladder, so stamping a workspace the agent is not on
	// cannot open that workspace's browser. What IS checked is existence — a
	// session stamped with a workspace that is not there would be a binding
	// nothing can ever resolve, and silently keeping it would reproduce the
	// same "refused with no explanation" shape from the other direction.
	workspaceID := ""
	if req.WorkspaceId != nil {
		workspaceID = strings.TrimSpace(*req.WorkspaceId)
	}
	if workspaceID != "" {
		if err := validateEntityID(workspaceID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
		if _, wsErr := readWorkspaceFile(a.homePath, workspaceID); wsErr != nil {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("workspace %q not found", workspaceID))
			return
		}
	}

	meta, err := store.NewSession(sessionType, "webchat", agentID)
	if err != nil {
		slog.Error("rest: create session", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not create session: %v", err))
		return
	}
	if workspaceID != "" {
		wsCopy := workspaceID
		if setErr := store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &wsCopy}); setErr != nil {
			// Not fatal to the create — the session exists and is usable as a
			// chat. But it is fatal to the binding, and a panel that then
			// refuses would look like the original bug, so say so loudly
			// rather than returning a session that quietly lost its workspace.
			slog.Warn("rest: create session: could not stamp workspace",
				"session_id", meta.ID, "workspace_id", workspaceID, "error", setErr)
		} else if refreshed, getErr := store.GetMeta(meta.ID); getErr == nil && refreshed != nil {
			// Return what was actually persisted, so the caller's own
			// workspace_id echo is the stamp and not the request.
			meta = refreshed
		}
	}
	jsonCreated(w, unifiedMetaToGenSession(meta))
}

// resolveSessionStore finds which agent's UnifiedStore owns the given sessionID.
// Delegates to the shared AgentLoop method.
func (a *restAPI) resolveSessionStore(sessionID string) *session.UnifiedStore {
	return a.agentLoop.ResolveSessionStore(sessionID)
}
