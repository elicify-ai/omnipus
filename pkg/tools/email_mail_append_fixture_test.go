package tools

// FIXTURE-ONLY test file (no assertions) — extends the legacy fakeTransport
// with the structural capabilities the outbound-mail tools detect on the
// production *email.Client: messageAppender (Sent copies, drafts) and
// mailboxIdentity (the From for composition). Written by backend-lead during
// GREEN so the qa-lead RED tests can exercise the compose/APPEND paths without
// touching any RED or legacy assertion; flagged for qa-lead/CHECK review.

import "context"

const (
	// fixture values the RED draft test only checks are positive.
	fixtureUID         uint32 = 101
	fixtureUIDValidity uint32 = 777
)

// AppendMessage records every APPEND (draft or Sent copy) for inspection by
// tests that have a reason to look; the RED draft test only needs success
// with a non-zero uid/uidvalidity.
func (f *fakeTransport) AppendMessage(_ context.Context, folder string, flags []string, raw []byte) (uint32, uint32, error) {
	fixtureAppends = append(fixtureAppends, fakeAppend{Folder: folder, Flags: flags, Raw: string(raw)})
	return fixtureUID, fixtureUIDValidity, nil
}

// AccountAddress gives the fake a sending identity, so the outbound tools
// take the same composed (SendRequest.Raw) path they take in production.
func (f *fakeTransport) AccountAddress() string {
	return "agent@example.test"
}

// fixtureAppends records every AppendMessage call (fixture-only
// bookkeeping; tests that have a reason to look may inspect it).
var fixtureAppends []fakeAppend

// fakeAppend records one AppendMessage call (fixture-only bookkeeping).
type fakeAppend struct {
	Folder string
	Flags  []string
	Raw    string
}
