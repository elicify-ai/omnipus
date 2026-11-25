package agent

// Spec #1968 (read-file native PDF) — loop-level tests T1, T2, T3, T3b, T9,
// T10, T10a, T10b, T12, T13, T14, T15, T16, T17 (spec §6 table). The
// A-labelled tests FAIL BY ASSERTION on today's code: the media conductor
// attaches a chat PDF natively only when the model id matches the hard-coded
// pdfCapableModel allow-list (loop_media.go::resolveMediaRefsWithOffload),
// so for the fixture pairs the PDF never reaches the wire, and the
// PDF limits come only from config; the catalogue supplies the pdf modality.
//
// T9/T15 also cover the FR-005/FR-007 fallback chain (F8): one agent whose
// primary is flagged and whose AgentConfig.FallbackModels candidate is
// text-only, both attempts visible on one recording peer — the native
// decision is re-evaluated per candidate within ONE turn, in both chain
// directions. The chat side of T14 (history replays the native PDF every
// turn, §4.4 line 198) is TestChatPDF_HistoryReplayResendsNative here; the
// tool T14 row (non-retention sweep) and T16 live in
// chat_pdf_tool_item_buildfail_test.go.
//
// The harness (chat_pdf_1968_fixture_test.go) routes each fixture pair
// through a recording openai-compatible httptest peer; every assertion
// reads the RAW request bodies the peer saw. Oracles: spec §5
// (FR-5..FR-18), §4.2 (limit precedence), §4.3 (seed data as fixture data
// only — no limit numbers in engine code), §5.6 (fixed notice content),
// §6 (test table). Run targeted:
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -p 1 -count=1 \
//	  -run 'TestChatPDF_|TestPDF_|TestAttachPDF_|TestToolPDF_' ./pkg/agent/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// pdf1968FixedResponses scripts two successful "ok" peer answers.
func pdf1968FixedResponses() []pdf1968Step {
	return []pdf1968Step{{content: "ok"}, {content: "ok"}}
}

// pdf1968FlaggedHarness builds a one-agent harness on the flagged fixture
// pair (fx/doc-reader-9 / fx/tiny-haiku-pdf) with the given scripted peer —
// the common shape of every recovery-matrix arm (F8/F10 edit round).
func pdf1968FlaggedHarness(t *testing.T, peer *pdf1968Peer) (*AgentLoop, map[string]*AgentInstance, *session.UnifiedStore, media.MediaStore) {
	t.Helper()
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{
		pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
	})
	return pdf1968NewPeerHarness(t, peer, cat, cat, nil,
		pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
}

// pdf1968ChainHarness builds a ONE-agent harness whose primary and fallback
// candidates ride the given catalogue rows — F8 drives BOTH chain
// directions through it (flagged→text-only and text-only→flagged) — wired
// through AgentConfig.FallbackModels (FR-005/FR-007). Both candidates route
// to the same recording peer; the fallback's provider row is appended in
// the tune hook so the chain resolves it from cfg.Providers.
func pdf1968ChainHarness(t *testing.T, peer *pdf1968Peer, primaryRow, fallbackRow pdf1968ProviderRow,
	primary, fallback pdf1968Pair,
) (*AgentLoop, map[string]*AgentInstance, *session.UnifiedStore, media.MediaStore) {
	t.Helper()
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{primaryRow, fallbackRow})
	tune := func(c *config.Config) {
		c.Agents.List[0].FallbackModels = config.FallbackModelSlice{
			{Model: fallback.model, Provider: fallback.provider},
		}
		c.Providers = append(c.Providers, &config.ModelConfig{
			Provider:  fallback.provider,
			Model:     fallback.model,
			APIBase:   peer.server.URL,
			APIKeyRef: pdf1968APIKeyEnv,
		})
	}
	return pdf1968NewPeerHarness(t, peer, cat, cat, tune, primary)
}

// ---------------------------------------------------------------------------
// T1 (A) — a chat PDF on a flagged pair reaches the wire as the file's exact
// native bytes. FR-13, §6 row T1.
// ---------------------------------------------------------------------------

func TestChatPDF_FlaggedRowSendsNativeBytes(t *testing.T) {
	data := pdf1968TextPDF(t, 1, true)
	peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{
		pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
	})
	al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
		pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
	sid := pdf1968NewSession(t, store, "pdf1968-agent")
	ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

	answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
	require.NoError(t, err)
	assert.Equal(t, "ok", answer)

	bodies := peer.Bodies()
	require.NotEmpty(t, bodies, "the provider must have been called")
	pdfs := pdf1968BodyPDFs(t, bodies[0])
	require.Len(t, pdfs, 1,
		"a flagged chat PDF must travel as exactly one native PDF media block on the first request")
	require.True(t, bytes.Equal(pdfs[0], data),
		"the native PDF block must carry the file's exact bytes (%d vs %d)", len(pdfs[0]), len(data))
}

// ---------------------------------------------------------------------------
// T2 (G) — an unflagged row never sends native bytes; the model still sees
// the extracted text. FR-13's non-native arm. Green on today's code (the
// conductor's text-injection branch) and must stay green after the change.
// ---------------------------------------------------------------------------

func TestChatPDF_UnflaggedRowShowsText(t *testing.T) {
	data := pdf1968TextPDF(t, 1, true)
	peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{
		pdf1968TextOnlyRow("fx/plain-gateway", "fx/shared-model", nil),
	})
	al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
		pdf1968Pair{provider: "fx/plain-gateway", model: "fx/shared-model"})
	sid := pdf1968NewSession(t, store, "pdf1968-agent")
	ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

	answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
	require.NoError(t, err)
	assert.Equal(t, "ok", answer)

	bodies := peer.Bodies()
	require.NotEmpty(t, bodies)
	assert.False(t, pdf1968HasPDF(t, bodies[0]),
		"an unflagged row must never carry native PDF bytes")
	assert.Contains(t, bodies[0], "PAGE 1 text",
		"the non-native arm must still show the model the PDF's extracted text")
}

// ---------------------------------------------------------------------------
// T3 (A) — no usable catalogue document ⇒ the turn is refused before any
// LLM call with code model_list_unavailable and the C1 notice (§5.6,
// fixed content). FR-11. Today the conductor is optimistic (nil catalog →
// text injection) and the provider is called with no such notice.
// ---------------------------------------------------------------------------

func TestPDF_NoCatalogDocument_TurnRefused(t *testing.T) {
	data := pdf1968TextPDF(t, 1, true)

	t.Run("nil catalog refuses the PDF turn before the provider", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "must never be reached"})
		// procCat resolves the pair for construction; the loop boots with NO
		// catalogue document.
		procCat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		// A caller-chosen home so the persisted entry can be re-read through
		// a genuinely reopened store (F11): production roots the shared
		// store at <home>/sessions (shared_session_store.go).
		home := filepath.Join(t.TempDir(), "home")
		al, agents, store, mediaStore := pdf1968NewPeerHarnessAt(t, peer, procCat, nil, home, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.Error(t, err, "a PDF turn without a catalogue document must fail the turn")
		assert.Equal(t, 0, peer.CallCount(),
			"the provider must never be called when the model list is not loaded")
		errs := events.modelListErrors()
		require.Len(t, errs, 1, "exactly one model_list_unavailable error event expected, got %+v", events.all())
		pdf1968AssertC1(t, errs[0].Message, "fx/tiny-haiku-pdf")
		pdf1968AssertStoredC1(t, store, sid, "fx/tiny-haiku-pdf")

		// Persisted error entry (F11): the refusal is recorded with the
		// typed code, not just free text — replay routes on ErrorCode
		// (turn_transcript.go::appendClassifiedError).
		entries, readErr := store.ReadTranscript(sid)
		require.NoError(t, readErr)
		found := false
		for _, e := range entries {
			if e.ErrorCode != "model_list_unavailable" {
				continue
			}
			found = true
			assert.Equal(t, "error", e.Status, "the refusal entry must be an error entry")
			pdf1968AssertC1(t, e.Content, "fx/tiny-haiku-pdf")
		}
		assert.True(t, found, "the C1 refusal must be persisted with the model_list_unavailable code")

		// Reopened replay (F11): a fresh store instance over the same
		// on-disk root replays the same refusal (S8: "error C1 live and
		// after reload"). The loop-level sharedStores cache would return
		// the same instance, so this constructs an independent store the
		// way production's constructor does.
		fresh, ferr := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
		require.NoError(t, ferr)
		replayed, rerr := fresh.ReadTranscript(sid)
		require.NoError(t, rerr)
		replayedFound := false
		for _, e := range replayed {
			if e.ErrorCode == "model_list_unavailable" {
				replayedFound = true
				assert.Equal(t, "error", e.Status, "the reopened refusal remains an error entry")
				pdf1968AssertC1(t, e.Content, "fx/tiny-haiku-pdf")
			}
		}
		assert.True(t, replayedFound, "the reopened transcript must replay the model_list_unavailable refusal")
	})

	t.Run("catalog.New() (a catalogue object serving no document) refuses the PDF turn", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "must never be reached"})
		procCat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		empty := catalog.New()
		require.Nil(t, empty.Document(),
			"premise: catalog.New() must serve no document")
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, procCat, empty, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.Error(t, err, "a PDF turn on a document-less catalogue object must fail the turn")
		assert.Equal(t, 0, peer.CallCount(),
			"the provider must never be called when the catalogue serves no document")
		errs := events.modelListErrors()
		require.Len(t, errs, 1, "exactly one model_list_unavailable error event expected, got %+v", events.all())
		pdf1968AssertC1(t, errs[0].Message, "fx/tiny-haiku-pdf")
		pdf1968AssertStoredC1(t, store, sid, "fx/tiny-haiku-pdf")
	})

	// F11: a VALID catalogue that merely lacks this provider/model pair is
	// NOT the FR-3 state. FR-3 fires only when the catalogue serves no
	// document at all; a pair-less model simply is not PDF-capable (FR-1:
	// pdfNativeSupported needs the exact row), so the chat PDF takes the
	// normal text path and the turn succeeds. Demanding model_list_unavailable
	// here would forbid exactly the graceful behaviour FR-3 keeps for the
	// pair-less case.
	t.Run("valid catalogue without this pair sends text normally — no refusal", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		// procCat carries the pair (construction resolves); the loop document
		// is a valid catalogue that does NOT list this pair.
		procCat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		loopDoc := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/other-gateway", "fx/other-model", nil),
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, procCat, loopDoc, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err, "a pair-less model is not capable — the turn takes the normal text path, it does not refuse")
		assert.Equal(t, "ok", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 1, "exactly one normal provider call")
		assert.False(t, pdf1968HasPDF(t, bodies[0]),
			"a model the catalogue does not list is not PDF-capable: no native bytes")
		assert.Contains(t, bodies[0], "PAGE 1 text",
			"the model still sees the PDF's extracted text (the normal non-native arm)")
		assert.Empty(t, events.modelListErrors(),
			"a valid catalogue without the pair must NOT produce model_list_unavailable (FR-3 fires only on no document)")
		assert.NotContains(t, strings.ToLower(bodies[0]), "pdf not sent",
			"FR-3: no catalogue-refusal notice on the normal pair-less text path")
	})

	// F11: the third T3 loop state — Boot with an INVALID embedded snapshot
	// and no persisted copy yields a catalogue with NO document (E7), and the
	// PDF turn must refuse exactly like the nil case (the production wiring
	// is gateway → Boot → SetCapabilityCatalog; the test drives the same
	// seam with the real Boot).
	t.Run("boot with an invalid embedded snapshot and no persisted copy refuses the PDF turn", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "must never be reached"})
		procCat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		booted := catalog.Boot(t.Context(), []byte("{ this is not a catalogue"),
			nil, pdf1968BootStore{readErr: fs.ErrNotExist}, nil)
		require.Nil(t, booted.Document(),
			"premise: Boot with an invalid embedded snapshot and no persisted copy must serve no document (E7)")
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, procCat, booted, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.Error(t, err, "a PDF turn on a Boot-with-no-document catalogue must fail the turn")
		assert.Equal(t, 0, peer.CallCount(),
			"the provider must never be called when Boot served no document")
		errs := events.modelListErrors()
		require.Len(t, errs, 1)
		pdf1968AssertC1(t, errs[0].Message, "fx/tiny-haiku-pdf")
		pdf1968AssertStoredC1(t, store, sid, "fx/tiny-haiku-pdf")
	})
}

