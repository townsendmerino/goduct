// Package tsclient generates client.ts: a typed fetch client. Unlike
// tstypes/zod (which walk api.Types and emit declarations), tsclient
// walks api.Routes and emits runtime client code — a createClient()
// returning tag-grouped async methods, plus fixed scaffolding
// (ClientOptions, GoductError, request()) copied verbatim from the
// golden. It consumes gen.SourcePath and gen.JSDocFull (ADR 0024); it
// does NOT use TopoSortTypes/WireFields/EmitTS (those operate on types).
// Route grouping by tag is generator-local (not promoted to internal/gen
// until goadapter needs it).
package tsclient

import (
	"io"
	"strings"

	"github.com/townsendmerino/goduct/internal/gen"
	"github.com/townsendmerino/goduct/internal/ir"
)

// scaffold is the IR-independent client boilerplate, byte-for-byte from
// the chi-basic golden (ADR 0022: identical across every generated
// client until v0.2 features land). It spans ClientOptions through the
// end of request(), and is followed by a blank line then createClient.
const scaffold = `export interface ClientOptions {
  /** Base URL of the API, e.g. "https://example.com" or "/api". No trailing slash. */
  baseUrl: string;
  /** Override the fetch implementation (e.g. for testing or SSR). */
  fetch?: typeof fetch;
  /** Returns headers to attach to every request (e.g. auth). */
  headers?: () => Record<string, string> | Promise<Record<string, string>>;
}

export class GoductError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly details?: unknown,
  ) {
    super(message);
    this.name = "GoductError";
  }
}

interface RequestOpts {
  method: string;
  path: string;
  query?: Record<string, string | number | boolean | undefined>;
  body?: unknown;
}

async function request(opts: ClientOptions, r: RequestOpts): Promise<unknown> {
  const f = opts.fetch ?? fetch;
  const isForm = typeof FormData !== "undefined" && r.body instanceof FormData;
  const headers: Record<string, string> = {};
  if (!isForm) headers["Content-Type"] = "application/json";
  if (opts.headers) Object.assign(headers, await opts.headers());

  let url = opts.baseUrl + r.path;
  if (r.query) {
    const usp = new URLSearchParams();
    for (const [k, v] of Object.entries(r.query)) {
      if (v !== undefined) usp.set(k, String(v));
    }
    const qs = usp.toString();
    if (qs) url += "?" + qs;
  }

  let body: BodyInit | undefined;
  if (r.body === undefined) body = undefined;
  else if (isForm) body = r.body as FormData;
  else body = JSON.stringify(r.body);

  const res = await f(url, {
    method: r.method,
    headers,
    body,
  });

  if (res.status === 204) return undefined;

  const text = await res.text();
  const data = text ? JSON.parse(text) : undefined;

  if (!res.ok) {
    const err = data as { code?: string; message?: string; details?: unknown } | undefined;
    throw new GoductError(
      res.status,
      err?.code ?? "unknown",
      err?.message ?? res.statusText,
      err?.details,
    );
  }
  return data;
}
`

// streamScaffold is appended after the regular scaffold when api has
// at least one streaming route (ADR 0041). It defines a generic
// streamSSE<E> helper that fetches the URL, reads the response body
// as a stream, splits on \n\n, parses each `data: <json>` line, and
// yields E values. Generated streaming methods call it.
const streamScaffold = `

async function* streamSSE<E>(opts: ClientOptions, r: RequestOpts): AsyncIterable<E> {
  const f = opts.fetch ?? fetch;
  const headers: Record<string, string> = { Accept: "text/event-stream" };
  if (opts.headers) Object.assign(headers, await opts.headers());

  let url = opts.baseUrl + r.path;
  if (r.query) {
    const usp = new URLSearchParams();
    for (const [k, v] of Object.entries(r.query)) {
      if (v !== undefined) usp.set(k, String(v));
    }
    const qs = usp.toString();
    if (qs) url += "?" + qs;
  }

  const res = await f(url, { method: r.method, headers });
  if (!res.ok || !res.body) {
    const text = res.body ? await res.text() : "";
    const err = text ? (JSON.parse(text) as { code?: string; message?: string; details?: unknown }) : undefined;
    throw new GoductError(
      res.status,
      err?.code ?? "unknown",
      err?.message ?? res.statusText,
      err?.details,
    );
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) return;
    buffer += decoder.decode(value, { stream: true });
    let idx: number;
    while ((idx = buffer.indexOf("\n\n")) !== -1) {
      const block = buffer.slice(0, idx);
      buffer = buffer.slice(idx + 2);
      for (const line of block.split("\n")) {
        if (line.startsWith("data: ")) {
          yield JSON.parse(line.slice(6)) as E;
        }
      }
    }
  }
}
`

