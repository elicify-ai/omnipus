package main

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/daemon"
	"github.com/stretchr/testify/require"
)

// not-wire-format: private operator-file oracle, never a gateway/SPA type.
type repairInstanceBinding struct {
	SchemaVersion     int    `json:"schema_version"`
	RecordType        string `json:"record_type"`
	DataRoot          string `json:"data_root"`
	PIDFile           string `json:"pid_file"`
	RootIdentity      string `json:"root_identity"`
	PreviousPID       int    `json:"previous_pid"`
	PIDFileSHA256     string `json:"pid_file_sha256"`
	ProcessStartID    string `json:"process_start_id"`
	ProcessExecutable string `json:"process_executable"`
	CapturedAt        string `json:"captured_at"`
	Operator          string `json:"operator"`
	ToolCommit        string `json:"tool_commit"`
	InstanceID        string `json:"instance_id"`
}

// not-wire-format: private operator-file oracle, never a gateway/SPA type.
type repairStoppedReceipt struct {
	SchemaVersion         int    `json:"schema_version"`
	RecordType            string `json:"record_type"`
	DataRoot              string `json:"data_root"`
	InstanceID            string `json:"instance_id"`
	PreviousPID           int    `json:"previous_pid"`
	InstanceBindingSHA256 string `json:"instance_binding_sha256"`
	PIDFileState          string `json:"pid_file_state"`
	CheckedAt             string `json:"checked_at"`
	InspectionMethod      string `json:"inspection_method"`
	Operator              string `json:"operator"`
	ToolCommit            string `json:"tool_commit"`
}

const repairTestOperator = "operator-évidence-1266"

func repairEvidencePath(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return filepath.Join(dir, name)
}

func repairDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Independent D13/G codec; it never calls the command's identity encoder.
func repairExpectedInstanceID(b repairInstanceBinding) string {
	var preimage bytes.Buffer
	for _, value := range []string{b.DataRoot, b.RootIdentity, strconv.Itoa(b.PreviousPID), b.ProcessStartID, b.ProcessExecutable} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len([]byte(value))))
		preimage.Write(size[:])
		preimage.WriteString(value)
	}
	sum := sha256.Sum256(preimage.Bytes())
	return "hi1_" + base64.RawURLEncoding.EncodeToString(sum[:])
}

func repairBuiltCommit(t *testing.T, executable string) string {
	t.Helper()
	info, err := buildinfo.ReadFile(executable)
	require.NoError(t, err)
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			require.Regexp(t, `^[0-9a-f]{40}$`, setting.Value, "actual native Go build VCS receipt")
			return setting.Value
		}
	}
	t.Fatal("repair build has no vcs.revision: cannot independently assert actual tool_commit")
	return ""
}

func repairReadEvidence(t *testing.T, path string, output any, wantKeys []string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(output), "private v1 evidence has the decided shape")
	var extra any
	require.ErrorIs(t, decoder.Decode(&extra), io.EOF, "exactly one object, not concatenated evidence")
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	keys := make([]string, 0, len(fields))
	for key, value := range fields {
		keys = append(keys, key)
		require.NotEqual(t, "null", string(bytes.TrimSpace(value)), "no null evidence member %s", key)
	}
	sort.Strings(keys)
	sort.Strings(wantKeys)
	require.Equal(t, wantKeys, keys, "no missing/extra/fictional lock or success evidence fields")
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.EqualValues(t, 0o600, info.Mode().Perm(), "private evidence permissions where supported")
	}
	return body
}

func repairAssertUTCTime(t *testing.T, raw string) {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, raw)
	require.NoError(t, err)
	_, offset := parsed.Zone()
	require.Zero(t, offset, "native observation time is UTC RFC3339")
}