// pdf1968BootStore is a catalog.Store stub for the Boot-path arms: it serves
// one fixed result from Read (bytes or a read error) and records nothing.
type pdf1968BootStore struct {
	data    []byte
	readErr error
}

func (s pdf1968BootStore) Read(context.Context) ([]byte, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.data, nil
}

func (s pdf1968BootStore) Write(context.Context, []byte) error { return nil }

// pdf1968BootPuller is a catalog.Puller stub whose Pull always fails with a
// transport error — the refresh-failure arm (T3b: a failed refresh keeps the
// served document; it is not "no catalogue").
type pdf1968BootPuller struct{ err error }

func (p pdf1968BootPuller) Pull(context.Context) ([]byte, error) { return nil, p.err }

func (p pdf1968BootPuller) LastPullDegraded() (bool, error) { return false, nil }

// ---------------------------------------------------------------------------
// T3b (G) — a usable catalogue document means no model_list_unavailable,
// with and without a PDF attached. Green today (optimistic conductor) and
// must stay green.
// ---------------------------------------------------------------------------

func TestPDF_CatalogPresent_NoTurnRefusal(t *testing.T) {
	data := pdf1968TextPDF(t, 1, true)

	t.Run("flagged pair with a chat pdf produces no refusal", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, peer.CallCount(), 1)
		assert.Empty(t, events.modelListErrors(),
			"a usable catalogue must not fire model_list_unavailable, got %+v", events.all())
	})

	t.Run("a no-PDF request is unaffected by the catalogue gate", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		al, agents, store, _ := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, peer.CallCount(), 1)
		assert.Empty(t, events.modelListErrors())
	})

	// F11: the T3b Boot-path states, driven through the real catalog.Boot the
	// same way production wires it.
	t.Run("a booted valid embedded snapshot offline serves the pair", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		procCat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		embedded := pdf1968DocJSON(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		booted := catalog.Boot(t.Context(), embedded, nil, pdf1968BootStore{readErr: fs.ErrNotExist}, nil)
		require.True(t, booted.Resolve("fx/doc-reader-9", "fx/tiny-haiku-pdf").Found(),
			"premise: Boot must serve the valid embedded snapshot offline")
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, procCat, booted, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err, "a valid embedded snapshot offline is not 'no catalogue'")
		assert.GreaterOrEqual(t, peer.CallCount(), 1)
		assert.Empty(t, events.modelListErrors())
	})

	t.Run("a failed refresh keeps serving the booted document", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		procCat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		embedded := pdf1968DocJSON(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		booted := catalog.Boot(t.Context(), embedded,
			pdf1968BootPuller{err: errors.New("network down")}, pdf1968BootStore{readErr: fs.ErrNotExist}, nil)
		require.Error(t, booted.Refresh(t.Context()), "the stubbed pull must fail")
		require.True(t, booted.Resolve("fx/doc-reader-9", "fx/tiny-haiku-pdf").Found(),
			"premise (the T3b oracle itself): a failed refresh must KEEP the document served")
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, procCat, booted, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err, "a failed refresh keeps the document; the PDF turn must go native")
		assert.GreaterOrEqual(t, peer.CallCount(), 1)
		assert.Empty(t, events.modelListErrors())
	})

	t.Run("a no-PDF request on a document-less catalogue sends normally", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		procCat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		empty := catalog.New()
		require.Nil(t, empty.Document(), "premise: catalog.New() carries no document")
		al, agents, store, _ := pdf1968NewPeerHarness(t, peer, procCat, empty, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		events := pdf1968Capture(al)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.NoError(t, err, "without a PDF there is nothing to refuse on a document-less catalogue")
		assert.GreaterOrEqual(t, peer.CallCount(), 1)
		assert.Empty(t, events.modelListErrors())
	})
}

// ---------------------------------------------------------------------------
// T9 (A) — the native decision is per (provider, model) attempt, not per
// model id: the same model id is native on a flagged provider and text on
// an unflagged one, in both directions, and inline_pdf:false forces text.
// FR-10/FR-13.
// ---------------------------------------------------------------------------

func TestAttachPDF_PerAttemptDecision(t *testing.T) {
	data := pdf1968TextPDF(t, 1, true)

	t.Run("same model id native on flagged provider, text on unflagged one", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968FixedResponses()...)
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/shared-model", nil),
			pdf1968TextOnlyRow("fx/plain-gateway", "fx/shared-model", nil),
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/shared-model"},
			pdf1968Pair{provider: "fx/plain-gateway", model: "fx/shared-model"})

		sid1 := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid1, ref)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.NotEmpty(t, bodies)
		assert.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1,
			"the flagged provider's attempt must carry the native PDF")

		sid2 := pdf1968NewSession(t, store, "pdf1968-agent-2")
		_, err = pdf1968RunTurn(t, al, agents["pdf1968-agent-2"], store, sid2, ref)
		require.NoError(t, err)
		bodies = peer.Bodies()
		require.Len(t, bodies, 2)
		assert.False(t, pdf1968HasPDF(t, bodies[1]),
			"the same model id on the unflagged provider must not carry native PDF bytes")
	})

	t.Run("reverse wiring: flagged attempt is the second agent", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968FixedResponses()...)
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968TextOnlyRow("fx/plain-gateway", "fx/shared-model", nil),
			pdf1968CapableRow("fx/doc-reader-9", "fx/shared-model", nil),
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/plain-gateway", model: "fx/shared-model"},
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/shared-model"})

		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)
		sid1 := pdf1968NewSession(t, store, "pdf1968-agent")
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid1, ref)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.NotEmpty(t, bodies)
		assert.False(t, pdf1968HasPDF(t, bodies[0]),
			"the unflagged provider's attempt must not carry the PDF (direction 1)")

		sid2 := pdf1968NewSession(t, store, "pdf1968-agent-2")
		_, err = pdf1968RunTurn(t, al, agents["pdf1968-agent-2"], store, sid2, ref)
		require.NoError(t, err)
		bodies = peer.Bodies()
		require.Len(t, bodies, 2)
		assert.Len(t, pdf1968BodyPDFs(t, bodies[1]), 1,
			"the flagged provider's attempt must carry the PDF (direction 2)")
	})

	t.Run("inline_pdf false row forces text despite the pdf modality", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			{
				id: "fx/doc-reader-9",
				models: []pdf1968ModelRow{{
					id:         "fx/pdf-flagged-off",
					modalities: []string{"text", "pdf"},
					limits:     map[string]any{"inline_pdf": false},
				}},
			},
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, func(c *config.Config) {
			c.PDFLimits = pdf1968LimitTable(t, nil, []pdf1968ProviderRow{pdf1968CapableRow("fx/doc-reader-9", "fx/pdf-flagged-off", map[string]any{"inline_pdf": false})})
		}, pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/pdf-flagged-off"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.NotEmpty(t, bodies)
		assert.False(t, pdf1968HasPDF(t, bodies[0]),
			"an inline_pdf:false row must force the text path even with the pdf modality")
	})

	t.Run("candidate switch inside one turn: flagged 503, unflagged fallback answers", func(t *testing.T) {
		// F8: the native decision is re-evaluated per candidate WITHIN one
		// turn. The primary (flagged) 503s; the fallback chain switches to
		// the unflagged candidate, whose own request must carry the
		// extracted text — a per-model decision computed once would attach
		// the PDF to the fallback's request too.
		primary := pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/chain-model"}
		fallback := pdf1968Pair{provider: "fx/plain-gateway", model: "fx/chain-model"}
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 503, errBody: "upstream overloaded"},
			pdf1968Step{content: "answered by the fallback"})
		al, agents, store, mediaStore := pdf1968ChainHarness(t, peer,
			pdf1968CapableRow(primary.provider, primary.model, nil),
			pdf1968TextOnlyRow(fallback.provider, fallback.model, nil),
			primary, fallback)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err, "the fallback candidate must answer the turn")
		assert.Equal(t, "answered by the fallback", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2, "exactly one attempt per candidate, inside ONE turn")
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1,
			"the flagged primary's attempt carries the native PDF")
		assert.False(t, pdf1968HasPDF(t, bodies[1]),
			"the unflagged fallback's attempt must not carry native PDF bytes")
		assert.Contains(t, bodies[1], "PAGE 1 text",
			"the fallback's request must show the extracted text instead")
	})

	t.Run("reverse chain inside one turn: unflagged 503, flagged fallback answers natively", func(t *testing.T) {
		// F8's reverse direction: the within-turn re-evaluation must go
		// text→native too. The primary (text-only) 503s; the chain switches
		// to the flagged candidate, whose request must carry the native PDF
		// byte-exact — a per-model decision computed once on the first
		// candidate would send the flagged fallback text as well. Exactly
		// two requests: one attempt per candidate, no extra retries.
		primary := pdf1968Pair{provider: "fx/plain-gateway", model: "fx/chain-model"}
		fallback := pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/chain-model"}
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 503, errBody: "upstream overloaded"},
			pdf1968Step{content: "answered by the flagged fallback"})
		al, agents, store, mediaStore := pdf1968ChainHarness(t, peer,
			pdf1968TextOnlyRow(primary.provider, primary.model, nil),
			pdf1968CapableRow(fallback.provider, fallback.model, nil),
			primary, fallback)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err, "the flagged fallback must answer the turn")
		assert.Equal(t, "answered by the flagged fallback", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2, "exactly one attempt per candidate, inside ONE turn")
		assert.False(t, pdf1968HasPDF(t, bodies[0]),
			"the text-only primary's attempt carries text, no native PDF")
		assert.Contains(t, bodies[0], "PAGE 1 text")
		carried := pdf1968BodyPDFs(t, bodies[1])
		require.Len(t, carried, 1,
			"the flagged fallback's attempt must carry the native PDF")
		require.True(t, bytes.Equal(carried[0], data),
			"the fallback's native PDF is byte-exact (%d vs %d bytes)", len(carried[0]), len(data))
	})
}

