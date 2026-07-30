# Library — UAT plan

Target: `https://uat-omnipus.fly.dev` (heavy image, `feat/library` @ `787f96d3`).
Method: parallel testers driving a real browser via Playwright MCP. Observation, not static checks.

## Ground rules for every tester

1. **You are a user, not a developer.** Do not read source code to decide whether something
   works. Click it. If the UI says one thing and the disk says another, the UI is what shipped.
2. **Report what you SAW.** "The panel opened" is a claim; a screenshot is evidence. Attach one
   for every PASS on a visual feature.
3. **Do not be primed.** Nothing in this plan tells you what the correct output looks like for
   the agent-awareness tests — an earlier UAT in this project was invalidated because the brief
   told the tester what to expect and they echoed it back. If you find yourself confirming an
   expectation rather than observing, stop and re-read what is actually on screen.
4. **A silent success is a failure.** If an action appears to work but you cannot verify the
   effect (file not actually renamed, save not actually persisted), that is a FAIL, not a PASS.
5. **Record exact reproduction steps** for every issue, including the file used.

## The 10 file types (mandatory coverage)

| # | Type | Sample | Expected preview surface |
|---|---|---|---|
| 1 | PNG image | any screenshot | inline `<img>` |
| 2 | JPEG image | photo | inline `<img>` |
| 3 | SVG image | vector | inline `<img>` (note: SVG can carry script — see EDGE-9) |
| 4 | MP4 video | short clip | `<video controls>`, must actually play |
| 5 | Markdown | `.md` with headings, list, table | rendered markdown, view/edit toggle |
| 6 | Markdown with Mermaid | ```mermaid fence | rendered DIAGRAM, not code |
| 7 | Mermaid file | `.mmd` | rendered diagram, editable source |
| 8 | Code — TypeScript | `.ts` | syntax-highlighted, editable |
| 9 | Code — Go / Python / JSON / YAML | any | syntax-highlighted, editable |
| 10 | Binary / unsupported | `.pptx`, `.zip`, `.bin` | metadata card + Download. Must NOT attempt to render |

## Functional scenarios

**F-1 Sidebar entry → virtual root.** Sidebar shows section "Assets" (not "Library") with a
file-ish icon, containing a "Library" item. Opening it lists ALL workspaces.

**F-2 Chat header entry → workspace-scoped.** The Library button in the chat header opens
directly into the active workspace's files, not the virtual root.

**F-3 Slideout, not modal.** The panel must dock beside the chat as a side-by-side split — the
rest of the app stays visible and usable. If it dims the background or blocks interaction, that
is a FAIL (explicitly a modal, which was rejected).

**F-4 Pop-out.** Opens a new browser tab showing the same Library full-screen. Closing that tab
re-docks the panel.

**F-5 CRUD.** Upload, rename, delete (with confirm), download. Verify each on disk-visible
effect: after rename the new name appears and the old is gone; after delete the file is gone
from the list.

**F-6 Show Hidden.** OFF by default. Turning it ON reveals `.library/` — this is the specific
thing the toggle exists for. Hidden entries should look visually distinct.

**F-7 Copy / Move, including across workspaces.** Move a file from workspace A to workspace B and
confirm it appears in B and is gone from A. Copy and confirm it exists in BOTH.

**F-8 Edit + save.** Open a markdown file, switch to edit, change text, save. Reload the page.
The change must persist. An apparent save that does not persist is the highest-severity class
of bug here.

## Agent-awareness scenarios (the reason this feature exists)

**A-1 Upload with NO caption.** Attach a file in chat and send WITHOUT typing anything. Then ask
the agent about the file. Record verbatim what the agent says.

**A-2 Upload with a caption.** Same, but type a message alongside the attachment.

**A-3 Agent reads the file.** Ask the agent to open/summarise the uploaded file by name. Record
whether it succeeds, and whether it had to guess the filename.

**A-4 Handoff.** Upload with agent 1, then switch to agent 2 and ask about the file. (This is the
exact scenario that failed in the 2026-07-29 session: Mia → Ray.)

**A-5 Filename with spaces.** Use a file whose name contains spaces and mixed case, e.g.
`Copy of My Deck.pptx`. This is the literal file that broke before.

## Edge cases

| ID | Case | What to check |
|---|---|---|
| EDGE-1 | Empty workspace | Library opens with an empty state, not an error or crash |
| EDGE-2 | Filename with spaces, unicode, emoji, very long (>200 chars) | Upload, list, rename, download all survive |
| EDGE-3 | Duplicate filename upload | Dedup suffix applied; the original is NOT overwritten |
| EDGE-4 | Large text file (>1MB) | Either edits or falls back to download — must not hang or render garbage |
| EDGE-5 | Binary mislabelled as text (`.txt` containing NUL bytes) | Falls back to download, does not render mojibake |
| EDGE-6 | Zero-byte file | Lists, previews as empty, does not crash |
| EDGE-7 | Deeply nested path | Navigate in and back out via breadcrumb |
| EDGE-8 | Rename to an existing name | Blocked with a clear message; does NOT silently overwrite |
| EDGE-9 | SVG containing `<script>` | Renders as an image WITHOUT executing script (check console) |
| EDGE-10 | Path traversal via the UI | Attempt `../` in any rename/move field. Must be refused |
| EDGE-11 | Unsaved edits + navigate away | Warns before discarding |
| EDGE-12 | Two tabs editing the same file | Last write wins (known, accepted) — confirm no corruption/crash |
| EDGE-13 | Delete a file currently open in preview | Preview clears gracefully |
| EDGE-14 | Move a file into the workspace it is already in | No-op or clear message, no data loss |
| EDGE-15 | Network failure mid-save | Error surfaced to the user, NOT silently swallowed |

## Severity

- **BLOCKER** — data loss, path traversal, silent save failure, agent still cannot see uploads.
- **HIGH** — a listed scenario does not work at all.
- **MEDIUM** — works but wrong/confusing behaviour.
- **LOW** — cosmetic.

## Output format (every tester)

For each scenario: ID, PASS/FAIL/BLOCKED, what you observed, screenshot reference, and for
failures: exact steps, expected vs actual, severity.
