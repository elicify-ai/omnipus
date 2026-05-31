# Adversarial Review: Env-Awareness + Agent Memory Architecture Spec (v6)

**Spec reviewed**: `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/docs/internal/specs/env-awareness-and-memory-spec.md`
**Review date**: 2026-04-24
**Spec format**: plan-spec (BDD + FR-xxx + Traceability Matrix)
**Prior review**: v4 review (31 findings, BLOCK) at same path. This review supersedes it because the spec was amended to v6 after the v4 review was written and most v4 CRITs are addressed; however, v6's new Fix A material introduces a fresh set of CRITICAL defects.
**Verdict**: **BLOCK**

## Executive Summary

v6's Fix A (env-awareness preamble) is specified against several codebase invariants that do not exist: `cfg.Sandbox.AllowedHosts`, `cfg.Gateway.AllowEmptyBearer`, and a function named `buildSubturnContextBuilder` all appear in the spec but not in the codebase. A `SandboxBackend.Describe()` method is added without reconciling with the existing `DescribeBackend()` function and existing `Name()` values, and the return-value enum the spec requires does not match what `Name()` produces. Fix A's cache semantics contradict themselves (US-10 AC-11 "rebuilt on every call" vs FR-053 "lives inside the cached system prompt"), and the graceful-failure contract (FR-054) mandates error-return behaviour on provider methods whose signatures return non-error types. The prior v4 CRIT-004 recap-cost budget is only half-addressed, a validateEntityID cross-package leak is silently inherited from v5, and the parallel-lane plan has a line-range overlap that contradicts its own "no shared-file edits" rule. The spec cannot be implemented as written — at minimum six FRs would fail to compile or fail their own BDDs on first run.

| Severity | Count |
|----------|-------|
| CRITICAL | 6 |
| MAJOR | 8 |
| MINOR | 7 |
| OBSERVATION | 5 |
| **Total** | **26** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Fix A references `cfg.Sandbox.AllowedHosts` — the field does not exist

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: FR-045, US-10 AC-6, "Integration Boundaries → EnvironmentProvider" (`NetworkPolicy.AllowedHostCount`), test #51, dataset row 5 ("Allow-list mode, 3 hosts"), clarification "Does network-policy field reveal the full `AllowedHosts` list?"
- **Description**: FR-045 specifies `DefaultProvider.NetworkPolicy()` returns `{OutboundAllowed: cfg.Sandbox.AllowNetworkOutbound, AllowedHostCount: len(cfg.Sandbox.AllowedHosts)}`. Inspection of `pkg/config/sandbox.go`:
  - `AllowNetworkOutbound bool` exists at line 104.
  - **No `AllowedHosts` field exists.** A repo-wide grep for `AllowedHosts` and `OutboundHosts` in `pkg/config/` and `pkg/sandbox/` returns zero results.

  The spec also prescribes the rendered form `outbound-allowlist:<N-hosts>` and dataset row 5 ("3 hosts currently allowed"), both of which require this field to exist.
- **Impact**: Any implementer who follows FR-045 literally will write `len(cfg.Sandbox.AllowedHosts)` and the Go compiler will reject it. Test #51 (`TestEnvironmentProvider_NetworkPolicyComputation`) cannot be implemented; dataset row 5 is unproducible; US-10 AC-6's `outbound-allowlist:<N-hosts>` branch is unreachable. Lane E cannot complete its deliverable.
- **Recommendation**: Pick one:
  1. **Remove the allow-list branch from Fix A.** Drop `AllowedHostCount` from `NetworkPolicy`, simplify US-10 AC-6 to `outbound-allowed | outbound-denied`, delete dataset row 5, and delete "reveal the full AllowedHosts list" clarification. Add a follow-up issue "add sandbox allow-list support then extend env preamble".
  2. **Specify the new config field as an FR in this spec.** Add FR-061: "Add `AllowedHosts []string` to `pkg/config/sandbox.go:SandboxConfig` with JSON tag `allowed_hosts,omitempty`. Default empty. Document its interaction with `AllowNetworkOutbound`." Add a test row and update the Impact Assessment table. This is scope creep into sandbox config — call it out explicitly.
  3. **Read the value from a different source** (e.g., a sandbox policy object already constructed elsewhere). If so, specify the exact symbol — not "len(cfg.Sandbox.AllowedHosts)".

---

#### [CRIT-002] Fix A references `cfg.Gateway.AllowEmptyBearer` — the field does not exist; the actual field has different semantics

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: FR-049 ("`allow_empty_bearer` is true"), US-10 AC-10 ("`allow_empty_bearer=true`"), test #53 (`TestEnvironmentProvider_ActiveWarnings_AllowEmptyBearer`), rendered template example (no explicit bypass render, but warning text "`allow_empty_bearer is ACTIVE`" is implied by AC-10), "Integration Boundaries" section `DefaultProvider` listing reads from `cfg.Gateway.AllowEmptyBearer`
- **Description**: The spec prescribes an active warning when `cfg.Gateway.AllowEmptyBearer` is true. That config field does **not** exist. Grep results across `pkg/`, `cmd/`:
  - `pkg/gateway/gateway.go:251` has `AllowEmptyStartup bool`, but that flag gates *boot without a default model configured* — see `cmd/omnipus/internal/gateway/command.go:64` where the CLI flag `--allow-empty` maps to it. It has **nothing to do with bearer-token auth**.
  - The bearer-auth-bypass in this codebase is `cfg.Gateway.DevModeBypass` (confirmed at `pkg/config/config.go:1051`) — already in FR-049's warning list as a separate item.
  - There is no grep hit for `AllowEmptyBearer`, `allow_empty_bearer`, or equivalent in the codebase.
- **Impact**: Test #53 cannot be implemented — the config field has no compile target. The warning it is supposed to surface is a phantom. Worse: if an implementer *does* add an `AllowEmptyBearer` field to match the spec, they will introduce a new security switch that duplicates DevModeBypass with different semantics, which is exactly the opposite of the spec's intent. Either way the PR fails CI or ships a security-relevant configuration field nobody asked for.
- **Recommendation**: Delete the `allow_empty_bearer` warning entirely. Update FR-049 to list only the actually-existing conditions: `dev_mode_bypass=true`, `runtime.GOOS=="windows"` (flock no-op), `sandbox mode == fallback on Linux ≥ 5.13`. Delete test #53; renumber or skip its slot. Update dataset row 2 ("Dev host / bypass on") to drop the "**true** / false" AllowEmpty column and, if bearer-bypass variance is desired, model it as a second DevModeBypass row. Update Ambiguity Warnings / Assumptions sections likewise.

---

#### [CRIT-003] `SandboxBackend.Describe()` clashes with existing `DescribeBackend()` and the prescribed return values do not match `Name()` strings