// ---------------------------------------------------------------------------
// T10 (A) — engine-configured PDF limit enforcement: file bytes, total file
// bytes, pages per file (101-page fixture), pages per request, unknown-page
// accounting, documents per request (newest-first), and the operator
// max_media_size interplay. FR-13 + §4.2. Today every "native" arm is red
// (no native attach at all) and no L2 notice exists.
// ---------------------------------------------------------------------------

func TestAttachPDF_Limits(t *testing.T) {
	// Fixture data (§6.1): the numbers below are config fixture row values, never
	// production Go constants or catalogue members.
	const (
		fileCap    = 200000
		totalCap   = 300000
		pageCap    = 100
		docCap     = 5
		bigPDF     = 250000
		smallPDF   = 100000
		mediaRaise = 40000000
		mediaLow   = 4096
	)

	// runOne builds a fresh harness with the given limit rows and runs one
	// turn carrying the given PDFs; it returns the first request's native
	// PDFs (decoded, wire order) and the raw first request body.
	runOne := func(t *testing.T, providerLimits, modelLimits, defaultLimits map[string]any,
		maxMediaSize int, datas ...[]byte,
	) (pdfs [][]byte, body string) {
		t.Helper()
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		rows := []pdf1968ProviderRow{
			{
				id:     "fx/limits-gate",
				limits: providerLimits,
				models: []pdf1968ModelRow{{id: "fx/limits-model", modalities: []string{"text", "pdf"}, limits: modelLimits}},
			},
		}
		cat := pdf1968Catalog(t, rows)
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, func(c *config.Config) {
			c.PDFLimits = pdf1968LimitTable(t, defaultLimits, rows)
			if maxMediaSize > 0 {
				c.Agents.Defaults.MaxMediaSize = maxMediaSize
			}
		}, pdf1968Pair{provider: "fx/limits-gate", model: "fx/limits-model"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		refs := make([]string, len(datas))
		for i, d := range datas {
			refs[i] = pdf1968StorePDF(t, mediaStore, fmt.Sprintf("doc%d.pdf", i), d)
		}
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, refs...)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.NotEmpty(t, bodies)
		return pdf1968BodyPDFs(t, bodies[0]), bodies[0]
	}

	t.Run("max_file_bytes: at the cap native, one byte over text with L2", func(t *testing.T) {
		limits := map[string]any{"max_file_bytes": fileCap}
		pdfs, _ := runOne(t, limits, nil, nil, 0, pdf1968PDFOfSize(t, fileCap))
		assert.Len(t, pdfs, 1, "a PDF exactly at max_file_bytes must go native")

		pdfs, body := runOne(t, limits, nil, nil, 0, pdf1968PDFOfSize(t, fileCap+1))
		assert.Empty(t, pdfs, "a PDF over max_file_bytes must not go native")
		pdf1968AssertL2(t, "max_file_bytes over", body, "max_file_bytes",
			[]string{"fx/limits-gate", "fx/limits-model"}, fileCap)
	})

	t.Run("max_total_file_bytes: exact cap admits both, one byte over demotes the oldest", func(t *testing.T) {
		// F4: the positive arm sits EXACTLY at the cap — 150,000 + 150,000
		// = 300,000 = totalCap — so both files must go native (inclusive
		// limit); the old fixture overshot the cap on the positive side, so
		// a wrongly-strict resolver could pass it. The over arm is cap+1:
		// only the newest stays native, with L2 naming the limit.
		limits := map[string]any{"max_total_file_bytes": totalCap}
		half := pdf1968PDFOfSize(t, totalCap/2)         // 150,000
		overNewest := pdf1968PDFOfSize(t, totalCap/2+1) // 150,001

		pdfs, _ := runOne(t, limits, nil, nil, 0, half, half)
		require.Len(t, pdfs, 2, "both files at exactly max_total_file_bytes must go native")
		for i, p := range pdfs {
			assert.True(t, bytes.Equal(p, half),
				"position %d must carry the exact fixture bytes", i)
		}

		pdfs, body := runOne(t, limits, nil, nil, 0, half, overNewest)
		require.Len(t, pdfs, 1, "one byte over max_total_file_bytes demotes the oldest")
		assert.True(t, bytes.Equal(pdfs[0], overNewest),
			"the newest (last-attached) file must be the one kept native")
		pdf1968AssertL2(t, "max_total_file_bytes over", body, "max_total_file_bytes",
			[]string{"fx/limits-gate", "fx/limits-model"}, totalCap)
	})

	t.Run("max_pages_per_file: 100 pages native, 101 pages text with L2", func(t *testing.T) {
		limits := map[string]any{"max_pages_per_file": pageCap}
		pdfs, _ := runOne(t, limits, nil, nil, 0, pdf1968TextPDF(t, 100, true))
		assert.Len(t, pdfs, 1, "a PDF at the page cap must go native")

		pdfs, body := runOne(t, limits, nil, nil, 0, pdf1968TextPDF(t, 101, true))
		assert.Empty(t, pdfs, "a PDF over max_pages_per_file must not go native")
		pdf1968AssertL2(t, "max_pages_per_file over", body, "max_pages_per_file",
			[]string{"fx/limits-gate", "fx/limits-model"}, pageCap)
	})

	t.Run("max_pages_per_request: newest admitted first, older demoted", func(t *testing.T) {
		limits := map[string]any{"max_pages_per_request": pageCap}
		p51 := pdf1968TextPDF(t, 51, true)
		p50 := pdf1968TextPDF(t, 50, true)

		pdfs, _ := runOne(t, limits, nil, nil, 0, p50, p50)
		assert.Len(t, pdfs, 2, "50 + 50 is exactly the inclusive 100-page request cap")

		// 51 + 50 is exactly cap+1, with neither PDF over a per-file cap.
		pdfs, body := runOne(t, limits, nil, nil, 0, p51, p50)
		require.Len(t, pdfs, 1)
		assert.True(t, bytes.Equal(pdfs[0], p50),
			"the newest file must be admitted first under max_pages_per_request")
		pdf1968AssertL2(t, "max_pages_per_request over", body, "max_pages_per_request",
			[]string{"fx/limits-gate", "fx/limits-model"}, pageCap)
	})

	t.Run("unknown page counts add zero pages but still count bytes and documents", func(t *testing.T) {
		// F4: genuinely unknown-page fixtures of EXACT size (no /Count),
		// so the byte arm cannot pass on a size the fixture does not
		// control — unknown-page accounting must consume bytes while adding
		// zero pages, and the file must still count as a document.
		unknown60k := pdf1968UnknownPagePDFOfSize(t, 60000)
		unknownSmall := pdf1968UnknownPagePDFOfSize(t, 8192)

		pdfs, _ := runOne(t, map[string]any{"max_pages_per_request": 1}, nil, nil, 0,
			pdf1968TextPDF(t, 1, false), pdf1968TextPDF(t, 1, false))
		assert.Len(t, pdfs, 2,
			"unknown page counts must add zero pages to the request total (both stay native)")

		pdfs, body := runOne(t, map[string]any{"max_pages_per_request": 1}, nil, nil, 0,
			pdf1968TextPDF(t, 2, true))
		assert.Empty(t, pdfs, "a known 2-page PDF exceeds a 1-page request cap")
		pdf1968AssertL2(t, "pages/request over an unknown-page pair", body, "max_pages_per_request",
			[]string{"fx/limits-gate", "fx/limits-model"}, 1)

		pdfs, body = runOne(t, map[string]any{"max_total_file_bytes": 50000}, nil, nil, 0, unknown60k)
		assert.Empty(t, pdfs, "an unknown-page PDF must still be counted against the byte total")
		pdf1968AssertL2(t, "total bytes over an unknown-page file", body, "max_total_file_bytes",
			[]string{"fx/limits-gate", "fx/limits-model"}, 50000)
		pdfs, _ = runOne(t, map[string]any{"max_total_file_bytes": 50000}, nil, nil, 0, unknownSmall)
		assert.Len(t, pdfs, 1, "the same unknown-page file under the cap stays native")

		pdfs, body = runOne(t, map[string]any{"max_documents_per_request": 1}, nil, nil, 0,
			unknownSmall, unknownSmall)
		assert.Len(t, pdfs, 1, "unknown pages do not excuse the documents-per-request count")
		pdf1968AssertL2(t, "documents/request over an unknown-page pair", body, "max_documents_per_request",
			[]string{"fx/limits-gate", "fx/limits-model"}, 1)
	})

	t.Run("max_documents_per_request: five native, sixth oldest demoted", func(t *testing.T) {
		limits := map[string]any{"max_documents_per_request": docCap}
		// Distinct sizes so the demoted file is identifiable by its bytes.
		sizes := []int{5000, 10000, 15000, 20000, 25000, 30000}
		six := make([][]byte, 6)
		for i := range six {
			six[i] = pdf1968PDFOfSize(t, sizes[i])
		}

		pdfs, _ := runOne(t, limits, nil, nil, 0, six[:5]...)
		require.Len(t, pdfs, 5, "five PDFs within max_documents_per_request all go native")
		for i, p := range pdfs {
			assert.True(t, bytes.Equal(p, six[i]),
				"within the cap the attachment order must be preserved (position %d)", i)
		}

		pdfs, body := runOne(t, limits, nil, nil, 0, six...)
		assert.Len(t, pdfs, 5, "over the cap, the newest five stay native")
		for _, p := range pdfs {
			assert.False(t, bytes.Equal(p, six[0]),
				"the oldest (first-attached) file must be the one demoted")
		}
		pdf1968AssertL2(t, "documents/request over", body, "max_documents_per_request",
			[]string{"fx/limits-gate", "fx/limits-model"}, docCap)
	})

	t.Run("operator max_media_size raised and lowered", func(t *testing.T) {
		// No effective PDF limits — only the operator media size setting
		// governs.
		raised := pdf1968PDFOfSize(t, 30000000)
		pdfs, _ := runOne(t, nil, nil, nil, mediaRaise, raised)
		assert.Len(t, pdfs, 1, "under a raised max_media_size a 30 MB PDF goes native")

		pdfs, body := runOne(t, nil, nil, nil, mediaLow, pdf1968PDFOfSize(t, 10240))
		assert.Empty(t, pdfs, "over max_media_size the PDF must not go native")
		assert.Contains(t, body, "media size setting",
			"the request-only L1 notice must name the media size setting")
	})
}

