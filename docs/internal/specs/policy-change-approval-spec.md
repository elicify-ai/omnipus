# Feature Specification: Policy Change Approval (v1.0)

**Created**: 2026-04-05
**Status**: Draft
**Input**: Omnipus v1.0 critical feature 3 — admin approval for security policy changes

---

## Overview

The system currently has a `ToolApprover` interface (`pkg/agent/hooks.go`) that handles one-time tool call approvals via WebSocket frames (`exec_approval_request` / `exec_approval_response`). The agent can also persistently modify security policies (e.g., add exec allowlist patterns, disable exec approval mode, modify SSRF whitelist) — but there is no approval gate for these **persistent policy changes**.

This spec adds a `PolicyApprover` interface and WebSocket-based approval flow for when the agent proposes security policy modifications. Only admin users can approve policy changes.

**Key distinction:**

| | ToolApprover | PolicyApprover |
|--|--|--|
| **What it approves** | One-time tool call | Persistent policy change |
| **Scope** | Single invocation | Written to disk, affects future sessions |
| **Can the user approve?** | Yes (interactive WS approval) | No — admin only |
| **Example** | "Allow `rm /tmp/foo` once" | "Always allow `rm /tmp/*` pattern" |
| **Decision storage** | In-memory `alwaysAllowed` map for session | Written to `exec-allowlist.json`, config file, etc. |

---

## Available Reference Patterns

