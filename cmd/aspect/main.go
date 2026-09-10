// Command aspect drives the Aspect pipeline: validate a specification, show
// the build plan, or run the agents and produce code, tests and a report.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/eideroliveira/aspect/internal/analyze"
	"github.com/eideroliveira/aspect/internal/importer"
	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/pipeline"
	"github.com/eideroliveira/aspect/internal/plan"
	"github.com/eideroliveira/aspect/internal/spec"
)

const usage = `aspect - build software from a formal specification with a team of agents

Usage:
  aspect validate <spec.yaml>          check the spec and report every issue
  aspect plan     <spec.yaml>          show the module build order and what each step needs
  aspect run      <spec.yaml> [flags]  generate code and tests, run them, validate goals
  aspect inventory <dir> [flags]       deterministic inventory of an existing Go codebase (no model calls)
  aspect import   <dir> [flags]        recover a spec from an existing Go codebase

Run flags:
  -out DIR          output directory (default ./out)
                    Go specs produce a Go module; Swift specs a SwiftPM package
  -model ID         model id (default $ASPECT_MODEL or claude-opus-5)
  -effort LEVEL     low|medium|high|xhigh|max (default high)
  -max-repairs N    coder repair rounds per module (default 3)
  -parallel N       independent modules generated at once (default 1)
  -fallbacks=false  disable server-side refusal fallbacks

Inventory and import flags:
  -include a,b      only these package directories (prefix match)
  -exclude a,b      skip these package directories (external, vendor, testdata are always skipped)

Import flags:
  -o FILE           where to write the spec (default aspect.yaml)
  -language LANG    language of the new system, or of its frontend tier (default: source)
  -topology T       monolith | api_backend | cloud_service
                    monolith:      one tier connecting to its own database (default when
                                   the language does not change)
                    api_backend:   a backend tier serving an API (mirrors the source when
                                   it keeps the source language) plus a frontend tier in
                                   -language consuming it (default when the language changes)
                    cloud_service: one app tier in -language over hosted services; the
                                   database and server interfaces are external
  -backend-language L  language of the api_backend backend tier (default: source)
  -hint TEXT        guidance for the target ("an iOS app for members; admin stays on the web")
  -name NAME        system name (default: last element of the module path)
  -module-path P    target module path
  -cache DIR        fragment cache so re-runs skip described packages (default .aspect-cache/<name>)
  -concurrency N    packages described in parallel (default 4)
  -model, -effort, -fallbacks as for run

Credentials come from ANTHROPIC_API_KEY or an "ant auth login" profile.
`