// ---------------------------------------------------------------------------
// T10a (A) — the §4.3 seed rows behave as researched: the default row's
// values, each lower own row, and the azure model row overriding the
// provider row. Seed values are config fixture data; shipped-file parity is checked
// independently in pkg/config.
// ---------------------------------------------------------------------------

func TestAttachPDF_DefaultRowAndOwnRows(t *testing.T) {
	// §4.3 seed data, verbatim.
	seedDefault := map[string]any{
		"max_file_bytes":            50000000,
		"max_total_file_bytes":      50000000,
		"max_pages_per_file":        100,
		"max_documents_per_request": 5,
	}
	seedProvs := []pdf1968ProviderRow{
		{
			id:     "anthropic",
			limits: map[string]any{"max_request_bytes": 128000000, "max_pages_per_request": 100},
			models: []pdf1968ModelRow{{id: "claude-sonnet-4", modalities: []string{"text", "pdf"}}},
		},
		{
			id: "amazon-bedrock",
			limits: map[string]any{
				"max_file_bytes": 4500000, "max_documents_per_message": 5,
				"max_request_bytes": 20000000, "max_pages_per_request": 100,
			},
			models: []pdf1968ModelRow{{id: "claude-sonnet-4", modalities: []string{"text", "pdf"}}},
		},
		{
			id: "google-vertex-anthropic",
			limits: map[string]any{
				"max_file_bytes": 20000000, "max_total_file_bytes": 20000000,
				"max_request_bytes": 30000000, "max_pages_per_request": 100,
			},
			models: []pdf1968ModelRow{{id: "claude-sonnet-4", modalities: []string{"text", "pdf"}}},
		},
		{
			// azure's provider row: default values + pages per request; the
			// claude-* MODEL row carries the Foundry caps.
			id:     "azure",
			limits: map[string]any{"max_pages_per_request": 100},
			models: []pdf1968ModelRow{{
				id:         "claude-sonnet-4",
				modalities: []string{"text", "pdf"},
				limits: map[string]any{
					"max_file_bytes": 20000000, "max_total_file_bytes": 20000000,
					"max_request_bytes": 32000000,
				},
			}},
		},
	}

	runSeed := func(t *testing.T, pair pdf1968Pair, datas ...[]byte) (pdfs [][]byte, body string) {
		t.Helper()
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		cat := pdf1968Catalog(t, seedProvs)
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, func(c *config.Config) {
			c.PDFLimits = pdf1968LimitTable(t, seedDefault, seedProvs)
			// F5: the seed rows admit 50 MB files; the operator media
			// setting must sit ABOVE the largest fixture so the
			// PDF-limit assertions are not masked by it.
			c.Agents.Defaults.MaxMediaSize = 100000000
		}, pair)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		refs := make([]string, len(datas))
		for i, d := range datas {
			refs[i] = pdf1968StorePDF(t, mediaStore, fmt.Sprintf("seed%d.pdf", i), d)
		}
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, refs...)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.NotEmpty(t, bodies)
		return pdf1968BodyPDFs(t, bodies[0]), bodies[0]
	}

	t.Run("anthropic inherits file, total and pages-per-file from the default row", func(t *testing.T) {
		pair := pdf1968Pair{provider: "anthropic", model: "claude-sonnet-4"}
		pdfs, _ := runSeed(t, pair, pdf1968PDFOfSize(t, 50000000))
		assert.Len(t, pdfs, 1, "50,000,000 bytes is exactly the default-row file cap")

		pdfs, body := runSeed(t, pair, pdf1968PDFOfSize(t, 50000001))
		assert.Empty(t, pdfs)
		pdf1968AssertL2(t, "default file cap", body, "max_file_bytes", []string{pair.provider, pair.model}, 50000000)

		pdfs, body = runSeed(t, pair, pdf1968TextPDF(t, 101, true))
		assert.Empty(t, pdfs, "101 pages exceeds the default-row pages-per-file cap")
		pdf1968AssertL2(t, "inherited page cap", body, "max_pages_per_file", []string{pair.provider, pair.model}, 100)

		// F5: the boundary POSITIVE for the inherited pages cap.
		pdfs, _ = runSeed(t, pair, pdf1968TextPDF(t, 100, true))
		assert.Len(t, pdfs, 1, "100 pages is exactly the default-row pages-per-file cap")
	})

	t.Run("bedrock's own 4.5 MB row overrides the default", func(t *testing.T) {
		pair := pdf1968Pair{provider: "amazon-bedrock", model: "claude-sonnet-4"}
		pdfs, _ := runSeed(t, pair, pdf1968PDFOfSize(t, 4500000))
		assert.Len(t, pdfs, 1, "4,500,000 bytes is exactly bedrock's file cap")

		pdfs, body := runSeed(t, pair, pdf1968PDFOfSize(t, 4500001))
		assert.Empty(t, pdfs)
		pdf1968AssertL2(t, "Bedrock file cap", body, "max_file_bytes", []string{pair.provider, pair.model}, 4500000)

		pdfs, body = runSeed(t, pair, pdf1968TextPDF(t, 101, true))
		assert.Empty(t, pdfs, "pages per file inherits the default row's 100")
		pdf1968AssertL2(t, "inherited page cap", body, "max_pages_per_file", []string{pair.provider, pair.model}, 100)

		// F5: the boundary positive on bedrock's own row too.
		pdfs, _ = runSeed(t, pair, pdf1968TextPDF(t, 100, true))
		assert.Len(t, pdfs, 1, "100 pages is exactly the inherited pages-per-file cap on bedrock")
	})

	t.Run("Bedrock request pages are independently bounded at 100 and 101", func(t *testing.T) {
		pair := pdf1968Pair{provider: "amazon-bedrock", model: "claude-sonnet-4"}
		p50, p51 := pdf1968TextPDF(t, 50, true), pdf1968TextPDF(t, 51, true)
		pdfs, _ := runSeed(t, pair, p50, p50)
		require.Len(t, pdfs, 2, "100 aggregate pages fit while both files are below 100")
		pdfs, body := runSeed(t, pair, p51, p50)
		require.Len(t, pdfs, 1, "101 aggregate pages demote the older file, not a per-file limit")
		require.True(t, bytes.Equal(pdfs[0], p50))
		pdf1968AssertL2(t, "Bedrock 101 request pages", body, "max_pages_per_request", []string{pair.provider, pair.model}, 100)
	})

	t.Run("vertex caps file bytes and total bytes", func(t *testing.T) {
		pair := pdf1968Pair{provider: "google-vertex-anthropic", model: "claude-sonnet-4"}
		pdfs, _ := runSeed(t, pair, pdf1968PDFOfSize(t, 20000000))
		assert.Len(t, pdfs, 1, "20,000,000 bytes is exactly vertex's file cap")

		pdfs, body := runSeed(t, pair, pdf1968PDFOfSize(t, 20000001))
		assert.Empty(t, pdfs)
		pdf1968AssertL2(t, "Vertex file cap", body, "max_file_bytes", []string{pair.provider, pair.model}, 20000000)

		half := pdf1968PDFOfSize(t, 10000000)
		pdfs, _ = runSeed(t, pair, half, half)
		require.Len(t, pdfs, 2, "10 MB + 10 MB is exactly Vertex's total cap")
		newest := pdf1968PDFOfSize(t, 10000001)
		pdfs, body = runSeed(t, pair, half, newest)
		require.Len(t, pdfs, 1, "20,000,001 total bytes demotes only the oldest")
		require.True(t, bytes.Equal(pdfs[0], newest))
		pdf1968AssertL2(t, "Vertex total +1", body, "max_total_file_bytes", []string{pair.provider, pair.model}, 20000000)
	})

	t.Run("azure's claude model row overrides the provider row and default", func(t *testing.T) {
		pair := pdf1968Pair{provider: "azure", model: "claude-sonnet-4"}
		pdfs, _ := runSeed(t, pair, pdf1968PDFOfSize(t, 20000000))
		assert.Len(t, pdfs, 1, "20,000,000 bytes is exactly the azure claude-* model-row cap")

		pdfs, body := runSeed(t, pair, pdf1968PDFOfSize(t, 20000001))
		assert.Empty(t, pdfs, "the model row's 20 MB cap must win over the 50 MB default")
		pdf1968AssertL2(t, "model file cap", body, "max_file_bytes", []string{pair.provider, pair.model}, 20000000)

		// F5: the azure model row's total cap at exact boundary and over.
		half := pdf1968PDFOfSize(t, 10000000)
		pdfs, _ = runSeed(t, pair, half, half)
		assert.Len(t, pdfs, 2, "the azure claude model row's 20,000,000 total admits both at the cap")

		overNewest := pdf1968PDFOfSize(t, 10000001)
		pdfs, body = runSeed(t, pair, half, overNewest)
		require.Len(t, pdfs, 1, "one byte over the azure model row's total demotes the oldest")
		assert.True(t, bytes.Equal(pdfs[0], overNewest), "the newest stays native")
		pdf1968AssertL2(t, "model total cap", body, "max_total_file_bytes", []string{pair.provider, pair.model}, 20000000)
	})

	t.Run("default row total: boundary admits two 25 MB files, one byte over demotes the oldest", func(t *testing.T) {
		// F5: the default row's 50,000,000 total cap exercised at the exact
		// boundary — two 25,000,000-byte fixtures sum exactly to it.
		pair := pdf1968Pair{provider: "anthropic", model: "claude-sonnet-4"}
		half := pdf1968PDFOfSize(t, 25000000)
		pdfs, _ := runSeed(t, pair, half, half)
		require.Len(t, pdfs, 2, "25,000,000 + 25,000,000 is exactly the default-row total cap")
		for i, p := range pdfs {
			assert.True(t, bytes.Equal(p, half), "position %d carries the exact fixture", i)
		}

		overNewest := pdf1968PDFOfSize(t, 25000001)
		pdfs, body := runSeed(t, pair, half, overNewest)
		require.Len(t, pdfs, 1, "one byte over the total demotes the oldest")
		assert.True(t, bytes.Equal(pdfs[0], overNewest), "the newest stays native")
		pdf1968AssertL2(t, "default total cap", body, "max_total_file_bytes", []string{pair.provider, pair.model}, 50000000)
	})

	t.Run("default row documents per request: five native, sixth oldest demoted", func(t *testing.T) {
		// F5: the default row's max_documents_per_request 5 at boundary and
		// over — distinct sizes identify the demoted file by its bytes.
		pair := pdf1968Pair{provider: "anthropic", model: "claude-sonnet-4"}
		sizes := []int{4096, 8192, 12288, 16384, 20480, 24576}
		six := make([][]byte, len(sizes))
		for i, s := range sizes {
			six[i] = pdf1968PDFOfSize(t, s)
		}

		pdfs, _ := runSeed(t, pair, six[:5]...)
		require.Len(t, pdfs, 5, "five PDFs within the default row's 5 all go native")
		for i, p := range pdfs {
			assert.True(t, bytes.Equal(p, six[i]),
				"within the cap the attachment order is preserved (position %d)", i)
		}

		pdfs, body := runSeed(t, pair, six...)
		require.Len(t, pdfs, 5, "the sixth, oldest PDF is demoted")
		for _, p := range pdfs {
			assert.False(t, bytes.Equal(p, six[0]),
				"the oldest (first-attached) file must be the demoted one")
		}
		pdf1968AssertL2(t, "default document count", body, "max_documents_per_request", []string{pair.provider, pair.model}, 5)
	})
}

