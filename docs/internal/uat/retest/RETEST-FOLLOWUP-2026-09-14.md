# UAT re-test follow-up — rows to re-run once the pending fixes merge

These rows run on the build that contains every fix branch still in flight on 2026-09-14. They use the same tester protocol as `RETEST-PLAN-2026-09-14.md`, with three stricter rules learned today:
- **PASS needs every part of the row's expected outcome.**
- **Never judge an "after an edit" result on a screen captured after the undo.**
- **Check every screenshot.** Take its checksum and open it, and confirm it shows what its name claims.

## Rows and what each needs

| Row | Why it re-runs | Depends on fix branch | Fixture or setup it needs |
|---|---|---|---|
| U-58 | NEW DEFECT: deleting `.omnipus-vault` silently fails with a 400 | `fix4/vault-marker-delete` (routing) and `fix4/library-dialog-errors` (error shown) | A fresh scratch knowledge base to demote |
| U-21 | PASS, with a follow-up: a refused mount shows a generic line instead of the server's reason (D-127 area) | `fix4/library-dialog-errors` | None |
| U-48 | FAIL: images download all at once; D-101's image in-flight cap is missing | `fix4/image-inflight-cap` | 40 distinct uncached images in one note, network log |
| U-32 | FAIL (validator): an embed says "No view named …" for a view that exists but failed to load | `fix4/image-inflight-cap` (the same file carries the message fix) | A `.base` with one declared view made unloadable |
| U-25 | BLOCKED (validator): only the enum editor was tried; date and text editors never opened | `fix4/spa-rate-limit-ux` (editor save behaviour) | `Types.base` offers all three editors |
| U-29 | BLOCKED (validator): the board was judged after the edit was undone | `fix4/spa-rate-limit-ux` | Judge the card move on the screen before any undo |
| U-31 | BLOCKED (validator): the 45-project fixture was never created, so the 40-row link cap was not exercised | none | Create 45 project records, via the agent or the UI |
| U-33 | BLOCKED (validator): `view-truncated` and `view-problems` never appeared | none | A view whose query exceeds the row limit, plus records with per-record warnings |
| U-23 | Plan error, now BLOCKED (fixture): views were created before `source:` existed | none | Create the Projects views with `source: Projects.base`; expect one tab per such view (11), each listing its parts |
| U-30, U-64 | Plan error, now BLOCKED (fixture): crosstab is a part of the `breakdown` kind, and no breakdown view exists | none (D-55 already fixed) | Create a breakdown view through the agent, then check its crosstab numbers against the records on disk |
| Layout | Possible S2: Library preview off-screen at 1440x900 with many saved views | `fix4/library-layout-offscreen` | A knowledge base with 30 to 69 saved views, at 1440x900 and 1280x720 |
| t4 rows U-34, U-35, U-36b, U-38, U-65, U-66, U-67 | BLOCKED: the private browser disconnected (t4/t4-rerun never connected; t4b dropped mid-lane). The validator OVERTURNED t4b's U-34/U-35 PASS to BLOCKED (screenshots showed the saved-views list, not results; U-34 exercised only the All tab). | none | One fresh lane, low machine load, no resume mid-lane; result screenshots must show the hit list, the coverage line and all five kind tabs, not the folder pane |
| B-30 | BLOCKED (validator): the trash-then-search check was never run | none | Trash a note, then run `knowledge_find` and `knowledge_read` right away; the note must be gone from search |
| B-35 | BLOCKED (validator): only a property add was run, no rename or removal | none (founder decision 7 may change the expected outcome) | Rename a property used by notes and a saved view; check the cascade report covers both |
| B-40 and D-01 | BLOCKED (validator): D-01, the S1 false word match while the index is degraded, was never re-tested | none | Write a file directly on disk, run a `words` search before and after indexing, and check the delete half |
| Q-23 | BLOCKED (validator): no record was inserted mid-walk | none | Page 1, insert a record that sorts into page 2, then page 2 |
| B-41 and Q-02 | FAIL (validator upheld): a malformed-frontmatter note is served as valid (D-06, S2) | `fix4/malformed-frontmatter` | `Broken FM.md` with an unclosed block, or a fresh copy |
| Q-26 | FAIL (validator upheld): an agent can create a link to `/etc`; the guard only reports it afterwards (D-14, S2) | `fix4/symlink-capability` if the triage finds a real capability; otherwise a founder decision | Same prompt as the first run |
| Q-08 | NEW DEFECT (validator): the agent cannot open a view by the label the Library shows | `fix4/view-label-lookup` | Ask the agent to use "All Projects" by name |

## Plan corrections

These go into the final report, not the plan file.
- **U-23:** "8+ tabs" is wrong. A `.base` preview shows one tab per saved view whose `source:` is that file (`view-kinds-design-2026-09-03.md`). `Projects.base` itself declares 7 views. With the fixture corrected, the expected count is 11.
- **U-30, U-64:** crosstab is not a view kind. It is a part produced by the `breakdown` kind. No documented limitation is needed, and D-13's "no such kind" wording should be corrected.
- **D-13:** the "no such kind" statement refers to crosstab as a kind. It is a part, so the statement is misleading.

## After each row

Sweep that lane's evidence folders for tester passwords, replacing each with `[REDACTED]`, before the final report is written.

## Harness rule added after the op lane (validator finding)

**Stop the agent before writing LANE DONE.** A test agent turn ("UAT Denied") kept running after the op lane finished. It rewrote notes, trashed a note and edited a schema, overwriting evidence other rows relied on. The operator lane must press Stop on every agent chat turn it started, and confirm the turn ended, before it writes `LANE DONE`.

**Policy note, recorded for the report:** denying an agent `knowledge_edit` (row P-02) does not make it read-only. The same agent still wrote through `write_file`, `knowledge_restructure` and `knowledge_configure`. This is per-tool policy by design, not a defect, but a person expecting "read-only" would be surprised.