func repairCapture(t *testing.T, tool, helper, source string, child *repairNativeInstance) (string, repairInstanceBinding) {
	t.Helper()
	path := repairEvidencePath(t, "instance-binding.json")
	before := repairTree(t, source)
	root, err := os.Stat(source)
	require.NoError(t, err)
	repairRun(t, tool, 0, "stopped-receipt", "--capture-instance", "--data-root", source, "--gateway-executable", helper, "--operator", repairTestOperator, "--output", path)
	require.Equal(t, before, repairTree(t, source), "capture never constructs a store, writes or cleans a PID/lock")
	after, err := os.Stat(source)
	require.NoError(t, err)
	require.True(t, os.SameFile(root, after), "actual captured directory object remains")
	var binding repairInstanceBinding
	repairReadEvidence(t, path, &binding, []string{"schema_version", "record_type", "data_root", "pid_file", "root_identity", "previous_pid", "pid_file_sha256", "process_start_id", "process_executable", "captured_at", "operator", "tool_commit", "instance_id"})
	require.Equal(t, 1, binding.SchemaVersion)
	require.Equal(t, "history_index_instance_binding", binding.RecordType)
	require.Equal(t, source, binding.DataRoot)
	require.Equal(t, daemon.PIDPath(source), binding.PIDFile)
	require.Equal(t, child.cmd.Process.Pid, binding.PreviousPID, "actual live child, never an invented dead PID")
	require.Equal(t, helper, binding.ProcessExecutable, "actual native helper path supplied as GATEWAY")
	require.Equal(t, repairDigest([]byte(strconv.Itoa(child.cmd.Process.Pid))), binding.PIDFileSHA256)
	require.NotEmpty(t, binding.RootIdentity, "native root identity is opaque, not a guessed codec")
	require.NotEmpty(t, binding.ProcessStartID, "native birth identity is opaque, not a guessed codec")
	require.NotEqual(t, strconv.Itoa(child.cmd.Process.Pid), binding.ProcessStartID, "PID alone is not process birth")
	require.Equal(t, repairTestOperator, binding.Operator)
	require.Equal(t, repairBuiltCommit(t, tool), binding.ToolCommit)
	repairAssertUTCTime(t, binding.CapturedAt)
	require.Equal(t, repairExpectedInstanceID(binding), binding.InstanceID)
	require.Len(t, binding.InstanceID, 47, "hi1_ plus canonical unpadded SHA256 base64url")
	return path, binding
}

func repairProduceReceipt(t *testing.T, tool, source, bindingPath string, binding repairInstanceBinding, state string) string {
	t.Helper()
	path := repairEvidencePath(t, "stopped-receipt.json")
	before := repairTree(t, source)
	bindingBytes, err := os.ReadFile(bindingPath)
	require.NoError(t, err)
	repairRun(t, tool, 0, "stopped-receipt", "--data-root", source, "--instance-binding", bindingPath, "--operator", repairTestOperator, "--output", path)
	require.Equal(t, before, repairTree(t, source), "stopped observation never clears/recreates PID or writes canonical/index state")
	var receipt repairStoppedReceipt
	repairReadEvidence(t, path, &receipt, []string{"schema_version", "record_type", "data_root", "instance_id", "previous_pid", "instance_binding_sha256", "pid_file_state", "checked_at", "inspection_method", "operator", "tool_commit"})
	require.Equal(t, 1, receipt.SchemaVersion)
	require.Equal(t, "history_index_stopped_receipt", receipt.RecordType)
	require.Equal(t, source, receipt.DataRoot)
	require.Equal(t, binding.InstanceID, receipt.InstanceID)
	require.Equal(t, binding.PreviousPID, receipt.PreviousPID)
	require.Equal(t, repairDigest(bindingBytes), receipt.InstanceBindingSHA256, "hash the supplied file bytes, not a reserialization")
	require.Equal(t, state, receipt.PIDFileState)
	require.Equal(t, "native_process", receipt.InspectionMethod)
	require.Equal(t, repairTestOperator, receipt.Operator)
	require.Equal(t, repairBuiltCommit(t, tool), receipt.ToolCommit)
	repairAssertUTCTime(t, receipt.CheckedAt)
	return path
}