// ---------------------------------------------------------------------------
// T10b (A) — flagged row with NO limits anywhere and a 30 MB, 700-page
// fixture: nothing is rejected up front, so a provider rejection must fall
// back to ONE text retry with the P1 notice. FR-16. Today the premise arm
// (native attach) is red.
// ---------------------------------------------------------------------------

func TestAttachPDF_NoRowRejectFallsBackToText(t *testing.T) {
	// F2: the fixture is the shared padded builder — a valid 700-page PDF
	// padded to exactly 30,000,000 bytes (not an ad-hoc pad whose trailer
	// might not parse). F5: the operator media setting is raised above the
	// fixture so the no-limits premise holds on the media gate too.
	data := pdf1968PadTo(t, pdf1968TextPDF(t, 700, true), 30000000)
	pdfRejectBody := `{"error":{"message":"pdf input not supported for this model","type":"invalid_request_error"}}`

	run := func(t *testing.T, steps []pdf1968Step) []string {
		t.Helper()
		peer := newPdf1968Peer(t, steps...)
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			// Flagged, with every shipped PDF default explicitly neutralized: no
			// provider row, no model row.
			pdf1968CapableRow("fx/free-row", "fx/free-model", nil),
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, func(c *config.Config) {
			c.Agents.Defaults.MaxMediaSize = 100000000
		}, pdf1968Pair{provider: "fx/free-row", model: "fx/free-model"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "huge.pdf", data)
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err, "the scripted recovery must resolve the turn")
		return peer.Bodies()
	}

	t.Run("400 pdf rejection: one text retry with P1, no L2", func(t *testing.T) {
		bodies := run(t, []pdf1968Step{
			{status: 400, errBody: pdfRejectBody},
			{content: "recovered"},
		})
		require.Len(t, bodies, 2, "exactly one text retry — no third attempt (FR-17)")
		pdfs0 := pdf1968BodyPDFs(t, bodies[0])
		require.Len(t, pdfs0, 1, "premise: the first attempt must carry the native PDF")
		assert.True(t, bytes.Equal(pdfs0[0], data))
		assert.NotContains(t, bodies[0], pdf1968L2Sentence,
			"the fallback must be limit-independent: no row, no L2")
		assert.False(t, pdf1968HasPDF(t, bodies[1]), "the retry must carry text, not the PDF")
		assert.Contains(t, bodies[1], "PAGE 1 text",
			"the retry shows the extracted text the model can actually read")
		assert.Contains(t, bodies[1], pdf1968P1Sentence)
	})

	t.Run("too many pages recovers once and later requests of the SAME large-PDF turn stay text", func(t *testing.T) {
		var alRef *AgentLoop
		var sidRef, ref string
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 400, errBody: `{"error":{"message":"too many pages","type":"invalid_request_error"}}`},
			pdf1968Step{content: "recovered", onRequest: func() {
				id, err := pdf1968QueueSteering(alRef, sidRef, "continue this same large-PDF turn", ref)
				if err != nil || id == "" {
					t.Errorf("enqueue later same-turn request: %q %v", id, err)
				}
			}},
			pdf1968Step{content: "finished"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{pdf1968CapableRow("fx/free-row", "fx/free-model", nil)})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, func(c *config.Config) { c.Agents.Defaults.MaxMediaSize = 100000000 }, pdf1968Pair{provider: "fx/free-row", model: "fx/free-model"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		alRef, sidRef = al, sid
		ref = pdf1968StorePDF(t, mediaStore, "huge.pdf", data)
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.Len(t, bodies, 3, "one native rejection, exactly one retry, one same-turn continuation")
		native := pdf1968BodyPDFs(t, bodies[0])
		require.Len(t, native, 1, "30 MB / 700 pages, no data limit: native payload premise")
		require.True(t, bytes.Equal(native[0], data), "exact 30 MB payload")
		assert.NotContains(t, bodies[0], pdf1968L2Sentence)
		for _, body := range bodies[1:] {
			assert.False(t, pdf1968HasPDF(t, body))
			assert.Contains(t, body, "PAGE 1 text")
			assert.Contains(t, body, pdf1968P1Sentence)
		}
		require.Contains(t, bodies[2], "continue this same large-PDF turn")
	})

	t.Run("413 payload too large: one text retry with P1", func(t *testing.T) {
		bodies := run(t, []pdf1968Step{
			{status: 413, errBody: "request entity too large"},
			{content: "recovered"},
		})
		require.Len(t, bodies, 2, "exactly one text retry (FR-17)")
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "premise: native PDF on the first attempt")
		assert.False(t, pdf1968HasPDF(t, bodies[1]))
		assert.Contains(t, bodies[1], "PAGE 1 text", "the retry carries the extracted text")
		assert.Contains(t, bodies[1], pdf1968P1Sentence)
	})

	t.Run("context overflow with a pdf: one text retry with P1", func(t *testing.T) {
		bodies := run(t, []pdf1968Step{
			{status: 400, errBody: "context window exceeded"},
			{content: "recovered"},
		})
		require.Len(t, bodies, 2, "exactly one text retry (FR-16/FR-17)")
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "premise: native PDF on the first attempt")
		assert.False(t, pdf1968HasPDF(t, bodies[1]))
		assert.Contains(t, bodies[1], "PAGE 1 text", "the retry carries the extracted text")
		assert.Contains(t, bodies[1], pdf1968P1Sentence)
	})
}

// ---------------------------------------------------------------------------
// T12 (A) — max_request_bytes enforcement consults the transport's
// serialized-body measurement (FR-12: "marshal without send", all four
// formats). The peer's recorded raw body IS the serialized request, so the
// exact boundary is MEASURED, not assumed: a first uncapped run measures the
// serialized length L of the fixture set; a cap of exactly L keeps the body
// native (and must re-measure to exactly L); L−1 demotes every PDF to text
// and the re-measured demoted body must actually fit the cap (FR-12's
// re-measure loop). The §6.1 discriminator pins decoded-vs-encoded: a
// 22,500,000-byte PDF encodes to exactly 30,000,000 base64 characters, so a
// row admitting 22,500,000 bytes passes the file check yet must demote on
// the REQUEST check — a decoded-arithmetic implementation stays native and
// fails. All four real transports are exercised: Chat Completions, Anthropic,
// Bedrock and Responses. The existing StoreProviderPool seam installs the
// real Responses adapter; only its network peer is scripted.
// ---------------------------------------------------------------------------

