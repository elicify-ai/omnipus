// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package steer

// SteeringUnavailableMessage is ADR-093 D5's refusal sentence: the exact
// text a delegate(run) result and a task launch return when
// IsSteeringUnavailable(err) is true. It never names a session id, a
// generation, the steered machinery (follow_up, Play, resumed_from) and
// never suggests a new chat (F890-1); it always names the conversation and
// says a new message resumes it.
const SteeringUnavailableMessage = "Delegation is unavailable because this conversation is not active right now. Tell the user that sending a new message in this conversation resumes it, and that their request has not been started."

// SteeringRevivalFailedMessage is the revival-failed variant of ADR-093 D5's
// refusal sentence (gate SFH#6): used when
// errors.Is(err, ErrSteeringRevivalFailed) — the conversation is inactive AND
// its most recent revive attempt itself failed, so the standard sentence's
// "sending a new message resumes it" would point the user at the action that
// just failed. Same constraints as D5: no session ids, no generation numbers,
// no steered machinery, no raw store text, never suggests a new chat.
const SteeringRevivalFailedMessage = "Delegation is unavailable because resuming this conversation just failed. Tell the user that the attempt to resume their conversation failed and their request has not been started, and that they should try again shortly."
