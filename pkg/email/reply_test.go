package email

// RED pack (w4 spec §9.1 row 9): the shared reply-recipient rule.
//
// Every expected value here is derived from the spec (US-5, §8 "Reply all,
// plain Reply and quote", DS-REPLY) — never from the implementation. The
// canonical fixture is the spec's own independent test: From A, Reply-To R,
// To = self+X+duplicate-R, Cc = Y+mixed-case-X+self+display-name-duplicate-R,
// hidden Bcc. Reply all → To=[R]; Cc=[X, Y] exactly once each; no self, no
// primary, no Bcc. Plain reply → [R] only.

import (
	"net/mail"
	"reflect"
	"strings"
	"testing"
)

// replyFixture builds the spec's US-5 independent-test message.
func replyFixture() ReplyInput {
	return ReplyInput{
		From:    `A <a@sender.example>`,
		ReplyTo: `R <r@sender.example>`,
		To: []string{
			`Self <self@me.example>`,           // the mailbox's own address
			`Xavier X <x@other.example>`,       // X
			`Duplicate R <r@SENDER.example>`,   // case-variant duplicate of R
		},
		Cc: []string{
			`Yara Y <y@other.example>`,         // Y
			`X@Other.example`,                  // mixed-case duplicate of X
			`self@ME.example`,                  // case-variant duplicate of self
			`"Display R" <r@sender.example>`,   // display-name duplicate of R
		},
		Mode:       ReplyModeReplyAll,
		OwnAddress: `self@me.example`,
	}
}

func bareAddresses(t *testing.T, as []mail.Address) []string {
	t.Helper()
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, strings.ToLower(a.Address))
	}
	return out
}

func TestBuildReplyRecipients_ReplyAllCleansRecipientSet(t *testing.T) {
	got, err := BuildReplyRecipients(replyFixture())
	if err != nil {
		t.Fatalf("BuildReplyRecipients returned error: %v", err)
	}
	wantTo := []string{"r@sender.example"}
	wantCc := []string{"x@other.example", "y@other.example"}
	if !reflect.DeepEqual(bareAddresses(t, got.To), wantTo) {
		t.Fatalf("To = %v, want exactly %v (spec US-5.AC-1: To = Reply-To)", got.To, wantTo)
	}
	if !reflect.DeepEqual(bareAddresses(t, got.Cc), wantCc) {
		t.Fatalf("Cc = %v, want exactly %v (spec US-5.AC-1: original To+Cc minus self/primary, de-duplicated case-insensitively)", got.Cc, wantCc)
	}
	for _, a := range got.Cc {
		if strings.EqualFold(a.Address, "self@me.example") {
			t.Fatalf("Cc carries the mailbox's own address: %v (spec US-5.AC-1: no self)", got.Cc)
		}
		if strings.EqualFold(a.Address, "r@sender.example") {
			t.Fatalf("Cc carries the primary: %v (spec US-5.AC-1: primary excluded)", got.Cc)
		}
	}
}

func TestBuildReplyRecipients_PlainReplyKeepsOnlyTheSender(t *testing.T) {
	in := replyFixture()
	in.Mode = ReplyModeReply
	got, err := BuildReplyRecipients(in)
	if err != nil {
		t.Fatalf("BuildReplyRecipients returned error: %v", err)
	}
	if !reflect.DeepEqual(bareAddresses(t, got.To), []string{"r@sender.example"}) {
		t.Fatalf("To = %v, want exactly [r@sender.example] (spec US-5.AC-2: plain Reply = primary only)", got.To)
	}
	if len(got.Cc) != 0 {
		t.Fatalf("Cc = %v, want empty (spec US-5.AC-2: the other recipients are not silently kept)", got.Cc)
	}
}

func TestBuildReplyRecipients_NoReplyToFallsBackToFrom(t *testing.T) {
	for _, mode := range []string{ReplyModeReply, ReplyModeReplyAll} {
		in := replyFixture()
		in.ReplyTo = ""
		in.Mode = mode
		got, err := BuildReplyRecipients(in)
		if err != nil {
			t.Fatalf("mode %q: error: %v", mode, err)
		}
		if !reflect.DeepEqual(bareAddresses(t, got.To), []string{"a@sender.example"}) {
			t.Fatalf("mode %q: To = %v, want [a@sender.example] (spec US-5.AC-3: primary falls back to From)", mode, got.To)
		}
	}
}

func TestBuildReplyRecipients_SelfOnlyRecipientsYieldEmptySets(t *testing.T) {
	in := ReplyInput{
		From:       `self@me.example`,
		To:         []string{`self@me.example`},
		Cc:         []string{`Self <SELF@me.example>`},
		Mode:       ReplyModeReplyAll,
		OwnAddress: `self@me.example`,
	}
	got, err := BuildReplyRecipients(in)
	if err != nil {
		t.Fatalf("BuildReplyRecipients returned error: %v", err)
	}
	if len(got.To) != 0 {
		t.Fatalf("To = %v, want empty (spec US-5.AC-3: no send-to-self, no guessed replacement)", got.To)
	}
	if len(got.Cc) != 0 {
		t.Fatalf("Cc = %v, want empty (spec US-5.AC-3: editable empty recipients)", got.Cc)
	}
}

func TestBuildReplyRecipients_InvalidAddressIsAnActionableError(t *testing.T) {
	in := replyFixture()
	in.Cc = append(in.Cc, "definitely not an address")
	_, err := BuildReplyRecipients(in)
	if err == nil {
		t.Fatalf("want an error for an invalid recipient (spec US-5.AC-3: actionable error, never silent omission)")
	}
	if !strings.Contains(err.Error(), "definitely not an address") {
		t.Fatalf("error %q does not name the offending entry (spec: actionable, never silent)", err)
	}
}

func TestBuildReplyRecipients_UnknownModeIsAnError(t *testing.T) {
	in := replyFixture()
	in.Mode = "reply_to_everyone"
	if _, err := BuildReplyRecipients(in); err == nil {
		t.Fatalf("want an error for an unknown mode, got none")
	}
}

// TestBuildReplyRecipients_ExtraCcStillApplies pins the reply tool's
// behaviour-preservation requirement (w4 spec §9.4: "existing reply tool
// behaviour unchanged after the BuildReplyRecipients extraction"). The
// first-seen precedence detail is the historical agent-tool behaviour the
// spec adopts by reference — characterization for the ordering, spec-derived
// for the merge itself.
func TestBuildReplyRecipients_ExtraCcStillApplies(t *testing.T) {
	in := replyFixture()
	in.Mode = ReplyModeReply
	in.ExtraCc = []string{`Zoe Z <z@extra.example>`}
	got, err := BuildReplyRecipients(in)
	if err != nil {
		t.Fatalf("BuildReplyRecipients returned error: %v", err)
	}
	if !reflect.DeepEqual(bareAddresses(t, got.Cc), []string{"z@extra.example"}) {
		t.Fatalf("plain reply with an explicit cc: Cc = %v, want [z@extra.example] (the tool's cc argument still applies in reply mode)", got.Cc)
	}

	in2 := replyFixture()
	in2.ExtraCc = []string{`z@extra.example`, `x@other.example`} // x dedupes against the originals
	got2, err := BuildReplyRecipients(in2)
	if err != nil {
		t.Fatalf("BuildReplyRecipients returned error: %v", err)
	}
	want := []string{"z@extra.example", "x@other.example", "y@other.example"}
	if !reflect.DeepEqual(bareAddresses(t, got2.Cc), want) {
		t.Fatalf("reply_all with ExtraCc: Cc = %v, want %v (merge + dedup, first-seen order)", got2.Cc, want)
	}
}
