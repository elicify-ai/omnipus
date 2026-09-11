# Popup ownership lifetime regression plan

Source: browser-improvements session retirement requirement FR-016 and passive
popup ownership correction. An event belongs to the exact tab set that admitted it using proven historical opener ownership; retries cannot transfer that authority to a replacement.

The real event callback, adoption, retries, command admission, CloseSession,
CloseTab and session creation remain intact. Only native target attachment is
controlled through the existing factory; synchronization-test time advances retry
backoff without wall-clock scheduling assumptions. Healthy memory is supplied at
the existing host measurement boundary.

| Timeline | Expected observation |
| --- | --- |
| Transient attach failure, original opener remains | Exactly two attach attempts, popup appended to original set |
| Attach fails, CloseSession, same ID recreated before retry (including native cleanup still pending) | One popup attempt only; replacement retains its one new tab |
| Attach fails, opener closed while another tab survives | Two attempts; historically owned popup joins the original surviving set |
| Attach fails or native attachment is pending, original browser context ends | No new attach against the dead original browser lifetime |
| Native attach held, original session closed, completion released | Candidate disposed exactly once, no session recreated, no further attempt |
| Native attach held, opener context ends, original session survives | Popup still adopted; historical opener proof remains valid |

Baseline runs the complete new case set against unchanged production. The
correction retains original session identity and historical opener provenance through asynchronous dispatch,
admission, attachment and each retry. A combined fault run removes session,
browser lifetime and post-attach qualification, followed by exact source restoration and
the relevant adoption/lifecycle race tests. Existing direct ReconcileTabs
adoption and bounded transient retry semantics remain covered.

No real Chrome popup, browser UI run, full CI or independent review of this
followup is established by these component checks. Go execution requires the
team's serial slot. Direct callers and staged scope are inspected manually;
GitNexus is disabled by the user's explicit instruction.

The parent clarified before baseline that opener closure is a positive case:
historical opener ownership remains valid within the exact surviving original
session. Opener membership/liveness is not an additional publication condition.

## Recorded evidence

Baseline session 41447 exited 1 against unchanged production. Same-ID replacement
and original browser-context death each incorrectly produced a second native
attach (expected one). Unchanged-owner and opener-removal positives passed, as did
late attachment disposal after CloseSession. The native-opener-closure positive
completed its assertions but exposed a fixture-only double chromedp cancellation
during cleanup. The fixture now wraps that native cancellation with sync.OnceFunc;
the expected popup survival behavior was not changed.

Milestone 16632 exited 0. Initial green and restored race selections each passed
39 entries with zero skips; the race run reported no race. Selection:
`^(TestPassivePopup|TestAdoptTarget|TestReconcileTabs|TestHandleTargetEvent|TestLifecycleLateAppend|TestLegacyTabCommand)`
in `./pkg/tools/browser`, tags `goolm,stdjson`, `-p 1 -count=1 -v -timeout=120s`,
with GOMAXPROCS=2. Initial/fault phases used CGO_ENABLED=0; restoration used
CGO_ENABLED=1 and `-race`.

Three combined production faults produced three distinct assertion failures:

- Ignoring original session pointer: replacement while native cleanup remains
  blocked produced a second native attach.
- Removing cancellation from retry waiting: the real retry completion channel
  remained open after session retirement, without waiting out its backoff.
- Omitting the final browser-context qualification: a candidate attaching when
  the original browser lifetime ended was not disposed (zero instead of one).

The combined fault phase exited 1 with seven passing and five failing entries
(including enclosing test groups). The deliberately uncanceled retry also caused
synctest to report its remaining goroutine after the explicit completion assertion;
this is not counted as another finding. Every production byte was restored before
the race run. The added delayed-cleanup identity, delayed context-death and prompt
retry completion oracles are proven by those faults rather than claimed as part
of the earlier baseline.

Raw logs reside at:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/popup-lifetime/`
(`baseline.log`, `initial-green.log`, `combined-faults.log`, `restored-race.log`).
Manual caller and staged-diff inspection restricts production edits to manager.go's
popup adoption/retry helpers and event dispatch. No pool registration, profile
locking, live-document input, capture or gateway changes are included. This is
implementation verification; the parent performs independent review and browser
acceptance. Full CI and actual Chrome popup behavior remain unverified here.
