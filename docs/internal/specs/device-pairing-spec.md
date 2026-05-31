# Feature Specification: Device Pairing (v1.0)

**Created**: 2026-04-05
**Status**: Draft
**Input**: Omnipus v1.0 critical feature 2 — device pairing with admin approval flow

---

## Available Reference Patterns

> No applicable reference patterns — device pairing is net-new infrastructure.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/gateway/ws_approval.go:wsApprovalRegistry` | Reused by analogy | Registry pattern for async approval frames |
| `pkg/gateway/ws_approval.go:wsApprovalHook` | Reused by analogy | WebSocket hook sending approval frames and blocking for response |
| `src/store/ui.ts:useUiStore` | Extends | `addToast()` used for in-app notifications |
| `src/routes/_app/settings.tsx` | Extends | Settings tab panel structure |
| `pkg/gateway/rest.go:restAPI` | Extends | REST handler registration pattern |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|----------------|------------|-------------------|---------------------|
| `pkg/gateway/ws_approval.go` | LOW | `wsApprovalHook` callers (none in prod) | Agent loop |
| `src/store/ui.ts` | LOW | Toast consumers (toast-container) | Chat, Settings UI |
| `src/routes/_app/settings.tsx` | LOW | Settings tab content | All settings sections |
| `pkg/gateway/rest.go` | MEDIUM | REST API callers | Frontend TanStack Query hooks |
| `pkg/agent/instance.go` | LOW | Tool registration callers | Agent startup |

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| Agent startup | Tool registry initialization — no change needed |
| WebSocket session | New `device_pairing_request/response` frames traverse existing WS path |
| REST API bootstrap | New `/api/v1/devices` endpoints registered alongside existing REST endpoints |
| Settings page load | New Devices tab rendered alongside existing tabs |

### Cluster Placement

This feature belongs to the **Device Management** cluster and cross-cuts **Security** (admin approval gate) and **Frontend** (Settings UI).

---

## User Stories & Acceptance Criteria

### User Story 1 — Pair a new device (Priority: P0)

A user wants to pair a new device (e.g., phone, tablet, secondary desktop) with their Omnipus instance so they can use Omnipus from that device while maintaining security. The system must ensure only admin-approved devices can connect.

**Why this priority**: Core security feature — no device pairing without admin approval means v1.0 cannot safely support multi-device usage.

**Independent Test**: A new device can be registered, shows as pending in the admin UI, and is not usable until approved. Verified without any other features present.

**Acceptance Scenarios**:

1. **Given** a user opens a new device and enters the pairing code, **When** they submit the pairing request, **Then** the request appears in the admin's pending devices list within 5 seconds.
2. **Given** an admin is logged in, **When** they navigate to Settings → Devices, **Then** they see a list of all paired devices and any pending requests.
3. **Given** a user initiates pairing, **When** the admin has not yet approved, **Then** the user's device receives a "pending approval" notification and cannot access the system.

---

### User Story 2 — Admin approves or rejects a device (Priority: P0)

An admin wants to review pending device pairing requests and decide whether to allow or deny each one, so only trusted devices can access Omnipus.

**Why this priority**: Admin approval is the security control — without it, anyone who discovers the pairing code can connect.

**Independent Test**: Admin approves a pending device; the device immediately becomes usable. Admin rejects; the device remains blocked with no access.

**Acceptance Scenarios**:

1. **Given** an admin is viewing a pending device request, **When** they click "Approve", **Then** the device is added to the paired devices list and the device receives an approval notification.
2. **Given** an admin is viewing a pending device request, **When** they click "Reject", **Then** the pending request is removed and the device receives a rejection notification.
3. **Given** an admin approves a device, **When** the approved device reconnects, **Then** full access is granted without further approval.

---

### User Story 3 — View and manage paired devices (Priority: P1)

A user (or admin) wants to see all devices that have been paired with their account and revoke access for devices they no longer trust.

**Why this priority**: Devices can be lost or stolen — users need the ability to revoke access quickly.

**Independent Test**: Admin can view paired devices, see last-seen time, and revoke any device. Revoked device cannot reconnect.

**Acceptance Scenarios**:

1. **Given** an admin is on the Devices settings panel, **When** they view the paired devices list, **Then** each device shows name, type, last-seen timestamp, and a Revoke button.
2. **Given** an admin clicks "Revoke" on a paired device, **When** the device attempts to reconnect, **Then** the connection is refused and the device is removed from the paired list.

---

### User Story 4 — Device pairing without admin interruption for auto-approved devices (Priority: P2)

An admin wants to pre-approve device types (e.g., always-allow company-issued laptops) so that those devices can pair without manual approval, reducing friction for trusted hardware.

**Why this priority**: Convenience feature for managed environments — reduces approval overhead. Defer to post-v1.0 if timeline pressure.

**Independent Test**: A device on the auto-approve list can pair and connect without any admin action.

**Acceptance Scenarios**:

1. **Given** a device is on the admin-configured auto-approve list (by device fingerprint or type), **When** it submits a pairing request, **Then** it is approved automatically and immediately usable.
2. **Given** a device is not on the auto-approve list, **When** it submits a pairing request, **Then** it shows as pending and requires admin approval.

---

## Behavioral Contract

Primary flows:
- When a user initiates device pairing on a new device, the system registers a pending device and notifies the admin.
- When an admin approves a pending device, the device becomes active and usable.
- When an admin rejects a pending device, the request is discarded and the device is notified.
- When an admin revokes an active device, the device loses access immediately.

Error flows:
- When a device submits a pairing request but the admin never acts, the request times out after 24 hours and is automatically dismissed.
- When a device tries to connect without being paired, the system refuses the connection with an appropriate error.
- When a device tries to connect after being revoked, the system refuses the connection.

Boundary conditions:
- When the same device initiates multiple simultaneous pairing requests, only the first is accepted and others are rejected.
- When there are more than 20 pending devices (unreasonable volume), the oldest pending requests are automatically expired.

---

## Edge Cases

- What happens when the device's clock is significantly skewed (NTP failure)? Expected: Pairing codes are time-based — device must have reasonable time sync. System rejects pairing codes older than 5 minutes.
- What happens when the same device is paired twice? Expected: The new pairing request is rejected if the device already has an active pairing. Admin must revoke the old pairing first.
- What happens when the admin is offline (not logged in)? Expected: The pending request queues until the admin next logs in. No notification is sent.
- What happens when a device receives a pairing code via a man-in-the-middle? Expected: Pairing codes are single-use and time-limited — intercepted codes expire before reuse.
- What happens when the pending request times out? Expected: The device is notified of expiry and must initiate a new pairing request.
- What happens when a non-admin tries to access the Devices settings panel? Expected: The Devices tab is hidden entirely from non-admin users (UI-only enforcement, backend does not expose device data to non-admin tokens regardless).

---

## Explicit Non-Behaviors

- The system must not allow a device to connect without admin approval (even if the user is authenticated on another device), because the threat model includes physical device theft.
- The system must not reveal pending device names or any device details to non-admin users, because even the existence of a pending request leaks security-relevant information.
- The system must not auto-approve devices based solely on being on the same network, because NAT'd networks are not a trust signal.
- The system must not store device tokens in plaintext, because device tokens are equivalent to session tokens.

---

## Integration Boundaries

### Pairing Request (Outbound Message)

- **Data in**: Device name, device type, device fingerprint (SHA-256 of public key), pairing code
- **Data out**: Confirmation of pending status with estimated wait time
- **Contract**: HTTPS POST to `/api/v1/devices/pair`, returns `{ request_id, status: "pending", expires_at }`
- **On failure**: Device shows "Could not reach server" — retry with exponential backoff, max 3 attempts
- **Development**: Mock server that echoes the request and stores it in memory for testing

### Approval Decision (Admin → Device)

- **Data in**: Admin decision (approve/reject), request_id
- **Data out**: Device receives push notification of decision
- **Contract**: WebSocket `device_pairing_response` frame with `{ type: "device_pairing_response", id, decision }`
- **On failure**: If device is offline, decision is stored and delivered on next connection (max 7 days)
- **Development**: Real WS connection between browser and gateway (same process)

### REST API: List Devices

- **Data in**: Bearer token (JWT), optional `?status=pending|active|revoked`
- **Data out**: JSON array of device records `{ id, name, type, status, last_seen, created_at }`
- **Contract**: `GET /api/v1/devices`, `DELETE /api/v1/devices/{id}`, `POST /api/v1/devices/approve`
- **On failure**: Non-admin tokens receive 403; expired tokens receive 401
- **Development**: Real REST handlers in `pkg/gateway/rest.go`

---

## BDD Scenarios

### Feature: Device Pairing

---

#### Scenario: User initiates pairing on a new device

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the user has opened Omnipus on a new device and navigated to the pairing flow
- **And** the device has a valid system clock (within 5 minutes of server time)
- **When** the user enters their account credentials and submits the pairing request
- **Then** the server returns a pairing request ID with status "pending" and an expiry timestamp
- **And** the device displays "Waiting for admin approval..." with the request ID

---

#### Scenario: User sees pending approval notification on new device

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a user has submitted a pairing request and it is awaiting admin approval
- **When** the device polls or holds an open connection
- **Then** the device UI shows "Pending Approval" and does not grant access to any Omnipus features
- **And** no chat, task, or agent functionality is accessible

---

#### Scenario: Admin sees pending device in Settings → Devices

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an admin is logged in and navigates to Settings → Devices
- **When** the page loads
- **Then** a "Pending" section is visible showing any devices awaiting approval
- **And** each pending item shows device name, device type, and relative time since request (e.g., "2 minutes ago")
- **And** Approve and Reject buttons are shown for each pending item

---

#### Scenario: Admin approves a pending device

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an admin is viewing a pending device in the Devices settings panel
- **When** the admin clicks the "Approve" button
- **Then** the device is moved from "Pending" to "Active" section
- **And** the device receives a WebSocket `device_pairing_response` frame with decision "approved"
- **And** the device immediately transitions to fully functional state

---

#### Scenario: Admin rejects a pending device

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an admin is viewing a pending device in the Devices settings panel
- **When** the admin clicks the "Reject" button
- **Then** the pending request is removed from the list
- **And** the device receives a WebSocket `device_pairing_response` frame with decision "rejected"
- **And** the device displays "Pairing request denied by administrator" and returns to the login screen

---

#### Scenario: Approved device reconnects successfully

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a device was previously approved by an admin
- **When** the device reconnects using its stored device token
- **Then** the system validates the token and grants full access without requiring re-approval
- **And** the last-seen timestamp for the device is updated

---

#### Scenario: Admin revokes an active device

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an admin is viewing an active device in the Devices settings panel
- **When** the admin clicks the "Revoke" button
- **Then** the device is immediately moved to "Revoked" section (or removed)
- **And** the device's stored token is invalidated
- **And** if the device is currently connected, the connection is terminated
- **And** the next connection attempt by the device is refused

---

#### Scenario: Non-admin user cannot access Devices panel

**Traces to**: User Story 3, Acceptance Scenario 1 (implicit)
**Category**: Error Path

- **Given** a regular user (non-admin) is logged in
- **When** the user navigates to the Settings page
- **Then** the Devices tab is not rendered in the tab list
- **And** a direct API call to `GET /api/v1/devices` returns HTTP 403

---

#### Scenario: Pairing request expires after 24 hours

**Traces to**: Edge Case: admin offline
**Category**: Edge Case

- **Given** a device has submitted a pairing request and the admin has not acted upon it
- **When** 24 hours pass since the request was made
- **Then** the pending request is automatically expired
- **And** the device is notified on next connection attempt that the pairing code has expired
- **And** the admin no longer sees the request in the pending list

---

#### Scenario: Duplicate pairing request for already-paired device is rejected

**Traces to**: Edge Case: same device paired twice
**Category**: Edge Case

- **Given** a device is already paired and active
- **When** the user on that device attempts to initiate a new pairing request
- **Then** the server returns HTTP 409 Conflict with message "Device is already paired"
- **And** no new pending request is created
- **And** the user is directed to contact their admin if they need to re-pair

---

#### Scenario: Unpaired device attempts to access system

**Traces to**: Edge Case: device tries to connect without being paired
**Category**: Error Path

- **Given** a device attempts to connect without having an active pairing
- **When** the gateway validates the device token (or lack thereof)
- **Then** the connection is refused with HTTP 401 and message "Device not paired"
- **And** no agent loop or chat access is granted

---

#### Scenario: Device on auto-approve list pairs without admin action

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a device's fingerprint matches an admin-configured auto-approve pattern
- **When** the device submits a pairing request
- **Then** the request is auto-approved without appearing in the pending list
- **And** the device receives an immediate approval response
- **And** the admin sees the device listed in the "Active" section with note "Auto-approved"

---

#### Scenario: Pairing code is single-use

**Traces to**: Edge Case: pairing code interception
**Category**: Edge Case

- **Given** a user initiates pairing and receives a pairing code
- **When** that code has been used once (regardless of whether pairing succeeded or failed)
- **Then** the code is invalidated and cannot be reused
- **And** a new pairing request must be initiated for a fresh code

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                                      | Purpose                                        |
|-------------|--------------------------------------------|------------------------------------------------|
| Unit        | Pairing token generation, expiry, validation | Validates token logic in isolation              |
| Unit        | REST handlers (approve/reject/ revoke)     | Validates handler logic without network        |
| Unit        | WebSocket frame serialization               | Validates frame format                         |
| Integration | Full pairing flow: request → notify → approve → connect | Validates end-to-end across components |
| Integration | Settings UI Devices panel                   | Validates panel renders correctly with mock API |
| E2E         | Complete pairing from device A through approval on device B | Full browser-based test |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestPairingCodeGeneration` | Unit | Scenario: Pairing code is single-use | Generates a code, verifies it is stored and single-use |
| 2 | `TestPairingCodeExpiry` | Unit | Scenario: Pairing request expires after 24 hours | Code expires after 24h, expired code is rejected |
| 3 | `TestDeviceAlreadyPaired` | Unit | Scenario: Duplicate pairing request is rejected | Already-paired device cannot create new pending request |
| 4 | `TestApproveDevice` | Unit | Scenario: Admin approves a pending device | Approve handler moves device to active, notifies via channel |
| 5 | `TestRejectDevice` | Unit | Scenario: Admin rejects a pending device | Reject handler removes pending, sends rejection frame |
| 6 | `TestRevokeDevice` | Unit | Scenario: Admin revokes an active device | Revoke handler invalidates token, removes from active list |
| 7 | `TestNonAdminForbidden` | Unit | Scenario: Non-admin user cannot access Devices panel | 403 returned for non-admin GET /api/v1/devices |
| 8 | `TestUnpairedDeviceDenied` | Unit | Scenario: Unpaired device attempts to access system | 401 returned for device connection without valid token |
| 9 | `TestAutoApprovePattern` | Unit | Scenario: Device on auto-approve list pairs without admin action | Device matching auto-approve pattern is immediately active |
| 10 | `TestWSDevicePairingFrameSerialization` | Unit | Scenario: Admin approves a pending device | wsServerFrame with type "device_pairing_request" serializes correctly |
| 11 | `TestFullPairingFlow` | Integration | Scenario: User initiates pairing → admin approves → device connects | In-process test with mock WS and REST |
| 12 | `TestSettingsDevicesPanelRenders` | Integration | Scenario: Admin sees pending device in Settings → Devices | Panel renders with mock API response |
| 13 | `TestBrowserPairingFlow` | E2E | Full flow in browser with real server | Playwright test, admin on desktop Chrome, device on mobile |

