// Package connector defines the shared interfaces and types used by all database connectors.
//
// Every connector (SQL Server, Postgres, etc.) implements the Reader or Writer interface
// defined here. The sync engine depends only on this package — it never imports a driver.
//
// Depends on: nothing
// Used by: connector/sqlserver, connector/postgres, pipeline engine
package connector

import "context"

// Op represents the type of change applied to a row.
type Op int

const (
	// Insert indicates a row was added.
	Insert Op = iota
	// Update indicates an existing row was modified.
	Update
	// Delete indicates a row was removed.
	Delete
)

// Change represents a single row-level change captured from a source database.
//
// For Insert, Before is nil. For Delete, After is nil. For Update, both are populated.
// PK is always populated and identifies the row.
type Change struct {
	Schema string
	Table  string
	Op     Op
	PK     map[string]any
	Before map[string]any
	After  map[string]any
}

// Row is a single row read during an initial full-table sync.
type Row map[string]any

// InitialCursor tracks progress through a full-table initial sync.
//
// LastPK is nil when starting from the beginning of the table. When a sync is
// resumed after interruption, LastPK holds the primary key of the last row that
// was successfully delivered to the caller.
type InitialCursor struct {
	LastPK map[string]any
}

// ChangeCursor is an opaque marker into the change stream.
//
// For CDC it holds a Log Sequence Number; for Change Tracking it holds a version
// number. Callers treat the Value as opaque and pass it back to Changes() on resume.
type ChangeCursor struct {
	Value string
}

// RowStream is a forward-only iterator over rows returned by InitialSync.
//
// The caller must call Close() when done, regardless of whether all rows were consumed.
// Next returns (nil, io.EOF) when the stream is exhausted.
//
// Example:
//
//	stream, err := reader.InitialSync(ctx, "dbo.Orders", connector.InitialCursor{})
//	if err != nil { ... }
//	defer stream.Close()
//	for {
//	    row, err := stream.Next(ctx)
//	    if errors.Is(err, io.EOF) { break }
//	    if err != nil { ... }
//	    // process row
//	}
type RowStream interface {
	// Next returns the next row, or (nil, io.EOF) when exhausted, or a non-nil
	// error if the underlying connection failed.
	Next(ctx context.Context) (Row, error)
	// Close releases resources held by the stream.
	Close() error
}

// ChangeStream is a forward-only iterator over change events from a source database.
//
// The caller must call Close() when done. Next blocks until a change is available,
// the context is cancelled, or an error occurs.
//
// Example:
//
//	stream, err := reader.Changes(ctx, []string{"dbo.Orders"}, cursor)
//	if err != nil { ... }
//	defer stream.Close()
//	for {
//	    change, err := stream.Next(ctx)
//	    if err != nil { ... }
//	    // process change, then advance cursor
//	}
type ChangeStream interface {
	// Next returns the next change. Blocks until one is available.
	// Returns an error if the context is cancelled or the connection is lost.
	Next(ctx context.Context) (Change, error)
	// Cursor returns the opaque position of the most recently returned change.
	// The caller persists this and passes it back to Changes() on resume.
	Cursor() ChangeCursor
	// Close releases resources held by the stream.
	Close() error
}

// Reader is implemented by source database connectors.
//
// A Reader connects to a source database, verifies connectivity and schema,
// performs a full initial sync, then streams ongoing changes. The sync engine
// calls these methods in order: Probe once at startup, InitialSync per table,
// then Changes for the ongoing stream.
//
// Example:
//
//	r, err := sqlserver.New(ctx, cfg)
//	if err != nil { ... }
//	defer r.Close()
//	if err := r.Probe(ctx); err != nil { ... }
type Reader interface {
	// Probe verifies the connection, validates that all configured tables exist,
	// and detects the available change mechanism (CDC or Change Tracking).
	// Returns a descriptive error if any check fails. Must be called before
	// InitialSync or Changes.
	Probe(ctx context.Context) error

	// InitialSync starts a full-table read for the named table, resuming from
	// cursor if provided. Returns a RowStream the caller drains row by row.
	// The table name must have been validated by Probe.
	InitialSync(ctx context.Context, table string, cursor InitialCursor) (RowStream, error)

	// Changes opens a stream of row-level changes for the given tables, starting
	// from the position described by from. The cursor advances only when the caller
	// calls Next() — no change is lost on connection failure.
	Changes(ctx context.Context, tables []string, from ChangeCursor) (ChangeStream, error)

	// Close releases all resources held by the reader, including the database connection.
	Close() error
}

// Writer is implemented by sink database connectors.
//
// A Writer connects to a target database and applies batches of changes atomically.
// The pipeline engine calls Probe once at startup then Apply for each committed batch
// it receives from the source.
//
// Example:
//
//	w, err := postgres.New(ctx, cfg)
//	if err != nil { ... }
//	defer w.Close()
//	if err := w.Probe(ctx); err != nil { ... }
type Writer interface {
	// Probe verifies connectivity and validates that target tables are writable.
	// Returns a descriptive error if any check fails.
	Probe(ctx context.Context) error

	// Apply writes a batch of changes to the target database. Changes within a
	// batch are applied in order. Apply is atomic — either all changes land or none do.
	Apply(ctx context.Context, batch []Change) error

	// Close releases all resources held by the writer.
	Close() error
}
