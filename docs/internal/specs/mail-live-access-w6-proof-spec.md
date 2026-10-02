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
3. **Given** a cache-first read that hits, **When** the record is emitted, **Then** `source=memory` (or `encrypted_disk`), `hit=true` and `socket_count=0` — a cache hit never dials, and the record says so.
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
7. **Given** a cache write, **When** the data folder's Git status, the autocommit staging rules and the application backup archive are inspected, **Then** the cache file is untracked, unstaged and absent from the archive — with an ordinary allowed state file as positive control proving the inspection could have seen a write.

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
| MC-P1 | Every mail operation emits exactly one record with fields `operation` (closed enum), `pair_ref` (opaque pair id + config generation), `source` (`live|memory|encrypted_disk|none`), `hit` (bool), `duration_ms` (int > 0 on completion), `acquire_wait_ms` (int), `socket_count` (int), `outcome` (`ok` or safe class), optional `rows` (int) and optional `revision` (opaque publication-revision id) | P-inst-1…P-inst-3 |
| MC-P2 | No record field contains a message subject, an email address, a folder name, a Message-ID, a credential value, a full URL or a raw upstream error string — proven with distinctive synthetic markers plus a positive control | P-inst-2 |
| MC-P3 | A cache-first hit record shows `hit=true`, `source∈{memory,encrypted_disk}`, `socket_count=0`; a live record shows `hit=false`, `source=live`, `socket_count≥1` | P-inst-3 |
| MC-P4 | Sum of records' socket acquisitions equals the fake server's accepted-connection counter delta over the same window | P-inst-4 |
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
| MC-P16 | Reusable headers ≤ 50 per role (150/mailbox); active-view pages never enlarge the cache; search runs live; 4 MiB global reusable-metadata overflow → visible cache-unavailable outcome, never truncation or silent omission | P-ca-5 |
| MC-P17 | Memory headers drop 30 minutes after the panel was last open (fake clock); a reopen inside 30 minutes retains them; drop issues zero IMAP commands | P-ca-6 |
| MC-P18 | A cache write leaves the data folder's `git status --porcelain` unchanged, adds nothing to the autocommit staging set, and appears in no backup archive member — with an allowed state file as positive control | P-ca-7 |
| MC-P19 | Open (temporary preview) performs zero attachment-file, directory, spool or persistent-byte writes — filesystem snapshot + Git status identical before/after — while a positive-control Save shows a write; responses carry `Cache-Control: no-store`; token/preview/inline caches hold no body or part bytes (today's 15-minute `MailPreviewTokenTTL` body retention must be gone) | P-ft-1 |
| MC-P20 | The temporary mail source's renderer resource policy refuses every sender-authored reference except the source's own minted resources and consent-gated token-scoped proxy images — same-origin Library/API paths, workspace embeds and remote URLs each produce zero requests, with the workspace-file positive control proving the observer | P-ft-2 |
| MC-P21 | Save lands only under `mail/<mailbox-label>/<UTC save-month>/` with the sanitized unique name (existing sanitizer + Library validation + `CreateUnique` numbering, final candidate validated); refusals (parent-file conflict, symlink/mount escape, disk full, vanished part) are visible with safe reasons; exact bytes saved | P-ft-3 |
| MC-P22 | Agent save tools ride the two-layer policy: shipped ceiling literal `list_email_attachments: allow`, `read_email_attachment: allow`, `download_email_attachment: ask`; `ReconcileToolPolicyCeiling` adds missing keys without overwriting operator values; effective policy per role grants list/read as that role's `read_message` and download `ask`; Auto-on uses the existing workspace-path conditional class (Q4=A); declined/failed approval → zero transfer and zero write | P-ft-4 |
| MC-P23 | Reply-all recipient set from the fixture (From A, Reply-To R, To = self+X+dup-R, Cc = Y+mixed-case-X+self+display-name-dup-R, hidden Bcc) is exactly To=[R], Cc=[X, Y]; plain Reply To=[R], Cc/Bcc empty; asserted on the final send payload shape | P-ft-5 |
| MC-P24 | Styled mail: computed style keeps safe colour/font/table/media-query presentation; scripts, event handlers, forms, `@import`, remote `url()`, `position:fixed`/`absolute`, `behavior`/`expression()` produce zero effects and zero default remote loads; Load-images loads only token-scoped proxy resources after consent; stripped fallback readable with one plain notice | P-ft-6 |
| MC-P25 | Date precedence Date → internal date → null renders as **No date** in list, detail, Sent and reply attribution; never year one, epoch, today or a list/detail discrepancy | P-ft-7 |
| MC-P26 | The agent reference journey (no Message-ID anywhere) succeeds end-to-end from returned references; wrong-pair, old-generation and old-epoch references each refuse with the typed stale-reference error before fetch/mutation | P-ft-8 |
| MC-P27 | Every measurement sample carries both clocks (click→rendered, request-start→response-end), status, rows, n; cached-display and live-fetch columns never mix; invalid-run conditions (§8.3) discard and re-run | P-ms-1…P-ms-3 |
| MC-P28 | Bars judged as stated (Q3=A): 5 s max acquisition inside 45 s read budget; 4 MiB metadata budget; cached display median ≤ 250 ms / p95 ≤ 500 ms; warm-live ≥ 50 % paired-median reduction where eligible; cold ≤ 10 % and body-open ≤ 10 % paired-median regression; summary p95 ≤ 1 s request-to-render with zero mail calls; busy within 5 s | P-ms-4, §8.4 |
| MC-P29 | UAT live-instance rows carry their named screenshots; prohibitions (no send, no delete, no key change, no server folder create/rename, no epoch-inducing op) hold; subjects/addresses redacted in receipts | P-uw-1, P-uw-2 |
| MC-P30 | Every mutation in §7, applied alone to a green implementation, makes its named test fail; a surviving mutation is a CHECK BLOCK | P-ck-1 |

### 4.4 Integration boundaries

| External system / producer | What this package consumes | Failure behaviour when unavailable |
|---|---|---|
| W0's generated contracts (`MailReadMetadata`, `publication_revision`, `mode=cache_first|live`, `MailUnavailableError` reasons, tool result shapes) | Tests bind to generated types only — never hand-written parallels | Tests stay red until contracts land; that is the correct RED state, not a workaround trigger |
| W1's pool/budget interfaces | The injected manager seams the runtime tests drive | Tests written against the frozen interface shapes from §17's PUBLISHES/CONSUMES exchange; interface drift = coordination finding, not a test edit |
| W2's cache envelope + `Store.DeriveSubkey` | Envelope round-trip/corruption tests | Same red-until-landed rule |
| W4's gateway wiring, preview endpoints, exclusion of the cache dir | Endpoint tests, exclusion test | The exclusion test stays red until W4 lands the exclusion — that red gates disk-cache activation (landing blocker, not a skipped test) |
| W7's save service, W8's viewer source union, W9's sanitizer table, W10's tools/catalog | Feature journey tests | Feature tests red until each lands; sequenced per the ADR (date/reply wins first, then attachments, CSS after security review) |
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
- **Given** eight leases held active by stalled (never-completing) fake-server responses and one reservation in the connecting state counted as active
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

#### Scenario B-P15: A cache write is invisible to Git and backups
**Traces to**: US-P3 AC-7 · **Category**: Edge Case
- **Given** the data folder under Git with the autocommit job's staging rules and the application backup walker
- **When** a cache write lands
- **Then** `git status --porcelain` is unchanged, nothing is staged, and no archive member contains the cache file — while the positive control (an ordinary allowed state file) **is** staged and archived, proving the inspection could see a write
- **And** until W4 traces and excludes the real deployed autocommit job, this test stays red — that red gates disk-cache activation

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

---

## 6. TDD plan (tests designed before implementation)

Naming conventions observed in this tree: Go RED files carry a `_red_test.go` suffix (e.g. `pkg/email/mail_budget_red_test.go`); frontend component tests are `*.test.tsx`; Playwright specs are `*.spec.ts` assigned to exactly one shard in `tests/e2e/shards.json`. All filenames below are **assignments** (the ADR leaves exact test filenames to this spec), and all are **test files** — the proof package writes nothing else. Every test is derived from the ADR's design rules, never from an implementation's current behaviour: a test whose expectation was read off the code it tests is an oracle violation, not a proof.

### 6.1 The instrument, specified first

**Problem:** mail operations are timed nowhere (§2.1) — not in the gateway log (failure counts only), not in tool results, not in the SPA. A measured comparison needs one record per operation, emitted by production code, with fields that cannot leak content.

**Record shape (normative — production owners W1/W2/W4 implement; W0 contracts nothing here, this is in-process/structured-log, not wire):**

| Field | Type | Rule |
|---|---|---|
| `operation` | closed enum | `folders`, `list`, `open`, `summary`, `discovery`, `attachment_metadata`, `attachment_read`, `attachment_save`, `seen` |
| `pair_ref` | opaque string | The pair's opaque id + its configuration/folder-mapping generation. Never an email address, host:port with username, or folder name |
| `source` | enum | `live` \| `memory` \| `encrypted_disk` \| `none` — mirrors the contract's `MailReadMetadata` source vocabulary |
| `hit` | bool | Cache hit (`true`) or miss (`false`); a hit never dialed |
| `duration_ms` | int | Operation end-to-end; > 0 on completion |
| `acquire_wait_ms` | int | Queue/acquisition time inside the read budget — the 5 s wait is measured here, not inferred |
| `socket_count` | int | Sockets/leases this operation used (0 for cache hits) |
| `outcome` | enum | `ok` or a safe failure class from the existing closed class set — never a raw upstream error string |
| `rows` | optional int | Row/message count where meaningful |
| `revision` | optional opaque string | The publication revision the read captured — makes a superseded-publish diagnosable from receipts |

**Emission rules:** emitted on **success and failure** alike (the inverse of today's failure-only logging); one record per operation; delivered through an injected sink interface (tests inject a recording sink) and mirrored to the structured gateway log under a dedicated message key so measurement receipts are auditable from logs. No new polling, no sampling timer — the record is written when the operation ends.

**Instrument truth tests — what a test must prove so a silent instrument cannot produce a false green:**

| Order | Test name | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 1 | `TestMailInstrument_SuccessPathEmitsRecord` | `startMemIMAP` + injected sink | B-P1 | The success-path gap is closed: an open (and each other operation) emits exactly one complete record — the test fails today's failure-only logging shape by construction |
| 2 | `TestMailInstrument_NoSensitiveMarkers` | Unit (sink capture) | B-P2 | With marker subject/address/folder fixtures, zero markers in any field; the positive control asserts the markers ARE in the mail data, proving the scan works (trap 8 of the false-green doc: a probe without a control proves nothing) |
| 3 | `TestMailInstrument_HitMissLabels` | `startMemIMAP` + warm cache | B-P1, (MC-P3) | Cache hit → `hit=true`, non-live source, `socket_count=0`; miss → `hit=false`, `source=live`, `socket_count≥1` |
| 4 | `TestMailInstrument_SocketCounterAgreement` | `startMemIMAP` (counter) | (MC-P4) | The records' socket acquisitions sum to the server's accepted-connection delta — the instrument agrees with an independent counter |
| 5 | `TestMailInstrument_DurationReflectsInjectedDelay` | `startMemIMAP` (fault hold) | (MC-P1) | A scripted 2 s server hold appears in `duration_ms` within tolerance — the field measures the operation, not a constant |
| 6 | `TestMailInstrument_DisabledProducesNoRecords_AndMeasurementRejects` | Unit + harness rule | (US-P1 AC-6) | Sink absent → no records; the measurement harness treats zero records for an exercised operation as an invalid run |

### 6.2 Runtime tests (pool, leases, coalescing, publication)

Harness note: `startMemIMAP` restores the `imapDial` seam and its dial seam is global — runtime tests must not run in parallel (existing test-suite constraint, verified file). A **test-only** fault-injection wrapper is added — proposed `pkg/email/imapserver_faults_test.go` — providing scripted command holds, stalled greeting/TLS/login, BYE injection, connection-limit replies and per-pair result markers. It is test code only; no production flag, hook or branch may exist for it (repo integrity rule).

| Order | Test name (file) | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 7 | `TestPoolCaps_TwoPerMailboxEightGlobalUnderConcurrency` (`pkg/email/pool_caps_red_test.go`) | `startMemIMAP` + faults | B-P3, (MC-P5) | 13 pairs × concurrent panel/watcher/tool work: accepted-connection counter never exceeds 2/mailbox or 8/global, **counting connecting reservations**; the counter — not returned rows — is the oracle |
| 8 | `TestPoolCeiling_TypedBusyNoNinthDial` (same file) | `startMemIMAP` + faults | B-P3 | Eight stalled leases + a connecting reservation → the ninth request gets typed `pool_busy` within the 5 s wait; server counter shows no ninth dial |
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
| 23 | `TestCacheBounds_FiftyHeadersFourMiB` (`pkg/email/cache_bounds_red_test.go`) | `startMemIMAP` | B-P14 | 500-message folder → exactly 50 reusable/role; active view doesn't grow the cache; search ran live (server command counters); 4 MiB overflow → visible cache-unavailable, no truncation/omission |
| 24 | `TestCacheRetention_ThirtyMinutePostCloseDrop` (`pkg/email/cache_bounds_red_test.go`) | Unit (fake clock) | B-P14 | 30 fake-clock minutes after panel close → memory headers gone, zero IMAP commands during the drop; reopen inside 30 min retains |
| 25 | `TestCacheExclusion_GitStatusAndArchiveUnchanged` (`pkg/gateway/mail_cache_exclusion_red_test.go`) | Integration | B-P15 | Cache write → data-folder `git status --porcelain` unchanged, staging set unchanged, `createTarGz` archive contains no cache member; positive control (allowed state file) IS staged/archived. **Stays red until W4 lands the exclusion and traces the real autocommit job — that red is the activation gate, never skipped** |

### 6.4 Feature tests (F1–F7 journeys and the grill corrections)

| Order | Test name (file) | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 26 | `TestTemporaryOpen_WritesNothing` (`pkg/gateway/mail_preview_nowrite_red_test.go`) | Integration + fs snapshot | B-P16 | Every renderable kind opened then dismissed by all five exit paths → filesystem + Git status identical; mid-stream close and mid-preview mailbox removal included; token dead after exit; positive-control Save shows exactly one write; `Cache-Control: no-store` asserted; today's 15-minute body retention in `mail_preview_token.go` must be gone (its retention test, if any remains, must fail) |
| 27 | `TestTemporarySource_ResourcePolicyRefusals` (`src/components/library/preview/__tests__/mailAttachmentPreviewSource.test.tsx`) | Component (request counter) | B-P17 | I-04: zero requests for same-origin Library/API, workspace embed, remote; own minted resources load; workspace-file positive control fires the counter; ordinary workspace rendering unchanged |
| 28 | `TestAttachmentSave_ConfinedSanitizedUnique` (`pkg/mailattachment/service_red_test.go` — via the W7 service's test pack) | Integration | B-P18 | Hostile names sanitized + numbered unique under the authorized hierarchy; escape attempts (symlink, mount, parent-file conflict) refused visibly with zero partial files; concurrent panel+agent saves produce two files; exact-bytes positive control; over-cap refuse before any write |
| 29 | `TestAgentSaveAsk_ShippedDefaultAndModes` (`pkg/tools/email_attachments_ask_red_test.go`) | Unit + Integration | B-P19 | Shipped ceiling literal allow/allow/ask; Reconcile adds missing keys without overwriting operator values; effective policy per role matches `read_message` grants + ask; declined approval → zero transfer/write; Auto-on rides the existing workspace-path conditional class (Q4=A); per-agent allow never loosens global ask; result carries the real absolute path |
| 30 | `TestReplyAll_ExactRecipientSet` (`pkg/email/reply_recipients_red_test.go` + `src/components/workspaces/mail/__tests__/MailComposeDialog.reply.test.tsx`) | Unit + Component | B-P20 | One shared helper yields the exact fixture set on the final payload shape (backend) and the compose dialog renders/consumes it without a second algorithm (frontend); plain Reply = To only; quote editable/escaped; stale context can't overwrite another compose |
| 31 | `TestMailCSS_SafeStylesSurviveUnsafeRemoved` (`pkg/email/mailhtml/sanitize_red_test.go` + `mail-html-styling.spec.ts` browser assertions) | Unit + E2E | B-P21 | Computed style keeps safe colour/font/table/media-query presentation; scripts/handlers/forms/`@import`/remote `url()`/`position:fixed`/`expression()` → zero effects, zero default remote loads (controlled endpoints + uncontained positive control); Load-images only via token-scoped proxy after consent; stripped fallback readable |
| 32 | `TestMailDate_MissingNeverYearOne` (`pkg/email/mail_date_red_test.go` + `src/components/workspaces/mail/__tests__/mail-format.date.test.tsx`) | Unit + Component | B-P22 | Precedence Date → internal date → null; **No date** in list, detail, Sent, attribution; never year one/epoch/today; list/detail agree; the generated `date` field is required-but-nullable (a Go zero date serialization fails this) |
| 33 | `TestAttachmentRef_JourneyWithoutMessageID` (`pkg/tools/email_attachments_ref_red_test.go`) | `startMemIMAP` + tools | (MC-P26) | I-03: chain read_message → list → read → download using only returned refs on a Message-ID-less message; wrong-pair / old-generation / old-epoch refs each refused pre-fetch on the acting lease; no test-fabricated refs, no UI side channel |
| 34 | `TestPreviewBytePath_CapAndDownloadSplit` (`pkg/gateway/mail_byte_paths_red_test.go`) | Integration | (MC-P19/21 wire) | I-05: preview-purpose endpoint refuses actual over-cap bytes even when reported metadata lies (below/at/above `maxViewPartBytes` dataset); the browser-Download path streams the same larger part to completion; a mid-stream disconnect/decode failure never satisfies a success assertion on either path |
| 35 | `TestHandoff_FocusAnnouncementKeyboard` (`tests/e2e/mail-attachment-handoff.spec.ts`) | E2E | (MC-P29, I-06) | Keyboard-only Open → viewer → Save (success and failure) → Back; focus lands on the trusted context heading, returns to the originating action or the defined fallback with announcements; context bar reachable at 320 px and 200 % zoom; disabled actions carry the accessible "Save to Library first" explanation |
| 36 | `TestPaperclip_MetadataOnlyIndicator` (`pkg/email/attachment_flag_red_test.go`) | `startMemIMAP` (command capture) | (MC-P21 scope) | F7: `has_attachments` derived from structure metadata — command capture proves no body bytes fetched and no `\Seen` change; CID-only and draft-marker parts produce false; a genuine `message.md` produces true; unavailable part still true; unclassifiable metadata is a visible failure, never fabricated false |

### 6.5 E2E additions

| Order | Test name (file) | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 37 | `tests/e2e/mail-live-access.spec.ts` — full panel journey against the fake IMAP server via the `GatewayProcess` fixture (own port, own `OMNIPUS_HOME`) | E2E | B-P1, B-P13, B-P23 | Cached-first display then live refresh as separate visible phases; stale label + Retry; busy result surfaces; folder rail with counts; assigned to exactly one shard in `tests/e2e/shards.json` (`scripts/e2e-shards.sh check` enforces) |
| 38 | `tests/e2e/mail-attachment-handoff.spec.ts` (T35) + `tests/e2e/mail-temporary-open.spec.ts` — Open/Back/Save/Open-in-Library, no-write browser check | E2E | B-P16, B-P18 | The user journeys in a real browser: Open renders via the Library viewer, Save lands and enables Open in Library, Download stays a browser download; the no-write claim re-verified at the process level (fixture workspace sweep) |

### 6.6 Test datasets

| Dataset | Rows (boundary → edge → error → happy) | Traces to |
|---|---|---|
| DS-P1 Instrument fixtures | marker subject/address/folder strings (positive control present in mail data, absent in records); operations: folders, list, open, summary, discovery, attachment_metadata/read/save, seen; success + one safe-class failure per operation; disabled sink | B-P1, B-P2 |
| DS-P2 Concurrency shapes | 13 pairs × {panel, watcher, tool} reads; 8 stalled leases + 1 connecting reservation; 9th demand; two pairs one account (different Sent mappings); old-generation flight + reconfigured joiner; cancelled only-waiter; detached late completion | B-P3…B-P10 |
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
