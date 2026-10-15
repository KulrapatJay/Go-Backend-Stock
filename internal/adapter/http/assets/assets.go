// Package assets embeds the Swagger UI files served under /docs/.
package assets

import "embed"

// SwaggerUI holds Swagger UI 5.33.1 (Apache-2.0, see swagger-ui/LICENSE and NOTICE) and index.html.
//
//go:embed swagger-ui
var SwaggerUI embed.FS
