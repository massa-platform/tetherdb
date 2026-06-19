// helpers_test.go — shared test utilities for the transport package.
package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

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

// dialTestServer dials the given httptest server and returns a Conn.
func dialTestServer(t *testing.T, srv *httptest.Server) *Conn {
	t.Helper()
	u := "ws" + srv.URL[4:] // http://... → ws://...
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return NewConn(ws)
}
