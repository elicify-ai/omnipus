// Tool-approval preview registry: tool name -> readable summary entry.
//
// ToolApprovalModal.tsx looks a tool name up here before falling back to the
// generic Tool-name line + raw Arguments JSON dump. Add a new tool by adding
// a new entry — nothing else in the modal needs to change, and every tool
// without an entry keeps working exactly as before (see mode's doc comment
// in types.ts for 'additive' vs 'replace').

import { BashApprovalPreview } from './BashApprovalPreview'
import { RequestMountApprovalPreview } from './RequestMountApprovalPreview'
import {
  EnvironmentSetupApprovalPreview,
  environmentSetupApprovalTitle,
} from './EnvironmentSetupApprovalPreview'
import { AgentChangeApprovalPreview, redactAgentChangeArgs } from './AgentChangeApprovalPreview'
import type { ToolApprovalPreviewEntry } from './types'

// tool-parity r17 FR-160: one additive summary for both agent writers; the
// raw Arguments block stays beside it (17.3) with env values masked.
const agentChangePreview: ToolApprovalPreviewEntry = {
  mode: 'additive',
  Body: AgentChangeApprovalPreview,
  redactArgs: redactAgentChangeArgs,
}

export const TOOL_APPROVAL_PREVIEWS: Record<string, ToolApprovalPreviewEntry> = {
  bash: {
    mode: 'additive',
    Body: BashApprovalPreview,
  },
  request_mount: {
    mode: 'replace',
    Body: RequestMountApprovalPreview,
    title: (ctx) => `${ctx.agentName} wants to add a folder`,
    primaryLabel: 'Add folder',
    secondaryLabel: "Don't add",
  },
  // Generic install (GENERIC-INSTALL-DECISION.md option A; ES spec
  // ES-FR-01/02): the approval display shows the agent-supplied installation
  // command/script itself, the purpose, the resolved destination workspace
  // and the installation scope. 'replace' mode (raw argument names like
  // target_workspace are jargon the summary translates); NO custom button
  // labels because a static label like "Install" would be wrong for
  // action=poll/read/kill calls, so the standard Approve/Deny row stays.
  // Title varies by action.
  environment_setup: {
    mode: 'replace',
    Body: EnvironmentSetupApprovalPreview,
    title: environmentSetupApprovalTitle,
  },
  create_agent: agentChangePreview,
  update_agent: agentChangePreview,
}
