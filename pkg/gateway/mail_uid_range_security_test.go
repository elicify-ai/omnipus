package gateway

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

func TestMailDraftUIDPreconditionsRejectOutOfUint32Range(t *testing.T) {
	cur := &email.MailView{UID: ^uint32(0), UIDValidity: ^uint32(0)}

	for _, tc := range []struct {
		name string
		uid  int64
		uv   int64
	}{
		{name: "negative uid", uid: -1, uv: 1},
		{name: "negative uidvalidity", uid: 1, uv: -1},
		{name: "overflowing uid", uid: 1 << 32, uv: 1},
		{name: "overflowing uidvalidity", uid: 1, uv: 1 << 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := mailDraftRefBodyAgreement("mid:<draft@example.test>", tc.uid, tc.uv); ok {
				t.Fatal("out-of-range draft precondition passed the pre-read request validation")
			}
			status, _, _ := mailDraftStaleness(cur, tc.uid, tc.uv)
			if status != http.StatusBadRequest {
				t.Fatalf("out-of-range draft precondition status = %d, want 400", status)
			}
		})
	}

	status, _, _ := mailDraftStaleness(cur, 1<<32-1, 1<<32-1)
	if status != 0 {
		t.Fatalf("maximum uint32 precondition status = %d, want accepted", status)
	}
}

