//go:build linux

package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/preview/extracttext"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type functionalScopeReceipt struct {
	Socket string            `json:"socket"`
	Create string            `json:"create"`
	Reads  map[string]string `json:"reads"`
}

func init() {
	name := filepath.Base(os.Args[0])
	if name == "t118-renderer" || (name == "t118-extractor" && len(os.Args) > 1 && os.Args[1] == extracttext.ModeArg) {
		os.Exit(functionalScopePeer())
	}
}

// External process edge only: drain the real request, perform real kernel
// operations under Service's actual renderer/extractor policy, then report
// observations in a terminal frame. No fake policy or successful PDF result.
func functionalScopePeer() int {
	_, length, err := protocol.ReadRequest(os.Stdin)
	if err != nil {
		return 70
	}
	if _, err = io.CopyN(io.Discard, os.Stdin, length); err != nil {
		return 71
	}
	receipt := functionalScopeReceipt{Socket: fmt.Sprintf("/tmp/of-scope-%d", os.Getpid()), Reads: map[string]string{}}
	receipt.Create = functionalDoProbe("socket", receipt.Socket).Errno
	for _, path := range []string{"/dev/urandom", "/etc/passwd", "/etc/nsswitch.conf"} {
		file, openErr := os.Open(path)
		if openErr != nil {
			if errors.Is(openErr, os.ErrPermission) {
				receipt.Reads[path] = "permission"
			} else {
				receipt.Reads[path] = openErr.Error()
			}
			continue
		}
		var one [1]byte
		_, readErr := io.ReadFull(file, one[:]) // Bounded, even for urandom.
		closeErr := file.Close()
		receipt.Reads[path] = "ok"
		if readErr != nil || closeErr != nil {
			receipt.Reads[path] = fmt.Sprintf("read=%v close=%v", readErr, closeErr)
		}
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return 72
	}
	if protocol.NewFrameWriter(os.Stdout).Error("T118_SCOPE:"+string(raw), "test process-edge kernel observations") != nil {
		return 73
	}
	return 0
}

// T-118 / FR-061: exercise Service.Preview's ACTUAL non-converter starts,
// not hooksFor with a policy invented by the test. Unchanged-scope controls
// may already pass on the pre-change tree; their mutation proof is CHECK's.
func TestPreviewOffice_T118_RendererAndExtractorDoNotGainConverterRights(t *testing.T) {
	functionalPinDataOutsideTmp(t)
	if !primeKernelSandbox(t) {
		return
	}
	for _, kind := range []string{"extractor", "renderer"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			self, err := os.Executable()
			require.NoError(t, err)
			root := t.TempDir()
			if kind == "renderer" {
				// §3.9 fixture move (sanctioned): the renderer registration
				// runs the live Linux own-folder check, so its fixture root
				// moves inside the default program path's own folder. The
				// extractor peer needs no registration and keeps its
				// staging. Assertions are untouched.
				root = filepath.Join(functionalOwnFolder(t), "t118-renderer")
				require.NoError(t, os.MkdirAll(root, 0o755))
			}
			peer := filepath.Join(root, "t118-"+kind)
			functionalCopyTree(t, self, peer)
			if kind == "extractor" {
				self = peer
			} else {
				functionalRegisterScopeRenderer(t, home, root, peer)
			}
			for _, path := range []string{"/dev/urandom", "/etc/passwd", "/etc/nsswitch.conf"} {
				file, openErr := os.Open(path)
				require.NoError(t, openErr, "same-object outside read control")
				var one [1]byte
				_, readErr := io.ReadFull(file, one[:])
				require.NoError(t, file.Close())
				require.NoError(t, readErr)
			}
			svc, err := New(Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
			require.NoError(t, err)
			adm, err := svc.Admit(context.Background(), false)
			require.NoError(t, err)
			defer adm.Release()
			result, previewErr := svc.Preview(context.Background(), adm, Request{Name: "scope.pdf", Ext: ".pdf", Data: officeRetryPDF(), Default: true})
			var message string
			if kind == "extractor" {
				require.Error(t, previewErr, "instrument: receive terminal extractor observations, not fake success")
				message = previewErr.Error()
			} else {
				require.NoError(t, previewErr, "real extractor handles fallback after scope probe")
				require.NotNil(t, result)
				require.Equal(t, ReasonRendererFailed, result.RenderReason)
				message = result.RenderDetail
			}
			const prefix = "PDF renderer failed (T118_SCOPE:"
			require.True(t, strings.HasPrefix(message, prefix) && strings.HasSuffix(message, ")"), "instrument: actual %s peer must have reported all probes: %s", kind, message)
			var receipt functionalScopeReceipt
			require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(message, prefix), ")")), &receipt))
			assert.Contains(t, []string{"EACCES", "EPERM"}, receipt.Create, "T-118: no converter socket grant on %s", kind)
			assert.Equal(t, map[string]string{"/dev/urandom": "permission", "/etc/passwd": "permission", "/etc/nsswitch.conf": "permission"}, receipt.Reads, "T-118: converter-only reads must not leak into %s", kind)
			outside := functionalRunProbe(t, nil, "socket", receipt.Socket, home)
			require.Equal(t, "ok", outside.Errno, "same socket pathname succeeds outside the sandbox")
		})
	}
}

func functionalRegisterScopeRenderer(t *testing.T, home, root, peer string) {
	t.Helper()
	hash, _, err := component.HashFile(peer)
	require.NoError(t, err)
	files := map[string]string{filepath.Base(peer): hash}
	manifest := component.Manifest{Schema: component.ManifestSchema, Component: component.KindPDFRenderer, Version: "T-118-fixture", BuildID: "T-118-scope", ProtocolVersion: 1, Platform: component.Platform(), Executable: filepath.Base(peer), Files: files, FileListSHA256: component.FileListDigest(files), Trust: component.TrustPackaged}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, component.ManifestFile), raw, 0600))
	_, err = component.RegisterRenderer(component.RegisterOptions{Home: home, Folder: root, Operator: "T-118-scope-test"})
	require.NoError(t, err)
}
