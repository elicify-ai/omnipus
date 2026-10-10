// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U6 — DEL-15: the owner-less cron-job backfill and the legacy
// AddJob/SetDefaultAgentID creation path are gone.
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md,
// DEL-15 row (read from the spec text, never from the implementation):
//
//	DEL-15 | pkg/cron/service.go::migrateOwners, migrateOwnersUnsafe, AddJob
//	back-compat creation; boot default-agent migration wiring | AddJobFull/
//	JobSpec with explicit authorized owner and derived mode. No owner-less job
//	upgrade backfill; CONV is saved chats only. KEEP CronService.StartAfterPhysicalBoot
//	and notify: deleting owner migration must not remove physical-boot restore
//	or the existing wake slot.
//
// Two tests, deliberately independent:
//
//   - TestU6_DEL15_NoOwnerMigrationSymbols is a grep-clean assertion over the
//     raw service.go source — it catches the deleted symbols coming back.
//   - TestU6_DEL15_AddJobFullRefusesOwnerlessJob drives the surviving
//     constructor and asserts an owner-less job is refused, so the BEHAVIOUR
//     (no owner-less job can be created) is pinned even if the symbols are
//     renamed.
//
// Both must compile against the pre-change code and fail for the reason they
// name, never on a compile error alone.

package cron

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readCronServiceSourceU6 reads the raw service.go source. Go tests run with
// their working directory set to the package directory, so the bare filename
// resolves to pkg/cron/service.go.
func readCronServiceSourceU6(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("read pkg/cron/service.go: %v", err)
	}
	return string(data)
}

// TestU6_DEL15_NoOwnerMigrationSymbols is the deletion guard for DEL-15's
// BE-only half. It asserts the owner-less backfill and the legacy AddJob
// back-compat creator are gone from pkg/cron/service.go, and that the two KEEP
// symbols the spec names explicitly survive.
func TestU6_DEL15_NoOwnerMigrationSymbols(t *testing.T) {
	src := readCronServiceSourceU6(t)

	// Definitions/calls that must NOT exist anywhere in production source.
	forbidden := []string{
		"func (cs *CronService) migrateOwners(",
		"func (cs *CronService) migrateOwnersUnsafe(",
		"cs.migrateOwners()",
		"cs.migrateOwnersUnsafe()",
		"func (cs *CronService) SetDefaultAgentID(",
		"func (cs *CronService) AddJob(",
	}
	for _, pattern := range forbidden {
		if strings.Contains(src, pattern) {
			t.Errorf("DEL-15: pkg/cron/service.go still contains %q. The owner-less backfill "+
				"(migrateOwners/migrateOwnersUnsafe), the boot default-agent migration wiring "+
				"(SetDefaultAgentID) and the legacy AddJob back-compat creator must be deleted — "+
				"AddJobFull/JobSpec with an explicit authorized owner is the only creation path. "+
				"No upgrade-only owner backfill (session-core-spec DEL-15).", pattern)
		}
	}

	// KEEP — deleting owner migration must not remove physical-boot restore or
	// the wake slot.
	kept := []string{
		"func (cs *CronService) StartAfterPhysicalBoot(",
		"func (cs *CronService) notify(",
	}
	for _, pattern := range kept {
		if !strings.Contains(src, pattern) {
			t.Errorf("DEL-15 KEEP: pkg/cron/service.go no longer declares %q. Deleting the owner "+
				"migration must NOT remove physical-boot restore (StartAfterPhysicalBoot) or the "+
				"existing wake slot (notify) (session-core-spec DEL-15 KEEP clause).", pattern)
		}
	}
}

// TestU6_DEL15_AddJobFullRefusesOwnerlessJob pins the surviving BEHAVIOUR of
// DEL-15: the one creation path takes an explicit authorized owner, so a job
// with no owner must be refused rather than persisted for a later backfill.
//
// RED today: AddJobFull copies spec.AgentID straight onto the job with no
// validation, so an owner-less job is persisted — the very shape the deleted
// migrateOwners path used to paper over at boot.
func TestU6_DEL15_AddJobFullRefusesOwnerlessJob(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "tasks_triggers", "jobs.json"))

	atMS := int64(4_000_000_000_000) // far future; value is irrelevant to the assertion
	job, err := cs.AddJobFull(JobSpec{
		Name:     "u6-ownerless",
		Schedule: CronSchedule{Kind: "at", AtMS: &atMS},
		Message:  "no owner supplied",
		AgentID:  "",
	})
	if err == nil {
		t.Fatalf("DEL-15: AddJobFull accepted a job with an empty AgentID (created job %q). "+
			"JobSpec requires an explicit authorized owner; there is no owner-less job upgrade "+
			"backfill anymore (session-core-spec DEL-15).", job.ID)
	}
}
