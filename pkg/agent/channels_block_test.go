package agent

// channels_block_test.go — T-25, the Channels block renderer + context
// builder (channels-spec-r8 T-25; FR-002, FR-005; scenarios S-01–S-06; T-5
// per-connection lines and whole-block cases).
//
// The renderer does not exist yet anywhere in the tree (no block builder
// carries the `## Channels` header — Verified by source search; the T-5
// texts are new production code, and WP-0 defined no shell for it). Per the
// RED rules this file is therefore a set of BLOCKED fatals — one per T-5
// state, each carrying its normative oracle so the implementer un-blocks
// into a ready test — never skips.
//
// What the backend lead must provide (named in the RED report):
//   a Channels block renderer wired into the turn context that takes ONE
//   config snapshot from the same live policy source the tool and dispatcher
//   use (FR-005) and ONE Manager.Availability per connection per turn, and
//   renders exactly the T-5 lines for each bound enabled connection
//   (FR-002), sorted by instance id, targets printed with their bare id and
//   kind.
//
// Oracle (T-5, normative; placeholders filled per row):
//
//	running, reply_only:
//	  - <channel> (<Platform>): only answer. Answer messages that arrive here (in your main conversation: send_message reply_to=<request id>). You cannot start messages here.
//	running, agent_may_start, >= 1 target:
//	  - <channel> (<Platform>): you may also start messages to:
//	    - <bare id> (<person, direct message | group | person or group>)
//	  To start one: send_message channel="<channel>" chat_id="<one id above>" content="…". The result says "accepted for sending (delivery not confirmed)".
//	running, agent_may_start, 0 targets:
//	  - <channel> (<Platform>): only answer (the list of people and groups is empty).
//	unsupported platform (any policy):
//	  - <channel> (<Platform>): only answer; starting messages is not supported yet on <Platform>.
//	not_running / not_loaded:
//	  - <channel> (<Platform>): configured but not connected right now (<reason>); you cannot post here until it reconnects.
//
// Whole block:
//
//	>= 1 bound enabled connection:  ## Channels\nConnections bound to you in this workspace (live, this turn):\n<lines>
//	none:                           ## Channels\nNo chat connection is bound to you in this workspace. You cannot post to any chat platform; do not ask the user for channel ids.
//	send_message denied for agent:  ## Channels\nYou cannot send messages (send_message is not allowed for you).
//	steered (delegated) session:    ## Channels\nYou are in a delegated session: you may message only your own conversation.
//
// Kind labels (no network call, FR-006): Telegram by sign (positive →
// `person, direct message`, negative → `group`); Discord `group` from the
// session's local cache for supported kinds, otherwise `person or group` —
// a cache miss is NEVER labelled "person".

import "testing"

func TestT25_Block_RunningReplyOnlyLine(t *testing.T) {
	t.Fatalf("BLOCKED: no Channels block renderer exists — required by T-5 row 1 / FR-002 / FR-005; backend lead provides the renderer wired from the live policy source + Manager.Availability named in the RED report")
}

func TestT25_Block_RunningMayStartWithTargets_LinesAndKindLabels(t *testing.T) {
	t.Fatalf("BLOCKED: no Channels block renderer exists — required by T-5 row 2 / FR-002 / FR-006 (bare ids + kind labels, no network call); backend lead provides the renderer named in the RED report")
}

func TestT25_Block_RunningMayStartEmptyList(t *testing.T) {
	t.Fatalf("BLOCKED: no Channels block renderer exists — required by T-5 row 3 / FR-002; backend lead provides the renderer named in the RED report")
}

func TestT25_Block_UnsupportedPlatformLine(t *testing.T) {
	t.Fatalf("BLOCKED: no Channels block renderer exists — required by T-5 row 4 / T-1; backend lead provides the renderer named in the RED report")
}

func TestT25_Block_NotRunningAndNotLoadedLines_NoTargets(t *testing.T) {
	t.Fatalf("BLOCKED: no Channels block renderer exists — required by T-5 row 5 / FR-002 (no targets on a down connection); backend lead provides the renderer named in the RED report")
}

func TestT25_Block_WholeBlockCases(t *testing.T) {
	t.Fatalf("BLOCKED: no Channels block renderer exists — required by T-5 whole-block cases / FR-002 (sorted by instance id); backend lead provides the renderer named in the RED report")
}

func TestT25_Block_PropertyEveryPrintedTargetPassesStartAllowed_S05(t *testing.T) {
	t.Fatalf("BLOCKED: no Channels block renderer exists — required by S-05 / FR-005 (every printed target passes StartAllowed on the same snapshot, no allowed target is missing and five random unlisted ids are refused); backend lead provides the renderer named in the RED report")
}

func TestT25_Block_RenderMakesNoNetworkCall_S06(t *testing.T) {
	t.Fatalf("BLOCKED: no Channels block renderer exists — required by S-06 / FR-006 (render a Discord may-start connection with two targets using a fake that fails on every REST call; rendering succeeds with exactly zero REST calls); backend lead provides the renderer and local-cache access named in the RED report")
}
