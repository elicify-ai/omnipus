# Spec-2 — Connection-as-Instance Migration, Connectors UI & Basic Email (v0.1.0 Foundation)

- **Spec:** 2 of 6 (v0.1.0 Foundation)
- **Source ADR:** [ADR-019](../architecture/ADR-019-v01-workspaces-foundation.md) — FR-2 (+ scope corrections + email-dep decision); risk R2 (the one deliberate breaking change)
- **Status:** Rev 2 (addresses `…-review.md` BLOCK — 5 CRITICAL / 7 MAJOR) → pending re-`/grill-spec`
- **Cross-spec (Phase 3.5):** Connection binds to a Spec-1 `Workspace`; `identity{agent|user}` aligns with the agent-reference shape (Spec-3/4).
- **Constraints:** greenfield; single-user; contract-first; secrets credential-ref'd (SEC-23); cap = 1/type (v0.3 lifts).

## 1. Overview

Replace the **typed-singleton** channel config with a **`map[string]ChannelInstanceConfig`** (keyed by instance id, **cap 1/type** in v0.1.0); rewrite `initChannels` into a **loop**; **update the 13 factory constructors** (which read `cfg.Channels.<Typed>` themselves) + the extra readers to consume an instance config; modify the `ChannelEntry`/`ChannelConfigureRequest`/`ChannelId` contracts to carry **`instance_id` + `identity{agent|user}`** and add **`email`**; surface a **Connectors UI**; ship a **basic email channel** (IMAP-in via `emersion/go-imap` + SMTP-out via stdlib, one mailbox). The one deliberate breaking change, done up front.

**In scope (corrected blast radius — F-1/F-3):** the config map migration (cap 1); `initChannels` loop **+ a type→factory map** (`whatsapp`→`whatsapp_native`); **the 13 factory constructors** read instance config; the extra readers `emitGHSARemovalWarn`/`countEnabledChannels`/`config_old.go`; `ChannelEntry`/`ChannelConfigureRequest`/`ChannelId` contract modification + regen; the new `email` factory; Connectors UI; per-instance credential refs.
**Out of scope:** cap >1 (v0.3); OAuth (v0.3); new channel types beyond email; the 13 channels' message behaviour (only their config-reading + activation change).

## 2. Existing Codebase Context (grounded)

### Symbols Involved
| Symbol | Role | Context (grounded) |
|---|---|---|
| `config.ChannelsConfig` (13 typed fields) | **replace** → `Channels map[string]ChannelInstanceConfig{type,enabled,identity,<secret>_ref}` | `config.go:~774` |
| `Manager.initChannels` if-ladder | **rewrite** → loop + **type→factory map** | `manager.go:582`; `whatsapp`→`whatsapp_native` (F-6) |
| **13 factory constructors** (`ChannelFactory(cfg *config.Config,…)`) | **update** to read instance config, not `cfg.Channels.<Typed>` | `registry.go:15`; e.g. `telegram.go:66,189,370,415,907,916` (F-1) |
| `emitGHSARemovalWarn(cfg)`, `countEnabledChannels(cfg)`, **`config.go:1916` Discord-`MentionOnly` normalizer**, `config_old.go` | **migrate** to the map (rewrite, NOT exclude) | `gateway.go:2081`, `rest.go:2984`, `config.go:1916`, `config_old.go` (F-3 / R2-F1) |
| **`cmd/` callers** — `auth/weixin.go:117`, `auth/wecom.go:206`, `doctor/command.go:118` | **migrate** to the map | onboarding + `doctor` read typed fields (R3-F1) |
| `contracts/.../ChannelEntry.yaml` (`required:[id,name,transport,enabled,description]`, `additionalProperties:false`) | **modify** → + `instance_id`, `identity` | regen |
| `contracts/.../ChannelId.yaml` (closed enum, 14 values, **no email**) | **modify** → + `email` | `openapi_types.gen.go:819-832` (F-5) |
| `channelSensitiveFields map[gen.ChannelId][]string` + `channelCredKey(id,field)` + `validChannelIDs[gen.ChannelId(id)]` | **reconcile** (F-4): keep **type-keyed** sensitive-fields + validate the **type** against the enum; `channelCredKey` storage uses the **instance id** | `rest.go:2086,4693,4282` |
| `pkg/channels/email/` | **NEW** | `emersion/go-imap` (in) + `net/smtp` (out), pure-Go (ADR FR-2 dep decision) |

