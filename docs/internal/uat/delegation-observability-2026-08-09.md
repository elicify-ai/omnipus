# UAT — parallel-artifact delegation, 2026-08-09

- **Deployment:** `omnipus-uat-swimlane` (Fly, `sin`, performance-2x/4GB), image
  `deployment-01KZJRXM84X9WK13M40KH66CSS`, built from `release/v0.1.1` @ `ae93a45e`.
- **Model:** `x-ai/grok-4.5` (OpenAI-compatible provider).
- **Parent session:** `session_01KZJSVAX1QWYFFRA29HEHYGYP` (agent `jim`), origin chat
  `webchat:582bf707-d753-42f2-a145-52d4e8b9e6c2`, workspace `01KZJS3V1ECKV453XBE2QFSDGA`.
- **Task given by the operator:** produce three artifacts — two SVGs and one Markdown document with
  Mermaid diagrams — using parallel subagents.

## Provenance and reproducibility

The figures below were extracted from the persisted session files pulled off the running machine
(`$OMNIPUS_HOME/sessions/` and `$OMNIPUS_HOME/session_lifecycle/`) on 2026-08-09 and analysed offline.
**The source deployment has no volume**, so that state was destroyed by the next deploy and the raw
files are no longer retrievable. This report is therefore the record of record; the counts here are
author-extracted and cannot be independently re-derived from the deployment.

Every figure below is a direct count from the JSONL transcripts, not an estimate.

## 1. Timeline

| Time (UTC) | Event |
|---|---|
| 08:48:51 | Turn starts. Wave 1: 3 workers spawned (2 SVG, 1 Markdown). |
| 08:48:58 | First `bash` call (`sleep 8`) — **fails**. |
| 08:49:04 | `overview.md` written successfully by worker `9bdc7376`. |
| 08:49:29–30 | 4 `steer` calls to the two silent SVG workers. No effect. |
| 08:49:37 | Both wave-1 SVG workers cancelled after 46.6 s of apparent silence. |
| 08:49:49–50 | Wave 2: **6** workers spawned — for **2** remaining artifacts. |
| 08:49:59 | Wave 3: 2 further workers spawned. |
| 08:50:02 | `agent-flow.svg` written successfully by worker `0c8d3530`. |
| 08:50:03 | `workspace-card.svg` written successfully by worker `9e08a653`. **All three artifacts now exist.** |
| 08:50:04–13 | 5 duplicate workers attempt the same two filenames; all get "already exists". |
| 08:50:13–41 | 9 further `bash` calls, all fail. |
| 08:50:36 | Parent's own 2 `write_file` calls fail — "already exists". |
| 08:50:45 | Turn ends. |

**Total elapsed: 114 seconds. Work complete at 08:50:03 — 72 seconds in. The remaining 42 seconds
were spent over already-finished work.**

## 2. Parent tool usage (232 calls in 114 s)

| Tool | Calls | Failed |
|---|---:|---:|
| `delegate` | 132 | 28 |
| `read_file` | 36 | 0 |
| `list_directory` | 21 | 0 |
| `list_jobs` | 15 | 0 |
| `set_todos` | 14 | 0 |
| `bash` | 11 | **11 (100%)** |
| `write_file` | 2 | **2 (100%)** |
| `load_tool` | 1 | 0 |

`delegate` by action (sums to 132):

| Action | Calls |
|---|---:|
| `run` (spawn) | 11 |
| `status` | 75 |
| `peek` | 7 |
| `inbox` | 7 |
| `steer` | 4 |
| `cancel` | 28 |

**20 of the 28 `cancel` calls returned a hard error** of the form
`delegate: cancel: session <id> is already terminal (completed|cancelled|failed) — nothing to cancel`.

## 3. Subagent roster (11 workers for 3 artifacts)

