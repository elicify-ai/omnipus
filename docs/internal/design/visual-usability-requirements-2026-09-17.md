# Visual usability requirements

**Status:** Founder-aligned 2026-09-17. Not implemented.  
**Source:** Visual audit lanes V1–V5 and V4b, then a structured interview.  
**Constraint:** Changes must obey `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/design-system-definition.md`. Same look. Shared parts (`Button`, `Field`, `EmptyState`, error treatment, autosave status). No restyle.

This document is **product behaviour**. It is not the design-system migration. Empty Library and a first-task button are not solved by tokens.

## How to read this

- **Do** — we agreed this is required.
- **Parked** — we will interview it before writing rules.
- **Rejected** — the review was wrong, or it is out of product scope.

Anything not listed is not a requirement.

---

## Do

### 1. Empty Board, List, and Graph — one pattern

**Context.** Calendar already explains the empty state and offers a next step. Board, List, and Graph often do not. All three show the same tasks.

**Requirement.** When a workspace has no tasks, each of those three tabs shows:

1. A short line of what this view is for.
2. One gold **Create first task** action.

Same pattern on all three. Not three different inventions. Copy and the button use the shared empty-state and `Button` parts. Layout, colours, and type stay as they are.

**Done when.** A new workspace with no tasks shows that copy and that action on Board, List, and Graph. Calendar is unchanged if it already meets this bar.

### 2. Empty Library — add files, mount nearby

**Context.** Empty Library was the worst dead end. Testers could land there with nothing to click. A vault is knowledge. Library is files in the workspace.

**Requirement.** An empty Library shows:

- Purpose copy (this is where workspace files live).
- Primary gold action: **Upload or create a file**.
- Secondary, quieter action: **Mount a vault**.

Two jobs, one gold button. Do not compete with two gold buttons.

**Done when.** An empty Library is not a dead end. Both actions are visible and work. No new Library visual language.

### 3. Media is not a separate product surface

**Context.** Library and Media felt like two names for one place. Founder: they are the same; media is a folder in the workspace.

**Requirement.** Remove the workspace **Media** tab. Files, including media, live in Library. Media may appear as a folder or filter inside Library, not as a second top-level tab.

**Done when.** Workspace chrome has no Media tab. Users find media files in Library. Existing files are not lost.

### 4. Login is only sign-in

**Context.** The review treated a “landing page” with feature and security proof. Founder: there is no marketing landing page. Signed-out is login.

**Requirement.** The signed-out route is a sign-in form (and the errors it needs). No feature list, no security essay, no extra proof to scroll to. If extra marketing blocks sit on that route today, they come off. We do not “make the proof visible.”

**Done when.** A signed-out user sees login, not a marketing page.

### 5. Settings tabs are real links

**Context.** Settings tabs are not in the URL. Refresh and Back dump you on the first tab. Design-system D16 already requires this for navigational tabs.

**Requirement.** Each Settings tab has a URL. Refresh, Back, Forward, and a shared link open the same tab.

**Done when.** Changing tab changes the URL. Opening that URL shows that tab.

### 6. Settings autosave everywhere, including Memory

**Context.** Most Settings pages already autosave and show Saving / Saved. Memory still has a Save button, and that button was clipped off screen.

**Requirement.** Memory autosaves like the other tabs. Remove the Memory Save button. Every Settings change writes itself and shows **Saving**, **Saved**, or an error, using the existing autosave status treatment. Do not add extra Save buttons.

**Done when.** Memory has no Save button. Changing Memory persists without a click. Status is visible. Nothing is clipped off screen.

### 7. New Event — no errors on a blank form

**Context.** Opening New Event showed validation errors before the user typed. Field rules: explain the problem after the person has tried.

**Requirement.** A blank New Event form is quiet. Errors appear after submit, or after the user leaves a field they filled wrongly.

**Done when.** Opening New Event shows no red errors. Wrong input is explained only after an attempt.

### 8. Live browser — honest failure

**Context.** The in-chat live view can fail. A silent JPEG fake was banned because it hid the failure.

**Requirement.** When live view breaks: show the real reason, **Retry**, and **Stop**. Never a blank panel. Never a silent fake video.

**Done when.** A forced live-view failure shows reason + Retry + Stop. JPEG screencast does not return.

### 9. Session stays until the user signs out

**Context.** Testers were kicked to login mid-flow. Founder: this is a product bug, not only a test-harness issue.

**Requirement.** An authenticated session stays until the user signs out (or a real auth failure: expired credentials, explicit revoke). Ordinary use — open a sheet, switch tabs, save settings, switch workspace — must not dump the user on login.

**Done when.** A single user in one browser can move around the app without an unexpected login screen. Unexpected login is a defect.

### 10. Activity panel — stop, open, return, details

**Context.** Activity is for background work. Some rows already expand. Session search and the sidebar already help with sessions.

**Requirement.**

- Every running background item has a **Stop** control.
- **Subagent** activity: open that subagent’s session in chat in one click, and from that session return to the parent session in one click (reuse sidebar / session search if they already do this; add only what is missing).
- **Non-agent** background activity: an expand control for more details if those details exist.

**Done when.** A user can stop background work, jump to a subagent and back to the parent, and open details on non-agent work, without a new panel design.

### 11. Model picker — one control, search only when long, no Recommended chip

**Context.** The shared `ModelSelector` already searches, groups by provider/vendor, and can virtualise long lists. OpenRouter may list hundreds of models; another provider may list five. The review asked for a friendlier catalog. Founder: keep that picker; do not rebuild it. Drop the “Recommended for chat” chip. Search is extra, only when the list is long.

