# B25 on-demand evaluator

**Not a CI shard, test gate, or independent B25 acceptance.** This utility measures
an already-built gateway with its embedded UI. It never builds the gateway, repairs
an index, sends a model turn, launches Fly, or discovers founder data.

Source of thresholds:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-b25/docs/internal/specs/chat-history-paging-spec.md`::Adopted performance envelope / B25 / Content-free observability.

## One command: small rehearsal

Prerequisites: Node 24+, Python 3, Go (only `go version -m` is used), installed
`@playwright/test`, Chromium and `esbuild`, plus a dispatcher-built gateway and
`pkg/session` test executable for the exact candidate. No install or full build is
performed by the evaluator. All processes run sequentially inside **one** local
`slotrun` allocation, lane `squad-1266-b25`.

```bash
bash /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-b25/tests/perf/chat-history-b25/run.sh \
  --candidate ef227406ff493f1ece29d5b1bd2583c23718dfa6 \
  --gateway /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/b25-artifacts-ef227406f/build/omnipus \
  --fixture-binary /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/b25-artifacts-ef227406f/build/session.test \
  --artifact-attestation /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/b25-artifacts-ef227406f/artifact-attestation.json \
  --dependencies /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/web-deps/node_modules \
  --groups 2000 --smoke \
  --output /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/b25-eval-evidence/my-small-rehearsal
```

Use a **new output directory** on every invocation. By default, candidate sources
are this checkout; `--candidate-source <absolute clean candidate checkout>` permits
read-only evaluation of another built candidate without changing the evaluator's
branch. The source diff for runtime, generated contracts and reused fixture code
must be empty against `--candidate`. Build info must have that exact revision.
The SHA and both executable hashes are saved. `--artifact-attestation` is required
because the compiled session test binary carries no VCS stamp. It must match the
candidate and both hashes and list **only** the generated design-system provenance
file as the allowed build-time tracked change. This is an explicit builder
attestation, not a claim the evaluator independently observed the build.

`--smoke` uses 3 distinct pages per server case, 3 actual UI selections and up to
6 older boundaries. It **cannot** pass the 30/100-sample rows. It still runs real
canonical generation, process restarts, Chromium, retained heap/state probes,
traffic capture, screenshots and trace. Without `--smoke`, the campaign uses
30 pages in each of four server cases, 30 UI opens, 30 normal boundaries and
100 traversal pages. A small dataset may still run that campaign without meeting
the full Data row; no synthetic result becomes genuine acceptance.

| Exit | Meaning |
|---|---|
| 0 | All reported envelope rows measured and passed. This does not replace the independent validator. |
| 1 | A measured condition failed, or setup/the instrument failed. Read the JSON reason and trace. |
| 2 | No measured failure, but one or more rows are UNMEASURED. **Not success.** Expected for a smoke or missing genuine evidence. |

## Data boundary

| Input | Behavior |
|---|---|
| Default | Synthetic only. No genuine path lookup or discovery, including existence checks. |
| `--groups 2000` | Real canonical appends through existing `TestChatHistoryFixtureDriver`, not handcrafted transcript/index files. 90 actual append days; text, completed tools, embedded 1-pixel images, system and exact clear rows. |
| `--synthetic-copy <directory>` | Accept an existing isolated synthetic session directory. Needs a valid published index; source is read-only. The Data row stays UNMEASURED without original generation provenance. |
| `--ui-built-copy <directory>` | The same read-only interface for an isolated session built through the UI. No genuine copy or synthetic generation is needed. Its actual archive size/source-record shape is recorded outside timings. |
| `--genuine-copy <directory>` | **Optional, explicit, founder-approved copy only**, supplied later by the dispatcher after founder approval. Never a live source. Requires `--input-root <isolated directory>` and the copy must be its child. |
| Missing genuine argument | Every genuine row is `UNMEASURED — no founder-approved copy supplied`. |
| Missing/symlinked/forbidden copy | Refused before reading session bytes. No fallback to any other source. |

Copy inputs also require `--input-root`. An accepted copy is cloned again into an
owned disposable gateway home. Only the working copy's identity metadata
(`agent_id`, `workspace_id`, `owner`) is re-homed to isolated REST-created identities;
the original session ID and every archive/index byte are preserved. The source
manifest is checked again after the campaign. No original source is written.
A copy without a healthy index is refused; this utility will not hide conversion
inside the first open.

The new gateway owns a throwaway home and free port, never port **10994**.
Gateway, fixture and browser children get isolated home/config paths. The Node
filesystem audit records reads and blocks the founder's live-data root; it is
not an OS-level syscall audit of native Go/Chromium children. The default-path
self-test additionally supplies a poison filesystem adapter that fails on any
optional-source lookup. Child-home isolation is separately checked. No founder
data was read or copied during authoring or the synthetic rehearsal.

### Full 100,000-group generation

**Do not run it locally.** The evaluator refuses generation of >=100,000 groups
unless `FLY_MACHINE_ID=d8d3977c946558` and `--go-log <absolute copy of authorization log>`
contains an actual `GO B25...` line. The dispatcher must first obtain that line
from the squad lead in
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/d8d3-done.log`,
reserve the machine, verify its lock, and collect evidence before stopping it.
The evaluator never obtains authorization, starts/stops a Fly machine, changes
a runner or manufactures a GO line. The full generation/run and any Section-H
producer execution are the UAT lane's job. Existing approved input copies avoid
regeneration, but do not waive reference-host/authorization rules.

## What each envelope row measures

