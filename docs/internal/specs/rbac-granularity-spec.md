# Feature Specification: RBAC Granularity — Admin/User Role Separation (v1.0)

**Created**: 2026-04-05
**Status**: Draft
**Input**: Omnipus v1.0 critical feature 4 — `admin` + `user` role separation with 403 gates and frontend menu hiding

---

## Overview

Omnipus is currently a single-user system: a single bearer token authenticates all requests. There is no role model. This spec adds a two-role model (`admin` and `user`) to enforce access control on sensitive operations.

**Confirmed scope (from prior conversation):**
- Two roles: `admin` and `user` — no custom roles, no per-agent permissions
- Config-based: roles stored in `config.json` (not a separate user DB)
- Frontend: hide admin-only menu items from non-admin users (not grayed-out, not disabled — hidden)
- Backend: 403 Forbidden on admin endpoints when called by a non-admin token

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/gateway/rest.go:withAuth` | Must add role check | Currently only checks bearer token validity; must also verify role |
| `pkg/gateway/rest.go:checkBearerAuth` | Must be extended | Currently returns bool; needs to also return role |
| `pkg/config/config.go:Config` | Extends | Add `Users []UserConfig` field |
| `pkg/config/config.go:UserConfig` | New type | `{token_hash, role, label}` |
| `pkg/gateway/rest.go:registerAdditionalEndpoints` | May need new admin-only endpoints | Policy approvals, device approvals |
| `src/routes/_app/settings.tsx` | Frontend gate | Conditionally render admin-only tabs |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|----------------|------------|-------------------|---------------------|
| `pkg/gateway/rest.go:withAuth` | CRITICAL | All REST handlers | All API consumers |
| `pkg/gateway/rest.go:checkBearerAuth` | CRITICAL | `withAuth`, WS handler | Token validation everywhere |
| `pkg/config/config.go` | MEDIUM | Config file format | All config consumers |
| `src/routes/_app/settings.tsx` | LOW | Settings tab components | Settings nav, routing |

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| REST API auth | `withAuth` → `checkBearerAuth` → validate token → check role → handler |
| WebSocket auth | Token validated at connect time; role stored in session context |
| Settings page load | Role determined at page load; admin-only tabs conditionally rendered |
| Device/Pairing approvals | Admin-only REST endpoints gated by `withAuth` + role check |

---

## User Stories & Acceptance Criteria

### User Story 1 — Admin-only REST endpoints return 403 for non-admin tokens (Priority: P0)

Non-admin users must be unable to access admin-only API endpoints, even if they have a valid bearer token, so that the admin panel and policy controls are protected.

**Why this priority**: Without backend 403 enforcement, frontend menu hiding is purely cosmetic — a determined user could call the API directly.

**Acceptance Scenarios**:

1. **Given** a user with role `user` holds a valid bearer token, **When** they call `GET /api/v1/devices`, **Then** the server returns HTTP 403 with `{"error": "admin access required"}`.
2. **Given** a user with role `admin` holds a valid bearer token, **When** they call `GET /api/v1/devices`, **Then** the server returns HTTP 200 with the device list.

---

### User Story 2 — Admin-only UI elements are hidden from non-admin users (Priority: P0)

Non-admin users must not see admin-only menu items or settings tabs, so they are not prompted with features they cannot use.

**Why this priority**: Cleaner UX — users see only what they can interact with. No grayed-out options, no misleading affordances.

**Acceptance Scenarios**:

1. **Given** a user with role `user` is logged into the frontend, **When** they open the Settings page, **Then** the Devices tab is not visible in the tab list.
2. **Given** a user with role `admin` is logged into the frontend, **When** they open the Settings page, **Then** the Devices tab is visible in the tab list.
3. **Given** a user with role `user` is logged into the frontend, **When** they look at the navigation sidebar, **Then** no admin-only nav items are visible.

---

### User Story 3 — Role assignment in config (Priority: P0)

The system owner must be able to assign roles to tokens via `config.json` so that the RBAC model is enforceable without an external identity provider.

**Why this priority**: The config file is the source of truth for all security configuration. Roles must live there.

**Acceptance Scenarios**:

1. **Given** `config.json` contains two users: one with `role: "admin"` and one with `role: "user"`, **When** each user calls a protected endpoint, **Then** their respective role-based access is enforced.
2. **Given** a new installation with default config, **When** the system starts, **Then** a default admin token is generated (or the user is guided through first-run onboarding to set an admin token).

---

## Behavioral Contract

- When a REST request arrives with a valid bearer token, the system determines the caller's role from `config.json` (token_hash → user → role).
- When a REST request targets an admin-only endpoint and the caller's role is `user`, the server returns HTTP 403.
- When a REST request targets an admin-only endpoint and the caller's role is `admin`, the server processes the request normally.
- When a REST request arrives with an unrecognized or expired token, the server returns HTTP 401 (not 403).
- When the frontend loads Settings, it renders admin-only tabs only if the current user's role is `admin`.

---

## Explicit Non-Behaviors

- The system must not grant admin access by default — the first admin must be explicitly configured in `config.json`.
- The system must not allow a `user` to escalate their own role — there is no self-service role change mechanism.
- The system must not show admin-only tabs even as grayed-out or disabled — they must be entirely absent from the DOM.
- The system must not store role information in bearer tokens (JWT or otherwise) — role is always resolved from `config.json` at request time.
- The system must not support multiple simultaneous user sessions with different roles from the same browser tab — each tab has one role (determined by which token it holds).

---

## Admin-Only Endpoints (v1.0 scope)

The following endpoints require `admin` role:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/devices` | GET | List paired devices |
| `/api/v1/devices/approve` | POST | Approve pending device |
| `/api/v1/devices/reject` | POST | Reject pending device |
| `/api/v1/devices/{id}` | DELETE | Revoke device |
| `/api/v1/policy-approvals` | GET | List pending policy approvals |
| `/api/v1/policy-approvals/{id}/approve` | POST | Approve policy change |
| `/api/v1/policy-approvals/{id}/reject` | POST | Reject policy change |
| `/api/v1/audit-log` | GET | View audit log |
| `/api/v1/backup` | POST | Create backup |
| `/api/v1/backups` | GET | List backups |
| `/api/v1/restore` | POST | Restore from backup |
| `/api/v1/credentials` | GET/POST/DELETE | Manage credentials |
| `/api/v1/config/gateway/rotate-token` | POST | Rotate gateway token |

