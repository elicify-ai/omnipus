# Built-in agents and skills — interview decisions

Date: 2026-09-17. Status: interview complete; decisions consolidated into the requirements.

Requirements: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/docs/internal/design/built-in-agents-and-skills-2026-09.md`.

These are the founder's confirmed directions from the review interview. They take precedence over conflicting wording in the original requirements. The requirements now incorporate these decisions. Implementation has not started.

| Topic | Confirmed decision |
|---|---|
| Scope | Agents and skills only. Worktree isolation is separate; retain only future prompt/skill guidance tied to actual availability. |
| Ordinary built-ins | Their built-in prompts/instructions remain protected. Users can edit tool permissions, installed connector assignments, and skill assignments. |
| Judge and Plan Supervisor | Instructions are editable. Tools, connector access, and skill assignments are fixed. This is the explicit exception to ordinary built-in editing rules. |
| Ava's role | Full configuration of agents, teams, and skills, including assigning installed connectors and tool permissions. Protected built-in fields and the Judge/Supervisor capability restrictions still apply. |
| Ava's confirmation | Show the proposed changes together and get confirmation in one user-facing question before applying them. Do not introduce a separate approval-tool workflow. This does not authorize bypassing the application's existing global permission enforcement. |
| Connector responsibility | Ava assigns access to installed connectors. Admin handles installation, configuration, and credentials. |
| Mia's execution access | Mia can use the existing execution tool for document-generation workflows. |
| General Purpose helpers | May create helpers of the same General Purpose type; may not delegate to other agent types. |
| Judge evidence | May inspect the task's activity record and relevant output files, read-only. Execution and connector tools remain unavailable. |
| Persistence and greenfield scope | Preserve permitted settings, deliberate empty selections, and edited Judge/Supervisor instructions through reload/restart. The final founder answer supersedes the migration discussion: assume greenfield, with no existing-installation migration or old-roster compatibility work. Future updates should preserve user choices, but upgrade machinery is outside this delivery. |

## Final answers

| Question | Confirmed answer |
|---|---|
| 1. Default tools | A: role-specific lists, generously complete. When in doubt about a relevant supporting tool, include it rather than leave the agent unable to finish. Keep explicit role boundaries and global permissions. |
| 2. Document skills | A: retain the existing Python and Node.js scripts and provide their required runtimes, libraries, and helper files. |
| 3. Missing dependencies | A: Mia explains the gap and hands setup to Admin; document work remains pending until the dependency is available. |
| 4. Old roster | Greenfield only. No migrations. Ray/Max are not seeded; do not add migration or historical-agent retirement workflows. |
| 5. Agent type | Never change an existing agent's type. Create a new agent for the required type. |
| 6. Completion | Real workflows plus appropriate automated checks: Ava configures a usable teammate, Mia produces valid files, editing restrictions work, and settings persist. Restart/reload checks apply; old-version upgrade checks do not apply to this greenfield scope. |

No product questions from this interview remain unanswered. The exact per-tool maps and document dependency inventory are implementation work to derive from these requirements, not a reason to reopen the confirmed product decisions.

## Follow-up decision — global tool visibility

Founder-confirmed after the interview on 2026-09-17. Use one global hardcoded upfront tool set, filtered by existing permissions and runtime applicability. No per-agent visibility setting or role-specific visibility resolver. The exact 37-name set is recorded in §5.4 of the requirements.

Each built-in prompt names its role-specific standard tools and explains their use. If one is not directly callable, the agent loads it through ToolSearch by exact name. Prompt text neither grants permission nor loads a definition. Skill itself is upfront; skill bodies remain on demand.

This supersedes the analysis's initial recommendation to offer role-specific tool sets directly. The role inventories remain useful for writing prompts and granting permissions, not for defining per-role visibility.

## Follow-up decision — image inspection through the read tool

Founder-confirmed on 2026-09-17: image viewing belongs in the existing `read_file` tool, with the same behavior through `library_read`; do not add a separate image-viewing tool. Reuse existing media processing and model-capability handling, preserve image content through provider conversion, and keep inspection separate from sending files to the user. The global upfront tool list stays unchanged.

Source verification confirms this requires modification: `ReadFileTool.Execute` currently rejects null-containing binary files unless they are extractable documents, and its successful ordinary-file path returns text. It has no image-content return branch. `LibraryReadTool.Execute` delegates directly to that implementation. Existing generic tool-media support does not change this reader behavior. See [verification evidence](built-in-agents-and-skills-visual-inspection-verification.md). Runtime implementation has not started.
