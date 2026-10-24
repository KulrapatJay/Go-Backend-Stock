package core_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
	"golang.org/x/crypto/argon2":    true,
}

// modulePath is read from go.mod so the rule survives a module rename.
var (
	modulePath = mustModulePath(filepath.Join("..", "..", "go.mod"))
	corePrefix = modulePath + "/internal/core/"
)

func mustModulePath(goMod string) string {
	data, err := os.ReadFile(goMod)
	if err != nil {
		panic(fmt.Sprintf("read %s: %v", goMod, err))
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	panic("no module directive in " + goMod)
}

// TestAllowedImportPaths pins the classification itself. The module path has no dot, so a rule
// of "no dot in the first element means standard library" would wrongly admit our own adapters.
func TestAllowedImportPaths(t *testing.T) {
	cases := map[string]bool{
		"fmt":                                         true,
		"net/http":                                    true,
		"github.com/shopspring/decimal":               true,
		"github.com/google/uuid":                      true,
		"golang.org/x/crypto/argon2":                  true,
		"golang.org/x/crypto/bcrypt":                  false,
		"go-backend-stock/internal/core/domain":       true,
		"go-backend-stock/internal/core/port":         true,
		"go-backend-stock/internal/adapter/postgres":  false,
		"go-backend-stock/internal/adapter/http":      false,
		"go-backend-stock/internal/config":            false,
		"go-backend-stock/docs/api":                   false,
		"go-backend-stock":                            false,
		"github.com/gofiber/fiber/v3":                 false,
		"github.com/jackc/pgx/v5":                     false,
		"go-backend-stock/internal/core-extra/domain": false,
	}
	for path, want := range cases {
		if got := allowed(path); got != want {
			t.Errorf("allowed(%q) = %v, want %v", path, got, want)
		}
	}
}

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
	// Inside this module only the core is allowed. Checked before the standard-library rule
	// because the module path has no dot and would otherwise look like the standard library.
	if p == modulePath || strings.HasPrefix(p, modulePath+"/") {
		return false
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
