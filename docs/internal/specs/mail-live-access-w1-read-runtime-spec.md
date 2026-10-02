# Implementation Specification: Mail live access — W1 read runtime (pooled connections, shared operation budget, watcher)

- **Status:** Draft for the plan-spec grill — derived from the approved design record **Mail live access: pooled connections, folder discovery and a bounded cache** (`docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`, grill report `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` applied; founder answers Q1–Q5 recorded). This spec is the **W1 work package only**: the pooled IMAP read runtime, the shared operation budget's identity/ordering corrections, and the watcher's relationship to both.
- **Date:** 2026-10-02
- **Work package:** W1 (read runtime) of the ADR's work-package table. Exclusive W1 production files: `pkg/email/transport.go`, `pkg/email/mail_budget.go`, `pkg/email/watcher.go`, `pkg/email/watcher_set.go`, proposed `pkg/email/pool.go`.
- **Companion specs:** W0 (contracts), W2 (discovery/metadata — owns all `pkg/email/view.go` changes), W3 (panel), W4 (gateway/agent integration), W5 (tests) are separate specifications. Interfaces W1 publishes to them are frozen in §3 so the packages can be built in parallel without editing each other's files.
- **Every code claim below was verified by reading the file in this checkout** (`wt-adr-mail` at `fc4c8bf6d`, branch `docs/adr-mail-live-access`) unless labelled otherwise. GitNexus was not available in this worktree (no `.gitnexus/` index) — impact rows are first-hand Read/Grep sweeps labelled **Inferred**, per `omnipus-shared-rules` rule 9. No test was run and no measurement was taken by this spec.

---

## 1. Summary and scope

**Bottom line:** today every mail read dials the server from scratch — TLS handshake, login, SELECT INBOX, command, close — on every click, for the panel, the agent tools and the watcher alike (`pkg/email/transport.go::Client` is documented "connectionless between calls"). This work package puts **one application-owned connection manager** below that client facade, so repeated reads reuse a healthy authenticated session instead of repeating setup; it caps the pool at **two sockets per mailbox and eight open globally** (counting connecting reservations, not just established sockets); it gives every operation an **exclusive lease** on its socket so one request's selected folder can never leak into another's; it bounds every read to **45 seconds total** with a **5-second** maximum wait for pool capacity and the existing **30-second** dial ceiling subordinate; and it re-worked the shared two-per-account operation budget so that identical reads **coalesce only when they truly mean the same thing** (same agent/workspace pair, same configuration generation, same operation, same normalized arguments, same live-versus-cache purpose — grill finding I-01) and a read that was superseded by a newer **publication revision** publishes nothing anywhere (grill finding I-02). The watcher keeps its independence and its cadence but gains bounded fair scheduling under the same caps. **Phase 1 is plain IMAP only; JMAP is out of scope for W1 entirely.**

In scope (all in `pkg/email/` plus its injected seams):

- **The pool manager** (new `pkg/email/pool.go`): socket ceilings, reservation accounting, exclusive mailbox leases, idle expiry and LRU eviction, poisoned-connection retirement, panel-observer presence hooks, and the publication-revision capture seam.
- **The client facade rewiring** (`pkg/email/transport.go`): the client stops dialing per call and borrows sessions from the injected manager instead. A REST-created or tool-registered client **cannot** construct a private pool — the manager is a process-singleton injected at the three production construction sites.
- **The budget's identity and ordering corrections** (`pkg/email/mail_budget.go`): the read-coalescing identity (I-01), the total-read-deadline plumbing, and the revision capture — the two-slot per-account semaphore, backoff gate and singleflight structure stay.
- **The watcher's relationship to the pool** (`pkg/email/watcher.go`, `pkg/email/watcher_set.go`): bounded fair scheduling (replacing the sequential loop), at most one cycle in flight per mailbox, non-blocking acquisition so watcher work never displaces foreground reads, no socket retention and no panel-cache fill while no panel is open, and a skipped cycle leaving the last-checked time unchanged.
- **Wiring seams consumed by W4** (gateway REST, agent tool registration, watcher provider): W1 publishes narrow interfaces; W4 injects the shared instances. W1 does not edit `pkg/gateway/` or `pkg/agent/` (W4's files).

Out of scope for this spec (owned elsewhere, interfaces frozen here):

- Folder discovery, the folder-mapping cache, header cache, and **all** changes to `pkg/email/view.go` (W2). W1 publishes the lease/session interface W2's file will consume; W2 never edits `pkg/email/transport.go`.
- Wire contracts (`contracts/`), generated types, the presence WebSocket frames, and the typed 503 `reason` field (W0 defines; W4 consumes).
- The gateway/agent injection call sites themselves, boot/reload wiring, removal cascades (W4).
- All test files (W5 owns them; §8 names them and what each must prove).
- SMTP — stays request-scoped, outside the read pool, unchanged.
- JMAP transport selection, encrypted disk header cache, Phase 2 anything (W6/later waves).

<!-- W1-SPEC-CONTINUES -->
