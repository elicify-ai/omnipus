---
name: tool-mapping
description: Inspect and change an agent's sparse tool-policy overrides without bypassing the global ceiling.
metadata:
  display_name: Tool Mapping
---
# Tool Mapping
## Prerequisites
Read the target's configuration, stored overrides, and revision with `tool:get_agent`, and its effective policies with `tool:get_agent_tools`. The latter returns a compact plain-text list of tool names, effective allow/ask/deny policies, and short descriptions, including denied tools. Its existing-agent header includes the current revision; preview headers have no id or revision. Separate per-agent override and ceiling metadata are no longer returned. Discover catalog names with `tool:ToolSearch`.
## Steps
1. Compare effective policies with stored overrides, respecting the global ceiling. Report unavailable ceiling details rather than inferring them from the effective list.
2. Propose the intended grants, asks, denials, and removals; explain grants blocked by the ceiling.
3. Present one combined proposal in the conversation and wait for the actual user's apply, change, or cancel reply. When delegated, send it through `tool:message_parent` and wait for the relayed user answer. Use `tool:AskUserQuestion` only for a real unknown, never as permission to apply.
4. After confirmation, call `tool:set_agent_permissions` with `id`, the current `revision` from `tool:get_agent`, and a sparse `tool_policy_changes` patch. Send only fields that tool advertises. If the advertised schema cannot express a proposed override, report that part incomplete rather than inventing a payload. Read back effective runtime policy.
## Expected output
Verified override changes with inherited entries left absent, or a precise incomplete-schema result.
## Stop and handoff
Never manufacture a deny-all backfill. A denied management operation remains incomplete. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
