package goduct

import (
	"context"
	"time"

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
	conn   *websocket.Conn
	pingFn func()
}

// WSOption configures a WSConn at construction time. Options
// compose in source order. Per ADR 0045 §2.
type WSOption func(*wsOpts)

type wsOpts struct {
	pingInterval time.Duration
}

// WithPingInterval spawns a background ping goroutine on the
// constructed WSConn that calls conn.Ping(ctx) every d. Useful
// for keeping long-lived connections alive across intermediary
// timeouts (NAT, proxies, load balancers). Zero / negative
// duration disables the ping goroutine (same as omitting the
// option). The goroutine exits on connection close or context
// cancellation.
func WithPingInterval(d time.Duration) WSOption {
	return func(o *wsOpts) { o.pingInterval = d }
}

// NewWSConn wraps a coder/websocket.Conn in the typed surface.
// Called by goadapter-generated wrappers; not normally invoked by
// user code. Options per ADR 0045 §2 (currently just
// WithPingInterval).
func NewWSConn[S any, C any](c *websocket.Conn, opts ...WSOption) *WSConn[S, C] {
	var o wsOpts
	for _, opt := range opts {
		opt(&o)
	}
	w := &WSConn[S, C]{conn: c}
	if o.pingInterval > 0 {
		w.startPinger(o.pingInterval)
	}
	return w
}

// startPinger spawns a background goroutine that pings the peer
// every d. Exits when the connection closes or when stopPinger
// is called via Close. coder/websocket's Conn.Ping uses its own
// context per call; we wrap it in a fresh context.Background
// since the user's handler ctx may go through Recv/Send calls
// concurrently.
func (c *WSConn[S, C]) startPinger(d time.Duration) {
	stop := make(chan struct{})
	c.pingFn = func() { close(stop) }
	go func() {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), d)
				_ = c.conn.Ping(ctx)
				cancel()
			}
		}
	}()
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
// Stops the background ping goroutine if one was started via
// WithPingInterval.
func (c *WSConn[S, C]) Close(code websocket.StatusCode, reason string) error {
	if c.pingFn != nil {
		c.pingFn()
	}
	return c.conn.Close(code, reason)
}

// Subprotocol returns the negotiated subprotocol name, or "" if
// the connection uses the default subprotocol. Per ADR 0045 §1.
func (c *WSConn[S, C]) Subprotocol() string {
	return c.conn.Subprotocol()
}
