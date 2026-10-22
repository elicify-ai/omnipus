package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type sessionRepairPlan struct {
	id, dir string
	build   *session.ChatHistoryIndexBuild
	err     error
}

func classifyBuildError(err error) error {
	if err == nil {
		return nil
	}
	var typed *repairError
	if errors.As(err, &typed) {
		return err
	}
	// The other backend lane supplies this typed seam at integration. A
	// structural interface keeps this independently based command buildable
	// without merging that lane's gateway/session production files here.
	var buildError interface{ HistoryIndexBuildClass() string }
	if errors.As(err, &buildError) {
		switch buildError.HistoryIndexBuildClass() {
		case "unconverted", "source_changed":
			return refusal(buildError.HistoryIndexBuildClass())
		case "corrupt_archive", "invalid_reference":
			return integrity(buildError.HistoryIndexBuildClass(), err)
		case "storage_error":
			return integrity("io_error", err)
		default:
			return integrity("index_verification", err)
		}
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return integrity("io_error", err)
	}
	// The shared engine owns decoding and reference validation. Its current
	// offline errors are private, fixed strings, never copied to the output.
	switch {
	case strings.Contains(err.Error(), "unconverted"):
		return refusal("unconverted")
	case strings.Contains(err.Error(), "source_changed"):
		return refusal("source_changed")
	case strings.Contains(err.Error(), "reference"), strings.Contains(err.Error(), "effect target"), strings.Contains(err.Error(), "effect metadata"):
		return integrity("invalid_reference", err)
	default:
		return integrity("corrupt_archive", err)
	}
}
func prepareAllSessions(ctx context.Context, a repairArgs, inventory []inventoryEntry) []sessionRepairPlan {
	plans := make([]sessionRepairPlan, 0)
	legacy := map[string]bool{}
	candidates := map[string]bool{}
	for _, e := range inventory {
		parts := strings.Split(e.Path, "/")
		if len(parts) == 3 && parts[0] == "sessions" && !strings.HasPrefix(parts[1], ".") && (parts[2] == "meta.json" || parts[2] == "context.jsonl" || strings.HasPrefix(parts[2], "transcript")) {
			candidates[parts[1]] = true
		}
	}
	for _, e := range inventory {
		parts := strings.Split(e.Path, "/")
		if len(parts) == 2 && parts[0] == "sessions" && e.Kind == "file" && strings.HasSuffix(e.Path, ".jsonl") || len(parts) >= 3 && parts[0] == "sessions" && parts[1] == ".context" && strings.HasSuffix(e.Path, ".jsonl") {
			legacy[e.Path] = true
		}
		// Former per-agent stores must go through the product's explicit
		// cutover first; they are never silently omitted from offline repair.
		if len(parts) >= 4 && parts[0] == "agents" && parts[2] == "sessions" && e.Kind == "file" && (strings.HasSuffix(e.Path, ".jsonl") || filepath.Base(e.Path) == "meta.json") {
			legacy[e.Path] = true
		}
		if len(parts) != 2 || parts[0] != "sessions" || e.Kind != "directory" || !candidates[parts[1]] {
			continue
		}
		p := sessionRepairPlan{id: parts[1], dir: filepath.Join(a.root, "sessions", parts[1])}
		p.err = checkSessionFormat(p.dir)
		if _, err := os.Stat(filepath.Join(p.dir, "context.jsonl")); err == nil {
			p.err = refusal("unconverted")
		} else if !errors.Is(err, os.ErrNotExist) {
			p.err = integrity("io_error", err)
		}
		if p.err == nil {
			var err error
			p.build, err = session.PrepareChatHistoryIndex(ctx, p.dir, session.ChatHistoryIndexBuildOptions{ScratchRoot: a.scratch})
			p.err = classifyBuildError(err)
		}
		plans = append(plans, p)
	}
	// Inventory order is stable. Do not iterate a map for observable output.
	for _, e := range inventory {
		if legacy[e.Path] {
			plans = append(plans, sessionRepairPlan{id: "legacy", err: refusal("unconverted")})
		}
	}
	return plans
}

func checkSessionFormat(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return integrity("io_error", err)
	}
	var meta struct { // not-wire-format: private canonical identity preflight
		Type session.UnifiedSessionType `json:"type"`
	}
	if err = json.Unmarshal(b, &meta); err != nil {
		return integrity("corrupt_archive", err)
	}
	if !session.IsValidSessionType(meta.Type) {
		return refusal("unconverted")
	}
	return nil
}

type repairTotals struct{ sessions, create, rebuild, unchanged, published, refused, code int }

