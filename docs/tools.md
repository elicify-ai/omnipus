# Tools

Tools let an agent act instead of only replying. You can control whether each agent may use them without approval, must ask first, or cannot use them.

## What it is

A tool is one action an agent can take. It might read a file, run a command, search the web, control the live browser, or send a message.

Omnipus provides two kinds of tools:

- **Built-in tools** come with Omnipus.
- **Connected tools** come from a Model Context Protocol (MCP) server. MCP is a standard that lets another service offer actions to an agent.

Open **Skills & Tools**, then select **Built-in Tools** to see the current built-in list. This product list stays current as tools change.

These are the main groups you will meet.

| Group | What agents can do | Examples |
|---|---|---|
| Files | Read, search, and change workspace files | `read_file`, `grep`, `write_file` |
| Commands | Run commands in the workspace | `bash` |
| Web | Search the web and read a page | `search_web`, `fetch_url` |
| Browser | Navigate and interact with the live browser | `browser_navigate`, `browser_click` |
| Communication | Send messages, files, or email | `send_message`, `send_email` |
| Work | Create tasks, plans, and delegated work | `create_task`, `create_plan`, `delegate` |

The built-in list is not an exhaustive catalog here. **Built-in Tools** is the source for what your installation currently provides.

## When you would use it

Change tool access when:

- an agent should never perform a particular action;
- you want to review a sensitive action before it runs;
- scheduled work must run without waiting for you;
- a connected service adds tools that need their own limits.

You can also name a tool in your request when you want the agent to use a particular method, such as `search_web` for current information.

## How to change tool access

1. Open **Agents** and select the agent.
2. Select the **Tools** tab.
3. Find **Tools & Permissions**.
4. Choose **Cautious**, **Balanced**, or **Full access** as a starting point.
5. Set individual tools to **Allow**, **Ask**, or **Deny** where you need a different rule. The change saves automatically.

For an installation-wide limit, open **Settings**, select **Security**, and expand **Advanced / technical details**. Change the entries under **Tool Access — Global Policies**. Omnipus asks you to confirm your password before it saves a global change.

## Allow, ask, and deny

Each setting controls what happens when an agent wants to call a tool.

| Setting | Result | What you see |
|---|---|---|
| Allow | The tool runs without a permission prompt | The tool call in the conversation |
| Ask | The tool pauses before it runs | Approve, Deny, and Always Allow choices |
| Deny | The agent cannot call the tool | The tool is unavailable to that agent |

**Always Allow** approves the current call and remembers the same tool and arguments for that conversation.

Global and per-agent policies form two layers. The stricter result wins: Deny is stricter than Ask, and Ask is stricter than Allow. A per-agent entry can tighten the global policy, but it cannot loosen it.

| Global policy | Agent policy | Effective result |
|---|---|---|
| Allow | Ask | Ask |
| Allow | Deny | Deny |
| Ask | Allow | Ask |
| Deny | Allow | Deny |

## How to add tools from a connected server

1. Open **Skills & Tools**.
2. Select **MCP Servers**.
3. Select **Add Server**.
4. Enter the connection details and save the server.
5. Check its status and discovered tool list on the server card.
6. Return to the agent's **Tools & Permissions** to set access for its tools.

Omnipus gives each connected tool a policy name in the form `mcp_<server>_<tool>`. A server-wide entry such as `mcp_docs_*` can cover every tool from a server named `docs`. The same global and per-agent rules apply to built-in and connected tools.

## Limits and things to watch

- **Ask needs a person present.** Scheduled runs automatically deny a tool that is waiting for approval. Allow the required tools before scheduling the work.
- **Allow grants real capability.** A command can read or change files without using the dedicated file tools. Review `bash` as well as file-tool policies.
- **Full access removes prompts.** It allows every listed tool for that agent unless a stricter global policy applies.
- **A connected server extends what an agent can do.** Review its discovered tools and policies before assigning it to sensitive work.
- **Tool access and the sandbox do different jobs.** Tool policy decides whether a call may start. The sandbox limits what an allowed call can reach. See [security](security.md).

## Related pages

- [agents](agents.md) — create agents and choose who does the work.
- [skills](skills.md) — add reusable instructions that guide how agents work.
- [browser](browser.md) — use the live browser that browser tools control.
- [security](security.md) — understand the sandbox and other safety controls.
- [settings](settings.md) — find installation-wide controls.
- [tasks](tasks.md) — follow work that agents create and run.
