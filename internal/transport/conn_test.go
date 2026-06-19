// conn_test.go — tests for Conn typed send/receive.
package transport

import (
	"context"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/massa-platform/tetherdb/internal/connector"
)

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
