package goduct

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// SSEStream serves a server-sent-events response: it reads events from
// ch and writes them as `data: <json>\n\n` blocks to w, flushing after
// each one. Returns when ctx.Done() fires, when ch is closed, or when
// the writer doesn't support flushing (in which case the stream can't
// work and SSEStream bails immediately).
//
// Caller responsibilities:
//   - Set response headers (Content-Type: text/event-stream etc.) and
//     call w.WriteHeader before invoking SSEStream. SSEStream does not
//     touch the header itself so the caller controls the status code
//     and any framework-specific header writes.
//   - Close ch when the source is exhausted, or let ctx cancellation
//     stop the producer; SSEStream exits cleanly on either signal.
//
// Generic over E so the marshal stays type-safe per ADR 0041 §3.
func SSEStream[E any](ctx context.Context, w http.ResponseWriter, ch <-chan E) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}