func (t *repairTotals) failure(out io.Writer, id string, err error) {
	class, code := failureClass(err)
	if code > t.code {
		t.code = code
	}
	t.refused++
	if id == "" {
		fmt.Fprintf(out, "class=%s\n", class)
	} else {
		fmt.Fprintf(out, "session=%q action=refused class=%s\n", id, class)
	}
}
func (t repairTotals) summary(out io.Writer, mode string) {
	fmt.Fprintf(out, "mode=%s sessions=%d create=%d unchanged=%d rebuild=%d published=%d refused=%d canonical_writes=0\n", mode, t.sessions, t.create, t.unchanged, t.rebuild, t.published, t.refused)
}
func repairGuards(ctx context.Context, a repairArgs) error {
	if err := verifyStoppedEvidence(a); err != nil {
		return err
	}
	if err := verifyBackupAndSource(ctx, a); err != nil {
		return err
	}
	return verifyStoppedEvidence(a)
}
func runRepairPlan(ctx context.Context, a repairArgs, out io.Writer) int {
	var report strings.Builder
	totals := repairTotals{}
	defer func() { io.WriteString(out, report.String()); totals.summary(out, a.mode) }()
	inventory, err := inventoryTree(ctx, a.root, false)
	if err != nil {
		totals.failure(&report, "", err)
		return totals.code
	}
	plans := prepareAllSessions(ctx, a, inventory)
	totals.sessions = len(plans)
	defer func() {
		for _, p := range plans {
			if p.build != nil {
				p.build.Close()
			}
		}
	}()
	for _, p := range plans {
		if p.err != nil {
			totals.failure(&report, p.id, p.err)
		}
	}
	// Format/integrity of every session is established before any backup or
	// stopped-instance prerequisite, and before publishing any session.
	if totals.code == 2 {
		return totals.code
	}
	if a.mode != "dry-run" {
		if a.mode == "apply" {
			err = repairGuards(ctx, a)
		} else {
			err = verifyBackupAndSource(ctx, a)
		}
		if err != nil {
			totals.failure(&report, "", err)
			return totals.code
		}
	}
	if totals.refused != 0 {
		return totals.code
	}
	if a.mode == "verify" {
		for _, p := range plans {
			if err = session.VerifyChatHistoryIndex(ctx, p.dir); err != nil {
				totals.failure(&report, p.id, integrity("index_verification", err))
			} else {
				totals.unchanged++
			}
		}
		if err = verifyBackupAndSource(ctx, a); err != nil {
			totals.failure(&report, "", err)
		}
		return totals.code
	}
	for _, p := range plans {
		s := p.build.Summary()
		if a.mode == "dry-run" {
			switch s.Action {
			case "create":
				totals.create++
			case "rebuild":
				totals.rebuild++
			case "unchanged":
				totals.unchanged++
			}
			fmt.Fprintf(&report, "session=%s action=%s groups=%d partitions=%d\n", s.SessionID, s.Action, s.SourceGroupCount, s.PartitionCount)
			continue
		}
		if err = repairGuards(ctx, a); err != nil {
			totals.failure(&report, p.id, err)
			break
		}
		err = p.build.Publish(ctx)
		if err != nil {
			// A durable checkpoint may already have landed before a reported
			// fsync/publication failure. Observe the actual shared verifier before
			// reporting counts; never claim zero after a detected late failure.
			if s.Action != "unchanged" && session.VerifyChatHistoryIndex(ctx, p.dir) == nil {
				totals.published++
			}
			totals.failure(&report, p.id, classifyBuildError(err))
			break
		}
		if s.Action == "unchanged" {
			totals.unchanged++
		} else {
			totals.published++
		}
		fmt.Fprintf(&report, "session=%s action=%s groups=%d partitions=%d\n", s.SessionID, s.Action, s.SourceGroupCount, s.PartitionCount)
		if err = repairGuards(ctx, a); err != nil {
			totals.failure(&report, p.id, err)
			break
		}
		if err = session.VerifyChatHistoryIndex(ctx, p.dir); err != nil {
			totals.failure(&report, p.id, integrity("index_verification", err))
			break
		}
	}
	if a.mode == "apply" {
		if err = repairGuards(ctx, a); err != nil && totals.refused == 0 {
			totals.failure(&report, "", err)
		}
	}
	if a.mode == "dry-run" && a.planOutput != "" {
		if err = fileutil.WriteFileAtomicSyncDir(a.planOutput, []byte(report.String()), 0o600); err != nil {
			totals.failure(&report, "", integrity("io_error", err))
		}
	}
	return totals.code
}
