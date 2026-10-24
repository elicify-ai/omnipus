# WU-4 selection, readback and K5 acceptance increment

## Frozen scope and test boundary

Production baseline: `a2b0f0b3496d244a4ee9fee289988fb185a77d1c`.
Publishing worktree: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-wu4-red-selection-20261017`.
Branch: `work/l1-wu4-red-selection-20261017`.
Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/selection-20261017`.

Only new test files and this new acceptance map are authored. Existing tests,
production, prompts, contracts, generated artifacts and frontend are read-only.
Other lanes own origin/SF-02 and registry SF-03/SF-05/S4.

The oracle is the frozen specification and binding gate dispositions, not
observed output. Real tools, registry, encrypted store, revision checks,
authenticated handler and native turn stay inside the tested boundary. Network,
connector runtime and model responses are process-edge fixtures. Credential
presence does not establish live authentication. A saved reference does not
establish successful live application.

## Authority

| Source | Requirements used |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-wu4-red-selection-20261017/docs/internal/specs/omnipus-system-agent-spec.md` | US-6, US-9; sections 6.6/6.7; K5; FR-030/062/063/064/065/070/075/079/085/087/088; BDD-6.8/6.9/6.10/6.14/6.24 |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/architect.md` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/architect-chief-dispositions.md` | F02 paired-reference readback; F03 runtime/contract agreement |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/r4/k5-shape-ruling.md` | Exact existing-component POST 503 union and GET 503 error shape; no historical absence claim |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/silent-failure-hunter.md` | SF-04 saved replacement versus stale live connection |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/pr-test-analyzer.md` | T1–T3 coverage additions; not demonstrated production vulnerabilities |

## Acceptance rows derived before execution

| Group | Cases and expected observable outcome | Instrument / discriminating regression |
|---|---|---|
| F02 | Real paired Slack card Submit, then reference attachment on default and namespaced unbound instances. `list_channels` reports `configured`; `test_channel` reports success/credentials present and disabled. Legacy token and identity remain recognized. No-ref negative reports `needs_credentials`/no credentials. No platform probe/save/reload on readback. | Actual list/test result bodies, unchanged config/store bytes and counters. Shared write inventory is implementation guidance, not an expected-value helper. |
| SF-04 | Select v2 while a runtime-edge fixture retains connected v1 and reconciliation fails before application. Result is `saved but not working`, safe reason, saved v2/revision retained, both values unchanged. Successful apply reports working; identical attachment is no-save/no-reconcile. CREATE-only Q4 prefix is retained and never added to attachment. | Runtime fixture records selected versus applied reference independently. A stale connected status must not override reconciliation failure. |
| F03/K5 | Authenticated POST and GET missing-registry/init/reconciliation failures return value-free ErrorResponse 503 without invented state. Postcommit metadata and dispatch cuts return submitted SecretRequestState 503 with receipt references. Validate actual bodies against the operation's resolved response schema. Same-ID duplicate returns 409 without a second version; state reads never replay values. | Real response validator must accept a valid state control and reject malformed state/error controls. Current undeclared unavailable responses must show behavioral contract-validation RED before backend regeneration. POST union uses existing internal refs; GET uses existing ErrorResponse; preserve component requirements. |
| T1 | Current expected revision; first matching provider row A, second differing endpoint or protocol B. A-bound reference refuses atomically, with both old references retained, exact bytes unchanged and zero save/reload/probe. Homogeneous two-row control selects on both rows. | Narrow proposed mutant validates only first row but still writes all rows. Coverage baseline PASS is not defect RED. |
| T2 direct registry | Direct real ToolRegistry invocation for all three integration tools refuses plaintext before its own argument-bearing log. An ordinary public call's exact arguments appear in the same real file logger. Config/store/counters unchanged. | Narrow proposed mutant moves only the registry call-site refusal below first argument log, retaining native guards. |
| T2 post-hook | Real native turn starts with safe call; counted BeforeTool hook rewrites to protected plaintext setup. Refusal precedes approval, argument log, start frame and persistence. Public non-secret hook rewrite still reaches execution, frames and persistence. | Narrow proposed mutant removes only post-hook recheck, leaving normalization and registry guards intact. |
| T3 | Real noncatalog endpoint-only request_secret -> card Submit -> current-revision configure_provider at identical/host-case/trailing-slash-normalized URL succeeds without rewriting credential storage. Changed host/scheme/path refuse atomically. Catalog provider/protocol identity stays strict. | Narrow proposed mutants remove endpoint-only identity exception, bypass endpoint equality, or relax catalog identity. Expectations use literal normalized endpoints, never the production normalizer. |

## Publication and execution integrity

Each process selects exactly one anchored top-level TestName and one package,
through `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh`,
lane `wu4qas`, with `CGO_ENABLED=0`, `GOTOOLCHAIN=go1.26.9`, `GOMAXPROCS=1`,
`GOGC=100`, `GOMEMLIMIT=3GiB`, tags `goolm,stdjson`, `-p 1 -count=1 -v`.
Raw logs, direct exit, exact command, commit/tree and source hashes accompany
runs. Compile/collection errors are not behavioral RED. Groups publish as soon
as runnable; nothing waits for completion of all groups.

GitNexus MCP is unavailable in this session. The brief identifies the registered
sibling graph as over 4,250 commits stale; it cannot establish current callers or
low impact. Direct caller/source reads are the fallback; impact is Inferred.
No reindex or graphify is run. Precommit scope checks compare actual changed
paths and ensure that only new test files and this map are staged.

## Explicit limits

| Limit | Treatment |
|---|---|
| RED versus CHECK | Author records actual defect RED and coverage baseline PASS separately. GREEN implementation, independent mutation verification and the skill's proof-of-failability checklist are deferred to a fresh CHECK instance. The role's separate-context rule prevents self-CHECK; targeted coverage mutation execution requested from dispatcher. |
| Full-suite/platform/race/CI | Not run locally and not inferred from narrow results. CI remains the authority. |
| Live service authenticity | No Slack authentication or external MCP connection claim; fixture connectivity does not prove new reference application. |
| No automatic plaintext replay | Server tests count submitted versions and only use GET for reconciliation; browser replay behavior is source-read only here, not browser-tested. Existing frontend suite remains unchanged. |
| Later units / other lanes | No origin, SF-02, SF-03, SF-05, S4, private-secret, rollback or channel-connect workflow duplication. |