- **Lens**: Infeasibility / Inconsistency
- **Affected section**: FR-044, FR-059, test #50, test #65, US-10 AC-5, Integration Boundaries, lane-E file list ("`pkg/sandbox/backend_interface.go` (add `Describe()` method + each existing backend implements it: `sandbox_linux.go`, `sandbox_windows.go`, `sandbox_fallback.go`)")
- **Description**: FR-044: "`DefaultProvider.SandboxMode()` returns the string from `SandboxBackend.Describe()`. Each backend implements `Describe()` returning one of: `off | fallback | landlock-abi-<N> | job-object | seccomp-only`."

  But:
  1. `pkg/sandbox/sandbox.go:229-234` already defines `SandboxBackend` with methods `Name()`, `Available()`, `Apply()`, `ApplyToCmd()`. Adding a new `Describe()` method is a breaking interface change; external implementers (if any) would break.
  2. A package-level function `DescribeBackend(backend SandboxBackend) Status` already exists at `pkg/sandbox/sandbox.go:618-635`. The close name "Describe" vs "DescribeBackend" is going to confuse every reader; one returns a string, the other returns a `Status` struct.
  3. `Name()` returns values like `"linux"`, `"fallback"`, `"windows"` — the existing per-backend identifier. The spec's prescribed enum values (`landlock-abi-<N>`, `job-object`, `seccomp-only`) are not what `Name()` produces; the ABI version comes from `abiReporter.ABIVersion()` (sandbox.go:578-582) and is populated inside `DescribeBackend`'s `Status.ABIVersion` field. A backend cannot produce `"landlock-abi-4"` from a plain `Describe() string` without access to that information. And the non-existent `sandbox_fallback.go`/`sandbox_windows.go` files (see CRIT-003a below) don't have the abiReporter implementation the spec assumes.
  4. FR-059 says "Default when backend nil → `"off"`", but the existing `DescribeBackend(nil)` returns `Status{Backend: "none", Available: false}` — the two defaults disagree on the sentinel string.
- **Impact**: (a) Adding `Describe()` to the interface forces every current backend to implement it, adding churn. (b) The prescribed enum can only be produced by re-implementing logic that already lives in `DescribeBackend`. A faithful implementation therefore duplicates `DescribeBackend` logic at every backend — guaranteed to drift. (c) Test #50 "Mocks each backend's `Describe()` return" cannot be decoupled from ABI-version detection without the backend also remembering its ABI version. (d) Tests #49-65 all depend on this — lane E blocked.
- **Recommendation**: Do NOT add a new method. Instead: call the existing `DescribeBackend(backend)` (or a wrapper) and map `Status` → string in `envcontext`. Specifically:
  1. Delete FR-044's "each backend implements Describe()". Replace with: "`DefaultProvider.SandboxMode()` calls `sandbox.DescribeBackend(backend)` and renders the returned `Status` into a stable string via a small helper `renderSandboxMode(Status) string` living in `envcontext`."
  2. Specify the mapping: `Backend=="linux" && KernelLevel && ABIVersion>0` → `"landlock-abi-%d"`; `Backend=="linux" && !KernelLevel` → `"fallback"`; `Backend=="none" || backend==nil` → `"off"`; `Backend=="fallback"` → `"fallback"`; etc. Pin every branch so the test has something to assert.
  3. Delete FR-059 entirely; the nil case is covered by `DescribeBackend(nil)`.
  4. Remove `sandbox_windows.go` and `sandbox_fallback.go` from the lane-E file list (see CRIT-003a); the only sandbox file to touch is `pkg/sandbox/sandbox.go` if at all, and only if a narrow helper is added there.

---

#### [CRIT-003a] Lane E's file list references `sandbox_fallback.go` and `sandbox_windows.go` — those files don't exist

- **Lens**: Incorrectness
- **Affected section**: "Parallel Implementation Plan → Lane E → Files" column
- **Description**: The lane E "Files" column lists `sandbox_fallback.go` and `sandbox_windows.go` as backends that implement the new `Describe()` method. The actual file layout in `pkg/sandbox/` is:
  - `sandbox.go` — interface + DescribeBackend + shared types
  - `sandbox_linux.go` — Linux backend (Landlock + seccomp)
  - `sandbox_other.go` — non-Linux fallback (one file, Go build tags for non-linux)

  There is no `sandbox_fallback.go` and no `sandbox_windows.go`. The fallback backend lives in `sandbox_other.go`; Windows does not have a dedicated backend (it falls through `sandbox_other.go` with build tag `!linux`).
- **Impact**: An implementer following the file list will create new files that duplicate `sandbox_other.go` functionality and confuse the build. Or they'll waste time looking for files that don't exist. Lane E's 8h budget is already tight.
- **Recommendation**: Rewrite lane E's file list to reflect reality: `pkg/sandbox/sandbox.go` (interface + shared helpers if any), `pkg/sandbox/sandbox_linux.go` (Linux backend), `pkg/sandbox/sandbox_other.go` (fallback). Remove invented filenames. If Windows JobObject support is planned, cite the real future file explicitly and label it out-of-scope for v6.

---

#### [CRIT-004] FR-053 and US-10 AC-11 disagree on preamble cache lifecycle; test #61 contradicts FR-053

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: US-10 AC-11 ("Rebuilt on every `BuildSystemPrompt` call — no file-mtime cache path"), FR-053 ("Env preamble lives INSIDE the cached system prompt returned by `BuildSystemPromptWithCache`. It regenerates only when the outer cache invalidates"), test #61 (`TestContextBuilder_GetEnvironmentContext_NoCache` — "Two back-to-back calls → two fresh renders (provider called twice)"), Behavioral Contract ("Preamble rebuilt on every prompt build (no file-cache; runtime state is cheap to read)"), Clarifications 2026-04-23 ("Cache the env preamble? → A: **no**")
- **Description**: Four spec sections say different things:
  1. US-10 AC-11: rebuilt on every `BuildSystemPrompt` call; never cached.
  2. Behavioral Contract: rebuilt on every prompt build; no file-cache.
  3. Clarifications (Q): cache the env preamble? → **no**.
  4. FR-053: env preamble is embedded inside the cached system prompt returned by `BuildSystemPromptWithCache`, so it is **only** rebuilt when the outer cache invalidates (new session, SOUL.md mtime change, memory-file mtime change, skill file change, or `InvalidateCache()`).
  5. Test #61 asserts "two back-to-back calls → two fresh renders (provider called twice)".

  #4 contradicts #1, #2, #3, #5. This is not a drafting slip — the design debate appears unresolved: is env preamble part of the cached prompt (FR-053 = yes) or is it re-rendered on every turn (AC-11/test/clarifications = no)? Both cannot be true.

  Additionally: the Q2 decision text says "generated once at session start, cached with the system prompt, NOT re-rendered per turn" — which aligns with FR-053 but contradicts AC-11 and the test. So the spec's own Q2 decision stakes out a third position: cached once per session (which is actually stricter than FR-053's "invalidates on SOUL.md mtime").
