# Review — generic install specification (ADR-090 environment setup), 2026-09-18

Adversarial spec review (`grill-spec`, structured-spec mode) of the founder-selected generic
installation direction, as recorded in `.local/adr090/environment-setup-delivery/GENERIC-INSTALL-DECISION.md`,
`adr-090-environment-setup-spec.md`, ADR-090 §6.5, config spec FR-011, and the 2026-09-18
visual-file-reading dependency amendment. Findings are grounded only in the current documents;
the superseded structured-recipe drafts and the earlier Fable review were not used as a basis.
Settled policy (Ask-only approval, no new workflow or God-mode exception, Admin cross-workspace
without membership, workspace default / shared explicit, existing Bash background sessions)
was checked for cross-document consistency and found consistent; it is not re-litigated here.

## Executive summary

**Verdict: REVISE.** 0 CRITICAL, 2 MAJOR, 5 MINOR, 2 OBSERVATION.

The five documents are mutually consistent on the decided policy, and the generic-vs-catalogue
boundary is drawn correctly in the spec text. What blocks implementation are two gaps: the tool
input contract is not enumerated (field list, scope values, and the installation-prefix
environment variable are unnamed — required before the contract-first step the spec itself
mandates), and no acceptance test asserts deferred-discovery reachability, which is precisely
the "registered but nobody can call it" failure class. No finding questions the founder's
generic-install decision itself.

## Findings

| ID | Severity | Lens | Spec location | Concrete failure | Recommended correction (smallest, existing machinery) |
|---|---|---|---|---|---|
| F-1 | **MAJOR** | Ambiguity / Infeasibility | ES-FR-02; §3 (contract-first) | Tool inputs are described narratively ("installation command or inline script, a short purpose, scope defaulting to workspace, an optional authorized target workspace, and existing-style timeout/session actions") but never enumerated: no field list with types/required/optional, no stated scope enum values, no workspace-identifier form, and the "documented environment variable" exposing the installation prefix is never named. §3 requires defining tool/wire types before handlers; two engineers will produce different schemas and different env-var names. ES-BDD-03/ES-BDD-12 and the document skills cannot reference an unnamed variable. | Add one table to ES-FR-02 enumerating the exact inputs (name, type, required/default — including the scope values and target-workspace identifier) and fix the single canonical name of the installation-prefix environment variable plus its contract (absolute path, pre-created, writable, the only authorized destination exposed). |
| F-2 | **MAJOR** | Test coverage / Reachability | ES-FR-01; ADR-090 §6.5 ("discoverable through ToolSearch, outside the unchanged upfront set"); ES §4 | No ES-BDD row asserts an agent can actually *discover* `environment_setup` through the deferred/ToolSearch tier before invoking it. ES-BDD-01 presumes visibility. A tool registered in the catalog but absent from discovery is unreachable to agents regardless of green tests — the exact delivery failure class this repo's Definition of Done calls out. | Extend ES-BDD-01 (or add a row): a permitted agent's tool search returns `environment_setup` (upfront set still 37), then invocation follows existing Ask. |
| F-3 | MINOR | Traceability / Inconsistency | GENERIC-INSTALL-DECISION ("Remove newly-added recipe machinery…") vs ES §3 | The removal requirement is carried only in the delivery plan (plan step 3); the authoritative ES spec's implementation section does not state it. If the plan is revised, the removal strands while the spec reads as additive-only. | Add one sentence to ES §3: superseded package-recipe machinery, the Admin finalizer/write route, and old structured-dependency instructions are removed; reusable boundary/path/staging/session work is retained. |
| F-4 | MINOR | Test coverage | ES-FR-01 ("Preserve operator policies through the existing reconciliation rules"); ES-BDD-08 | ES-BDD-08 never checks that policy reconciliation adds the shipped global default (`environment_setup: ask`) to the global ceiling for a config last written before this tool existed, without overwriting operator-set values. Without that leg, the tool can be uncallable on an existing config and no test would notice. | Add a ceiling-reconciliation assertion to ES-BDD-08 (shipped default self-heals into the ceiling; operator values preserved). |
| F-5 | MINOR | Security (information disclosure / tampering) | ES-FR-02 ("The command/script itself and destination are visible in the normal approval display") | No maximum input size for the command/script and no defined behavior when exceeded. A very large "multiline installation script" either truncates in the approval display (user approves unseen code) or is unbounded at validation. No dataset covers it. | State a validation-time size limit (existing input-bound conventions) and add oversized-input rejection to the ES-BDD-03 datasets line. |
| F-6 | MINOR | Test coverage (concurrency) | ES-BDD-05 | ES-BDD-05 covers two concurrent *setup* calls and the shared "active runtime" clause, but not ordinary execution-tool use of a workspace environment while another setup job mutates it (e.g. a venv or module tree replaced under a running task). | Add a workspace-scope execution-during-install leg to ES-BDD-05, or state explicitly that workspace-scope concurrent execution is accepted as today's bash behavior. Either is fine; silence is not. |
| F-7 | OBSERVATION | Ambiguity | ES-FR-01 matrix; ES-BDD-08 | "Custom native agent — explicit per-agent Ask" and ES-BDD-08's "custom creation … stores Ask" define behavior for *newly created* custom agents. Whether custom agents that already exist when this ships get a stored entry or ride the global ceiling is unstated; the two populations diverge if the operator later changes global. Under the no-backfill ruling, ceiling-riders are plausibly intended — but unstated, an implementer may add a load-time backfill that collides with the sparse-override doctrine and its guard script. | One sentence in ES-FR-01: existing custom agents ride the global ceiling (no stored entry); only agents created after this feature stores explicit per-agent Ask at creation. |
| F-8 | OBSERVATION | Incompleteness | ES-FR-03; ES-BDD-04 | A script that exits 0 while a detached child keeps running is not covered: the boundary holds by inheritance (sandbox restrictions pass to children), and ES-BDD-04 exercises hook writes, but post-exit child activity and process-group kill are nowhere stated or tested. Existing machinery appears sufficient; the spec should not need new mechanism — only a stated expectation. | Optional clause in ES-FR-03: child processes inherit the job's restrictions and kill is process-group scoped; one negative leg in ES-BDD-04 if cheap. |

