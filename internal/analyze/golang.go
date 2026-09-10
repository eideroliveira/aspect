// Package analyze builds a deterministic inventory of an existing codebase:
// packages, exported API, persistent types, routes, tests and dependencies.
// It uses the parser only (no type checking), so it works on any tree that
// parses, including ones whose dependencies are not downloaded.
//
// The inventory is the evidence the Specifier agents reason from. Keeping it
// deterministic means an import can be re-run and diffed.
package analyze

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Inventory is the analysed codebase.
type Inventory struct {
	Language   string    `json:"language"`
	Root       string    `json:"root"`
	ModulePath string    `json:"module_path"`
	Packages   []Package `json:"packages"`
	// Frameworks detected anywhere in the tree, by canonical name.
	Frameworks []string `json:"frameworks"`
}

// Package is one Go package.
type Package struct {
	ImportPath string `json:"import_path"`
	// Dir is relative to the root, "." for the root package.
	Dir   string `json:"dir"`
	Name  string `json:"name"`
	Doc   string `json:"doc,omitempty"`
	Files int    `json:"files"`
	Lines int    `json:"lines"`
	// Imports are the in-module packages this one imports (dependency edges).
	Imports []string `json:"imports"`
	// External are third-party import paths, deduplicated.
	External   []string   `json:"external"`
	Frameworks []string   `json:"frameworks"`
	Types      []TypeDecl `json:"types"`
	Funcs      []FuncDecl `json:"funcs"`
	Routes     []Route    `json:"routes"`
	Tests      []string   `json:"tests"`
	// Docs are Markdown documents kept in the package directory (README.md,
	// CLAUDE.md, design notes): the owner's own description of the package.
	Docs []Doc `json:"docs,omitempty"`
}

// Doc is one Markdown file found in a package directory.
type Doc struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// maxDocBytes caps one document; larger ones are truncated with a marker.
const maxDocBytes = 200 * 1024

// TypeDecl is an exported type.
type TypeDecl struct {
	Name   string      `json:"name"`
	Kind   string      `json:"kind"` // struct, interface, alias, other
	Doc    string      `json:"doc,omitempty"`
	Fields []FieldDecl `json:"fields,omitempty"`
	// Persistent is a heuristic: the struct carries gorm/db/bun tags or embeds
	// gorm.Model, so it is probably a database entity.
	Persistent bool `json:"persistent,omitempty"`
}

// FieldDecl is one exported struct field.
type FieldDecl struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
}

// FuncDecl is an exported function or method.
type FuncDecl struct {
	Name      string `json:"name"`
	Receiver  string `json:"receiver,omitempty"`
	Signature string `json:"signature"`
	Doc       string `json:"doc,omitempty"`
}

// Route is a route registration found by pattern matching call sites such as
// r.Get("/path", h) or mux.HandleFunc("/path", h).
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Func   string `json:"func,omitempty"`
	File   string `json:"file"`
}

// frameworkSignatures maps import-path prefixes to canonical framework names.
var frameworkSignatures = []struct{ prefix, name string }{
	{"github.com/qor5/admin", "qor5"},
	{"github.com/qor5/web", "qor5-web"},
	{"github.com/qor5/x", "qor5-x"},
	{"github.com/theplant/htmlgo", "htmlgo"},
	{"gorm.io/gorm", "gorm"},
	{"github.com/go-chi/chi", "chi"},
	{"github.com/gin-gonic/gin", "gin"},
	{"github.com/labstack/echo", "echo"},
	{"github.com/gofiber/fiber", "fiber"},
	{"github.com/gorilla/mux", "gorilla-mux"},
	{"google.golang.org/grpc", "grpc"},
	{"github.com/spf13/cobra", "cobra"},
	{"github.com/jackc/pgx", "pgx"},
	{"github.com/lib/pq", "pq"},
	{"github.com/mattn/go-sqlite3", "sqlite3"},
	{"github.com/stripe/stripe-go", "stripe"},
	{"golang.org/x/oauth2", "oauth2"},
	{"github.com/redis/go-redis", "redis"},
	{"cloud.google.com/go", "google-cloud"},
}

var routeMethods = map[string]string{
	"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH", "Delete": "DELETE", "Head": "HEAD", "Options": "OPTIONS",
	"GET": "GET", "POST": "POST", "PUT": "PUT", "PATCH": "PATCH", "DELETE": "DELETE",
	"Handle": "ANY", "HandleFunc": "ANY", "Mount": "MOUNT", "Route": "GROUP", "Group": "GROUP", "Any": "ANY",
}

