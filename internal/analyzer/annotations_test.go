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
