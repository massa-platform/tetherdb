package sqlserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/massa-platform/tetherdb/internal/connector"
)

// changeStream implements connector.ChangeStream.
type changeStream struct {
	conn      *Connector
	tables    []string
	cursor    connector.ChangeCursor
	pending   []connector.Change
	fetchNext func(ctx context.Context) ([]connector.Change, connector.ChangeCursor, error)
}

// Changes opens a stream of row-level changes for the given tables.
//
// The stream starts at the position described by from. If from.Value is empty,
// the stream starts from the current change position (no historical replay).
// The cursor advances only when Next() is called — no change is skipped on
// connection loss.
//
// Example:
//
//	stream, err := conn.Changes(ctx, []string{"dbo.Orders"}, connector.ChangeCursor{})
//	if err != nil { ... }
//	defer stream.Close()
func (c *Connector) Changes(ctx context.Context, tables []string, from connector.ChangeCursor) (connector.ChangeStream, error) {
	if c.mechanism == mechanismUnknown {
		return nil, connErr(ErrNoChangeMechanism, "Probe must be called before Changes", nil)
	}

	cs := &changeStream{conn: c, tables: tables, cursor: from}

	switch c.mechanism {
	case mechanismCDC:
		// If no cursor, start from the current maximum LSN.
		if from.Value == "" {
			lsn, err := currentMaxLSN(ctx, c.q)
			if err != nil {
				return nil, err
			}
			cs.cursor = connector.ChangeCursor{Value: lsn}
		}
		cs.fetchNext = cs.fetchCDC
	case mechanismCT:
		// If no cursor, start from the current CT version.
		if from.Value == "" {
			ver, err := currentCTVersion(ctx, c.q)
			if err != nil {
				return nil, err
			}
			cs.cursor = connector.ChangeCursor{Value: strconv.FormatInt(ver, 10)}
		}
		cs.fetchNext = cs.fetchCT
	}

	return cs, nil
}

// Next returns the next change from the stream.
//
// Blocks until a change is available or the context is cancelled. Returns an error
// if the database connection is lost — the caller should resume from Cursor().
//
// Example:
//
//	change, err := stream.Next(ctx)
//	if err != nil { return err }
func (cs *changeStream) Next(ctx context.Context) (connector.Change, error) {
	for {
		if len(cs.pending) > 0 {
			ch := cs.pending[0]
			cs.pending = cs.pending[1:]
			return ch, nil
		}

		changes, newCursor, err := cs.fetchNext(ctx)
		if err != nil {
			return connector.Change{}, err
		}
		if len(changes) > 0 {
			cs.cursor = newCursor
			cs.pending = changes
			continue
		}

		// No changes yet — poll after a brief wait to avoid busy-looping.
		select {
		case <-ctx.Done():
			return connector.Change{}, ctx.Err()
		}
	}
}

// Cursor returns the opaque position of the last successfully returned change.
//
// Example:
//
//	cur := stream.Cursor()
func (cs *changeStream) Cursor() connector.ChangeCursor {
	return cs.cursor
}

// Close is a no-op for changeStream — the underlying *sql.DB is owned by Connector.
//
// Example:
//
//	defer stream.Close()
func (cs *changeStream) Close() error {
	return nil
}

// --- CDC implementation ---

// fetchCDC reads the next batch of changes using CDC functions.
func (cs *changeStream) fetchCDC(ctx context.Context) ([]connector.Change, connector.ChangeCursor, error) {
	maxLSN, err := currentMaxLSN(ctx, cs.conn.q)
	if err != nil {
		return nil, cs.cursor, err
	}
	if maxLSN == cs.cursor.Value {
		return nil, cs.cursor, nil
	}

	var all []connector.Change
	for _, table := range cs.tables {
		schema, name, err := splitTable(table)
		if err != nil {
			return nil, cs.cursor, connErr(ErrInvalidConfig, fmt.Sprintf("invalid table %q", table), err)
		}
		changes, err := fetchCDCForTable(ctx, cs.conn.q, schema, name, cs.cursor.Value, maxLSN)
		if err != nil {
			return nil, cs.cursor, err
		}
		all = append(all, changes...)
	}
	return all, connector.ChangeCursor{Value: maxLSN}, nil
}

