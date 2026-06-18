// Package postgres implements the connector.Writer interface for PostgreSQL.
//
// It uses pgx/v5 native API (not database/sql) for proper type handling.
// Authentication and TLS are configured via DSN or explicit fields in Config.
//
// Depends on: connector, pgx/v5
// Used by: pipeline engine, cmd/tetherdb
package postgres

import "fmt"

// ConnectorError wraps a driver or validation error with domain context.
//
// Callers can inspect Kind to distinguish categories of failure without
// parsing error strings.
type ConnectorError struct {
	// Kind classifies the error for programmatic handling.
	Kind ErrorKind
	// Detail is a human-readable description of the specific failure.
	Detail string
	// Cause is the underlying error, if any.
	Cause error
}

// ErrorKind categorises connector errors.
type ErrorKind int

const (
	// ErrConnection means Postgres was unreachable or rejected the connection.
	ErrConnection ErrorKind = iota
	// ErrAuth means authentication failed.
	ErrAuth
	// ErrMissingTable means a target table was not found during Apply.
	ErrMissingTable
	// ErrDecode means a Change value could not be mapped to a Postgres column type.
	ErrDecode
	// ErrInvalidConfig means a configuration value failed validation.
	ErrInvalidConfig
)

// Error implements the error interface.
func (e *ConnectorError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("postgres: %s: %v", e.Detail, e.Cause)
	}
	return fmt.Sprintf("postgres: %s", e.Detail)
}

// Unwrap returns the underlying cause so errors.Is/As work through the chain.
func (e *ConnectorError) Unwrap() error {
	return e.Cause
}

func connErr(kind ErrorKind, detail string, cause error) *ConnectorError {
	return &ConnectorError{Kind: kind, Detail: detail, Cause: cause}
}
