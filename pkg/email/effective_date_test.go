package email

// RED pack (w4 spec §9.1 row 13; US-6; DS-DATE): the effective-date rule.
// The helper's precedence is the spec's: valid Date → internal date →
// null ("No date"). Never a zero date, epoch, today or a blank.

import (
	"context"
	"testing"
	"time"
)

func TestEffectiveDatePrecedence(t *testing.T) {
	date := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	internal := time.Date(2026, 3, 6, 8, 30, 0, 0, time.UTC)

	if got, ok := EffectiveDate(date, internal); !ok || !got.Equal(date) {
		t.Fatalf("valid Date must win over the internal date (US-6.AC-3): got (%v,%v)", got, ok)
	}
	for _, header := range []time.Time{{}} { // the zero time IS the missing/unparsable representation
		got, ok := EffectiveDate(header, internal)
		if !ok || !got.Equal(internal.UTC()) {
			t.Fatalf("header %v must fall back to the internal date (US-6.AC-1): got (%v,%v)", header, got, ok)
		}
	}
	if got, ok := EffectiveDate(time.Time{}, time.Time{}); ok {
		t.Fatalf("no usable date must report unknown (the caller renders \"No date\"), got (%v,%v)", got, ok)
	}
}

func TestFormatEffectiveDateRendersNoDate(t *testing.T) {
	if got := FormatEffectiveDate(time.Time{}, time.Time{}); got != NoDateText {
		t.Fatalf("neither source renders %q, got %q (US-6.AC-2: never year one, epoch or today)", NoDateText, got)
	}
	date := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	if got := FormatEffectiveDate(date, time.Time{}); got == NoDateText || got == "" {
		t.Fatalf("a usable date renders as a date, got %q", got)
	}
}

// US-6.AC-1 at the transport level: a message with NO usable Date header
// carries its INTERNAL date on the read surfaces — normalized once, here.
// RED against the landed code = finding F6 (the helper exists but the read
// paths never call it; the raw envelope zero time reaches the wire).
func TestReadFolderPageRowCarriesInternalDateWhenHeaderMissing(t *testing.T) {
	raw := []byte("From: Sender <sender@example.test>\r\n" +
		"To: mailbox@test.local\r\n" +
		"Subject: no-date-header\r\n" +
		"\r\nbody\r\n")
	cl := startMemIMAP(t, [][]byte{raw}, nil)
	rows, _, _, err := cl.ReadFolderPage(context.Background(), FolderInbox, 10, 0)
	if err != nil || len(rows) == 0 {
		t.Fatalf("ReadFolderPage: %v rows=%d", err, len(rows))
	}
	if rows[0].Date.IsZero() {
		t.Fatalf("message with no Date header carries a zero time on the read row — the internal-date fallback never ran (US-6.AC-1; finding F6): %+v", rows[0])
	}
}
