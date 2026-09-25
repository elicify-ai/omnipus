---
name: spec-drift-verify
description: Drift-detection protocol for interview-me's --verify mode. Diffs a spec against the current codebase, reports divergence with file-level evidence, and offers a targeted reconciliation. This is NOT a skill — it is a resource file read by the interview-me skill when dispatch rule 0 fires.
---

# `--verify`: Spec Drift Detection Protocol

Claude reads this file when `/interview-me --verify <spec-path>` is invoked, and when Resume step 2 ("re-validate against current codebase") needs to check whether code changes invalidated previous answers.

**Mode identity.** This is an **audit**, not an interview. It is report-first and may legitimately ask zero questions. The interview rule "every question must use AskUserQuestion" still applies to the questions you *do* ask, but "one question at a time" does not force you to serialize the report — findings are presented as a batch, then resolved individually.

**Write safety — non-negotiable.** Steps 0 through 10 are **strictly read-only**. Do not call `Write` or `Edit` on any file until the user has explicitly chosen a follow-up in Step 11. The two exceptions, both of which happen only after the report is printed, are the claim-table backfill and the cache write (Step 12).

`--verify` never edits source code. The only files it may ever write are the spec, its state files, and the verify cache.

---

## Step 0 — Parse arguments and check preconditions

```
--verify <spec-path>          run the protocol, incremental if possible
--verify --full <spec-path>   run the protocol, forcing a full scan
```

1. Resolve `<spec-path>`. If the file does not exist, list `spec-*.md` files in the project root **and in `specs/`, `docs/internal/specs/` (this repo), or `doc/specs/` if any exist**, then ask which one via AskUserQuestion. **Do not auto-discover** and pick one silently.
2. If the target is not a spec (source code, config, README), warn and confirm intent before proceeding.
3. Record `isGitRepo` — `git rev-parse --is-inside-work-tree`. Everything about incremental scoping and rename detection degrades gracefully when this is false.

---

## Step 1 — Load the spec and its state

Read the spec in full. Then locate its state, **reading either filename**:

| File | Tracked | Holds |
|---|---|---|
| `.<spec-name>.interview.json` | yes | `claims`, `qaLog`, `coverageMap`, `driftLedger`, `capturedAtCommit`, `previewUrl`, `sourceIssue`, `redTeamStatus`, `redTeamFindings`, `feedbackRounds` |
| `.<spec-name>.verify-cache.json` | no | `inventory`, per-claim `evidencePaths`, `lastVerifiedCommit`, verdict cache |
| `.<spec-name>.interview-state.json` | no | **legacy** — the pre-split combined file |

### Legacy migration (non-destructive)

If only the legacy `.interview-state.json` exists:

1. Read it.
2. Write durable keys to a new `.interview.json`.
3. Write cache-shaped keys to a new `.verify-cache.json`.
4. **Leave the legacy file in place, untouched.** Never delete or rewrite it.
5. Tell the user the migration happened, in one line.

Legacy state files routinely lack keys that the skill documents — `previewUrl`, `sourceIssue`, `feedbackRounds`, `redTeamStatus`, `redTeamFindings` were all added after early state files were written. Treat every key as possibly absent and default it rather than failing.

**No state file at all** is a supported case, not an error: proceed with an empty state, derive claims from prose in Step 2, and note in the report header that this run has no baseline commit and is therefore a full scan.

---

## Step 2 — Build the claim table

The "specified view" of the diff is a **claim table**, never raw prose.

### If `state.baseline.claims` exists

Use it. Note `baseline.capturedAtCommit` for Step 3 and Step 6.

### Otherwise — derive from prose and backfill

This is the common path: every spec written before `--verify` existed, and every spec produced in Greenfield Mode (which has no codebase analysis by design), lands here.

Walk the spec top to bottom and extract every **verifiable** claim:

```jsonc
{
  "id": "C1",                                  // stable, never renumbered on insert
  "type": "component",                         // component | endpoint | data-model | decision | constraint
  "area": "Repo Structure",                    // joined from qaLog[].area
  "quote": "the skill lives at interview-me/SKILL.md",
  "expectedPaths": ["interview-me/SKILL.md"],  // may be empty
  "searchTerms": ["interview-me", "SKILL.md"]
}
```

