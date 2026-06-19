// handshake_test.go — tests for Hello/HelloAck handshake validation.
package transport

import (
	"context"
	"testing"
)

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
