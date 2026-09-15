# Reconciliation snapshot lifetime plan

Source: FR-016 session retirement and the exact-session popup ownership contract.
The target list, historically tracked opener IDs, and session lifetime form one
snapshot. A retained result cannot authorize attachment in a replacement or dead
session. Opener closure alone remains valid historical provenance.

The real ReconcileTabs, snapshot reader, admission, adoption and lifecycle methods
remain intact. Tests control only native listing/attachment, native context cleanup,
and a real tab-observer callback. Healthy memory is supplied at the host-measurement
boundary. Expectations are fixed before production edits.

| Timeline | Required outcome |
| --- | --- |
| Native list returns after original browser context ends | No native adoption; stale session error |
| List returns after session removal while native cleanup remains pending | No native adoption; stale error despite still-live old context |
| Native attachment starts, then its original session is removed | Stale error is propagated rather than reported as an ordinary stranded tab |
| First popup is adopted, original session ends before second adoption or final return | One native attach only; stale error, no stale success outcome |
| Listing completes while opener closes; original session survives | Popup adopted normally using historical opener ownership |

Preserve existing no-session, unrelated target, memory refusal, transport error,
multiple-popup aggregation and reconciliation/adoption gate behavior. A stale
reconciliation aborts with ErrBrowserSessionChanged and an empty outcome; the
browser_click caller already handles reconciliation errors without consuming an
outcome from that failed pass.

Baseline precedes implementation. The bounded fix returns the original session
from reconcileTargetSnapshot and passes it to the existing qualified adoption
helper. Deliberate faults must prove snapshot lifetime, adoption qualification,
and stale-error propagation, followed by exact restoration and relevant race
checks. The team serializes Go execution; no full CI or real Chrome acceptance is
claimed by this slice. Direct callers and staged scope use grep/manual inspection;
GitNexus remains disabled by explicit user instruction.

## Recorded evidence

Baseline 59747 exited 1 against unchanged production. All four initial negatives
failed through behavioral assertions: a dead browser snapshot still attached a
target and reported success; a removed owner with cleanup pending returned no
error; retirement during attachment became an ordinary Unadopted result; and
retirement between two adoptions still attached the second target and returned
success. Historical opener closure passed. No compilation or fixture error
occurred. The exact same-ID replacement interval was established by source
inspection rather than an intrusive scheduling hook; session removal with the
old context deliberately alive isolates pointer identity from cancellation.

Milestone 65456 exited 0. Initial and restored race selections each passed 47
entries, with zero skips and no race report. The selection was
`^(TestPassivePopup|TestAdoptTarget|TestReconcile|TestHandleTargetEvent|TestLifecycleLateAppend|TestLegacyTabCommand)`
in `./pkg/tools/browser`, tags `goolm,stdjson`, `-p 1 -count=1 -v -timeout=120s`,
GOMAXPROCS=2. Initial/fault phases used CGO_ENABLED=0; restoration enabled CGO
and `-race`. The new one-target final-return case was added before this milestone
and proved by the final-qualification fault, not claimed as an earlier baseline.

The combined fault run exited 1 with seven failing and one passing entries
(including enclosing groups). All three faults had concrete assertion evidence:

- Dropping the original owner at adoption allowed native attachment after the
  original browser context ended, including the second item in a retained list.
- Swallowing the stale-session error during attachment returned nil instead of
  the required retirement error.
- Bypassing final qualification returned stale success when the original session
  ended during the last adoption's observer callback.

Historical opener closure remained a passing positive under those faults. The
driver restored every original production byte before the race phase. Expected
native-cleanup timeout logging in the held-cleanup fixture uses virtual time and
its blocked cleanup is explicitly released before the test exits.

Raw logs:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/reconciliation-lifetime/`
(`baseline.log`, `initial-green.log`, `combined-faults.log`, `restored-race.log`).
Manual caller inspection found one production snapshot caller (ReconcileTabs)
and one tool caller (browser_click). The finite production scope is that snapshot
function, ReconcileTabs and two directly affected ownership comments. Independent
review, integrated root validation, full CI and actual Chrome acceptance are
parent-owned; no additional broad audit is part of this delivery.
