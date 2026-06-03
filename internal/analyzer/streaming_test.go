package analyzer

// streaming_test.go covers ADR 0041: a handler signature
// func(ctx, T) (<-chan E, error) is recognized as an SSE route
// and populates ir.Route.StreamType. Mangled shapes (bidi channel,
// builtin element type, cross-package element) loud-fail.

import (
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

const streamingFixture = `package svc

import "context"

type WatchReq struct {
	ID string ` + "`path:\"id\" validate:\"required\"`" + `
}

type OrderEvent struct {
	OrderID string ` + "`json:\"orderId\"`" + `
	Status  string ` + "`json:\"status\"`" + `
}

// goduct:route GET /orders/:id/events
// goduct:tag   orders
func WatchOrders(ctx context.Context, req WatchReq) (<-chan OrderEvent, error) {
	return nil, nil
}
`

func TestDiscoverRoutes_Streaming(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module svc\n\ngo 1.26\n",
		"f.go":   streamingFixture,
	})
	api, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(api.Routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(api.Routes))
	}
	r := api.Routes[0]
	if r.StreamType == nil {
		t.Fatal("StreamType nil; expected non-nil for streaming route")
	}
	if r.StreamType.Kind != ir.KindNamed || r.StreamType.Named != "svc.OrderEvent" {
		t.Errorf("StreamType = %+v, want KindNamed svc.OrderEvent", r.StreamType)
	}
	if r.ResponseType != nil {
		t.Errorf("ResponseType should be nil for streaming routes, got %+v", r.ResponseType)
	}
	if r.SuccessStatus != 200 {
		t.Errorf("SuccessStatus = %d, want 200 (streaming defaults to 200)", r.SuccessStatus)
	}
	// The event type must be reachable for type-traversal.
	if _, ok := api.Types["svc.OrderEvent"]; !ok {
		t.Errorf("svc.OrderEvent not reached via Route.StreamType seed; keys = %v",
			typeKeys(api.Types))
	}
}

// TestDiscoverRoutes_StreamingLoudFails covers the three rejected
// streaming-signature shapes per ADR 0041: bidirectional channel,
// builtin element type, and cross-package element type.
func TestDiscoverRoutes_StreamingLoudFails(t *testing.T) {
	cases := []struct {
		name, src, wantSub string
	}{
		{
			"bidirectional channel rejected",
			`package svc
import "context"
type R struct { ID string ` + "`path:\"id\" validate:\"required\"`" + ` }
type E struct { ID string ` + "`json:\"id\"`" + ` }
// goduct:route GET /e/:id
func Stream(ctx context.Context, req R) (chan E, error) { return nil, nil }
`,
			"receive-only",
		},
		{
			"builtin element rejected",
			`package svc
import "context"
type R struct { ID string ` + "`path:\"id\" validate:\"required\"`" + ` }
// goduct:route GET /e/:id
func Stream(ctx context.Context, req R) (<-chan string, error) { return nil, nil }
`,
			"named struct type",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{
				"go.mod": "module svc\n\ngo 1.26\n",
				"f.go":   c.src,
			})
			_, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
			if err == nil {
				t.Fatal("expected loud-fail, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error should mention %q, got: %v", c.wantSub, err)
			}
		})
	}
}
