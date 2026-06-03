package analyzer

// structfields.go is the classification half of the shared field analysis:
// "should this field be on the wire, and from where?" (tags, skip rules,
// optionality). It builds the TypeRef via fieldtypes.go but owns no
// type-construction logic itself. Route discovery and type traversal both
// call ParseStructField so they cannot disagree.

import (
	"fmt"
	"go/ast"
	"go/types"
	"reflect"
	"strings"

	"github.com/townsendmerino/goduct/internal/ir"
	"golang.org/x/tools/go/packages"
)

// StructContext tells ParseStructField about the struct that owns the field.
type StructContext struct {
	// IsRequestType is true only for a route's top-level request struct;
	// path/query/header tags are valid only there (ADR 0016, ADR 0018 E2).
	IsRequestType bool
	// QualifiedName is the short display name of the containing type, e.g.
	// "api.GetUserRequest", used only in error messages.
	QualifiedName string
}

// ParsedField is ParseStructField's result. WireName carries the
// path/query/header tag value (empty for json/none); it has no home in
// ir.Field, so only route discovery consumes it.
type ParsedField struct {
	Field    ir.Field
	WireName string
}

// ParseStructField classifies one struct field and returns its ir.Field
// (+ WireName), or an ADR 0019 Format B error. It returns nil,nil when the
// field is skipped entirely: unexported (ADR 0018 D1) or `json:"-"` only
// (ADR 0018 D2). It does NOT recurse into named types — fieldTypeRef emits
// KindNamed and the traversal layer expands it.
func ParseStructField(pkg *packages.Package, field *types.Var, tag reflect.StructTag, ctx StructContext) (*ParsedField, error) {
	if !field.Exported() {
		return nil, nil // ADR 0018 D1
	}
	if field.Embedded() {
		return nil, formatFieldErr(pkg, field, ctx, "B5",
			"embedded fields in request structs are not yet supported (field "+
				field.Name()+" in "+ctx.QualifiedName+") — extract to a named field with an explicit tag",
			"extract the embedded struct into named fields")
	}

	src, wire, count, jsonName, jsonDash := classifyTag(tag)
	if count == 0 && jsonDash {
		return nil, nil // ADR 0018 D2 (json:"-" with no other source)
	}
	if count > 1 {
		return nil, formatFieldErr(pkg, field, ctx, "E3",
			"field has conflicting tags (path/query/header/json are mutually exclusive)",
			"use exactly one of path/query/header/json")
	}
	if !ctx.IsRequestType && (src == "path" || src == "query" || src == "header" ||
		src == "multipart" || src == "form") {
		return nil, formatFieldErr(pkg, field, ctx, "E2",
			"a "+src+" tag on a non-request type's field is not allowed",
			src+" tags are only valid on a route's request type")
	}

	rules := parseValidate(tag)
	f := ir.Field{
		GoName:     field.Name(),
		Validation: rules,
		Doc:        docForField(pkg, ctx.QualifiedName, field.Name()),
	}

	switch src {
	case "path", "query", "header":
		ref, isPtr, ok := paramTypeRef(field.Type(), src != "path")
		if !ok {
			return nil, formatFieldErr(pkg, field, ctx, "PATH2",
				src+" param has unsupported type "+types.TypeString(field.Type(), nil)+" in v0.1",
				"path/query/header params must be primitives (query/header may be []primitive)")
		}
		if src == "path" && isPtr {
			return nil, formatFieldErr(pkg, field, ctx, "PATH1",
				"path param cannot be a pointer (path params are always present)",
				"use a non-pointer field")
		}
		f.Type = ref
		switch src {
		case "path":
			f.Source, f.Optional = ir.FieldSourcePath, false
		case "query":
			f.Source, f.Optional = ir.FieldSourceQuery, !hasRule(rules, "required")
		case "header":
			f.Source, f.Optional = ir.FieldSourceHeader, !hasRule(rules, "required")
		}
		return &ParsedField{Field: f, WireName: wire}, nil

	case "json":
		ref, isPtr, te := fieldTypeRef(field.Type())
		if te != nil {
			return nil, formatFieldErr(pkg, field, ctx, te.cat, te.desc, te.hint)
		}
		f.Type, f.Source, f.JSONName = ref, ir.FieldSourceJSON, jsonName
		f.Optional = isPtr || tagHasOmitempty(tag) // ADR 0020
		return &ParsedField{Field: f}, nil

	case "multipart":
		// ADR 0042 (single) + ADR 0043 (multi): a multipart field is
		// either *multipart.FileHeader or []*multipart.FileHeader.
		// The TypeRef encodes the difference via Kind so generators
		// branch naturally (KindSlice → multi-file, KindBuiltin →
		// single).
		isFile, isMulti := multipartFileShape(field.Type())
		if !isFile {
			return nil, formatFieldErr(pkg, field, ctx, "UPL1",
				"multipart field must be of type *multipart.FileHeader or []*multipart.FileHeader, got "+
					types.TypeString(field.Type(), nil),
				"declare the field as `*multipart.FileHeader` (single) or `[]*multipart.FileHeader` (multi)")
		}
		f.Source, f.Optional = ir.FieldSourceMultipart, !hasRule(rules, "required")
		f.JSONName = wire
		single := ir.TypeRef{Kind: ir.KindBuiltin, Builtin: "multipart.FileHeader"}
		if isMulti {
			f.Type = ir.TypeRef{Kind: ir.KindSlice, Element: &single}
		} else {
			f.Type = single
		}
		return &ParsedField{Field: f, WireName: wire}, nil

	case "form":
		// Text field on a multipart form. Same type rule as query/header.
		ref, isPtr, ok := paramTypeRef(field.Type(), true /* allow ptr */)
		if !ok {
			return nil, formatFieldErr(pkg, field, ctx, "UPL2",
				"form field has unsupported type "+types.TypeString(field.Type(), nil),
				"form fields must be primitives (string/int*/uint*/float*/bool)")
		}
		f.Type, f.Source, f.JSONName = ref, ir.FieldSourceForm, wire
		f.Optional = isPtr || !hasRule(rules, "required")
		return &ParsedField{Field: f, WireName: wire}, nil

	default: // "none": untagged exported field — present in IR, off the wire
		ref, _, te := fieldTypeRef(field.Type())
		if te != nil {
			return nil, formatFieldErr(pkg, field, ctx, te.cat, te.desc, te.hint)
		}
		f.Type, f.Source = ref, ir.FieldSourceNone
		return &ParsedField{Field: f}, nil
	}
}