The following are available to all authenticated users (`user` or `admin`):
- Chat, sessions, tasks, agents, skills, tools, channels, providers, MCP servers, user-context, status, about

---

## Config Schema

### `config.json` changes

```json
{
  "gateway": {
    "token": "bcrypt_hash_of_token",
    "users": [
      {
        "token_hash": "bcrypt_hash_of_token",
        "role": "admin",
        "label": "Primary Admin"
      },
      {
        "token_hash": "bcrypt_hash_of_token",
        "role": "user",
        "label": "Daniel's Phone"
      }
    ]
  }
}
```

**Backward compatibility**: If `users` is absent (existing configs), the `token` field is treated as an admin token (single-user mode). This ensures existing deployments continue to work without changes.

**Token hashing**: Tokens are never stored in plaintext. Use bcrypt with cost 12. The `token` field in existing configs (plaintext or hashed) is migrated on first read.

**Token format**: Bearer tokens are random 32-byte hex strings (64 hex chars), generated by `openssl rand -hex 32` or equivalent.

### New `UserConfig` type

```go
type UserConfig struct {
    TokenHash string `json:"token_hash"`
    Role      string `json:"role"`   // "admin" or "user"
    Label     string `json:"label"` // e.g., "Daniel's Phone"
}
```

---

## Implementation Tasks

### 1. Add `Users` field to `GatewayConfig` (`pkg/config/config.go`)

```go
type GatewayConfig struct {
    Port            string            `json:"port"`
    Token           string            `json:"token,omitempty"`  // Deprecated: use Users[0]
    TokenHash       string            `json:"token_hash,omitempty"`
    Users           []UserConfig      `json:"users,omitempty"`
    AllowedOrigins  []string          `json:"allowed_origins,omitempty"`
    // ...
}
```

### 2. Add `UserConfig` type and role constants

```go
type UserConfig struct {
    TokenHash string `json:"token_hash"`
    Role      string `json:"role"`
    Label     string `json:"label"`
}

const (
    RoleAdmin = "admin"
    RoleUser  = "user"
)
```

### 3. Extend `checkBearerAuth` to return role

Current signature: `func checkBearerAuth(w http.ResponseWriter, r *http.Request) bool`
New signature: `func checkBearerAuth(w http.ResponseWriter, r *http.Request) (bool, string /* role */)`