func TestMailDraftUIDPreconditionsRejectAtHandlersBeforeDial(t *testing.T) {
	env := newMailRedEnv(t)
	// The handler must enforce the uint32 domain even when optional inbound
	// schema validation is disabled.
	env.api.agentLoop.GetConfig().Gateway.ValidateInbound = false
	port, dials := listenCount(t)
	pointMailboxAt(t, env, port, port)
	path := mailMessagesPath("drafts") + "/" + url.PathEscape("mid:<draft@example.test>")

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name: "update negative uid", method: http.MethodPut, path: path,
			body: `{"uid":-1,"uidvalidity":1,"to":["a@b.test"],"subject":"s","body_markdown":"b","keep_attachment_parts":[]}`,
		},
		{
			name: "update overflowing uidvalidity", method: http.MethodPut, path: path,
			body: `{"uid":1,"uidvalidity":4294967296,"to":["a@b.test"],"subject":"s","body_markdown":"b","keep_attachment_parts":[]}`,
		},
		{
			name: "send negative uidvalidity", method: http.MethodPost, path: path + "/send",
			body: `{"uid":1,"uidvalidity":-1,"to":["a@b.test"],"subject":"s","body_markdown":"b"}`,
		},
		{
			name: "send overflowing uid", method: http.MethodPost, path: path + "/send",
			body: `{"uid":4294967296,"uidvalidity":1,"to":["a@b.test"],"subject":"s","body_markdown":"b"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := mailDo(env.mux, tc.method, tc.path, nextMailIP(), true, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if got := dials.Load(); got != 0 {
				t.Fatalf("invalid draft precondition opened %d connection(s), want zero", got)
			}
		})
	}
}

func TestMailUIDToWirePreservesUint32Maximum(t *testing.T) {
	const want = int64(1<<32 - 1)
	if got := mailUIDToWire(^uint32(0)); got != want {
		t.Fatalf("mailUIDToWire(max uint32) = %d, want %d", got, want)
	}

	payload, err := json.Marshal(gen.MailMessage{
		Uid:         mailUIDToWire(^uint32(0)),
		Uidvalidity: mailUIDToWire(^uint32(0)),
	})
	if err != nil {
		t.Fatalf("marshal generated mail response: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("decode generated mail response: %v", err)
	}
	for _, field := range []string{"uid", "uidvalidity"} {
		if got := string(wire[field]); got != "4294967295" {
			t.Fatalf("serialized %s = %q, want 4294967295", field, got)
		}
	}
}

func TestMailUIDWireConversionsAvoidArchitectureSizedInt(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "rest_mail.go", nil, 0)
	if err != nil {
		t.Fatalf("parse rest_mail.go: %v", err)
	}
	var target *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "mailUIDToWire" {
			target = fn
			break
		}
	}
	if target == nil {
		t.Fatal("mailUIDToWire declaration not found")
	}

	ast.Inspect(target.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		conversion, ok := call.Fun.(*ast.Ident)
		if ok && conversion.Name == "int" {
			t.Error("mailUIDToWire passes through architecture-sized int")
		}
		return true
	})

	callSites := make(map[string][]string)
	paths, err := filepath.Glob("rest_mail*.go")
	if err != nil {
		t.Fatalf("glob mail REST files: %v", err)
	}
	wireAssignments := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		// Nullable wire fields cannot take the int64 call result directly:
		// the compliant indirection is `v := mailUIDToWire(x)` followed by
		// `field: &v` (the uidvalidity-unknown pattern, ADR P1.3). Pass 1
		// collects every variable the conversion feeds, directly or through
		// one &-alias, so pass 2 can accept exactly that pattern — a raw
		// value or a non-convertible expression still fails below.
		converted := map[string]bool{}
		aliases := map[string]string{}
		ast.Inspect(parsed, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) != 1 {
				return true
			}
			name, isIdent := assign.Lhs[0].(*ast.Ident)
			if !isIdent {
				return true
			}
			switch rhs := assign.Rhs[0].(type) {
			case *ast.CallExpr:
				if fn, ok := rhs.Fun.(*ast.Ident); ok && fn.Name == "mailUIDToWire" {
					converted[name.Name] = true
				}
			case *ast.UnaryExpr:
				if rhs.Op == token.AND {
					if target, ok := rhs.X.(*ast.Ident); ok {
						aliases[name.Name] = target.Name
					}
				}
			}
			return true
		})
		for name, target := range aliases {
			if converted[target] {
				converted[name] = true
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if name, isIdent := call.Fun.(*ast.Ident); isIdent && name.Name == "mailUIDToWire" {
					var argument bytes.Buffer
					if printErr := printer.Fprint(&argument, fset, call.Args[0]); printErr != nil {
						t.Fatalf("print mailUIDToWire argument at %s: %v", fset.Position(call.Pos()), printErr)
					}
					callSites[path] = append(callSites[path], argument.String())
				}
			}
			field, ok := node.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := field.Key.(*ast.Ident)
			if !ok || (key.Name != "Uid" && key.Name != "Uidvalidity") {
				return true
			}
			wireAssignments++
			if ident, isIdent := field.Value.(*ast.Ident); isIdent {
				if !converted[ident.Name] {
					t.Errorf("%s must route %s through mailUIDToWire", fset.Position(field.Pos()), key.Name)
				}
				return true
			}
			call, ok := field.Value.(*ast.CallExpr)
			if !ok {
				t.Errorf("%s must route %s through mailUIDToWire", fset.Position(field.Pos()), key.Name)
				return true
			}
			conversion, isIdent := call.Fun.(*ast.Ident)
			if !isIdent || conversion.Name != "mailUIDToWire" {
				t.Errorf("%s must route %s through mailUIDToWire", fset.Position(field.Pos()), key.Name)
			}
			return true
		})
	}
	if wireAssignments == 0 {
		t.Fatal("no UID response assignments found; conversion guard did not exercise production call sites")
	}
	expectedCallSites := map[string][]string{
		"rest_mail_draft.go": {"newUID", "newUV"},
		// s.UIDValidity is handleMailFolders' STATUS call site (folder
		// discovery, commit 7c7e076c1) — its landing missed this map and the
		// guard has been red since; the entry records the real call site.
		"rest_mail_read.go":    {"s.UIDValidity", "rows[len(rows)-1].UID", "row.UID", "uv", "v.UID", "v.UIDValidity"},
		"rest_mail_summary.go": {"st.LastSeenUID"},
	}
	if !reflect.DeepEqual(callSites, expectedCallSites) {
		t.Fatalf("mailUIDToWire production call sites changed:\n got: %#v\nwant: %#v", callSites, expectedCallSites)
	}
}
