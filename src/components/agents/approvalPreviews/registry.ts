// Tool-approval preview registry: tool name -> readable summary entry.
//
// ToolApprovalModal.tsx looks a tool name up here before falling back to the
// generic Tool-name line + raw Arguments JSON dump. Add a new tool by adding
// a new entry — nothing else in the modal needs to change, and every tool
// without an entry keeps working exactly as before (see mode's doc comment
// in types.ts for 'additive' vs 'replace').

import { BashApprovalPreview } from './BashApprovalPreview'
import { RequestMountApprovalPreview } from './RequestMountApprovalPreview'
import type { ToolApprovalPreviewEntry } from './types'

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
    showCancel: false,
  },
}
