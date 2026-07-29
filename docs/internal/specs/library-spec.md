# Library — spec

Status: driving implementation on `feat/library`. Supersedes the "Media Library" surface.

## 1. Why

The current Media Library stores uploads as UUID blobs in `workspaces/<id>/media/` behind a
`manifest.json`, exposes a workspace-scoped list+delete UI, and is **invisible to agents**.
A real UAT session (2026-07-29) produced the motivating failure: the user uploaded a PPTX,
asked Ray about it, and Ray — correctly — reported the workspace was empty, then guessed
filenames (`Elicify.pptx`, `elicify.pptx`) and failed. The real file was
`Copy of elicify_company_profile.pptx`, stored as `media/755c0a0f-…`.

Four independent defects, each confirmed by code inspection:

| # | Defect | Location |
|---|---|---|
| D1 | A caption-less attachment turn is dropped **entirely** — media never reaches the LLM | `pkg/agent/context.go:965` (`if strings.TrimSpace(currentMessage) != ""`) |
| D2 | The persisted transcript never records attachments | `pkg/gateway/websocket.go:1637-1644` builds `TranscriptEntry` and never sets `Attachments` |
| D3 | No agent tool can list or read Library entries | zero imports of `pkg/media/library` under `pkg/tools/`, `pkg/sysagent/` |
| D4 | `media/` is structurally unreachable by agent file tools | `media/` is a **sibling** of `work/`; tools open `os.Root` at `work/` and cannot escape (ADR-046) |

D4 is deliberate — ADR-046 keeps `AGENT.md` and `.omnipus/` out of agent reach — so the fix
must not be "point the agent at `media/`".

**Open question, deliberately unresolved:** in the same session an image WAS seen by the agent
despite also being caption-less, which D1 alone does not explain. Do not build on the premise
"images work, documents don't" until this is reproduced. It may indicate a second assembly path.

## 2. Decisions

**D-1 — Uploads land as real, named files at `work/.library/` (operator decision).**
Chat uploads are written to `workspaces/<id>/work/.library/<filename>` (de-duplicated with a
numeric suffix on collision), not only as an opaque blob. Being inside `work/` is what makes the
agent requirement satisfiable: `work/` is exactly the directory agent file tools are rooted at,
so `read_file(".library/<name>")` works with no carve-out and no new policy. The dot prefix
namespaces library-managed files away from the agent's own working files and hides them from
casual listing, without putting them out of reach.

Two traps this creates, both must be handled:
- Directory-listing helpers commonly skip dotfiles by default. If `library_list` or any agent
  file tool filters them, the agent goes blind to exactly the files this fix exists to expose.
- The UI must be able to show them: see D-8.

Rejected: granting agent tools a carve-out into `media/`. That punctures the ADR-046 rooting
invariant for every agent and every turn, to solve a problem that a real path solves.

**D-2 — The Library is a file explorer over workspace trees, not a blob list.**
Entries are paths, not UUIDs. A workspace's Library root IS its `work/` directory — the whole
tree, with `.library/` simply one (hidden-by-default) directory inside it. This is what makes
rename/copy/move/download/preview/edit coherent, and lets one component serve both the virtual
root and the workspace-scoped view.

**D-3 — Two entry points, one component.**
- From the **sidebar**: a virtual root listing every workspace as a top-level node.
- From **chat / header bar**: opens scoped to the active workspace, showing its `work/`.

**D-8 — Hidden entries are shown on demand (operator decision).**
"Hidden" means the name begins with a dot — defined once, in the contract, so client and server
cannot drift. Listing takes `include_hidden` (default false); each entry carries `is_hidden` so
the UI can style them distinctly even when shown. The explorer has a **Show Hidden** toggle.

**D-9 — Copy and move, including across workspaces (operator decision).**
The user can reorganise files, not merely rename in place, and may transfer between workspaces.
Source and destination workspace ids are both explicit in the request body, so the two-workspace
nature is visible in the schema. **Permitted for the authenticated UI user only — never for
agents**, which stay confined to their own workspace. Enforced server-side; stated in the
contract so nobody later wires an agent path to it.

