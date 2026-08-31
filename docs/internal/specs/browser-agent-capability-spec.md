# Spec — Browser: the agent-facing capability surface (ADR-072 **D2 only**)

- **Source ADR:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md`, **§D2.0–D2.11 only**. D1 (ownership / workspace re-keying) is a **separate spec by a sibling agent** — this document never decides an ownership question and marks every D1 dependency explicitly.
- **Round-1 grill:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions-review.md` (BLOCK, 26 findings). C5, M8, M9, m7 and the STRIDE rows for `browser_upload_file` / the AX snapshot are D2 findings and are folded in below.
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` · **Branch:** `feat/browser-streaming-performance` · **HEAD at spec time:** `077c5237`
- **Status:** Draft for grill-spec → implementation.
- **Verification posture:** every `file.go:line` below was read in this worktree at `077c5237`. Where this spec **contradicts the ADR**, the contradiction is called out inline and repeated in §12 — the ADR carries at least four claims about existing code that do not reproduce.

---

## 1. Overview / Actors / Scope

**Problem.** The browser tool surface lets an agent *read* a page and lets it *fail* on one. It ships eleven tools (`register.go:65-81`). A `<select>` cannot be operated at all. There is no Enter key, no hover, no file attach, and a page that calls `alert()` stops the tab answering CDP with no way out. Targeting is CSS-or-visible-text only; the CDP Accessibility domain is unused (**verified: zero occurrences of `getFullAXTree`/`queryAXTree`/`cdproto/accessibility` across `pkg/` and `src/`**). And the actionability contract is one quarter built: `tools.go:257` (click) and `:461` (type) prepend `chromedp.WaitVisible`, `:685` uses `WaitReady`; nothing anywhere checks enabled-ness, positional stability, or whether a click would actually land on the element rather than an overlay.

**Solution (ADR-072 D2).** Complete the surface along four axes, all additive:

1. **Find** — role + accessible name as a third locator alongside CSS and visible text, sourced from CDP Accessibility, wired into the *same* resolution seam (`text_selector.go:710`) so every action tool inherits it.
2. **Wait** — finish the actionability gate (visible → stable → enabled → hit-testable) in one shared pre-action path, with an error that names *which* condition failed.
3. **Act** — six new tools: `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file`, `browser_handle_dialog`, `browser_snapshot`.
4. **Route** — the `file://` rejection names the supported alternative in the error, not only in a tool description read hundreds of tokens earlier.

**Actors.** `resolveActionSelector` / `resolvePseudoOnlySelector` (`text_selector.go:710`, `:676`) — the target-resolution seam every action tool already funnels through; the eleven tool structs (`tools.go`, `tabs.go`); `BrowserManager` (session/tab ownership, listener install at `manager.go:2578`); `BrowserManager.ValidateURL` (`manager.go:684`); the tool-policy seeding pair (`pkg/config/defaults.go:276-287`, `pkg/coreagent/core.go`); the manifest tier authority (`pkg/tools/manifest.go`); the coverage validator (`pkg/config/validate.go:448`).

