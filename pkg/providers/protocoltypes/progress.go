package protocoltypes

// ToolCallProgress is a liveness signal emitted while a tool call's arguments
// are still streaming in. It deliberately carries no argument CONTENT: the
// arguments are frequently large and often sensitive, and the only question a
// consumer needs answered is "is this still making forward progress?".
//
// Why this exists: an OpenAI-compatible stream reports a tool call as a
// sequence of argument deltas. Those deltas were previously accumulated
// silently — only text deltas ever reached the streaming callback. A model
// spending 45 seconds emitting a multi-kilobyte tool argument (a large SVG,
// a long file body) therefore produced ZERO observable output, which is
// bit-for-bit indistinguishable from a hung generation.
//
// That ambiguity had real consequences: an orchestrating agent polled a
// delegated worker 75 times over 46 seconds, saw no activity, concluded the
// worker was stalled, and killed it mid-generation — repeatedly. Progress
// reporting exists so "still generating" can be told apart from "hung".
type ToolCallProgress struct {
	// Index identifies WHICH concurrent tool call this progress belongs to,
	// within one response, from one provider. It is NOT a stable tool-call
	// ordinal and must not be treated as one (ADR-059 D2):
	//
	//   - OpenAI-compatible streams report it as the tool_calls delta index,
	//     which does count tool calls: 0, 1, 2…
	//   - Anthropic reports it as the CONTENT-BLOCK index, and content blocks
	//     include text. A response that emits a paragraph and then one tool
	//     call reports that tool call at Index 1, not 0.
	//
	// The only contract a consumer may rely on is that two progress events
	// with the same Index, within the same response, describe the same tool
	// call. Use Name if you need to know what is being called.
	Index int
	// Name is the tool being called, once the stream has revealed it. It may
	// be empty for the first few deltas, since providers commonly send the
	// function name after the call has been opened.
	Name string
	// ArgsBytes is the number of argument bytes accumulated so far for THIS
	// tool call. Monotonically increasing within a single call.
	ArgsBytes int
	// TotalArgsBytes is the number of argument bytes accumulated across every
	// tool call in this response so far.
	TotalArgsBytes int
}

// OnToolCallProgress is the callback shape passed to a provider's ChatStream.
//
// It is a per-call PARAMETER rather than provider state (ADR-059 D1): the
// provider pointer is shared by every concurrent turn on an agent, so anything
// stored on it would be last-writer-wins between parallel delegations.
//
// Implementations MUST be cheap and non-blocking: this fires on every
// argument delta of a live SSE stream, so anything expensive here throttles
// token consumption. Consumers that need rate limiting should throttle on
// their own side rather than blocking the parser.
//
// Providers must invoke it via SafeInvoke, never directly — see that
// function's doc comment.
type OnToolCallProgress func(ToolCallProgress)
