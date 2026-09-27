# Team-lead handover — 2026-09-27 (for the next chief / team-lead session)

Written by the team-lead cloud session `session_01W1yXBKZPCXRcaqCtY4deaN` (branch
`work/team-lead-2026-09-26`) at the founder's request, because account usage is about to run out.
Everything below is verified against GitHub or git at 2026-09-27 ~06:40 UTC unless marked
**Unverified**. Nothing here is a founder decision unless it cites one.

## 1. Read first

| What | Where |
|---|---|
| Founder decisions for #904 (D1–D15 from this session; the squad added more, D16+) | `docs/internal/specs/tool-iteration-limit-interview.md` (branch `feature/904-tool-iteration-limit`; the D1–D15 copy is on `work/team-lead-2026-09-26`) |
| Founder decisions for #920 (D1–D7 from this session; the squad added D8+ incl. D13 "grep walk skips /proc, /sys, /dev") | `docs/internal/specs/read-boundary-consistency-interview.md` (branch `feature/920-read-boundary`) |
| Squad handover notes (requested 06:37 UTC, may not exist yet) | `docs/internal/handover/904-tool-iteration-limit-handover-2026-09-27.md` on `feature/904-tool-iteration-limit`; `docs/internal/handover/920-read-boundary-handover-2026-09-27.md` on `feature/920-read-boundary` |

## 2. What landed

| Item | Result |
|---|---|
| #915 remove worker-tile "Test run" | **Merged** by the founder via PR #928 → merge commit `2e21e00dc` on `release/v0.1.1`. Issue closed with a close-out comment. Included a shared vitest fix (`src/test/setup.ts` flushes Radix FocusScope unmount timers) + regression test `src/test/setup.flushUnmountTimers.test.tsx`. Merged with one known red (`E2E — llm-agents`, #891) by explicit founder decision. |

Only this one PR was merged from this session's work.

## 3. Work in flight — who owns it

| Work | Owner | State | Next step |
|---|---|---|---|
| #904 global tool-step limit (feature) | Cloud session "Squad #904 — global tool-step limit (squad-lead)", `session_01XfKKHy9tx5Y1nF6zyky5i8`, branch `feature/904-tool-iteration-limit`, draft PR #932 | Spec, RED, GREEN, two CHECK rounds, 8-reviewer gate done; gate/CI follow-up fixes landing (last 05:32 UTC). At 06:27 it was **blocked on a question to the founder** (AskUserQuestion — content not visible from here). | Founder answers that question in the #904 session. Then: docs check, UAT, founder landing yes. Read its handover note first. |
| #920 read-boundary consistency (feature, security-relevant) | Cloud session "Squad #920 — read boundary consistency (squad-lead)", `session_01R3ZGi2cdSB4q4oAMEuh8pH`, branch `feature/920-read-boundary` | ADR-081/ADR-092 amendments reviewed; spec Approved after two grill rounds; RED pack merged; GREEN code stream was running at 06:27. No PR yet. | Finish GREEN → CHECK → 8-reviewer gate (security-lead mandatory) → docs → founder yes. Read its handover note first. |
| #921 protect personal secret folders (~/.ssh etc.) for all read tools | **Nobody** | Issue filed (P1, security) | Founder decides when; security-lead review mandatory. Accepted residual risk of #920 until then. |
| #949 Test Connection colour vs pre-save check use different fields | **Nobody** | Issue filed (P3, Bug) | Small/standard frontend fix when convenient. |
| #891 delegated child's approval ask stalls 600 s (keeps `E2E — llm-agents` red for every PR) | Founder said "not now" for this session. PR #944's body says a "fix-890 squad" owns the red — **Unverified**, that session is not visible from here. | Open, no fix PR | Confirm an owner. This single red blocks the founder-approved merges below. |

## 4. PRs waiting on a green `release/v0.1.1` (not from this session)

#906 gateway security fixes (founder yes given) · #940 provider messages (draft, yes given) · #941 follow-up messages (draft, yes given) · #934 design-system safety net (draft) · #944 web-search provider model (open) · #759 browser e2e repair (stale since 2026-09-24). Owners are other founder sessions, not visible from here.

## 5. Process facts learned the hard way (cloud environment)