> No applicable reference patterns — PolicyApprover is net-new infrastructure.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/agent/hooks.go:ToolApprover` | Reused by analogy | Interface pattern to follow |
| `pkg/agent/hooks.go:ApprovalVerdict` | Reused by analogy | Verdict enum for policy decisions |
| `pkg/gateway/ws_approval.go:wsApprovalRegistry` | Reused by analogy | Registry + channel pattern for async approval |
| `pkg/gateway/ws_approval.go:wsApprovalHook` | Reused by analogy | WS hook sending frames and blocking |
| `pkg/security/execapproval.go:ExecApprovalManager` | Extended | Persists exec allowlist patterns; `PersistPattern` is the write target |
| `pkg/security/execapproval.go:MatchExecAllowlist` | Extended | Used at exec tool runtime to check if command is allowed |
| `pkg/config/config.go:ToolsConfig` | Extended | Config fields that can be changed via policy approval |
| `pkg/gateway/rest.go:restAPI` | Extends | REST handlers for pending policy approval list |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|----------------|------------|-------------------|---------------------|
| `pkg/agent/hooks.go` | LOW | `HookManager.Mount` callers | Existing tool/interceptor hooks |
| `pkg/gateway/ws_approval.go` | LOW | WS handler frame processing | Browser approval UI |
| `pkg/security/execapproval.go` | MEDIUM | `exec` tool runtime | Any code calling `MatchExecAllowlist` |
| `pkg/gateway/rest.go` | MEDIUM | Frontend TanStack Query | Settings UI |
| `pkg/config/config.go` | LOW | Config file read/write | All config consumers |

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| Agent tool call (exec) | `ToolApprover` gates one-time approval; `PolicyApprover` gates persistent pattern changes |
| Agent proposes policy change | Agent calls a new internal API → `PolicyApprover.ApprovePolicyChange` → WS frame to admin |
| Admin approves policy | WS frame → `policyApprovalRegistry.resolve` → policy written to disk |
| Exec tool runtime | `MatchExecAllowlist` checks against `exec-allowlist.json` (updated by PolicyApprover) |

---

## Policy Types Subject to Approval

The following policy changes require admin approval before being committed:

| Policy Type | Description | Risk if Auto-Approved |
|------------|-------------|----------------------|
| `exec_allowlist_add` | Add a glob pattern to `exec-allowlist.json` | Medium — enables persistent exec |
| `exec_mode_change` | Change `tools.exec.approval` from "ask" to "off" | High — disables exec security |
| `ssrf_whitelist_add` | Add host/URL pattern to SSRF allowlist | Medium — weakens SSRF protection |
| `tool_enable` | Enable a currently-disabled tool (e.g., `exec`) | High — expands attack surface |
| `tool_disable` | Disable a currently-enabled tool | Low — security improvement |

For v1.0, only `exec_allowlist_add` and `exec_mode_change` are in scope. The others are deferred.

---

## User Stories & Acceptance Criteria

### User Story 1 — Agent proposes a policy change (Priority: P0)

An agent, when blocked by a security policy during task execution, proposes a policy change (e.g., "allow this command pattern permanently") so that the admin can review and approve or reject it.

**Why this priority**: Without a mechanism for the agent to request policy relaxation, agents are permanently blocked by restrictive policies — making the system unusable for legitimate tasks.

**Acceptance Scenarios**:

1. **Given** an agent encounters a command not in the exec allowlist and the user approves "always allow", **When** the agent proposes adding the pattern to the allowlist, **Then** the admin receives a `policy_approval_request` frame via WebSocket.
2. **Given** the admin has already approved an identical pattern, **When** the agent proposes the same pattern again, **Then** the pattern is auto-approved without sending a frame to the admin.

---

### User Story 2 — Admin reviews and approves a policy change (Priority: P0)

An admin wants to review pending policy change requests and approve or reject each one from the Settings UI, so only intentional security policy modifications are committed.

**Why this priority**: Policy changes directly affect the security posture. Admin review prevents the agent from silently expanding its own permissions.

**Acceptance Scenarios**:

1. **Given** an admin navigates to Settings → Policy Approvals, **When** they see a pending request for "add `curl *` to exec allowlist", **Then** they can click Approve or Reject.
2. **Given** an admin clicks Approve on an `exec_allowlist_add` request, **When** the pattern is committed to `exec-allowlist.json`, **Then** future exec calls matching that pattern are auto-approved without further prompts.
3. **Given** an admin clicks Reject on a policy request, **When** the agent retries the blocked operation, **Then** the agent is told the policy change was denied.

---

### User Story 3 — Policy approval requests expire (Priority: P1)

Pending policy approval requests that are not acted upon must expire so that stale requests don't accumulate and so the admin is not indefinitely on the hook.

**Why this priority**: Prevents indefinite queuing and ensures the agent eventually knows the answer.

**Acceptance Scenarios**:

1. **Given** a policy approval request has been pending for more than 24 hours, **When** the admin or agent checks its status, **Then** it is marked as expired and the agent is notified.
2. **Given** a policy approval request expires, **When** the agent retries the blocked operation, **Then** the agent is told the approval request timed out and must be re-initiated.

---

## Behavioral Contract

Primary flows:
- When an agent proposes a policy change, a `policy_approval_request` frame is sent to the admin's open WebSocket session and the agent blocks.
- When an admin approves a pending policy request, the change is written to the appropriate config/policy file and the agent unblocks.
- When an admin rejects a pending policy request, the change is discarded and the agent is notified of the rejection.

Error flows:
- When a policy approval request expires (24h timeout), the agent unblocks with an "approval timed out" error.
- When the admin WebSocket is disconnected, pending requests queue until reconnection.
- When an identical policy request arrives while one is already pending, the duplicate is rejected (request already in flight).

Boundary conditions:
- When there are more than 20 pending policy requests, older requests are automatically expired.
- When the agent proposes a policy change that would disable all exec commands, the admin must explicitly approve — it cannot be auto-approved.

---

## Explicit Non-Behaviors

- The system must not allow non-admin users to approve policy changes — the `PolicyApprover` interface only delivers decisions from admin sessions.
- The system must not commit a policy change to disk before the admin approves it — no optimistic writes.
- The system must not allow the agent to bypass a rejected policy change by re-proposing it immediately — there must be a cooldown (5 minutes) after rejection before the same policy can be proposed again.
- The system must not auto-approve policy changes that disable security controls (e.g., `exec_mode_change` from "ask" to "off") — these always require explicit admin approval.

---

## Integration Boundaries

### Policy Change Request (Agent → Gateway)

- **Data in**: Policy type, current value, proposed value, justification, proposed by (agent/session ID)
- **Data out**: Request ID, status (pending/approved/rejected/expired), estimated wait
- **Contract**: Internal Go call → `PolicyApprover.ApprovePolicyChange(ctx, req)` → returns after decision
- **On failure**: Returns error; agent surfaces error to user with the text

### Policy Approval Frame (Gateway → Admin Browser)

- **Data in**: Request ID, policy type, current value, proposed value, justification, timestamp
- **Data out**: Admin decision via `policy_approval_response` frame
- **Contract**: WebSocket `wsServerFrame{type: "policy_approval_request", ...}`
- **On failure**: If admin is offline, request queues — no silent drop

### REST API: Policy Approvals

- **Data in**: Bearer token (JWT), optional `?status=pending|approved|rejected`
- **Data out**: JSON array of `PolicyApprovalRequest` records
- **Contract**: `GET /api/v1/policy-approvals`, `POST /api/v1/policy-approvals/{id}/approve`, `POST /api/v1/policy-approvals/{id}/reject`
- **On failure**: Non-admin tokens receive 403; pending requests are never exposed to non-admin

---

## New WebSocket Frame Types

### Server → Client: `policy_approval_request`

```json
{
  "type": "policy_approval_request",
  "id": "pol-uuid-string",
  "policy_type": "exec_allowlist_add",
  "current_value": ["curl *", "wget *"],
  "proposed_value": ["curl *", "wget *", "rm -rf /tmp/foo/*"],
  "justification": "Need to clean temp files before saving output",
  "risk": "medium",
  "requested_at": "2026-04-05T10:00:00Z",
  "expires_at": "2026-04-06T10:00:00Z"
}
```

### Client → Server: `policy_approval_response`

```json
{
  "type": "policy_approval_response",
  "id": "pol-uuid-string",
  "decision": "approve"
}
```

`decision` is one of: `"approve"`, `"reject"`.

---

## Functional Requirements

- **FR-001**: System MUST provide a `PolicyApprover` interface in `pkg/agent/hooks.go` with method `ApprovePolicyChange(ctx, *PolicyChangeRequest) (*PolicyChangeDecision, error)`.
- **FR-002**: System MUST mount a `wsPolicyApprovalHook` in the WebSocket handler that sends `policy_approval_request` frames to the admin's session and blocks until a decision is received.
- **FR-003**: System MUST commit approved policy changes to disk immediately upon receiving the approval decision — no delayed writes.
- **FR-004**: System MUST reject duplicate policy approval requests (same policy type + same proposed value) while a pending request for that change exists.
- **FR-005**: System MUST expire pending policy approval requests after 24 hours if no admin acts on them.
- **FR-006**: System MUST NOT expose pending policy approval requests to non-admin users via REST API (HTTP 403) or WebSocket.
- **FR-007**: System MUST provide a `GET /api/v1/policy-approvals` REST endpoint that returns pending (and optionally historical) policy approval requests for admin users.
- **FR-008**: System MUST notify the agent when a pending policy approval request is approved, rejected, or expired — unblocking the agent's blocked task.
- **FR-009**: System MUST cooldown re-proposal of the same rejected policy change for 5 minutes.
- **FR-010**: System MUST NOT auto-approve `exec_mode_change` from "ask" to "off" — always requires explicit admin approval.

---

## BDD Scenarios

### Feature: Policy Change Approval

---

#### Scenario: Agent proposes exec allowlist pattern addition

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an agent encounters an exec command not in the allowlist and the user has approved "always allow"
- **When** the agent proposes adding the command pattern to the exec allowlist
- **Then** a `policy_approval_request` frame is sent to the admin's open WebSocket session
- **And** the agent's task is blocked pending the approval decision
- **And** a pending request appears in Settings → Policy Approvals

---

#### Scenario: Admin approves an exec allowlist addition

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an admin is viewing a pending policy approval in Settings → Policy Approvals
- **When** the admin clicks "Approve" on a request to add `curl *` to the exec allowlist
- **Then** `curl *` is added to `exec-allowlist.json` via atomic write
- **And** the agent's blocked task is unblocked with an "approved" notification
- **And** the request moves from "Pending" to "Approved" in the history

---

#### Scenario: Admin rejects a policy change

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** an admin is viewing a pending policy approval in Settings → Policy Approvals
- **When** the admin clicks "Reject"
- **Then** the policy change is not committed to disk
- **And** the agent's blocked task is unblocked with a "rejected" notification containing the admin's reason (if any)
- **And** a 5-minute cooldown begins before the same policy can be proposed again

---

#### Scenario: Duplicate policy request rejected while one is pending

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path

- **Given** a policy approval request for `exec_allowlist_add: "curl *"` is already pending
- **When** the agent proposes the same policy change again
- **Then** the duplicate request is rejected immediately without sending a frame to the admin
- **And** the agent is told "a request for this pattern is already pending"

---

#### Scenario: Policy approval request expires after 24 hours

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a policy approval request has been pending for more than 24 hours
- **When** the admin views Settings → Policy Approvals
- **Then** the request is marked as "Expired"
- **And** the agent's blocked task is unblocked with "approval request expired"
- **And** the agent can initiate a new approval request after expiry

---

#### Scenario: Non-admin cannot see pending policy approvals

**Traces to**: FR-006
**Category**: Error Path

- **Given** a non-admin user is logged in
- **When** they navigate to Settings or call `GET /api/v1/policy-approvals`
- **Then** the Policy Approvals section is not visible in the UI
- **And** the API call returns HTTP 403

---

#### Scenario: Exec mode change always requires explicit admin approval

**Traces to**: FR-010
**Category**: Happy Path

- **Given** an agent proposes changing `tools.exec.approval` from "ask" to "off"
- **When** the admin is presented with the approval request
- **Then** the request is NOT auto-approved regardless of prior approvals
- **And** the admin must explicitly click "Approve" to commit the change

---

#### Scenario: Agent unblocked immediately on approval

**Traces to**: FR-008
**Category**: Happy Path

- **Given** an agent is blocked waiting for a policy approval decision
- **When** the admin clicks "Approve" in the Settings UI
- **Then** the agent is unblocked within 2 seconds of the admin's click
- **And** the agent retries the previously blocked operation with the updated policy in place

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | `PolicyApprover` interface implementation | Validates interface contract |
| Unit | `policyApprovalRegistry` register/resolve/unregister | Validates registry logic |
| Unit | Policy write to disk (exec-allowlist.json) | Validates atomic write |
| Unit | Cooldown logic after rejection | Validates 5-minute cooldown |
| Integration | Agent proposes policy → WS frame → admin approves → disk written | Full flow |
| Integration | Settings Policy Approvals panel | Validates panel with mock API |
| E2E | Full policy approval in browser | Playwright E2E |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestPolicyApproverInterface` | Unit | N/A (interface contract) | Verify `PolicyApprover` interface is correctly defined |
| 2 | `TestPolicyApprovalRegistryRegisterResolve` | Unit | Scenario: Agent proposes exec allowlist pattern addition | Register request; resolve with decision; verify unregister |
| 3 | `TestPolicyApprovalRegistryDuplicate` | Unit | Scenario: Duplicate policy request rejected while one is pending | Second register for same policy returns existing channel |
| 4 | `TestPolicyApprovalExpiry` | Unit | Scenario: Policy approval request expires after 24 hours | Background goroutine expires after 24h; agent unblocked |
| 5 | `TestPolicyApprovalCooldown` | Unit | Scenario: Admin rejects a policy change | Re-proposal within 5 min returns error; after 5 min succeeds |
| 6 | `TestExecAllowlistAtomicWrite` | Unit | Scenario: Admin approves an exec allowlist addition | Pattern written to `exec-allowlist.json`; file is valid JSON |
| 7 | `TestPolicyApprovalRESTForbiddenForNonAdmin` | Unit | Scenario: Non-admin cannot see pending policy approvals | GET /api/v1/policy-approvals returns 403 for non-admin token |
| 8 | `TestPolicyApprovalAutoApproveAlwaysDenied` | Unit | Scenario: Exec mode change always requires explicit admin approval | `exec_mode_change` to "off" never auto-approved regardless of history |
| 9 | `TestFullPolicyApprovalFlow` | Integration | Scenario: Agent proposes → admin approves → agent unblocked | In-process test: agent blocks, admin approves, agent resumes |
| 10 | `TestSettingsPolicyApprovalsPanelRenders` | Integration | Scenario: Admin sees pending policy approval in Settings | Mock API; verify panel renders correctly |
| 11 | `TestBrowserPolicyApprovalFlow` | E2E | Full flow in browser | Playwright: agent proposes, admin approves in browser |