### Test Datasets

#### Dataset: Pairing Code Validity

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | Valid, unused code within 5-min window | Happy path | Accept code, create pending request | Scenario: User initiates pairing | Normal successful flow |
| 2 | Expired code (>5 min since generation) | Error | Reject with "code expired" | Scenario: Pairing code is single-use | Clock skew test |
| 3 | Already-used code | Error | Reject with "code already used" | Scenario: Pairing code is single-use | Replay attack |
| 4 | Malformed code (wrong length) | Error | Reject with 400 Bad Request | N/A | Input validation |
| 5 | Code for non-existent request ID | Error | Reject with 404 Not Found | N/A | Non-existent resource |

#### Dataset: Device Status Transitions

| # | Initial State | Action | Expected State | Traces to | Notes |
|---|--------------|--------|----------------|-----------|-------|
| 1 | None (unpaired) | Submit valid pairing request | Pending | Scenario: User initiates pairing | New device |
| 2 | Pending | Admin clicks Approve | Active | Scenario: Admin approves | Normal approval |
| 3 | Pending | Admin clicks Reject | (request removed) | Scenario: Admin rejects | Rejection |
| 4 | Pending | 24 hours pass | (request removed) | Scenario: Pairing request expires | Timeout |
| 5 | Active | Admin clicks Revoke | Revoked | Scenario: Admin revokes | Access revoked |
| 6 | Active | Device reconnects | Active (updated last-seen) | Scenario: Approved device reconnects | Normal use |
| 7 | Revoked | Device attempts to connect | Connection refused | Scenario: Admin revokes | Token invalidated |
| 8 | Already active | New pairing request submitted | 409 Conflict | Scenario: Duplicate pairing | Must revoke first |

