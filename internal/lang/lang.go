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
	"path"
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

	// Init writes the tier's workspace manifest (go.mod, Package.swift) once.
	Init func(root string, s *spec.Spec, t *spec.Tier) error
	// Sync updates the manifest for the modules generated so far. Go needs
	// nothing; SwiftPM must list every target it can see on disk.
	Sync func(root string, s *spec.Spec, t *spec.Tier, modules []string) error
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

// Dropped is a proposed file the profile refused, with the reason, so the
// agent can surface it instead of losing it silently.
type Dropped struct {
	Path   string
	Reason string
}

// KeepModuleFiles drops anything an agent proposed outside the module's code
// or test directory, and keeps only test files when wantTests is set (only
// implementation files otherwise). Agents are trusted to write code, not to
// reach across modules or roles. See SplitModuleFiles for the reasons.
func (p *Profile) KeepModuleFiles(module string, files []workspace.File, wantTests bool) []workspace.File {
	kept, _ := p.SplitModuleFiles(module, files, wantTests)
	return kept
}

// SplitModuleFiles is KeepModuleFiles that also reports what it refused and
// why. Paths are cleaned before the directory check, so "stock/../ledger/x.go"
// is refused rather than written into another module; a path repeated in one
// answer keeps its first occurrence; a file with no content is refused
// because the model elided it rather than wrote it.
func (p *Profile) SplitModuleFiles(module string, files []workspace.File, wantTests bool) ([]workspace.File, []Dropped) {
	dir := p.CodeDir(module)
	role := "implementation"
	if wantTests {
		dir = p.TestDir(module)
		role = "test"
	}
	var kept []workspace.File
	var dropped []Dropped
	seen := map[string]bool{}
	for _, f := range files {
		raw := strings.TrimSpace(f.Path)
		clean := path.Clean(strings.ReplaceAll(raw, "\\", "/"))
		switch {
		case raw == "" || clean == "." || path.IsAbs(clean) || hasDotDot(clean):
			dropped = append(dropped, Dropped{raw, "path is empty, absolute, or leaves the module directory"})
		case !strings.HasPrefix(clean, dir):
			dropped = append(dropped, Dropped{raw, fmt.Sprintf("outside the module's %s directory %s", role, dir)})
		case !strings.HasSuffix(clean, p.SourceExt):
			dropped = append(dropped, Dropped{raw, "not a " + p.SourceExt + " source file"})
		case p.IsTestFile(clean) != wantTests:
			if wantTests {
				dropped = append(dropped, Dropped{raw, "an implementation file; the Tester writes only tests"})
			} else {
				dropped = append(dropped, Dropped{raw, "a test file; the Coder writes only implementation"})
			}
		case seen[clean]:
			dropped = append(dropped, Dropped{raw, "repeated in the same answer; the first copy was kept"})
		case strings.TrimSpace(f.Content) == "":
			dropped = append(dropped, Dropped{raw, "has no content"})
		default:
			seen[clean] = true
			f.Path = clean
			kept = append(kept, f)
		}
	}
	return kept, dropped
}

// hasDotDot reports whether a cleaned slash path still has a ".." segment,
// which after Clean means it climbs above its first segment.
func hasDotDot(clean string) bool {
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
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
