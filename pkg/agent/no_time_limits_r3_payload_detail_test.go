package agent

import (
	"reflect"
	"testing"
)

// Reflection keeps the whole RED pack executable on 00e37a9f3, where this
// newly agreed internal field does not exist yet. Missing implementation is
// a loud Fatal, never a Skip or a permissive empty-string default.
func r3InternalConclusionDetail(t *testing.T, payload ErrorPayload) string {
	t.Helper()
	field := reflect.ValueOf(payload).FieldByName("Detail")
	if !field.IsValid() {
		t.Fatal("BLOCKED: ErrorPayload.Detail not implemented — required by architect R2-3 safe-text rule")
	}
	if field.Kind() != reflect.String {
		t.Fatalf("R2-3 internal diagnostic Detail type=%s, want string", field.Kind())
	}
	return field.String()
}

func r3DrainConclusionPayloads(sub EventSubscription) []ErrorPayload {
	var payloads []ErrorPayload
	for {
		select {
		case event, ok := <-sub.C:
			if !ok {
				return payloads
			}
			if p, ok := event.Payload.(ErrorPayload); ok && p.Stage == "goal_conclusion" {
				payloads = append(payloads, p)
			}
		default:
			return payloads
		}
	}
}
