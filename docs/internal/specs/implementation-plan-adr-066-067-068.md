# Implementation plan — ADR-066 / 067 / 068 on `feat/context-budget-and-tool-result-routing`

- **Status:** Proposed 2026-08-23, for operator approval before any code.
- **Inputs:** the three ADRs (Proposed) and their specs, all reviews resolved, zero open questions. Landing order fixed by the cross-spec review: **067 → 068 → 066**, with 067 owning the single coordinated contract commit.
- **Operator rules that bind this plan:** everything ships together on this one branch (no hotfix branch); commit often, never merge; author = Daniel's GitHub identity, no agent trailer; greenfield — no back-compat anywhere; contract-first (Constraint #8); **never run the full Go suite on this machine** — gate through the Fly runner (`runci.sh <ref> <gate>`) and CI (`gh workflow run pr.yml --ref <branch>`, which `pr.yml` allows without a PR); Go tags `goolm,stdjson`.
- **Orchestration:** multi-agent workflows (operator opt-in 2026-08-23), sized large; every implementation wave is followed by the 7-reviewer quality gate run as an adversarial workflow at **high** effort, and the whole-branch gate runs once more at the end.

---

## 0. Prep (one workflow, ~6 agents, parallel)

| Step | What | Why |
|---|---|---|
| 0.1 | Re-index GitNexus **for this worktree** (`node .gitnexus/run.cjs analyze` here; the registry currently points `omnipus` at `wt-library-improvements`) | every spec's impact analysis was done by grep; the HIGH-impact changes in 067 (factory collapse, helper deletions at 13+ sites) need the real blast radius before editing |
| 0.2 | `/taskify` each spec in parallel (3 agents) → three task lists with dependencies, each task naming its files, its FRs/scenarios/tests, and its Fly gate | turns 49 + 40 + 45 requirements into ordered, checkable units |
| 0.3 | Baseline: trigger `pr.yml` on the branch head and run the Fly `all` gate once | establishes the pre-change green/red state so nothing "pre-existing" is discovered mid-build (Constraint #7: we fix everything anyway, but we want to know what we inherit) |
| 0.4 | Scaffold the assembly repository `elicify-ai/omnipus-provider-catalog` (empty, with the schema 2.0.0 doc and CI skeleton) | 067 Wave B depends on a first real release |

Exit: task lists committed under `docs/internal/specs/tasks/`; baseline report committed; index fresh.

## 1. Wave A — the contract commit (serial, one agent, then gate)

ADR-067 owns **one** commit editing every shared schema: `Provider`, `ProviderUpdateRequest`, `Agent` (`degraded_reason`, `needs_model`, window-source fields via `ContextWindowSource.yaml`), `ProbeProviderRequest` (`auth`, optional `api_key`, `model`, `api_base`, `protocol`), `DefaultModel` / `DefaultModelUpdateRequest`, `EntitlementResponse`, `ContextSettings`, the providers-catalog `GET`, `DELETE /providers/{id}`, `PUT /providers/default-model`, `POST …/entitlement`, `GET/PUT /settings/context`, and the **four** `LLMError` copies (`needs_provider`, `model_unassigned`, `turn_canceled`, `turn_timed_out`, `context_unrecoverable`, `context_window_unknown`, attribution `user` added) plus the `inboundschemas/` twins — then `make gen-contracts`, commit spec + generated artefacts atomically.

Gate: Fly `contracts` + `quick`. Nothing else starts until this is green — every later wave compiles against these types.

## 2. Wave B — parallel build (one workflow per stream, worktree-isolated, serial integrator)

Each stream is a `pipeline()` over its task list: **implement → unit-test → Fly `quick`/`lint` → self-review → hand to integrator**. Agents work in their own git worktree off the branch head (`isolation: 'worktree'`) so parallel edits never collide; a single **integrator** agent rebases and fast-forwards each finished task onto the branch in dependency order, running the Fly gate after every integration. Streams and their internal order:

| Stream | Scope (spec) | Internal order | Depends on |
|---|---|---|---|
| **B1 · Catalog core** | ADR-067: single `pkg/providers/catalog` (fold `capabilities` in), feed loader (checksum-only, 16 MB cap, `v`-date version, URL validation, `locality`, `company`, `custom`, `cli_kind`), committed snapshot, 24 h + startup pull, exact `(provider, model)` resolve (delete `resolveStrippedPrefix`, `ExtractProtocol`), **atomic**: protocol-dispatch factory + deletion of `GetDefaultAPIBase`/`IsKnownProtocol`/`knownProtocols` + all vendor cases + aliases, `custom` case, `GET /providers` configured-only, entitlement per protocol, probe-model rule, per-provider/agent degrade on unknown id | catalog → loader → resolver → factory collapse (one task, one commit) → REST | Wave A |
| **B2 · Assembly repo** | the daily job: pull models.dev + LiteLLM, merge to 2.0.0, overrides/ + resize table, 5 %/4,096 tolerance + last-known-good, checksum release; **first release published** and its snapshot committed into Omnipus | separate repository; lands in parallel with B1 | 0.4 |
| **B3 · Providers backend** | ADR-068: greenfield deletions with the grep gate (`antigravity`, `claude-cli`, OpenAI device-code, store-OAuth ladders, template rows, `model-capabilities`, `refresh-models`, `providerCatalog.ts`), `DELETE` with the five idempotent steps + audit + startup sweep + undeletable-default 409, default model as a pair (`model_name` deleted), `github-copilot` subprocess provider, `openai-chatgpt`, xAI gated | deletions first (they shrink the surface everything else touches) → DELETE/default → providers | Wave A; factory collapse from B1 for the provider constructors |
| **B4 · Context backend** | ADR-066: ladder + floor + `ResolveWindow` + clamps, the **single tool-result choke point** with caps (12 producers), user-message bound in `processMessage`, tool-argument refusal, D5 empty-in-place on the in-memory slice + meta emptied-set + restore point, **D5.4 recall injection at the tool-result site**, **D5.5 hydration fill-only + standalone tool_call reconstruction + `SetHistory` refusal**, D6 mid-turn check on `windowTrim`'s one budget, D7 typed exits, D10 ingest bounds (search providers, MCP transport), `settings/context` | choke point + caps → D5/D5.4/D5.5 → D6 → D7/D10 → settings | Wave A; catalog rung from B1 (compiles after B1's resolver) |
| **B5 · SPA** | ADR-068 §4–5 + ADR-066 D9: shared Popular-first picker (cmdk, virtualised, unsupported-disabled, Custom last), onboarding step 3 with sign-in/key per provider and no pre-selected model, Settings → Providers (default-model card, Check with my account, Remove provider dialog, signed-in/expired states, draft-key preservation), Settings → Models (window + source + overrides + caps), chat: `content_state` rendering under Verbose only | picker → onboarding → providers section → settings → chat | Wave A types; B1's catalog `GET` for real data (mock until then) |