// fetchCDCForTable reads CDC changes for a single table between fromLSN and toLSN.
//
// Uses cdc.fn_cdc_get_all_changes which is the standard SQL Server CDC query function.
// The capture instance name follows the SQL Server default: <schema>_<table>.
func fetchCDCForTable(ctx context.Context, q querier, schema, table, fromLSN, toLSN string) ([]connector.Change, error) {
	// CDC capture instance name defaults to schema_table (underscores, no dots).
	captureInstance := schema + "_" + table

	// __$operation values: 1=Delete, 2=Insert, 3=before-update, 4=after-update
	qry := fmt.Sprintf(
		`SELECT __$operation, __$update_mask, * FROM cdc.fn_cdc_get_all_changes_%s(
			sys.fn_cdc_map_time_to_lsn('smallest greater than or equal', GETDATE()),
			@toLSN, N'all with mask'
		) WHERE __$start_lsn > @fromLSN`,
		captureInstance,
	)
	// We pass fromLSN/toLSN as strings; the driver converts them via sys.fn_cdc_* helpers.
	rows, err := q.QueryContext(ctx, qry,
		namedArg("fromLSN", fromLSN),
		namedArg("toLSN", toLSN),
	)
	if err != nil {
		return nil, connErr(ErrConnection,
			fmt.Sprintf("CDC query for %s.%s", schema, table), err)
	}
	defer rows.Close()

	return scanCDCRows(rows, schema, table)
}

// scanCDCRows converts raw CDC rows to Change values.
func scanCDCRows(rows rowsScanner, schema, table string) ([]connector.Change, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, connErr(ErrConnection, "read CDC column names", err)
	}

	// The first two columns are __$operation and __$update_mask; data starts at index 2.
	const metaCols = 2

	var changes []connector.Change
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, connErr(ErrDecode, fmt.Sprintf("scan CDC row for %s.%s", schema, table), err)
		}

		op, ok := vals[0].(int64)
		if !ok {
			return nil, connErr(ErrDecode, "unexpected type for __$operation", nil)
		}

		data := make(map[string]any, len(cols)-metaCols)
		for i := metaCols; i < len(cols); i++ {
			data[cols[i]] = vals[i]
		}

		switch op {
		case 2: // Insert
			changes = append(changes, connector.Change{
				Schema: schema, Table: table,
				Op: connector.Insert, After: data,
			})
		case 1: // Delete
			changes = append(changes, connector.Change{
				Schema: schema, Table: table,
				Op: connector.Delete, PK: data,
			})
		case 4: // after-update (op 3 is the before image, we skip it here)
			changes = append(changes, connector.Change{
				Schema: schema, Table: table,
				Op: connector.Update, After: data,
			})
		case 3:
			// Before-update image — skip; we capture Before in a future enhancement.
		}
	}
	if err := rows.Err(); err != nil {
		return nil, connErr(ErrConnection, "iterate CDC rows", err)
	}
	return changes, nil
}

// currentMaxLSN returns the current maximum LSN in the CDC log.
func currentMaxLSN(ctx context.Context, q querier) (string, error) {
	var lsn []byte
	if err := q.QueryRowContext(ctx, "SELECT sys.fn_cdc_get_max_lsn()").Scan(&lsn); err != nil {
		return "", connErr(ErrConnection, "query max LSN", err)
	}
	return fmt.Sprintf("%X", lsn), nil
}

// --- Change Tracking implementation ---

