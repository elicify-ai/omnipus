# Omnipus Design System — Claude Code Handover

**Date:** 2026-09-19
**From:** Codex orchestration agent (multi-agent lead)
**To:** Claude Code
**Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session`
**Starting revision:** `92aeb4d5dcb0d545e2370ddd1c57c099a03392ba` (baseline); all work is uncommitted on this branch.

## 1. Goal

Execute the Omnipus design-system migration plan end to end. The plan defines eight stages: Encode → Locks → six repair batches → founder acceptance. The previous Codex session completed most of Encode (A) and Locks (B) but did not formally accept either stage. Stage C (repair) has not started.

The founder's instruction: **pause after Stage B and realign before starting C1.** No C1 work may begin without explicit founder approval.

## 1a. Working trees

All design-system work (A and B) was done in a single shared worktree. Agents did not use separate working trees — they all operated in the same directory with exclusive file ownership to prevent conflicts.

**The only relevant worktree is:**

```
/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session
```

Other worktrees under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/` belong to the ADR-090 release effort and are unrelated to this design-system migration. Do not touch them.

To resume or continue work, Claude Code should `cd` into `wt-release-session` and deploy agents there. Workers should share the same directory but receive exclusive file-ownership assignments (see the execution contract for the ownership model). The CLI session IDs listed in §5 are `claudez` sessions that were run from this same worktree.

## 1b. How to deploy agents

Use the `claudez` CLI binary for delegated implementation work:

```bash
# New session
/Users/danielpiatkowski/.local/bin/claudez \
  -p "<detailed prompt with file ownership, constraints, and verification requirements>" \
  --output-format json \
  --max-turns 80 \
  --permission-mode bypassPermissions

# Resume an existing session (session IDs in checkpoint)
/Users/danielpiatkowski/.local/bin/claudez \
  --resume <session-id> \
  -p "<follow-up prompt>" \
  --output-format json \
  --max-turns 80 \
  --permission-mode bypassPermissions
```

Every worker prompt must include:

1. Exact worktree path (absolute, no `~`).
2. Exclusive file-ownership list (which files this worker may edit).
3. "You are not alone; preserve others' edits."
4. Requirement to run GitNexus impact before every existing-symbol edit.
5. No `/tmp` artifacts; all evidence under `dist/design-system-baseline/cli-lanes/`.
6. No commits, no baselines, no C1, no application edits unless explicitly assigned.
7. Red→green→mutation-proof testing cycle for every repair.
8. Instruction to freeze on completion with a summary and hashes.

Claude Code should run workers as background processes, capture their JSON output, and review results when they finish. The lead role is: coordinate, verify, accept. No self-implementation.

## 2. Authoritative references

Read these files first, in this order:

| File | What it is |
|---|---|
| `docs/internal/design/design-system-migration-plan.md` | Master plan: stages, gates, approved normalizations, C batch assignments |
| `docs/internal/design/design-system-definition.md` | Target-state constitution: D1–D14 rules, token schema, component contracts |
| `docs/internal/design/design-system-execution-contract.md` | Encoding contract: file ownership, scanner API, fingerprint rules, fail-closed policy |
| `docs/internal/design/design-system-foundation-policy.md` | Foundation adoption policy |
| `docs/internal/design/design-system-surface-inventory.md` | Every application surface mapped to exactly one repair lane |
| `design-system/enforcement/contract.json` | Machine-readable enforcement contract: rules, schemas, boundary and runtime policies |
| `design-system/catalog.json` | Full component catalog with classifications |
| `design-system/surfaces.json` | Lane assignment for every source path |
| `docs/internal/design/evidence/design-system-review-repair-checkpoint.json` | Live checkpoint: everything completed, everything remaining, evidence paths |

## 3. Current state summary

### Stage A — Encode: substantially complete, not formally accepted

