// loop_session.go: Channel session index and listing

package agent

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// GetCurrentSession returns the active session ID for the given agent, and whether
// one was found. Used by the WebSocket lazy-CAS logic (FR-024).
func (al *AgentLoop) GetCurrentSession(agentID string) (string, bool) {
	if v, ok := al.agentCurrentSession.Load(agentID); ok {
		s, ok := v.(string)
		if !ok {
			logger.ErrorCF("agent", "agentCurrentSession: invariant violated — unexpected value type",
				map[string]any{"agent_id": agentID, "got_type": fmt.Sprintf("%T", v)})
			return "", false
		}
		return s, true
	}
	return "", false
}

// SetCurrentSession records the active session ID for the given agent.
// Used by the WebSocket lazy-CAS logic (FR-024).
func (al *AgentLoop) SetCurrentSession(agentID, sessionID string) {
	al.agentCurrentSession.Store(agentID, sessionID)
}

// GetSessionStore returns the shared UnifiedStore for new sessions. May be nil
// in tests or when the shared sessions directory could not be initialized.
func (al *AgentLoop) GetSessionStore() *session.UnifiedStore {
	return al.sharedSessionStore
}

// GetAgentStore returns the UnifiedStore for a given agent, or nil if not found
// or if the agent's session store is not a UnifiedStore.
// Use GetSessionStore() for creating new sessions; GetAgentStore is kept for
// legacy per-agent session access.
func (al *AgentLoop) GetAgentStore(agentID string) *session.UnifiedStore {
	agent, ok := al.GetRegistry().GetAgent(agentID)
	if !ok {
		return nil
	}
	us, ok := agent.Sessions.(*session.UnifiedStore)
	if !ok {
		logger.WarnCF("agent", "GetAgentStore: session store is not UnifiedStore",
			map[string]any{"agent_id": agentID})
		return nil
	}
	return us
}

// getLegacyAgentStore returns the per-agent UnifiedStore for legacy session
// access. It is an internal alias for GetAgentStore used by ListAllSessions.
func (al *AgentLoop) getLegacyAgentStore(agentID string) *session.UnifiedStore {
	return al.GetAgentStore(agentID)
}

// rebuildChannelSessionIndex populates channelSessionIdx from existing shared sessions.
// Called once after sharedSessionStore is initialized.
func (al *AgentLoop) rebuildChannelSessionIndex() {
	if al.sharedSessionStore == nil {
		return
	}
	sessions, _ := al.sharedSessionStore.ListSessions()
	for _, s := range sessions {
		if s.Channel != "" && s.Channel != "webchat" && s.PeerID != "" {
			al.channelSessionIdx.Store(s.Channel+"/"+s.PeerID, s.ID)
		}
	}
}

// resolveOrCreateChannelSession returns the shared session ID for (channel, chatID),
// creating a new session in the shared store if none exists yet.
// workspaceID is stamped onto newly-created sessions when non-empty (ADR-029
// FR-022: a session created by a bound channel instance inherits the instance's
// workspace_id). Already-existing sessions are NOT patched — the index hit
// returns the existing ID unchanged (workspace_id was set at creation time).
// Returns "" if the shared store is unavailable or inputs are empty.
func (al *AgentLoop) resolveOrCreateChannelSession(
	channel, instanceID, chatID, agentID, displayName, workspaceID string,
) string {
	if al.sharedSessionStore == nil || channel == "" || chatID == "" {
		return ""
	}
	// Index by the channel INSTANCE, not the bare type, so two instances of the
	// same type (e.g. whatsapp.eu and whatsapp.us) sharing a chat ID get DISTINCT
	// sessions (ADR-029 FR-022 / US-9 — BUG-2). instanceID is the canonical
	// instance identity (== channel for legacy single-instance); fall back
	// defensively when unstamped.
	indexID := instanceID
	if indexID == "" {
		indexID = channel
	}
	key := indexID + "/" + chatID
	if v, ok := al.channelSessionIdx.Load(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
		logger.ErrorCF("agent", "channelSessionIdx: invariant violated — unexpected value type, treating as cache miss",
			map[string]any{"key": key, "got_type": fmt.Sprintf("%T", v)})
	}
	title := displayName
	if title == "" {
		title = chatID
	}
	// Persist the instance identity, not just the bare type. The in-memory
	// index above has always keyed on it ("so two instances of the same type
	// do not collide") — the session record did not, so that distinction was
	// lost the moment the process restarted, and anything acting on "this
	// channel's sessions" could not tell a hundred WhatsApp numbers apart.
	meta, err := al.sharedSessionStore.NewChannelSession(channel, indexID, chatID, agentID, title)
	if err != nil {
		logger.WarnCF("agent", "Failed to create channel session",
			map[string]any{"channel": channel, "chat_id": chatID, "error": err.Error()})
		return ""
	}
	// FR-022: stamp workspace_id on newly-created sessions for bound instances.
	if workspaceID != "" {
		if patchErr := al.sharedSessionStore.SetMeta(
			meta.ID,
			session.MetaPatch{WorkspaceID: &workspaceID},
		); patchErr != nil {
			logger.WarnCF("agent", "Failed to stamp workspace_id on channel session",
				map[string]any{"session_id": meta.ID, "workspace_id": workspaceID, "error": patchErr.Error()})
		}
	}
	al.channelSessionIdx.Store(key, meta.ID)
	return meta.ID
}

