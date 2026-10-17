# Parent wake visibility — hands-on UAT checklist

Not executed by this RED pack. Required on the exact commit being landed, by a tester and an independent validator. Do not use the founder's live instance.

Provider: `openrouter`. Model: `deepseek/deepseek-v4.1-flash`.

Source: `docs/internal/specs/parent-wake-visibility-spec.md` section 12, "Hands-on UAT".

Every screenshot shows the workspace name and the composer identity (the parent agent, not the helper).

| Row | Steps | Pass |
|---|---|---|
| UAT-1 | Open the parent chat. Ask the parent to delegate a short task and not to poll. Wait until the parent's turn ends. Stay on the chat. | The parent's next reply appears live, without reload. A partial token is visible before the turn completes. |
| UAT-2 | Delegate a goal the Judge can confirm. Stay on the parent chat. | A "Goal met" row is visible in the parent chat. The parent's reply does not say the verdict was missing. The composer is the parent. |
| UAT-3 | Press Stop on the helper only, while the parent is idle. | The parent chat shows a stopped row. No new parent turn. The helper stays stopped. |
| UAT-4 | Start a native helper whose tool appends one line to an append-only fixture file, correlated to that assistant occurrence and call id. Restart the gateway after that line exists. | The fixture file still has one line for that historical occurrence and call. A new model decision may append a different line. The transcript's single success row is supporting history, not the proof. The parent chat shows one immutable restart row and a live parent turn. Tester and validator both read the fixture on this commit. A unit-test receipt does not replace them. |
| UAT-5 | Reload the parent chat after UAT-2 and UAT-4. | The verdict row, the restart row, and the parent replies are present once each. |
| UAT-6 | A helper that emits progress while the parent is idle. | The side panel updates. The parent does not start a turn until a real ending. |

Lane note for the campaign planner: one account per lane. The session cookie is a single slot per user, so the validator uses a different account from the tester and re-drives UAT-1, UAT-2 and UAT-4 rather than re-reading the tester's screenshots.
