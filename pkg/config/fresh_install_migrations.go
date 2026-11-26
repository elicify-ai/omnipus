// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// Skills & Tools C-19: the one-time skill-permissions upgrade translates the
// values of an existing install. A fresh install has none to translate: its
// config is in the new shape from the start, so the config it first writes
// records the upgrade as done. Without the marker the C-2 heal would wait for
// an upgrade that never runs on this home (a project skill's key would not
// be created at turn start), and the next start would run the whole upgrade
// over a config already in the new shape.

// SkillPermissionsMigration is the sandbox.migrations key of the C-19
// skill-permissions upgrade (pkg/skillperm.MigrationName).
const SkillPermissionsMigration = "skill_permissions"

// migrationDone is the sandbox.migrations value of a finished upgrade.
const migrationDone = "done"

// markFreshInstallMigrationsDone records the one-time upgrades a fresh
// install does not need as done.
func markFreshInstallMigrationsDone(c *Config) {
	if c.Sandbox.Migrations == nil {
		c.Sandbox.Migrations = map[string]string{}
	}
	c.Sandbox.Migrations[SkillPermissionsMigration] = migrationDone
}
