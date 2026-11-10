// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// BDD (delta review R1 #11): inputProducer labels who wrote an input the model
// sees. It must not claim a PERSON wrote it when the lifecycle record that would
// say "a parent sent this" could not be read: a read failure is not "no parent".
func TestInputProducer_UnreadableLifecycleIsNotLabelledUser(t *testing.T) {
	al, _ := newSteerAL(t)
	lifecycle := al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle)
	const sid = "sess-r1-11"
	ts := &turnState{transcriptSessionID: sid, sessionKey: sid, channel: "webchat"}

	// Control: no record at all is a plain chat, written by a person.
	require.Equal(t, windowProducerUser, al.inputProducer(ts), "no lifecycle record means a plain chat")

	// A record whose file exists but cannot be read (a directory stands where the
	// append-only log belongs): the read fails for a reason other than "not found".
	require.NoError(t, os.MkdirAll(filepath.Join(lifecycle.Dir(), sid+".jsonl"), 0o700))
	_, loadErr := lifecycle.Load(sid)
	require.Error(t, loadErr, "premise: the record is unreadable")
	require.NotErrorIs(t, loadErr, session.ErrLifecycleNotFound, "premise: it exists, it is not 'not found'")

	got := al.inputProducer(ts)
	require.NotEqual(t, windowProducerUser, got, "an unverifiable input must not be archived as a person's")
	require.Equal(t, "anonymous", got.modelAppend(providers.Message{Role: "user", Content: "hi"}, "a").Source.Kind,
		"it is archived with the existing 'anonymous' kind: a source that grants no human authority")
}
