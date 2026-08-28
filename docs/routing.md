# Channel routing: send the right message to the right agent

Every inbound message — Telegram, Slack, Discord, WhatsApp, Matrix, anything compiled in — is routed to a specific agent before the agent loop runs. The routing decision is operator-controlled, evaluated in priority order, and falls back to a configurable default. Once an agent has the message, **mid-conversation hand-off** can switch to a different agent inside the same transcript without losing context.

This page describes the inbound routing layer (channel → agent). For mid-session agent switching, see [memory.md](memory.md) for the handoff tool, or the `handoff` row in [tools-reference.md](tools-reference.md).

## The two routing layers

| Layer | When | What it does | Configured by |
|---|---|---|---|
| **Inbound binding** | Before the turn starts | Picks which agent receives this channel message | `gateway.bindings[]` in `config.json` |
| **Hand-off** | Mid-turn, agent-driven | Transfers control to a different agent inside the live session | Agent's `handoff` tool call |

The two compose: an inbound binding routes a Telegram message to Mia; Mia decides the task is build-related and hands off to Jim; Jim picks up in the same session with full context.

## Inbound bindings

A binding maps a channel + match criteria to an `agent_id`. The first binding that matches wins. Bindings live at `gateway.bindings[]` in `config.json`:

```json
{
  "gateway": {
    "bindings": [
      {
        "agent_id": "support",
        "match": {
          "channel":   "telegram",
          "account_id": "*",
          "peer":      { "kind": "direct", "id": "user123" }
        }
      },
      {
        "agent_id": "sales",
        "match": {
          "channel":    "discord",
          "account_id": "my-discord-bot",
          "guild_id":   "987654321"
        }
      },
      {
        "agent_id": "ops",
        "match": {
          "channel":    "slack",
          "account_id": "ops-workspace",
          "team_id":    "T0XYZ"
        }
      },
      {
        "agent_id": "mia",
        "match": {
          "channel": "*"
        }
      }
    ]
  }
}
```

Bindings are evaluated top-to-bottom. The first match wins. The last entry (`channel: "*"`) is the catch-all default — if no specific rule matches, route to Mia.

## Match precedence

Within a binding, criteria are matched in this order (`pkg/routing/route.go::ResolveRoute`). The first criterion that matches stamps the route's `MatchedBy` field on the returned `ResolvedRoute`; the value is used for observability and downstream filtering but the gateway does **not** log a per-message "matched by" line from `route.go` itself (see [observability.md](observability.md) for the actual event log surface).

