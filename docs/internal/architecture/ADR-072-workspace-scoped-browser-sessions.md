# ADR-072 — The browser belongs to the conversation, not to the agent

- **Status:** Proposed
- **Date:** 2026-08-31
- **Supersedes:** nothing. **Replaces** an earlier, unpushed draft that also
  claimed ADR-072 ("Region-aware transport for the live browser") — deleted in
  the same commit as this file lands. §7 records why.
- **Related:** ADR-038 (live browser panel, human takeover), ADR-041 (tabs),
  ADR-044 (single listener), ADR-057 (routing vs transcript session split),
  ADR-062 (WebRTC connectivity tiers)

---

## 1. Context

### 1.1 What the operator hit

Reported 2026-08-31 on `omnipus-uat-swimlane`, verbatim:

> hey jim check the browser tools and what i have open in the tab
>
> **Jim:** There's nothing open right now — the browser has zero tabs. The
> session is empty.
>
> it is mias tab
>
> **Jim:** The browser session is shared across the workspace — there's no
> separate "Mia's tab." Right now the shared browser is completely empty with
> zero tabs open.

The operator was correct and the agent was wrong, twice: the tab existed, and
the browser is **not** shared across the workspace.

The sequence that produced it: the operator opened the chat with Mia (the
default agent), opened the browser panel, browsed — so the tab was recorded
against Mia — then switched the chat to Jim. Nothing moved the tab.

### 1.2 What the code actually does

`AgentLoop.BrowserManagers()` returns a **slice** — one `BrowserManager` per
agent (`pkg/agent/loop.go`). Each manager owns its own `sessions` map
(`pkg/tools/browser/manager.go`), and every browser tool reads from its own
manager under one hardcoded key:

```go
const DefaultSessionID = "default"          // pkg/tools/browser/tools.go
tabs, activeIdx, err := t.mgr.ListTabs(defaultSessionID)   // tabs.go
```

In coordinator mode all managers share **one Chrome** — `allocCtx` is the
coordinator's `rootCtx`, and managers create chromedp child contexts against
that single CDP pipe. So the tabs physically coexist in one browser while each
manager tracks only the ones it created. The live panel confirms the binding
in its own log prefix: `browser-webrtc[mia]`.

Jim was not looking at an empty shared browser. He was looking at his own
empty book, in a library that had the page open on the next desk.

### 1.3 Why he stated it so confidently

Two independent reasons, both in the code rather than in the model.

**The tool descriptions assert the wrong model.** `ListTabsTool.Description()`:

> "List every open tab in **the shared browser session**"

Every browser tool says "shared". The agent reported what its tools told it.

**And the failure is unobservable.** `BrowserManager.ListTabs`:

```go
se, ok := m.sessions[sessionID]
if !ok {
    return nil, 0, nil        // "no browsing context" == "no tabs open"
}
```

"I have no browsing context" and "the browser has no tabs" return the same
value. Nothing downstream can distinguish them, so no agent — and no test —
can notice the difference. This is the `docs/internal/false-green-patterns.md`
shape: a wrong answer that is indistinguishable from a right one.

### 1.4 The existing adoption path does not help

`ReconcileTabs` looks like a discovery mechanism and is not one. It adopts an
untracked target only when that target's **opener is already ours**:

```go
if info.OpenerID == "" {
    continue // not opened by a page — a top-level target, not ours to adopt
}
if _, openerIsOurs := tracked[info.OpenerID]; !openerIsOurs {
    continue // opened by a target outside this browsing context
}
```

That is deliberately scoped to `target="_blank"` pop-ups from an agent's own
tabs. It cannot adopt another agent's tab, and relaxing its
`len(se.tabs) == 0` precondition would not make it able to. Recorded here
because that precondition looks like the fix and isn't.

---

## 2. Decision

**A browsing context belongs to a conversation. One browser manager serves the
workspace.**

Two changes, in this order:

1. **One `BrowserManager` per workspace**, shared by every agent, instead of
   one per agent. Today the correct key would still be looked up in the wrong
   book.
2. **Key browsing contexts by the routing session id** — the conversation —
   instead of the constant `"default"`.

### 2.1 The keys already exist and already mean the right thing

Tool context already carries both, and ADR-057 already gave them exactly the
semantics this needs — a delegated sub-turn **inherits** `routingSessionID`
and gets its **own** `transcriptSessionID`:

| Key | Helper | Scope it produces |
|---|---|---|
| Routing session | `tools.ToolSessionKey(ctx)` | The conversation. Survives switching which agent answers, and is inherited by delegated sub-turns. |
| Transcript session | `tools.ToolTranscriptSessionID(ctx)` | One turn/delegation. Distinct per delegated child. |

So the **operator-facing browser is keyed by the routing session**, and
unattended delegated work that must not touch the operator's tab can be keyed
by the transcript session instead. No new identity concept is introduced.

Every browser tool already takes a session id on every call: `defaultSessionID`
is passed at **9 call sites in `tools.go`** and **14 across
`pkg/tools/browser/`** (non-test), and `tabs.go` passes it to `ListTabs`.
The parameter is threaded, wired and currently wasted on a constant.

Six tool descriptions also contain the literal phrase "the shared browser
session" and must change with the behaviour, not after it.

