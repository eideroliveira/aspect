// Package lang holds everything that differs between target languages: where
// a module's files live, how tests are recognised, which commands build and
// test the workspace, and the language-specific rules the agents are given.
//
// The spec is language-agnostic; a profile is what turns it into a concrete
// project layout. Adding a language means adding a profile here, not
// touching the agents or the pipeline.
package lang

import (
	"fmt"
	"sort"
	"strings"

	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

// Profile describes one target language.
type Profile struct {
	// Name matches spec.System.Language.
	Name string
	// DisplayName is used in prompts ("Go", "Swift").
	DisplayName string
	// SourceExt is the source file extension including the dot.
	SourceExt string

	// CodeDir is the directory (with trailing slash) holding a module's
	// implementation files.
	CodeDir func(module string) string
	// TestDir is the directory (with trailing slash) holding a module's tests.
	TestDir func(module string) string
	// IsTestFile reports whether a path is a test file.
	IsTestFile func(path string) bool

	// Init writes the workspace manifest (go.mod, Package.swift) once.
	Init func(root string, s *spec.Spec) error
	// Sync updates the manifest for the modules generated so far. Go needs
	// nothing; SwiftPM must list every target it can see on disk.
	Sync func(root string, s *spec.Spec, modules []string) error
	// Steps are the commands, run in root, that build and test one module.
	Steps func(module string) [][]string
	// CheckImports enforces the spec's import allowlist. May be nil.
	CheckImports func(files []workspace.File, allowed []string) []workspace.ImportViolation

	// CoderRules and TesterRules are appended to the agents' system prompts.
	CoderRules  string
	TesterRules string
}

var profiles = map[string]*Profile{}

func register(p *Profile) { profiles[p.Name] = p }

// For returns the profile for a language name.
func For(name string) (*Profile, error) {
	p, ok := profiles[name]
	if !ok {
		return nil, fmt.Errorf("lang: no profile for %q (have %s)", name, strings.Join(Names(), ", "))
	}
	return p, nil
}

// Names lists registered languages.
func Names() []string {
	out := make([]string, 0, len(profiles))
	for n := range profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// KeepModuleFiles drops anything an agent proposed outside the module's code
// or test directory, and keeps only test files when wantTests is set (only
// implementation files otherwise). Agents are trusted to write code, not to
// reach across modules or roles.
func (p *Profile) KeepModuleFiles(module string, files []workspace.File, wantTests bool) []workspace.File {
	dir := p.CodeDir(module)
	if wantTests {
		dir = p.TestDir(module)
	}
	var kept []workspace.File
	for _, f := range files {
		path := strings.TrimPrefix(f.Path, "./")
		if !strings.HasPrefix(path, dir) || !strings.HasSuffix(path, p.SourceExt) || p.IsTestFile(path) != wantTests {
			continue
		}
		f.Path = path
		kept = append(kept, f)
	}
	return kept
}

// PascalCase turns a spec identifier (snake_case) into a type-like name.
func PascalCase(s string) string {
	var b strings.Builder
	for _, part := range strings.Split(s, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}
