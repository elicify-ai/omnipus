# UAT results — vault library, 2026-08-27

Branch `feat/library-improvements` @ `afeda510`. Four testers, isolated gateway
instances (ports 5101–5104), each driving the real SPA in its own browser.

Plan: `uat-library-records-2026-08-26.md`.

**Scope, stated plainly:** these results validate the **vault library only**. The
ADR-068 record layer has no route, no tool and no screen — `grep -rl
"omnipus/pkg/records" pkg/gateway pkg/sysagent cmd internal` returns nothing —
so no tester exercised it, and nothing here says anything about it.

---

## Verdicts

| Case | Verdict |
|---|---|
| 1 — Mount a vault and watch it index | **FAIL** — indexing works; progress never displayed |
| 2 — Indexing after a settings change | **PASS** — the shipped regression does not reproduce |
| 3 — HTML files | **FAIL** — preview not rendered |
| 4 — Embedded images | **FAIL** — no `<img>`; embed link resolves to a 404 |
| 5 — Obsidian `.base` files | **PASS** — refused with a clear message, findable by name |
| 6 — Awkward content | **PASS** — all five sub-steps |
| 7 — Two vaults at once | **PASS** — all four sub-steps |

---

## A correction, recorded because it nearly became a false bug report

The first run of Cases 1–2 reported "nothing ever indexes". That was **bad test
data, not a defect**: a folder is only a knowledge base if it contains a
`.obsidian/` or `.omnipus-vault/` **marker directory**, and the fixtures were
plain folders of markdown. `AttachMount` classified them correctly and skipped
them. Confirmed by running the real `IsKnowledgeBase` against the fixture —
`false` before adding the marker, `true` after — and the retest then found
indexing working normally.

A separate probe of the progress WebSocket also produced "zero progress frames",
which looked like a root cause. Its own anti-vacuity counter showed **zero frames
of any type**, i.e. the probe's socket was dead and the result meant nothing. It
was discarded rather than reported.

Both mistakes share one lesson worth keeping: **prove the instrument works before
trusting its silence.**

---

## Findings

### F1 — The gateway exits on its own, silently, when a spawned Chrome's pipe closes
**Severity: high. Corroborated by two independent testers.**

Both saw the process terminate with **exit code 0**, no panic, nothing in
`gateway_panic.log`, while the UI sat idle or was being driven normally. Both
captured the same final log line:

```
pkg/tools/browser/exec_resolver.go:386 > cdppipe: chrome: [...]
ERROR:.../devtools_pipe_handler.cc:188 Connection terminated while reading from pipe
```

In both cases the Chrome was spawned by the gateway itself during onboarding;
neither tester used the browser feature. Occurrences: 19:40:22 on port 5101,
09:02:04 on port 5104. A user loses their session with no message and no
indication anything is wrong.

### F2 — Indexing progress is never displayed, for any vault size
**Severity: high (this is the originally-reported issue).**

Indexing itself works — searches return correct results with honest completeness
reporting. Only the progress display is missing. The banner permanently reads:

> This folder is a knowledge base. No indexing progress has arrived since you
> opened it, so Omnipus cannot tell you here how far the index has got.

Observed across 60, 600, 2000 and 6000-note vaults, sampled at 150 ms for up to
120 s, watched from the instant of mount. No bar, no percentage, no file counter,
and **no note count anywhere in the UI**. "Did it finish?" is answerable only by
running a search and reading its completeness line.

### F3 — HTML files are not rendered
`report.html` opens to *"Preview unavailable … Rendering it needs the isolated
preview endpoint, which this build does not serve yet."* The content fetch
returns **200** — the file is there and readable; the panel declines to show it.
Edit mode does show the source. 3/3 attempts.

### F4 — Image embeds resolve to the wrong path
A note embedding `![[picture.png]]` produces **zero `<img>` elements** — all
embeds render as text links. The generated link **drops the mount prefix**:

