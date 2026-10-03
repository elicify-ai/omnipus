package gateway

// RED pack (w4 spec §9.1 row 13; US-6.AC-4; §2.3): the wire contract for the
// effective message date. The spec is explicit: `date` becomes required-but-
// NULLABLE, the W0 amendment landing atomically with the Wave C/D consumer
// PRs; a message with neither usable date serializes as explicit null, and
// no Go zero time is ever serialized.
//
// RED against the landed code = finding F5: the consumer PRs landed WITHOUT
// the amendment — the generated `date` is still a bare time.Time, so the
// founder's #1175 "No date" state cannot exist on the wire and a zero time
// serializes as "0001-01-01T00:00:00Z".

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

func TestWireDateIsNullable(t *testing.T) {
	typ := reflect.TypeOf(generated.MailMessageSummary{})
	field, ok := typ.FieldByName("Date")
	if !ok {
		t.Fatalf("generated MailMessageSummary has no Date field")
	}
	if field.Type.Kind() != reflect.Ptr {
		t.Fatalf("wire date is %s, want a nullable pointer (*time.Time) — the W0 amendment was to land atomically with the consumer PRs (US-6.AC-4; finding F5): consumers shipped without it", field.Type)
	}
}

func TestWireNeverSerializesAZeroDate(t *testing.T) {
	// The message with neither usable date source: the summary that goes on
	// the wire must carry date: null — never "0001-01-01T00:00:00Z".
	sum := generated.MailMessageSummary{Date: nil}
	data, err := json.Marshal(sum)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if strings.Contains(string(data), "0001-01-01") {
		t.Fatalf("a Go zero time reached the wire: %s (US-6.AC-4: explicit null, never a zero date; finding F5)", data)
	}
}

// The effective-date rule the wire must express (the helper's own contract,
// restated here so the gateway file carries the whole row-13 oracle):
// valid Date → internal date → unknown.
func TestEffectiveDateFeedsTheWireContract(t *testing.T) {
	date := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	internal := time.Date(2026, 3, 6, 8, 0, 0, 0, time.UTC)
	if _, ok := email.EffectiveDate(date, internal); !ok {
		t.Fatalf("a valid Date header is usable")
	}
	if _, ok := email.EffectiveDate(time.Time{}, time.Time{}); ok {
		t.Fatalf("neither source usable must report unknown — the state the nullable wire field must carry")
	}
}
