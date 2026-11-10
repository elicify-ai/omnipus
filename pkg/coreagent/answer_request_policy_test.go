// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// Hard Constraint #6 + Definition of Done: answer_request has a ceiling entry
// and resolves allow, through the real compositor, for every seeded role that
// can receive a request (the roles that hold send_message).
func TestAnswerRequest_ResolvesAllowForTheConversationalRoles(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies["answer_request"], "the shipped ceiling entry")
	for _, id := range []coreagent.CoreAgentID{coreagent.IDMia, coreagent.IDJim, coreagent.IDOmnipus} {
		assert.Equal(t, "allow", resolveFor(t, cfg, string(id), "answer_request", nil),
			"%s can receive a request and must be able to answer it", id)
		assert.Equal(t, "allow", resolveFor(t, cfg, string(id), "send_message", nil),
			"instrument check: %s holds send_message, the tool this one mirrors", id)
	}
}
