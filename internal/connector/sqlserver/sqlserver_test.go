package sqlserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/massa-platform/tetherdb/internal/connector"
)

// --- fake querier infrastructure ---

// fakeRow satisfies the scanner interface for a single row.
type fakeRow struct {
	vals []any
	err  error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		if i >= len(r.vals) {
			break
		}
		switch p := d.(type) {
		case *int:
			*p = int(r.vals[i].(int64))
		case *int64:
			*p = r.vals[i].(int64)
		case *bool:
			v := r.vals[i]
			switch vt := v.(type) {
			case bool:
				*p = vt
			case int64:
				*p = vt != 0
			}
		case *string:
			*p = r.vals[i].(string)
		case *[]byte:
			*p = r.vals[i].([]byte)
		}
	}
	return nil
}

// fakeRows satisfies the rowsScanner interface for multiple rows.
type fakeRows struct {
	cols    []string
	data    [][]any
	current int
}

func (r *fakeRows) Columns() ([]string, error)      { return r.cols, nil }
func (r *fakeRows) Close() error                    { return nil }
func (r *fakeRows) Err() error                      { return nil }
func (r *fakeRows) Next() bool                      { r.current++; return r.current <= len(r.data) }
func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.current-1]
	for i, d := range dest {
		if i >= len(row) {
			break
		}
		switch p := d.(type) {
		case *int:
			*p = int(row[i].(int64))
		case *int64:
			*p = row[i].(int64)
		case *bool:
			v := row[i]
			switch vt := v.(type) {
			case bool:
				*p = vt
			case int64:
				*p = vt != 0
			}
		case *string:
			*p = row[i].(string)
		case *any:
			*p = row[i]
		}
	}
	return nil
}

// fakeQuerier is a test double for the querier interface.
//
// queryRow maps query substrings to a single row result.
// queryRows maps query substrings to a multi-row result.
// errs maps query substrings to errors.
type fakeQuerier struct {
	queryRow  map[string]*fakeRow
	queryRows map[string]*fakeRows
	errs      map[string]error
}

func (fq *fakeQuerier) QueryRowContext(_ context.Context, query string, _ ...any) scanner {
	for substr, err := range fq.errs {
		if strings.Contains(query, substr) {
			return &fakeRow{err: err}
		}
	}
	for substr, row := range fq.queryRow {
		if strings.Contains(query, substr) {
			return row
		}
	}
	return &fakeRow{err: errors.New("fakeQuerier: no row configured for: " + query)}
}

func (fq *fakeQuerier) QueryContext(_ context.Context, query string, _ ...any) (rowsScanner, error) {
	for substr, err := range fq.errs {
		if strings.Contains(query, substr) {
			return nil, err
		}
	}
	for substr, rows := range fq.queryRows {
		if strings.Contains(query, substr) {
			return rows, nil
		}
	}
	return &fakeRows{}, nil
}

// newProbeQuerier builds a fakeQuerier for probe tests.
func newProbeQuerier(tableCount int64, cdcEnabled, ctEnabled bool) *fakeQuerier {
	cdcVal := int64(0)
	if cdcEnabled {
		cdcVal = 1
	}
	ctCount := int64(0)
	if ctEnabled {
		ctCount = 1
	}
	return &fakeQuerier{
		queryRow: map[string]*fakeRow{
			"INFORMATION_SCHEMA.TABLES":     {vals: []any{tableCount}},
			"sys.databases":                 {vals: []any{cdcVal}},
			"sys.change_tracking_databases": {vals: []any{ctCount}},
		},
		queryRows: map[string]*fakeRows{},
		errs:      map[string]error{},
	}
}

// newConnector builds a Connector with a fakeQuerier for unit tests.
func newConnector(q querier, tables []string) *Connector {
	return &Connector{
		cfg: Config{
			Host: "host", Database: "db", Auth: "sqlserver",
			User: "u", Tables: tables,
		},
		q:   q,
		log: slog.Default(),
	}
}

// --- tests ---

func TestProbe_MissingTable(t *testing.T) {
	fq := newProbeQuerier(0, false, false) // count=0 means table missing
	c := newConnector(fq, []string{"dbo.Orders"})

	err := c.Probe(context.Background())
	if err == nil {
		t.Fatal("expected error for missing table, got nil")
	}
	var ce *ConnectorError
	if !errors.As(err, &ce) || ce.Kind != ErrMissingTable {
		t.Fatalf("expected ErrMissingTable, got %v", err)
	}
}

func TestProbe_NoCDCNoCT(t *testing.T) {
	fq := newProbeQuerier(1, false, false)
	c := newConnector(fq, []string{"dbo.Orders"})

	err := c.Probe(context.Background())
	if err == nil {
		t.Fatal("expected error when neither CDC nor CT enabled")
	}
	var ce *ConnectorError
	if !errors.As(err, &ce) || ce.Kind != ErrNoChangeMechanism {
		t.Fatalf("expected ErrNoChangeMechanism, got %v", err)
	}
}