// hasStreaming reports whether any route in api uses SSE streaming
// (ADR 0041). Used to decide whether to emit the streamSSE helper.
func hasStreaming(api *ir.API) bool {
	for _, r := range api.Routes {
		if r.StreamType != nil {
			return true
		}
	}
	return false
}

// Generate writes a deterministic client.ts for api to w (ADR 0022).
func Generate(api *ir.API, w io.Writer) error {
	var b strings.Builder
	b.WriteString("// Code generated by goduct. DO NOT EDIT.\n")
	b.WriteString("// source: " + gen.SourcePath(api) + "\n\n")
	b.WriteString("import * as schemas from \"./schemas\";\n")
	b.WriteString("import type * as t from \"./types\";\n\n")
	b.WriteString(scaffold)
	// ADR 0041: streamSSE helper is only emitted when needed so APIs
	// without streaming routes get the v0.4-byte-identical scaffold.
	if hasStreaming(api) {
		b.WriteString(streamScaffold)
	}
	b.WriteString("\nexport function createClient(opts: ClientOptions) {\n  return {\n")

	// Tags in first-appearance order; methods within a tag in route order.
	var tagOrder []string
	byTag := map[string][]ir.Route{}
	for _, r := range api.Routes {
		if _, ok := byTag[r.Tag]; !ok {
			tagOrder = append(tagOrder, r.Tag)
		}
		byTag[r.Tag] = append(byTag[r.Tag], r)
	}
	tagBlocks := make([]string, 0, len(tagOrder))
	for _, tag := range tagOrder {
		methods := make([]string, 0, len(byTag[tag]))
		for _, r := range byTag[tag] {
			methods = append(methods, renderMethod(r, api))
		}
		tagBlocks = append(tagBlocks,
			"    "+tag+": {\n"+strings.Join(methods, "\n\n")+"\n    },")
	}
	b.WriteString(strings.Join(tagBlocks, "\n"))
	b.WriteString("\n  };\n}\n")
	b.WriteString("\nexport type Client = ReturnType<typeof createClient>;\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func renderMethod(r ir.Route, api *ir.API) string {
	adapters := api.CustomAdapters
	var b strings.Builder
	if d := gen.JSDocFull(r.HandlerName, r.Doc); d != "" {
		b.WriteString("      /** " + d + " */\n")
	}
	// ADR 0041: streaming routes return AsyncIterable<E> and delegate
	// to the streamSSE scaffold helper; no zod validation per event
	// in v0.5 (the per-event type is still emitted as a regular type
	// in types.ts, so callers can type-narrow downstream).
	if r.StreamType != nil {
		ret := "AsyncIterable<" + tsTypeRef(*r.StreamType) + ">"
		b.WriteString("      " + gen.MethodName(r.HandlerName, r.Tag) +
			": (" + signature(r, adapters) + "): " + ret + " =>\n")
		b.WriteString("        streamSSE<" + tsTypeRef(*r.StreamType) + ">(opts, {\n")
		b.WriteString("          method: \"" + r.Method + "\",\n")
		b.WriteString("          path: " + pathExpr(r.Path) + ",\n")
		if len(r.QueryParams) > 0 {
			parts := make([]string, len(r.QueryParams))
			for i, q := range r.QueryParams {
				parts[i] = q.WireName + ": params." + q.WireName
			}
			b.WriteString("          query: { " + strings.Join(parts, ", ") + " },\n")
		}
		b.WriteString("        }),")
		return b.String()
	}
	hasResp := r.ResponseType != nil && r.ResponseType.Kind == ir.KindNamed
	ret := "Promise<void>"
	if hasResp {
		ret = "Promise<" + tsTypeRef(*r.ResponseType) + ">"
	}
	b.WriteString("      " + gen.MethodName(r.HandlerName, r.Tag) +
		": async (" + signature(r, adapters) + "): " + ret + " => {\n")

	// ADR 0042: typed-upload routes build a FormData from the body
	// shape (multipart file fields + form text fields) instead of
	// calling zod parse + JSON.stringify. The shared `request` helper
	// detects `body instanceof FormData` and switches Content-Type
	// + serialization automatically.
	if r.Upload && r.BodyType != nil && r.BodyType.Kind == ir.KindNamed {
		b.WriteString("        const fd = new FormData();\n")
		td := api.Types[r.BodyType.Named]
		for _, f := range gen.UploadFields(td) {
			ref := "body." + f.JSONName
			// Multi-file (ADR 0043) appends one entry per element under
			// the same field name — matches how browsers serialize
			// <input type="file" multiple>.
			if f.Source == ir.FieldSourceMultipart && f.Type.Kind == ir.KindSlice {
				stmt := ref + ".forEach((f) => fd.append(\"" + f.JSONName + "\", f));"
				if f.Optional {
					b.WriteString("        if (" + ref + " !== undefined) " + stmt + "\n")
				} else {
					b.WriteString("        " + stmt + "\n")
				}
				continue
			}
			line := "        fd.append(\"" + f.JSONName + "\", " + ref + ");\n"
			if f.Source == ir.FieldSourceForm && f.Type.Kind == ir.KindBuiltin && f.Type.Builtin != "string" {
				// Non-string form fields: stringify so FormData accepts them.
				line = "        fd.append(\"" + f.JSONName + "\", String(" + ref + "));\n"
			}
			if f.Optional {
				b.WriteString("        if (" + ref + " !== undefined) " + strings.TrimPrefix(strings.TrimSuffix(line, "\n"), "        ") + "\n")
			} else {
				b.WriteString(line)
			}
		}
	} else if r.BodyType != nil && r.BodyType.Kind == ir.KindNamed {
		b.WriteString("        const parsed = " + schemasExpr(*r.BodyType) + ".parse(body);\n")
	}
	open := "        await request(opts, {\n"
	if hasResp {
		open = "        const data = await request(opts, {\n"
	}
	b.WriteString(open)
	b.WriteString("          method: \"" + r.Method + "\",\n")
	b.WriteString("          path: " + pathExpr(r.Path) + ",\n")
	if len(r.QueryParams) > 0 {
		parts := make([]string, len(r.QueryParams))
		for i, q := range r.QueryParams {
			parts[i] = q.WireName + ": params." + q.WireName
		}
		b.WriteString("          query: { " + strings.Join(parts, ", ") + " },\n")
	}
	if r.Upload && r.BodyType != nil && r.BodyType.Kind == ir.KindNamed {
		b.WriteString("          body: fd,\n")
	} else if r.BodyType != nil && r.BodyType.Kind == ir.KindNamed {
		b.WriteString("          body: parsed,\n")
	}
	b.WriteString("        });\n")
	if hasResp {
		b.WriteString("        return " + schemasExpr(*r.ResponseType) + ".parse(data);\n")
	}
	b.WriteString("      },")
	return b.String()
}

// tsTypeRef renders a named TypeRef as it appears in the client's
// method signatures (with the `t.` alias prefix on each named segment,
// matching the existing `import type * as t from "./types"` form).
// ADR 0033: instantiations carry TypeArgs which append `<X, Y>`,
// composable arbitrarily deep (Page<Result<User, Err>>). Builtin /
// slice / map TypeArgs fall through to tsType's regular rendering
// (no `t.` prefix needed for those).
func tsTypeRef(ref ir.TypeRef) string {
	if ref.Kind != ir.KindNamed {
		return tsType(ref, nil)
	}
	out := "t." + short(ref.Named)
	if len(ref.TypeArgs) > 0 {
		args := make([]string, len(ref.TypeArgs))
		for i, ta := range ref.TypeArgs {
			args[i] = tsTypeRef(*ta)
		}
		out += "<" + strings.Join(args, ", ") + ">"
	}
	return out
}

// schemasExpr renders a TypeRef as the corresponding zod expression
// for use at a `.parse(...)` call site, with `schemas.` prefix on
// named refs. A generic instantiation invokes the zod factory: a
// route returning `*Page[User]` produces
// `schemas.Page(schemas.User).parse(data)` (ADR 0033 §5).
//
// Per ADR 0022 §6 generator-local: the builtin map mirrors zod's
// zodExpr small-table for completeness (a Page<string>-style arg
// would otherwise have no zod expression at this call site).
// Validators / .optional() etc. are NOT applied here — at a
// .parse-call site we want the bare schema expression, not the
// field-decorated form.
func schemasExpr(ref ir.TypeRef) string {
	switch ref.Kind {
	case ir.KindNamed:
		out := "schemas." + short(ref.Named)
		if len(ref.TypeArgs) > 0 {
			args := make([]string, len(ref.TypeArgs))
			for i, ta := range ref.TypeArgs {
				args[i] = schemasExpr(*ta)
			}
			out += "(" + strings.Join(args, ", ") + ")"
		}
		return out
	case ir.KindBuiltin:
		switch ref.Builtin {
		case "string", "[]byte", "time.Time", "uuid.UUID":
			return "z.string()"
		case "bool":
			return "z.boolean()"
		case "json.RawMessage":
			return "z.unknown()"
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64", "time.Duration":
			return "z.number()"
		}
		panic("tsclient: cannot render zod expression for builtin " + ref.Builtin +
			" (most likely an unsupported generic TypeArg)")
	case ir.KindSlice:
		return "z.array(" + schemasExpr(*ref.Element) + ")"
	case ir.KindMap:
		return "z.record(" + schemasExpr(*ref.Key) + ", " + schemasExpr(*ref.Value) + ")"
	}
	panic("tsclient: unhandled TypeRef kind in schemasExpr")
}

// signature builds the method's argument list: a `params` object (path
// then query members; path required, query per Param.Optional) and/or a
// `body` argument. adapters threads ir.API.CustomAdapters through to
// tsType so adapted path/query types render per ADR 0032.
func signature(r ir.Route, adapters map[string]string) string {
	var args []string
	if len(r.PathParams)+len(r.QueryParams) > 0 {
		var m []string
		for _, p := range r.PathParams {
			m = append(m, p.WireName+": "+tsType(p.Type, adapters))
		}
		for _, q := range r.QueryParams {
			opt := ""
			if q.Optional {
				opt = "?"
			}
			m = append(m, q.WireName+opt+": "+tsType(q.Type, adapters))
		}
		args = append(args, "params: { "+strings.Join(m, "; ")+" }")
	}
	if r.BodyType != nil && r.BodyType.Kind == ir.KindNamed {
		args = append(args, "body: t."+short(r.BodyType.Named))
	}
	return strings.Join(args, ", ")
}

// pathExpr converts a Route.Path (colon params) into a TS template
// literal: `:name` → ${encodeURIComponent(params.name)}, other segments
// verbatim. Always backticked, even for static paths.
func pathExpr(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if name, ok := strings.CutPrefix(s, ":"); ok && name != "" {
			segs[i] = "${encodeURIComponent(params." + name + ")}"
		}
	}
	return "`" + strings.Join(segs, "/") + "`"
}


