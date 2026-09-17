// Omnipus — ADR-068 D16.6 / spec AC-8.10: the single path to the driver.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"database/sql"
)

// ---------------------------------------------------------------------------
// THIS FILE IS THE ONLY PLACE IN THE PACKAGE THAT MAY TOUCH database/sql's
// Exec/Query methods.
//
// That is not a style preference. AC-8.10's recorder is only a control if it is
// UNAVOIDABLE, and a recorder you can go around is a recorder that reports green
// while the thing it watches is happening somewhere else. sqlgate_test.go reads
// the package's own source and fails the build if `.ExecContext(`,
// `.QueryContext(` or `.QueryRowContext(` appears outside this file.
// ---------------------------------------------------------------------------

// record appends one statement. It is here rather than in recorder.go so the
// no-SQLite build carries no writer it never calls.
func (r *Recorder) record(phase Phase, sql string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = append(r.stmts, Statement{Phase: phase, SQL: sql})
}

func (ix *Index) exec(ctx context.Context, phase Phase, query string, args ...any) (sql.Result, error) {
	ix.rec.record(phase, query)
	return ix.db.ExecContext(ctx, query, args...)
}

func (ix *Index) execTx(ctx context.Context, tx *sql.Tx, phase Phase, query string, args ...any) (sql.Result, error) {
	ix.rec.record(phase, query)
	return tx.ExecContext(ctx, query, args...)
}

func (ix *Index) query(ctx context.Context, phase Phase, query string, args ...any) (*sql.Rows, error) {
	ix.rec.record(phase, query)
	return ix.db.QueryContext(ctx, query, args...)
}

func (ix *Index) queryRow(ctx context.Context, phase Phase, query string, args ...any) *sql.Row {
	ix.rec.record(phase, query)
	return ix.db.QueryRowContext(ctx, query, args...)
}

func (ix *Index) queryRowTx(ctx context.Context, tx *sql.Tx, phase Phase, query string, args ...any) *sql.Row {
	ix.rec.record(phase, query)
	return tx.QueryRowContext(ctx, query, args...)
}
