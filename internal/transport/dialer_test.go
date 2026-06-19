// dialer_test.go — tests for Dialer reconnect loop and batch delivery.
package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/massa-platform/tetherdb/internal/connector"
)

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
		scheme:    "ws",
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
		// On second connection: serve one batch and ACK it.
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

	// The batch is re-queued when the first connection drops before ACK arrives.
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