// ResolveSessionStore finds which UnifiedStore owns the given sessionID.
// Checks the shared store first, then the main agent's legacy store, then
// all other per-agent stores. Returns nil if the session cannot be found.
//
// FIX (silent-corruption gap): each probe below used to accept a store only
// on err == nil, treating "session genuinely doesn't live here"
// (os.ErrNotExist — the frequent, legitimate case for a session that hasn't
// been created yet) and "session lives here but its meta.json is corrupt or
// unreadable" (JSON decode error, I/O error, permission error) identically —
// both fell through to the next probe, and a corrupt hit on the LAST probe
// made the function return nil exactly as if the session never existed.
// Callers (resolveWorkspaceIDForContinuation, ProcessScheduled,
// processMessage's M4 block, processSystemMessage) then proceeded as if the
// session had no workspace bound, silently dropping the binding — and their
// own downstream WARN logging (which re-reads GetMeta expecting to
// distinguish the same two cases) never fired, because they never got a
// non-nil store to call GetMeta on in the first place.
//
// A non-ErrNotExist GetMeta error on a given store is itself strong evidence
// that THIS store owns the session — readUnifiedMeta only reaches the JSON
// decode (or a bare read failure past ENOENT) once sessionDir/meta.json
// exists under that store's baseDir, so there is no reason to keep scanning
// remaining stores for a "real" copy elsewhere: continuing to probe would
// either find nothing (wasted work, same nil-after-corruption outcome) or,
// in the pathological case of a duplicate session ID across stores, mask the
// corruption behind an unrelated hit. So on a non-ErrNotExist error this
// still returns the store immediately (log first) rather than falling
// through — that is what makes the store reach the caller at all, which is
// what makes the four downstream WARNs reachable. An out-of-scope
// alternative — changing this function's signature to return the error too
// — was rejected: it would ripple into pkg/gateway/websocket.go and
// pkg/gateway/rest.go call sites outside this agent's edit scope for no
// benefit over logging here and letting callers' own GetMeta re-read see the
// identical error.
func (al *AgentLoop) ResolveSessionStore(sessionID string) *session.UnifiedStore {
	// Fast path: shared store owns new sessions.
	if al.sharedSessionStore != nil {
		if _, err := al.sharedSessionStore.GetMeta(sessionID); err == nil {
			return al.sharedSessionStore
		} else if !errors.Is(err, os.ErrNotExist) {
			logger.WarnCF(
				"agent",
				"ResolveSessionStore: session meta unreadable (not a missing-session case); returning owning store despite read failure",
				map[string]any{"session_id": sessionID, "store": al.sharedSessionStore.BaseDir(), "error": err.Error()},
			)
			return al.sharedSessionStore
		}
	}
	// Slow path: scan all per-agent stores. The former "legacy fast path"
	// here special-cased the retired "main" sentinel agent (which used to own
	// most old sessions); with the sentinel removed there is no reserved
	// agent ID to fast-path or skip, so every registered agent is scanned
	// uniformly.
	for _, id := range al.GetRegistry().ListAgentIDs() {
		store := al.GetAgentStore(id)
		if store == nil {
			continue
		}
		if _, err := store.GetMeta(sessionID); err == nil {
			return store
		} else if !errors.Is(err, os.ErrNotExist) {
			logger.WarnCF(
				"agent",
				"ResolveSessionStore: session meta unreadable (not a missing-session case); returning owning store despite read failure",
				map[string]any{"session_id": sessionID, "store": store.BaseDir(), "error": err.Error()},
			)
			return store
		}
	}
	return nil
}