var skipDirs = map[string]bool{"vendor": true, "node_modules": true, "testdata": true, ".git": true, ".claude": true, ".build": true}

// Options filter the walk.
type Options struct {
	// Exclude are directory names or relative paths (prefix match) to skip,
	// beyond the built-in vendor/testdata/.git set.
	Exclude []string
	// Include, when non-empty, keeps only packages whose relative dir matches
	// one of these prefixes.
	Include []string
}

// Go analyses a Go module rooted at root.
func Go(root string, opts Options) (*Inventory, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	modulePath, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	inv := &Inventory{Language: "go", Root: root, ModulePath: modulePath}
	fset := token.NewFileSet()
	frameworks := map[string]bool{}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if path != root {
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), "_") || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
				return filepath.SkipDir
			}
			for _, ex := range opts.Exclude {
				if d.Name() == ex || rel == ex || strings.HasPrefix(rel, strings.TrimSuffix(ex, "/")+"/") {
					return filepath.SkipDir
				}
			}
			// A nested module is a different project.
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
		}
		if len(opts.Include) > 0 && !included(rel, opts.Include) {
			return nil
		}
		pkg, err := analysePackage(fset, root, rel, modulePath)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if pkg == nil {
			return nil
		}
		for _, f := range pkg.Frameworks {
			frameworks[f] = true
		}
		inv.Packages = append(inv.Packages, *pkg)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(inv.Packages, func(i, j int) bool { return inv.Packages[i].Dir < inv.Packages[j].Dir })
	inv.Frameworks = sortedKeys(frameworks)
	return inv, nil
}

func included(rel string, include []string) bool {
	for _, in := range include {
		in = strings.TrimSuffix(strings.TrimPrefix(in, "./"), "/")
		if rel == in || strings.HasPrefix(rel, in+"/") || in == "." {
			return true
		}
	}
	return false
}

func analysePackage(fset *token.FileSet, root, rel, modulePath string) (*Package, error) {
	dir := filepath.Join(root, rel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	pkg := &Package{Dir: rel, ImportPath: modulePath}
	if rel != "." {
		pkg.ImportPath = modulePath + "/" + rel
	}
	internal := map[string]bool{}
	external := map[string]bool{}
	frameworks := map[string]bool{}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(name), ".md") {
			if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil && len(strings.TrimSpace(string(b))) > 0 {
				content := string(b)
				if len(content) > maxDocBytes {
					content = content[:maxDocBytes] + "\n\n…(document truncated)\n"
				}
				pkg.Docs = append(pkg.Docs, Doc{Name: name, Content: content})
			}
			continue
		}
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		path := filepath.Join(dir, name)
		isTest := strings.HasSuffix(name, "_test.go")
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			// A file that does not parse is skipped, not fatal: generated or
			// build-tagged files are common in real trees.
			continue
		}
		if isTest {
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
					pkg.Tests = append(pkg.Tests, fn.Name.Name)
				}
			}
			continue
		}
		if pkg.Name == "" {
			pkg.Name = file.Name.Name
		} else if file.Name.Name != pkg.Name {
			continue // a second package in the same dir (rare); ignore
		}
		pkg.Files++
		pkg.Lines += countLines(path)
		if file.Doc != nil && pkg.Doc == "" {
			pkg.Doc = strings.TrimSpace(file.Doc.Text())
		}
		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			switch {
			case p == modulePath || strings.HasPrefix(p, modulePath+"/"):
				if p != pkg.ImportPath {
					internal[p] = true
				}
			case strings.Contains(strings.SplitN(p, "/", 2)[0], "."):
				external[p] = true
				for _, sig := range frameworkSignatures {
					if strings.HasPrefix(p, sig.prefix) {
						frameworks[sig.name] = true
					}
				}
			}
		}
		collectDecls(fset, file, pkg)
		pkg.Routes = append(pkg.Routes, findRoutes(fset, file, name)...)
	}
	if pkg.Files == 0 {
		return nil, nil
	}
	sort.Slice(pkg.Docs, func(i, j int) bool { return pkg.Docs[i].Name < pkg.Docs[j].Name })
	pkg.Imports = sortedKeys(internal)
	pkg.External = sortedKeys(external)
	pkg.Frameworks = sortedKeys(frameworks)
	sort.Strings(pkg.Tests)
	sort.Slice(pkg.Types, func(i, j int) bool { return pkg.Types[i].Name < pkg.Types[j].Name })
	sort.Slice(pkg.Funcs, func(i, j int) bool {
		if pkg.Funcs[i].Receiver != pkg.Funcs[j].Receiver {
			return pkg.Funcs[i].Receiver < pkg.Funcs[j].Receiver
		}
		return pkg.Funcs[i].Name < pkg.Funcs[j].Name
	})
	return pkg, nil
}