**D-4 — Slideout panel, not a modal; pop-out to a tab.**
Mirror `BrowserLivePanel` exactly: a docked flex `<aside>` sibling in `AppShell` (NOT a Radix
`Sheet` — that variant was retired by operator direction 2026-07-16, "do not reintroduce
without an ADR"), a `ui.ts` store slice, a `/_app/library` pop-out route opened with
`window.open('/#/library?…', '_blank', 'noopener,noreferrer')`, and a `BroadcastChannel`
handoff (own channel name) so closing the pop-out re-docks the panel.

**D-5 — Reuse the chat renderers; introduce an editor.**
- Markdown view: `HistoricalMessageMarkdown` (`content: string`) — already the static-string renderer.
- Mermaid: `<MermaidDiagram code={…} />` — drop-in.
- Code view: `SyntaxHighlighter` (react-shiki), already bundled.
- **Editing**: CodeMirror 6 via `@uiw/react-codemirror`, lazy-loaded. ~50-300kB tree-shaken
  vs Monaco's 2-5MB. The SPA is `go:embed`-ed into the binary and entry bundle was deliberately
  cut 1.8MB → 0.27MB; Monaco would undo that.
- Video: no player exists anywhere (the only `<video>` is the WebRTC sink). Build a plain
  `<video controls>` wrapper.

**D-6 — Fix the chat/history code-highlighting parity gap as part of this.**
`HistoricalCodeBlock` (`historical-markdown.tsx:41`) renders plain `<pre><code>` while the live
path uses Shiki, so highlighting vanishes when a message finalizes or the page reloads. Route
history through the same Shiki component. Shiki is already in the bundle; grammars lazy-load
per language. This is the second parity drift between these two renderers (Mermaid was the
first, fixed in `3ed49f01`) — the shared module covers every element **except** block code.

**D-7 — Naming.** The sidebar section currently titled "Library" (Agents / Skills & Tools /
Connectors) is renamed **Assets**; the new nav entry takes the name **Library**. The Assets
section needs an icon people associate with files (operator decision) — Phosphor, matching the
existing sidebar icon set.

## 3. Agent behaviour (the requirement)

1. **Nothing the user submits is silently dropped.** Remove the empty-caption gate: a turn with
   media and no text must still reach the model.
2. **Announce the upload.** The turn carries a synthesized, model-visible line naming each
   uploaded file and its workspace-relative path, e.g.
   `[user uploaded: .library/Copy of elicify_company_profile.pptx]`. The agent must be able to
   pass that exact string to `read_file` and have it succeed — that round trip is the acceptance
   test for the whole fix, not the presence of the text.
3. **Persist it.** `TranscriptEntry.Attachments` is populated, so a later turn — or a different
   agent after a handoff, as in the UAT — can still see what was uploaded.
4. **Give the agent a tool.** `library_list` / `library_read` over the workspace's own files,
   so it can find a file by name instead of guessing.

## 4. Scope of preview/edit

| Type | View | Edit |
|---|---|---|
| Images | `<img>` | — |
| Video | `<video controls>` | — |
| Markdown | `HistoricalMessageMarkdown` + Mermaid | CodeMirror (markdown source) |
| Mermaid | `MermaidDiagram` | CodeMirror |
| Code / any text | Shiki | CodeMirror + highlighting |
| Everything else | metadata card + download | — |

## 5. Constraints

- **Contract-first (Constraint #8).** Every new REST type goes in `contracts/components/schemas/`
  and `contracts/openapi.yaml` **before** Go/TS, then `make gen-contracts`, artifacts committed
  in the same commit. Two existing drifts to fix rather than carry forward: the attachments
  endpoint returns a body its spec declares 204-empty, and `/upload`'s workspace-library routing
  is undocumented.
- The wire-type lint covers **all of `src/lib/**`**, not just `api.ts`/`ws.ts`.
- Path safety: every Library path operation resolves inside the target workspace root; no `..`
  escape, symlinks not followed out of the root.
- Dark-first Sovereign Deep tokens (`var(--color-surface-*)`, Forge Gold accent). No emoji.