Four rules govern extraction:

1. **`quote` is verbatim spec text.** Copy it; never paraphrase. A claim you cannot quote is a claim you invented, and requiring the quote is the only thing standing between this protocol and hallucinated requirements.
2. **`area` is tagged now, not inferred later.** Join against `qaLog[].area` and `coverageMap` keys by topic match. Where no area matches, use the spec's own section heading. This is what makes "which coverage areas drifted" a lookup in Step 11 instead of a guess.
3. **Extract claims, not aspirations.** "Users should have a good experience" is not a claim. "Rate-limit all public endpoints" is. When a sentence states intent with no checkable consequence, either skip it or type it `constraint` and expect it to classify as `unverifiable`.
4. **Decisions Log rows are claims too**, typed `decision`. They are the only source for detecting silently reversed decisions.

Write the derived claims back to `.interview.json` with `derivedFrom: "spec-prose"` — **in Step 12, after the report**, so the read-only guarantee holds. Run two is then deterministic and cheap.

---

## Step 3 — Determine scan scope

Default to **incremental**. Fall back to **full** automatically when any of these hold, and state which one in the report header:

- `--full` was passed
- not a git repository
- `baseline.capturedAtCommit` is absent
- `capturedAtCommit` is unreachable — `git cat-file -e <sha>^{commit}` fails (rebase, squash, shallow clone)
- the spec file changed since `lastVerifiedCommit` (compare `git log -1 --format=%H -- <spec-path>`, or mtime when untracked)
- `.verify-cache.json` is absent or malformed

### Incremental scoping

```
git diff --name-only <capturedAtCommit>..HEAD
git status --porcelain          # uncommitted work counts as changed
```

- **Fork A** re-verifies a claim when its cached verdict is anything other than `implemented`, **or** when any of its cached `evidencePaths` appears in the changed set. Claims that were `implemented` and whose evidence is untouched are skipped and reported as unchanged.
- **Fork B** inventories only the changed paths and merges the result into the cached inventory.

**The subtlety that keeps this honest:** Fork A cannot simply scan the changed files. A claim that was *never implemented* has no file to appear in any diff, so `not-found` and `partial` claims are **always** re-verified regardless of scope. Only confirmed-implemented claims are skippable.

---

## Step 4 — Fork A (primed): verify the claims

Launch a forked agent with the Task tool, `subagent_type: Explore`. It receives the claim table and nothing else about intent.

**Prompt contract — include all of this:**

> You are verifying whether specific claims are satisfied by the code in this repository. Here is the claim table: `<claims>`.
>
> For each claim, return exactly one verdict. **You may not add, merge, split, or reinterpret claims.** If a claim is unclear, return `unverifiable` with an explanation — do not substitute your own version of it.
>
> Verdicts:
> - `implemented` — satisfied. Requires `file:line` evidence.
> - `partial` — partially satisfied. Requires `file:line` plus what is missing.
> - `contradicted` — you found evidence the *opposite* decision was made. Requires `file:line`.
> - `not-found` — you could not find it. **Requires the literal list of greps and globs you ran that returned nothing.**
> - `unverifiable` — the claim has no checkable code artifact by its nature (process, prose, intent).
>
> Mark evidence that lives in tests, fixtures, mocks, examples, or generated output with `evidenceKind: "non-production"`. A fixture is not an implementation.
>
> Never report `not-found` without the searches. Absence of evidence is not evidence of absence, and the report says so in those words.

**Return schema:**

```jsonc
{
  "verdicts": [
    {
      "claimId": "C1",
      "verdict": "not-found",
      "evidence": [],                       // ["path/file.ts:42", ...]
      "evidenceKind": "production",         // production | non-production
      "searchesTried": [                    // REQUIRED when verdict = not-found
        "grep -r 'interview-me/SKILL.md' .",
        "glob interview-me/**"
      ],
      "notes": "string"
    }
  ]
}
```

---

## Step 5 — Fork B (blind): inventory what exists

Launch a **second, separate** forked agent. **Do not show it the spec, the claim table, Fork A's output, or any of the spec's vocabulary.**

This separation is structural, not stylistic. An agent primed on the spec anchors on the spec's own words and systematically under-reports what the spec *omits* — and "code that grew beyond the spec" is half of what drift detection is for. One agent doing both jobs cannot do the second one well.

