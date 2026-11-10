// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"strings"
	"testing"
)

// Oracle: session-core DEL-19 — the legacy cron_expr occurrence expansion AND
// the legacy every projection are deleted from the occurrences endpoint;
// recurring Calendar work uses the RRULE path only. The scan has a positive
// control (the RRULE path it must keep) so an empty result cannot mean "read
// the wrong file".
func TestOccurrences_LegacyCronAndEveryProjectionsStayDeleted(t *testing.T) {
	data, err := os.ReadFile("task_occurrences.go")
	if err != nil {
		t.Fatalf("read task_occurrences.go: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "task.ExpandRRULE") {
		t.Fatal("instrument check failed: the RRULE expansion this guard protects is missing from the file read")
	}
	for _, banned := range []string{
		"expandCronServerZone", "cronDayFn", "gronx",
		"countEveryMsInRange", "everyMsDayFn", "ProjectEveryMs", "TriggerEvery",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("DEL-19: pkg/gateway/task_occurrences.go mentions %q — the legacy every/cron_expr projection must stay deleted", banned)
		}
	}
}
