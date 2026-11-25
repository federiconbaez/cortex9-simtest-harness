// Package archtest enforces the dependency rule and the "no goroutines in
// the simulation core" rule by parsing the source, independently of linters.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/federiconbaez/cortex9-simtest-harness"

var domainAllowed = map[string]bool{
	"container/heap": true, "errors": true, "fmt": true, "hash/fnv": true,
	"math": true, "sort": true, "strconv": true, "strings": true,
}

func parseDir(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	files := map[string]*ast.File{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[n] = f
	}
	if len(files) == 0 {
		t.Fatalf("no sources in %s", dir)
	}
	return files
}

func imports(f *ast.File) []string {
	var out []string
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		out = append(out, p)
	}
	return out
}

func TestDomainIsPureAndSingleThreaded(t *testing.T) {
	for name, f := range parseDir(t, "../domain") {
		for _, p := range imports(f) {
			if !domainAllowed[p] {
				t.Errorf("domain/%s imports %q (not in the pure allowlist)", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.GoStmt:
				t.Errorf("domain/%s starts a goroutine", name)
			case *ast.SelectStmt:
				t.Errorf("domain/%s uses select", name)
			case *ast.ChanType:
				t.Errorf("domain/%s declares a channel", name)
			}
			return true
		})
	}
}

func TestAppDependsOnPortsNotAdapters(t *testing.T) {
	for name, f := range parseDir(t, "../app") {
		for _, p := range imports(f) {
			if strings.HasPrefix(p, module+"/internal/adapters") || p == "os" || p == "net" || p == "time" {
				t.Errorf("app/%s imports %q", name, p)
			}
		}
	}
}

func TestAdaptersDoNotImportApp(t *testing.T) {
	for _, dir := range []string{"../adapters/kv", "../adapters/memstore"} {
		for name, f := range parseDir(t, dir) {
			for _, p := range imports(f) {
				if strings.HasPrefix(p, module+"/internal/app") {
					t.Errorf("%s/%s imports %q", dir, name, p)
				}
			}
		}
	}
}
