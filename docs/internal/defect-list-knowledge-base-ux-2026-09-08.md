# Defect list — knowledge base & grep UX (2026-09-08)

Findings from founder review and an agent field-test of the `grep` tool, on
`integrate/library-improvements-v0.1.1`. **This file documents; it does not
fix.** Every claim below was checked against the code — the "Evidence" line
says what was actually observed, and where a reported finding turned out not
to be a defect, that is recorded too rather than quietly dropped.

Naming follows ADR-082: the product concept is a **knowledge base**. Phase 1
of that rename (user-visible copy) has landed; identifiers, wire types and the
on-disk `.omnipus-vault` marker are still the old name and are Phase 2/3 work.

---

## Agent-facing capability gaps

### KB-1 — no way for an agent to create a knowledge base
**Severity:** high · **Area:** knowledge tools · **Reported by:** founder

An agent has sixteen `knowledge_*` tools and not one of them creates a
knowledge base. `knowledge_create` creates a **note inside an existing**
knowledge base, which makes the name actively misleading — an agent reaching
for the obvious verb gets the wrong object.

Creating one is currently possible only through the SPA (`POST
/library/{workspace_id}/vaults`, reached from the Library "+" menu), so an
agent asked to "set up a knowledge base for this project" cannot complete the
task at all.

**Evidence:** tool names enumerated from `pkg/knowledge`,
`pkg/records/knowledgefind`, `pkg/vaultprops` — `knowledge_append_section`,
`knowledge_configure`, `knowledge_create`, `knowledge_describe`,
`knowledge_edit`, `knowledge_find`, `knowledge_graph`, `knowledge_link`,
`knowledge_move`, `knowledge_read`, `knowledge_rename`,
`knowledge_restructure`, `knowledge_search`, `knowledge_set_property`,
`knowledge_tasks`, `knowledge_version_conflict`. No create-collection entry
point exists in `pkg/knowledge` or `pkg/vaultprops`. The REST route is at
`pkg/gateway/rest_library.go:133` (`case "vaults"`).

**Note for whoever fixes this:** the tool name will collide conceptually with
`knowledge_create`. Renaming that one to say it makes a *note* is the honest
fix, but it is a breaking change to a tool agents already use — weigh it
against adding a distinctly-named creation tool.

---

### KB-2 — no intuitive way for an agent to list the knowledge bases it can reach
**Severity:** high · **Area:** knowledge tools / filesystem tools · **Reported by:** founder

There is no `knowledge_list`. The only existing route to the answer is
`knowledge_describe`, which renders a `COLLECTIONS in scope (n): …` line — but
that is a side effect of describing **one** knowledge base, so an agent has to
already know about a knowledge base to discover the others. That is backwards.

The founder's suggested alternative is at least as good and possibly better:
have `list_directory` **mark the type** of each entry, so a knowledge base is
visibly distinct from an ordinary folder while browsing. The SPA already gets
this — `LibraryEntry.is_knowledge_base` was added for the icon work — so the
detection exists and simply is not exposed to agents.

**Evidence:** `pkg/knowledge/knowledge_describe.go:249`
(`renderIndexAndCollections`) emits `COLLECTIONS in scope`. `list_directory`
(`pkg/tools/filesystem.go`) contains no knowledge/marker handling — it cannot
distinguish a knowledge base from a folder. The REST listing does, via
`is_knowledge_base`.

**Options, not a decision:** (a) add a `knowledge_list` tool; (b) mark types in
`list_directory` output; (c) both — (b) helps an agent that is browsing, (a)
helps one that is not. Whichever is chosen, mounts must be covered: a
knowledge base reached through a mount is detected today and must stay so.

---

## Library UI

### KB-3 — the New knowledge base dialog asks for a location it should already know
**Severity:** medium · **Area:** Library SPA · **Reported by:** founder

The dialog has three fields: **Name** (correct), a **workspace** dropdown, and
a free-text **"Folder within the workspace (optional)"** path box. Two of the
three are wrong:

- The workspace picker duplicates state the user has already expressed by
  being *in* a workspace.
- A free-text path field asks the user to type a location they are already
  standing in, and invites typos the dialog then has to reject.

It should behave like the **New folder** dialog: create the knowledge base
**where the user currently is** in the Library, taking workspace and parent
path from context, with only a name to fill in.

**Evidence:** observed in the running product — dialog renders `Name`,
`Location` (combobox, "My Workspace"), and a textbox placeholder "Leave blank
for the workspace root". Component: `LibraryNewVaultDialog.tsx`.

---

### KB-4 — "New workspace" should not appear in the Library create menu
**Severity:** low · **Area:** Library SPA · **Reported by:** founder

Workspaces are created from the **sidebar**. Offering "New workspace" in the
Library "+" menu is a second, redundant entry point for an object that is not
a Library item, and it sits directly above "New knowledge base" where it
invites mis-clicks between two very different outcomes.

**Evidence:** observed in the running product — the Library "+" menu lists
`New vault` / `New workspace` / `New folder` / `Upload files` / `Add a folder
from your Mac` / `Manage mounted folders`. Component:
`LibraryCreateMenu.tsx`. The sidebar's own inline create-workspace row is the
sanctioned path.

