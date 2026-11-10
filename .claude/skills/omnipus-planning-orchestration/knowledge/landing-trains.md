# Landing trains — landing branches, the cut and verification

Last reviewed: 2026-10-29 — trunk-based landing branches, cut when the leads say ready, Test Lab lane
Process version: 2026-10-29.1

Root `CLAUDE.md` is the rule source: "Landing trains", "Feature ownership", "Change
sizes and the review gate", "CI scope and remote pre-flight", "CI quarantine",
"Hands-on pipeline", "In-product test step" and "Running CLI agents". This page is the
execution procedure, not a second rule table. Lock/approval/push formats and the terms
"release branch" and "landing branch" live in `coordination-ledger.md`.

## 1. Plan owners and acceptance before dispatch

Record the feature, size, whole-area implementer(s), its single test author, the
landing branch (`landing/<train>`, or the standalone feature's own landing branch),
the riders and each owner's file area in the ledger. Separate CHECK and UAT validation
instances remain independent. For a bounded small cross-stack commission, name both
backend and frontend scope explicitly and supply the required frontend guidance;
otherwise use the one backend/one frontend split. Keep related findings with those
owners instead of creating sub-workers for overlapping files.

Write hands-on rows with the spec: acceptance criterion, human actions, expected
result, affected area and validator critical path. `plan-spec`'s spec template carries
the row shape; qa-lead refines the lane/account/evidence plan from it, including the
in-product row set for a train that changes agent tools. Missing product oracles go
back through the unchanged founder-question process, never guesses.

## 2. Landing branches, direct pushes, then the cut

At planning the chief creates `landing/<train>` from the release head (or from the
previous train's head while that train is unlanded) and records the train's scope,
owners and file areas in the ledger. Teams push to it directly under the own-tests rule
in root `CLAUDE.md`; there are no work-package branches and no PR into the landing
branch. Small commits, pull and rebase before every push, one owner per file area; a
push that breaks the build or its own tests is fixed or reverted by the pusher within
the hour.

There is no fixed cut time. When the team leads say the train is ready, the chief
schedules the cut, snapshots the included item list, records the exact SHA, opens the
single draft PR from the landing branch to the release branch and freezes the branch:
only fixes land on it from then on. Later work waits for the next train's landing
branch rather than continuously extending this one. Features bring their RED/GREEN/CHECK
evidence; small/standard items bring implementation/test and applicable check evidence
for the train's combined review.

A standalone feature has its own landing branch and PR, built the same way, instead of
holding a train open. Record both landing branches' bases and check which landed first;
whoever lands second merges the other and re-checks affected areas on that result. The
chief does mechanical merges directly. Delegate only real conflicts needing a
design/code choice, to the existing area owner (architect for a design decision).
Record conflict resolutions and the resulting SHA; a text-only merge is not grounds to
commission a merge worker.

## 3. Scope reviews and CI once

Use the root size/reviewer table, not a reviewer set invented for each author. Dispatch
the feature gate once on the landing branch at the cut commit. On the train, give the
applicable reviewers the combined small/standard item diff, with each item's size and
scope; shared reviewers see it once, not one dispatch per item. Include merge
resolutions in their relevant scope. Do not repeat a clean feature gate merely because
it rides a train; review real conflict resolutions proportionately. Track each gate's
rounds and send fixes only to the reviewers whose findings changed. Retain the
whole-epic gate before `main` and on-demand security review for focus areas.

The cut PR starts the one full CI run. Save the run ID, SHA, job names, direct results
and evidence. Re-run failed jobs only; bind each receipt to the commit it actually
checked. After a fix lands on the frozen branch, never relabel an old green as evidence
for new code: identify affected jobs and re-check that scope, retaining unaffected
receipts with their original SHAs. If that scope cannot be justified, report the
evidence gap to the founder rather than silently starting another full run or claiming
a pass. No full CI before the cut, no cancellation loop and no per-fix full Fly tier is
part of this procedure. Use shared-rules "Build and test" for the own-tests rule and
approved Fly pre-flight. Agent-text-only work retains the root reviewer exception.

For quarantine, use the implemented list and required **CI** aggregate linked from
root `CLAUDE.md` ("CI implementation pointer", "CI quarantine"). Read its summary
for exact exception scopes, issue links and dependent checks not run; leaf jobs and
the overall workflow banner may stay red. An unlisted failure or missing evidence
blocks that aggregate. Re-running failed jobs uses the latest attempt evidence;
never describe retained earlier successes as freshly executed on the new commit.
Use the pinned implementation guide when the release-based checkout predates that
CI implementation; a missing mechanism in an older tree is not a qualified pass.

## 4. Run the hands-on pipeline on the cut commit

Map the cut diff to the spec's affected-area rows and record included/excluded row IDs
with reasons. Use one campaign for the cut, not a full campaign for each fix. For a
cut with no hands-on surface, record why no rows apply rather than inventing a product
PASS. Agent-engine changes still require the root's exact-commit check.

Resolve the one rebuild script from the campaign ledger and inspect its invocation
before use; never invent a script path. It must rebuild the standing per-lane tester
and validator instances from the exact cut commit. Save the source SHA, binary
identity and readiness receipts. If the script or version proof is missing, report
BLOCKED on that prerequisite; do not describe a planned rebuild as an executed test.
Live builds use the release-pinned toolchain, not whichever compiler is first on PATH.

Launch tester and validator together on separate instances, accounts and private
browsers. Give both the same rows and expected results, but separate evidence spaces.
The validator starts its independent critical-path re-drive immediately. As tester
evidence arrives, the dispatcher makes it available for validation; no tester/validator
contact or implementation excuse substitutes for observation. Pairing start times does
not permit a validator to approve a claim whose evidence has not arrived.

After a fix, select only rows touched by that diff and bind both workers' evidence to
the new commit. Record unchanged rows as retained evidence from their original
version, never as freshly re-tested. Ask the feature's test author to convert stable
rows into automated end-to-end tests over time, with independent CHECK as applicable.

After the landing and the Mac's live update, the Omnipus Test Lab runs the in-product
rows (root `CLAUDE.md`, "In-product test step"); this lane never blocks the landing and
is not a substitute for the exact-commit hands-on test. Its findings reach the board as
issues filed by the chief.

## 5. Present evidence, then land under the lock

Use `coordination-ledger.md`'s landing sequence. The founder sees the exact cut commit,
review verdicts, CI result (including qualified exceptions), hands-on receipts and
reachability evidence before the specific landing ask. Approval still precedes the
lock. If the target moved, merge and re-check affected code/rows on the new version,
then update the evidence and ask for that commit; never hold the lock across checks.

After the push, release the lock, record the landed commit, delete the merged landing
branch and close resolved issues with commit-citing comments (the cut PR's keywords do
not close issues on a release-branch merge). Keep the two delivery claims separate.
After three trains, re-measure the process-review measures (worker/review runs, CI runs
and landing/campaign time), retaining the receipts and distinguishing measured costs
from reported notional prices; report the result to the founder without changing rules
on your own.
