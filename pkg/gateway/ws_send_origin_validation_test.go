package gateway

// ws_send_origin_validation_test.go — SV-12 (r5: full M dataset incl.
// M-8b/M-8c and the media-bearing controls; #1428 step 2 option C, qa-lead):
// every row M-1..M-12 of spec §4.3.2 driven against BOTH validation modes
// (gateway.validate_inbound true and false, one server per mode) with the
// content variants the dataset states, requiring byte-identical replies
// across modes and the per-row reply shapes.
//
// SPEC SOURCES (expected values derive from these): §4.3.2 (the M-1..M-12
// dataset and the content-variants paragraph), §4.3.4 (old-tab rows M-8/
// M-8c), §6.1 SV-12. The byte-identity oracle masks only VOLATILE values —
// timestamps, per-session seq numbers, minted session ids, uuid entry ids
// and the synthetic old-tab id suffix — never a reply's structure, reason
// string or echo shape; those are asserted by each row's own oracle on BOTH
// servers, so a mode that answers with today's processed-frames path fails
// its row oracle even where the masked bytes happen to match.
//
// RED STATE: there is no send-origin pre-check (F-h) and the two modes
// already answer malformed frames differently (validate_inbound gates the
// JSON-schema pass today), so rows fail on their oracle: the refusal is
// missing and the frame is processed (or schema-error'd) instead. M-8c is a
// control for M-8b but its not_saved first-send reply is NEW behaviour
// (P2b), so it is RED today as well (the old tab mints a chat instead).
// Content variants for redirect/cancel rows: those frames carry no content
// field, so the "runs twice" rule degenerates to one variant per shape.
//
// The generated-browser-Zod parse of each recorded send_refused (spec:
// "fixture file checked by vitest, owner test author") belongs to the
// frontend RED step 0 pack; the Go side pins the bytes these fixtures are
// recorded from by asserting every send_refused decodes into the generated
// Go SendRefusedFrame with its contract fields set.

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

const soRefusalWindow = 1300 * time.Millisecond

// Volatile-value masks for the cross-mode byte comparison (see header).
var (
	soMaskTimestamp = regexp.MustCompile(`"timestamp":"[^"]*"`)
	soMaskSeq       = regexp.MustCompile(`"seq":\d+`)
	soMaskSessionID = regexp.MustCompile(`session_[0-9A-Za-z]{10,}`)
	soMaskUUID      = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	soMaskOutdated  = regexp.MustCompile(`outdated-[0-9A-Za-z_-]{12}`)
)

func soNormalize(raw []byte) string {
	s := string(raw)
	s = soMaskTimestamp.ReplaceAllString(s, `"timestamp":"MASKED"`)
	s = soMaskSeq.ReplaceAllString(s, `"seq":0`)
	s = soMaskSessionID.ReplaceAllString(s, `session_MASKED`)
	s = soMaskUUID.ReplaceAllString(s, `uuid-MASKED`)
	s = soMaskOutdated.ReplaceAllString(s, `outdated-MASKED`)
	return s
}

func soValidJSON(s string) string { return `"` + s + `" ` }

// soRawMsg builds a raw message frame; empty sessionID/origin/cmid strings
// omit the key, so null vs number vs absent stay exactly as written.
func soRawMsg(sessionID, origin, clientMessageID, content string, media ...string) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"message","agent_id":"mia"`)
	if sessionID != "" {
		b.WriteString(`,"session_id":` + strings.TrimSpace(sessionID))
	}
	if origin != "" {
		b.WriteString(`,"origin":` + strings.TrimSpace(origin))
	}
	if clientMessageID != "" {
		b.WriteString(`,"client_message_id":` + strings.TrimSpace(clientMessageID))
	}
	b.WriteString(`,"content":` + strconv.Quote(content))
	if len(media) > 0 {
		b.WriteString(`,"media":[`)
		for i, m := range media {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(strconv.Quote(m))
		}
		b.WriteString(`]`)
	}
	b.WriteString(`}`)
	return []byte(b.String())
}

func soRawRedirect(sessionID, origin, clientFrameID string) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"redirect","instruction":"sv12 redirect"`)
	if sessionID != "" {
		b.WriteString(`,"session_id":` + strings.TrimSpace(sessionID))
	}
	if origin != "" {
		b.WriteString(`,"origin":` + strings.TrimSpace(origin))
	}
	if clientFrameID != "" {
		b.WriteString(`,"client_frame_id":` + strings.TrimSpace(clientFrameID))
	}
	b.WriteString(`}`)
	return []byte(b.String())
}

