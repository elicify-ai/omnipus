# ADR-093 — founder decisions (#890)

## Amended 2026-10-06 — founder decision

Later founder clarification: a finished root chat is lifecycle **completed/done, not archived or hidden**. Human input continues it; a scheduled/heartbeat run may revive completed as the system principal into a new round, **never a stopped root**. Stop ends only this session's current turn, never helpers; Stop all / `/cancel` stops its whole downward helper tree. The dated verbatim F890 rows below are retained as history; the blanket system-wake wording must be read with this completed-versus-stopped distinction. Authority: [The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-adr-spec-sync-20261006/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md)::D-B/D-C and **An open conversation must keep the ability to delegate**::Amended 2026-10-06.

| ID | Date | Decision (founder, verbatim first) |
|---|---|---|
| F890-1 | 2026-09-25 | "Parent and child session need to keep their state and continue — why is that done so complicated. If I open a parent session after one month and there is still a child session not deleted yet by housekeeping, the parent session could even send a follow-up to the child session. What I try to say: sessions and child sessions are always resumable, like in Claude Code." |

Interpretation for the ADR correction round (team-lead; confirm with founder at the grill interview):
- No session state is a permanent dead end. A session (conversation root or child/worker) that ended — stopped, failed, interrupted by a restart — is resumable at any time until housekeeping deletes it.
- A parent can send a follow-up to any of its children that still exists, regardless of either side's past terminal state.
- A restart never makes a conversation unusable; the next message (or follow-up) resumes it.
- Consequence for the RED test on fix/890-red: the expected user text "a new chat is needed" is WRONG under this decision — delegation from a previously-interrupted chat must succeed; the defect-2 test must be re-pinned by qa-lead after the ADR correction.
| F890-2 | 2026-09-25 | Confirmed team-lead's reading of F890-1 exactly: a restart never makes a chat, heartbeat or recurring session unusable; Stop only ends the current turn — the next message (or a parent's follow-up to a child) continues it; a task created from a stopped/finished chat still runs; the "running but stopped" state that blocks delegation (review MAJ-001) is fixed too. Answers ADR-093 Q1 (B+), Q2 (A), Q4 (A+). |
| F890-3 | 2026-09-25 | Group chats: only people authorised to act for that conversation may resume one the operator stopped; automatic wake-ups never do — but NOT part of this work: tracked in #892. |
| F890-4 | 2026-09-25 | Defaults taken by team-lead, stated to the founder: ADR Q3 A — no new "interrupted" screen element (chats simply resume); review Q6 A — a chat's lifecycle record is deleted with the chat, housekeeping removes the rest (founder: sessions live "until housekeeping deletes" them). |
