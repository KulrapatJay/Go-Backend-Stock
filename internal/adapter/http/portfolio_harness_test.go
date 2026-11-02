package http

import (
	"encoding/json"
	nethttp "net/http"
	"testing"
)

func withPortfolios() harnessOption {
	return func(c *harnessConfig) { c.portfolios = true }
}

// createPortfolio asks for a token first, as a page would. An empty description is left out of the
// body, which the API treats as "".
func (b *browser) createPortfolio(name, description string) (response, []byte) {
	b.t.Helper()
	b.fetchCSRF()
	body := map[string]string{"name": name}
	if description != "" {
		body["description"] = description
	}
	return b.do(nethttp.MethodPost, "/api/v1/portfolios", body, nil)
}

func (b *browser) getPortfolio(id string) (response, []byte) {
	b.t.Helper()
	return b.do(nethttp.MethodGet, "/api/v1/portfolios/"+id, nil, nil)
}

// listPortfolios lists with a raw query such as "limit=5&cursor=abc"; "" lists the first page.
func (b *browser) listPortfolios(query string) (response, []byte) {
	b.t.Helper()
	path := "/api/v1/portfolios"
	if query != "" {
		path += "?" + query
	}
	return b.do(nethttp.MethodGet, path, nil, nil)
}

func decodePortfolio(t *testing.T, body []byte) PortfolioResponse {
	t.Helper()
	var p PortfolioResponse
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("portfolio body %s: %v", body, err)
	}
	return p
}

func decodePortfolioPage(t *testing.T, body []byte) PortfolioPageResponse {
	t.Helper()
	var page PortfolioPageResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("portfolio page body %s: %v", body, err)
	}
	return page
}

// mustCreatePortfolio creates a portfolio and fails the test unless the API answers 201.
func (b *browser) mustCreatePortfolio(name, description string) PortfolioResponse {
	b.t.Helper()
	resp, body := b.createPortfolio(name, description)
	if resp.StatusCode != nethttp.StatusCreated {
		b.t.Fatalf("create portfolio %q = %d %s", name, resp.StatusCode, body)
	}
	return decodePortfolio(b.t, body)
}