### 2.2 Fallback when there is no conversation

A browser call outside any chat (a scheduled trigger, a heartbeat, a CLI
invocation) has no routing session. Those keep a context keyed by the constant,
as today. That path must be explicit, not an accidental empty string —
an empty key silently colliding every unattended run into one shared browsing
context is the same class of defect this ADR exists to remove.

### 2.3 The silent zero is removed, independently

`ListTabs` must distinguish "this conversation has no browsing context yet"
from "the browsing context has no tabs". Both are legitimate states; reporting
them identically is what made §1.1 unobservable. The tool's description must
also stop claiming "shared" unless and until it is true.

---

## 3. Acceptance criteria

The operator's three cases, which are the test plan:

| # | Case | Required behaviour |
|---|---|---|
| 1 | Mia browses; operator watches and takes over | Works as today — unchanged |
| 2 | Operator switches the chat from Mia to Jim mid-session | Jim sees and drives the same tabs. No handover step, no command |
| 3 | Operator browses first, **then** asks an agent to take over | The tab was never owned by "whoever happened to be on screen"; any agent in that conversation sees it |
| 4 | An agent delegates unattended background browsing | The sub-agent does not hijack the tab the operator is reading |
| 5 | `browser_list_tabs` with no browsing context | Says so. Must not be indistinguishable from an empty tab set |

Case 2 and case 3 are the ones broken today; 1 works; 4 and 5 are the
regressions this design must not introduce.

---

## 4. Consequences

**Gained**

- Switching agents mid-conversation stops silently stranding the browser.
- The tool descriptions become true, so agents stop asserting a false model to
  operators.
- One less per-agent object; the shared Chrome gains a single owner of record.

**Lost / risked**

- **Concurrency.** Two agents in one conversation can now reach the same tab.
  Today they are isolated by accident. `controlledResult` already arbitrates
  human-vs-agent control (ADR-038 D6); agent-vs-agent within one conversation
  is a case it was not written for and must be re-read against this change.
- **Blast radius.** Collapsing per-agent managers touches every browser tool
  call site and the live panel's session resolution.
- **Idle reaping.** Session lifetime becomes conversation lifetime, not agent
  lifetime. Reaping rules need re-deriving, or a long chat pins a Chrome tab
  indefinitely.

---

## 5. Alternatives rejected

**Explicit "hand over the browser" command.** Makes the operator do bookkeeping
for an implementation detail. Fails case 3 outright — at the moment the
operator starts browsing, there is no agent to hand over *from*.

**Keep per-agent browsers, fix only the wording.** Honest, and leaves cases 2
and 3 broken. Rejected as documenting a defect rather than removing it.

**Let `ReconcileTabs` adopt any untracked target.** Would make every agent
silently absorb every other agent's tabs, including unattended delegated ones,
with no ownership model at all. §1.4 has the mechanics.

---

## 6. Open questions

- Does `controlledResult` need an agent-vs-agent arm, or is "one conversation,
  one driver" sufficient?
- Should the live panel's WebRTC session (`browser-webrtc[<agent>]`) also
  re-key to the conversation, or is the agent label harmless once tabs are
  conversation-scoped?
- Reaping: what ends a conversation's browsing context?

---

## 7. What this file replaces, and why that work stopped

An earlier draft under this number proposed **region-aware transport**: media
segment forwarding for `<video>`, dirty-rectangle tiles for static UI, and
per-rectangle encoding for canvas. It is deleted rather than superseded,
because it was never ratified — two independent reviews and a test-integrity
audit all declined it as a build authorisation, on the grounds that cheaper
fixes had not been tried.

They were right. On 2026-08-31, `/proc` sampling on `omnipus-uat-swimlane`
(performance-2x, 2 dedicated cores, no GPU) measured, with a viewer attached:

```
idle, no capture                 chrome   0.1% of a core, machine  0.4%
boot warm capture, NO viewer     chrome  57.0% of a core, machine 29.1%
operator watching + interacting  chrome 150-192% of a core, machine 85-99%
```

The machine was full. Video looked fine (one-way media tolerates a busy box)
while input — which is round-trip — was starved: scrolling arrived in bursts,
clicks were dropped, and ICE consent checks missed their deadline. A single
change, capping the capture pixel budget (commit `08d21393`), halved the
warm-capture cost (57% → 29% of a core in the identical condition) and the
operator's verdict on the next run was "scrolling video audio is great".

**The expensive rebuild was for a problem a bound solved.** The research
behind it is not lost — `docs/internal/spikes/browser-streaming/` keeps the
measured evidence (media replication works on YouTube with audio in sync;
Safari's AV1 gap is solvable), which stands on its own and can be reopened
under its own ADR if in-panel media playback ever becomes a requirement.

Recorded so it is not re-derived: the industry has already run this
experiment. Browserbase, Steel.dev and Kernel each built DOM-mirroring session
replay and each replaced it with pixels. Menlo Security patented DOM Mirroring
and then built a compositor display-list engine to replace it. Cloudflare's
draw-command path requires a patched Chromium; Chrome moves to a two-week
release cadence on 2026-09-08, and Playwright and Puppeteer both drive
Chromium in production with zero Chromium patches. Nothing above this ADR's
scope should be attempted without re-reading that.
