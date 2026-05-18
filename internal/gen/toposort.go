package gen

import (
	"sort"
	"strconv"
	"strings"

	"github.com/townsendmerino/goduct/internal/ir"
)

// posKey turns a TypeDef.Pos of the form "file:line:col" into a sortable
// (file, line) — file compared alphabetically, line numerically (NEVER
// string-compared: "9" > "115" lexically). Unparseable Pos sorts last
// (defensive; the analyzer always emits the canonical form — such a case
// is an analyzer bug).
func posKey(pos string) (file string, line int) {
	const last = 1<<62 - 1
	c := strings.LastIndex(pos, ":")
	if c < 0 {
		return pos, last
	}
	l := strings.LastIndex(pos[:c], ":")
	if l < 0 {
		return pos, last
	}
	n, err := strconv.Atoi(pos[l+1 : c])
	if err != nil {
		return pos[:l], last
	}
	return pos[:l], n
}

// srcLess orders two TypeDefs by source declaration position (ADR 0022
// §3): file alphabetically, then line numerically.
func srcLess(a, b ir.TypeDef) bool {
	fa, la := posKey(a.Pos)
	fb, lb := posKey(b.Pos)
	if fa != fb {
		return fa < fb
	}
	return la < lb
}

// namedDeps collects the qualified names td references through its fields'
// (and alias's) TypeRef trees.
func namedDeps(td ir.TypeDef) []string {
	var out []string
	var walk func(r *ir.TypeRef)
	walk = func(r *ir.TypeRef) {
		if r == nil {
			return
		}
		if r.Kind == ir.KindNamed && r.Named != "" {
			out = append(out, r.Named)
		}
		walk(r.Element)
		walk(r.Key)
		walk(r.Value)
	}
	for i := range td.Fields {
		walk(&td.Fields[i].Type)
	}
	walk(td.AliasTo)
	return out
}

// TopoSortTypes returns every type in api.Types in dependency order: if A
// references B, B precedes A. Ties (and SCC members, for legal IR cycles
// per ADR 0018 D4) break by source declaration order (ADR 0022 §3). All
// types are returned unfiltered; generators apply their own emission
// predicate (e.g. EmitTS) at emit time so non-emitted types still inform
// the order of emitted ones.
func TopoSortTypes(api *ir.API) []ir.TypeDef {
	names := make([]string, 0, len(api.Types))
	for n := range api.Types {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		return srcLess(api.Types[names[i]], api.Types[names[j]])
	})

	deps := map[string][]string{}
	for _, n := range names {
		seen := map[string]bool{}
		for _, d := range namedDeps(api.Types[n]) {
			if _, ok := api.Types[d]; ok && d != n && !seen[d] {
				seen[d] = true
				deps[n] = append(deps[n], d)
			}
		}
	}

	sccs := tarjanSCC(names, deps)
	sccOf := map[string]int{}
	for i, comp := range sccs {
		sort.Slice(comp, func(a, b int) bool {
			return srcLess(api.Types[comp[a]], api.Types[comp[b]])
		})
		for _, v := range comp {
			sccOf[v] = i
		}
	}
	// Condensation dependency edges: scc i depends on scc j (j first).
	cdeps := make([]map[int]bool, len(sccs))
	for i := range cdeps {
		cdeps[i] = map[int]bool{}
	}
	for u, ds := range deps {
		for _, v := range ds {
			if iu, iv := sccOf[u], sccOf[v]; iu != iv {
				cdeps[iu][iv] = true
			}
		}
	}

	// Kahn over the condensation, picking the ready SCC with the
	// smallest source key (its first, already source-sorted, member).
	emitted := make([]bool, len(sccs))
	var order []ir.TypeDef
	for done := 0; done < len(sccs); done++ {
		best := -1
		for i := range sccs {
			if emitted[i] {
				continue
			}
			ready := true
			for j := range cdeps[i] {
				if !emitted[j] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if best == -1 || srcLess(api.Types[sccs[i][0]], api.Types[sccs[best][0]]) {
				best = i
			}
		}
		emitted[best] = true // best != -1: an SCC DAG always has a ready node
		for _, n := range sccs[best] {
			order = append(order, api.Types[n])
		}
	}
	return order
}

// tarjanSCC returns the strongly connected components of the dependency
// graph. nodes must already be in deterministic (source) order.
func tarjanSCC(nodes []string, deps map[string][]string) [][]string {
	idx := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var sccs [][]string
	counter := 0

	var sc func(v string)
	sc = func(v string) {
		idx[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range deps[v] {
			if _, seen := idx[w]; !seen {
				sc(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && idx[w] < low[v] {
				low[v] = idx[w]
			}
		}
		if low[v] == idx[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, comp)
		}
	}
	for _, n := range nodes {
		if _, seen := idx[n]; !seen {
			sc(n)
		}
	}
	return sccs
}
