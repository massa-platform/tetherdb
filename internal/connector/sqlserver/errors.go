// Package sqlserver implements the connector.Reader interface for Microsoft SQL Server.
//
// It supports both CDC (Change Data Capture) and Change Tracking as change mechanisms,
// preferring CDC when both are available. Authentication supports SQL Server login
// and Windows Authentication.
//
// Depends on: connector, go-mssqldb
// Used by: pipeline engine
package sqlserver

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
	// ErrConnection means the SQL Server was unreachable or rejected the connection.
	ErrConnection ErrorKind = iota
	// ErrAuth means authentication failed.
	ErrAuth
	// ErrMissingTable means a configured table was not found in the database.
	ErrMissingTable
	// ErrNoChangeMechanism means neither CDC nor Change Tracking is enabled.
	ErrNoChangeMechanism
	// ErrDecode means a row value could not be decoded to a supported Go type.
	ErrDecode
	// ErrInvalidConfig means a configuration value failed validation.
	ErrInvalidConfig
)

// Error implements the error interface.
func (e *ConnectorError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("sqlserver: %s: %v", e.Detail, e.Cause)
	}
	return fmt.Sprintf("sqlserver: %s", e.Detail)
}

// Unwrap returns the underlying cause so errors.Is/As work through the chain.
func (e *ConnectorError) Unwrap() error {
	return e.Cause
}

func connErr(kind ErrorKind, detail string, cause error) *ConnectorError {
	return &ConnectorError{Kind: kind, Detail: detail, Cause: cause}
}
