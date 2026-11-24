//go:build goolm && stdjson

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config_test

// GREEN binding for TestSkillPermissionsUpgrade_StopAtEveryStep: the real
// startup upgrader with only the stop and write-fault hooks passed through.

import "github.com/elicify-ai/omnipus/pkg/skillperm"

func init() {
	startSkillUpgradeAtBoot = func(home string, boundary func(point, agent string), beforeAgentWrite func(agent string) error) error {
		return skillperm.Run(home, skillperm.Options{Boundary: boundary, BeforeAgentWrite: beforeAgentWrite})
	}
}
