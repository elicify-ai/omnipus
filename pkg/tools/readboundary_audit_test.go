// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #920 RED pack — US-5 (every search leaves an audit trail) and the D10
// accepted risk. Tests 20, 21 and 31 of read-boundary-consistency-spec.md.
//
// Oracles: MV-3 (one path.search_roots row per call whose search ran;
// decision allow; details exactly {roots, path_arg}; roots = realpaths in
// walk order), FR-020 (grep satisfies auditLoggerAware and so receives the
// registry's logger by BOTH propagation paths), FR-021 (emission point:
// after filegrep.Search returned nil), FR-022 (no per-file read rows),
// decision D10 (Plan Supervisor is not read-confined).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
)

// rbAssertOneRootsRow checks MV-3's row shape.
func rbAssertOneRootsRow(t *testing.T, rows []auditRow, wantRoots []string, wantPathArg string) {
	t.Helper()
	roots := rbRowsFor(rows, rbSearchRootsEvent, "")
	if len(roots) != 1 {
		t.Fatalf("path.search_roots rows = %d, want exactly 1 (MV-3); all rows: %+v", len(roots), rows)
	}
	row := roots[0]
	if row.Tool != "grep" || row.Decision != string(audit.DecisionAllow) {
		t.Errorf("roots row tool/decision = %q/%q, want grep/allow (MV-3)", row.Tool, row.Decision)
	}
	got, ok := rbStringSlice(row.Details["roots"])
	if !ok || strings.Join(got, "\x00") != strings.Join(wantRoots, "\x00") {
		t.Errorf("roots = %#v, want %q (MV-3: realpaths in walk order)", row.Details["roots"], wantRoots)
	}
	if pa, present := row.Details["path_arg"]; !present || pa != wantPathArg {
		t.Errorf("path_arg = %#v (present=%v), want %q (MV-3)", pa, present, wantPathArg)
	}
	if len(row.Details) != 2 {
		keys := make([]string, 0, len(row.Details))
		for k := range row.Details {
			keys = append(keys, k)
		}
		t.Errorf("roots row detail keys = %q, want exactly [path_arg roots] (MV-3: no other detail keys)", keys)
	}
}

