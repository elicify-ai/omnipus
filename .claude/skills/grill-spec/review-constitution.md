# Adversarial Review Constitution

These principles govern the adversarial review process. The reviewer MUST
evaluate the document against every applicable principle. When a principle is
violated, it becomes a finding.

Last reviewed: 2026-09-25

## Core axioms

1. **The document is wrong until proven right.** Do not extend the benefit
   of the doubt. If something is unclear, it is a defect.
2. **Silence is a bug.** If the document does not address a concern, that
   concern is unaddressed — not implicitly handled.
3. **Every requirement must be testable.** If you cannot write a test for a
   requirement, the requirement is defective.
4. **Every test must trace to a requirement.** Orphan tests indicate scope
   creep or missing requirements.
5. **Failure is the default.** Assume every external call fails, every input
   is malformed, every user is confused, every screen is used without a
   mouse, and every attacker is motivated.
6. **Frontend earns the same suspicion as backend.** A UI section that
   names only the happy-path render is exactly as defective as a backend
   section that names only the success response — never grade one leniently
   because it "obviously" works.
7. **A code claim is unverified until checked against the tree.** If the
   document asserts existing behaviour ("the API already returns X", "this
   component already handles Y"), verify it with GitNexus or Grep before
   accepting it — do not let a confident sentence stand in for evidence.

## Lens 1 principles: Ambiguity

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| AMB-01 | Every domain term must be defined exactly once | Using "workload", "service", and "application" interchangeably |
| AMB-02 | Requirements must use RFC 2119 language (MUST/SHOULD/MAY) | "The system will try to..." or "The system handles..." |
| AMB-03 | Numeric thresholds must have explicit units and bounds | "Response time should be fast" without ms/s and percentile |
| AMB-04 | Conditional logic must cover all branches | "If the user is authenticated, show the dashboard" (what about unauthenticated?) |
| AMB-05 | Error messages must specify exact content or format | "Display an appropriate error message" |
| AMB-06 | Time references must be absolute or relative with a defined anchor | "Recently created", "old records", "stale data" |
| AMB-07 | Quantities must be explicit | "Multiple retries", "a few seconds", "several items" |

## Lens 2 principles: Incompleteness

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| INC-01 | Every external dependency must have a failure mode scenario | Assuming the database/API/queue is always available |
| INC-02 | Every user input must have validation rules specified | Accepting user input without defining valid range/format |
| INC-03 | Every state machine must show all transitions, including error states | Happy path only state diagrams |
| INC-04 | Data lifecycle must be complete: create, read, update, delete, archive | Specifying creation but not cleanup/deletion |
| INC-05 | Concurrency model must be specified for shared resources | Assuming single-threaded access to shared state |
| INC-06 | Idempotency requirements must be stated for retryable operations | POST/PUT without duplicate detection |
| INC-07 | Timeout values must be specified for every blocking operation | "Wait for response" without timeout or fallback |
| INC-08 | Pagination must be specified for any list/query operation | Returning unbounded result sets |
| INC-09 | Rate limiting must be specified for any public-facing endpoint | No throttling on API endpoints |
| INC-10 | Migration strategy must be specified for schema/data changes — unless the change is explicitly greenfield (founder ruling: no migrations, no upgrade backfills) | Adding new fields without saying whether existing data is backfilled or the change is greenfield |

## Lens 3 principles: Inconsistency and contradiction with ADRs / AS-IS

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| CON-01 | The same concept must use the same name everywhere | "user ID" in one section, "userId" in another, "user_id" in a third |
| CON-02 | Traceability must be bidirectional with no orphans | Requirements without scenarios, scenarios without tests |
| CON-03 | Priority ordering must be consistent across dependencies | P0 feature depending on P3 prerequisite |
| CON-04 | Data types must be consistent across all references | String in one place, integer in another for the same field |
| CON-05 | Error codes/messages must be consistent across scenarios | Different error messages for the same failure condition |
| CON-06 | Acceptance criteria must not contradict each other | "MUST allow special characters" and "input MUST be alphanumeric" |
| CON-07 | Claims about existing code must match `AS-IS-architecture.md` and the tree itself, not an assumption | "The agent loop already retries on timeout" when neither the AS-IS doc nor GitNexus confirms it |
| CON-08 | The document must not contradict an existing `ADR-*.md`'s decision without citing it and superseding it explicitly | Quietly reintroducing per-agent sandbox profiles after ADR-035 removed them, without mentioning ADR-035 |
| CON-09 | The document must not reintroduce a retired surface | Command Center UI, raw cron display, JPEG screencast fallback, goal confirm-gate, fail-closed tool-policy backfill, goal-ending-on-lost-UI watchdog (root `CLAUDE.md`, "Retired surfaces") |

## Lens 4 principles: Infeasibility

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| FEA-01 | Requirements must be achievable with the stated tech stack (single Go binary, pure Go, no CGo, file-based storage) | Requiring a dependency this repo forbids (Postgres, Redis, CGo, a new runtime) |
| FEA-02 | Performance targets must be realistic for the architecture | Sub-millisecond response with multiple service hops |
| FEA-03 | Test scenarios must be reproducible in CI/CD | Tests requiring manual setup, specific network conditions, or time-of-day |
| FEA-04 | Success criteria must be measurable with available tooling | Metrics requiring instrumentation that doesn't exist |
| FEA-05 | Ordering guarantees must be achievable given the actual concurrency model | Assuming global ordering without a coordination mechanism |
| FEA-06 | Kernel-level features must degrade gracefully below Linux 5.13 / on non-Linux, per Hard Constraint #4 | A requirement that only works with Landlock/seccomp, with no stated fallback |

## Lens 5 principles: Contract-first gaps

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| CTR-01 | Every new wire type has a schema in `contracts/components/schemas/` before any code | A handler that invents a JSON shape and only later gets a schema, if ever |
| CTR-02 | The five-step add-a-wire-type order is followed: schema -> reference from openapi/asyncapi -> `scripts/gen-contracts.sh` -> commit generated diff -> use the generated type | Writing the Go struct and the TS interface by hand, "to save time" |
| CTR-03 | Cross-boundary types come only from `pkg/api/generated/` / `src/lib/api/generated/` | A parallel hand-written struct/interface shadowing the generated one |
| CTR-04 | A discriminated union is hosted inline in `openapi.yaml` over internal refs, per ADR-034 | External `$ref`s inside a `oneOf`, which oapi-codegen inlines as non-compiling anonymous structs |

## Lens 6 principles: Security (STRIDE + user promises)

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| SEC-01 | Every entry point must specify authentication mechanism | Endpoints without auth requirements |
| SEC-02 | Every operation must specify authorization rules | "Authenticated users can..." without role/permission checks |
| SEC-03 | Every sensitive operation must produce an audit log entry | State changes without audit trail |
| SEC-04 | Error responses must not leak internal details | Stack traces, internal IPs, or database schemas in error messages |
| SEC-05 | All inputs must be validated at the system boundary | Trusting data from external sources |
| SEC-06 | Secrets must never appear in logs, URLs, or error messages | API keys in query parameters, tokens in log output |
| SEC-07 | Data at rest and in transit must specify encryption requirements | Storing PII without encryption specification |
| SEC-08 | Session/token management must specify expiry and revocation | Tokens without TTL or invalidation mechanism |
| SEC-09 | Resource limits must be specified to prevent exhaustion | Unbounded file uploads, unbounded query results, no connection limits |
| SEC-10 | A new tool is added with an explicit policy entry for every agent — the two-layer model (ceiling + per-agent tighten-only), never a third layer or a hardcoded fallback | A new tool the spec assumes "denies by default" — this repo has no such fallback (Hard Constraint #6) |
| SEC-11 | A user-visible feature must not silently weaken a promise `docs/security.md` or `docs/tools.md` already makes to the user | Changing Auto-approve semantics or a tool-policy default without updating what the user was told |

## Lens 7 principles: Reachability

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| REACH-01 | An agent-facing tool has a builtin catalog entry and an explicit policy entry for every agent | A tool implemented in `pkg/tools/` with no policy row — `grep -rl '"<tool_name>"' pkg/coreagent/ pkg/config/ pkg/tools/` returns 0 |
| REACH-02 | A user-facing feature has a named screen or component that renders it | A backend endpoint with no SPA surface — a library, not a feature |
| REACH-03 | The test plan states execution, not only authorship | "Tests written" claimed as done when no run has produced a result |
| REACH-04 | A "done" claim states both code-correct-and-tested and reachable-by-a-user-or-agent, as two separate lines | A single "complete" claim that only covers correctness |

## Lens 8 principles: UI states and journey gaps

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| UIJ-01 | Every screen/component names loading, empty, error, and partial states | A mock or spec that only shows the populated happy-path state |
| UIJ-02 | The user journey is described start to finish, not as a component inventory | "Add a Settings panel" with no description of how a user gets there or what happens next |
| UIJ-03 | Touch and narrow-screen behaviour is addressed for anything reachable outside desktop | A flow that assumes hover, drag, or a fixed wide layout with no narrower fallback stated |
| UIJ-04 | A journey step does not assume a retired surface | Referencing the Command Center screen or a raw cron UI that no longer exists |
| UIJ-05 | Partial-failure states (some data loaded, some failed) are distinguished from full error and full loading | Only "loading" and "error" named, with a partial/degraded state silently folded into one of them |

## Lens 9 principles: Accessibility and keyboard

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| A11Y-01 | Every interactive element is operable by keyboard alone | A drag-only reorder control with no keyboard equivalent specified |
| A11Y-02 | Focus behaviour is specified for anything non-trivial (dialog open/close, toast, route change) | "The dialog opens" with no statement of where focus goes |
| A11Y-03 | Dynamic content that should be announced to assistive tech is called out | A live status update with no mention of an ARIA live region or equivalent |
| A11Y-04 | No emoji specified in stored data or UI chrome | A notification template or empty-state copy that includes an emoji |

## Lens 10 principles: Design-system reuse and brand

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| DSB-01 | `design-system/catalog.json` is checked before proposing any new component | A new one-off "SmallButton" for something `Button` already covers |
| DSB-02 | Colour, spacing, type size, and line-height come from tokens, never a literal value | "Use a light gray, about 14px, with a bit of padding" |
| DSB-03 | A recurring UI job (tooltip, inline error, copy-to-clipboard, confirm) reuses a catalogued component (design-system skill rule 14) | Building a bespoke tooltip component for one screen |
| DSB-04 | A genuinely new component states the four-part publication contract it will need | A new component described with no mention of the catalog entry, `@source` line, or manifest it will require |
| DSB-05 | The document holds to The Sovereign Deep brand (dark-first, chat-first, defined palette) | Copy or mockups that drift from the stated brand archetype with no rationale |

## Lens 11 principles: Testability and false-green risk

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| TEST-01 | Every BDD scenario's expected values are derived from the spec's own stated behaviour, never from reading a draft implementation | A `Then` step whose expected value matches whatever the prototype currently returns |
| TEST-02 | A test asserting on wall-clock elapsed time, or on raw file text, to prove a logic property is flagged | "Then the response arrives within roughly a second" as the entire correctness check |
| TEST-03 | A loop with a `continue` past a condition has an assertion after the loop, not just inside the skipped branch | A validation loop that silently passes when every iteration was skipped |
| TEST-04 | CI is the named authority for Go/build results; at most one narrow local test at a time is planned | A test plan describing a full local `go test ./...` run |
| TEST-05 | A claimed "PASS"/"green" is falsifiable — the plan states what the test could not have caught | A plan that would report success even if the described defect were present |

## Lens 12 principles: Overcomplexity

| ID | Principle | Anti-Pattern |
|----|-----------|-------------|
| CPX-01 | Every abstraction must have at least two concrete implementations or a stated reason to exist | Interface with one implementation "for testability" when a concrete type and simple test double would suffice |
| CPX-02 | Configuration options must correspond to values that will realistically change | Externalizing a retry count that has been 3 for five years and nobody has ever changed |
| CPX-03 | The number of architectural layers must be justified by the problem's complexity | Request -> Controller -> Service -> Repository -> DAO -> Database for a single-table CRUD operation |
| CPX-04 | Requirements must solve the current problem, not hypothetical future ones | "MAY support pluggable storage backends" when the only backend is file-based storage |
| CPX-05 | Error handling complexity must match error likelihood and impact | Circuit breakers and exponential backoff for an internal synchronous call that fails once a year |
| CPX-06 | Test infrastructure must not exceed the complexity of the code under test | Test factories, builders, and fixtures more complex than the production code they test |
| CPX-07 | The simplest solution that satisfies all stated requirements is the correct one | Introducing an event-driven architecture when a direct function call achieves the same result |
| CPX-08 | Feature flags, toggles, and gradual rollout mechanisms must justify their maintenance cost | Feature flag for a feature that will never be toggled off after initial release |
| CPX-09 | New concepts (types, services, tables, queues) must each solve a distinct stated problem | Creating a dedicated microservice for logic that belongs in an existing module |
| CPX-10 | Performance optimizations must target measured bottlenecks, not theoretical ones | Adding caching, connection pooling, or async processing without evidence of a performance problem |
| CPX-11 | Nominal types must carry invariants, methods, or domain semantics beyond the underlying built-in type | `type StringSlice []string` when the wrapper adds no invariants, methods, or domain value |

## Review completeness check

Before finalising the review, verify:

- [ ] Every one of the twelve lenses has been applied (or explicitly marked
      not applicable, with the reason)
- [ ] Every finding cites a specific section, requirement ID, or scenario
      name from the document — and `file::symbol` (never `file:line`) for
      any code claim
- [ ] Every finding has a concrete, actionable recommendation
- [ ] Findings are classified by severity (CRITICAL, MAJOR, MINOR,
      OBSERVATION)
- [ ] No false reassurance language appears in the report
- [ ] The frontend lenses (5, 8, 9, 10) got the same depth of scrutiny as
      the backend lenses — not a token paragraph each
- [ ] The "Questions for the founder" list is genuinely open questions, not
      restated findings
- [ ] Nothing in the report recommends `/taskify`
