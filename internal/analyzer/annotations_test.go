package analyzer

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDirectives_Success(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want Directives
	}{
		{
			name: "happy path: every directive present",
			doc: strings.Join([]string{
				"CreateUser creates a new user.",
				"",
				"goduct:route POST /users",
				"goduct:tag users",
				"goduct:status 201",
				"goduct:request CreateUserRequest",
				"goduct:response User",
			}, "\n"),
			want: Directives{
				Route:    &RouteDirective{Method: "POST", Path: "/users"},
				Tag:      "users",
				Status:   new(201),
				Request:  "CreateUserRequest",
				Response: "User",
				Doc:      "CreateUser creates a new user.",
			},
		},
		{
			name: "minimal: just goduct:route",
			doc:  "goduct:route GET /foo",
			want: Directives{Route: &RouteDirective{Method: "GET", Path: "/foo"}},
		},
		{
			name: "no directives at all",
			doc:  "Just a normal doc comment.\n\nSecond paragraph.",
			want: Directives{Doc: "Just a normal doc comment.\n\nSecond paragraph."},
		},
		{
			name: "empty input",
			doc:  "",
			want: Directives{},
		},
		{
			name: "tag only: route absent is valid (Route nil)",
			doc:  "goduct:tag users",
			want: Directives{Tag: "users"},
		},
		{
			name: "doc preserved, trailing directives stripped",
			// This is the shape go/ast's CommentGroup.Text() produces for
			// the chi-basic GetUser handler (trailing newline included).
			doc: "GetUser returns a single user by ID.\n\ngoduct:route GET /users/:id\ngoduct:tag   users\n",
			want: Directives{
				Route: &RouteDirective{Method: "GET", Path: "/users/:id"},
				Tag:   "users",
				Doc:   "GetUser returns a single user by ID.",
			},
		},
		{
			name: "directive in the middle of the doc, not at the end",
			// Interior blank lines are intentionally preserved: the spec
			// only trims *surrounding* whitespace, so removing a directive
			// from the middle leaves the blanks that bracketed it.
			doc: "First paragraph.\n\ngoduct:route GET /foo\n\nSecond paragraph.",
			want: Directives{
				Route: &RouteDirective{Method: "GET", Path: "/foo"},
				Doc:   "First paragraph.\n\n\nSecond paragraph.",
			},
		},
		{
			name: "method lowercase normalized to upper",
			doc:  "goduct:route post /users",
			want: Directives{Route: &RouteDirective{Method: "POST", Path: "/users"}},
		},
		{
			name: "tabs between directive name and args",
			doc:  "goduct:route\tGET\t/foo\ngoduct:tag\t\tusers",
			want: Directives{
				Route: &RouteDirective{Method: "GET", Path: "/foo"},
				Tag:   "users",
			},
		},
		{
			name: "leading/trailing whitespace on lines",
			doc:  "  Padded doc line.  \n   goduct:route   GET   /foo   \n  goduct:status  204  ",
			want: Directives{
				Route:  &RouteDirective{Method: "GET", Path: "/foo"},
				Status: new(204),
				Doc:    "Padded doc line.",
			},
		},
		{
			name: "negative status accepted (no range check yet)",
			doc:  "goduct:route GET /x\ngoduct:status -1",
			want: Directives{
				Route:  &RouteDirective{Method: "GET", Path: "/x"},
				Status: new(-1),
			},
		},
		{
			name: "all five HTTP methods are valid",
			doc:  "goduct:route delete /x",
			want: Directives{Route: &RouteDirective{Method: "DELETE", Path: "/x"}},
		},
		{
			name: "line that merely contains goduct: is doc, not a directive",
			doc:  "See the goduct:route directive docs for details.",
			want: Directives{Doc: "See the goduct:route directive docs for details."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDirectives(tt.doc)
			if err != nil {
				t.Fatalf("ParseDirectives() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseDirectives() =\n  %#v\nwant\n  %#v", got, tt.want)
			}
		})
	}
}

