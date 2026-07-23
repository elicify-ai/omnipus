// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// media_present.go — presentation-layer wiring (ADR-051 Rev 4, Wave 3 T9).
//
// This file holds the cross-slice integration types that compose the
// individual Wave 1-2 slices (B1 library, C capability catalog, F resize,
// E classifier, D offload, G resolver) into the 7-step presentation chain
// driven by resolveMediaRefsWithOffload (loop_media.go). It lives here so
// loop_media.go stays focused on the per-ref resolution logic and loop.go
// stays focused on the turn loop — the orchestration seams (capability
// gate, manifest refcount lifecycle) are first-class types in this file.

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/providers/capabilities"
)

// workspaceRefcounter is the manifest-refcount contract the presentation
// chain uses to track which workspace library files an active session
// references (FR-007a). *library.Library implements it; a nil refcounter
// disables refcount tracking (legacy/empty-workspace turns degrade
// gracefully — orphan GC simply has no manifest data to act on).
//
// The refcount is a SEPARATE manifest-level counter from the media store's
// pathStates.refCount (which is path-keyed and immediately deletes at
// zero). The manifest refcount drives the deferred (30d, refcount==0)
// orphan GC and has different semantics — see FR-007a.
type workspaceRefcounter interface {
	IncrementRefcount(mediaID string) (int, error)
	DecrementRefcount(mediaID string) (int, error)
}

// sessionRefcountState is the per-session refcount bookkeeping stored on
// the AgentLoop (sessionRefcounts). It carries the workspace ID (so
// CloseSession can locate the owning library for the decrement pass) and
// the set of media IDs this session has already incremented (per-session
// dedup — each session contributes at most +1 per file, matching the
// spec's "a session/turn references the file" increment semantics).
type sessionRefcountState struct {
	workspaceID string
	seen        *sync.Map // map[string]struct{} — media IDs already incremented
}

// sessionRefcounter wraps a workspace library refcounter with per-session
// dedup: the first IncrementRefcount(mediaID) for a given session forwards
// to the library; subsequent calls for the same mediaID in the same
// session are no-ops. This makes refcount == "number of sessions
// referencing the file" (the quantity orphan GC needs), not "number of
// turns" or "number of messages" (which resolveMediaRefs would otherwise
// over-count, since it runs every turn over every message including
// history). The matching decrement fires once per mediaID at session
// cleanup (AgentLoop.decrementSessionMediaRefcounts).
type sessionRefcounter struct {
	lib  workspaceRefcounter
	seen *sync.Map // shared with sessionRefcountState.seen for this session
}

// IncrementRefcount forwards to the library the first time mediaID is seen
// for this session; subsequent calls are no-ops (per-session dedup).
func (s *sessionRefcounter) IncrementRefcount(mediaID string) (int, error) {
	if s == nil || s.lib == nil {
		return 0, nil
	}
	if _, loaded := s.seen.LoadOrStore(mediaID, struct{}{}); loaded {
		return 0, nil // already incremented for this session
	}
	return s.lib.IncrementRefcount(mediaID)
}

// DecrementRefcount is not used on the session wrapper — the decrement
// pass goes directly through the library at CloseSession time (via
// decrementSessionMediaRefcounts). The method exists only to satisfy the
// workspaceRefcounter interface for the increment-side wrapper.
func (s *sessionRefcounter) DecrementRefcount(mediaID string) (int, error) {
	return 0, nil
}

// modelSupportsImage is the step-1 capability gate (FR-010) for the image
// branch. A nil catalog means the gate is not wired (the embedded seed has
// not been loaded or the gateway is running in a degraded mode); the
// optimistic FR-026 default applies — assume image-capable, so a wrong
// guess costs one outcome-based step-4 retry, never a dead turn.
func modelSupportsImage(catalog *capabilities.Catalog, model string) bool {
	if catalog == nil {
		return true
	}
	return catalog.Resolve(model).Supports(capabilities.ModalityImage)
}

// incrementWorkspaceRef parses a media://workspace/<ws>/<id> ref and
// increments its manifest refcount via the provided refcounter (FR-007a).
// Non-workspace refs (legacy media://<uuid>, agent inline, pre-resolved
// data URLs) are no-ops — they do not have a manifest entry. Errors are
// logged-not-fatal: a refcount bookkeeping failure must never break a
// turn (the file still reaches the model through the presentation chain;
// only orphan GC loses visibility).
func incrementWorkspaceRef(rc workspaceRefcounter, ref string) {
	if rc == nil {
		return
	}
	if !media.IsWorkspaceRef(ref) {
		return
	}
	_, mediaID, ok := media.ParseWorkspaceRef(ref)
	if !ok {
		return
	}
	if _, err := rc.IncrementRefcount(mediaID); err != nil {
		logger.WarnCF("agent", "media library: manifest refcount increment failed", map[string]any{
			"media_id": mediaID,
			"ref":      ref,
			"error":    err.Error(),
		})
	}
}

// getCapabilityCatalog returns the AgentLoop's capability catalog (nil if
// not constructed — callers nil-check or use modelSupportsImage which
// treats nil as optimistic).
func (al *AgentLoop) getCapabilityCatalog() *capabilities.Catalog {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.capabilityCatalog
}

