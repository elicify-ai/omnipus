# UAT test plan — vault library & the record layer

**Branch:** `feat/library-improvements`
**Date:** 2026-08-26
**Audience:** a human tester with the app in front of them. No Go, no terminal beyond
starting the binary, no reading of code.

---

## Read this first: what is, and is not, testable by hand

This branch contains two very different kinds of work, and only one of them has
anything to click.

**Testable by hand — the vault library.** Mounting a vault, watching it index,
opening files of various types, and the behaviour of the library screen. This is
where the whole of this plan lives.

**NOT testable by hand — the record layer (ADR-068).** The typed-record engine
(schemas, money, enums, filters, comparison) is a *library with no consumer yet*.
There is no REST route, no agent tool and no screen that reaches it. Verified:
`grep -rl "omnipus/pkg/records" pkg/gateway pkg/sysagent cmd internal` returns
nothing, and no `/api/v1/records` route is registered.

A tester therefore **cannot** exercise records at all, and any UAT script claiming
otherwise would be describing a screen that does not exist. Its correctness rests
on the automated suite — including a 30,324-cell generated comparison table and a
`math/big` rational oracle over ~70,000 decimal pairs — not on this document.

**What this means for sign-off:** passing every case below does NOT mean ADR-068 is
validated. It means the vault library is. Say so when you report.

---

## Setup (once)

1. Start with a clean data directory so you see genuine first-run behaviour:

   ```
   export OMNIPUS_HOME=/tmp/omnipus-uat
   rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"
   OMNIPUS_BEARER_TOKEN="" ./omnipus gateway --allow-empty
   ```

2. Open the app in a browser. Complete onboarding if prompted.
3. Keep the browser console open (F12). **Console errors are findings.** A WebSocket
   reconnect warning is normal and is not a finding.

If the gateway exits without a message, look in `$OMNIPUS_HOME/logs/gateway_panic.log`.

---

## Case 1 — Mount a vault and watch it index

*Why this matters: indexing progress silently never arriving is a bug that has
already shipped once on this branch.*

| Step | Do this | Expect |
|---|---|---|
| 1.1 | Go to the **Library** screen | The screen loads with no console error |
| 1.2 | Mount a vault folder containing at least ~50 notes | The mount is accepted |
| 1.3 | Watch the screen **without refreshing** | Indexing progress appears **on its own**, within a few seconds, and advances |
| 1.4 | Wait for it to finish | Progress reaches completion and the note count is plausible for the folder |

**Fail if:** progress never appears; progress appears only after a manual refresh;
the count is zero for a folder that clearly has notes; or you must restart the app
to make indexing start.

---

## Case 2 — Indexing still works after a settings change (regression)

*Why this matters: this is the exact defect that shipped. Changing a setting
triggers an internal reload; the reload used to kill the indexing service and never
restart it. Mounting a vault afterwards returned success and indexed **nothing** —
no error, no log line — until the process was restarted.*

| Step | Do this | Expect |
|---|---|---|
| 2.1 | With the app running, go to **Settings** and change any setting, then save | Saved confirmation |
| 2.2 | Return to **Library**. Mount a **second, different** vault folder | The mount is accepted |
| 2.3 | Watch without refreshing and without restarting | Indexing progress appears and completes, exactly as in Case 1 |
| 2.4 | Search for a word you know is in the second vault | The note is found |

**Fail if:** the second vault reports success but never indexes; progress never
appears; or search cannot find content you can see in the folder. **Do not restart
the app to "make it work" — restarting hides this defect.** That is the whole point
of the case.

---

## Case 3 — HTML files

| Step | Do this | Expect |
|---|---|---|
| 3.1 | Put an `.html` file with a heading and a paragraph into the vault | — |
| 3.2 | Let it index, then open it in the library | The readable text is shown |
| 3.3 | Search for a word that appears only in that HTML file | The file is found |

**Fail if:** the file is skipped entirely, shows raw markup as its content, or its
text is not searchable.

---

## Case 4 — Embedded images

| Step | Do this | Expect |
|---|---|---|
| 4.1 | Open a note that embeds an image (`![[picture.png]]` or standard markdown) | — |
| 4.2 | Look at the preview panel | The image is **displayed**, not shown as a broken icon or as literal text |
| 4.3 | Try a note whose image is in a subfolder | Also displays |
| 4.4 | Try a note referencing an image that does not exist | Degrades gracefully — no crash, no blank panel |

**Note:** check the real preview panel, not a raw URL opened directly. They behave
differently, and a raw URL passing is not evidence the panel works.

---

## Case 5 — Obsidian `.base` files

| Step | Do this | Expect |
|---|---|---|
| 5.1 | Put a `.base` file in the vault | — |
| 5.2 | Let it index and open it | It is handled — either rendered, or refused with a message that says what it is |

**Fail if:** the app crashes, the panel goes blank, or the file silently vanishes
with no indication it was seen.

---

## Case 6 — Awkward but legitimate content

*Real vaults are messy. None of these should break anything.*

| Step | Do this | Expect |
|---|---|---|
| 6.1 | A note with a very long single line (a few thousand characters) | Handled |
| 6.2 | A note with accented and non-Latin characters in body and filename | Displayed correctly |
| 6.3 | A note with an empty body (frontmatter only) | Handled, not an error |
| 6.4 | A file with an unknown extension (`.xyz`) | Ignored quietly or reported clearly — never a crash |
| 6.5 | A folder with several hundred notes | Indexes without freezing the UI |

---

## Case 7 — Two vaults at once

| Step | Do this | Expect |
|---|---|---|
| 7.1 | Mount two vaults | Both listed |
| 7.2 | Search for a term present in both | Results identify which vault each hit came from |
| 7.3 | Unmount one | The other keeps working; its search results are unaffected |

---

## How to report

For each case: **Pass**, **Fail**, or **Blocked** (couldn't run it).

For a failure, state:
1. What you did, step by step, so someone else can repeat it exactly
2. What you expected
3. What actually happened
4. Any console error, copied verbatim
5. Whether it happened again when you retried

**Please do not diagnose or work around.** "I restarted and then it worked" is a
finding, not a pass — Case 2 exists precisely because a restart hides the defect.

**Assume every failure is real.** Do not write anything off as a glitch or a slow
machine. If it happened once, it is worth reporting; if it happens twice, say so.
