# ADR-051: Should AWS Bedrock stop being build-tag-gated and become a first-class provider?

- **Status:** **Proposed — 2026-07-21.** Not ratified. The operator has made no
  decision; nothing in this ADR is licence to change code.
- **Deciders:** Daniel Piatkowski (operator); architect (recommendation).
- **Evidence level:** 1 — every claim below is cited to a file:line, a git object,
  or a measurement taken on this machine and reproduced in-document. No claim is
  carried over from prior analysis without re-verification.

## Why this ADR exists

An earlier analysis in this session classified `pkg/providers/bedrock/` as **dead
code — "nothing ships it"** and came close to recommending deletion. That reading
was **wrong**, and it was wrong in a way that will recur, because tag-gated code
looks identical to dead code under grep: the file that contains the real
implementation is invisible to the default build, so a symbol search finds only a
stub that returns `"not available"`.

The precise, verified state is:

| Claim | Verdict | Evidence |
|---|---|---|
| Bedrock has a real, complete provider implementation | **TRUE** | 590 lines implementing the Converse API incl. tool calling, multimodal images, usage accounting `[FACT — pkg/providers/bedrock/provider_bedrock.go:1-590]` |
| It is covered by tests | **TRUE** | 541 lines `[FACT — pkg/providers/bedrock/provider_bedrock_test.go]` + 35 lines of stub tests `[FACT — provider_stub_test.go]` |
| It is wired into the provider factory | **TRUE** | `case "bedrock":` with region/endpoint/timeout/profile handling `[FACT — pkg/providers/factory_provider.go:147-181]`, package imported **unconditionally** `[FACT — factory_provider.go:18]`, registered in `knownProtocols` `[FACT — factory_provider.go:414]` |
| The wire contract already knows about it | **TRUE** | `bedrock` is in the `ProbeProviderRequest.id` enum `[FACT — contracts/components/schemas/ProbeProviderRequest.yaml:24]` and in the `wire` derivation rule `[FACT — ProviderCatalogEntry.yaml — "anthropic" when id ∈ {anthropic, anthropic-messages, bedrock}]` |
| **Any shipped artifact contains it** | **FALSE** | the goreleaser release build declares `tags: [goolm, stdjson]` only `[FACT — .goreleaser.yaml:42-44]`; `bedrock` appears nowhere in `.github/`, `.goreleaser.yaml`, `docker/`, `deploy/`, or `scripts/` — its **only** occurrence outside Go source is the Makefile's documentation line `[FACT — Makefile:24]` |
| **It is selectable in the product** | **FALSE** | absent from the 23-entry provider catalog, and *deliberately* so `[FACT — pkg/providers/catalog/catalog_test.go:151-152]` |

So: **Bedrock is a working, tested, opt-in provider that no release binary contains
and no user can select from the UI.** It is not dead code; it is unreachable code
in the shipped configuration. Deleting it on a "nothing references it" reading
would destroy a real capability. That is the misreading this ADR exists to
prevent.

## Context

### The build tag was inherited, never chosen

`//go:build !bedrock` was introduced exactly once and has never been modified
since. `git log -p --follow` over the stub shows the tag line appearing in a
single commit — `2b37b1e7` "Squash-merge PicoClaw codebase as Omnipus foundation"
(2026-03-29) — with no later `+//go:build` or `-//go:build` diff hunk anywhere in
the file's history `[FACT — git log -p --follow pkg/providers/bedrock/provider_stub.go]`.
The only later commits touching the stub are `0b9c3e0c` (picoclaw→omnipus rename,
2026-03-29) and `4b092c52` (module path rename dapicom-ai→elicify-ai, 2026-07-06)
`[FACT — git log --follow --format='%h %ad %s' -- pkg/providers/bedrock/provider_stub.go]`.
Two further commits touched the *implementation* file only — `5bef5074` (Wave V4
review sweep, 15 lines changed) and `390896e3` (Sprint J security hardening, 10
insertions) `[FACT — git show --stat 5bef5074 / 390896e3 -- pkg/providers/bedrock/]`
— neither altered the tag.

**There is no Omnipus-era design decision behind this gate.** It is an artifact of
the upstream codebase that was never re-litigated. That is the specific reason
this question is worth an ADR rather than a judgement call: the status quo has
never been argued for.

