# UAT plan — ADR-092 shell permission modes and Auto-approve (2026-09-24)

**Audience:** human testers working only through the Omnipus web UI. The same plan is executed by
six AI tester lanes, each playing one of the personas below — they follow it exactly as a person
would, and may not use the API, the terminal or the database to reach a verdict.

**Build under test:** branch `feat/adr-092-shell-permissions`, including the 2026-09-24 founder
change *"Auto works without a kernel sandbox, shell included"* (lanes `auto/nosb-be`, `auto/nosb-fe`).
The exact commit is recorded in the run header of the defect report.

**Where expected results come from:** every expected result below cites the requirement it comes
from — `FR-xxx` in `docs/internal/specs/adr-092-shell-permission-modes-spec.md`, `D-x` in
`docs/internal/architecture/ADR-092-shell-permission-modes.md`, or a founder ruling with its date.
No expected result was taken from what the build currently does. Where the build disagrees with
the citation, that is a finding, not a reason to change the expectation.

---

## 1. What the feature promises (plain English)

| Idea | What a user should experience |
|---|---|
| **Ask mode** | Every tool set to "Ask" shows an approval card before it runs. |
| **Auto mode** | A tool set to "Ask" runs **without** a card when it is safe: it stays inside the workspace (and inside the sandbox where one exists). A fixed list of 28 risky tools still shows a card every time. |
| **God Mode** | Removes the sandbox for this install. Tools on "Ask" still show a card (Auto is off under God Mode). A red banner says so. |
| **Where Auto is switched** | Global default (Settings → Security → Auto-approve), an agent's own "Never auto-approve for this agent" box (can only turn it off), and a per-chat switch in the message box (can turn it on or off for that one chat). |
| **When a flip takes effect** | New chat: from the first message — the whole first reply already follows the choice. Running chat: from the very next step, even in the middle of a reply (founder rulings 2026-09-24). |
| **No sandbox** | Auto still works, shell included; the chat badge reads "Auto — no sandbox" and warns that shell commands ask first, except read-only ones and commands an operator rule allows (founder ruling 2026-09-24, reworded by founder decision A the same day). |
| **Command rules** | An operator's rule can force a shell command to ask, deny it, or allow it. A command matching an "ask" rule shows exactly **one** card. |
| **Approval buttons** | Approve Once (runs once, remembers nothing), Always Allow (the identical call no longer asks in this chat), Deny (does not run). |
| **Unattended runs** | A scheduled task follows the same Auto rules; anything that would need a human is refused instead of waiting. |
| **Audit** | Every call that Auto let through leaves one `tool.auto_approved` entry in the audit log. |

---

## 2. Test environment

| Item | Value |
|---|---|
| Install | One Fly.io UAT app (`omnipus-uat-swimlane`), fresh data folder, onboarded with `openrouter` + `z-ai/glm-5.3-flash` (founder-set UAT model). |
| Sandbox | **Fly cannot enforce a kernel sandbox**: its guest kernel returns ENOSYS for Landlock (verified 2026-09-24 — `sandbox-status` reports `backend=fallback`, `kernel_sandbox_active=false`). Phases 1–2 on Fly therefore run in the **no-sandbox** state (badge "Auto — no sandbox"). Phase 3 re-runs the sandbox-dependent rows on a local macOS install where Seatbelt enforces. |
| Accounts | `admin` / `admin123` (operator and phase-2 lane) plus six tester accounts `t1`–`t6`, added to the install's config by the operator (there is no account screen). One account per tester: a second login on the same account logs the first one out. |
| Isolation | Each tester works **only in their own workspace and their own agent**. Approval cards are shown per workspace, so another tester's card appears at most as a small "waiting in another workspace" banner — **ignore it, never click it, note if it ever shows a card inside your workspace.** |
| Global settings | Frozen during phase 1: global Auto-approve **ON**, God Mode **OFF**, no global policy edits. Only the phase-2 lane changes global settings, and only after every phase-1 lane has finished. |
| Operator-seeded command rules (config file only — there is no screen for them, D3) | `ask` for `git push`; `deny` for `rm -rf`; `allow` for `ls`. |

### Setup every tester does first (5 minutes)

