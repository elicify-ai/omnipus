package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Owner stamping at the three agent entry points that create a main no person
// is typing in (peer request, bound connector input, room request).
//
// Oracle (founder decision 2026-10-28, ADR-019 attribution): a main created
// here is owned by the install's operator, i.e. the first configured account
// whose username is a person (never the machine principal "cli", never ""). With
// no configured account the main stays ownerless (""). Expected values are
// written from that requirement; the owner is read back from the persisted
// meta.json by an independent store, not from the creating store's cache.

func mainOwnerAdmissionAccounts(withAccounts bool) []config.UserConfig {
	if !withAccounts {
		return nil
	}
	// A legacy "cli" row comes FIRST on purpose: the owner must skip it.
	return []config.UserConfig{{Username: config.MachinePrincipalUsername}, {Username: "op"}}
}

// persistedMainOwner reads the owner from the identity file on disk through a
// second store that shares no cache with the one that created the main.
func persistedMainOwner(t *testing.T, store *session.UnifiedStore, mainID string) string {
	t.Helper()
	fresh, err := session.NewUnifiedStore(store.BaseDir())
	require.NoError(t, err, "open an independent disk-reading store")
	t.Cleanup(func() { require.NoError(t, fresh.Close()) })
	meta, err := fresh.GetMeta(mainID)
	require.NoError(t, err, "the admitted request must have created the receiver's main")
	require.Equal(t, session.SessionTypeMain, meta.Type)
	return meta.Owner
}

func TestMainOwnerAdmission_EntryPointsStampOperatorNeverCLI(t *testing.T) {
	entryPoints := []struct {
		name  string
		admit func(t *testing.T, f *addrFixture)
	}{
		{"address_router.admitRequest", func(t *testing.T, f *addrFixture) {
			_, err := f.r.SendPeer(context.Background(), f.peerReq())
			require.NoError(t, err)
		}},
		{"connector_intake.admitBoundConnectorInputCtx", func(t *testing.T, f *addrFixture) {
			msg := connMsg("chat-owner", "hello")
			refusal, err := f.al.admitBoundConnectorInputCtx(context.Background(), &msg)
			require.NoError(t, err)
			require.Empty(t, refusal)
		}},
		{"room_request.AdmitRoomRequest", func(t *testing.T, f *addrFixture) {
			_, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
			require.NoError(t, err)
		}},
	}
	cases := []struct {
		name         string
		withAccounts bool
		wantOwner    string
	}{
		{"accounts cli then op: owner is op", true, "op"},
		{"no account configured: owner stays empty", false, ""},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				f := boundConnFixture(t)
				f.al.GetConfig().Gateway.Users = mainOwnerAdmissionAccounts(c.withAccounts)
				mainID, err := session.MainSessionID(addrWS, addrReceiver)
				require.NoError(t, err)
				store := f.al.GetSessionStore()
				_, err = store.GetMeta(mainID)
				require.Error(t, err, "precondition: the main must not exist before the admission")

				ep.admit(t, f)

				require.Equal(t, c.wantOwner, persistedMainOwner(t, store, mainID))
			})
		}
	}
}
