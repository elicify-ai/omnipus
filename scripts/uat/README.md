# UAT re-test harness scripts

Reference copies of the scripts that ran the 2026-09-13/14 knowledge-base
UAT campaigns. The live copies (with current paths) live outside the repo in
`uat/` on the operator machine; these copies are committed so the campaigns
are reproducible and citable from issues.

- `retest-workflow.js` — the Workflow-tool script: nine parallel self-validating
  tester lanes (one account each), an independent validator per lane, and a
  merge into one feature-level report. Paths inside are the operator
  machine's absolute paths at the time of the run.
- `rebuild-retest.sh` — the rebuild window: build SPA + binary from a worktree,
  stop the UAT gateway by exact pid, install, add tester accounts, restart,
  prove every account can log in.
- `ci-watch.sh` — launch and stream a full CI run on the Fly worker
  (ci-omnipus-3), gate by gate, with a final pass/fail count.
- `retest-watch.sh` — watch lane reports for `LANE DONE`, and the UAT gateway
  for outages and panics.

Plans: `docs/internal/uat/retest/` (RETEST-PLAN-2026-09-14.md — the plan and
tester protocol; RETEST-FOLLOWUP-2026-09-14.md — every row left to re-run,
with its fixture). Original campaign plan:
`docs/internal/uat/knowledge-base-full-uat-plan-2026-09-13.md`.
