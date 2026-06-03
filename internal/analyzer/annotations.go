// Package analyzer turns Go API source into goduct's intermediate
// representation. This file implements directive parsing: extracting
// `goduct:` directives from a godoc comment block. It is pure string
// manipulation with no go/ast dependency, so it can be unit-tested in
// isolation and reused wherever a comment string is available.
package analyzer

import (
	"fmt"
	"strconv"
	"strings"
)

// directivePrefix marks a goduct directive line once comment markers have
// been stripped — i.e. what go/ast's CommentGroup.Text() yields.
const directivePrefix = "goduct:"

// RouteDirective is the parsed `goduct:route METHOD PATH` directive.
type RouteDirective struct {
	// Method is the HTTP method, normalized to upper case.
	Method string
	// Path is the route pattern exactly as written (e.g. "/users/:id").
	Path string
}

// Directives is the result of parsing one godoc comment block. A field's
// zero value means the directive was absent; Route and Status are pointers
// so "absent" stays distinct from a valid zero argument, leaving defaulting
// (status per method, tag from path) to the caller.
type Directives struct {
	Route    *RouteDirective
	Tag      string
	Status   *int
	Request  string
	Response string

	// Example is the raw JSON literal from `goduct:example <json>`
	// (ADR 0039). Empty when absent. Validated only at OpenAPI
	// generate time, not here (this parser is content-agnostic).
	Example string

	// ErrorResponses captures each `goduct:errorresponse <status>
	// <TypeName>` line (ADR 0039). Repeatable; duplicate-status
	// detection lives here (two entries for the same status is a
	// loud-fail).
	ErrorResponses []ErrorResponseDirective

	// RequestExample is the raw JSON literal from
	// `goduct:requestexample <json>` (ADR 0040). Single-shot;
	// duplicates loud-fail. Empty when absent.
	RequestExample string

	// Security captures each `goduct:security <name|none>` line
	// (ADR 0040). Each entry is the scheme name, or the literal
	// "none" for an explicit unauthenticated operation. Repeatable
	// for OR-semantics. The `none + named` contradiction is
	// detected when applied; see apply().
	Security []string

	// Upload is true iff `goduct:upload` was present (ADR 0042).
	// Toggles the request body's wire-format hint for non-Go
	// generators (TS client → FormData, OpenAPI → multipart/form-
	// data). The Go side is untouched (raw handlers manage their
	// own multipart parsing).
	Upload bool

	// Doc is the comment text with every goduct: line removed and
	// surrounding whitespace trimmed. Interior blank lines are preserved.
	Doc string
}

// ErrorResponseDirective is the parsed form of one
// `goduct:errorresponse <status> <TypeName>` line (ADR 0039). The
// type name is resolved against the handler's package scope by the
// caller; this struct only carries the textual capture + the
// validated status.
type ErrorResponseDirective struct {
	Status   int
	TypeName string
}

// ParseDirectives extracts goduct directives from doc, which must be a
// comment block with "//" markers already stripped (the form produced by
// go/ast doc.Text() / CommentGroup.Text()), lines newline-separated.
//
// A block with no goduct:route is valid and returns Route == nil; the
// caller decides whether a route is required in that context. Unknown,
// duplicate, or malformed directives are errors — this is a fail-fast
// parser; leniency here only serves to hide typos.
func ParseDirectives(doc string) (Directives, error) {
	var d Directives
	var docLines []string
	seen := make(map[string]bool)

	for i, raw := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(raw)
		if !strings.HasPrefix(trimmed, directivePrefix) {
			docLines = append(docLines, raw)
			continue
		}
		body := strings.TrimSpace(trimmed[len(directivePrefix):])
		name, args := splitFirstField(body)
		if err := d.apply(name, args, i+1, trimmed, seen); err != nil {
			return Directives{}, err
		}
	}

	d.Doc = strings.TrimSpace(strings.Join(docLines, "\n"))
	return d, nil
}

