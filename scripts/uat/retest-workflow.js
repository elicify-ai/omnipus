export const meta = {
  name: 'uat-retest-2026-09-14',
  description: 'Re-run every failed UAT row in nine parallel self-validating Sonnet tester lanes, independently validate each lane, merge into one feature-level re-test report',
  phases: [
    { title: 'Test', detail: 'nine lanes, one account each, all in parallel', model: 'sonnet' },
    { title: 'Validate', detail: 'one independent validator per finished lane' },
    { title: 'Report', detail: 'merge into RETEST-REPORT-2026-09-14.md' },
  ],
}

const EVID = '/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13'
const RETEST = `${EVID}/retest`
const PLAN = '/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-integrate/docs/internal/uat/knowledge-base-full-uat-plan-2026-09-13.md'
const RPLAN = `${RETEST}/RETEST-PLAN-2026-09-14.md`
const ACCOUNTS = '/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/tester-accounts.txt'
const BASE = 'http://127.0.0.1:5177'
const BUILD_SHA = (args && args.sha) || 'UNKNOWN'

const COMMON = `
Omnipus UAT re-test, 2026-09-14. You re-run rows that failed on 2026-09-13, against a rebuilt instance at ${BASE} (build ${BUILD_SHA}) that contains four rounds of fixes.

READ FIRST, in this order:
1. The re-test plan, including the tester protocol you must follow exactly: ${RPLAN}
2. For each of your rows: the row's expected outcome in the test plan ${PLAN}, the first run's observation, and the defect ids it confirmed in ${EVID}/DEFECTS.md. Sub-rows such as B-25h, U-12b or X-07 are not separate table rows; find them with Bash: grep -rn "<row id>" ${EVID}/report-*.md ${EVID}/DEFECTS.md ${EVID}/UAT-REPORT-2026-09-13.md.
3. The test plan's persona sheet (section 3), Appendix A (selectors), Appendix B (documented limitations) and the pre-logged defects table.

YOUR ACCOUNT: log in through the UI once, at the start. Read your password with Bash: awk '$1=="<username>"{print $2}' ${ACCOUNTS}. Never print it into your report. Never log in a second time: a login signs out every other session of that account. Make oracle API calls with browser_evaluate fetch from your own logged-in page, never with a separate login.

EIGHT OTHER LANES run on the same instance at the same time, each on its own account. Work inside your own scratch folder retest-<lane>/ in the "UAT Build" workspace wherever a row allows. Copy a shared fixture before changing it. If a row's artefact already exists from the first run, create a fresh one with a -retest suffix, and never destroy first-run evidence. If a result could have been caused by another lane's concurrent change, check once more and say so.

SELF-VALIDATION (mandatory before any PASS): the screen must match the plan's expected outcome AND an independent oracle must agree. The oracle is an API read via browser_evaluate fetch, or a read-only disk check with Bash cat or ls under /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/home/workspaces/01M2CXCBZC63H936J1H8EV86XD/work. If they disagree, it is not a PASS. Record both signals in the report row.

VERDICTS: PASS, FAIL (same defect still present), NEW DEFECT, GAP (documented limitation, cite it), BLOCKED (concrete cause), N/A (out of scope by ruling).

REPORT: write ${RETEST}/report-retest-<lane>.md as one table row per UAT row: verdict, defect ids it confirms fixed or still open, evidence filenames, and one plain sentence comparing the screen with the oracle. Screenshot paths are absolute, under ${RETEST}/pw-out/<lane>/. The last line of the file is exactly: LANE DONE

Return the structured result when the report file is complete.
`

const OP_CONTEXT = `
OPERATOR CONTEXT: you drive the Omnipus agent "UAT Builder" (id 0d3bd8e0-432b-4c90-b33f-3c8e09bc0801) through the CHAT UI of workspace "UAT Build" (id 01M2CXCBZC63H936J1H8EV86XD), exactly as the first run did (test plan sections 0.4 and 0.5). The chat endpoint cannot be driven by API: always type in the chat box. After each row, record the agent's tool-call card text and your oracle check. If the agent asks for approval, the modal must appear in YOUR browser; screenshot it and answer as the row requires.
`

