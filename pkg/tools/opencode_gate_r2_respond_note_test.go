package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// CHECK round 3 F1. Oracle: architect ruling R2 section 3 and the approved
// delegate Description: queued startup waits for the current run to end;
// underway delivery interrupts/resumes. This internal note does not add a
// delivery-outcome field to the acknowledgement-only response contract.
func TestOpenCodeGateR2_RespondNoteDoesNotInventInterruptionForQueuedStartup(t *testing.T) {
	t.Run("queued_startup", func(t *testing.T) {
		note := strings.ToLower(externalRespondNote(externalDeliveryQueued))
		assert.Contains(t, note, "queu")
		assert.Contains(t, note, "same conversation")
		assert.Contains(t, note, "current run ends")
		assert.NotContains(t, note, "interrupt", "queueing cannot claim that the startup run was interrupted")
		assert.NotContains(t, note, "resume", "queueing cannot claim that a not-yet-ready conversation was resumed")
	})
	t.Run("interrupted_delivery", func(t *testing.T) {
		note := strings.ToLower(externalRespondNote(externalDeliveryInterrupted))
		assert.Contains(t, note, "interrupt")
		assert.Contains(t, note, "resume")
		assert.NotContains(t, note, "queued", "actual interruption must not be presented as queued startup")
	})
	t.Run("unknown_delivery", func(t *testing.T) {
		note := strings.ToLower(externalRespondNote(externalDeliveryUnknown))
		assert.Contains(t, note, "deliver")
		assert.NotContains(t, note, "interrupt")
		assert.NotContains(t, note, "resume")
		assert.NotContains(t, note, "queu", "an unclassified acceptance cannot invent either route")
	})
}