### Impact Assessment
| Modified | Risk | Direct (d=1) | Indirect (d=2) |
|---|---|---|---|
| `ChannelsConfig` shape | **CRITICAL** (breaking) | the 13 factory constructors + `initChannels` + `emitGHSARemovalWarn`/`countEnabledChannels`/`config_old.go` + load/save | every channel's enable path |
| `ChannelEntry`/`ChannelId`/`ChannelConfigureRequest` | **CRITICAL** (contract) | generated types + SPA Channels screen + REST + `channelSensitiveFields` enum keys | routing |
| cred-keying (type vs instance) | **HIGH** (SEC-23) | `configureChannel`, `channelCredKey`, `validChannelIDs` | secret-at-rest |
| email factory (new dep) | MEDIUM | factory registry, go.mod | Constraint #1 (ADR-approved) |

## 3. User Stories

**US-1 — Instance-map config, all readers migrated (P0).** **Independent test:** with the typed fields **deleted**, `go build ./...` succeeds (the compiler proves every reader is migrated) AND the AST guard (test #8) flags 0 `config.ChannelsConfig` selectors. 1. **Given** a telegram instance keyed "tg-1", **When** loaded, **Then** `Channels["tg-1"].Type=="telegram"`. 2. **Given** the typed fields are removed, **When** `go build ./...` runs, **Then** it succeeds only once every reader (compiler-surfaced) is migrated; the AST guard additionally catches alias-form reads (R4-F2).

**US-2 — Cap of one per type (P0).** 1. **Given** one telegram instance, **When** a 2nd telegram is added, **Then** 422 "one-per-type in v0.1.0". 2. **Given** the cap is a single constant, **When** v0.3 lifts it, **Then** only the constant changes.

**US-3 — `initChannels` loop + type→factory map (P0).** 1. **Given** the loop, **When** an `email` instance is enabled, **Then** it activates with no `initChannels` branch. 2. **Given** a `whatsapp`-type instance, **When** activated, **Then** the loop maps it to the `whatsapp_native` factory (F-6).

**US-4 — Contracts regenerate with instance_id+identity+email (P0).** 1. **Given** `ChannelId` += `email`, `ChannelEntry` += `instance_id`+`identity`, **When** `make verify-contracts` runs, **Then** exit 0; `additionalProperties:false` still holds (new fields declared).

**US-5 — Identity (agent|user) persisted + wired (P0).** *The identity selects how inbound messages are attributed/routed.* 1. **Given** `identity{kind:agent,id:X}`, **When** an inbound message arrives, **Then** the connection acts AS agent X (bound to X). 2. **Given** `identity{kind:user}`, **Then** inbound is attributed to the user and routed via the default agent (`ResolveRoute`). This spec **persists the field and wires it into the existing `ResolveRoute` input**; it does not change the routing algorithm. (F-12)

**US-6 — Per-instance secret refs, type-validated (P0, SEC-23).** 1. **Given** I configure "tg-1" with a token, **When** saved, **Then** `config.json` holds `token_ref` (no plaintext) and the cred store resolves it; the **sensitive-field lookup uses the type** (`telegram`), the **storage key uses the instance id** (`tg-1`). 2. **Given** an instance whose type isn't a valid `ChannelId`, **Then** configure rejects it (422).

**US-7 — Basic email channel (P1).** 1. **Given** IMAP+SMTP creds (host/port/user/`password_ref`), **When** enabled, **Then** inbound email reaches the bus (IMAP poll) and the agent sends via SMTP. 2. **Given** the cap, **Then** one email instance.

**US-8 — Connectors UI (P1).** 1. **Given** the Connectors screen, **When** I add/configure/test/enable, **Then** it works; a 2nd-of-type is blocked.

### Edge Cases
- Empty map → degraded boot (existing). · IMAP unreachable → non-fatal failure surfaced. · inline secret → migrated to ref on save. · duplicate instance id → rejected. · disabled instance → not activated. · `whatsapp` type → `whatsapp_native` factory.

## 4. Behavioral Contract · Non-Behaviors · Integration Boundaries

**Contract:** map keyed by instance; loop activation via type→factory map; 1/type (422 on 2nd); secrets `<field>_ref` (type-keyed fields, instance-keyed storage); identity persisted; email IMAP-in/SMTP-out; `verify-contracts` green.

**Non-behaviors:** must **not** allow >1/type; must **not** persist plaintext secrets; must **not** change the 13 channels' message behaviour (only config-reading+activation); must **not** add OAuth; must **not** leave typed `ChannelsConfig.<X>` access; must **not** key `channelSensitiveFields` by instance id (stays type-keyed); **greenfield** — no old config migration; must **not** run the full Go suite locally (CI authority).

**Integration boundaries:**
- **IMAP/SMTP (email):** IMAP-in via `emersion/go-imap` (pure-Go, poll interval); SMTP-out via stdlib `net/smtp`. Creds host/port/user/`password_ref`. Failure = non-fatal degraded; surfaced in Connectors. Dev = a local greenmail/dovecot container or a real test mailbox.
- **13 existing channel services:** unchanged wire behaviour; only their config source + activation change.

## 5. BDD Scenarios

```gherkin
Scenario: All 13 factories + extra readers compile against the map
  Traces to: US-1 / AC-1
  Category: Happy Path
  Given ChannelsConfig is a map[string]ChannelInstanceConfig
  When the gateway builds
  Then telegram/slack/…/the 13 factories and emitGHSARemovalWarn/countEnabledChannels read instance config and compile

Scenario: Typed-field-access guard is clean (excludes only openclaw)
  Traces to: US-1 / AC-2
  Category: Edge Case
  Given the migrated code (typed fields deleted; config_old.go's ToChannelsConfig migrated to emit the map)
  When the stdlib go/types AST guard scans pkg/+cmd/ for selectors resolving to config.ChannelsConfig, excluding only pkg/migrate/sources/openclaw
  Then there are 0 occurrences

Scenario: Second instance of a type rejected
  Traces to: US-2 / AC-1
  Category: Error Path
  Given one enabled telegram instance
  When I POST a second telegram
  Then 422 "one-per-type in v0.1.0"

Scenario: whatsapp type maps to the whatsapp_native factory
  Traces to: US-3 / AC-2
  Category: Alternate Path
  Given a whatsapp-type instance
  When the loop activates it
  Then it dispatches to the whatsapp_native factory

Scenario: Contracts regen with email + instance_id + identity
  Traces to: US-4 / AC-1
  Category: Happy Path
  Given ChannelId += email and ChannelEntry += instance_id,identity
  When make verify-contracts runs
  Then exit 0 and additionalProperties:false still validates

Scenario: Secret stored as ref; fields type-keyed, storage instance-keyed
  Traces to: US-6 / AC-1
  Category: Happy Path
  Given I configure instance tg-1 (type telegram) with a token
  When saved
  Then channelSensitiveFields lookup uses "telegram"
  And channelCredKey storage uses "tg-1"
  And config.json holds token_ref with no plaintext

Scenario: Email round-trips one mailbox
  Traces to: US-7 / AC-1
  Category: Happy Path
  Given valid IMAP+SMTP creds
  When an email arrives
  Then it reaches the bus (IMAP poll) and the agent replies via SMTP

Scenario: IMAP down degrades, not crash
  Traces to: US-7 (edge)
  Category: Error Path
  Given unreachable IMAP host
  When the channel starts
  Then a non-fatal failure is recorded, the gateway boots, Connectors shows the error
```

## 6. TDD Plan

| Order | Test | Level | Traces | Description |
|---|---|---|---|---|
| 1 | `TestChannelsConfig_InstanceMap_RoundTrip` | Unit | "compile against the map" | map load/save |
| 2 | `TestFactories_ReadInstanceConfig` | Integration | "13 factories compile" | each factory reads instance cfg |
| 3 | `TestChannels_CapOnePerType_422` | Integration | "second rejected" | cap |
| 4 | `TestInitChannels_LoopAndTypeFactoryMap` | Integration | "whatsapp→whatsapp_native" / "email no branch" | loop + map |
| 5 | `TestConfigureChannel_TypeKeyedFields_InstanceKeyedStorage` | Unit | "secret stored as ref" | F-4 reconciliation |
| 6 | `TestEmail_ImapSmtp_RoundTrip` | Integration | "email round-trips" | greenmail/dovecot |
| 7 | `TestEmail_ImapDown_DegradesNonFatal` | Integration | "IMAP down degrades" | degraded boot |
| 8 | `TestTypedFieldGuard_NoChannelsConfigSelector` | Integration (**stdlib `go/types`**, `pkg/`+`cmd/`) | "guard clean" | receiver-agnostic AST (stdlib, no x/tools — R5-F2); flags selectors resolving to `config.ChannelsConfig`; primary gate = `go build` after field deletion |
| 9 | `verify-contracts` (CI) | CI | "contracts regen" | drift = fail |
| 10 | `e2e: Connectors add/test/enable; 2nd blocked` | E2E | US-8 | SPA |

**Test Datasets**: one-instance→map; 2nd-of-type→422; inline-secret→ref; imap-down→degraded; whatsapp→whatsapp_native; instance-type-not-in-enum→422.

**Regression:** modifies the 13 channels' activation + config reading. (1) The 13 channels start/send unchanged (port enable tests); (2) `ChannelRouting` GET/PUT resolves; (3) the existing SEC-23 credential-ref tests pass with the type-keyed-fields/instance-keyed-storage split; (4) NEW: cap, type→factory, email, guard. **CI is the authority; local = scoped tests only.**

## 7. Functional Requirements & Success Criteria

- **FR-2.1:** MUST replace `ChannelsConfig` typed fields with `Channels map[string]ChannelInstanceConfig`.
- **FR-2.2 (completeness BY CONSTRUCTION):** the typed fields ARE **deleted** from `ChannelsConfig`, so **every reader becomes a Go compile error until migrated — `go build ./...` is the exhaustive completeness oracle.** No hand-enumeration is trusted (four review rounds each found another reader; a fifth count would too). Known readers the compiler will surface (non-exhaustive): the factory constructors (13 external; webchat internal); `emitGHSARemovalWarn`; `countEnabledChannels`; `buildEnabledRefMap`; `HandleChannels` (emits `ChannelEntry`); `toChannelHashes`/`toChannelConfig` (`pkg/channels/manager_channel.go`); `ResolveAll` (`pkg/credentials/inject.go` boot-path); `config.go:1916` (Discord normalizer); the `cmd/` callers (`auth/weixin.go`, `auth/wecom.go`, `doctor/command.go` — **both** blocks). **Composite-literal PRODUCERS must also be rewritten** (deleting the fields breaks their compile — R5-F1): `pkg/config/defaults.go:78` (seed literal → seed the map), `pkg/channels/manager_channel.go:108` (`&config.ChannelsConfig{}`), and the converters `pkg/migrate/sources/openclaw/openclaw_config.go:990 ToStandardChannels` + `pkg/config/config_old.go:113 ToChannelsConfig` → emit `map[string]ChannelInstanceConfig`. **`config_old.go` is the LIVE v0→v1 migration module (consumed by `config.go:1784` + `migration.go:37,484`) — KEEP it; rewrite only `ToChannelsConfig`, do NOT delete.** **"Excluded" (openclaw) means excluded from the typed-access GUARD only — NOT left uncompiled; every producer still compiles.** (R2-F1, R3-F1, R4-F1, R5-F1, R6-F1)
- **FR-2.3:** MUST cap 1 enabled instance/type — enforced both at the **API** (422 on a 2nd) **and at config LOAD** (a hand-edited `config.json` with 2 of a type is rejected/logged, not silently run); cap = single constant. (F-8)
- **FR-2.4:** MUST rewrite `initChannels` as a loop + a **type→factory map** (`whatsapp`→`whatsapp_native`).
- **FR-2.5:** MUST modify `ChannelId` (+`email`), `ChannelEntry` (+`instance_id`,`identity{kind,id}`), `ChannelConfigureRequest`; `verify-contracts` exits 0; `additionalProperties:false` preserved.
- **FR-2.6:** MUST keep `channelSensitiveFields` **type-keyed** + validate the **type** against `ChannelId`; `channelCredKey` storage uses the **instance id**; all secrets `<field>_ref` (SEC-23); no plaintext.
- **FR-2.7:** MUST add a pure-Go `email` channel (`emersion/go-imap` in + `net/smtp` out, one mailbox), **over TLS (IMAPS/SMTPS or STARTTLS — no plaintext auth)**, degraded-boot-safe; MUST distinguish **auth-failure (permanent — surface in Connectors, stop retrying)** from **unreachable/timeout (transient — retry with backoff)**. (Dep approved in ADR FR-2; F-11)
- **FR-2.8:** MUST surface a Connectors UI (cap-1 enforced).
- **FR-2.9:** Greenfield — no old config migration.

**Success Criteria**
- **SC-1:** `verify-contracts` exits 0 (CI). · **SC-2:** build + typecheck exit 0 (CI authority; local scoped). · **SC-3:** 2nd-of-type → 422. · **SC-4 (sound, receiver-agnostic):** **primary gate** = `CGO_ENABLED=0 go build -tags goolm,stdjson ./...` succeeds **after the typed fields are deleted** (every typed reader migrated by construction). **Secondary guard** (test #8) = an **AST pass using stdlib `go/parser`+`go/types`+`go/importer` (NOT `golang.org/x/tools` — avoids a new dep, R5-F2)** flagging any selector whose receiver type resolves to `config.ChannelsConfig` (alias-safe, R4-F2), excluding `migrate/sources/openclaw` (its own `OpenClawChannels` type); MUST report 0. (`config_old.go`'s `ToChannelsConfig` is migrated to emit the map — counted as migrated, not excluded.) · **SC-5:** 0 plaintext secrets; fields type-keyed, storage instance-keyed. · **SC-6:** email round-trips; IMAP-down boots degraded. · **SC-7:** the 13 channels start/send unchanged.

## 8. Traceability Matrix

| Req | US | BDD | Test |
|---|---|---|---|
| FR-2.1/2.2 | US-1 | "compile against the map" / "guard clean" | #1,#2,#8 |
| FR-2.3 | US-2 | "second rejected" | #3 |
| FR-2.4 | US-3 | "whatsapp→whatsapp_native" | #4 |
| FR-2.5 | US-4 | "contracts regen" | #9 |
| FR-2.6 | US-6 | "secret stored as ref…" | #5 |
| FR-2.7 | US-7 | "email round-trips" / "IMAP down" | #6,#7 |
| FR-2.8 | US-8 | (e2e) | #10 |
| FR-2.5 (identity) | US-5 | "compile against the map" | #1 |

## 9. Ambiguity Warnings

| # | Ambiguous | Likely assumption | Resolution |
|---|---|---|---|
| 1 | instance id source | server-generated | document |
| 2 | IMAP poll vs IDLE | poll interval (pure-Go) | RESOLVED — poll; IDLE later |
| 3 | IMAP dep / Constraint #1 | needs a dep | **RESOLVED — `emersion/go-imap` approved in ADR FR-2** |
| 4 | cred keying type vs instance | broke SEC-23 | **RESOLVED — fields type-keyed, storage instance-keyed (F-4)** |
| 5 | ChannelId closed enum | must add email | **RESOLVED — `ChannelId += email` (F-5)** |
| 6 | identity vs Spec-3/4 agent-reference | share the shape | Phase-3.5 cross-spec — note the shared `identity{kind,id}` |

## 10. Holdout Evaluation Scenarios *(post-impl; NOT in traceability)*
- H1: configure telegram + email, reboot → persist; 2nd telegram blocked.
- H2: real email send/receive round-trip.
- H3: `config.json` → only `_ref` secrets.
- H4: register a hypothetical new type → activates with no `initChannels` edit.
- H5: kill IMAP host → boots degraded; Connectors shows failure.
- H6: grep the branch diff → 0 typed `cfg.Channels.<X>` access outside `migrate/sources/openclaw`.

## 11. Assumptions
- Greenfield — no old channel config migrated. `[Q6/ADR]`
- `emersion/go-imap` (pure-Go) is the approved IMAP dep; SMTP via stdlib. `[ADR FR-2]`
- `channelSensitiveFields` stays type-keyed; secrets per-type field names, per-instance storage. `[F-4]`
- Identity routing uses existing `ResolveRoute`; this spec persists the field. `[FACT: pkg/routing]`
- `pkg/migrate/sources/openclaw` is excluded from the typed-access **guard** (its `OpenClawChannels` is a separate source type) **but its `ToStandardChannels` producer is rewritten to emit the map**; `config_old.go` (the **live v0→v1 migration** module) is **kept** — only its `ToChannelsConfig` is rewritten to emit the map (deleting it would break `config.go`/`migration.go`). Exclusion = guard-only, never left-uncompiled. `[F-7, R5-F1, R6-F1]`
