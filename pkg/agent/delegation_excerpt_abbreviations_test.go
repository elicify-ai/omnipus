package agent

import "testing"

// R1 / CR-01: keep the complete source routing sentence, including bounded
// common abbreviations, without leaking later description sentences. Expected
// sentences were derived from the requirement before reading the implementation.
func TestDelegationUseForAbbreviations(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        string
	}{
		{
			name:        "use_for_keeps_us_mid_sentence",
			description: "Intro. Use for: U.S. tax filings and compliance. Additional details.",
			want:        "U.S. tax filings and compliance.",
		},
		{
			name:        "fallback_keeps_eg_mid_sentence",
			description: "Handle e.g. invoice reviews. Additional details.",
			want:        "Handle e.g. invoice reviews.",
		},
		{
			name:        "use_when_keeps_ie_mid_sentence",
			description: "Intro. Use when: reviewing invoices, i.e. checking totals and dates. Private escalation notes.",
			want:        "reviewing invoices, i.e. checking totals and dates.",
		},
		{
			name:        "fallback_keeps_etc_mid_sentence",
			description: "Handle invoices, receipts, etc. during reconciliation. Private escalation notes.",
			want:        "Handle invoices, receipts, etc. during reconciliation.",
		},
		{
			name:        "use_for_keeps_inc_mid_sentence",
			description: "Intro. Use for: Acme Inc. account reconciliation. Private escalation notes.",
			want:        "Acme Inc. account reconciliation.",
		},
		{
			name:        "use_when_keeps_dr_title",
			description: "Intro. Use when: Dr. Rivera needs referral scheduling. Private escalation notes.",
			want:        "Dr. Rivera needs referral scheduling.",
		},
		{
			name:        "fallback_keeps_mr_title",
			description: "Handle Mr. Lee's invoice approvals. Private escalation notes.",
			want:        "Handle Mr. Lee's invoice approvals.",
		},
		{
			name:        "use_when_keeps_us_mid_sentence",
			description: "Intro. Use when: U.S. payroll rules need review. Private escalation notes.",
			want:        "U.S. payroll rules need review.",
		},
		{
			name:        "fallback_stops_after_sentence_final_us",
			description: "Route filings for the U.S. Next sentence.",
			want:        "Route filings for the U.S.",
		},
		{
			name:        "use_for_stops_after_sentence_final_us",
			description: "Intro. Use for: tax filings for the U.S. Next sentence.",
			want:        "tax filings for the U.S.",
		},
		{
			name:        "use_when_stops_at_genuine_period",
			description: "Intro. Use when: invoice totals disagree. Private escalation notes.",
			want:        "invoice totals disagree.",
		},
		{
			name:        "fallback_stops_before_private_abbreviations",
			description: "Review invoice totals. Private notes mention Dr. Rivera and U.S. accounts.",
			want:        "Review invoice totals.",
		},
		{
			name:        "use_for_stops_at_exclamation",
			description: "Intro. Use for: reconcile urgent invoices! Private escalation notes.",
			want:        "reconcile urgent invoices!",
		},
		{
			name:        "use_when_stops_at_question",
			description: "Intro. Use when: do these totals match? Private escalation notes.",
			want:        "do these totals match?",
		},
		{
			name:        "use_for_keeps_closing_quote_without_private_tail",
			description: `Intro. Use for: "Review this invoice." Private escalation notes.`,
			want:        `"Review this invoice."`,
		},
		{
			name:        "fallback_keeps_closing_curly_quote_without_private_tail",
			description: "“Review this invoice!” Private escalation notes.",
			want:        "“Review this invoice!”",
		},
		{
			name:        "use_when_keeps_dotted_version_mid_sentence",
			description: "Intro. Use when: upgrading v1.2.3 invoice exports. Private escalation notes.",
			want:        "upgrading v1.2.3 invoice exports.",
		},
		{
			name:        "fallback_stops_after_dotted_version",
			description: "Review invoice exporter v1.2.3. Private escalation notes.",
			want:        "Review invoice exporter v1.2.3.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := delegationUseFor(tt.description); got != tt.want {
				t.Fatalf("delegationUseFor(%q) = %q; want complete routing sentence %q with no trailing description", tt.description, got, tt.want)
			}
		})
	}
}
