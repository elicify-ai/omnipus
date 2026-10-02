# Feature Specification: Mail live access — proof package (test and measurement plan, W6)

**Created**: 2026-10-02
**Status:** Corrected — the one prescribed correction round applied (landing-order register §2 ownership assignments; grill-4 findings C1–C3/I1–I6/m1–m5; founder rulings 2026-10-02), then the FINAL round-2 grill applied to this file (F-1, F-4, F-5, F-6, O-1…O-3 fixed here; **F-2 and F-3 are cross-spec and routed**: F-2's dated ADR correction to the architect, F-3 to the w5 author, F-5's w1-side filename row to the w1 author — this file carries the in-place supersession note and the authoritative filename list); the landed contracts wave reconciled throughout (commit `5f23ae8a0` on this branch)
**Revision**: 3 (round-2 grill fix)
**Input**: `docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md` (approved design, correction round applied 2026-10-02) · its grill report `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` (I-01–I-06, M-01–M-02) · the baseline trace `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/baseline-series.md` (qa-lead, 2026-10-02) · correction-round sources: the landing-order register `docs/internal/specs/mail-live-access-landing-order.md` (§2 interface register — the single publisher per interface), grill round 4 `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-4.md`, and the founder rulings in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md` (correction-round input section) · round-2 sources: the final grill review `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill2-w6.md` (F-1…F-6, O-1…O-3; verdict PASS WITH FINDINGS) and the landed contracts wave with its independent check `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/contracts-wave-check.md` (commit `5f23ae8a0`, `verify-contracts` green, drift-free)
**Work branch**: `docs/adr-mail-live-access` (this worktree)
**Package mapping** (the landing-order register row 24's one-line ADR-letter ↔ spec-file map — binding for every dispatch and reviewer): **w1** = ADR-W1 (read runtime) · **w2** = ADR-W2 (discovery/cache) · **w3** = ADR-W3 (panel) · **w4** = ADR-W7–W10 (features) · **w5** = ADR-W4 (integration/privacy) · **w6** = ADR-W5 (proof — this spec) · **W0** = the ADR's contracts work package, a role (backend-lead), not a spec file — none exists and none is required (register §5). The ADR's work-package table names the proof package **W5 — proof (qa-lead)**; this dispatch indexes the same package **W6** in the spec filename. They are one package: this spec is its implementation specification. Where this document says "the proof package", it means ADR-W5/dispatch-W6 — no other package exists between them. Every ADR-numbered label in this document is read through this map (grill-4 I6).

---

## 1. Summary and scope

**Bottom line:** Mail operations cannot be measured today — the gateway log carries 628,457 lines and times no mail operation at all, and every existing Mail timing is a click-to-screen number from one UAT lane (baseline receipt §1 and §3, all **Verified**). Before any before/after comparison can be trusted, Mail must record a timing record per operation with safe fields. This specification defines (a) that instrument and the tests that prove the instrument itself cannot lie, (b) the failable test lists for the pooled runtime, the caches and the six founder features, (c) the mutation list the CHECK auditor runs, (d) the executable measurement plan over the same 13 mailboxes with cache-first display measured separately from live fetch, and (e) the UAT campaign rules for the live instance.

**In scope (this package owns):** test files only — RED fixtures under `pkg/email/`, `pkg/gateway/`, frontend test siblings and `tests/e2e/`; the CHECK/mutation audit; the measurement procedures and receipts; the UAT campaign plan. The **instrument's record shape and safety rules are specified here and frozen by this package** — w6 is the single publisher of the shape (landing-order register §2 row 17, which adopts this spec's Q-P1(b) split). The **emitting obligations** land as binding rows in the producers' specs in the same correction round: **w5-integration owns the request-scoped envelope** (gateway emitters for preview mint/serve, removal, summary), **w1 supplies the pool sub-fields**, **w2 supplies the cache sub-fields**. The emitting code is production code owned by those packages — the proof package writes tests only and never becomes the fallback implementer of a missing emitter (register R-4); the emitter must exist before Wave E, or the measurement campaign (§8) has no data source (register wave gate).

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
| `pkg/gateway/mail_preview_token.go::MailPreviewTokenTTL` = 15 min | Today's body-retention in preview grants (**Verified**) — violates the new request-only body rule | The no-byte-cache test (B-P16 sibling) fails while this retention stands; removal is w5-integration's (ADR-W4) |
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
| Coalescing identity (pair/generation/operation/args/purpose) | w1 (`mail_budget.go` rewrite) | HIGH — the I-01/I-02 tests gate the highest-risk concurrency correction in the design; a weak test here would let cross-pair leakage ship |
| Publication revision ordering | w1/w2 (cache layers), w5-integration (counter + advancing events — register row 10) | HIGH — B-P10 is the only guard against stale-cache resurrection |
| Instrument records | w5-integration (request-scoped envelope), w1 (pool sub-fields), w2 (cache sub-fields) — register row 17's split; the shape itself is frozen by this package | MEDIUM — a silent instrument produces false measurement greens; the instrument tests (§6.1) are the guard, and the emitter must exist before Wave E |
| Preview byte-path split (I-05) | W0 (contracts), w5-integration (endpoints — the single gateway Mail-route writer), w2 (the single-part reader the split consumes — register row 14) | MEDIUM — over-cap preview or broken Download are the two failure modes the tests must separate |
| Tool policy entries (F3) | w4-features (ADR-W10: catalog/inventory/defaults), w5-integration (ADR-W4: injection) | HIGH for reachability — Hard Constraint #6: registration + explicit policy entry for every agent, else nobody can call the tool whatever the tests say |
| Product-owned cache exclusion | w5-integration publishes the gate decision; w2 enforces it at the first cache write (register row 18). The condition is the product's own staging-exclusion AND backup-skip — proved by the product's own means on every install, never a dependency on operator machine setup | HIGH — the exclusion test stays red until that gate decision and its first-write enforcement land; that red gates disk-cache activation. No personal machine-setup repository, operator-side job or operator ignore file is part of this feature's landing (founder ruling 2026-10-02) |

### 2.4 Cluster placement

This package spans the **Mail transport/pool** and **Mail cache** clusters on the Go side, the **workspace Mail panel / Library viewer** clusters on the frontend side, and the **E2E/UAT** harness. It publishes no production interfaces; it consumes every other package's (§17).

---

## 3. User stories and acceptance criteria

Priorities: P0 = the gate cannot open without it; P1 = required before landing but parallelizable.

### US-P1 — The instrument: mail operations become measurable, safely (P0)

Today nothing times a mail operation (§2.1). The founder cannot be shown a before/after comparison, and the ADR's accepted bars (Q3=A) cannot be judged, until every mail operation records a timing record whose fields cannot leak mail content. The instrument is production code owned by W1/W2/W4; its record shape, safety rules and truth conditions are specified here and proven by this package's tests.

**Why this priority**: every other proof and all five measurement arms depend on it; a silent instrument is the one defect that can manufacture a false green for the whole change.

**Independent test**: exercise one operation against `startMemIMAP` with an injected recording sink; assert exactly one record with `operation`, `source`, `duration > 0`, `hit=false`, `socket_count ≥ 1` — and no sensitive marker anywhere in it (B-P1, B-P2).

**Acceptance scenarios**:

1. **Given** any successful mail operation (folders, list, open, summary, discovery, attachment read/save), **When** it completes, **Then** exactly one instrumentation record exists for it with a positive duration, the operation label, the source (`live|memory|encrypted_disk|none`), hit/miss, the socket count it used, and an outcome of `ok` or a safe class — even on the success paths that today log nothing.
2. **Given** an operation whose synthetic subject, address and folder name are distinctive marker strings, **When** the record is emitted, **Then** no marker string appears in any field of the record (and a positive control proves the scan could have found the markers had they been present).
3. **Given** a cache-first read that hits, **When** the record is emitted, **Then** `hit=true`, `socket_count=0`, and the `source` label mirrors the contract's `MailReadMetadata` scoping: `memory` for a header/list hit in Phase 1; `encrypted_disk` appears in Phase 1 only on folder-mapping hits and in Phase 2 on header hits — a Phase 1 header producer never emits `encrypted_disk` (ADR freshness-metadata scoping; register row 2 — the landed `MailReadMetadata` `source` enum carries it; grill-2 F-4c removed the imprecise row-3 citation, which is the folder-role-fields row). A cache hit never dials, and the record says so.
4. **Given** the fake server's accepted-connection counter, **When** N operations dial, **Then** the sum of the records' socket acquisitions equals the server's counter delta — the instrument agrees with an independent counter, not only with itself.
5. **Given** an artificial delay injected at the fake server, **When** the operation completes, **Then** the recorded duration reflects the injected delay (the field measures the operation, not a constant).
6. **Given** instrumentation disabled or a sink absent, **When** operations run, **Then** no records are produced — and a measurement run for an exercised operation that yields zero records is an **invalid run** (§8.3), never a pass.

### US-P2 — The pooled runtime is proven under stress, not asserted (P0)

The design's hardest corrections — read coalescing scoped by pair and generation (I-01), publication revision ordering (I-02), exclusive leases, poisoned-socket retirement, reservation release, the eight-socket ceiling with a typed busy result, and no leaks after panel close, logout or disconnect — are exactly the places a careless implementation passes every happy-path test. This package's runtime tests must be able to fail on each.

**Why this priority**: cross-pair data leakage and stale-cache resurrection are the two failure modes with user-visible data-integrity consequences; the design's own risk table names them first.

**Independent test**: drive `startMemIMAP` (plus its scripted fault extensions, §6.2) with concurrent panel/watcher/tool work across multiple pairs and generations; assert caps, isolation, retirement, release and publication ordering from server counters and manager state — never from returned rows alone (B-P3…B-P10).

**Acceptance scenarios**:

1. **Given** simultaneous panel, watcher and agent reads across 13 pairs on the fake server, **When** all work runs, **Then** concurrent established sockets never exceed 2 per mailbox and 8 globally, counting connecting reservations as well as established sockets.
2. **Given** all eight global leases active and a ninth request arriving, **When** the bounded acquisition wait (5 s, inside the 45 s read budget) elapses, **Then** the ninth request receives a typed busy result (`pool_busy`) — no ninth socket is dialed (server counter proves it).
3. **Given** two borrowers reading different folders concurrently while the server pauses one command mid-flight, **When** both complete, **Then** each result contains only its own folder's marker messages — SELECT state belongs to the lease, never to another simultaneous request — and PEEK reads preserve `\Seen`.
4. **Given** a socket that suffers a timeout, a cancelled command, a server BYE or a protocol error, **When** the failure is handled, **Then** that socket is closed and retired — it never re-enters idle reuse; the next operation dials fresh (server login counter increments).
5. **Given** the last panel observer closing (close frame, browser disconnect without a frame, logout, workspace exit), **When** cleanup runs, **Then** zero panel-retained sockets remain, an independent watcher/tool request in flight is **not** cancelled, and repeated open/close cycles leave goroutine and socket counts at baseline.
6. **Given** a cancelled request that was the only waiter on a shared read, **When** cancellation completes, **Then** every reservation it held — global slot, account slot, mailbox lease — is released; a late completion of the detached flight publishes only under the current revision and never returns a socket to panel retention.
7. **Given** two pairs sharing one `host:port|username` account but configured with different Sent mappings, **When** both issue the same concurrent `list folder=sent` request, **Then** each receives only its own mapping's results (per-pair markers) while the account's two-slot work gate still bounds total concurrent dials at two (I-01).
8. **Given** an identical operation already in flight for a pair's old configuration generation, **When** the pair is reconfigured (generation bumps) and the same operation arrives, **Then** the new request never joins the old flight, the old flight's completion publishes nothing for the new generation, and one joiner cancelling mid-flight neither fails nor cancels the flight other waiters still depend on (I-01).
9. **Given** a pre-mutation read paused after its server snapshot, **When** a mark-read mutation and its post-mutation refresh complete, **Then** the released old read publishes nothing — no memory rows, no disk snapshot, no last-validated timestamp advance, no UI/count update — and the post-mutation refresh never joined the superseded flight (I-02).

### US-P3 — The caches are bounded, encrypted, invalidated and invisible to Git (P0)

Phase 1 ships an encrypted per-mailbox folder-metadata file on disk and bounded memory-only headers; Phase 2 adds the bounded encrypted header snapshot. Every boundary — envelope authentication, UIDVALIDITY invalidation, stale-vs-fresh labelling, the 50-header and 4 MiB bounds, the 30-minute post-close drop, and the data-folder Git/backup exclusion — must have a test that fails when the boundary is missing.

**Why this priority**: the cache is the change's main new stored-data surface; its failures are silent (stale presented as fresh) or permanent (sensitive names leaking into Git history).

**Independent test**: round-trip an envelope, corrupt it, flip epochs on the fake server, overflow the bounds with synthetic folders, and diff the data folder's Git status before/after a cache write (B-P11…B-P15).

**Acceptance scenarios**:

1. **Given** a resolved folder mapping, **When** it is written to the encrypted folder file and read back, **Then** roles, names and UIDVALIDITY are identical, the file on disk is ciphertext (marker scan finds no plaintext, positive control proves the scan works), and the envelope binds purpose, schema version, pair and config generation as authenticated data.
2. **Given** corrupt, truncated, foreign-key, wrong-pair or schema-mismatched ciphertext, **When** the cache reads it, **Then** it is rejected without plaintext salvage, a visible cache warning is surfaced, the live path is used, and no `last_validated` timestamp advances on the failure.
3. **Given** a UIDVALIDITY change on the server, **When** any read/mutation exercises an old reference, **Then** every old cursor and row is discarded and the old reference is refused with a typed stale-reference error **before** any fetch or mutation — including the recreated-folder case where the numeric UID now denotes a different message.
4. **Given** cached rows past the 5-minute threshold, **When** the panel displays them, **Then** they render labelled stale with a refresh indicator, never as fresh; a failed refresh preserves the labelled rows plus a visible error and does **not** reset the timestamp.
5. **Given** folders of 500 synthetic messages and metadata exceeding the 4 MiB budget, **When** the caches fill, **Then** at most 50 reusable headers per role are retained, the active view's older pages do not enlarge the cache, search runs live, and overflow produces a visible cache-unavailable outcome — never truncated fields or silently omitted rows.
6. **Given** the panel closed, **When** 30 minutes pass (fake clock), **Then** memory headers are dropped; reopening refetches (server fetch counter) — and a reopen before 30 minutes retains them.
7. **Given** a cache write, **When** the data folder's Git status, its version-control staging set (as governed by the product's own exclusion) and the application's backup archive are inspected, **Then** the cache file is untracked, unstaged and absent from the archive — with an ordinary allowed state file as positive control proving the inspection could have seen a write.

### US-P4 — The founder features are proven as journeys, not unit shapes (P0)

F1–F7 (Open-without-saving, Save-to-Library, agent attachment tools with the ask default, safe styling, Reply all, missing dates, paperclip indicator) each carry a founder decision. The proof is end-to-end: real files, real renderers, real tool outputs, real browser requests — with the counterexamples the grill demanded (I-03 reference journey, I-04 resource policy, I-05 byte-path split, I-06 handoff interaction).

**Why this priority**: these are the user-visible deliverables; a green unit test beside a broken journey is exactly the false-completion pattern this repo's Definition of Done exists to stop.

**Independent test**: each acceptance scenario below names its own observable end state — filesystem diff, request counter, landed file, recipient set, computed style, rendered date (B-P16…B-P22 plus the I-03/I-05/I-06 scenarios in §6.4).

**Acceptance scenarios**:

1. **Given** an attachment of every renderable kind (image, SVG, video, audio, PDF, markdown/code/text, restricted HTML), **When** the user Opens it and then goes Back/closes/navigates/reloads/logs out, **Then** the filesystem snapshot and the data folder's Git status are byte-identical before and after — Open writes nothing — while a positive-control Save of the same payload proves the instrument sees a write.
2. **Given** a mail Markdown attachment containing `![x]` image syntax pointing at (a) a same-origin Library download/API path, (b) a workspace embed target, and (c) a remote image, **When** the temporary viewer renders it, **Then** the request counter observes **zero** requests for all three targets; the viewer's own minted resources load; and the positive control — the same Markdown opened as an ordinary workspace Library file — proves the observer would have seen the requests (I-04).
3. **Given** a Save of an attachment with a hostile name (separators, control characters, dot names, Windows-invalid, case-colliding), **When** the save completes, **Then** the file lands only under the authorized `mail/<mailbox>/<UTC save-month>/` hierarchy with a sanitized, numbered-unique name, exact original bytes (Q5=A: original HTML bytes, scripts-off profile), and no path escape via symlink, mount or parent-file conflict; concurrent panel/agent saves never clobber.
4. **Given** an agent invoking `download_email_attachment`, **When** the tool runs under each permission mode (Auto off with approval granted/declined, global deny, per-agent ask/allow, Auto on, God Mode), **Then** the shipped global default `ask` governs, a declined approval performs zero transfer/write, Auto-on runs through the existing workspace-path conditional class with no attachment-specific prompt (Q4=A), and a per-agent `allow` can never loosen the global `ask`.
5. **Given** an original message From A, Reply-To R, To = self+X+duplicate R, Cc = Y + mixed-case X + self + display-name duplicate R, and a hidden Bcc, **When** Reply all is composed, **Then** To = R and Cc = X, Y once each — no self, no primary, no Bcc, no display-name duplicate — and plain Reply produces To = R with Cc/Bcc empty; the assertion reads the final send payload shape, not the displayed chips.
6. **Given** a styled HTML mail with safe colour/font/table/media-query styling plus scripts, handlers, forms, `@import`, remote `url()`, `position:fixed` and `expression()`, **When** it renders, **Then** computed-style assertions prove the safe styling survives, the unsafe constructs are gone, zero remote loads occur by default (controlled endpoints), Load-images fetches only token-scoped proxy resources after explicit consent, and a stripped-style fallback stays readable.
7. **Given** messages with (a) no Date header + valid internal date, (b) neither date, (c) a valid Date differing from received time, **When** list, detail, Sent and reply attribution render, **Then** the precedence Date → internal date → **No date** holds everywhere, absence never renders as year one, epoch or today, and list/detail agree.
8. **Given** a message whose Message-ID header is absent, **When** the agent chains `read_message` → `list_email_attachments` → `read_email_attachment` → `download_email_attachment` using **only** returned references, **Then** every step succeeds with no Message-ID and no synthesized identity; references from a wrong pair, an old configuration generation and a recreated folder (new UIDVALIDITY, old numeric UID) are each refused with the typed stale-reference error before any fetch or mutation (I-03).
9. **Given** a message whose MIME structure carries a genuine user attachment — with the siblings: a CID-only inline part, the dedicated draft-body marker, an unavailable attachment part, and unclassifiable structure metadata — **When** the list row and detail render, **Then** the paperclip indicator is derived from MIME structure metadata only: command capture proves no body bytes were fetched and `\Seen` is unchanged; a genuine `message.md` is `true`, CID-only and draft-marker parts are `false`, an unavailable part is still `true`, and unclassifiable metadata is a visible failure — never a fabricated `false` (F7; founder #1174 display half; the classifier is w2's — register row 15).

### US-P5 — The measurement campaign is executable and honest (P0)

The ADR's acceptance bars (Q3=A) are targets. Judging them requires the same 13 mailboxes, both clocks, cache-first display measured **separately** from live fetch, the six missing series, stated repetitions and an invalid-run rule — so a bad run is discarded and said so, never averaged into a pass.

**Why this priority**: the founder approved specific numeric bars; a measurement that cannot fail, or that merges clocks, cannot honour them.

**Independent test**: the harness produces per-sample receipts (both clocks, status, rows, n) and applies the invalid-run rule; a deliberately corrupted run (double request, browser-memory-served response) is discarded by the rule (B-P23, B-P24).

**Acceptance scenarios**:

1. **Given** the same 13 configured mailboxes from lane-M, **When** the candidate build is measured, **Then** every sample records both clocks — click→rendered (user clock) and request-start→response-end (request clock) — plus status, row count and sample count, with pair IDs opaque and no subjects/addresses/content in receipts.
2. **Given** the panel open with a warm cache, **When** a cached-first display is measured, **Then** its time is reported in its own column, never mixed with live-fetch times; the live refresh is a separate sample with its own clock.
3. **Given** the sampling rule (≥ 5 cold, ≥ 10 warm per applicable operation per pair, randomized pair order), **When** a series completes, **Then** the receipt reports n, median, p95, worst and all failures — an empty folder is *not applicable*, never 0 ms, and no missing sample is extrapolated.
4. **Given** any of the invalid-run conditions (§8.3) — reload mid-op, double request, response served from browser memory, a second actor on the mailbox, gateway restart, build mismatch — **When** it occurs, **Then** the run is discarded, re-run, and the receipt says so.
5. **Given** the completed series, **When** judged, **Then** each ADR bar is evaluated as stated: 5 s maximum acquisition wait inside the 45 s read budget; 4 MiB reusable metadata budget; cached display median ≤ 250 ms / p95 ≤ 500 ms; ≥ 50 % paired-median warm-live reduction where reuse is eligible; ≤ 10 % paired-median cold and body-open regression; summary p95 ≤ 1 s request-to-render with **zero** mail network calls; busy surfaced within the 5 s wait — with absolute bars named by clock and host, paired bars by exact percentile and opposing measurement.

### US-P6 — The UAT campaign proves the live instance without touching its data (P1)

Automated tests prove behaviour against the fake server and controlled cases; the live instance must show the real thing works there — reads, cache labels, preview, Save, agent tools — without sending, deleting, or changing keys on real mailboxes.

**Why this priority**: Definition of Done requires reachability by a real user on a real instance; but the instance is shared state — one careless send or key rotation would damage the founder's data and invalidate the measurement series.

**Independent test**: the campaign checklist executes on the live instance with the prohibitions enforced; every PASS row carries its named screenshot; an independent validator re-drives the critical rows (B-P25).

**Acceptance scenarios**:

1. **Given** the live instance and the 13 mailboxes, **When** the UAT rows execute, **Then** each claim (folder rail with counts, cached-first display with stale labelling, unknown-vs-absent folder states, busy/Retry surfaces, preview Open/Back, Save → Open in Library, paperclip indicator, Reply all composition without sending, No date rendering, agent list/read journey) has its named screenshot in the evidence pack.
2. **Given** the campaign rules, **When** the tester works, **Then** no send is completed, no message or folder is deleted, no credential/key change is made, no server folder is created or renamed, and no UIDVALIDITY-inducing operation is performed — compose may be opened for the Reply-all rows but never sent.
3. **Given** live mail content visible in screenshots, **When** the evidence pack is assembled, **Then** subjects and addresses are redacted before receipts leave the lane (the baseline receipt's privacy rule), and the redaction is noted per screenshot.

### US-P7 — CHECK proves the tests can fail: mutations (P0)

A test suite that cannot fail proves nothing (the repo's false-green rules; `docs/internal/false-green-patterns.md`). Before any green is trusted, the CHECK auditor runs a fixed mutation list — deliberate defects a careless implementation would survive — and each must kill its test.

**Why this priority**: the design's eight named hazards each have a "careless implementation survives" shape; the mutation list is the audit trail that the suite actually detects them.

**Independent test**: apply each mutation of §7 to the implementation, run its named test, confirm it fails, restore — one at a time, per the repo's one-narrow-run rule (B-P26).

**Acceptance scenarios**:

1. **Given** the mutation list applied one at a time to a green implementation, **When** each mutation's named test runs, **Then** the test fails — a mutation whose test still passes is a **CHECK BLOCK** naming both.
2. **Given** a test that failed once and passed on re-run, **When** the suite is judged, **Then** the retry-pass counts as red: the test is investigated to a mechanism (and a second isolated failure is a defect, not a flake).

---

## 4. Behavioral contract (quick reference)

### 4.1 When/Then summary

- When a mail operation completes, the system has one instrumentation record for it — success paths included.
- When a record is written, it carries no subject, address, folder name, message content, credential or raw upstream error text.
- When all leases are active and a ninth request arrives, the system answers with a typed busy result inside the 5 s acquisition wait and dials nothing.
- When a socket fails (timeout, cancel, BYE, protocol error), the system retires it — it is never reused.
- When the last panel observer leaves, the system releases every panel socket and leaves independent work untouched.
- When two pairs share an account or a generation changes, the system never shares a read result across the boundary.
- When a read is superseded by a mutation or invalidation, the system publishes nothing from it.
- When ciphertext is corrupt, foreign or oversized, the system rejects it, warns visibly, and never salvages plaintext or advances a freshness timestamp.
- When UIDVALIDITY changes, the system refuses every old reference before any fetch or mutation.
- When a cached row is stale, the system labels it stale and keeps the label through a failed refresh.
- When Open displays an attachment, the system writes nothing to disk.
- When a temporary mail source renders, sender-authored markup can reach only that source's own authorized resources.
- When Save runs, the file lands in the authorized hierarchy under a sanitized unique name — or a visible refusal with a safe reason.
- When an agent save runs, the ordinary `ask` default and standard Auto rules govern — no extra mechanism.
- When Reply all composes, the recipient set is exactly To = primary, Cc = original To+Cc minus self/primary, deduplicated — never Bcc.
- When a message has no usable date, the system shows **No date** — never year one, epoch or today.
- When a measurement run violates its conditions, the system's receipts discard it as invalid.
- When a mutation from the CHECK list is applied, the matching test fails.

### 4.2 Explicit non-behaviors (the proof package must not…)

1. …write production code, contract files, or any file outside its own test pack and this spec (dispatch restriction; W0 owns contracts, W1–W10 own production).
2. …weaken, skip or delete an existing test to get green — the three `view_missing_folder` tests and every currently passing suite are regression fences (§6.7).
3. …accept a timing number from the gateway's historical log: it times nothing (§2.1); only instrument records and browser-network clocks are evidence.
4. …merge the click clock with the request clock, or cached-display times with live-fetch times, in any receipt, table or bar.
5. …report a measurement pass from a run that violated §8.3's conditions, or extrapolate a missing sample (empty folder = not applicable).
6. …run the full Go test suite locally, or two local test processes at once — CI is the authority for Go results (repo rule 2; the OOM history behind it).
7. …introduce test hooks, flags or globals into production code to make a test pass — instrumentation is production code with a real purpose, never a test-only branch.
8. …let the UAT campaign send, delete, rename server folders, or change credentials/keys on the live instance (US-P6 AC-2).
9. …treat a test that passed on retry as a pass, or a twice-failing test as a flake (§10).
10. …claim reachability from registration/policy greps alone — a screen or component that renders the feature and an executed journey are required (repo Definition of Done).

### 4.3 Machine-verifiable constraints

Each MC-P is testable as stated; the right-hand column names the primary proof.

| ID | Constraint | Primary proof |
|---|---|---|
| MC-P1 | Every mail operation emits exactly one record with fields `operation` (closed enum), `pair_ref` (opaque pair id + config generation), `source` (`live|memory|encrypted_disk|none`), `hit` (bool), `duration_ms` (int > 0 on completion), `acquire_wait_ms` (int), `socket_count` (int), `outcome` (`ok` or safe class), optional `rows` (int), optional `revision` (opaque publication-revision id) and optional `shared_flight` (bool — set by a coalesced joiner, which records `socket_count=0`; register row 17's joiner rule) | P-inst-1…P-inst-3 |
| MC-P2 | No record field contains a message subject, an email address, a folder name, a Message-ID, a credential value, a full URL or a raw upstream error string — proven with distinctive synthetic markers plus a positive control. The production counterparts of the same no-raw-error rule are w1's `recordFailure` redaction and w5-integration's `mailErr502` redaction (register row 19) | P-inst-2 |
| MC-P3 | A cache-first hit record shows `hit=true`, `socket_count=0`, and its `source` mirrors the contract's scoping: `memory` for Phase 1 header/list hits; `encrypted_disk` only on Phase 1 folder-mapping hits and Phase 2 header hits — a Phase 1 header producer never emits `encrypted_disk` (register row 2; the landed `MailReadMetadata` `source` enum states this scoping verbatim in its description); a live record shows `hit=false`, `source=live`, `socket_count≥1` | P-inst-3 |
| MC-P4 | Sum of records' socket acquisitions equals the fake server's accepted-connection counter delta over the same window, with coalescing accounted by the joiner rule: a joiner records `socket_count=0` plus `shared_flight=true` and the flight owner's record carries the socket — each dial counted exactly once (register row 17; grill-4 I1) | P-inst-4 |
| MC-P5 | Concurrent established sockets ≤ 2 per mailbox and ≤ 8 globally, counting connecting reservations; a 9th demand yields typed busy (reason `pool_busy`) within ≤ 5 s acquisition wait and zero extra dials | P-rt-1, P-rt-2 |
| MC-P6 | A retired socket is never reused: after poison, the next operation's record shows a fresh dial (server login count +1); reader goroutines terminate (bounded goroutine trend over 20 cycles) | P-rt-3 |
| MC-P7 | Last observer exit (close, disconnect, logout) leaves 0 panel-retained sockets; in-flight watcher/tool work completes; 20 open/close cycles return socket and goroutine counts to baseline | P-rt-4 |
| MC-P8 | Cancelling a request releases its global reservation, account slot and mailbox lease before returning; a detached completion never restores panel retention | P-rt-5 |
| MC-P9 | Two concurrent reads of different folders on one mailbox never observe each other's SELECT state; PEEK preserves `\Seen`; no eviction/release performs EXPUNGE | P-rt-6 |
| MC-P10 | The read-sharing identity is (pair, config/folder-mapping generation, operation, normalized arguments, live/cache purpose); the account key decides contention only. Same-account/different-mapping and old-generation/new-generation concurrency produce separate results; account dials stay ≤ 2 | P-rt-7, P-rt-8 |
| MC-P11 | A read captured under an older publication revision publishes nothing on completion — memory rows, disk snapshot, timestamps, UI state and counts unchanged; a post-mutation refresh never joins a superseded flight; a delayed frontend response carrying an older `publication_revision` is dropped | P-rt-9 |
| MC-P12 | Cache envelope: AES-256-GCM with fresh random nonce per write, keys only via `Store.DeriveSubkey` purpose separation, AAD = purpose + schema version + pair + config generation (+ transport in Phase 2); ciphertext-only atomic replacement; oversized/malformed envelopes rejected before unbounded allocation | P-ca-1, P-ca-2 |
| MC-P13 | Corrupt/foreign/truncated ciphertext → visible cache warning + live path + no timestamp advance + no plaintext salvage; a marker scan with positive control proves no plaintext persisted | P-ca-2 |
| MC-P14 | UIDVALIDITY change discards every old cursor/row/ref; any old-ref exercise (read, mark-seen, attachment) is refused with the typed stale-reference error on the same selected lease that would have acted — before fetch or mutation | P-ca-3 |
| MC-P15 | Stale rows render labelled stale with a refresh indicator; a failed refresh preserves them plus a visible error and never resets `last_validated` | P-ca-4 |
| MC-P16 | Reusable headers ≤ 50 per role (150/mailbox); active-view pages never enlarge the cache; search runs live — folder search matches subject plus sender/recipient substring server-side, within the 25/200 bounds (founder Q-D=A); 4 MiB global reusable-metadata overflow → visible cache-unavailable outcome, never truncation or silent omission | P-ca-5 |
| MC-P17 | Memory headers drop 30 minutes after the panel was last open (fake clock); a reopen inside 30 minutes retains them; drop issues zero IMAP commands | P-ca-6 |
| MC-P18 | The product owns the exclusion by its own means on every install (founder ruling 2026-10-02): the Mail cache directory is excluded from data-directory version-control staging AND from the application's own backups/archives; w5-integration publishes the gate decision and w2 enforces it at the first write, checked with `git check-ignore`-equivalent semantics evaluated in-process (never shelling out on this security path), failing closed to live-only with the visible `cache_unavailable` notice when exclusion cannot be proven — the notice excuses nothing. A cache write therefore leaves the data folder's `git status --porcelain` unchanged and appears in no backup archive member; the watcher state files (`email-watch/`) carry the same exclusion AND are purged on mailbox removal (founder Q-B=A); an allowed state file is the positive control. **The ADR's E-Backup row ("the implementing lead must trace the real job before enabling disk writes") and its work-package "release blocker" line predate the founder's Q-A ruling (2026-10-02) and are superseded by register row 18 and this constraint** — the exclusion gate is the product's own mechanism with no external deployment dependency; the ADR's dated correction is routed to the architect (grill-2 F-2). Read the register and this constraint for the gate, never the stale ADR row | P-ca-7 |
| MC-P19 | Open (temporary preview) performs zero attachment-file, directory, spool or persistent-byte writes — filesystem snapshot + Git status identical before/after — while a positive-control Save shows a write; responses carry `Cache-Control: no-store`; token/preview/inline caches hold no body or part bytes (today's 15-minute `MailPreviewTokenTTL` body retention must be gone) | P-ft-1 |
| MC-P20 | The temporary mail source's renderer resource policy refuses every sender-authored reference except the source's own minted resources and consent-gated token-scoped proxy images — same-origin Library/API paths, workspace embeds and remote URLs each produce zero requests, with the workspace-file positive control proving the observer | P-ft-2 |
| MC-P21 | Save lands only under `mail/<mailbox-label>/<UTC save-month>/` with the sanitized unique name (existing sanitizer + Library validation + `CreateUnique` numbering, final candidate validated); refusals (parent-file conflict, symlink/mount escape, disk full, vanished part) are visible with safe reasons; exact bytes saved | P-ft-3 |
| MC-P22 | Agent save tools ride the two-layer policy: shipped ceiling literal `list_email_attachments: allow`, `read_email_attachment: allow`, `download_email_attachment: ask`; `ReconcileToolPolicyCeiling` adds missing keys without overwriting operator values; effective policy per role grants list/read as that role's `read_message` and download `ask`; Auto-on uses the existing workspace-path conditional class (Q4=A); declined/failed approval → zero transfer and zero write | P-ft-4 |
| MC-P23 | Reply-all recipient set from the fixture (From A, Reply-To R, To = self+X+dup-R, Cc = Y+mixed-case-X+self+display-name-dup-R, hidden Bcc) is exactly To=[R], Cc=[X, Y]; plain Reply To=[R], Cc/Bcc empty; asserted on the final send payload shape | P-ft-5 |
| MC-P24 | Styled mail: computed style keeps safe colour/font/table/media-query presentation; scripts, event handlers, forms, `@import`, remote `url()`, `position:fixed`/`absolute`, `behavior`/`expression()` produce zero effects and zero default remote loads; Load-images loads only token-scoped proxy resources after consent; stripped fallback readable with one plain notice | P-ft-6 |
| MC-P25 | Date precedence Date → internal date → null renders as **No date** in list, detail, Sent and reply attribution; never year one, epoch, today or a list/detail discrepancy | P-ft-7 |
| MC-P26 | The agent reference journey (no Message-ID anywhere) succeeds end-to-end from returned references; wrong-pair, old-generation and old-epoch references each refuse with the typed stale-reference error (landed schema `MailStaleReferenceError`, 409) before fetch/mutation | P-ft-8 |
| MC-P27 | Every measurement sample carries both clocks (click→rendered, request-start→response-end), status, rows, n; cached-display and live-fetch columns never mix; invalid-run conditions (§8.3) discard and re-run | P-ms-1…P-ms-3 |
| MC-P28 | Bars judged as stated (Q3=A): 5 s max acquisition inside 45 s read budget; 4 MiB metadata budget; cached display median ≤ 250 ms / p95 ≤ 500 ms; warm-live ≥ 50 % paired-median reduction where eligible; cold ≤ 10 % and body-open ≤ 10 % paired-median regression; summary p95 ≤ 1 s request-to-render with zero mail calls; busy within 5 s | P-ms-4, §8.4 |
| MC-P29 | UAT live-instance rows carry their named screenshots; prohibitions (no send, no delete, no key change, no server folder create/rename, no epoch-inducing op) hold; subjects/addresses redacted in receipts | P-uw-1, P-uw-2 |
| MC-P30 | Every mutation in §7, applied alone to a green implementation, makes its named test fail; a surviving mutation is a CHECK BLOCK | P-ck-1 |
| MC-P31 | `has_attachments` is derived from MIME structure metadata only: command capture proves no body bytes fetched and no `\Seen` change; a genuine user `message.md` yields true; CID-only inline and the dedicated draft-marker part yield false; an unavailable part still yields true; unclassifiable metadata is a visible failure, never a fabricated false (F7; the classifier's single publisher is w2 — register row 15) | P-ft-9 |

### 4.4 Integration boundaries

| External system / producer (single publisher per the landing-order register §2) | What this package consumes | Failure behaviour when unavailable |
|---|---|---|
| **W0** — the contracts wave: backend-lead in the ADR-W0 role, a role not a spec file (register row 1, §5) — **landed** (commit `5f23ae8a0`; `verify-contracts` exit 0, regeneration drift-free, no hand-written wire type): `MailReadMetadata` (nullable `last_validated_at`, closed `notice_code` enum with `cache_unavailable`, nullable `publication_revision`, the four-value `source` enum with its Phase-1 scoping — row 2), `mode=cache_first\|live`, `MailFolder` folder-role fields `availability` (`present\|absent\|unknown`), nullable `uidvalidity`, five-value `mapping_source` incl. `saved` (row 3) — **`total` landed non-nullable**: the register row 3 nullable form is the wave's disclosed deferral, scheduled for amendment with the Wave C/D consumer PRs (contracts check F2; w2's unknown-count test M-α10 stays red until it lands), the REST `observer_id` query param on folder/list reads (row 6), presence frames `mail_panel_observer` open/close + ack/error and `WsFrameType` entries (row 5), `MailUnavailableError` reasons `pool_busy\|account_busy\|server_connection_limit\|backoff` (row 7), paging/search/stale-409 shapes `MailMessagePage` (`next_cursor`/`has_more`/`view_limit_reached`) + the `search` param + typed 409 `MailStaleReferenceError` (row 4 — the shapes; w2 implements the search itself), and the feature wire shapes: `has_attachments` + `message_ref` on `MailMessageSummary`/`MailMessage`, `date` on both — **landed non-nullable** (the No-date nullable form is the same disclosed deferral, contracts check F3; T32's null-shape assertion stays red until it lands), `MailAttachmentPreviewRequest/Response` with the dedicated preview byte endpoint (`MailAttachmentContentSource.byte_url`), `MailAttachmentSaveRequest/Response` with required `save_operation_token`, `MailReplyContextRequest/Response`, `LibraryEntry.preview_profile` + per-file `preview_scripts_allowed` (row 8). **Tool-result schemas are not landed** — the wave reported rather than invented them, pending an architect decision on admitting concrete mail tool-result shapes to the `ToolCallResultFrame.result` oneOf (§15 Q-P7; no §6 test binds such a field) | Tests bind to generated types only — never hand-written parallels | The wave has landed: wire-asserting tests now bind to the generated types. The two disclosed nullability deferrals and the tool-result half keep their dependent assertions red until the scheduled amendment lands — that is the correct RED state, not a workaround trigger |
| **w1** (register rows 9, 10, 11, 13, 16, 17, 19): pool/budget manager interfaces (its §3.1 freeze), the `RevisionSource` capture/compare accessor, the same-lease epoch/generation validation capability, the transport-agnostic presence registry `PanelPresence` in `pool.go`, the watcher dirty-mark signal, the pool sub-fields of the instrument record, and the `recordFailure` raw-error redaction inside its own exclusive file | The injected manager seams the runtime tests drive; the no-raw-error rule MC-P2 asserts | Tests written against the frozen interface shapes from §16's PUBLISHES/CONSUMES exchange; interface drift = coordination finding, not a test edit |
| **w2** (register rows 4, 14, 15, 16, 17, 18, 21): the single-part reader (`pkg/email/attachment_parts.go`), the MIME structure classifier + `has_attachments` derivation (one MIME walker, no second), IMAP SEARCH + cursor issuance in `view.go` (row 4's split), the I-03 normative validation FRs in `view.go`, the counts-freshness rule (refresh when absent or > 5 min, on the four triggers — row 21), the cache envelope + `Store.DeriveSubkey` usage, `HeaderCache.Put`/`FolderSnapshotStore.Save` taking w1's captured revision value, first-write enforcement of the exclusion gate, the cache sub-fields of the instrument record | Envelope round-trip/corruption tests; the paperclip test (T36); the search-live assertions | Same red-until-landed rule; w2's correction rows land in Wave A, its code in Wave C |
| **w5-integration** = ADR-W4 (register rows 5, 6, 10, 11, 12, 16, 17, 18, 19, 23) — the single gateway Mail-route writer: gateway wiring incl. the REST `observer_id` request + presence handlers, teardown binding to w1's registry, the revision counter + advancing events, `message_ref` issuance (minting) in the gateway handlers, publication of the exclusion-gate decision in the unified stricter condition (staging-exclusion AND backup-skip, `git check-ignore`-equivalent, in-process, fail-closed to `cache_unavailable`), the `mailErr502` raw-error redaction, the request-scoped instrument envelope, the generation/pair-ID construction (register row 12), removal cascades incl. `email-watch/` exclude + purge (founder Q-B=A), the Windows evidence workflow extension (row 23) | Endpoint tests, exclusion-gate tests, warm-pool measurement arms | The exclusion test stays red until the gate decision and w2's first-write enforcement land — that red gates disk-cache activation (landing blocker, not a skipped test); no operator machine setup is part of the gate |
| **w4-features** = ADR-W7–W10 (register rows 8, 22 — those rows define the wire shapes and the token reconciliation; the save service per the ADR's feature-extension assignment, and `BuildReplyRecipients` + the sanitizer property/value table per register R-4's one-implementation rule, which names them in prose only — no register row of their own, grill-2 F-4b): the save service (`Transfer`), the save-operation-token bounded lookup + prior-receipt reconciliation, sanitized naming, tool adapters/catalog/inventory/defaults, `BuildReplyRecipients`, the sanitizer property/value table — consume-only against w5-integration's gateway files | Feature journey tests (T28…T32) | Feature tests red until each lands; sequenced per the ADR (date/reply wins first, then attachments, CSS after security review) |
| **w3** (register row 20): SPA presence lifecycle hooks (w3 publishes — row 20: open with panel, close on unmount/pagehide/logout/socket death, fresh `observer_id` per panel instance); the viewer source union, the resource-policy pass-through and the handoff focus/announcement contract have no register row naming a single publisher — their publisher is named in w3's own spec (§3.1/US-9; w4's US-1/US-8 name the feature side). Grill-2 F-4a: the label's former stray second package letter named no file in the register's vocabulary (w1…w6 + W0) | E2E journeys (T35, T37, T38) | Presence frames have landed (commit `5f23ae8a0`); the rest red-until-landed per w3's wave |
| Fake IMAP server + its fault-injection extensions (test code only) | Every runtime/cache test | A harness fault that cannot be injected becomes a coordination question — never a production hook |
| The live instance + the 13 mailboxes | Measurement arms and UAT rows | An unavailable pair is reported *not applicable*/unavailable — never silently replaced with a convenient provider (ADR measurement rule) |

---

## 5. BDD scenarios

Every scenario carries its category (Happy / Alternate / Error / Edge) and traces to a US-Pn acceptance criterion. Fixtures are synthetic throughout — marker subjects/addresses/folder names exist so leak scans have something to find.

#### Scenario B-P1: A successful message open emits a timing record
**Traces to**: US-P1 AC-1 · **Category**: Happy Path
- **Given** the instrument wired with a recording sink and `startMemIMAP` holding ≥ 1 message
- **When** the panel opens that message end-to-end
- **Then** exactly one record exists with `operation=open`, `source=live`, `hit=false`, `duration_ms > 0`, `socket_count ≥ 1`, `outcome=ok` — on the success path that today logs nothing
- **And** the same holds for folders, list, summary, discovery and attachment reads (each its own record, correct operation label)

#### Scenario B-P2: The record cannot leak mail content
**Traces to**: US-P1 AC-2 · **Category**: Error Path
- **Given** synthetic mail whose subject, sender address and folder name are distinctive marker strings, and a record-scan harness that finds markers when present (positive control on the mail data itself)
- **When** any operation completes and its record is captured
- **Then** zero marker strings appear in any record field — and the positive control proves the scan could have found them

#### Scenario B-P3: The ninth socket is refused with a typed busy result
**Traces to**: US-P2 AC-2 · **Category**: Error Path
- **Given** all eight global leases held active by stalled (never-completing) fake-server responses — the cap counts connecting reservations as active, so the eight may include a connecting reservation (e.g. seven established + one connecting); the state is exactly **eight counted actives** before the next demand (grill-4 I2 arithmetic fix)
- **When** a ninth read arrives
- **Then** it waits at most the 5 s acquisition wait and receives the typed busy result (`pool_busy`) — and the fake server's connection counter shows **no ninth dial**

#### Scenario B-P4: Two simultaneous different-folder reads stay isolated
**Traces to**: US-P2 AC-3 · **Category**: Alternate Path
- **Given** one mailbox whose server pauses borrower A's command mid-flight, while borrower B selects a different folder
- **When** both complete
- **Then** A's results contain only A's folder markers, B's only B's — neither fetch observes the other's SELECT state
- **And** the fetches were PEEK: `\Seen` is unchanged server-side, and no release/eviction performed EXPUNGE

#### Scenario B-P5: A poisoned socket is retired, never reused
**Traces to**: US-P2 AC-4 · **Category**: Error Path
- **Given** a socket that receives a server BYE (siblings: command timeout, cancelled command, protocol error)
- **When** the failure is handled and the next operation runs
- **Then** the poisoned connection was closed, the next operation dialed fresh (server login count +1), and 20 subsequent cycles show no reuse of the retired session and a bounded goroutine trend

#### Scenario B-P6: Panel close releases sockets; independent work survives
**Traces to**: US-P2 AC-5 · **Category**: Alternate Path
- **Given** an open panel with warm sockets and an in-flight watcher cycle plus an agent tool read
- **When** the last panel observer leaves (each variant: close frame, browser disconnect without a frame, logout, workspace exit)
- **Then** panel-retained sockets return to zero, the watcher/tool work completes normally, and a detached shared flight never returns its socket to panel retention
- **But** an independent tool request is never cancelled merely because the UI disappeared

#### Scenario B-P7: A cancelled request releases every reservation
**Traces to**: US-P2 AC-6 · **Category**: Alternate Path
- **Given** a read in its acquisition queue holding a global reservation, an account slot and a mailbox lease, with no other waiter
- **When** its context is cancelled
- **Then** all three reservations return to baseline before the call returns
- **And** if the detached flight later completes, it publishes only under the current revision and restores no panel retention

#### Scenario B-P8: Same account, different mapping — no shared result
**Traces to**: US-P2 AC-7 · **Category**: Error Path
- **Given** two pairs on one `host:port|username` account with different configured Sent mappings, both issuing the same concurrent `list folder=sent`
- **When** both complete
- **Then** each result carries only its own mapping's marker messages — the second pair never receives the first's Sent mapping
- **And** total concurrent dials across both pairs never exceeded the account's two-slot gate

#### Scenario B-P9: Old generation never joins or feeds a new one
**Traces to**: US-P2 AC-8 · **Category**: Error Path
- **Given** an identical operation in flight for a pair's old configuration generation, the pair then reconfigured (generation bumped), the same operation issued again, and one joiner of the old flight cancelled mid-flight
- **When** both flights finish
- **Then** the new-generation request never joined the old flight and never received its data; the old flight's completion published nothing for the new generation; the joiner's cancellation neither failed nor cancelled the flight other waiters still depend on

#### Scenario B-P10: A superseded read publishes nothing
**Traces to**: US-P2 AC-9 · **Category**: Error Path
- **Given** a pre-mutation read paused (fake-server hold) after its server snapshot
- **When** a mark-read mutation completes and its post-mutation refresh finishes, then the paused read is released
- **Then** the released read publishes nothing — memory rows unchanged, no disk snapshot write, no `last_validated` advance, no UI/count update — and the post-mutation refresh never joined the superseded flight
- **And** sibling cases hold: a cache-first read overtaken by the mutation; UIDVALIDITY changing mid-read; a delayed frontend response carrying an older `publication_revision` arriving after a newer one is dropped
- **But** no new scheduled refresh timer was introduced (the revision advances only on the listed events)

#### Scenario B-P11: Envelope round-trips; corruption is rejected without salvage
**Traces to**: US-P3 AC-1, AC-2 · **Category**: Happy Path (round-trip) / Error Path (corruption)
- **Given** a resolved folder mapping written through the cache envelope
- **When** it is read back, then read again as (a) bit-flipped, (b) truncated, (c) foreign-purpose-key, (d) wrong-pair-AAD and (e) schema-mismatched ciphertext
- **Then** the round-trip is byte-faithful (roles, names, UIDVALIDITY); each corruption is rejected with a visible cache warning, the live path serves the request, no plaintext is salvaged, and no `last_validated` timestamp advances on any failure
- **And** the on-disk file is ciphertext only (marker scan with positive control), atomically replaced, with a fresh nonce per write

#### Scenario B-P12: A UIDVALIDITY change discards every old cursor and row
**Traces to**: US-P3 AC-3 · **Category**: Error Path
- **Given** cached rows, cursors and issued references for a folder whose epoch then changes on the fake server (folder recreated; a numeric UID reused for a different message)
- **When** any old reference is exercised — read, mark-seen or attachment access
- **Then** every old cursor/row is discarded, the old reference is refused with the typed stale-reference error **before** any fetch or mutation, and the refusal is checked on the same selected lease that would have acted
- **But** a fresh reference issued after the change works normally

#### Scenario B-P13: A stale row is never presented as fresh
**Traces to**: US-P3 AC-4 · **Category**: Edge Case
- **Given** cached rows older than the 5-minute threshold
- **When** the panel displays them and the single live refresh then fails
- **Then** the rows render labelled stale with a refresh indicator, the error and Retry stay visible, and `last_validated` is not reset — the stale label survives the failed refresh

#### Scenario B-P14: The bounds hold — 50 headers, 4 MiB, 30 minutes
**Traces to**: US-P3 AC-5, AC-6 · **Category**: Edge Case
- **Given** synthetic folders of 500 messages and metadata sized past the 4 MiB global budget
- **When** the caches fill and the panel closes for 30 fake-clock minutes
- **Then** at most 50 reusable headers per role are retained, active-view older pages did not enlarge the cache, search ran live, 4 MiB overflow produced a visible cache-unavailable outcome (no truncated fields, no silently omitted rows), and the memory headers were dropped after exactly the 30-minute threshold (a reopen inside 30 minutes retained them; the drop issued zero IMAP commands)

#### Scenario B-P15: A cache write is invisible to Git and backups — by the product's own exclusion
**Traces to**: US-P3 AC-7 · **Category**: Edge Case
- **Given** the product's own exclusion in force on a scratch data directory: w5-integration's published gate decision and w2's first-write enforcement (register row 18) — the Mail cache directory and, per founder Q-B=A, the watcher `email-watch/` state directory excluded from data-directory version-control staging AND from the application's own backup/archive walk, the exclusion proven with `git check-ignore`-equivalent semantics evaluated in-process before the first write
- **When** a cache write lands, and again when a watcher cycle persists state
- **Then** `git status --porcelain` is unchanged, nothing is staged, and no archive member contains the cache or watcher-state file — while the positive control (an ordinary allowed state file) **is** staged and archived, proving the inspection could see a write
- **And** when the exclusion cannot be proven (simulated: the product's own ignore write absent or removed), the first write is refused — the cache stays live-only with the visible `cache_unavailable` notice, and that refusal is itself asserted; the notice excuses nothing (founder ruling 2026-10-02: the product owns the exclusion on every install, by its own means — no operator machine setup of any kind, on any machine, is a dependency of this feature)
- **And** purging `email-watch/` state on mailbox removal is asserted (founder Q-B=A)
- **And** until w5-integration's gate decision and w2's first-write enforcement land (register row 18), this test stays red — that red gates disk-cache activation

#### Scenario B-P16: Open writes nothing to disk
**Traces to**: US-P4 AC-1 · **Category**: Happy Path
- **Given** attachments of every renderable kind and a filesystem snapshot plus data-folder Git status taken before
- **When** each is Opened and then dismissed by Back / Close / navigation / reload / logout — including a close mid-stream and a mid-preview mailbox removal
- **Then** the snapshot and Git status are byte-identical after — zero attachment-file, directory, spool or persistent-byte writes; object URLs are revoked; the token is dead after exit; reopening performs a fresh authorized fetch
- **And** the positive control (a Save of the same payload) produces exactly one visible write, proving the instrument sees writes

#### Scenario B-P17: The temporary source cannot load a same-origin workspace resource
**Traces to**: US-P4 AC-2 · **Category**: Error Path
- **Given** a mail Markdown attachment containing `![x]` pointing at (a) a same-origin Library download/API path, (b) a workspace embed target, (c) a remote image — and a request counter observing every image/embed/network request
- **When** the temporary viewer renders it
- **Then** the counter records **zero** requests for all three targets; the viewer's own minted resources do load; a consented Load-images case loads only through the token-scoped proxy
- **And** the positive control — the same Markdown opened as an ordinary workspace Library file — shows the counter firing, proving the observer works
- **But** ordinary workspace rendering of the same file is unchanged
- **And** the mail-restricted resource policy scopes the **temporary preview only**: after Save the file is an ordinary workspace file under ordinary workspace rules (founder Q-E=A); saved HTML keeps the already-decided scripts-off default with the per-file scripts checkbox (Q5=A)

#### Scenario B-P18: Save lands confined, sanitized and unique
**Traces to**: US-P4 AC-3 · **Category**: Happy Path / Error Path
- **Given** attachments named with separators, control characters, dot names, Windows-invalid names and an existing case-folded collision; plus escape attempts (symlink, mount redirect, parent-file conflict) and a concurrent panel+agent save of the same name
- **When** each Save runs
- **Then** every landed file sits under the authorized `mail/<mailbox-label>/<UTC save-month>/` hierarchy with the sanitized, numbered-unique name and exact original bytes; the collision became `name (1).ext`; the concurrent saves produced two distinct files; every escape attempt was refused with a visible safe reason and zero partial files
- **And** a positive control asserts exact bytes for a benign payload (except an explicitly disclosed Q5=A HTML case, which keeps original bytes with the scripts-off profile)

#### Scenario B-P19: The agent save follows the ask default with no extra mechanism
**Traces to**: US-P4 AC-4 · **Category**: Alternate Path
- **Given** an agent invoking `download_email_attachment` under each mode: Auto off (approval granted / declined), global deny, per-agent deny/ask/allow, Auto on, God Mode — and an old config missing the new keys
- **When** the tool runs
- **Then** the shipped ceiling is literal allow/allow/**ask**; a declined approval performed zero transfer and zero write; Auto-on ran through the existing workspace-path conditional class with no attachment-specific prompt; per-agent `allow` did not loosen the global `ask`; reconciliation added the missing keys without overwriting operator values
- **And** the saved result returned the actual workspace file's absolute path, readable by the agent's normal file tools

#### Scenario B-P20: The Reply-all recipient set is exact
**Traces to**: US-P4 AC-5 · **Category**: Happy Path
- **Given** the fixture message (From A, Reply-To R, To = self+X+duplicate R, Cc = Y + mixed-case X + self + display-name duplicate R, hidden Bcc)
- **When** Reply all is composed (and separately plain Reply)
- **Then** the final send payload shape carries To=[R], Cc=[X, Y] — no self, no primary duplicate, no Bcc, no display-name duplicate — and plain Reply carries To=[R], Cc/Bcc empty
- **And** the quoted original is editable, escaped text with attribution; editing or deleting the quote and sending/cancelling leaves the recipient rule intact; a stale context response cannot overwrite a different message's compose input

#### Scenario B-P21: Styled mail keeps colour and loses every remote load
**Traces to**: US-P4 AC-6 · **Category**: Alternate Path
- **Given** an HTML mail with safe colour/font/table/media-query styling and scripts, handlers, forms, `@import`, remote `url()`, `position:fixed`, `expression()` — plus controlled external and same-origin API endpoints, and an uncontained positive-control page
- **When** it renders in the isolated mail view
- **Then** computed-style assertions confirm the safe styling survives (including a media-query layout change); the unsafe constructs produced zero effects; the endpoints recorded zero default loads; Load-images fetched only token-scoped proxy resources after explicit consent; the stripped-style fallback stayed readable with one plain notice
- **But** the positive-control page proves the observers detect loads when they occur

#### Scenario B-P22: A missing date is never year one
**Traces to**: US-P4 AC-7 · **Category**: Edge Case
- **Given** messages with (a) no Date + valid internal date, (b) neither date, (c) a valid Date differing from received time, (d) an unparsable/zero Date
- **When** list, detail, Sent and reply attribution render
- **Then** precedence holds (Date → internal date → **No date**), absence renders **No date** everywhere (never year one, epoch or today), and list/detail agree for the same message

#### Scenario B-P23: Cached display and live fetch are measured separately
**Traces to**: US-P5 AC-2, AC-1 · **Category**: Happy Path
- **Given** the measurement harness on the candidate build with a warm cache
- **When** a cached-first display and its follow-up live refresh are both exercised
- **Then** the receipt carries two separate samples — the cache hit with its display-time column and the live refresh with its own request-clock sample — never merged, each with both clocks, status, rows and n

#### Scenario B-P24: An invalid measurement run is discarded and re-run
**Traces to**: US-P5 AC-3, AC-4 · **Category**: Error Path
- **Given** a series in progress when an invalid-run condition fires (each tested: reload mid-op; a second request to the same URL; a response served from browser memory — request verified absent from the network log; a second actor on the mailbox; a gateway restart; a build mismatch)
- **When** the harness evaluates the series
- **Then** the affected run is discarded and re-run, and the receipt states the discard — no invalid sample reaches any median, percentile or bar judgement
- **And** an exercised operation with zero instrument records is itself invalid

#### Scenario B-P25: The live-instance campaign never mutates real mail
**Traces to**: US-P6 AC-1, AC-2 · **Category**: Edge Case
- **Given** the live instance, the 13 mailboxes and the campaign checklist
- **When** the rows execute
- **Then** every claim carries its named screenshot; no send completed, no message/folder deleted, no credential/key change, no server folder created/renamed, no epoch-inducing operation performed; Reply-all rows opened compose without sending
- **And** subjects/addresses are redacted in the receipts before they leave the lane, noted per screenshot

#### Scenario B-P26: Each mutation kills its test
**Traces to**: US-P7 AC-1 · **Category**: Error Path
- **Given** a green implementation and the mutation list of §7
- **When** each mutation is applied alone and its named test runs
- **Then** the test fails; restoring the code returns it to green
- **But** any mutation whose test still passes is a CHECK BLOCK naming both

#### Scenario B-P27: The paperclip is a metadata-only structure fact
**Traces to**: US-P4 AC-9 · **Category**: Edge Case
- **Given** messages whose MIME structure carries (a) a genuine user `message.md`, (b) a CID-only inline part, (c) the dedicated draft-body marker, (d) an unavailable attachment part — and a command-capture harness on the fake server
- **When** the list is fetched and the paperclip indicator renders (the classifier is w2's single MIME structure walker — register row 15; this scenario's test consumes it)
- **Then** only structure/part metadata was fetched — command capture proves no body bytes and no `\Seen` change; (a) and (d) are `true`, (b) and (c) are `false`; and an unclassifiable structure is a visible metadata failure — never a fabricated `false`

#### Scenario B-P28: The agent reference journey works without a Message-ID
**Traces to**: US-P4 AC-8 · **Category**: Error Path
- **Given** a message whose Message-ID header is absent, and a fake server whose references are issued only by returned results (no test-fabricated refs, no UI side channel)
- **When** the agent chains `read_message` → `list_email_attachments` → `read_email_attachment` → `download_email_attachment` using only returned references
- **Then** every step succeeds with no Message-ID anywhere and no synthesized identity
- **And** references from a wrong pair, an old configuration generation and a recreated folder (new UIDVALIDITY, old numeric UID) are each refused with the typed stale-reference error (`MailStaleReferenceError`) before any fetch or mutation — the I-03 counterexamples live at the scenario layer, not only as test rows (grill-2 F-1; executed by T33)

#### Scenario B-P29: Each bar is judged exactly as its own stated measurement
**Traces to**: US-P5 AC-5 · **Category**: Edge Case
- **Given** the completed series (the §8.2 arms) with per-sample receipts carrying both clocks, status, rows and n
- **When** each ADR bar is judged (§8.4)
- **Then** every bar is evaluated as stated — absolute bars named by clock and host, paired bars by the exact percentile and the opposing measurement; cached-display samples never mix with live-fetch samples; the summary bar judges clock (c) with its zero-mail-calls condition; busy is judged within the 5 s wait
- **And** a bar whose opposing series does not yet exist (message-open before M-A runs), a series with missing samples, or an empty folder is reported *not judgeable with evidence* / *not applicable* — never passed by absence and never filled with a fabricated 0 (grill-2 F-1 — the scenario layer for US-P5 AC-5; executed through T37 plus the §8 arm procedures)

---

## 6. TDD plan (tests designed before implementation)

Naming conventions observed in this tree: Go RED files carry a `_red_test.go` suffix (e.g. `pkg/email/mail_budget_red_test.go`); frontend component tests are `*.test.tsx`; Playwright specs are `*.spec.ts` assigned to exactly one shard in `tests/e2e/shards.json`. All filenames below are **assignments** (the ADR leaves exact test filenames to this spec), and all are **test files** — the proof package writes nothing else. These assignments are the **authoritative filenames** for this package's tests: where another spec's file table names a different filename for the same test (w1's §3.3 names pre-`_red` variants of the pool/poison/coalescing/watcher files), the name here stands — one test, one file, no duplicate under the other name (grill-2 F-5; w1's differing row is superseded for filenames only — authorship is unchanged: qa-lead). Every test is derived from the ADR's design rules, never from an implementation's current behaviour: a test whose expectation was read off the code it tests is an oracle violation, not a proof.

### 6.1 The instrument, specified first

**Problem:** mail operations are timed nowhere (§2.1) — not in the gateway log (failure counts only), not in tool results, not in the SPA. A measured comparison needs one record per operation, emitted by production code, with fields that cannot leak content.

**Record shape (the normative definition — frozen by this package, which the landing-order register row 17 names its single publisher; the emission obligations land as binding rows in the producers' specs: w5-integration owns the request-scoped envelope, w1 supplies the pool sub-fields, w2 the cache sub-fields; W0 contracts nothing here — this is in-process/structured-log, not wire. The emitter must exist before Wave E: a campaign run against a build without it produces zero records and is invalid by T6's rule):**

| Field | Type | Rule |
|---|---|---|
| `operation` | closed enum | `folders`, `list`, `open`, `summary`, `discovery`, `attachment_metadata`, `attachment_read`, `attachment_save`, `seen` |
| `pair_ref` | opaque string | The pair's opaque id + its configuration/folder-mapping generation. Never an email address, host:port with username, or folder name |
| `source` | enum | `live` \| `memory` \| `encrypted_disk` \| `none` — mirrors the contract's `MailReadMetadata` vocabulary with its scoping: `encrypted_disk` appears in Phase 1 only on folder-mapping records, never on header/list records (register row 2; the ADR's freshness-metadata row) |
| `hit` | bool | Cache hit (`true`) or miss (`false`); a hit never dialed |
| `duration_ms` | int | Operation end-to-end; > 0 on completion |
| `acquire_wait_ms` | int | Queue/acquisition time inside the read budget — the 5 s wait is measured here, not inferred |
| `socket_count` | int | Sockets/leases this operation used (0 for cache hits) |
| `outcome` | enum | `ok` or a safe failure class from the existing closed class set — never a raw upstream error string |
| `rows` | optional int | Row/message count where meaningful |
| `revision` | optional opaque string | The publication revision the read captured — makes a superseded-publish diagnosable from receipts |
| `shared_flight` | optional bool | True when this record joined an already-running coalesced flight (a joiner); a joiner records `socket_count=0` — the flight owner's record carries the socket — so MC-P4's sum stays comparable to the server's connection counter (register row 17's joiner rule; grill-4 I1) |

**Emission rules:** emitted on **success and failure** alike (the inverse of today's failure-only logging); one record per operation; delivered through an injected sink interface (tests inject a recording sink) and mirrored to the structured gateway log under a dedicated message key so measurement receipts are auditable from logs (the key's concrete name is decided at Wave D and recorded in w5-integration's US-7 — no T1–T6 test or receipt format binds the name; grill-2 O-1). No new polling, no sampling timer — the record is written when the operation ends.

**Instrument truth tests — what a test must prove so a silent instrument cannot produce a false green:**

| Order | Test name | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 1 | `TestMailInstrument_SuccessPathEmitsRecord` | `startMemIMAP` + injected sink | B-P1 | The success-path gap is closed: an open (and each other operation) emits exactly one complete record — the test fails today's failure-only logging shape by construction |
| 2 | `TestMailInstrument_NoSensitiveMarkers` | Unit (sink capture) | B-P2 | With marker subject/address/folder fixtures, zero markers in any field; the positive control asserts the markers ARE in the mail data, proving the scan works (trap 8 of the false-green doc: a probe without a control proves nothing) |
| 3 | `TestMailInstrument_HitMissLabels` | `startMemIMAP` + warm cache | B-P1, (MC-P3) | Cache hit → `hit=true`, non-live source, `socket_count=0`; miss → `hit=false`, `source=live`, `socket_count≥1` |
| 4 | `TestMailInstrument_SocketCounterAgreement` | `startMemIMAP` (counter) | (MC-P4) | The records' socket acquisitions sum to the server's accepted-connection delta **under coalescing**: a joiner records `socket_count=0` + `shared_flight=true`, the flight owner carries the socket — each dial counted exactly once (grill-4 I1's joiner rule) |
| 5 | `TestMailInstrument_DurationReflectsInjectedDelay` | `startMemIMAP` (fault hold) | (MC-P1) | A scripted 2 s server hold appears in `duration_ms` within tolerance — the field measures the operation, not a constant |
| 6 | `TestMailInstrument_DisabledProducesNoRecords_AndMeasurementRejects` | Unit + harness rule | (US-P1 AC-6) | Sink absent → no records; the measurement harness treats zero records for an exercised operation as an invalid run |

### 6.2 Runtime tests (pool, leases, coalescing, publication)

Harness note: `startMemIMAP` restores the `imapDial` seam and its dial seam is global — runtime tests must not run in parallel (existing test-suite constraint, verified file). A **test-only** fault-injection wrapper is added — proposed `pkg/email/imapserver_faults_test.go` — providing scripted command holds, stalled greeting/TLS/login, BYE injection, connection-limit replies and per-pair result markers. It is test code only; no production flag, hook or branch may exist for it (repo integrity rule).

| Order | Test name (file) | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 7 | `TestPoolCaps_TwoPerMailboxEightGlobalUnderConcurrency` (`pkg/email/pool_caps_red_test.go`) | `startMemIMAP` + faults | B-P3, (MC-P5) | 13 pairs × concurrent panel/watcher/tool work: accepted-connection counter never exceeds 2/mailbox or 8/global, **counting connecting reservations**; the counter — not returned rows — is the oracle |
| 8 | `TestPoolCeiling_TypedBusyNoNinthDial` (same file) | `startMemIMAP` + faults | B-P3 | Eight **counted-active** leases (connecting reservations count toward the eight, e.g. 7 established + 1 connecting) → the ninth request gets typed `pool_busy` within the 5 s wait; server counter shows no ninth dial (grill-4 I2 arithmetic fix) |
| 9 | `TestPoolLease_ExclusiveFolderState` (`pkg/email/pool_lease_red_test.go`) | `startMemIMAP` + faults | B-P4 | Concurrent different-folder readers with a mid-command hold see only their own folder's markers; PEEK preserves `\Seen`; no release/eviction EXPUNGEs |
| 10 | `TestPoolPoison_RetiredNotReused` (`pkg/email/pool_poison_red_test.go`) | `startMemIMAP` + faults | B-P5 | BYE / timeout / cancelled command / protocol error each retire the session; next operation dials fresh (login count); 20 cycles show bounded goroutine trend and no reuse |
| 11 | `TestPoolPresence_LastObserverReleases` (`pkg/email/pool_presence_red_test.go`) | `startMemIMAP` + faults | B-P6 | Close frame / silent disconnect / logout / workspace exit each return panel sockets to zero; the in-flight watcher cycle and agent read complete; a detached flight never restores retention |
| 12 | `TestPoolCancel_ReleasesEveryReservation` (same file) | `startMemIMAP` | B-P7 | Cancelling the only waiter frees the global reservation, account slot and mailbox lease before returning; late detached completion publishes only under the current revision |
| 13 | `TestCoalescingIdentity_SameAccountDifferentMapping` (`pkg/email/coalescing_identity_red_test.go`) | `startMemIMAP` + faults | B-P8 | I-01, case 1: per-pair Sent markers stay separate; account gate still bounds dials at 2 |
| 14 | `TestCoalescingIdentity_OldGenerationNeverJoined` (same file) | `startMemIMAP` + faults | B-P9 | I-01, case 2: new generation never joins/receives the old flight; old flight publishes nothing for the new generation; a joiner's cancel harms no survivor |
| 15 | `TestPublicationRevision_SupersededReadPublishesNothing` (`pkg/email/publication_revision_red_test.go`) | `startMemIMAP` + faults | B-P10 | I-02 main case + siblings (cache-first overtaken; UIDVALIDITY mid-read; delayed frontend response with older `publication_revision` dropped) — memory, disk, timestamps, UI state all unchanged |
| 16 | `TestPoolWarmReuse_LoginCountUnchanged` (`pkg/email/pool_caps_red_test.go`) | `startMemIMAP` | (MC-P5 converse) | The converse guard: a second read inside the idle window reuses the session (login count unchanged) — a pool that never reuses passes every cap test silently, so the reuse property needs its own test |
| 17 | `TestWatcher_FairUnderOneStalledMailbox` (`pkg/email/watcher_fairness_red_test.go`) | `startMemIMAP` + faults | (US-P2 scope) | One mailbox stalls; the other twelve due cycles still progress under bounds; a skipped cycle leaves `last_success_at` unchanged (a skip is never "checked just now") |
| 18 | `TestMailEndpoints_TypedBusyAndBackoffReasons` (`pkg/gateway/mail_busy_result_red_test.go`) | Integration (httptest) | B-P3 (wire) | The gateway maps an exhausted pool to the generated 503 with safe `reason=pool_busy` (and existing `busy`/`backoff`), upstream failure to 502 — the typed surface the SPA and Retry rules depend on |

### 6.3 Cache tests (envelope, invalidation, bounds, exclusion)

| Order | Test name (file) | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 19 | `TestCacheEnvelope_RoundTripFreshNonce` (`pkg/email/cache_envelope_red_test.go`) | Unit | B-P11 | Write→read byte-faithful; AAD binding (purpose, schema version, pair, generation) verified — a file moved between pairs/purposes is rejected; fresh nonce per write (two writes of the same plaintext differ); keys obtained only via `Store.DeriveSubkey` purpose separation |
| 20 | `TestCacheEnvelope_CorruptionRejectedNoSalvage` (same file) | Unit | B-P11 | Bit-flip / truncation / foreign key / wrong pair / schema mismatch → rejection + visible warning + live path + **no** `last_validated` advance; plaintext-salvage absence proven by marker scan with positive control |
| 21 | `TestCacheEpoch_UIDValidityChangeDiscardsAll` (`pkg/email/cache_epoch_red_test.go`) | `startMemIMAP` + faults | B-P12 | Epoch change discards every cursor/row; old-ref exercise (read, mark-seen, attachment) refused pre-fetch **on the same selected lease**; the recreated-folder/UID-reuse case refuses; fresh refs work |
| 22 | `TestCacheStale_LabelSurvivesFailedRefresh` (`pkg/email/cache_stale_red_test.go`) | `startMemIMAP` + faults | B-P13 | Rows past the 5-minute threshold render labelled stale; the single refresh failing keeps rows + visible error + Retry and never resets the timestamp |
| 23 | `TestCacheBounds_FiftyHeadersFourMiB` (`pkg/email/cache_bounds_red_test.go`) | `startMemIMAP` | B-P14 | 500-message folder → exactly 50 reusable/role; active view doesn't grow the cache; search ran live with the settled fields (subject + sender/recipient substring, server-side, 25/200 bounds — founder Q-D=A; server command counters); 4 MiB overflow → visible cache-unavailable, no truncation/omission |
| 24 | `TestCacheRetention_ThirtyMinutePostCloseDrop` (`pkg/email/cache_bounds_red_test.go`) | Unit (fake clock) | B-P14 | 30 fake-clock minutes after panel close → memory headers gone, zero IMAP commands during the drop; reopen inside 30 min retains |
| 25 | `TestCacheExclusion_GitStatusAndArchiveUnchanged` (`pkg/gateway/mail_cache_exclusion_red_test.go`) | Integration | B-P15 | The product's own exclusion, proved: cache write → data-folder `git status --porcelain` unchanged, staging set unchanged, the archive walk contains no cache member; watcher state files (`email-watch/`) carry the same exclusion and are purged on mailbox removal (founder Q-B=A); the cannot-prove-exclusion case refuses the first write — live-only with the visible `cache_unavailable` notice (that refusal is asserted, never assumed); positive control (allowed state file) IS staged/archived. **Stays red until w5-integration publishes the gate decision and w2 enforces it at the first write (register row 18) — that red is the activation gate, never skipped; no operator machine setup is a dependency** |

Windows ACL/permission evidence for the cache files rides the landing-order register's **row 23** instrument: w5-integration's wave extends the Windows CI workflow to run the `pkg/email` cache tests; the fallback evidence path is a founder-run/UAT Windows machine executing the ACL checks with a signed receipt. Where that instrument has not run, this package's Windows permission assertions are reported **not-judgeable-with-evidence** — never assumed from a Unix-only run.

### 6.4 Feature tests (F1–F7 journeys and the grill corrections)

| Order | Test name (file) | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 26 | `TestTemporaryOpen_WritesNothing` (`pkg/gateway/mail_preview_nowrite_red_test.go`) | Integration + fs snapshot | B-P16 | Every renderable kind opened then dismissed by all five exit paths → filesystem + Git status identical; mid-stream close and mid-preview mailbox removal included; token dead after exit; positive-control Save shows exactly one write; `Cache-Control: no-store` asserted; today's 15-minute body retention in `mail_preview_token.go` must be gone (its retention test, if any remains, must fail) |
| 27 | `TestTemporarySource_ResourcePolicyRefusals` (`src/components/library/preview/__tests__/mailAttachmentPreviewSource.test.tsx`) | Component (request counter) | B-P17 | I-04: zero requests for same-origin Library/API, workspace embed, remote; own minted resources load; workspace-file positive control fires the counter; ordinary workspace rendering unchanged |
| 28 | `TestAttachmentSave_ConfinedSanitizedUnique` (`pkg/mailattachment/service_red_test.go` — authored by qa-lead into the save service's test pack: the *plan* is owned by w4-features/ADR-W7, the *authorship* is this package's, per the register's w6-writes-tests-only rule; grill-2 O-3) | Integration | B-P18 | Hostile names sanitized + numbered unique under the authorized hierarchy; escape attempts (symlink, mount, parent-file conflict) refused visibly with zero partial files; concurrent panel+agent saves produce two files; exact-bytes positive control; over-cap refuse before any write |
| 29 | `TestAgentSaveAsk_ShippedDefaultAndModes` (`pkg/tools/email_attachments_ask_red_test.go`) | Unit + Integration | B-P19 | Shipped ceiling literal allow/allow/ask; Reconcile adds missing keys without overwriting operator values; effective policy per role matches `read_message` grants + ask; declined approval → zero transfer/write; Auto-on rides the existing workspace-path conditional class (Q4=A); per-agent allow never loosens global ask; result carries the real absolute path |
| 30 | `TestReplyAll_ExactRecipientSet` (`pkg/email/reply_recipients_red_test.go` + `src/components/workspaces/mail/__tests__/MailComposeDialog.reply.test.tsx`) | Unit + Component | B-P20 | One shared helper yields the exact fixture set on the final payload shape (backend) and the compose dialog renders/consumes it without a second algorithm (frontend); plain Reply = To only; quote editable/escaped; stale context can't overwrite another compose |
| 31 | `TestMailCSS_SafeStylesSurviveUnsafeRemoved` (`pkg/email/mailhtml/sanitize_red_test.go` + `mail-html-styling.spec.ts` browser assertions) | Unit + E2E | B-P21 | Computed style keeps safe colour/font/table/media-query presentation; scripts/handlers/forms/`@import`/remote `url()`/`position:fixed`/`expression()` → zero effects, zero default remote loads (controlled endpoints + uncontained positive control); Load-images only via token-scoped proxy after consent; stripped fallback readable |
| 32 | `TestMailDate_MissingNeverYearOne` (`pkg/email/mail_date_red_test.go` + `src/components/workspaces/mail/__tests__/mail-format.date.test.tsx`) | Unit + Component | B-P22 | Precedence Date → internal date → null; **No date** in list, detail, Sent, attribution; never year one/epoch/today; list/detail agree; the generated `date` field is required-but-nullable (a Go zero date serialization fails this) — the nullable form is the contracts wave's disclosed deferral (contracts check F3), so this assertion stays red until the amendment lands and is never re-pointed at the interim non-nullable shape |
| 33 | `TestAttachmentRef_JourneyWithoutMessageID` (`pkg/tools/email_attachments_ref_red_test.go`) | `startMemIMAP` + tools | B-P28 (MC-P26) | I-03: chain read_message → list → read → download using only returned refs on a Message-ID-less message; wrong-pair / old-generation / old-epoch refs each refused pre-fetch on the acting lease; no test-fabricated refs, no UI side channel |
| 34 | `TestPreviewBytePath_CapAndDownloadSplit` (`pkg/gateway/mail_byte_paths_red_test.go`) | Integration | (MC-P19/21 wire) | I-05: preview-purpose endpoint refuses actual over-cap bytes even when reported metadata lies (below/at/above `maxViewPartBytes` dataset); the browser-Download path streams the same larger part to completion; a mid-stream disconnect/decode failure never satisfies a success assertion on either path |
| 35 | `TestHandoff_FocusAnnouncementKeyboard` (`tests/e2e/mail-attachment-handoff.spec.ts`) | E2E | (MC-P29, I-06) | Keyboard-only Open → viewer → Save (success and failure) → Back; focus lands on the trusted context heading, returns to the originating action or the defined fallback with announcements; context bar reachable at 320 px and 200 % zoom; disabled actions carry the accessible "Save to Library first" explanation |
| 36 | `TestPaperclip_MetadataOnlyIndicator` (`pkg/email/attachment_flag_red_test.go`) | `startMemIMAP` (command capture) | B-P27, MC-P31 | F7: `has_attachments` derived from structure metadata — command capture proves no body bytes fetched and no `\Seen` change; CID-only and draft-marker parts produce false; a genuine `message.md` produces true; unavailable part still true; unclassifiable metadata is a visible failure, never fabricated false. The classifier is **w2's** (register row 15 — one MIME walker, no second); this test consumes it (grill-4 I5's trace re-point — the draft's "(MC-P21 scope)" anchor was wrong: MC-P21 is the Save-confined constraint) |

### 6.5 E2E additions

| Order | Test name (file) | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 37 | `tests/e2e/mail-live-access.spec.ts` — full panel journey against the fake IMAP server via the `GatewayProcess` fixture (own port, own `OMNIPUS_HOME`) | E2E | B-P1, B-P13, B-P23, B-P29 | Cached-first display then live refresh as separate visible phases; stale label + Retry; busy result surfaces; folder rail with counts; assigned to exactly one shard in `tests/e2e/shards.json` (`scripts/e2e-shards.sh check` enforces) |
| 38 | `tests/e2e/mail-attachment-handoff.spec.ts` (T35) + `tests/e2e/mail-temporary-open.spec.ts` — one row, two files (§17's counts are by rows, not files; grill-2 O-2) — Open/Back/Save/Open-in-Library, no-write browser check | E2E | B-P16, B-P18 | The user journeys in a real browser: Open renders via the Library viewer, Save lands and enables Open in Library, Download stays a browser download; the no-write claim re-verified at the process level (fixture workspace sweep) |

### 6.6 Test datasets

| Dataset | Rows (boundary → edge → error → happy) | Traces to |
|---|---|---|
| DS-P1 Instrument fixtures | marker subject/address/folder strings (positive control present in mail data, absent in records); operations: folders, list, open, summary, discovery, attachment_metadata/read/save, seen; success + one safe-class failure per operation; disabled sink | B-P1, B-P2 |
| DS-P2 Concurrency shapes | 13 pairs × {panel, watcher, tool} reads; 8 counted-active leases (connecting reservations count toward the eight, e.g. 7 established + 1 connecting); 9th demand; two pairs one account (different Sent mappings); old-generation flight + reconfigured joiner; cancelled only-waiter; detached late completion; coalesced flight with 2 joiners (`shared_flight` accounting) | B-P3…B-P10 |
| DS-P3 Poison/lease faults | server BYE; command timeout; cancelled command; protocol error; stalled greeting; stalled TLS/login; stalled SELECT; concurrent different-folder readers with mid-command hold; PEEK flag assertions | B-P4, B-P5 |
| DS-P4 Envelope corruption | valid round-trip; bit-flip; truncation; foreign-purpose key; wrong-pair AAD; schema mismatch; oversized envelope (> allocation guard); same-plaintext-twice (nonce check); file moved between pairs | B-P11 |
| DS-P5 Size/epoch boundaries | part bytes at `maxViewPartBytes - 1`, exactly, `+1`; false reported size (metadata lies, actual over/under); unknown size; encoded-vs-decoded inflation; UIDVALIDITY changed with numeric UID reused; UIDVALIDITY unchanged control | B-P12, T34 |
| DS-P6 Save names | `a/b.txt`, `..`, `.`, control-char name, `CON`/`NUL` (Windows-invalid), existing name, case-folded collision, unicode name, empty-after-sanitize (→ `attachment`), concurrent same-name saves, symlink parent, mount-redirect parent, disk-full | B-P18 |
| DS-P7 Reply fixtures | From A / Reply-To R / To = self+X+dup-R / Cc = Y+mixed-case-X+self+display-name-dup-R / hidden Bcc; self-is-primary; no eligible primary; already-`Re:` subject; text-only and HTML-only bodies | B-P20 |
| DS-P8 Style/date fixtures | safe colour/font/table/media-query mail; each banned construct in isolation (`position:fixed`, `expression()`, `@import`, remote `url()`, handlers, forms); stripped-style fallback content; dates: valid Date ≠ received, no Date + internal date, neither, unparsable, zero | B-P21, B-P22 |

### 6.7 Regression impact

Existing behaviour the change must preserve, with the existing tests that guard it — all must keep passing **unchanged** (weakening any of them is a CHECK BLOCK):

| Existing test / behaviour | Why it must survive |
|---|---|
| `pkg/email/view_missing_folder_test.go::TestFolderCounts_MissingSentFolderStillOpensMailbox`, `::TestFolderCounts_MissingDraftsFolderStillOpensMailbox`, `::TestFolderCounts_CancelledRequestStillFails` | The hotfix's count-path behaviour; the new discovery/page negative controls extend, never replace, them |
| `pkg/email/imap_peek_red_test.go`, `pkg/email/imapserver_test.go` | PEEK-only fetch discipline and the real-protocol harness itself |
| `pkg/email/mail_budget_red_test.go`, `pkg/email/mail_budget_failopen_red_test.go` | Existing two-slot account budget and coalescing behaviour that the new identity layers beneath — not replaces |
| `pkg/email/watcher_backoff_red_test.go`, `pkg/email/watcher_budget_red_test.go`, `pkg/email/watcher_flags_red_test.go`, `pkg/email/watcher_state_test.go` | Watcher independence: backoff shape (±20 % jitter), budget participation, no flag mutation, saved-state honesty |
| `pkg/email/dial_timeout_classification_test.go`, `pkg/email/append_timeout_red_test.go` | The 30 s dial / 45 s command bounds and timeout classification the pool must respect as subordinate bounds |
| `pkg/email/compose_readback_signature_test.go`, `pkg/email/decode_test.go`, `pkg/email/view_test.go` | Compose/decode/view behaviour outside this change's scope |
| Frontend: existing `MailPanel`/`mail-format`/query-client suites | The panel's current contracts (retry asymmetry, 30 s refetch while mounted) until W3's event-driven refresh lands — then the new tests replace the timer assertions deliberately, never silently |

---

## 7. Mutation list for the CHECK auditor

Each row is a deliberate defect a careless implementation would survive. CHECK applies **one mutation at a time** to the green implementation, runs the named test, expects failure, restores (the repo's one-narrow-local-run rule serializes these; or they run as a scripted mutation pass on CI where the harness allows). A mutation whose test still passes is a **CHECK BLOCK** naming both.

| # | Mutation (defect injected) | Careless implementation it mimics | Test that must die |
|---|---|---|---|
| M-α1 | Reuse a socket after a protocol error (retire-on-poison removed / poison path closes but re-enqueues the session) | "Close on error" that forgets the retirement bookkeeping | T10 `TestPoolPoison_RetiredNotReused` |
| M-α2 | Publish a superseded read (drop the publication-revision check at completion) | Cache/service layer that trusts "my read succeeded" over "my read is current" | T15 `TestPublicationRevision_SupersededReadPublishesNothing` |
| M-α3 | Drop the pair (or generation) from the read-sharing key — keep `account + operation + params` | The exact I-01 hazard in today's `flightKey`, reproduced by "simplifying" the identity | T13, T14 `TestCoalescingIdentity_*` |
| M-α4 | Skip UIDVALIDITY validation when exercising a reference (validate at resolve time only) | Today's resolve-then-mutate split the ADR explicitly replaces | T21 `TestCacheEpoch_UIDValidityChangeDiscardsAll` |
| M-α5 | Treat a failed candidate sweep as confirmed absence (no recognized role + failed probes → `availability=absent`) | M-01: the finite candidate list failing read as "the server has no such folder" | **w2's** `TestDiscovery_ProbeTimeoutIsUnknownNotAbsent` (the failed-probe→unknown case, w2 FR-W2-8) plus its no-candidates sibling `TestDiscovery_NoCandidatesIsUnknownNeverAbsent` — both live in w2's spec and test plan (register rows 3/21; grill-4 I3/Q4 re-point: the draft's `TestDiscovery_UnresolvedRoleIsUnknown` exists in no spec). A mutation whose named test is another package's is executable once that package's Wave A correction merge lands; until then the row is a **blocked** CHECK row reported to team-lead — the auditor never invents the test ad hoc |
| M-α6 | Allow an unlimited preview byte path (cap check reads reported metadata only, or the preview reuses the download endpoint) | I-05's single-shared-endpoint failure in either direction | T34 `TestPreviewBytePath_CapAndDownloadSplit` |
| M-α7 | Ship `download_email_attachment: allow` (or make the save auto-approve regardless of the setting) | "Helpfully" loosening the ask default, or a bespoke approval path that ignores Auto state | T29 `TestAgentSaveAsk_ShippedDefaultAndModes` |
| M-α8 | Let the sanitizer keep `position:fixed` (or any single banned construct) | A property/value table with one permissive row; the E2E computed-style half catches what a string-presence test would miss | T31 `TestMailCSS_SafeStylesSurviveUnsafeRemoved` |
| M-α9 | Advance `last_validated` on a failed refresh | "We tried, so we're fresh" timestamp handling | T22 `TestCacheStale_LabelSurvivesFailedRefresh` |
| M-α10 | Return `total=0` for an unknown count (drop the nullable) | Fabricated zero in the absence/unknown mapping | **w2's** `TestFolderCounts_UnknownRoleHasNullableTotal` (unknown → `total=null`; w2's plan extends the existing `view_missing_folder` pack — register row 3 makes w2 the value producer; grill-4 I3/Q4 re-point: the draft's `TestMailFolders_UnknownCountIsNull` exists in no spec). Same Wave A executability rule as M-α5 |
| M-α11 | Copy the original Bcc into a Reply-all | Recipient-merge shortcut | T30 `TestReplyAll_ExactRecipientSet` |
| M-α12 | Keep the 15-minute preview body retention (metadata-only grant not implemented) | The P1 compatibility item silently deferred | T26 `TestTemporaryOpen_WritesNothing` (its token-store byte assertion) |

Rules of engagement for CHECK: never weaken a test to make a mutation detectable *after the fact* — if a mutation survives, the finding is the test's, and the fix is a stronger assertion derived from the design (MC-P reference), not an adjusted expectation read off the mutated code. Mutation evidence is a saved log path + exit code per run (receipt-per-claim rule).

---

## 8. Measurement plan (executable procedure)

No measurement is performed by this specification task. This section makes the campaign executable: who runs what, on which build, with which clocks, repetitions, invalid-run rules and pass/fail bars. Numbers marked **target** are the ADR's founder-accepted bars (Q3=A); numbers marked **baseline** are read from the baseline receipt — none is invented.

### 8.1 Fixed conditions

| Condition | Value | Source |
|---|---|---|
| Mailboxes | The same 13 configured mailboxes as lane-M; opaque pair IDs in receipts; a slow/failing pair is never swapped for a convenient provider | ADR measurement plan; baseline §3.1 |
| Baseline build | `0.1.1+ee936a38` (the ADR's evidence baseline) | baseline §header |
| Candidate build | Bound to its exact SHA in every receipt; a receipt without the SHA is invalid | ADR "Bind all new receipts to exact candidate and baseline SHAs" |
| Host | One benchmark browser/host recorded per campaign; live-provider variability reported as an explained caveat, never omitted | ADR sampling recommendation |
| Clocks | (a) click→rendered (user clock: Playwright visible-state probe + screenshot), (b) request-start→response-end (request clock: network events on the exact URL), (c) request-start→rendered (summary-bar clock: network request start on the summary URL to the rendered-visible probe — the span the ADR's summary bar names; grill-4 m2). All recorded per sample; (a) and (b) are never merged | baseline §4 general rules; clock (c) named by this correction |
| Instrument | The §6.1 records — whose emitter is a Wave E precondition (register row 17): a run against a build without the emitter produces zero records for exercised operations and is invalid by §8.3; receipts join browser clocks to instrument records per operation | this spec; register row 17 |
| Privacy | Durations, statuses, byte counts, row counts only. No subjects, addresses, message content, credentials in any receipt | baseline §4; MC-P2 |

### 8.2 Arms (the missing series, now scheduled)

| Arm | Number produced | Procedure (condensed; full click-level detail inherits baseline §4 M-1…M-6) | Repetitions | Invalid if |
|---|---|---|---|---|
| **M-A message-open** (the headline gap — lane-M opened exactly one message and never timed it) | **Build: baseline first, then candidate** — this arm *creates* the missing message-open series the §8.4 message-open bar is judged against (grill-4 I4). Request + click clock for the detail GET; time-to-rendered-body; mark-seen noted, not measured as part of open | Deep-link into the panel (`?panel=mail&agent=<id>`); for each pair with ≥ 1 inbox message, open the first listed message; capture both clocks + screenshot | ≥ 5 cold, ≥ 10 warm per measured message, interleaved | 30 s folders refetch fired during the open; row count changed between list and open; a warm sample materially faster than its cold pair on this build (no cache exists — reuse would mean browser memory served it) |
| **M-B independent request clocks — folders / list / summary / drafts** | Splits lane-M's shared-origin click clock into user-perceived vs server time | Fresh panel session per pair; deep-link; network-layer captures all requests per URL; byte size + row count recorded | ≥ 5 cold + ≥ 10 warm per pair per operation | Panel auto-selected a mailbox (ambiguous click origin) — deep links only |
| **M-C attachment + HTML-preview cost** | **Build: baseline + candidate** (grill-4 I4). Request clock for attachment fetch and for the Load-images mint + frame fetches; click→images-painted | Only where an artifact exists; > 15-minute spacing or a different message to avoid measuring today's token-body store; **candidate build: token store must hold no bodies (MC-P19)** — a repeated-fetch comparison proves it | ≥ 3 per available artifact; n recorded, *not applicable* where none | Any token-store hit on the candidate build (a body cache exists — that is a correctness failure, not a timing) |
| **M-D large-folder benchmark** (controlled, never live-provider evidence) | Folders/list/open against the fake server at 10,000 and 100,000 synthetic messages; paging to the 200 ceiling; search finding older synthetic mail with the settled fields (subject + sender/recipient substring, server-side, 25/200 — founder Q-D=A); gateway peak RSS delta; socket counts; UID-search share attribution | Scratch `OMNIPUS_HOME` + one disposable mailbox against the fake server; never the live data dir or credentials; fixed artificial server latency recorded | ≥ 5 per size | The gateway served any real mailbox; fake-server clock skew unrecorded |
| **M-E connection-failure Retry exercise** (controlled) | Time-to-visible-error and Retry round-trip per class: `connect_refused`, `timeout` (accept-then-silence), `dns`, `tls`; busy/backoff shape; summary's residual retries measured separately | Scratch home; one benchmark mailbox per case; capture failing attempt clock (≈ bound), click→error-visible, then Retry round-trip; assert named class + Retry + zero automatic retries on folders/messages/detail; `retry=true` present only on the human click | ≥ 5 per class | Any silent retry on the read queries; error class rendered as empty-folder; the marker on an automatic request |
| **M-F cached-first vs live arms** (candidate only; baseline has no cache) | Cached-first display time **separate** from live-fetch time; stale-threshold behaviour (immediate stale display + one live refresh — the live refresh fires only when the data is absent or older than five minutes, founder-settled stale-gating Q-C); warm-socket reuse eligibility and hit rate; closed-30-minute re-fetch | Phase-1 matrix of the ADR run table: empty-cache cold, saved-folder cold (restart), warm socket within 2 min, warm headers, watcher interplay, > 8 active saturation, one-connection server. **Requires the REST `observer_id` behaviour** (register row 6 — the W0 schema half has landed, commit `5f23ae8a0`; w5-integration requests/implements the behaviour in Wave D): without the behaviour every panel read stays request-scoped, warm-panel retention never activates, and this arm's warm rows are **not judgeable** — reported as such, never passed | Per ADR sampling: ≥ 5 cold, ≥ 10 warm per operation per pair, randomized order | Any run violating §8.3; a cached sample compared against a live sample as if like-for-like |

**Honest-comparison rules** (inherited verbatim from baseline §6 — the unfair comparisons already proven in this evidence base): never average error round-trips with success timings; never sum the folders and list click ranges (same origin, overlapping); compare Sent success against Inbox pages of similar size, reporting the old 502 error speed separately; compare only same-row-count populations or M-D's controlled folders; require the browser/HTTP/server split before crediting any summary fix; never compare "IMAP cold live" with "cached warm display" and credit the transport.

### 8.3 Invalid-run rule (discard, re-run, and say so)

A run is invalid and is re-run with the discard recorded when: the workspace or panel reloaded mid-operation; a second request to the same URL was observed (double-click); the response came from browser memory (React Query `staleTime` hit — verified by checking the request actually fired on the network tab); any other client or agent touched the same mailbox during the series; the gateway restarted; the build is not the receipt's SHA; or an exercised operation produced zero instrument records (§6.1 T6). Never extrapolate a missing sample; an empty folder is *not applicable*, not 0 ms.

### 8.4 Pass/fail bars (founder-accepted targets, Q3=A — judged, not assumed)

| Bar | Judgement | Baseline anchor |
|---|---|---|
| Connection wait | Busy acquisition surfaces within the **5 s** maximum wait, inside the **45 s** total read budget (30 s dial ceiling subordinate); typed result, no ninth socket | **target**; baseline worst-case waits derived from `dialTimeout`/`commandTimeout` (Verified constants) |
| Reusable metadata | ≤ **4 MiB** global reusable metadata budget; overflow visible, never truncated | **target** (Q3=A, both phases) |
| Cached first display | Median ≤ **250 ms**, p95 ≤ **500 ms**, same benchmark browser/host; stale-vs-validated reported separately; no full-list skeleton replacing useful rows | **target**, absolute (no baseline cache exists — never paired against baseline) |
| Warm live reuse | ≥ **50 %** lower paired median vs baseline re-dial cost where reuse is eligible; outliers and failures recorded | **target**, paired against baseline warm ≈ cold re-dials (baseline §2.4) |
| Cold folder/list work | No > **10 %** paired-median regression vs baseline rows 1–2 on the same pairs/folders, on **both** clocks; a regression caused by newly-honest discovery is shown as a split and requires explicit acceptance | **target**, paired; baseline folders 2.5–8.1 s, list 4.7–22.8 s click-clock (**baseline**) |
| Message open | No > **10 %** paired-median regression vs the M-A series, same message, both clocks — M-A must exist first | **target**; baseline: none exists (that is the gap) |
| Summary | p95 ≤ **1 s** on clock (c) — request-start→rendered (§8.1; grill-4 m2's clock naming) + **zero** mail network calls + no wait on pool/cache refresh; the old 17–25 s delay (**baseline**, cause Unknown) gets a diagnosed trace, not just a fast number | **target** + **baseline** anchor |
| Visible failure | One attempt ends by its bound with named class + Retry; folders/messages/detail show **zero** automatic retries; summary's residual 3-retry behaviour measured-and-accepted or removed | **target**; baseline anchor: 80 logged upstream failures with no timing, one ≤ 5 s `folder_missing` Retry (**baseline**) |
| Resource invariants | ≤ 2 sockets/mailbox, ≤ 8 global, existing 2/account work slots; panel-close releases sockets; ≈ 2 min idle expiry; ≤ 50 headers/role; < **10 MiB** runtime overhead vs the same warmed baseline; request-scoped body buffers reported separately | **target**; baseline socket profile trivially 1-per-request — verify, don't infer, in M-D |

A bar is **failed** by its own stated percentile and opposing measurement; a bar with missing samples reports the gap and is not passed by absence. Absolute bars name clock and host; paired bars name the exact percentile and the opposing series.

Campaign preconditions (the register's wave table, Wave E entry): the instrument emitter exists in the producers' code (register row 17 — otherwise every exercised operation yields zero records and every run is invalid by §8.3); the row-23 Windows evidence path exists where Windows permission assertions are claimed; and cache-activation rows run on a machine where the product-owned exclusions (register row 18) hold. A precondition that is missing makes the affected rows **not judgeable**, reported as such to team-lead — never silently passed.

---

## 9. UAT campaign (live instance)

Two lanes minimum (uat-tester), each PASS row checked by an independent uat-validator — the repo's standing UAT structure. The campaign runs **on the live instance with the 13 mailboxes** after CI-green, before the founder's landing yes. The prohibitions are absolute: this is shared state holding the founder's real mail.

### 9.1 What the campaign must cover (rows → claims → screenshots)

| # | Row | Claim it evidences | Named screenshot(s) |
|---|---|---|---|
| U-1 | Open the workspace Mail panel per mailbox (deep link) | Folder rail renders with counts; cached-first or live per state | `u1-rail-<pair>.png` — rail with all three roles and counts visible |
| U-2 | Reopen a recently opened mailbox | Cached-first display: rows appear immediately, labelled with age; one live refresh follows **only when the data is absent or older than five minutes** — the founder-settled stale-gating (Q-C: manual Refresh always refreshes; own-action refresh unchanged); on fresh data the rows appear with no live dial | `u2-cached-first-<pair>.png` (immediate rows) + `u2-refresh-settled-<pair>.png` (post-refresh, freshness label) |
| U-3 | Let rows age past the threshold, then trigger refresh failure (network simulate at the harness level, not the live server) | Stale label + visible error + Retry; rows never presented as fresh | `u3-stale-error-<pair>.png` — stale label AND error AND Retry in one frame |
| U-4 | Exercise Sent/Drafts on a mailbox without them | Confirmed-absent shows empty with the server-folder-absent explanation; an unresolved discovery shows the unknown state and the name-setting prompt — never a false "no such folder" (M-01) | `u4-absent-<pair>.png` vs `u4-unknown-<pair>.png` — the two states visually distinct |
| U-5 | Saturate (open many mailboxes rapidly) until busy | Typed busy message "Mail is busy. Try again.", cached rows remain visible | `u5-busy-<pair>.png` |
| U-6 | Open an attachment (each kind available), then Back | Temporary Library viewer with the exact context bar; focus behaviour visible; no new Library entry | `u6-open-<kind>.png` (context bar readable), `u6-back-<kind>.png` (returned to mail) |
| U-7 | Save an attachment → Open in Library | Saved under the mail hierarchy; "Saved to Library" + Open in Library; file exists at the real path | `u7-saved-<name>.png` + `u7-open-in-library.png` + a redacted path listing |
| U-8 | Browser Download the same attachment | A browser download, not a Library file | `u8-download-<name>.png` (browser download surface) |
| U-9 | Keyboard-only pass over U-6/U-7 (Open → viewer → Save → Back), narrow width + 200 % zoom | Focus on the trusted heading; Back restores the originating action; context bar reachable at 320 px | `u9-keyboard-open.png`, `u9-narrow-context-bar.png`, `u9-zoom-context-bar.png` |
| U-10 | Paperclip indicator on a list row with attachments | Indicator present with accessible "Has attachments" text | `u10-paperclip-<pair>.png` |
| U-11 | Reply all composition on a multi-recipient message | To/Cc prefilled exactly per the rule; Bcc absent; compose opened, **never sent** | `u11-replyall-compose.png` — recipient chips visible, send NOT clicked |
| U-12 | Open a message with no usable date | **No date** rendered in list and detail | `u12-no-date-list.png`, `u12-no-date-detail.png` |
| U-13 | Agent journey (allowed agent): read_message → list_email_attachments → read_email_attachment on an owned pair | Tool results correct; list/read create no file; transcript shows the issued reference | `u13-agent-read.png` (tool transcript, redacted) |
| U-14 | Agent save under ask, approval granted — then declined case | Granted: file saved, real path returned; declined: explicit refusal, zero transfer | `u14-agent-save-granted.png`, `u14-agent-save-declined.png` |
| U-15 | Styled HTML mail render (a message in the tester's own synthetic seed where available) | Colour/layout preserved; no remote loads by default; Load-images consent path works | `u15-styled-default.png` (no remote loads), `u15-styled-consent.png` (after Load images) |
| U-16 | Panel close → summary/badge still updates from saved state; reopen within 30 min vs after | No background panel spinner while closed; badge honest | `u16-closed-badge.png` |

### 9.2 What the campaign must NOT do (hard prohibitions)

| Prohibition | Reason |
|---|---|
| **No sends** — never complete a send on any live mailbox, including the Reply-all rows (compose may be opened and then abandoned) | Real mail would go to real people; the send path's live acceptance is a separate, founder-scoped exercise |
| **No deletes** — no message or folder deletion, no `\Deleted` flag, no expunge-inducing action | Irreversible on live data |
| **No key/credential changes** — no password rotation, no mailbox reconfiguration that would bump a generation mid-campaign | Would invalidate caches/series mid-run and risks locking the account |
| **No server folder create/rename/move** | Would change folder discovery results mid-campaign and mutate the founder's mailbox layout |
| **No UIDVALIDITY-inducing operations** (no folder recreation, no server-side migration) | Destroys the epoch for every cached reference and the M-A/M-F series |
| **No flag mutations beyond an ordinary open** (mark-seen from opening is accepted and noted; no unread-toggling sweeps) | Keeps read-state honest for the measurement pairs |
| **No convenience substitution** — an unavailable/failing mailbox is reported unavailable; it is never swapped for a healthy one | The ADR's rule: do not silently replace slow or failing pairs |

### 9.3 Evidence rules

Every PASS row carries its named screenshot(s) (workspace badge visible in every screenshot, per the UAT lane conventions), a redacted page snapshot, and both clocks where the row is a timing row. Subjects/addresses are redacted before receipts leave the lane; the redaction is noted per screenshot. The uat-validator re-drives the critical rows (U-2, U-4, U-6, U-7, U-11, U-13, U-14) with its own account and browser, and overturns with its own evidence.

---

## 10. Test-integrity rules this suite enforces

From `docs/internal/false-green-patterns.md` (read in full for this spec) and the shared rules — binding on every test this package writes and every green it accepts:

1. **A retry-pass counts as red.** A test that failed and passed on re-run has not passed: it is investigated to a mechanism before any green is reported. A test failing twice under isolated re-runs is a **defect**, not a flake — and calling it a flake is how a real defect survives (repo "Reporting Results").
2. **Every test must be able to fail.** `go test -run` prints `ok` for a pattern matching nothing — count `--- PASS`/`--- FAIL` lines by name; a package `FAIL` with zero `--- FAIL` lines is a hang, not a finding; `ok` with zero named passes is not evidence. Each new test's first RED run is proven (tests-only commit through CI, or the single dispatcher-owned narrow local run), and the suite is mutation-proven via §7.
3. **Exit codes captured directly**, never through a pipe: `cmd > log 2>&1; echo "exit=$?"` — a wrapper's 0 has masked a hard compile error here before.
4. **Assert behaviour, never source text** (trap 2): a grep proving an identifier exists passed 673/673 while the gate was deleted. The I-04 resource-policy test counts requests; the no-write test diffs the filesystem; nothing in this plan passes on a string search of the implementation.
5. **Count discrete properties, never stopwatches** (trap 3): socket caps, login counts, refresh counts, request counts are the oracles; where a duration is the property (instrument truth, M-E bounds), the delay is *injected and known*, not asserted from wall-clock margins.
6. **Positive controls everywhere** (trap 8's lesson): the leak scan must find the marker when it is present (in the mail data); the request counter must fire on the workspace-file control; the exclusion test must see the allowed state file. A check that could never see the failure proves nothing.
7. **Coverage is enforced, not maintained** (trap 4): every new Playwright spec is assigned to exactly one shard in `tests/e2e/shards.json` (`scripts/e2e-shards.sh check` fails CI otherwise); Go tests are picked up by the CI runners by construction — a test that no job runs is not a test.
8. **The generated validator can be weaker than the contract** (trap 8): where a test binds to generated types, the probe carries a passing control so a wrong-shape rejection is distinguishable from a validator that accepts anything.
9. **Build configuration**: Go tests run with `CGO_ENABLED=0`, tags `goolm,stdjson`; the full suite never runs locally — CI is the authority (repo rule 2 and the OOM history behind it); at most one narrow local test at a time.
10. **Bind verdicts to tree hashes**: a result from a tree that moved mid-run is meaningless; every receipt names its SHA (MC-P27 harness rule).

---

## 11. Explicit non-goals

| Outside this package | Boundary / reason |
|---|---|
| Writing or fixing production code | The proof package owns tests only; findings route to the owning lead (rule 15 discipline). The instrument's *shape* is specified here; its *implementation* belongs to W1/W2/W4 |
| Contract-file edits and regeneration | W0 alone (`contracts/` + generated artifacts); tests bind to generated types only |
| Phase 2 (JMAP, encrypted disk headers) test implementation | This spec defines the Phase 1 proof; the Phase 2 arms (§8.2 note) are scheduled after separate Phase 1 evidence per the ADR's phase gate — their test files are a later wave under the same rules |
| Sending-path, drainer or mail-as-chat testing | No send is exercised live (UAT prohibition); the retired drainer stays retired; Mail stays an agent tool + notice surface (ADR non-goals) |
| Security certification | The tests *evidence* the security-relevant behaviours; the security pass remains security-lead's gate review. A green here is not a pentest |
| Performance tuning | The measurement plan judges bars; it does not optimize code. A failed bar is a finding, not a tuning licence |
| Re-opening founder decisions | Q1=A, Q2=B, Q3=A, Q4=A, Q5=A are settled; the grill's single round is spent; a blocking finding escalates to the founder, never re-grills |
| Invented numbers | No measurement or test result exists in this spec. Every number cites the ADR (target) or the baseline receipt (measured) or a Verified code constant |

---

## 12. Traceability matrix

| Requirement | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| MC-P1 | US-P1 AC-1 | B-P1 | T1, T5 |
| MC-P2 | US-P1 AC-2 | B-P2 | T2 |
| MC-P3 | US-P1 AC-3 | B-P1 | T3 |
| MC-P4 | US-P1 AC-4 | (B-P1) | T4 |
| MC-P5 | US-P2 AC-1, AC-2 | B-P3 | T7, T8, T16 |
| MC-P6 | US-P2 AC-4 | B-P5 | T10 |
| MC-P7 | US-P2 AC-5 | B-P6 | T11 |
| MC-P8 | US-P2 AC-6 | B-P7 | T12 |
| MC-P9 | US-P2 AC-3 | B-P4 | T9 |
| MC-P10 | US-P2 AC-7, AC-8 | B-P8, B-P9 | T13, T14 |
| MC-P11 | US-P2 AC-9 | B-P10 | T15 |
| MC-P12 | US-P3 AC-1 | B-P11 | T19 |
| MC-P13 | US-P3 AC-2 | B-P11 | T20 |
| MC-P14 | US-P3 AC-3 | B-P12 | T21 |
| MC-P15 | US-P3 AC-4 | B-P13 | T22 |
| MC-P16 | US-P3 AC-5 | B-P14 | T23 |
| MC-P17 | US-P3 AC-6 | B-P14 | T24 |
| MC-P18 | US-P3 AC-7 | B-P15 | T25 |
| MC-P19 | US-P4 AC-1 | B-P16 | T26, T34 |
| MC-P20 | US-P4 AC-2 | B-P17 | T27 |
| MC-P21 | US-P4 AC-3 | B-P18 | T28 |
| MC-P22 | US-P4 AC-4 | B-P19 | T29 |
| MC-P23 | US-P4 AC-5 | B-P20 | T30 |
| MC-P24 | US-P4 AC-6 | B-P21 | T31 |
| MC-P25 | US-P4 AC-7 | B-P22 | T32 |
| MC-P26 | US-P4 AC-8 | B-P28 | T33 |
| MC-P27 | US-P5 AC-1…AC-4 | B-P23, B-P24 | T6, T37, §8.3 |
| MC-P28 | US-P5 AC-5 | B-P29 | T37, M-A…M-F |
| MC-P29 | US-P6 AC-1, AC-2 | B-P25 | U-1…U-16, T35 |
| MC-P30 | US-P7 AC-1 | B-P26 | §7 M-α1…M-α12 |
| MC-P31 | US-P4 AC-9 | B-P27 | T36 |

Every MC-P traces to at least one story, scenario and test; every scenario traces to at least one story (its `Traces to` line) and one MC-P through this table; the seven user stories carry **41** numbered acceptance scenarios in §3, all covered above (the draft's "34" was a miscount — the pre-correction text held 40, grill-4 m1; this correction adds AC-9). The round-2 F-1 gap is closed in the text rather than excepted: B-P28 gives MC-P26 its scenario (the former "(T33 scope)" cell was a test ID, not a scenario) and B-P29 gives MC-P28 its scenario (the former "(§8.4)" cell was a section reference) — the claim above is now true by construction, and §17's counts were re-derived from the corrected text.

---

## 13. Definition of Done

The repo's two never-merged lines, with the specific evidence each requires **for this package**:

**Code correct and tested** — every test file of §6 exists, ran RED first (tests-only commit through CI, or the single permitted narrow local run, receipt kept), then GREEN on CI (go-build, go-vet, go-test, go-race, contracts, spa, typecheck all green on the feature branch — Hard Constraint #7: pre-existing failures are ours too); the CHECK audit ran §7's mutations with a saved log + exit code per mutation and zero surviving; no existing test was weakened, skipped or deleted (§6.7 fences green and unchanged); every measurement receipt names its build SHA, both clocks, n/median/p95/worst/failures, and its invalid-run discards; the §8.4 bars each judged or reported not-judgeable-with-evidence (a missing series is reported, never assumed).

**Reachable by a user/agent** — the features the tests prove are invocable, not merely implemented:
- **Hard Constraint #6**: each new tool (`list_email_attachments`, `read_email_attachment`, `download_email_attachment`) registered in the builtin catalog **and** carrying an explicit policy entry for every agent — `grep -rl '"list_email_attachments"' pkg/coreagent/ pkg/config/ pkg/tools/` returning nonzero per name, the shipped ceiling literal allow/allow/ask asserted effective per role (T29), not just present in defaults. Registration alone grants no access; policy and assignment checks both proven.
- **A screen or component renders each feature**: the Mail panel (rail, list, reader, compose), the temporary Library viewer with its context bar, the Connectors mailbox settings — all exercised by the executed UAT rows U-1…U-16 with named screenshots, independently validated.
- **The test plan EXECUTED, not written**: "written, not executed" is not testing — the UAT pack and the measurement receipts are artifacts from runs that happened, checked by an independent validator and by team-lead's output review.
- **User-facing documentation** updated in the same change and audited (§14): docs-verifier compares every changed page against actual behaviour and the executed evidence.

---

## 14. User-facing documentation TODOs

The ADR assigns the five user pages to the implementing leads (W3/W4/W8); this package's obligation is that **each documented claim lands only with its executed proof**, and docs-verifier audits the pages against the code in the same change:

| Page (exact path) | What must be said (proof-linked) |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/mail.md` | Cached rows vs live validation and the visible age/stale label (U-2/U-3 evidence); unknown vs absent folder states and the name-setting prompt (U-4, M-01 wording); busy message and Retry rules (U-5); the 25/+25/200 paging and search reachability (search matches subject + sender/recipient substring server-side, founder Q-D=A); paperclip indicator (U-10); Open as temporary viewing with no disk write, exit disposal, and the exact context bar (U-6); 25 **MiB** preview/save cap (25 × 1,048,576 bytes — `pkg/email/view.go::maxViewPartBytes` = `25 << 20`; grill-4 m5 wording fix) and larger-files browser Download-only (T34-executed before the page claims it); Reply all recipient rule and No date (U-11/U-12); the Save-result-unknown state and what an explicit same-token retry returns (bounded reconciliation, M-02 — docs-verifier audits the page's wording against the landed behaviour, including the gateway-restart case, so the page never over-claims "never a duplicate") |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/library.md` | A Mail Open is not a file/list entry and vanishes on exit; the mail bar and Back behaviour; Save-to-Library-first gating for edit/rename/move/PDF fill-sign; the mail → mailbox → UTC save-month hierarchy and sanitized numbered names (U-7); mail-derived HTML: original bytes, scripts off by default, per-file scripts checkbox (Q5=A wording, security-lead checks the page) |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/connectors.md` | Sent/Drafts override and Automatic-clear semantics; resolved-role status display; removal/cleanup-pending Retry; (Phase 2 later) Auto/IMAP/JMAP preference with visible fallback (Q2=B wording) |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/security.md` | What folder/header metadata is stored, that it is encrypted, key dependency, expiry/removal, Git/backup exclusions (T25-executed before "excluded" is claimed); no body cache (T26); the temporary source's resource policy (I-04 wording); ordinary tool ask/Auto vs the (non-existent) attachment-specific approval |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/troubleshooting.md` | Busy/backoff vs server connection limit; Refresh vs Retry; cache failure/key lock; missing vs unresolved folders (M-01); preview expiry/Back/reopen; over-cap Download-only vs refused Save; the Save-result-unknown state and what an explicit retry returns (M-02 wording); no credential/token-bearing logs or screenshots in support instructions |

Internal receipts (measurement, mutation logs, UAT evidence packs) are **not** user documentation and stay out of `docs/` user pages.

---

## 15. Open questions — six decided in the correction round, one open with a named owner

Every question below is **decided** except **Q-P7**, which is open with its owner named; each row records the decision and its owner. None reopens a founder decision. A blocked implementation escalates through team-lead under rule 15 — never by reopening a settled row.

| # | Question | Decision | Owner / authority |
|---|---|---|---|
| Q-P1 | Instrument emission ownership | **Decided — the landing-order register row 17 split (adopting this spec's (b)):** the record shape is frozen by this package (§6.1, the only normative definition); **w5-integration** owns the request-scoped envelope (gateway emitters for preview mint/serve, removal, summary); **w1** supplies the pool sub-fields; **w2** supplies the cache sub-fields; the joiner rule (`socket_count=0` + `shared_flight` for joiners) keeps MC-P4 comparable to the server counter; the emitter must exist before Wave E | Landing-order register §2 row 17; team-lead dispatches the obligation rows into the three producers' specs in Wave A |
| Q-P2 | Fault-injection harness shape | **Decided: (b)** — the proposed `pkg/email/imapserver_faults_test.go` wrapper composing with `startMemIMAP`; the existing harness file is not edited | This package (qa-lead); test code only |
| Q-P3 | Exclusion test vs an external staging job | **Decided — superseded by the founder ruling (2026-10-02):** there is no external job to trace. The **product owns the exclusion by its own means on every install**: its Mail cache directory (and, per Q-B=A, `email-watch/`) is excluded from data-directory version-control staging and from the application's own backups/archives; w5-integration publishes the gate decision, w2 enforces it at the first write (`git check-ignore`-equivalent semantics, evaluated in-process, fail-closed to live-only with the visible `cache_unavailable` notice). The tests run as written (B-P15/T25) against that product-owned mechanism; none of the draft's options (a)/(b)/(c) survives, and no personal machine-setup artifact of any kind — no setup repository, no operator-side staging job, no backup remote, no secrets scanner, no personal ignore file — appears anywhere in this feature | Founder ruling in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md` (correction-round input, Q-A); register row 18 |
| Q-P4 | UAT instance | **Decided: (b)** — behavioural rows on the live instance under §9.2 prohibitions; induced-condition rows (U-3/U-4/U-5) on a scratch seeded mailbox against the fake server | Team-lead dispatch; this package plans the lanes |
| Q-P5 | M-A repetitions vs thin live data | **Decided: (b)** — record the actual n; mark *not applicable* where the live data cannot support the bar; backfill the series from M-D's controlled folders; a bar impossible on the data is reported not-judgeable-with-evidence, never silently passed | This package; matches the baseline receipt's stated rule |
| Q-P6 | Mutation pass mechanics | **Decided: (a) where the harness allows, (b) as fallback** — serialized either way; a saved log path + exit code per mutation | Team-lead runs the pass at CHECK |
| Q-P7 | Tool-result schemas (register row 8's deferred half) | **Open — owner: the architect.** The landed contracts wave (`5f23ae8a0`) delivered every other consumed shape; the mail tool-result schemas were deliberately reported rather than invented, pending an architect decision on admitting concrete mail tool-result shapes to the `ToolCallResultFrame.result` oneOf (contracts check, row 8). No test of §6 binds a tool-result schema field (T29/T33 assert tool behaviour through the tool layer, not wire schemas), so no w6 test is blocked; until decided, the §4.4 and §16.2 W0 rows carry the deferral | Contracts-wave check (row 8: "tool-result schemas reported, not invented"); register row 8; register R-2.2 gives the architect the wire-shape decision |

---

## 16. Interfaces — PUBLISHES and CONSUMES

So the packages can be built in parallel without editing each other's files:

### 16.1 PUBLISHES (this package is the source of truth for)

| Artifact | Consumers | Freeze point |
|---|---|---|
| The instrumentation record shape and safety rules (§6.1, MC-P1/P2, incl. the `shared_flight` joiner rule) — **the only normative definition; this package is the register row 17 publisher of the shape**. The emitting obligations land as binding rows in w5-integration's (request-scoped envelope), w1's (pool sub-fields) and w2's (cache sub-fields) specs in the same correction round; the emitting code is theirs (Waves C/D), and the emitter must exist before Wave E | w5-integration, w1, w2 (implement emission), the measurement harness, W0 (no contract impact — in-process/log surface) | The shape freezes with this correction round (Wave A); the emitter exists by the end of Wave D (register wave gate) |
| The assigned test-file names and their proof obligations (§6.2–§6.5) | qa-lead RED (author), the production owners (must make green without weakening), CHECK auditor | Wave A merge |
| The mutation list (§7) | CHECK auditor; the 5-reviewer gate's pr-test-analyzer | Wave A merge; M-α5/M-α10's named tests are w2-owned and executable after w2's Wave A merge lands |
| The measurement procedures, invalid-run rule and bar judgements (§8) | The measurement lanes (uat-tester or a dedicated measurement dispatch), team-lead's landing gate | Wave E entry is gated on: the emitter existing (register row 17), the row-23 Windows evidence path existing, and cache-activation rows running where the product-owned exclusions (row 18) hold |
| The UAT row list, prohibitions and evidence rules (§9) | uat-tester lanes, uat-validator | Wave E |
| The test-integrity rules application (§10) | Every reviewer of this change's tests | Standing |

### 16.2 CONSUMES (this package edits none of these; tests bind to their frozen shapes; the single publisher of each row is named per the landing-order register §2)

| Publisher (register row) | What is consumed | Freeze point |
|---|---|---|
| **W0** — backend-lead in the ADR-W0 contract role, dispatched as Wave B (register row 1; no spec file exists or is needed — register §5) — **landed** (commit `5f23ae8a0`, `verify-contracts` exit 0, regeneration drift-free; the branch stays unpushed until the wave's disclosed Wave C/D consumer adaptation lands — contracts check F1) | Generated contract types: `MailReadMetadata` (nullable `last_validated_at`; closed `notice_code` enum incl. `cache_unavailable`; nullable `publication_revision`; the four-value `source` enum with its Phase-1 scoping — row 2); `mode=cache_first\|live`; `MailFolder` folder-role fields `availability` / nullable `uidvalidity` / five-value `mapping_source` incl. `saved` (row 3) — **`total` landed non-nullable**, the nullable form being the wave's disclosed deferral (contracts check F2; amendment scheduled with the Wave C/D consumer PRs); the REST `observer_id` query param on folder/list reads (row 6); presence frames `mail_panel_observer` open/close + ack/error and `WsFrameType` entries (row 5); `MailUnavailableError` reasons `pool_busy\|account_busy\|server_connection_limit\|backoff` (row 7); paging/search/stale-409 shapes `MailMessagePage` (`next_cursor`/`has_more`/`view_limit_reached`) + the `search` param + typed 409 `MailStaleReferenceError` (row 4 — the shapes; w2 implements the search itself); the feature wire shapes: `has_attachments` + `message_ref` on `MailMessageSummary`/`MailMessage`, `date` on both — **landed non-nullable**, the nullable form being the same disclosed deferral (contracts check F3); `MailAttachmentPreviewRequest/Response` with the dedicated preview byte endpoint (`MailAttachmentContentSource.byte_url`), `MailAttachmentSaveRequest/Response` with required `save_operation_token`, `MailReplyContextRequest/Response`, `LibraryEntry.preview_profile` + per-file `preview_scripts_allowed` (row 8); **tool-result schemas not landed** — deferred pending the architect decision (§15 Q-P7) | Landed and frozen: tests bind to the generated types from here and never hand-write wire types; the two nullability deferrals keep their dependent assertions red until the scheduled amendment |
| **w1** (register rows 9, 10, 11, 13, 16, 17, 19) | Pool/lease interfaces `MailSessions`/`Lease`/session-source injection (its §3.1 freeze), the `RevisionSource` capture/compare accessor, the same-lease epoch/generation validation capability, the transport-agnostic presence registry `PanelPresence` in `pool.go`, the watcher dirty-mark signal, the pool sub-fields of the instrument record, and the `recordFailure` raw-error redaction (its own exclusive file) | w1 §3.1 frozen; its correction rows land in Wave A; its code in Wave C |
| **w2** (register rows 4, 14, 15, 16, 17, 18, 21) | The targeted single-part reader (`pkg/email/attachment_parts.go`), the MIME structure classifier + `has_attachments` derivation (one MIME walker — T36's target), IMAP SEARCH + cursor issuance in `view.go` (row 4's split), the I-03 normative validation FRs in `view.go`, the counts-freshness rule (memory-only counts; refresh when absent or > 5 minutes; the four triggers — row 21), the cache envelope + `Store.DeriveSubkey` usage, `HeaderCache.Put`/`FolderSnapshotStore.Save` taking w1's captured revision value, first-write enforcement of the exclusion gate, the cache sub-fields of the instrument record | Wave A rows; code in Wave C |
| **w5-integration** = ADR-W4 (register rows 6, 10, 11, 12, 16, 17, 18, 19, 23) — the single gateway Mail-route writer | Gateway wiring: the REST `observer_id` request + presence handlers, teardown binding to w1's registry, the revision counter + advancing events, `message_ref` issuance (minting) in the gateway handlers, the request-scoped instrument envelope, publication of the exclusion-gate decision in the unified stricter condition (staging-exclusion AND backup-skip, `git check-ignore`-equivalent, evaluated in-process, fail-closed to `cache_unavailable`), the `mailErr502` raw-error redaction, the generation/pair-ID implementation (construction per register row 12: generation = a non-secret fingerprint of the canonical pair identity plus the persisted config epoch; pair ID = a random minted 128-bit value stored beside the pair config), removal cascades incl. `email-watch/` exclude + purge (founder Q-B=A), the Windows CI workflow extension (row 23) | Wave A queue rows; code in Wave D; the exclusion-gate decision lands before w2's first write can succeed |
| **w4-features** = ADR-W7–W10 (register rows 8, 22 — those rows define the wire shapes and the token reconciliation; the save service per the ADR's feature-extension assignment, and `BuildReplyRecipients` + the sanitizer property/value table per register R-4's one-implementation rule, which names them in prose only — no register row of their own, grill-2 F-4b) | The save service (`Transfer`), the save-operation-token bounded lookup + prior-receipt reconciliation, sanitized naming, tool adapters/catalog/inventory/defaults, `BuildReplyRecipients`, the sanitizer property/value table — consume-only against w5-integration's gateway files | Feature tests written to the ADR's F2/M-02 rules before the service lands (RED); Wave C |
| **w3** (register row 20; the viewer source union, the resource-policy pass-through and the handoff contract have no register row of their own — their publisher is named in w3's own spec §3.1/US-9, with w4's US-1/US-8 on the feature side; grill-2 F-4a retired the non-existent "w8" label) | SPA presence lifecycle hooks (w3 publishes — row 20: open with panel, close on unmount/pagehide/logout/socket death, a fresh `observer_id` per panel instance), the viewer source union, the resource-policy pass-through, the handoff focus/announcement/keyboard/reflow contract | Presence frames have landed (row 5 — commit `5f23ae8a0`); the rest ships in Wave C |
| Baseline receipts | `baseline-series.md`, `lane-M.md`, `email-loading-rca.md` — every baseline number and missing-series gap | Read-only; never re-measured into this spec |

**File-ownership fence:** this package writes only its own test files (§6 assignments) and this spec. No W0–W10 production file, contract file, or another package's spec is edited by the proof package — overlap is a rule-15 coordination finding, not a merge conflict to resolve quietly. The proof package never creates a stub or a second implementation of a shared interface — the paging/search/stale-409 shapes, the REST `observer_id`, the single-part reader, the MIME classifier, `message_ref` issuance, the instrument emitter, or the raw-error redaction: each names its single publisher above, and a missing publisher is a **blocked** report to team-lead under rule 15 — never a local stand-in (register R-3/R-4 and §5's no-stub rule).

---

## 17. Assembly summary (plan-spec Phase 6)

| Measure | Count |
|---|---|
| User stories | 7 (US-P1…US-P7) |
| Acceptance scenarios | **41** numbered across the stories (40 in the draft — grill-4 m1's miscount, "34" was wrong; this correction adds AC-9) |
| BDD scenarios | **29** (B-P1…B-P29) — Happy 6 · Alternate 5 · Error 11 · Edge 7 (recounted from the scenario headers after the round-2 fix: B-P28 (Error) and B-P29 (Edge) added for grill-2 F-1, dual-label rows counted under their first category; the earlier recount's 6/5/10/6 held for B-P1…B-P27) |
| Machine-verifiable constraints | **31** (MC-P1…MC-P31) |
| Test plan rows | 38 named tests (T1…T38) across instrument/runtime/cache/feature/E2E + 16 UAT rows + 12 mutations |
| Test datasets | 8 (DS-P1…DS-P8) |
| Measurement arms | 6 (M-A…M-F) + 9 judged bars |
| Open questions | 7 (Q-P1…Q-P7): Q-P1…Q-P6 **decided in the correction round** with owners recorded; Q-P7 **open** with its owner named (the architect — the landed contracts wave's disclosed tool-result-schema deferral) (§15) |
| Non-goals | 8 |
| Founder questions opened | 0 (all decisions settled; open questions are engineering coordination for team-lead) |

---

## 18. Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| The gateway log times no mail operation; every existing Mail timing is click-to-screen from lane-M | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/baseline-series.md` §1, §3 (628,457-line scan; per-operation counts; zero success-timing lines) | Verified (the receipt's scan); not re-run in this task |
| Mail handlers log failures only in this checkout | `pkg/gateway/rest_mail.go::mailErr502` — `logsafeError("rest: mail upstream failure", …)` is the sole upstream-outcome call (read this session) | Verified |
| The fake-IMAP harness and the three missing-folder regression tests exist as cited | `pkg/email/imapserver_test.go::startMemIMAP` (signature read); `pkg/email/view_missing_folder_test.go` (three `func Test` names read) | Verified |
| Today's coalescing key excludes pair and generation | `pkg/email/mail_budget.go::MailBudgetRequest.flightKey` — `Account + "\x00" + Operation + "\x00" + json(Params)`, empty params opt out (read this session) | Verified |
| Bounds and constants cited | `pkg/email/transport.go::dialTimeout` = 30 s, `::commandTimeout` = 45 s; `pkg/email/view.go::maxViewPartBytes` = `25 << 20`; `pkg/gateway/mail_preview_token.go::MailPreviewTokenTTL` = 15 min (all read this session) | Verified |
| Panel query retry asymmetry and 30 s refetch | `src/components/workspaces/mail/MailPanel.tsx::FOLDERS_REFETCH_MS`/`::SUMMARY_REFETCH_MS` = 30 000; `retry: false` on folders/messages/detail; `src/lib/queryClient.ts::shouldRetryQuery` (read this session) | Verified |
| Typed busy/backoff mapping exists | `pkg/gateway/rest_mail_budget.go::mailBudgetWrap` + generated `MailUnavailableError` `backoff`/`busy` codes (read this session) | Verified |
| E2E harness files exist | `tests/e2e/setup.ts`, `tests/e2e/shards.json`, `tests/e2e/fixtures/gateway-process.ts` (listed this session) | Verified |
| ADR bars, founder answers, corrections I-01…I-06/M-01/M-02 and work-package ownership | `docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md` (full 807-line read); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` (full read) | Verified (as design record — targets, not results) |
| Symbols cited "per ADR" but not re-read this session | `pkg/email/view.go::ReadView/ResolveRef/MarkSeenIn/fetchMailRows/SanitizeAttachmentName`; `pkg/email/watcher.go::WatcherBackoff`; `pkg/gateway/rest_settings.go::createTarGz`; `pkg/credentials/store.go::Store.DeriveSubkey`; `pkg/library/transfer.go::Root.CreateUnique`; `pkg/gateway/embed.go::spaBaseContentSecurityPolicy`; `src/lib/url-safe.ts::isDisplayableImageSrc`; `pkg/config/defaults.go::defaultToolPoliciesGeneral`; `pkg/config/validate.go::ReconcileToolPolicyCeiling`; `pkg/tools/compositor.go::resolveEffectivePolicyWith`; `pkg/tools/auto_approve.go::autoApproveClasses`; `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` — each carries the ADR's evidence label (E-* rows) | Inferred from the ADR's verified reads; not re-read here — flagged so reviewers know which citations are second-hand |
| All proposed filenames (test files, fault wrapper, cache path examples) are future work, not existing files | §6/§9 naming labelled "assignments"; target spec path confirmed absent before writing (`ls` exit non-zero for the spec path pre-creation) | Verified (absence check) |
| Correction-round inputs read in full | `docs/internal/specs/mail-live-access-landing-order.md` (24-row §2 register, §5 deadlock ruling, §6 founder questions); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-4.md` (C1–C3, I1–I6, m1–m5 with evidence tables); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md` (correction-round input section, Q-A–Q-E); the ADR re-read at its freshness-metadata, wire-impact, measurement and work-package rows | Verified |
| Mutation re-points cite tests that exist in a published spec | `docs/internal/specs/mail-live-access-w2-discovery-and-cache-spec.md`: `TestDiscovery_ProbeTimeoutIsUnknownNotAbsent` + `TestDiscovery_NoCandidatesIsUnknownNeverAbsent` (FR-W2-8 row / CX-1) and `TestFolderCounts_UnknownRoleHasNullableTotal` (existing-pack extension row) — grepped this session, on this branch's w2 correction | Verified |
| No personal machine-setup reference remains in this spec | A case-insensitive grep over this spec for the personal-setup artifacts the founder ruling names (`omnipus-agent-os`, its auto-commit job, its backup remote, gitleaks, launchd) returns **exactly two matching lines — this row's own enumeration and §15 Q-P3's own negation**: only the sentences that state the artifacts appear nowhere. The round-2 fix (grill-2 F-6) rewrote this row because its earlier "zero matches" wording was defeated by its own enumeration; the substance was never in doubt — no dependency reference exists outside those two negations | Verified (grep re-run after the final edit → 2 hits, both self-negations) |
| Landed contract shapes match this spec's named fields (round-2 reconciliation, commit `5f23ae8a0`) | Read in this tree this session: `contracts/components/schemas/MailFolder.yaml::total` (integer, no `nullable` — contracts check F2) and `::uidvalidity` (`nullable: true`), `MailMessageSummary.yaml::date` + `MailMessage.yaml::date` (no `nullable` — F3), `MailReadMetadata.yaml::source` (4 values, Phase-1 scoping in the description), `MailUnavailableError.yaml::reason` (exactly the 4 register values), `MailFolder.yaml::mapping_source` (5 values incl. `saved`), `LibraryEntry.yaml::preview_profile`/`::preview_scripts_allowed`, `MailAttachmentSaveRequest.yaml::save_operation_token` (required), `MailAttachmentContentSource.yaml::byte_url`, `MailStaleReferenceError.yaml` and `MailMessagePage.yaml` present; `grep -c observer_id contracts/openapi.yaml` → 2; `grep -c MailPanelObserver contracts/asyncapi.yaml` → 21; no mail tool-result schema landed (`grep -rl email_attachment contracts/` → `openapi.yaml` + `MailAttachmentSaveResponse.yaml` only; `ToolResultProjectionFrame`/`ToolResultRef` are the pre-existing generic ADR-066 shapes) | Verified |
| No §6 test is blocked by the deferred shapes | The two dependent assertions are stated red-until-amendment in the text (§6.4 T32's `date` null-shape assertion; M-α10's unknown-count test via §4.4's W0 row); no §6 test binds a tool-result schema field | Inferred (from the §6 trace cells, all read — T29/T33 assert tool behaviour through the tool layer, not wire schemas) |
| Founder decisions not reopened | Q1=A/Q2=B/Q3=A/Q4=A/Q5=A and the correction-round rulings Q-A–Q-E applied as settled; no row of §15/§16 contradicts any of them | Verified |
| No measurement, test run, or benchmark performed by this task | Only reads, this spec's writes, and git commits; no Go/vitest/Playwright process started | Verified (session scope) |
| **Self-check** (correction round) | Re-read the corrected spec end-to-end against the dispatch's six ordered instructions: (1) every consumed interface names its single landing-order-register publisher (§4.4, §16.2) and the published shape (§6.1) states its exact field list and freeze point — no stub or second implementation created anywhere; (2) grill-4 Criticals applied: C1 (row-17 emission split + joiner rule + Wave E gate), C2 (REST `observer_id` consumed via W0/w5-integration, M-F prerequisite), C3 (single-part reader/MIME classifier consumed from w2 — T33/T34/T36 rows), each with its required test or counterexample; grill-4 Importants applied: I1 (MC-P4/T4/`shared_flight`/DS-P2), I2 (B-P3/T8/DS-P2 arithmetic — eight counted actives), I3 (M-α5/M-α10 re-pointed to w2's named tests + blocked-not-skipped rule), I4 (M-A/M-C build markers), I5 (MC-P31 + B-P27 + US-P4 AC-9 + T36 re-point), I6 (header map + §16 labels); grill-4 Minors applied: m1 (34→41), m2 (clock (c)), m3 (B-P15 premise), m4 (MC-P3/US-P1 AC-3/§6.1 scoping), m5 (25 MiB); (3) founder rulings applied and nothing reopened: Q-A (product-owned exclusion — every external-staging-job reference rewritten, zero machine-setup references remain per the re-run grep), Q-B (`email-watch/` exclude+purge in MC-P18/B-P15/T25), Q-C (stale-gating in U-2/M-F), Q-D (search fields in MC-P16/T23/M-D/mail.md TODO), Q-E (temporary-preview-only policy in B-P17; saved HTML keeps Q5=A's scripts-off + per-file checkbox); (4) §15's six correction-round questions decided with owners (Q-P7 was added later by the round-2 fix — see the round-2 self-check row below), §16 PUBLISHES/CONSUMES rewritten with publishers and freeze points, §17 counts corrected, this evidence table updated to match the corrected text. Forbidden moves checked: no test weakened, no limit widened (25 MiB, 5 s, 45 s, 50 headers, 4 MiB, 8 sockets all as the ADR set them), no stub type, no silent scope change — every correction is visible in the spec text, and the only file this package touched across all its commits is this spec (`git show --name-only` per commit; the branch also carries other packages' interleaved correction commits, which are theirs). Commits: the correction round landed as one single-file commit per corrected section on `docs/adr-mail-live-access` (section commits `998ecab99`, `4725f910b`, `862ad3f3a`, `789674c6d`, `84204f400`, `167f87eb3`, `86933709a`, `4d19c16dc`, plus the final-sweep fixes that followed), each authored and committed solely as Daniel Piatkowski's required no-reply identity, no `Co-Authored-By` trailers (verified per commit via `git log --format='%an <%ae>'` and `%(trailers:key=Co-authored-by)`); `git diff --check` clean | Verified |
| **Self-check** (round-2 fix) | Re-read the spec's changed sections after every edit and re-ran the check each finding required: (1) **F-1** — recounted §5 by hand: 29 scenarios, B-P1…B-P29, Happy 6 · Alternate 5 · Error 11 · Edge 7 (dual-label rows counted under their first category, same rule as the prior recount); walked the §12 footer's "every" claim row by row — MC-P26→B-P28→T33 and MC-P28→B-P29→T37/M-A…M-F now have real scenario cells, the other 29 MC-P rows re-checked unchanged; §17 re-derived (41 ACs, 38 test rows, 8 datasets, 8 non-goals all unchanged by this round). (2) **F-4** — the imprecise source-scoping citation (rows 2 and 3): 0 remaining hits; the retired w3 publisher label with its stray second package letter: 0 remaining hits — this self-check row deliberately names no literal search pattern, so each stated check stays re-runnable without self-matching; the remaining uppercase W-8 occurrence is §14's ADR-work-package sentence ("implementing leads (W3/W4/W8)"), a different vocabulary from the register's file labels (w1…w6 + W0) that the grill's finding did not name. (3) **F-5** — the authoritative-filename note added at §6's head; the w1-side filename row routed to the w1 author. (4) **F-6** — the evidence row now states the literal result; re-ran that row's own artifact grep (its pattern is printed in the row above, deliberately not here, so this self-check cannot add a hit) after the final edit → 2 matching lines (the evidence row's enumeration and §15 Q-P3's negation), matching its claim exactly. (5) **Contract reconciliation** — every "landed" claim written only after reading the schema in this tree (see the landed-shapes evidence row above); the two disclosed nullability deferrals and the tool-result half are stated as deferrals with their dependent tests' red state, never described as landed; commit `5f23ae8a0`'s unpushed-branch hold (contracts check F1) recorded in §16.2's W0 row. (6) **w2 CRIT-1** (the dispatch's Critical item) names w2's §3.13/§2.1/§4.1 — no w6 section is implicated; this spec's search statements (MC-P16, T23, M-D, §14) already match founder Q-D=A and register row 4's publisher split, so nothing here contradicts either scoping settlement; the w2 fix is another writer's file and is not touched from this dispatch. Forbidden moves checked: no test weakened, no limit moved (25 MiB, 5 s, 45 s, 50 headers/role, 4 MiB, 8 sockets, 25/200 — all as the ADR and Q1/Q3=A set them), no stub type, no silent scope change; every fix is visible in the spec text; the only file this round's commits touch is this spec (`git show --name-only` per commit), each authored solely as Daniel Piatkowski's required no-reply identity with no `Co-Authored-By` trailer (grep per commit → 0) | Verified |