## Test-only package names vs prohibited production catalogue

The boundary is correctly drawn and the review confirms it: ES-FR-05's dependency table
(python-docx, openpyxl, python-pptx, reportlab, pypdf, LibreOffice, a PDF rasterizer, fonts,
conditional Node) is explicitly scoped to "the document skills and test fixtures only; the
generic tool must not contain special handling for them", and ES-BDD-12 plus ES-FR-02/03
prohibit any tool-side package catalogue, version pins, or eligibility control in production.
No finding arises here. The names above must not appear in the tool implementation, seed
policies, contracts, or approval logic — only in skill text, fixtures, and acceptance datasets.
The one adjacent risk is F-1: skills and tests cannot stay on the skill-side of that line while
the prefix variable they must target has no name.

## Structural integrity (structured-spec mode)

- Every ES-FR (01–05) maps to at least one ES-BDD row via the Requirements column — pass.
- Every ES-BDD row references at least one requirement — pass.
- ES-SC-01..04 jointly cover ES-BDD-01–12 with the two-claims separation — pass.
- Scope boundaries explicit (header Scope line; §3 exclusions) — pass.
- Cross-document matrix consistency (Ask/Deny/locked-Deny per agent, lifecycle, unattended
  auto-deny, Admin targeting, shared-explicit scope) — pass across all five documents.
- Gaps recorded as findings: discovery acceptance (F-2), removal traceability (F-3),
  reconciliation leg (F-4).

## STRIDE summary (spec-level, reusing existing machinery)

| Component | Dominant threats | Spec coverage |
|---|---|---|
| Ask approval of command/script | Tampering (unseen code — F-5), Repudiation (covered by existing audit) | ES-FR-02, ES-BDD-01 |
| Install execution boundary | Elevation via hooks, escape to host | ES-FR-03, ES-BDD-04 — reuse of sandbox/secret/audit machinery adequate |
| Background session | Spoofing of session access, DoS via runaway children (F-8) | ES-FR-03, ES-BDD-09 (authority binding, kill) |
| Shared runtime publication | Tampering with active runtime | ES-FR-03 staging/serialization, ES-BDD-04/05 |
| Cross-workspace targeting | Elevation (ordinary agent targeting B) | ES-FR-02/03, ES-BDD-03/10 |

No new threat is introduced by the generic decision that existing machinery cannot bound; the
founder-accepted residual risk (arbitrary agent-supplied code behind a human Ask) is inherent
to the decision and correctly stated in the documents.

## Unasked questions (prompts for the author; none reopens settled policy)

1. What are the exact input field names, types, and the scope enum values? (F-1)
2. What is the installation-prefix environment variable called, and what guarantees accompany its value? (F-1)
3. How does an agent discover the tool, and which test proves it? (F-2)
4. Do pre-existing custom agents ever receive a stored per-agent entry? (F-7)

## Non-claims

This review is a specification review only. It is not implementation proof, not a code review of
the in-progress work, and its verdict stands on its own findings — not on any disposition in the
earlier Fable report. No runtime tests were executed in producing it.

## Coordinator dispositions after review

- F-1: added exact seven-field contract, timeout bounds and OMNIPUS_ENV_PREFIX/CACHE/TMP meanings; shared prefix remains stable after publication.
- F-2: ES-BDD-01 now requires actual ToolSearch discovery and unchanged upfront count.
- F-3: authoritative implementation section now explicitly removes superseded recipe machinery and structured-dependency handling.
- F-4: ES-BDD-08 now checks existing global reconciliation and preserved operator values, without new per-agent backfill.
- F-5: command limit is 65,536 UTF-8 bytes; oversize rejection and full scrollable approval text are explicit.
- F-6: workspace scope retains existing direct-write execution semantics; concurrent commands may observe changes. Shared stable publication remains stronger. No new workspace snapshot engine.
- F-7: preserve saved overrides and existing global reconciliation; no new per-agent load-time backfill. New custom agents receive explicit Ask at creation.
- F-8: inherited child restrictions and process-group cleanup receive an explicit fixture expectation.

These are spec corrections, not implementation/test evidence or a fresh independent PASS verdict.