### The tag's discretionary siblings

The Makefile documents exactly three discretionary tags:

> ```
> #   lite       drops WhatsApp native AND WebRTC live-browser video
> #   nogodmode  compiles out the sandbox-off ("god mode") toggle; for hosted
> #   bedrock    compiles in the real AWS Bedrock provider (stub without it)
> ```
> `[FACT — Makefile:21-24]`

`lite` and `nogodmode` are load-bearing: `lite` has a real release role (8
platform binaries via `make build-lite` `[FACT — Makefile:192-213]`) and
`nogodmode` is a hosted-deployment security posture. `bedrock` is the only one of
the three with **zero** consumers in any build target, CI workflow, or release
pipeline.

## The two questions, separated

This decision is usually stated as one question. It is two, and they have very
different costs:

1. **Should the build tag go?** — a ~4 MB binary-size question with no design
   content.
2. **Should Bedrock appear in the product?** — a **credential-architecture**
   question. This is the substantive one, and it is not "add a row to a JSON
   file".

### Q1 — the build tag: measured cost

Built on this machine, `CGO_ENABLED=0 go build`, both with `-ldflags "-s -w"`,
`./cmd/omnipus`, tags `goolm,stdjson` vs `goolm,stdjson,bedrock`, run
back-to-back on the same tree (`fix/ui-stability`, clean):

| Build | Bytes | `du -m` |
|---|---|---|
| `-tags goolm,stdjson` | 111,984,802 | 107 MB |
| `-tags goolm,stdjson,bedrock` | 116,252,834 | 111 MB |
| **Delta** | **+4,268,032 (+4.07 MiB)** | **+4 MB (+3.81 %)** |

`[FACT — measured 2026-07-21; both builds exit 0]`

Both build paths compile:
`go build -tags goolm,stdjson,bedrock ./pkg/providers/...` exits 0
`[FACT — verified 2026-07-21]`.

For scale, the embedded SPA alone is **20 MB** `[FACT — du -sm pkg/gateway/spa/ → 20]`
— five times the Bedrock delta, in the same binary, unconditionally.

**Against Hard Constraint #1** ("single Go binary… minimal footprint"): the
constraint's only *numeric* form in CLAUDE.md is "security-feature RAM overhead
< 10 MB beyond baseline". Bedrock is not a security feature and the 4 MB is
binary size, not resident RAM. So **no stated numeric constraint is violated** by
compiling it in. The relevant pressure is the qualitative "minimal footprint"
principle and the general dislike of paying for what nobody uses.

### The dependency is already paid for, regardless of the tag

`github.com/aws/aws-sdk-go-v2 v1.41.9`,
`github.com/aws/aws-sdk-go-v2/config v1.32.20`, and
`github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.53.1` are **direct**
requires — in the first `require` block, not marked `// indirect`
`[FACT — go.mod:9-11]`. A further 13 AWS/smithy modules sit in the indirect block
`[FACT — go.mod:64-76]`, and `go.sum` carries 32 AWS/smithy lines
`[FACT — grep -c 'aws-sdk-go-v2\|smithy-go' go.sum → 32]`.

Consequently module download, `go.sum` verification, dependency-graph supply-chain
surface, and Dependabot/SCA scope **already include the AWS SDK today**, tag or no
tag. The tag decides only whether ~4 MB of it is *linked*.

> **One nuance, stated rather than glossed:** `govulncheck` does symbol-level
> reachability analysis, and the tagged file is excluded from the default build.
> So while the *modules* are in scope regardless, a vulnerability in an AWS SDK
> symbol reached only from `provider_bedrock.go` would today be reported as
> unreachable. Compiling Bedrock in would make such a finding *actionable* rather
> than invisible — a small argument in favour of dropping the tag, and one I have
> **not** empirically confirmed by running `govulncheck` both ways.
> `[INFERRED — from govulncheck's documented reachability model + the verified
> fact that the only importers of the AWS SDK are provider_bedrock.go and its
> test (grep -rln "aws-sdk-go-v2" --include=*.go → 2 files, both in
> pkg/providers/bedrock/)]`

### Q2 — the credential model: the real design problem