- **Impact**: Developers cannot implement this. Test #61 asserts two provider calls for two prompt builds; FR-053 says the provider is called once and cached. Whichever is implemented, half the requirements fail their own assertions. More insidiously: if an implementer picks AC-11 (no cache), every single turn pays the provider cost (incl. `/proc/version` read, even with sync.Once mitigation) and every LLM call's prompt-prefix cache hit rate collapses (because the preamble content is stable but the cached prompt gets rebuilt each turn). If they pick FR-053 (cached), the FR-053 "runtime config change → InvalidateCache() call on every agent's ContextBuilder" dance becomes mandatory, but no test verifies the REST hook actually invalidates the cache correctly across all per-agent ContextBuilder instances (and the "all agents" part assumes a global registry of ContextBuilders, which doesn't exist).
- **Recommendation**: Pick ONE lifecycle and rewrite everything around it.

  **Recommended: FR-053 wins (preamble is cached inside system prompt).**

  1. Rewrite US-10 AC-11: "Rebuilt on each system-prompt cache rebuild. Runtime config changes trigger `InvalidateCache()` on every affected ContextBuilder instance, so the next `BuildSystemPromptWithCache` call reflects the change."
  2. Rewrite Behavioral Contract: "Preamble is part of the cached system prompt; rebuilds with the cache."
  3. Rewrite Clarifications 2026-04-23 "Cache the env preamble?" → "Yes, embedded inside the cached system prompt. Runtime changes trigger cache invalidation via REST hooks."
  4. Rewrite test #61 to `TestContextBuilder_GetEnvironmentContext_CachedInPrompt`: two back-to-back `BuildSystemPromptWithCache` calls → provider called ONCE (sync.Once-adjacent semantics).
  5. Add test #61b: `TestContextBuilder_GetEnvironmentContext_InvalidateCacheRebuilds` — call `InvalidateCache()`, next call re-renders.
  6. Specify the REST-hook requirement precisely: "`rest_sandbox_config.go` and `rest_settings.go` handlers MUST, on successful config write, iterate the agent registry and call `InvalidateCache()` on each agent's `ContextBuilder`. There is no global registry; add one as part of this PR." (This is scope — call it out.)

---

#### [CRIT-005] FR-054 mandates error-handling on provider methods whose signatures don't return errors

- **Lens**: Infeasibility
- **Affected section**: FR-054 ("Sub-field errors in any provider method MUST render the field value as `<unknown>`"), test #57 (`TestEnvironmentProvider_GracefulFieldFailure` — "mock `Platform()` to return an error"), Integration Boundaries Provider interface definition
- **Description**: The Provider interface declared in the spec is:
  ```go
  type Provider interface {
      Platform() Platform                 // GOOS, GOARCH, kernel release
      SandboxMode() string
      NetworkPolicy() NetworkPolicy
      WorkspacePath() string
      OmnipusHome() string
      ActiveWarnings() []string
  }
  ```
  None of these methods return an `error`. FR-054 says "Sub-field errors ... MUST render the field value as `<unknown>` and log a `slog.Debug`". Test #57's description says "mock `Platform()` to return an error" — but it can't, because the interface doesn't let it.

  Compounding the confusion: `Platform()` returns `Platform{GOOS, GOARCH, Kernel string}`. If Kernel is the only sub-field that can fail (e.g., `/proc/version` missing), the graceful-failure contract applies to a field inside a composite struct, not to the provider method. The spec conflates method-level failure with field-level failure.
- **Impact**: Test #57 can't be written as described. FR-054's contract has no surface to enforce on. Implementers will either (a) add `error` returns to every provider method (breaking FR-054's signature), or (b) make every sub-field into a `*string` / `""` sentinel with no error channel (silent failure — CLAUDE.md violation), or (c) panic-and-recover inside the provider (reinventing error handling). Each option requires rewriting the interface and a cascade of tests.
- **Recommendation**: Decide the error model up front. Option A (recommended): each provider method that can fail returns `(T, error)`.
  ```go
  type Provider interface {
      Platform() (Platform, error)           // error if /proc/version unreadable
      SandboxMode() (string, error)
      NetworkPolicy() NetworkPolicy          // cannot fail (reads config in memory)
      WorkspacePath() string                 // cannot fail
      OmnipusHome() string                   // cannot fail
      ActiveWarnings() []string              // cannot fail (derived)
  }
  ```
  Rewrite FR-054 to say "any provider method that returns an `error` — on error, render the field as `<unknown>` and log `slog.Debug("envcontext: field unreadable", ...)`". Update test #57 to mock `Platform()` returning an error and assert the rendered preamble shows `Platform: <unknown>`. Update the graceful-failure Behavioral Contract row.

---

#### [CRIT-006] Prior CRIT-004 (recap cost) is only half-resolved; SC-010b's "any primary model" language still fails on Opus

- **Lens**: Incorrectness
- **Affected section**: SC-010b ("Per-session recap cost with any primary model (after 2000-token truncation + 250-token output cap): ≤ $0.05")
- **Description**: The prior v4 review (CRIT-004) found that a 5000-token truncation + Opus pricing ($15/Mtok input) violated SC-010b ($0.05). v6 halves the truncation to 2000 tokens (FR-030), which brings Opus input cost to 2000 × $15/1M = $0.030. Add the 250-token output cap at $75/Mtok output = $0.01875. Total = **~$0.049** per call — squeaks under the $0.05 cap for Opus 4.

  But "any primary model" is still unbounded. Claude Opus 4.5 costs $15 input / $75 output (not changed). Anthropic's Opus 4.6 and prospective 4.7 prices are not guaranteed to stay flat — if Anthropic bumps prices, SC-010b silently breaks. Other candidates:
  - OpenAI `o1-preview`: $15/Mtok input + $60/Mtok output + reasoning tokens billed as output. One recap could consume hundreds of reasoning tokens (not user-visible), pushing cost to $0.08+.
  - Anthropic extended-thinking mode (ThinkingCapable interface in pkg/providers/types.go:56) — thinking tokens billed; 250-token max_tokens cap does NOT clamp thinking.

  The prior review offered three fixes, including "Set a hard config cap: `recap_model` must be a ≤ $5/Mtoken-input model, and boot fails if `recap_model` resolves to something more expensive." The v6 spec did not adopt this guardrail; SC-010b still relies on implicit model-pricing assumptions.
- **Impact**: CI SC-010b test will fluke-pass on Opus 4 today and flake-fail when Anthropic bumps prices or the user upgrades to Opus 4.7. The spec lacks a mechanism to keep the cost cap honest. In a team deployment, a user who configures a thinking-capable model gets unbounded recap cost with no warning — same MAJ-002 privacy-and-cost surprise the prior review flagged.
- **Recommendation**: Pick one:
  1. Replace "any primary model" with an explicit allow-list: "SC-010b applies only when `recap_model` resolves to a model in `{claude-sonnet-*, gpt-4o-mini, z-ai/glm-*, gemini-flash-*}` — an explicit cheap-model set. If `recap_model` resolves outside this set, `AutoRecapEnabled` MUST fail-closed at boot with a config error."
  2. Enforce `max_tokens=250` AND `extended_thinking=false` in the recap call options; add a test that asserts both are set.
  3. Add FR-029a: "Recap provider options MUST set `extra_body.reasoning.exclude=true` (OpenAI) and never opt into extended thinking (Anthropic), regardless of agent's primary options."
  4. Renumber the follow-up issue list: "recap privacy review" is already listed in Done Criteria — make it explicit about cost caps too, or create a new follow-up "recap cost guardrail".

---

### MAJOR Findings

#### [MAJ-001] `buildSubturnContextBuilder` function does not exist; subagent inherits ContextBuilder via struct-literal field copy

- **Lens**: Incorrectness
- **Affected section**: FR-058, test #63 (`TestSubturn_InheritsEnvironmentProvider`), lane E file list ("`pkg/agent/subturn.go` (env propagation only: `buildSubturnContextBuilder`)"), "Existing Codebase Context → Fix A → `subturn.go:buildSubturnContextBuilder`"
- **Description**: The spec references a function named `buildSubturnContextBuilder` in `pkg/agent/subturn.go` and says FR-058 "MUST call `WithEnvironmentProvider(parentCB.environmentProvider)` so subagent inherits the same provider instance." A repo-wide grep for that function name returns zero hits. The actual subagent-context mechanism (`pkg/agent/subturn.go:402`) is a direct field assignment inside an AgentInstance struct literal:
  ```go
  agent := AgentInstance{
      ...
      ContextBuilder: baseAgent.ContextBuilder,  // <-- shared reference
      ...
  }
  ```
  There is no wrapper or builder function. The subagent shares the **same** `*ContextBuilder` instance as the parent — which means `WithEnvironmentProvider(p)` mutating the builder *already* affects both parent and subagent (if the builder pattern is additive) or neither (if it clones).