func soRawCancel(sessionID, origin, clientFrameID string) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"cancel"`)
	if sessionID != "" {
		b.WriteString(`,"session_id":` + strings.TrimSpace(sessionID))
	}
	if origin != "" {
		b.WriteString(`,"origin":` + strings.TrimSpace(origin))
	}
	if clientFrameID != "" {
		b.WriteString(`,"client_frame_id":` + strings.TrimSpace(clientFrameID))
	}
	b.WriteString(`}`)
	return []byte(b.String())
}

func soOriginChat(sid string) string { return `{"kind":"chat","session_id":"` + sid + `"}` }
func soOriginNull() string           { return `null` }
func soKeyJSON(k string) string      { return `{"kind":"new_chat","new_chat_key":"` + k + `"}` }
func soRowKey(tag string, n int) string {
	k := "K-sv12-" + tag + "-" + strconv.Itoa(n)
	for len(k) < 16 {
		k += "x"
	}
	return k
}

// soUploadMediaRefAPI performs a REAL POST /upload (the production handler
// the mux registers) into the given workspace and returns the media:// ref.
func soUploadMediaRefAPI(t *testing.T, al *agent.AgentLoop, home, workspaceID string) string {
	t.Helper()
	api := &restAPI{agentLoop: al, allowedOrigin: "http://localhost:3000", homePath: home}
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("file", "sv12-media.txt")
	require.NoError(t, err)
	_, err = fw.Write([]byte("sv12 media control payload"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload?workspace_id="+workspaceID, body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()
	api.HandleUpload(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "SETUP: real POST /upload must succeed; body: %s", rr.Body.String())
	var resp generated.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1, "SETUP: the upload must return exactly one file")
	require.NotNil(t, resp.Files[0].Ref, "SETUP: the upload must return a media:// ref")
	require.True(t, strings.HasPrefix(*resp.Files[0].Ref, "media://workspace/"+workspaceID+"/"),
		"SETUP: the ref must be workspace-scoped, got %q", *resp.Files[0].Ref)
	return *resp.Files[0].Ref
}

// soRowFrames is one dataset row: the frames it sends (both content
// variants, in order) for a target id, the per-server reply oracle, and
// whether the masked reply bytes must match across modes.
type soRowFrames struct {
	name    string
	unbound bool // the row targets no chat: run on a fresh, never-bound browser conn
	cli     bool // M-11 runs on a programmatic CLI-token connection
	build   func(sid string) [][]byte
	check   func(t *testing.T, s *sendOriginSrv, frames []soFrame, sid string, before int)
	bytes   bool
}

// soRunRow drives one row on one server: fresh conn, keyed mint (bound) or
// unbound/CLI per the row, every frame sent with its own reply window.
func soRunRow(t *testing.T, s *sendOriginSrv, row soRowFrames, n int) (frames []soFrame, sid string, before int) {
	t.Helper()
	var conn *websocket.Conn
	switch {
	case row.cli:
		conn = s.dialCLIConn(t)
		drainAdmissionOneShot(t, conn)
		// M-11 setup: mint the target chat through today's legacy path
		// (no origin, non-browser), then send the row to it.
		soSend(t, conn, soRawMsg("", "", "", "sv12 legacy mint"))
		started := readFrameOfType(t, conn, "session_started", lifecycleEW)
		sid = started.SessionID
		require.NotEmpty(t, sid, "SETUP: the legacy mint must acknowledge")
	case row.unbound:
		conn = s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
	default:
		conn = s.dialBrowserConn(t)
		drainAdmissionOneShot(t, conn)
		sid = s.mintChat(t, conn, soRowKey(row.name, n), "sv12 row setup mint "+row.name)
	}
	if sid != "" {
		before = s.transcriptLen(t, sid)
	}
	for _, raw := range row.build(sid) {
		soSend(t, conn, raw)
		frames = append(frames, soWindow(t, conn, soRefusalWindow)...)
	}
	row.check(t, s, frames, sid, before)
	return frames, sid, before
}

// soRefusalOracle is the oracle shared by the refusal rows that echo the
// same shape: no message_status, no user_message echo, no snapshot.
func soRefusalOracle(t *testing.T, frames []soFrame, phase string) generated.SendRefusedFrame {
	t.Helper()
	refused := soRefusedFrame(t, frames, phase)
	require.NotContains(t, soTypes(frames), "message_status", "%s: no message_status", phase)
	require.NotContains(t, soTypes(frames), "user_message", "%s: no echo", phase)
	require.NotContains(t, soTypes(frames), "session_state", "%s: message refusals get no snapshot", phase)
	return refused
}

// TestCheckSendOriginValidationModes is SV-12: the M dataset, both modes.
func TestCheckSendOriginValidationModes(t *testing.T) {
	sTrue := newSendOriginSrvMode(t, &restMockProvider{}, true)
	sFalse := newSendOriginSrvMode(t, &restMockProvider{}, false)

	// The media-bearing control variants need one real upload per server
	// (§4.3.2: "a real POST /upload in the test setup, into that chat's
	// workspace").
	mediaTrue := soUploadMediaRefAPI(t, sTrue.al, sTrue.home, "sv12-media-ws")
	mediaFalse := soUploadMediaRefAPI(t, sFalse.al, sFalse.home, "sv12-media-ws")

	rows := append(soOriginShapeRows(), soMalformedFieldRows()...)
	rows = append(rows, soOldTabRows()...)

	for i, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			framesTrue, _, _ := soRunRow(t, sTrue, row, i)
			framesFalse, _, _ := soRunRow(t, sFalse, row, i)
			if !row.bytes {
				return
			}
			require.Equal(t, soJoinFrames(framesTrue), soJoinFrames(framesFalse),
				"SV-12: the replies must be byte-identical across validate_inbound modes "+
					"(masked volatile values only); true-mode: %v false-mode: %v",
				soTypes(framesTrue), soTypes(framesFalse))
		})
	}

	soValidationPositiveControls(t, sTrue, sFalse, mediaTrue, mediaFalse)
}

func soOriginShapeRows() []soRowFrames {
	return []soRowFrames{
		{
			// M-1: origin null. Both content variants (§4.3.2).
			name: "M-1-null-origin",
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawMsg(soValidJSON(sid), soOriginNull(), soValidJSON("m1"), ""),
					soRawMsg(soValidJSON(sid), soOriginNull(), soValidJSON("m1"), "hello"),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, sid string, _ int) {
				refused := soRefusalOracle(t, frames, "M-1")
				require.Equal(t, "origin_malformed", refused.Reason)
				require.Equal(t, "message", refused.RefusedFrame)
				require.NotNil(t, refused.ClientMessageId)
				require.Equal(t, "m1", *refused.ClientMessageId, "M-1: the id is echoed")
				require.NotNil(t, refused.SessionId)
				require.Equal(t, sid, *refused.SessionId, "M-1: the target is echoed")
			},
			bytes: true,
		},
		{
			// M-2: the origin-shape variants (the row's empty-content
			// variant runs on its first shape).
			name: "M-2-origin-shapes",
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawMsg(soValidJSON(sid), `{"kind":"room","session_id":"`+sid+`"}`, soValidJSON("m2"), ""),
					soRawMsg(soValidJSON(sid), soOriginNull(), soValidJSON("m2"), "hello"),
					soRawMsg(soValidJSON(sid), `{"kind":"chat","session_id":"`+sid+`","new_chat_key":"`+soKeyA+`"}`, soValidJSON("m2"), "hello"),
					soRawMsg(soValidJSON(sid), `{}`, soValidJSON("m2"), "hello"),
					soRawMsg(soValidJSON(sid), soKeyJSON(strings.Repeat("a", 15)), soValidJSON("m2"), "hello"),
					soRawMsg(soValidJSON(sid), soKeyJSON(strings.Repeat("b", 129)), soValidJSON("m2"), "hello"),
					soRawMsg(soValidJSON(sid), soKeyJSON("K-so-bad-alphabet!"), soValidJSON("m2"), "hello"),
					soRawMsg(soValidJSON(sid), `{"kind":"chat","session_id":"`+sid+`","workspace_id":"w"}`, soValidJSON("m2"), "hello"),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, sid string, _ int) {
				refused := soRefusalOracle(t, frames, "M-2")
				require.Equal(t, "origin_malformed", refused.Reason)
				require.Equal(t, "m2", *refused.ClientMessageId)
				require.NotNil(t, refused.SessionId)
				require.Equal(t, sid, *refused.SessionId)
			},
			bytes: true,
		},
	}
}

func soMalformedFieldRows() []soRowFrames {
	return []soRowFrames{
		{
			// M-3 + M-4: valid matching origin and target, malformed
			// correlation id (number / "" / 129 chars / null). Required by
			// SV-12: no id echoed, no transcript write, no message_status.
			name: "M-3-M-4-bad-message-id",
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawMsg(soValidJSON(sid), soOriginChat(sid), `123`, "hello"),
					soRawMsg(soValidJSON(sid), soOriginChat(sid), soValidJSON(""), ""),
					soRawMsg(soValidJSON(sid), soOriginChat(sid), soValidJSON(strings.Repeat("c", 129)), "hello"),
					soRawMsg(soValidJSON(sid), soOriginChat(sid), `null`, "hello"),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, sid string, before int) {
				refused := soRefusalOracle(t, frames, "M-3/M-4")
				require.Equal(t, "frame_malformed", refused.Reason)
				require.Equal(t, "message", refused.RefusedFrame)
				require.Nil(t, refused.ClientMessageId, "M-3: the invalid id is never echoed")
				require.NotNil(t, refused.SessionId)
				require.Equal(t, sid, *refused.SessionId)
				require.NotNil(t, refused.Origin, "M-3: the valid origin is echoed")
				require.Equal(t, 0, s.transcriptLen(t, sid)-before, "M-3: no transcript write")
			},
			bytes: true,
		},
		{
			// M-5: malformed target, valid id — origin + id echoed, no
			// target echo (the target is not valid, so there is no
			// transcript baseline to compare).
			name: "M-5-bad-target",
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawMsg(`7`, soOriginChat(sid), soValidJSON("m5"), "hello"),
					soRawMsg(soValidJSON(""), soOriginChat(sid), soValidJSON("m5"), ""),
					soRawMsg(`null`, soOriginChat(sid), soValidJSON("m5"), "hello"),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, _ string, _ int) {
				refused := soRefusalOracle(t, frames, "M-5")
				require.Equal(t, "frame_malformed", refused.Reason)
				require.Equal(t, "message", refused.RefusedFrame)
				require.Equal(t, "m5", *refused.ClientMessageId)
				require.Nil(t, refused.SessionId, "M-5: the invalid target is never echoed")
				require.NotNil(t, refused.Origin)
			},
			bytes: true,
		},
		{
			// M-6: redirect/cancel with a malformed client_frame_id.
			// Required by SV-12: no id, no command_received, no snapshot,
			// no redirect/cancel effect, no transcript write.
			name: "M-6-bad-command-id",
			build: func(sid string) [][]byte {
				frames := make([][]byte, 0, 6) // three id shapes × redirect/cancel
				for _, id := range []string{`5`, `null`, soValidJSON("")} {
					frames = append(frames, soRawRedirect(soValidJSON(sid), soOriginChat(sid), id))
					frames = append(frames, soRawCancel(soValidJSON(sid), soOriginChat(sid), id))
				}
				return frames
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, sid string, before int) {
				refused := soRefusedFrame(t, frames, "M-6")
				require.Equal(t, "frame_malformed", refused.Reason)
				require.Nil(t, refused.ClientFrameId, "M-6: the invalid id is never echoed")
				require.NotNil(t, refused.SessionId)
				require.Equal(t, sid, *refused.SessionId)
				require.NotContains(t, soTypes(frames), "command_received", "M-6: no receipt for a malformed id")
				require.NotContains(t, soTypes(frames), "session_state", "M-6: no snapshot (r5)")
				require.NotContains(t, soTypes(frames), "cancel_stage", "M-6: no cancel effect")
				require.Equal(t, 0, s.transcriptLen(t, sid)-before, "M-6: no effect on the chat")
			},
			bytes: true,
		},
		{
			// M-7: redirect/cancel with a MISSING target and a valid id.
			name: "M-7-missing-command-target",
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawRedirect("", soOriginChat(sid), soValidJSON("r1")),
					soRawCancel("", soOriginChat(sid), soValidJSON("r1")),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, _ string, _ int) {
				refused := soRefusedFrame(t, frames, "M-7")
				require.Equal(t, "frame_malformed", refused.Reason)
				require.Equal(t, "redirect", refused.RefusedFrame)
				require.Equal(t, "r1", *refused.ClientFrameId)
				require.Nil(t, refused.SessionId, "M-7: no valid target to echo")
				require.NotNil(t, refused.Origin)
				require.NotContains(t, soTypes(frames), "command_received", "M-7: no receipt")
				require.NotContains(t, soTypes(frames), "session_state", "M-7: no snapshot (r5)")
			},
			bytes: true,
		},
	}
}

func soOldTabRows() []soRowFrames {
	return []soRowFrames{
		{
			// M-8: origin absent, browser, valid target, numeric id — the
			// old-tab "message, valid target, no valid id" row of 4.3.4.
			name: "M-8-old-tab-valid-target",
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawMsg(soValidJSON(sid), "", `123`, "hello"),
					soRawMsg(soValidJSON(sid), "", `123`, ""),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, sid string, _ int) {
				errs := soErrors(t, frames)
				require.NotEmpty(t, errs, "M-8: the old-tab notice must arrive (window: %v)", soTypes(frames))
				var ef generated.ErrorFrame
				require.NoError(t, json.Unmarshal(frames[soFirstError(t, frames)].Raw, &ef),
					"M-8: the old-tab error parses with the generated ErrorFrame")
				require.NotNil(t, ef.SessionId)
				require.Equal(t, sid, *ef.SessionId, "M-8: the valid target is named")
				require.NotNil(t, ef.ClientMessageId)
				require.True(t, strings.HasPrefix(*ef.ClientMessageId, "outdated-") && len(*ef.ClientMessageId) == len("outdated-")+12,
					"M-8: synthetic id is \"outdated-\" + 12 chars, got %q", *ef.ClientMessageId)
				require.Equal(t, soOutdated, ef.Message, "M-8: the OUTDATED text is exact")
				require.NotContains(t, soTypes(frames), "message_status", "M-8: no message_status — there is no id to reconcile")
				require.NotContains(t, soTypes(frames), "session_started", "M-8: nothing is minted")
			},
			bytes: true,
		},
		{
			// M-8b: origin absent, browser, target PRESENT BUT MALFORMED,
			// valid id — NO reply at all, never the not_saved row.
			name:    "M-8b-malformed-present-target",
			unbound: true,
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawMsg(`7`, "", soValidJSON("m8b"), "hello"),
					soRawMsg(soValidJSON(""), "", soValidJSON("m8b"), ""),
					soRawMsg(`null`, "", soValidJSON("m8b"), "hello"),
					soRawMsg(soValidJSON(strings.Repeat("z", 256)), "", soValidJSON("m8b"), "hello"),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, _ string, _ int) {
				require.Empty(t, frames, "M-8b: zero frames (got %v)", soTypes(frames))
				for _, f := range frames {
					require.NotContains(t, string(f.Raw), "first_message_error",
						"M-8b: no first_message_error reply may be sent")
				}
			},
			bytes: true,
		},
		{
			// M-8c — the CONTROL for M-8b: origin absent, target ABSENT,
			// valid id → exactly the not_saved first-message reply.
			name:    "M-8c-old-tab-first-send",
			unbound: true,
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawMsg("", "", soValidJSON("m8c"), "hello"),
					soRawMsg("", "", soValidJSON("m8c"), ""),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, _ string, _ int) {
				errs := soErrors(t, frames)
				require.NotEmpty(t, errs, "M-8c: the not_saved reply must arrive (window: %v)", soTypes(frames))
				var ef generated.ErrorFrame
				require.NoError(t, json.Unmarshal(frames[soFirstError(t, frames)].Raw, &ef))
				require.NotNil(t, ef.FirstMessageError, "M-8c: first_message_error must be set")
				require.Equal(t, "not_saved", *ef.FirstMessageError)
				require.Equal(t, "m8c", *ef.ClientMessageId)
				require.Equal(t, soOutdated, ef.Message)
				require.Nil(t, ef.SessionId, "M-8c: the first-message reply carries no session_id")
				require.NotContains(t, soTypes(frames), "session_started",
					"M-8c: the refused old-tab first message must NOT mint a chat")
			},
			bytes: true,
		},
		{
			// M-9: origin absent, target absent-or-malformed, id
			// absent-or-malformed — silence.
			name:    "M-9-unroutable-message",
			unbound: true,
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawMsg("", "", "", "hello"),
					soRawMsg("", "", `123`, "hello"),
					soRawMsg(`7`, "", "", "hello"),
					soRawMsg(`7`, "", `123`, "hello"),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, _ string, _ int) {
				require.Empty(t, frames, "M-9: zero frames (got %v)", soTypes(frames))
			},
			bytes: true,
		},
		{
			// M-10: redirect/cancel with origin absent and target
			// malformed-or-absent — silence.
			name:    "M-10-unroutable-commands",
			unbound: true,
			build: func(sid string) [][]byte {
				return [][]byte{
					soRawRedirect("", "", soValidJSON("r10")),
					soRawCancel("", "", soValidJSON("r10")),
					soRawRedirect(`7`, "", soValidJSON("r10")),
					soRawCancel(`7`, "", soValidJSON("r10")),
				}
			},
			check: func(t *testing.T, s *sendOriginSrv, frames []soFrame, _ string, _ int) {
				require.Empty(t, frames, "M-10: zero frames (got %v)", soTypes(frames))
			},
			bytes: true,
		},
	}
}

func soValidationPositiveControls(t *testing.T, sTrue, sFalse *sendOriginSrv, mediaTrue, mediaFalse string) {
	// ---- Positive controls (SV-12: accepted identically in both modes) ----
	// M-11: origin absent, non-browser, valid target, valid id. Content
	// "hello" AND the empty-content variant whose validity comes from one
	// real media:// ref (MessageFrame anyOf: content ≥1 OR ≥1 media item).

	t.Run("M-11-non-browser-control", func(t *testing.T) {
		for _, s := range []*sendOriginSrv{sTrue, sFalse} {
			conn, sid := soM11Setup(t, s)
			soSend(t, conn, soRawMsg(soValidJSON(sid), "", soValidJSON("m11"), "hello"))
			frames := soWindow(t, conn, lifecycleEW, "message_status")
			require.Contains(t, soTypes(frames), "message_status",
				"M-11: the content variant is accepted; window: %v", soTypes(frames))
			require.NotContains(t, soTypes(frames), "send_refused", "M-11: accepted, never refused")
		}
	})

	t.Run("M-11b-non-browser-empty-with-real-media", func(t *testing.T) {
		for _, tc := range []struct {
			s     *sendOriginSrv
			ref   string
			label string
		}{{sTrue, mediaTrue, "true"}, {sFalse, mediaFalse, "false"}} {
			conn, sid := soM11Setup(t, tc.s)
			soSend(t, conn, soRawMsg(soValidJSON(sid), "", soValidJSON("m11m"), "", tc.ref))
			frames := soWindow(t, conn, lifecycleEW, "message_status")
			require.Contains(t, soTypes(frames), "message_status",
				"M-11 media variant: empty content + one real media ref is accepted (mode %s); window: %v",
				tc.label, soTypes(frames))
			require.NotContains(t, soTypes(frames), "send_refused", "M-11 media variant (mode %s)", tc.label)
		}
	})

	// M-12: valid chat origin, target matching it, id ABSENT (legacy
	// optional id, P3) — accepted; the turn starts.
	t.Run("M-12-legacy-optional-id-control", func(t *testing.T) {
		for _, s := range []*sendOriginSrv{sTrue, sFalse} {
			conn := s.dialBrowserConn(t)
			drainAdmissionOneShot(t, conn)
			sid := s.mintChat(t, conn, soRowKey("M-12", 1), "sv12 M-12 mint")
			soSend(t, conn, soRawMsg(soValidJSON(sid), soOriginChat(sid), "", "hello"))
			frames := soWindow(t, conn, lifecycleEW, "done")
			require.Contains(t, soTypes(frames), "user_message", "M-12: the message is accepted (echo)")
			require.Contains(t, soTypes(frames), "done", "M-12: the turn starts and completes")
			require.NotContains(t, soTypes(frames), "send_refused", "M-12: a valid legacy send is never refused")
		}
	})

	t.Run("M-12b-empty-with-real-media-control", func(t *testing.T) {
		for _, tc := range []struct {
			s     *sendOriginSrv
			ref   string
			label string
		}{{sTrue, mediaTrue, "true"}, {sFalse, mediaFalse, "false"}} {
			conn := tc.s.dialBrowserConn(t)
			drainAdmissionOneShot(t, conn)
			sid := tc.s.mintChat(t, conn, soRowKey("M-12b", 1), "sv12 M-12b mint")
			soSend(t, conn, soRawMsg(soValidJSON(sid), soOriginChat(sid), soValidJSON("m12b"), "", tc.ref))
			frames := soWindow(t, conn, lifecycleEW, "done")
			require.Contains(t, soTypes(frames), "done", "M-12b media variant: turn starts (mode %s)", tc.label)
			require.NotContains(t, soTypes(frames), "send_refused", "M-12b media variant accepted (mode %s)", tc.label)
		}
	})
}

// soM11Setup mints M-11's target chat ON a CLI connection (the keyed mint is
// the one first-send shape today's handler acknowledges from a non-browser
// conn — the ws_send_origin_test.go P2 row precedent) and returns the conn
// and sid. The ROW's send then uses an ABSENT origin against this target.
func soM11Setup(t *testing.T, s *sendOriginSrv) (*websocket.Conn, string) {
	t.Helper()
	conn := s.dialCLIConn(t)
	drainAdmissionOneShot(t, conn)
	sid := s.mintChat(t, conn, soRowKey("M-11", 1), "sv12 M-11 legacy mint")
	return conn, sid
}

func soJoinFrames(frames []soFrame) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString(soNormalize(f.Raw))
		b.WriteString("\n")
	}
	return b.String()
}

func soFirstError(t *testing.T, frames []soFrame) int {
	t.Helper()
	for i, f := range frames {
		if f.Type == "error" {
			return i
		}
	}
	t.Fatalf("no error frame in window: %v", soTypes(frames))
	return -1
}