| Wave | Spawned | Count | IDs |
|---|---|---:|---|
| 1 | 08:48:51 | 3 | `b5a76216`, `4259f3c6`, `9bdc7376` |
| 2 | 08:49:49–50 | 6 | `57eb647f`, `1101f946`, `fad86487`, `0c8d3530`, `9e08a653`, `f8fd8227` |
| 3 | 08:49:59 | 2 | `7bda9f10`, `ca50086d` |

Final lifecycle states: 3 `completed`, 4 `cancelled`, 4 `failed`.

### 3.1 The two workers cancelled as "stalled"

`b5a76216` and `4259f3c6` each ran **46.6 seconds** and, from every surface the parent could observe,
did nothing: no persisted transcript rows, no inbox messages, no lifecycle change beyond `running`.
Their own transcripts each contain exactly one assistant message:

- `b5a76216`: *"Creating a clean workspace-dashboard SVG and saving it to `workspace-card.svg`."*
- `4259f3c6`: *"Creating a polished self-contained SVG of the parallel agent flow and writing it to `agent-flow.svg`."*

That is the narration preceding a tool call whose arguments — a multi-kilobyte SVG — were still being
generated. Both were cancelled at 08:49:37. **They were working.**

### 3.2 The control case

Same session, same model, same `worker` agent:

| Worker | Prompt constraint | Outcome |
|---|---|---|
| `9bdc7376` (Markdown) | none needed — prose | wrote its file in **16 s** |
| `b5a76216`, `4259f3c6` (wave 1) | *"polished… cohesive palette… icons"*, **no size limit** | silent 46.6 s, cancelled |
| `0c8d3530`, `9e08a653` (wave 2) | **"Keep it concise (under 100 lines of SVG)"** | wrote their files in **13–16 s** |

The variable is output length, and output length is invisible.

### 3.3 Duplicate-write collisions

Five workers lost a race to write a filename a sibling had already written successfully, and received
`file: <name> already exists. Set overwrite=true to replace.`:
`1101f946`, `fad86487`, `f8fd8227` (08:50:04–05), `7bda9f10`, `ca50086d` (08:50:12–13).

**Every one of them was terminated within seconds of that result — all five transcripts end at the
errored `write_file` with no assistant turn after it.** The incident therefore shows the ambiguity
being created but never shows a worker responding to it.

## 4. `bash` — 100% failure, not load-related

All 11 calls failed. The **first** was `sleep 8` at 08:48:58, 7 seconds into the turn with only 3
workers alive. Gateway log text for the later ones: `sh: 1: Cannot fork`. Machine state measured
while this was happening: **~3 GB free RAM, loadavg 0.00, no swap, 274 tasks across ~99 processes.**

Root-caused separately: `RLIMIT_NPROC` was sized by counting processes while the kernel enforces
against tasks, so the cap (99+128=227) landed below the live task count (~274) and every `fork()`
returned `EAGAIN`. Fixed in `a79976c4`, hardened in `f7d0f4bf` and `29ab227f`.

Consequence for this incident: the parent could not run `ls` or `wc` to check whether the artifacts
existed, so it could not self-correct.

## 5. Accounting

Parent session `stats.json`: `tokens_out` 1,407,458, `cache_read` 1,069,952, `cost` $1.08,
`tool_calls` 232, `message_count` 90. `tokens_in` **0** — structurally always zero; no caller
populates `Tokens` on a user-role entry (documented separately).

Completed workers recorded `tokens_out` of 17,130 / 17,437 / 17,454. The cancelled and failed workers
recorded **0**, including the two that demonstrably generated narration — accounting, like the
transcript, only lands at round completion.

## 6. Outcome

All three artifacts were produced and verified correct. The operator asked the orchestrator for a
post-mortem, and its own table correctly identified stalled parallel workers, ineffective steer, turn
cancellations and duplicate cleanup — but attributed the SVG failures to "resource contention /
workers idle after spawn", which the transcripts show is wrong: the workers were mid-generation.

Referenced by [ADR-059](../architecture/ADR-059-delegation-observability.md).