---

---

### KB-5 — the active workspace is collapsed in the sidebar
**Severity:** low · **Area:** Sidebar SPA · **Reported by:** founder (UAT run)

The sidebar's workspace accordion opens with **every** workspace collapsed,
including the one currently active. The workspace you are working in is the one
whose sessions you are most likely to want, so it should be expanded by
default; today it takes an extra click every time the app loads.

**Evidence:** `src/components/layout/Sidebar.tsx` — expansion state is
`useState<Set<string>>(new Set())`, i.e. empty on mount, and the row computes
`isExpanded = expandedWorkspaceIds.has(project.id)` independently of
`isActive = activeWorkspaceId === project.id`. Nothing seeds the active
workspace into the set, and nothing re-seeds it when the active workspace
changes.

**Worth deciding when fixing:** whether switching workspaces should also expand
the newly-active one (and whether it should collapse the previous one), and
whether a manual collapse of the active workspace must survive a reload — a
naive "always expand the active one" would fight a user who deliberately
collapsed it.

---

### KB-6 — search results give no signal about what is relevant, and show too little to judge
**Severity:** medium · **Area:** Library SPA (+ contract for part of it) · **Reported by:** founder (UAT run)

Searching a real knowledge base returns a long flat list of title-plus-one-line
rows. Nothing indicates which document actually matters, the matched term is
not visually marked, one line is rarely enough to judge a hit, and a row does
not say whether it came from the filesystem or a knowledge base.

Four distinct causes sit behind the one complaint, and they are NOT equally
expensive:

**(a) One hit = one matching line.** A note with 30 matching lines becomes 30
rows. This is most of the perceived volume. Collapsing to one row per
document, with a match count and the best excerpt (expandable), is the single
biggest reduction available and needs no ranking work. The match count is
itself a relevance signal people read instinctively.

**(b) Relevance is computed and then thrown away — for knowledge bases.**
`IndexHit.Score` is a real BM25 score (`pkg/knowledge/index.go:221`) but it is
NOT on the wire: neither `VaultSearchNoteHit` nor `FileSearchHit` carries a
score. The UI cannot order by relevance or show it, even though the engine
knows.

**File search has no ranking at all, by design.** `Result.Hits` is sorted
path-lexicographic-then-line for determinism (spec A3, `pkg/filegrep/filegrep.go`),
so the most relevant file can legitimately be last. Ranking it is a SPEC
DECISION, not a tweak: the deterministic order is what makes truncation honest
and results reproducible. If relevance ordering is introduced, keep the
path-lexicographic order as the tiebreak so equal-relevance results stay stable.

**Do not display a raw BM25 number.** The code states it directly: scores are
"comparable only within one result set — BM25 is not normalised across queries
or across indexes" (`pkg/knowledge/index.go:223-224`). A number like `0.83`
would look authoritative and mean nothing between two searches. Ordering, or at
most a coarse strong/weak marker, is honest; a number is not.

**(c) Too little context — and for FILE search this is a one-line fix.**
`FileSearchHit` already carries `context_before` and `context_after` (maxItems 5
each) and the engine populates them. The SPA asks for
`context_lines: 0` (`src/components/library/search/useFileSearch.ts:171`).
Setting it to 1 yields the match plus a line either side with NO backend,
contract or engine change.
`VaultSearchNoteHit` is different: it has only a single `snippet` and no context
fields, so multi-line for knowledge base hits is real work — widen what the
indexer produces, or add context fields to the contract.

**(d) The matched term is not highlighted, and no offsets exist on the wire.**
Two options: highlight CLIENT-SIDE (the SPA knows the query — cheap, works for
both search kinds today, but approximate under smart/insensitive case and
cannot know which occurrence the engine actually matched), or carry match
OFFSETS from the engine (exact and honest, but a contract change plus engine
work). Client-side first is the sensible order.
Colour note: the founder asked for yellow; yellow reads as "warning" elsewhere
in this UI. The Forge Gold accent is the established emphasis colour — confirm
which is wanted before implementing.

**(e) No per-row source marker.** A row does not say whether it is a filesystem
result or a knowledge base result. The row already knows which shape it
rendered from, so a marker is cheap.
**Worth verifying first:** today these are separate MODES — a plain folder
renders file results, a knowledge base renders the tabbed
Notes/Records/Views/Attachments view — so they should not be mixing. If the
founder is seeing an ambiguous mixed list, that is a distinct defect and should
be reproduced before this is treated as presentation-only.

**Suggested bundle (not a decision):** (c) context_lines -> 1, (d) client-side
highlight, (e) source marker, (a) collapse per document, (b) order by the score
that already exists. Together they address both halves of the complaint — too
many results, and no signal about which matter.

## `grep` tool — agent field test

