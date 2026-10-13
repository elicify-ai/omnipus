// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U6 — the wire-shaped half: no user-facing session-mode chooser,
// a run_isolated checkbox for scheduled work, and the legacy every/cron
// Calendar surface removed.
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md,
// FR-017 and DEL-19 (read from the spec text, never from the implementation):
//
//	FR-017 | ... derive MAIN fresh real assignee-main child per run (MAIN beats
//	CONTINUE), worker one-time ISOLATED, recurring worker CONTINUE; scheduled
//	isolation checkbox forces fresh independent chat for either role. ... No
//	mode chooser/handover.
//	DEL-19 | ... pkg/gateway/task_occurrences.go::expandCronServerZone, legacy
//	every projection; src/components/calendar/CalendarEventSlideOver.tsx::isLegacyTrigger,
//	old-trigger preservation/preview ...
//
// These are file-shape assertions over the committed contract and Calendar
// sources. They compile against the pre-change tree and fail because the
// chooser/legacy surface is still present, not for any compile reason.
//
// Certainty note: the exact SHAPE of the new derived mode and the run_isolated
// checkbox's home schema are NOT fully fixed by the spec text (see the U6
// questions in the RED report). This test therefore asserts only the two
// properties the spec states outright — the chooser is gone, and a run_isolated
// field exists somewhere on the scheduled-work contracts.

package gateway

import (
	"os"
	"strings"
	"testing"
)

// readU6RepoFile reads a repo-relative file. Go tests run with their working
// directory set to the package directory (pkg/gateway/), so two levels up is
// the repo root.
func readU6RepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// TestU6_FR017_ScheduleContractHasNoModeChooser asserts the user-facing
// session-mode chooser is gone from the schedule contracts, and that a
// run_isolated field exists on the scheduled-work surface.
func TestU6_FR017_ScheduleContractHasNoModeChooser(t *testing.T) {
	chooserDocs := []string{
		"../../contracts/components/schemas/ScheduleCreate.yaml",
		"../../contracts/components/schemas/ScheduleUpdate.yaml",
		"../../contracts/components/schemas/Schedule.yaml",
	}
	for _, rel := range chooserDocs {
		content := readU6RepoFile(t, rel)
		if strings.Contains(content, "session_mode") {
			t.Errorf("FR-017: %s still exposes a user-facing session_mode chooser. The task/schedule "+
				"mode is DERIVED from who runs it (MAIN assignee → fresh main child; worker → "+
				"ISOLATED once / CONTINUE recurring); there is no mode chooser/handover "+
				"(session-core-spec FR-017).", rel)
		}
	}

	// A run_isolated checkbox must exist on the scheduled-work contracts. The
	// spec names it but not the exact schema, so the guard accepts any of the
	// schedule/task create-update documents.
	isolatedDocs := []string{
		"../../contracts/components/schemas/ScheduleCreate.yaml",
		"../../contracts/components/schemas/ScheduleUpdate.yaml",
		"../../contracts/components/schemas/TaskCreateRequest.yaml",
		"../../contracts/components/schemas/TaskUpdateRequest.yaml",
	}
	found := false
	for _, rel := range isolatedDocs {
		if strings.Contains(readU6RepoFile(t, rel), "run_isolated") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FR-017: none of %v declare a run_isolated field. The scheduled-work isolation "+
			"checkbox forces a fresh independent chat for either role and must be a wire field "+
			"(session-core-spec FR-017).", isolatedDocs)
	}
}

// TestU6_DEL19_NoLegacyCalendarEverySurface asserts the legacy every/cron
// Calendar adapters are gone from both halves DEL-19 names outside pkg/agent.
func TestU6_DEL19_NoLegacyCalendarEverySurface(t *testing.T) {
	// pkg/gateway half: the legacy cron_expr occurrence expander.
	if src := readU6RepoFile(t, "task_occurrences.go"); strings.Contains(src, "expandCronServerZone") {
		t.Errorf("DEL-19: pkg/gateway/task_occurrences.go still contains expandCronServerZone. The " +
			"legacy cron_expr occurrence projection must be deleted; recurring Calendar work uses " +
			"the RRULE next-occurrence path (session-core-spec DEL-19).")
	}

	// Calendar FE half: the legacy-trigger detection/preservation.
	if src := readU6RepoFile(t, "../../src/components/calendar/CalendarEventSlideOver.tsx"); strings.Contains(src, "isLegacyTrigger") {
		t.Errorf("DEL-19: src/components/calendar/CalendarEventSlideOver.tsx still contains " +
			"isLegacyTrigger. The old-trigger preservation/preview and its misleading legacy " +
			"label must be deleted (session-core-spec DEL-19).")
	}
}
