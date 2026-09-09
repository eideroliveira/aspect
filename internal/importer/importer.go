// Package importer recovers an Aspect spec from an existing codebase. The
// inventory is deterministic; the Describer reads one package at a time; the
// Synthesizer writes the system level; assembly is deterministic again so
// the same fragments always produce the same spec.
//
// In retarget mode (target language differs from the source) the
// Synthesizer designs the module list for the target platform and the
// assembler only sanitises and validates it.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eideroliveira/aspect/internal/agents"
	"github.com/eideroliveira/aspect/internal/analyze"
	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/spec"
)

// Options configure an import.
type Options struct {
	Root    string
	Include []string
	Exclude []string
	// Target language; empty means the source language.
	Target     string
	TargetHint string
	// Name overrides the system name (default: last element of the module path).
	Name string
	// ModulePath overrides the target module path.
	ModulePath string
	// Repository and Commit are recorded as provenance.
	Repository string
	Commit     string
	// CacheDir stores fragments so a re-run skips packages already described.
	CacheDir    string
	Concurrency int
	Log         io.Writer
}

// Result of an import.
type Result struct {
	Inventory *analyze.Inventory
	Fragments []agents.Fragment
	Synthesis agents.Synthesis
	Spec      *spec.Spec
	Issues    spec.Issues
	Warnings  []string
	Usage     llm.Usage
}

// Run performs a full import.
func Run(ctx context.Context, c llm.Client, opts Options) (*Result, error) {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	inv, err := analyze.Go(opts.Root, analyze.Options{Include: opts.Include, Exclude: opts.Exclude})
	if err != nil {
		return nil, err
	}
	if len(inv.Packages) == 0 {
		return nil, fmt.Errorf("importer: no packages found under %s", opts.Root)
	}
	res := &Result{Inventory: inv}
	fmt.Fprintf(opts.Log, "== describing %d packages (concurrency %d)\n", len(inv.Packages), opts.Concurrency)

	frags, usage, err := describeAll(ctx, c, inv, opts)
	res.Usage = usage
	if err != nil {
		return res, err
	}
	res.Fragments = frags

	fmt.Fprintf(opts.Log, "== synthesizing system level (target %s)\n", targetLanguage(inv, opts))
	synth, resp, err := synthesize(ctx, c, inv, frags, opts)
	res.Usage.Add(resp)
	if err != nil {
		return res, err
	}
	res.Synthesis = synth

	res.Spec, res.Warnings = Assemble(inv, frags, synth, opts)
	res.Issues = spec.Validate(res.Spec)
	return res, nil
}

func targetLanguage(inv *analyze.Inventory, opts Options) string {
	if opts.Target != "" {
		return opts.Target
	}
	return inv.Language
}

func describeAll(ctx context.Context, c llm.Client, inv *analyze.Inventory, opts Options) ([]agents.Fragment, llm.Usage, error) {
	d := &agents.Describer{LLM: c}
	frags := make([]agents.Fragment, len(inv.Packages))
	errs := make([]error, len(inv.Packages))
	var mu sync.Mutex
	var usage llm.Usage
	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	for i := range inv.Packages {
		pkg := &inv.Packages[i]
		if f, ok := loadCached(opts.CacheDir, pkg.Dir); ok {
			fmt.Fprintf(opts.Log, "   %-40s cached\n", pkg.Dir)
			frags[i] = f
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				errs[i] = ctx.Err()
				return
			}
			f, resp, err := d.Describe(ctx, inv, pkg)
			mu.Lock()
			usage.Add(resp)
			mu.Unlock()
			if err != nil {
				errs[i] = err
				fmt.Fprintf(opts.Log, "   %-40s error: %v\n", pkg.Dir, err)
				return
			}
			frags[i] = f
			saveCached(opts.CacheDir, pkg.Dir, f)
			fmt.Fprintf(opts.Log, "   %-40s %d entities, %d interfaces, %d ops, %d scenarios\n", pkg.Dir, len(f.Entities), len(f.Interfaces), len(f.Operations), len(f.Scenarios))
		}(i)
	}
	wg.Wait()
	return frags, usage, errors.Join(errs...)
}

func synthesize(ctx context.Context, c llm.Client, inv *analyze.Inventory, frags []agents.Fragment, opts Options) (agents.Synthesis, llm.Response, error) {
	key := "_synthesis." + targetLanguage(inv, opts)
	if cached, ok := loadCachedSynthesis(opts.CacheDir, key); ok {
		fmt.Fprintf(opts.Log, "   synthesis cached\n")
		return cached, llm.Response{}, nil
	}
	s := &agents.Synthesizer{LLM: c}
	out, resp, err := s.Synthesize(ctx, agents.SynthesisInput{Inventory: inv, Fragments: frags, Target: opts.Target, TargetHint: opts.TargetHint})
	if err == nil {
		saveCachedSynthesis(opts.CacheDir, key, out)
	}
	return out, resp, err
}