func TestAttachPDF_RequestBytes(t *testing.T) {
	// Fixture/§6.1 data: 22,500,000 bytes → exactly 30,000,000 base64 chars.
	const (
		decodedPDF  = 22500000
		fileCapSame = 22500000
		reqCapSame  = 30000000
	)

	filler := "" // ASCII non-PDF text used only by the specified 30 MB boundary case.
	// run builds one fixture row of the given protocol shape, runs one turn
	// carrying datas, and returns the first request's native PDFs (decoded,
	// for byte comparison), every raw body the provider received in order,
	// and the turn error. steps default to one "ok" answer in the row's
	// dialect. The home is caller-chosen so two harnesses in one subtest
	// produce byte-length-comparable bodies (T12's measured boundary needs
	// the two runs' serialized bodies to differ only by the thing under
	// test).
	run := func(t *testing.T, shape, home string, providerLimits map[string]any, datas [][]byte, steps ...pdf1968Step,
	) (pdfs [][]byte, bodies []string, runErr error) {
		t.Helper()
		if len(steps) == 0 {
			steps = []pdf1968Step{{content: "ok", shape: shape}}
		}
		peer := newPdf1968Peer(t, steps...)
		row := pdf1968ProviderRow{
			id:       "fx/req-bytes",
			limits:   providerLimits,
			protocol: shape,
			models:   []pdf1968ModelRow{{id: "fx/req-model", modalities: []string{"text", "pdf"}}},
		}
		if shape == "responses" {
			row.protocol = ""
		} // real adapter installed by the harness pool seam
		if shape == "bedrock" {
			row.region = "us-east-1"
			row.regions = []map[string]any{{"id": "us-east-1", "group": "us"}}
		}
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{row})
		al, agents, store, mediaStore := pdf1968NewPeerHarnessAt(t, peer, cat, cat, home, func(c *config.Config) {
			c.PDFLimits = pdf1968LimitTable(t, nil, []pdf1968ProviderRow{row})
			// Large ASCII filler must not hit an unrelated context-window cap.
			window := 100000000
			c.Agents.List[0].ContextWindowOverride = &window
			// F5: the fixtures exceed the 20 MiB default media setting.
			c.Agents.Defaults.MaxMediaSize = 100000000
		}, pdf1968Pair{provider: "fx/req-bytes", model: "fx/req-model"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		refs := make([]string, len(datas))
		for i, d := range datas {
			refs[i] = pdf1968StorePDF(t, mediaStore, fmt.Sprintf("req%d.pdf", i), d)
		}
		_, runErr = pdf1968RunTurnMsg(t, al, agents["pdf1968-agent"], store, sid, "summarise the attachment"+filler, refs...)
		if shape == "responses" && peer.CallCount() > 0 {
			require.Equal(t, "/openai/v1/responses", peer.Paths()[0])
		}
		bodies = peer.Bodies()
		if len(bodies) == 0 {
			return nil, nil, runErr
		}
		return pdf1968BodyPDFs(t, bodies[0]), bodies, runErr
	}

	// Full serialized requests from all four real transports.
	shapes := []struct{ shape, name string }{
		{"", "openai-compatible"},
		{"anthropic", "anthropic"},
		{"bedrock", "bedrock"},
		{"responses", "responses"},
	}

	for _, s := range shapes {
		t.Run("measured boundary "+s.name, func(t *testing.T) {
			base := t.TempDir()
			data := pdf1968PDFOfSize(t, 20000000)

			// Phase 1: measure the serialized body with no request cap —
			// pinning the premise on the DECODED native bytes, not a text
			// marker: the measured body must carry the fixture's exact
			// PDF, or the measurement below measures nothing real (N4).
			pdfs1, bodies1, err := run(t, s.shape, filepath.Join(base, "h1"), nil, [][]byte{data})
			require.NoError(t, err)
			require.Len(t, bodies1, 1)
			require.Len(t, pdfs1, 1, "premise: the measured request carries the native PDF")
			require.True(t, bytes.Equal(pdfs1[0], data),
				"premise: the native payload is the fixture's exact bytes")
			uncapped := bodies1[0]
			measured := len(uncapped)

			// Phase 2: the cap exactly at the measured length keeps the
			// body native — and its re-measured length must equal the
			// measurement, or the two harnesses disagreed and this
			// boundary is void (a loud failure, not a silent pass).
			pdfs, bodies, err := run(t, s.shape, filepath.Join(base, "h2"),
				map[string]any{"max_request_bytes": measured}, [][]byte{data})
			require.NoError(t, err)
			require.Len(t, bodies, 1)
			body := bodies[0]
			require.Len(t, pdfs, 1, "a body of exactly max_request_bytes bytes must go native")
			require.True(t, bytes.Equal(pdfs[0], data),
				"the capped request carries the fixture's exact native bytes")
			require.Len(t, body, measured,
				"the capped run's serialized body must reproduce the measured length exactly")

			// Phase 3: one byte less room demotes every PDF to text and
			// the re-measured demoted body must actually fit the cap
			// (FR-12 re-measures after each demotion before sending).
			pdfs, bodies, err = run(t, s.shape, filepath.Join(base, "h3"),
				map[string]any{"max_request_bytes": measured - 1}, [][]byte{data})
			require.NoError(t, err)
			require.Len(t, bodies, 1, "the demoted request is sent — FR-12 has no second recovery round")
			body = bodies[0]
			assert.Empty(t, pdfs, "over max_request_bytes the PDF must be demoted to text")
			pdf1968AssertL2(t, "request bytes over (measured boundary)", body, "max_request_bytes",
				[]string{"fx/req-bytes", "fx/req-model"}, measured-1)
			assert.LessOrEqual(t, len(body), measured-1,
				"the demoted request must re-measure under the cap before sending")
			assert.Contains(t, body, "PAGE 1 text", "the extracted text travels instead")
		})

		t.Run("decoded-vs-encoded discriminator "+s.name, func(t *testing.T) {
			// §6.1: the PDF passes the file check (22,500,000 ≤ cap) yet
			// must be demoted by the serialized request check
			// (30,000,000 base64 chars + JSON > cap). A decoded-arithmetic
			// implementation would send it native and fail here.
			data := pdf1968PDFOfSize(t, decodedPDF)
			pdfs, bodies, err := run(t, s.shape, t.TempDir(), map[string]any{
				"max_file_bytes":    fileCapSame,
				"max_request_bytes": reqCapSame,
			}, [][]byte{data})
			require.NoError(t, err)
			require.Len(t, bodies, 1)
			body := bodies[0]
			assert.Empty(t, pdfs,
				"the file admits it but the serialized request does not — demote by the REQUEST check")
			pdf1968AssertL2(t, "request bytes over (discriminator)", body, "max_request_bytes",
				[]string{"fx/req-bytes", "fx/req-model"}, reqCapSame)
			// The complete L2 assertion above accepts semantic request-byte wording,
			// not only an internal snake-case identifier.
		})

		t.Run("all-text still over is sent and the 413 is not a PDF failure "+s.name, func(t *testing.T) {
			// FR-12: a cap small enough that even the all-text body exceeds
			// it sends anyway; the 413 that comes back fails the turn as
			// today WITHOUT the PDF recovery (the attempt carried no native
			// PDF, so FR-16 has nothing to recover).
			filler = strings.Repeat("x", 8192)
			defer func() { filler = "" }()
			data := pdf1968PDFOfSize(t, 20000000)
			pdfs, bodies, err := run(t, s.shape, t.TempDir(),
				map[string]any{"max_request_bytes": 4096}, [][]byte{data},
				pdf1968Step{status: 413, errBody: "request entity too large", shape: s.shape})
			require.Error(t, err, "the 413 on the all-text request fails the turn as today")
			require.Len(t, bodies, 1,
				"the all-text body still sends — exactly one request, no recovery round")
			body := bodies[0]
			assert.Greater(t, len(body), 4096, "premise: even the all-text serialized body is still over the cap")
			assert.Empty(t, pdfs, "every PDF was demoted before sending")
			assert.False(t, pdf1968HasPDF(t, body), "no native bytes may travel")
			assert.Contains(t, body, "PAGE 1 text", "the demoted text still travels")
			assert.NotContains(t, body, pdf1968P1Sentence,
				"the 413 on a text-only attempt is not a PDF failure — no P1")
		})

		t.Run("multi-PDF demotion re-measures "+s.name, func(t *testing.T) {
			// FR-12/N4: two admitted PDFs over the cap demote OLDEST first,
			// and the demotion loop re-measures the serialized body after
			// each demotion — one PDF stays native and the re-measured body
			// fits the cap. A demote-everything defect (pdfs empty) or a
			// send-without-remeasure defect (body over cap) fails here.
			base := t.TempDir()
			oldest := pdf1968PDFOfSize(t, 20000000)
			newest := pdf1968PDFOfSize(t, 20000001)
			require.False(t, bytes.Equal(oldest, newest), "N12 premise: candidate identities differ")

			// Measure the one-PDF body; the cap is that length plus slack
			// that fits the demoted text rendering but nowhere near a
			// second encoded PDF (≈26.7 MB base64).
			onePDFs, oneBodies, err := run(t, s.shape, filepath.Join(base, "m1"), nil, [][]byte{newest})
			require.NoError(t, err)
			require.Len(t, oneBodies, 1)
			require.Len(t, onePDFs, 1, "premise: native bytes, not text, determine the budget")
			require.True(t, bytes.Equal(onePDFs[0], newest))
			measured := len(oneBodies[0])
			budget := measured + 500000

			pdfs, bodies, err := run(t, s.shape, filepath.Join(base, "m2"),
				map[string]any{"max_request_bytes": budget}, [][]byte{oldest, newest})
			require.NoError(t, err)
			require.Len(t, bodies, 1, "the re-measured request sends — no recovery round")
			require.Len(t, pdfs, 1,
				"demoting the oldest PDF frees enough room; exactly one stays native")
			require.NoError(t, pdf1968NewestPDFError(pdfs, newest), "N12: newest survives byte-exact; newest-first demotion must fail")
			assert.LessOrEqual(t, len(bodies[0]), budget,
				"the request re-measures under the cap after the demotion before sending")
			assert.Contains(t, bodies[0], "PAGE 1 text", "the demoted PDF's text travels")
			pdf1968AssertL2(t, "request bytes over (two PDFs)", bodies[0], "max_request_bytes",
				[]string{"fx/req-bytes", "fx/req-model"}, budget)

			// N12: one demotion is insufficient for three encoded 20 MB
			// candidates. Repeated complete-body measurement must keep only
			// the newest, not stop after one or demote all three.
			middle := pdf1968PDFOfSize(t, 20000002)
			pdfs, bodies, err = run(t, s.shape, filepath.Join(base, "m3"),
				map[string]any{"max_request_bytes": budget}, [][]byte{oldest, middle, newest})
			require.NoError(t, err)
			require.Len(t, bodies, 1)
			require.Len(t, pdfs, 1, "three PDFs require at least two demotions")
			require.NoError(t, pdf1968NewestPDFError(pdfs, newest), "both older identities must be demoted")
			assert.LessOrEqual(t, len(bodies[0]), budget)
		})

		t.Run("30 MB full body with non-PDF filler and plus one "+s.name, func(t *testing.T) {
			const ceiling = 30000000 // §6.1 encoded-vs-decoded scenario
			base := t.TempDir()
			data := pdf1968PDFOfSize(t, 20000000)
			pdfs, bodies, err := run(t, s.shape, filepath.Join(base, "f0"), nil, [][]byte{data})
			require.NoError(t, err)
			require.Len(t, bodies, 1)
			require.Len(t, pdfs, 1, "premise: measure a real native request")
			require.True(t, bytes.Equal(pdfs[0], data))
			require.Less(t, len(bodies[0]), ceiling)
			filler = strings.Repeat("x", ceiling-len(bodies[0]))
			defer func() { filler = "" }()
			limits := map[string]any{"max_file_bytes": 20000000, "max_request_bytes": ceiling}
			pdfs, bodies, err = run(t, s.shape, filepath.Join(base, "f1"), limits, [][]byte{data})
			require.NoError(t, err)
			require.Len(t, bodies, 1)
			require.Len(t, pdfs, 1, "exactly 30,000,000 serialized bytes must stay native")
			require.True(t, bytes.Equal(pdfs[0], data))
			require.Len(t, bodies[0], ceiling, "measure the ENTIRE emitted body, including non-PDF text and JSON")
			filler += "x"
			pdfs, bodies, err = run(t, s.shape, filepath.Join(base, "f2"), limits, [][]byte{data})
			require.NoError(t, err)
			require.Len(t, bodies, 1)
			require.Empty(t, pdfs, "one extra non-PDF byte requires demotion, even though decoded PDF bytes still fit")
			pdf1968AssertL2(t, "30 MB +1 body", bodies[0], "max_request_bytes", []string{"fx/req-bytes", "fx/req-model"}, ceiling)
			assert.LessOrEqual(t, len(bodies[0]), ceiling)
		})
	}
}

// ---------------------------------------------------------------------------
// T13 (A) — a scanned (no extractable text) PDF still goes native on a
// flagged pair, and on an unflagged pair the model is told N1 + N2 (§5.6
// fixed content). FR-13.
// ---------------------------------------------------------------------------

func TestAttachPDF_ScannedPDF(t *testing.T) {
	data := pdf1968NoTextPDF(t, 1, true)

	t.Run("scanned pdf goes native on a flagged pair", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968CapableRow("fx/doc-reader-9", "fx/tiny-haiku-pdf", nil),
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/tiny-haiku-pdf"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "scan.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.NotEmpty(t, bodies)
		assert.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1,
			"absence of extractable text must not block the native send on a flagged pair")
	})

	t.Run("scanned pdf on an unflagged pair shows N1 and N2", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{content: "ok"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968TextOnlyRow("fx/plain-gateway", "fx/shared-model", nil),
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/plain-gateway", model: "fx/shared-model"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "scan.pdf", data)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.NotEmpty(t, bodies)
		assert.False(t, pdf1968HasPDF(t, bodies[0]))
		assert.Contains(t, bodies[0], pdf1968N1Sentence)
		assert.Contains(t, bodies[0], pdf1968N2Sentence)
	})
}