1. Log in with your tester account.
2. Create a workspace named `UAT-<your id>` (e.g. `UAT-t3`) and switch to it.
3. Create an agent named `Tester-<your id>` with the default settings. **Use only this agent.**
4. Open the agent → Tools & Permissions. Set these tools to **Ask** on your agent (they are "Allow" by default, and Auto only changes tools that are on "Ask", FR-052): `write_file`, `read_file`, `list_directory`, `edit_file`, `append_file`, `send_file`, `create_task`, `list_tasks`, `send_email`, `create_agent`, `serve_web`, `run_doctor`, `set_config` (if listed), `install_skill`, `browser_evaluate`.
5. Take a screenshot of the policy table. Every tool you set to Ask must show an "Auto: …" marker (FR-060).

### How to ask the agent to use a tool

The agent is an AI and may pick a different tool if asked loosely. Always ask precisely, e.g.:
> Use the `write_file` tool to create `notes/t3-a.md` containing the word hello. Use no other tool.

If the agent uses a different tool than asked, **repeat the request once**. If it still does not
use the tool, record the case as **BLOCKED (agent did not call the tool)** — never as PASS or FAIL.

### Evidence rules (non-negotiable)

- A screenshot for **every** step whose expected result is visible, named `<case-id>-<step>.png`.
- **PASS needs every part of the expected result.** "No card appeared" is only evidence if the tool
  card in the chat also shows the tool ran with the arguments you asked for.
- Something that changes after an action must be judged on a screenshot taken **before** any undo.
- Wait at least 30 seconds before concluding "no card appeared".
- Record the exact text of every approval card (headline, note, buttons).

---

## 3. Personas and lanes

| Lane | Persona | Area | Phase |
|---|---|---|---|
| t1 | **Nadia**, first-time owner. Reads every word, does not know the jargon, notices confusing copy. | Security card, badge, markers, wording | 1 |
| t2 | **Marco**, hurried power user. Flips switches mid-flight, reloads pages, opens new chats constantly. | Per-chat switch and its timing | 1 |
| t3 | **Priya**, analyst who works with files all day. Tries paths she should not reach, sometimes by accident. | File tools and the workspace rule | 1 |
| t4 | **Tomasz**, cautious admin. Tests every button on every card, and the always-ask list. | Approval cards, grants, always-ask tools, agent off-switch | 1 |
| t5 | **Sam**, developer. Lives in the shell, chains commands, tries clever ones. | Shell under Auto, command rules, network | 1 |
| t6 | **Ines**, operations and auditing. Schedules work overnight and reads the audit log. | Scheduled runs, audit log, delegation | 1 |
| admin | **Operator** (phase 2 re-uses the t4 persona) | Global settings, God Mode, no-sandbox | 2 and 3 |

---

## 4. Test cases

Legend — **Neg** = negative or edge case. Unless stated, the chat's Auto state is inherited from the
global default (ON) and the tool named is on "Ask" for your agent.

### Lane t1 — Nadia: can a newcomer understand it?

| ID | Steps | Expected result | Source | Neg |
|---|---|---|---|---|
| T1-01 | Settings → Security. Find Auto-approve. | One short summary sentence, a switch, a collapsed "Still asks every time" list, and a small caveat line about running without a sandbox. No wall of text. | founder ruling 2026-09-24 (redesign) | |
| T1-02 | Expand "Still asks every time". | 8 groups, one per line, plain words, no raw tool names. Collapses again. | FR-053; founder list | |
| T1-03 | Read the list against step 4 of setup: on a fresh install `send_email`, `run_doctor`, `install_skill` are on **Allow**. Does "Still asks every time" tell the truth for these? Judge as a newcomer. | The heading must not promise a card for a tool that never asks. If it does, record a **copy defect** with the exact wording. | FR-052 (Auto only acts on "Ask") | Neg |
| T1-04 | Read the caveat line. | Says in plain words that without a kernel sandbox shell commands ask first, except read-only ones and commands an operator rule allows. No mention of Auto being unavailable on Windows. | founder ruling 2026-09-24 (rewording, founder decision A) | |
| T1-05 | In your agent's policy table, look at the markers next to `write_file`, `create_task`, `send_email`, and `bash`. | `write_file`: "Auto: runs inside workspace". `create_task`: "Auto: runs". `send_email`: "Auto: asks". `bash`: no marker. Tools on Allow/Deny: no marker. | FR-060, D9 | |
| T1-06 | Chat header badge, sandbox enforcing. | Reads **Auto**. Hover: explains what Auto does. | FR-001 | |
| T1-07 | Composer: hover the Auto switch in a new chat, then in a running chat. | New chat: "…applies from your first message". Running chat: "…applies from the next step". | founder rulings 2026-09-24 | |
| T1-08 | Read an approval card for `delete_task` (ask the agent to delete a task you created). | Plain-English headline; the tool and its arguments visible; buttons Approve Once, Always Allow, Deny; a countdown. | FR-023 | |
| T1-09 | Look for any emoji, jargon ("TOCTOU", "Landlock", "seccomp", "FR-") or raw config keys in the Security card, badge, tooltips and cards you saw. | None. | brand guidelines; CLAUDE.md language rule | Neg |
| T1-10 | During onboarding (operator run) or Settings → Providers, type an API key. | The key is masked (dots), with a show/hide control. | security practice; FR-023-era UI rules | Neg |

