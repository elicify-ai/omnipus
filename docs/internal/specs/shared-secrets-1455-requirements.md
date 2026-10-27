# Shared secrets — requirements (#1455)

Status: Requirements (founder interview 2026-10-27). Input to the feature spec (`plan-spec`) and its review rounds.
Captured at: release/v0.1.1 `75d48f041`.
Related: #1458 (skills), #1459 (agent details) — same theme: what an agent may see and change.

## Problem

A secret (password, API key, token) is tied to exactly one user when it is created: one provider, one connector, one channel, one mailbox, or one agent in one workspace (`pkg/credentials/card.go::CardRecipient`). Five agents that need the same GitHub key need five copies. There is no page that shows all secrets, and an agent cannot ask for a key "for the whole team". Field case (2026-10-26): Jarvis was asked to store a Git credential for everyone in the workspace; no recipient fitted, and the turn looped.

Physically, every secret already lives once in the encrypted `credentials.json` (AES-256-GCM, `pkg/credentials/store.go::Store`). What is missing is **central management** and **one secret serving many users**.

## Goals

1. Each secret is stored **once**, with a **unique name**, and carries an **access list** (which workspaces and which agents may use it) plus **"used by"** (system consumers such as a provider or connector).
2. Any agent can ask for a secret through the existing secure card. **The person decides the reach in the card, never the agent.**
3. A new **Settings → Secrets** page manages all secrets in one place.

## Non-goals

- No change to how values are encrypted at rest.
- No reminders or notifications (expiry is information only).
- No migration of existing data (greenfield; existing secrets may be re-entered).

## Requirements

### Storage and naming
- R1. One central store for **all kinds**: agent secrets (handed to commands, e.g. `GITHUB_TOKEN`), provider API keys, connector (MCP) keys, chat-channel credentials and mailbox passwords. (D8)
- R2. A secret name is **unique** across the whole system; there are no copies per level. "Levels" are only the access list. (D3, D5)
- R3. Each secret may carry a free-text **note** (purpose, origin), shown on the Settings page and to agents that can use it. (D12)
- R4. Each secret may carry an optional **expiry date**, shown as information only — no reminder. (D13)

### Access (reach)
- R5. Reach options: **only this agent**, **a named list of agents**, **a workspace** (all its agents), **all agents** (system-wide). (D1)
- R6. System keys (provider, connector, channel, mailbox) are used by the system but are **never handed to an agent** unless the person explicitly adds that agent to the access list. (D8)
- R7. A change of access or value applies **from the next command / tool call**. Commands already running keep their copy until they finish; the Settings page states this. (D9)

### Collecting a secret in chat (secure card)
- R8. **One tool** (`request_secret`); every main agent may trigger it; tool permissions stay as they are today. (D1)
- R9. The agent may **suggest** a reach, with a reason; the card **pre-selects** it, shows the **exact list of agents** that will get access, and the person confirms or changes it. (D4)
- R10. An agent that sees an existing secret it cannot use may **ask for access through the same card**; the card shows the current access list and lets the person add the agent or widen the reach. No new value, no duplicate. (D7)

### What agents see
- R11. An agent's secret list shows **all secret names of its workspace** (including system-wide ones), each marked **"you have access" / "no access"**. Values are never shown to agents. Omnipus sees all names. (D6)

### Settings → Secrets page
- R12. Add a secret by hand; set its reach / access list; replace its value; delete it (first showing what uses it); edit note and expiry. (D2)
- R13. Group or filter secrets by reach; show "used by" and the access list.
- R14. **Reveal**: a stored value may be shown with one click by anyone signed in, **without re-authentication and without logging** — founder decision, risk accepted. (D11)

### Audit
- R15. Every change (create, access widened/narrowed, value replaced, delete) is logged with who did it. (D10)
- R16. Use is logged **once per agent turn**: which secrets that turn received. Values never appear in the log. (D10)

## Security considerations

| Risk | Handling |
|---|---|
| A manipulated agent spreads a key | The person decides every reach in the card (R9/R10); system keys never reach agents implicitly (R6). |
| Reveal on the Settings page (R14) | **Accepted by the founder (D11)**: any signed-in account, an unlocked screen, or an agent that can sign in to Omnipus in its own browser could read every value, unlogged. The reviewers must not silently re-open this; if they find a concrete new exploit, it goes back to the founder. |
| Values in transcripts | Unchanged rule: injected values are registered for masking (`EnsureSensitiveValues`); values never enter agent context or logs. |
| Revocation latency | R7: next command; running commands keep their copy. |
| Name collisions | Removed by R2 (unique names). |