func cachePath(dir, key string) string {
	return filepath.Join(dir, strings.ReplaceAll(strings.ReplaceAll(key, "/", "__"), ".", "_")+".json")
}

func loadCached(dir, pkgDir string) (agents.Fragment, bool) {
	var f agents.Fragment
	if dir == "" {
		return f, false
	}
	b, err := os.ReadFile(cachePath(dir, "pkg."+pkgDir))
	if err != nil || json.Unmarshal(b, &f) != nil {
		return f, false
	}
	return f, true
}

func saveCached(dir, pkgDir string, f agents.Fragment) {
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	if b, err := json.MarshalIndent(f, "", "  "); err == nil {
		_ = os.WriteFile(cachePath(dir, "pkg."+pkgDir), b, 0o644)
	}
}

func loadCachedSynthesis(dir, key string) (agents.Synthesis, bool) {
	var s agents.Synthesis
	if dir == "" {
		return s, false
	}
	b, err := os.ReadFile(cachePath(dir, key))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return s, false
	}
	return s, true
}

func saveCachedSynthesis(dir, key string, s agents.Synthesis) {
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	if b, err := json.MarshalIndent(s, "", "  "); err == nil {
		_ = os.WriteFile(cachePath(dir, key), b, 0o644)
	}
}

var nonIdent = regexp.MustCompile(`[^a-z0-9]+`)

// Ident turns any string into a spec identifier: lowercase, snake_case,
// starting with a letter.
func Ident(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(nonIdent.ReplaceAllString(s, "_"), "_")
	if s == "" {
		return "x"
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "p_" + s
	}
	return s
}

// ModuleName maps a package directory to a module name.
func ModuleName(dir string) string {
	if dir == "." || dir == "" {
		return "root"
	}
	return Ident(dir)
}

// Assemble builds the spec from the inventory, the fragments and the
// synthesis. It never fails: everything it cannot reconcile becomes a
// warning and, where the spec would otherwise be invalid, a conservative
// default, so the user always gets a file to edit.
func Assemble(inv *analyze.Inventory, frags []agents.Fragment, synth agents.Synthesis, opts Options) (*spec.Spec, []string) {
	a := &assembler{inv: inv, opts: opts}
	target := targetLanguage(inv, opts)
	s := &spec.Spec{Aspect: spec.Version}
	s.System.Language = target
	s.System.Name = opts.Name
	if s.System.Name == "" {
		s.System.Name = Ident(synth.Name)
		if synth.Name == "" {
			s.System.Name = Ident(filepath.Base(inv.ModulePath))
		}
	}
	s.System.Intent = strings.TrimSpace(synth.Intent)
	s.System.Constraints = synth.Constraints
	s.System.ModulePath = opts.ModulePath
	if s.System.ModulePath == "" {
		if target == inv.Language {
			s.System.ModulePath = inv.ModulePath
		} else {
			s.System.ModulePath = "com.example." + s.System.Name
		}
	}
	s.System.Source = &spec.Source{
		Language: inv.Language, Repository: opts.Repository, Commit: opts.Commit,
		ImportedAt: time.Now().UTC().Format(time.RFC3339), Frameworks: inv.Frameworks,
	}
	if !emptyStack(synth.Stack) {
		s.System.Stack = spec.Stack{target: synth.Stack}
	}

	if target != inv.Language {
		a.retarget(s, synth)
	} else {
		a.mirror(s, frags, synth)
	}
	a.goals(s, synth, frags)
	a.tidy(s)
	return s, a.warnings
}

type assembler struct {
	inv      *analyze.Inventory
	opts     Options
	warnings []string
}

func (a *assembler) warn(format string, args ...any) {
	a.warnings = append(a.warnings, fmt.Sprintf(format, args...))
}

func emptyStack(ls spec.LanguageStack) bool {
	return ls.Version == "" && len(ls.Frameworks) == 0 && ls.ORM == nil && len(ls.AllowedModules) == 0 && ls.Guidance == ""
}

