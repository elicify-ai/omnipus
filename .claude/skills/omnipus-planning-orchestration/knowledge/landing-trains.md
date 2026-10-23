# Landing trains — candidate preparation and verification

Last reviewed: 2026-10-23

Root `CLAUDE.md` is the rule source: "Landing trains", "Feature ownership", "Change
sizes and the review gate", "CI scope and remote pre-flight", "CI quarantine",
"Hands-on pipeline" and "Running CLI agents". This page is the execution procedure,
not a second rule table. Lock/approval/push formats live in `coordination-ledger.md`.

## 1. Plan owners and acceptance before dispatch

Record the feature, size, whole-area implementer(s), its single test author, combined
feature branch and intended cut in the ledger. Separate CHECK and UAT validation
instances remain independent. For a bounded small cross-stack commission, name both
backend and frontend scope explicitly and supply the required frontend guidance;
otherwise use the one backend/one frontend split. Keep related findings with those
owners instead of creating sub-workers for overlapping files.

Write hands-on rows with the spec: acceptance criterion, human actions, expected
result, affected area and validator critical path. `plan-spec`'s spec template carries
the row shape; qa-lead refines the lane/account/evidence plan from it. Missing product
oracles go back through the unchanged founder-question process, never guesses.

## 2. Cut the candidate, not another growing bundle

Read the founder-adjusted schedule from the ledger, or use the root default. At the
cut, snapshot the eligible item list and exact source SHAs. Record later arrivals for
the next cut rather than continuously extending this candidate. Features bring their
combined-feature gate evidence; small/standard items bring implementation/test and
applicable check evidence for the train's combined review.

The chief creates one integration candidate branch and draft PR from the confirmed
release integration target, then makes its mechanical merges directly. Delegate only
real conflicts needing a design/code choice, to the existing area owner (architect
for a design decision). Record conflict resolutions and the resulting candidate SHA;
a text-only merge is not grounds to commission a merge worker.

Large standalone features keep their own candidate, draft PR and landing instead of
holding a train open. Record both candidates' bases and check which landed first.
The second lander must merge the other and re-check affected areas on that result.

## 3. Scope reviews and CI once

Use the root size/reviewer table, not a reviewer set invented for each lane. Dispatch
the feature gate once on the combined feature branch. On the train, give the applicable
reviewers the combined small/standard item diff, with each item's size and scope;
shared reviewers see it once, not one dispatch per item. Include merge resolutions
in their relevant scope. Do not repeat a clean feature gate merely because it rides
a train; review real conflict resolutions proportionately. Track each gate's rounds
and send fixes only to the reviewers whose findings changed. Retain the whole-epic
gate before `main` and on-demand security review for focus areas.

Start the candidate's one full CI run via its integration draft PR. Save the run ID,
SHA, job names, direct results and evidence. Re-run failed jobs only; bind each receipt
to the commit it actually checked. After a candidate changes, never relabel an old green
as evidence for new code: identify affected jobs and re-check that scope, retaining
unaffected receipts with their original SHAs. If that scope cannot be justified, report
the evidence gap to the founder rather than silently starting full lane CI or claiming
a pass. No full work/lane workflow, cancellation loop or per-fix full Fly tier is part
of this procedure. Use shared-rules "Build and test" for targeted checks and approved
Fly pre-flight. Agent-text-only work retains the root reviewer exception.

For quarantine, inspect the single tracked list and the machine-produced qualified
verdict under root "CI quarantine". Retain issue links and distinguish every listed
exception from an unlisted red. A manually interpreted red is not that verdict. If the
backend mechanism or list is not available, state the gap; only the existing explicit
founder deferral can accept a raw red, and nothing is reported as an unqualified green.

## 4. Run the hands-on pipeline on the candidate

Map the candidate diff to the spec's affected-area rows and record included/excluded
row IDs with reasons. Use one candidate campaign, not a full campaign for each fix.
For a candidate with no hands-on surface, record why no rows apply rather than inventing
a product PASS. Agent-engine changes still require the root's exact-commit check.

Resolve the one rebuild script from the campaign ledger and inspect its invocation
before use; never invent a script path. It must rebuild the standing per-lane tester
and validator instances from the exact candidate version. Save the source SHA, binary
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
the new candidate. Record unchanged rows as retained evidence from their original
version, never as freshly re-tested. Ask the feature's test author to convert stable
rows into automated end-to-end tests over time, with independent CHECK as applicable.

## 5. Present evidence, then land under the lock

Use `coordination-ledger.md`'s landing sequence. The founder sees the exact candidate,
review verdicts, CI result (including qualified exceptions), hands-on receipts and
reachability evidence before the specific landing ask. Approval still precedes the
lock. If the target moved, merge and re-check affected code/rows on the new version,
then update the evidence and ask for that candidate; never hold the lock across checks.

After the push, release the lock, record the landed commit and close resolved issues
with commit-citing comments. Keep the two delivery claims separate. After three trains,
re-measure the process-review measures (worker/review runs, CI runs and landing/campaign
time), retaining the receipts and distinguishing measured costs from reported notional
prices; report the result to the founder without changing rules on your own.