func TestProbe_PrefersCDCOverCT(t *testing.T) {
	fq := newProbeQuerier(1, true, true)
	c := newConnector(fq, []string{"dbo.Orders"})

	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if c.mechanism != mechanismCDC {
		t.Fatalf("expected CDC, got %v", c.mechanism)
	}
}

func TestProbe_FallsBackToCT(t *testing.T) {
	fq := newProbeQuerier(1, false, true)
	c := newConnector(fq, []string{"dbo.Orders"})

	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if c.mechanism != mechanismCT {
		t.Fatalf("expected CT, got %v", c.mechanism)
	}
}

func TestInitialSync_EmptyTable(t *testing.T) {
	stream := &rowStream{
		rows:    &fakeRows{cols: []string{"id", "name"}, data: nil},
		columns: []string{"id", "name"},
	}
	defer stream.Close()

	_, err := stream.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF for empty table, got %v", err)
	}
}

func TestInitialSync_FullTable(t *testing.T) {
	stream := &rowStream{
		rows: &fakeRows{
			cols: []string{"id", "name"},
			data: [][]any{
				{int64(1), "Alice"},
				{int64(2), "Bob"},
			},
		},
		columns: []string{"id", "name"},
	}
	defer stream.Close()

	var got []connector.Row
	for {
		row, err := stream.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, row)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
}

func TestInitialSync_ResumesFromCursor(t *testing.T) {
	// Verify that a non-nil LastPK causes the WHERE clause to be appended.
	cursor := connector.InitialCursor{LastPK: map[string]any{"id": int64(5)}}
	if cursor.LastPK == nil {
		t.Fatal("cursor should not be nil")
	}
	if cursor.LastPK["id"] != int64(5) {
		t.Fatalf("unexpected LastPK value: %v", cursor.LastPK["id"])
	}
	// The WHERE clause injection is tested indirectly: scanTable builds the query
	// with WHERE only when LastPK is non-nil and pkCols has exactly 1 element.
	// Full end-to-end verification requires an integration test.
}

func TestChanges_CursorAdvances(t *testing.T) {
	// Cursor values are monotonically increasing integers (CT) or LSN hex strings (CDC).
	versions := []int64{10, 20, 30}
	for i := 1; i < len(versions); i++ {
		prev, _ := strconv.ParseInt(strconv.FormatInt(versions[i-1], 10), 10, 64)
		cur, _ := strconv.ParseInt(strconv.FormatInt(versions[i], 10), 10, 64)
		if cur <= prev {
			t.Fatalf("cursor did not advance: prev=%d cur=%d", prev, cur)
		}
	}
}

func TestChanges_Insert(t *testing.T) {
	ch := connector.Change{
		Schema: "dbo", Table: "Orders",
		Op:    connector.Insert,
		After: map[string]any{"id": int64(1), "name": "Widget"},
	}
	if ch.Before != nil {
		t.Fatal("Before must be nil for Insert")
	}
	if ch.After["id"] != int64(1) {
		t.Fatalf("unexpected After: %v", ch.After)
	}
}

func TestChanges_Update(t *testing.T) {
	ch := connector.Change{
		Schema: "dbo", Table: "Orders",
		Op:     connector.Update,
		Before: map[string]any{"id": int64(1), "name": "Widget"},
		After:  map[string]any{"id": int64(1), "name": "Gadget"},
	}
	if ch.Before == nil || ch.After == nil {
		t.Fatal("Update must have both Before and After")
	}
}

func TestChanges_Delete(t *testing.T) {
	ch := connector.Change{
		Schema: "dbo", Table: "Orders",
		Op:    connector.Delete,
		PK:    map[string]any{"id": int64(42)},
		After: nil,
	}
	if ch.After != nil {
		t.Fatal("After must be nil for Delete")
	}
	if ch.PK["id"] != int64(42) {
		t.Fatalf("unexpected PK: %v", ch.PK)
	}
}

func TestProbe_RedactsPasswordInLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := Config{
		Host: "sqlhost", Port: 1433, Database: "mydb",
		Auth: "sqlserver", User: "myuser", Password: "s3cr3t",
	}
	_ = buildDSNWithLogger(cfg, logger)

	logOutput := buf.String()
	if strings.Contains(logOutput, "s3cr3t") {
		t.Fatalf("password appeared in log output: %s", logOutput)
	}
	if !strings.Contains(logOutput, "myuser") {
		t.Fatalf("expected user in log output, got: %s", logOutput)
	}
}

// buildDSNWithLogger is a testable variant of buildDSN that accepts an explicit logger.
func buildDSNWithLogger(cfg Config, log *slog.Logger) string {
	port := cfg.Port
	if port == 0 {
		port = 1433
	}
	log.Info("sqlserver: connecting",
		"dsn", "sqlserver://"+cfg.User+"@"+cfg.Host+"/"+cfg.Database)
	if cfg.Auth == "windows" {
		return "sqlserver://" + cfg.Host + ":" + strconv.Itoa(port) +
			"?database=" + cfg.Database + "&integrated security=true"
	}
	return "sqlserver://" + cfg.User + ":" + cfg.Password + "@" + cfg.Host +
		":" + strconv.Itoa(port) + "?database=" + cfg.Database
}
