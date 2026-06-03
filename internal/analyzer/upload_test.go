package analyzer

// upload_test.go covers ADR 0042: typed-upload detection
// (multipart/form tags → Route.Upload + BodyType set), the
// json+multipart co-occurrence loud-fail, and the raw-mode
// goduct:upload toggle.

import (
	"strings"
	"testing"

	"github.com/townsendmerino/goduct/internal/ir"
)

const typedUploadFixture = `package svc

import (
	"context"
	"mime/multipart"
)

type AvatarReq struct {
	UserID  string                ` + "`path:\"id\"        validate:\"required\"`" + `
	File    *multipart.FileHeader ` + "`multipart:\"file\" validate:\"required\"`" + `
	Caption string                ` + "`form:\"caption\"`" + `
}

type User struct {
	ID string ` + "`json:\"id\"`" + `
}

// goduct:route POST /users/:id/avatar
// goduct:tag   users
func Upload(ctx context.Context, req AvatarReq) (*User, error) {
	_ = req.File
	return nil, nil
}
`

func TestDiscoverRoutes_TypedUpload(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod": "module svc\n\ngo 1.26\n",
		"f.go":   typedUploadFixture,
	})
	api, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(api.Routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(api.Routes))
	}
	r := api.Routes[0]
	if !r.Upload {
		t.Errorf("Route.Upload should be true for a multipart-tagged request struct")
	}
	if r.BodyType == nil || r.BodyType.Named != "svc.AvatarReq" {
		t.Errorf("BodyType should point at the request struct, got %+v", r.BodyType)
	}
	if len(r.PathParams) != 1 || r.PathParams[0].WireName != "id" {
		t.Errorf("PathParams = %+v, want [id]", r.PathParams)
	}
	// Multipart/form fields live on the request type's TypeDef, not
	// on Route.QueryParams/HeaderParams — those are only for
	// path/query/header.
	td, ok := api.Types["svc.AvatarReq"]
	if !ok {
		t.Fatal("svc.AvatarReq missing from api.Types")
	}
	var sawFile, sawCaption bool
	for _, f := range td.Fields {
		switch f.Source {
		case ir.FieldSourceMultipart:
			sawFile = true
			if f.JSONName != "file" {
				t.Errorf("multipart field JSONName = %q, want file", f.JSONName)
			}
			if f.Optional {
				t.Errorf("multipart file should be required (validate:required)")
			}
		case ir.FieldSourceForm:
			sawCaption = true
			if f.JSONName != "caption" {
				t.Errorf("form field JSONName = %q, want caption", f.JSONName)
			}
			if !f.Optional {
				t.Errorf("form caption should be optional (no validate:required)")
			}
		}
	}
	if !sawFile || !sawCaption {
		t.Errorf("AvatarReq should have multipart + form fields; sawFile=%v sawCaption=%v", sawFile, sawCaption)
	}
}

// TestDiscoverRoutes_UploadLoudFails covers the three rejected
// upload-related shapes per ADR 0042: json+multipart co-occurrence,
// multipart on a non-FileHeader field, and goduct:upload on an
// idiomatic handler.
func TestDiscoverRoutes_UploadLoudFails(t *testing.T) {
	cases := []struct {
		name, src, wantSub string
	}{
		{
			"json + multipart on same struct",
			`package svc
import (
	"context"
	"mime/multipart"
)
type R struct {
	Name string                ` + "`json:\"name\"`" + `
	File *multipart.FileHeader ` + "`multipart:\"file\"`" + `
}
type U struct { ID string ` + "`json:\"id\"`" + ` }
// goduct:route POST /x
func H(ctx context.Context, req R) (*U, error) { return nil, nil }
`,
			"mixes json: and multipart:/form: tags",
		},
		{
			"multipart on a non-FileHeader field",
			`package svc
import "context"
type R struct {
	File string ` + "`multipart:\"file\"`" + `
}
type U struct { ID string ` + "`json:\"id\"`" + ` }
// goduct:route POST /x
func H(ctx context.Context, req R) (*U, error) { return nil, nil }
`,
			"*multipart.FileHeader",
		},
		{
			"goduct:upload on idiomatic handler",
			`package svc
import "context"
type R struct { Name string ` + "`json:\"name\"`" + ` }
type U struct { ID string ` + "`json:\"id\"`" + ` }
// goduct:route POST /x
// goduct:upload
func H(ctx context.Context, req R) (*U, error) { return nil, nil }
`,
			"not allowed on idiomatic handlers",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{
				"go.mod": "module svc\n\ngo 1.26\n",
				"f.go":   c.src,
			})
			_, err := Analyze([]string{"."}, LoadOptions{Dir: dir})
			if err == nil {
				t.Fatal("expected loud-fail, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error should mention %q, got: %v", c.wantSub, err)
			}
		})
	}
}
