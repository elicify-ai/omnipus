// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 — the shared per-boundary helper every publication boundary
// calls: resolve audience through the injected
// steer.AudienceResolver, then call steer.BoundaryObserver.Observe BEFORE
// the caller acts on the decision (I-5, FR-B-001, FR-B-014). One function,
// reused by every boundary this package hosts directly. Boundaries hosted by
// pkg/tools, pkg/channels, pkg/gateway and pkg/askuser call the injected
// steer.AudienceResolver/BoundaryObserver directly, since those packages
// cannot import this one.
package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// SetSteerAudienceDeps wires the ADR-091 I-5 injected dependencies
// (AudienceResolver, BoundaryObserver, UpwardDeliverer) onto the loop, and
// propagates the deliverer into every currently-registered agent's
// message_parent tool surface (mirrors SetChannelOwnership's re-push
// discipline — wireSessionMessagingForAgent re-applies it on every future
// per-agent (re)registration too, so a hot reload after this call stays
// wired). A nil observer defaults to steer.NopBoundaryObserver{} (the
// production no-op).
//
// Called once at boot: gateway_boot.go::wireSteerDeps builds the real
// steer.AudienceResolver/BoundaryObserver/UpwardDeliverer implementations
// and calls this setter to wire them in — pkg/steer.Deps carries no
// *AgentLoop back-reference, so gateway_boot.go cannot set these fields
// directly.
func (al *AgentLoop) SetSteerAudienceDeps(resolver steer.AudienceResolver, observer steer.BoundaryObserver, deliverer steer.UpwardDeliverer) {
	if observer == nil {
		observer = steer.NopBoundaryObserver{}
	}
	al.steerDepsMu.Lock()
	al.audienceResolver = resolver
	al.boundaryObserver = observer
	al.upwardDeliverer = deliverer
	al.steerDepsMu.Unlock()

	// Back-wire the concrete SteerUpwardDeliverer's *AgentLoop dependency
	// (live-turn detection, the steering queue, the lifecycle/inbox stores,
	// asyncNotifier) — see steer_audience.go's SteerUpwardDeliverer doc
	// comment. A caller injecting a fake steer.UpwardDeliverer (any test)
	// simply skips this branch.
	if sud, ok := deliverer.(*SteerUpwardDeliverer); ok {
		sud.agentLoop = al
	}

	reg := al.GetRegistry()
	if reg == nil {
		return
	}
	for _, agentID := range reg.ListAgentIDs() {
		if inst, ok := reg.GetAgent(agentID); ok && inst != nil {
			al.wireSessionMessagingForAgent(inst)
		}
	}
}

// SetSteerSessionLauncher installs ADR-091's single launch/dispatch primitive
// and re-wires every registered delegate tool. Future registry refreshes pick
// it up through wireSessionMessagingForAgent.
func (al *AgentLoop) SetSteerSessionLauncher(launcher steer.SessionLauncher) {
	al.steerDepsMu.Lock()
	al.sessionLauncher = launcher
	al.steerDepsMu.Unlock()

	reg := al.GetRegistry()
	if reg == nil {
		return
	}
	for _, agentID := range reg.ListAgentIDs() {
		if inst, ok := reg.GetAgent(agentID); ok && inst != nil {
			al.wireSessionMessagingForAgent(inst)
		}
	}
}

func (al *AgentLoop) getSteerSessionLauncher() steer.SessionLauncher {
	al.steerDepsMu.RLock()
	defer al.steerDepsMu.RUnlock()
	return al.sessionLauncher
}

// getSteerAudienceResolver, getBoundaryObserver and getUpwardDeliverer are
// steerDepsMu-guarded read accessors (SetSteerAudienceDeps writes late,
// post-boot, exactly like askUserRegistry/toolApprover).
func (al *AgentLoop) getSteerAudienceResolver() steer.AudienceResolver {
	al.steerDepsMu.RLock()
	defer al.steerDepsMu.RUnlock()
	return al.audienceResolver
}

func (al *AgentLoop) getBoundaryObserver() steer.BoundaryObserver {
	al.steerDepsMu.RLock()
	defer al.steerDepsMu.RUnlock()
	if al.boundaryObserver == nil {
		return steer.NopBoundaryObserver{}
	}
	return al.boundaryObserver
}

func (al *AgentLoop) getUpwardDeliverer() steer.UpwardDeliverer {
	al.steerDepsMu.RLock()
	defer al.steerDepsMu.RUnlock()
	return al.upwardDeliverer
}

// audienceFor resolves sessionID's audience for boundary through the
// injected steer.AudienceResolver, then calls
// steer.BoundaryObserver.Observe — the publication-boundary contract every
// landing-order §6 site follows (FR-B-001, FR-B-014). A resolver that was
// never wired (nil — a bare test AgentLoop that never called
// SetSteerAudienceDeps) answers AudienceUser, matching today's unrestricted
// behaviour, so the pre-existing test suite is unaffected; once wired, any
// doubt (a resolver error, or any class but ordinary_root/steered) answers
// AudienceNone (D3, "answers none on any doubt").
func (al *AgentLoop) audienceFor(ctx context.Context, boundary steer.Boundary, sessionID string) steer.Audience {
	resolver := al.getSteerAudienceResolver()
	observer := al.getBoundaryObserver()
	if resolver == nil {
		observer.Observe(boundary, sessionID, steer.AudienceUser)
		return steer.AudienceUser
	}
	audience, _, err := resolver.Audience(ctx, sessionID)
	if err != nil {
		audience = steer.AudienceNone
	}
	observer.Observe(boundary, sessionID, audience)
	return audience
}

// toolFeedbackReachesUser is the single publication decision for automatic
// tool feedback. I-5 decides the recipient, then the durable record's origin
// excludes internal task and verifier work. OriginKind is only the fallback
// for internal turns that predate or do not own a lifecycle record.
func (al *AgentLoop) toolFeedbackReachesUser(ctx context.Context, boundary steer.Boundary, ts *turnState) bool {
	if ts == nil {
		return false
	}
	audience := al.audienceFor(ctx, boundary, ts.transcriptSessionID)
	if audience != steer.AudienceUser {
		return false
	}
	origin := ts.opts.OriginKind
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil && ts.transcriptSessionID != "" {
		if rec, err := lifecycle.Load(ts.transcriptSessionID); err == nil && rec.Origin != nil {
			origin = rec.Origin.Kind
		}
	}
	return origin != session.OriginKindTask && origin != session.OriginKindVerifier
}
