package main

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/daemon"
)

type instanceBinding struct { // not-wire-format: private operator evidence
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
type stoppedReceipt struct { // not-wire-format: private operator evidence
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

func bindingInstanceID(b instanceBinding) string {
	h := sha256.New()
	writeTuple(h, b.DataRoot, b.RootIdentity, strconv.Itoa(b.PreviousPID), b.ProcessStartID, b.ProcessExecutable)
	return "hi1_" + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func checkRootBinding(b instanceBinding) error {
	identity, err := rootObjectIdentity(b.DataRoot)
	if err != nil {
		return integrity("io_error", err)
	}
	if identity != b.RootIdentity {
		return refusal("source_changed")
	}
	return nil
}
func captureInstance(a repairArgs) (instanceBinding, error) {
	b := instanceBinding{SchemaVersion: 1, RecordType: "history_index_instance_binding", DataRoot: a.root, PIDFile: daemon.PIDPath(a.root), Operator: a.operator}
	identity, err := rootObjectIdentity(a.root)
	if err != nil {
		return b, integrity("io_error", err)
	}
	b.RootIdentity = identity
	pidBytes, err := os.ReadFile(b.PIDFile)
	if errors.Is(err, os.ErrNotExist) {
		return b, refusal("stopped_evidence_unknown")
	}
	if err != nil {
		return b, integrity("io_error", err)
	}
	b.PreviousPID, err = parsePositivePID(pidBytes)
	if err != nil {
		return b, err
	}
	p, err := daemon.InspectProcess(b.PreviousPID)
	if err != nil || p.State != "running" || p.StartID == "" || p.Executable == "" {
		return b, refusal("stopped_evidence_unknown")
	}
	if !samePath(p.Executable, a.executable) {
		return b, refusal("stopped_evidence_unknown")
	}
	actual, err := os.Stat(p.Executable)
	if err != nil {
		return b, integrity("io_error", err)
	}
	expected, err := os.Stat(a.executable)
	if err != nil {
		return b, integrity("io_error", err)
	}
	if !os.SameFile(actual, expected) || !actual.Mode().IsRegular() {
		return b, refusal("stopped_evidence_unknown")
	}
	b.ProcessStartID, b.ProcessExecutable, b.PIDFileSHA256 = p.StartID, p.Executable, digestBytes(pidBytes)
	b.ToolCommit, err = builtToolCommit()
	if err != nil {
		return b, err
	}
	b.CapturedAt, b.InstanceID = time.Now().UTC().Format(time.RFC3339), bindingInstanceID(b)
	if err = checkRootBinding(b); err != nil {
		return b, err
	}
	again, err := os.ReadFile(b.PIDFile)
	if err != nil {
		return b, integrity("io_error", err)
	}
	p2, err := daemon.InspectProcess(b.PreviousPID)
	if err != nil || p2.State != "running" || p2.StartID != b.ProcessStartID || p2.Executable != b.ProcessExecutable || digestBytes(again) != b.PIDFileSHA256 {
		return b, refusal("stopped_evidence_unknown")
	}
	return b, nil
}
func loadInstanceBinding(a repairArgs) (instanceBinding, []byte, error) {
	var b instanceBinding
	body, err := readEvidenceFile(a.binding)
	if err != nil {
		return b, nil, err
	}
	if err = validateRepairJSON("instance-binding.schema.json", body, &b); err != nil {
		return b, nil, refusal("stopped_evidence_unknown")
	}
	if b.DataRoot != a.root || b.PIDFile != daemon.PIDPath(a.root) || b.InstanceID != bindingInstanceID(b) || !utcTimestampValid(b.CapturedAt) || strings.TrimSpace(b.Operator) == "" {
		return b, nil, refusal("stopped_evidence_unknown")
	}
	resolved, err := canonicalLocation(b.ProcessExecutable)
	if err != nil || resolved != b.ProcessExecutable {
		return b, nil, refusal("stopped_evidence_unknown")
	}
	if err = checkRootBinding(b); err != nil {
		return b, nil, err
	}
	return b, body, nil
}

// nativeStopped is deliberately check-only. Missing PID bytes alone never
// establish stopped state: the captured native process is inspected as well.
func nativeStopped(b instanceBinding) (string, error) {
	if err := checkRootBinding(b); err != nil {
		return "", err
	}
	state := "absent"
	pidBytes, err := os.ReadFile(b.PIDFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", integrity("io_error", err)
	}
	if err == nil {
		pid, parseErr := parsePositivePID(pidBytes)
		if parseErr != nil {
			return "", parseErr
		}
		if pid != b.PreviousPID || digestBytes(pidBytes) != b.PIDFileSHA256 {
			p, probeErr := daemon.InspectProcess(pid)
			if probeErr == nil && p.State == "running" {
				return "", refusal("instance_running")
			}
			return "", refusal("stopped_evidence_unknown")
		}
		state = "same_stale"
	}
	p, err := daemon.InspectProcess(b.PreviousPID)
	if err != nil {
		return "", refusal("stopped_evidence_unknown")
	}
	switch p.State {
	case "running":
		if p.StartID != b.ProcessStartID || p.Executable != b.ProcessExecutable {
			return "", refusal("stopped_evidence_unknown")
		}
		return "", refusal("instance_running")
	case "exited":
		if p.StartID != b.ProcessStartID {
			return "", refusal("stopped_evidence_unknown")
		}
	case "absent":
	default:
		return "", refusal("stopped_evidence_unknown")
	}
	return state, nil
}
func produceStoppedEvidence(a repairArgs, out io.Writer) error {
	if a.capture {
		b, err := captureInstance(a)
		if err != nil {
			return err
		}
		if err = writeRepairJSON(a.output, "instance-binding.schema.json", b); err != nil {
			return err
		}
		fmt.Fprintln(out, "mode=stopped-receipt capture=1 observation=running")
		return nil
	}
	b, body, err := loadInstanceBinding(a)
	if err != nil {
		return err
	}
	state, err := nativeStopped(b)
	if err != nil {
		return err
	}
	commit, err := builtToolCommit()
	if err != nil {
		return err
	}
	r := stoppedReceipt{SchemaVersion: 1, RecordType: "history_index_stopped_receipt", DataRoot: a.root, InstanceID: b.InstanceID, PreviousPID: b.PreviousPID, InstanceBindingSHA256: digestBytes(body), PIDFileState: state, CheckedAt: time.Now().UTC().Format(time.RFC3339), InspectionMethod: "native_process", Operator: a.operator, ToolCommit: commit}
	again, err := readEvidenceFile(a.binding)
	if err != nil {
		return err
	}
	state2, err := nativeStopped(b)
	if err != nil {
		return err
	}
	if digestBytes(again) != r.InstanceBindingSHA256 || state != state2 {
		return refusal("stopped_evidence_unknown")
	}
	if err = writeRepairJSON(a.output, "stopped-receipt.schema.json", r); err != nil {
		return err
	}
	fmt.Fprintln(out, "mode=stopped-receipt observation=stopped")
	return nil
}
func verifyStoppedEvidence(a repairArgs) error {
	if a.binding == "" || a.receipt == "" {
		return refusal("stopped_evidence_missing")
	}
	b, body, err := loadInstanceBinding(a)
	if err != nil {
		return err
	}
	raw, err := readEvidenceFile(a.receipt)
	if err != nil {
		return err
	}
	var r stoppedReceipt
	if err = validateRepairJSON("stopped-receipt.schema.json", raw, &r); err != nil {
		return refusal("stopped_evidence_unknown")
	}
	if r.DataRoot != b.DataRoot || r.InstanceID != b.InstanceID || r.PreviousPID != b.PreviousPID || r.InstanceBindingSHA256 != digestBytes(body) || !utcTimestampValid(r.CheckedAt) || strings.TrimSpace(r.Operator) == "" {
		return refusal("stopped_evidence_unknown")
	}
	state, err := nativeStopped(b)
	if err != nil {
		return err
	}
	if r.PIDFileState != state {
		return refusal("stopped_evidence_unknown")
	}
	return nil
}