#### Dataset: Admin vs Non-Admin Access

| # | Role | Endpoint | Expected Status | Traces to | Notes |
|---|------|----------|----------------|-----------|-------|
| 1 | admin | GET /api/v1/devices | 200 + device list | Scenario: Admin sees pending device | Normal admin access |
| 2 | admin | POST /api/v1/devices/approve | 200 | Scenario: Admin approves | Normal admin action |
| 3 | admin | DELETE /api/v1/devices/{id} | 200 | Scenario: Admin revokes | Normal admin action |
| 4 | user | GET /api/v1/devices | 403 Forbidden | Scenario: Non-admin user cannot access Devices panel | Role enforcement |
| 5 | user | POST /api/v1/devices/approve | 403 Forbidden | N/A | Role enforcement |
| 6 | user | DELETE /api/v1/devices/{id} | 403 Forbidden | N/A | Role enforcement |
| 7 | unauthenticated | GET /api/v1/devices | 401 Unauthorized | N/A | No token |
| 8 | device (no user token) | GET /api/v1/devices | 401 Unauthorized | N/A | Device token only |

### Regression Test Requirements

> No regression impact — new capability. Integration seams protected by: existing REST handler registration pattern (rest.go), existing WS frame handling (websocket.go), and existing Settings tab structure (settings.tsx). All existing tests continue to pass unchanged.

