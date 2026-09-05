package fsx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPackageReExportsAnFsxPrimitive holds the one-implementation-one-name
// guarantee this package exists for: a primitive lives here, and every caller
// imports fsx to reach it. A local alias — a one-line forwarder or a package
// var bound to an fsx function — gives the same primitive a second name, and
// the second name is what drifts: it grows a doc comment that outlives the
// call sites it claims, and it hides the real dependency from anyone reading
// the importing package. configio.GlobalConfigDir and configio.AtomicWriteFile
// were exactly that, and were deleted for it (issue #912).
//
// The check is deliberately narrow. A function that calls an fsx primitive and
// does anything else with the result is a caller, not an alias, and passes.
func TestNoPackageReExportsAnFsxPrimitive(t *testing.T) {
	root := repoRootFrom(t)
	fsxDir := filepath.Join(root, "internal", "fsx")

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) || path == fsxDir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		reportReExports(t, root, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// skipDir names the trees that hold no first-party Go source. Walking them is
// harmless but slow, and vendor/ would report a third party's own helpers.
func skipDir(name string) bool {
	return name == ".git" || name == "vendor" || name == "graphify-out" ||
		strings.HasPrefix(name, ".") && name != "."
}

// reportReExports parses one file and fails the test for each alias it holds.
func reportReExports(t *testing.T, root, path string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if !importsFsx(file) {
		return
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if name, ok := forwardedPrimitive(d); ok {
				t.Errorf("%s: %s forwards straight to fsx.%s — delete it and let callers import fsx",
					rel, d.Name.Name, name)
			}
		case *ast.GenDecl:
			for _, name := range aliasedPrimitives(d) {
				t.Errorf("%s: a package-level identifier is bound to fsx.%s — delete it and let callers import fsx",
					rel, name)
			}
		}
	}
}

// importsFsx reports whether the file imports internal/fsx under its own
// name. A renamed import is out of scope: the check reads the `fsx` selector
// literally, and an alias is already a second name by another route.
func importsFsx(file *ast.File) bool {
	for _, imp := range file.Imports {
		if imp.Name != nil {
			continue
		}
		if strings.Trim(imp.Path.Value, `"`) == "github.com/filippolmt/toolbox/internal/fsx" {
			return true
		}
	}
	return false
}

// forwardedPrimitive returns the fsx primitive a function does nothing but
// re-expose: a body of exactly one statement, `return fsx.X(...)` or
// `fsx.X(...)`, whose arguments are the function's own parameters in order.
//
// The argument check is what separates an alias from a caller. A function that
// binds an argument of its own — a path it joins, a payload it renders, a mode
// it fixes — is naming a domain operation, not the primitive, and belongs
// where it is (reload.WriteMarker, bridge.writePIDFile). Only a signature the
// primitive could be substituted into unchanged is a second name for it.
func forwardedPrimitive(fn *ast.FuncDecl) (string, bool) {
	if fn.Body == nil || len(fn.Body.List) != 1 {
		return "", false
	}
	var call *ast.CallExpr
	switch stmt := fn.Body.List[0].(type) {
	case *ast.ReturnStmt:
		if len(stmt.Results) != 1 {
			return "", false
		}
		c, ok := stmt.Results[0].(*ast.CallExpr)
		if !ok {
			return "", false
		}
		call = c
	case *ast.ExprStmt:
		c, ok := stmt.X.(*ast.CallExpr)
		if !ok {
			return "", false
		}
		call = c
	default:
		return "", false
	}
	name, ok := fsxSelector(call.Fun)
	if !ok || !argsArePassedThrough(call.Args, paramNames(fn)) {
		return "", false
	}
	return name, true
}

// paramNames flattens a function's parameters into their declared order. A
// blank or unnamed parameter yields "", which no argument identifier matches,
// so such a function is never reported.
func paramNames(fn *ast.FuncDecl) []string {
	var names []string
	if fn.Type.Params == nil {
		return names
	}
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			names = append(names, "")
			continue
		}
		for _, n := range field.Names {
			names = append(names, n.Name)
		}
	}
	return names
}

// argsArePassedThrough reports whether args is exactly params, in order, as
// bare identifiers.
func argsArePassedThrough(args []ast.Expr, params []string) bool {
	if len(args) != len(params) {
		return false
	}
	for i, arg := range args {
		ident, ok := arg.(*ast.Ident)
		if !ok || params[i] == "" || ident.Name != params[i] {
			return false
		}
	}
	return true
}

// aliasedPrimitives returns the fsx primitives a var/const block binds to a
// package-level identifier without calling them (`var Home = fsx.Home`).
func aliasedPrimitives(decl *ast.GenDecl) []string {
	if decl.Tok != token.VAR && decl.Tok != token.CONST {
		return nil
	}
	var names []string
	for _, spec := range decl.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, v := range value.Values {
			if name, ok := fsxSelector(v); ok {
				names = append(names, name)
			}
		}
	}
	return names
}

// fsxSelector returns the primitive name in an `fsx.X` expression.
func fsxSelector(expr ast.Expr) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fsx" {
		return "", false
	}
	return sel.Sel.Name, true
}

// repoRootFrom walks up from the test's working directory until it finds
// go.mod.
func repoRootFrom(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}

// TestForwardedPrimitiveTellsAnAliasFromACaller pins the classifier in both
// directions, so the repo-wide guard above cannot go quietly green by
// misreading every function as a caller. The alias cases are the two
// facades issue #912 deleted; the caller cases are the domain functions that
// must keep passing.
func TestForwardedPrimitiveTellsAnAliasFromACaller(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "no-arg forward is an alias",
			src:  `func GlobalConfigDir() (string, error) { return fsx.Home() }`,
			want: "Home",
		},
		{
			name: "every parameter passed through is an alias",
			src:  `func AtomicWriteFile(d string, b []byte, m os.FileMode) error { return fsx.AtomicWriteFile(d, b, m) }`,
			want: "AtomicWriteFile",
		},
		{
			name: "a bound argument makes it a caller",
			src:  `func writePIDFile(path string) error { return fsx.AtomicWriteFile(path, []byte("1"), 0o644) }`,
		},
		{
			name: "a computed argument makes it a caller",
			src:  `func attemptFresh(dir string) bool { return fsx.MarkerFresh(filepath.Join(dir, "s"), ttl) }`,
		},
		{
			name: "reordered parameters are not a pass-through",
			src:  `func w(a string, b string) error { return fsx.AtomicWriteFile(b, a) }`,
		},
		{
			name: "a call to another package is not a forward",
			src:  `func h() (string, error) { return os.UserHomeDir() }`,
		},
		{
			name: "more than one statement is not a forward",
			src:  `func h() (string, error) { log(); return fsx.Home() }`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "x.go", "package p\n"+tc.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			fn, ok := file.Decls[0].(*ast.FuncDecl)
			if !ok {
				t.Fatalf("first decl is not a func")
			}
			got, isAlias := forwardedPrimitive(fn)
			if tc.want == "" {
				if isAlias {
					t.Fatalf("reported alias fsx.%s, want caller", got)
				}
				return
			}
			if !isAlias || got != tc.want {
				t.Fatalf("got (%q, %v), want (%q, true)", got, isAlias, tc.want)
			}
		})
	}
}
