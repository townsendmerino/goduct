package analyzer

// websocket_test.go covers ADR 0044: a handler with third parameter
// *goduct.WSConn[S, C] returning error is recognized as a WebSocket
// route and populates ir.Route.WebSocket. Mangled signatures (wrong
// third param type, non-error return, wrong arity, cross-package
// message types) loud-fail.

import (
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

const wsFixture = `package svc

import (
	"context"

	goduct "github.com/townsendmerino/goduct/runtime"
)

type ChatReq struct {
	Room string ` + "`path:\"room\" validate:\"required\"`" + `
}

type ChatEvent struct {
	From string ` + "`json:\"from\"`" + `
	Text string ` + "`json:\"text\"`" + `
}

type ChatMessage struct {
	Text string ` + "`json:\"text\"`" + `
}

// goduct:route GET /chat/:room
// goduct:tag   chat
func Chat(ctx context.Context, req ChatReq, conn *goduct.WSConn[ChatEvent, ChatMessage]) error {
	_ = req.Room
	_ = conn
	return nil
}
`

func TestDiscoverRoutes_WebSocket(t *testing.T) {
	dir := t.TempDir()
	// Point the runtime import at the local checkout via a replace
	// directive so go/packages resolves *goduct.WSConn against the
	// real type (which is the only way the analyzer's structural
	// check on third-param-is-WSConn works).
	root := repoRoot(t)
	gomod := "module svc\n\ngo 1.26\n\n" +
		"require github.com/townsendmerino/goduct v0.0.0\n\n" +
		"replace github.com/townsendmerino/goduct => " + root + "\n"
	writeFiles(t, dir, map[string]string{
		"go.mod": gomod,
		"f.go":   wsFixture,
	})
	api, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(api.Routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(api.Routes))
	}
	r := api.Routes[0]
	if r.WebSocket == nil {
		t.Fatal("Route.WebSocket should be non-nil for a WS handler")
	}
	if r.WebSocket.Send.Named != "svc.ChatEvent" {
		t.Errorf("WebSocket.Send = %q, want svc.ChatEvent", r.WebSocket.Send.Named)
	}
	if r.WebSocket.Recv.Named != "svc.ChatMessage" {
		t.Errorf("WebSocket.Recv = %q, want svc.ChatMessage", r.WebSocket.Recv.Named)
	}
	if r.ResponseType != nil || r.StreamType != nil || r.BodyType != nil {
		t.Errorf("WS routes should have no Response/Stream/BodyType, got %+v / %+v / %+v",
			r.ResponseType, r.StreamType, r.BodyType)
	}
	if r.SuccessStatus != 200 {
		t.Errorf("WS SuccessStatus = %d, want 200", r.SuccessStatus)
	}
	// Both message types must be reachable seeds.
	for _, name := range []string{"svc.ChatEvent", "svc.ChatMessage"} {
		if _, ok := api.Types[name]; !ok {
			t.Errorf("%s not reached via Route.WebSocket seed; keys = %v",
				name, typeKeys(api.Types))
		}
	}
}

// TestDiscoverRoutes_WebSocketLoudFails covers the four rejected
// shapes per ADR 0044: third param not *goduct.WSConn, WS handler
// returning (T, error), four-arg handler, and a non-pointer WSConn.
func TestDiscoverRoutes_WebSocketLoudFails(t *testing.T) {
	cases := []struct {
		name, src, wantSub string
	}{
		{
			"third param is not *goduct.WSConn",
			`package svc
import "context"
type R struct { ID string ` + "`path:\"id\" validate:\"required\"`" + ` }
// goduct:route GET /x/:id
func H(ctx context.Context, req R, extra string) error { return nil }
`,
			"*goduct.WSConn[S, C]",
		},
		{
			"WS handler returns (T, error) instead of error",
			`package svc
import (
	"context"

	goduct "github.com/townsendmerino/goduct/runtime"
)
type R struct { ID string ` + "`path:\"id\" validate:\"required\"`" + ` }
type S struct { F string ` + "`json:\"f\"`" + ` }
type C struct { F string ` + "`json:\"f\"`" + ` }
// goduct:route GET /x/:id
func H(ctx context.Context, req R, conn *goduct.WSConn[S, C]) (*S, error) { return nil, nil }
`,
			"WebSocket handlers must return a single error",
		},
		{
			"four-argument handler is not allowed",
			`package svc
import "context"
type R struct { ID string ` + "`path:\"id\" validate:\"required\"`" + ` }
// goduct:route GET /x/:id
func H(ctx context.Context, req R, x int, y int) error { return nil }
`,
			"must be func(context.Context, T)",
		},
	}
	root := repoRoot(t)
	gomodTemplate := "module svc\n\ngo 1.26\n\n" +
		"require github.com/townsendmerino/goduct v0.0.0\n\n" +
		"replace github.com/townsendmerino/goduct => " + root + "\n"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{
				"go.mod": gomodTemplate,
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

// Helper to avoid `_ = ir` complaints when no Route.WebSocket field
// is consulted in some compile paths.
var _ = ir.WebSocketTypes{}
