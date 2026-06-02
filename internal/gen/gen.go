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
		for _, ref := range []*ir.TypeRef{r.ResponseType, r.BodyType} {
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