### DEFECT-G1 — a path pointing at a file produces a false "not found" error
**Severity:** medium (reported low; raised — the message misleads rather than
merely refusing) · **Area:** `pkg/tools/grep.go` · **Reported by:** agent field test
**Status: FIXED** — merged, verified. Scoping to a single file now works
(the file's parent is opened as the confined root and the walk is narrowed to
that one entry, with globs and ignore rules still applying). Remaining
refusals name what is actually true: *does not exist* / *permission denied* /
*wrong kind* (e.g. a FIFO). A carved-out secret named directly as `path`
still returns zero hits, leaking neither content nor existence.

Root cause, established by experiment rather than assumed: `(*os.Root).OpenRoot`
on a regular file returns a BARE error — `errors.Is` matches neither
`fs.ErrNotExist` nor `syscall.ENOTDIR` — so the old blanket wrap could not
have distinguished the cases even in principle.

Pointing `path` at a single file rather than a directory returns:

```
path "uat-grep/sample.txt" not found in your workspace
```

with an underlying errno of *not a directory* — for a file confirmed to exist
seconds earlier. The message is false, and it contradicts the errno embedded in
the same sentence. The tester hit it three times before deducing the
constraint.

**Evidence:** `pkg/tools/grep.go` workspace-scoped branch wraps **every**
`wr.OpenRoot(scope)` failure as `path %q not found in your workspace: %w`.
`os.Root.OpenRoot` on a regular file fails with `ENOTDIR`, so an existing file
is reported as missing.

**Direction taken:** scoping to a single file will be **supported** — it is the
natural action, attempted three times — and any remaining refusal must
distinguish *does not exist* / *wrong kind* / *permission denied* / *escapes the
workspace*. A file scope must not bypass the secret carve-out.

---

### OBS-G1 — a glob that matches nothing fails silently
**Severity:** medium (reported as an observation; raised to defect — see below)
· **Area:** `pkg/tools/grep.go` · **Reported by:** agent field test
**Status: FIXED** — merged, verified. A zero-hit result now states how many
entries the globs excluded before any match was attempted, and explains the
anchoring rule when the excluded count dominates. The count also appears in
the stats footer on non-empty results. Bare filenames are still NOT
auto-anchored — pinned by `TestGrepTool_GlobDoesNotAutoAnchor`.

`include_globs: ["spike.txt"]` matches nothing; only `**/spike.txt` works. The
result is a silent zero — indistinguishable from "the term genuinely is not
present".

Raised above the reporter's own scoring because this engine's stated contract
is that **every skip is observable, never silent**. A filter that discards the
entire corpus without saying so is precisely what that contract exists to
prevent.

**Evidence:** doublestar patterns match the whole relative path, so a bare
filename only matches at the root. `Stats.FilesFilteredGlob` already counts
glob-rejected entries; `renderGrepResult` in `pkg/tools/grep.go` does not
surface it.

**Direction taken:** make the filtering visible rather than auto-anchoring bare
filenames — implicit pattern rewriting would surprise a different user later.

---

### DOC-1 — concurrency doc reported inaccurate — **NOT A DEFECT**
**Severity:** none · **Reported by:** agent field test · **Status: closed, no change**

Reported as the documentation being wrong because four concurrent searches all
succeeded where the doc promises a busy rejection at the third.

Checked: the tool genuinely acquires the shared two-slot walk semaphore with a
two-second wait and releases it (`pkg/tools/grep.go`, `filegrep.TryAcquire` /
`filegrep.Release`). A third search is refused only if two others are **still
running** when it starts; over a small corpus each search completes in
milliseconds, so the four calls never overlapped. The documented behaviour is
accurate and the cap is covered by a passing test that creates real contention
(`TestLibraryFilesSearch_SemaphoreIsSharedWithTheAgentTool`).

Recorded rather than dropped, because "the tool behaved better than documented"
is a conclusion worth correcting: it behaved exactly as documented, under a
test that did not reproduce the documented condition.

---

## Summary

| ID | Title | Severity | Status |
|---|---|---|---|
| KB-1 | No agent-facing knowledge base creation | High | Open |
| KB-2 | No intuitive way to list reachable knowledge bases | High | Open |
| KB-3 | New knowledge base dialog asks for known context | Medium | Open |
| KB-4 | "New workspace" shown in Library create menu | Low | Open |
| KB-5 | Active workspace collapsed in the sidebar | Low | Open |
| KB-6 | Search results: no relevance signal, too little context, no source marker | Medium | Open |
| DEFECT-G1 | `path` at a file gives a false "not found" | Medium | **Fixed** |
| OBS-G1 | Glob matching nothing fails silently | Medium | **Fixed** |
| DOC-1 | Concurrency doc claim | — | Closed — not a defect |

---

## Adjacent gap found while fixing DEFECT-G1 — not a defect, needs a decision

The REST Library file-search surface does **not** share either grep defect: it
`Stat`s first and returns a real `400 "path is not a directory"` (never a false
"not found"), and it already puts `files_filtered_glob` on the wire. What it
lacks is **single-file scoping** — a `path` naming a file is rejected before the
search runs, so the human search bar cannot do what the agent tool now can.

That is a feature-parity gap, not a bug. Recorded for a product decision rather
than fixed silently.