func main() {
	if len(os.Args) < 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, path := os.Args[1], os.Args[2]
	var err error
	switch cmd {
	case "validate":
		err = runValidate(path)
	case "plan":
		err = runPlan(path)
	case "run":
		err = runRun(path, os.Args[3:])
	case "inventory":
		err = runInventory(path, os.Args[3:])
	case "import":
		err = runImport(path, os.Args[3:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "aspect:", err)
		os.Exit(1)
	}
}

func loadValidated(path string) (*spec.Spec, spec.Issues, error) {
	s, err := spec.Load(path)
	if err != nil {
		return nil, nil, err
	}
	issues := spec.Validate(s)
	for _, i := range issues {
		fmt.Fprintln(os.Stderr, i)
	}
	if issues.HasErrors() {
		return s, issues, fmt.Errorf("spec has errors")
	}
	return s, issues, nil
}

func runValidate(path string) error {
	s, issues, err := loadValidated(path)
	if err != nil {
		return err
	}
	var langs []string
	for _, t := range s.EffectiveTiers() {
		langs = append(langs, t.Language)
	}
	fmt.Printf("ok: %s (%s, %s, %d modules, %d goals, %d warnings)\n", s.System.Name, s.EffectiveTopology(), strings.Join(langs, "+"), len(s.AllModules()), len(s.System.Goals), len(issues))
	return nil
}

func runPlan(path string) error {
	s, _, err := loadValidated(path)
	if err != nil {
		return err
	}
	p, err := plan.Build(s)
	if err != nil {
		return err
	}
	fmt.Printf("system %s (%s): %d modules in build order\n", p.System, p.Topology, len(p.Steps()))
	i := 0
	for _, tp := range p.Tiers {
		if tp.Name != "" {
			fmt.Printf("\n## tier %s (%s) -> %s\n", tp.Name, tp.Language, pipeline.TierDir("out", s.System.Name, tp.Name))
		}
		fmt.Println()
		for _, st := range tp.Steps {
			i++
			fmt.Printf("%d. %s\n", i, st.Module)
			if len(st.DependsOn) > 0 {
				fmt.Printf("   depends on: %v\n", st.DependsOn)
			}
			fmt.Printf("   coder     -> implement %d operation(s)\n", len(s.Module(st.Module).Interface))
			if len(st.Entities) > 0 {
				fmt.Printf("   owns entities: %v\n", st.Entities)
			}
			if len(st.Surfaces) > 0 {
				fmt.Printf("   implements: %v\n", st.Surfaces)
			}
			if len(st.Consumes) > 0 {
				fmt.Printf("   consumes: %v\n", st.Consumes)
			}
			fmt.Printf("   tester    -> %d scenario(s), %d invariant(s)\n", st.Scenarios, st.Invariants)
			fmt.Printf("   validator -> verdict on goals %v\n", st.Goals)
		}
	}
	return nil
}

func runRun(path string, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	out := fs.String("out", "out", "output directory")
	model := fs.String("model", envOr("ASPECT_MODEL", llm.DefaultModel), "model id")
	effort := fs.String("effort", "high", "effort level")
	maxRepairs := fs.Int("max-repairs", 3, "coder repair rounds per module")
	parallel := fs.Int("parallel", 1, "independent modules generated at once")
	fallbacks := fs.Bool("fallbacks", true, "server-side refusal fallbacks")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, _, err := loadValidated(path)
	if err != nil {
		return err
	}
	p, err := plan.Build(s)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := llm.NewAnthropic(llm.WithModel(*model), llm.WithEffort(*effort), llm.WithFallbacks(*fallbacks))
	runner := pipeline.New(client, pipeline.Options{
		OutDir: *out, MaxRepairs: *maxRepairs, Parallel: *parallel, Log: os.Stderr, Model: *model,
	})
	rep, runErr := runner.Run(ctx, s, p)
	if rep != nil {
		if err := rep.Write(*out); err != nil {
			return err
		}
		fmt.Printf("\nreport: %s\n", filepath.Join(*out, s.System.Name, "REPORT.md"))
		for _, g := range rep.Goals {
			fmt.Printf("  %-4s %-13s %s\n", g.ID, g.Status, g.Statement)
		}
	}
	return runErr
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func runInventory(dir string, args []string) error {
	fs := flag.NewFlagSet("inventory", flag.ContinueOnError)
	include := fs.String("include", "", "package directories to include")
	exclude := fs.String("exclude", "", "package directories to exclude")
	asJSON := fs.Bool("json", false, "print the inventory as JSON")
	pkg := fs.String("package", "", "render one package as the Describer would see it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inv, err := analyze.Go(dir, analyze.Options{Include: splitList(*include), Exclude: splitList(*exclude)})
	if err != nil {
		return err
	}
	switch {
	case *pkg != "":
		p := inv.Package(*pkg)
		if p == nil {
			return fmt.Errorf("no package at %q", *pkg)
		}
		fmt.Print(p.Render(0))
	case *asJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(inv)
	default:
		inv.Summary(os.Stdout)
	}
	return nil
}

func runImport(dir string, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	out := fs.String("o", "aspect.yaml", "output spec file")
	include := fs.String("include", "", "package directories to include")
	exclude := fs.String("exclude", "", "package directories to exclude")
	language := fs.String("language", "", "target language (default: the source language)")
	topology := fs.String("topology", "", "monolith | api_backend | cloud_service")
	backendLanguage := fs.String("backend-language", "", "backend tier language for api_backend")
	hint := fs.String("hint", "", "guidance for the target platform")
	name := fs.String("name", "", "system name")
	modulePath := fs.String("module-path", "", "target module path")
	cache := fs.String("cache", "", "fragment cache directory")
	concurrency := fs.Int("concurrency", 4, "packages described in parallel")
	model := fs.String("model", envOr("ASPECT_MODEL", llm.DefaultModel), "model id")
	effort := fs.String("effort", "high", "effort level")
	fallbacks := fs.Bool("fallbacks", true, "server-side refusal fallbacks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cache == "" {
		base := *name
		if base == "" {
			base = filepath.Base(mustAbs(dir))
		}
		*cache = filepath.Join(".aspect-cache", base)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := llm.NewAnthropic(llm.WithModel(*model), llm.WithEffort(*effort), llm.WithFallbacks(*fallbacks))
	res, err := importer.Run(ctx, client, importer.Options{
		Root: dir, Include: splitList(*include), Exclude: splitList(*exclude),
		Target: *language, TargetHint: *hint, Name: *name, ModulePath: *modulePath,
		Topology: spec.Topology(*topology), BackendLanguage: *backendLanguage,
		Repository: gitRemote(dir), Commit: gitHead(dir),
		CacheDir: *cache, Concurrency: *concurrency, Log: os.Stderr,
	})
	if res != nil {
		fmt.Fprintf(os.Stderr, "LLM calls: %d (in %d / out %d / cached %d tokens)\n", res.Usage.Calls, res.Usage.InputTokens, res.Usage.OutputTokens, res.Usage.CacheReadTokens)
	}
	if err != nil {
		return err
	}
	if err := importer.Write(*out, res.Spec, res.Warnings); err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	for _, i := range res.Issues {
		fmt.Fprintln(os.Stderr, i)
	}
	fmt.Printf("wrote %s: %s (%s, %d tier(s), %d modules, %d goals, %d entities, %d interfaces)\n", *out, res.Spec.System.Name, res.Spec.EffectiveTopology(),
		len(res.Spec.EffectiveTiers()), len(res.Spec.AllModules()), len(res.Spec.System.Goals), entityCount(res.Spec), len(res.Spec.System.Interfaces))
	if res.Issues.HasErrors() {
		return fmt.Errorf("the recovered spec has validation errors; fix them in %s and run `aspect validate`", *out)
	}
	return nil
}

func entityCount(s *spec.Spec) int {
	n := 0
	for _, db := range s.Databases() {
		n += len(db.Entities)
	}
	return n
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func gitRemote(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitHead(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