func TestParseDirectives_Errors(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantSub string
	}{
		{"unknown directive", "goduct:rout GET /foo", "unknown directive"},
		{"unknown directive (plausible typo)", "goduct:tags users", "unknown directive"},
		{"empty directive name", "goduct:", "unknown directive"},
		{"bad method", "goduct:route FETCH /foo", "invalid HTTP method"},
		{"bad path (no leading slash)", "goduct:route GET foo", "path must start with '/'"},
		{"bad status (not an integer)", "goduct:route GET /x\ngoduct:status twohundred", "status must be an integer"},
		{"malformed route: missing path", "goduct:route GET", "malformed route"},
		{"malformed route: nothing at all", "goduct:route", "malformed route"},
		{"malformed route: extra token", "goduct:route GET /foo /bar", "malformed route"},
		{"tag missing argument", "goduct:tag", "tag requires an argument"},
		{"status missing argument", "goduct:status", "status requires an argument"},
		{"request takes a single argument", "goduct:request A B", "single argument"},
		{"duplicate route", "goduct:route GET /a\ngoduct:route POST /b", "duplicate goduct:route"},
		{"duplicate tag", "goduct:route GET /a\ngoduct:tag x\ngoduct:tag y", "duplicate goduct:tag"},
		{"duplicate status", "goduct:route GET /a\ngoduct:status 200\ngoduct:status 201", "duplicate goduct:status"},
		{"duplicate route reported on second occurrence (line 2)", "goduct:route GET /a\ngoduct:route GET /a", "(line 2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDirectives(tt.doc)
			if err == nil {
				t.Fatalf("ParseDirectives() expected error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("ParseDirectives() error = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}

// TestParseDirectives_Example_Errorresponse covers the v0.4 OpenAPI
// polish directives (ADR 0039): example captures the rest-of-line
// verbatim, errorresponse is the only repeatable directive, and
// duplicate-status / out-of-range / malformed cases loud-fail.
func TestParseDirectives_Example_Errorresponse(t *testing.T) {
	t.Run("example captures the JSON literal verbatim", func(t *testing.T) {
		const j = `{"id":"u-1","name":"Alice","tags":["admin","beta"]}`
		d, err := ParseDirectives("goduct:route GET /a\ngoduct:example " + j)
		if err != nil {
			t.Fatalf("ParseDirectives: %v", err)
		}
		if d.Example != j {
			t.Errorf("Example = %q, want %q", d.Example, j)
		}
	})
	t.Run("example without argument is a loud-fail", func(t *testing.T) {
		_, err := ParseDirectives("goduct:route GET /a\ngoduct:example")
		if err == nil || !strings.Contains(err.Error(), "JSON-literal") {
			t.Errorf("expected JSON-literal error, got %v", err)
		}
	})
	t.Run("duplicate example loud-fails (single-shot)", func(t *testing.T) {
		_, err := ParseDirectives("goduct:route GET /a\ngoduct:example {}\ngoduct:example []")
		if err == nil || !strings.Contains(err.Error(), "duplicate goduct:example") {
			t.Errorf("expected duplicate example error, got %v", err)
		}
	})
	t.Run("errorresponse parses status + type", func(t *testing.T) {
		d, err := ParseDirectives(
			"goduct:route POST /a\n" +
				"goduct:errorresponse 400 ValidationError\n" +
				"goduct:errorresponse 409 ConflictError")
		if err != nil {
			t.Fatalf("ParseDirectives: %v", err)
		}
		if len(d.ErrorResponses) != 2 {
			t.Fatalf("ErrorResponses len = %d, want 2", len(d.ErrorResponses))
		}
		if d.ErrorResponses[0].Status != 400 || d.ErrorResponses[0].TypeName != "ValidationError" {
			t.Errorf("ErrorResponses[0] = %+v", d.ErrorResponses[0])
		}
		if d.ErrorResponses[1].Status != 409 || d.ErrorResponses[1].TypeName != "ConflictError" {
			t.Errorf("ErrorResponses[1] = %+v", d.ErrorResponses[1])
		}
	})
	t.Run("duplicate status across errorresponses loud-fails", func(t *testing.T) {
		_, err := ParseDirectives(
			"goduct:route POST /a\n" +
				"goduct:errorresponse 400 A\n" +
				"goduct:errorresponse 400 B")
		if err == nil || !strings.Contains(err.Error(), "duplicate errorresponse for status 400") {
			t.Errorf("expected duplicate-status error, got %v", err)
		}
	})
	t.Run("errorresponse status out of range loud-fails", func(t *testing.T) {
		for _, bad := range []string{"99", "600", "0", "-1"} {
			_, err := ParseDirectives(
				"goduct:route GET /a\ngoduct:errorresponse " + bad + " ErrType")
			if err == nil || !strings.Contains(err.Error(), "out of range") {
				t.Errorf("status %q expected out-of-range error, got %v", bad, err)
			}
		}
	})
	t.Run("errorresponse malformed (missing type)", func(t *testing.T) {
		_, err := ParseDirectives("goduct:route GET /a\ngoduct:errorresponse 400")
		if err == nil || !strings.Contains(err.Error(), "malformed errorresponse") {
			t.Errorf("expected malformed-errorresponse error, got %v", err)
		}
	})
	t.Run("errorresponse status non-integer", func(t *testing.T) {
		_, err := ParseDirectives("goduct:route GET /a\ngoduct:errorresponse fourhundred ErrType")
		if err == nil || !strings.Contains(err.Error(), "must be an integer") {
			t.Errorf("expected integer error, got %v", err)
		}
	})
}

// TestParseDirectives_Requestexample_Security covers the v0.4.1
// directives (ADR 0040): single-shot capture, repeatable security
// with the `none`-vs-named contradiction rejected, and a
// duplicate-named-scheme loud-fail.
func TestParseDirectives_Requestexample_Security(t *testing.T) {
	t.Run("requestexample captures the JSON literal verbatim", func(t *testing.T) {
		const j = `{"email":"alice@example.com","name":"Alice"}`
		d, err := ParseDirectives("goduct:route POST /u\ngoduct:requestexample " + j)
		if err != nil {
			t.Fatalf("ParseDirectives: %v", err)
		}
		if d.RequestExample != j {
			t.Errorf("RequestExample = %q, want %q", d.RequestExample, j)
		}
	})
	t.Run("requestexample without argument is a loud-fail", func(t *testing.T) {
		_, err := ParseDirectives("goduct:route POST /u\ngoduct:requestexample")
		if err == nil || !strings.Contains(err.Error(), "JSON-literal") {
			t.Errorf("expected JSON-literal error, got %v", err)
		}
	})
	t.Run("duplicate requestexample loud-fails", func(t *testing.T) {
		_, err := ParseDirectives(
			"goduct:route POST /u\n" +
				"goduct:requestexample {}\n" +
				"goduct:requestexample []")
		if err == nil || !strings.Contains(err.Error(), "duplicate goduct:requestexample") {
			t.Errorf("expected duplicate error, got %v", err)
		}
	})
	t.Run("security single named scheme", func(t *testing.T) {
		d, err := ParseDirectives("goduct:route GET /a\ngoduct:security bearerAuth")
		if err != nil {
			t.Fatalf("ParseDirectives: %v", err)
		}
		if len(d.Security) != 1 || d.Security[0] != "bearerAuth" {
			t.Errorf("Security = %v", d.Security)
		}
	})
	t.Run("security `none` is captured", func(t *testing.T) {
		d, err := ParseDirectives("goduct:route GET /healthz\ngoduct:security none")
		if err != nil {
			t.Fatalf("ParseDirectives: %v", err)
		}
		if len(d.Security) != 1 || d.Security[0] != "none" {
			t.Errorf("Security = %v", d.Security)
		}
	})
	t.Run("security multiple named schemes compose as OR", func(t *testing.T) {
		d, err := ParseDirectives(
			"goduct:route GET /a\n" +
				"goduct:security bearerAuth\n" +
				"goduct:security apiKey")
		if err != nil {
			t.Fatalf("ParseDirectives: %v", err)
		}
		if len(d.Security) != 2 || d.Security[0] != "bearerAuth" || d.Security[1] != "apiKey" {
			t.Errorf("Security = %v", d.Security)
		}
	})
	t.Run("security `none` + named scheme is contradictory", func(t *testing.T) {
		_, err := ParseDirectives(
			"goduct:route GET /a\n" +
				"goduct:security none\n" +
				"goduct:security bearerAuth")
		if err == nil || !strings.Contains(err.Error(), "`none` cannot be combined") {
			t.Errorf("expected contradiction error, got %v", err)
		}
	})
	t.Run("security duplicate-named loud-fails", func(t *testing.T) {
		_, err := ParseDirectives(
			"goduct:route GET /a\n" +
				"goduct:security bearerAuth\n" +
				"goduct:security bearerAuth")
		if err == nil || !strings.Contains(err.Error(), "duplicate goduct:security") {
			t.Errorf("expected duplicate-named error, got %v", err)
		}
	})
	t.Run("security takes a single argument", func(t *testing.T) {
		_, err := ParseDirectives("goduct:route GET /a\ngoduct:security bearerAuth apiKey")
		if err == nil || !strings.Contains(err.Error(), "single argument") {
			t.Errorf("expected single-argument error, got %v", err)
		}
	})
}
