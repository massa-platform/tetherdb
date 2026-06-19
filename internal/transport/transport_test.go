// transport_test.go — unit tests for the WebSocket transport layer.
// All tests use in-process connections (net.Pipe or httptest) — no real TLS required.
package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/massa-platform/tetherdb/internal/connector"
)

// pipeConn creates a pair of in-process WebSocket connections via an httptest server.
// Returns (client, server) Conn pointers ready for use.
func pipeConn(t *testing.T) (*Conn, *Conn) {
	t.Helper()
	serverConnCh := make(chan *Conn, 1)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("pipeConn upgrade: %v", err)
			return
		}
		serverConnCh <- NewConn(ws)
		<-done
	}))
	t.Cleanup(func() {
		close(done)
		srv.Close()
	})

	u := "ws" + srv.URL[4:] // http://... → ws://...
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("pipeConn dial: %v", err)
	}
	return NewConn(ws), <-serverConnCh
}

func TestConn_SendRecvHello(t *testing.T) {
	client, server := pipeConn(t)
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	sent := Hello{Type: "hello", NodeName: "node-a", Subscribe: []string{"dbo.Orders"}}

	errCh := make(chan error, 1)
	var got any
	go func() {
		var err error
		got, err = server.Recv(ctx)
		errCh <- err
	}()

	if err := client.Send(ctx, sent); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Recv: %v", err)
	}

	h, ok := got.(Hello)
	if !ok {
		t.Fatalf("expected Hello, got %T", got)
	}
	if h.NodeName != "node-a" {
		t.Errorf("NodeName: got %q, want %q", h.NodeName, "node-a")
	}
	if len(h.Subscribe) != 1 || h.Subscribe[0] != "dbo.Orders" {
		t.Errorf("Subscribe: got %v", h.Subscribe)
	}
}

func TestConn_SendRecvChangeBatch(t *testing.T) {
	client, server := pipeConn(t)
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	changes := []connector.Change{
		{Schema: "dbo", Table: "Orders", Op: connector.Insert, PK: map[string]any{"id": 1}},
		{Schema: "dbo", Table: "Orders", Op: connector.Update, PK: map[string]any{"id": 2}},
	}
	sent := ChangeBatch{Type: "change_batch", BatchID: 7, Changes: changes}

	errCh := make(chan error, 1)
	var got any
	go func() {
		var err error
		got, err = server.Recv(ctx)
		errCh <- err
	}()

	if err := client.Send(ctx, sent); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Recv: %v", err)
	}

	b, ok := got.(ChangeBatch)
	if !ok {
		t.Fatalf("expected ChangeBatch, got %T", got)
	}
	if b.BatchID != 7 {
		t.Errorf("BatchID: got %d, want 7", b.BatchID)
	}
	if len(b.Changes) != 2 {
		t.Errorf("Changes len: got %d, want 2", len(b.Changes))
	}
}

func TestConn_SendRecvAck(t *testing.T) {
	client, server := pipeConn(t)
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	errCh := make(chan error, 1)
	var got any
	go func() {
		var err error
		got, err = client.Recv(ctx)
		errCh <- err
	}()

	if err := server.Send(ctx, Ack{Type: "ack", BatchID: 3}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Recv: %v", err)
	}

	a, ok := got.(Ack)
	if !ok {
		t.Fatalf("expected Ack, got %T", got)
	}
	if a.BatchID != 3 {
		t.Errorf("BatchID: got %d, want 3", a.BatchID)
	}
}