### Lane t2 — Marco: the per-chat switch

| ID | Steps | Expected result | Source | Neg |
|---|---|---|---|---|
| T2-01 | New chat. Before typing, try to flip the Auto switch. | The switch is clickable and flips immediately. | founder ruling 2026-09-24 | |
| T2-02 | New chat. Flip Auto **OFF**. First message: "Use write_file to create notes/t2-a.md with hello." | A card appears for `write_file` — the chat's OFF applies to the very first reply. | founder ruling; FR-002 | Neg |
| T2-03 | Same chat, Deny the card. | File not created; the agent reports it was refused. | FR-023 | Neg |
| T2-04 | New chat, leave Auto **ON**. First message as T2-02 with `t2-b.md`. | No card; tool card shows `write_file` ran and "File written". | FR-052 | |
| T2-05 | Running chat with Auto ON. Flip it OFF. Next message: create `t2-c.md`. | A card appears. | founder ruling (next step) | |
| T2-06 | Mid-reply flip. Ask: "Create notes/t2-1.md, then wait for my next instruction, then create t2-2.md, t2-3.md, t2-4.md, one write_file call each, pausing briefly between them." with Auto OFF; approve the first card; **while the agent is still working**, flip Auto ON. | Calls after the flip run without cards; calls before it asked. | founder ruling 2026-09-24 (mid-reply) | |
| T2-07 | New chat. Flip Auto OFF, **send nothing**, open a different new chat. | The other chat shows the global default (ON), not the abandoned choice. | pending-choice rule, founder 2026-09-24 | Neg |
| T2-08 | In a chat where you set Auto OFF, reload the page. | The switch still shows OFF for that chat. | FR-004 (session-scoped modifier) | Neg |
| T2-09 | Flip the switch rapidly five times, ending ON, then send a write request. | Ends ON; no card; no error toast. | FR-002 | Neg |
| T2-10 | Switch agent in the chat header to a different agent. | Badge and switch reflect the new agent's resolved state. | FR-002 | |
| T2-11 | Check the header badge after every flip in T2-01…T2-09. | Badge always matches the switch. | FR-001 | |

### Lane t3 — Priya: files and the workspace rule