// mirror builds one module per package.
func (a *assembler) mirror(s *spec.Spec, frags []agents.Fragment, synth agents.Synthesis) {
	byDir := map[string]agents.Fragment{}
	for _, f := range frags {
		byDir[f.Package] = f
	}
	names := map[string]string{} // import path -> module name
	for _, p := range a.inv.Packages {
		names[p.ImportPath] = ModuleName(p.Dir)
	}

	entityOwner := map[string]string{}
	ifaces := map[string]*spec.Interface{}
	var ifaceOrder []string
	// Interface metadata from the synthesis wins over fragments.
	for _, si := range synth.Interfaces {
		name := Ident(si.Name)
		cp := si
		cp.Name = name
		cp.Surfaces = nil
		ifaces[name] = &cp
		ifaceOrder = append(ifaceOrder, name)
	}

	for _, p := range a.inv.Packages {
		f := byDir[p.Dir]
		m := spec.Module{
			Name:        ModuleName(p.Dir),
			Intent:      strings.TrimSpace(f.Intent),
			Interface:   f.Operations,
			Invariants:  f.Invariants,
			Scenarios:   f.Scenarios,
			Constraints: f.Constraints,
		}
		if m.Intent == "" {
			m.Intent = fmt.Sprintf("Package %s (no description recovered).", p.ImportPath)
			a.warn("module %s: no intent recovered", m.Name)
		}
		for _, imp := range p.Imports {
			if n, ok := names[imp]; ok {
				m.DependsOn = append(m.DependsOn, n)
			}
		}
		sort.Strings(m.DependsOn)
		for _, e := range f.Entities {
			if owner, dup := entityOwner[e.Name]; dup {
				a.warn("entity %s defined by %s and %s; keeping %s", e.Name, owner, m.Name, owner)
				continue
			}
			entityOwner[e.Name] = m.Name
			if s.System.Database == nil {
				s.System.Database = &spec.Database{}
			}
			s.System.Database.Entities = append(s.System.Database.Entities, e)
			m.Entities = append(m.Entities, e.Name)
		}
		for _, fi := range f.Interfaces {
			name := Ident(fi.Name)
			iface, ok := ifaces[name]
			if !ok {
				cp := fi
				cp.Name = name
				cp.Surfaces = nil
				ifaces[name] = &cp
				iface = &cp
				ifaceOrder = append(ifaceOrder, name)
			}
			if iface.Kind == "" {
				iface.Kind = fi.Kind
			}
			if iface.Intent == "" {
				iface.Intent = fi.Intent
			}
			for _, sf := range fi.Surfaces {
				sf.Name = Ident(sf.Name)
				if surfaceExists(iface, sf.Name) {
					sf.Name = Ident(m.Name + "_" + sf.Name)
				}
				iface.Surfaces = append(iface.Surfaces, sf)
				m.Surfaces = append(m.Surfaces, iface.Name+"."+sf.Name)
			}
		}
		s.Modules = append(s.Modules, m)
	}
	for _, name := range ifaceOrder {
		if len(ifaces[name].Surfaces) > 0 {
			s.System.Interfaces = append(s.System.Interfaces, *ifaces[name])
		}
	}
	if s.System.Database != nil {
		s.System.Database.Engine = detectEngine(a.inv.Frameworks)
		s.System.Database.Migrations = "auto"
		s.System.Database.Test = spec.DBTest{Engine: "sqlite"}
	}
}

// retarget takes the module design from the synthesis.
func (a *assembler) retarget(s *spec.Spec, synth agents.Synthesis) {
	s.System.Database = synth.Database
	if s.System.Database != nil && len(s.System.Database.Entities) == 0 {
		s.System.Database = nil
	}
	for _, iface := range synth.Interfaces {
		iface.Name = Ident(iface.Name)
		for i := range iface.Surfaces {
			iface.Surfaces[i].Name = Ident(iface.Surfaces[i].Name)
		}
		s.System.Interfaces = append(s.System.Interfaces, iface)
	}
	rename := map[string]string{}
	for _, m := range synth.Modules {
		rename[m.Name] = Ident(m.Name)
	}
	for _, m := range synth.Modules {
		m.Name = rename[m.Name]
		for i, d := range m.DependsOn {
			m.DependsOn[i] = Ident(d)
		}
		for i, sf := range m.Surfaces {
			iface, surf, ok := strings.Cut(sf, ".")
			if ok {
				m.Surfaces[i] = Ident(iface) + "." + Ident(surf)
			} else {
				m.Surfaces[i] = Ident(iface)
			}
		}
		s.Modules = append(s.Modules, m)
	}
	if len(s.Modules) == 0 {
		a.warn("the synthesis proposed no modules for the target; add them by hand")
	}
}

func surfaceExists(iface *spec.Interface, name string) bool {
	for _, sf := range iface.Surfaces {
		if sf.Name == name {
			return true
		}
	}
	return false
}