func collectDecls(fset *token.FileSet, file *ast.File, pkg *Package) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			fd := FuncDecl{Name: d.Name.Name, Signature: funcSignature(fset, d)}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				fd.Receiver = exprString(fset, d.Recv.List[0].Type)
				// Skip methods on unexported receivers: not part of the API.
				if !ast.IsExported(strings.TrimPrefix(fd.Receiver, "*")) {
					continue
				}
			}
			if d.Doc != nil {
				fd.Doc = firstSentence(d.Doc.Text())
			}
			pkg.Funcs = append(pkg.Funcs, fd)
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, s := range d.Specs {
				ts, ok := s.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				td := TypeDecl{Name: ts.Name.Name, Kind: "other"}
				if ts.Doc != nil {
					td.Doc = firstSentence(ts.Doc.Text())
				} else if d.Doc != nil && len(d.Specs) == 1 {
					td.Doc = firstSentence(d.Doc.Text())
				}
				switch t := ts.Type.(type) {
				case *ast.StructType:
					td.Kind = "struct"
					for _, f := range t.Fields.List {
						typ := exprString(fset, f.Type)
						tag := ""
						if f.Tag != nil {
							tag, _ = strconv.Unquote(f.Tag.Value)
						}
						if strings.Contains(tag, "gorm:") || strings.Contains(tag, `db:"`) || strings.Contains(tag, "bun:") || typ == "gorm.Model" {
							td.Persistent = true
						}
						if len(f.Names) == 0 {
							td.Fields = append(td.Fields, FieldDecl{Name: "(embedded)", Type: typ, Tag: tag})
							continue
						}
						for _, n := range f.Names {
							if n.IsExported() {
								td.Fields = append(td.Fields, FieldDecl{Name: n.Name, Type: typ, Tag: tag})
							}
						}
					}
				case *ast.InterfaceType:
					td.Kind = "interface"
					for _, m := range t.Methods.List {
						if len(m.Names) > 0 && m.Names[0].IsExported() {
							td.Fields = append(td.Fields, FieldDecl{Name: m.Names[0].Name, Type: exprString(fset, m.Type)})
						}
					}
				default:
					if ts.Assign.IsValid() {
						td.Kind = "alias"
					}
					td.Fields = []FieldDecl{{Name: "(underlying)", Type: exprString(fset, ts.Type)}}
				}
				pkg.Types = append(pkg.Types, td)
			}
		}
	}
}

// findRoutes looks for calls whose selector is a known router method and
// whose first argument is a string literal starting with "/".
func findRoutes(fset *token.FileSet, file *ast.File, filename string) []Route {
	var routes []Route
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method, ok := routeMethods[sel.Sel.Name]
		if !ok {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		path, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.HasPrefix(path, "/") {
			return true
		}
		r := Route{Method: method, Path: path, File: filename}
		if len(call.Args) > 1 {
			r.Func = exprString(fset, call.Args[1])
			if len(r.Func) > 60 {
				r.Func = r.Func[:57] + "..."
			}
		}
		routes = append(routes, r)
		return true
	})
	return routes
}

func funcSignature(fset *token.FileSet, d *ast.FuncDecl) string {
	// Print the declaration without its body.
	copy := *d
	copy.Body = nil
	copy.Doc = nil
	var b strings.Builder
	if err := printer.Fprint(&b, fset, &copy); err != nil {
		return d.Name.Name
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		return "?"
	}
	return b.String()
}

var sentenceEnd = regexp.MustCompile(`(?s)^(.*?[.!?])(\s|$)`)

func firstSentence(doc string) string {
	doc = strings.TrimSpace(doc)
	if m := sentenceEnd.FindStringSubmatch(doc); m != nil {
		return m[1]
	}
	if len(doc) > 200 {
		return doc[:200] + "…"
	}
	return doc
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		n++
	}
	return n
}

func readModulePath(gomod string) (string, error) {
	b, err := os.ReadFile(gomod)
	if err != nil {
		return "", fmt.Errorf("analyze: %w (is this the root of a Go module?)", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("analyze: no module line in %s", gomod)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Package returns the package at rel, or nil.
func (inv *Inventory) Package(rel string) *Package {
	for i := range inv.Packages {
		if inv.Packages[i].Dir == rel {
			return &inv.Packages[i]
		}
	}
	return nil
}
