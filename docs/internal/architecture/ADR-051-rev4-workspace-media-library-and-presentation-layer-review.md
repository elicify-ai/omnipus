# Grill Review — ADR-051 Rev 4 (Workspace Media Library + Capability-Aware Presentation Layer)

**Review target:** `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
**Reviewer mode:** adversarial (grill-spec, generic-markdown / ADR mode)
**Date:** 2026-07-22
**Read-only on ADR:** confirmed — the ADR was not modified.

---

## Executive Summary

Rev 4's two-layer split (store / present) is architecturally sound and the operator's
"any file, any model → useful turn" goal is the right north star. The competitor audit
and the 9-provider matrix are real evidence, correctly cited in broad strokes.

**However, the ADR as written would cause production incidents if implemented verbatim.**
Two of the seven presentation steps are under-specified to the point of infeasibility
(the capability-registry "live pull" names no data source that exists; the step-5 offload
claims file access the sandbox model does not grant), and one step (the "outcome-based"
strip-retry) would **regress behavior that is already correctly shipped** by replacing a
precise classifier gate with a blanket "any 4xx" trigger that masks non-media errors and
re-fires content-policy violations. On top of that, the load-bearing mitigation for the
release-scope tension ("the layers compose onto existing `MediaStore`") materially
overstates how close the existing code is to the target: `MediaStore` is a global in-memory
ref→path index with a persisted registry, uploads land in a session-scoped dir, and the
manifest fields the ADR promises (sha256, uploaded_at-on-disk) do not exist on `MediaMeta`.

Two evidence errors in the ADR (the Anthropic "≤1568px" claim, contradicted by the cited
matrix; and the silent treatment of the already-shipped `media_downgrade.go` RD2) make the
design look smaller than it is. None of this is unfixable, and the architecture survives
the fixes — but the ADR is not implementable as-is.

**Verdict: REVISE.** (Not BLOCK: the design is recoverable, the operator has explicitly
accepted the scope, and the failures are fixable inside the same shape. But C1, C2, M3 must
be resolved before this ADR can govern a v0.1.1 implementation.)

**Counts:** 2 CRITICAL · 6 MAJOR · 3 MINOR · 1 OBSERVATION.

---

## Findings Table

| ID | Severity | Lens | Section | One-line |
|---|---|---|---|---|
| C1 | CRITICAL | Infeasibility / Incorrectness | Step 4 (line 53) | "Any 4xx with media → strip+retry" masks non-media 400s (content-policy, bad-tool-args, schema) and regresses the already-shipped classifier-gated `TryMediaDowngrade`; exclusion list (401/403/413/ctx-overflow) is incomplete. |
| C2 | CRITICAL | Infeasibility | Capability source (lines 60–67) | "Live pull capability data" has no data source: no `input_modalities` field or /models modality client exists in `pkg/providers/` — only an ID-lister in `validate.go`. Direct providers don't expose modalities uniformly. This is v0.3 scope. |
| M1 | MAJOR | Inconsistency / Release-scope | "What's already true" (line 22) + Consequences §"Release-scope tension" (line 96) | "Compose onto existing `MediaStore`" overstates the baseline: store is global in-memory + persisted ref→path registry; uploads are session-scoped (`uploads/<sessionID>/`); `MediaMeta` lacks sha256/uploaded_at-on-disk. Promotion is material new code, not a composition. |
| M2 | MAJOR | Insecurity (DoS) | Layer 0 Lifecycle (line 42) + Operator decision 1 (line 130) | Disc-as-only-bound + workspace-scoped + multi-agent delegation = workspace-flood DoS; orphan GC only hits *unreferenced* files; containerized/small-volume deployments (Fly devpods) are exposed. |
| M3 | MAJOR | Infeasibility | Step 5 (line 54) + Operator decision 3 (line 132) | "Agent retains file access via the `media://workspace/<id>` ref" — media:// refs resolve *inside the trusted loop process* (`resolveMediaRefs`), not to Landlock-accessible paths for sandboxed agent tools. No mechanism described. |
| M4 | MAJOR | Incorrectness | Format coverage (line 71) | "≤1568px long edge per Anthropic standard tier" is wrong: 1568px is OpenAI's `detail:low` threshold; the cited matrix says Anthropic allows ≤8000×8000px / ≤10MB. A universal 1568 default needlessly destroys fidelity on Claude/Gemini. |
| M5 | MAJOR | Inconsistency / Incompleteness | Step 4 (line 53) + Consequences (line 93) | ADR silently ignores the already-implemented Rev 3 RD2 (`pkg/agent/media_downgrade.go::TryMediaDowngrade`, classifier-gated, per-class per-turn guards). "RD2 hardened" hides a real semantic change (classifier gate → outcome gate) with no transition plan for `CodeMediaUnsupported`/`classifyByProviderError`. |
| M6 | MAJOR | Inoperability | Consequences §Neutral (line 102) | Backward-compat for existing session-scoped `media://<uuid>` refs across the namespace split to `media://workspace/<id>` is hand-waved as "add a resolver shim." No migration plan; two naming schemes must coexist. |
| m1 | MINOR | Contract-first (CLAUDE.md #8) | Affected Components (line 118) | ADR commits to "generated, not hand-written" wire types but doesn't name the schema files, the openapi/asyncapi references, or invoke the 5-step process. Pre-name `MediaLibraryEntry.yaml` etc. |
| m2 | MINOR | Overcomplexity / Ambiguity | Layer 1 table (lines 48–56) | The 7-step chain has under-specified ordering: steps 5 (offload+guidance) and 6 (text-injection) can both apply to the same file (e.g. an SVG on a text-only model). Tie-break rule missing. |
| m3 | MINOR | Incompleteness / Insecurity | Format coverage (line 71) + Layer 0 (line 43) | No memory budget for the synchronous resize decode at turn time. A hostile 100MB upload (allowed by `maxUploadFileSize`) decodes to hundreds of MB. `svg_raster.go` has `maxImagePixels`/`maxSVGRasterDimension` guards; the new resize pipeline must too — ADR doesn't say. |
| O1 | OBSERVATION | Incompleteness | Affected Components table (lines 108–118) | Table omits `pkg/workspace/` (the workspace entity/dir model the library "extends") and `pkg/agent/media_downgrade.go` (existing RD2). Both must change. |

---

## Phase 1 — Structural / Narrative Assessment

The ADR follows the project's accepted ADR shape (Context → Decision → Options →
Consequences → Affected Components → Non-goals → Operator decisions). The
"what-changed-since-Rev-3" preamble is honest about replacing the reactive-only design and
is explicit that RD4–RD7 are retained unchanged — that boundary is correctly drawn (verified
against Rev 3 §RD4–RD7; Rev 4 does not touch error translation, the two choke points, or
the Verbose-Chat `detail` gating).

