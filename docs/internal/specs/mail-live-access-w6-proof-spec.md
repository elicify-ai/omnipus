# Feature Specification: Mail live access — proof package (test and measurement plan, W6)

**Created**: 2026-10-02
**Status:** Draft for review — one implementation specification for the proof work package
**Revision**: 1
**Input**: `docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md` (approved design, correction round applied 2026-10-02) · its grill report `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` (I-01–I-06, M-01–M-02) · the baseline trace `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/baseline-series.md` (qa-lead, 2026-10-02)
**Work branch**: `docs/adr-mail-live-access` (this worktree)
**Package mapping**: the ADR's work-package table names the proof package **W5 — proof (qa-lead)**; this dispatch indexes the same package **W6** in the spec filename. They are one package: this spec is its implementation specification. Where this document says "the proof package", it means ADR-W5/dispatch-W6 — no other package exists between them.

---

## 1. Summary and scope

**Bottom line:** Mail operations cannot be measured today — the gateway log carries 628,457 lines and times no mail operation at all, and every existing Mail timing is a click-to-screen number from one UAT lane (baseline receipt §1 and §3, all **Verified**). Before any before/after comparison can be trusted, Mail must record a timing record per operation with safe fields. This specification defines (a) that instrument and the tests that prove the instrument itself cannot lie, (b) the failable test lists for the pooled runtime, the caches and the six founder features, (c) the mutation list the CHECK auditor runs, (d) the executable measurement plan over the same 13 mailboxes with cache-first display measured separately from live fetch, and (e) the UAT campaign rules for the live instance.

**In scope (this package owns):** test files only — RED fixtures under `pkg/email/`, `pkg/gateway/`, frontend test siblings and `tests/e2e/`; the CHECK/mutation audit; the measurement procedures and receipts; the UAT campaign plan. The **instrument's record shape and safety rules are specified here** and consumed by the production owners (W1/W2/W4) — the emitting code is production code and belongs to them, exactly as the contract files belong to W0.

**Out of scope:** production code, contract-file edits, test *execution* beyond the single narrow local run the repo rules permit, and any push (dispatch restriction; ADR "Scope: design only" carries to the spec). No measurement is performed by this task; every number in this document is either an ADR-accepted target or a number read from the baseline receipts, labelled as such.

**Sizing note:** this is the test-and-measurement half of a feature-size change; the RED/GREEN/CHECK sequence, the 5-reviewer gate and the founder's yes remain ahead of any implementation. Nothing here re-opens a founder decision — Q1=A, Q2=B, Q3=A, Q4=A, Q5=A are settled (ADR "Founder decisions — every recorded question answered").

---

## 2. Existing Codebase Context

### 2.1 The evidence baseline this plan starts from

