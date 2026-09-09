// Command aspect drives the Aspect pipeline: validate a specification, show
// the build plan, or run the agents and produce code, tests and a report.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

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

Run flags:
  -out DIR          output directory (default ./out)
                    Go specs produce a Go module; Swift specs a SwiftPM package
  -model ID         model id (default $ASPECT_MODEL or claude-opus-5)
  -effort LEVEL     low|medium|high|xhigh|max (default high)
  -max-repairs N    coder repair rounds per module (default 3)
  -fallbacks=false  disable server-side refusal fallbacks

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
	fmt.Printf("ok: %s (%s, %d modules, %d goals, %d warnings)\n", s.System.Name, s.System.Language, len(s.Modules), len(s.System.Goals), len(issues))
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
	fmt.Printf("system %s: %d modules in build order\n\n", p.System, len(p.Steps))
	for i, st := range p.Steps {
		fmt.Printf("%d. %s\n", i+1, st.Module)
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
		fmt.Printf("   tester    -> %d scenario(s), %d invariant(s)\n", st.Scenarios, st.Invariants)
		fmt.Printf("   validator -> verdict on goals %v\n", st.Goals)
	}
	return nil
}

func runRun(path string, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	out := fs.String("out", "out", "output directory")
	model := fs.String("model", envOr("ASPECT_MODEL", llm.DefaultModel), "model id")
	effort := fs.String("effort", "high", "effort level")
	maxRepairs := fs.Int("max-repairs", 3, "coder repair rounds per module")
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
		OutDir: *out, MaxRepairs: *maxRepairs, Log: os.Stderr, Model: *model,
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