Bedrock authenticates via the **AWS credential chain** — env vars, `~/.aws`
shared-config profiles, IAM instance/role credentials — resolved by
`config.LoadDefaultConfig` `[FACT — provider_bedrock.go:91-119]`. There is **no
API key**. The factory reflects this: the `case "bedrock":` branch never reads
`cfg.APIKey()` `[FACT — factory_provider.go:147-181]`, unlike every HTTP branch
around it (e.g. `if cfg.APIKey() == "" && cfg.APIBase == ""` for the shared
OpenAI-compatible case `[FACT — factory_provider.go:253]`).

Every one of the 23 catalog providers is API-key based
`[FACT — pkg/providers/catalog/data/providers_catalog.json — 23 entries: openai,
anthropic, google, openrouter, groq, mistral, nvidia, cerebras, ollama, azure,
z-ai, zhipu, z-ai-coding, zhipu-coding, moonshot, moonshot-cn, minimax,
minimax-cn, deepseek, qwen, qwen-intl, qwen-us, coding-plan]`, and the whole
provisioning path hard-codes that assumption at **five** independent layers:

| # | Layer | What it does | Evidence |
|---|---|---|---|
| 1 | **Wire contract — onboarding** | `provider.api_key` is `required`, `minLength: 1` | `[FACT — contracts/components/schemas/OnboardingCompleteRequest.yaml:13-26]` |
| 2 | **Wire contract — probe** | `api_key` is `required`, `minLength: 1`; Bedrock is in the same enum it cannot satisfy | `[FACT — contracts/components/schemas/ProbeProviderRequest.yaml:6-8, 78-81]` |
| 3 | **Gateway handler** | new provider with no key → **HTTP 422** `"api_key is required"` | `[FACT — pkg/gateway/rest.go:5483-5488]`; onboarding equivalents at `[FACT — pkg/gateway/rest_onboarding.go:132]` and `[FACT — rest_onboarding.go:492]` |
| 4 | **Validation probe** | empty key short-circuits to `OutcomeInvalidKey` with no network call `[FACT — pkg/providers/validate.go:483-491]`; the probe is an HTTP `POST {BaseURL}/chat/completions` `[FACT — validate.go:543]` — a path Bedrock's SigV4 Converse API does not expose | see cells |
| 5 | **SPA** | Save is gated on a non-empty trimmed key; onboarding Connect likewise | `[FACT — src/components/settings/ProvidersSection.tsx:387-392]`, `[FACT — src/routes/onboarding.tsx:1145-1148]` |

