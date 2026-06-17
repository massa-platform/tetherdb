package sqlserver

import (
	"context"
	"database/sql"
)

// namedArg creates a named query parameter.
func namedArg(name string, value any) sql.NamedArg {
	return sql.Named(name, value)
}

// scanner abstracts a single-row result for testability.
type scanner interface {
	Scan(dest ...any) error
}

// rowsScanner abstracts a multi-row result for testability.
type rowsScanner interface {
	Columns() ([]string, error)
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// querier abstracts database query operations for testability.
//
// The production implementation wraps *sql.DB. Tests supply a fakeQuerier.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) scanner
	QueryContext(ctx context.Context, query string, args ...any) (rowsScanner, error)
}

// dbQuerier wraps *sql.DB to satisfy the querier interface.
type dbQuerier struct{ db *sql.DB }

func (d *dbQuerier) QueryRowContext(ctx context.Context, query string, args ...any) scanner {
	return d.db.QueryRowContext(ctx, query, args...)
}

func (d *dbQuerier) QueryContext(ctx context.Context, query string, args ...any) (rowsScanner, error) {
	return d.db.QueryContext(ctx, query, args...)
}
