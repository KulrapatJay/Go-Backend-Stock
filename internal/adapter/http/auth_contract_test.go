package http

import (
	"strconv"
	"testing"
)

// documentedAuthOperations lists every auth operation with the statuses its handler can return. The
// generated Swagger spec must declare each of them, so the docs cannot fall behind the code.
var documentedAuthOperations = []struct {
	method, path string
	statuses     []int
}{
	{"get", "/api/v1/auth/csrf", []int{200, 503}},
	{"post", "/api/v1/auth/register", []int{202, 400, 403, 429, 503}},
	{"post", "/api/v1/auth/email-verification/request", []int{202, 400, 403, 429}},
	{"post", "/api/v1/auth/email-verification/confirm", []int{204, 400, 403, 429}},
}

func TestAuthOperationsAreDocumented(t *testing.T) {
	spec := loadSpec(t)
	for _, op := range documentedAuthOperations {
		declared, ok := spec.Paths[op.path][op.method]
		if !ok {
			t.Errorf("%s %s is not in swagger.json", op.method, op.path)
			continue
		}
		for _, status := range op.statuses {
			if _, ok := declared.Responses[strconv.Itoa(status)]; !ok {
				t.Errorf("%s %s does not declare status %d", op.method, op.path, status)
			}
		}
	}
}