The baseline receipt (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/baseline-series.md`, build `0.1.1+ee936a38`, branch tip of `docs/adr-mail-live-access` at the ADR's evidence baseline) establishes, **Verified**:

| Finding | Consequence for this package |
|---|---|
| The gateway log times **no** mail operation: 628,457 lines scanned; `rest: mail upstream failure` (80 lines) and `email watcher: cycle failed` (125) carry class+error only, no duration; **zero** `Tool execution completed` lines for any tool ever | A measured comparison is impossible until the instrument (§6.1) exists. Every timing claim in receipts must come from the new instrument or from the browser's network layer — never from this log |
| Mail handlers log **failures only** — `pkg/gateway/rest_mail.go::mailErr502` (with `logsafeError("rest: mail upstream failure", …)`) is the only upstream-outcome log call in this checkout (**Verified** this session) | The instrument must emit **on success**; a success-invisible instrument reproduces today's blind spot. B-P1 proves the inverse of today's gap |
| Every existing Mail timing is click-to-screen from lane-M (folders 2.5–8.1 s, inbox list 4.7–22.8 s, summary 17–25 s, all 13 Sent lists 502 `folder_missing`, one message open **never timed**); no request-clock exists anywhere | The measurement plan (§8) must establish the request clock (M-1/M-3 of the baseline receipt) before Phase 1 numbers are judged |
| Missing series: message-open latency, large-folder benchmark, connection-failure Retry exercise, attachment/HTML-preview cost, independent request clocks, Drafts timings | These six are the measurement plan's arms M-A…M-F (§8.2) |
| The summary query inherits the global 3-retry policy (`src/lib/queryClient.ts::shouldRetryQuery`, **Verified** this session) while folders/messages/detail are `retry: false` (`src/components/workspaces/mail/MailPanel.tsx::foldersQuery`/`::messagesQuery`/`::detailQuery`, **Verified**) | The Retry test (B-P24, M-E) asserts the asymmetry: zero automatic retries on reads, measured-and-accepted or removed for summary |
| The baseline build has **no cache layer** — every panel request dials fresh; all baseline numbers are live-fetch numbers by construction | "Cached display" bars are **absolute** (never compared to baseline); warm-live wins are paired against baseline warm-live re-dials only |

### 2.2 Symbols involved

Certainty: **Verified** = read in this checkout this session; **per ADR** = the ADR's evidence table records the read and its evidence label; **per receipt** = the baseline receipt records the read.

| Symbol | Role today | Relationship to this package |
|---|---|---|
| `pkg/email/imapserver_test.go::startMemIMAP(t, msgs, seenUIDs) *Client` | Existing go-imap/v2 in-memory server on loopback TCP; appends messages through a real client; restores the `imapDial` seam at cleanup (**Verified**) | The foundation of every runtime and cache test — real protocol, not a mocked final result. Tests must not blindly run in parallel (global dial seam) |
| `pkg/email/view_missing_folder_test.go::TestFolderCounts_MissingSentFolderStillOpensMailbox`, `::TestFolderCounts_MissingDraftsFolderStillOpensMailbox`, `::TestFolderCounts_CancelledRequestStillFails` | Existing count-path regression tests (**Verified**) | Must keep passing unchanged; extended by page/discovery negative controls (§6.6) — an existing count green does not prove the list fix or the unknown/absent distinction |
| `pkg/email/mail_budget.go::MailBudgetRequest.flightKey` | Today's read-coalescing key: `Account + "\x00" + Operation + "\x00" + json(Params)`; empty params opt out (**Verified** — I-01's target: no pair, no generation) | The I-01 tests (B-P8, B-P9) fail until pair/generation join the identity. Production change owned by W1 |
| `pkg/email/mail_budget.go::call`, `::flightContext` | Shared flight whose context outlives the first waiter (**per ADR, E-Budget**) | The cancellation/publication tests (B-P7, B-P10) exercise detached completions |
| `pkg/gateway/rest_mail_budget.go::mailBudgetWrap` | Maps refusals to the generated `MailUnavailableError` (`busy`/`backoff`) and upstream failures to 502 (**Verified**) | The typed-busy surface B-P3 asserts; `reason=pool_busy` is W0's contract addition |
| `pkg/gateway/rest_mail_read.go::handleMailFolders`, `::handleMailList`, `::handleMailAttachment` | Today's read endpoints; attachment path fetches the whole message then selects the part (**per ADR, E-Attachment-transfer**) | Consumers of cache-first/live modes (W0 contract); the I-05 split-preview/download tests bind to the generated shapes only |
| `pkg/email/view.go::maxViewPartBytes` = `25 << 20` | The 25 MiB decoded-part cap (**Verified**) | Boundary dataset for the preview-cap tests (DS-P5) |
| `pkg/gateway/mail_preview_token.go::MailPreviewTokenTTL` = 15 min | Today's body-retention in preview grants (**Verified**) — violates the new request-only body rule | The no-byte-cache test (B-P16 sibling) fails while this retention stands; removal is W4's |
| `pkg/email/transport.go::dialTimeout` = 30 s, `::commandTimeout` = 45 s | Existing backend bounds (**Verified**) | The acquisition-wait (5 s) sits **inside** the 45 s read budget; the deadline tests assert the bound, never widen it |
| `pkg/gateway/rest_mail.go::mailErr502` | Failure-only upstream logging (**Verified**) | B-P1's positive proof that success lines do not exist today |
| `pkg/tools/registry.go::Execute` | Logs `duration`/`duration_ms` on tool execution (**Verified** field presence; baseline receipt: zero success lines in the whole log) | Tool-path timing must survive into the instrument's records |
| `pkg/email/watcher.go::WatcherBackoff`, `::recordFailure`; `pkg/email/watcher_set.go::CycleAll` | Nominal 15-min cap with ±20% jitter (12–18 min real); sequential set loop (**per ADR, E-Watcher**) | Backoff tests assert the jitter honestly (never a hard 15-min ceiling); the fairness test proves one stalled mailbox cannot starve the other twelve |
| `pkg/credentials/store.go::Store.DeriveSubkey` | Sanctioned purpose-key derivation seam (**per ADR, E-Crypto**; grill verified compatibility) | Cache-envelope tests obtain keys only through this seam — never a second password |
| `pkg/library/transfer.go::Root.CreateUnique` | Reserves a fresh numbered file per call (**per ADR, E-Unique-save**) | The M-02 duplicate-on-lost-retry scenario and the save tests bind to its behaviour |
| `pkg/gateway/rest_settings.go::createTarGz` | Application archive walker; excludes only top-level logs/backups today (**per ADR, E-Backup**) | The exclusion test (B-P15) fails until the cache directory exclusion lands (W4) |
| `pkg/gateway/embed.go::spaBaseContentSecurityPolicy`; `src/lib/url-safe.ts::isDisplayableImageSrc`; `src/components/library/preview/KbMarkdownImage.tsx::KbMarkdownImage` | SPA permits same-origin images; the Markdown image renderer accepts any displayable src (**per ADR, E-Renderer-resources**) | The I-04 test (B-P17) proves the temporary-source policy closes exactly this same-origin gap, with a positive control |
| `pkg/config/defaults.go::defaultToolPoliciesGeneral`; `pkg/config/validate.go::ReconcileToolPolicyCeiling`; `pkg/tools/compositor.go::resolveEffectivePolicyWith`; `pkg/tools/auto_approve.go::autoApproveClasses`; `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` | The two-layer tool-policy model: literal shipped ceiling, additive reconciliation, sparse strictest-wins tightening, standard Auto classes (**per ADR, E-Tool-policy**) | The ask-default tests (B-P19) prove shipped `allow`/`allow`/`ask`, no operator overwrite, no loosening, and Q4=A's standard Auto treatment — no extra mechanism |
| `tests/e2e/setup.ts`, `tests/e2e/shards.json`, `tests/e2e/fixtures/gateway-process.ts::GatewayProcess` | Existing E2E harness: dynamic ports, own port + own `OMNIPUS_HOME` per gateway process (**Verified** file presence; pattern per the email-mail-view spec §7 note) | New E2E specs must be assigned to exactly one shard (`scripts/e2e-shards.sh check`) — the coverage-guard trap |

### 2.3 Impact assessment

The proof package **edits test files only** (plus this spec). Blast radius therefore runs through what the tests *pin*:

| Area the tests pin | Production owners affected | Risk |
|---|---|---|
| Coalescing identity (pair/generation/operation/args/purpose) | W1 (`mail_budget.go` rewrite) | HIGH — the I-01/I-02 tests gate the highest-risk concurrency correction in the design; a weak test here would let cross-pair leakage ship |
| Publication revision ordering | W1/W2 (cache layers), W4 (gateway advancement) | HIGH — B-P10 is the only guard against stale-cache resurrection |
| Instrument records | W1/W2/W4 (emission) | MEDIUM — a silent instrument produces false measurement greens; the instrument tests (§6.1) are the guard |
| Preview byte-path split (I-05) | W0 (contracts), W4 (endpoints) | MEDIUM — over-cap preview or broken Download are the two failure modes the tests must separate |
| Tool policy entries (F3) | W10 (catalog/inventory/defaults), W4 (injection) | HIGH for reachability — Hard Constraint #6: registration + explicit policy entry for every agent, else nobody can call the tool whatever the tests say |
| Git/backup exclusion | W4 (traces the real data-autocommit job — **Unknown** owner today, ADR E-Autocommit) | HIGH — the exclusion test stays red until the real job is traced and excluded; that red is correct and gates disk-cache activation |

### 2.4 Cluster placement

This package spans the **Mail transport/pool** and **Mail cache** clusters on the Go side, the **workspace Mail panel / Library viewer** clusters on the frontend side, and the **E2E/UAT** harness. It publishes no production interfaces; it consumes every other package's (§17).
