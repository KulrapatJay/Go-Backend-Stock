package http

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// fullSpec reads more of swagger.json than loadSpec does: tags, descriptions, response schemas and
// the types of every definition.
type fullSpec struct {
	Info struct {
		Description string `json:"description"`
	} `json:"info"`
	Paths       map[string]map[string]fullOperation `json:"paths"`
	Definitions map[string]fullDefinition           `json:"definitions"`
}

type fullOperation struct {
	Tags        []string                `json:"tags"`
	Summary     string                  `json:"summary"`
	Description string                  `json:"description"`
	Parameters  []fullParameter         `json:"parameters"`
	Responses   map[string]fullResponse `json:"responses"`
}

type fullParameter struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type fullResponse struct {
	Schema *fullDefinition `json:"schema"`
}

type fullDefinition struct {
	Ref        string                    `json:"$ref"`
	Type       string                    `json:"type"`
	Required   []string                  `json:"required"`
	Properties map[string]fullDefinition `json:"properties"`
	Items      *fullDefinition           `json:"items"`
}

func readSpec(t *testing.T) (fullSpec, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "api", "swagger.json"))
	if err != nil {
		t.Fatalf("read swagger.json (run `make swagger` first): %v", err)
	}
	var spec fullSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse swagger.json: %v", err)
	}
	return spec, raw
}

var fiberParam = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)

// TestEveryRouteIsDocumented compares the routes the real server registers with the paths of the
// generated spec in both directions, so neither an undocumented route nor a documented route that
// does not exist can pass. HEAD is served for every GET by getAndHead and needs no entry of its own;
// the Swagger UI and spec routes document themselves.
func TestEveryRouteIsDocumented(t *testing.T) {
	spec, _ := readSpec(t)
	h := newAuthHarness(t, withValuation(), withBars())

	registered := map[string]bool{}
	heads := map[string]bool{}
	for _, route := range h.app.GetRoutes(true) {
		path := fiberParam.ReplaceAllString(route.Path, "{$1}")
		if path == "/docs" || strings.HasPrefix(path, "/docs/") || path == "/swagger.json" || path == "/swagger.yaml" {
			continue
		}
		switch method := strings.ToLower(route.Method); method {
		case "head":
			heads[path] = true
		default:
			registered[method+" "+path] = true
		}
	}

	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			documented[method+" "+path] = true
		}
	}
	for key := range registered {
		if !documented[key] {
			t.Errorf("route %s is served but not in swagger.json", key)
		}
	}
	for key := range documented {
		if !registered[key] {
			t.Errorf("swagger.json documents %s but the server has no such route", key)
		}
	}
	for path := range heads {
		if !registered["get "+path] {
			t.Errorf("HEAD %s is served without a GET", path)
		}
	}
}

var documentedTags = []string{"Health", "Auth", "Password Reset", "Portfolios", "Transactions", "Holdings/Prices"}

func TestEveryOperationIsDescribed(t *testing.T) {
	spec, _ := readSpec(t)
	envelope, ok := spec.Definitions["http.ErrorResponse"]
	if !ok || envelope.Properties["error"].Ref != "#/definitions/http.ErrorBody" {
		t.Fatalf("http.ErrorResponse must wrap http.ErrorBody, got %+v", envelope)
	}
	body := spec.Definitions["http.ErrorBody"]
	for _, field := range []string{"code", "message", "request_id"} {
		if !slices.Contains(body.Required, field) {
			t.Errorf("http.ErrorBody does not require %q", field)
		}
	}

	for path, ops := range spec.Paths {
		for method, op := range ops {
			where := strings.ToUpper(method) + " " + path
			if strings.TrimSpace(op.Summary) == "" || strings.TrimSpace(op.Description) == "" {
				t.Errorf("%s needs a summary and a description", where)
			}
			if len(op.Tags) != 1 || !slices.Contains(documentedTags, op.Tags[0]) {
				t.Errorf("%s tags = %v, want exactly one of %v", where, op.Tags, documentedTags)
				continue
			}
			switch tag := op.Tags[0]; {
			case strings.HasPrefix(path, "/api/v1/auth/password-reset/") && tag != "Password Reset":
				t.Errorf("%s is tagged %q, want Password Reset", where, tag)
			case strings.HasPrefix(path, "/api/v1/auth/") && !strings.HasPrefix(path, "/api/v1/auth/password-reset/") && tag != "Auth":
				t.Errorf("%s is tagged %q, want Auth", where, tag)
			}
			if op.Tags[0] == "Health" {
				continue
			}
			failures := 0
			for status, resp := range op.Responses {
				code, err := strconv.Atoi(status)
				if err != nil || code < 400 {
					continue
				}
				failures++
				if resp.Schema == nil || resp.Schema.Ref != "#/definitions/http.ErrorResponse" {
					t.Errorf("%s status %s does not use the http.ErrorResponse envelope", where, status)
				}
			}
			if failures == 0 {
				t.Errorf("%s documents no error response", where)
			}
		}
	}
}

// moneyFreeIntegers are the only integer fields in the API: counters and numbers of seconds or
// minutes. Money, prices, share counts and percentages are decimal strings (AGENTS.md), so a new
// integer or any number is a decision to make deliberately, not by accident.
var moneyFreeIntegers = []string{
	"http.OTPAcceptedResponse.expires_in",
	"http.PortfolioResponse.version",
	"http.QuoteResponse.delay_minutes",
	"http.ResetTokenResponse.expires_in",
	"http.TransactionResponse.version",
}

func TestSpecHasNoNumbersForMoney(t *testing.T) {
	spec, _ := readSpec(t)
	var integers []string
	var walk func(path string, def fullDefinition)
	walk = func(path string, def fullDefinition) {
		switch def.Type {
		case "number":
			t.Errorf("%s is a JSON number; money, prices and quantities must be decimal strings", path)
		case "integer":
			integers = append(integers, path)
		}
		for name, prop := range def.Properties {
			walk(path+"."+name, prop)
		}
		if def.Items != nil {
			walk(path+"[]", *def.Items)
		}
	}
	for name, def := range spec.Definitions {
		walk(name, def)
	}
	sort.Strings(integers)
	if !slices.Equal(integers, moneyFreeIntegers) {
		t.Errorf("integer fields = %v, want %v", integers, moneyFreeIntegers)
	}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			for _, p := range op.Parameters {
				if p.Type == "number" {
					t.Errorf("%s %s parameter %s is a JSON number", strings.ToUpper(method), path, p.Name)
				}
			}
		}
	}
}

var credentialShapes = regexp.MustCompile(`(?i)(APCA-|GOCSPX-|postgres://[^"]*:[^"@]*@|-----BEGIN|sk_live_|apps\.googleusercontent\.com|dev-only-)`)

func TestSpecHasNoCredentials(t *testing.T) {
	_, raw := readSpec(t)
	if found := credentialShapes.FindAllString(string(raw), -1); len(found) > 0 {
		t.Errorf("swagger.json contains credential-like text: %q", found)
	}
}

func TestSpecDescribesTheCurrentPhase(t *testing.T) {
	spec, _ := readSpec(t)
	description := spec.Info.Description
	for _, stale := range []string{"not implemented", "Milestone 8b"} {
		if strings.Contains(description, stale) {
			t.Errorf("info.description still says %q: %s", stale, description)
		}
	}
}