| Row | Instrument and rule |
|---|---|
| Data | Canonical writer receipt, actual 90 partitions, source-group count and actual encoded archive bytes. Full synthetic threshold is exactly 100,000 groups and >=104,857,600 bytes, with all five shapes. Copy shape scan is outside timing and counts source records, not invented projected groups. |
| Server healthy-index | Real `history_page.duration_ms` and every physical-work counter; 30 distinct opaque cuts (pages may overlap) per limit 40/100. Warm case is primed; cold case restarts the gateway for **every** page. Nearest-rank p95 (29th of 30), <=300/750 ms. Existing checkpoint checked before/after. No mutation recovery; T18 permits at most one separately counted saved-anchor validation. Cold process is **not** a flushed OS disk cache. |
| UI ready | 30 selections from another genuinely selected session in the real embedded SPA. Timer includes page visibility, enabled composer, actual catch-up completion, fonts, idle paging and stable recent-bottom layout; includes real opening top-ups. <=1500 ms p95, with actual `/health` gateway RTT <=100 ms before each selection. Browser/version/user agent/viewport/host recorded. |
| Normal reading | Real wheel input at <=0.75 viewport/s, sampled each animation frame for blank history coverage; actual gateway older pages delivered unchanged through Playwright's route API. Measure same visible anchor immediately before/after delivery and layout settlement. Wheel pauses briefly during the anchor observation so user movement is not mislabeled as drift. Require 30 comparable boundaries, response<=500 ms, no gap and <=1 CSS pixel drift. Missing comparable anchor is UNMEASURED, never zero. |
| Prefix independence | Import the **existing** `TestHistoryPageMeasurement_SectionHReceipt`, plus its successful named-run/full-read-negative-control log and actual 1000/100000 prefix diagnostic pair. No new allocation producer or stopwatch substitute. All four selected/dependency record/byte counters and allocation bytes vary <=10%; explicit independently evidenced omitted-prefix decode count is zero. |
| Row memory/replay | CDP `HeapProfiler.collectGarbage` then `Performance.getMetrics` for retained renderer heap; HTTP encoded transfer bytes, WS payload bytes/frame counts, source/drawn-row counts and replay source ownership counts. Actual current Zustand store is read through CDP reachable-object inspection, not a mounted-row proxy: 100 distinct page states enforce 500-reader/100-tail source bounds, orphan-owner cleanup and inactive idle payload/map release. Unavailable/ambiguous inspection is UNMEASURED. Sampled process RSS is recorded separately, **never called attributable Go allocation or allocator peak**. Section-H allocation evidence remains isolated-operation evidence, not mixed-dataset per-request attribution. True server allocator peak is currently UNMEASURED (no product profiler seam supplied). |
| Offline utility | Separately imported stopped-command throughput/peak-scratch/canonical-and-backup-integrity evidence. Never run as part of boot/open and never presumed passed. |

This is not an automated replacement for all B25 hands-on lanes. Clear/reconnect/
Retry/Return, keyboard focus, real send/approval/Stop and an independent
validator's fresh-account re-drive remain required. No model execution is needed
here; only the **external provider onboarding probe** is simulated locally. The
isolated provider/model configuration is openrouter + deepseek/deepseek-v4.1-flash.

## Optional evidence imports (private artifact formats)

| Flag | Required evidence |
|---|---|
| `--allocation-receipt <file>` | Exactly three lines: `HEAD: <40-character candidate SHA>`, then short_prefix and long_prefix `history_page_measurement` JSON from the existing producer. Positive sample counts; limit40/min_rows20; actual allocated bytes. |
| `--allocation-run-log <file>` | Named producer PASS, `T18_FULL_READ_NEGATIVE_CONTROL=PASS`, no named FAIL, directly captured `exit=0`. Missing producer success is UNMEASURED even if a receipt exists. |
| `--prefix-work <file>` | Object `{candidate, prefix_groups:[1000,100000], same_suffix_and_dependencies:true, omitted_prefix_decoded:0, samples:[<short real history_page>,<long real history_page>]}`. Supply the actual same-suffix fixture evidence pack; these declarations are inputs to independent validation, not a new collector-generated claim. |
| `--offline-evidence <file>` | Object with exact `candidate`, numeric `throughput_bytes_per_second`, integer `peak_scratch_bytes`, boolean `canonical_integrity`, `backup_integrity`, plus evidence paths/method/host in that object. Imported, not independently re-driven. |

Never relabel an old receipt with a new candidate. Missing allocation evidence is
not zero and a successful producer alone does not prove the mixed-data byte peak.

## Evidence and independent validation

All numeric observations, partial failures, imported receipts, SHA/build binding,
content-free diagnostics, browser errors and filesystem audit are in one
`report.json`; `summary.md` shows each row's **PASS / FAIL / UNMEASURED**, observed
numbers and reasons. Screenshots, redacted structural snapshots and the private
Playwright trace are under each dataset's evidence directory. Opening screenshots
and failure screenshots carry the evaluator account/role/dataset/SHA badge. A
validator must use its own separate account and produce its own clearly badged
evidence; do not pass off this operator evaluator's images as validator evidence.

The trace/screenshots and generated fixture plan can contain permissioned content.
Keep the entire output directory private and **never commit generated data**.
Gateway home is deleted after actual process exit; failures retain evidence. No
paths or binary names are guessed from the live instance.

Instrument controls can be run alone through the same slot wrapper:

```bash
bash /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh \
  squad-1266-b25 \
  /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/b25-eval-evidence/selftest.log \
  node --test /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-b25/tests/perf/chat-history-b25/selftest.mjs
```

Controls check adverse measurements, missing evidence, independent thresholds,
wrong SHA, actual counter contradictions, caps, explicit data refusals and the
default no-genuine-I/O path. They are not a fresh CHECK audit or proof that the
production feature met the envelope. Feature RED/green/mutation proof remains
with the existing packs and a separate CHECK instance.
