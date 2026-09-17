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

---

## Parked — do not invent requirements yet

| Topic | Why it is parked | Next step |
|---|---|---|
| Model picker | Depends on the provider (OpenRouter has hundreds; others may have five). Current search is not that bad. | Dedicated interview (next). |
| Knowledge views | A filled vault is not an empty vault. Do not overload users, but do not guess. | Dedicated interview after the model picker. |
| Task / event side panel | Duplicates, “advanced”, and density are not yet listed field by field. | Walk the real panel together before writing rules. |

---

## Rejected or out of scope

| Review claim | Decision |
|---|---|
| There is no way to switch workspaces | **Rejected.** Switching exists in the sidebar workspace list and in slash-command search (`SearchModal` workspace mode). Do not build a new workspace home. |
| Make landing-page marketing proof visible | **Rejected.** Login is login only. |
| Restyle empty states, Settings, or Chat | **Out of scope.** Design-system continuity. |
| Compact/comfortable density as the default | **Out of scope.** Design-system constitution. |
| Bring back JPEG live-browser fallback | **Forbidden.** Existing operator rule. |

---

## Suggested delivery order

1. Empty Board / List / Graph, empty Library, drop Media tab (first-run).
2. Settings URLs + Memory autosave; New Event errors.
3. Login-only signed-out route; session must not drop.
4. Live browser failure + Activity stop / open / return / details.
5. Then the parked interviews: model picker, then Knowledge, then the task panel.

Internal batches are fine. These remain product requirements until each row’s **Done when** is true.

## Evidence

- Findings: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/runtime/visual-audit/findings/`
- Design system: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/design/design-system-definition.md`
- Interview: this file’s Do / Parked / Rejected tables, 2026-09-17.
