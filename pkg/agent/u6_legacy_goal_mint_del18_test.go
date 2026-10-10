// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// session-core U6 — DEL-18: the upgrade-only legacy task-goal minting is gone.
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md,
// DEL-18 row (read from the spec text, never from the implementation):
//
//	DEL-18 | pkg/agent/task_goal_terminal.go::mintLegacyTaskGoal; legacy
//	task-goal backfill branches reached from activateTaskGoal, task create/edit/
//	wire | fresh task-goal creation/update and task-owned goal activation. Keep
//	intentional scratchpad/non-goal behavior; no upgrade-only goal minting.
//
// Two tests, deliberately independent:
//
//   - TestU6_DEL18_NoLegacyGoalMinterSymbols is a grep-clean assertion over the
//     two production files that carried the minter and its call site — it
//     catches the symbol coming back under its old name.
//   - TestU6_DEL18_NoGoalMintedAtRunStart drives the real activation path
//     (activateTaskGoal) for a task with no paired goal record and asserts no
//     record is created — so the BEHAVIOUR is pinned even if the helper is
//     renamed.

package agent

import (
	"errors"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestU6_DEL18_NoLegacyGoalMinterSymbols is the deletion guard for DEL-18.
// The minter lived in task_goal_terminal.go and its only caller was the
// ErrOwnerNotFound branch of activateTaskGoal in task_executor.go; both must be
// free of it after U6.
func TestU6_DEL18_NoLegacyGoalMinterSymbols(t *testing.T) {
	const forbidden = "mintLegacyTaskGoal"
	for _, filename := range []string{"task_goal_terminal.go", "task_executor.go"} {
		content := readOwnedFileForTest(t, filename)
		if strings.Contains(content, forbidden) {
			t.Errorf("DEL-18: pkg/agent/%s still references %q. The upgrade-only legacy "+
				"task-goal minter and its activateTaskGoal backfill branch must be deleted — a "+
				"task with no paired goal record must not have one minted for it at run start "+
				"(session-core-spec DEL-18).", filename, forbidden)
		}
	}
}

// TestU6_DEL18_NoGoalMintedAtRunStart is DEL-18's behavioural half.
//
// Given a task with NO paired goal record (a shape greenfield only reaches for
// a scratchpad or a malformed fixture)
//
//	When its run activates the task's goal (activateTaskGoal)
//
//	Then no goal record is created for it.
//
// RED today: activateTaskGoal's ErrOwnerNotFound branch calls mintLegacyTaskGoal,
// persisting a fresh record the task never had — the upgrade-only minting DEL-18
// deletes. The pre-fix run leaves a record behind, which the assertion catches.
func TestU6_DEL18_NoGoalMintedAtRunStart(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	tk := &task.Task{
		ID:          "u6-legacy-nogoal-1",
		AgentID:     "native-agent",
		WorkspaceID: "test-ws",
		Title:       "no paired goal record",
		Status:      task.StatusNext,
	}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatalf("create task fixture: %v", err)
	}

	gstore := goal.NewStore(config.OmnipusHomeDir())

	// Precondition: the fixture genuinely has no paired goal record, otherwise
	// this test could pass for the wrong reason (a record already present).
	if _, err := gstore.GetByOwner(generated.GoalOwnerKindTask, tk.ID); err == nil {
		t.Fatal("arrange: the task fixture already has a paired goal record — the precondition " +
			"(no record) is violated, so this test would prove nothing")
	} else if !errors.Is(err, goal.ErrOwnerNotFound) {
		t.Fatalf("arrange: reading the task's paired goal record failed for an unexpected reason: %v", err)
	}

	// Drive the real activation path. Its error is not the oracle — a task with
	// no usable goal still runs (GOAL-FR-023) — the created record is.
	_ = al.taskExecutor.activateTaskGoal(tk, "u6-legacy-nogoal-session")

	if g, err := gstore.GetByOwner(generated.GoalOwnerKindTask, tk.ID); err == nil {
		t.Fatalf("DEL-18: activateTaskGoal minted a goal record (goal_id=%q) for a task that had "+
			"none. Upgrade-only task-goal minting must be deleted: a task reaching run start with no "+
			"paired record must not have one created for it (session-core-spec DEL-18).", g.GoalID)
	} else if !errors.Is(err, goal.ErrOwnerNotFound) {
		t.Fatalf("reading the task's paired goal record after activation failed for an unexpected reason: %v", err)
	}
}
