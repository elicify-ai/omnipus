# Getting help with Omnipus

A short routing guide. Pick the channel that matches what you're trying to do.

## I found a bug

→ Open a [GitHub issue](https://github.com/elicify-ai/omnipus/issues/new/choose) using the **Bug report** template. Include:

- Omnipus version (`omnipus version`)
- OS + kernel (`uname -a`)
- Sandbox mode (`curl -s http://localhost:5000/health | jq .sandbox`)
- Reproduction steps
- `$OMNIPUS_HOME/logs/gateway.log` excerpt around the failure
- `$OMNIPUS_HOME/logs/gateway_panic.log` if it exists

## I have a feature idea

→ Open a [GitHub issue](https://github.com/elicify-ai/omnipus/issues/new/choose) using the **Feature request** template. For substantial features, discuss the design in an issue **before** writing code — saves wasted effort. Check [`ROADMAP.md`](ROADMAP.md) to see which release phase the work might land in.

## I have a question — how do I use X?

→ [GitHub Discussions → Q&A](https://github.com/elicify-ai/omnipus/discussions/categories/q-a). Search first; many common questions are already answered. If the docs disagree with the code, the [docs index](docs/README.md) is the first stop.

## I want to share something I built / ran into

→ [GitHub Discussions → Show & tell / Ideas](https://github.com/elicify-ai/omnipus/discussions).

## I found a security vulnerability

→ **Do not** open a public issue. See [`SECURITY.md`](SECURITY.md) for the private disclosure process. Either open a [private security advisory](https://github.com/elicify-ai/omnipus/security/advisories/new) or email **connect@elicify.ai** with subject line `[security]`.

## I want to use the Omnipus name / logo / brand

→ See [`TRADEMARKS.md`](TRADEMARKS.md). Most use cases (articles, integrations, unmodified redistribution) don't need permission. Commercial use of the brand does — email **connect@elicify.ai**.

## I want to contribute code

→ [`CONTRIBUTING.md`](CONTRIBUTING.md) for development setup, workflow, and the contract-first wire-format rules. External contributors need a one-time [CLA](CLA.md) before their first PR can merge — see CLA.md for status.

## I want to sponsor / fund the project

→ See [`.github/FUNDING.yml`](.github/FUNDING.yml) for current sponsorship channels.

## Anything else (partnerships, press, commercial inquiries)

→ **connect@elicify.ai**

---

© 2026 elicify.ai Pte. Ltd. · Singapore · https://omnipus.ai
