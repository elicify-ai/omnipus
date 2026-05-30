# Contributing to Omnipus

Thank you for your interest in contributing to Omnipus. This project is a community-driven effort to build a sovereign, sandboxed, single-binary AI agent runtime. We welcome bug fixes, features, documentation, and testing contributions.

Omnipus was substantially developed with AI assistance — we embrace this approach and have built our contribution process around it.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Contributor License Agreement (CLA)](#contributor-license-agreement-cla)
- [Trademarks](#trademarks)
- [Ways to Contribute](#ways-to-contribute)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Making Changes](#making-changes)
- [Contract-First Wire Formats](#contract-first-wire-formats)
- [AI-Assisted Contributions](#ai-assisted-contributions)
- [Pull Request Process](#pull-request-process)
- [Branch Strategy](#branch-strategy)
- [Code Review](#code-review)
- [Communication](#communication)

---

## Code of Conduct

We are committed to maintaining a welcoming and respectful community. Be kind, constructive, and assume good faith. Harassment or discrimination of any kind will not be tolerated.

---

## Contributor License Agreement (CLA)

Omnipus is developed by **elicify.ai Pte. Ltd.** under the MIT license. Before your first pull request can be merged, you (or your employer, if you contribute on company time) must sign a one-time **Contributor License Agreement** based on the Apache Individual CLA.

The CLA assigns copyright in your contribution to elicify.ai Pte. Ltd. while granting you a perpetual license back to your own work. It exists so that future Omnipus variants (Desktop, Cloud / SaaS — see [`ROADMAP.md`](ROADMAP.md)) can be distributed under terms appropriate to each variant, without retroactively chasing every contributor for permission. The upstream MIT-licensed code is **irrevocable** — anyone who downloads Omnipus today keeps the MIT grant forever.

See [`CLA.md`](CLA.md) for the full text and rationale, including the corporate CLA path for contributions made on company time.

**Status:** The CLA scaffolding is in place; the `cla-assistant.io` bot integration and final lawyer-reviewed text are being prepared. **Until that lands, outside pull requests are not being merged.** Inside contributors (elicify.ai employees and direct contractors) are covered by their employment / contractor agreement and proceed normally.

---

## Trademarks

The **Omnipus** name, logo, the **"Sovereign Deep"** design system, and the octopus mascot are trademarks of **elicify.ai Pte. Ltd.** The MIT license covers source code only — it does not grant rights to use the Omnipus brand on a fork, a commercial variant, or any goods or services.

You can refer to "Omnipus" in articles, integrations ("Omnipus-compatible"), and unmodified redistributions. You **cannot** name your fork "Omnipus Pro" or sell a "Omnipus Cloud" service without written permission. Full policy: [`TRADEMARKS.md`](TRADEMARKS.md).

---

## Ways to Contribute

- **Bug reports** — Open an issue using the bug report template.
- **Feature requests** — Open an issue using the feature request template; discuss before implementing.
- **Code** — Fix bugs or implement features. See the workflow below.
- **Documentation** — Improve READMEs, docs, inline comments, or translations.
- **Testing** — Run Omnipus on new channels or LLM providers and report your results.

For substantial new features, please open an issue first to discuss the design before writing code. This prevents wasted effort and ensures alignment with the project's direction. Check `ROADMAP.md` to understand which release phase your work targets.

---

## Getting Started

1. **Fork** the repository on GitHub.
2. **Clone** your fork locally:
   ```bash
   git clone https://github.com/<your-username>/omnipus.git
   cd omnipus
   ```
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/elicify-ai/omnipus.git
   ```

---

## Development Setup

### Prerequisites

- Go 1.26.3 or later (`go.mod` pins `go 1.26.3`)
- Node.js 24 or later
- `make`

### Build

The Go binary embeds the React SPA via `go:embed`. `make build` handles the full pipeline:

```bash
make build   # npm ci + npm run build + copy SPA into pkg/gateway/spa + go generate + go build
```

The output is `build/omnipus-<platform>-<arch>` (plus a `build/omnipus` symlink to it).

If you need to run the SPA build steps by hand (e.g. iterating on the frontend without re-linking the Go binary), the equivalent sequence is `npm ci && npm run build && rm -rf pkg/gateway/spa && cp -r dist/spa pkg/gateway/spa`. After that, plain `go build ./cmd/omnipus` works.

### Running Tests

```bash
make test                                    # Run all tests (go test ./...)
go test -run TestName -v ./pkg/session/      # Run a single test
go test -bench=. -benchmem -run='^$' ./...  # Run benchmarks
```

The test suite requires the SPA to be built first (see Build section above) because the gateway package embeds it.

### Code Style

```bash
make fmt    # Format Go code (via golangci-lint fmt)
make vet    # Static analysis (go vet)
make lint   # Full golangci-lint run with project build tags
```

Run `make check` before pushing — it runs `deps`, `fmt`, `vet`, and `test` in sequence.

---

## Making Changes

### Branching

Always branch off `main` and target `main` in your PR. Never push directly to `main`:

```bash
git checkout main
git pull upstream main
git checkout -b your-feature-branch
```

Use descriptive branch names, e.g. `fix/telegram-timeout`, `feat/ollama-provider`, `docs/contributing-guide`.

### Commits

- Write clear, concise commit messages in English.
- Use the imperative mood: "Add retry logic" not "Added retry logic".
- Reference the related issue when relevant: `Fix session leak (#123)`.
- Keep commits focused. One logical change per commit is preferred.
- Follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).

### Keeping Up to Date

Rebase your branch onto upstream `main` before opening a PR:

```bash
git fetch upstream
git rebase upstream/main
```

---

## Contract-First Wire Formats

**Hard constraint:** every type that crosses the gateway/SPA boundary (REST request/response, WebSocket frame, persisted JSON consumed by the SPA) must be defined in the contract specs **before** any Go or TypeScript code is written:

- `contracts/openapi.yaml` — REST endpoints
- `contracts/asyncapi.yaml` — WebSocket frames
- `contracts/components/schemas/` — shared JSON Schema component definitions

Generated artifacts live in `pkg/api/generated/` (Go) and `src/lib/api/generated/` (TypeScript + Zod). These are committed to the repo and must never be edited by hand.

**To add a new wire type:**

1. Add the schema to `contracts/components/schemas/<TypeName>.yaml`
2. Reference it from `openapi.yaml` or `asyncapi.yaml`
3. Run `make gen-contracts` to regenerate all artifacts
4. Commit the generated diff alongside the spec change (one atomic commit)
5. Write the handler or consumer using the generated type only

`make verify-contracts` (also run as a CI job) fails if the committed generated files are stale relative to the spec. Hand-written wire-format structs in `pkg/gateway/*.go` or hand-written wire interfaces in `src/lib/api.ts` / `src/lib/ws.ts` are caught by the `wire-types-lint` CI job and must be corrected.

---

## AI-Assisted Contributions

Omnipus was built with substantial AI assistance, and we fully embrace AI-assisted development. However, contributors must understand their responsibilities when using AI tools.

### Disclosure Is Required

Every PR must disclose AI involvement using the PR template's AI Code Generation section. There are three levels:

| Level | Description |
|---|---|
| Fully AI-generated | AI wrote the code; contributor reviewed and validated it |
| Mostly AI-generated | AI produced the draft; contributor made significant modifications |
| Mostly human-written | Contributor led; AI provided suggestions or none at all |

Honest disclosure is expected. There is no stigma attached to any level — what matters is the quality of the contribution.

### You Are Responsible for What You Submit

Using AI to generate code does not reduce your responsibility as the contributor. Before opening a PR with AI-generated code, you must:

- **Read and understand** every line of the generated code.
- **Test it** in a real environment (see the Test Environment section of the PR template).
- **Check for security issues** — AI models can generate subtly insecure code (e.g., path traversal, injection, credential exposure). Review carefully.
- **Verify correctness** — AI-generated logic can be plausible-sounding but wrong. Validate the behavior, not just the syntax.

PRs where it is clear the contributor has not read or tested the AI-generated code will be closed without review.

### AI-Generated Code Quality Standards

AI-generated contributions are held to the **same quality bar** as human-written code:

- All CI checks must pass.
- Code must be idiomatic Go and consistent with the existing codebase style.
- It must not introduce unnecessary abstractions, dead code, or over-engineering.
- It must include or update tests where appropriate.

### Security Review

AI-generated code requires extra security scrutiny. Pay special attention to:

- File path handling and sandbox escapes
- External input validation in channel handlers and tool implementations
- Credential or secret handling
- Command execution (`exec.Command`, shell invocations)

If you are unsure whether a piece of AI-generated code is safe, say so in the PR — reviewers will help.

---

## Pull Request Process

### Before Opening a PR

- [ ] Run `make check` and ensure it passes locally.
- [ ] Run `npm run typecheck` (`tsc -b --noEmit`) — use this form, not `tsc --noEmit` alone, which silently no-ops on this repo's project-references config.
- [ ] Run `npx vitest run` for frontend tests.
- [ ] Run `make verify-contracts` if you changed any contract spec or gateway handler.
- [ ] Fill in the PR template completely, including the AI disclosure section.
- [ ] Link any related issue(s) in the PR description.
- [ ] Keep the PR focused. Avoid bundling unrelated changes together.

### PR Template Sections

The PR template asks for:

- **Description** — What does this change do and why?
- **Type of Change** — Bug fix, feature, docs, or refactor.
- **AI Code Generation** — Disclosure of AI involvement (required).
- **Related Issue** — Link to the issue this addresses.
- **Technical Context** — Reference URLs and reasoning (skip for pure docs PRs).
- **Test Environment** — OS, model/provider, and channels used for testing.
- **Evidence** — Optional logs or screenshots demonstrating the change works.
- **Checklist** — Self-review confirmation.

### PR Size

Prefer small, reviewable PRs. A PR that changes 200 lines across 5 files is much easier to review than one that changes 2000 lines across 30 files. If your feature is large, consider splitting it into a series of smaller, logically complete PRs.

---

## Branch Strategy

### Long-Lived Branches

- **`main`** — the active development branch. All feature PRs target `main`. The branch is protected: direct pushes are not permitted, and at least one maintainer approval is required before merging.
- **`release/x.y`** — stable release branches, cut from `main` when a version is ready to ship. These branches are more strictly protected than `main`.

### Requirements to Merge into `main`

A PR can only be merged when all of the following are satisfied:

1. **CI passes** — All GitHub Actions jobs (typecheck, wire-types-lint, verify-contracts, lint, vuln\_check, test, security, perf-smoke, playwright) must be green.
2. **Reviewer approval** — At least one maintainer has approved the PR.
3. **No unresolved review comments** — All review threads must be resolved.
4. **PR template is complete** — Including AI disclosure and test environment.

### Who Can Merge

Only maintainers can merge PRs. Contributors cannot merge their own PRs, even if they have write access.

### Merge Strategy

We use **squash merge** for most PRs to keep the `main` history clean and readable. Each merged PR becomes a single commit referencing the PR number, e.g.:

```
feat: Add Ollama provider support (#491)
```

If a PR consists of multiple independent, well-separated commits that tell a clear story, a regular merge may be used at the maintainer's discretion.

### Release Branches

When a version is ready, maintainers cut a `release/x.y` branch from `main`. After that point:

- **New features are not backported.** The release branch receives no new functionality after it is cut.
- **Security fixes and critical bug fixes are cherry-picked.** If a fix in `main` qualifies (security vulnerability, data loss, crash), maintainers will cherry-pick the relevant commit(s) onto the affected `release/x.y` branch and issue a patch release.

If you believe a fix in `main` should be backported to a release branch, note it in the PR description or open a separate issue. The decision rests with the maintainers.

Release branches have stricter protections than `main` and are never directly pushed to under any circumstances.

---

## Code Review

### For Contributors

- Respond to review comments within a reasonable time. If you need more time, say so.
- When you update a PR in response to feedback, briefly note what changed (e.g., "Updated to use `sync.RWMutex` as suggested").
- If you disagree with feedback, engage respectfully. Explain your reasoning; reviewers can be wrong too.
- Do not force-push after a review has started — it makes it harder for reviewers to see what changed. Use additional commits instead; the maintainer will squash on merge.

### For Reviewers

Review for:

1. **Correctness** — Does the code do what it claims? Are there edge cases?
2. **Security** — Especially for AI-generated code, tool implementations, and channel handlers.
3. **Architecture** — Is the approach consistent with the existing design?
4. **Simplicity** — Is there a simpler solution? Does this add unnecessary complexity?
5. **Tests** — Are the changes covered by tests? Are existing tests still meaningful?
6. **Contract compliance** — Any new cross-boundary types must go through the contract spec; generated types only.

Be constructive and specific. "This could have a race condition if two goroutines call this concurrently — consider using a mutex here" is better than "this looks wrong".

---

## Communication

- **GitHub Issues** — Bug reports, feature requests, design discussions.
- **GitHub Discussions** — General questions, ideas, community conversation.
- **Pull Request comments** — Code-specific feedback.

When in doubt, open an issue before writing code. It costs little and prevents wasted effort.

---

## A Note on the Project's AI-Driven Origin

Omnipus's architecture was substantially designed and implemented with AI assistance, guided by human oversight. If you find something that looks odd or over-engineered, it may be an artifact of that process — opening an issue to discuss it is always welcome.

We believe AI-assisted development done responsibly produces great results. We also believe humans must remain accountable for what they ship. These two beliefs are not in conflict.

Thank you for contributing.
