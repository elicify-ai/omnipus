package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/voice"
)

type fixedTranscriber struct {
	text  string
	calls atomic.Int32
}

func (f *fixedTranscriber) Name() string { return "fixed" }
func (f *fixedTranscriber) Transcribe(context.Context, string) (*voice.TranscriptionResponse, error) {
	f.calls.Add(1)
	return &voice.TranscriptionResponse{Text: f.text}, nil
}

func audioConnFixture(t *testing.T, transcript string) (*addrFixture, *fixedTranscriber, string) {
	t.Helper()
	f := boundConnFixture(t)
	f.al.GetConfig().Context.BuiltinSuccessCap = 4000
	store := media.NewFileMediaStore()
	f.al.SetMediaStore(store)
	tr := &fixedTranscriber{text: transcript}
	f.al.SetTranscriber(tr)
	path := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(path, []byte("OggS"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := store.Store(path, media.MediaMeta{Filename: "voice.ogg", ContentType: "audio/ogg"}, "scope-f5")
	if err != nil {
		t.Fatal(err)
	}
	return f, tr, ref
}

// U8 r2 F5: a voice message whose TRANSCRIPTION pushes the turn text over the
// user-message bound is refused BEFORE any write (no main, capture, transcript
// entry); a transcription that fits is admitted, sized and persisted as the
// text the turn will consume, and is not transcribed a second time by the turn.
func TestAdmitBoundConnectorInput_AudioExpansionOverBoundRefusedBeforeAnyWrite(t *testing.T) {
	f, tr, ref := audioConnFixture(t, strings.Repeat("y", 4000))
	msg := connMsg("chat-1", "[voice]")
	msg.Media = []string{ref}
	refusal, _ := f.al.admitBoundConnectorInput(&msg)
	if refusal == "" {
		t.Fatal("a voice message whose transcription exceeds the bound must be refused at intake")
	}
	if msg.SessionID != "" {
		t.Fatalf("a refused message must stay unaddressed: %+v", msg)
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	if _, err := f.al.GetSessionStore().GetMeta(mainID); err == nil {
		t.Fatal("a refused voice message created the main")
	}
	if tr.calls.Load() != 1 {
		t.Fatalf("instrument check: the transcriber must have run once, ran %d", tr.calls.Load())
	}
}

func TestAdmitBoundConnectorInput_AudioThatFitsIsAdmittedOnceAndNotTranscribedAgain(t *testing.T) {
	f, tr, ref := audioConnFixture(t, "please summarise the report")
	msg := connMsg("chat-1", "[voice]")
	msg.Media = []string{ref}
	if refusal, err := f.al.admitBoundConnectorInput(&msg); refusal != "" || err != nil {
		t.Fatalf("refusal=%q err=%v", refusal, err)
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	entries, _ := f.al.GetSessionStore().ReadTranscript(mainID)
	if len(entries) != 1 || entries[0].Content != "[voice: please summarise the report]" {
		t.Fatalf("the transcript must hold the text the turn consumes: %+v", entries)
	}
	if !strings.Contains(msg.Content, "[voice: please summarise the report]") || msg.Metadata[metadataKeyAudioTranscribed] == "" {
		t.Fatalf("envelope/mark wrong: %+v", msg)
	}
	// The turn's own preparation must not transcribe again.
	// (did stays true so the turn still sends the deferred placeholder.)
	out, _ := f.al.transcribeAudioInMessageUnlessDone(context.Background(), msg)
	if tr.calls.Load() != 1 || out.Content != msg.Content {
		t.Fatalf("second transcription happened: calls=%d content=%q", tr.calls.Load(), out.Content)
	}
}