Two of those five (#1, #2) are **contract** changes, which under Hard Constraint
#8 means spec-first edits plus regenerated `pkg/api/generated/` and
`src/lib/api/generated/` committed atomically. This is not a data-only change.

**This assumption already produces a wart independent of Bedrock.** Ollama is
local and needs no key, yet it is a full catalog entry
`[FACT — providers_catalog.json — "id": "ollama", "endpointHint": "localhost:11434"]`.
The SPA lets it through only because Ollama is classified `catalogMode: 'manual'`,
where `canSave = keyChanged || modelsChanged`
`[FACT — ProvidersSection.tsx:387-392]` — but the gateway's
`api_key is required` 422 at `rest.go:5485` has no such exemption. The
single-credential-archetype assumption is therefore already leaky, and Bedrock
merely makes it undeniable.

### The catalog exclusion is deliberate, documented, and CI-enforced

Bedrock's absence from the catalog is **not** an oversight. There is a
hand-authored triage set with a human-in-the-loop drift guard: every
`knownProtocols` id must be either a catalog entry, an alias, an `AnthropicId`
sibling, or explicitly listed in `catalogExcluded`, or the test fails naming the
untriaged id `[FACT — pkg/providers/catalog/catalog_test.go:92-98, 335-353]`.
Bedrock's entry reads:

```go
// Deployment-configured; intentionally excluded from user-selectable roster
"bedrock": true,
```
`[FACT — pkg/providers/catalog/catalog_test.go:151-152]`

**But the stated reason does not survive scrutiny.** "Deployment-configured" is
also true of Azure — `GetDefaultAPIBase` returns `""` for `azure`,
`azure-openai`, *and* `bedrock`, and the drift guard exempts exactly those three
`[FACT — catalog_test.go:281-296]` — yet **Azure is a full catalog entry**, with a
placeholder endpoint hint `"<resource>.openai.azure.com"`
`[FACT — providers_catalog.json — azure entry]`. So the catalog already supports
the deployment-configured pattern. The actual thing separating Bedrock from Azure
is not deployment configuration; **it is the credential archetype.** The comment
names the wrong reason for the right decision.

Reinforcing this: ADR-031's own spec tables list `bedrock` in the same example
rows as catalog entries — "`| bedrock | true | true | false (exempt) |`"
`[FACT — docs/internal/specs/connectors-providers-redesign-spec.md:332]` and
"`| bedrock | Anthropic-compatible |`" `[FACT — same file:461]` — and the SPA's
catalogue-mode layer already names Bedrock as a 'manual' provider
`[FACT — src/lib/agents/providerCatalog.ts:15-16]`. The spec anticipated Bedrock
being visible; the implementation triaged it out at the last step.

### Feature gap: no streaming

The real provider implements `Chat`, `GetDefaultModel`, and `Region` only
`[FACT — provider_bedrock.go:139, 217, 222]`. It does **not** implement
`StreamingProvider.ChatStream` `[FACT — pkg/providers/types.go:43-52]`, which is
an *optional* interface.

**The agent loop degrades gracefully — verified, not assumed.** Streaming is
selected by a type assertion; when it fails, control falls straight through to the
blocking call:

```go
if sp, ok := activeProvider.(providers.StreamingProvider); ok && al.bus != nil {
```
`[FACT — pkg/agent/loop.go:6745]`

```go
return activeProvider.Chat(providerCtx, messagesForCall, toolDefsForCall, llmModel, llmOpts)
```
`[FACT — pkg/agent/loop.go:6791]`

No panic, no error path, no special-casing. The user-visible consequence is
precisely one thing: **a Bedrock reply lands as a single block after the full
generation latency, rather than streaming token-by-token.** Bedrock does offer
`ConverseStream` upstream, so this is an unimplemented feature, not a platform
limitation — but it would be a visible quality gap the moment Bedrock became
selectable next to 23 streaming providers.

## Decision

**Proposed, not ratified. Recommendation: Option C (drop the tag *and* add the
catalog entry, with the credential-archetype work done properly), routed to
v0.3.** Option A (status quo) remains correct until that work is scheduled —
because Option B, the tempting middle step, is strictly dominated.

The single most important structural finding: **the build tag is the trivial half
of this question and the credential model is the whole of it.** Any plan that
drops the tag without solving the credential archetype pays 4 MB in every shipped
binary and delivers nothing a user can reach.

Routing, per CLAUDE.md's routing rule: this is provider/onboarding **structure**
touching two wire contracts, so it is **v0.3**, not a v0.2 quick win.

## Options considered

| Option | Binary cost | User-visible result | Contract change | Effort | Confidence |
|---|---|---|---|---|---|
| **A — status quo (keep the tag)** | 0 | none; Bedrock reachable only by building from source with `-tags bedrock` and hand-editing `config.json` | none | none | **High** that this is *safe*; **High** that it is *not a decision*, merely an inherited default |
| **B — drop the tag only** | +4 MB always | still none — config-only, still absent from the UI | none | ~1 hour | **High** that this is dominated; do not do it alone |
| **C — drop the tag + catalog entry + credential archetype** | +4 MB always | Bedrock selectable in onboarding and Settings | **yes** — 2 schemas + regenerated Go/TS | Medium (5 layers, 2 contracts) | **Medium** as the right destination |
| **D — delete Bedrock entirely** | −4 MB (never linked today, so −0 from shipped binaries) | destroys a working capability | none | ~2 hours | **High** that this is wrong |
| **E — generalise to an `auth_kind` archetype** | +4 MB | Bedrock selectable **and** the Ollama keyless wart fixed | **yes** — superset of C | Medium-High | **Medium-Low** on scope discipline; **High** that it is the correct end-state model |

### A — Status quo: keep the build tag

**Trade-offs.** Costs nothing and breaks nothing. Bedrock stays available to the
subset of users who build from source, which for an enterprise AWS shop is a
plausible profile. Against it: the gate has *never been argued for* (verified
above), so "status quo" here means "an upstream project's decision that we
inherited by squash-merge". It also leaves the codebase in the exact shape that
caused the misreading this ADR documents — code that looks dead to every tool.

**Constraint #1 interaction.** Perfect fit: zero footprint. **Build-path
interaction:** keeps one of three discretionary tags alive for zero consumers,
i.e. keeps the discretionary tag matrix at 2³ = 8 combinations rather than 4.

**Confidence: High** that A is safe and cheap. **High** that A is nonetheless an
un-made decision rather than a made one.

### B — Drop the tag only

**Trade-offs.** Mechanical: merge `provider_stub.go` away, delete the `//go:build`
lines, drop the Makefile note. Removes a never-chosen build path and halves the
discretionary tag matrix. But it pays the full 4 MB in **every shipped binary,
on every platform, for every user**, and the user-visible outcome is *unchanged*:
Bedrock still is not in the catalog `[FACT — catalog_test.go:151-152]`, so it is
still reachable only by hand-editing `config.json`.

**Why it is dominated.** B has A's user outcome with C's footprint cost. The only
thing it buys over A is build-matrix hygiene and the govulncheck-reachability
nuance above — real but small. If the credential work is going to happen, do C; if
it is not, stay at A. B is the shape of decision that gets made because the tag
question is easy and the credential question is hard.

**Confidence: High** that B should not be adopted on its own.

### C — Drop the tag AND add the catalog entry (recommended destination)

**What it actually requires** — this is the honest scope, derived from the
five-layer table above:

1. Delete the tag and the stub; keep `Provider` unconditional.
2. Add a `bedrock` entry to `catalog.go`'s hand-authored `Entries` (the JSON and
   the TS constant are **generated artifacts**, not the source of truth
   `[FACT — pkg/providers/catalog/catalog.go:21-25]`), and remove it from
   `catalogExcluded` `[FACT — catalog_test.go:151-152]`. `wire: anthropic` is
   already the contract-mandated derivation
   `[FACT — ProviderCatalogEntry.yaml wire enum + description]` and is already
   asserted by a table test `[FACT — catalog_test.go:388]`, so **no schema or enum
   change is needed for the catalog entry itself** — this part is genuinely a data
   addition under Constraint #8.