> Inventory what this repository actually contains. Do not consult any specification or design document. Report: top-level structure and purpose of each area, modules and their responsibilities, public entry points and endpoints, data models and schemas, configuration and build files, and external dependencies. For each item give a path and one line on what it is.

**Return schema:**

```jsonc
{
  "inventory": [
    { "path": "skills/interview-me/SKILL.md",
      "kind": "component",                  // component | endpoint | data-model | config | dependency
      "summary": "string" }
  ]
}
```

Both forks can run concurrently — issue them in a single message.

---

## Step 6 — Reconcile renames with git

**Run this before classifying anything.**

Without it, one file move is reported twice: Fork A returns `not-found` for the old path while Fork B reports the new path as an unmatched artifact. Relocations are the single largest false-positive class in spec drift, and a report that is mostly noise gets used exactly once.

```
git diff --name-status -M --find-renames=40% <capturedAtCommit>..HEAD
```

```
R092  interview-me/SKILL.md  skills/interview-me/SKILL.md
```

For every claim whose verdict is **`not-found` or `partial`**, whose `expectedPaths` match a rename **source**, and whose rename **target** appears in Fork B's unmatched inventory:

- collapse both into **one** finding, `kind: "relocated"`, carrying git's similarity score as evidence
- remove the target from the unmatched-inventory set so it does not also surface as `exceeded`

**`partial` belongs in this rule, not just `not-found`.** A relocation is the single most natural cause of a `partial` verdict: the artifact satisfies the substance of the claim and fails only on path, so a correctly-reasoning Fork A reports `partial` rather than `not-found`. Gating rename reconciliation on `not-found` alone makes it miss its own primary case.

Git's similarity scoring is real evidence rather than model inference, and it returns the identical answer on every run — which is also what keeps the Step 7 fingerprint stable.

### Set the threshold explicitly

`--find-renames=40%` is deliberate; do not fall back to git's implicit 50%. A spec's file gets moved *and* substantially rewritten in the same window, and similarity decays fast — a single 74-line addition to a 289-line file drove one real rename here from R062 to R046. At the default threshold git stops reporting it as a rename at all, the move degrades to an add plus a delete, and the report emits exactly the `missing` + `exceeded` pair this step exists to prevent.

### Fallback: correlation

When there is no git, no reachable `capturedAtCommit`, or a squashed history, correlate `not-found` claims against unmatched inventory by filename, symbol name, and content similarity. Collapse only confident matches.

**Label every correlated finding as a model judgment in the report.** Do not present it with the same confidence as a git-derived one.

---

## Step 7 — Classify and fingerprint

Two orthogonal axes.

| `kind` | Trigger | Default severity |
|---|---|---|
| `missing` | Claim `not-found`, and no rename or correlation explains it | major |
| `relocated` | `not-found` or `partial` matched to a git rename or a confident correlation | minor |
| `incomplete` | Claim `partial`, and no relocation explains the gap | major |
| `exceeded` | Inventory artifact with no claim behind it, after the exclusions below | minor |
| `contradicted` | Evidence that the opposite decision was made in code | major |
| `unverifiable` | Claim has no code artifact by its nature | minor |

### Exclude these from the unmatched-inventory set

Fork B is blind by design, so it faithfully reports things that are not candidates for `exceeded`. Subtract all of the following **before** classifying, and never report them:

- **The spec under verification.** Left in, a spec is reported as drift from itself. This is not hypothetical — it happens on the very first run against any spec that lives in its own repo.
- **Sibling specs and every state file** — `spec-*.md`, `.*.interview.json`, `.*.verify-cache.json`, `.*.interview-state.json`. These are this tool's own output. A spec does not claim the existence of the artifacts that describe it.
- **Local editor and agent config** — `.claude/`, `.vscode/`, `.idea/`, and equivalents. Environment, not product. Flag one of these only if it contains something genuinely alarming, in which case it is a Step 8 security finding rather than `exceeded`.
- **External tools already implied by the spec's own instructions.** If the spec tells the reader to run `git` or `gh`, Fork B listing them as dependencies is not drift. Fork B uses a broader definition of "dependency" than a spec's no-dependencies constraint usually means — reconcile toward the claim's evident intent, and say so in the report rather than raising a finding.