// apply records one directive or returns a contextual error. line is the
// 1-based line number within the block and src the offending line. The
// parser has no absolute filename here; the caller (routes.go) prepends
// the ADR 0019 Format A prefix `goduct: <file>:<line>:<col>: ` from the
// function's Pos. The `(line N): <src>` suffix keeps the in-doc-block
// coordinate so users can find the directive inside a long godoc comment.
// seen tracks directives already applied so a repeat fails fast.
func (d *Directives) apply(name, args string, line int, src string, seen map[string]bool) error {
	fail := func(msg string) error {
		return fmt.Errorf("%s (line %d): %s", msg, line, src)
	}
	// errorresponse and security are repeatable; each case branch
	// enforces its own per-entry duplicate rules. Every other
	// directive is single-shot.
	if name != "errorresponse" && name != "security" {
		if seen[name] {
			return fail("duplicate " + directivePrefix + name + " directive")
		}
		seen[name] = true
	}
	switch name {
	case "route":
		f := strings.Fields(args)
		if len(f) != 2 {
			return fail("malformed route, want `goduct:route METHOD PATH`")
		}
		method := strings.ToUpper(f[0])
		if !validMethod(method) {
			return fail("invalid HTTP method " + strconv.Quote(f[0]))
		}
		if !strings.HasPrefix(f[1], "/") {
			return fail("path must start with '/'")
		}
		d.Route = &RouteDirective{Method: method, Path: f[1]}
	case "tag":
		v, err := singleArg(args, "tag")
		if err != nil {
			return fail(err.Error())
		}
		d.Tag = v
	case "status":
		v, err := singleArg(args, "status")
		if err != nil {
			return fail(err.Error())
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fail("status must be an integer, got " + strconv.Quote(v))
		}
		d.Status = &n
	case "request":
		v, err := singleArg(args, "request")
		if err != nil {
			return fail(err.Error())
		}
		d.Request = v
	case "response":
		v, err := singleArg(args, "response")
		if err != nil {
			return fail(err.Error())
		}
		d.Response = v
	case "example":
		// Whole-line capture: the JSON literal may contain spaces,
		// quotes, and inner braces. args is everything after the
		// directive name with leading whitespace trimmed.
		if args == "" {
			return fail("example requires a JSON-literal argument")
		}
		d.Example = args
	case "errorresponse":
		f := strings.Fields(args)
		if len(f) != 2 {
			return fail("malformed errorresponse, want `goduct:errorresponse STATUS TYPE`")
		}
		status, err := strconv.Atoi(f[0])
		if err != nil {
			return fail("errorresponse status must be an integer, got " + strconv.Quote(f[0]))
		}
		if status < 100 || status > 599 {
			return fail("errorresponse status out of range [100,599], got " + strconv.Itoa(status))
		}
		for _, existing := range d.ErrorResponses {
			if existing.Status == status {
				return fail(fmt.Sprintf("duplicate errorresponse for status %d", status))
			}
		}
		d.ErrorResponses = append(d.ErrorResponses,
			ErrorResponseDirective{Status: status, TypeName: f[1]})
	case "requestexample":
		if args == "" {
			return fail("requestexample requires a JSON-literal argument")
		}
		if d.RequestExample != "" {
			return fail("duplicate goduct:requestexample directive")
		}
		d.RequestExample = args
	case "upload":
		// Toggle directive: takes no argument.
		if args != "" {
			return fail("upload takes no arguments")
		}
		d.Upload = true
	case "security":
		v, err := singleArg(args, "security")
		if err != nil {
			return fail(err.Error())
		}
		// `none` and a named scheme on the same handler is
		// contradictory — either the op is public or it has a
		// requirement; not both. Detect on apply so the offending
		// line carries the (line N) suffix.
		for _, prev := range d.Security {
			if (prev == "none") != (v == "none") {
				return fail("goduct:security `none` cannot be combined with a named scheme")
			}
			if prev == v {
				return fail("duplicate goduct:security " + strconv.Quote(v))
			}
		}
		d.Security = append(d.Security, v)
	default:
		return fail("unknown directive " + strconv.Quote(directivePrefix+name))
	}
	return nil
}

// splitFirstField splits s into its first whitespace-delimited token and
// the remainder with leading whitespace trimmed. The separator may be any
// mix of spaces and tabs.
func splitFirstField(s string) (first, rest string) {
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// singleArg returns the sole argument of a directive, erroring if it is
// missing or if extra tokens follow it.
func singleArg(args, directive string) (string, error) {
	f := strings.Fields(args)
	switch len(f) {
	case 0:
		return "", fmt.Errorf("%s requires an argument", directive)
	case 1:
		return f[0], nil
	default:
		return "", fmt.Errorf("%s takes a single argument, got %d", directive, len(f))
	}
}

func validMethod(m string) bool {
	switch m {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}
