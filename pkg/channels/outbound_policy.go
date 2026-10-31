package channels

// Channels package WP-0 shells (channels-spec-r8, T-1, T-2, T-3, FR-011,
// FR-012, FR-041). Signatures only: every function here returns a zero value
// and nothing in production calls it. WP-2 implements the bodies; the test
// author writes failing tests against these signatures first.

import (
	"github.com/elicify-ai/omnipus/pkg/config"
)

// StartPolicy is a connection's outbound policy (FR-010). The values are the
// contract enum ChannelOutbound.policy.
type StartPolicy string

const (
	// StartPolicyReplyOnly is "Only answer", the default when config.json
	// has no outbound setting.
	StartPolicyReplyOnly StartPolicy = "reply_only"
	// StartPolicyAgentMayStart is "Answer and also start messages".
	StartPolicyAgentMayStart StartPolicy = "agent_may_start"
)

// TargetKind says what a canonical target is (T-1, T-5 kind labels).
type TargetKind string

const (
	// TargetKindPerson is a person: a start opens a direct message.
	TargetKindPerson TargetKind = "person"
	// TargetKindGroup is a group, channel or thread.
	TargetKindGroup TargetKind = "group"
	// TargetKindPersonOrGroup is a Discord id the local cache cannot place;
	// never labelled "person" (T-5, M-07).
	TargetKindPersonOrGroup TargetKind = "person_or_group"
)

// TargetErrorCode is why one target entry was refused. The values are the
// contract enum ChannelOutboundRejection.target_errors[].code.
type TargetErrorCode string

const (
	TargetErrInvalidID              TargetErrorCode = "invalid_id"
	TargetErrWildcardNotAllowed     TargetErrorCode = "wildcard_not_allowed"
	TargetErrUsernameNotAllowed     TargetErrorCode = "username_not_allowed"
	TargetErrWrongPlatform          TargetErrorCode = "wrong_platform"
	TargetErrNotFound               TargetErrorCode = "not_found"
	TargetErrUnsupportedChannelKind TargetErrorCode = "unsupported_channel_kind"
	TargetErrCannotMessagePeople    TargetErrorCode = "cannot_message_people"
	TargetErrLookupUnavailable      TargetErrorCode = "lookup_unavailable"
)

// TargetError is one refused entry of a raw target list. Index is the
// entry's 0-based position in the raw input (the contract's
// target_errors[].index); Raw is the entry as received (unsanitized — the
// REST layer sanitizes it into the contract's target hint).
type TargetError struct {
	Raw   string
	Code  TargetErrorCode
	Index int
}

// CanonicalizeTarget is the one canonicalizer of T-2 (FR-012) for one raw
// entry on a connection of the given platform ("discord", "telegram", ...,
// lower case). On success it returns the canonical "platform:id" value and
// an empty code; on refusal it returns "" and the refusal code. It decides
// syntax and the T-2 numeric rules only — never a network look-up and never
// the platform-support refusals of T-1 (PlatformStartRuleFor).
//
// WP-0 shell: returns ("", "").
func CanonicalizeTarget(platform, raw string) (canonical string, code TargetErrorCode) {
	return "", ""
}

// CanonicalizeTargets canonicalizes a whole raw list (FR-012): each entry
// through CanonicalizeTarget, duplicates (in any input form) collapsed to
// their first occurrence, request order kept. errs lists every refused entry
// in request order with its raw index. The limit of 20 is NOT applied here
// (the caller refuses too_many_targets on len(canonical) > 20 when errs is
// empty).
//
// WP-0 shell: returns (nil, nil).
func CanonicalizeTargets(platform string, raw []string) (canonical []string, errs []TargetError) {
	return nil, nil
}

