// listener_test.go — tests for Listener inbound connection handling.
package transport

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

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

	// Connect two clients simultaneously.
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
