package workspace

import (
	"fmt"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// ImportViolation is an import a generated file is not allowed to use.
type ImportViolation struct {
	File   string
	Import string
}

func (v ImportViolation) String() string {
	return fmt.Sprintf("%s imports %q, which the spec does not allow", v.File, v.Import)
}

// CheckGoImports parses each Go file's import list and reports imports that are
// neither standard library, nor under one of the allowed prefixes. The spec's
// allowlist is enforced here, mechanically, so a model cannot pull in a
// dependency by simply ignoring a prompt.
//
// A file that does not parse is reported as a violation with the parse error
// as the import, so the pipeline feeds it back to the Coder like any other
// build failure.
func CheckGoImports(files []File, allowed []string) []ImportViolation {
	var out []ImportViolation
	fset := token.NewFileSet()
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		ast, err := parser.ParseFile(fset, f.Path, f.Content, parser.ImportsOnly)
		if err != nil {
			out = append(out, ImportViolation{File: f.Path, Import: "<parse error: " + err.Error() + ">"})
			continue
		}
		for _, imp := range ast.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if isStdlib(path) || allowedImport(path, allowed) {
				continue
			}
			out = append(out, ImportViolation{File: f.Path, Import: path})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Import < out[j].Import
	})
	return out
}

// isStdlib uses the Go convention that module paths of third-party code start
// with a dotted host, while standard library paths never do.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

func allowedImport(path string, allowed []string) bool {
	for _, a := range allowed {
		a = strings.TrimSuffix(a, "/")
		if a == "" {
			continue
		}
		if path == a || strings.HasPrefix(path, a+"/") {
			return true
		}
	}
	return false
}

// FormatViolations renders violations as feedback for a repair round.
func FormatViolations(vs []ImportViolation, allowed []string) string {
	var b strings.Builder
	b.WriteString("[aspect] disallowed imports:\n")
	for _, v := range vs {
		fmt.Fprintf(&b, "  %s\n", v)
	}
	fmt.Fprintf(&b, "Allowed beyond the standard library: %s\n", strings.Join(allowed, ", "))
	return b.String()
}
