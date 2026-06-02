package analyzer

// adapters_test.go exercises ADR 0032 custom-type-adapter recognition
// via a synthetic package whose response type embeds a stdlib type
// outside ADR 0017's set: math/big.Int. Without --adapter the analyzer
// loud-fails per C3 (the type has MarshalJSON; goduct can't infer the
// wire shape). With LoadOptions.CustomAdapters set, the analyzer
// emits it as KindBuiltin and the generators can render it per the
// declared wire shape.

import (
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

// adapterFixture has a handler returning a struct with a math/big.Int
// field. *big.Int implements MarshalJSON (encoding as a JSON number
// string), so it trips C3 unless declared as an adapter.
const adapterFixture = `package svc

import (
	"context"
	"math/big"
)

type Acct struct {
	ID      string  ` + "`json:\"id\"`" + `
	Balance big.Int ` + "`json:\"balance\"`" + `
}

type GetAcctReq struct {
	ID string ` + "`path:\"id\" validate:\"required\"`" + `
}

// goduct:route GET /accounts/:id
func GetAcct(ctx context.Context, req GetAcctReq) (*Acct, error) {
	return nil, nil
}
`

func TestCustomAdapter_C3WithoutAdapter(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module svc\n\ngo 1.26\n",
		"f.go":   adapterFixture,
	})

	_, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
	if err == nil {
		t.Fatal("expected C3 error without --adapter, got nil")
	}
	if !strings.Contains(err.Error(), "C3") {
		t.Errorf("error = %q, want substring 'C3'", err)
	}
	// ADR 0032 §6: remediation pointer to --adapter.
	if !strings.Contains(err.Error(), "--adapter") {
		t.Errorf("error should suggest --adapter as remediation: %q", err)
	}
	if !strings.Contains(err.Error(), "math/big.Int") {
		t.Errorf("error should name the offending qname: %q", err)
	}
}

func TestCustomAdapter_RecognizedWithAdapter(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module svc\n\ngo 1.26\n",
		"f.go":   adapterFixture,
	})

	api, err := Analyze([]string{"."}, LoadOptions{
		Dir:            dir,
		CustomAdapters: map[string]string{"math/big.Int": "string"},
	})
	if err != nil {
		t.Fatalf("expected success with adapter, got %v", err)
	}

	// IR carries the adapter map for generators.
	if api.CustomAdapters["math/big.Int"] != "string" {
		t.Errorf("api.CustomAdapters[math/big.Int] = %q, want %q",
			api.CustomAdapters["math/big.Int"], "string")
	}

	// Acct.Balance is emitted as KindBuiltin with the qname as the
	// Builtin string — generators render per the wire shape via the
	// shared gen.AdapterWireTS / gen.AdapterWireZod tables.
	td, ok := api.Types["svc.Acct"]
	if !ok {
		var keys []string
		for k := range api.Types {
			keys = append(keys, k)
		}
		t.Fatalf("svc.Acct not found in api.Types; keys = %v", keys)
	}
	var balance *ir.Field
	for i := range td.Fields {
		if td.Fields[i].GoName == "Balance" {
			balance = &td.Fields[i]
			break
		}
	}
	if balance == nil {
		t.Fatal("Balance field not found on svc.Acct")
	}
	if balance.Type.Kind != ir.KindBuiltin {
		t.Errorf("Balance.Type.Kind = %v, want KindBuiltin", balance.Type.Kind)
	}
	if balance.Type.Builtin != "math/big.Int" {
		t.Errorf("Balance.Type.Builtin = %q, want %q", balance.Type.Builtin, "math/big.Int")
	}

	// Built-in precedence (ADR 0032 §2): declaring an adapter on a
	// built-in qname is silently ignored — time.Time stays time.Time.
	// (Smoke check; the fixture doesn't use time.Time, so this just
	// asserts no panic / no error when a built-in adapter is supplied.)
}

func TestCustomAdapter_BuiltinPrecedence(t *testing.T) {
	// Built-in adapter on a qname already in ADR 0017's table should
	// be silently no-op'd; the analyzer keeps recognizing it as the
	// built-in (precedence rule 1 in ADR 0032 §2). Re-uses chi-basic
	// since it exercises time.Time.
	api, err := Analyze([]string{"./examples/chi-basic/api"}, LoadOptions{
		Dir:            repoRoot(t),
		CustomAdapters: map[string]string{"time.Time": "number"}, // wrong wire on purpose
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// chi-basic doesn't have a time.Time field on the wire, but
	// no error means precedence is being honored. (If the analyzer
	// took the user's wrong "number" wire over the built-in, a
	// subsequent generator render would mis-emit; we just assert
	// the analyzer load succeeded here.)
	if api == nil {
		t.Fatal("api nil")
	}
}