// ---------------------------------------------------------------------------
// T14 — chat retention stays RETAINED (F6): the chat attachment rides turn 1
// natively (premise), and chat PDFs in history are RE-SENT every later turn —
// §4.4 line 198: "Chat PDFs in history are re-sent every turn". The cost of
// native replay is a documented cost, not a defect; R1's "later turns see
// this text" wording concerns the persisted notice the user reads, not the
// wire form. The TOOL-PDF non-retention sweep — history, transcript,
// archive, checkpoint, reopened store (FR-14, §6 row T14) — lives in
// chat_pdf_tool_item_buildfail_test.go::TestToolPDF_NotRetained.
// ---------------------------------------------------------------------------

func TestChatPDF_HistoryReplayResendsNative(t *testing.T) {
	data := pdf1968TextPDF(t, 1, true)
	peer := newPdf1968Peer(t, pdf1968Step{content: "first"}, pdf1968Step{content: "second"})
	al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
	sid := pdf1968NewSession(t, store, "pdf1968-agent")
	ref := pdf1968StorePDF(t, mediaStore, "report.pdf", data)

	_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
	require.NoError(t, err)
	bodies := peer.Bodies()
	require.NotEmpty(t, bodies)
	require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "premise: turn 1 attaches the chat PDF natively")

	// §4.4 line 198: the next request replays the SAME native bytes from
	// history — byte-exact, not a re-encoded copy, and not dropped.
	_, err = pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
	require.NoError(t, err)
	bodies = peer.Bodies()
	require.Len(t, bodies, 2)
	replayed := pdf1968BodyPDFs(t, bodies[1])
	require.Len(t, replayed, 1,
		"a chat PDF in history is re-sent natively on the next turn (§4.4 line 198)")
	assert.True(t, bytes.Equal(replayed[0], data),
		"the replayed history PDF is the same document, byte-exact")
}

// ---------------------------------------------------------------------------
// T15 (A) — recovery attribution matrix (FR-16/FR-17): which error classes
// trigger the one text retry for a native-PDF attempt, and which do not.
// Arms with a native-PDF premise are red today (no native attach); the
// PDF-free negative arms pin today's correct behaviour and must survive.
// ---------------------------------------------------------------------------

func TestPDFRecovery_AttributionMatrix(t *testing.T) {
	pdfRejectBody := `{"error":{"message":"pdf input not supported for this model","type":"invalid_request_error"}}`
	imageRejectBody := `{"error":{"message":"unsupported image format","type":"invalid_request_error"}}`
	pngBytes := pdf1968TinyPNG(t)
	pdfData := pdf1968TextPDF(t, 1, true)

	// everyAttemptNative asserts on a never-demotes arm: the FIRST attempt
	// carried the native PDF, every recorded attempt keeps it, and no P1
	// appears anywhere.
	everyAttemptNative := func(t *testing.T, bodies []string) {
		t.Helper()
		require.NotEmpty(t, bodies)
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "premise: native PDF on the first attempt")
		for i, b := range bodies {
			assert.Len(t, pdf1968BodyPDFs(t, b), 1,
				"attempt %d: this failure class retries in place with the native PDF — never demotes (FR-16)", i+1)
			assert.NotContains(t, b, pdf1968P1Sentence)
		}
	}

	t.Run("pdf attempt + transient 503 never demotes", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{status: 503, errBody: "upstream overloaded"})
		al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.Error(t, err, "a transient failure must not be answered by a text demotion")
		everyAttemptNative(t, peer.Bodies())
	})

	t.Run("chain A native+503 then B text+413 recovers nothing", func(t *testing.T) {
		// F8/T15: candidate A (flagged) sends the PDF and fails 503; the
		// chain switches to candidate B (unflagged) whose own request
		// carries text and fails 413 with an image-shaped body. Recovery is
		// attributed PER ATTEMPT: A's failure is transient (no demotion) and
		// B's attempt sent no PDF — so nothing recovers and the turn fails.
		primary := pdf1968Pair{provider: "fx/doc-reader-9", model: "fx/chain-model"}
		fallback := pdf1968Pair{provider: "fx/plain-gateway", model: "fx/chain-model"}
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 503, errBody: "upstream overloaded"},
			pdf1968Step{status: 413, errBody: "image attachment too large"})
		al, agents, store, mediaStore := pdf1968ChainHarness(t, peer,
			pdf1968CapableRow(primary.provider, primary.model, nil),
			pdf1968TextOnlyRow(fallback.provider, fallback.model, nil),
			primary, fallback)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.Error(t, err, "a 413 on the text-only candidate fails the turn; no chain left")
		bodies := peer.Bodies()
		require.Len(t, bodies, 2, "exactly one attempt per candidate")
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1,
			"attempt A carries the native PDF")
		assert.False(t, pdf1968HasPDF(t, bodies[1]),
			"attempt B's own row decision is text; the PDF is not forwarded natively")
		assert.Contains(t, bodies[1], "PAGE 1 text",
			"attempt B sees the extracted text")
		for i, b := range bodies {
			assert.NotContains(t, b, pdf1968P1Sentence,
				"attempt %d: no demotion may fire (503 transient; 413 on a text attempt)", i+1)
		}
	})

	t.Run("text-only request + 413 does not demote anything", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{status: 413, errBody: "request entity too large"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968TextOnlyRow("fx/plain-gateway", "fx/shared-model", nil),
		})
		al, agents, store, _ := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/plain-gateway", model: "fx/shared-model"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.Error(t, err, "a 413 on a PDF-free request must fail the turn")
		for _, b := range peer.Bodies() {
			assert.NotContains(t, b, pdf1968P1Sentence,
				"the PDF recovery must not fire when no PDF was attached")
		}
	})

	t.Run("image rejection keeps the image recovery untouched", func(t *testing.T) {
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 400, errBody: imageRejectBody},
			pdf1968Step{content: "seen"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968VisionRow("fx/vision-gate", "fx/vision-model"),
		})
		al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/vision-gate", model: "fx/vision-model"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePNG(t, mediaStore, "snap.png", pngBytes)

		answer, runErr := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, runErr, "the image recovery must resolve the turn (synthesis or strip-retry)")
		assert.NotEmpty(t, answer)
		for _, b := range peer.Bodies() {
			assert.NotContains(t, b, pdf1968P1Sentence,
				"the PDF recovery path must stay out of an image-only rejection")
		}
	})

	t.Run("pdf attempt + 400 media-unsupported recovers to text once", func(t *testing.T) {
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 400, errBody: pdfRejectBody},
			pdf1968Step{content: "recovered"})
		al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		assert.Equal(t, "recovered", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2, "exactly one text retry (FR-16/FR-17)")
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "premise: native PDF on the first attempt")
		assert.False(t, pdf1968HasPDF(t, bodies[1]))
		assert.Contains(t, bodies[1], "PAGE 1 text", "the retry carries the extracted text")
		assert.Contains(t, bodies[1], pdf1968P1Sentence)
	})

	t.Run("pdf attempt + 413 recovers to text once", func(t *testing.T) {
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 413, errBody: "request entity too large"},
			pdf1968Step{content: "recovered"})
		al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		assert.Equal(t, "recovered", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2, "exactly one text retry (FR-16/FR-17)")
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "premise: native PDF on the first attempt")
		assert.False(t, pdf1968HasPDF(t, bodies[1]))
		assert.Contains(t, bodies[1], "PAGE 1 text", "the retry carries the extracted text")
		assert.Contains(t, bodies[1], pdf1968P1Sentence)
	})

	t.Run("context overflow with a pdf recovers; without one it compacts", func(t *testing.T) {
		// With a native PDF: context overflow qualifies for the text retry.
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 400, errBody: "context window exceeded"},
			pdf1968Step{content: "recovered"})
		al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err)
		assert.Equal(t, "recovered", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 2, "exactly one text retry (FR-16/FR-17)")
		require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "premise: native PDF on the first attempt")
		assert.False(t, pdf1968HasPDF(t, bodies[1]))
		assert.Contains(t, bodies[1], "PAGE 1 text", "the retry carries the extracted text")
		assert.Contains(t, bodies[1], pdf1968P1Sentence)

		pdf1968AssertNoPDFCompaction(t)
	})

	t.Run("context overflow without PDF or evictable history fails without recovery", func(t *testing.T) {
		// FR-16/FR-17: no native PDF means no PDF retry; existing compaction
		// cannot shorten a session consisting of only the current user message.
		peer := newPdf1968Peer(t, pdf1968Step{status: 400, errBody: "context window exceeded"})
		cat := pdf1968Catalog(t, []pdf1968ProviderRow{
			pdf1968TextOnlyRow("fx/plain-gateway", "fx/shared-model", nil),
		})
		al, agents, store, _ := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
			pdf1968Pair{provider: "fx/plain-gateway", model: "fx/shared-model"})
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid)
		require.ErrorContains(t, err, "context window exceeded")
		bodies := peer.Bodies()
		require.Len(t, bodies, 1, "no progress means no unchanged resend and no PDF recovery")
		assert.False(t, pdf1968HasPDF(t, bodies[0]))
		assert.NotContains(t, bodies[0], pdf1968P1Sentence)
	})

	// FR-16 exclusions (F8): auth, content policy, wrong-model and
	// rate-limit failures NEVER trigger the demotion — each fails or
	// retries in place with the native PDF attached.
	t.Run("auth 401 never demotes", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{status: 401, errBody: "invalid api key"})
		al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.Error(t, err, "an auth failure with no fallback candidate must fail the turn")
		everyAttemptNative(t, peer.Bodies())
	})

	t.Run("content policy 400 never demotes", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{
			status:  400,
			errBody: `{"error":{"message":"content policy violation","type":"invalid_request_error"}}`,
		})
		al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.Error(t, err, "a content-policy rejection is a format failure, not a PDF failure")
		everyAttemptNative(t, peer.Bodies())
	})

	t.Run("wrong model 400 model_not_found never demotes", func(t *testing.T) {
		peer := newPdf1968Peer(t, pdf1968Step{
			status: 400,
			errBody: `{"error":{"message":"model not found","code":"model_not_found",` +
				`"type":"invalid_request_error"}}`,
		})
		al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.Error(t, err, "a model_not_found failure is not a PDF failure")
		everyAttemptNative(t, peer.Bodies())
	})

	t.Run("rate limit 429 retries in place with the PDF and succeeds", func(t *testing.T) {
		// F8: a 429 with Retry-After retries IN PLACE (the header replaces
		// the long backoff steps) — the PDF stays attached on every attempt
		// and the turn eventually succeeds with no demotion.
		peer := newPdf1968Peer(t,
			pdf1968Step{status: 429, errBody: "rate limited", headers: map[string]string{"Retry-After": "1"}},
			pdf1968Step{status: 429, errBody: "rate limited", headers: map[string]string{"Retry-After": "1"}},
			pdf1968Step{content: "ok"})
		al, agents, store, mediaStore := pdf1968FlaggedHarness(t, peer)
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdfData)

		answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
		require.NoError(t, err, "the in-place rate-limit retry must eventually answer")
		assert.Equal(t, "ok", answer)
		bodies := peer.Bodies()
		require.Len(t, bodies, 3,
			"both rate-limit responses retried in place, then one success — exactly 3 attempts")
		everyAttemptNative(t, bodies)
	})
}