`pkg/credentials` is a security-lead focus area: security-lead reviews the spec and the implementation.

## Edge cases to specify in the feature spec

- An agent moved to another workspace: its workspace-reach access ends; its agent-list entries stay.
- A workspace or agent is deleted: remove it from all access lists; a secret with an empty access list remains for system consumers only.
- Deleting a secret that a provider uses: the delete dialog shows the dependency and requires confirmation; afterwards the provider reports a missing key.
- Channel-triggered and unattended turns: the card cannot open (today's rule); the agent reports that it needs a person.
- Worker agents: today `request_secret` is denied for workers (`workerTierDeniedTools`); keep, unless the spec argues otherwise.

## Contract and UI notes

- New wire fields (access list, reach, used-by, note, expiry) are defined in `contracts/` first (Hard Constraint #8). Today `SecretNameEntry.kind` is a closed enum and `CardRecipient` holds one recipient; both change.
- New screen: Settings → Secrets. Existing screens (providers, connectors, channels, mail, agent editor) store into, or pick from, the central store.

## Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Who decides the reach | One tool; any agent may trigger it; the card offers the reach options; the person decides; tool permissions unchanged | Keeps permissions simple; the person, not the agent, controls spread | Interview | 2026-10-27 |
| D2 | Management screen | New Settings → Secrets page to add secrets by hand and see secrets per reach | Today there is no overview | Interview | 2026-10-27 |
| D3 | Storage | Secrets stored once centrally; the system controls which workspaces and agents have access | Founder requirement | Interview | 2026-10-27 |
| D4 | Agent suggestion | The agent may suggest a reach with a reason; the card pre-selects it and shows who gets access; the person confirms or changes it | The agent often knows who needs it | Interview | 2026-10-27 |
| D5 | Names | One secret exists once at the top level; names are unique; no per-level copies | Founder: "ideally not stored on different levels" | Interview | 2026-10-27 |
| D6 | Agent view | An agent sees all secret names of its workspace with an access indicator; never values | Avoids duplicate requests | Interview | 2026-10-27 |
| D7 | Requesting access | An agent asks for access to an existing secret through the same card | No duplicates | Interview | 2026-10-27 |
| D8 | Kinds in scope | All kinds in one store and on one page; system keys never handed to agents unless explicitly added | Central management with protection of system keys (founder chose after the impact explanation) | Interview | 2026-10-27 |
| D9 | Revocation timing | From the next command; running commands keep their copy | A running program's copy cannot be revoked | Interview | 2026-10-27 |
| D10 | Audit | All changes logged; use logged once per agent turn | Answers "who used it" without flooding the log | Interview | 2026-10-27 |
| D11 | Reveal on the Settings page | Reveal freely: one click, anyone signed in, no re-auth, no log. Chief pushed back (other accounts, unlocked screen, agents with a browser) and offered admins-only + logged; founder kept "reveal freely, no log". Risk accepted | Founder convenience; risk recorded | Interview | 2026-10-27 |
| D12 | Notes | A free-text note per secret | Founder extra | Interview | 2026-10-27 |
| D13 | Expiry | An optional expiry date as information only, no reminder | Founder: "not the reminder, just as an information" | Interview | 2026-10-27 |

## Dependency Graph & Implementation Order

1. Contracts: secret entry with access list, reach, used-by, note, expiry; card request/response with suggested reach and access-request mode.
2. Store: access-list data model in `pkg/credentials` (replaces the single `CardRecipient` binding); unique-name enforcement.
3. Resolution/injection: `PrivateEnvironment` and `inject.go` evaluate the access list per command (R7); system keys excluded unless listed (R6).
4. Tools: `request_secret` suggested reach + access-request mode; `list_secrets` workspace view with access indicator.
5. Audit events (R15/R16).
6. SPA: secure card reach picker; Settings → Secrets page; existing screens pick from the central store.
7. User docs; hands-on UAT (founder flow: Jarvis asks for a team GitHub key → card → team gets access).

Steps 1–2 block everything; 3–5 can run in parallel after 2; 6 needs 1 and 4.
