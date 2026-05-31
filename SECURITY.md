# Security Policy

Omnipus's central pitch is a kernel-level sandbox, an HMAC-chained audit log, and a deny-by-default policy engine. Vulnerabilities in any of these — or in the security-adjacent code that supports them — matter to us, and we want to hear about them privately first.

## Reporting a vulnerability

**Do not open a public GitHub issue for a security report.** Choose one of the following:

1. **(Preferred) GitHub Private Vulnerability Reporting.** Open a private advisory at <https://github.com/elicify-ai/omnipus/security/advisories/new>. This keeps the conversation between you, the project maintainers, and (optionally) named experts you invite — without exposing the issue to the public until a fix ships.
2. **Email.** Write to **connect@elicify.ai** with subject line `[security]` and a description detailed enough for us to reproduce the issue. PGP / signed mail is not yet supported; consider GitHub PVR if confidentiality of transport matters.

If you don't get an acknowledgement within 72 hours, please re-send through the other channel — mail filters do occasionally lose messages.

## What we commit to

| Stage | SLA |
|---|---|
| Acknowledgement of receipt | **within 72 hours** |
| Initial severity assessment + reproduction confirmation | **within 7 days** |
| Status update or fix in progress | **at least every 14 days** until resolved |
| Coordinated public disclosure | only after a fix is released, or earlier if the issue is already public |
| Credit for the reporter (optional) | yes, in the release notes and the GitHub Security Advisory — we ask before publishing your name |

We don't pay bounties at this time. We will credit you publicly if you want it, and we'll keep you in the loop until the fix lands.

## Scope

### In scope

- The Omnipus Go binary and SPA shipped at `github.com/elicify-ai/omnipus`
- The Docker image at `ghcr.io/elicify-ai/omnipus`
- Code under `pkg/`, `cmd/`, `src/`, `contracts/`, and the gateway routes
- Dependencies declared in `go.mod` / `package.json` **only where Omnipus is responsible for the vulnerable usage** (e.g. a library we call insecurely). Upstream CVEs in libraries we ship are interesting but typically should be reported upstream — we'll happily coordinate.
- Build-pipeline outputs (`scripts/install.sh`, GHCR images, GitHub Releases)

### Out of scope

- **Third-party LLM provider behaviour** — model jailbreaks, prompt-injection on the model side, etc. Report those to the provider (OpenRouter, Anthropic, OpenAI, etc.).
- **Kernel CVEs** that affect the sandbox — those go to the relevant kernel maintainers (`linux-kernel`, etc.).
- **Channel sidecar dependencies** (e.g. `signal-cli-rest-api` for the in-flight Signal channel) — report upstream and let us know so we can update.
- **Issues that require an attacker to already have the master key** (`~/.omnipus/master.key`). The threat model assumes the host filesystem is trustworthy.
- **Self-DoS via misconfiguration** (e.g. setting `sandbox.mode = off` then complaining the sandbox doesn't enforce). These are documentation issues, not security bugs.

If you're unsure whether something is in scope, send it. We'd rather sort it out than miss a real one.

## Supported versions

Omnipus is pre-1.0. Only the current `main` branch and the active release branch are supported.

| Branch / version | Supported |
|---|---|
| `main` | ✅ active |
| `feature/iframe-preview-tier13` (v0.1 RC) | ✅ active |
| Any pre-release tag | ❌ historical reference only — please upgrade |

When 1.0 ships, this table will list the supported semver range explicitly.

## Disclosure history

We will list resolved security advisories here (and on GitHub Security Advisories) once we have any. For now: no public security advisories outstanding.

## How we handle reports (for the curious)

1. Acknowledge within 72 hours.
2. Try to reproduce locally. If we can't, ask for more detail.
3. Triage severity (CVSS 3.1 or 4.0, plus our own judgement).
4. Open a private GitHub Security Advisory linked to a draft PR. Invite the reporter as a collaborator.
5. Develop and review the fix in private.
6. Coordinate a release. The advisory goes public and the release ships on the same day.
7. Update this document and the changelog with a link to the advisory.

We try to be specific in advisories about what was vulnerable, what's now fixed, and how to verify. The goal is that an operator reading the advisory can decide in under a minute whether they need to act.

## Researcher acknowledgements

We will acknowledge security researchers here as advisories are resolved. If you'd prefer to stay anonymous, say so when you report — we honour that.

---

© 2026 elicify.ai Pte. Ltd. · Singapore · https://omnipus.ai