### Test Datasets

#### Dataset: Policy Approval Decision Outcomes

| # | Policy Type | Proposed Value | Admin Decision | Expected Disk State | Traces to |
|---|------------|---------------|----------------|-------------------|-----------|
| 1 | `exec_allowlist_add` | `"curl *"` | Approve | `"curl *"` added to `exec-allowlist.json` | Scenario: Admin approves |
| 2 | `exec_allowlist_add` | `"curl *"` | Reject | No change | Scenario: Admin rejects |
| 3 | `exec_mode_change` | `"off"` | Approve | `tools.exec.approval = "off"` in config | Scenario: Exec mode change |
| 4 | `exec_mode_change` | `"off"` | Reject | No change | Scenario: Admin rejects |
| 5 | `exec_allowlist_add` | `"curl *"` | (24h timeout) | No change | Scenario: Expires |
| 6 | `exec_allowlist_add` | `"curl *"` | Approve | `"curl *"` NOT added twice (dedup) | Scenario: Duplicate |

#### Dataset: Cooldown After Rejection

| # | Time Since Rejection | Re-proposal Result | Traces to |
|---|---------------------|-------------------|-----------|
| 1 | 0 minutes | Error: "cooldown active, try again in X minutes" | Scenario: Cooldown |
| 2 | 4 minutes | Error: "cooldown active, try again in 1 minute" | Scenario: Cooldown |
| 3 | 5 minutes | Success: new pending request created | Scenario: Cooldown |
| 4 | 10 minutes | Success: new pending request created | Scenario: Cooldown |