**Done:**
- 297 tokens defined, generated, and validated (CSS + TypeScript).
- 35 components implemented with stories, tests, and manifests.
- Storybook builds and runs on Chromium, Firefox, WebKit + coarse pointer.
- Component suite: 456 tests across 42 files, all passing.
- Story suite: 159 tests × 3 engines, all passing.
- Browser suite: 1172 checks across 4 projects, all passing (0 skipped, 0 flaky; stage-a-gate-record.md item 7). The 437 below is a different number: manifest-declared checks with executed evidence.
- Typecheck: passing.
- Production bundle budget: passing (+16,835 B gzip initial, +32,929 B raw total — both within limits).
- Package isolation: 8/8 checks passing (library builds without app code).
- Coverage command: `npm run verify:design-system` passes with 35 manifests / 437 checks / 2105 evidence results.
- Individual component repairs (Badge contrast, Button aux/form/ARIA, Field validation/optional, ConfirmDialog escape, CollectionState/JobStatus, Progress ARIA, DateTime locale) completed with red→green→mutation-proof cycles.

**Not done:**
- Formal A gate record not yet consolidated (evidence is scattered across multiple artifact files).
- Human screen-reader and founder visual acceptance not obtained.

### Stage B — Locks: substantially complete, not formally accepted

**Done:**
- Six E1 scanners implemented: CSS colours, TS/JSX colours, typography, spacing, controls (raw buttons/dialogs/switches), status palette.
- 1044 lock fixture tests across 70 suites, all passing.
- Audit orchestrator with fingerprint-based debt tracking, exact-file boundaries, owned ledger entries, expiry checkpoints.
- 682 reviewed boundary paths installed (test/story/generated-token files exempted from scanner scan; independently verified).
- `design-system/enforcement/baseline.json` and `ledger.json` composed from approved fragments only (4 calendar token entries + 682 boundaries, zero exemptions).
- CI job `design-system` wired into `.github/workflows/pr.yml` with required-gate propagation verified locally (4/4 failure-propagation tests passed).
- `npm run audit:design-system` command installed and executes the full six-scanner audit with coverage.
- Public coverage lock (`export → manifest → story → executed check`) implemented and blocking.
- Typography template-traversal repair independently reviewed: no blocking findings.

**Not done (all delegated to scanner owners):**
- **233 unsupported syntax findings** (116 ts-colors, 54 typography, 63 spacing) that block the audit. These require scanner capability improvements, not application changes.
- **3768 unapproved debt fingerprints** across application sources.
- Independent final review of colour collision repair (partially delivered; four original collision cases fixed; nested-helper case still unsupported but fail-closed).
- Independent CI review (interrupted by quota; local gate tests passed).
- Ledger ownership proposal needs correction (claim that boundaries suppress 9 unsupported was false; all 9 remain blocking).
- Helper-array occurrence undercount in spacing scanner (a pure function returning an array forces one location onto elements, causing identical strings to dedupe to one). Must be fixed before B acceptance.

### Stage C — Repair: NOT STARTED

Six batches (C1–C6) defined in the migration plan. **Do not start C1 without founder realignment.**

## 4. Key constraints

1. **No C1 without founder realignment.** After B acceptance, pause and confirm with the founder.
2. **No application conversion before B acceptance.** Stage B means all locks detect new violations; existing debt stays in audit mode.
3. **Unsupported syntax is never baselined.** Parser failures and unproven governed syntax block. They require scanner capability improvement or exact-source repair, not ledger exceptions.
4. **12px floor, status colours, 4px/8px spacing snap** are the only approved visual normalizations. Everything else is Redesign Risk and needs founder approval.
5. **Visual continuity is founder-judged.** No screenshot suite. The founder looks at the running app.
6. **All implementation work should be delegated to sub-workers.** The lead (or Claude Code lead) coordinates, verifies, and accepts; no self-implementation.
7. **GitNexus impact analysis before every symbol edit.** CLI: `node .gitnexus/run.cjs impact --uid <exact-uid> --direction upstream --repo /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session --summary-only`. Unindexed targets are UNKNOWN, not LOW.
8. **No `/tmp` artifacts.** All evidence goes to `dist/design-system-baseline/cli-lanes/`.

## 5. Immediate next steps for Claude Code