3. **Relax `api_key` from required to conditional** in
   `OnboardingCompleteRequest.yaml` and `ProbeProviderRequest.yaml`, regenerate,
   and commit spec + generated artifacts atomically (Constraint #8, 5-step
   process).
4. Exempt keyless archetypes from the gateway's 422 `[FACT — rest.go:5483-5488]`
   and from the two onboarding checks `[FACT — rest_onboarding.go:132, 492]`.
5. Replace the HTTP `/chat/completions` probe with an archetype-appropriate
   validation for Bedrock — the current `ValidateKey` returns `InvalidKey` on an
   empty key before any network call `[FACT — validate.go:483-491]`, so Bedrock
   would fail validation 100 % of the time with the existing path. A
   `ListFoundationModels` / minimal `Converse` call is the natural substitute.
6. SPA: relax the Save/Connect gates for the keyless archetype
   `[FACT — ProvidersSection.tsx:387-392; onboarding.tsx:1145-1148]`, and surface
   *what Bedrock actually needs* — region plus a credential-chain hint — instead
   of an API-key field the user cannot fill.

**Trade-offs.** Delivers the only outcome that justifies the 4 MB: an AWS-shop
user can pick Bedrock in onboarding. Removes the never-chosen tag. Aligns the
implementation with what ADR-031's spec tables already assumed
`[FACT — connectors-providers-redesign-spec.md:332, 461]`. Against it: six
touch-points across Go, contracts, and TS; two generated-artifact regenerations;
and it ships a provider that visibly does not stream while its 23 neighbours do.
It also enlarges the *supported* surface — a keyless credential path is a new
posture the security review has not looked at.

**Constraint #1 interaction.** +3.81 % binary for a capability users can reach.
Defensible: the SPA is 5× larger and unconditional, and no numeric constraint is
breached. **Build-path interaction:** removes one of three discretionary tags,
halving the discretionary matrix from 8 to 4 combinations — the goreleaser release
matrix (3 binaries: linux/amd64, linux/arm64, darwin/arm64
`[FACT — .goreleaser.yaml:51-64]`) is unaffected either way, since it never set
the tag.