**Requirement.**

- Every model chooser (chat, agent create/edit, Settings) uses the same `ModelSelector` behaviour. No chat-only snowflake.
- The control is always the same. **Search appears when there are more than 10 models** in the list being shown. At 10 or fewer, it is a grouped list without a search field.
- Groups by provider/vendor stay.
- Each row shows a **readable name** when the catalog has one, and the **slug underneath** (or beside, smaller) so power users still see `anthropic/claude-sonnet-4.6`.
- Remove the **Recommended for chat** chip. Do not auto-select a model.
- Typing a slug that is not in the catalog stays allowed, with a **clear warning** that it is not in the catalog. Do not present it as a normal catalog pick.

**Done when.** A 5-model provider has no search field. An OpenRouter-sized list has search. No Recommended chip. Readable name + slug on catalog rows. Unknown slug warns. Chat, wizard, and Settings match.

### 12. Agent picker — search when more than 10

**Context.** The chat agent picker is a plain dropdown with no search. Team → Add agent already searches. Other dropdowns in the app switch to search at 5 items (`SmartSelect`). Founder: one rule for every agent list, search when more than 10.

**Requirement.**

- Every agent chooser (chat composer, Team add, and any other agent list) follows the same rule.
- **10 or fewer agents:** simple dropdown, no search field.
- **More than 10 agents:** search field, same look as other searchable selects.
- Team add may keep search if it already has it when the list is long; when the list is 10 or fewer it must match this rule (no extra search field).
- Workers stay out of the chat picker (existing rule). This requirement does not change who is on the list, only how you find them.

**Done when.** A workspace with 8 chat agents has no agent search. A workspace with 12 has search in chat and in every other agent list. No new picker visual language.

### 13. Task detail panel — one heading each, autosave, shorter first view

**Context.** The panel is a long scroll. Title and Goal have Save / Cancel while everything else autosaves. Acceptance criteria and Definition of Done are **two different required lists**, not duplicates. The review saw DoD three times because the panel, the editor, and the results block each print the same heading.

Checked in code: create already requires Title, Goal, at least one acceptance criterion, and at least one Definition of Done item. Agent instruction fields elsewhere already autosave.

**Requirement.**

- **Autosave Title and Goal.** Click to edit, it saves. Escape cancels. No Save button. Same pattern as other instruction fields and Settings.
- **Keep both lists, both required.** Acceptance criteria = checks while work runs. Definition of Done = standing gates before we call it finished.
- **One heading each.** Pass/fail (when judged) sits on the row. Do not render a second “Definition of Done” results section or a second editor label.
- **First view, in this order:** Title; Goal *; Agent; Status and Due; Acceptance criteria * (compact list); Definition of Done * (compact list); then **More** for Priority, Plan, Tags, Trigger, Depends on, Artifacts, Sub-tasks, and run history.
- Start / Stop / Delete stay at the bottom. Delete stays a quiet danger action.
- Creating a **new task** still requires the same four: Title, Goal, at least one criterion, at least one DoD item. Errors wait until the user tries (same Field rule as New Event). The create form may keep those fields visible; we are not hiding required lists on create.

**Done when.** Opening a task shows the first view above without duplicate headings. Title and Goal have no Save button and still persist. Both lists are editable and required. More reveals the rest. Start still refuses an incomplete task.

### 14. New Event form — keep the fields; delay errors

**Context.** New Event already has its own requirement (item 7) for no red errors on a blank form. The form today is Title, Agent, Instruction, Acceptance criteria, Definition of Done, date and time, repeat — more than a simple calendar dialog. Founder: keep all those fields visible; only delay the errors. Do not force the task-panel first-view / More pattern onto events.

**Requirement.** All current New Event / Edit event fields stay on one scroll. Red errors appear only after the user tries (submit or leaving a filled field). Apply the same one-heading-each rule for the two lists (no stacked “Definition of Done” labels). Repeat stays a UI control, never raw cron.

**Done when.** Opening New Event shows no red errors. Every field above is still visible. DoD and criteria each have one heading.

---

## Parked — do not invent requirements yet

| Topic | Why it is parked | Next step |
|---|---|---|
| Knowledge views | A filled vault is not an empty vault. Do not overload users, but do not guess. | Dedicated interview next. |

---

## Rejected or out of scope

| Review claim | Decision |
|---|---|
| There is no way to switch workspaces | **Rejected.** Switching exists in the sidebar workspace list and in slash-command search (`SearchModal` workspace mode). Do not build a new workspace home. |
| Make landing-page marketing proof visible | **Rejected.** Login is login only. |
| Restyle empty states, Settings, or Chat | **Out of scope.** Design-system continuity. |
| Compact/comfortable density as the default | **Out of scope.** Design-system constitution. |
| Bring back JPEG live-browser fallback | **Forbidden.** Existing operator rule. |
| “Recommended for chat” model chips | **Dropped.** Search, grouping, and readable names are enough. |

---

## Suggested delivery order

1. Empty Board / List / Graph, empty Library, drop Media tab (first-run).
2. Settings URLs + Memory autosave; New Event errors.
3. Login-only signed-out route; session must not drop.
4. Live browser failure + Activity stop / open / return / details.
5. Model picker + agent-list search (items 11–12).
6. Task detail first view + autosave Goal (item 13); New Event headings (item 14).
7. Then the parked interview: Knowledge views.

Internal batches are fine. These remain product requirements until each row’s **Done when** is true.

## Evidence

- Findings: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/runtime/visual-audit/findings/`
- Design system: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/design-system-definition.md`
- Interview: this file’s Do / Parked / Rejected tables, 2026-09-17.
