package sqlserver

import (
	"context"
	"fmt"
	"io"

	"github.com/massa-platform/tetherdb/internal/connector"
)

// rowStream implements connector.RowStream backed by a live database cursor.
type rowStream struct {
	rows    rowsScanner
	columns []string
}

// InitialSync opens a forward-only scan of all rows in table, resuming from cursor.
//
// Rows are returned ordered by primary key. If cursor.LastPK is non-nil, rows with
// a PK value at or below LastPK are skipped, allowing resumption after an interruption.
// The caller must close the returned RowStream when done.
//
// Example:
//
//	stream, err := conn.InitialSync(ctx, "dbo.Orders", connector.InitialCursor{})
//	if err != nil { ... }
//	defer stream.Close()
func (c *Connector) InitialSync(ctx context.Context, table string, cursor connector.InitialCursor) (connector.RowStream, error) {
	schema, name, err := splitTable(table)
	if err != nil {
		return nil, connErr(ErrInvalidConfig, fmt.Sprintf("invalid table %q", table), err)
	}

	pkCols, err := primaryKeyColumns(ctx, c.q, schema, name)
	if err != nil {
		return nil, err
	}

	rs, err := c.scanTable(ctx, schema, name, pkCols, cursor)
	if err != nil {
		return nil, err
	}

	cols, err := rs.Columns()
	if err != nil {
		rs.Close()
		return nil, connErr(ErrConnection, "read column names", err)
	}

	return &rowStream{rows: rs, columns: cols}, nil
}

// Next returns the next row from the stream, or (nil, io.EOF) when exhausted.
//
// Returns an error if the database connection is lost mid-scan.
//
// Example:
//
//	row, err := stream.Next(ctx)
//	if errors.Is(err, io.EOF) { break }
func (rs *rowStream) Next(_ context.Context) (connector.Row, error) {
	if !rs.rows.Next() {
		if err := rs.rows.Err(); err != nil {
			return nil, fmt.Errorf("sqlserver: initial sync row advance: %w", err)
		}
		return nil, io.EOF
	}

	vals := make([]any, len(rs.columns))
	ptrs := make([]any, len(rs.columns))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	if err := rs.rows.Scan(ptrs...); err != nil {
		return nil, fmt.Errorf("sqlserver: initial sync row scan: %w", err)
	}

	row := make(connector.Row, len(rs.columns))
	for i, col := range rs.columns {
		row[col] = vals[i]
	}
	return row, nil
}

// Close releases the underlying database cursor.
//
// Example:
//
//	defer stream.Close()
func (rs *rowStream) Close() error {
	return rs.rows.Close()
}

// primaryKeyColumns returns the PK column names for the given table, in key ordinal order.
func primaryKeyColumns(ctx context.Context, q querier, schema, table string) ([]string, error) {
	const qry = `
		SELECT c.COLUMN_NAME
		FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc
		JOIN INFORMATION_SCHEMA.CONSTRAINT_COLUMN_USAGE c
		  ON c.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
		 AND c.TABLE_SCHEMA    = tc.TABLE_SCHEMA
		 AND c.TABLE_NAME      = tc.TABLE_NAME
		WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY'
		  AND tc.TABLE_SCHEMA    = @schema
		  AND tc.TABLE_NAME      = @table
		ORDER BY c.COLUMN_NAME`

	rows, err := q.QueryContext(ctx, qry,
		namedArg("schema", schema),
		namedArg("table", table),
	)
	if err != nil {
		return nil, connErr(ErrConnection, fmt.Sprintf("query PK columns for %s.%s", schema, table), err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, connErr(ErrConnection, "scan PK column name", err)
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		return nil, connErr(ErrConnection, "iterate PK columns", err)
	}
	return cols, nil
}

// scanTable opens a forward-only scan of the table, skipping rows at or below cursor.LastPK.
//
// Because column names cannot be parameterised in SQL, we use INFORMATION_SCHEMA-validated
// schema/table values (verified by Probe) to build the query. The PK value in the WHERE
// clause IS parameterised — no injection risk.
func (c *Connector) scanTable(ctx context.Context, schema, name string, pkCols []string, cursor connector.InitialCursor) (rowsScanner, error) {
	// Safe: schema and name were validated against INFORMATION_SCHEMA by Probe/tableExists.
	q := fmt.Sprintf("SELECT * FROM [%s].[%s]", schema, name)

	if cursor.LastPK != nil && len(pkCols) == 1 {
		// Single-column PK resume — skip rows at or below the last delivered key.
		q += fmt.Sprintf(" WHERE [%s] > @lastpk", pkCols[0])
	}

	if len(pkCols) > 0 {
		q += fmt.Sprintf(" ORDER BY [%s]", pkCols[0])
		for _, col := range pkCols[1:] {
			q += fmt.Sprintf(", [%s]", col)
		}
	}

	if cursor.LastPK != nil && len(pkCols) == 1 {
		return c.q.QueryContext(ctx, q, namedArg("lastpk", cursor.LastPK[pkCols[0]]))
	}
	return c.q.QueryContext(ctx, q)
}
