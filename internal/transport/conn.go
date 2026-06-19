// conn.go — typed message send/receive over a gorilla WebSocket connection.
package transport

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
)

// Conn wraps a gorilla/websocket connection with typed message send/receive.
//
// All methods are safe to call from a single goroutine only — callers must
// not call Send and Recv concurrently on the same Conn.
//
// Example:
//
//	conn := NewConn(ws)
//	if err := conn.Send(ctx, Hello{Type: "hello", NodeName: "n1"}); err != nil { ... }
//	msg, err := conn.Recv(ctx)
type Conn struct {
	ws *websocket.Conn
}

// NewConn wraps an existing gorilla WebSocket connection.
//
// The caller is responsible for closing the underlying connection when done.
func NewConn(ws *websocket.Conn) *Conn {
	return &Conn{ws: ws}
}

// Send encodes msg as JSON and writes it as a single WebSocket text frame.
//
// Returns an error if ctx is already cancelled, the marshal fails, or the
// underlying write fails.
//
// Example:
//
//	err := conn.Send(ctx, Ack{Type: "ack", BatchID: 42})
func (c *Conn) Send(ctx context.Context, msg any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("transport: send marshal: %w", err)
	}
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("transport: send write: %w", err)
	}
	return nil
}

// Recv reads the next WebSocket text frame and deserialises it into the
// concrete message type identified by the "type" JSON field.
//
// Returns one of Hello, HelloAck, ChangeBatch, Ack, or Nack.
// Returns an error if the frame cannot be read, the JSON is malformed,
// or the type field is unknown.
//
// Example:
//
//	msg, err := conn.Recv(ctx)
//	if err != nil { ... }
//	switch m := msg.(type) {
//	case Ack:  ...
//	case Nack: ...
//	}
func (c *Conn) Recv(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, data, err := c.ws.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("transport: recv read: %w", err)
	}
	return decode(data)
}

// Close closes the underlying WebSocket connection.
//
// Example:
//
//	defer conn.Close()
func (c *Conn) Close() error {
	return c.ws.Close()
}
