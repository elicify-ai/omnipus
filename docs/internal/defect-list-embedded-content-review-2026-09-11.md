# Defect list — embedded content review (2026-09-11)

Defects found by review during the ADR-083 embedded-content work on
`integrate/library-improvements-v0.1.1`, all of them **now fixed**. Companion to
`defect-list-wikilink-rendering-2026-09-08.md`,
`defect-list-knowledge-base-ux-2026-09-08.md` and
`defect-list-html-preview-2026-09-08.md`, which record the defects you reported
yourself.

**Why these are written down at all.** Every entry here was found, fixed and
verified inside one working session. None of it ever reached you as a symptom.
But the lists are the record of what was wrong with the product, not only the
record of what you happened to notice — and a defect fixed but never written
down is invisible to the next person who has to reason about this code.

Status re-verified against the code at `4e2ef3dbb` on 2026-09-11, in the file
each entry names, not against the commit messages that claimed the fixes.

**A pattern runs through four of these, and it is worth naming.** R-7, R-9,
R-11 and R-10 are all the same shape: something was *declared* — a policy
widened, a component written, a test file created — and the thing it was
declared for was never connected. Each one passed its tests. None of them
worked. That shape is the reason this branch checks the code and not the commit
message.

---

### R-1 — a save could silently destroy someone else's work, and answer "Saved."
**Severity:** critical (silent data loss) · **Area:** Library SPA + gateway
**Status: FIXED** — `b9149a615`

**What it meant for you.** You open a note in the Library. An agent writes to
the same note a moment later. You edit what is still on your screen and save.
Your save succeeds — status 200, *"Saved."* — and the agent's version is gone,
with nothing anywhere recording that it ever existed.

**The mechanism.** Every save carries a version token, so that a save can be
refused if the file changed underneath it. That check was working. The problem
was upstream of it: the editor fetched the note's content and its version token
in one request, threw the content away, and then paired that fresh token with
content it had read in an *earlier* request. The token matched what was on disk,
so the safety check passed — while the text on screen was the older version.
This is not a microsecond race; the window is a full network round trip, and up
to ten seconds when the displayed text came from cache.

**The fix, and why it is two behaviours rather than one.** If you have typed
nothing yet, the editor quietly moves to the newer text and tells you it did —
nothing of yours is lost, and the token now matches what you can see. If you
*have* typed against the older text, moving you would eat your keystrokes, so
the save is refused and the conflict is shown. Always-rebase would discard your
work; always-refuse would block the ordinary case where nobody has typed.

**Two related defects fixed in the same change.** A note deleted underneath the
editor made Save an inescapable loop — the same conflict returned forever and
only a page reload got you out. And the server computed the version token in a
*second* read of the file, so the content it sent and the token it sent could
come from different versions; the token is now derived from the exact bytes that
were returned.

**Why no test caught it:** every fixture in the conflict test set the "current"
content equal to the displayed content, which made the divergence impossible to
express.

---

### R-2 — the limit on how many PDFs can open at once was measured by nothing
**Severity:** high · **Area:** Library SPA · **Status: FIXED** — `1ad501890`

**What it meant for you.** Open a third PDF and you got a spinner reading
*"Waiting for a PDF worker…"* — forever, with no explanation and no way to tell
whether it was stuck or working. It now names the real reason and the way out:
only two PDFs can be open on this page at once, and this one opens when another
closes or scrolls out of view. The number comes from the limit itself, so the
message cannot drift from the behaviour.

**The review finding was half right, and the half that was wrong is recorded.**
The review said the PDF worker was never released on success and called it a
leak. It is not: edit mode and save both keep using the same worker after the
first render, so releasing it there would either kill a worker still in use or
allow a third real one alongside two half-released ones. The code was right and
its own documentation was wrong — it described the limit as bounding documents
"under construction at once" when in fact a worker is held for as long as the
document is open. The comment now says what actually happens.

**The real defect was in the measurement.** Embedded content only loads when it
scrolls near the screen, and how *near* is the difference between a dashboard
that loads smoothly and one that thrashes at the boundary. The test's fake
scroll-observer accepted the margins and threw them away — so both could have
been set to zero and all eight tests would still have passed. The margins are
now captured and two tests pin the actual distances.

