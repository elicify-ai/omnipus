package protocoltypes

// OnToolCallProgressKey is the well-known `options` map key carrying an
// OnToolCallProgress callback into a provider's ChatStream. It is passed
// through the existing `options map[string]any` parameter so no provider
// interface signature has to change.
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
const OnToolCallProgressKey = "on_tool_call_progress"

// ToolCallProgress is a liveness signal emitted while a tool call's arguments
// are still streaming in. It deliberately carries no argument CONTENT: the
// arguments are frequently large and often sensitive, and the only question a
// consumer needs answered is "is this still making forward progress?".
type ToolCallProgress struct {
	// Index is the tool call's position in the response (OpenAI's delta index).
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

// OnToolCallProgress is the callback shape stored under OnToolCallProgressKey.
//
// Implementations MUST be cheap and non-blocking: this fires on every
// argument delta of a live SSE stream, so anything expensive here throttles
// token consumption. Consumers that need rate limiting should throttle on
// their own side rather than blocking the parser.
type OnToolCallProgress func(ToolCallProgress)

// ToolCallProgressFromOptions extracts the progress callback from a provider
// options map, returning nil when absent or of an unexpected type. Callers
// treat nil as "no progress reporting requested".
func ToolCallProgressFromOptions(options map[string]any) OnToolCallProgress {
	if options == nil {
		return nil
	}
	switch cb := options[OnToolCallProgressKey].(type) {
	case OnToolCallProgress:
		return cb
	case func(ToolCallProgress):
		return OnToolCallProgress(cb)
	default:
		return nil
	}
}