**Confidence: Medium.** High that C is the right *destination*; Medium overall,
because the value depends entirely on demand I cannot evidence (see Evidence gaps)
and because step 5 is a genuine unknown-unknown — nobody has run this provider
against real AWS in the Omnipus era. The test suite explicitly cannot cover the
build path: `TestEveryProbeProviderBuilds` skips Bedrock with
`"bedrock": true, // AWS SDK credential flow, no api_key HTTP path"`
`[FACT — pkg/providers/factory_provider_test.go:1157-1160]`.

### D — Delete Bedrock entirely

**What it would actually save.** 1,239 lines across four files — 590 (provider),
541 (tests), 73 (stub), 35 (stub tests)
`[FACT — wc -l pkg/providers/bedrock/*.go]`. Three direct `go.mod` requires
`[FACT — go.mod:9-11]` and, since the *only* Go files importing the AWS SDK are
`provider_bedrock.go` and its test `[FACT — grep -rln "aws-sdk-go-v2" --include=*.go
→ exactly 2 files]`, `go mod tidy` would also drop the 13 indirect AWS/smithy
modules `[FACT — go.mod:64-76]` and 32 `go.sum` lines. Plus the `case "bedrock":`
branch `[FACT — factory_provider.go:147-181]`, the `knownProtocols` entry
`[FACT — factory_provider.go:414]`, and the contract enum member
`[FACT — ProbeProviderRequest.yaml:29]` — the last being a **breaking wire
change**.

**What it destroys.** A complete, tested Converse-API implementation with tool
calling, multimodal image support, and usage accounting — and with it, the only
route to AWS-native model access (Bedrock-only enterprise accounts, PrivateLink,
IAM-governed model access, AWS-billed spend). Rebuilding it later is days, not
hours.

**Why it is wrong.** The saving is illusory where it matters most: since **no
shipped binary links Bedrock today** `[FACT — .goreleaser.yaml:42-44]`, deletion
removes **0 bytes from every artifact users actually download**. The saving is
confined to `go.mod` hygiene and 1,239 lines of source. That is a real but small
carrying cost, and it is being weighed against permanently destroying a working
capability. This is the option the earlier "dead code" misreading pointed at, and
the evidence does not support it.

**Confidence: High** that D should be rejected.

### E — Generalise: an explicit credential archetype

Rather than special-casing Bedrock, introduce an explicit auth archetype on the
catalog entry — conceptually `auth_kind ∈ {api_key, aws_chain, local, cli}` — and
let every layer branch on it instead of assuming `api_key`.

**Trade-offs.** This is the model the system is already reaching for and failing
to express: `catalogExcluded` calls Bedrock "deployment-configured" when the real
distinguisher is credentials `[FACT — catalog_test.go:151-152 vs the azure catalog
entry]`; Ollama needs no key yet occupies an API-key-shaped slot; and
`knownProtocols` already mixes CLI executors (`claude-cli`, `codex-cli`) and
self-hosted infra (`vllm`, `litellm`) into one keyed abstraction
`[FACT — factory_provider.go:410-470]`. E fixes the class of problem, not the
instance, and makes future keyless providers (Vertex AI, Azure managed identity)
additive rather than each a new special case.

Against it: strictly larger than C, and scope-creeps a 4 MB build-tag question into
a provider-model refactor. It is the right end-state, but only if it is scheduled
as its own v0.3 item — not smuggled in as "while we're here".

**Confidence: Medium-Low** on adopting E *now* (scope discipline); **High** that
`auth_kind` is the correct eventual model.

## Recommendation

**Adopt C, scheduled into v0.3, with E's `auth_kind` shape used for the
credential work so C does not calcify a Bedrock-specific special case. Hold at A
until that work is scheduled. Reject B and D.**

The reasoning chain, stated plainly:

- **D is rejected** because it deletes a working capability to save bytes that are
  not in any shipped binary.
