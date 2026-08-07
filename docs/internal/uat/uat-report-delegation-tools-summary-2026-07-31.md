# UAT Execution Summary

**Date:** 2026-07-31
**Tester:** Jim (Orchestrator)
**Plan:** uat-plan.md (v2)
**Issues:** uat-issues.md

---

## Test Results by Suite

| Suite | Tests | PASS | FAIL | BLOCKED | Notes |
|-------|-------|------|------|---------|-------|
| A: Dispatch | 7 | 5 | 0 | 2 | snapshot/critical params blocked by ISS-001 |
| B: Monitor | 12 | 10 | 0 | 2 | list_jobs, list_tasks, peek all work well |
| C: Communicate | 7 | 1 | 4 | 2 | steer works; inbox/respond/message_parent broken (ISS-001) |
| D: Control | 3 | 0 | 2 | 1 | cancel no-op (ISS-002), follow_up broken (ISS-004) |
| E: Plan/Task Lifecycle | 8 | 2 | 4 | 2 | create_plan works; plan_id broken (ISS-003); run_task works |
| F: Background Bash | 7 | 6 | 1 | 0 | for-loop blocked (ISS-008); all else works |
| G: Profiles | 2 | 0 | 2 | 0 | Both blocked by ISS-001 (message_parent) |
| H: Edge Cases (Invalid) | 22 | 18 | 2 | 2 | Most validation works; priority not enforced (ISS-009) |
| I: Edge Cases (Boundary) | 9 | 7 | 1 | 1 | timeout bounds enforced; chain depth untested |
| J: Edge Cases (Security) | 5 | 5 | 0 | 0 | All pass — absolute paths, .. escapes, allowlist all enforced |
| K: Edge Cases (Race) | 5 | 1 | 0 | 4 | Most blocked by ISS-001/ISS-002 |
| L: Edge Cases (Resource) | 4 | 2 | 0 | 2 | Partial — terminal threshold, large output tested |
| M: Edge Cases (Timing) | 5 | 2 | 1 | 2 | Steer timing race (ISS-007) |
| **TOTAL** | **96** | **59** | **16** | **21** | |

---

## Issues Summary

| ID | Severity | Title | Status |
|----|----------|-------|--------|
| ISS-001 | P0 Blocker | message_parent non-functional for all delegated subagents | Open |
| ISS-002 | P0 Blocker | Cancel (soft+hard) has no effect on subagent execution | Open |
| ISS-003 | P1 Critical | create_task with plan_id fails — "plan store not configured" | Open |
| ISS-004 | P1 Critical | follow_up on completed session fails — "lifecycle record not found" | Open |
| ISS-005 | P2 Major | Default 5-min timeout kills long-running subagents | Open |
| ISS-006 | P3 Minor | respond action requires session_id despite docs | Open |
| ISS-007 | P4 Trivial | Steer timing race — both STEERED and no-steer in output | Open |
| ISS-008 | P3 Minor | bash for-loop blocked by safety guard | Open |
| ISS-009 | P3 Minor | create_task does not validate priority range | Open |

---

## What Works

- **Dispatch (async/sync):** Both work correctly. Subagents spawn, run, and return results.
- **Status polling:** `delegate(action="status")` correctly reports running/completed states.
- **Peek:** Non-disruptive progress read works as designed.
- **list_jobs:** Dashboard with kind/status/label filters all work. Terminal suppression works.
- **list_tasks:** Role and status filters work correctly.
- **Steer:** Mid-run instruction injection works (with minor timing race).
- **create_task (without plan_id):** Rich params (title, prompt, criteria, priority, due) all work.
- **update_task:** Status transitions, reassignment, rename all work.
- **run_task:** Standalone task execution with retry loop works.
- **create_plan:** Plan creation works, returns draft status.
- **Foreground bash:** Works correctly, returns stdout.
- **Background bash:** Dispatch, poll, read incremental output, kill, timeout enforcement, cwd, persistent — all work.
- **Security boundaries:** Absolute path rejection, `..` escape rejection, allowlist enforcement, empty command rejection — all work.
- **Input validation:** Empty title, empty criteria, empty command, invalid agent — all correctly rejected.

## What's Broken

- **Parent-child messaging (P0):** `message_parent` is completely non-functional. No subagent can send any message to its parent. This breaks the entire collaboration channel.
- **Cancel (P0):** Both soft and hard cancel have no effect. Subagents run to completion regardless.
- **Plan lifecycle (P1):** Plans can be created but no tasks can be attached (plan store not configured). The entire plan execution pipeline is broken.
- **Follow-up (P1):** Cannot warm-resume completed subagents.
- **Priority validation (P3):** Out-of-range priority values are silently accepted.

---

## Recommended Fix Priority

1. **ISS-001 (P0):** Fix `message_parent` session registration — the entire specialist collaboration model depends on this.
2. **ISS-002 (P0):** Fix cancel to actually terminate subagent execution — resource safety depends on this.
3. **ISS-003 (P1):** Configure plan store so `create_task` with `plan_id` works — the entire plan lifecycle is blocked.
4. **ISS-004 (P1):** Fix follow_up session lifecycle record lookup.
5. **ISS-005 (P2):** Increase default timeout or document it more clearly.
6. **ISS-008, ISS-009 (P3):** Fix for-loop safety guard and priority validation.
7. **ISS-006, ISS-007 (P3/P4):** Minor doc/timing fixes.
