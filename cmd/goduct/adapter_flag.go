package main

// adapter_flag.go implements the flag.Value used for the repeatable
// --adapter <qname>=<wire> CLI flag (ADR 0032 §1). Each Set call
// appends one declaration; the accumulated map is passed through to
// analyzer.LoadOptions.CustomAdapters.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/townsendmerino/goduct/internal/gen"
)

// adapterFlag accumulates --adapter declarations into a map. It is the
// flag.Value contract for repeated invocations of the same flag name;
// Go's stdlib flag calls Set once per repetition (e.g.
// `--adapter A=string --adapter B=number` becomes two Set calls).
type adapterFlag struct {
	pairs map[string]string
}

// String renders the accumulated pairs in a stable order so flag
// defaults / printouts are deterministic.
func (a *adapterFlag) String() string {
	if len(a.pairs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(a.pairs))
	for k := range a.pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + a.pairs[k]
	}
	return strings.Join(parts, ",")
}

// Set parses one <qname>=<wire> declaration and accumulates it.
// Validation per ADR 0032 §7: wire is in {string,number,boolean,
// unknown}; qname contains a '.' (catches `--adapter Decimal=string`
// typos without the package prefix). Other malformedness is caught
// implicitly when nothing in the analyzed source matches the qname —
// silently a no-op, which is the right behavior for shared CI scripts.
func (a *adapterFlag) Set(s string) error {
	i := strings.Index(s, "=")
	if i < 0 {
		return fmt.Errorf("--adapter %q: expected <qname>=<wire>", s)
	}
	qname, wire := s[:i], s[i+1:]
	if qname == "" {
		return fmt.Errorf("--adapter %q: qname cannot be empty", s)
	}
	if !strings.Contains(qname, ".") {
		return fmt.Errorf("--adapter %q: qname must contain '.' "+
			"(e.g. github.com/shopspring/decimal.Decimal)", s)
	}
	if !validWire(wire) {
		return fmt.Errorf("--adapter %q: wire must be one of %s, got %q",
			s, strings.Join(gen.AdapterWires, "/"), wire)
	}
	if a.pairs == nil {
		a.pairs = make(map[string]string)
	}
	a.pairs[qname] = wire
	return nil
}

// Map returns the accumulated map. Returns nil when no --adapter was
// passed, so downstream LoadOptions/IR treat "no adapters" as the
// pre-ADR-0032 path (zero-value semantics preserved).
func (a *adapterFlag) Map() map[string]string {
	return a.pairs
}

func validWire(w string) bool {
	for _, v := range gen.AdapterWires {
		if v == w {
			return true
		}
	}
	return false
}
