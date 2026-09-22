# Stage B closure — founder summary

**Date:** 2026-09-20 · **Prepared by:** the lead (the founder asked the lead to approve the ledger and report on one page)

## What this means

Every design-system problem the scanners find in the app is now accounted for. The final audit passes with zero errors. Nothing is hidden: each finding is either owned debt with a fix batch, an approved exception with a written reason, or a test or story file under a reviewed boundary. The UI migration (C1) can start once you give the go-ahead.

| Where each finding went | Count |
|---|---:|
| Owned debt, each with a repair batch (C1–C6) and an owner | 3,451 rows |
| Approved exceptions (exact file, rule and code, each with a reason) | 431 rules, covering 480 occurrences |
| Test and story files (reviewed boundary; test styling never ships) | 389 occurrences |
| Findings the scanners could not classify ("unsupported") | **0** (172 when this stretch began) |

The 3,451 debt rows are the 3,447 built from this audit plus 4 calendar token references approved earlier and carried over unchanged.

## Approved exceptions, by category

| Category | Rules | Files | What it covers |
|---|---:|---:|---|
| Caller pass-through | 391 | 67 | A component forwards a class or style its caller wrote, unchanged. The value is checked where the caller writes it. |
| User-authored colours | 31 | 21 | An agent's chosen colour, used only as an avatar or chip background, icon or text colour, or a tint of it. Never status or control chrome. |
| Third-party widget values | 8 | 4 | Values a library hands back to us: the date picker's chevron slot (a source repair was tried and rejected because it dropped the chevron style), the calendar's event colours (a repair was ruled out because it would replace the library's own event colouring), and the graph edge style (approved in the earlier registry). |
| User-adjustable root font | 1 | 1 | The profile font-size setting written to a page-wide variable. **Needs your confirmation** — see below. |

Of the 431 rules, 375 were proven by a script that traces each value from its source to where it is used, 36 were read by the lead with the evidence line recorded, and 20 were carried over from the registry the lead approved earlier. Six earlier rules no longer matched anything and were dropped. Status-colour chrome was refused as an exception and repaired at the source instead.

## Decisions for you to confirm

1. **"User-adjustable root font" is not one of the six approved categories.** It covers one file: the profile font-size setting. It is closest to "live layout measurement", but it is a user preference, not a measurement. Options: approve it as its own category, fold it into live layout measurement, or make it debt to fix in C1.
2. **Brand logos are debt, not exceptions.** The Omnipus logo's Forge Gold and 12 provider logos' `line-height: 1` are owned debt in C1. If the logo's own colour should never change, a "brand asset" exception category would be the cleaner answer.

## Ownership decisions the lead made

58 items had no owner. All are assigned to C1, because their debt is the 12px floor, the spacing scale and colour tokens, whose locks switch on at the end of C1:

| Decision | Files | Items |
|---|---|---:|
| Shared UI feedback and picker primitives | AutoSaveIndicator, FormError, error-boundary, model-selector, toast-container | 40 |
| Brand logo assets | 12 provider logos, 2 Omnipus logos | 14 |
| Tree depth indentation (one shared pattern) | FileTreeView, Sidebar, KnowledgeOutline, SearchModal | 4 |

---

## Debt ledger in detail

**Generated:** 2026-09-19T19:23:10.591Z
**From:** 3447 ledger entries, 0 blocking findings, and the unresolved-item breakdown below — all read from this run's proposal-ledger.json, proposal-blocking.json and proposal-stats.json.

---

### The Numbers

| What | Count |
|---|---:|
| Ledger entries (repairable debt, has an owner) | 3447 |
| Blocking findings (never enter the ledger — see "Blocking findings" below) | 0 |
| Unresolved items (not yet owned — see "Unresolved, by bucket" below) | 0 |

---

### By Repair Checkpoint

Each ledger entry carries a repair-batch label (`expiryCheckpoint` in ledger.schema.json). The batches run in the fixed order C1 → C6; this summary states the count in each batch only, not a date any batch is due.

| Repair batch | Entries |
|---|---:|
| **C1** | 3265 |
| **C2** | 133 |
| **C5** | 49 |

---

### By Lane Ownership

Each row is a lane (or the lead, for shared foundations) named in the ledger entries' own `owner` field.

| Lane | Entries |
|---|---:|
| **Lane 1** | 512 |
| **Lane 2** | 133 |
| **Lane 3** | 427 |
| **Lane 4** | 523 |
| **Lane 5** | 456 |
| **Lane 6** | 785 |
| **Lane 7** | 157 |
| **Lane 8** | 109 |
| **Lead—shared-foundation** | 345 |

---

### By Rule Family

The part of the rule id before the `/` (e.g. `spacing`, `typography`). Entries in the same family are usually fixed with the same kind of change.

| Rule family | Entries |
|---|---:|
| **controls** | 210 |
| **css-colors** | 30 |
| **design-system** | 31 |
| **spacing** | 2171 |
| **ts-colors** | 376 |
| **typography** | 629 |

---

### Unresolved, By Bucket

"Unresolved" means the item has not been given an owner yet — it is neither in the ledger above nor in the blocking list below. There are 0 unresolved items, split three ways:

| Bucket | Count | What it means |
|---|---:|---|
| Runtime extension boundaries awaiting registration | 0 | The colour, spacing or type value is computed or passed in while the app is running (for example, forwarded from another component), not written directly in the file. The automatic scanner cannot check a value it cannot see in the source, so each one needs a person to confirm it fits an approved exception category and add it to a central approved list before it counts as resolved. |
| UI catalog, no lane owner yet | 0 | The file is a shared building block already listed in the UI catalog, but no lane currently owns it. |
| Unassigned | 0 | The file has neither a lane owner nor a UI catalog entry, so nobody is currently responsible for it. |

**0% of the unresolved items (0 of 0) are runtime extension boundaries** — the blocking findings below are a separate, much smaller problem.

---

### Blocking Findings

Blocking findings are parser errors and syntax the scanner does not understand yet. They never enter the ledger or the baseline (fail-closed: design-system/enforcement/contract.json), and they stay blocking until either the scanner learns to prove them or the source is rewritten, without any visible change, into a form it can check.

**Two different counts appear for blocking findings, because they count two different things:**

- **0 occurrences** — one row per blocking finding in proposal-blocking.json. The same rule/file/pattern can appear more than once (for example, twice on one line), and each appearance is its own row.
- **0 unique identities** — the same 0 rows, but counting each distinct (rule, file, pattern) combination only once.

0 of those 0 identities show up more than once, adding 0 extra occurrence(s) — which is why 0 (occurrences) and 0 (identities) are both correct, for different questions ("how many rows to fix" vs. "how many distinct patterns to fix").

---

*End of summary*
