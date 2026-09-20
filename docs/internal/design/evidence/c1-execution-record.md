# C1 execution record

Live record of the C1 repair batch (foundations: tokens, 12px floor, 4/8px spacing, status colours, touch foundations). One row per applied script or change, in the order applied. Every row states the gate results that were actually observed.

**Start state:** HEAD `040caf34d`, Stage B installed, checkpoint-B audit PASS (0 errors), 3,451 recorded breaches (C1 3,269 · C2 133 · C5 49), 0 unsupported.

## Order of application

Scripts are applied ONE AT A TIME by the lead, because 255 of the 302 affected files (84%) carry breaches from more than one rule family. Authoring and verification run in parallel; application does not.

| # | Script or change | Items targeted | Applied | Typecheck | Tests | Audit diff | Commit |
|---|---|---|---|---|---|---|---|
| _(rows are appended as each is applied)_ | | | | | | | |

## Gate for every row

1. Preview run: planned edits and refusals recorded.
2. Apply, then re-run: must plan zero further edits (safe to re-run).
3. `npm run typecheck` exit 0.
4. Related tests green; any failure root-caused before the next row.
5. Audit with committed scanners: exactly the targeted findings gone, no new finding, no debt fingerprint removed without a probe.
6. Commit, authored by the human, referencing this record.

## Batch close

C1 closes when: the four C1 families report zero debt, the four locks are switched to blocking, the touch check passes before and after, `/code-review high` is clean, and the founder has looked at the running app (this batch changes text sizes and spacing visibly, by prior approval).
