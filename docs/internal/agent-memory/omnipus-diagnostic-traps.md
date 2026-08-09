---
name: omnipus-diagnostic-traps
description: "Where to look when an Omnipus test/CI signal misleads — WARN-only logs, keyword greps, and the cross-agent seam nobody owns"
metadata: 
  node_type: memory
  type: project
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-07-29T16:08:49.868Z
---

Hard-won on `feature/plan-swimlane-board` (2026-07-29). Each cost real time.

**The gateway log is WARN-level only.** `/tmp/omnipus-e2e-<shard>.gw.log` on the CI
worker had **zero INF/DBG lines** in 9526 lines. It is structurally incapable of
containing a causal chain. **Go to the persisted state instead:**
`$OMNIPUS_HOME/plans/<id>.json` (`state`, `failed_reason`, `correction_rounds`),
`$OMNIPUS_HOME/plan_intents/<id>.jsonl` (the committed correction verbs — authoritative),
`$OMNIPUS_HOME/tasks/`. I diagnosed a plan failure from the log, was wrong, and the
plan JSON answered it in one command.

**Never diagnose from a keyword grep.** I grepped a shard log for `abandon`, got 3 hits,
and concluded a stall wake had abandoned a recoverable plan. The hits were
`task_executor.go`'s unrelated *"goal-loop: judge cycle abandoned (context canceled
during backoff)"* for three different task ids. The plan had actually committed **9
supersedes** and ended `judge_rounds_exhausted` — the opposite story. Confirm a verb in
the intent log before building a narrative on it.

**Playwright retries compound load.** `playwright.config.ts` runs `retries: 3`,
`workers: 1`, and a failed attempt's plan is **never stopped** — so each retry leaves
its plan/members/supervision-wakes live in the SAME gateway process. One shard showed 3
concurrent `t3` plans + 2 `t2` plans. Heavy real-LLM tests then fail *deterministically*
while lighter ones merely *flake* — same root cause, two severities. Fixed with an auto
fixture that stops created plans on every attempt (teardown-runs-on-failure verified by
reading `workerProcessEntry.js`, not assumed).

**Nobody owns the seam between parallel agents.** Three times, a correct fix broke a
test owned by a different agent, and each agent correctly said "not my write-set":
shadow-lint findings both dismissed (went red in CI), a nil-pointer from a new gate
meeting an old harness, and a latch narrowing invalidating 3 tests in 2 other packages.
**After any parallel wave, grep the whole repo for the changed premise** — don't rely on
per-agent scope.

**A red test is not evidence of what it blames** (and a green one proves nothing —
see [[mechanism-not-property-defect-class]]). Same test, opposite meanings across two
runs: t3 failed first because SUPERSEDE was genuinely unusable, then because the test
demanded a redundant verb the engine's own auto-reset made unnecessary. Telling those
apart needed the engine source (`plan_engine.go:4295` auto-resets all failed members on
append/supersede), not a re-run.

**The `.fablize/` hook fires "tool failure" spuriously** — a stale state dir predating
the session. ~8 agents independently reported it. Check each gate's own exit code.

Related: [[race-testability-unlocked-four-races]], [[ci-worker-deployed-script-drift]].