// TestGrepAudit_SearchRootsRow is test 21.
//
// Traces: S-5.1, S-5.2, S-5.4, S-5.6; FR-021, FR-022, SL-8.
func TestGrepAudit_SearchRootsRow(t *testing.T) {
	t.Run("S-5.1 300 matched files produce exactly one row and no per-file read rows", func(t *testing.T) {
		f := newRBFixture(t)
		bulk := filepath.Join(f.ext, "bulk")
		for i := 0; i < 300; i++ {
			rbWrite(t, filepath.Join(bulk, fmt.Sprintf("f%03d.txt", i)), "needle\n")
		}
		res := f.grepCall("needle", rbStr(bulk))
		if res.IsError {
			t.Fatalf("grep path=%q must run, got error: %s", bulk, res.ForLLM)
		}
		if hits := rbHitPaths(res.ForLLM); len(hits) != 300 {
			t.Fatalf("precondition: want 300 matched files, got %d", len(hits))
		}
		rows := f.rows(t)
		rbAssertOneRootsRow(t, rows, []string{bulk}, bulk)
		for _, r := range rows {
			if r.Event == audit.EventFileOp && r.Tool == "grep" {
				t.Fatalf("FR-022: grep wrote a per-file %s row: %+v", audit.EventFileOp, r)
			}
		}
	})

	t.Run("S-5.2 default search lists the workspace and the mount as roots", func(t *testing.T) {
		f := newRBFixture(t)
		if res := f.grepCall("needle", nil); res.IsError {
			t.Fatalf("grep with no path failed: %s", res.ForLLM)
		}
		rbAssertOneRootsRow(t, f.rows(t), []string{f.ws, f.mnt}, "")
	})

	t.Run("S-5.4 the row carries no search term and no matched line", func(t *testing.T) {
		f := newRBFixture(t)
		rbWrite(t, filepath.Join(f.ext, "term.txt"), "line with s3cr3t-term inside\n")
		if res := f.grepCall("s3cr3t-term", rbStr(f.ext)); res.IsError {
			t.Fatalf("grep path=<EXT> failed: %s", res.ForLLM)
		}
		rows := f.rows(t)
		roots := rbRowsFor(rows, rbSearchRootsEvent, "")
		if len(roots) != 1 {
			t.Fatalf("path.search_roots rows = %d, want 1", len(roots))
		}
		raw, err := json.Marshal(roots[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"s3cr3t-term", "line with"} {
			if strings.Contains(string(raw), banned) {
				t.Errorf("S-5.4: serialized roots row contains %q: %s", banned, raw)
			}
		}
	})

	t.Run("S-5.6 an invalid pattern writes no roots row", func(t *testing.T) {
		f := newRBFixture(t)
		res := f.grep.Execute(f.ctx, map[string]any{"pattern": "(", "regex": true})
		if !res.IsError || !strings.Contains(res.ForLLM, "invalid pattern") {
			t.Fatalf("precondition: an invalid regex must be rejected as an invalid pattern, got: %s", res.ForLLM)
		}
		if roots := rbRowsFor(f.rows(t), rbSearchRootsEvent, ""); len(roots) != 0 {
			t.Fatalf("S-5.6: invalid pattern wrote %d path.search_roots rows, want 0", len(roots))
		}
		// Discriminating control on a fresh fixture: the same call with a
		// VALID pattern writes one, so the zero above is not a logger that
		// never writes anything.
		g := newRBFixture(t)
		if res := g.grepCall("needle", nil); res.IsError {
			t.Fatalf("control grep failed: %s", res.ForLLM)
		}
		if roots := rbRowsFor(g.rows(t), rbSearchRootsEvent, ""); len(roots) != 1 {
			t.Fatalf("S-5.6 control: a valid search must write exactly 1 path.search_roots row, got %d", len(roots))
		}
	})

	t.Run("S-5.6 a call refused as busy writes no roots row", func(t *testing.T) {
		f := newRBFixture(t)
		held := 0
		for filegrep.TryAcquireNow() {
			held++
			if held > 16 {
				t.Fatal("filegrep.TryAcquireNow never refused — the walk semaphore is not bounded")
			}
		}
		res := f.grepCall("needle", nil)
		for i := 0; i < held; i++ {
			filegrep.Release()
		}
		if held != 2 {
			t.Errorf("DS-5 row 1: the shared walk semaphore holds %d slots, want 2", held)
		}
		if !res.IsError || !strings.Contains(res.ForLLM, "busy") {
			t.Fatalf("precondition: with every walk slot held grep must report busy, got: %s", res.ForLLM)
		}
		if roots := rbRowsFor(f.rows(t), rbSearchRootsEvent, ""); len(roots) != 0 {
			t.Fatalf("S-5.6: a busy refusal wrote %d path.search_roots rows, want 0", len(roots))
		}
	})
}

// TestGrepTool_RegistryWiresAuditLogger is test 31: grep receives the
// registry's audit logger through BOTH auditLoggerAware propagation paths —
// logger set before Register, and logger set after Register — and then
// writes its path.access_denied and path.search_roots rows. No logger is
// ever set by hand on the tool.
//
// Traces: S-1.4, S-5.1; FR-020.
func TestGrepTool_RegistryWiresAuditLogger(t *testing.T) {
	for _, order := range []string{"logger set before Register", "logger set after Register"} {
		t.Run(order, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(config.EnvHome, home)
			work := seedGrepWorkspace(t, home, "ws-rb-wire", "agent-rb-wire")
			rbWrite(t, filepath.Join(work, "notes", "a.md"), "needle\n")
			rbWrite(t, filepath.Join(home, "master.key"), "MASTER\n")
			ext := t.TempDir()
			rbWrite(t, filepath.Join(ext, "x.txt"), "needle\n")

			auditDir := t.TempDir()
			logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
			if err != nil {
				t.Fatal(err)
			}
			reg := NewToolRegistry()
			tool := NewGrepTool(work, true)
			if order == "logger set before Register" {
				reg.SetAuditLogger(logger)
				reg.Register(tool)
			} else {
				reg.Register(tool)
				reg.SetAuditLogger(logger)
			}
			ctx := WithTurnWorkspaceDir(WithAgentID(context.Background(), "agent-rb-wire"), work)

			if res := tool.Execute(ctx, map[string]any{"pattern": "needle", "path": filepath.Join(home, "master.key")}); !res.IsError {
				t.Fatalf("precondition: master.key must be refused, got: %s", res.ForLLM)
			}
			extReal := rbRealpath(t, ext)
			if res := tool.Execute(ctx, map[string]any{"pattern": "needle", "path": ext}); res.IsError {
				t.Fatalf("grep path=<EXT> must run (D1), got: %s", res.ForLLM)
			}

			rows := readAuditRows(t, logger, auditDir)
			denials := rbRowsFor(rows, rbAccessDeniedEvent, "grep")
			if len(denials) != 1 || denials[0].detail("reason") != ReasonCarveOut {
				t.Errorf("FR-020 (%s): want one grep path.access_denied row with reason carve_out, got %+v", order, denials)
			}
			roots := rbRowsFor(rows, rbSearchRootsEvent, "grep")
			if len(roots) != 1 {
				t.Fatalf("FR-020/FR-021 (%s): want one grep path.search_roots row, got %d; rows: %+v", order, len(roots), rows)
			}
			if got, _ := rbStringSlice(roots[0].Details["roots"]); len(got) != 1 || got[0] != extReal {
				t.Errorf("roots = %#v, want [%q]", roots[0].Details["roots"], extReal)
			}
		})
	}
}