// ListAllSessions returns a stably-ordered, paginated window of sessions from
// the shared store merged with legacy per-agent stores, deduplicated
// (ADR-057 FR-092/FR-098, W16b, owner U9 — the loop layer of the four-layer
// pagination stack FR-068/FR-092 requires: UnifiedStore.ListSessions (U6) ->
// AgentLoop.ListAllSessions (here) -> restAPI.listSessions (U18) ->
// fetchSessions (U12)).
//
// Ordering and pagination contract (FR-098, stated once here as this
// method's owner):
//
//   - (a) The merged sequence is ordered by UpdatedAt descending with the
//     session id as a stable tiebreak, so two sessions sharing a timestamp
//     cannot silently swap places between two calls with no intervening
//     write — mirroring UnifiedStore.ListSessions' own post-FR-097a
//     ordering exactly (pkg/session/unified.go).
//   - (b) Paging is offset-based over that merged sequence. limit <= 0 means
//     "no limit" — the remainder of the sequence from offset is returned in
//     one page, matching UnifiedStore.ListSessionsPage's contract. offset <
//     0 is treated as 0. An offset at or beyond the end of the sequence
//     returns an empty, non-nil page with NextOffset == -1, not an error. A
//     cursor built from NextOffset stays valid for the duration of a
//     client's expansion even if a store's contents change in between — a
//     shifted window is acceptable, a duplicated or skipped row within one
//     already-served page is not, because this method never re-derives an
//     already-returned row's position from anything but that same total
//     order.
//   - (c) A legacy per-agent store that errors mid-merge contributes zero
//     rows, is appended to the returned errs, and does NOT halt the page or
//     invalidate the cursor — the merge simply continues over the remaining
//     stores (unchanged from this method's pre-pagination behavior; see the
//     loop below). Callers should surface these as partial_errors rather
//     than treating the whole response as a failure.
//
// Hierarchy (FR-091/FR-104) is applied BEFORE pagination, over the full
// merged set, so a page boundary can never split a parent from its
// child-count context or silently promote/demote a row depending on which
// page it lands on:
//
//   - flat == true: no hierarchy filter — every merged, deduplicated session
//     is a candidate row (FR-104's per-session usage-accounting listing).
//     parentSessionID is ignored in this combination; the 400 for supplying
//     both is a REST-layer concern (U18), not this method's.
//   - flat == false, parentSessionID != "": only that session's DIRECT
//     children (meta.ParentSessionID == parentSessionID) are returned.
//   - flat == false, parentSessionID == "": only ROOT sessions are returned
//     — meta.ParentSessionID == "", OR meta.ParentSessionID names a session
//     absent from this merge (an orphan; FR-091: "a session whose
//     ParentSessionID names a session that no longer resolves MUST be
//     returned as a root"). Orphan detection is computed across the WHOLE
//     merged id set rather than by delegating to UnifiedStore.IsOrphan
//     (which is scoped to one store's own metaCache) because this method
//     spans multiple stores — in practice every delegated child lives in
//     the one shared store per FR-010, but resolving membership against the
//     full merge is correct regardless of that placement detail.
func (al *AgentLoop) ListAllSessions(limit, offset int, parentSessionID string, flat bool) (session.SessionListPage, []error) {
	var all []*session.UnifiedMeta
	var errs []error

	// 1. Shared store (new sessions).
	sharedIDs := make(map[string]bool)
	allIDs := make(map[string]bool)
	if al.sharedSessionStore != nil {
		shared, err := al.sharedSessionStore.ListSessions()
		if err != nil {
			logger.WarnCF("agent", "ListAllSessions: could not list shared sessions",
				map[string]any{"error": err.Error()})
			errs = append(errs, fmt.Errorf("shared: %w", err))
		} else {
			for _, s := range shared {
				sharedIDs[s.ID] = true
				allIDs[s.ID] = true
				all = append(all, s)
			}
		}
	}

	// 2. Legacy per-agent stores — deduplicate against shared. FR-098(c): a
	// store that errors here contributes zero rows and the merge continues.
	for _, id := range al.GetRegistry().ListAgentIDs() {
		store := al.getLegacyAgentStore(id)
		if store == nil {
			continue
		}
		sessions, err := store.ListSessions()
		if err != nil {
			logger.WarnCF("agent", "ListAllSessions: could not list sessions for agent",
				map[string]any{"agent_id": id, "error": err.Error()})
			errs = append(errs, fmt.Errorf("agent=%s: %w", id, err))
			continue
		}
		for _, s := range sessions {
			if !sharedIDs[s.ID] {
				allIDs[s.ID] = true
				all = append(all, s)
			}
		}
	}

	all = u9FilterSessionHierarchy(all, allIDs, parentSessionID, flat)

	// FR-098(a): stable total order.
	sort.Slice(all, func(i, j int) bool { return u9SessionRecencyLess(all[i], all[j]) })

	// FR-098(b): offset-based paging over the merged, filtered, sorted
	// sequence — mirrors UnifiedStore.ListSessionsPage's contract exactly.
	if offset < 0 {
		offset = 0
	}
	total := len(all)
	if offset >= total {
		return session.SessionListPage{Sessions: []*session.UnifiedMeta{}, NextOffset: -1, Total: total}, errs
	}
	end := total
	if limit > 0 && offset+limit < total {
		end = offset + limit
	}
	nextOffset := -1
	if end < total {
		nextOffset = end
	}
	page := make([]*session.UnifiedMeta, end-offset)
	copy(page, all[offset:end])
	return session.SessionListPage{Sessions: page, NextOffset: nextOffset, Total: total}, errs
}

