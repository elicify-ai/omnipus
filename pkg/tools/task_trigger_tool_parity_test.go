// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_trigger_tool_parity_test.go — RED tests for the task trigger tool
// surface (tool-parity spec r17 §13.1 test 12; scenarios 18.1, 18.3–18.10;
// dataset D-9; §7.2 TK-30).
//
// ORACLE: the store's own ValidateTrigger messages (§7.11: "the store's
// ValidateTrigger message"), the §7.7 authority matrix, D-2/D-9, and TK-30's
// scheduling states. The tools do not accept a `trigger` argument yet, so the
// expected RED failure is "the trigger argument was ignored" — asserted on
// the real store and, where named by the spec, byte for byte.
//
// This file pins tool-side store behaviour only; despite the historical test
// name it does not run a REST differential. pkg/agent's production-wiring tests
// reach create and attachment, but recurring rule replacement/removal through
// REST, approvals and audit are still incomplete; see the traceability report.

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// triggerArg builds a tool-level trigger argument (same wire shape as
// TaskTrigger: type + config).
func triggerArg(triggerType string, config map[string]any) map[string]any {
	return map[string]any{"type": triggerType, "config": config}
}

func TestTaskTrigger_ToolParityWithRest(t *testing.T) {
	t.Run("18.1 create with a recurring trigger stores it and lands inbox", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tool := parityCreateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"title": "weekly report", "agent_id": "agent-a", "prompt": "write the weekly report",
			"criteria": validCriteriaArg(), "dod": validDoDArg(),
			"trigger": triggerArg("recurring", map[string]any{
				"rrule": "FREQ=WEEKLY;BYDAY=MO", "dtstart_ms": 1793596800000, "tz": "Europe/Berlin",
			}),
		})
		require.False(t, res.IsError, "the create must accept the trigger: %s", res.ForLLM)

		// Read the created task back from the store.
		tk := parityFindTaskByTitle(t, store, "weekly report")
		require.NotNil(t, tk.Trigger, "the task carries the trigger")
		assert.Equal(t, task.TriggerRecurring, tk.Trigger.Type)
		require.NotNil(t, tk.Trigger.Config.Rrule)
		assert.Equal(t, "FREQ=WEEKLY;BYDAY=MO", *tk.Trigger.Config.Rrule)
		require.NotNil(t, tk.Trigger.Config.Tz)
		assert.Equal(t, "Europe/Berlin", *tk.Trigger.Config.Tz)
		assert.Equal(t, task.StatusInbox, tk.Status,
			"a non-manual trigger lands the scheduler-owned waiting state inbox, never next (TK-30)")
	})

	t.Run("18.3 delegation refusal still applies first for a target with no edge", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tool := parityCreateTool(store)
		tool.SetDelegationDenyChecker(func(_ context.Context, target string) *DelegationDenial {
			if target == "agent-c" {
				return &DelegationDenial{Reason: "policy: no delegation edge"}
			}
			return nil
		})

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"title": "for c", "agent_id": "agent-c", "prompt": "p",
			"criteria": validCriteriaArg(), "dod": validDoDArg(),
			"trigger": triggerArg("once", map[string]any{"at_ms": 1793596800000}),
		})
		require.True(t, res.IsError, "the delegation refusal applies before any trigger handling: %s", res.ForLLM)
		assert.Nil(t, parityFindTaskByTitle(t, store, "for c"), "nothing stored")
	})

	t.Run("18.4 the assignee (not creator) changes the rrule; the store follows", func(t *testing.T) {
		store, _ := parityStorePair(t)
		// A recurring task created by A and assigned to B.
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, false)
		monday := "FREQ=WEEKLY;BYDAY=MO"
		tuesday := "FREQ=WEEKLY;BYDAY=TU"
		_, err := store.Update(tk.ID, task.Patch{Trigger: &[]*task.Trigger{{Type: task.TriggerRecurring,
			Config: task.TriggerConfig{Rrule: &monday, DtstartMs: int64Ptr(1793596800000), Tz: strPtr("UTC")}}}[0]})
		require.NoError(t, err, "fixture: arm the Monday schedule")
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-b"), map[string]any{
			"task_id": tk.ID,
			"trigger": triggerArg("recurring", map[string]any{
				"rrule": tuesday, "dtstart_ms": 1793596800000, "tz": "UTC",
			}),
		})
		require.False(t, res.IsError, "the assignee in the task's workspace may change the trigger (D-2): %s", res.ForLLM)

		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		require.NotNil(t, got.Trigger.Config.Rrule)
		assert.Equal(t, tuesday, *got.Trigger.Config.Rrule,
			"the persisted trigger follows the assignee's edit (this assertion does not prove the live scheduler update)")
	})

	t.Run("18.5 trigger manual removes the schedule and changes no state", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		monday := "FREQ=WEEKLY;BYDAY=MO"
		_, err := store.Update(tk.ID, task.Patch{Trigger: &[]*task.Trigger{{Type: task.TriggerRecurring,
			Config: task.TriggerConfig{Rrule: &monday, DtstartMs: int64Ptr(1793596800000), Tz: strPtr("UTC")}}}[0]})
		require.NoError(t, err, "fixture: arm the schedule")
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID, "trigger": triggerArg("manual", map[string]any{}),
		})
		require.False(t, res.IsError, "%s", res.ForLLM)

		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		if got.Trigger != nil && got.Trigger.Type != task.TriggerManual {
			t.Fatalf("trigger = %v, want manual", got.Trigger.Type)
		}
		assert.Equal(t, task.StatusInbox, got.Status,
			"the manual-ization changes no state (TK-30 rule 4)")
	})

	t.Run("D-9 invalid triggers are refused with the store's messages", func(t *testing.T) {
		store, _ := parityStorePair(t)
		create := parityCreateTool(store)
		update := NewTaskUpdateTool(store)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusNext, false)

		cases := []struct {
			name     string
			trig     map[string]any
			wantText string
		}{
			{
				name:     "D-9 row 3: once without at_ms",
				trig:     triggerArg("once", map[string]any{}),
				wantText: `trigger "once" requires config.at_ms`,
			},
			{
				name:     "D-9 row 10: rrule on a once trigger",
				trig:     triggerArg("once", map[string]any{"at_ms": 1793596800000, "rrule": "FREQ=DAILY"}),
				wantText: `config.rrule/dtstart_ms/tz are only valid on a "recurring" trigger`,
			},
			{
				name:     "D-9 row 9: removed type every",
				trig:     triggerArg("every", map[string]any{"every_ms": 60000}),
				wantText: `trigger type "every" is no longer supported`,
			},
			{
				name:     "D-9 row 9: cron_expr is no longer supported",
				trig:     triggerArg("recurring", map[string]any{"rrule": "", "cron_expr": "0 9 * * MON"}),
				wantText: "config.cron_expr is no longer supported",
			},
			{
				name: "D-9 row 6: a 513-character rrule is refused",
				trig: triggerArg("recurring", map[string]any{
					"rrule": "FREQ=DAILY;" + strings.Repeat("X", 513-len("FREQ=DAILY;")), "dtstart_ms": 1793596800000, "tz": "UTC",
				}),
			},
			{
				name: "D-9 row 7: COUNT=100001 is refused",
				trig: triggerArg("recurring", map[string]any{
					"rrule": "FREQ=DAILY;COUNT=100001", "dtstart_ms": 1793596800000, "tz": "UTC",
				}),
			},
			{
				name: "D-9 row 8: a rule that never fires within five years is refused",
				trig: triggerArg("recurring", map[string]any{
					"rrule": "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31", "dtstart_ms": 1793596800000, "tz": "UTC",
				}),
			},
			{
				name:     "D-9 row 5: recurring without dtstart_ms is refused",
				trig:     triggerArg("recurring", map[string]any{"rrule": "FREQ=DAILY;COUNT=3", "tz": "UTC"}),
				wantText: "config.dtstart_ms",
			},
			{
				name:     "D-9 row 9: every_ms is no longer supported",
				trig:     triggerArg("recurring", map[string]any{"rrule": "FREQ=DAILY;COUNT=3", "dtstart_ms": 1793596800000, "tz": "UTC", "every_ms": 60000}),
				wantText: "config.every_ms is no longer supported",
			},
			{
				name: "#1771: dtstart_ms in seconds is refused with a field-and-unit message",
				trig: triggerArg("recurring", map[string]any{
					"rrule": "FREQ=WEEKLY;BYDAY=MO", "dtstart_ms": 1794816000, "tz": "Europe/Berlin",
				}),
				wantText: "config.dtstart_ms must be a Unix time in milliseconds",
			},
			{
				name: "D-9 row 5: recurring without tz is refused",
				trig: triggerArg("recurring", map[string]any{
					"rrule": "FREQ=DAILY;COUNT=3", "dtstart_ms": 1793596800000,
				}),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Run("create", func(t *testing.T) {
					res := create.Execute(parityCtx("agent-a"), map[string]any{
						"title": "bad trigger", "agent_id": "agent-a", "prompt": "p",
						"criteria": validCriteriaArg(), "dod": validDoDArg(), "trigger": tc.trig,
					})
					require.True(t, res.IsError, "create must refuse the invalid trigger; got success: %s", res.ForLLM)
					if tc.wantText != "" {
						assert.Contains(t, res.ForLLM, tc.wantText,
							"the store's ValidateTrigger message is the refusal (§7.11)")
					}
					assert.Nil(t, parityFindTaskByTitle(t, store, "bad trigger"), "nothing stored")
				})
				t.Run("update", func(t *testing.T) {
					res := update.Execute(parityCtx("agent-a"), map[string]any{
						"task_id": tk.ID, "trigger": tc.trig,
					})
					require.True(t, res.IsError, "update must refuse the invalid trigger; got success: %s", res.ForLLM)
					if tc.wantText != "" {
						assert.Contains(t, res.ForLLM, tc.wantText)
					}
				})
			})
		}
	})

	t.Run("18.9 the assignee may change the trigger while the task runs; state unchanged", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-a", "ws-a", task.StatusInProgress, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-a"), map[string]any{
			"task_id": tk.ID,
			"trigger": triggerArg("recurring", map[string]any{
				"rrule": "FREQ=DAILY;COUNT=3", "dtstart_ms": 1793596800000, "tz": "UTC",
			}),
		})
		require.False(t, res.IsError, "the trigger is not frozen while running (as REST): %s", res.ForLLM)
		got, _ := store.Get(tk.ID)
		require.NotNil(t, got.Trigger)
		assert.Equal(t, task.TriggerRecurring, got.Trigger.Type)
		assert.Equal(t, task.StatusInProgress, got.Status, "the state is unchanged by the trigger edit")
	})

	t.Run("18.10 an unrelated agent may not set the trigger", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, false)
		tool := NewTaskUpdateTool(store)

		res := tool.Execute(parityCtx("agent-x"), map[string]any{
			"task_id": tk.ID,
			"trigger": triggerArg("once", map[string]any{"at_ms": 1793596800000}),
		})
		require.True(t, res.IsError, "neither creator nor assignee: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, "you can only update tasks you own or are assigned",
			"the existing ownership refusal text")
	})
}

// int64Ptr is a fixture helper for TriggerConfig's pointer fields.
func int64Ptr(v int64) *int64 { return &v }

// parityCreateTool wires the delegation gate open for these fixtures (no
// delegation policy edge is installed here; 18.3 overrides the checker with
// its own refusing one).
func parityCreateTool(store *task.Store) *TaskCreateTool {
	tool := NewTaskCreateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	return tool
}

// parityFindTaskByTitle returns the store's task with the given title (the
// tool result's id parsing is not pinned here; the store is the oracle).
func parityFindTaskByTitle(t *testing.T, store *task.Store, title string) *task.Task {
	t.Helper()
	list, err := store.List(task.Filter{})
	require.NoError(t, err, "list tasks")
	for i := range list {
		if list[i].Title == title {
			return &list[i]
		}
	}
	return nil
}
