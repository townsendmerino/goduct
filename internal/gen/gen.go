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