**And a latent freeze.** A callback sitting in the wrong place would have made
the component re-run itself every render. Proving it was fixed produced the most
unambiguous evidence in this list: restoring the bug killed the test run outright
after 30 seconds with no output at all, because the loop blocks the browser
before a test timeout can even fire.

---

### R-3 — a refused save left no trace
**Severity:** high · **Area:** pkg/gateway · **Status: FIXED** — `845431acb`

**What it meant for you.** Asking "who changed my note" returned only the saves
that succeeded. Every save that was *refused* — a conflicting write, a stuck
lock, a malformed request — left nothing at all. For a feature whose entire
premise is that a lost note is undetectable afterwards, the refused attempts are
exactly the population you would need to see.

**What you get now.** A refused save leaves the same kind of record as a
successful one, under the same event name, so one query returns every attempted
save. Five distinct reasons are recorded rather than one: a version conflict, a
lock timeout, two different kinds of malformed request, and an actual write
failure.

**Two distinctions in there are deliberate, not incidental.** "Sent no version"
and "sent the version in the wrong format" are separate reasons because they are
different incidents with different fixes — the second is a client bug that
repeats on every save, and it would be invisible if both shared a label. And a
write that *failed* is recorded differently from one that was *refused*: refused
means your file is intact, failed means the file on disk needs looking at.

**One gap flagged rather than quietly left.** With strict request validation
turned on — not the default — two of the malformed cases are rejected before the
handler runs, so they produce no record. Closing that means auditing one layer
further out, in a different file.

---

### R-4 — an agent could inject arbitrary markdown into a note through one argument
**Severity:** high · **Area:** pkg/knowledge · **Status: FIXED** — `a9c8bfbaa`

**What it meant.** The tool an agent uses to embed content into a note validates
every argument it accepts — a width against a pattern, a view name against the
collection's real views, a heading against the target's real headings. One
argument, the block reference, was validated against nothing and written into the
note verbatim. A crafted value wrote several lines of chosen markdown into your
note under the guise of a single embed — including a second embed the caller
never asked for, which skipped the existence check that applies to a declared
target.

**Scoped honestly, because the scope matters.** This is not a privilege
escalation. An agent that can call this tool can already write arbitrary markdown
into the same file through its ordinary content arguments. What it broke is the
tool's own promise — *"the correct notation is written for you"* — and the data
rule that a block reference is supposed to obey.

**The fix is anchored to the reader, not invented.** The pattern the validator
now enforces was traced from the code that actually *reads* these references, so
the two ends agree exactly. Anything outside it could never have resolved to a
real reference even if it had been written safely.

**A test's premise was corrected rather than the test deleted.** The existing
test's comment documented writing the value verbatim as *intended* behaviour.

---

### R-5 — the tool said it had checked something it had not checked
**Severity:** high · **Area:** pkg/knowledge · **Status: FIXED** — `6a1927f69`

**What it meant.** After R-4, a block reference had to look valid — but nothing
checked that it actually *existed*. Both of its sibling arguments do check: a
view name is looked up in the collection's real views and refused with a list, a
heading is scanned for in the target. So an agent asking to embed a reference
that was not there received a **success**, and you found out later when the note
rendered a broken embed.

**Why that is worse than checking nothing.** The tool's own description told the
model the notation is written *"after checking the target exists."* An agent
that trusts a success message has no reason to verify it. A tool asserting a
check it does not perform is more dangerous than one that admits it checks
nothing.

**What you get now.** The reference is looked up in the target file, and a
missing one is refused with a list of the references that *do* exist, so an
agent can correct itself instead of guessing — the same shape the heading
refusal already had. The two grammars, the one the writer enforces and the one
the reader accepts, are now byte-identical; they were diffed, not assumed,
because two grammars that drift would refuse references the reader would find.

---

### R-6 — agents were told a file that exists is missing
**Severity:** high · **Area:** pkg/knowledge + Library SPA
**Status: FIXED** — `162b81ced`