The structural weaknesses are:

1. **The "already true today" section (line 22) is imprecise about the baseline**, and that
   imprecision is load-bearing for the release-scope mitigation. It says uploads land in
   `MediaStore.Store` as `media://` refs and "uploads don't error today" — both true — but
   then characterizes `MediaStore` as "per-session/ephemeral" (line 40). It is neither: it
   is a single **global** in-memory index (`pkg/media/store.go:93`, "pure in-memory
   implementation"), keyed by arbitrary scope strings, with a **persisted** ref→path
   registry (`registry.json`, loaded at boot via `gateway.go:1895`). Files live in
   `$OMNIPUS_HOME/media/` (when `OMNIPUS_HOME` is set), not per-session. This makes the
   "promote to workspace-scoped + persistent" framing mis-describe the work: persistence
   already works; the real delta is a new workspace namespace + a richer manifest. (→ M1)

2. **The presentation layer (Layer 1) is specified as a table, not a contract.** Tables
   with "first viable option wins" look rigorous but hide ordering ambiguity when multiple
   rows match the same input (→ m2). A 7-step chain with two text-producing steps (5 and 6)
   needs an explicit composition rule.

3. **The ADR cites the matrix as evidence but contradicts it on Anthropic limits** (→ M4).
   Adversarial reviewers treat any evidence error as a signal to re-check every other
   evidence claim; here, the rest of the matrix citations (xAI jpg/png-only, Gemini HEIC,
   no-SVG-anywhere) check out, so the M4 error appears isolated — but it is in the one
   sentence that drives the resize budget, so it is not cosmetic.

4. **The Affected Components table is incomplete** in a way that understates the blast
   radius: it omits `pkg/workspace/` (which owns the workspace entity + dir model the
   library "extends") and `pkg/agent/media_downgrade.go` (the existing RD2 implementation
   that step 4 re-semantics). (→ O1, M5)

The Options Considered table is well-formed and Option E (defer library to v0.3) being
"rejected by operator" is correctly attributed rather than argued on technical merits —
that is the right way to record an operator override.

---

## Phase 2 — Eight Lenses

### 1. Ambiguity

**m2 — Step ordering for files that match multiple rows.** Layer 1 (lines 48–56) says
"first viable option wins" top-down, but rows 5 and 6 both produce text output and both can
apply to the same file. Concretely: an SVG on a text-only model. Step 1 (capability gate)
sends it to step 5 (offload + guidance). But step 6 (text-injection) is *also* the correct
path for SVG markup (the matrix at line 50 calls this out: "Option B fallback: SVG markup
injected as text block … works even on text-only models"). Does the offload guidance line
replace the text-injection, prepend it, or suppress it? The existing code (`loop_media.go`
lines 109–115) already does text-injection for SVG on rasterization failure; the ADR's
step-5-first rule would change that behavior without acknowledging it.

*Fix:* add an explicit composition rule — "step 6 (text-injection) runs *in addition to*
step 5 when the file is text-extractable; the guidance line prefixes the injected text."
Or re-order so text-injection (6) precedes offload (5) for text-like files. State which.

### 2. Incompleteness

**m3 — No decode memory budget for the synchronous resize.** The resize pipeline (line 71)
runs "at turn time" (Layer 1 runs at turn time by construction; `resolveMediaRefs` is called
from `loop.go:6264` inside the turn). A 100MB PNG (the upload cap is 100MB, `rest.go:8699`)
decodes to a bitmap on the order of hundreds of MB before the resize can shrink it. The
existing SVG path guards this (`svg_raster.go:26-34`, `maxSVGRasterDimension=4096`,
`maxImagePixels`); the existing raster path in `encodeImageToDataURL` does not appear to
pre-flight `image.DecodeConfig` for pixel budget. The ADR proposes a *new* synchronous
resize and says nothing about a decode guard — under Hard Constraint #3 (minimal footprint
<10MB RAM overhead) and the OOM history called out in `CLAUDE.md`, this is a real gap.

*Fix:* mandate `image.DecodeConfig` pre-flight with a pixel cap (e.g. the same
`maxImagePixels` guard), reject-to-step-7 (honest marker) on overflow, before any
`image.Decode`. Add it to Layer 0's normalization contract, not just as an implementation
note — operators need to be able to tune it.

### 3. Inconsistency

**M5 — Rev 3 RD2 is already implemented; the ADR pretends it isn't.** `pkg/agent/media_downgrade.go::TryMediaDowngrade`
is shipped, called from `loop.go:6915`, classifier-gated on `CodeMediaUnsupported`, with
per-class (`mediaRetryDone` / `imageRetryDone`) per-turn guards. Rev 4 step 4 (line 53)
proposes "outcome-based strip-retry (RD2, hardened)" with trigger "any 4xx (excl.
401/403/413/context-overflow) with media present." That is a **different trigger** — the
existing code fires on the classifier's `CodeMediaUnsupported` verdict, not on any-4xx. The
ADR does not say:

- whether `TryMediaDowngrade` is rewritten, wrapped, or deleted;
- what happens to `classifyByProviderError` / `CodeMediaUnsupported` (does the classifier
  get demoted, retired, kept-for-telemetry-only?);
- how the per-class per-turn guards (which currently prevent a PDF downgrade from consuming
  the image retry budget) survive the move to an outcome-based trigger.

The Consequences section (line 93) even says "the classifier (RD4–RD7) is demoted from
control-flow-critical to UX-copy + telemetry" — but RD2 is the control-flow consumer of the
classifier, and the ADR doesn't reconcile that with "outcome-based." This is the kind of
inconsistency that produces a fork in the implementation: one engineer extends the
classifier, another rips it out, and they meet at code review.

*Fix:* add a paragraph under Step 4 explicitly stating the fate of `TryMediaDowngrade`,
`classifyByProviderError`, and the per-class guards. If the answer is "the classifier is no
longer the trigger; it is retained only to *label* the retry outcome," say so. Then C1
becomes easier to fix too.

### 4. Infeasibility

**C2 — The capability registry's "live pull" has no implementable source.** Lines 60–67
specify "on provider configuration, Omnipus pulls live capability data for that provider's
models" and "every 7 days, refreshes all configured providers from live metadata." The
9-provider matrix (the cited evidence) was compiled by a human reading docs; it is not
machine-fetchable. What exists in the codebase today:

- `pkg/providers/validate.go:376-418` — a `/models` GET that lists model IDs only. The
  decoded struct is tagged `// not-wire-format` and captures no `input_modalities`.
- No `input_modalities` / `InputModalities` symbol anywhere in `pkg/providers/` (verified
  by grep).

OpenRouter exposes `input_modalities` on its `/api/v1/models`, but the **direct** providers
the ADR needs to cover do not expose it uniformly: Anthropic's `/v1/models` returns no
modality field; xAI's docs list formats in prose; Gemini exposes modality at the *model*
docs page, not the models endpoint; DeepSeek/Z.ai return nothing useful. So "pull live
capability data" requires, per provider: (a) a /models fetch, (b) a provider-specific
modality resolver (some heuristic, some hardcoded, some impossible), (c) a refresh
scheduler, (d) a cache + last-known-good fallback, (e) operator-overridable storage. That
is the RD3 capability registry from Rev 3 — which Rev 3 explicitly **deferred to v0.3** as
"optimise-only; adds nothing to reliability." Rev 4 quietly un-defers it into a
stabilization patch without engaging with Rev 3's reasoning.

*Fix:* either (a) **drop live-pull from v0.1.1** and ship the compiled seed only (Rev 4's
own step-4 outcome-based retry already self-corrects for seed errors, so the live pull is
not load-bearing for the "never dead-turn" guarantee — exactly Rev 3's argument), or
(b) write a per-provider modality-resolution sub-spec naming the endpoint and field for
each of the 9 providers, and accept that this sub-spec is most of v0.3's RD3.

**M3 — Step 5 offload claims file access the sandbox does not grant.** Line 54: "inject
`media://workspace/<id>` ref into content … agent retains file access via the ref." Operator
decision 3 (line 132) repeats: "the agent keeps file access." But `media://` refs are
resolved **inside the trusted loop process** by `resolveMediaRefs` (`loop_media.go:75`,
`store.ResolveWithMeta`) — they are *not* filesystem paths. The agent's own tools
(`read_file`, `bash`, etc.) run **under Landlock** (`pkg/sandbox/`) against workspace
`work/` paths; they have no mechanism to resolve a `media://workspace/<id>` ref to a path
they're allowed to open. If the resolver hands back the original `$OMNIPUS_HOME/media/…`
path, that is a **path outside the workspace work-dir** — either Landlock denies it (agent
can't read its own "retained" file) or the sandbox must be punched to allow it (a new
escape vector). The ADR names neither outcome.

This is the same shape as the existing `tool:inline:session:` media (browser screenshots
etc.) — and there, the agent does **not** retain tool access to the bytes; the loop resolves
them at send time only. Rev 4 is claiming a new capability ("agent keeps file access") that
the current architecture does not provide.

*Fix:* either (a) drop "agent retains file access" and ship offload as guidance-only (the
rejected opencode shape — but at least it's honest), or (b) specify the sandbox path: copy
the offloaded file into the workspace `work/` dir (Landlock-allowed) and inject that
*filesystem path* (not a `media://` ref) into the content, so the agent's existing file
tools work. Option (b) has quota implications (see M2) and must be explicit about who owns
that copy's lifecycle.

### 5. Insecurity (STRIDE)

| STRIDE class | Finding |
|---|---|
| **Spoofing** | `media://workspace/<id>` refs must validate the *caller's workspace membership* before resolving. Today the store is global and refs are unguessable UUIDs; making them workspace-scoped introduces a cross-workspace enumeration/read path (an agent in workspace A resolving `media://workspace/B/<id>`). The ADR does not specify an authorization check on the new ref shape. → new (fold into M2/M3 fix). |
| **Tampering** | The manifest includes `sha256` (line 40) — good — but the ADR does not say the hash is *verified on read*. A pure-Go resize/normalize pipeline that trusts unverified bytes is a decode-bomb vector (cf. m3). Make sha256 verification-on-read explicit. |
| **Repudiation** | No audit requirement on upload/delete. The project has an audit subsystem (`pkg/audit/`); media library mutations (especially delete) should be auditable. Not mentioned. |
| **Information disclosure** | Acknowledged: persistent plaintext media on disk (line 97). Acceptable for a sovereign local app. **Not acknowledged:** cross-workspace ref resolution (Spoofing row above) and the step-5 path-exposure risk (M3). |
| **Denial of service** | **M2** — disc-as-only-bound. The operator accepted this for single-user, but the workspace model is inherently **multi-agent** (delegation, core_team — `pkg/workspace/delegation.go`), so a delegated sub-agent or a compromised channel can fill a workspace's library with referenced files (orphan GC won't touch them). Containerized deployments (the devpod env brief itself runs on Fly with small root volumes; `df -h /` has been seen at ~96%) make this operationally dangerous, not just theoretical. |
| **Elevation of privilege** | M3 — if the step-5 resolver returns a path outside the workspace work-dir to satisfy "agent retains file access," that is a sandbox-confinement breach. |

**M2 fix (the load-bearing one):** a workspace-scoped **soft quota** (operator-tunable,
default on, disableable for the sovereign-single-user case) is the minimum. The
"sovereign local app" framing in operator decision 1 does not survive the
workspace = multi-agent delegation model the rest of the product is built on. If the
operator truly refuses any quota, the ADR must at least (i) cap per-upload bytes lower than
the 100MB gateway cap for workspace library writes, (ii) make the orphan GC also sweep
*old* referenced files with a much longer horizon (e.g. 365d) after warning, and
(iii) call out explicitly that a workspace flood is a denial vector the operator accepts.

### 6. Inoperability

**M6 — Backward compat across the namespace split.** Line 102: "`media://` refs become
workspace-scoped, not session-scoped — session replay/restore must resolve via the workspace
library (add a resolver shim)." This hand-waves a real migration:

- Every existing session transcript references `media://<uuid>` refs (session-scoped
  uploads, `tool:inline:session:<id>` screenshots, etc.).
- `registry.json` persists those (`pkg/media/registry.go`), and `LoadRegistry`
  (`gateway.go:1895`) restores them at boot.
- The new scheme is `media://workspace/<id>`. The ADR does not say whether the old
  `media://<uuid>` shape continues to resolve, whether there's a one-time migration that
  re-scopes old refs into a workspace, or what workspace a pre-Rev4 session-scoped ref
  belongs to (pre-Rev4 sessions are not workspace-pinned in the ref itself).
- Files registered with `CleanupPolicyDeleteOnCleanup` are already TTL-deleted today
  (`CleanExpired`, store.go:280) — so some old refs are already unresolvable. The
  `[attachment unavailable]` marker handles that gracefully (verified at
  `loop_media.go:85`), so this is status quo, *not* a new break — but the ADR should say so
  rather than imply a shim is the whole story.

*Fix:* add a "Backward compatibility" subsection: (i) old `media://<uuid>` refs continue to
resolve via the existing global registry for at least one minor release; (ii) no automatic
re-scoping — old refs stay session-scoped, new uploads are workspace-scoped; (iii) the
"resolver shim" is specifically a fallback in the new resolver that tries the workspace
library first, then the legacy global registry. Name the sunset release.

### 7. Incorrectness

**M4 — The Anthropic limit is wrong, and it is the resize budget.** Line 71: "scale to
provider budget (default ≤1568px long edge per Anthropic standard tier; ≤5 MB)." The cited
matrix (`provider-media-format-support.md:36`) says Anthropic is "≤10 MB/img
(5 MB Bedrock/Vertex); ≤8000×8000 px." 1568px is **OpenAI's** `detail: low` auto-resize
threshold (images ≥1568px long edge are auto-downscaled for low-detail processing). The ADR
has confused OpenAI's threshold with Anthropic's. A universal 1568px default would
**needlessly destroy image fidelity** on Claude (which accepts 8000×8000) and Gemini
(which accepts 20MB inline), directly degrading the "useful turn" guarantee for the
highest-quality providers — the opposite of the ADR's intent.

*Fix:* the resize budget must be **per-provider**, sourced from the same matrix that drives
the capability registry. Concretely: a default of ~7680px long edge / 10MB covers every
documented provider (Anthropic 8000/10MB, Gemini 20MB, xAI 20MiB, Mistral 10MB); tighter
per-provider overrides applied when the registry knows the bound. Drop the "1568 /
Anthropic" sentence.

### 8. Overcomplexity

The two-layer split itself is not overcomplex — it is the right factoring. The overcomplexity
is in **cramming all of it into v0.1.1**. Rev 4's own Consequences (line 96) names this
("⚠️ Release-scope tension … material scope for a patch") and offers the mitigation "the
layers compose onto existing MediaStore/resolveMediaRefs (no greenfield)." That mitigation
is M1 — it isn't true. The honest minimum-shippable subset, which the ADR itself names
("resize-to-fit, outcome-based RD2 are independently shippable"), is the right v0.1.1 and
the rest is v0.3. The operator rejected that split (Option E), but the ADR's job is to make
the cost of the rejection visible, not to paper over it with a "compose onto existing"
claim that doesn't survive a read of `pkg/media/store.go`.

*Fix:* either (a) re-open Option E with the evidence in M1 (the "compose" mitigation is
~3–4 packages of new code: `pkg/media/resize`, `pkg/providers/capabilities`, manifest
schema + migration, workspace namespace in the store), or (b) keep full scope but replace
the "compose onto existing" mitigation with an honest work-breakdown showing the new
packages, and move the **capability registry live-pull** (C2) and the **step-5 sandbox
path** (M3) to v0.1.2 / v0.3 where they belong.

---

## Phase 3 — Testability Assessment

| Claim in ADR | Testable as written? | Gap |
|---|---|---|
| "Every upload succeeds" (Layer 0 invariant) | ✅ Yes — extend `tests/e2e/media.spec.ts` with every format from the matrix. | None. |
| "Any file, any model → useful turn" (Layer 1 invariant) | ✅ Yes — the matrix §5 stress-test table is already a test plan. | None; reuse it. |
| Step-4 "outcome-based" retry fires exactly once | ⚠️ Partial — the existing `runturn_redo_test.go` covers the classifier-gated path. The *outcome-based* path needs new fixtures for the 4xx variants C1 calls out. | Must add: 400-content-policy, 400-bad-tool-args, 400-schema — assert they do **not** trigger strip-retry. |
| Capability registry "live pull" (C2) | ❌ No — no test plan, no VCR/provider-fixture strategy, no test for "live-pull failure is non-fatal." | Untestable until the data source is specified (C2). |
| "Disc as only bound" graceful degradation | ⚠️ The `[attachment unavailable]` path is tested. The **fill-disk** behavior (M2) has no test and no operator-visible signal. | Add: a quota/usage gauge in the workspace UI even if enforcement is soft; add a log warning at a high-water mark. |
| Optimistic unknown-model default ("cost: one retry, never a dead turn") | ⚠️ The "one retry" half is testable. The "never a dead turn" half depends on step 5 (M3) actually granting file access — which is currently infeasible. | Blocked on M3 resolution. |
| Resize-to-fit (line 71) | ⚠️ Behavior testable; **memory-budget guard** (m3) not specified enough to test. | Add a decode-bomb fixture (a crafted huge-pixel PNG) and assert the turn survives via step 7. |
| Backward compat (M6) | ❌ No test that legacy `media://<uuid>` refs still resolve after the namespace split. | Add a replay fixture with pre-Rev4 refs. |

**Net testability:** the Layer 0 / Layer 1 *behavioral* invariants are testable and the
matrix already gives fixtures. The **mechanism** claims (live pull, sandbox file access,
memory budget, backward compat) are not testable because they are not specified. Resolving
C2, M3, m3, M6 makes the ADR testable; until then an implementer will guess.

---

## Unasked Questions

1. **Who owns the workspace library's lifecycle when a workspace is deleted?** The ADR says
   workspace-scoped, but `pkg/workspace/` workspace deletion is not in the Affected
   Components table. Does deleting a workspace cascade-delete its media? If yes, say so
   (and audit it — Repudiation row). If no, the "orphan GC" has a new definition of orphan.
2. **What happens to `tool:inline:session:<id>` media (browser screenshots, charts)?**
   Today these are session-pinned and excluded from TTL expiry (`store.go:299-308`). The
   ADR's "workspace-scoped" framing doesn't address them — are they migrated, left
   session-scoped, or dual-homed? This is the single largest existing media population.
3. **Is the capability registry operator-overridable per-agent, per-workspace, or global?**
   Line 61 says "operator-overridable" but the override scope matters: a workspace running
   a fine-tuned model with non-standard modalities needs a per-agent override, not just
   global.
4. **The open question (line 137) — offload guidance naming the current model vs. generic —
   has a third option the ADR misses:** name the current model *and* link to the workspace
   team's actual vision-capable agent (which the resolver knows from
   `pkg/workspace/`). That is strictly more useful than either binary.