```go
func checkBearerAuth(w http.ResponseWriter, r *http.Request) (bool, string) {
    token := extractBearerToken(r)
    if token == "" {
        http.Error(w, "missing bearer token", http.StatusUnauthorized)
        return false, ""
    }
    role := resolveRoleFromToken(token)
    if role == "" {
        http.Error(w, "invalid token", http.StatusUnauthorized)
        return false, ""
    }
    return true, role
}
```

### 4. Add `requireAdmin` middleware

```go
func (a *restAPI) withAuth(handler http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if ok, role := checkBearerAuth(w, r); !ok {
            return
        }
        ctx := context.WithValue(r.Context(), ctxKeyRole, role)
        ctx = context.WithValue(ctx, ctxKeyToken, extractBearerToken(r))
        r = r.WithContext(ctx)
        a.setCORSHeaders(w, r)
        r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
        handler(w, r)
    }
}

func (a *restAPI) requireAdmin(handler http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if role, ok := r.Context().Value(ctxKeyRole).(string); !ok || role != RoleAdmin {
            jsonErr(w, http.StatusForbidden, "admin access required")
            return
        }
        handler(w, r)
    }
}
```

### 5. Register admin-only endpoints with `requireAdmin`

```go
cm.RegisterHTTPHandler("/api/v1/devices", a.withAuth(a.requireAdmin(a.HandleDevices)))
cm.RegisterHTTPHandler("/api/v1/devices/", a.withAuth(a.requireAdmin(a.HandleDevices)))
cm.RegisterHTTPHandler("/api/v1/policy-approvals", a.withAuth(a.requireAdmin(a.HandlePolicyApprovals)))
cm.RegisterHTTPHandler("/api/v1/policy-approvals/", a.withAuth(a.requireAdmin(a.HandlePolicyApprovals)))
// ... etc.
```

### 6. Frontend: add role to auth state

In `src/lib/api.ts`, extract role from the token response or a new `/api/v1/me` endpoint:

```ts
// GET /api/v1/me → { role: 'admin' | 'user', label: string }
interface MeResponse { role: 'admin' | 'user'; label: string }

// Store in auth state
const role = useAuthStore(s => s.role) // 'admin' | 'user'
```

### 7. Frontend: conditionally render Settings tabs

```tsx
// In Settings page
const role = useAuthStore(s => s.role)

<TabsList>
  <TabsTrigger value="providers">Providers</TabsTrigger>
  {/* ... other tabs visible to all ... */}
  {role === 'admin' && <TabsTrigger value="devices">Devices</TabsTrigger>}
  {role === 'admin' && <TabsTrigger value="security">Security</TabsTrigger>}
  {role === 'admin' && <TabsTrigger value="policy-approvals">Policy</TabsTrigger>}
  {/* ... rest of tabs ... */}
</TabsList>
```

### 8. Frontend: add `/Me` endpoint to backend

```go
func (a *restAPI) HandleMe(w http.ResponseWriter, r *http.Request) {
    role, _ := r.Context().Value(ctxKeyRole).(string)
    label := resolveLabelFromToken(extractBearerToken(r))
    jsonOK(w, map[string]string{"role": role, "label": label})
}
```

---

## Test Datasets

#### Dataset: Role-Based Access Control

| # | Token Hash | Role | Endpoint | Method | Expected Status | Traces to |
|---|-----------|------|----------|--------|----------------|-----------|
| 1 | Valid admin hash | admin | GET /api/v1/devices | GET | 200 | US-1 AC2 |
| 2 | Valid user hash | user | GET /api/v1/devices | GET | 403 | US-1 AC1 |
| 3 | Valid user hash | user | GET /api/v1/tasks | GET | 200 | Non-admin endpoint |
| 4 | Invalid token | none | GET /api/v1/devices | GET | 401 | Invalid token |
| 5 | Valid admin hash | admin | DELETE /api/v1/devices/{id} | DELETE | 200 | US-3 |
| 6 | Valid user hash | user | DELETE /api/v1/devices/{id} | DELETE | 403 | US-1 AC1 |
| 7 | Valid admin hash | admin | POST /api/v1/backup | POST | 200 | US-3 |
| 8 | Valid user hash | user | POST /api/v1/backup | POST | 403 | US-1 AC1 |
| 9 | Empty token | none | GET /api/v1/devices | GET | 401 | Edge case |
| 10 | Expired/revoked token | none | GET /api/v1/devices | GET | 401 | Edge case |

