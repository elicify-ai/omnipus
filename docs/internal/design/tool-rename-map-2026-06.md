# Tool Rename + Recategorization Map (§4 + §7) — canonical source of truth

Drives the v0.1.0-completion rename. Every agent applies THIS map verbatim — no
improvisation. Current names from the live registry (81 tools); to-be names from
`.preview-doc/tools-catalog.html` (78 after the 3 redundant retirements).

## ToolCategory enum (pkg/tools/base.go)

ADD: `CategoryMemory="memory"`, `CategoryDelegation="delegation"`,
`CategoryToolDiscovery="tool_discovery"`, `CategoryAgents="agents"`,
`CategoryChannels="channels"`, `CategoryProviders="providers"`,
`CategoryPlatform="platform"`, `CategoryFilesystem="filesystem"`,
`CategoryShell="shell"`, `CategoryTasks="tasks"`, `CategoryWorkspaces="workspaces"`.

KEEP: `CategoryWeb="web"`, `CategoryBrowser="browser"`,
`CategoryCommunication="communication"`, `CategorySkills="skills"`,
`CategoryMCP="mcp"` (behavior-bearing — drives server_id; reserved for dynamic
MCP-server tools AND the mcp-management tools, see note).

KEEP-AS-DEPRECATED-DEFAULT (do NOT delete — BaseTool default + back-compat):
`CategoryCore="core"`, `CategorySystem="system"`. No shipped tool may RETURN
these after the rename, but the constants stay as the BaseTool fallback. Mark
them `// Deprecated: dumping-ground value; all shipped tools use a domain category.`

DROP (unreferenced): `CategoryFile`, `CategoryCode`, `CategoryTask`,
`CategoryWorkspace` are RENAMED (their string values change) — replace every
reference with the new constant. `CategoryAutomation`, `CategoryHardware`,
`CategorySearch` are unused-by-shipped-tools; `CategorySearch` is replaced by
`CategoryToolDiscovery` for search_tools_* (and web_search moves to web). Remove
`CategoryAutomation`/`CategoryHardware` only if zero references remain.

mcp-management note: `add_mcp_server`/`remove_mcp_server`/`list_mcp_servers` use
`CategoryMCP`. In production (builtinRegistry wired) source="builtin" via the
primary path, so the category is display-only. The fallback path
(`rest_tool_registry.go:155`, builtinRegistry==nil, test-only) keys source off
CategoryMCP — fix any test that asserts these as source="builtin" in that path.

## scope enum (contract)

`contracts/components/schemas/ToolRegistryEntry.yaml`: drop `system` from the
`scope` enum → `[core, general]`. Regenerate. (No tool returns ScopeSystem; all
`system.*` tools return ScopeCore. ToolScope Go constants unchanged.)

## Name map (current → to-be) + category

### Filesystem (pkg/tools)
| current | to-be | category |
|---|---|---|
| read_file | read_file | filesystem |
| write_file | write_file | filesystem |
| edit_file | edit_file | filesystem |
| append_file | append_file | filesystem |
| list_dir | list_directory | filesystem |

### Shell (pkg/tools)
| exec | exec | shell |
| workspace.shell | workspace_shell | shell |
| workspace.shell_bg | workspace_shell_bg | shell |

### Web (pkg/tools)
| web_fetch | fetch_url | web |
| web_search | search_web | web |
| web_serve | serve_web | web |

### Browser (pkg/tools/browser) — drop the dot
| browser.navigate | browser_navigate | browser |
| browser.click | browser_click | browser |
| browser.type | browser_type | browser |
| browser.get_text | browser_get_text | browser |
| browser.screenshot | browser_screenshot | browser |
| browser.wait | browser_wait | browser |
| browser.evaluate | browser_evaluate | browser |

### Communication (pkg/tools)
| message | send_message | communication |
| send_file | send_file | communication |
| read_inbox | read_inbox | communication |
| read_message | read_message | communication |
| reply | reply | communication |
| send_email | send_email | communication |
| search_email | search_email | communication |

### Delegation (pkg/tools)
| spawn | spawn | delegation |
| subagent | run_subagent | delegation |
| spawn_status | check_spawn_status | delegation |
| handoff | hand_off | delegation |
| return_to_default | return_to_default | delegation |

### Memory (pkg/tools)
| remember | remember | memory |
| recall_memory | recall_memory | memory |
| retrospective | run_retrospective | memory |

