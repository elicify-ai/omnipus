package generated

import (
	"os"
	"strings"
	"testing"
)

// session-core DEL-F36: a truncated entry with no truncation_reason has NO
// reason - the wire never says "absent means cancelled" and no reader may
// default it. The contract text must say what the producer and the SPA do.
func TestTruncationReasonContract_DoesNotPromiseALegacyCancelledDefault(t *testing.T) {
	files := []string{
		"../../../contracts/components/schemas/Message.yaml",
		"../../../contracts/components/schemas/ReplayMessageFrame.yaml",
		"../../../contracts/asyncapi.yaml",
	}
	sawReason := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := strings.Join(strings.Fields(string(raw)), " ")
		if strings.Contains(text, "truncation_reason") {
			sawReason++ // instrument check: the file is the one that documents the field
		}
		for _, banned := range []string{
			`means "cancelled" — every entry written`,
			`always a cancel`,
		} {
			if strings.Contains(text, banned) {
				t.Errorf("%s still promises a legacy default (%q); DEL-F36 removed it", f, banned)
			}
		}
	}
	if sawReason != len(files) {
		t.Fatalf("instrument check: only %d of %d contract files mention truncation_reason", sawReason, len(files))
	}
}
