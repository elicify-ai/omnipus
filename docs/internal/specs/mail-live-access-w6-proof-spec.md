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
