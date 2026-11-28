// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package sandbox

// T-125(a) (office-preview spec §3.8 BR-2.4; FR-065; founder Q100 A) — the
// startup grants never reach the agents. For the T-118 agent fixture under
// confined Linux, the per-turn kernel policy and the tool policy built
// through KernelPolicyForTurn are deep-equal whether or not the gateway
// started with the six T-123(a)+(b) paths granted, and no rule names any of
// the six paths or a path under the four folders that contain them.
//
// RED on 24f2f912c: DefaultBootPolicyForModel does not exist (spec §13: stub
// first).

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/require"
)

// t125SixPaths is T-123(a)+(b)'s grant set; t125FourFolders are the folders
// that contain them. T-125(a)'s oracle: no rule names ANY OF THE SIX PATHS,
// or A PATH UNDER the four folders. The folders themselves are not
// startup-grant space — /usr/bin is a readOnlySystem read in every agent
// policy (sandbox.go), a pre-existing rule BR-2.4 does not forbid.
var (
	t125SixPaths = []string{
		"/usr/share/omnipus/office",
		"/usr/share/omnipus/pdfium",
		"/usr/bin/omnipus",
		"/usr/local/share/omnipus/office",
		"/usr/local/share/omnipus/pdfium",
		"/usr/local/bin/omnipus",
	}
	t125FourFolders = []string{
		"/usr/share/omnipus",
		"/usr/local/share/omnipus",
		"/usr/bin",
		"/usr/local/bin",
	}
)

func t125NamesGrantedSpace(path string) bool {
	clean := filepath.Clean(path)
	for _, banned := range t125SixPaths {
		if clean == banned || strings.HasPrefix(clean, banned+"/") {
			return true
		}
	}
	for _, folder := range t125FourFolders {
		if strings.HasPrefix(clean, folder+"/") {
			return true
		}
	}
	return false
}

func TestT125_AgentPoliciesNeverCarryStartupGrants(t *testing.T) {
	home := officeVariantCScopeHome(t)
	work := filepath.Join(home, "workspace")
	require.NoError(t, os.Mkdir(work, 0700))

	old, installed := RegisteredTurnPolicyBase()
	defer func() {
		if installed {
			RegisterTurnPolicyBase(&old)
		} else {
			RegisterTurnPolicyBase(nil)
		}
	}()
	RegisterTurnPolicyBase(&TurnPolicyInput{HomePath: home, Model: FilesystemModelConfined})

	// The gateway starts WITHOUT the grants: the reference agent policy.
	reference, err := KernelPolicyForTurn(fspolicy.FSPolicy{WorkDir: work, Scope: fspolicy.FSScopeConfined})
	require.NoError(t, err)
	require.NotNil(t, reference, "instrument: the real tool seam must produce a policy")

	// The gateway starts WITH the six paths granted (the boot builder the
	// gateway uses per BR-2.2): the agent policy must be identical, and the
	// direct DeriveKernelPolicy construction site must agree.
	_ = DefaultBootPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil, t125SixPaths)
	withGrants, err := KernelPolicyForTurn(fspolicy.FSPolicy{WorkDir: work, Scope: fspolicy.FSScopeConfined})
	require.NoError(t, err)
	require.NotNil(t, withGrants)

	require.True(t, reflect.DeepEqual(*reference, *withGrants),
		"T-125(a): the agent's per-turn/tool policy must be identical whether or not the gateway started with the six paths granted")

	direct := DeriveKernelPolicy(fspolicy.FSPolicy{WorkDir: work, Scope: fspolicy.FSScopeConfined},
		TurnPolicyInput{HomePath: home, Model: FilesystemModelConfined})
	require.True(t, reflect.DeepEqual(*reference, direct),
		"T-125(a): KernelPolicyForTurn and DeriveKernelPolicy must agree (the single construction site)")

	// No rule of the agent's policy names the granted paths or anything
	// under the folders containing them.
	for _, rules := range [][]PathRule{reference.FilesystemRules, reference.AllowedBranches} {
		for _, rule := range rules {
			require.False(t, t125NamesGrantedSpace(rule.Path),
				"T-125(a): agent policy rule %s names the startup-granted space; BR-2.4 forbids that", rule.Path)
		}
	}
}