const LANES = [
  { key: 'op', agentType: 'uat-browser-op', account: 'founder', prior: 'report-op.md', extra: OP_CONTEXT,
    rows: 'B-11, B-19, B-24, B-24b, B-25, B-25e, B-25h, B-26, B-30, B-31, B-34, B-35, B-40, B-40b, B-41, Q-02, Q-03, Q-04, Q-08, Q-13, Q-19, Q-21, Q-23, Q-26, X-05, P-02, P-04. Also write R-03 and C-02 as N/A with the plan\'s ruling (CLI locking and CLI import are out of scope). Run the B rows before the Q rows, because the queries read what the B rows write.' },
  { key: 'op2', agentType: 'uat-browser-op2', account: 'uat-op2', prior: 'report-op2.md', extra: OP_CONTEXT,
    rows: 'X-02, X-04. The first run noted that X-02 did not clear a known S2 (see X-02b in the prior report); re-check that S2 explicitly. For X-04, judge both the product behaviour and the agent\'s explanation.' },
  { key: 'x07', agentType: 'uat-browser-x07', account: 'uat-x1 (browser A) and uat-x2 (browser B)', prior: 'report-op.md and DEFECTS.md (X-07 was recorded on the coordinator report)',
    extra: `
X-07 CONTEXT (cross-account approval leak, defect D-16, plus review finding C4):
- In browser A, as uat-x1, open workspace "UAT Build", start a new chat with "UAT Builder", and ask it to do something its policy puts behind an approval. Ask it to call request_mount for a new folder under /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/fixtures, or pick any tool the agent's approval prompt is shown for.
- While A's approval modal is open, browser B, logged in as uat-x2 and sitting on the same workspace chat screen, must show NO approval modal. Screenshot both browsers at the same moment.
- Oracle 1: from browser B's page, use browser_evaluate fetch to POST a decision to /api/v1/tool-approvals/<A's approval id> with {"action":"approve"}. Take the id from A's page, the WS frame or a session read in A. It must be refused (403 or 404) and must NOT approve A's request. Confirm in browser A that the modal is still pending.
- Then resolve it from browser A legitimately (deny) and confirm it closes only there.
PASS needs all three: B never sees the modal, B's decision is refused, and A's own decision works.` ,
    rows: 'X-07' },
  { key: 't1', agentType: 'uat-browser-t1', account: 'admin', prior: 'report-t1.md', extra: 'PERSONA: Nadia (test plan section 3).', rows: 'U-05, U-07, U-10, U-51, U-53' },
  { key: 't2', agentType: 'uat-browser-t2', account: 'uat-t2', prior: 'report-t2.md', extra: 'PERSONA: Marek (test plan section 3). Run the rows in the listed order: rename, trash and restore build on each other.', rows: 'U-12, U-12b, U-14, U-16, U-19, U-20, U-21, U-54, U-55, U-56, U-58, U-59, U-60' },
  { key: 't3', agentType: 'uat-browser-t3', account: 'uat-t3', prior: 'report-t3.md and report-t3b.md', extra: 'PERSONA: Priya (test plan section 3).', rows: 'U-23, U-25, U-28, U-29, U-30, U-31, U-32, U-33, U-63, U-64' },
  { key: 't4', agentType: 'uat-browser-t4', account: 'uat-t4', prior: 'report-t4.md', extra: 'PERSONA: Jonas (test plan section 3). U-67 is the founder-copy regression pass reassigned to you in the first run; see RUNBOOK.md in the evidence root.', rows: 'U-34, U-35, U-36b, U-38, U-65, U-66, U-67' },
  { key: 't5', agentType: 'uat-browser-t5', account: 'uat-t5', prior: 'report-t5.md', extra: 'PERSONA: Lena (test plan section 3). U-52 uses the fixture Dashboards/PageProbe.md (written in B-24c).', rows: 'U-48, U-49, U-50, U-52, U-69, U-70' },
]

const LANE_RESULT = {
  type: 'object',
  properties: {
    lane: { type: 'string' },
    report_path: { type: 'string' },
    lane_done_written: { type: 'boolean' },
    rows: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'string' },
          verdict: { type: 'string', enum: ['PASS', 'FAIL', 'NEW DEFECT', 'GAP', 'BLOCKED', 'N/A'] },
          defects: { type: 'string' },
          screen: { type: 'string' },
          oracle: { type: 'string' },
          evidence: { type: 'array', items: { type: 'string' } },
        },
        required: ['id', 'verdict', 'defects', 'screen', 'oracle', 'evidence'],
      },
    },
  },
  required: ['lane', 'report_path', 'lane_done_written', 'rows'],
}

const VALIDATION = {
  type: 'object',
  properties: {
    lane: { type: 'string' },
    checked: { type: 'integer' },
    upheld: { type: 'integer' },
    overturned: {
      type: 'array',
      items: {
        type: 'object',
        properties: { id: { type: 'string' }, claimed: { type: 'string' }, corrected: { type: 'string' }, why: { type: 'string' } },
        required: ['id', 'claimed', 'corrected', 'why'],
      },
    },
    notes: { type: 'string' },
  },
  required: ['lane', 'checked', 'upheld', 'overturned', 'notes'],
}

