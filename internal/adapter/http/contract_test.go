package http

import (
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type swaggerSpec struct {
	Paths       map[string]map[string]swaggerOperation `json:"paths"`
	Definitions map[string]swaggerSchema               `json:"definitions"`
}

type swaggerOperation struct {
	OperationID string                     `json:"operationId"`
	Responses   map[string]swaggerResponse `json:"responses"`
}

type swaggerResponse struct {
	Schema *swaggerSchema `json:"schema"`
}

type swaggerSchema struct {
	Ref        string                   `json:"$ref"`
	Format     string                   `json:"format"`
	Required   []string                 `json:"required"`
	Properties map[string]swaggerSchema `json:"properties"`
}

func loadSpec(t *testing.T) swaggerSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "api", "swagger.json"))
	if err != nil {
		t.Fatalf("read swagger.json (run `make swagger` first): %v", err)
	}
	var spec swaggerSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse swagger.json: %v", err)
	}
	return spec
}

func TestSwaggerOperationIDsAreUnique(t *testing.T) {
	spec := loadSpec(t)
	seen := map[string]string{}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			where := strings.ToUpper(method) + " " + path
			if op.OperationID == "" {
				t.Errorf("%s has no operationId", where)
				continue
			}
			if prev, dup := seen[op.OperationID]; dup {
				t.Errorf("operationId %q used by %s and %s", op.OperationID, prev, where)
			}
			seen[op.OperationID] = where
		}
	}
}

func TestSwaggerContractMatchesResponses(t *testing.T) {
	spec := loadSpec(t)
	tests := []struct {
		name       string
		path       string
		readiness  error
		wantStatus int
	}{
		{"healthz", "/healthz", nil, nethttp.StatusOK},
		{"readyz ready", "/readyz", nil, nethttp.StatusOK},
		{"readyz not ready", "/readyz", errors.New("db down"), nethttp.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, _ := newTestServer(t, fakeReadiness{err: tt.readiness})
			resp, body := doRequest(t, app, httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, tt.path, nil))
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			op, ok := spec.Paths[tt.path]["get"]
			if !ok {
				t.Fatalf("GET %s is not documented", tt.path)
			}
			declared, ok := op.Responses[strconv.Itoa(resp.StatusCode)]
			if !ok || declared.Schema == nil {
				t.Fatalf("GET %s returned %d which is not documented", tt.path, resp.StatusCode)
			}
			def, ok := spec.Definitions[strings.TrimPrefix(declared.Schema.Ref, "#/definitions/")]
			if !ok {
				t.Fatalf("schema %q not found in definitions", declared.Schema.Ref)
			}

			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			for prop := range def.Properties {
				if _, ok := got[prop]; !ok {
					t.Errorf("response missing documented property %q: %s", prop, body)
				}
			}
			for prop := range got {
				if _, ok := def.Properties[prop]; !ok {
					t.Errorf("response has undocumented property %q", prop)
				}
			}
		})
	}
}
