//go:build goolm && stdjson

package agent

// Order 34 — TestSkillConsent_Table (S-10, S-11, S-30, S-31, S-93; Dataset
// D10 rows 1–8, 21, 22; the C-12 decision table is the spec's own oracle for
// D10). Order 47 extends this table to every D10 row (S-105–S-109 and the
// install/stale/replay rows) — the table and its assertions are written so
// that extension is additive.
//
// Oracle rows (skills-tools-redesign-spec.md, C-12 decision table and
// Dataset D10):
//   C-12 #1 / D10 #1  — P closed, T closed: loads, no card
//   C-12 #2 / D10 #2  — P open, chat, no settler: permission card with
//                       Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny
//   C-12 #2 unattended — P open, unattended: skipped with the unattended
//                       text ("Not run: needs approval, and task and plan
//                       runs never ask." — the same text order 12 pins)
//   D10 #2            — Auto-approve active settles the permission part:
//                       loads, no card
//   D10 #3 / C-26     — a running timed allowance for this agent settles it:
//                       loads, no card
//   D10 #4 / C-26     — allowance ended, or running for another agent only:
//                       the permission card again
//   C-12 #3 / D10 #5  — T open only: trust card "Approve this version"/Deny,
//                       NO time choices
//   C-12 #4 / D10 #13 — P and T open: one combined card ("Approve this
//                       version", "Approve this version and allow for"
//                       1h/3h/1d, Deny)
//   D10 #14           — Auto settles the permission part of the combined:
//                       trust card only
//   C-12 #5 / D10 #8  — the person's /name with T open: trust card
//   D10 #9            — /name with P settled: loads, no card
//   D10 #11           — Auto in a run: loads (Auto is checked BEFORE the
//                       unattended refusal)
//   D10 #21           — the agent's OWN Ask: the same permission card with
//                       time choices
//   D10 #22 / S-30    — God Mode on, agent's own Ask: the permission card
//                       (Auto is never active in God Mode)
//   C-12 refusal      — a Deny anywhere refuses before either check: no
//                       card, the refusal names the setting
//
// RED: pkg/agent/loop_run_turn_tools.go::resolveAskPolicy consults
// auto-approve and (order 12) the unattended refusal, but the consent
// decision that returns WHICH card (permission / trust / combined) with
// C-26's time choices does not exist. The seam below is that missing
// decision; GREEN wires it into resolveAskPolicy's skill leg.
import (
	"testing"
)

// skillConsentInput states ALL inputs of one consent decision (D10: "Every
// row states all inputs; defaults are never implied").
type skillConsentInput struct {
	Entry             string // "load" or "slash" (C-12 rows 1–5; install rows are order 47)
	PermissionOpen    bool   // P — the effective permission is still Ask after its settlers
	TrustOpen         bool   // T — unverified, trust Ask, revision not approved
	Denied            bool   // a Deny anywhere (checked before both)
	AutoActive        bool   // autoApproveActive for this run (never true in God Mode)
	AllowanceForAgent bool   // C-26: a timed allowance running for THIS agent
	GodMode           bool
	Unattended        bool // task, plan or scheduled run
}

// skillConsentDecision is the consent outcome for one load.
type skillConsentDecision struct {
	Outcome     string   // "loads", "permission-card", "trust-card", "combined-card", "unattended-skipped", "refused"
	CardChoices []string // the offered actions of the card, in order
}

func TestSkillConsent_Table(t *testing.T) {
	if skillConsentDecide == nil {
		t.Fatal("BLOCKED: the skill consent decision does not exist — pkg/agent/loop_run_turn_tools.go::resolveAskPolicy must decide which card (permission / trust / combined, with C-26 time choices) a skill load opens; required by S-10, S-11, S-30, S-31, S-93 and Dataset D10")
	}

	permissionCard := []string{"approve_once", "allow_for_1h", "allow_for_3h", "allow_for_1d", "deny"}
	combinedCard := []string{"approve_version", "approve_version_allow_for_1h", "approve_version_allow_for_3h", "approve_version_allow_for_1d", "deny"}
	trustCard := []string{"approve_version", "deny"}

	for _, tc := range []struct {
		name  string
		in    skillConsentInput
		must  string
		choices []string
	}{
		{"C-12#1: settled on both checks loads with no card", skillConsentInput{Entry: "load"}, "loads", nil},
		{"C-12#2: permission open in chat opens the permission card with C-26 time choices", skillConsentInput{Entry: "load", PermissionOpen: true}, "permission-card", permissionCard},
		{"C-12#2 unattended: a needed approval skips the run with the unattended text", skillConsentInput{Entry: "load", PermissionOpen: true, Unattended: true}, "unattended-skipped", nil},
		{"D10#2: Auto-approve active settles the permission part", skillConsentInput{Entry: "load", PermissionOpen: true, AutoActive: true}, "loads", nil},
		{"D10#3 C-26: a running allowance for this agent settles the permission part", skillConsentInput{Entry: "load", PermissionOpen: true, AllowanceForAgent: true}, "loads", nil},
		{"D10#4 C-26: an ended allowance (or another agent's) reopens the permission card", skillConsentInput{Entry: "load", PermissionOpen: true}, "permission-card", permissionCard},
		{"C-12#3: trust open only opens the trust card with no time choice", skillConsentInput{Entry: "load", TrustOpen: true}, "trust-card", trustCard},
		{"C-12#4: both open opens one combined card", skillConsentInput{Entry: "load", PermissionOpen: true, TrustOpen: true}, "combined-card", combinedCard},
		{"D10#14: Auto settles the permission part of the combined card to a trust card", skillConsentInput{Entry: "load", PermissionOpen: true, TrustOpen: true, AutoActive: true}, "trust-card", trustCard},
		{"C-12#5: the person's /name with trust open opens the trust card", skillConsentInput{Entry: "slash", TrustOpen: true}, "trust-card", trustCard},
		{"D10#9: /name with the permission settled loads with no card", skillConsentInput{Entry: "slash"}, "loads", nil},
		{"D10#11: Auto in a run loads — Auto is checked before the unattended refusal", skillConsentInput{Entry: "load", PermissionOpen: true, AutoActive: true, Unattended: true}, "loads", nil},
		{"D10#21: the agent's own Ask opens the same permission card with time choices", skillConsentInput{Entry: "load", PermissionOpen: true}, "permission-card", permissionCard},
		{"D10#22 S-30: God Mode on, agent's own Ask — the permission card, Auto never active", skillConsentInput{Entry: "load", PermissionOpen: true, GodMode: true}, "permission-card", permissionCard},
		{"C-12 refusal: a Deny anywhere refuses before either check", skillConsentInput{Entry: "load", Denied: true}, "refused", nil},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := skillConsentDecide(tc.in)
			if got.Outcome != tc.must {
				t.Fatalf("D10 decision: outcome = %q, want %q (inputs: %+v)", got.Outcome, tc.must, tc.in)
			}
			if len(got.CardChoices) != len(tc.choices) {
				t.Fatalf("D10 decision: choices = %v, want %v", got.CardChoices, tc.choices)
			}
			for i, want := range tc.choices {
				if got.CardChoices[i] != want {
					t.Fatalf("D10 decision: choice[%d] = %q, want %q", i, got.CardChoices[i], want)
				}
			}
		})
	}
}

// skillConsentDecide is the test-local seam GREEN wires to the production
// consent decision (resolveAskPolicy's skill leg). Declared AFTER the table
// so the RED failure above names the missing behaviour first.
var skillConsentDecide func(in skillConsentInput) skillConsentDecision