**In scope (v1):**
- Role + accessible-name resolution (D2.1), through the existing marker-attribute seam.
- The four-condition actionability gate (D2.2) with a closed, named failure set and a **≤150 ms p95** budget.
- The six new tools (D2.3, D2.4).
- The `serve_web` pointer in the `file://` error (D2.5).
- Manifest tier assignment + the two drift tests it pins (D2.8).
- Tool-policy seeding for all six, on **every** seeded agent, so boot does not abort (D2.9, Hard Constraint #6).
- The per-browsing-context write lease (D2.10), keyed by whatever key D1 lands on.
- The snapshot's information-disclosure posture (D2.11 bullet 3).

**Out of scope, and why:**
- **All of D1** — ownership, workspace re-keying, live-panel manager resolution, the workspace-less fallback, delegated-sub-turn isolation. Sibling spec.
- **D2.11 bullets 1 and 2** (elevation-of-privilege disclosure in the team-editing UI; the browsing-context-creation audit event). Both act on a *browsing context* whose key D1 decides; specifying them here would fork the decision. Named as an integration boundary in §6.
- **D2.6's own exclusions**: replacing chromedp with playwright-go (Hard Constraint #1); network interception, frame targeting, drag-and-drop, cookie/storage manipulation.
- **Human-facing dialog UX.** `browser_handle_dialog` is agent-facing. A human who takes the wheel on a tab with an open dialog is wedged today and stays wedged after this work — see §6 and US-12's note.

---

## 2. Existing Codebase Context

### 2.1 Symbols involved

| Symbol | Role | Context (verified at `077c5237`) |
|---|---|---|
| `resolveActionSelector` (`text_selector.go:710-724`) | **modifies** | The seam. Takes `(selector, text)`, returns `(target, cleanup, err)` where `target` is a CSS string — either the caller's own selector, or the internal marker selector `[data-omnipus-tsel="<tok>"]` (`textMarkerAttr`, `:56`). Role/name enters **here** and returns the same marker shape, so every downstream `chromedp` `ByQuery` action is untouched. |
| `resolvePseudoOnlySelector` (`text_selector.go:676-690`) | **modifies** | The second entry point — `browser_type` uses this, **not** `resolveActionSelector`, because its `text` arg is the value to type, not a locator (`tools.go:406-409, 444`). Role/name must be added to **both** or `browser_type` silently misses the new locator. |
| `wrapTextMatch` (`text_selector.go:623`) | **reuses** | Shared `(marker, err) → (target, cleanup, err)` adapter. The AX path returns through it unchanged. |
| `displayLocator` / `scrubMarkerFromError` (`text_selector.go:208`, `:223`) | **modifies** | Error-surface helpers. `displayLocator` must learn to render `role=button name="Submit"`; `scrubMarkerFromError` needs no change (it scrubs the marker attr, which the AX path also uses). |
| `ClickTool.Execute` (`tools.go:226-...`, wait at `:257`) | **modifies** | `WaitVisible` → the new actionability gate. |
| `TypeTool.Execute` (`tools.go:422-...`, wait at `:461`) | **modifies** | Same; note the `clear` arg's `SetValue`+`SendKeys` sequence at `:461-465` must run *after* the gate, not before. |
| `GetTextTool.Execute` (`tools.go:651-...`, `WaitReady` at `:685`) | **no wait change** | Read-only. `WaitReady` (DOM presence, 8 s budget `getTextWaitTimeout`, `tools.go:48`) is deliberate — `<title>` is present but never visible (`tools.go:679-684`). The gate is for **action** tools; forcing it here would reintroduce the documented ~30 s hang. Gains the role/name locator only. |
| `WaitTool.Execute` (`tools.go:770-...`, `:792`, `:810`) | **modifies (locator only)** | Gains role/name. Its own `WaitVisible` at `:810` stays — `browser_wait`'s contract *is* visibility, not actionability. |
| `controlledResult` (`tools.go:962-978`) | **reuses + extends** | Returns `{"deferred": true, "reason": …}` as a **non-error** result when a human holds the live view (`mgr.Live().IsControlled`). Four callers in `tools.go` (`:119, :232, :429, :879`); its doc comment names seven total (the three tab tools are in `tabs.go`). The D2.10 write lease returns the **same shape** — no prompt rewrite. Every new *action* tool must call it; `browser_snapshot` must not (read-only, matching `browser_screenshot`/`browser_get_text`/`browser_wait`, `tools.go:967-970`). |
| `capGetText` / `maxGetTextChars` (`tools.go:20-35`) | **reuses** | `maxGetTextChars = config.DefaultBuiltinSuccessCap` = 64,000 (`pkg/config/context_settings.go:80`). ADR-066 B-15 / FR-014, pinned by `per_tool_cap_alignment_test.go:11-38`. **`browser_snapshot` must obey the identical cap and mirror that test.** |
| `BrowserManager.ValidateURL` (`manager.go:684-707`) | **modifies** | `blockedSchemes` (`:673-681`) covers `file`, `javascript`, `data`, `chrome`, `chrome-extension`. **One** format string at `:695` serves all five: `"browser: %s:// URLs are blocked for security reasons"`. Only `file` has a supported alternative, so the pointer must be scheme-specific, not appended to the shared string. |
| `installTargetListenerLocked` (`manager.go:2578-2590`) | **modifies** | Installs `chromedp.ListenTarget` on **`se.tabs[0]` only**, deliberately — `Target` discovery is browser-global so one listener suffices (`:2569-2574`). **`Page.javascriptDialogOpening` is per-tab, not browser-global** — a dialog listener installed on tab 0 alone will not see a dialog on tab 2. This is a load-bearing structural difference the ADR does not mention. |
| `handleTargetEvent` (`manager.go:2607`) | **reuses (pattern)** | Runs synchronously on the CDP dispatch goroutine and must never block or call `chromedp.Run` inline (`:2597-2606`). The dialog listener inherits this discipline verbatim. |
| `BrowserBuiltinMetadata` (`metadata.go:36-51`) | **modifies** | Eleven metadata-only instances, nil `*BrowserManager`. Feeds `buildKnownBuiltinToolNames` (`pkg/gateway/gateway.go:1261-1290`) → the coverage universe. **Six additions required.** |
| `RegisterTools` (`register.go:41-84`) | **modifies** | Six `RegisterReplacing` calls. `RegisterReplacing`, not `Register` — the hot-reload rationale at `:54-63` applies identically. |
| `allStaticToolNames` browser block (`pkg/coreagent/core.go:386-389`) | **modifies** | Six additions. **Mandatory**: `validateOverrideKeys` (`core.go:436-452`) **panics** on an override key absent from this list, so Jim/Ray's new entries would panic at first seed without it. |
| `denyAllThenOverride` (`core.go:466-478`) | **reuses** | Starts every name at `deny`, then applies overrides. **This is why Mia and Ava need no per-agent edit** — their deny is automatic once the six names are in `allStaticToolNames`. The ADR's D2.9 table implies four hand-edits; two of them are already free. |
| `tightenGlobalCeiling` (`core.go:491-497`) | **inherits** | `IDWorker` (`core.go:605`) returns a **sparse** map — everything absent inherits the **global** ceiling. So `IDWorker`'s posture for the six is whatever `defaults.go` says, with no Worker-specific edit. Decided explicitly in §11 FR-021. |
| Explorer / Researcher browser grants (`core.go:756-760`, `:782-786`) | **modifies (decision required)** | **Two seeded agents the ADR's D2.9 table omits entirely.** Both are granted the full 11-tool browsing surface today. Left alone they land at `deny` for all six — an Explorer that can click but cannot operate a dropdown. |
| `ValidateToolPolicyCoverage` (`pkg/config/validate.go:448-470`) | **no change** | **OR-based per (agent, tool)**: a **global** `sandbox.tool_policies` entry covers **every** agent (`:461-466`). A single edit to `defaults.go:276-287` therefore closes the boot-abort risk for all agents at once; the per-agent edits are about *posture*, not coverage. |
| Boot / reload / write enforcement | **no change** | Boot abort `pkg/gateway/gateway.go:2521`; hot-reload abort `:4017`; REST 400 `pkg/gateway/rest.go:2243`. |
| `ToolManifestVisibility` (`pkg/tools/manifest.go:243-251`) | **no change (probably)** | **Tier 3 is the residual, not a list.** Anything lazy and not in `previewedLazyToolNames` (7 names, `manifest.go:148-156`) resolves to `ManifestSearchOnly`. The six become Tier 3 with **zero production-code edits**. Only Tier 2 needs a code edit. |
| `tier3SearchOnlyToolNames` (`pkg/tools/manifest_test.go:667-681`) | **modifies** | Test fixture transcribing ADR-071 §4.1. **62 names, not 63** — `write_agent_metadata` retired. Pinned by hard literals in `TestVisibility_TierArithmetic` (`:694-745`: `17`, `7`, `62`, `1`, union `87`). |
| `ToolNameWebServe` (`pkg/tools/web_serve.go:46`) | **reads** | **`= "serve_web"`.** The ADR, the review and root `CLAUDE.md` all write `web_serve`. Corroborated independently: `previewedLazyToolNames` contains `"serve_web"` (`manifest.go:151`). An error naming `web_serve` sends the agent to a tool that does not exist. |
| `tools.ResolveTurnFSPolicy` / `ResolvePath` (`tools.go:575-583` at the screenshot call site) | **reuses (with a caveat)** | ADR-046 chokepoint precedent. `FSOp` semantics at `pkg/tools/resolvepath.go:54-59`: `FSOpRead/List/Send` are allowed **anywhere except the secret carve-out**; `FSOpWrite/Serve` are work-dir-or-mount only. See §3 Stream C's upload note — `PathHandle` cannot mediate a CDP upload. |
| `Config.RegisterSensitiveValues` / `SensitiveDataReplacer` (`pkg/config/security.go:44-77`) | **evaluated, NOT inherited** | See §2.3. |

### 2.2 CDP / chromedp primitives — all present, no new dependency

`go.mod:15-16` pins `chromedp v0.15.1` and `cdproto v0.0.0-20260321001828-e3e3800016bc`. Every primitive D2 needs already ships:

| Need | Primitive | Location |
|---|---|---|
| Role + name query | `accessibility.QueryAXTree()` with `WithRole` / `WithAccessibleName` | `cdproto/accessibility/accessibility.go:347`, options `:377-386` |
| Page structure | `accessibility.GetFullAXTree()` | `cdproto/accessibility/accessibility.go:132` |
| AX → DOM bridge | `accessibility.Node.BackendDOMNodeID` | `cdproto/accessibility/types.go:465` |
| Stability | `dom.GetBoxModel()` / `dom.GetContentQuads()` | `cdproto/dom/dom.go:403`, `:460` |
| Hit-test | `dom.GetNodeForLocation(x, y)` | `cdproto/dom/dom.go:630` |
| Scroll-into-view | `dom.ScrollIntoViewIfNeeded()` | `cdproto/dom/dom.go:212` |
| File attach | `chromedp.SetUploadFiles(sel, files, opts…)` | `chromedp/query.go:1115` |
| Key events | `chromedp.KeyEvent` / `KeyEventNode` | `chromedp/input.go:166`, `:184` |
| Dialog dismiss | `page.HandleJavaScriptDialog(accept)` + `.WithPromptText` | `cdproto/page/page.go:721`, `:729` |
| Dialog detect | `page.EventJavascriptDialogOpening` | `cdproto/page/events.go:138-145` |
| Select | `chromedp.SetValue` (+ a dispatched `change`) | `chromedp/query.go:859` |

**The wedge, from CDP's own contract.** `EventJavascriptDialogOpening.HasBrowserHandler` (`cdproto/page/events.go:143`) documents it verbatim: *"When browser has no dialog handler for given target, calling alert while Page domain is engaged will stall the page execution. Execution can be resumed via calling Page.handleJavaScriptDialog."* That sentence — not a design intuition — is why acceptance is "the tab still answers CDP", never "the dialog was dismissed".

**Precedent for synthetic key input.** The live-view human path already dispatches real key events (`live.go:2784`, `:2795`, `input.DispatchKeyEvent`). `browser_press_key` is the agent-facing counterpart, not a new mechanism.

### 2.3 The redaction claim does not reproduce — read before speccing D2.11

ADR D2.11 bullet 3: the snapshot *"inherits `browser_get_text`'s redaction posture and passes through the same `RegisterSensitiveValues` path."*

**Verified false, twice over.**

1. **There is no such path in this package.** `RegisterSensitiveValues` / `SensitiveDataReplacer` appear in `pkg/config`, `pkg/tools/list_jobs_row.go:213`, `pkg/gitevidence/repo.go`, `pkg/audit/secretscan.go`, `pkg/agent/session_messaging_wire.go` and `pkg/gateway/gateway.go:4843`. **Zero occurrences anywhere in `pkg/tools/browser/`.** `browser_get_text`'s entire output treatment is `capGetText` — a 64,000-character truncation (`tools.go:29-35`). Its redaction posture is *none*; inheriting it inherits nothing.
2. **Even if wired, it would not do the job claimed.** The replacer substitutes `[FILTERED]` for **registered credential plaintexts** and reflection-walked `SecureString` config fields (`pkg/config/security.go:81-107`), and skips any value ≤3 chars (`:103`). An account identifier or a form value on a signed-in page is not a registered secret, so it would pass through untouched.

**And the risk is real but differently shaped than the ADR states.** `browser_get_text` calls `chromedp.Text` (`tools.go:697`) — *innerText*, rendered text only. An `<input>`'s value is **not** innerText, so `browser_get_text` never emits form-field values today. `accessibility.Node.Value` (`cdproto/accessibility/types.go:461`) **is** the field's computed value. A snapshot that emits `Value` is therefore a **strict widening** of the disclosure surface relative to `browser_get_text`, not an inheritance of it. FR-018 decides this on its own terms.

### 2.4 Impact assessment

| Symbol modified | Risk | Direct dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `resolveActionSelector` / `resolvePseudoOnlySelector` | **HIGH** | click (`tools.go:249`), type (`:444`), get_text (`:672`), wait (`:792`) + all six new tools | `text_selector_test.go`, `text_selector_e2e_test.go` (2 files, ~59 KB of assertions) |
| Click/type pre-action wait (`tools.go:257`, `:461`) | **CRITICAL** | Every agent click and keystroke on every page | `execute_e2e_test.go`, `text_selector_e2e_test.go`, `tab_adoption_e2e_test.go` — behaviour change on the hot path; latency budget FR-007 |
| `allStaticToolNames` (`core.go:386-389`) | **CRITICAL** | `validateOverrideKeys` **panics** on drift; `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` | Every seeded agent's policy map; boot |
| `defaults.go:276-287` | **CRITICAL** | `ValidateToolPolicyCoverage` for **every** agent (OR-semantics) | Boot abort `gateway.go:2521`; reload abort `:4017`; REST 400 `rest.go:2243` |
| `BrowserBuiltinMetadata` (`metadata.go:37-50`) | **HIGH** | `buildKnownBuiltinToolNames` (`gateway.go:1266-1268`); `GET /api/v1/tools` catalog | Coverage universe — a tool in metadata but absent from `allStaticToolNames` fails the sync test |
| `tier3SearchOnlyToolNames` + tier arithmetic literals | **MEDIUM** | `TestVisibility_TierArithmetic` (`manifest_test.go:694`), `TestVisibility_SearchOnlyToolsRemainInSearchIndex` (`:786`) | Build fails until updated — by design (ADR-071 FR-034) |
| `installTargetListenerLocked` (`manager.go:2578`) | **HIGH** | Every tab's event plumbing; ADR-041 tab adoption | `tabs_test.go`, `tab_adoption_e2e_test.go`, `navigate_stranded_tab_test.go` |
| `ValidateURL` (`manager.go:684`) | LOW | `browser_navigate`, `browser_open_tab` | `blocked_schemes_test.go` (asserts the current message) |

---

## 3. Implementation Streams (fan-out for parallel agents)

Six streams. **Stream A is the critical path** — it defines the resolution and actionability interfaces every other stream codes against. **Stream E (policy + tier) is independently mandatory and must land in the SAME commit series as any stream that registers a tool**, because a registered tool with no seeded policy aborts boot.

### Shared interface contract (Stream A's first commit — everyone codes against this)

```go
// pkg/tools/browser/target.go (new) — placement: same package as the existing
// text_selector.go seam. NOT a new package: resolveActionSelector, wrapTextMatch,
// textMarkerAttr and scrubMarkerFromError are all unexported and stay that way.

// Locator is the closed set of ways an agent may name an element. Exactly one
// of {CSS, Text, Role+Name} is honoured, resolved in that documented order so
// the behaviour is total and deterministic — never "whichever the caller filled in".
type Locator struct {
    Selector string // CSS, optionally with a trailing :has-text()/:text-is() pseudo
    Text     string // visible-text substring (NOT valid on browser_type — see below)
    Role     string // ARIA/computed role, e.g. "button", "combobox", "link"
    Name     string // computed accessible name, e.g. "Submit"
    Index    int    // 0-based disambiguator on multi-match; 0 = "must be unique"
    HasIndex bool   // distinguishes an explicit index:0 from an unset field
}

// resolveTarget is the SINGLE seam. It supersedes resolveActionSelector and
// resolvePseudoOnlySelector as the entry point; both survive as internal
// branches so the existing CSS/text tests keep exercising the same code.
// Returns a CSS string the caller's existing chromedp ByQuery action uses
// unchanged — for the Role+Name branch this is the SAME data-omnipus-tsel
// marker selector (text_selector.go:56) the text branch already produces, set
// via DOM.setAttributeValue on the AX node's BackendDOMNodeID
// (cdproto/accessibility/types.go:465). cleanup MUST be deferred immediately;
// it is always safe to call, including on the error path.
func resolveTarget(tabCtx context.Context, toolName string, loc Locator, timeout time.Duration) (target string, cleanup func(), err error)

// ActionCondition is the CLOSED set the actionability gate reports on failure.
// Criterion 7 of the ADR requires the error to name WHICH condition was unmet;
// a closed set is what makes that testable rather than prose.
type ActionCondition string

const (
    CondVisible     ActionCondition = "visible"      // rendered, non-zero box
    CondStable      ActionCondition = "stable"       // two consecutive identical boxes
    CondEnabled     ActionCondition = "enabled"      // not [disabled], not aria-disabled=true
    CondHitTestable ActionCondition = "hit-testable" // DOM.getNodeForLocation at the box centre resolves to this node or a descendant
)

// ErrNotActionable is the ONLY error type the gate returns on timeout. Failed
// names the FIRST condition that never became true within the budget — first,
// not last, because the conditions are evaluated in the order above and a
// later one is meaningless while an earlier one is false.
type ErrNotActionable struct {
    Failed  ActionCondition
    Display string // the user-facing locator (displayLocator, text_selector.go:208)
    Detail  string // e.g. the occluding element's tag+id for CondHitTestable
}
func (e *ErrNotActionable) Error() string // "browser_click: element %q is not actionable: %s (%s)"

// waitActionable runs the four-condition gate. Called by every ACTION tool
// (click, type, select_option, press_key-with-target, hover, upload_file);
// NEVER by a read-only tool (get_text, screenshot, wait, snapshot).
func waitActionable(tabCtx context.Context, toolName, target, display string, timeout time.Duration) error

// leaseWrite acquires the D2.10 single-writer lease for the browsing context
// backing tabCtx, held for the duration of ONE action tool call. On contention
// it returns the SAME non-error {"deferred": true, "reason": ...} ToolResult
// shape controlledResult already returns (tools.go:962-978) — so the
// model-facing contract is unchanged and no system prompt needs rewriting.
// Returns (nil, release) when acquired.
func leaseWrite(mgr *BrowserManager, sessionID, toolName string) (deferred *tools.ToolResult, release func())
```

**Discipline inherited, not invented.** `waitActionable` and `resolveTarget` issue CDP round trips and therefore **must never run with `m.mu` held** — the ADR-038 rule the manager already documents at `manager.go:2574` and re-states at `:1573`. Every new tool follows the existing call order exactly: `controlledResult` → `leaseWrite` → `mgr.Session(...)` → `context.WithTimeout` → `displayLocator` → `resolveTarget` (+`defer cleanup()`) → `waitActionable` → the act.

**One decision the interface encodes deliberately:** `browser_type` cannot accept `Text` as a locator — its `text` argument is already the value typed (`tools.go:406-409`, and the code comment at `:441-443` says so). It accepts `Selector` and `Role`+`Name` only, and `Locator` validation must **reject** `Text` for that tool with a named error rather than silently ignoring it.

### Stream A — Target resolution + actionability [CRITICAL PATH]
**Owns:** `target.go` (new: `Locator`, `resolveTarget`, AX branch), `actionable.go` (new: `waitActionable`, `ErrNotActionable`), the rewiring of `tools.go:249/444/672/792` onto `resolveTarget`, the wait replacement at `tools.go:257` and `:461`, `displayLocator`'s role/name rendering (`text_selector.go:208`).
**Depends on:** nothing.
**Interface out:** the contract above.
**AX branch mechanics (the part that must not be guessed):**
- Query via `accessibility.QueryAXTree().WithRole(r).WithAccessibleName(n)` (`accessibility.go:347`). Its own doc states it returns *"those that match the specified attributes, **including nodes that are ignored for accessibility**"* — so the result set **must be filtered on `Node.Ignored == false`** (`types.go:455`) before ordering, or a hidden node wins.
- **Deterministic ordering** is document order of the surviving nodes' `BackendDOMNodeID`s, mirroring the text matcher's existing behaviour. Ordering must be asserted directly (a stable-order test), not inferred from a passing click.
- **Multi-match** with no `index`: error naming the count and the first three candidates' names — the same shape `resolveTextTarget` uses for ambiguity (`text_selector.go:536`, `resolvePendingErr`). With `index`: select the nth; out of range is an error naming the count.
- **Marker, not node handle.** Set `data-omnipus-tsel` on the winner via `DOM.setAttributeValue` against `BackendDOMNodeID`, return `[data-omnipus-tsel="<tok>"]`, reuse `removeTextMarker` (`text_selector.go:581`) as cleanup. This is what makes the change additive: no downstream `chromedp` action changes at all.
**Actionability mechanics:**
- **visible** — the existing `chromedp.WaitVisible` semantics, unchanged.
- **stable** — two consecutive `dom.GetBoxModel` reads (`dom.go:403`) yielding identical quads, separated by one animation frame. Poll interval reuses `textResolvePollInterval` (150 ms, `text_selector.go:74`) rather than inventing a second constant.
- **enabled** — `[disabled]` absent and `aria-disabled` not `"true"`. Evaluated in the same round trip as the box read to hold the budget.
- **hit-testable** — `dom.GetNodeForLocation` (`dom.go:630`) at the box centre resolves to the target node or a descendant of it. When it does not, `ErrNotActionable.Detail` carries the occluding node's tag and id — this is what turns "timeout" into "covered by another element".
- **Fast path (the budget's whole basis, FR-007).** On an already-actionable element the gate must complete in **one** batched CDP round trip: box + enabled + hit-test resolved together, with the second box read taken only if the first pass did not already satisfy stability against a cached prior box. The ADR's ≤150 ms p95 is unreachable with four sequential polls; it is comfortable with one batch.

### Stream B — The four interaction verbs
**Owns:** `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file` in a new `tools_interact.go`; their `RegisterReplacing` lines (`register.go`) and `BrowserBuiltinMetadata` entries (`metadata.go`).
**Depends on:** Stream A's `resolveTarget` + `waitActionable` interfaces (not their internals).
- `browser_select_option` — accepts `value` **or** `label` (an agent reads a label, not a value attribute), sets it, and **dispatches a real `change` event**; a `<select>` mutated by `SetValue` alone fires nothing and React-style listeners never see it. This is the same lesson `browser_type`'s `clear` path already records at `tools.go:452-459`. Multi-select accepts an array.
- `browser_press_key` — `chromedp.KeyEvent` (`input.go:166`) globally, or `KeyEventNode` (`:184`) when a locator is supplied (locator ⇒ the actionability gate applies). Accepts a **named** key set (`Enter`, `Tab`, `Escape`, `ArrowUp/Down/Left/Right`, `Backspace`, `Delete`, `Home`, `End`, `PageUp`, `PageDown`) plus modifiers. An unrecognised name is an error listing the accepted set — **never** silently typed as literal text.
- `browser_hover` — scroll into view (`dom.go:212`), then `Input.dispatchMouseEvent{type:"mouseMoved"}` at the box centre. Gate applies. **Must not** click.
- `browser_upload_file` — `chromedp.SetUploadFiles` (`query.go:1115`). See the confinement note below; it is the one genuinely unresolved security decision in D2.

> **`browser_upload_file` confinement — the ADR does not decide this and the review flagged it (unasked question 6).** `SetUploadFiles` hands Chrome an **absolute host path** and *Chrome* opens the file. Three consequences the implementation cannot dodge:
> 1. `tools.PathHandle`'s `os.Root`-mediated I/O — the whole TOCTOU-hardness argument at `resolvepath.go:88-99` — **cannot apply**, because the Go process never performs the read. The only usable output is `handle.RealPath()`, which is the documented exception (`tools.go:585-587`) and reintroduces exactly the race `PathHandle` exists to close.
> 2. The read happens in the **Chrome** process, outside whatever confinement covers the gateway thread.
> 3. `FSOp` choice is a real decision: `FSOpRead` permits anywhere outside the secret carve-out (`resolvepath.go:54-56`); `FSOpWrite` would confine to work-dir-or-mount (`:57-58`); `FSOpSend` exists for *"a disclosure to a chat channel"* and carries **no** extra path restriction (`:81-85`).
>
> **This spec's decision (FR-012), recorded as a decision so it can be overruled rather than discovered:** resolve through `ResolvePath` with **`FSOpSend`** — an upload to a remote origin is a disclosure, and `FSOpSend`'s own doc reasons that the real gate is tool policy, which D2.9's `ask` seed supplies. Confine additionally to `policy.AllowedRoots` (the `FSOpWrite` rule) so an `ask`-fatigued operator cannot be walked into `/etc/`. **Flagged in §12 as requiring an operator ruling before implementation** — it is stricter than `FSOpSend` alone and the ADR authorises neither.

### Stream C — Dialog handling [HIGHEST WEDGE RISK]
**Owns:** `browser_handle_dialog` + the per-tab dialog listener and pending-dialog state on `sessionEntry`.
**Depends on:** nothing in A/B. Can start immediately.
**The structural constraint the ADR misses.** `installTargetListenerLocked` (`manager.go:2578-2590`) attaches exactly **one** listener, on `se.tabs[0]`, and the doc comment at `:2569-2574` explains why that is correct for `Target` discovery — *"discovery itself is browser-global"*. `Page.javascriptDialogOpening` is **not** browser-global; it is per-target. A dialog on tab 2 with a tab-0-only listener is invisible, and the tab is wedged with no record that a dialog exists. **Every tab needs its own dialog listener, installed at tab creation/adoption and torn down at close.** The listener body inherits `handleTargetEvent`'s contract verbatim (`manager.go:2597-2606`): synchronous on the CDP dispatch goroutine, never blocks, never calls `chromedp.Run` inline — it records `{tabID, type, message, defaultPrompt, hasBrowserHandler}` under the manager mutex and returns.
**Behaviour:**
- `browser_handle_dialog{accept: bool, prompt_text?: string}` → `page.HandleJavaScriptDialog(accept)` (`page.go:721`), `.WithPromptText` when the dialog type is `prompt` (`:729`).
- Called with **no** dialog pending: a **non-error** result `{"dialog": null}`. Not an error — "check whether a dialog is blocking" is a legitimate, expected question.
- **Every other browser tool**, on a CDP timeout, must check pending-dialog state and, if one is present, return an error naming it and pointing at `browser_handle_dialog`. Without this the agent sees a bare timeout on a tab that will never recover, which is precisely §1.1's failure shape reproduced with a new cause.
**Deliberately NOT auto-dismissed.** An auto-dismiss policy is a decision about the *page's* semantics (an `onbeforeunload` confirm is not an `alert`), and silently accepting one is indistinguishable from a click the agent did not make. The tool is explicit; the *recovery pointer* is automatic.

### Stream D — `browser_snapshot`
**Owns:** `browser_snapshot` in `tools_snapshot.go`; the shared AX-tree fetch with Stream A (D2.4: *"Build them together or the second one is built twice"*).
**Depends on:** Stream A's AX fetch + filter helpers.
- `accessibility.GetFullAXTree()` (`accessibility.go:132`), filtered on `Ignored == false`, rendered as an indented `role "name"` outline carrying whatever handle the action tools accept (`index` within the snapshot's own ordering — the **same** document ordering Stream A's multi-match uses, so a handle read from a snapshot resolves identically in the next call).
- **Cap: 64,000 characters via the exact `capGetText` mechanism** (`tools.go:29-35`), matching ADR-066 B-15 / FR-014 and mirroring `per_tool_cap_alignment_test.go:11-38`. A full AX tree on a real page will exceed this routinely; truncation must be depth-first-with-marker, so the top of the page survives, not an arbitrary byte cut mid-node.
- **Read-only**: does **not** call `controlledResult` and does **not** take the write lease — matching `browser_screenshot`/`browser_get_text`/`browser_wait` (`tools.go:967-970`).
- **Disclosure posture: FR-018.** See §2.3 — this is not an inheritance, it is a new decision.

### Stream E — Policy seeding, tier assignment, catalog sync [MANDATORY, BLOCKS BOOT]
**Owns:** `pkg/config/defaults.go:276-287`; `pkg/coreagent/core.go:386-389` (`allStaticToolNames`), `:756-760` (Explorer), `:782-786` (Researcher), `:910-921` (Ray), `:1052-1064` (Jim); `pkg/tools/browser/metadata.go:37-50`; `pkg/tools/manifest_test.go:667-681` + the arithmetic literals at `:694-745` and `:752+`; the ADR-071 §4.1 doc list (`docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md:795-807`).
**Depends on:** only the six tool **names**, which are fixed by the ADR. **Can and should land first** — before any tool registers.
**Ordering that is not optional:** `allStaticToolNames` (`core.go:386-389`) must be edited **before or with** Jim's and Ray's override maps, because `validateOverrideKeys` (`core.go:436-452`) **panics** — not errors — on an override key it does not recognise.
**Coverage is closed by one edit.** `ValidateToolPolicyCoverage` is OR-based (`validate.go:461-466`): a global entry in `defaults.go` covers every agent. The per-agent edits set *posture*, not coverage. This is why Mia and Ava need **no** edit at all — `denyAllThenOverride` (`core.go:466-478`) starts every `allStaticToolNames` member at `deny` and they list no browser override.

### Stream F — Error routing + write lease
**Owns:** the `file://` pointer in `ValidateURL` (`manager.go:684-707`); `leaseWrite` (D2.10).
- **`file://` (FR-019).** The shared message at `:695` serves five schemes and only `file` has an answer, so branch: `file` gets `"browser: file:// URLs are blocked (they would bypass filesystem confinement). To view a local file in the browser, serve it with the serve_web tool — it returns a /preview/<agent>/<token>/ http URL that browser_navigate accepts."` The other four keep the existing string. **The tool name is `serve_web`** (`web_serve.go:46`), not `web_serve` — see §12 A-1.
- **Write lease (FR-023).** Per browsing context, held for one action tool call, non-error `{"deferred": true, "reason": …}` on contention (identical shape to `controlledResult`, `tools.go:970-977`). **D1 boundary:** the lease is keyed by *the browsing context*, whatever key D1 lands on; this spec deliberately does not name the key. Read-only tools stay ungated. Mid-tool preemption and fairness stay out of scope (ADR D2.10's own carve-out).

**Parallelization.** E lands first (names + policy, no behaviour). A is then the critical path. Once A's interface commit exists, B, C, D and F fan out — different files (`tools_interact.go` / dialog plumbing in `manager.go` / `tools_snapshot.go` / `manager.go:ValidateURL`). Stream C is the only one that touches `manager.go`'s listener plumbing and should not be parallelised against another `manager.go` edit.

---

## 4. Behavioral contract (observable)

- When an agent names an element as role + accessible name on a page whose CSS classes are generated, the element resolves — through the same seam, in the same call shape, as a CSS or visible-text locator.
- When two elements share a role and name, the call **errors** with the count and the first candidates, and succeeds when an `index` is supplied. It never silently picks the first.
- When an agent clicks or types on an element that is disabled, still animating, or covered by an overlay, the call fails naming **which** of `visible` / `stable` / `enabled` / `hit-testable` was not met — and for `hit-testable`, what is on top.
- When an agent clicks an element that was already actionable, the added pre-check costs **≤150 ms at p95**.
- When a page presents a `<select>`, the agent can set it by visible label and a `change` event fires.
- When a form needs Enter, Tab or Escape, the agent can send it as a discrete key event.
- When a menu opens on hover, the agent can open it without clicking.
- When a form needs an attachment, the agent can attach one — subject to an `ask` prompt on a default install, and to path confinement.
- **When a page calls `alert()`/`confirm()`/`prompt()`, the tab continues to answer CDP.** Either the agent dismisses the dialog explicitly, or any other browser tool that times out on that tab reports the pending dialog and names `browser_handle_dialog`. There is no state in which the tab is silently unreachable.
- When an agent needs to know what is on a page, `browser_snapshot` returns roles, accessible names and usable handles, without vision and without a pre-known CSS selector, capped at 64,000 characters.
- When an agent navigates to `file://`, the error names `serve_web` as the supported route.
- When two writers contend for one browsing context, the loser gets a non-error `{"deferred": true, …}` — never an error, never a torn interleave.
- When a fresh install boots with all six tools registered, it boots. No policy-coverage abort.

---

## 5. Explicit non-behaviors

- The system must **not** replace chromedp, add playwright-go, or add any runtime dependency (Hard Constraint #1). Every primitive §2.2 names already ships in the pinned `chromedp`/`cdproto`.
- The system must **not** apply the actionability gate to read-only tools. `browser_get_text` keeps `WaitReady` with its 8 s budget (`tools.go:679-685`) — `WaitVisible` there reintroduces the documented ~30 s hang on `<title>`.
- The system must **not** take the write lease or call `controlledResult` from `browser_snapshot` — it is read-only, like the three tools already exempted at `tools.go:967-970`.
- The system must **not** auto-dismiss dialogs. Explicit tool, automatic *pointer*.
- The system must **not** install the dialog listener on tab 0 only. `Page.javascriptDialogOpening` is per-target.
- The system must **not** report an actionability timeout as "timeout". The unmet condition is the payload.
- The system must **not** hold `m.mu` across `resolveTarget` or `waitActionable` (ADR-038 discipline, `manager.go:2574`).
- The system must **not** silently ignore a `text` locator passed to `browser_type` — reject it by name.
- The system must **not** register any of the six tools in a commit that does not also seed their policy. A registered tool with no policy entry aborts boot (`gateway.go:2521`).
- The system must **not** claim, in code comments or docs, that `browser_get_text` has a redaction posture. It has a length cap (§2.3).
- The system must **not** name `web_serve` in any agent-facing string. The tool is `serve_web` (`web_serve.go:46`).

---

## 6. Integration boundaries

- **chromedp / CDP.** In-process. `Accessibility`, `DOM`, `Input`, `Page` domains. Failure → the tool errors with the domain error text scrubbed of the internal marker (`scrubMarkerFromError`, `text_selector.go:223`). **Spike required (§10 order 1):** confirm the `Page` domain is already enabled per-tab by chromedp's own session bring-up; if it is not, `browser_handle_dialog` must enable it and the enable cost lands on every tab, which is a latency question for FR-007's neighbours.
- **Tool policy (Hard Constraint #6).** Boot `gateway.go:2521`, hot-reload `gateway.go:4017`, REST write `rest.go:2243`. All three consume `buildKnownBuiltinToolNames` (`gateway.go:1261`), which reads `BrowserBuiltinMetadata` (`metadata.go:36`). Six names must appear in **both** that catalog and `allStaticToolNames`, or `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` fails.
- **Manifest tiering (ADR-071).** Tier 3 is the residual (`manifest.go:243-251`) — no production edit for Tier 3. The **test fixture** and the ADR-071 §4.1 prose list are the edit sites, plus `previewedLazyToolNames` (`manifest.go:148-156`) *only if* `browser_snapshot` takes Tier 2 (FR-020, open).
- **`contracts/` (Hard Constraint #8) — checked, and NOT triggered.** The seventeen `Browser*.yaml` schemas in `contracts/components/schemas/` are all **live-panel WS frames** (attach/detach/input/viewport/tabs/WebRTC/inspect/capture). None carries a tool result. Tool results reach the SPA through the generic transcript/tool-call channel as opaque text. The six new tools introduce **no new cross-boundary shape**, so no `contracts/` change and no 5-step process. `make verify-contracts` must stay green as a regression assertion, not as a deliverable.
- **SPA.** No change. `src/lib/toolVisibility.ts` contains no `browser` reference — the new tools are visible in the thread like the existing eleven. If a future decision hides them, that is a separate change.
- **Filesystem (ADR-046 / ADR-063).** `browser_upload_file` only. Chokepoint `tools.ResolvePath`; the `PathHandle`-cannot-mediate caveat is in Stream B.
- **D1 boundary — three items this spec does not decide.** (a) The browsing-context key the write lease uses. (b) D2.11's browsing-context-creation audit event. (c) D2.11's team-editing-UI disclosure. All three act on an object D1 owns.
- **Human live view.** `browser_handle_dialog` is agent-facing. A human driving the wheel on a tab with an open dialog has no button. Today that tab is wedged for the human too; after this work it stays wedged for the human and becomes recoverable for the agent. **Recorded as an accepted gap, not solved** — a live-panel dialog affordance would need a new WS frame and therefore a `contracts/` change, which is out of scope here.

---

## 7. User stories & acceptance criteria

**US-1 (P0) Target by role and accessible name.** As an agent driving a page whose CSS classes are generated, I want to name "the Submit button" the way I already reason about it, so a class-name change does not break me.
- *Why P0:* the ADR's headline D2.1 benefit; the one that survives a redesign.
- *Independent test:* a fixture page with hashed class names and a `<button>Submit</button>`; `browser_click{role:"button", name:"Submit"}` clicks it.
- AC1: **Given** a page whose only stable property is `role=button, name="Submit"`, **When** `browser_click{role, name}` runs, **Then** the button is clicked.
- AC2: **Given** the same page, **When** `browser_get_text{role, name}` and `browser_wait{role, name}` run, **Then** both resolve the same element — the seam is inherited, not per-tool.
- AC3: **Given** `browser_type`, **When** `{text: "Submit"}` is passed as a locator, **Then** it is rejected by name (its `text` arg is the value typed, `tools.go:406-409`).

**US-2 (P0) Deterministic multi-match.** As an agent, when two things share a role and name I want to be told, not guessed for.
- AC1: **Given** three `role=button name="Delete"`, **When** `browser_click{role, name}` runs with no index, **Then** it errors naming the count `3` and the candidates.
- AC2: **Given** the same page, **When** `index: 1` is supplied, **Then** the second in document order is clicked, and the ordering is asserted directly (not inferred from the click landing).
- AC3: **Given** a hidden node also matching, **When** the query runs, **Then** the `Ignored == true` node is excluded (`accessibility/types.go:455`).

**US-3 (P0) Actionability names the cause.** As an agent, a failed click must tell me what to do next.
- *Why P0:* ADR criterion 7 — one of the two failure modes the ADR calls silent rather than wrong.
- AC1: **Given** a `<button disabled>`, **When** `browser_click` runs, **Then** it fails with `enabled` in the message, not "timeout".
- AC2: **Given** a button under a full-screen overlay, **When** `browser_click` runs, **Then** it fails with `hit-testable` **and** names the occluding element.
- AC3: **Given** a button mid-CSS-transition that settles, **When** `browser_click` runs, **Then** it waits for two identical boxes and clicks — no error.
- AC4: **Given** a button that never stops moving, **When** `browser_click` runs, **Then** it fails with `stable`.
- AC5: **Given** any gate failure, **When** the error is produced, **Then** the named condition is one of exactly four literals, and the test asserts the literal.

**US-4 (P0) The gate is not a tax.** As an operator on a loaded box, I want the new safety to be cheap on the common path.
- AC1: **Given** an already-actionable button, **When** 100 `browser_click` calls run, **Then** the p95 delta versus the pre-change build is **≤150 ms**, measured on the `performance-2x` profile ADR-072 §7 uses.

**US-5 (P0) Forms with dropdowns.** As an agent, I want to complete a form containing a `<select>` — impossible today.
- AC1: **Given** a `<select>` with options Alpha/Beta, **When** `browser_select_option{label:"Beta"}` runs, **Then** the value is Beta **and** a `change` event fired (asserted via a listener on the fixture page, not by reading `.value`).

**US-6 (P1) Discrete keys.** As an agent, I want Enter to submit, Tab to advance and Escape to dismiss.
- AC1: **Given** a focused input in a form, **When** `browser_press_key{key:"Enter"}` runs, **Then** the form submits.
- AC2: **Given** `key: "Ctrl+Banana"`, **When** the tool runs, **Then** it errors listing the accepted key names — it never types "Ctrl+Banana" as text.

**US-7 (P1) Hover menus.** As an agent, I want to reach a menu that only opens on hover.
- AC1: **Given** a nav item revealing a submenu on `mouseover`, **When** `browser_hover` runs, **Then** the submenu is visible and no click occurred (asserted by a click listener that must not fire).

**US-8 (P1) Attachments.** As an agent, I want to attach a file to a file input.
- AC1: **Given** an `<input type=file>` and a file in the turn's working directory, **When** `browser_upload_file` runs, **Then** the input reports one file with that name.
- AC2: **Given** a path outside `policy.AllowedRoots`, **When** the tool runs, **Then** it returns a permission-denied result naming the path — it does not hand the path to Chrome.
- AC3: **Given** a default install, **When** Jim calls it, **Then** policy resolves to `ask` (FR-021).

**US-9 (P0) A dialog does not wedge the tab.** As an operator, a page calling `alert()` must not end the session.
- *Why P0:* ADR criterion 12; the ADR's own "new worst case".
- AC1: **Given** a page whose button calls `alert('hi')`, **When** the agent clicks it and then calls `browser_handle_dialog{accept:true}`, **Then** the dialog is gone **and** a subsequent `browser_get_text{selector:"body"}` **returns within the normal timeout**. *The second half is the acceptance test.*
- AC2: **Given** an open dialog and **no** `browser_handle_dialog` call, **When** any other browser tool is invoked and times out, **Then** the error names the pending dialog and points at `browser_handle_dialog`.
- AC3: **Given** a dialog opened on **tab 2** (not tab 0), **When** AC1 is repeated, **Then** it holds identically. *This is the case a tab-0-only listener fails.*
- AC4: **Given** no dialog pending, **When** `browser_handle_dialog` runs, **Then** it returns a **non-error** `{"dialog": null}`.
- AC5: **Given** a `prompt()`, **When** `{accept:true, prompt_text:"x"}` runs, **Then** the page receives `"x"`.

**US-10 (P1) Read a page as structure.** As an agent, I want to know what is on a page and what I can do next, without vision and without a pre-known selector.
- AC1: **Given** a form with three labelled fields and a submit button, **When** `browser_snapshot` runs, **Then** all four appear with role and accessible name, and a handle from the output resolves in the very next action call.
- AC2: **Given** a page whose AX tree exceeds 64,000 characters, **When** the snapshot runs, **Then** output is capped at exactly 64,000 plus the truncation marker, and the marker names the cap (mirroring `per_tool_cap_alignment_test.go:33`).

**US-11 (P1) The `file://` dead end names a route.** As an agent told "no", I want to be told "instead".
- AC1: **Given** `browser_navigate{url:"file:///tmp/x.html"}`, **When** it is rejected, **Then** the error contains the literal `serve_web` and the literal `/preview/`.
- AC2: **Given** `browser_navigate{url:"javascript:alert(1)"}`, **When** it is rejected, **Then** the message is unchanged and does **not** mention `serve_web` — there is no supported route for it.

**US-12 (P0) Boot survives the six new tools.** As an operator, a fresh install must start.
- *Why P0:* Hard Constraint #6; the review's C5.
- AC1: **Given** a fresh install with all six registered, **When** the gateway boots, **Then** `ValidateToolPolicyCoverage` returns zero gaps and boot completes.
- AC2: **Given** the seeded config, **When** Jim's and Ray's policies are read, **Then** the five action/read tools are `allow` and `browser_upload_file` is `ask`.
- AC3: **Given** the seeded config, **When** Mia's and Ava's policies are read, **Then** all six are `deny` — with **no** per-agent edit, via `denyAllThenOverride` (`core.go:466-478`).
- AC4: **Given** an override map naming a tool absent from `allStaticToolNames`, **When** it is seeded, **Then** `validateOverrideKeys` panics (`core.go:446-451`) — the guard that makes the ordering in Stream E non-optional.

**US-13 (P1) Tier membership is a decision, not drift.** As a maintainer, a new tool must not slip into a tier silently.
- AC1: **Given** the six names, **When** `TestVisibility_TierArithmetic` runs, **Then** its literals reflect the new partition and the union count is asserted, not derived.
- AC2: **Given** each of the six, **When** `ToolManifestVisibility` is called, **Then** it returns the tier this spec's FR-020 records — including `browser_snapshot`, whichever option is chosen.

**US-14 (P1) Two writers, one context.** As an operator, concurrent agent writes must not interleave.
- AC1: **Given** two concurrent `browser_click` calls on one browsing context, **When** both run, **Then** exactly one acts; the other returns non-error `{"deferred": true, …}` and `IsError` is false.

**US-15 (P1) Existing browser behaviour is preserved.** As a maintainer, the added waits must not break what works.
- AC1: **Given** the named regression suite (§10), **When** it runs with `OMNIPUS_BROWSER_E2E=1`, **Then** every test passes and the CI pass-count floor (`.github/workflows/pr.yml:481`) is **raised**, never lowered.

---

## 8. BDD scenarios

**Scenario: role + name resolves on a hashed-class page (Happy) — US-1/AC1, FR-001**
- **Given** a fixture page whose submit control is `<button class="_a7f3x">Submit</button>`
- **When** the agent calls `browser_click{role:"button", name:"Submit"}`
- **Then** the click lands on that button and the result echoes the role/name locator, not the internal `data-omnipus-tsel` marker

**Scenario: every action tool inherits the seam (Happy) — US-1/AC2, FR-002**
- **Given** the same page
- **When** `browser_get_text`, `browser_wait`, `browser_hover` and `browser_select_option` are each called with `{role, name}`
- **Then** all four resolve the same element, and none carries its own resolution branch

**Scenario: ambiguous role+name errors rather than guessing (Edge) — US-2/AC1, FR-003**
- **Given** three `<button>Delete</button>` elements
- **When** `browser_click{role:"button", name:"Delete"}` runs with no `index`
- **Then** it errors naming `3` candidates, and no click occurred (asserted by a click counter on the page)

**Scenario: index selects deterministically in document order (Edge) — US-2/AC2, FR-003**
- **Given** the same three buttons, each with a distinct `data-testid`
- **When** `index: 1` is supplied
- **Then** the button with the **second** `data-testid` in source order is clicked

**Scenario: AX-ignored nodes are excluded (Edge) — US-2/AC3, FR-003**
- **Given** a `<button aria-hidden="true">Delete</button>` plus one visible `<button>Delete</button>`
- **When** `browser_click{role:"button", name:"Delete"}` runs with no index
- **Then** it succeeds on the visible one — the hidden node neither wins nor makes the match ambiguous

**Scenario: disabled element names `enabled` (Error) — US-3/AC1, FR-006**
- **Given** `<button disabled>Save</button>`
- **When** `browser_click` runs
- **Then** the error contains the literal `enabled` and does not consist solely of "timeout" or "context deadline exceeded"

**Scenario: covered element names `hit-testable` and the occluder (Error) — US-3/AC2, FR-006**
- **Given** `<button id="save">Save</button>` beneath `<div id="overlay">` at `z-index: 9999` covering the viewport
- **When** `browser_click` runs
- **Then** the error contains `hit-testable` **and** the string `overlay`

**Scenario: a settling element is waited for, not failed (Happy) — US-3/AC3, FR-005**
- **Given** a button with a 300 ms CSS transform that settles
- **When** `browser_click` runs
- **Then** it succeeds, and the stability check observed two consecutive identical box models

**Scenario: a perpetually moving element names `stable` (Error) — US-3/AC4, FR-006**
- **Given** a button under an infinite `@keyframes` translate
- **When** `browser_click` runs
- **Then** the error contains `stable`

**Scenario: the gate is cheap on the common path (Performance) — US-4/AC1, FR-007**
- **Given** a static page with an immediately-actionable button, on the `performance-2x` profile
- **When** 100 sequential `browser_click` calls are timed against the pre-change build
- **Then** the p95 delta is ≤150 ms and the gate issued **one** batched CDP round trip on the fast path

**Scenario: `<select>` set by label fires `change` (Happy) — US-5/AC1, FR-009**
- **Given** `<select>` with Alpha/Beta and a `change` listener that sets `window.__changed = true`
- **When** `browser_select_option{label:"Beta"}` runs
- **Then** the value is Beta **and** `window.__changed === true`

**Scenario: unknown key name is refused, not typed (Error) — US-6/AC2, FR-010**
- **Given** a focused text input
- **When** `browser_press_key{key:"Ctrl+Banana"}` runs
- **Then** the tool errors listing the accepted key names, and the input's value is unchanged

**Scenario: hover opens a menu without clicking (Happy) — US-7/AC1, FR-011**
- **Given** a nav item revealing a submenu on `mouseover`, with a click listener setting `window.__clicked = true`
- **When** `browser_hover` runs
- **Then** the submenu is visible and `window.__clicked` is `undefined`

**Scenario: upload outside the allowed roots is denied before Chrome sees it (Security) — US-8/AC2, FR-012**
- **Given** `<input type=file>` and a path outside `policy.AllowedRoots`
- **When** `browser_upload_file` runs
- **Then** a permission-denied result is returned, **and** `chromedp.SetUploadFiles` was never invoked (asserted at the seam, not by the absence of a file)

**Scenario: the tab still answers CDP after a dialog (Error → recovery) — US-9/AC1, FR-013 — THE acceptance test**
- **Given** a page whose button calls `alert('hi')`
- **When** the agent clicks it, then calls `browser_handle_dialog{accept:true}`, then calls `browser_get_text{selector:"body"}`
- **Then** `browser_get_text` **returns page text within the normal timeout** — the assertion is the tab's continued responsiveness, not the dialog's disappearance

**Scenario: a dialog on a non-zero tab is still seen (Edge) — US-9/AC3, FR-014**
- **Given** three open tabs, with the `alert()` triggered on tab index 2
- **When** the previous scenario is repeated against tab 2
- **Then** it holds identically — proving the listener is per-tab, not tab-0-only (`manager.go:2578-2590`)

**Scenario: an unhandled dialog is reported, not silently timed out (Error) — US-9/AC2, FR-013**
- **Given** an open, unhandled `confirm()` on the active tab
- **When** `browser_click` is called on any element
- **Then** the error names the pending dialog and contains the literal `browser_handle_dialog`

**Scenario: snapshot handles round-trip into an action (Happy) — US-10/AC1, FR-015/FR-016**
- **Given** a form with three labelled inputs and a submit button
- **When** `browser_snapshot` runs and its handle for "Email" is passed to `browser_type`
- **Then** the text lands in the email field

**Scenario: snapshot obeys the 64,000-char cap (Edge) — US-10/AC2, FR-017**
- **Given** a page whose AX tree renders beyond 64,000 characters
- **When** `browser_snapshot` runs
- **Then** output is exactly 64,000 characters plus the truncation marker, the marker names `64,000`, and the retained portion is the top of the tree

**Scenario: `file://` names the route; `javascript:` does not (Error) — US-11, FR-019**
- **Given** `browser_navigate{url:"file:///tmp/x.html"}` and `browser_navigate{url:"javascript:alert(1)"}`
- **When** both are rejected
- **Then** the first contains `serve_web` and `/preview/`; the second contains neither and matches the pre-change message

**Scenario: fresh install boots with all six registered (Boot) — US-12/AC1, FR-021**
- **Given** a fresh `$OMNIPUS_HOME` and a build with all six tools registered
- **When** the gateway boots
- **Then** `ValidateToolPolicyCoverage(cfg, buildKnownBuiltinToolNames())` returns zero gaps and no abort is logged

**Scenario: seeded posture is exactly the ADR's table (Boot) — US-12/AC2-AC3, FR-021**
- **Given** the seeded config
- **When** each agent's resolved policy for the six is read
- **Then** Jim = Ray = `allow`×5 + `ask` for `browser_upload_file`; Mia = Ava = `deny`×6; and Mia's/Ava's values come from `denyAllThenOverride`'s default, with no per-agent literal

**Scenario: an unknown override key panics (Boot guard) — US-12/AC4, FR-022**
- **Given** an override map naming `browser_selct_option` (typo)
- **When** `coreAgentSeed` runs
- **Then** `validateOverrideKeys` panics naming the unknown tool (`core.go:446-451`)

**Scenario: tier arithmetic is updated deliberately (Build) — US-13, FR-020**
- **Given** the six new names
- **When** `TestVisibility_TierArithmetic` runs
- **Then** the four set sizes and the union count match FR-020's chosen option, asserted as literals

**Scenario: the second writer defers, it does not error (Concurrency) — US-14, FR-023**
- **Given** two concurrent `browser_click` calls on one browsing context
- **When** both execute
- **Then** exactly one acts; the other's result parses as `{"deferred": true, "reason": …}` with `IsError == false`

**Scenario: existing browser behaviour is preserved (Regression) — US-15, FR-008**
- **Given** the named regression suite in §10
- **When** it runs with `OMNIPUS_BROWSER_E2E=1`
- **Then** all listed tests pass and the CI floor at `.github/workflows/pr.yml:481` has been raised to the newly measured count

---

## 9. Traceability matrix (FR ↔ US ↔ BDD ↔ test ↔ ADR/grill)

| FR | US | BDD | Test (TDD) | ADR / grill |
|---|---|---|---|---|
| FR-001 role+name resolves via the shared seam | US-1 | role-name-hashed-class | `TestResolveTarget_RoleName_ResolvesOnHashedClasses` | D2.1 |
| FR-002 every action tool inherits the seam | US-1 | seam-inherited-by-all-tools | `TestResolveTarget_AllActionToolsShareSeam` | D2.1 |
| FR-003 deterministic order + index + ignored-filter | US-2 | ambiguous-errors / index-doc-order / ax-ignored-excluded | `TestResolveTarget_MultiMatch_ErrorsWithCount`, `_IndexSelectsDocumentOrder`, `_ExcludesIgnoredNodes` | D2.1 |
| FR-004 `browser_type` rejects a `text` locator | US-1/AC3 | (in seam-inherited) | `TestTypeTool_RejectsTextAsLocator` | D2.1 / `tools.go:406-409` |
| FR-005 four-condition gate on every action tool | US-3 | settling-element-waited | `TestWaitActionable_AllFourConditions`, `TestWaitActionable_StabilityTwoIdenticalBoxes` | D2.2 |
| FR-006 failure names the unmet condition (closed set of 4) | US-3 | disabled/covered/moving | `TestWaitActionable_NamesFailedCondition_Table` | D2.2 / criterion 7 |
| FR-007 ≤150 ms p95 on an actionable element | US-4 | gate-is-cheap | `TestWaitActionable_FastPathBudget` (bench + assertion) | D2.2 / grill m7 |
| FR-008 existing click/type behaviour preserved | US-15 | existing-behaviour-preserved | the §10 regression list | §4 blast radius |
| FR-009 `browser_select_option` + `change` event | US-5 | select-by-label-fires-change | `TestSelectOption_ByLabel_FiresChange` | D2.3 / criterion 8 |
| FR-010 `browser_press_key` named keys, unknown refused | US-6 | unknown-key-refused | `TestPressKey_Enter_SubmitsForm`, `_UnknownKeyErrors` | D2.3 / criterion 9 |
| FR-011 `browser_hover` opens without clicking | US-7 | hover-no-click | `TestHover_OpensMenu_NoClick` | D2.3 / criterion 10 |
| FR-012 `browser_upload_file` + path confinement | US-8 | upload-denied-outside-roots | `TestUploadFile_AttachesFile`, `_DeniesOutsideAllowedRoots` | D2.3 / criterion 11 / grill STRIDE |
| FR-013 dialog handled; **tab still answers CDP** | US-9 | tab-answers-after-dialog / unhandled-dialog-reported | `TestDialog_TabStillRespondsAfterHandle`, `_UnhandledDialogNamedInTimeout` | D2.3 / criterion 12 |
| FR-014 dialog listener on **every** tab | US-9/AC3 | dialog-on-tab-2 | `TestDialog_OnNonZeroTab_StillDetected` | *(this spec — `manager.go:2578`)* |
| FR-015 `browser_snapshot` returns roles+names+handles | US-10 | snapshot-handles-round-trip | `TestSnapshot_ReturnsRolesNamesHandles` | D2.4 / criterion 13 |
| FR-016 snapshot handles resolve in the next action | US-10 | snapshot-handles-round-trip | `TestSnapshot_HandleResolvesInNextCall` | D2.4 |
| FR-017 snapshot obeys the 64,000-char cap | US-10/AC2 | snapshot-cap | `TestChokePoint_PerSurfaceCap_Snapshot` (mirrors `per_tool_cap_alignment_test.go:11`) | ADR-066 B-15 / grill Q7 |
| FR-018 snapshot disclosure posture (**decision, §12 A-2**) | US-10 | *(follows the chosen option)* | `TestSnapshot_DisclosurePosture` | D2.11 bullet 3 / grill M7 |
| FR-019 `file://` error names `serve_web` + `/preview/` | US-11 | file-names-route | `TestValidateURL_FileScheme_NamesServeWeb`, `_JavascriptSchemeUnchanged` | D2.5 / criterion 14 |
| FR-020 tier assignment + drift-test edits (**§12 A-3**) | US-13 | tier-arithmetic-updated | `TestVisibility_TierArithmetic`, `TestVisibility_PreviewedSetIsExactlySeven` | D2.8 / grill M9 |
| FR-021 policy seeded for every agent; boot survives | US-12 | fresh-install-boots / seeded-posture | `TestToolPolicyCoverage_SixNewBrowserTools_NoGaps`, `TestCoreAgentSeed_BrowserD2Posture` | D2.9 / grill C5 |
| FR-022 catalog sync (`allStaticToolNames` ↔ metadata) | US-12/AC4 | unknown-override-panics | `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` (existing), `TestValidateOverrideKeys_PanicsOnUnknown` | Hard Constraint #6 |
| FR-023 write lease defers, never errors | US-14 | second-writer-defers | `TestLeaseWrite_SecondWriterDeferred` | D2.10 / grill M3 |
| FR-024 Explorer/Researcher parity (**decision, §12 A-4**) | US-12 | seeded-posture | `TestCoreAgentSeed_ExplorerResearcherBrowserParity` | *(this spec — `core.go:756-760`, `:782-786`)* |
| FR-025 no `contracts/` change | — | — | `make verify-contracts` | Hard Constraint #8 |
| FR-026 no new runtime dependency | — | — | `go mod tidy` produces no diff; `go.mod:15-16` unchanged | Hard Constraint #1 |

---

## 10. TDD plan (ordered; Unit → Integration → E2E)

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 0 | **SPIKE:** `Page` domain enabled per-tab by chromedp's own bring-up? | Spike | FR-013/FR-014 | **Gates Stream C.** If not enabled, `browser_handle_dialog` must enable it per tab and the cost lands on FR-007's neighbours. Verify, don't assume. |
| 1 | `TestValidateOverrideKeys_PanicsOnUnknown` | Unit | FR-022 | Cheapest; proves the Stream E ordering constraint before any seeding |
| 2 | `TestToolPolicyCoverage_SixNewBrowserTools_NoGaps` | Unit | FR-021 | **The boot blocker.** Must be red before Stream E, green after |
| 3 | `TestCoreAgentSeed_BrowserD2Posture` | Unit | FR-021 | Jim/Ray allow+ask; Mia/Ava deny via the default, not a literal |
| 4 | `TestCoreAgentSeed_ExplorerResearcherBrowserParity` | Unit | FR-024 | Pins whichever way §12 A-4 is ruled |
| 5 | `TestVisibility_TierArithmetic` (edit) + `TestVisibility_PreviewedSetIsExactlySeven` (edit) | Unit | FR-020 | Build-breaking by design (ADR-071 FR-034) |
| 6 | `TestValidateURL_FileScheme_NamesServeWeb` / `_JavascriptSchemeUnchanged` | Unit | FR-019 | Pure string; no browser. Asserts the literal `serve_web`, catching §12 A-1 |
| 7 | `TestWaitActionable_NamesFailedCondition_Table` | Unit | FR-006 | Table across the four conditions, against a stubbed CDP seam |
| 8 | `TestTypeTool_RejectsTextAsLocator` | Unit | FR-004 | |
| 9 | `TestResolveTarget_MultiMatch_ErrorsWithCount` / `_IndexSelectsDocumentOrder` / `_ExcludesIgnoredNodes` | Integration | FR-003 | Real Chrome; ordering asserted directly, never inferred |
| 10 | `TestResolveTarget_RoleName_ResolvesOnHashedClasses` | Integration | FR-001 | The D2.1 headline |
| 11 | `TestResolveTarget_AllActionToolsShareSeam` | Integration | FR-002 | Guards against a per-tool resolution branch reappearing |
| 12 | `TestWaitActionable_AllFourConditions` / `_StabilityTwoIdenticalBoxes` | Integration | FR-005 | Real Chrome; overlay + disabled + animated fixtures |
| 13 | `TestDialog_TabStillRespondsAfterHandle` | Integration | FR-013 | **The load-bearing one.** Asserts a *subsequent* CDP call returns |
| 14 | `TestDialog_OnNonZeroTab_StillDetected` | Integration | FR-014 | The tab-0-only listener bug |
| 15 | `TestDialog_UnhandledDialogNamedInTimeout` | Integration | FR-013 | Recovery pointer |
| 16 | `TestSelectOption_ByLabel_FiresChange` | Integration | FR-009 | `change` asserted by a page listener, not `.value` |
| 17 | `TestPressKey_Enter_SubmitsForm` / `_UnknownKeyErrors` | Integration | FR-010 | |
| 18 | `TestHover_OpensMenu_NoClick` | Integration | FR-011 | Click-counter must stay zero |
| 19 | `TestUploadFile_AttachesFile` / `_DeniesOutsideAllowedRoots` | Integration | FR-012 | Denial asserted **at the `SetUploadFiles` seam** |
| 20 | `TestSnapshot_ReturnsRolesNamesHandles` / `_HandleResolvesInNextCall` | Integration | FR-015/016 | |
| 21 | `TestChokePoint_PerSurfaceCap_Snapshot` | Unit | FR-017 | Mirrors `per_tool_cap_alignment_test.go:11-38` exactly |
| 22 | `TestSnapshot_DisclosurePosture` | Integration | FR-018 | Shape depends on §12 A-2's ruling |
| 23 | `TestLeaseWrite_SecondWriterDeferred` | Integration | FR-023 | `IsError == false` is half the assertion |
| 24 | `TestWaitActionable_FastPathBudget` | E2E / bench | FR-007 | `performance-2x`; 100 iterations; p95 delta vs. the pre-change build |
| 25 | Full `pkg/tools/browser` suite with `OMNIPUS_BROWSER_E2E=1` | E2E | FR-008 | Then **raise** the floor at `.github/workflows/pr.yml:481` |
| 26 | `make verify-contracts` | Build | FR-025 | Must stay green with no `contracts/` diff |

### Regression requirements

**These tests exercise the code paths this work changes and MUST keep passing, by name:**

| Test | File:line | Why it is at risk |
|---|---|---|
| `TestExecute_HappyChain_NavigateWaitClickGetText` | `execute_e2e_test.go:163` | The click path gains the four-condition gate |
| `TestExecute_Type_PersistsInDOM` | `execute_e2e_test.go:892` | The type path's `WaitVisible` is replaced (`tools.go:461`) |
| `TestTextSel_Click_HasTextPseudo_ClicksLink` | `text_selector_e2e_test.go:109` | Text branch rerouted through `resolveTarget` |
| `TestTextSel_Click_TextParam_ClicksButton` | `text_selector_e2e_test.go:151` | Same |
| `TestTextSel_Specificity_ClicksButtonNotWrappingDiv` | `text_selector_e2e_test.go:191` | Specificity must survive the new seam |
| `TestTextSel_TypeTool_PseudoSelector_TypesIntoInput` | `text_selector_e2e_test.go:379` | `browser_type` moves off `resolvePseudoOnlySelector` |
| `TestTextSel_Specificity_NoExtraProse_ClicksButtonNotDiv` | `text_selector_e2e_test.go:511` | Same |
| `TestTextSel_TypeTool_PseudoSelector_TypesIntoContentEditable` | `text_selector_e2e_test.go:878` | Contenteditable + the gate |
| `TestExecute_TargetBlankClick_AdoptsNewTab` | `tab_adoption_e2e_test.go:77` | Per-tab dialog listener touches the same tab plumbing |
| `TestReconcileTabs_OneClickTwoNewTargets_OneAdoptedOneStranded` | `tabs_test.go:908` | Same |
| `TestChokePoint_PerSurfaceCap_B15_GetText` | `per_tool_cap_alignment_test.go:11` | Snapshot must not change `browser_get_text`'s cap |
| all of `tools_control_test.go`, `shared_control_test.go` | — | `controlledResult` gains the lease as a sibling; the deferral shape must not change |
| all of `blocked_schemes_test.go` | — | Asserts the current `file://` message text; **this test WILL need updating**, and it must be updated to assert the new literal, never deleted |
| all of `text_selector_test.go` (17.7 KB) | — | Unit-level seam behaviour |

**How to run the browser suite (do not run the full Go suite — OOM):**
`CGO_ENABLED=0 OMNIPUS_BROWSER_E2E=1 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/tools/browser` — matching the CI job at `.github/workflows/pr.yml:403-484`. `skipIfNoBrowser` (`browser_e2e_test.go:57-67`) skips in CI without that env var, which is exactly how coverage silently went to zero in #615.

### Test datasets

| Input | Expected | Traces to |
|---|---|---|
| `role=button, name="Submit"`, one match | resolves, clicks | FR-001 |
| same, three matches, no index | error naming `3` + candidates; **zero** clicks | FR-003 |
| same, three matches, `index:1` | second in document order | FR-003 |
| same, one visible + one `aria-hidden` | resolves the visible one, not ambiguous | FR-003 |
| same, `index:9` on three matches | error naming the count | FR-003 |
| `browser_type{text:"Submit"}` as a locator | rejected by name | FR-004 |
| `<button disabled>` | error contains `enabled` | FR-006 |
| button under a `z-index:9999` overlay | error contains `hit-testable` **and** `overlay` | FR-006 |
| button in a 300 ms transition that settles | succeeds | FR-005 |
| button under an infinite keyframe translate | error contains `stable` | FR-006 |
| already-actionable button ×100, `performance-2x` | p95 delta ≤150 ms | FR-007 |
| `<select>` set by label, `change` listener armed | value set **and** listener fired | FR-009 |
| `key:"Ctrl+Banana"` | error listing accepted keys; input unchanged | FR-010 |
| hover target with a click counter | menu visible, counter `0` | FR-011 |
| upload path inside `AllowedRoots` | attached | FR-012 |
| upload path outside `AllowedRoots` | denied **before** `SetUploadFiles` is called | FR-012 |
| `alert()` on tab 0, then handle, then `get_text` | `get_text` returns within the normal timeout | FR-013 |
| `alert()` on tab 2, then handle, then `get_text` | identical | FR-014 |
| `prompt()` with `{accept:true, prompt_text:"x"}` | page receives `"x"` | FR-013 |
| `browser_handle_dialog` with no dialog pending | non-error `{"dialog": null}` | FR-013 |
| any tool timing out with a dialog pending | error names the dialog + `browser_handle_dialog` | FR-013 |
| AX tree rendering >64,000 chars | capped at exactly 64,000 + marker naming `64,000`; top retained | FR-017 |
| `file:///tmp/x.html` | error contains `serve_web` **and** `/preview/` | FR-019 |
| `javascript:alert(1)` | pre-change message; no `serve_web` | FR-019 |
| fresh install, all six registered | zero coverage gaps; boot completes | FR-021 |
| override key `browser_selct_option` (typo) | `validateOverrideKeys` panics | FR-022 |
| two concurrent `browser_click` on one context | one acts; other `{"deferred":true}`, `IsError == false` | FR-023 |

---

## 11. Functional requirements & success criteria

- **FR-001 … FR-026** as tabulated in §9. All MUST.
- **SC-001 (headline).** An agent completes a form containing a text input, a `<select>`, a file attachment and an Enter-key submit, on a page with generated class names, using **only** role + accessible-name locators — end to end, no CSS selector anywhere in the call sequence. This is the single scenario that is impossible on every axis today.
- **SC-002.** A fresh install boots with all seventeen browser tools registered and **zero** `ValidateToolPolicyCoverage` gaps (`validate.go:448`, boot gate `gateway.go:2521`).
- **SC-003.** Every actionability failure across the four conditions is reported with the failing condition named; the failure-message test table covers all four literals and no path emits a bare "timeout".
- **SC-004.** ≤150 ms p95 added to a click on an already-actionable element, `performance-2x`, 100 iterations, measured against the pre-change build on the same machine in the same session.
- **SC-005.** After a handled `alert()`/`confirm()`/`prompt()` on **any** tab index, a subsequent `browser_get_text` returns within the normal page timeout. Asserted as the tab's continued responsiveness.
- **SC-006.** The `pkg/tools/browser` suite passes with `OMNIPUS_BROWSER_E2E=1`, every test in §10's regression list included, and the CI pass floor (`.github/workflows/pr.yml:481`, currently `180`) is **raised** to the newly measured count.
- **SC-007.** `make verify-contracts` green with a zero-line diff under `contracts/`, `pkg/api/generated/` and `src/lib/api/generated/` — the positive evidence for FR-025.
- **SC-008.** `gofmt -l . | wc -l` is `0`; `golangci-lint run --build-tags=goolm,stdjson` exits 0; `go.mod` unchanged (FR-026).

**Seeded policy table (FR-021) — the exact target state.**

| Tool | Global (`defaults.go:276-287`) | Jim (`core.go:1052-1064`) | Ray (`core.go:910-921`) | Explorer (`:756-760`) | Researcher (`:782-786`) | Mia | Ava | Worker |
|---|---|---|---|---|---|---|---|---|
| `browser_select_option` | allow | allow | allow | **§12 A-4** | **§12 A-4** | deny¹ | deny¹ | inherits global² |
| `browser_press_key` | allow | allow | allow | **§12 A-4** | **§12 A-4** | deny¹ | deny¹ | inherits global² |
| `browser_hover` | allow | allow | allow | **§12 A-4** | **§12 A-4** | deny¹ | deny¹ | inherits global² |
| `browser_handle_dialog` | allow | allow | allow | **§12 A-4** | **§12 A-4** | deny¹ | deny¹ | inherits global² |
| `browser_snapshot` | allow | allow | allow | **§12 A-4** | **§12 A-4** | deny¹ | deny¹ | inherits global² |
| `browser_upload_file` | **ask** | **ask** | **ask** | **§12 A-4** | **§12 A-4** | deny¹ | deny¹ | inherits global² |

¹ **Automatic, no per-agent edit** — `denyAllThenOverride` (`core.go:466-478`) starts every `allStaticToolNames` member at `deny`; Mia (`core.go:848`) and Ava (`core.go:794`) list no browser override. The ADR's D2.9 table reads as four hand-edits; two of them cost nothing.
² `IDWorker` uses `tightenGlobalCeiling` (`core.go:491-497`, invoked at `:605`) — a **sparse** map. Absent names inherit the **global** ceiling. Today Worker inherits `allow` for all eleven browser tools by exactly this route; the six follow, so `browser_upload_file` lands at **`ask`** for Worker. Recorded as intended, not discovered.

**Required edit sites, exhaustive:**
1. `pkg/coreagent/core.go:386-389` — six names into `allStaticToolNames`. **First**, or `validateOverrideKeys` panics.
2. `pkg/config/defaults.go:276-287` — six global entries. **This one edit closes the boot-abort risk for every agent** (OR-semantics, `validate.go:461-466`).
3. `pkg/coreagent/core.go:1052-1064` — Jim: 5 × `allow` + `browser_upload_file: ask`.
4. `pkg/coreagent/core.go:910-921` — Ray: same.
5. `pkg/coreagent/core.go:756-760` / `:782-786` — Explorer / Researcher, per §12 A-4.
6. `pkg/tools/browser/metadata.go:37-50` — six metadata instances.
7. `pkg/tools/browser/register.go:65-81` — six `RegisterReplacing` calls.
8. `pkg/tools/manifest_test.go:667-681` — the Tier 3 fixture (**62 → 68**, or 67 if `browser_snapshot` takes Tier 2).
9. `pkg/tools/manifest_test.go:694-745` — the literals `62`, `87` (→ `68`/`93`, or `67`/`93` with `previewed` `7`→`8`).
10. `pkg/tools/manifest_test.go:752+` — `TestVisibility_PreviewedSetIsExactlySeven`, **only** under Tier-2 snapshot.
11. `pkg/tools/manifest.go:148-156` — `previewedLazyToolNames`, **only** under Tier-2 snapshot.
12. `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md:795-807` — the prose Tier 3 list. **Note it currently reads 63 and still contains `write_agent_metadata`; the code fixture is already at 62. Correcting that drift is part of this edit, not a separate change.**
13. `pkg/tools/browser/manager.go:695` — the scheme-specific `file://` branch.
14. `.github/workflows/pr.yml:481` — raise the pass-count floor.

---

## 12. Ambiguity self-audit

Items **A-1 … A-4** are decisions the ADR does not make or makes incorrectly. They need a ruling **before** implementation. Items **B-x** are recorded assumptions.

| # | Ambiguity | Disposition |
|---|---|---|
| **A-1** | **The ADR names a tool that does not exist.** D2.5, the round-1 review, and root `CLAUDE.md` all write `web_serve`. The registered name is **`serve_web`** — `pkg/tools/web_serve.go:46` (`const ToolNameWebServe = "serve_web"`), corroborated by `previewedLazyToolNames` containing `"serve_web"` (`manifest.go:151`). An error naming `web_serve` sends the agent to a nonexistent tool and a failed `ToolSearch`. | **Decided by code, not judgement.** FR-019 specifies the literal `serve_web`. Its test asserts the literal so the ADR's wording cannot leak back in. Root `CLAUDE.md`'s ADR-044 paragraph carries the same error and should be corrected separately. |
| **A-2** | **`browser_get_text` has no redaction posture to inherit (§2.3).** D2.11 bullet 3 prescribes inheriting one and passing through `RegisterSensitiveValues`. Verified: zero occurrences in `pkg/tools/browser/`; `browser_get_text`'s entire treatment is a 64,000-char cap. And the replacer only substitutes registered credential plaintexts (`pkg/config/security.go:81-107`), so it would not redact account identifiers or form values anyway. Worse, `browser_get_text` uses `chromedp.Text` (innerText, `tools.go:697`) which never emits input values, while `accessibility.Node.Value` (`accessibility/types.go:461`) does — a snapshot emitting `Value` is a **strict widening**, not an inheritance. | **JUDGEMENT CALL, flagged for ruling.** This spec proposes: (a) **omit `Node.Value` by default** for nodes whose role is `textbox`/`searchbox`/`combobox`/`spinbutton`/`slider` or whose DOM element is `<input type=password>`, emitting `value: "[omitted]"`; (b) an explicit `include_values: true` opt-in; (c) route the rendered output through `cfg.SensitiveDataReplacer()` **as defence in depth** — so a provider key pasted into a form is caught — while stating plainly that this is not the control that protects form values. **Do not implement (a) as "inherit `browser_get_text`" — there is nothing to inherit.** |
| **A-3** | **`browser_snapshot`'s tier is genuinely open (ADR D2.8, §6 Q1).** D2.4 calls it "the default way an agent reads a page"; Tier 3 is search-only. Also: **Tier 3 in code is the residual, not an enumerated list** — `ToolManifestVisibility` (`manifest.go:243-251`) returns `ManifestSearchOnly` for anything lazy outside the 7-name previewed set. The ADR's "the six new names need explicit entries in the closed 63-name list" is wrong at the code level; and the list is **62**, not 63 (`manifest_test.go:667-681`; `write_agent_metadata` retired). | **BOTH OPTIONS SPECIFIED, neither picked.** **Option A — Tier 3 (all six).** Cost: zero production edits; fixture 62→68; arithmetic 87→93. Consequence: the agent must `ToolSearch` for `browser_snapshot` once per session before it can read a page structurally, which contradicts D2.4's own "default way" wording — and every alternative (`browser_screenshot`, `browser_get_text`) is *also* Tier 3, so the agent may never discover any of them and simply not read the page. **Option B — Tier 2 (`browser_snapshot` only).** Cost: one production edit (`manifest.go:148-156`), previewed 7→8, fixture 62→67, arithmetic 87→93, and `TestVisibility_PreviewedSetIsExactlySeven` must be renamed. Consequence: one preview line's tokens on every turn, and the previewed set — sized at 7 by a deliberate ADR-071 decision — grows by one. **Recommendation: Option B**, because D2.4's stated purpose and Tier 3 are not reconcilable and a preview line is the cheaper of the two contradictions. **Operator ruling required.** |
| **A-4** | **The ADR's D2.9 table omits two seeded browser-using agents.** `IDExplorer` (`core.go:756-760`) and `IDResearcher` (`core.go:782-786`) are both granted the full 11-tool browsing surface today. The table names only Jim/Ray/Mia/Ava. Left alone, both land at `deny` for all six (via `denyAllThenOverride`) — no boot abort, but an Explorer that can click and cannot operate a dropdown, which is the "intermittent, learns nothing" failure §5 of the ADR warns about. | **JUDGEMENT CALL, flagged for ruling.** This spec proposes **parity with their existing grant**: the five action/read tools `allow`; `browser_upload_file` **`deny`** (not `ask` — both are unattended delegation-tier workers with no operator at the keyboard to answer an `ask`, so `ask` would hang or auto-deny depending on the approval path, and `deny` is the honest version of that). **Operator ruling required.** |
| **A-5** | **`Page` domain enablement per tab.** `Page.javascriptDialogOpening` fires only where the `Page` domain is enabled. Whether chromedp enables it per-target during its own session bring-up was **not verified**. | **Spike, ordered first in §10.** If it is not already enabled, `browser_handle_dialog`'s listener install must enable it on every tab, which adds a per-tab CDP round trip at tab creation — a latency cost adjacent to FR-007 that must be measured, not assumed. Do not build Stream C past the listener until this is answered. |
| **A-6** | **The `performance-2x` profile is a Fly machine size, not a repo artifact.** ADR-072 §7's numbers came from `/proc` sampling on `omnipus-uat-swimlane`. Nothing in the repo defines the profile or a p95 harness. | **Assumption, recorded.** FR-007 is measured as a **delta against the pre-change build on the same machine in the same session**, 100 iterations, not against an absolute number. An absolute ms figure on an unpinned host would be exactly the unreproducible number `docs/internal/false-green-patterns.md` warns about. |
| **A-7** | **`ErrNotActionable.Failed` reports the FIRST unmet condition, not all of them.** A disabled element under an overlay is both. The ADR says "which condition", singular. | **Assumption, recorded.** First-in-evaluation-order (`visible → stable → enabled → hit-testable`), because a later condition is meaningless while an earlier one is false. Deterministic and testable; a set-valued report would make the table test order-dependent for no agent benefit. |
| **A-8** | **`browser_snapshot`'s handle format.** D2.4 says "the handles needed to act on them" without naming a form. | **Assumption, recorded.** A 0-based `index` into the snapshot's own ordering, which is the **same** document ordering Stream A's multi-match uses — so a handle read from a snapshot resolves identically in the next call. An opaque node id would be a second identity scheme that goes stale on the next DOM mutation with no way for the agent to tell. |
| **A-9** | **`browser_upload_file`'s `FSOp` and confinement.** ADR silent; review unasked-question 6. `PathHandle` cannot mediate the read at all — Chrome opens the file (`SetUploadFiles`, `query.go:1115`), so only `handle.RealPath()` is usable and the TOCTOU that `PathHandle` closes is reintroduced. | **Decision recorded in Stream B, needs ratification.** `FSOpSend` + additional `policy.AllowedRoots` confinement (stricter than `FSOpSend` alone, which carries no path restriction — `resolvepath.go:81-85`). The `ask` seed is the second gate. The residual TOCTOU window is **accepted and stated**, not hidden. |
| **A-10** | **Whether `browser_press_key` without a locator is an "action" for lease/deferral purposes.** It injects input but names no element. | **Assumption, recorded.** Yes — it calls `controlledResult` and takes the write lease, because it fights for the cursor exactly as `browser_type` does. It skips `waitActionable` when no locator is supplied (there is nothing to gate on). |
| **A-11** | **The tool count in ADR §4 ("11 → 17").** Verified correct: `register.go:65-81` registers 11; +6 = 17. The review's M8 (D2.7's "twelve") is already fixed in the current ADR text. | No action. Recorded so the count is not re-litigated. |

---

## 13. Holdout evaluation scenarios (post-implementation, NOT in the TDD plan or the traceability matrix)

1. **(happy)** On a real public site with generated class names, an agent books/fills a multi-field form end to end using **only** `browser_snapshot` + role/name locators + `browser_select_option` + `browser_press_key{Enter}` — no CSS selector in the whole trace.
2. **(happy)** An agent attaches a real file to a real upload form; the operator sees exactly one `ask` prompt, approves, and the upload completes.
3. **(error)** A site fires `confirm()` on navigate-away. The agent hits it, does **not** call `browser_handle_dialog`, and the next browser tool's error tells it what happened and what to call. The agent recovers unaided.
4. **(error)** A cookie banner overlays the page. `browser_click` on the underlying CTA fails naming `hit-testable` **and** the banner element; the agent dismisses the banner and retries successfully — without the operator intervening.
5. **(edge)** A single-page app re-renders between the snapshot and the action. The handle is stale. Assert the failure is a *named* resolution error, not a click on the wrong element.
6. **(edge)** A page with 5,000 AX nodes. Snapshot returns within the page timeout, capped at 64,000 chars, with the top of the tree retained and the marker naming the cap.
7. **(edge)** Two agents on one browsing context both drive a form. One acts; the other's `{"deferred": true}` is visible in the transcript and the agent waits and retries rather than treating it as a failure.
8. **(edge)** A dialog opens on tab 2 while the operator is watching tab 0 in the live panel. The agent recovers tab 2. Record what the **human** sees — this is the accepted gap in §6, and the holdout exists to measure how bad it is, not to pass.
9. **(edge)** `linux/arm64`, where no Chromium is present (ADR D2.7 / #665). All six new tools are registered and fail. Confirm the failure names the missing browser and does not read as a tool bug.
10. **(regression)** With the actionability gate live, run a 10-minute mixed browsing session and compare per-click latency against the pre-change build. Confirm SC-004 holds under real page load, not only on a static fixture.

---

**Next:** `/grill-spec docs/internal/specs/browser-agent-capability-spec.md`, then rule on **A-2, A-3, A-4** (and ratify **A-9**) before Stream A opens, then implement — **Stream E first** (names + policy, no behaviour), then A as critical path, then B/C/D/F in parallel, then the 7-reviewer gate, then SC-001 as the headline demonstration.
