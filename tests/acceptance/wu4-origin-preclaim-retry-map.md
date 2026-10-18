# WU-4 P0-d and SF-02 — independent RED increment

## Independent oracles and boundary

Expected results are fixed before the new assertions: the system-agent specification section 6.18 / FR-061 / FR-240 requires a turn's consumed inputs, not its transport or session, to determine origin. A channel input from Anna on telegram-ops is channel-triggered with label `while answering Anna on telegram-ops`; a server-published peer request from noah has label `asked by noah`; a genuine web input has no label. FR-087 requires one reference-only continuation and one durable request-id claim, including retry after a transient rejection before that claim. Explicit Stop is a durable veto, never erased by retry or session revival.

Read-only finding sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/code-reviewer.md` (W1, NOT a rendered-card bypass claim) and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/silent-failure-hunter.md` (SF-02). Frozen production baseline: `a2b0f0b3496d244a4ee9fee289988fb185a77d1c`.

REAL: inbound origin derivation, server peer-wake publication, checked steering queues, ordinary Continue, real steered launcher/admission/post-turn drain, turn/tool context construction, tool-policy dispatch, registry, encrypted store, submit receipt, lifecycle, transcript claim, message bus and attach RetryResumes. Only the external model response is scripted. A registered test workload tool observes the real tool context; it does not replace origin computation or approval/card behavior. Filesystem faults use real dependency files, never a replacement registry, fake ClaimResume, provider metadata or a second executor.

## Acceptance matrix

| Row | State / input | Required observable result | Selector |
|---|---|---|---|
| P0-d ordinary/channel | First ordinary turn fully returns; late channel input joins checked queue with no active turn; actual Continue | Exactly one late-input injection; the executed tool sees Anna/telegram-ops, channel-triggered | TestWU4P0DOrdinaryContinuePreservesChannelOrigin |
| P0-d ordinary/peer | Same closing window; input published by the real peer-wake producer | Executed tool sees `asked by noah`, not channel-triggered | TestWU4P0DOrdinaryContinuePreservesPeerOrigin |
| P0-d ordinary/web control | Same window; genuine web input | Executed tool sees an empty label and not channel-triggered | TestWU4P0DOrdinaryContinueWebOriginStaysUnlabelled |
| P0-d steered/channel | Real dispatch's final-reply boundary, after its body returns and before its real post-turn drain | Fresh real turn's executed tool sees Anna/telegram-ops and channel-triggered | TestWU4P0DSteeredDrainPreservesChannelOrigin |
| P0-d steered/peer | Same final-reply boundary; real server-published peer request | Fresh real turn's tool retains `asked by noah` | TestWU4P0DSteeredDrainPreservesPeerOrigin |
| P0-d steered/web control | Same final-reply boundary; genuine web input | Fresh real turn's tool remains unlabelled | TestWU4P0DSteeredDrainWebOriginStaysUnlabelled |
| P0-d active positive controls | Channel, peer and web messages queued during the first real workload tool's Execute; normal post-tool mid-turn poll consumes them | The next executed tool observes exactly the required origin within the SAME turn | TestWU4P0DMidTurnConsumptionPreservesInputOrigin |
| SF-02 registry read rejection | Actual ordinary secret wake accepted; public FlushAndEvictSessionMeta then meta.json temporarily replaced by a directory; repair; repeated attach retry on the same registry | Zero pre-repair continuation/claim; then exactly one real continuation and durable claim, not stranded by transport acceptance | TestWU4SF02RegistryReadRejectionRemainsRetryableAfterRepair |
| SF-02 lifecycle read rejection | Accepted ordinary wake; actual lifecycle journal temporarily replaced by a directory before claim; repair; repeated attach retry | Same exactly-once recovery with original references/version unchanged | TestWU4SF02LifecycleReadRejectionRemainsRetryableAfterRepair |
| SF-02 restart revival rejection | Ordinary wake accepted; real boot lands restart Stop; revival commits, then its real WriteSteerRevivalState dependency fails a filesystem read at the existing SetRevivalStateWriter seam; repair and retry | Same exactly-once recovery from this pre-claim error; no fake revival failure or weakened Stop precondition | TestWU4SF02RestartRevivalRejectionRemainsRetryableAfterRepair |
| SF-02 duplicate control | Consume healthy ordinary request; repeat attach and old wake, then reload the real registry and repeat | Claim stays byte-for-byte the same sole entry; no second model continuation | TestWU4SF02ConsumedOrdinaryRequestNeverResetsDurableClaim |
| SF-02 durable Stop control | Human and agent Stop of accepted unclaimed request; retry; clear session Stop via public revival; fresh registry/retry | Old receipt remains submitted, saved value/version unchanged; zero continuation/claim; request Stop veto survives | TestWU4SF02ExplicitStopVetoSurvivesAttachAndSessionRevival |