// TestReadBoundary_PlanSupervisorGrepReach is test 20 (S-4.9, DS-3 row 14,
// SL-5): the Plan Supervisor, with its SHIPPED seed (grep allowed,
// read_file denied) and not read-confined (D10), greps
// $OMNIPUS_HOME/sessions and gets the transcript match; a grep of
// master.key is still refused. Pins the D10 accepted risk.
//
// Traces: S-4.9; FR-016, FR-030.
func TestReadBoundary_PlanSupervisorGrepReach(t *testing.T) {
	cfg := &config.Config{}
	if !coreagent.SeedConfig(cfg) {
		t.Fatal("coreagent.SeedConfig reported no change on an empty config")
	}
	var seeded *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(coreagent.IDPlanSupervisor) {
			seeded = &cfg.Agents.List[i]
		}
	}
	if seeded == nil || seeded.Tools == nil {
		t.Fatal("precondition: the Plan Supervisor must be seeded with an explicit tools policy")
	}
	pol := seeded.Tools.Builtin.Policies
	if pol["grep"] != config.ToolPolicyAllow || pol["read_file"] != config.ToolPolicyDeny {
		t.Fatalf("precondition (S-4.9 Given): shipped seed must be grep=allow, read_file=deny; got grep=%q read_file=%q",
			pol["grep"], pol["read_file"])
	}

	f := newRBFixture(t)
	rbWrite(t, filepath.Join(f.home, "sessions", "s1.jsonl"), "{\"content\":\"needle in a plan session\"}\n")
	rbWrite(t, filepath.Join(f.home, "master.key"), "MASTER-KEY-MATERIAL\n")
	psCtx := WithTurnWorkspaceDir(WithAgentID(context.Background(), string(coreagent.IDPlanSupervisor)), f.ws)
	if ReadConfined(psCtx) {
		t.Fatal("D10: the Plan Supervisor's turn must not be read-confined")
	}

	sessions := filepath.Join(f.home, "sessions")
	res := f.grep.Execute(psCtx, map[string]any{"pattern": "needle", "path": sessions})
	if res.IsError {
		t.Fatalf("S-4.9: the Plan Supervisor's grep of $OMNIPUS_HOME/sessions must run (D10 accepted risk), got: %s", res.ForLLM)
	}
	want := []string{rbSlash(filepath.Join(sessions, "s1.jsonl"))}
	if got := rbHitPaths(res.ForLLM); !rbSameSet(got, want) {
		t.Fatalf("S-4.9: match paths = %q, want exactly %q", got, want)
	}

	key := f.grep.Execute(psCtx, map[string]any{"pattern": "needle", "path": filepath.Join(f.home, "master.key")})
	if !key.IsError || strings.Contains(key.ForLLM, "MASTER-KEY-MATERIAL") {
		t.Fatalf("S-4.9: master.key must stay refused; IsError=%v: %s", key.IsError, key.ForLLM)
	}
}