### Tasks (pkg/tools current-workspace)
| task_create | create_task | tasks |
| task_update | update_task | tasks |
| task_list | list_tasks | tasks |
| task_delete | delete_task | tasks |
| set_todos | set_todos | tasks |

### Tasks cross-workspace (pkg/sysagent/tools → §4 rename + behavioral parity)
| system.task.create | create_task_in_workspace | tasks |
| system.task.update | update_task_in_workspace | tasks |
| system.task.list | list_tasks_in_workspace | tasks |
| system.task.delete | delete_task_in_workspace | tasks |

### Skills
| find_skills (pkg/tools) | find_skills | skills |
| install_skill (pkg/tools) | install_skill | skills |
| system.skill.remove | remove_skill | skills |
| system.skill.list | list_skills | skills |
| system.skill.create | create_skill | skills |
| system.skill.edit | edit_skill | skills |
| system.skill.search | **RETIRE** (redundant with find_skills) | — |
| system.skill.install | **RETIRE** (redundant with install_skill) | — |

### Tool-discovery (pkg/tools)
| tool_search_tool_bm25 | search_tools_bm25 | tool_discovery |
| tool_search_tool_regex | search_tools_regex | tool_discovery |

### Agents
| agent_list (pkg/tools) | list_agents | agents |
| system.agent.create | create_agent | agents |
| system.agent.update | update_agent | agents |
| system.agent.delete | delete_agent | agents |
| system.agent.activate | activate_agent | agents |
| system.agent.deactivate | deactivate_agent | agents |
| system.agent.read_metadata | read_agent_metadata | agents |
| system.agent.write_metadata | write_agent_metadata | agents |
| system.agent.list | **RETIRE** (redundant with list_agents) | — |

### Workspaces (pkg/sysagent/tools)
| system.workspace.create | create_workspace | workspaces |
| system.workspace.update | update_workspace | workspaces |
| system.workspace.delete | delete_workspace | workspaces |
| system.workspace.list | list_workspaces | workspaces |
| system.workspace.get | get_workspace | workspaces |

### Channels (pkg/sysagent/tools)
| system.channel.enable | enable_channel | channels |
| system.channel.disable | disable_channel | channels |
| system.channel.configure | configure_channel | channels |
| system.channel.list | list_channels | channels |
| system.channel.test | test_channel | channels |

### Providers (pkg/sysagent/tools)
| system.provider.configure | configure_provider | providers |
| system.provider.list | list_providers | providers |
| system.provider.test | test_provider | providers |
| system.models.list | list_models | providers |

### MCP (pkg/sysagent/tools) — category mcp
| system.mcp.add | add_mcp_server | mcp |
| system.mcp.remove | remove_mcp_server | mcp |
| system.mcp.list | list_mcp_servers | mcp |

### Platform (pkg/sysagent/tools)
| system.config.get | get_config | platform |
| system.config.set | set_config | platform |
| system.doctor.run | run_doctor | platform |
| system.cost.query | query_cost | platform |
| system.navigate | navigate | platform |

## Cross-references that MUST be re-keyed in lockstep (keyed by tool NAME)

- `pkg/sysagent/rbac.go` (`toolPermissions` map) — re-key all `system.*` → new names.
- `pkg/sysagent/confirmation.go` (`confirmationLevels` map) — re-key.
- `pkg/sysagent/ratelimit.go` (`toolCategory` map) — re-key.
- `pkg/sysagent/prompt.go` — the tool-name list in the system-agent prompt.
- `pkg/coreagent/*` seed prompts — any tool names referenced in SOUL/persona text.
- `pkg/config` tool_policies / default policies keyed by tool name.
- SPA `src/components/.../ToolPolicyEditor*` + any tool-name constants in `src/`.
- `pkg/policy/*` builtinToolPolicies (vestigial but keep consistent).
- Tests: `pkg/sysagent/tools/contract_test.go` (drops the `system.` prefix
  assertion + CategorySystem assertion → assert no dots / domain categories),
  `pkg/sysagent/sysagent_test.go` (RBAC list), count tests, golden files.

## Counts after rename

systools.AllTools: 40 − 3 retired (system.agent.list, system.skill.install,
system.skill.search) = **37**. Reconcile gateway builtin-registry test,
contract_test, deps_atomic, sysagent RBAC test.