---

## Functional Requirements

- **FR-001**: System MUST generate a unique, time-limited pairing code when a user initiates device pairing on a new device.
- **FR-002**: System MUST send a WebSocket `device_pairing_request` frame to the admin's open session when a new pairing request is created.
- **FR-003**: System MUST NOT grant any Omnipus functionality to a device with a pending pairing request.
- **FR-004**: System MUST allow admin users to approve or reject pending pairing requests from the Settings → Devices panel.
- **FR-005**: System MUST notify the pairing device of the admin's decision via a WebSocket `device_pairing_response` frame.
- **FR-006**: System MUST allow admin users to revoke active device pairings, immediately invalidating the device's access token.
- **FR-007**: System MUST reject connections from devices that have been revoked or have no pairing record.
- **FR-008**: System MUST return HTTP 403 for any non-admin user attempting to access `/api/v1/devices` endpoints.
- **FR-009**: System MUST expire pending pairing requests after 24 hours if no admin action is taken.
- **FR-010**: System MUST allow admin to configure device fingerprint patterns for auto-approval (auto-approve list).
- **FR-011**: System MUST prevent the same device from initiating a new pairing request while an active pairing exists.

---

## Success Criteria

- **SC-001**: A new device can complete the full pairing flow (submit request → wait → receive approval → gain access) in under 60 seconds from the admin clicking Approve.
- **SC-002**: Non-admin users cannot access device management functionality via UI (tab not visible) or API (403 returned).
- **SC-003**: Revoked devices are unable to connect within 5 seconds of the revoke action being completed.
- **SC-004**: Pending pairing requests that are not acted upon by an admin automatically expire within 24 hours ± 1 minute.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s)          | Test Name(s)                    |
|-------------|-----------|--------------------------|---------------------------------|
| FR-001      | US-1      | Scenario: User initiates pairing | TestPairingCodeGeneration |
| FR-002      | US-1      | Scenario: Admin sees pending device | TestFullPairingFlow |
| FR-003      | US-1      | Scenario: User sees pending approval notification | TestUnpairedDeviceDenied |
| FR-004      | US-2      | Scenario: Admin approves a pending device | TestApproveDevice |
| FR-005      | US-2      | Scenario: Admin approves a pending device | TestApproveDevice, TestWSDevicePairingFrameSerialization |
| FR-006      | US-3      | Scenario: Admin revokes an active device | TestRevokeDevice |
| FR-007      | US-3      | Scenario: Admin revokes an active device | TestUnpairedDeviceDenied |
| FR-008      | US-3      | Scenario: Non-admin user cannot access Devices panel | TestNonAdminForbidden |
| FR-009      | US-1      | Scenario: Pairing request expires after 24 hours | TestPairingCodeExpiry |
| FR-010      | US-4      | Scenario: Device on auto-approve list pairs without admin action | TestAutoApprovePattern |
| FR-011      | US-1      | Scenario: Duplicate pairing request for already-paired device is rejected | TestDeviceAlreadyPaired |

