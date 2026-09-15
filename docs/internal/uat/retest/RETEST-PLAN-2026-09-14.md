# UAT re-test plan — 2026-09-14

**Goal:** re-run every UAT row that failed, was blocked, or confirmed a defect on 2026-09-13, against the build that contains all four fix rounds. Each row is run by a self-validating Sonnet tester agent.

**Inputs:**
- Source rows: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/RETEST-ROWS.md` (75 entries, 73 distinct rows).
- Test plan, including each row's expected outcome: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate/docs/internal/uat/knowledge-base-full-uat-plan-2026-09-13.md`.
- Prior observations: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/report-<lane>.md`.
- Defect register: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/DEFECTS.md`.
- Fix reports: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/FIX-REPORT-*.md` and `reviews/FIX2-*`, `FIX3-*`, `FIX4-*`.

## What changed since the first run

The product allows one live session per account. A login signs out that account's other sessions. The first run therefore had only two accounts and ran testers one after another.

This re-test adds eight tester accounts, so every lane has its own account and all nine lanes run at the same time. Passwords are in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/tester-accounts.txt` (mode 600). `founder` and `admin` keep their original passwords from the runbook.

Workspace ownership is a display label only, so every account sees the "UAT Build" workspace. Approval prompts are scoped to the account whose session raised them (the D-16 and C4 fixes), so parallel lanes cannot see each other's prompts.

## Lanes

| Lane | Agent type | Account | Rows |
|---|---|---|---|
| op | uat-browser-op | founder | B-11, B-19, B-24, B-24b, B-25, B-25e, B-25h, B-26, B-30, B-31, B-34, B-35, B-40, B-40b, B-41, Q-02, Q-03, Q-04, Q-08, Q-13, Q-19, Q-21, Q-23, Q-26, X-05, P-02, P-04 |
| op2 | uat-browser-op2 | uat-op2 | X-02, X-04 |
| x07 | uat-browser-x07 | uat-x1 (browser A), uat-x2 (browser B) | X-07 |
| t1 | uat-browser-t1 | admin | U-05, U-07, U-10, U-51, U-53 |
| t2 | uat-browser-t2 | uat-t2 | U-12, U-12b, U-14, U-16, U-19, U-20, U-21, U-54, U-55, U-56, U-58, U-59, U-60 |
| t3 | uat-browser-t3 | uat-t3 | U-23, U-25, U-28, U-29, U-30, U-31, U-32, U-33, U-63, U-64 |
| t4 | uat-browser-t4 | uat-t4 | U-34, U-35, U-36b, U-38, U-65, U-66, U-67 |
| t5 | uat-browser-t5 | uat-t5 | U-48, U-49, U-50, U-52, U-69, U-70 |

Notes on the lane layout:
- **The op lane stays one lane.** Its rows build on each other (vault edits, then queries over them), so splitting them would create false failures from races.
- **The t2 lane stays one lane** for the same reason: its rename, trash and restore rows touch the same files in sequence.
- **U-52 runs in t5 and U-67 runs in t4**, where the first run reassigned them. They appear twice in the source list and run once.
- **X-07 moves from op to its own lane.** It needs two browsers on two accounts at the same moment.

## Not re-run

| Row | Reason |
|---|---|
| R-03 | CLI and gateway locking is out of scope by founder ruling (D-79, D-83). |
| C-02 | CLI Obsidian import is out of scope by founder ruling (D-03). |

Both rows are reported as N/A with this reason.

## Tester protocol (every lane)

1. **Know the row before touching it.** Read the row's expected outcome in the test plan, the first run's observation in the lane's original report, and the defect ids it confirmed in the defect register.
2. **Run it through the UI only.** Take a screenshot before and after every state change, using absolute paths under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/retest/pw-out/<lane>/`.
3. **Validate before you report.** A PASS needs two agreeing signals:
   - what the screen shows matches the plan's expected outcome;
   - an independent oracle agrees.
   The oracle is either an API read made with `browser_evaluate` fetch from the lane's own logged-in page, or a read-only check of the file on disk. If the two disagree, the row is not a PASS.
4. **Avoid false alarms from other lanes.** Nine lanes share one instance. Use the lane's own scratch folder, `retest-<lane>/`, inside the workspace wherever a row allows. Copy shared fixtures before changing them. If a result could have been caused by another lane's concurrent change, check once more and say so in the report.
5. **Use one verdict per row:**
   - **PASS** — fixed, both signals agree.
   - **FAIL** — the same defect is still there.
   - **NEW DEFECT** — a different problem appeared.
   - **GAP** — a documented limitation; cite it.
   - **BLOCKED** — could not run; give the concrete cause.
   - **N/A** — out of scope by ruling.
6. **Write the report** to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/retest/report-retest-<lane>.md`. Give one table row per UAT row: verdict, the defect ids it confirms fixed or still open, the evidence filenames, and one plain sentence comparing the screen with the oracle.
7. **Finish** with the line `LANE DONE`.

## Rebuild window (orchestrator, before any lane starts)

**Change of order, 2026-09-14 16:40.** The rebuild runs at `dd25339bf` in parallel with that head's CI run, not after it. Reasons:
- **Every UAT-relevant fix is in that head.** All four fix rounds are merged, plus the attachment and folder cascade, the folder restore index refresh and the folder follow-ups.
- **The previous head was checked in full.** CI on `829e26253` passed every Go gate except go-race, which failed on one Copilot sign-in test outside UAT scope. All non-judge browser test groups passed except the expected preview groups.
- **Nothing still open touches a re-test row.** The two open items are the preview isolation tests being rewritten to run inside the frame (test-only) and the Copilot sign-in race under investigation.

If CI on `dd25339bf` shows a regression in code a lane covers, the affected rows are re-run on the fixed build.

1. All fix rounds are merged into `integrate/library-improvements-v0.1.1`. The original gate was CI green on that head; it now runs in parallel, as stated above.
2. Build the SPA, sync it into the Go embed folder, and build the binary from that head as `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/omnipus.<sha>`. Check that a string from the newest SPA change is present in the embedded bundle.
3. Back up `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/home/config.json` to `config.json.pre-retest`.
4. Stop the running UAT gateway by its exact process id. Never signal a process group. Install the new binary.
5. Merge the new accounts from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/retest-users-patch.json` into the gateway user list. Add missing accounts only; never replace `founder` or `admin`.
6. Start the gateway from the same directory with the same environment it had before, on 127.0.0.1:5177. Wait for the state endpoint to answer.
7. Log in once per account through the API to prove every password works, then log out.
8. Run a short isolation probe: two lanes on different screens at the same time, each seeing only its own page.
9. Start all nine lanes together.

## Deliverable

When every lane has written `LANE DONE`, merge the nine reports into `RETEST-REPORT-2026-09-14.md` beside this plan. For each of the 73 rows it states whether the original defect is fixed, still open, or replaced by a new one. It also includes a feature-level summary for the founder. No GitHub issues are filed.
