package pipeline

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
)

// This file implements a minimal, hand-rolled database/sql/driver so the
// Repository methods (which take a *sql.DB) can be unit tested without a
// real database or a third-party mocking library. Each test supplies a
// queryFn and/or execFn; every *sql.DB call (Query, QueryRow, Exec via a
// prepared statement) is routed through them.
//
// *sql.Rows and *sql.Row can only be constructed by the database/sql
// package itself, so the fake plugs in one level down, at the
// database/sql/driver boundary, and lets the standard library do the rest.

var fakeDriverSeq int64

// fakeRow is a single row of driver values, in column order.
type fakeRow []driver.Value

type fakeConn struct {
	queryFn func(query string, args []driver.Value) (columns []string, rows []fakeRow, err error)
	execFn  func(query string, args []driver.Value) (lastInsertID, rowsAffected int64, err error)
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return &fakeStmt{query: query, conn: c}, nil
}
func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("fakeConn: transactions not supported")
}

type fakeStmt struct {
	query string
	conn  *fakeConn
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 } // -1 disables driver-side arg count validation

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	if s.conn.execFn == nil {
		return nil, fmt.Errorf("fakeStmt: no execFn configured for query: %s", s.query)
	}
	lastID, affected, err := s.conn.execFn(s.query, args)
	if err != nil {
		return nil, err
	}
	return fakeResult{lastInsertID: lastID, rowsAffected: affected}, nil
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.conn.queryFn == nil {
		return nil, fmt.Errorf("fakeStmt: no queryFn configured for query: %s", s.query)
	}
	cols, rows, err := s.conn.queryFn(s.query, args)
	if err != nil {
		return nil, err
	}
	return &fakeRows{columns: cols, rows: rows}, nil
}

type fakeRows struct {
	columns []string
	rows    []fakeRow
	pos     int
}

func (r *fakeRows) Columns() []string { return r.columns }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

type fakeResult struct {
	lastInsertID int64
	rowsAffected int64
}

func (r fakeResult) LastInsertId() (int64, error) { return r.lastInsertID, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

// fakeDriverWrapper adapts a single *fakeConn into a driver.Driver: Open just
// returns the same conn regardless of the DSN, since a test only ever needs
// one canned connection.
type fakeDriverWrapper struct{ conn *fakeConn }

func (d *fakeDriverWrapper) Open(name string) (driver.Conn, error) { return d.conn, nil }

// newFakeDB registers a uniquely-named driver backed by conn and opens a
// *sql.DB against it, closing the DB automatically at test cleanup.
func newFakeDB(t *testing.T, conn *fakeConn) *sql.DB {
	t.Helper()

	name := fmt.Sprintf("fakesql-%d", atomic.AddInt64(&fakeDriverSeq, 1))
	sql.Register(name, &fakeDriverWrapper{conn: conn})

	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("failed to open fake sql db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db
}