| path requested | result |
|---|---|
| `vault-a/picture.png` (correct) | **200** |
| `picture.png` (what the panel emits) | **404** |

Clicking it as a user gives *"picture.png was not found"* for a file visible in
the listing. The image path itself is fine: opening `picture.png` directly in the
same panel emits a real `<img>` that returns 200. A missing embed
(`![[nope.png]]`) renders **identically** to a present one, so nothing signals the
difference.

### F5 — "The file changed since it was indexed" is reported for files that never changed
Searching a **filename** fragment returns
*"No excerpt: the file changed since it was indexed."* The files had not changed —
mtimes predate the gateway's start and nothing wrote to the vault. Not a stale
index either: the same file, same session, seconds apart, returns a full excerpt
for a **body-word** query and the "changed" message for a **filename** query.
Reproduced on every markdown file matched by filename, twice.

### F6 — A folder without a marker gives no indication it will never be indexed
Confirmed by two testers. A folder with no `.obsidian/` mounts silently and looks
**identical** to a real vault — same `MOUNTED` badge, counted in the mount total.
Its detail view has no knowledge-base banner and no "Search notes" box. The only
signal is the *absence* of two things the user would have to already know to look
for. Nothing before or after mounting says the folder will not be indexed, or
what would make it a knowledge base.

*(This gap is what made this exercise's own test fixtures silently wrong.)*

### F7 — Unmounting a vault mid-index leaves the row showing MOUNTED
**3 of 3 attempts.** Unmount a 6000-note vault ~3 s after mounting, while
indexing runs: the row stays, still badged `MOUNTED`, count unchanged, for 50–70 s
of continuous observation. A page reload shows it really is gone — the server did
unmount it (`DELETE … 204`); only the list is stale. **Not a size problem:** the
same vault unmounted *after* indexing completed updated in 3 seconds. One of the
three also left the confirmation dialog painted and stalled the page's timers for
~35 s.

### F8 — Minor
- **Error copy is developer-facing.** Mount rejections are correct but surface raw:
  `400: workspace: mount target is not an existing directory: "/private/…": stat /private/…` — and truncated mid-word.
- **The banner over-promises.** It says *"Each set of search results below states its own completeness"*; a 20-result set and a 0-result set both get one, but a **3-result set** shows bare "3 results" with no completeness line.
- **Search results are never labelled by vault.** Search is scoped to the vault you are inside, so results can't be ambiguous — but attribution is contextual (breadcrumb, truncated to `vault-b-51…`) rather than on the rows. A design decision, not a bug.
- **Every unmount logs `net::ERR_ABORTED`** on a DELETE that returns **204** — including unmounts that work perfectly. Not user-visible, but it means that error can't be used as a failure signal.

---

## What went right, and is worth not breaking

- **Completeness reporting is honest.** Mid-index: *"these results cover none of it"*. Done: *"index was complete at query time"*. Checked against vaults of known contents; it told the truth every time.
- **No UI freeze under load.** A 6000-note vault indexed in ~100 s with main-thread round-trip at 0.00 s. Frame sampling over a separate run: 1,446 frames, max gap 35 ms, zero frames over 200 ms.
- **Unicode is clean end to end** — filename, list row, preview header, body and search excerpt. Byte-checked; explicit mojibake probes (`CafÃ©`, `ï¿½`, U+FFFD) all absent.
- **Unknown file types and `.base` files are refused with a message that says what they are**, rather than vanishing — and remain findable by filename, with the honest note *"Matched on the file name — attachment contents are never read."*
- **Rapid mount/unmount cycles**, empty folders, duplicate mounts and non-existent paths are all handled correctly.

---

## Method note

Two testers verified their own console capture — injecting a deliberate
`console.error` and a bad fetch at both the start and end of the run — before
reporting "no console errors", on the grounds that a silent console can equally
mean a dead listener. Both confirmed the capture live at both ends. That is why
the zero-error results in this report are stated as results rather than as an
absence of evidence.