func detectEngine(frameworks []string) string {
	for _, f := range frameworks {
		switch f {
		case "pgx", "pq":
			return "postgres"
		case "sqlite3":
			return "sqlite"
		}
	}
	return "postgres"
}

// goals maps synthesis goals to modules, dropping references to modules that
// do not exist and guaranteeing at least one owned goal.
func (a *assembler) goals(s *spec.Spec, synth agents.Synthesis, frags []agents.Fragment) {
	mods := map[string]*spec.Module{}
	for i := range s.Modules {
		mods[s.Modules[i].Name] = &s.Modules[i]
	}
	seen := map[string]bool{}
	for i, g := range synth.Goals {
		id := strings.TrimSpace(g.ID)
		if id == "" || seen[id] {
			id = fmt.Sprintf("G%d", i+1)
		}
		seen[id] = true
		var owners []string
		for _, ref := range g.Modules {
			name := ModuleName(ref)
			if m, ok := mods[name]; ok {
				m.Goals = append(m.Goals, id)
				owners = append(owners, name)
				continue
			}
			if m, ok := mods[Ident(ref)]; ok {
				m.Goals = append(m.Goals, id)
				owners = append(owners, m.Name)
			}
		}
		if len(owners) == 0 {
			a.warn("goal %s (%q) names no known module; dropped", id, g.Statement)
			continue
		}
		verify := spec.VerifyMethod(g.Verify)
		if verify == "" {
			verify = spec.VerifyTest
		}
		s.System.Goals = append(s.System.Goals, spec.Goal{ID: id, Statement: strings.TrimSpace(g.Statement), Verify: verify})
	}
	if len(s.System.Goals) == 0 {
		a.warn("no goals recovered; a placeholder goal owned by every module was added")
		s.System.Goals = []spec.Goal{{ID: "G1", Statement: "The system preserves the behaviour of the source it was imported from.", Verify: spec.VerifyReview}}
		for i := range s.Modules {
			s.Modules[i].Goals = []string{"G1"}
		}
	}
}

// tidy fixes what would otherwise fail validation for mechanical reasons:
// duplicate scenario ids, empty field types, references to unknown entities
// or frameworks.
func (a *assembler) tidy(s *spec.Spec) {
	entities := map[string]bool{}
	if s.System.Database != nil {
		for i := range s.System.Database.Entities {
			e := &s.System.Database.Entities[i]
			entities[e.Name] = true
			for j := range e.Fields {
				if e.Fields[j].Type == "" {
					e.Fields[j].Type = "string"
					a.warn("entity %s field %s had no type; defaulted to string", e.Name, e.Fields[j].Name)
				}
			}
		}
		for i := range s.System.Database.Entities {
			e := &s.System.Database.Entities[i]
			kept := e.Relations[:0]
			for _, r := range e.Relations {
				if entities[r.Entity] {
					kept = append(kept, r)
				} else {
					a.warn("entity %s relation to unknown entity %s dropped", e.Name, r.Entity)
				}
			}
			e.Relations = kept
		}
	}
	frameworks := map[string]bool{}
	if ls := s.LanguageStack(); ls != nil {
		for _, f := range ls.Frameworks {
			frameworks[f.Name] = true
		}
	}
	for i := range s.System.Interfaces {
		iface := &s.System.Interfaces[i]
		if iface.Framework != "" && !frameworks[iface.Framework] {
			a.warn("interface %s names framework %q not in the stack; cleared", iface.Name, iface.Framework)
			iface.Framework = ""
		}
		if iface.Role == "provider" {
			iface.Role = ""
		}
		for j := range iface.Surfaces {
			sf := &iface.Surfaces[j]
			if sf.Entity != "" && !entities[sf.Entity] {
				a.warn("surface %s.%s names unknown entity %q; cleared", iface.Name, sf.Name, sf.Entity)
				sf.Entity = ""
			}
			if iface.Kind == "http" && sf.Method == "" {
				sf.Method = "GET"
			}
		}
	}
	for i := range s.Modules {
		m := &s.Modules[i]
		ids := map[string]bool{}
		for j := range m.Scenarios {
			sc := &m.Scenarios[j]
			if sc.ID == "" || ids[sc.ID] {
				sc.ID = fmt.Sprintf("S%d", j+1)
			}
			ids[sc.ID] = true
			if sc.When == "" {
				sc.When = "(unspecified)"
			}
			if sc.Then == "" {
				sc.Then = "(unspecified)"
			}
		}
		for j := range m.Interface {
			if m.Interface[j].Signature == "" {
				m.Interface[j].Signature = m.Interface[j].Name + "()"
			}
		}
		m.Goals = dedupe(m.Goals)
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