Negative/error classes are the channel/peer provenance regressions and the three pre-claim faults; genuine web, active-turn, duplicate and Stop controls detect an instrument or overbroad-fix error. State ordering, zero/one claim boundaries, zero/one message delivery and repeated retries are explicit. Unrelated numeric input limits belong to existing packs, not this increment.

## Failability and deliberate gaps

RED: execute each anchored top-level selector serially through the designated slot runner, preserve raw output, direct exit and exact source SHA/hash; count named PASS/FAIL lines. A compilation failure, empty selector or wait timeout is not behavioral RED. Existing correct controls are named PASS, not falsely represented as observed RED.

GREEN, three mutation probes and the skill's Proof-of-failability checklist are **deferred to fresh CHECK**. Suggested mutations for CHECK: discard channel origins during post-turn handoff; discard peer origins; incorrectly label web input; retain the in-flight latch on a pre-claim rejection; remove durable claim replay protection; clear ResumeStopped on retry. No implementation is mutated by this RED author.

Not claimed: rendered approval/secret-card bypass, missing transcript-session fixes, full suites/CI, hands-on UAT, independent CHECK, or production correctness. P0-a/b/c and every existing test/assertion remain immutable. GitNexus MCP is unavailable; current source/caller tracing is the impact fallback, labelled Inferred rather than graph-proven safe. Only new test symbols and this map are added.

Evidence directory: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/origin-20261017`.

## P0-d publication and observed results

Published tests-only commit `20870939ac4a7be1b4ae54509ee04d1ce9244661`, branch `work/l1-wu4-red-origin-20261017`. All seven top-level selectors ran separately with exact anchors, unchanged source and the requested toolchain/tags/serial slot runner. Four behavioral FAILs (ordinary and steered, channel and peer): actual tools executed, late messages were consumed once in a fresh turn, but the tool context lost the channel flag/person/connection or the peer label. The ordinary and steered web controls PASS. The active/mid-turn selector and all three named leaves (channel, peer, web) PASS with exact expected labels.

Exact commands, direct exit codes, source SHA/tree/file hashes and raw log paths: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/origin-20261017/p0d-results.json` and its seven adjacent `p0d-<ExactTestName>.receipt.json` files. No build, selector or wait failure is credited as RED. No P0-d assertion/source change followed these runs.

SF-02 safety instruments capture actual event-frame producer inputs, generated session-state card snapshots, the actual bus wake, native model prompts, all durable files under the isolated fixture home, returned diagnostics and both native/structured logs. Both log level controls are set and restored; distinct nonempty positive controls are mandatory before trusting no-plaintext scans. Saved encrypted bytes, reference inventory, every version/recipient binding and the original atomic submit receipt must remain unchanged. These are agent-side transport/snapshot checks, not a rendered browser/WebSocket UAT claim. The restart writer cut happens AFTER the genuine revival mutation but BEFORE ClaimResume, and is labelled that way rather than falsely claiming a refused mutation. Fresh-registry controls occur only after a durable claim or explicit Stop; defect recovery retains the same live registry throughout.