---

## Functional Requirements

- **FR-001**: System MUST provide a `PolicyApprover` interface in `pkg/agent/hooks.go` with method `ApprovePolicyChange(ctx, *PolicyChangeRequest) (*PolicyChangeDecision, error)`.
- **FR-002**: System MUST mount a `wsPolicyApprovalHook` in the WebSocket handler that sends `policy_approval_request` frames to the admin's session and blocks until a decision is received.
- **FR-003**: System MUST commit approved policy changes to disk immediately upon receiving the approval decision — no optimistic writes.
- **FR-004**: System MUST reject duplicate policy approval requests (same policy type + same proposed value) while a pending request for that change exists.
- **FR-005**: System MUST expire pending policy approval requests after 24 hours if no admin acts on them.
- **FR-006**: System MUST NOT expose pending policy approval requests to non-admin users via REST API (HTTP 403) or WebSocket.
- **FR-007**: System MUST provide a `GET /api/v1/policy-approvals` REST endpoint for admin users.
- **FR-008**: System MUST notify the agent when a pending policy approval request is approved, rejected, or expired.
- **FR-009**: System MUST cooldown re-proposal of the same rejected policy change for 5 minutes.
- **FR-010**: System MUST NOT auto-approve `exec_mode_change` from "ask" to "off".

