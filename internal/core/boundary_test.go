package core_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// allowedThirdParty are the only non-standard-library imports the core may use (AGENTS.md:
// the core knows nothing of Fiber, pgx, sqlc, Alpaca or Supabase).
var allowedThirdParty = map[string]bool{
	"github.com/shopspring/decimal": true,
	"github.com/google/uuid":        true,
}

const corePrefix = "go-backend-stock/internal/core/"

// TestCoreImportsOnlyAllowedPackages parses every .go file under internal/core, tests included,
// and fails on any import outside the standard library, the allowlist and the core itself.
func TestCoreImportsOnlyAllowedPackages(t *testing.T) {
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		files++
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if !allowed(p) {
				t.Errorf("%s imports %q: the core may import only the standard library, %v and %s*", path, p, keys(allowedThirdParty), corePrefix)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/core: %v", err)
	}
	if files < 10 {
		t.Fatalf("checked only %d files; is the test running from internal/core?", files)
	}
}

func allowed(p string) bool {
	if allowedThirdParty[p] || strings.HasPrefix(p, corePrefix) {
		return true
	}
	// Standard library paths have no dot in their first element (e.g. "net/http", not "github.com/x").
	first, _, _ := strings.Cut(p, "/")
	return !strings.Contains(first, ".")
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