**What it meant.** When Omnipus cannot read part of a knowledge base — a folder
whose permissions block it, a symbolic link it will not follow — it records that
it *skipped* it. The reading pane used that skip list and correctly said a link
"could not be checked." The agent-facing tool never received the skip list at
all, so it reported the same link as **`no_match`**, whose own definition is
"nothing in the collection carries that path or name." The file exists. The
agent was told, flatly, that it does not.

This is the central thing ADR-083 exists to eliminate, and it was half-built:
your decision that the honesty marker must reach agents too was the half nobody
wired up.

**A second, structural blindness fixed alongside it.** Even the reading pane
could not handle the *normal* way links are written in Obsidian — a bare name
with no folder in it. The rule that matched a skipped folder to a link needed a
folder separator to work with, so for `![[plan]]` it always missed, and an
unreadable folder produced a confident "this does not exist." The honest rule
now is: while any unreadable folder is present, a bare name's absence is not
proven, so it is reported as undetermined rather than absent.

**Deliberately narrow, so it does not over-fire.** Only three kinds of skip
count — the three that can name a folder. Traversal limits and non-addressable
leaves are excluded, because including them would suppress genuinely absent
names and trade one falsehood for another.

---

### R-7 — the application's security policy was widened for a component nothing used
**Severity:** high · **Area:** Library SPA + pkg/gateway
**Status: FIXED** — `9ba45abe2` (the wiring); policy widened by `a0f52947e`,
component built by `e31ff8616`

**What it meant.** Supporting video embeds required permitting a YouTube domain
in the application's own content policy. That permission shipped, on every
install, and the API advertised it. The component it was granted for was
imported by nothing but its own tests — so the policy was widened for a feature
that could not run. My own earlier commit said *"wiring follows."* It did not.

**What you get now.** A video destination in a note mounts the real player, so
the permission is earned rather than merely declared. Verified directly: the
component is imported by production code, not only by tests.

**One design detail worth knowing.** Image-form and link-form video
destinations are funnelled into the *same* dispatch at parse time rather than
handled separately, so there is one definition of "what counts as a video URL"
instead of two that can drift. The recogniser reuses the player's own parser for
the same reason.

---

### R-8 — `![[photo.png|400]]` showed the picture at full size with "400" as its description
**Severity:** medium · **Area:** Library SPA · **Status: FIXED** — `9ba45abe2`

**What it meant.** Writing a width on a picture embed did nothing. The write
side was complete and correct — it validated the number, refused it on things
that are not pictures, and composed the notation properly. The read side put
everything after the `|` into the picture's alternative-text field, so the image
rendered at its natural size with the text `400` as its description. Worse, the
same code path meant `![[song.mp3|400]]` **lost its filename**.

**What you get now.** A width on an embed is read as a width and passed to the
existing picture renderer, which already had the capability — it simply had no
caller in production, so its own test was exercising a case production never
produced. The word "width" appeared zero times in the reading file before this
change.

---

### R-9 — a broken base file told you your notation was wrong
**Severity:** medium · **Area:** Library SPA · **Status: FIXED** — `9ba45abe2`

**What it meant.** Embed a view from a `.base` file that could not be read, and
the note said *"No view named X"* — i.e. *you typed the wrong name*. The truth
was that the file could not be read at all. You would have gone looking for a
typo that was not there.

**What you get now.** The reader checks the count of views that failed to load,
which the server already reports and which the full-screen pane already used.
When every view failed to load, the note says so. This is the same class of
falsehood as R-6, pointed at a base file instead of a missing note.

**Scoped precisely, not broadly.** A *partial* failure — some views loaded, your
fragment just does not match any of them — keeps the "no view named X" answer,
which is still true of the views that did load.

---

### R-10 — three security test files ran green while asserting nothing
**Severity:** high · **Area:** e2e suite · **Status: FIXED** — `4e2ef3dbb`

**What it meant.** Three test files existed, were wired into the test plan, ran
on every CI build and reported green. All three contained a skip marker and a
body reading `// Intentionally empty`. The browser's handling of a file that
claims to be a PDF but is not — a real attack shape — was covered by zero
assertions while the dashboard said it was covered. I had flagged these earlier
as "declared, not hidden" and moved on. That was the wrong call.