---

## Success Criteria

- **SC-001**: A blocked agent is unblocked within 2 seconds of the admin clicking Approve in the Settings UI.
- **SC-002**: Approved policy changes are durable — after a restart, the `exec-allowlist.json` reflects all approved patterns.
- **SC-003**: Non-admin users receive HTTP 403 when attempting to access the policy approvals API.
- **SC-004**: Policy approval requests that expire unblock the agent within 5 seconds of the 24-hour mark.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|-----------------|--------------|
| FR-001 | N/A (interface) | N/A | TestPolicyApproverInterface |
| FR-002 | US-1 | Scenario: Agent proposes exec allowlist pattern | TestPolicyApprovalRegistryRegisterResolve, TestFullPolicyApprovalFlow |
| FR-003 | US-2 | Scenario: Admin approves an exec allowlist addition | TestExecAllowlistAtomicWrite, TestFullPolicyApprovalFlow |
| FR-004 | US-1 | Scenario: Duplicate policy request rejected | TestPolicyApprovalRegistryDuplicate |
| FR-005 | US-3 | Scenario: Policy approval expires after 24 hours | TestPolicyApprovalExpiry |
| FR-006 | US-2 | Scenario: Non-admin cannot see pending approvals | TestPolicyApprovalRESTForbiddenForNonAdmin |
| FR-007 | US-2 | Scenario: Admin sees pending approval in Settings | TestSettingsPolicyApprovalsPanelRenders |
| FR-008 | US-2 | Scenario: Agent unblocked immediately on approval | TestFullPolicyApprovalFlow |
| FR-009 | US-2 | Scenario: Admin rejects, 5-min cooldown | TestPolicyApprovalCooldown |
| FR-010 | US-2 | Scenario: Exec mode change always requires explicit approval | TestPolicyApprovalAutoApproveAlwaysDenied |