function laneFor(lane) {
  return `${COMMON}
YOUR LANE: ${lane.key}
YOUR ACCOUNT(S): ${lane.account}
FIRST-RUN REPORT(S) FOR YOUR ROWS: ${EVID}/${lane.prior}
${lane.extra}
YOUR ROWS: ${lane.rows}
YOUR REPORT FILE: ${RETEST}/report-retest-${lane.key}.md
YOUR SCREENSHOT FOLDER: ${RETEST}/pw-out/${lane.key}/ (lane x07 uses ${RETEST}/pw-out/x07a/ and ${RETEST}/pw-out/x07b/)`
}

function validatorFor(lane, result) {
  return `You are an independent VALIDATOR for one UAT re-test lane. You did not run it. Your job is to try to overturn its verdicts with evidence, not to agree.

Lane: ${lane.key}. Its account(s): ${lane.account}. Its report: ${RETEST}/report-retest-${lane.key}.md. Its screenshots: ${RETEST}/pw-out/${lane.key}/ (x07: pw-out/x07a and pw-out/x07b).
The lane's structured result:
${JSON.stringify(result, null, 2)}

For EVERY row:
1. Open the screenshots it cites with the Read tool and look at them. Does the image really show what the "screen" field claims? A PASS whose screenshot shows an error, a spinner, the wrong page, or nothing is overturned.
2. Re-run the oracle yourself for every PASS row, and for any FAIL where you doubt the claim. The lane has finished, so its account is free. Log in with your own cookie jar: username from the lane, password via awk '$1=="<username>"{print $2}' ${ACCOUNTS}. Use curl against ${BASE}; the API helper /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/uat_api.sh shows the cookie and CSRF mechanics. Or read the file on disk under /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/home/workspaces/01M2CXCBZC63H936J1H8EV86XD/work. Read-only: never write, rename or delete anything.
3. Check the verdict against the row's expected outcome in ${PLAN} and the defect in ${EVID}/DEFECTS.md. A PASS that tested something weaker than the row asks for is overturned to FAIL or BLOCKED.
4. For lane x07 on account uat-x1 or uat-x2: log in ONE account at a time, and log out before logging in the other.

Never print passwords. Append a "## Validation" section to the lane's report file listing every overturned verdict and why, then return the structured result.`
}

phase('Test')
const results = await pipeline(
  LANES,
  lane => agent(laneFor(lane), {
    label: `retest:${lane.key}`, phase: 'Test', agentType: lane.agentType, model: 'sonnet', schema: LANE_RESULT,
  }),
  (result, lane) => result
    ? agent(validatorFor(lane, result), { label: `validate:${lane.key}`, phase: 'Validate', agentType: 'general-purpose', schema: VALIDATION })
        .then(v => ({ lane: lane.key, result, validation: v }))
    : ({ lane: lane.key, result: null, validation: null }),
)

const missing = results.filter(r => !r || !r.result).map((r, i) => (r && r.lane) || LANES[i].key)
if (missing.length) log(`lanes with no result: ${missing.join(', ')}`)

phase('Report')
const report = await agent(`Merge the UAT re-test into one founder-facing report.

Inputs:
- The nine lane reports: ${RETEST}/report-retest-*.md (each has a "## Validation" section)
- The structured results plus validations below
- The first run's report ${EVID}/UAT-REPORT-2026-09-13.md
- The defect register ${EVID}/DEFECTS.md
- The re-test plan ${RPLAN}

Structured results:
${JSON.stringify(results, null, 2)}

Lanes with no result: ${JSON.stringify(missing)}

Write ${RETEST}/RETEST-REPORT-2026-09-14.md for a technically literate non-engineer: plain English, short sections, tables.
1. Start with the outcome in three sentences: how much of what was broken is now fixed, what is still broken, what is new. Where a validator overturned a lane verdict, use the validator's verdict.
2. By FEATURE, not by row: search, record editing, views and dashboards, embeds and previews, the Library explorer and file writes, mounts and approvals, agent knowledge tools, security. For each feature: what now works, what still fails, and any new defect, each in one or two sentences, citing row ids only in brackets at the end.
3. A table of every still-open or new defect: what the user experiences, severity, the row ids, and the evidence path.
4. A table of every re-tested row: row, first-run verdict, re-test verdict, defect ids, and whether the validator changed it.
5. Rows not re-run (R-03 and C-02, out of scope) and any lane that produced no result, stated plainly.
Absolute paths only. No GitHub issues. Return the report path and the three-sentence outcome.`,
  { label: 'report', phase: 'Report', agentType: 'general-purpose' })

return { build: BUILD_SHA, missing, results, report }