The general rule: `exceeded` means **product** that outgrew the spec. Scaffolding, tool output, and environment are not product. Without these exclusions, `exceeded` fills with the tool's own exhaust and the signal is gone.

### Every verdict must map to a kind

Fork A can return five verdicts. Each one has exactly one destination, and **there is no fallthrough**:

| Fork A verdict | Becomes |
|---|---|
| `implemented` | no finding |
| `not-found` | `relocated` if Step 6 explains it, else `missing` |
| `partial` | `relocated` if Step 6 explains it, else `incomplete` |
| `contradicted` | `contradicted` |
| `unverifiable` | `unverifiable` |

A verdict with no matching row is a **bug in this protocol, not a claim to discard**. An audit that silently drops a verdict prints "0 findings" while real drift sits in the verdict set — the worst failure mode available to it. If you encounter a verdict this table does not cover, report it as `incomplete` with `confidence: "needs-human"` and say in the report that the protocol did not anticipate it.

Severity uses **`critical | major | minor`** — the same vocabulary as the Phase 3 red-team finding schema. Do not introduce a second severity system.

Adjust severity up when a `missing` or `contradicted` claim covers auth, data handling, or a stated constraint; down when a `missing` claim is a helper or convenience. Any finding in a security hard-block category is forced to `critical` in Step 8.

```jsonc
{
  "id": "D3",
  "fp": "C7:contradicted:.claude-plugin/",
  "kind": "contradicted",
  "severity": "major",
  "claimId": "C7",
  "area": "Repo Structure",
  "quote": "Minimal — match cache-audit",
  "evidence": ".claude-plugin/plugin.json:1, STYLE_PRESETS.md:1",
  "evidenceKind": "production",
  "confidence": "needs-human",              // confident | needs-human
  "searchesTried": [],                      // required when kind = missing
  "securityCategory": null                  // set in Step 8
}
```

### Fingerprints

`fp` = `claimId + ":" + kind + ":" + normalizedEvidencePath`

Normalize the path: repo-relative, forward slashes, no line numbers. For `exceeded` findings there is no claim, so use `"-"` as the claim segment.

Fingerprints — not sequence numbers — are what make acceptance stick. `D3` shifts the moment a finding is inserted above it; `fp` does not.

### `contradicted` is best-effort by construction

No grep proves that intent was reversed. Deciding that *"Minimal — match cache-audit"* was contradicted the day a plugin directory appeared requires reasoning over a one-line rationale. Every `contradicted` finding therefore:

- carries `confidence: "needs-human"`
- is **never** auto-recorded as accepted
- is **always** adjudicated by an AskUserQuestion in Step 11

### The evidence rule is absolute

Every finding carries `file:line` evidence — or, for `missing`, the literal `searchesTried` that returned nothing — **or it is dropped before the report is assembled.** No exceptions. This is the only defense against plausible-but-wrong findings, and it is the same discipline the red-team finding schema already enforces.

---

## Step 8 — Security escalation (hard block)

Check every finding against the six hard-block categories from SKILL.md:

1. PII/sensitive data handling without encryption or access controls
2. Authentication/authorization bypass risks
3. Injection vulnerabilities (SQL, XSS, command injection)
4. Secrets/credentials in plaintext
5. Missing rate limiting on public endpoints
6. Data retention without a deletion strategy

A verify-time hard block is **strictly stricter** than an interview-time one. The interview blocks a hypothesis; `--verify` has confirmed a live gap in shipped code.

On a match, set `securityCategory` and apply all four:

1. **Severity is forced to `critical`**, regardless of kind.
2. **Batch-dismiss is disabled** for that finding.
3. **All Step 11 follow-up writes are blocked** until every such finding is addressed or explicitly risk-accepted.
4. **Accepting requires a written rationale** from the user, recorded in both the drift ledger and the Decisions Log. A single keystroke must not be able to silence it.

**The report always prints in full.** Reading is always safe, and halting the scan on the first security hit would let one false positive brick the whole feature.

---

## Step 9 — Suppress previously accepted drift

For each finding, look up `fp` in `state.driftLedger`:

- **fingerprint present** → suppress silently. Count it in the header line, do not list it.
- **fingerprint absent** → report it.

A fingerprint that *changed* means the drift genuinely moved, so the finding is correctly re-raised as new rather than staying suppressed. This is the intended behavior, not a bug.

Ledger entries with `status: "acknowledged"` (skipped or batch-dismissed, not accepted) are suppressed the same way but surfaced in the header as outstanding, with severity preserved.

---

## Step 10 — Report

Terminal first. Sort by severity, then by kind.

```
DRIFT REPORT · spec-open-source-publish.md
baseline 1feea3b → HEAD  ·  11 claims  ·  3 findings  ·  incremental

⚠ major   contradicted  C7   "Minimal — match cache-audit"
                             .claude-plugin/, STYLE_PRESETS.md   [needs-human]
○ minor   relocated     C1   interview-me/ → skills/interview-me/   (R092)
○ minor   exceeded      —    .claude-plugin/marketplace.json

8 claims unchanged since last verify (skipped)
2 findings suppressed by the drift ledger
```

The header **always** states: spec name, baseline commit → HEAD, claim count, finding count, and `incremental` or `full`. When the scope fell back to full, say which trigger caused it. When rename detection fell back to correlation, say so.

### Early exit: the spec was never implemented

Before printing findings, check coverage: if **fewer than 20%** of claims returned any evidence at all, stop and print this instead:

```
spec-x.md appears unimplemented — 1 of 14 claims found in code.

Reporting 13 violations would be noise, not signal. If implementation
has genuinely started elsewhere, re-run with --full, or point --verify
at the right spec.
```

Then stop. Do not offer follow-ups. Enumerating a wall of violations for a spec nobody has started is worse than useless.

### Zero findings

Report it cleanly: claims verified, scope used, and which claims were skipped as unchanged. The spec and the code agree.

---

## Step 11 — Follow-up actions

Only now may you write anything. **Blocked entirely** while any unresolved security finding remains from Step 8.

Present the resolution options for the finding set via AskUserQuestion. Resolve findings one at a time, in severity order, with the same active pushback the interview uses.

### Per-finding options

| Option | Effect |
|---|---|
| **Fix in code** | Nothing is written. The finding is left unresolved for a later run — say so explicitly |
| **Accept the drift** | Reality wins. Ledger entry + `Drift Accepted` row in the Decisions Log |
| **Re-interview this area** | The spec was right and the code diverged in a way worth re-deciding. Queues the area |
| **Acknowledge only** | Logged as `acknowledged` with severity preserved; suppressed next run but surfaced in the header |

Security-category findings drop "Acknowledge only" and require a typed rationale for "Accept the risk".

After all `critical` and `major` findings are resolved, offer to batch-dismiss remaining `minor` ones as `acknowledged`. Security findings are never in that batch.

### Accepting drift writes in two places

```jsonc
// machine filter — .interview.json
"driftLedger": [
  { "fp": "C1:relocated:skills/interview-me/SKILL.md",
    "status": "accepted",              // accepted | risk-accepted | acknowledged
    "acceptedAt": "2026-07-25",
    "rationale": null,                 // REQUIRED when securityCategory is set
    "decisionId": "D11" }
]
```

```markdown
| D11 | Repo layout | skills/ subdirectory | plugin convention | Drift Accepted | 2026-07-25 |
```

The ledger is the machine filter that keeps the next run quiet. The log row is what a human reads six months later. Both, always — a ledger without a log row makes accepted drift invisible to anyone reading the spec, and a log row without a ledger entry leaks past suppression.

### The targeted re-interview

Because every claim carries its `area`:

1. Reset exactly the drifted areas in `coverageMap` to `pending`. Leave every other area alone.
2. Re-ask **only** those areas, following the Phase 2 interview rules.
3. **Rewrite the spec sections that own the drifted claims, in place.** This is the point of the entire feature — appending a decision row does not fix a wrong sentence.
4. Append the corresponding Decisions Log rows.
5. Cycle those areas back to `done`.

```diff
  ## Repository Structure          <- rewritten in place
- interview-me/SKILL.md
+ skills/interview-me/SKILL.md
```

