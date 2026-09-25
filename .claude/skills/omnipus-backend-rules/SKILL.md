---
name: omnipus-backend-rules
description: Go-specific operational detail for backend-lead only — build/test command shapes, golangci-lint invocation, size-budget enforcers, the tool-policy implementation symbols, and the platform build-tag rule. Preloaded via the `skills:` frontmatter field on backend-lead.md alongside `omnipus-shared-rules`. No other role loads this skill — it is not general Omnipus procedure, it is backend-lead's own.
---

# Omnipus Backend Rules

Last reviewed: 2026-09-25

Backend-lead only. Read `omnipus-shared-rules` first — this skill adds Go-specific
detail on top of it, never repeats it. Root `CLAUDE.md` stays authoritative on the facts
this restates.

## Build and test

- Build tags are always `goolm,stdjson`; `CGO_ENABLED=0`. Prefer `make test` / `make
  build`, which inject the tags. `build constraints exclude all Go files in
  .../pkg/channels/matrix` means a missing tag, not a broken package (`CLAUDE.md`,
  "Build, test, and quality gates").
- Never run the full Go suite locally (`go test ./...` OOM-kills the shared machine # agent-guard: allow (quoted to forbid it)). At
  most ONE narrowly-scoped local test:
  `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/<one>/` — never
  multiple Go suites in parallel.
- Toolchain minimum: Go 1.26.6 (`go.mod`) — a 1.22 compiler cannot build this.
- Fresh worktree / new clone: stub the SPA embed before building `pkg/gateway`
  (`CLAUDE.md` gives the exact `mkdir`/`echo`/`touch` sequence) — the resulting compile
  error looks like a code defect and is not one.
- Ad-hoc `golangci-lint` runs must pass `--max-issues-per-linter=0 --max-same-issues=0`.
  Why: the default grouping caps output at 3 per message and has hidden real findings
  (`docs/internal/false-green-patterns.md` section 1 — errcheck read 64, was 253).
- A green under one flag set is not a pass: race bugs need `-race`; a cross-platform
  break needs `GOOS=<target> go vet`.

## Contracts

- Backend-lead is the only role that edits `contracts/openapi.yaml`,
  `contracts/asyncapi.yaml`, and `contracts/components/schemas/` — architect decides the
  shape, backend-lead edits the spec and regenerates via `scripts/gen-contracts.sh`
  (`make gen-contracts`).
- Discriminated unions are the one exception to schema-file-first: the `oneOf` +
  `discriminator` wrapper lives INLINE in `openapi.yaml` over internal
  `#/components/schemas/...` refs — oapi-codegen inlines external refs inside a `oneOf`
  as anonymous structs with non-compiling `As*` accessors (precedent: `AgentCreateRequest`,
  `docs/internal/architecture/ADR-034-agent-create-discriminated-union.md` — cite ADRs by
  title, never number alone).

## Security model — implementation symbols

- Tool policy is two layers, no third, and only ever tightens below the ceiling — the
  full rule is `CLAUDE.md` Hard Constraint #6; this is the pointer into the code.
- `config.ReconcileToolPolicyCeiling` (`pkg/config/validate.go`) keeps the ceiling
  complete for the static catalog on every load, self-heals old installs additively, and
  never overwrites an operator-set value. Reconciling to the shipped default (including
  `bash = allow`) is intended, not a gap.
- Strictest-wins resolution: `pkg/tools/compositor.go::resolveEffectivePolicyWith`. No
  hardcoded allow/deny/ask fallback, no `DefaultPolicy` field, no fail-closed per-agent
  backfill — do not reintroduce (`scripts/check-no-fail-closed-backfill.sh`). Lock a tool
  down with an explicit `deny`, per-agent or on the ceiling.
- Retired security surfaces (shell deny-pattern list, per-binary exec allowlist, # agent-guard: allow
  `ExecApprovalManager`) stay deleted # agent-guard: allow (retired names quoted to forbid reintroducing them); enforcer:
  `scripts/check-no-shell-deny-patterns.sh`.

## Size budgets

- File fails CI over 3,000 lines, function over 240 (shared rule 10). Enforcers:
  `scripts/check-file-budget.sh`, `scripts/check-function-budget.sh`
  (`make lint-budgets`). Grandfathered entries in `scripts/budgets/files.txt` and
  `scripts/budgets/functions.txt` may only shrink — extract instead of adding to them.
- `gocyclo`: WARN at 30 is advisory; the only FAIL is a listed function grown past its
  pinned value in `scripts/budgets/gocyclo.txt`. Enforcer:
  `scripts/check-gocyclo-budget.sh`.

## Platforms

- Linux, macOS, Windows only — no `//go:build freebsd|netbsd|openbsd` terms, no BSD
  branches; that is a regression, not portability (`CLAUDE.md`, "Tech stack and
  platforms").

## Scripts ownership

- backend-lead owns `scripts/` generally (build, CI, budgets, guards it did not author);
  agent- and skill-related guard/tooling scripts are prometheus-prompt-engineer's as
  author.