// TelegramTargetKind derives a Telegram target's kind locally from the sign
// of its chat id (T-1): positive = person, negative = group (with or without
// a "/topic" suffix). ok is false for a value that is not a canonical
// Telegram target.
//
// WP-0 shell: returns ("", false).
func TelegramTargetKind(canonical string) (kind TargetKind, ok bool) {
	return "", false
}

// PlatformStartRule is one row of the T-1 platform table.
type PlatformStartRule struct {
	// Platform is the lower-case platform key, e.g. "telegram".
	Platform string
	// DisplayName is the name used in user and agent texts, e.g. "Telegram".
	DisplayName string
	// PersonRefusal is the contract ChannelOutboundRejection.code for a
	// person target on this platform ("" when allowed):
	// "platform_cannot_message_people" or "platform_cannot_start".
	PersonRefusal string
	// GroupRefusal is the same for a group target ("" when allowed).
	GroupRefusal string
	// StartSupported is true for Discord and Telegram only.
	StartSupported bool
}

// PlatformStartRuleFor returns the T-1 row for a platform key. An unknown
// key returns a row with StartSupported false.
//
// WP-0 shell: returns the zero row.
func PlatformStartRuleFor(platform string) PlatformStartRule {
	return PlatformStartRule{}
}

// OutboundPolicy returns the effective policy of one channel instance for one
// (workspace, agent) pair (FR-011): StartPolicyReplyOnly unless the instance
// is enabled, bound to exactly that pair, and its stored policy is
// agent_may_start.
//
// WP-0 shell: returns "".
func OutboundPolicy(inst config.ChannelInstanceConfig, workspaceID, agentID string) StartPolicy {
	return ""
}

// AvailabilityState is the contract enum ChannelAvailability.state (T-7).
type AvailabilityState string

const (
	AvailabilityRunning    AvailabilityState = "running"
	AvailabilityNotRunning AvailabilityState = "not_running"
	AvailabilityNotLoaded  AvailabilityState = "not_loaded"
)

// Availability is the answer of Manager.Availability (T-7) and the
// availability input of StartAllowed. Reason is empty when State is running.
type Availability struct {
	State  AvailabilityState
	Reason string
}

// StartRefusalCode is a T-3 refusal code.
type StartRefusalCode string

const (
	StartRefusedUnknownChannel      StartRefusalCode = "unknown_channel"
	StartRefusedNotBoundToYou       StartRefusalCode = "not_bound_to_you"
	StartRefusedPlatformCannotStart StartRefusalCode = "platform_cannot_start"
	StartRefusedOnlyAnswer          StartRefusalCode = "only_answer"
	StartRefusedTargetNotListed     StartRefusalCode = "target_not_listed"
	StartRefusedNotRunning          StartRefusalCode = "not_running"
)

// StartDecision is the result of StartAllowed. When Allowed is false, Code is
// the first failing T-3 row and Text its exact normative tool text with the
// placeholders filled in. When Allowed is true, Code and Text are empty.
type StartDecision struct {
	Code    StartRefusalCode
	Text    string
	Allowed bool
}

// StartAllowed is the one pure start check of T-3 (FR-041), shared by the
// send_message tool, the Channels block, the dispatch hook and the dequeue
// check. It reads only its arguments: cfg is the policy snapshot (the live
// policy source), avail the caller-supplied availability answer for
// instanceID (Manager.Availability at the tool and block; the worker's
// IsRunning at dispatch and dequeue — a caller that only has the running
// flag passes AvailabilityRunning or AvailabilityNotRunning), chatID the
// target as the agent supplied it (canonicalized inside with
// CanonicalizeTarget), workspaceID and agentID the caller. Rows are
// evaluated in the T-3 order a, b, e, c, d, f; the first failing row decides.
// It takes no lock and makes no network call.
//
// WP-0 shell: returns the zero StartDecision (not allowed, empty code).
func StartAllowed(
	cfg *config.Config,
	avail Availability,
	instanceID, chatID, workspaceID, agentID string,
) StartDecision {
	return StartDecision{}
}
