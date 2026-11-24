//go:build goolm && stdjson

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

// GREEN binding for TestApprovalGrantStore_AgentScopedTimed: the real clock
// input of the store.

import "github.com/elicify-ai/omnipus/pkg/security"

func init() {
	injectTimedStoreClock = security.SetApprovalGrantStoreClock
}