| `matched_by` | Trigger |
|---|---|
| `binding.peer` | Direct match on a specific user/peer (`peer.id`) |
| `binding.peer.parent` | Match on a thread parent or reply-target (note: `peer.kind` is matched case-insensitively while `peer.id` is case-sensitive — see `pkg/routing/route.go:154-171`) |
| `binding.guild` | Discord guild ID match |
| `binding.team` | Slack team ID match |
| `binding.account` | Channel account ID match (e.g. a specific bot account) |
| `binding.channel` | Channel-level wildcard (`*` or the channel name only) |
| `default` | No binding matched — route to the **default agent** (see [The default agent](#the-default-agent)) |

## The default agent

The **default agent** handles any message that has no more-specific binding. Exactly one agent is the default: the one whose config carries `default: true`. On a fresh install this is **Mia**.

A subtle but important distinction: the built-in *fallback* agent ID constant in code is `routing.DefaultAgentID = "main"` (`pkg/routing/agent_id.go:9`). On a fresh install, `pkg/coreagent/core.go:228` sets `Default: true` on the `mia` agent when seeding, so user-visible routing behavior is "Mia is the default" — but the `DefaultAgentID` constant itself is the string `"main"`. The `resolveDefaultAgentID()` function (`pkg/routing/route.go:248-283`) prefers a user-marked default first, then falls back to the first enabled agent, then to the `DefaultAgentID` constant.

- **From the UI:** on the **Agents** page, hover an agent card and click **Set as default** — the gold ★ moves to it. To override the default for a single channel, use **Connectors → Configure → Routing → Default agent** (leave it on "(Global default)" to inherit the global default). See [Using the web app](using-omnipus-ui.md#connectors).
- **From config / API:** mark one agent `"default": true` under `agents.list[]`, add a channel-wildcard binding (`account_id: "*"`) under `gateway.bindings[]`, or `PUT /api/v1/channels/{id}/routing` with `{ "default_agent_id": "<id>" }`.

If **no** agent is marked default, Omnipus falls back to the **first enabled agent** in the roster (not a hardcoded name) and emits a `WARN` so the misconfiguration is visible (`pkg/routing/route.go::resolveDefaultAgentID`).

## Wildcards

`account_id: "*"` matches any account on the channel. `channel: "*"` matches any channel and should only appear on the catch-all binding at the end of the list. Omitting a field entirely (e.g. no `peer` block) means "don't constrain on this dimension".

## Per-binding access control

Each channel config also accepts `allow_from`, `dm_policy`, and `group_policy` to gate who can talk to the bot regardless of binding. These are evaluated **before** routing: a message that fails `allow_from` is rejected at the channel layer and never reaches the binding resolver. See [pkg/channels/README.md](../pkg/channels/README.md) for per-channel allowlist semantics.

## Worked examples

### One bot, one agent (simple)

A single Telegram bot. All messages go to Mia. Bindings can be empty — the gateway routes to the **default agent** (Mia on a fresh install) when no bindings match.

### One bot, per-user routing

A Telegram bot used by both a support agent and a sales agent. Direct messages from known support customers route to Mia; everyone else routes to Jim:

```json
"bindings": [
  { "agent_id": "support-agent", "match": { "channel": "telegram", "peer": { "kind": "direct", "id": "support-user-1" } }},
  { "agent_id": "support-agent", "match": { "channel": "telegram", "peer": { "kind": "direct", "id": "support-user-2" } }},
  { "agent_id": "jim", "match": { "channel": "telegram" }}
]
```

### Multi-platform, multi-agent fleet

Discord guild `987` goes to the gaming agent; Slack workspace `T0XYZ` goes to the ops agent; Telegram and WhatsApp both route to Mia as default:

```json
"bindings": [
  { "agent_id": "gaming-agent", "match": { "channel": "discord", "guild_id": "987" }},
  { "agent_id": "ops-agent",    "match": { "channel": "slack",   "team_id":  "T0XYZ" }},
  { "agent_id": "mia",          "match": { "channel": "telegram" }},
  { "agent_id": "mia",          "match": { "channel": "whatsapp" }}
]
```

### Coach plus specialists

Mia takes everything inbound, evaluates the request, and hands off to the right specialist (the pattern in the README demo). Bindings stay simple:

```json
"bindings": [
  { "agent_id": "mia", "match": { "channel": "*" }}
]
```

Mia's prompt knows about Jim / Ava / Ray / Max and uses the `handoff` tool when she identifies a request that fits one of them better. The receiving agent picks up in the same transcript — no copy-paste, no context loss.

## Mid-session hand-off (the second layer)

Once an agent owns a message, it can call the `handoff` tool to transfer control. It accepts a target `agent_id` and an optional short brief, atomically switches the session's active agent, hands the receiver the full transcript so it sees what came before, and returns immediately (12 ms in the live demo).

The receiving agent's first turn sees a tool-call entry naming the handoff and the brief — the agent can ack, ask scoping questions, or start work. The chat UI shows the handoff chip in line with the conversation, so the user understands who they're talking to at every point.

Hand-off is reversible: any agent in the chain can call `return_to_default` to send control back to the default routing agent (typically Mia).

For the full agent-tools API including handoff arguments, see [tools-reference.md](tools-reference.md).

## Why this matters

A typical multi-channel deployment has one Omnipus binary fielding messages from a Telegram bot, a Discord bot, a Slack workspace, and a WhatsApp number. Without inbound bindings every message would hit a single default agent — the operator has no way to specialize. With bindings, you build a team of agents, each with its own prompt, tool policy, and persona, and you let the routing rules decide who answers. The hand-off layer is the dynamic complement — even when binding got the first pick wrong, agents can route to each other mid-conversation.

## See also

[pkg/channels/README.md](../pkg/channels/README.md) covers per-channel config (`allow_from`, `dm_policy`, `group_policy`).

[tools-reference.md](tools-reference.md) documents `handoff`, `return_to_default`, and the rest of the agent-tools API.

[memory.md](memory.md) explains what survives a hand-off (the transcript) and what doesn't (per-agent memory).

[observability.md](observability.md) describes how every routing decision is logged with its `matched_by` value.
