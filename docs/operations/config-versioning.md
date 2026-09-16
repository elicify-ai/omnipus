# Config Schema Versioning Guide

## Overview

Omnipus uses a schema versioning system for `config.json` to ensure smooth upgrades as the configuration format evolves. The current schema version is declared in `pkg/config/config.go` as `const CurrentVersion = 1`.

## Version History

### Version 1

**Introduction:** Initial version with version field support.

**Changes:** Added `version` field to the Config struct.

**Migration:** No structural changes are needed for existing configs.

## How It Works

### Architecture

The versioning system is built around three pieces:

1. **`CurrentVersion`** — a single integer constant in `pkg/config/config.go` (currently `1`) that names the current schema. The version is stored on the `Config` struct itself (`Version int`).
2. **`loadConfig(data []byte) (*Config, error)`** in `pkg/config/migration.go` — the v1 loader. It unmarshals a v1 JSON blob into the current `*Config`, applying two pre-unmarshal shims:
   - `jsonRenameKey(data, "model_list", "providers")` to accept the legacy key as an alias.
   - `detectUnknownConfigFields(data, cfg)` to capture unknown top-level keys for round-trip preservation.
3. **`configV0.Migrate() (*Config, error)`** — a method on the legacy `configV0` struct (defined in `pkg/config/config_old.go`) that converts an old-style config into the current `*Config`. The companion `MigrateWithStore` variant also moves plaintext secrets into the encrypted credential store during the migration.

There is no per-version `loadConfigV1`, `applyMigration`, or `migrateConfig` step function. The v0 case is handled by `loadConfigV0` (which returns a `migratable`), followed by a direct call to `Migrate()` or `MigrateWithStore()`.

### Automatic Migration

When you load a config file:
1. The system reads the `version` field from the JSON (or treats a missing field as `0`).
2. For `version == CurrentVersion`, it calls `loadConfig(data)` to unmarshal directly into `*Config`.
3. For `version == 0` (legacy), it calls `loadConfigV0(data)` to get a `configV0`, then invokes `Migrate()` (or `MigrateWithStore(store)` if a credential store is available) to produce a `*Config`.
4. The version number is updated to `CurrentVersion`.
5. The migrated config is automatically saved back to disk (best-effort).

### Version Field

The `version` field in `config.json` indicates the schema version. A value of `0` or a missing field means a legacy config with no version field. A value of `1` means the current version with versioning support.

```json
{
  "version": 1,
  "agents": {...},
  ...
}
```

## Adding a New Migration

When making breaking changes to the config schema:

### Step 1: Update the Current Version Constant

```go
// pkg/config/config.go
const CurrentVersion = 2
```

### Step 2: Introduce a Legacy Struct (if the Old Shape Cannot Be Unmarshaled Into the New One)

Add a new struct in `pkg/config/config_old.go` (or a new `configV<n>` file) that mirrors the previous schema, and give it a `Migrate() (*Config, error)` method. The migration method is responsible for copying every field it can carry forward and for translating renamed/removed fields.

### Step 3: Add a Loader for the New Version

```go
// pkg/config/migration.go
func loadConfigV2(data []byte) (*Config, error) {
    cfg := DefaultConfig()

    if err := json.Unmarshal(data, cfg); err != nil {
        return nil, err
    }
    return cfg, nil
}
```

### Step 4: Update LoadConfig Switch

```go
// pkg/config/config.go — LoadConfig
switch versionInfo.Version {
case 0:
    v, err := loadConfigV0(data)
    if err != nil { return nil, err }
    cfg, err = v.(*configV0).Migrate()
case 1:
    cfg, err = loadConfigV0(data) // example: same struct
    // ...
case CurrentVersion: // 2
    cfg, err = loadConfigV2(data)
default:
    return nil, fmt.Errorf("unsupported config version: %d", versionInfo.Version)
}
```

The old `loadConfig` (v1) stays in place; you add a new `loadConfigV2` for the new shape and switch on `CurrentVersion` in `LoadConfig`.

### Step 5: Test Your Migration

Create a test in `pkg/config/migration_test.go` (or a versioned `_test.go` file) that:
1. Constructs a v1 config JSON (or struct).
2. Runs it through the new migration path.
3. Asserts that every preserved field round-trips, and renamed fields land in the new location.

## Migration Best Practices

#### Field-by-Field Copy in Migrate()

`configV0.Migrate()` copies fields explicitly. New fields on the destination must be added to the migration body, or they will be silently lost.

#### Backward Compatibility

Old configs continue to load via the v0 path. Never delete the `configV0` struct or its `Migrate()` method without confirming no v0 configs exist in the wild.

#### No Data Loss

Every field the old struct carries must be either preserved on the new struct, transformed, or explicitly retired. Silent drops cause operator confusion.

#### Idempotent

Running `Migrate()` on an already-migrated config is not a supported path — the input is expected to be a v0 config. But the loader for the current version (`loadConfig`) is idempotent and safe to call on hot-reload.

#### Auto-Save

After a v0 → v1 migration succeeds, the loader writes the migrated config back to disk (deferred in `LoadConfig`). No manual save step is required.

#### Test Thoroughly

Test with real user config files in addition to synthetic test data. Edge cases found in production configs are often the ones that matter most.

#### Update Defaults

Keep `pkg/config/defaults.go` in sync with the latest schema whenever a migration adds or renames fields, so a fresh `DefaultConfig()` reflects the same shape as a migrated user config.

## Example Migration

### Scenario: Promoting a v0 Config to v1

Old config (no version field, or `version: 0`):
```json
{
  "providers": {
    "openai": { "api_key": "sk-...", "api_base": "https://api.openai.com/v1" }
  },
  "agents": { "defaults": { "max_tokens": 32768 } }
}
```

The loader detects `version == 0`, calls `loadConfigV0` to decode into a `configV0`, then calls `Migrate()`. The v0 `providers` object (per-provider credentials) is converted to a v1 `providers` array (per-model entries) by `v0ConvertProvidersToModelList`. Other top-level fields are copied field-by-field.

New config (v1):
```json
{
  "version": 1,
  "providers": [
    {
      "model_name": "openai",
      "model": "openai/gpt-5.4",
      "api_key": "sk-..."
    }
  ],
  "agents": { "defaults": { "max_tokens": 32768 } }
}
```

The `model_list` key from older v0 configs is silently accepted and renamed to `providers` by `jsonRenameKey` before unmarshalling, so configs that still say `model_list` continue to load.

## Troubleshooting

### Config Not Upgrading

Verify that `CurrentVersion` was bumped in `pkg/config/config.go` and that the `LoadConfig` switch handles the new version. Confirm the new `loadConfigV<n>` function is wired up.

### Migration Errors

Check error messages from `configV0.Migrate()` or `loadConfigV0`. The v0 path is the most common source of failure on upgrade.

### Data Loss After Migration

Cross-check `Migrate()` in `config_old.go` against the destination struct in `config.go` — every old field that should persist needs an explicit copy.
