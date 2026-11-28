// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools C-3, C-13: what the trust setting means for an install of
// an unverified skill through the screen or REST. Allow installs it; Ask
// needs the person's install review of that exact revision; Deny refuses it.

import (
	"fmt"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// Install refusals of an unverified skill (C-3, S-28, S-95).
const (
	installTrustDenyText = skills.InstallTrustDenyText
	installTrustAskText  = "Review needed: this skill is unverified, and unverified skills are set to Ask. Review it in Skills & Tools first."
)

// installRefusal is an install answer that stops before publication.
type installRefusal struct {
	status int
	text   string
}

// stagedInstallRefusal checks a freshly staged skill: a moderation block,
// then the trust setting. reviewed is true for the install review itself.
func (a *restAPI) stagedInstallRefusal(result *skills.InstallResult, slug string, reviewed bool) *installRefusal {
	if result != nil && result.IsMalwareBlocked {
		return &installRefusal{http.StatusForbidden, fmt.Sprintf("skill %q is blocked by registry moderation", slug)}
	}
	return a.unverifiedInstallRefusal(result, reviewed)
}

// unverifiedInstallRefusal applies the trust setting of the loaded config to
// an unverified install. reviewed is true when the person's install review
// covers it (C-13), which answers Ask.
func (a *restAPI) unverifiedInstallRefusal(result *skills.InstallResult, reviewed bool) *installRefusal {
	if result != nil && result.Verified {
		return nil
	}
	switch a.agentLoop.GetConfig().Sandbox.EffectiveSkillTrust() {
	case config.SkillTrustDeny:
		return &installRefusal{http.StatusForbidden, installTrustDenyText}
	case config.SkillTrustAllow:
		return nil
	default:
		if reviewed {
			return nil
		}
		return &installRefusal{http.StatusConflict, installTrustAskText}
	}
}
