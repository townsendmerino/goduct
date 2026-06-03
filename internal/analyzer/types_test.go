package analyzer

import (
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

// dtTemp scaffolds a temp module, runs DiscoverRoutes then DiscoverTypes.
// resp is the body of a `type Resp struct{ ... }`; decls is extra
// top-level source. Pattern defaults to ".".
func dtTemp(t *testing.T, imports, decls, resp string, files map[string]string, pattern string) (map[string]ir.TypeDef, error) {
	t.Helper()
	dir := t.TempDir()
	if files == nil {
		src := "package m\nimport (\n\t\"context\"\n" + imports + ")\n" + decls +
			"type Req struct{}\ntype Resp struct{\n" + resp + "\n}\n" +
			"// goduct:route GET /x\nfunc H(ctx context.Context, r Req) (*Resp, error) { return nil, nil }\n"
		files = map[string]string{"go.mod": "module m\n\ngo 1.26\n", "f.go": src}
	}
	if pattern == "" {
		pattern = "."
	}
	writeFiles(t, dir, files)
	pkgs, err := Load([]string{pattern}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	routes, rerr := DiscoverRoutes(pkgs[0])
	if rerr != nil {
		t.Fatalf("DiscoverRoutes: %v", rerr)
	}
	return DiscoverTypes(pkgs[0], routes)
}

func TestDiscoverTypes_ChiBasic(t *testing.T) {
	pkgs, err := Load([]string{"./examples/chi-basic/api"}, LoadOptions{Dir: repoRoot(t)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	routes, err := DiscoverRoutes(pkgs[0])
	if err != nil {
		t.Fatalf("DiscoverRoutes: %v", err)
	}
	got, err := DiscoverTypes(pkgs[0], routes)
	if err != nil {
		t.Fatalf("DiscoverTypes: %v", err)
	}

	want := []string{"User", "Profile", "UserStatus", "ListUsersResponse",
		"CreateUserRequest", "UpdateUserRequest", "GetUserRequest",
		"DeleteUserRequest", "ListUsersRequest", "ValidationError",
		"UploadAvatarRequest", "WatchUserEventsRequest", "UserEvent"}
	if len(got) != len(want) {
		t.Fatalf("got %d types, want %d: %v", len(got), len(want), keysOf(got))
	}
	for _, w := range want {
		if _, ok := got[chiBasicPkg+"."+w]; !ok {
			t.Errorf("missing type %s", chiBasicPkg+"."+w)
		}
	}

	user := got[chiBasicPkg+".User"]
	if user.Kind != ir.TypeStruct || user.Name != "User" || len(user.Fields) != 5 {
		t.Fatalf("User = %+v", user)
	}
	for i, f := range user.Fields {
		if f.Source != ir.FieldSourceJSON {
			t.Errorf("User.%s Source = %v, want JSON", f.GoName, f.Source)
		}
		if i == 4 { // Profile
			if f.GoName != "Profile" || !f.Optional || f.Type.Kind != ir.KindNamed ||
				f.Type.Named != chiBasicPkg+".Profile" {
				t.Errorf("User.Profile = %+v", f)
			}
		}
	}

	us := got[chiBasicPkg+".UserStatus"]
	if us.Kind != ir.TypeEnum || us.Underlying != "string" || len(us.EnumValues) != 3 {
		t.Fatalf("UserStatus = %+v", us)
	}
	wantEnum := []ir.EnumValue{
		{GoName: "UserStatusActive", Value: "active"},
		{GoName: "UserStatusInvited", Value: "invited"},
		{GoName: "UserStatusSuspended", Value: "suspended"},
	}
	for i, ev := range wantEnum {
		if us.EnumValues[i].GoName != ev.GoName || us.EnumValues[i].Value != ev.Value {
			t.Errorf("UserStatus[%d] = %+v, want %+v", i, us.EnumValues[i], ev)
		}
	}

	uur := got[chiBasicPkg+".UpdateUserRequest"]
	if uur.Kind != ir.TypeStruct || len(uur.Fields) != 3 {
		t.Fatalf("UpdateUserRequest = %+v", uur)
	}
	byName := map[string]ir.Field{}
	for _, f := range uur.Fields {
		byName[f.GoName] = f
	}
	if byName["ID"].Source != ir.FieldSourcePath {
		t.Errorf("UpdateUserRequest.ID Source = %v, want Path", byName["ID"].Source)
	}
	if !byName["Name"].Optional || byName["Name"].Source != ir.FieldSourceJSON {
		t.Errorf("UpdateUserRequest.Name = %+v", byName["Name"])
	}
	if !byName["Status"].Optional || byName["Status"].Type.Named != chiBasicPkg+".UserStatus" {
		t.Errorf("UpdateUserRequest.Status = %+v", byName["Status"])
	}

	prof := got[chiBasicPkg+".Profile"]
	pf := map[string]ir.Field{}
	for _, f := range prof.Fields {
		pf[f.GoName] = f
	}
	if pf["Tags"].Source != ir.FieldSourceJSON || pf["Tags"].Type.Kind != ir.KindSlice {
		t.Errorf("Profile.Tags = %+v", pf["Tags"])
	}
	if !pf["AvatarURL"].Optional {
		t.Errorf("Profile.AvatarURL Optional = false, want true (omitempty)")
	}
}

func keysOf(m map[string]ir.TypeDef) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	return k
}

func TestDiscoverTypes_Errors(t *testing.T) {
	tests := []struct {
		name    string
		imports string
		decls   string
		resp    string
		wantCat string
	}{
		{"A1 chan", "", "", "C chan int `json:\"c\"`", "A1"},
		{"A2 func", "", "", "F func() `json:\"f\"`", "A2"},
		{"A3 complex", "", "", "X complex128 `json:\"x\"`", "A3"},
		{"A4 unsafe", "\t\"unsafe\"\n", "", "P unsafe.Pointer `json:\"p\"`", "A4"},
		{"A5 uintptr", "", "", "U uintptr `json:\"u\"`", "A5"},
		{"B1 map key", "", "", "M map[int]string `json:\"m\"`", "B1"},
		{"B2 interface", "", "", "I any `json:\"i\"`", "B2"},
		{"B3 anon struct", "", "", "A struct{ X int } `json:\"a\"`", "B3"},
		{"B4 named bad underlying", "", "type Bad chan int\n", "B Bad `json:\"b\"`", "B4"},
		// ADR 0033 (v0.3) lifted the old C1 "generics deferred" — Box[int]
		// is now a valid instantiation. Constraint-bearing generics
		// (other than `any`) keep the C1 loud-fail and are exercised
		// separately in TestDiscoverTypes_Generics_ConstraintLoudFail.
		{"C3 marshaljson", "", "type Cust struct{}\nfunc (Cust) MarshalJSON() ([]byte, error) { return nil, nil }\n", "C Cust `json:\"c\"`", "C3"},
		{"E2 path on response", "", "", "ID string `path:\"id\"`", "E2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := dtTemp(t, tt.imports, tt.decls, tt.resp, nil, "")
			if err == nil {
				t.Fatalf("expected error category %s, got nil", tt.wantCat)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "goduct: ") || !strings.Contains(msg, " "+tt.wantCat+": ") {
				t.Errorf("want category %s in Format B error, got: %s", tt.wantCat, msg)
			}
		})
	}
}

func TestDiscoverTypes_CrossPackage_C2(t *testing.T) {
	files := map[string]string{
		"go.mod":         "module m\n\ngo 1.26\n",
		"other/other.go": "package other\ntype T struct{ X int }\n",
		"f.go": "package m\nimport (\n\t\"context\"\n\t\"m/other\"\n)\n" +
			"type Req struct{}\ntype Resp struct{ T other.T `json:\"t\"` }\n" +
			"// goduct:route GET /x\nfunc H(ctx context.Context, r Req) (*Resp, error) { return nil, nil }\n",
	}
	_, err := dtTemp(t, "", "", "", files, ".")
	if err == nil || !strings.Contains(err.Error(), "C2") ||
		!strings.Contains(err.Error(), "cross-package") {
		t.Fatalf("want C2 cross-package error, got: %v", err)
	}
}

func TestDiscoverTypes_Cycles(t *testing.T) {
	// self-referential
	got, err := dtTemp(t, "", "type Node struct{ Next *Node `json:\"next\"` }\n",
		"N *Node `json:\"n\"`", nil, "")
	if err != nil {
		t.Fatalf("self-cycle: %v", err)
	}
	node := got["m.Node"]
	if node.Kind != ir.TypeStruct || len(node.Fields) != 1 ||
		node.Fields[0].Type.Named != "m.Node" || !node.Fields[0].Optional {
		t.Fatalf("Node = %+v", node)
	}

	// mutual
	got, err = dtTemp(t, "",
		"type A struct{ B *B `json:\"b\"` }\ntype B struct{ A *A `json:\"a\"` }\n",
		"X *A `json:\"x\"`", nil, "")
	if err != nil {
		t.Fatalf("mutual cycle: %v", err)
	}
	if _, ok := got["m.A"]; !ok {
		t.Error("missing m.A")
	}
	if _, ok := got["m.B"]; !ok {
		t.Error("missing m.B")
	}
}

func TestDiscoverTypes_Enums(t *testing.T) {
	// string enum
	got, _ := dtTemp(t, "",
		"type Color string\nconst ( Red Color = \"red\"; Green Color = \"green\" )\n",
		"C Color `json:\"c\"`", nil, "")
	c := got["m.Color"]
	if c.Kind != ir.TypeEnum || c.Underlying != "string" || len(c.EnumValues) != 2 {
		t.Fatalf("Color = %+v", c)
	}

	// int enum
	got, _ = dtTemp(t, "",
		"type Lvl int\nconst ( Low Lvl = 1; High Lvl = 2 )\n",
		"L Lvl `json:\"l\"`", nil, "")
	l := got["m.Lvl"]
	if l.Kind != ir.TypeEnum || l.Underlying != "int" || l.EnumValues[0].Value != "1" {
		t.Fatalf("Lvl = %+v", l)
	}

	// named string, NO consts → alias, not enum
	got, _ = dtTemp(t, "", "type UserID string\n", "U UserID `json:\"u\"`", nil, "")
	u := got["m.UserID"]
	if u.Kind != ir.TypeAlias || u.AliasTo == nil || u.AliasTo.Builtin != "string" {
		t.Fatalf("UserID = %+v (want TypeAlias→string)", u)
	}
}

func TestDiscoverTypes_SpecialBuiltins(t *testing.T) {
	got, err := dtTemp(t, "\t\"time\"\n\t\"encoding/json\"\n",
		"type BS []byte\n",
		"When time.Time `json:\"when\"`\n\tPWhen *time.Time `json:\"pwhen\"`\n\tBytes []byte `json:\"bytes\"`\n\tDur time.Duration `json:\"dur\"`\n\tRaw json.RawMessage `json:\"raw\"`\n\tAl BS `json:\"al\"`",
		nil, "")
	if err != nil {
		t.Fatalf("special builtins: %v", err)
	}
	for _, k := range []string{"time.Time", "time.Duration", "encoding/json.RawMessage"} {
		if _, ok := got[k]; ok {
			t.Errorf("special type %s must NOT get a TypeDef", k)
		}
	}
	resp := got["m.Resp"]
	by := map[string]ir.Field{}
	for _, f := range resp.Fields {
		by[f.GoName] = f
	}
	if by["When"].Type.Kind != ir.KindBuiltin || by["When"].Type.Builtin != "time.Time" {
		t.Errorf("When = %+v", by["When"])
	}
	if !by["PWhen"].Optional || by["PWhen"].Type.Builtin != "time.Time" {
		t.Errorf("PWhen = %+v (want optional time.Time)", by["PWhen"])
	}
	if by["Bytes"].Type.Builtin != "[]byte" || by["Dur"].Type.Builtin != "time.Duration" ||
		by["Raw"].Type.Builtin != "json.RawMessage" {
		t.Errorf("Bytes/Dur/Raw = %+v %+v %+v", by["Bytes"], by["Dur"], by["Raw"])
	}
	// named []byte alias is NOT the []byte special builtin: D5 TypeAlias.
	if by["Al"].Type.Kind != ir.KindNamed || by["Al"].Type.Named != "m.BS" {
		t.Errorf("Al = %+v (want KindNamed m.BS)", by["Al"])
	}
	bs := got["m.BS"]
	if bs.Kind != ir.TypeAlias || bs.AliasTo == nil || bs.AliasTo.Kind != ir.KindSlice {
		t.Errorf("BS = %+v (want TypeAlias→slice, D5)", bs)
	}
}
