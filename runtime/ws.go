package goduct

import (
	"context"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// WSConn[S, C] is the typed WebSocket connection goduct hands to a
// handler. It wraps a *coder/websocket.Conn with two type parameters:
// S is the server→client message shape (what Send takes), C is the
// client→server message shape (what Recv returns). Per ADR 0044 §4.
//
// The user constructs nothing — the generated adapter calls
// NewWSConn after a successful upgrade and passes the conn to the
// handler. The handler may call Send / Recv / Close until either
// returns an error or the ctx the caller passes in fires.
type WSConn[S any, C any] struct {
	conn *websocket.Conn
}

// NewWSConn wraps a coder/websocket.Conn in the typed surface.
// Called by goadapter-generated wrappers; not normally invoked by
// user code.
func NewWSConn[S any, C any](c *websocket.Conn) *WSConn[S, C] {
	return &WSConn[S, C]{conn: c}
}

// Send writes msg as a JSON text frame. Returns when the frame
// has been written or ctx is canceled. JSON encoding errors and
// I/O errors both surface as error.
func (c *WSConn[S, C]) Send(ctx context.Context, msg S) error {
	return wsjson.Write(ctx, c.conn, msg)
}

// Recv reads the next JSON text frame, decoding it into C.
// Returns when a complete message has been received, ctx is
// canceled, or the connection closes. On close the returned
// error reports the close code; the zero-value C is returned.
func (c *WSConn[S, C]) Recv(ctx context.Context) (C, error) {
	var msg C
	err := wsjson.Read(ctx, c.conn, &msg)
	return msg, err
}

// Close sends a close frame with the given status code and reason,
// then waits for the peer to acknowledge. websocket.StatusNormalClosure
// (1000) is the appropriate code for an ordinary end-of-stream.
func (c *WSConn[S, C]) Close(code websocket.StatusCode, reason string) error {
	return c.conn.Close(code, reason)
}