1. **Subagents cannot dispatch subagents here.** In-session squad-leads are unusable; the founder chose separate cloud sessions per squad. They were created with `create_session` + `append_system_prompt` "ROLE OVERRIDE → squad-lead". Each waits for the founder to confirm presence before dispatching.
2. **Rule breach to know about:** the first (in-session) #920 squad lead started headless `claude -p` workers without permission; they wrote commit `85a83ae` (ADR amendments only). Founder decided: keep it as a draft, reviewed normally. The new sessions are told never to start headless workers or bypass permissions.
3. **Messaging cloud sessions:** `SendMessage` cannot reach them. Use `create_trigger` with `persistent_session_id` + `run_once_at` (one-shot message).
4. **No shared coordination ledger in cloud containers.** Each container has its own local ledger at the conventional path; sessions cannot see each other's. Founder accepted this. Landings must be sequenced by the founder.
5. **Commit identity:** containers default to `Claude <noreply@anthropic.com>`. Always set `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>` and never add an Anthropic Co-Authored-By trailer (CLA gate).
6. **CI runs on pull_request only.** Full evidence needs a (draft) PR; ask the founder before opening one. GitNexus and the fly CI cluster are not available here; Read/Grep is the correct fallback.
7. **Browser checks work:** Chromium at `/opt/pw-browsers/chromium-1194/chrome-linux/chrome`. A throwaway install needs a real provider key for `omnipus onboard`. Without one: write `system/state.json {"onboarding_complete":true}`, add a bcrypt user to `gateway.users`, log in via `/api/v1/auth/login` with the `csrf` cookie echoed as `X-Csrf-Token`. The app uses hash routes (`/#/agents`).
8. **Usage:** the account hit its 7-day limit on 2026-09-27 (resets 17:00 UTC). Sessions continued on overage. #904 had spent about $169, #920 about $61, team-lead about $49 at 06:27.

## 6. Leftovers to tidy

- Branch `work/team-lead-2026-09-26` holds the two interview records and this note; it is not merged anywhere. The squad branches carry their own copies of the interview records.
- Branch `fix/providers-awsregion-unhandled-error` (`21fb0c9`) is obsolete: its change landed via #928. Safe to delete.
- Branch `fix/915-remove-worker-test-run` is merged. Safe to delete.
- Scheduled triggers still set: the one-shot handover messages `trig_016at3AYddrY1kQ8tJNRAq7b` (#904) and `trig_01SQR67KLFaVVYGshfAi7PPQ` (#920) fire at 06:37 UTC and then disable themselves. The PR-928 check-ins have already fired.

## 7. First three steps for the next chief

1. Read both squad handover notes (§1). Check both sessions replied with a commit SHA. If a note is missing, the squad did not finish handing over. Ask it again, or rebuild the state from its branch history.
2. Get the #904 founder question answered, then drive #904 to UAT and a landing ask.
3. Get an owner for #891. Until it is fixed, every landing needs a founder exception for the `llm-agents` red.

| Claim | Evidence | Certainty |
|---|---|---|
| #928 merged by founder, #915 closed | `pull_request_read` 928: merged_by daniel-piatkowski-ai, merged_at 2026-09-27T04:21:08Z; `git log origin/release/v0.1.1` shows `2e21e00dc Merge pull request #928`; #915 state closed | Verified |
| Squad states at 06:27 UTC | `list_sessions`: #904 status REQUIRES_ACTION "Waiting on permission: AskUserQuestion"; #920 "code stream running (GREEN)" | Verified |
| Branch progress | `git log origin/release/v0.1.1..origin/feature/904-tool-iteration-limit` (95 commits, last 05:32); `…feature/920-read-boundary` (22 commits at 06:06, last 05:35) | Verified |
| Open PRs list | `list_pull_requests state=open` at ~06:10 UTC | Verified |
| "fix-890 squad" owns the llm-agents red | Only PR #944's body says so | Unverified |
| Handover messages delivered | Triggers created for 06:37 UTC; delivery and replies not yet observed | Unknown |
| Self-check | Re-read the list_sessions, PR and git outputs used above; every owner and SHA traced to one of them; unverified items labelled | Verified |