Concurrency: B1 ∥ B2 ∥ B5 start immediately after Wave A; B3 and B4 start on Wave A too but their factory-dependent tasks queue behind B1's collapse commit. The integrator enforces the 067 → 068 → 066 order at the *commit* level even while agents build in parallel.

Per-task definition of done: FRs listed in the task all have their tests written **first** (test-driven-development skill), the Fly `quick` + `lint` gates pass in the agent's worktree, the agent's self-review finds nothing, the integrator's gate passes after rebase. Commit per task.

## 3. Wave C — review gate after each stream (adversarial workflow, effort **high**)

For each stream as it completes (and again for the whole branch in Wave E), the 7-reviewer gate runs as one workflow:

1. **Find** — seven reviewers in parallel, one per lens: `pr-review-toolkit:code-reviewer` (conventions/CLAUDE.md), `silent-failure-hunter`, `pr-test-analyzer`, `type-design-analyzer`, `comment-analyzer`, `code-simplifier`, and `security-review` (D10 ingest bounds, D13 credentials, URL validation, DELETE secret handling). Plus `test-integrity-auditor` over every new/changed test (the project's false-green history makes this non-optional).
2. **Verify** — every finding is handed to **three independent skeptics** with distinct lenses (correctness / does-it-reproduce / spec-compliance) told to *refute*; a finding survives only on majority. This is what keeps the gate from drowning the build in plausible-but-wrong notes.
3. **Fix** — surviving findings go back to the stream's agent in its worktree; re-gate.
4. **Loop until dry** — re-run Find on the fixed code until two consecutive rounds surface nothing new.
5. `/grill-code` against the spec: FR and scenario compliance, task completeness.

Effort: reviewers and skeptics at `high`; finder fan-out sized by stream (B1 and B4 are the largest).

## 4. Wave D — exit proofs and the greenfield gates (one workflow, parallel)

Run each spec's exit-proof list as real tests, each one an agent on the Fly runner or CI:

- **ADR-066 §17** (1–9): 2 MB result → no error; 50-call long turn under a small window; window agreement; thrash guard only by injected fault; silent-exit records; greenfield grep; **recall second-request test; attach-twice byte-identical; standalone tool_call reconstruction**.
- **ADR-067 §9**: exact `(provider, model)` resolve; checksum/size/URL/version-order rejection paths; selector offline from the catalog; protocol dispatch; provider-id greenfield grep; `antigravity` absent.
- **ADR-068 §9**: no-removed-providers grep (allow-listed docs only); `DELETE` + 409 on the default; default-model switch takes effect without restart; onboarding stays three steps; picker shape; draft-key preservation; unsupported visible-disabled; 11 WCAG constraints via axe/Playwright.
- The four CLAUDE.md quality gates in full: `gofmt`, `golangci-lint`, Go tests with tags, `govulncheck`, `npm run typecheck`, `vitest`, `make verify-contracts` — via Fly `all` and `gh workflow run pr.yml --ref feat/context-budget-and-tool-result-routing`. Read exit codes, not tails (false-green-patterns doc).

## 5. Wave E — whole-branch gate and handover

1. Wave C's workflow once more over the **entire** branch diff (the mandatory second run of the 7-gate).
2. `detect_changes({scope: "compare", base_ref: "release/v0.1.1"})` — affected symbols and flows vs the release branch, read by a reviewer for anything outside the three specs' scope.
3. **Holdout scenarios** (7 per spec, excluded from traceability) executed by the operator against the running binary — not by agents.
4. Operator ratifies the three ADRs as **Accepted** with the commit that delivered them; branch handed over for the operator's own merge to `release/v0.1.1`.

## 6. Sizing and effort

| Wave | Workflows | Agents (approx.) | Effort |
|---|---|---|---|
| 0 Prep | 1 | 6 | medium |
| A Contract | 1 | 1 + gate | high |
| B Build | 5 streams | 20–35 (pipelines, worktree-isolated) + 1 integrator | high for B1/B4, medium elsewhere |
| C Review | 1 per stream + 1 | 7 finders + 3 skeptics per surviving finding, loop-until-dry | **high** |
| D Exit proofs | 1 | ~15 | medium |
| E Whole-branch | 1 | as C | **high** |

The default workflow-size guideline is "medium, under 15 agents"; this plan assumes the operator raises it to **high** in `/config` ("Dynamic workflow size") before Wave B.

## 7. Risks named up front

- **B1's atomic collapse** is the single riskiest commit (13+ call sites, two HIGH-impact deletions). It is one task, one agent, one commit, with the GitNexus impact report attached and the Fly `go-test` gate before integration — no partial state ever reaches the branch.
- **Three agents committing to one branch** collided once already (index lock). Worktree isolation plus a single integrator removes it.
- **The full Go suite must never run here** (OOM history); every gate is remote. Budget ~60–90 s per Fly `quick`, several minutes per `all`.
- **The assembly repo is a second codebase** with its own CI; B2 must publish a real release before B1's loader can be integration-tested against anything but a fixture.
- **Unverified items carried from the specs** (Copilot CLI flags and login surface; Ollama's exact context field) are confirmed by the implementing agent against the installed binaries *before* writing the fixture that pins them.
