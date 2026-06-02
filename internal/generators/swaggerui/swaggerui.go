// Package swaggerui generates swagger-ui.html: a one-file static
// page that loads Swagger UI from the public CDN and points at
// ./openapi.json (the sibling file the OpenAPI generator writes).
// See ADR 0035 for the design pins — CDN-loaded (no bundled JS),
// pinned to Swagger UI major v5, zero configuration.
package swaggerui

import (
	"html"
	"io"
	"strings"

	"github.com/townsendmerino/goduct/internal/gen"
	"github.com/townsendmerino/goduct/internal/ir"
)

// page is the HTML template. The %s slot is the escaped page title.
// Indentation matches Prettier's HTML default (2 spaces) so users
// editing the file see consistent shape with everything else goduct
// writes.
const page = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <title>%s</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script>
      window.onload = () => {
        window.ui = SwaggerUIBundle({
          url: "./openapi.json",
          dom_id: "#swagger-ui",
        });
      };
    </script>
  </body>
</html>
`

// Generate writes swagger-ui.html for api to w (ADR 0035). The page
// title is the API's package name; empty input falls back to
// "goduct" rather than producing an empty <title> tag.
func Generate(api *ir.API, w io.Writer) error {
	title := gen.PackageName(api)
	if title == "" {
		title = "goduct"
	}
	out := strings.Replace(page, "%s", html.EscapeString(title), 1)
	_, err := io.WriteString(w, out)
	return err
}