---

## Ambiguity Warnings

All ambiguities resolved — see Clarifications section below.

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only.
> Do not reference these in the TDD plan or traceability matrix.

### Scenario: Happy path pairing across two browsers
- **Setup**: User is logged into Omnipus on a desktop Chrome browser. They open Firefox on the same desktop to simulate a "new device".
- **Action**: User initiates pairing on Firefox, gets a 6-digit code, types it into Chrome's admin approval UI, clicks Approve on Chrome.
- **Expected outcome**: Firefox immediately transitions from "pending" to fully functional state with access to chat and agents.
- **Category**: Happy Path

### Scenario: Revoked device cannot reconnect
- **Setup**: Device B is actively paired and connected. Admin is on desktop Chrome.
- **Action**: Admin clicks Revoke on Device B. Device B is disconnected (or a second tab is opened attempting to use Device B's token).
- **Expected outcome**: Device B's connection is terminated within 5 seconds and reconnect is refused with "Access denied — device has been revoked."
- **Category**: Happy Path

### Scenario: Non-admin cannot even see the Devices panel
- **Setup**: A regular user (non-admin) is logged in on desktop Chrome.
- **Action**: User navigates to Settings and inspects all available tabs.
- **Expected outcome**: The Devices tab is absent from the Settings tab list. Direct API call to GET /api/v1/devices returns 403.
- **Category**: Error

### Scenario: Pairing request expires while admin is away
- **Setup**: User initiates pairing on a new device. Admin is on vacation for 3 days.
- **Action**: 25 hours pass. User tries to use the pending device.
- **Expected outcome**: Device shows "Pairing request expired. Please request a new pairing code." Admin's pending list no longer shows the request.
- **Category**: Edge Case

### Scenario: User tries to pair the same device twice
- **Setup**: Device A is actively paired and working.
- **Action**: User on Device A navigates to pairing flow and attempts to submit a new pairing request.
- **Expected outcome**: Server returns HTTP 409 Conflict: "This device is already paired. Contact your administrator to revoke the existing pairing before setting up a new one."
- **Category**: Error

### Scenario: Auto-approved device bypasses pending state
- **Setup**: Admin has configured an auto-approve pattern matching "company-laptop-*". A device named "company-laptop-daniel" submits a pairing request.
- **Action**: Device submits the pairing request.
- **Expected outcome**: Device appears directly in the Active section (not Pending) with "Auto-approved" annotation. No admin action required.
- **Category**: Happy Path

### Scenario: Man-in-the-middle cannot reuse intercepted pairing code
- **Setup**: User on Device A gets pairing code "123456". An attacker on the same network intercepts this code.
- **Action**: Attacker (or the same user on a different device) attempts to use code "123456" to pair.
- **Expected outcome**: The code is rejected because it was already consumed by the original device's pairing attempt. Attacker sees "Invalid or expired pairing code."
- **Category**: Edge Case

---

## Assumptions

- Pairing initiation happens on an already-authenticated session (the user is logged in on Device A and wants to add Device B). This means the pairing code is tied to the user's identity, not a registration flow.
- Admin is always a logged-in Omnipus user. There is no separate admin portal — the admin uses the same Omnipus UI with elevated privileges.
- Device tokens are JWTs signed by the gateway with a 7-day expiry and refresh token rotation.
- Pairing requests are stored in the existing JSON file-based store (like tasks and sessions), not in a separate database.
- Devices are personal (user-owned), not shared. A shared device would have one pairing per user, not one pairing per device.
- Pairing codes are 6 numeric digits, expiring after 5 minutes, single-use.

## Clarifications

### 2026-04-05

- Q: How is the pairing code delivered from the admin's device to the new device? -> A: The user on Device A sees the code on screen and manually types it into Device B (the new device being paired). Think of it like Discord's "add a server" 6-digit code flow. Code expires after 5 minutes.
- Q: Does the admin need to be online to get the notification? -> A: WebSocket notification fires when admin is online. For offline admins, a badge count on the Settings nav item updates via polling every 30 seconds. The admin never misses a pending request.
- Q: Is there a maximum number of devices per account? -> A: Default limit of 5 active devices per user account. Admin can configure (min 1, no hard max).
- Q: What uniquely identifies a device? -> A: Ed25519 key pair generated on first launch. Public key hash = device fingerprint. Private key stored in OS Keychain (iOS Keychain, Android Keystore, Electron safeStorage, Linux libsecret).
- Q: Where is the device token stored on the device? -> A: OS Keychain — same as private key. Never in localStorage, never in an app-managed file.
- Q: How is device type determined? -> A: User-Agent string sent during pairing request. Server infers device type (e.g., "Windows Desktop") with an OS icon. Both user and admin can override with a manual label.