func short(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// tsType maps a param TypeRef to a TS type string. Generator-local per
// ADR 0022 §6. Params are primitives or []primitive (ADR 0014/PATH2);
// named/map handled for completeness. Custom-adapted builtins (ADR 0032)
// fall through to gen.AdapterWireTS by wire shape; otherwise unknown
// builtin/kind = analyzer-invariant violation, panic per ADR 0022 §5.
func tsType(ref ir.TypeRef, adapters map[string]string) string {
	switch ref.Kind {
	case ir.KindBuiltin:
		switch ref.Builtin {
		case "string", "time.Time", "[]byte", "uuid.UUID":
			return "string"
		case "bool":
			return "boolean"
		case "json.RawMessage":
			return "unknown"
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64", "time.Duration":
			return "number"
		}
		if wire, ok := adapters[ref.Builtin]; ok {
			if ts := gen.AdapterWireTS(wire); ts != "" {
				return ts
			}
		}
		panic("tsclient: unknown builtin " + ref.Builtin)
	case ir.KindNamed:
		return short(ref.Named)
	case ir.KindSlice:
		return tsType(*ref.Element, adapters) + "[]"
	case ir.KindMap:
		return "Record<" + tsType(*ref.Key, adapters) + ", " + tsType(*ref.Value, adapters) + ">"
	}
	panic("tsclient: unhandled TypeRef kind")
}