| ID | Steps | Expected result | Source | Neg |
|---|---|---|---|---|
| T3-01 | `write_file notes/t3-a.md` (inside the workspace). | No card; file written. | FR-054 | |
| T3-02 | `read_file notes/t3-a.md`. | No card; content returned. | FR-054 | |
| T3-03 | `write_file /tmp/t3-out.md`. | A card appears (outside the workspace). | FR-054 | Neg |
| T3-04 | `write_file ../t3-escape.md`. | A card appears (the path resolves outside). | FR-054 | Neg |
| T3-05 | `write_file notes/../../t3-escape2.md`. | A card appears. | FR-054 | Neg |
| T3-06 | `read_file /etc/hosts`. | A card appears (read outside). | FR-054, D9 | Neg |
| T3-07 | Ask the agent (bash): `ln -s /etc notes/etc-link`. Then `read_file notes/etc-link/hosts`. | The read is refused or asks — never silently runs through the link. | FR-055 (re-check real path) | Neg |
| T3-08 | `list_directory notes`. | No card. | FR-054 | |
| T3-09 | `list_directory /`. | A card appears. | FR-054 | Neg |
| T3-10 | `edit_file notes/t3-a.md` replacing hello with bye; then `append_file` a line. | No card for either; file changed. | FR-054 | |
| T3-11 | `send_file notes/t3-a.md` to yourself in chat. | No card. | FR-053 (RUNS-IF) | |
| T3-12 | `send_file /etc/hosts`. | A card appears. | FR-053 | Neg |
| T3-13 | Ask the agent to read Omnipus's own `credentials.json` or `master.key` by absolute path, and **approve** any card. | Refused outright — never readable, even after approval. | FR-037 | Neg |
| T3-14 | Ask the agent to request a mounted folder (`request_mount`). | A card appears (always-ask tool). | FR-053 | |
| T3-15 | `write_file` with an empty path, and with a 300-character file name. | A clear error or a card — never a silent write elsewhere. | FR-054 | Neg |

### Lane t4 — Tomasz: cards, grants and the always-ask list

| ID | Steps | Expected result | Source | Neg |
|---|---|---|---|---|
| T4-01 | Create a task (`create_task`). | No card (runs). | FR-053 | |
| T4-02 | Delete that task (`delete_task`). | A card, every time. | FR-053 | |
| T4-03 | Ask for `send_email` to a made-up address. Deny. | Card appears; Deny → nothing sent, agent told. | FR-053, FR-023 | Neg |
| T4-04 | Ask for `create_agent` named t4-tmp. | Card appears. | FR-053 | |
| T4-05 | Ask for `serve_web` of a one-line page. | Card appears. | FR-053 | |
| T4-06 | Ask for `run_doctor`. | Card appears. | FR-053 | |
| T4-07 | Ask for `browser_evaluate` on any page. | Card appears. | FR-053 | |
| T4-08 | Repeat T4-02 with a new task: **Approve Once**. Then an identical delete of another identical request in the same chat. | First runs; the next identical call asks again. | FR-058 | Neg |
| T4-09 | `delete_task` a task: **Always Allow**. Create the identical task again and repeat the identical delete in the same chat. | The second identical call runs **without** a card. | FR-058 | |
| T4-10 | Same identical call as T4-09 in a **new chat**. | A card appears (grants are per chat). | FR-017, FR-058 | Neg |
| T4-11 | Same tool, different arguments, in the T4-09 chat. | A card appears (grant is exact-arguments). | FR-058 | Neg |
| T4-12 | Let a card time out without clicking. | The call is refused when the countdown ends; the agent is told. | FR-023 | Neg |
| T4-13 | Agent → Tools & Permissions → tick "Never auto-approve for this agent". New chat, Auto switch ON, `write_file notes/t4.md`. | **No card** — the chat switch may turn Auto on for its own chat even when the agent has it off (a person is present). The agent box only wins for a delegated agent (T6-08). | FR-002 (corrected 2026-09-23); docs/security.md | Neg |
| T4-14 | Untick it again; repeat in a new chat. | No card. | FR-002 | |
| T4-15 | Look for a list of your "Always Allow" grants anywhere in the UI. | There is none (by design; the audit log is the record). | FR-028 | |

### Lane t5 — Sam: the shell