- **Impact**: Lane E cannot edit a function that doesn't exist. Test #63's description ("Subagent spawn → subagent's ContextBuilder has same provider instance") is trivially true under the current implementation (same pointer), so the test as written will pass vacuously without any new code. The spec's intent — "each subagent spawn threads the provider through" — is misaligned with the actual design. If a future refactor makes the ContextBuilder *not* shared (e.g., per-subturn clone), FR-058 as written has no hook to modify.
- **Recommendation**: Rewrite FR-058 and the lane-E file list against the real mechanism:
  - Rename the target: "FR-058: `pkg/agent/subturn.go`'s AgentInstance struct-literal at line ~402 assigns `ContextBuilder: baseAgent.ContextBuilder` by reference; this already shares the parent's env provider if the provider is stored on the ContextBuilder. Add test #63 verifying the shared-pointer property: `subAgent.ContextBuilder == parentAgent.ContextBuilder`."
  - Delete test #64's implicit assumption that a "subagent's first LLM call's system prompt contains env preamble" tests *propagation* — it tests pointer equality + preamble presence, which is a weaker claim.
  - If a stronger isolation property is desired (subagent gets an *independent* snapshot of env so runtime changes mid-turn don't leak), add an FR that specifies *cloning* the ContextBuilder. That's a design change, not a clarification.

---

#### [MAJ-002] `validateEntityID` lives in `pkg/gateway/rest.go` (unexported) — `MemoryStore.AppendRetro` cannot call it without circular or duplication

- **Lens**: Infeasibility / Inconsistency
- **Affected section**: FR-009 (inherited from v5: "`MemoryStore.AppendRetro(sessionID, Retro)`. `sessionID` MUST pass `validateEntityID` first"), test #9 (`TestMemoryStore_AppendRetro_ValidatesSessionID`)
- **Description**: `validateEntityID` is defined in `pkg/gateway/rest.go:2297` as a lowercase (unexported) helper. `MemoryStore` lives in `pkg/agent/memory.go`. To use `validateEntityID` from `pkg/agent`:
  - Option A: import `pkg/gateway` from `pkg/agent` — creates a circular dependency (gateway already imports agent via `gateway/websocket.go`, `gateway/rest.go`).
  - Option B: export `ValidateEntityID` (rename the symbol) — breaking change, needs an FR and a mention in Impact Assessment.
  - Option C: duplicate the function — maintenance burden, drift risk.
  - Option D: move `validateEntityID` to a new neutral package (`pkg/entityid` or similar) and import from both — cross-package refactor, needs FR + Impact Assessment row.

  The spec silently assumes the call is possible. It is not.
- **Impact**: Test #9 cannot be implemented. Lane M is blocked on a decision the spec doesn't make. If an implementer picks Option B unilaterally, every caller site of `validateEntityID` (listed in my grep — 7 callers across gateway package) needs updating; the reviewer team may correctly flag this as scope creep done without spec cover.
- **Recommendation**: Decide and specify. Suggested: Option D — move to `pkg/validation/entityid.go` with exported `EntityID(id string) error`. Add an FR (FR-061) capturing the move. Update all gateway callers. Add a lane column (likely "cross-cutting" or pushed into lane T's prep phase). Add a regression test that the gateway-side callers still reject the same inputs (no behavior drift from the move).

---

#### [MAJ-003] `SystemPromptOverride` is NOT dead — it is written in subturn.go but unread; the "regression test" for it is trivial to pass and meaningless

- **Lens**: Incorrectness / Ambiguity
- **Affected section**: Assumptions ("`SystemPromptOverride` remains dead (v5 MAJ-011)"), test #46 (`TestSystemPromptOverride_StaysDead`), "Available Reference Patterns" row for `subturn.go:443 SystemPromptOverride`
- **Description**: The spec asserts `SystemPromptOverride` is "dead" with a regression test. Actual state:
  - `pkg/agent/loop.go:153` declares `SystemPromptOverride string` as a field of `processOptions`.
  - `pkg/agent/subturn.go:443` actively writes to it: `SystemPromptOverride: cfg.ActualSystemPrompt`.
  - Grep for `opts.SystemPromptOverride` in `pkg/agent/loop.go` returns zero reads.

  So the field is **written but never read**. "Dead" is a misnomer — "wired up but inert" is more accurate. The spec's test #46 ("Grep assertion + compile check") is ambiguous: if the assertion is "zero occurrences of `SystemPromptOverride` in the codebase", it will fail (two occurrences exist). If the assertion is "zero READS of `opts.SystemPromptOverride`", it will pass but tells us nothing about whether the write still happens. And the reference table row lists the subturn.go:443 write location as "Dead assignment. v6 regression test asserts it stays dead OR, if wired, re-propagates the env preamble." — the OR is a paper-bag escape hatch that makes the test unreal.
- **Impact**: Test #46 as written is vacuous. A future refactor that actually *reads* `opts.SystemPromptOverride` will silently bypass the env preamble injection (because `ActualSystemPrompt` doesn't contain it). There is no regression guard against that specific failure mode. The intent of the original v5 MAJ-011 was "make sure no one re-enables this code path without realising it bypasses memory context"; v6 adds env preamble to that list but doesn't strengthen the test.
- **Recommendation**: Make the test specific. Rename to `TestSubturn_ActualSystemPromptIgnoredByLoop` and assert one of:
  1. "`grep -n 'opts.SystemPromptOverride' pkg/agent/loop.go` returns zero matches" — i.e., the read side stays absent.
  2. A positive behavioral test: spawn a subagent with a non-empty `cfg.ActualSystemPrompt`, capture the child's first LLM system prompt, assert the *parent's* `BuildSystemPromptWithCache()` output appears verbatim at the top and the override does NOT appear in the child's prompt.

  Also update Assumption #1: replace "remains dead" with "is written but not read; the read-side introduction gated by a regression test". And drop the "OR if wired" escape clause in the reference table row — either it's dead or it's not.

---

#### [MAJ-004] `parts[0]` insertion quietly shifts every existing agent's system-prompt layout; no test verifies layout is still semantically correct

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: FR-051 ("inserts env preamble as `parts[0]` when non-empty"), test #58 (`TestContextBuilder_GetEnvironmentContext_TopOfPrompt`), BuildSystemPrompt code at `pkg/agent/context.go:196-260`
- **Description**: `BuildSystemPrompt` currently builds `parts[]` with one of three identity branches as `parts[0]` (compiled-prompt branch `getWorkspaceInfo()`, custom-SOUL branch `getWorkspaceInfo()`, default branch `getIdentity()`) and then appends bootstrap, skills, memory, multi-message-output. FR-051 prepends env preamble as the new `parts[0]`, shifting identity content to `parts[1]`. Consequences not addressed in spec:
  - Every existing agent's cached system prompt is invalidated (OK, mtime-based; but cache-warm metrics dip on deploy).
  - Upstream LLM prompt-prefix cache hit rate is impacted for the first turn per-session after deploy (acceptable but worth noting in Release Notes).
  - The `"\n\n---\n\n"` separator on line 259 separates parts. A `parts = [envPreamble, identity, bootstrap, ...]` layout yields `envPreamble\n\n---\n\nidentity\n\n---\n\nbootstrap...`. The `---` visual break might confuse future Markdown-rendering UIs or an agent that thinks the `---` signals end-of-system-prompt. No test verifies the output is still parsable / rendered correctly.
  - If test #58 only asserts `strings.HasPrefix(prompt, "## Environment")`, it passes trivially but doesn't guard the preservation of identity below it.
- **Impact**: Regression risk: any LLM behaviour tuned to "system prompt starts with `# You are <identity>`" (e.g., tool-use fine-tunes, few-shot patterns in the wild) will see `## Environment` first and may mis-route. Agent evaluation benchmarks that scrape the first line break.
- **Recommendation**: Add a positive structural test: `TestContextBuilder_BuildSystemPrompt_StructureAfterEnvPrepend` — builds a prompt with env preamble enabled, asserts (a) output begins with `## Environment`, (b) followed by `\n\n---\n\n`, (c) followed by the existing identity marker (either `You are <name>` or `## Workspace` depending on branch), (d) followed by remainder in the same order as before. This is cheap to write and catches future drift. Also add a release note in Done Criteria: "system prompt layout changes — downstream prompt-prefix caches will miss on first deploy."

---

#### [MAJ-005] Lane E / Lane M overlap: both own lines inside `pkg/agent/context.go` 244-250

- **Lens**: Inconsistency
- **Affected section**: Parallel Implementation Plan → Ownership Matrix, Coordination rules 3
- **Description**: Ownership matrix:
  - Lane E: "`pkg/agent/context.go` (env integration only: lines 110-240 — boundary tightened to avoid overlap with lane M)"
  - Lane M: "`pkg/agent/context.go` (memory integration only: lines 244-250)"

  But elsewhere the spec says:
  - "Lane E edits lines 110-260 (identity + env integration + `BuildSystemPrompt`); lane M edits lines 244-250 (memory integration) + `sourcePaths` + `Rule 4`. The boundaries are line-disjoint; document exact line ranges in each PR."

  110-240 vs 110-260 is inconsistent within the same spec (the first sentence says 110-240, the second says 110-260). If the latter is correct, 110-260 and 244-250 overlap on lines 244-250. Either way, Lane E's `BuildSystemPrompt` edit (inserting env preamble at `parts[0]`) lives at line 197 in the current code, and Lane M's memory-context edit lives at line 245-248 (the `memoryContext` block). They're not line-disjoint once env preamble code is written.

  Also: the coordination rules promise "line-disjoint ownership" for context.go but don't define what happens on a line-number drift (every edit changes line numbers). The "exact line range" rule becomes meaningless after the first merge.
- **Impact**: First merge conflict. Lane E's integration PR will re-order `parts[]`; Lane M's memory block insertion will land against a file whose line numbers have shifted. The spec's own rule ("no shared-file edits") is violated before any code ships.
- **Recommendation**:
  1. Fix the inconsistency: pick one range (110-260) and document it consistently.
  2. Acknowledge that context.go IS a shared file for lanes E and M. Formalise a merge-order contract: Lane E lands first; Lane M rebases on E's tip and adjusts around E's new `parts[0]` insertion.
  3. Move the `BuildSystemPrompt` edit ownership to neither lane — make it a tiny prep-PR ("PR-0: prep BuildSystemPrompt for env + memory insertions") that lands before E and M branch. Then E and M only touch their respective helper functions.

---

#### [MAJ-006] Lane E's "Interface-first" publishes `LLMProvider.Identity()`, which was DROPPED per Q3

- **Lens**: Inconsistency
- **Affected section**: Coordination rules 1 ("Lane E publishes `envcontext.Provider` interface + `SandboxBackend.Describe()` + `LLMProvider.Identity()` shapes in a `shared-interfaces` subtask FIRST"), FR-048 (DROPPED), FR-060 (DROPPED), Q3 decision (C)
- **Description**: FR-048 and FR-060 are explicitly marked DROPPED per Q3 decision (C): no `LLMProvider.Identity()` method is added. But the Coordination rules 1 still says lane E publishes its shape. An implementer reading the coordination rules will publish an interface that the FR list says not to add.
- **Impact**: Wasted work, confused reviewers, or (worse) a PR that adds the interface and becomes un-grill-able because a reviewer can't tell which side is authoritative. Lane E's budget was reduced from ~10h to ~8h on the assumption Identity() is dropped — the coordination rules undo that assumption.
- **Recommendation**: Strike `LLMProvider.Identity()` from Coordination rules 1. Replace with "Lane E publishes `envcontext.Provider` interface and (if needed) a sandbox helper shape — no LLMProvider changes." Add a test to CI that fails if any file mentions an `Identity()` method on LLMProvider, matching the Q3 decision.

---

#### [MAJ-007] Bootstrap recap pass re-sends potentially stale transcripts to a paid provider on boot

- **Lens**: Insecurity (Information Disclosure, Privacy) / Inoperability (inherited from prior review)
- **Affected section**: FR-032 ("Bootstrap pass on gateway startup: walks `AgentRegistry.ListAgentIDs()` → per-agent `memory/sessions/` — any session without `LAST_SESSION.md` whose most-recent transcript entry > 30 min ago → enqueue one-shot `CloseSession(sessionID, 'bootstrap')`. **Gated by `AutoRecapEnabled`.**"), MAJ-002 from prior review
- **Description**: The gating on `AutoRecapEnabled=false` (default) neutralizes this during a default install. But when an operator opts in (which the spec presents as the happy path for US-3), the bootstrap pass fires once per gateway restart against every orphaned session. The spec doesn't bound:
  - How many sessions get processed per boot (no throttle).
  - What happens if `ListAgentIDs()` returns 50 agents with 100 orphaned sessions each — that's 5000 recap LLM calls in the first few minutes after boot.
  - What happens if a session's transcript is archived / rotated / retention-swept (FR-031 sweeps retros, but *session* transcripts are swept by `pkg/session/retention_sweep.go`). An archived session has no "most-recent transcript entry" — spec doesn't say whether to skip or to fail.
  - Whether the bootstrap pass respects rate limits (SC-010a/b budgets are per-session; a bootstrap burst of 5000 sessions could bill $250+ in a minute).
- **Impact**: Cost DoS / surprise bill; provider rate-limit trip; transcript privacy re-exposure (same v4 MAJ-002 concern, re-surfaced with boot-time amplification).
- **Recommendation**: Add FR-032a:
  1. Bootstrap pass is rate-limited: max N recap starts per minute (default 5, configurable via `cfg.Agents.Defaults.BootstrapRecapMaxPerMinute`).
  2. Bootstrap pass requires `AutoRecapEnabled=true` AND a separate `BootstrapRecapEnabled=true` (default false) — two opt-ins, because boot amplification is a distinct risk from steady-state auto-recap.
  3. Archived-transcript case: skip sessions whose transcript directory is absent; log warn + audit-entry with `outcome=skipped_archived`.
  4. Cost-cap: accumulate estimated cost across the bootstrap pass; stop and log-warn if daily budget is exceeded.

---

#### [MAJ-008] `_audit.jsonl` growth still not bounded at the per-memory-tool level (v4 MAJ-003 partial inheritance)

- **Lens**: Inoperability / Incompleteness
- **Affected section**: FR-011 (audit every memory mutation), Explicit Non-Behaviors ("`recall_memory` reads NOT audited"), missing audit retention FR
- **Description**: The prior review's MAJ-003 called out unbounded `_audit.jsonl` growth on the memory audit. v6 addresses PART of it — "recall_memory NOT audited" removes the hot-path — but still writes every `remember` / `retrospective` / `auto_recap` to the audit log with no retention, rotation, or cap specified in this spec.

  The `pkg/audit.Logger` does have rotation — `MaxSizeBytes` (default 50MB) and `RetentionDays` (default 90) — and `cleanupExpired` is called on startup. That's fine for the pkg/audit file itself. But if the memory audit writes to a *separate* file (`memory/_audit.jsonl` referenced in FR-039 of prior v5 — though v6 removed this and says "Reuse `pkg/audit.Logger`"), the separate file bypasses the Logger's rotation. v6 says reuse the shared logger, which is good, but:
  - Shared audit file now conflates memory-mutation audits with security-policy audits, command audits, tool-invocation audits, etc. Filtering to "memory mutations in the last 30 days" is an O(N) scan. No index.
  - 90-day default retention on the shared log + memory mutations every ~6 turns → growth is linear with agent activity; no operational knob specific to memory audit frequency.
- **Impact**: Operator investigating a memory-related incident has to grep 50MB files. No per-event-type retention (e.g., "keep memory.remember for 7 days, keep security.policy.change for 1 year"). This isn't a CRIT because the shared logger's rotation is a real safety net, but it is an operability gap.
- **Recommendation**:
  1. Add an FR clarifying: "Memory-mutation audit entries share the `pkg/audit.Logger` with other subsystems. Operators can filter by `event` field (e.g., `memory.remember`). No separate per-tool retention policy."
  2. Add an operator doc note: how to use `jq` to filter the shared audit log for memory events.
  3. Consider whether `memory.remember` writes at 50MB/90d are going to crowd out more important security.* events. If yes, add a future-feature follow-up: separate retention per event-class.

---

### MINOR Findings

#### [MIN-001] Active-warnings criterion "sandbox backend is `fallback` on Linux kernels ≥ 5.13" depends on kernel detection that can fail silently

- **Lens**: Incompleteness
- **Affected section**: FR-049 (active warnings)
- **Description**: One active-warning condition is "sandbox backend is `fallback` on Linux kernels ≥ 5.13 (indicates explicit downgrade)". To evaluate this, the provider must know (a) current backend identity (via SandboxMode()) AND (b) current kernel version. But the kernel version is itself a `Platform()` sub-field that FR-054 allows to render as `<unknown>` on error. If `/proc/version` is unreadable, the warning condition silently never fires, hiding a real downgrade.
- **Recommendation**: Add a tie-breaker clause to FR-049: "If kernel version cannot be detected, the 'fallback-on-modern-kernel' warning is emitted regardless (conservative: warn on unknown-kernel rather than hide the downgrade)."

---

#### [MIN-002] FR-055 secret-redaction regex misses common secret formats

- **Lens**: Insecurity (Information Disclosure)
- **Affected section**: FR-055 ("`(?i)(key|token|secret|password|auth|bearer)`"), test #55 (`TestEnvironmentProvider_NoSecretLeakage`)
- **Description**: The regex catches VARIABLE NAMES containing those keywords but not VALUES. An env var named `OPENROUTER_API_KEY` triggers the match; the value `sk-or-v1-abc123...` does NOT. The env preamble doesn't render env-var values anyway (it only reads named fields like `SandboxMode`, `WorkspacePath`), so the practical leak path is small — but if a future FR adds "render the effective OPENROUTER_BASE_URL for debugging", a URL containing `?api_key=sk-...` is a straight leak. Also: the codebase already has `pkg/audit/redactor.go` which does proper pattern-based value redaction with AWS keys, JWTs, private keys, etc. FR-055 re-invents a weaker mechanism.
- **Recommendation**: Replace the keyword-substring scan with a call to `pkg/audit/redactor.go` patterns (or a subset relevant to env-preamble content). Update FR-055 to specify the exact redactor invocation. Keep test #55 but expand the dataset to include AWS keys, JWTs, OAuth tokens.

---

#### [MIN-003] Env-preamble template uses hard-coded example paths that look like defaults

- **Lens**: Ambiguity
- **Affected section**: "Integration Boundaries → ContextBuilder env preamble rendering"
- **Description**: The rendered Markdown example shows `/home/daniel/.omnipus/agents/ava` and `/home/daniel/.omnipus`. These are one user's paths. A reader skimming the spec might mistake them for defaults or accidentally check them in as constants.
- **Recommendation**: Replace with placeholder tokens: `<absolute workspace path>` and `<absolute OMNIPUS_HOME path>`, with a note "values are runtime-derived".

---

#### [MIN-004] E2E test #75 hardcodes `/tmp/test-home`; CI parallel runs can collide

- **Lens**: Infeasibility
- **Affected section**: test #75 (`test_e2e_workspace_path_correctness_under_omnipus_home_override`), "All E2E tests run against the embedded SPA binary ... Test fixture: `$OMNIPUS_HOME=/tmp/omnipus-e2e-v6`, fresh for each test"
- **Description**: The E2E fixture uses a single shared path. If two E2E tests run in parallel (and the spec doesn't explicitly forbid this), they'll clobber each other's `OMNIPUS_HOME`. Test #75 also hardcodes `/tmp/test-home`, which overlaps with the fixture root but isn't the same directory — confusing.
- **Recommendation**: Use `t.TempDir()` or `os.MkdirTemp("", "omnipus-e2e-*")` per test. Specify this in the E2E Test Specification section. Rename test #75's env to a per-test temp path.

---

#### [MIN-005] Bootstrap-pass definition "most-recent transcript entry > 30 min ago" has ambiguous mtime source

- **Lens**: Ambiguity
- **Affected section**: FR-032
- **Description**: Does "most-recent transcript entry > 30 min ago" mean (a) stat mtime of the latest `.jsonl` file, (b) last entry's `ts` field, or (c) last entry's file modification time? These can differ (a user attaches the session from another device; the file gets rewritten but no new entry is added). Spec doesn't say.
- **Recommendation**: Pin the definition: "The `ts` field of the newest transcript entry across all `.jsonl` files in the session's directory." Add a test case for the ambiguous variants.

---

#### [MIN-006] US-12 "parallel implementation safety" has no enforcement mechanism

- **Lens**: Incompleteness
- **Affected section**: US-12 (Acceptance criterion 1: "Each lane owns its files exclusively"), Coordination rules 3
- **Description**: There's no CI check or pre-merge script that enforces lane file boundaries. If lane M's PR touches `envcontext/provider.go` (owned by lane E), the rule is violated but nothing catches it.
- **Recommendation**: Add FR-061 (or a Done Criteria item): "A pre-merge check compares the PR's file list to the declared lane ownership; mismatches fail CI." Suggested implementation: a `.github/CODEOWNERS`-style file mapping paths to lane labels plus a GH Action that verifies the PR label matches its diff. If this is too much for v6, at minimum, add a manual checklist item to the PR template.

---

#### [MIN-007] Test count arithmetic is consistent, but "74 total" is easy to get wrong after future drops

- **Lens**: Ambiguity
- **Affected section**: TDD plan heading, FR-038, Done Criteria
- **Description**: Spec says "74 total — 50 from v5 + 25 new for env-awareness and expanded E2E, MINUS test #66 per Q3 = C decision". Checking: rows 1-65 = 65 rows, 67-75 = 9 rows, #66 dropped. Total 74. Math is right. But the naming "75 tests" vs "74 total" vs "test #46" etc. creates a gap: future drops will desync the count.
- **Recommendation**: Replace numeric count with "count of live rows in the test table". Make the TDD plan header auto-computed (or just write "Tests 1-75 with 1 dropped = 74 live").

---

### Observations

#### [OBS-001] "System Agent refactor (issue #141)" out-of-scope coupling

- **Lens**: Incompleteness
- **Affected section**: "Out of scope (v6)" list, FR-016 (exclude `main` / `omnipus-system` from memory tools)
- **Description**: FR-016 excludes memory tools for `omnipus-system`. If issue #141's refactor renames or restructures the system agent, this exclusion list drifts. No follow-up ticket or cross-PR coordination noted.
- **Suggestion**: Add a follow-up in the Done Criteria list: "file issue #141 comment referencing FR-016 so refactor author knows to update the exclusion list."

---

#### [OBS-002] Wave sequence claims 12h wall-clock parallel compression — doesn't account for integration friction

- **Lens**: Overcomplexity
- **Affected section**: Appendix: Sequence of implementation, "Total: ~38h (reduced from ~40h after Q3 = C). Parallel execution compresses to ~12-14h wall-clock"
- **Description**: 38h sequential compressing to 12h parallel requires perfect parallelism (38/3 = ~12.7h). Real projects hit integration bugs, merge conflicts, blocking reviews. The 12-14h estimate ignores that the final T-phase reviews can surface blockers that kick work back to E/M/S.
- **Suggestion**: Add a 20-30% overhead factor: revised estimate 16-20h. Spec remains technically correct but operators plan more realistically.

---

#### [OBS-003] "Always-on" decision Q1=A removes the off-switch, but this conflicts with future debugging needs

- **Lens**: Ambiguity
- **Affected section**: Q1 decision ("A — always on, no off-switch"), US-10 ("always present")
- **Description**: A future support engineer debugging an agent issue may want to temporarily suppress the env preamble to isolate a problem ("does the agent behave differently without the preamble?"). The decision to ship with no off-switch forecloses this test path.
- **Suggestion**: Consider adding a dev-only env var (`OMNIPUS_ENV_PREAMBLE_DISABLED=1`) that disables the preamble. Document it as debug-only, not user-facing. This doesn't violate Q1's intent (agents are always preamble-aware) but preserves ops-debugging ability.

---

#### [OBS-004] Q5 "no baseline SHA — full green" interacts with flaky CI

- **Lens**: Incorrectness
- **Affected section**: FR-040, SC-008, US-9, Q5 decision
- **Description**: "Any pre-existing flake gets fixed in this PR" is a strong commitment. Flakiness in other subsystems may not be fixable by this PR's author. The spec has no escape hatch ("if the flake is clearly unrelated, it's documented and skipped via an issue"). This can stall merge indefinitely.
- **Suggestion**: Add a narrow escape: "a pre-existing flake unrelated to env-awareness or memory may be quarantined via `t.Skip` with a tracking issue, up to 2 such skips per PR, approved by architect review."

---

#### [OBS-005] SC-017 "≤ 1 ms warm-process median" latency target is unmeasured

- **Lens**: Incompleteness
- **Affected section**: SC-017 ("Env preamble rendering latency ≤ 1 ms on warm-process median (excluding first-ever `/proc/version` read). Benchmark test.")
- **Description**: "Benchmark test" is the entirety of the spec. No benchmark file named, no success threshold under contention, no CI gate. SC-017 will exist as spec text with no enforcement hook.
- **Suggestion**: Name the benchmark: `BenchmarkContextBuilder_GetEnvironmentContext` in `pkg/agent/envcontext/provider_bench_test.go`. Define the assertion: `b.ReportAllocs()` + "median < 1ms across 1000 iterations on warm provider state". Wire it to `go test -bench=.` in CI (non-blocking warn if median regresses > 2x, block if > 5x).

---

## Structural Integrity

### Variant A: Plan-Spec Format

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-10..12 and US-1..9 all have acceptance criteria. |
| Every acceptance scenario has BDD scenarios | PASS | BDD Scenarios section covers env + memory. |
| Every BDD scenario has `Traces to:` reference | FAIL | BDD scenarios in the Env-awareness (Fix A) subsection do NOT include per-scenario `Traces to:` back-references — they are bullet-form. Memory scenarios say "All v5 BDD scenarios preserved unchanged" but do not inline the traces. Verification requires cross-reading v5. |
| Every BDD scenario has a test in TDD plan | PARTIAL | Env-awareness BDD scenarios map to tests #49-65 via Traceability Matrix. But several BDDs cite test shapes that don't match their listed test number (e.g., "Unknown sub-field renders `<unknown>`" BDD vs test #57, which fails CRIT-005 as written). |
| Every FR appears in traceability matrix | PASS | FR-001..FR-060 covered; FR-048 and FR-060 explicitly marked DROPPED. |
| Every BDD scenario in traceability matrix | FAIL | The traceability matrix lists FRs → BDDs, not BDDs → FRs. Given the bullet-form BDDs, reverse verification requires manual scanning. |
| Test datasets cover boundaries/edges/errors | PARTIAL | Env-preamble dataset has 8 rows; covers several OS/sandbox permutations. Missing: concurrent render calls (SC-017 claim but no dataset row), provider errors on multiple fields simultaneously (test #57 is single-field), and secret-like-env-var injection (test #55 dataset not specified). |
| Regression impact addressed | PARTIAL | Impact Assessment table exists but doesn't call out MAJ-004 (parts[0] shift impact on existing agent prompts). |
| Success criteria are measurable | PARTIAL | SC-015..025 mostly measurable (test-based). SC-017 (latency) has no CI gate. SC-010a/b have cost budget but no enforcement hook. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| **Concurrent env-preamble renders** | No test for 1000 parallel `BuildSystemPrompt` calls asserting byte-identical preambles (H-9 holdout claims this but no test row). | SC-017 latency claim; H-9 holdout |
| **REST config hook → InvalidateCache → next prompt reflects change** | Test #62 ("config-change-reflected") describes the property but doesn't specify the call must go through the REST handler path. | FR-053 runtime-config-change requirement |
| **Cross-lane integration** | No integration test that E+M+S all land together and a new session gets env preamble AND memory context AND auto-recap. | Merge-integrity, the whole point of v6 |
| **Env preamble with missing workspace** | What if `agent.Workspace` is empty or the directory was deleted? Env preamble renders a bad path with no warning. | FR-046 |
| **Failed InvalidateCache broadcast** | If the REST hook iterates agents and one `InvalidateCache()` panics, do the others still get invalidated? | FR-053 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Env-preamble scenarios (Fix A) | Multi-field failure (both `Platform()` kernel and `SandboxMode()` error simultaneously) | Add row 9: "all fields error". Expected: every line shows `<unknown>`, build does not fail. |
| Env-preamble scenarios | Secret-looking substring in workspace path (e.g., `/srv/app-secret-123/.omnipus`) | Add row 10: test regex redactor doesn't over-match on legitimate path segments. |
| Env-preamble scenarios | Very long warning string that causes truncation at exactly 1999 / 2000 / 2001 runes | Add dataset rows probing the truncation boundary. |
| Recall test data (v5 carry) | Multi-tenant: two agents' MEMORY.md in sibling workspaces — cross-read prevention | v5 already covers but not re-asserted here. |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `envcontext.Provider` | ok | ok | ok | **risk** | ok | ok | CRIT-002, MIN-002: weak secret filter + phantom AllowEmptyBearer field create potential for misconfigured bypass rendering. |
| `ContextBuilder` env-preamble rendering | ok | ok | ok | **risk** | ok | ok | MIN-002: secret regex doesn't use shared redactor. MIN-003: hard-coded path example. |
| `SandboxBackend.Describe()` | ok | ok | ok | ok | ok | ok | CRIT-003 blocks implementation but no direct STRIDE threat. |
| `MemoryStore.AppendLongTerm` | ok | ok | ok | ok | **risk** | ok | MAJ-008: unbounded audit log co-tenancy. |
| `MemoryStore.AppendRetro` | ok | **risk** | ok | ok | ok | ok | MAJ-002: validateEntityID not callable; path traversal gate is load-bearing but broken. |
| Bootstrap recap pass | ok | ok | ok | **risk** | **risk** | ok | MAJ-007: on-boot LLM burst risks DoS + cost amplification + transcript re-disclosure. |
| REST sandbox config → InvalidateCache | ok | **risk** | ok | ok | ok | ok | CRIT-004: unclear whether all ContextBuilders are invalidated; race window could serve stale sandbox state to in-flight turns. |

**Legend**: risk = identified threat not mitigated in spec; ok = adequately addressed or not applicable. "risk" markers reference the corresponding finding IDs.

---

## Unasked Questions

1. Does `OMNIPUS_HOME` change at runtime require re-rendering? The spec says "runtime state is cheap to read" but OMNIPUS_HOME is an env var read at process start. If it changes mid-process (possible via `os.Setenv`), does env preamble pick it up?
2. What is the exact format of `dev_mode_bypass is ACTIVE` in the preamble when multiple warnings are active? Newline-separated, bullet-separated, comma-separated?
3. If a user opts into `AutoRecapEnabled=true` and mid-session the operator disables sandbox enforcement, does the recap LLM call itself leak sensitive paths that were previously filtered?
4. When `runtime.GOOS=="darwin"`, FR-043's "`runtime.Version()` placeholder" returns something like `go1.22.3` — is that a valid "kernel" value for the preamble, or does it need a separate "Go runtime" field?
5. What happens when the env-preamble is 1999 runes and an additional warning arrives during this request? Does it truncate mid-warning with `[env context truncated]`, or drop the whole new warning?
6. Does `ActiveWarnings()` include warnings about warnings (e.g., "truncation applied")? Nested visibility matters for operators diagnosing "why does my agent think the env is X?".
7. If `/proc/version` is readable but malformed (e.g., bind-mount returns an empty file), does the spec prefer `<unknown>` or the raw content? FR-054 says `<unknown>`, but the test doesn't cover this.
8. How does the env preamble interact with skill-provided system prompts? A skill's SKILL.md might contain instructions that contradict the preamble's "network: outbound-denied". No resolution rule is specified.
9. What's the behavior when `cfg.Gateway.DevModeBypass` is toggled DURING a sub-turn? Parent's ContextBuilder caches the old state; subagent's shared builder reads... the new state? The old state? Race condition.
10. Is the env preamble included in transcripts saved to disk? If yes, that's additional surface for retention / export / GDPR. If no, replay/audit scenarios lose context.
11. For the "rendered Markdown template" shown in Integration Boundaries: is the ``` markdown fence literal in the prompt, or stripped? LLMs sometimes try to "escape" nested code fences, risking prompt-injection bugs.
12. Lane E's 8h budget includes "Hook `InvalidateCache()` into `rest_sandbox_config.go` + any runtime config-write site" — but which OTHER config-write sites exist? The spec doesn't enumerate them. REST settings (`rest_settings.go`), CLI overrides, hot-reload signals, runtime admin API — which? Without an enumeration, lane E can't close the feature.

---

## Verdict Rationale

**BLOCK.** Six CRITICAL findings each represent a spec requirement that cannot be implemented against the current codebase — either because the referenced field/function doesn't exist (CRIT-001, -002, -003a, MAJ-001), because two FRs contradict each other and an implementer must guess (CRIT-004), or because the prescribed interface method can't do what the spec asks (CRIT-003, CRIT-005). These are not minor drafting slips; they are the same class of "codebase invariants that do not exist" that took down the v3 spec. The recap-cost math (CRIT-006) is a half-fix of a prior CRIT.

The MAJOR findings compound this: a lane-ownership overlap (MAJ-005) plus an un-deleted interface reference (MAJ-006) mean that even if someone papered over the CRITs, the parallel-implementation plan would produce merge conflicts in the first day. The SystemPromptOverride "regression test" (MAJ-003) is a paper-bag escape hatch that protects nothing. The validateEntityID package-leak (MAJ-002) is a cross-package refactor the spec handwaves.

The fix list is extensive but mostly mechanical: decide the cache lifecycle, remove phantom fields, rename functions to match reality, pick one strategy per contradiction, and close the "any primary model" cost-cap hole. Expect 4-6 hours of spec revision before the spec is ready for implementation, and the revision should go through `/grill-spec` one more time because several finds create second-order questions.

### Recommended Next Actions

- [ ] Remove `AllowedHosts` references or add a new config field with FR + impact row (CRIT-001)
- [ ] Delete every `allow_empty_bearer` reference; keep only `DevModeBypass` (CRIT-002)
- [ ] Replace `SandboxBackend.Describe()` with a call to existing `DescribeBackend()` + render helper (CRIT-003)
- [ ] Fix the lane-E file list: `sandbox_other.go`, not `sandbox_fallback.go` / `sandbox_windows.go` (CRIT-003a)
- [ ] Resolve US-10 AC-11 vs FR-053 vs test #61 — pick one cache lifecycle (CRIT-004)
- [ ] Add `error` returns to provider methods that can fail; rewrite FR-054 + test #57 (CRIT-005)
- [ ] Tighten SC-010b with an explicit cheap-model allow-list + extended-thinking-off option (CRIT-006)
- [ ] Rename `buildSubturnContextBuilder` in the spec to match the real subturn.go mechanism (MAJ-001)
- [ ] Specify validateEntityID export/move strategy (MAJ-002)
- [ ] Rewrite test #46 to be non-vacuous (MAJ-003)
- [ ] Add positive structural test for `parts[0]` insertion (MAJ-004)
- [ ] Resolve lane E / lane M line-range overlap with a merge-order contract (MAJ-005)
- [ ] Delete `LLMProvider.Identity()` mention in Coordination rules 1 (MAJ-006)
- [ ] Add bootstrap-recap rate limit + second opt-in flag (MAJ-007)
- [ ] Document shared-audit-file retention semantics for memory events (MAJ-008)
- [ ] Address all 7 MINOR findings (reg target, redactor, path placeholders, etc.)
- [ ] Consider the 5 OBSERVATIONs as backlog items

---

**To address these findings, run:**

```
/plan-spec --revise docs/internal/specs/env-awareness-and-memory-spec.md docs/internal/specs/env-awareness-and-memory-spec-review.md
```
