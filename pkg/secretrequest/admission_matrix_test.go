package secretrequest_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// BDD-6.4: assert the whole concurrent effect, not merely two return codes.
func TestSecretMatrix_ConcurrentSubmitsCommitOneValueAndReceipt(t *testing.T) {
	f := newMatrixEnv(t)
	rec := f.pending()
	before := len(f.reg.Snapshot(matrixOwner, rec.Card.SessionId))
	type outcome struct {
		rec   secretrequest.Record
		err   error
		value string
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, value := range []string{"FAKE-CONCURRENT-FIRST", "FAKE-CONCURRENT-SECOND"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			<-start
			out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": value}))
			results <- outcome{out, err, value}
		}(value)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	var winner outcome
	for result := range results {
		if result.err == nil {
			successes++
			winner = result
		} else {
			conflicts++
			requireMatrixFailure(t, result.err, http.StatusConflict, "submitted")
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	require.Len(t, winner.rec.Card.ReferenceNames, 1)
	names, err := f.store.List()
	require.NoError(t, err)
	require.Equal(t, winner.rec.Card.ReferenceNames, names, "loser must not create a second reference/version")
	value, err := f.store.Get(names[0])
	require.NoError(t, err)
	require.Equal(t, winner.value, value)
	receipt, exists, err := f.store.CardReceipt(rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, names, receipt.References)
	require.Len(t, f.attempts, 1)
	require.Equal(t, names, f.attempts[0].Card.ReferenceNames)
	require.Len(t, f.reg.Snapshot(matrixOwner, rec.Card.SessionId), before)
}

// §6.2 / BDD-6.17: bounds are install-wide, not one account/agent/session.
func TestSecretMatrix_PendingLimitsAreOnePerSessionAnd64InstallWide(t *testing.T) {
	f := newMatrixEnv(t)
	cards := make([]secretrequest.Record, 0, 64)
	for i := range 64 {
		agentID := "omnipus"
		if i%2 == 1 {
			agentID = "mia"
		}
		cards = append(cards, f.create(f.newSession(agentID, ""), agentID, providerInput()))
		if i == 62 {
			require.Len(t, f.reg.Snapshot(matrixOwner, ""), 63, "max-1")
		}
	}
	require.Len(t, f.reg.Snapshot(matrixOwner, ""), 64, "max")
	_, err := f.reg.Create(context.Background(), cards[0].Card.SessionId, "omnipus", "web-card", "", providerInput())
	require.EqualError(t, err, "a secret request is already pending in this session")
	overflow := f.newSession("jim", "")
	_, err = f.reg.Create(context.Background(), overflow, "jim", "web-card", "", providerInput())
	require.EqualError(t, err, "too many pending secret requests", "max+1 must create no card")
	require.Empty(t, f.reg.Snapshot(matrixOwner, overflow))
	_, err = f.reg.Submit(cards[0].Card.RequestId, matrixOwner, cancelInput())
	require.NoError(t, err)
	replacement := f.create(overflow, "jim", providerInput())
	require.Equal(t, "pending", replacement.Card.State, "terminal card frees pending capacity")
	require.Len(t, f.reg.Snapshot(matrixOwner, ""), 65, "64 pending plus one terminal, never a silently dropped request")
}

// §6.2 and FR-087: bounded terminal metadata and matching receipts are pruned
// together; stored values never expire when their request metadata is pruned.
func TestSecretMatrix_TerminalRetentionPrunesReceiptsNotValues(t *testing.T) {
	f := newMatrixEnv(t)
	sid := f.newSession("omnipus", "")
	submitted := make([]secretrequest.Record, 0, 33)
	for i := range 33 {
		rec := f.create(sid, "omnipus", providerInput())
		value := fmt.Sprintf("FAKE-RETAINED-VERSION-%02d", i+1)
		out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": value}))
		require.NoError(t, err)
		require.Equal(t, "submitted", out.Card.State)
		submitted = append(submitted, out)
		if i == 30 {
			require.Len(t, f.reg.Snapshot(matrixOwner, sid), 31, "retention max-1")
		}
		if i == 31 {
			require.Len(t, f.reg.Snapshot(matrixOwner, sid), 32, "retention max")
		}
	}
	snapshot := f.reg.Snapshot(matrixOwner, sid)
	require.Len(t, snapshot, 32, "retain only newest 32 terminal states")
	for i, card := range snapshot {
		require.Equal(t, submitted[i+1].Card, card)
	}
	requireMatrixNotFound(t, f.reg, submitted[0].Card.RequestId)
	for i, rec := range submitted {
		_, exists, err := f.store.CardReceipt(rec.Card.RequestId)
		require.NoError(t, err)
		require.Equal(t, i != 0, exists, "prune receipt iff corresponding terminal state was pruned")
		value, err := f.store.Get(rec.Card.ReferenceNames[0])
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("FAKE-RETAINED-VERSION-%02d", i+1), value)
	}
}

// BDD-6.16 trust checks happen before storage even for an otherwise valid value.
func TestSecretMatrix_StoppedOrRemovedAgentRefusesBeforeAnyWrite(t *testing.T) {
	for _, event := range []string{"session stopped", "acting agent deleted"} {
		t.Run(event, func(t *testing.T) {
			f := newMatrixEnv(t)
			rec := f.pending()
			want := "refused"
			if event == "session stopped" {
				f.stopped[rec.Card.SessionId] = true
				want = "stopped"
			} else {
				f.cfg.Agents.List = nil
			}
			_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
			requireMatrixFailure(t, err, http.StatusConflict, want)
			requireNoMatrixCommit(t, f, rec.Card.RequestId)
		})
	}
}

// BDD-6.9 / chief ruling 2026-10-16: server expands Slack's mandatory pair.
func TestSecretMatrix_SlackMissingFieldIsAllOrNone(t *testing.T) {
	f := newMatrixEnv(t)
	f.cfg.Channels = map[string]config.ChannelInstanceConfig{"slack-1": {Type: "slack"}}
	rec := f.create(f.newSession("omnipus", ""), "omnipus", secretrequest.Input{Purpose: "Connect Slack", Instance: "slack-1", Field: "bot_token"})
	require.Equal(t, 2, len(rec.Card.Fields))
	require.Equal(t, "bot_token", rec.Card.Fields[0].Key)
	require.Equal(t, "app_token", rec.Card.Fields[1].Key)
	_, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"bot_token": "FAKE-SLACK-BOT"}))
	requireMatrixFailure(t, err, http.StatusBadRequest, "")
	requireNoMatrixCommit(t, f, rec.Card.RequestId)
	out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"bot_token": "FAKE-SLACK-BOT", "app_token": "FAKE-SLACK-APP"}))
	require.NoError(t, err)
	require.Equal(t, "submitted", out.Card.State)
	require.Len(t, out.Card.ReferenceNames, 2)
	for i, value := range []string{"FAKE-SLACK-BOT", "FAKE-SLACK-APP"} {
		stored, readErr := f.store.Get(out.Card.ReferenceNames[i])
		require.NoError(t, readErr)
		require.Equal(t, value, stored)
	}
	receipt, exists, err := f.store.CardReceipt(rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, out.Card.ReferenceNames, receipt.References)
	requireValueFree(t, "Slack state", secretrequest.State(out), "FAKE-SLACK-BOT", "FAKE-SLACK-APP")
}
