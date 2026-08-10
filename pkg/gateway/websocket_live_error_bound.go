package gateway

// maxLiveErrorChars bounds ToolCallResultFrame.Error on the LIVE websocket
// path.
//
// It deliberately mirrors pkg/agent's maxFailClosedOutputChars (2000), which
// bounds the same string on the PERSISTED side (tcRecord.Error). The constant
// is duplicated rather than imported because the pkg/agent one is unexported
// and pkg/gateway must not take a dependency on pkg/agent's internals — but
// the two MUST stay equal, because their equality is what makes live and
// replay render the identical text. If you change one, change the other.
const maxLiveErrorChars = 2000

// truncateRunesForFrame bounds s to maxRunes runes, appending the same
// continuation marker pkg/agent's truncateRunes uses so a reader cannot tell
// the live and replayed strings apart.
//
// Rune-based, not byte-based: cutting mid-rune would emit invalid UTF-8 into a
// JSON frame the SPA validates with zod, and a dropped frame is a worse
// failure than a truncated message.
func truncateRunesForFrame(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "\n... (truncated, output continues)"
}