1. Read the plan and checkpoint files listed in §2.
2. Resume the spacing scanner helper-array occurrence fix (session `8cf0e028-53da-4400-9354-88176ff86acc` via `claudez` CLI, or re-implement from scratch).
3. Resume the independent CI review (session `5d380512-4e48-4d30-86a7-f042a02dfd8b`).
4. Fix remaining colour-collision coverage (nested helper should report raw, not unsupported; session `fa04f4fb-21d0-4279-b5fe-bc4ca4cdf40e`).
5. Correct ledger ownership proposal (session `a4b20ede-7670-4b61-9205-7ddf44047dee`; apply lead decisions from checkpoint `glmDebtLedgerProposal.leadOwnershipDecisions`).
6. Close remaining 233 unsupported findings with scanner capability work.
7. Run full frozen verification: `npm run test:design-system:locks` then `npm run audit:design-system`.
8. When audit passes, consolidate Stage A+B gate evidence, pause, and realign with founder before C1.

## 6. Key commands

| Command | What it does |
|---|---|
| `npm run tokens:check` | Validate generated tokens |
| `npm run test:design-system:unit` | Design-system unit tests (script/tooling) |
| `npm run test:design-system:locks` | All six scanner fixture suites (1044 tests) |
| `npm run test:design-system:components` | Component tests (456 tests, 42 files) |
| `npm run build:storybook` | Build Storybook |
| `npm run test:storybook` | Storybook interaction tests (3 engines) |
| `npm run test:design-system:browser` | Playwright browser tests (4 projects) |
| `npm run verify:design-system` | Public coverage check (manifest → story → executed check) |
| `npm run audit:design-system` | Full six-scanner audit with coverage; intentionally RED until debt is resolved |
| `npm run test:design-system:package` | Library build + package isolation checks |
| `npm run build` | SPA production build |
| `npm run measure:design-system-bundle` | Production bundle measurement |
| `npm run audit:design-system-bundle` | Production bundle budget check |

## 7. Evidence directory

All evidence lives under `dist/design-system-baseline/cli-lanes/`. Key files:

| File | Evidence |
|---|---|
| `b-glm-quota-frozen-result.json` | Frozen verification: 1044 fixtures pass, audit RED on 233 unsupported + 3768 debt |
| `b-glm-quota-frozen-diagnostic.json` | Full audit report at frozen state |
| `b-default-storybook-result.json` | Default Storybook build + coverage: 437 checks / 2105 evidence, exit 0 |
| `glm-colours-independent-review.md` | Independent colour-collision review (V1–V7 findings) |
| `glm-spacing-followup-independent-review.md` | Independent spacing repair review (F1/F3/F4 verified) |
| `glm-typography-template-independent-review.md` | Independent typography template review (no blocking findings) |
| `glm-boundary-install-summary.md` | 682 boundary installation proof |
| `glm-b-command-integration-summary.json` | CI/command integration proof |
| `b-ci-required-lead-probes.json` | CI gate failure-propagation tests (4/4) |
| `glm-debt-ledger-proposal-summary.md` | Historical debt ledger proposal (3122 entries, not approved) |
| `colour-frame-mutation-verify/result.json` | Isolated mutation proof for colour frame fix |
| `a-integration-inputs.json` | Stage A input fingerprint |
| `a-2ec-current-production-checks.json` | Production bundle evidence |

## 8. Provider notes

- **claudez (GLM):** Available as of 2026-09-19. Previous 5-hour limit has reset.
- **claudeg (Grok):** Balance exhausted (HTTP 402). May require the founder to top up.
- If using `claudez`, resume from stored session IDs where listed in the checkpoint. Use `--resume <session-id>` with the `claudez` CLI binary at `/Users/danielpiatkowski/.local/bin/claudez`.

## 9. What success looks like

**Stage B exit criteria:**
- All 1044 lock fixtures pass.
- `npm run audit:design-system` exits 0 (no unsupported findings, no new unapproved debt, all boundaries valid, all coverage checks pass).
- Independent reviews of colour, spacing, typography, CI, and ledger ownership complete with no blocking findings.
- Founder realignment obtained.

**Full program completion (C1–C6):**
- Every scanner lock becomes repository-blocking when its C batch reaches zero debt.
- Exception ledger is empty at C6.
- Production budget within limits.
- Founder judges the running app still looks like Omnipus.