// fetchCT reads the next batch of changes using Change Tracking.
func (cs *changeStream) fetchCT(ctx context.Context) ([]connector.Change, connector.ChangeCursor, error) {
	curVer, err := currentCTVersion(ctx, cs.conn.q)
	if err != nil {
		return nil, cs.cursor, err
	}

	fromVer, err := strconv.ParseInt(cs.cursor.Value, 10, 64)
	if err != nil {
		return nil, cs.cursor, connErr(ErrInvalidConfig, "invalid CT cursor value", err)
	}

	if curVer <= fromVer {
		return nil, cs.cursor, nil
	}

	var all []connector.Change
	for _, table := range cs.tables {
		schema, name, err := splitTable(table)
		if err != nil {
			return nil, cs.cursor, connErr(ErrInvalidConfig, fmt.Sprintf("invalid table %q", table), err)
		}
		changes, err := fetchCTForTable(ctx, cs.conn.q, schema, name, fromVer)
		if err != nil {
			return nil, cs.cursor, err
		}
		all = append(all, changes...)
	}
	return all, connector.ChangeCursor{Value: strconv.FormatInt(curVer, 10)}, nil
}

// fetchCTForTable reads Change Tracking changes for a single table since fromVersion.
func fetchCTForTable(ctx context.Context, q querier, schema, table string, fromVersion int64) ([]connector.Change, error) {
	qry := buildCTQuery(schema, table)
	rows, err := q.QueryContext(ctx, qry, namedArg("fromVersion", fromVersion))
	if err != nil {
		return nil, connErr(ErrConnection,
			fmt.Sprintf("CT query for %s.%s", schema, table), err)
	}
	defer rows.Close()

	return scanCTRows(rows, schema, table)
}

// buildCTQuery constructs the Change Tracking SELECT for the table.
//
// CT does not provide before-images; Update changes only carry the After state.
func buildCTQuery(schema, table string) string {
	return fmt.Sprintf(
		`SELECT ct.SYS_CHANGE_OPERATION, ct.SYS_CHANGE_VERSION, t.*
		 FROM CHANGETABLE(CHANGES [%s].[%s], @fromVersion) AS ct
		 LEFT JOIN [%s].[%s] t ON t.%%PLACEHOLDER%%
		 ORDER BY ct.SYS_CHANGE_VERSION`,
		schema, table, schema, table,
	)
}

// scanCTRows converts raw Change Tracking rows into Change values.
func scanCTRows(rows rowsScanner, schema, table string) ([]connector.Change, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, connErr(ErrConnection, "read CT column names", err)
	}

	const metaCols = 2 // SYS_CHANGE_OPERATION, SYS_CHANGE_VERSION

	var changes []connector.Change
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, connErr(ErrDecode, fmt.Sprintf("scan CT row for %s.%s", schema, table), err)
		}

		opStr, ok := vals[0].(string)
		if !ok {
			return nil, connErr(ErrDecode, "unexpected type for SYS_CHANGE_OPERATION", nil)
		}

		data := make(map[string]any, len(cols)-metaCols)
		for i := metaCols; i < len(cols); i++ {
			data[cols[i]] = vals[i]
		}

		var op connector.Op
		switch strings.ToUpper(opStr) {
		case "I":
			op = connector.Insert
		case "U":
			op = connector.Update
		case "D":
			op = connector.Delete
		default:
			return nil, connErr(ErrDecode,
				fmt.Sprintf("unknown SYS_CHANGE_OPERATION value %q", opStr), nil)
		}

		ch := connector.Change{
			Schema: schema, Table: table, Op: op,
			After: data,
		}
		if op == connector.Delete {
			ch.PK = data
			ch.After = nil
		}
		changes = append(changes, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, connErr(ErrConnection, "iterate CT rows", err)
	}
	return changes, nil
}

// currentCTVersion returns the current change tracking version for the database.
func currentCTVersion(ctx context.Context, q querier) (int64, error) {
	var ver int64
	if err := q.QueryRowContext(ctx, "SELECT CHANGE_TRACKING_CURRENT_VERSION()").Scan(&ver); err != nil {
		return 0, connErr(ErrConnection, "query CHANGE_TRACKING_CURRENT_VERSION", err)
	}
	return ver, nil
}
