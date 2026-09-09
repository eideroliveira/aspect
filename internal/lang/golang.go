package lang

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

func init() {
	register(&Profile{
		Name:        "go",
		DisplayName: "Go",
		SourceExt:   ".go",
		CodeDir:     func(m string) string { return m + "/" },
		TestDir:     func(m string) string { return m + "/" },
		IsTestFile:  func(p string) bool { return strings.HasSuffix(p, "_test.go") },
		Init:        goInit,
		Sync:        func(string, *spec.Spec, []string) error { return nil },
		Steps: func(module string) [][]string {
			pattern := "./" + module + "/..."
			return [][]string{
				{"go", "mod", "tidy"},
				{"go", "vet", pattern},
				{"go", "test", "-count=1", "-race", pattern},
			}
		},
		CheckImports: workspace.CheckGoImports,
		CoderRules: `Go rules:
- A module named "stock" lives in stock/ as package stock. Only write files inside that directory, and never files ending in _test.go.
- Import dependency modules by their import path (<module_path>/<module>). Do not re-implement them.
- Import only the standard library, this system's own module, and the modules listed under the stack section. The pipeline parses your imports and rejects anything else.
- Exported identifiers get doc comments. Errors are values; wrap with %w.`,
		TesterRules: `Go rules:
- Tests live in the module's directory as package <module> (internal tests) unless the spec requires the external package, in files ending in _test.go. Never write implementation files.
- Standard library testing only, no assertion frameworks. Table-driven tests and t.Run subtests.
- Concurrency constraints get a test that runs operations from many goroutines; the pipeline runs go test -race.
- Property-style tests use math/rand with a fixed seed so failures reproduce.`,
	})
}

func goInit(root string, s *spec.Spec) error {
	gomod := filepath.Join(root, "go.mod")
	if _, err := os.Stat(gomod); err == nil {
		return nil
	}
	version := runtime.Version()
	if ls := s.LanguageStack(); ls != nil && ls.Version != "" {
		version = ls.Version
	}
	version = strings.TrimPrefix(version, "go")
	if i := strings.Index(version, " "); i > 0 {
		version = version[:i]
	}
	content := fmt.Sprintf("module %s\n\ngo %s\n", s.System.ModulePath, version)
	return os.WriteFile(gomod, []byte(content), 0o644)
}