---

## BDD Scenarios

### Feature: RBAC — Admin/User Role Separation

---

#### Scenario: Non-admin token receives 403 on admin endpoint

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Error Path

- **Given** a bearer token with `role: "user"` is included in the request
- **When** the request is made to `GET /api/v1/devices`
- **Then** the server returns HTTP 403 with body `{"error": "admin access required"}`

---

#### Scenario: Admin token accesses admin endpoint successfully

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a bearer token with `role: "admin"` is included in the request
- **When** the request is made to `GET /api/v1/devices`
- **Then** the server returns HTTP 200 with the device list JSON

---

#### Scenario: User token accesses non-admin endpoint successfully

**Traces to**: Non-behavior coverage
**Category**: Happy Path

- **Given** a bearer token with `role: "user"` is included in the request
- **When** the request is made to `GET /api/v1/tasks`
- **Then** the server returns HTTP 200 with the task list

---

#### Scenario: Invalid token receives 401 on any endpoint

**Traces to**: Edge Case
**Category**: Error Path

- **Given** a request is made with an unrecognized or expired token
- **When** the request is made to any protected endpoint
- **Then** the server returns HTTP 401 (not 403)

---

#### Scenario: Non-admin user does not see Devices tab in Settings

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a user with role `user` is logged into the frontend
- **When** the Settings page is rendered
- **Then** the Devices tab is not present in the DOM
- **And** no network request is made to `/api/v1/devices`

---

#### Scenario: Admin user sees Devices tab in Settings

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a user with role `admin` is logged into the frontend
- **When** the Settings page is rendered
- **Then** the Devices tab is visible in the tab list

---

