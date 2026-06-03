// Package gen holds helpers shared by the code generators (tstypes, zod,
// tsclient, goadapter). Per ADR 0022 §6/§8 these live here rather than in
// any one generator so the cross-cutting rules have a single home.
package gen

import (
	"go/doc"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/townsendmerino/goduct/internal/ir"
)

// WireFields returns the subset of td.Fields with Source ==
// FieldSourceJSON, order preserved. The "wire-visible = JSON source"
// rule lives here so every generator agrees (ADR 0022 §6).
func WireFields(td ir.TypeDef) []ir.Field {
	var out []ir.Field
	for _, f := range td.Fields {
		if f.Source == ir.FieldSourceJSON {
			out = append(out, f)
		}
	}
	return out
}

// EmitTS reports whether td should produce a top-level TypeScript
// declaration (ADR 0022 §9). A TypeStruct with no wire-visible fields is
// omitted (it exists in the IR only to carry path/query/header params);
// TypeEnum and TypeAlias always emit.
func EmitTS(td ir.TypeDef) bool {
	if td.Kind == ir.TypeStruct {
		return len(WireFields(td)) > 0
	}
	return true
}

// SourcePath returns the import path of the package containing api's
// routes, for the generated-file `// source:` header (ADR 0022 §2). The
// qualified name format is "<import-path>.<TypeName>"; strip the final
// segment. Multi-package input → sorted, comma+space joined. Empty when
// no route carries a named response/body type.
func SourcePath(api *ir.API) string {
	paths := map[string]struct{}{}
	for _, r := range api.Routes {
		// StreamType (ADR 0041) joins ResponseType + BodyType as a
		// source for the package path — without it, an API with only
		// streaming routes would yield an empty SourcePath.
		for _, ref := range []*ir.TypeRef{r.ResponseType, r.BodyType, r.StreamType} {
			if ref != nil && ref.Kind == ir.KindNamed {
				if i := strings.LastIndex(ref.Named, "."); i > 0 {
					paths[ref.Named[:i]] = struct{}{}
					break
				}
			}
		}
	}
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// PackageName returns the unqualified package name shared by api's
// routes — the last path segment of SourcePath (e.g. "api"). v0.1 is
// single-package; "" when no route carries a named type.
func PackageName(api *ir.API) string {
	p := SourcePath(api)
	if p == "" {
		return ""
	}
	if i := strings.Index(p, ", "); i >= 0 {
		p = p[:i] // v0.1: first of any multi-package join
	}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

var jsdocCopula = map[string]bool{
	"is": true, "are": true, "was": true, "were": true,
	"represents": true, "represent": true,
}

// JSDoc transforms a raw godoc comment for a Go identifier named typeName
// into a JSDoc-friendly *summary* (first sentence, via go/doc.Synopsis;
// without the /** ... */ markers), or "" if no JSDoc should be emitted.
// Used by tstypes. See ADR 0023.
func JSDoc(typeName, rawDoc string) string {
	raw := strings.TrimSpace(rawDoc)
	if raw == "" {
		return ""
	}
	return jsdocCore(typeName, doc.Synopsis(raw)) // first sentence; handles "v1.2"
}

// JSDocFull is like JSDoc but preserves all sentences (no Synopsis step).
// Used by tsclient, where method docs are part of the API surface and
// multi-sentence guidance must not be truncated. See ADR 0023/0024.
func JSDocFull(typeName, rawDoc string) string {
	raw := strings.TrimSpace(rawDoc)
	if raw == "" {
		return ""
	}
	return jsdocCore(typeName, raw)
}

// jsdocCore is the shared transform: strip a leading token equal to
// typeName, strip a following copula, capitalize the first rune. text is
// already trimmed (and, for JSDoc, already reduced to one sentence).
func jsdocCore(typeName, text string) string {
	toks := strings.Fields(text)
	if len(toks) > 0 && toks[0] == typeName {
		toks = toks[1:]
	}
	if len(toks) > 0 && jsdocCopula[toks[0]] {
		toks = toks[1:]
	}
	if len(toks) == 0 {
		return ""
	}
	s := strings.Join(toks, " ")
	r, sz := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[sz:]
}

// MethodName derives the tag-grouped client method name from a handler
// name and its route tag: it strips the tag's PascalCase suffix (plural
// then singular fallback) from the handler and lowercases the first rune
// (camelCase). If no suffix matches, it returns the handler with the
// first rune lowercased. Examples (tag = "users"):
//
//	GetUser    -> "get"
//	ListUsers  -> "list"
//	CreateUser -> "create"
//
// Shared between tsclient and the hooks generator per ADR 0022 §8 so
// both agree on the method name embedded in queryKey (ADR 0028 §4).
// The tsclient golden is the spec anchor; the move from tsclient to
// here is verified by that golden's byte-identity.
func MethodName(handler, tag string) string {
	singular := tag
	if strings.HasSuffix(tag, "s") {
		singular = strings.TrimSuffix(tag, "s")
	}
	stem := handler
	for _, suffix := range []string{Pascal(tag), Pascal(singular)} {
		if strings.HasSuffix(stem, suffix) && len(stem) > len(suffix) {
			stem = stem[:len(stem)-len(suffix)]
			break
		}
	}
	if stem == "" {
		stem = handler
	}
	rs := []rune(stem)
	rs[0] = unicode.ToLower(rs[0])
	return string(rs)
}

// Pascal uppercases the first rune of s and returns the rest unchanged.
// Used by MethodName; exported because tsclient and hooks both need it.
func Pascal(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(s)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

// AdapterWireTS maps an ADR 0032 wire shape to its TypeScript spelling.
// Returns "" for an unknown wire (callers panic loudly with the offending
// value — an unknown wire here is an analyzer/IR-invariant violation,
// not a user error; the CLI validates the value at flag-parse time).
func AdapterWireTS(wire string) string {
	switch wire {
	case "string":
		return "string"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "unknown":
		return "unknown"
	}
	return ""
}

// AdapterWireZod maps an ADR 0032 wire shape to its zod-schema spelling.
// Returns "" for unknown wires; callers panic.
func AdapterWireZod(wire string) string {
	switch wire {
	case "string":
		return "z.string()"
	case "number":
		return "z.number()"
	case "boolean":
		return "z.boolean()"
	case "unknown":
		return "z.unknown()"
	}
	return ""
}

// AdapterWires is the set of valid ADR 0032 wire-shape strings, used by
// the CLI to validate --adapter values before invoking the analyzer.
var AdapterWires = []string{"string", "number", "boolean", "unknown"}

// TypeParamDecl renders one type-param + optional constraint as a TS
// declaration fragment (the bit that goes inside `<...>` on
// `interface Foo<T extends X>`). render is the generator-local function
// that turns each constraint term into its TS spelling — passed in
// rather than inlined here per ADR 0022 §6 (each generator owns its
// target-language type-string rules). Terms are deduplicated in
// source order so [T int | int64] collapses to `<T extends number>`.
//
// param is the type-param name (e.g. "T"); constraint is the IR
// TypeRef (nil for `any`, KindUnion for multi-term, anything else
// for a single-term constraint). Returns "T" for any-constrained,
// "T extends X" for constrained.
//
// v0.4 per ADR 0036.
func TypeParamDecl(param string, constraint *ir.TypeRef, render func(ir.TypeRef) string) string {
	if constraint == nil {
		return param
	}
	terms := []ir.TypeRef{*constraint}
	if constraint.Kind == ir.KindUnion {
		terms = nil
		for _, t := range constraint.UnionTerms {
			if t != nil {
				terms = append(terms, *t)
			}
		}
	}
	seen := make(map[string]struct{}, len(terms))
	var rendered []string
	for _, t := range terms {
		s := render(t)
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		rendered = append(rendered, s)
	}
	if len(rendered) == 0 {
		return param
	}
	return param + " extends " + strings.Join(rendered, " | ")
}
