# B25 on-demand evaluation plan

This is an operator-invoked measurement utility, not a CI shard, regression gate,
or a claim that the feature passed B25. Production code is out of scope.

## Independent oracles

Source: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-b25/docs/internal/specs/chat-history-paging-spec.md`::Adopted performance envelope, Content-free observability, B25, D-U1.

| Row | Instrument / independent expectation |
|---|---|
| Data | Canonical `TestChatHistoryFixtureDriver`, 90 real append days, 100,000 groups, encoded archive >=104857600 bytes, mixed text/tool/image/clear/system. Small rehearsal deliberately cannot pass the full-data row. |
| Server healthy-index | Exactly 30 distinct cuts per limit (40/100), warm and fresh-process. Match one new real `history_page` diagnostic per request; nearest-rank p95, <=300/750 ms. Setup/index construction excluded; reject mutation recovery/non-success or absent counters; T18's separately charged saved-anchor validation (at most one record) remains allowed. |
| UI ready | Real embedded SPA, authenticated isolated Chromium, 30 route selections; visible populated recent page, catch_up_complete, enabled composer, idle history, settled recent bottom, all actual opening requests included. p95<=1500 ms; actual gateway round trips <=100 ms. |
| Normal reading | Real wheel input <=1 viewport/second; observe 30 completed older boundaries and response<=500 ms. Sample visible row coverage every animation frame. Measure the same visible anchor immediately before/after real page delivery, independently of user scroll. Settled drift<=1 CSS pixel. Missing comparable anchors is UNMEASURED, never zero. |
| Prefix independence | Import exact-candidate Section-H producer receipt AND real diagnostic-pair evidence. Do not duplicate the producer, build 100k locally, or treat receipt absence as zero. Four selected/dependency record/byte counters and allocated bytes differ <=10%; omitted prefix decoding must be independently evidenced. |
| Row memory/replay | Actual CDP retained heap after collection, HTTP encoded transfers and WS bytes/frame counts, source/drawn counts, sampled process RSS (explicitly NOT allocated Go bytes), 100 older-page boundaries. Read-only React inspection can expose actual retained history state; if unavailable, caps/cleanup stay UNMEASURED. Inactive release is checked separately, not inferred from an empty DOM. |
| Offline utility | No repair is hidden in ordinary opens. Optional imported candidate-bound offline evidence; otherwise UNMEASURED. |
| Genuine-session rows | No discovery/default/source access. Every row is UNMEASURED (`no founder-approved copy supplied`) unless an explicit approved copy inside the supplied isolated data directory is given. Refuse missing, symlinked or forbidden source. Never open the founder's live data. |

## Real boundaries

Real gateway, archive writer/index, generated REST/WS validators, browser/layout,
network and allocator receipt. Only the external provider onboarding probe is
answered locally; no chat/model turn or controls execution is claimed. The approved
provider/model configuration remains openrouter + deepseek/deepseek-v4.1-flash.

## Instrument self-controls (not feature acceptance)

| Case | Expected result / source |
|---|---|
| Nearest-rank p95, 30 ordered samples | 29th sample (ceil(.95*30)); arithmetic, not product output. |
| Empty/NaN/incomplete timings | UNMEASURED, not PASS/zero. |
| Exactly 300/750/1500/1 px thresholds, just over | PASS at boundary, FAIL above; envelope. |
| 29/30/31 samples | 29 cannot pass; all actual samples recorded, required campaign remains 30. |
| Missing/mismatched receipt or failed producer | UNMEASURED/FAIL, never PASS. |
| Counter total contradiction / missing metric / index recovery | Instrument refusal, never healthy-index PASS. |
| Default synthetic run | No genuine-copy FS operation and no path under the forbidden live-data root opened. A child FS audit records read paths and checks this explicitly. |
| Explicit forbidden alias/symlink/missing copy | Refused before reading session bytes; founder boundary. |
| Known too-slow samples, >500-reader or >100-tail | FAIL, demonstrating adverse evidence is not converted to green. |

## Known gaps / handoff

The local rehearsal uses 2,000 groups and is not real-session acceptance. Full D-U1,
Section H, independently supplied offline evidence and independent UAT-validator
re-drive remain separate. UI control existence/readiness is not live send/approval/
Stop/reconnect/clear/Retry acceptance. Missing measurements are retained with reason.
No CHECK of this author's work is performed here; feature RED-before-GREEN and
mutation audit are deferred to a separate CHECK instance. No generated data is committed.

GitNexus tools and an index are unavailable in this checkout. New utility symbols
have no pre-existing callers; direct scope sweeps cover the test tree and existing
fixture/producer consumers. Impact is Inferred, not a claimed graph analysis.