#### Scenario: Backward compatible single-token config continues to work

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an existing `config.json` with only `gateway.token: "hex_token"` and no `users` array
- **When** the system starts and processes a request with `Authorization: Bearer hex_token`
- **Then** the request is accepted with role `admin`
- **And** no migration or configuration change is required

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | `checkBearerAuth` with role resolution | Validates token → role mapping |
| Unit | `requireAdmin` middleware | Validates 403 for non-admin |
| Unit | Config migration (single token → users array) | Validates backward compatibility |
| Unit | Frontend role-gated tab rendering | Validates conditional rendering |
| Integration | Admin token → admin endpoint → 200 | Full happy path |
| Integration | User token → admin endpoint → 403 | Full error path |
| E2E | Admin and user log in from different browsers | Full role isolation |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestCheckBearerAuthReturnsRole` | Unit | Scenario: Admin token accesses admin endpoint | Token hash → role mapping returns correct role |
| 2 | `TestCheckBearerAuthUnknownToken` | Unit | Scenario: Invalid token receives 401 | Unknown token → 401 |
| 3 | `TestRequireAdminAllowsAdmin` | Unit | Scenario: Admin token accesses admin endpoint | `requireAdmin` passes admin through |
| 4 | `TestRequireAdminDeniesUser` | Unit | Scenario: Non-admin token receives 403 | `requireAdmin` returns 403 for user role |
| 5 | `TestConfigMigrationSingleToken` | Unit | Scenario: Backward compatible single-token config | Old config with only `token` field → admin role |
| 6 | `TestDevicesEndpoint403ForUser` | Integration | Scenario: Non-admin token receives 403 on admin endpoint | User token → GET /api/v1/devices → 403 |
| 7 | `TestDevicesEndpoint200ForAdmin` | Integration | Scenario: Admin token accesses admin endpoint | Admin token → GET /api/v1/devices → 200 |
| 8 | `TestTasksEndpoint200ForUser` | Integration | Scenario: User token accesses non-admin endpoint | User token → GET /api/v1/tasks → 200 |
| 9 | `TestSettingsTabsHiddenForNonAdmin` | Integration | Settings rendered with user role → Devices tab absent | |
| 10 | `TestBrowserAdminUserIsolation` | E2E | Admin browser → sees Devices tab; user browser → Devices tab absent | Playwright |

---

## Regression Test Requirements

> The `withAuth` change (adding role return) is a behavioral change to the auth middleware. All existing tests that call `withAuth` must be checked:

| Existing Behavior | Existing Test | Regression Risk |
|-----------------|---------------|----------------|
| `withAuth` always passes to handler after token check | Many REST handler tests | HIGH — handler tests need updating to account for role context |

**Mitigation**: `requireAdmin` is a new wrapper — existing handlers without it continue to work. Only new admin-gated handlers use `requireAdmin`. Backward compatibility test covers the single-token migration case.

---

## Functional Requirements

- **FR-001**: System MUST support multiple users with roles (`admin` or `user`) stored in `config.json` under `gateway.users`.
- **FR-002**: System MUST return HTTP 403 when a `user`-role token accesses an admin-only endpoint.
- **FR-003**: System MUST return HTTP 401 when a request has an unrecognized, expired, or missing bearer token.
- **FR-004**: System MUST treat a config with a bare `gateway.token` field (no `users` array) as a single-admin deployment for backward compatibility.
- **FR-005**: System MUST resolve the caller's role from `config.json` at request time — role is not embedded in the token.
- **FR-006**: System MUST expose the caller's role to REST handlers via `r.Context()` so handlers can make contextual decisions.
- **FR-007**: Frontend MUST NOT render admin-only Settings tabs (Devices, Policy Approvals, Security) when the current user's role is `user`.
- **FR-008**: Frontend MUST provide a `/api/v1/me` endpoint returning the current user's `role` and `label`.

---

## Success Criteria

- **SC-001**: All admin-only REST endpoints return 403 within 50ms when called with a `user`-role token.
- **SC-002**: An existing `config.json` with only `gateway.token` (no `users` array) continues to work without modification after upgrade — admin role is inferred.
- **SC-003**: A user browsing the Settings page with role `user` cannot locate the Devices tab via URL navigation, tab order, or DOM inspection.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|-----------------|--------------|
| FR-001 | US-3 | Scenario: Backward compatible single-token config | TestConfigMigrationSingleToken |
| FR-002 | US-1 | Scenario: Non-admin token receives 403 | TestRequireAdminDeniesUser, TestDevicesEndpoint403ForUser |
| FR-003 | US-1 | Scenario: Invalid token receives 401 | TestCheckBearerAuthUnknownToken |
| FR-004 | US-3 | Scenario: Backward compatible single-token config | TestConfigMigrationSingleToken |
| FR-005 | US-1 | Scenario: Admin token accesses admin endpoint | TestCheckBearerAuthReturnsRole |
| FR-006 | US-1 | (Implementation detail) | Covered by FR-002 tests |
| FR-007 | US-2 | Scenario: Non-admin user does not see Devices tab | TestSettingsTabsHiddenForNonAdmin |
| FR-008 | US-2 | (Implementation detail) | TestHandleMeReturnsRole |

---

## Ambiguity Warnings

All ambiguities resolved — see Clarifications section below.

---

## Clarifications

### 2026-04-05

- Q: How is the initial admin token created? -> A: On first run, if no `gateway.token` and no `gateway.users` exist in config, the system prompts the user through the onboarding flow to set an initial admin token. This is consistent with the onboarding flow already in the codebase.
- Q: Can a user-role token call `/api/v1/me` to discover admin-only endpoints? -> A: Yes — `/api/v1/me` returns `{role, label}` which includes the user's own role. This is not a security concern (you need a valid token to call it, and it only reveals your own role).
- Q: How does the frontend learn the user's role at page load? -> A: On login, the role is returned alongside (or in lieu of) the token. It is stored in the auth store and refreshed on page reload via `GET /api/v1/me`.
- Q: Can there be multiple admins? -> A: Yes — `gateway.users` is an array. Multiple users can have `role: "admin"`.
- Q: What happens if `gateway.users` has a user with `role: "user"` but also has the old `gateway.token` field pointing to a different token? -> A: Both tokens work — the old `gateway.token` is treated as an admin token (backward compat), and each entry in `users` is resolved individually. Admins can deprecate the old token by removing `gateway.token`.
- Q: Is there a `device` role for device-pairing context? -> A: No — device tokens are separate from user tokens. A device pairs to a user account, and that user's role determines what that device can do. If a user is `user` role, their paired devices are also `user`-role devices.

---

## Assumptions

- The system is single-node in v1.0 — no distributed auth state. All role resolution is from the local `config.json`.
- Tokens are 64-character hex strings (`openssl rand -hex 32`), stored as bcrypt hashes in config.
- The onboarding flow already handles initial token generation — the RBAC spec reuses that mechanism.
- WebSocket connections use the same bearer token auth as REST — the role is determined at WS connection time and stored in the connection context.