// classifyTag determines the field's wire source. `json:"-"` does not count
// as a json source (ADR 0018 D2/E1): `json:"-"` alone → skip; `json:"-"`
// with a path/query/header tag → that source (the json:"-" is redundant).
// ADR 0042: `multipart:"name"` and `form:"name"` join the wire-source set;
// they are mutually exclusive with json on the same struct (enforced at
// the route-discovery level, not here).
func classifyTag(tag reflect.StructTag) (src, wire string, count int, jsonName string, jsonDash bool) {
	pathV, hasPath := tag.Lookup("path")
	queryV, hasQuery := tag.Lookup("query")
	headerV, hasHeader := tag.Lookup("header")
	jsonV, hasJSON := tag.Lookup("json")
	multipartV, hasMultipart := tag.Lookup("multipart")
	formV, hasForm := tag.Lookup("form")
	if hasJSON {
		jsonName = firstToken(jsonV)
		jsonDash = jsonName == "-"
	}
	if hasPath {
		count, src, wire = count+1, "path", firstToken(pathV)
	}
	if hasQuery {
		count, src, wire = count+1, "query", firstToken(queryV)
	}
	if hasHeader {
		count, src, wire = count+1, "header", firstToken(headerV)
	}
	if hasJSON && !jsonDash {
		count, src = count+1, "json"
	}
	if hasMultipart {
		count, src, wire = count+1, "multipart", firstToken(multipartV)
	}
	if hasForm {
		count, src, wire = count+1, "form", firstToken(formV)
	}
	if count == 0 {
		src = "none"
	}
	return
}

func firstToken(v string) string { return strings.Split(v, ",")[0] }

func tagHasOmitempty(tag reflect.StructTag) bool {
	v, ok := tag.Lookup("json")
	if !ok {
		return false
	}
	parts := strings.Split(v, ",")
	for _, p := range parts[1:] {
		if p == "omitempty" {
			return true
		}
	}
	return false
}

func parseValidate(tag reflect.StructTag) []ir.ValidationRule {
	v, ok := tag.Lookup("validate")
	if !ok || v == "" {
		return nil
	}
	var rules []ir.ValidationRule
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if name, arg, found := strings.Cut(part, "="); found {
			rules = append(rules, ir.ValidationRule{Name: name, Arg: arg})
		} else {
			rules = append(rules, ir.ValidationRule{Name: part})
		}
	}
	return rules
}

// multipartFileShape reports whether t is *mime/multipart.FileHeader
// (single, ADR 0042) or []*mime/multipart.FileHeader (multi,
// ADR 0043). Structural check, not string-comparison: the field type
// (or its slice element) must be a pointer to a *types.Named whose
// object is FileHeader in the mime/multipart package. Returns
// (isFile, isMulti).
func multipartFileShape(t types.Type) (isFile, isMulti bool) {
	if slice, ok := t.(*types.Slice); ok {
		return isPtrToMultipartFileHeader(slice.Elem()), true
	}
	return isPtrToMultipartFileHeader(t), false
}

func isPtrToMultipartFileHeader(t types.Type) bool {
	ptr, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj.Name() != "FileHeader" {
		return false
	}
	pkg := obj.Pkg()
	return pkg != nil && pkg.Path() == "mime/multipart"
}

// hasRule reports whether a validate rule with the given name is present.
// Signature kept stable: the frozen route-discovery tests call it directly.
func hasRule(rules []ir.ValidationRule, name string) bool {
	for _, r := range rules {
		if r.Name == name {
			return true
		}
	}
	return false
}

// formatFieldErr renders an ADR 0019 Format B (3-line) field error.
func formatFieldErr(pkg *packages.Package, field *types.Var, ctx StructContext, cat, desc, hint string) error {
	pos := pkg.Fset.Position(field.Pos())
	return fmt.Errorf("goduct: %s:%d:%d: %s: %s\n        in %s.%s (%s)\n        hint: %s",
		pos.Filename, pos.Line, pos.Column, cat, desc,
		ctx.QualifiedName, field.Name(), types.TypeString(field.Type(), nil), hint)
}

// docForField returns a field's godoc, best-effort, by locating the
// containing struct's TypeSpec in the package AST (go/types has no docs).
func docForField(pkg *packages.Package, qualName, fieldName string) string {
	typeName := qualName
	if i := strings.LastIndex(qualName, "."); i >= 0 {
		typeName = qualName[i+1:]
	}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != typeName {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, fld := range st.Fields.List {
					for _, id := range fld.Names {
						if id.Name == fieldName {
							return strings.TrimSpace(fld.Doc.Text())
						}
					}
				}
			}
		}
	}
	return ""
}