---

## Ambiguity Warnings

All ambiguities resolved — see Clarifications section below.

---

## Clarifications

### 2026-04-05

- Q: Who can approve policy changes (admin only, or the approving user)? -> A: Admin only. Non-admin users can trigger the agent to *propose* a policy change, but only admin users can approve/reject. This is consistent with confirmed approach: "user sees notification but only admin can approve."
- Q: What does the agent's blocked task show to the user while waiting for approval? -> A: The chat shows a system message "Awaiting admin approval for policy change: [summary]. This may take time." The agent does not respond with any task output until the policy is approved or rejected.
- Q: What is the file storage location for policy approvals state? -> A: Pending requests stored in `~/.omnipus/policy-approvals.jsonl` (day-partitioned like sessions). Approved exec patterns written to `~/.omnipus/exec-allowlist.json`. Config changes written to `~/.omnipus/config.json` via the existing config manager.
- Q: Does the agent see the admin's reason when rejected? -> A: Yes — the admin can optionally type a reason when rejecting. The agent receives the reason and can surface it to the user.
- Q: What happens when multiple admins are connected? -> A: The `policy_approval_request` frame is sent to all connected admin sessions. The first admin to click Approve/Reject wins. Other admin sessions see the request update to "already decided."

---

## Assumptions

- Only one `PolicyApprover` implementation exists: the `wsPolicyApprovalHook` sending frames to admin sessions.
- Policy approval requests are not replicated across multiple Omnipus instances (single-node only in v1.0).
- The agent proposes policy changes via an internal API call (not a tool call the LLM can invoke directly) — the agent's reasoning engine decides when to propose a policy change based on blocked exec operations.
- Rejected policy changes are not stored in a history accessible to the agent — only the cooldown timestamp is stored to prevent spam.
- Policy approvals are stored in a JSONL file (not a database) — same as sessions and tasks.