- **B is rejected** because it pays C's footprint for A's outcome.
- Between **A and C**, the deciding factor is not the 4 MB — that is small,
  breaches no stated constraint, and is dwarfed by the 20 MB SPA. It is whether
  anyone wants to select Bedrock in the UI. Since C's cost is concentrated in the
  credential-archetype work and *that* work independently repairs the Ollama wart
  and unblocks every future keyless provider, C's value is larger than
  "Bedrock ships" — which is what tips it.
- **A remains the correct interim state**, and is explicitly *not* a
  no-op: adopting A here converts an inherited accident into a deliberate,
  documented choice, which is most of what this ADR is for.

**Overall confidence: Medium.**

## Evidence gaps — stated, not papered over

1. **No demand evidence.** I found no issue, spec, or user report requesting
   Bedrock. Every doc reference `[FACT — grep -rln bedrock docs/]` treats it as an
   edge case to exempt, never as a requested feature. **C's value rests on an
   assumption of AWS-shop demand that I cannot evidence.** If the operator knows of
   no such demand, A is the correct answer and this ADR should be accepted as
   "status quo, now deliberate".
2. **Never run against real AWS in the Omnipus era.** The build path is skipped in
   the comprehensive provider test `[FACT — factory_provider_test.go:1157-1160]`
   and carried by 541 lines of unit tests only. "It compiles and is unit-tested"
   is not "it works against Bedrock". Any C work must budget for real-AWS
   validation.
3. **The "~20 build paths → 3" consolidation effort is unverified.** I could not
   find it documented in the repo. What I *did* verify: exactly 3 discretionary
   tags exist `[FACT — Makefile:21-24]`; `make build-all` emits 9 platform
   binaries and `make build-lite` 8 `[FACT — Makefile:192-213, 215-249]`; the
   goreleaser release emits 3 `[FACT — .goreleaser.yaml:51-64]`. Dropping the
   `bedrock` tag halves the *discretionary tag* matrix (8→4 combinations) and
   changes the platform matrix not at all. I have deliberately not asserted
   anything stronger.
4. **govulncheck reachability not measured both ways** — see the nuance box under
   Q1; the directional argument is inferred from documented behaviour, not run.

## Consequences

### If A is ratified (status quo made deliberate)

- **Positive:** zero cost, zero risk; the inherited gate becomes an owned decision.
- **Negative:** Bedrock remains invisible; the "looks like dead code" hazard
  persists.
- **Required regardless:** correct the misleading comment at
  `catalog_test.go:151-152` — "Deployment-configured" is factually wrong as the
  distinguisher, since Azure is equally deployment-configured and *is* in the
  catalog. Replace with the real reason: *"AWS credential chain, not an API key —
  the onboarding/Settings flow assumes api_key at five layers."* Add the same
  pointer to `Makefile:24` and to `provider_stub.go`'s package doc so the next
  reader cannot repeat this session's misreading.

### If C is ratified

- **Positive:** Bedrock becomes selectable; one never-chosen build path removed;
  discretionary tag matrix halved; the keyless-credential archetype is expressible,
  which also fixes Ollama and unblocks Vertex/managed-identity later; ADR-031's
  spec tables and the implementation stop disagreeing.
- **Negative:** +4,268,032 bytes (+3.81 %) in every shipped binary, permanently,
  including the `lite` variant whose entire purpose is smallness
  `[FACT — Makefile:191]` — **if C is adopted, decide explicitly whether `lite`
  drops Bedrock**, since `lite` already gates whatsmeow and WebRTC. Two wire
  contracts change (regeneration + `make verify-contracts`). A keyless credential
  path is new security surface requiring `security-lead` review. Bedrock ships
  visibly non-streaming next to 23 streaming providers.
- **Neutral:** `wire: anthropic` needs no contract change — already derived and
  table-tested `[FACT — ProviderCatalogEntry.yaml; catalog_test.go:388]`. The
  goreleaser platform matrix is untouched. `ChatStream` can be added later without
  a contract change, since `StreamingProvider` is an optional interface
  `[FACT — types.go:40-52]`.

### Binding on every option

**Do not delete `pkg/providers/bedrock/` on a "nothing references it" reading.**
It is tag-gated, not dead. Any future analysis reaching that conclusion should be
checked against this ADR's first table before any code is removed.
