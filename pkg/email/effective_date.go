package email

// EffectiveDate — the F6 message-date fallback rule (founder decision;
// w4 spec US-6): a valid message Date first, otherwise the mail server's
// internal/received date, otherwise absence. A zero or unparsable date is
// ABSENCE — never "1 Jan 1", never the epoch, never today, never a blank
// cell (US-6.AC-2).
//
// The rule is applied ONCE, in the transport layer, not re-derived per
// surface (US-6.AC-1): the gateway handlers and the agent adapter call
// EffectiveDate on the fetched values and carry the result. NOTE (w4 wave
// boundary): the internal date itself is not yet fetched on the panel read
// paths (fetchMailRows/ReadView are w2-owned files); until the internal
// date lands in those fetches, callers pass the zero Time and the rule
// degrades exactly as specified — Date, else "No date". The display
// surfaces' own year-one guards stay.

import "time"

// EffectiveDate applies the precedence rule. ok is false when NEITHER source
// is usable — the caller renders "No date"; it never fabricates a date.
func EffectiveDate(headerDate, internalDate time.Time) (time.Time, bool) {
	if !headerDate.IsZero() {
		return headerDate.UTC(), true
	}
	if !internalDate.IsZero() {
		return internalDate.UTC(), true
	}
	return time.Time{}, false
}

// FormatEffectiveDate renders the effective date for server-composed text
// surfaces (the reply attribution). No date renders the exact founder-set
// string.
const NoDateText = "No date"

// FormatEffectiveDate renders one effective date, or NoDateText.
func FormatEffectiveDate(headerDate, internalDate time.Time) string {
	if d, ok := EffectiveDate(headerDate, internalDate); ok {
		return d.Format("2 Jan 2006 15:04 MST")
	}
	return NoDateText
}