// u9FilterSessionHierarchy applies FR-091/FR-104's hierarchy rule to a
// merged, deduplicated session list, given the full set of ids present in
// that same merge (allIDs) so the orphan clause of FR-091 ("a session whose
// ParentSessionID names a session that no longer resolves MUST be returned
// as a root") is evaluated across every store ListAllSessions merged, not
// just one. See ListAllSessions' doc comment for the three cases.
func u9FilterSessionHierarchy(all []*session.UnifiedMeta, allIDs map[string]bool, parentSessionID string, flat bool) []*session.UnifiedMeta {
	if flat {
		return all
	}
	filtered := make([]*session.UnifiedMeta, 0, len(all))
	if parentSessionID != "" {
		for _, s := range all {
			if s.ParentSessionID == parentSessionID {
				filtered = append(filtered, s)
			}
		}
		return filtered
	}
	for _, s := range all {
		if s.ParentSessionID == "" || !allIDs[s.ParentSessionID] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// u9SessionRecencyLess is ListAllSessions' FR-098(a) total order, factored
// into a named, directly-testable function rather than left as an inline
// sort.Slice closure: UpdatedAt descending, with session id as a stable
// tiebreak so two sessions sharing a timestamp (down to whatever resolution
// the clock gives) cannot silently swap places between two calls with no
// intervening write. Mirrors UnifiedStore.ListSessions' own post-FR-097a
// comparator (pkg/session/unified.go) exactly, at the layer that merges
// ACROSS stores rather than within one.
func u9SessionRecencyLess(a, b *session.UnifiedMeta) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID < b.ID
}

// forgetSession removes every (agent, session) bucket belonging to sessionID
// from the loaded-tool map and its pendingSearchPromotions/bucketTurnCounter
// siblings, preventing unbounded memory growth. Called from CloseSession with
// the transcript sessionID — the same value manifestBucketKey's session
// component derives from.
//
// ADR-071 D3 §4.6 point 4: since loadedTools is now keyed by
// manifestBucketKey(agentID, transcriptID, sessionKey) — a composite key —
// an exact-match `delete(al.loadedTools, sessionID)` would match nothing and
// silently reintroduce the unbounded growth this function exists to prevent.
// This is now a suffix sweep for every key ending in
// manifestBucketKeySep+sessionID, mirroring the O(n) recallSpans scan two
// lines below (same justification: session close is a cold path, not the hot
// turn path).
//
// §4.3.1(a) r5: the same sweep MUST cover pendingSearchPromotions too — it is
// not swept by virtue of sharing loadedToolsMu (the mutex protects the maps,
// it does not enumerate them). Every entry found there is, by definition, a
// promotion abandoned before its follow-up horizon elapsed (the session is
// closing), so it is tallied and counted via
// tools.RecordToolSearchNoFollowUp() — count-then-delete in the same critical
// section, and the recorder call made AFTER releasing the lock so no
// cross-package call happens under it.
//
// Safe for concurrent access — protected by loadedToolsMu. No-op for the
// empty key.
func (al *AgentLoop) forgetSession(sessionID string) {
	if sessionID == "" {
		return
	}
	suffix := manifestBucketKeySep + sessionID

	al.loadedToolsMu.Lock()
	for key := range al.loadedTools {
		if key == sessionID || strings.HasSuffix(key, suffix) {
			delete(al.loadedTools, key)
		}
	}
	var abandonedPromotions int
	for key, pending := range al.pendingSearchPromotions {
		if key == sessionID || strings.HasSuffix(key, suffix) {
			abandonedPromotions += len(pending)
			delete(al.pendingSearchPromotions, key)
		}
	}
	for key := range al.bucketTurnCounter {
		if key == sessionID || strings.HasSuffix(key, suffix) {
			delete(al.bucketTurnCounter, key)
		}
	}
	al.loadedToolsMu.Unlock()

	for i := 0; i < abandonedPromotions; i++ {
		tools.RecordToolSearchNoFollowUp()
	}

	// MINOR fix: clean up recall spans for this session so recallSpans sync.Map
	// does not grow without bound as sessions are closed (FR-019). The span key
	// is ts.sessionKey ("agent:<agentID>:session:<sessionID>") while forgetSession
	// receives only the transcript sessionID. We scan the map for any key that
	// contains the sessionID as a suffix so we delete spans regardless of agentID.
	// The scan is safe here because forgetSession is on the session-close path
	// (not the hot turn path), so the O(n) Range is acceptable.
	recallSuffix := ":session:" + sessionID
	al.recallSpans.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok {
			if key == sessionID || strings.HasSuffix(key, recallSuffix) {
				al.recallSpans.Delete(key)
			}
		}
		return true
	})
}
