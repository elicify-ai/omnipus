# Config Schema Versioning Guide

## Overview

Omnipus uses a schema versioning system for `config.json` to ensure smooth upgrades as the configuration format evolves.

## Version History

### Version 1

**Introduction:** Initial version with version field support.

**Changes:** Added `version` field to the Config struct.

**Migration:** No structural changes are needed for existing configs.

## How It Works

### Automatic Migration

When you load a config file:
1. The system first reads the `version` field from the JSON
2. Based on the detected version, it loads the appropriate config struct (`ConfigV0`, `ConfigV1`, etc.)
3. If the loaded version is less than the latest, migrations are applied incrementally
4. The version number is updated automatically
5. The migrated config is automatically saved back to disk

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

### Step 1: Define the New Version Struct

Create a new struct for the new version if the structure changes significantly:

```go
// ConfigV2 represents version 2 config structure
type ConfigV2 struct {
    Version   int             `json:"version"`
    Agents    AgentsConfig    `json:"agents"`
    // ... other fields with new structure
}
```

### Step 2: Update Current Config Version

```go
const CurrentConfigVersion = 2  // Increment this
```

### Step 3: Add a Loader Function

```go
// loadConfigV2 loads a version 2 config
func loadConfigV2(data []byte) (*Config, error) {
    cfg := DefaultConfig()

    // Parse to ConfigV2 struct
    var v2 ConfigV2
    if err := json.Unmarshal(data, &v2); err != nil {
        return nil, err
    }

    // Convert to current Config
    cfg.Version = v2.Version
    cfg.Agents = v2.Agents
    // ... map other fields

    return cfg, nil
}
```

### Step 4: Add Migration Logic

```go
// applyMigration applies a single migration step from fromVersion to toVersion
func applyMigration(cfg *Config, fromVersion, toVersion int) (*Config, error) {
    switch toVersion {
    case 1:
        // Migration from version 0 to 1
        return &Config{
            Version: 1,
            Agents:  cfg.Agents,
            // ... copy all fields
        }, nil
    case 2:
        // Migration from version 1 to 2
        // Example: Move or rename fields
        migrated := *cfg
        migrated.Version = 2
        // Apply structural changes
        if cfg.SomeOldField != "" {
            migrated.SomeNewField = cfg.SomeOldField
        }
        return &migrated, nil
    default:
        return nil, fmt.Errorf("unsupported migration target version: %d", toVersion)
    }
}
```

### Step 5: Update LoadConfig Switch

```go
func LoadConfig(path string) (*Config, error) {
    // ... read file ...

    switch versionInfo.Version {
    case 0:
        cfg, err = loadConfigV0(data)
    case 1:
        cfg, err = loadConfigV1(data)
    case 2:
        cfg, err = loadConfigV2(data)
    default:
        return nil, fmt.Errorf("unsupported config version: %d", versionInfo.Version)
    }

    // ... migrate and validate ...
}
```

### Step 6: Test Your Migration

Create a test in `config_migration_test.go`:

```go
func TestMigrateV1ToV2(t *testing.T) {
    // Create a version 1 config
    v1Config := Config{
        Version: 1,
        // ... set up test data
    }

    // Apply migration
    migrated, err := applyMigration(&v1Config, 1, 2)
    if err != nil {
        t.Fatalf("Migration failed: %v", err)
    }

    // Verify version is updated
    if migrated.Version != 2 {
        t.Errorf("Expected version 2, got %d", migrated.Version)
    }

    // Verify data is preserved/transformed correctly
    // ...
}
```

## Migration Best Practices

#### Version-Specific Structs

Define a separate struct for each version that has structural changes. This keeps the parsing logic for each version isolated and unambiguous.

#### Backward Compatibility

Ensure old configs can still be loaded with their specific structs. Never remove a version-specific loader until you are certain no configs at that version exist in the wild.

#### No Data Loss

Migrations must preserve all user settings. Every field present in the source version must be explicitly copied or transformed into the destination version struct.

#### Idempotent

Running the same migration multiple times must be safe and produce the same result. Avoid side effects that accumulate across repeated runs.

#### Auto-Save

Migrated configs are automatically saved to update the user's file. No manual save step is required after a successful migration.

#### Test Thoroughly

Test with real user config files in addition to synthetic test data. Edge cases found in production configs are often the ones that matter most.

#### Update Defaults

Keep `defaults.go` in sync with the latest schema whenever a migration adds or renames fields.

## Example Migration

### Scenario: Adding a new field with default value

Old config (version 1):
```json
{
  "version": 1,
  "agents": {
    "defaults": {
      "max_tokens": 32768
    }
  }
}
```

Migration to version 2:
```go
case 2:
    migrated := *cfg
    migrated.Version = 2

    // Add new field with default value if not set
    if migrated.Agents.Defaults.NewFeatureEnabled == false {
        // Use default value
    }

    return &migrated, nil
```

New config (version 2):
```json
{
  "version": 2,
  "agents": {
    "defaults": {
      "max_tokens": 32768,
      "new_feature_enabled": false
    }
  }
}
```

## Troubleshooting

### Config Not Upgrading

Verify that `CurrentConfigVersion` is incremented. Check that migration logic in `applyMigration()` handles the target version. Ensure `migrateConfig()` is called in `LoadConfig()`.

### Migration Errors

Check error messages for specific migration failures. Review migration logic for edge cases. Ensure all required fields are properly initialized and verify the loader function for the source version.

### Data Loss After Migration

Ensure all fields are copied during migration. Check that the migration does not overwrite values with defaults unnecessarily. Review the conversion logic in the loader functions.