func TestConn_SendRecvNack(t *testing.T) {
	client, server := pipeConn(t)
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	errCh := make(chan error, 1)
	var got any
	go func() {
		var err error
		got, err = client.Recv(ctx)
		errCh <- err
	}()

	if err := server.Send(ctx, Nack{Type: "nack", BatchID: 5, Reason: "write failed"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Recv: %v", err)
	}

	n, ok := got.(Nack)
	if !ok {
		t.Fatalf("expected Nack, got %T", got)
	}
	if n.BatchID != 5 || n.Reason != "write failed" {
		t.Errorf("Nack: got %+v", n)
	}
}

func TestConn_UnknownMessageType(t *testing.T) {
	client, server := pipeConn(t)
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		_, err := server.Recv(ctx)
		errCh <- err
	}()

	// Send raw JSON with an unknown type field directly.
	if err := client.ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"bogus"}`)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	err := <-errCh
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

// --- Handshake tests using httptest ---

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// handshakeServer creates an httptest server that acts as a minimal sink:
// it performs the Hello/HelloAck handshake, then invokes fn with the Conn.
func handshakeServer(t *testing.T, published map[string]bool, fn func(*Conn)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		conn := NewConn(ws)
		defer conn.Close()

		msg, err := conn.Recv(context.Background())
		if err != nil {
			t.Errorf("recv hello: %v", err)
			return
		}
		h, ok := msg.(Hello)
		if !ok {
			_ = conn.Send(context.Background(), HelloAck{Type: "hello_ack", Accepted: false, Reason: "expected Hello"})
			return
		}
		for _, tbl := range h.Subscribe {
			if !published[tbl] {
				_ = conn.Send(context.Background(), HelloAck{
					Type:     "hello_ack",
					Accepted: false,
					Reason:   "table not published: " + tbl,
				})
				return
			}
		}
		_ = conn.Send(context.Background(), HelloAck{Type: "hello_ack", Accepted: true})
		if fn != nil {
			fn(conn)
		}
	}))
}

func dialTestServer(t *testing.T, srv *httptest.Server) *Conn {
	t.Helper()
	u := "ws" + srv.URL[4:] // http://... → ws://...
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return NewConn(ws)
}

func TestHandshake_Accepted(t *testing.T) {
	published := map[string]bool{"dbo.Orders": true}
	srv := handshakeServer(t, published, nil)
	defer srv.Close()

	conn := dialTestServer(t, srv)
	defer conn.Close()

	ctx := context.Background()
	if err := conn.Send(ctx, Hello{Type: "hello", NodeName: "src", Subscribe: []string{"dbo.Orders"}}); err != nil {
		t.Fatalf("Send Hello: %v", err)
	}
	msg, err := conn.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv HelloAck: %v", err)
	}
	ack, ok := msg.(HelloAck)
	if !ok {
		t.Fatalf("expected HelloAck, got %T", msg)
	}
	if !ack.Accepted {
		t.Errorf("expected Accepted=true, got Reason=%q", ack.Reason)
	}
}

func TestHandshake_Rejected(t *testing.T) {
	published := map[string]bool{"dbo.Orders": true}
	srv := handshakeServer(t, published, nil)
	defer srv.Close()

	conn := dialTestServer(t, srv)
	defer conn.Close()

	ctx := context.Background()
	// Subscribe to a table the sink does not publish.
	if err := conn.Send(ctx, Hello{Type: "hello", NodeName: "src", Subscribe: []string{"dbo.Secret"}}); err != nil {
		t.Fatalf("Send Hello: %v", err)
	}
	msg, err := conn.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv HelloAck: %v", err)
	}
	ack, ok := msg.(HelloAck)
	if !ok {
		t.Fatalf("expected HelloAck, got %T", msg)
	}
	if ack.Accepted {
		t.Error("expected Accepted=false for unpublished table")
	}
	if ack.Reason == "" {
		t.Error("expected non-empty Reason on rejection")
	}
}

// --- Dialer tests ---

func TestDialer_SendBatchAck(t *testing.T) {
	published := map[string]bool{"dbo.Orders": true}
	srv := handshakeServer(t, published, func(conn *Conn) {
		ctx := context.Background()
		msg, err := conn.Recv(ctx)
		if err != nil {
			t.Errorf("server recv batch: %v", err)
			return
		}
		b, ok := msg.(ChangeBatch)
		if !ok {
			t.Errorf("expected ChangeBatch, got %T", msg)
			return
		}
		_ = conn.Send(ctx, Ack{Type: "ack", BatchID: b.BatchID})
	})
	defer srv.Close()

	cfg := DialerConfig{
		NodeName:  "src",
		Address:   srv.Listener.Addr().String(),
		Subscribe: []string{"dbo.Orders"},
		// TLS is not used for httptest plain HTTP server
		TLSSkipVerify: false,
		scheme:        "ws",
	}
	d := NewDialer(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = d.Run(ctx) }()

	batch := ChangeBatch{
		Type:    "change_batch",
		BatchID: 1,
		Changes: []connector.Change{{Schema: "dbo", Table: "Orders", Op: connector.Insert}},
	}
	if err := d.SendBatch(ctx, batch); err != nil {
		t.Fatalf("SendBatch: %v", err)
	}

	select {
	case ack := <-d.Acks:
		if ack.BatchID != 1 {
			t.Errorf("Ack BatchID: got %d, want 1", ack.BatchID)
		}
	case nack := <-d.Nacks:
		t.Fatalf("unexpected Nack: %+v", nack)
	case <-ctx.Done():
		t.Fatal("timed out waiting for Ack")
	}
}

func TestDialer_SendBatchNack(t *testing.T) {
	published := map[string]bool{"dbo.Orders": true}
	srv := handshakeServer(t, published, func(conn *Conn) {
		ctx := context.Background()
		msg, err := conn.Recv(ctx)
		if err != nil {
			return
		}
		b, _ := msg.(ChangeBatch)
		_ = conn.Send(ctx, Nack{Type: "nack", BatchID: b.BatchID, Reason: "apply failed"})
	})
	defer srv.Close()

	cfg := DialerConfig{
		NodeName:  "src",
		Address:   srv.Listener.Addr().String(),
		Subscribe: []string{"dbo.Orders"},
		scheme:    "ws",
	}
	d := NewDialer(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = d.Run(ctx) }()

	batch := ChangeBatch{Type: "change_batch", BatchID: 2, Changes: nil}
	if err := d.SendBatch(ctx, batch); err != nil {
		t.Fatalf("SendBatch: %v", err)
	}

	select {
	case nack := <-d.Nacks:
		if nack.BatchID != 2 || nack.Reason != "apply failed" {
			t.Errorf("Nack: got %+v", nack)
		}
	case <-d.Acks:
		t.Fatal("unexpected Ack")
	case <-ctx.Done():
		t.Fatal("timed out waiting for Nack")
	}
}

func TestDialer_ReconnectsOnDrop(t *testing.T) {
	connectCount := 0
	published := map[string]bool{"dbo.Orders": true}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn := NewConn(ws)
		connectCount++

		ctx := context.Background()
		msg, err := conn.Recv(ctx)
		if err != nil {
			return
		}
		h, ok := msg.(Hello)
		if !ok {
			conn.Close()
			return
		}
		accepted := true
		for _, tbl := range h.Subscribe {
			if !published[tbl] {
				accepted = false
			}
		}
		if !accepted {
			_ = conn.Send(ctx, HelloAck{Type: "hello_ack", Accepted: false})
			conn.Close()
			return
		}
		_ = conn.Send(ctx, HelloAck{Type: "hello_ack", Accepted: true})

		// On first connection: close immediately to force reconnect.
		if connectCount == 1 {
			conn.Close()
			return
		}
		// On second connection: serve one batch.
		msg, err = conn.Recv(ctx)
		if err != nil {
			return
		}
		b, _ := msg.(ChangeBatch)
		_ = conn.Send(ctx, Ack{Type: "ack", BatchID: b.BatchID})
	}))
	defer srv.Close()

	cfg := DialerConfig{
		NodeName:  "src",
		Address:   srv.Listener.Addr().String(),
		Subscribe: []string{"dbo.Orders"},
		scheme:    "ws",
	}
	d := NewDialer(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() { _ = d.Run(ctx) }()

	// Wait for a successful Ack — this requires reconnect + re-handshake.
	batch := ChangeBatch{Type: "change_batch", BatchID: 10, Changes: nil}
	if err := d.SendBatch(ctx, batch); err != nil {
		t.Fatalf("SendBatch: %v", err)
	}
	select {
	case ack := <-d.Acks:
		if ack.BatchID != 10 {
			t.Errorf("BatchID: got %d, want 10", ack.BatchID)
		}
	case <-ctx.Done():
		t.Fatal("timed out — dialer did not reconnect")
	}
}

func TestDialer_BackoffOnDialFailure(t *testing.T) {
	// Point the dialer at a port with nothing listening.
	cfg := DialerConfig{
		NodeName:  "src",
		Address:   "127.0.0.1:19999",
		Subscribe: []string{"dbo.Orders"},
		scheme:    "ws",
	}
	d := NewDialer(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	_ = d.Run(ctx)
	elapsed := time.Since(start)

	// With backoff the dialer should not spin tightly. 3 s timeout with 1 s first
	// backoff means at most ~3 attempts. If it spins it would attempt hundreds.
	if elapsed < 1*time.Second {
		t.Errorf("dialer exited too quickly (%v) — expected backoff to slow retries", elapsed)
	}
}

// --- Listener tests ---

func TestListener_MultipleConnections(t *testing.T) {
	published := map[string]bool{"dbo.Orders": true}
	results := make(chan string, 2)

	handler := func(ctx context.Context, nodeName string, conn *Conn) {
		results <- nodeName
	}

	l := NewListener(ListenerConfig{
		Address:   "127.0.0.1:0",
		Published: published,
		Handler:   handler,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ready := make(chan string, 1)
	l.onReady = func(addr string) { ready <- addr }

	go func() { _ = l.Start(ctx) }()

	addr := <-ready

	// Connect two clients.
	for _, name := range []string{"node-a", "node-b"} {
		ws, _, err := websocket.DefaultDialer.Dial("ws://"+addr, nil)
		if err != nil {
			t.Fatalf("dial %s: %v", name, err)
		}
		conn := NewConn(ws)
		if err := conn.Send(ctx, Hello{Type: "hello", NodeName: name, Subscribe: []string{"dbo.Orders"}}); err != nil {
			t.Fatalf("send hello %s: %v", name, err)
		}
		msg, err := conn.Recv(ctx)
		if err != nil {
			t.Fatalf("recv hello_ack %s: %v", name, err)
		}
		ack := msg.(HelloAck)
		if !ack.Accepted {
			t.Fatalf("%s rejected: %s", name, ack.Reason)
		}
	}

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case name := <-results:
			seen[name] = true
		case <-ctx.Done():
			t.Fatal("timed out waiting for handler calls")
		}
	}
	if !seen["node-a"] || !seen["node-b"] {
		t.Errorf("not all handlers called, seen: %v", seen)
	}
}