// pdf1968AssertNoPDFCompaction keeps the no-PDF half of T15 observable on
// the wire without making the attribution matrix exceed the function budget.
func pdf1968AssertNoPDFCompaction(t *testing.T) {
	t.Helper()
	// FR-17/T15 preserves existing compaction, not an unconditional retry.
	// Two completed turns give it an older evictable turn while retaining
	// the newest assistant as the history floor. Use a substantial answer:
	// replacing two tiny messages with archive breadcrumbs need not shrink
	// the request. No PDF recovery may fire.
	const oldest = "oldest completed turn eligible for eviction"
	const newest = "newest completed turn must remain"
	const current = "current request must survive compaction"
	oldAnswer := strings.Repeat("discardable earlier answer ", 256)
	peer2 := newPdf1968Peer(t,
		pdf1968Step{content: oldAnswer},
		pdf1968Step{content: "newest answer"},
		pdf1968Step{status: 400, errBody: "context window exceeded"},
		pdf1968Step{content: "ok"})
	cat2 := pdf1968Catalog(t, []pdf1968ProviderRow{
		pdf1968TextOnlyRow("fx/plain-gateway", "fx/shared-model", nil),
	})
	al2, agents2, store2, _ := pdf1968NewPeerHarness(t, peer2, cat2, cat2, nil,
		pdf1968Pair{provider: "fx/plain-gateway", model: "fx/shared-model"})
	sid2 := pdf1968NewSession(t, store2, "pdf1968-agent")
	_, err2 := pdf1968RunTurnMsg(t, al2, agents2["pdf1968-agent"], store2, sid2, oldest)
	require.NoError(t, err2)
	_, err2 = pdf1968RunTurnMsg(t, al2, agents2["pdf1968-agent"], store2, sid2, newest)
	require.NoError(t, err2)
	answer2, err2 := pdf1968RunTurnMsg(t, al2, agents2["pdf1968-agent"], store2, sid2, current)
	require.NoError(t, err2)
	assert.Equal(t, "ok", answer2)
	textBodies := peer2.Bodies()
	require.Len(t, textBodies, 4, "two seed turns, rejected request, one compacted retry")
	require.Contains(t, textBodies[2], oldest, "premise: the rejected request has an older complete turn")
	require.Contains(t, textBodies[2], oldAnswer, "premise: full older answer reached the rejected request")
	assert.NotContains(t, textBodies[3], oldAnswer, "the full evicted answer must leave the request")
	assert.Less(t, len(textBodies[3]), len(textBodies[2]), "compaction must actually shrink the retry")
	// Existing sliding-window FR-006 permits archive snippets in system;
	// prompt-caching US-3.1 keeps a volatile clock note after history. Assert
	// the exact retained suffix, including that note, not whole-body absence
	// of a snippet which the breadcrumb is required to retain.
	var requests [2][]string
	for i, raw := range []string{textBodies[2], textBodies[3]} {
		var wire struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &wire))
		for _, msg := range wire.Messages {
			if msg.Role != "system" {
				requests[i] = append(requests[i], msg.Role+":"+msg.Content)
			}
		}
	}
	require.Len(t, requests[0], 6, "premise: two complete turns, current user, volatile note")
	require.Equal(t, []string{"user:" + oldest, "assistant:" + oldAnswer, "user:" + newest, "assistant:newest answer", "user:" + current}, requests[0][:5])
	require.True(t, strings.HasPrefix(requests[0][5], "user:## Current Time\n"), "the final request-only note is the clock")
	assert.Equal(t, requests[0][2:], requests[1],
		"only the oldest turn leaves live history; preserve the newest complete turn, current user and volatile note byte-exact")
	for _, b := range textBodies {
		assert.False(t, pdf1968HasPDF(t, b))
		assert.NotContains(t, b, pdf1968P1Sentence)
	}
}

// ---------------------------------------------------------------------------
// T17 (G — a guard that passes on the base tree, §6 row T17:
// TestChatPDF_RejectRetry_Unchanged) — the EXISTING chat recovery, unchanged.
// The base tree already recovers a rejected native chat PDF: the shared
// media classifier (translate_error.go) fires and media_downgrade.go's
// TryMediaDowngrade downgrades the PDF to extracted text for one retry
// (loop_media.go::downgradePDFMediaToText). The fixture pair uses the model
// id `claude-sonnet-4` so the BASE tree attaches natively too (its
// pdfCapableModel allow-list names that model AND the catalogue row is
// flagged) — the premise below holds before the landing as well. This test
// therefore asserts only the stable recovery shape (success, exactly one
// text retry, retry carries no native bytes and does carry the extracted
// text) and deliberately NOT P1: P1 is new §5.6 content whose presence is
// T10b's and T15's job (A tests); asserting it here would break the guard
// on base.
// ---------------------------------------------------------------------------

func TestChatPDF_RejectRetry_Unchanged(t *testing.T) {
	pdfRejectBody := `{"error":{"message":"pdf input not supported for this model","type":"invalid_request_error"}}`
	peer := newPdf1968Peer(t,
		pdf1968Step{status: 400, errBody: pdfRejectBody},
		pdf1968Step{content: "recovered"})
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{
		pdf1968CapableRow("fx/doc-reader-9", "claude-sonnet-4", nil),
	})
	al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, nil,
		pdf1968Pair{provider: "fx/doc-reader-9", model: "claude-sonnet-4"})
	sid := pdf1968NewSession(t, store, "pdf1968-agent")
	ref := pdf1968StorePDF(t, mediaStore, "report.pdf", pdf1968TextPDF(t, 1, true))

	answer, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, ref)
	require.NoError(t, err, "the existing media-downgrade recovery must resolve the turn")
	assert.Equal(t, "recovered", answer)
	bodies := peer.Bodies()
	require.Len(t, bodies, 2, "exactly one text retry after the rejection (base and landed alike)")
	require.Len(t, pdf1968BodyPDFs(t, bodies[0]), 1, "premise: the first attempt carried the native PDF")
	assert.False(t, pdf1968HasPDF(t, bodies[1]), "the retry carries text, not the PDF")
	assert.Contains(t, bodies[1], "PAGE 1 text", "the retry carries the extracted text")
}