**What you get now.** 15 tests and 136 assertions, run against a real gateway in
real browsers. The case with teeth serves HTML bytes named `.pdf` with no
protective policy at all — the case where a browser does try to build something
— and proves nothing executes and nothing leaves. It is paired with a positive
control serving the identical bytes as a web page, which *does* execute and
whose network calls all arrive, so the negative result is attributable to the
defence rather than to a broken test.

**Two real findings came out of it, neither smoothed over.**

1. **One browser engine does not enforce a font-loading protection at all.**
   Reproduced twice, including with Omnipus entirely out of the picture. A
   second engine returns a success then rejects the font later — so a test that
   watched only network status would have called it a pass. The requirement
   still holds and is asserted, since two of three engines refuse; but font
   loading is **not** a containment control, and ADR-067's table reads as a
   universal browser fact when it is not. The failing engine is asserted in the
   direction it was measured, so a future fix turns the file red and says so
   rather than letting the table quietly rot.
2. **A development auth bypass short-circuits authentication even when users are
   configured.** Reported separately — it is not a test defect and was not fixed
   inside a test commit.

**Two measurement corrections worth recording**, because both had produced a
false pass: the obvious way to look for an embedded viewer is blind to
Chromium's, which lives in a nested frame; and the test harness's own
request-faking bypasses a browser's cross-origin check, so the first version of
one mutation "passed" with the protection deleted. Both now run over real
sockets.

---

### R-11 — two ADR requirements were specified and never built
**Severity:** medium · **Area:** Library SPA · **Status: FIXED** — `0263b7629`

**What it meant, in two parts.**

**A dashboard could fire a query per module, all at once.** The limit of four
simultaneous view queries did not exist — a search for it across the whole SPA
found only the unrelated PDF limit. Your own Founder Cockpit has 15+ modules, and
loading-on-scroll does not help, because everything near the screen mounts
together. There is now a real queue, built in the same shape as the PDF one, with
one deliberate difference: a view query releases its slot when the request
finishes, rather than holding it for as long as the component is mounted, which
is correct for a query and wrong for a worker.

**A printed dashboard could silently be a partial one.** ADR-083 dropped print
support, and the compensating control for that decision was a notice telling you
when part of the page has not loaded yet — which also matters for browser
find-in-page, since it cannot find text in a module that has not scrolled into
view. The notice was built, tested, and **rendered nowhere**: the same shape as
R-7. It is now rendered above the reader.

---

## Summary

| ID | Title | Severity | Status | Commit |
|---|---|---|---|---|
| R-1 | A save could silently destroy another writer's version and report success | Critical | **Fixed** | `b9149a615` |
| R-2 | PDF limit measured by nothing; permanent unexplained spinner; latent freeze | High | **Fixed** | `1ad501890` |
| R-3 | A refused Library save left no audit record | High | **Fixed** | `845431acb` |
| R-4 | Unvalidated block reference allowed markdown injection into a note | High | **Fixed** | `a9c8bfbaa` |
| R-5 | Embed tool asserted an existence check it never performed | High | **Fixed** | `6a1927f69` |
| R-6 | Agents told a file that exists is missing; bare names structurally unresolvable | High | **Fixed** | `162b81ced` |
| R-7 | Security policy widened for a video component nothing imported | High | **Fixed** | `9ba45abe2` |
| R-8 | Picture width rendered as alternative text; audio embeds lost their filename | Medium | **Fixed** | `9ba45abe2` |
| R-9 | A broken base file reported as a wrong view name | Medium | **Fixed** | `9ba45abe2` |
| R-10 | Three security test files green with zero assertions | High | **Fixed** | `4e2ef3dbb` |
| R-11 | Four-query ceiling and the unmounted-content notice specified, never built | Medium | **Fixed** | `0263b7629` |

**Two findings from R-10 are NOT defects in Omnipus and remain open questions
elsewhere:** a browser engine that does not enforce font-loading protection at
all (so ADR-067's table overstates a browser guarantee), and a development auth
bypass that short-circuits authentication even with users configured, reported
separately.
