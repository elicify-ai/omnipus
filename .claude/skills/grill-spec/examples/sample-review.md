# Worked example: grilling an Omnipus feature spec (round 1)

This is a trimmed, illustrative pass — a real review is longer and quotes the
actual spec text at every finding. It exists to show the *shape* grill-spec's
output takes: the findings table split by lens (backend and frontend given
equal weight), the verdict, the "Questions for the founder" list kept
separate from findings, and the round-1 "always run round 2" next step. It
does not correspond to a real spec in this repo.

Last reviewed: 2026-09-25

---

```markdown
# Adversarial Review: Per-agent heartbeat pause control

**Document reviewed**: docs/internal/specs/heartbeat-pause-control-spec.md
**Mode**: Spec
**Round**: Round 1 of 2
**Review date**: 2026-09-25
**Verdict**: REVISE

## Executive Summary

The spec covers the backend toggle adequately but the frontend section is a
placeholder: it names a "Pause" button with no empty/loading/error states, no
keyboard behaviour, and no catalog check. 1 CRITICAL, 3 MAJOR (two of them
frontend), 2 MINOR. Frontend coverage was not as thorough as backend coverage
here — that gap is itself MAJ-001.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 3 |
| MINOR | 2 |
| OBSERVATION | 2 |
| **Total** | **8** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Pause tool has no per-agent policy entry

- **Lens**: Reachability
- **Affected section**: FR-004 ("System MUST expose `heartbeat_pause` as a tool")
- **Failure scenario**: The tool is implemented and registered in the builtin
  catalog, but the spec never states a policy row for any agent. Under the
  two-layer tool-policy model (root `CLAUDE.md` Hard Constraint #6), a tool
  with no explicit per-agent entry rides the global ceiling — which is fine
  only if that's a deliberate choice the spec states. As written, nobody
  reviewing this spec can tell whether `heartbeat_pause` is reachable by any
  agent at all until it ships.
- **Evidence**: FR-004 defines the tool; no "Reachability" section exists in
  the document; `grep -rl '"heartbeat_pause"' pkg/coreagent/ pkg/config/ pkg/tools/`
  returns nothing (not yet implemented, expected pre-implementation — but the
  spec must still state the intended policy).
- **Recommendation**: Add a "Reachability" section: which agents get
  `heartbeat_pause` at what default policy (allow/ask/deny), and confirm this
  rides the ceiling deliberately or needs a per-agent tighten.

---

### MAJOR Findings

#### [MAJ-001] Frontend section is a placeholder, not a spec

- **Lens**: UI states and journey gaps
- **Affected section**: "## UI" (two sentences: "Add a Pause button to the
  agent card. Clicking it pauses the heartbeat.")
- **Failure scenario**: Nothing here tells frontend-lead what the button
  looks like mid-request, what happens if the pause call fails, or what a
  paused agent's card shows afterward. Built as literally specified, a slow
  network leaves the button in an ambiguous state with no loading indicator,
  and a failed pause silently does nothing.
- **Evidence**: The entire UI section quoted above — no loading, empty,
  error, or partial state named; no journey from "agent running" to "agent
  paused" to "agent resumed."
- **Recommendation**: Expand to name: default state (button reads "Pause"),
  in-flight state (disabled + spinner, per the catalogued `Button` loading
  prop), error state (inline error text + button re-enabled, no silent
  failure), and the resulting card state for a paused agent (a persistent
  "Paused" badge, not just the button label flipping).

---

#### [MAJ-002] No keyboard path specified for the pause control

- **Lens**: Accessibility and keyboard
- **Affected section**: "## UI"
- **Failure scenario**: If the agent card's pause control ships as a
  click-only affordance (common when a spec doesn't call out keyboard
  behaviour explicitly), keyboard-only users cannot pause an agent at all.
- **Evidence**: No mention of keyboard operability, focus behaviour, or
  whether the control is the catalogued `Button`/`IconButton` (which get
  this for free) or something bespoke.
- **Recommendation**: State explicitly: "Pause/Resume uses the catalogued
  `IconButton` (`design-system/catalog.json`), reachable by Tab, activated
  by Enter/Space, with `aria-pressed` reflecting pause state."

---

#### [MAJ-003] Pause/resume payload has no contract schema

- **Lens**: Contract-first gaps
- **Affected section**: FR-005 ("The SPA calls the pause endpoint and updates
  the card")
- **Failure scenario**: With no `contracts/components/schemas/` entry, the
  natural path is a hand-written fetch call and a hand-written response
  type on the SPA side — exactly the pattern Hard Constraint #8 and
  `scripts/check-no-handwritten-wire-types.sh` exist to stop.
- **Evidence**: No `contracts/` reference anywhere in the spec; FR-005 only
  describes behaviour, not a schema.
- **Recommendation**: Add a schema (`AgentHeartbeatPauseRequest`/`Response`)
  under `contracts/components/schemas/`, reference it from `openapi.yaml`,
  and state that the SPA consumes it only via `src/lib/api/generated/`.

---

### MINOR Findings

#### [MIN-001] "Pause" and "Paused" used inconsistently for the same state

- **Lens**: Inconsistency and contradiction with ADRs / AS-IS
- **Affected section**: FR-004 uses "paused"; the UI section uses "on hold"
  once
- **Failure scenario**: Low risk on its own, but a naming drift between spec
  and implementation is exactly the seed CON-01 warns about.
- **Recommendation**: Use "paused" everywhere; remove "on hold."

---

#### [MIN-002] No catalog check recorded

- **Lens**: Design-system reuse and brand
- **Affected section**: "## UI"
- **Failure scenario**: The spec doesn't state whether `design-system/catalog.json`
  was checked before assuming a plain button is fine — likely it is, but the
  spec should say so rather than leave it implicit (DSB-01).
- **Recommendation**: Add one line: "Reuses the catalogued `IconButton`; no
  new component required."

---

### Observations

#### [OBS-001] Consider an audit-log entry for pause/resume

- **Lens**: Security
- **Affected section**: General — no audit logging specified
- **Suggestion**: Pausing an agent is an operator action with downstream
  effect (missed heartbeats). Consider stating that it produces an audit
  log entry (`pkg/audit`), consistent with other state-changing operator
  actions.

---

#### [OBS-002] Resume-after-pause boundary not covered in the BDD set

- **Lens**: Incompleteness
- **Affected section**: BDD Scenarios
- **Suggestion**: Add a scenario for resuming an agent that was paused,
  then had its config changed while paused — does resume pick up the new
  config or the one live at pause time?

---

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | `Status: Draft` |
| ADR linked (or explicitly stated not needed) | PASS | States no open design decision |
| Contract changes stated first, citing `contracts/` | FAIL | MAJ-003 |
| API and data section | PASS | |
| UI screens and states (loading/empty/error/partial) | FAIL | MAJ-001 |
| User journey section | FAIL | Folded into two-sentence UI section |
| Accessibility and keyboard section | FAIL | MAJ-002 |
| Design-system components, catalogue-first | FAIL | MIN-002 |
| Security and user promises section (when touched) | PASS | N/A — no `docs/security.md`/`docs/tools.md` promise touched |
| BDD acceptance scenarios, oracle from spec | PASS | |
| Traceability table: requirement -> scenario -> test | PASS | |
| Reachability section (tool policy / screen wiring) | FAIL | CRIT-001 |

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | No | No policy stated (CRIT-001); tool not yet implemented, pre-check |
| User-facing: named screen/component renders it? | Partial | Agent card named, but states unspecified (MAJ-001) |
| Test plan describes execution, not just authorship? | N/A | Pre-implementation spec review |

---

## Questions for the founder

1. Should a paused agent still process an inbound message it was mid-turn
   on, or does pause take effect only between turns? The spec is silent.
2. Is `heartbeat_pause` meant to be available to every agent by default, or
   only to specific roles — this decides CRIT-001's resolution.

---

## Verdict Rationale

The backend requirements (FR-001 through FR-004, minus the reachability gap)
are solid: clear, testable, traced. The frontend section is not at the same
bar — two sentences describing a button is not a UI spec, and it produces
three of this review's four MAJOR/CRITICAL findings. Per the founder's ruling
that plan-spec must cover frontend as fully as backend, this spec is not
ready for round-1 sign-off: REVISE.

### Recommended Next Actions

- [ ] Address CRIT-001: add the Reachability section with the per-agent
      policy decision
- [ ] Address MAJ-001: expand the UI section into states + journey
- [ ] Address MAJ-002: state the keyboard/focus contract
- [ ] Address MAJ-003: add the contract schema before any implementation

### Next step in the process

This is grill round 1 of 2 (fixed). Team-lead interviews the founder on the
two "Questions for the founder" above, then the spec author fixes the
round-1 findings, then grill-spec runs **spec mode round 2** on the corrected
spec at `docs/internal/specs/heartbeat-pause-control-spec.md` — regardless of
this round's verdict. No `/taskify` step exists in this process.
```