**Show the rewrite as a diff and get confirmation before saving.** You are mutating prose a human wrote. Never touch sections that did not drift — that is what keeps the diff reviewable and preserves hand-edits, including trailing content the spec generator did not produce.

Parse defensively: **do not assume the Decisions Log is the last section in the file.** Specs accumulate hand-pasted content after their final generated section, and appending blindly will corrupt it.

### Optional publication

After the writes, two opt-in prompts. Neither fires without asking:

1. **Shareable Artifact drift report** — build it from `STYLE_PRESETS.md` using the Phase 4 machinery, then save the URL to `previewUrl`. Redeploy to an existing `previewUrl` by passing it as the Artifact tool's `url` parameter.
2. **Post the summary to the source issue** — only when `sourceIssue` is set.

---

## Step 12 — Persist state

The only writes outside Step 11:

**`.<spec-name>.interview.json`** — backfilled `baseline.claims` (with `derivedFrom` and `capturedAtCommit`), plus any `driftLedger` entries and `coverageMap` changes from Step 11.

**`.<spec-name>.verify-cache.json`** — Fork B's merged inventory, per-claim `evidencePaths` and verdicts, and `lastVerifiedCommit` set to current `HEAD`.

Set `capturedAtCommit` to `HEAD` **only** when the spec was reconciled in Step 11 — the baseline should advance when the spec catches up to the code, not merely because the code was looked at. A pure read-only run leaves it where it was.

Skip the cache write entirely when not in a git repository; there is no stable key to invalidate against.

### The baseline SHA cannot be written in the same commit it names

`capturedAtCommit` must point at the commit that **carries the reconciled spec**. That commit does not exist yet while Step 12 is running, so there is no value to write. Resolve it in this order:

1. Commit the reconciled spec.
2. Read the resulting SHA — `git rev-parse --short HEAD`.
3. Write it into `capturedAtCommit` and `baseline.capturedAtCommit`, and commit that as a **separate** follow-up.

**Never `git commit --amend` after step 3.** Amending replaces the SHA, and the value just written now names a commit that no longer exists — which Step 3 reads as "`capturedAtCommit` unreachable" and silently downgrades every future run to a full scan. The same trap applies to rebasing or squashing the reconciliation commit afterward.

A two-commit baseline update is correct and intended: the state file lands one commit *after* the spec it describes. If `--verify` is not committing on the user's behalf, write nothing here — leave `capturedAtCommit` at its old value and tell the user which SHA to set it to once they commit. A stale-but-reachable SHA costs one full scan; an unreachable one costs a full scan on every run forever.

---

## Edge cases

| Case | Behavior |
|---|---|
| No state file at all | Derive claims from prose, backfill, full scan, say so in the header |
| Greenfield spec (no codebase analysis by design) | Same as above. Greenfield specs are the *most* likely to drift, so support them rather than rejecting them |
| Spec never implemented | Under 20% claim evidence → early exit, no follow-ups |
| `capturedAtCommit` unreachable | Full scan + correlation renames, both stated in the header |
| Not a git repository | Full scan every run, correlation-only renames, no cache write |
| Evidence in tests, fixtures, mocks, examples | Report the path with `evidenceKind: "non-production"`; never let a fixture pass as an implementation |
| Spec edited since last verify | Invalidate the cache, re-extract claims, force a full scan |
| Finding returns after acceptance | Fingerprint changed → the drift moved → re-raise as new. Correct, not a bug |
| Multiple specs in the repo | One invocation targets exactly one spec. Never auto-discover and pick |
| Monorepo or vendored dependencies | Scope Fork B to the spec's own area; exclude `node_modules`, `vendor`, `dist`, `build` |
| Unresolved security finding | Report prints in full; all Step 11 writes blocked |

---

## Invariants

1. Steps 0–10 write nothing.
2. No finding survives without `file:line` evidence, or `searchesTried` for `missing`.
3. Fork B never sees the spec.
4. Renames are reconciled before classification, never after, and consider `partial` as well as `not-found`.
5. Every Fork A verdict maps to exactly one kind. A dropped verdict is a bug.
6. Fingerprints, never sequence numbers, key the ledger.
7. Security findings cannot be batch-dismissed, and accepting one requires a written rationale.
8. Claims are quoted verbatim from the spec, never paraphrased.
9. `--verify` never edits source code.