| ID | Steps | Expected result | Source | Neg |
|---|---|---|---|---|
| T5-01 | bash `echo hello`. | No card; output hello. | D2 Auto | |
| T5-02 | bash `touch notes/t5.txt`. | No card; file exists. | D2 Auto | |
| T5-03 | bash `touch /etc/t5-probe`. | A card explaining the command wants to reach outside the workspace. Deny → refused, not retried. | FR-009, FR-048 | Neg |
| T5-04 | bash `curl -sI https://example.com`. | A card explaining the command wants network access. Approve → works. | FR-042, FR-043 | Neg |
| T5-05 | Repeat T5-04 but Deny. | Refused; no retry without network. | FR-048 | Neg |
| T5-06 | bash `echo a && curl -sI https://example.com`. | A card (one segment needs network); nothing runs before approval. | FR-020 | Neg |
| T5-07 | bash `git push` (in any folder). | **Exactly one** card; headline says an operator command rule requires approval; a note names the rule. | FR-061 | |
| T5-08 | Always Allow on T5-07's card; repeat the identical `git push`. | No second card. | FR-058, FR-061 | |
| T5-09 | bash `rm -rf notes/tmp-dir` (create it first). | Refused with no card (deny rule), even under Auto. | FR-019 | Neg |
| T5-10 | bash `ls`. | No card. | FR-019 (allow rule) | |
| T5-11 | bash `eval "echo hi"` and `$(printf 'ec''ho') hi`. | A card (the checker cannot read what it runs). | FR-020 (blind spots ask) | Neg |
| T5-12 | bash `echo aW5zaWRl | base64 -d > notes/t5-b64.txt`. | No card (stays inside). | D2 Auto | |
| T5-13 | bash `cat /etc/hosts`. | Runs (reading is not writing) — confirm and record. | D7 | |
| T5-14 | Approve Once on a `curl` card, then run the same `curl` again. | Asks again. | FR-058 | Neg |
| T5-15 | Always Allow with the **prefix** scope for `npm run test` (if offered); then run `npm run testfoo`. | `npm run testfoo` still asks (token boundary). | FR-024 | Neg |

### Lane t6 — Ines: unattended runs, audit, delegation

| ID | Steps | Expected result | Source | Neg |
|---|---|---|---|---|
| T6-01 | Calendar → schedule a one-off task in 3 minutes: "Use write_file to create notes/t6-sched.md with hello." Wait for it. | Runs; file exists; no card needed. | FR-057 | |
| T6-02 | Schedule: "Use delete_task to delete task X." | Refused (needs a human); no card shown to anyone; the run record says why. | FR-057 | Neg |
| T6-03 | Schedule: "Use bash to run git push." | Refused once (rule needs a human); no card. | FR-057, FR-061 | Neg |
| T6-04 | Schedule: "Use bash to run touch /etc/t6." | Refused; no card. | FR-057 | Neg |
| T6-05 | Settings → Security → Audit log. Find the entries for T6-01 and for any chat write you did. | One `tool.auto_approved` entry per auto-run call with tool, class, reason, and whether a kernel sandbox was enforcing. | FR-059; founder 2026-09-24 | |
| T6-06 | Find your Deny and Approve decisions. | Each decision is logged. | FR-032 | |
| T6-07 | Count entries for a single auto-run call. | Exactly one. | FR-059 | Neg |
| T6-08 | Create a second agent `t6-helper` and tick its "Never auto-approve". From your main agent (Auto ON) ask it to delegate "write notes/t6-deleg.md" to t6-helper. | The delegate's call asks (the tighter setting wins). | FR-005 | Neg |
| T6-09 | Untick the helper's box and repeat. | No card. | FR-005 | |

### Phase 2 — Operator (admin): global settings, one lane, after phase 1

| ID | Steps | Expected result | Source | Neg |
|---|---|---|---|---|
| G-01 | Security → turn global Auto-approve **OFF**. | Asks for your password before saving. | FR-045 | |
| G-02 | Enter a wrong password. | Refused; setting unchanged. | FR-045 | Neg |
| G-03 | With global OFF: new chat, `write_file` inside workspace. | A card. | FR-002 | |
| G-04 | With global OFF: new chat, flip the chat switch ON, same request. | No card (a chat may loosen for itself). | FR-002 | |
| G-05 | Turn global Auto back **ON**: a confirmation dialog appears. Read it. | Short, consistent with the card; password required. | FR-045; founder 2026-09-24 | |
| G-06 | Turn **God Mode** on (Settings → Gateway). | Password step-up; red banner; badge "God Mode". | FR-034 | |
| G-07 | Under God Mode: `write_file` inside workspace (on Ask **on your agent**). | A card — an agent's own Ask survives God Mode, and Auto is off under God Mode. | FR-051; compositor God Mode contract | Neg |
| G-07b | Under God Mode: ask for `delete_task` (on Ask in the **global** policies by default, not on your agent). | No card — God Mode lifts global Ask to Allow. | `pkg/tools/compositor.go` God Mode contract (global ask → allow; agent ask stays) | Neg |
| G-08 | Under God Mode: bash `rm -rf notes/x`. | Still refused by the deny rule. | GodModeControl / D3 | Neg |
| G-09 | Turn God Mode off. Banner gone; badge back to Auto. | As stated. | FR-034 | |
| G-10 | Global policy: set `create_task` to **Allow** at the global level for a moment — does the marker on agents without an override disappear? Revert. | Marker only on Ask tools. | FR-060 | |

