package api

import _ "embed"

// SwaggerJSON is the generated Swagger 2.0 spec served at /swagger.json.
//
//go:embed swagger.json
var SwaggerJSON []byte

// SwaggerYAML is the generated Swagger 2.0 spec served at /swagger.yaml.
//
//go:embed swagger.yaml
var SwaggerYAML []byte
