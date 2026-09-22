// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package steer

import "github.com/elicify-ai/omnipus/pkg/session"

// The types below are DEFINED in pkg/session (they are persisted fields of
// session.LifecycleRecord, or — for IndexReport — returned by
// session.LifecycleIndex) and re-exported here by Go type alias so every
// landing-order-binding name in §2 resolves as `steer.X` without
// pkg/session importing pkg/steer back. See steer.go's package doc for the
// full statement of why.
type (
	// Origin — I-1: what created a lifecycle record.
	Origin     = session.Origin
	OriginKind = session.OriginKind

	// Principal — I-1 Stop.By, I-6 CancelSubtree/Revive `by`: who performed
	// a steering action.
	Principal     = session.Principal
	PrincipalKind = session.PrincipalKind

	// SteeredBy — I-1: the durable edge naming who steers a session, and
	// its component value types.
	SteeredBy         = session.SteeredBy
	ReportingTarget   = session.ReportingTarget
	Authorization     = session.Authorization
	AuthorizationMode = session.AuthorizationMode
	Limits            = session.Limits

	// Stop — I-1/D8: the durable Stop marker on a session's own record.
	Stop = session.Stop

	// IndexReport — I-9: session.LifecycleIndex.Report()'s return shape.
	IndexReport      = session.IndexReport
	UnreadableRecord = session.UnreadableRecord
)

const (
	OriginKindDelegate  = session.OriginKindDelegate
	OriginKindTask      = session.OriginKindTask
	OriginKindChat      = session.OriginKindChat
	OriginKindChannel   = session.OriginKindChannel
	OriginKindScheduled = session.OriginKindScheduled
	OriginKindHeartbeat = session.OriginKindHeartbeat
	OriginKindVerifier  = session.OriginKindVerifier
	OriginKindPlan      = session.OriginKindPlan
	OriginKindHuman     = session.OriginKindHuman

	PrincipalKindAgent = session.PrincipalKindAgent
	PrincipalKindHuman = session.PrincipalKindHuman

	AuthorizationModeDirect = session.AuthorizationModeDirect
	AuthorizationModeTask   = session.AuthorizationModeTask
)
