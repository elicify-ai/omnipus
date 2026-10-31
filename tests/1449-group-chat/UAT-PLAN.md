# #1449 hands-on UAT plan

Source: `docs/internal/specs/1449-group-chat-spec.md` rows UAT-1 to UAT-36, minus the Stop rows removed by D21 (UAT-8, UAT-9, UAT-22 are not reused). Procedure: root `CLAUDE.md` "Hands-on pipeline". Provider for any agent-driven row: `openrouter` + `deepseek/deepseek-v4.1-flash`.

Rows marked AE touch the agent engine. They need a tester and an independent validator on the exact candidate before landing.

## Lanes and accounts

The session cookie is one slot per user. One tester account per lane. The validator account is a different user and is never the tester's session.

| Lane | Account pair | Rows | Why this split |
|---|---|---|---|
| Read | tester `uat-1449-read`, validator `uat-1449-read-v` | UAT-1, UAT-4, UAT-5, UAT-6, UAT-7, UAT-18, UAT-19, UAT-20, UAT-21, UAT-27, UAT-31 | One group, no file deletion, no approval cards |
| Files | tester `uat-1449-files`, validator `uat-1449-files-v` | UAT-2, UAT-3, UAT-17, UAT-26, UAT-28, UAT-29, UAT-33, UAT-34, UAT-35 | Uploads and Library deletion. Separate workspace from Read so a deletion cannot wipe the reading lane's files |
| Engine | tester `uat-1449-engine`, validator `uat-1449-engine-v` | UAT-10, UAT-11b, UAT-12, UAT-13, UAT-14, UAT-15, UAT-16, UAT-23, UAT-24, UAT-25, UAT-30, UAT-32, UAT-36 | AE rows. Two browsers on UAT-23. Validator re-drives with its own account |

## Evidence

```
/workspace/logs/1449-uat/<candidate-sha>/<lane>/<row-id>/
  step-<n>.png          screenshot
  step-<n>.html         redacted page snapshot (no tokens, no secret values)
  notes.md              one line per step: action, expected, actual
```

Every screenshot must show the badge: the signed-in account name and the role `tester` or `validator`. A validator shot that does not show the validator account is not evidence.

## Validator re-drive (not a re-read of the tester's shots)

| Row | What the validator does again |
|---|---|
| UAT-1 | Send a fresh agent answer with a table, a diagram, a formula and code. Compare with the same text in a normal chat |
| UAT-2, UAT-29 | Open the addressed agent's own chat and the Library. The image or the attachment-only message is there |
| UAT-3, UAT-33 | Download the file from the group with the keyboard, and from the Library in the validator's own browser |
| UAT-4, UAT-19 | Side-by-side: group composer and header against a normal chat and a helper chat. Tab to Participants |
| UAT-5, UAT-6 | Rename, send, delete the @ token, pause, and check who received the message |
| UAT-7 | Watch both working lines until both answers. Confirm no partial text |
| UAT-10 | Second browser on the agent's chat. Approve in the group. The agent's chat updates without reload |
| UAT-11b, UAT-12, UAT-14 | Confirm the card is absent from the group. Follow the link. Approve. The attention animation stops |
| UAT-13, UAT-24 | Answer in the group, then reload both places |
| UAT-15, UAT-16, UAT-25, UAT-30 | Jarvis's chat has no new message. Read the summary. Confirm the registered value is absent. With the provider blocked, the first message is not held |
| UAT-18 | A group of 10,005 entries. Scroll, return, reopen |
| UAT-20 | Click the struck link. Confirm the group is not paused. The remote endpoint log stays empty |
| UAT-23 | Two browsers, approve and deny together. Both end on the same decision |
| UAT-27 | Stream in chat A, switch to the group, send, press Escape, return to A still streaming |
| UAT-28 | Replay the other-workspace media reference. Nothing is posted |
| UAT-31, UAT-35 | Send both sizes. The error names the limit in force |
| UAT-32, UAT-36 | No card in the group (or in either group). Approve in the agent's chat. Both attention animations stop |
| UAT-34 | Second browser reloads the group and the normal chat, fetches the old media URL (not found), restarts, confirms nothing returns |

## Rebuild

Standing lane instances are rebuilt from the candidate SHA with one script before the pair starts. The receipt (SHA, build time, instance URL) is stored next to the evidence and is not a product PASS. Tester and validator are started together by team-lead. They do not talk before the validator rules. A later diff re-runs only the rows whose area the diff touches.

## Automation candidates

Stable rows named in the spec (T-27, T-28, T-47, T-48, T-49, T-50, T-54, T-56, T-60, T-64, T-65) are the ones to turn into end-to-end tests after they have passed hands-on once. Rows marked "Not yet" in the spec stay hands-on.