### Phase 3 — Operator: compare with and without a kernel sandbox

N-01…N-08 (including N-04b) are read on **Fly** (no sandbox). Then the operator repeats T1-06, T5-01, T5-03, T5-04 and N-03 on a **local macOS install** (Seatbelt enforcing): the badge must read "Auto" (not "Auto — no sandbox"), and the audit entry must show a kernel sandbox was enforcing.

| ID | Steps | Expected result | Source | Neg |
|---|---|---|---|---|
| N-01 | Chat header badge with Auto ON. | "Auto — no sandbox"; tooltip reads "No kernel sandbox is enforcing. Safe tool calls still run without asking; shell commands ask first, except read-only ones and commands an operator rule allows." | founder 2026-09-24 (rewording, founder decision A) | |
| N-02 | Security card. | The caveat line shows as a caution and reads "Without a kernel sandbox (for example on Windows), shell commands ask first, except read-only ones and commands an operator rule allows." | founder 2026-09-24 (rewording, founder decision A) | |
| N-03 | `write_file notes/n1.md`. | No card. | founder 2026-09-24; FR-054 | |
| N-04 | bash `echo hi`. | No card — `echo` is on the no-sandbox read-only allowlist. | founder 2026-09-24, founder decision A | |
| N-04b | bash `touch notes/x`. | A card — a write via a relative path is neither read-only nor on an operator allow rule, so founder decision A's no-sandbox gate asks (`pkg/tools/shell_no_sandbox_gate.go`); this did NOT ask before founder decision A, since D7's classifier is absolute-path-only. | founder 2026-09-24, founder decision A | Neg |
| N-05 | bash `touch /etc/n-probe`. | A card (the text check still catches it). | founder 2026-09-24; FR-009 | Neg |
| N-06 | `delete_task`. | A card (always-ask list unchanged). | FR-053 | Neg |
| N-07 | Audit log entry for N-03. | Shows that no kernel sandbox was enforcing. | founder 2026-09-24 | |
| N-08 | Security health page. | Record what it says about the sandbox (known issue #853 — note only). | issue #853 | |

**Totals:** 95 cases — 52 positive, 43 negative/edge (45%).

---

## 5. Known gaps (deliberately not covered here)

| Gap | Why |
|---|---|
| MCP server tools (FR-056) | Needs a connected MCP server with annotated tools; no UI-only way to provide one on a fresh install. Covered by unit tests; add a UAT row once a test server is available. |
| Windows | No Windows UAT host. The no-sandbox phase stands in for the Windows behaviour. |
| Linux vs macOS sandbox differences | Fly runs Linux only. macOS was hand-checked on 2026-09-24 (Seatbelt enforcing). |
| Disguised shell commands that evade the text check | Accepted risk by founder ruling 2026-09-24 when no sandbox is enforcing; T5-11 checks that the obvious forms still ask. |

---

## 6. Defect report format

Each lane appends to its own report; the operator merges them into one register.

| Field | Content |
|---|---|
| ID | `D-<lane>-<n>` |
| Case | e.g. T3-07 |
| Severity | **S1** security bypass (something ran that must ask or be refused) · **S2** feature broken · **S3** wrong or confusing behaviour/copy · **S4** cosmetic |
| Expected | The expected result and its source (FR/D/ruling) |
| Actual | What happened, card text verbatim |
| Evidence | Absolute screenshot paths |
| Repeatable | Yes / No / Once-of-N |

Case verdicts: **PASS** (every part of the expected result observed), **FAIL**, **BLOCKED** (the
test could not be carried out — say why), **NOTE** (observation outside the expected result).
A lane's verdicts are claims; the operator re-checks every FAIL and at least three PASSes per lane
against the screenshots before they enter the report.
