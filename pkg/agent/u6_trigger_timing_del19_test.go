// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// session-core U6 — DEL-19: the legacy `every`/`cron_expr` task-timing
// adapters are gone; once/at_ms and RRULE survive.
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md,
// DEL-19 row + FR-017 (read from the spec text, never from the implementation):
//
//	FR-017 | ... Keep once/at_ms and RRULE, delete task every/cron_expr
//	compatibility, not heartbeat engine.
//	DEL-19 | pkg/agent/task_trigger.go::triggerToCronSchedule old
//	every/cron_expr branches; pkg/gateway/task_occurrences.go::expandCronServerZone,
//	legacy every projection; src/components/calendar/CalendarEventSlideOver.tsx::isLegacyTrigger,
//	old-trigger preservation/preview | DELETE old every_ms/cron_expr adapters and
//	compatibility promises; recurring Calendar work uses RRULE/compileRecurrence/
//	next-occurrence/rearm path. KEEP once/at_ms ...
//
// Two tests here:
//
//   - TestU6_DEL19_NoLegacyTimingAdapters is the deletion guard over
//     pkg/agent/task_trigger.go (the mapping) — it also pins the two KEEP
//     mappings so an over-broad deletion is caught.
//   - TestU6_DEL19_LegacyTimingTriggersRefused drives the surviving mapping and
//     asserts a legacy `every` or `cron_expr` trigger is REFUSED, so the
//     BEHAVIOUR is pinned even if the type's fields are reworked.
//
// Triggers are built by JSON decoding rather than by naming the trigger type's
// constants/fields, so this test keeps compiling whether or not DEL-19 removes
// TriggerEvery / TriggerConfig.CronExpr / TriggerConfig.EveryMs.

package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestU6_DEL19_NoLegacyTimingAdapters is the grep-clean guard for DEL-19's
// pkg/agent half, plus a KEEP-guard for the two mappings the spec retains.
func TestU6_DEL19_NoLegacyTimingAdapters(t *testing.T) {
	src := readOwnedFileForTest(t, "task_trigger.go")

	forbidden := []string{
		"case task.TriggerEvery:",
		`cron.CronSchedule{Kind: "cron"`,
		"tr.Config.CronExpr",
	}
	for _, pattern := range forbidden {
		if strings.Contains(src, pattern) {
			t.Errorf("DEL-19: pkg/agent/task_trigger.go still contains %q. The legacy "+
				"every/cron_expr trigger-to-cron adapters must be deleted; recurring task work uses "+
				"the RRULE path only, and legacy timing triggers are refused (session-core-spec "+
				"DEL-19 / FR-017).", pattern)
		}
	}

	// KEEP — once/at_ms and RRULE must survive the deletion.
	kept := []string{
		"case task.TriggerOnce:",
		"task.NextOccurrenceAfter(",
	}
	for _, pattern := range kept {
		if !strings.Contains(src, pattern) {
			t.Errorf("DEL-19 KEEP: pkg/agent/task_trigger.go no longer contains %q. Deleting the "+
				"legacy every/cron_expr adapters must NOT remove once/at_ms or the RRULE "+
				"next-occurrence mapping (session-core-spec DEL-19 KEEP clause).", pattern)
		}
	}
}

// u6TriggerFromJSON builds a task.Trigger by decoding raw JSON, so the test
// never names a trigger constant or config field DEL-19 may remove.
func u6TriggerFromJSON(t *testing.T, raw string) *task.Trigger {
	t.Helper()
	var tr task.Trigger
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		t.Fatalf("decode trigger fixture %s: %v", raw, err)
	}
	return &tr
}

// TestU6_DEL19_LegacyTimingTriggersRefused is DEL-19's behavioural half: the
// surviving trigger-to-cron mapping refuses a legacy `every` or `cron_expr`
// trigger instead of adapting it.
//
// RED today: triggerToCronSchedule maps `every` to {Kind:"every"} and a
// `recurring` trigger carrying only cron_expr to {Kind:"cron"} with no error,
// so both legacy adapters are still live.
func TestU6_DEL19_LegacyTimingTriggersRefused(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"every", `{"type":"every","config":{"every_ms":60000}}`},
		{"cron_expr", `{"type":"recurring","config":{"cron_expr":"0 * * * *"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := u6TriggerFromJSON(t, c.raw)
			sched, err := triggerToCronSchedule(tr, time.Now().UnixMilli())
			if err == nil {
				t.Fatalf("DEL-19: triggerToCronSchedule accepted the legacy %s trigger and produced "+
					"a %q schedule. The every/cron_expr compatibility adapters must be deleted — "+
					"recurring task timing is RRULE-only (session-core-spec DEL-19 / FR-017).",
					c.name, sched.Kind)
			}
		})
	}
}