5. **Auditlogging:** `pkg/audit/` is a first-class subsystem. Is media library
   upload/delete an auditable event? The ADR is silent.

---

## Verdict

**REVISE.**

The architecture is recoverable and the operator's goal is right, but the ADR is not
implementable as-written. Three findings must be resolved before this ADR can govern a
v0.1.1 implementation:

- **C1** — narrow step 4's trigger back to classifier-gated (or specify the exact 4xx
  exclusion set to include content-policy, bad-tool-args, schema, and any non-media
  classifier code), and state the fate of the already-shipped `TryMediaDowngrade` (M5).
- **C2** — either drop the live-pull from v0.1.1 (ship seed-only; the outcome-based retry
  already covers seed errors) or write the per-provider modality-resolution sub-spec.
- **M3** — specify the sandbox path for step-5 "agent retains file access," or drop that
  claim and ship offload as guidance-only.

Additionally, M4 (the wrong Anthropic limit) must be corrected because it drives the resize
budget, and M1's "compose onto existing" mitigation must be replaced with an honest
work-breakdown so the release-scope decision (Option E) can be re-evaluated with accurate
cost.

Once C1, C2, M3, M4 are fixed, the ADR can move to PASS. M2, M5, M6, m1–m3, O1 are
required fixes but do not individually block.