// SetCapabilityCatalog injects a capability catalog (typically one with a
// puller + store wired by the gateway boot for FR-025 repo-pull). It
// replaces any catalog constructed from the embedded seed at NewAgentLoop
// time. Passing nil restores the optimistic posture (the gate always
// passes). Safe to call after boot; the catalog is read per-turn.
func (al *AgentLoop) SetCapabilityCatalog(c *capabilities.Catalog) {
	al.mu.Lock()
	al.capabilityCatalog = c
	al.mu.Unlock()
}

// getTurnRefcounter builds the per-session refcounter for a turn. It
// returns nil (no refcount tracking) when workspaceID or
// transcriptSessionID is empty — legacy/no-workspace turns have no
// manifest to account against. On first access for a session it creates
// the sessionRefcountState (workspaceID + seen-set) and caches it on the
// AgentLoop so CloseSession can find it for the decrement pass. The
// workspace library is constructed+cached per workspace (manifest load is
// not free; caching bounds it to one load per workspace per process).
func (al *AgentLoop) getTurnRefcounter(workspaceID, transcriptSessionID string) *sessionRefcounter {
	if workspaceID == "" || transcriptSessionID == "" {
		return nil
	}
	lib := al.getWorkspaceLibrary(workspaceID)
	if lib == nil {
		return nil
	}
	stateAny, _ := al.sessionRefcounts.LoadOrStore(transcriptSessionID, &sessionRefcountState{
		workspaceID: workspaceID,
		seen:        &sync.Map{},
	})
	state, ok := stateAny.(*sessionRefcountState)
	if !ok || state.seen == nil {
		return nil
	}
	return &sessionRefcounter{lib: lib, seen: state.seen}
}

// GetWorkspaceLibrary is the exported gateway-facing seam for routing user
// uploads (FR-001) to the workspace media library (ADR-051 Rev 4). It returns
// a cached *library.Library for the workspace, constructing it on first
// access. Returns nil if the library cannot be constructed — callers degrade
// gracefully to the legacy session-scoped upload path.
func (al *AgentLoop) GetWorkspaceLibrary(workspaceID string) *library.Library {
	return al.getWorkspaceLibrary(workspaceID)
}

// getWorkspaceLibrary returns a cached *library.Library for the workspace,
// constructing it on first access. Returns nil if the library cannot be
// constructed (missing home path, invalid workspace ID) — callers degrade
// gracefully (no refcount tracking). The cache is keyed by workspace ID;
// the same workspace always gets the same library instance so refcount
// mutations are consistent within a process.
func (al *AgentLoop) getWorkspaceLibrary(workspaceID string) *library.Library {
	if workspaceID == "" {
		return nil
	}
	if cached, ok := al.workspaceLibCache.Load(workspaceID); ok {
		if lib, ok := cached.(*library.Library); ok {
			return lib
		}
	}
	home := al.homePath
	if home == "" {
		return nil
	}
	lib, err := library.New(home, workspaceID)
	if err != nil {
		logger.WarnCF(
			"agent",
			"media library: workspace library unavailable, refcount tracking disabled",
			map[string]any{
				"workspace_id": workspaceID,
				"error":        err.Error(),
			},
		)
		return nil
	}
	// LoadOrStore so a concurrent caller that also constructed a library
	// for this workspace wins deterministically — both are valid, but we
	// keep exactly one to avoid split refcount state.
	actual, _ := al.workspaceLibCache.LoadOrStore(workspaceID, lib)
	return actual.(*library.Library)
}

// decrementSessionMediaRefcounts is the FR-007a decrement call-site: when
// a session closes, every workspace library file it referenced has its
// manifest refcount decremented. It loads the per-session seen-set
// (populated by sessionRefcounter during the turn), resolves the owning
// workspace library, and decrements each media ID once. Errors
// (underflow, library unavailable) are logged-not-fatal — a missed
// decrement means orphan GC keeps the file slightly longer (refcount
// stays > 0), never data loss. After the pass the seen-set is deleted so
// a re-close is a no-op.
func (al *AgentLoop) decrementSessionMediaRefcounts(transcriptSessionID string) {
	if transcriptSessionID == "" {
		return
	}
	stateAny, ok := al.sessionRefcounts.LoadAndDelete(transcriptSessionID)
	if !ok {
		return
	}
	state, ok := stateAny.(*sessionRefcountState)
	if !ok || state == nil {
		return
	}
	lib := al.getWorkspaceLibrary(state.workspaceID)
	if lib == nil {
		return
	}
	state.seen.Range(func(key, _ any) bool {
		mediaID, _ := key.(string)
		if mediaID == "" {
			return true
		}
		if _, err := lib.DecrementRefcount(mediaID); err != nil {
			logger.WarnCF("agent", "media library: manifest refcount decrement failed", map[string]any{
				"media_id":     mediaID,
				"workspace_id": state.workspaceID,
				"session_id":   transcriptSessionID,
				"error":        err.Error(),
			})
		}
		return true
	})
}
